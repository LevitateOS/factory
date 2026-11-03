package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanReviewQuery       = errors.New("invalid human review request")
	ErrHumanReviewAdmission   = errors.New("current human review admission required")
	ErrHumanReviewConflict    = errors.New("human review intent conflicts")
	ErrHumanReviewUnavailable = errors.New("human review provider unavailable")
	ErrHumanReviewNamespace   = errors.New("captured review installation differs from current provider")
)

// HumanReviewWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanReviewWriter interface {
	PrepareHumanReview(context.Context, factory.HumanReviewCommand) (factory.HumanReviewCapture, error)
	SubmitHumanReview(context.Context, factory.HumanReviewCapture) (collaboration.OperationRecord, error)
	LookupHumanReview(context.Context, factory.HumanReviewCapture) (collaboration.OperationLookup, error)
	CancelHumanReview(context.Context, factory.HumanReviewCapture) (collaboration.OperationRecord, error)
}

// HumanReviews runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanReviews struct {
	Store  *store.Store
	Writer HumanReviewWriter
}

func (a Application) HumanReviews(writer HumanReviewWriter) HumanReviews {
	return HumanReviews{Store: a.Store, Writer: writer}
}

// command resolves original server-retained review event/prose only within its
// admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanReviews) command(ctx context.Context, scope factory.HumanReviewCommand) (factory.HumanReviewCommand, error) {
	if scope.ValidateScope() != nil || scope.Event != "" || scope.Body != "" {
		return factory.HumanReviewCommand{}, ErrHumanReviewQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanReviewCommand{}, ErrHumanReviewUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanReviewCommand{}, err
	}
	c, err := factory.DecodeHumanReviewCapture(cmd)
	if err != nil {
		return factory.HumanReviewCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Event, actual.Body = "", ""
	if !actual.SameRequest(scope) {
		return factory.HumanReviewCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanReviews) LookupCommand(ctx context.Context, scope factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanReviews) CancelCommand(ctx context.Context, scope factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanReviews) capture(ctx context.Context, q factory.HumanReviewCommand, prepare bool) (factory.HumanReviewCapture, error) {
	var empty factory.HumanReviewCapture
	if q.Validate() != nil {
		return empty, ErrHumanReviewQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanReviewUnavailable
	}
	c, err := s.Store.HumanReviewCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanReview(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanReviewCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanReviewConflict
	}
	c, _, err = s.Store.CaptureHumanReview(ctx, c)
	return c, err
}

func (s HumanReviews) observe(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanReview(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanReview(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanReviews) unknown(ctx context.Context, c factory.HumanReviewCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanReview(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanReviews) save(ctx context.Context, c factory.HumanReviewCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanReview(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanReviews) Submit(ctx context.Context, q factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	o, err := s.observe(ctx, c)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if o.Record != nil || o.State != collaboration.LookupNotObserved {
		return o, nil
	}
	r, err := s.Writer.SubmitHumanReview(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanReviews) Lookup(ctx context.Context, q factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanReviews) Cancel(ctx context.Context, q factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	o, err := s.observe(ctx, c)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	if o.Record != nil && (o.Record.Effect == collaboration.EffectCommitted || o.Record.Effect == collaboration.EffectNotCommitted) {
		return o, nil
	}
	if o.Record == nil && o.State != collaboration.LookupNotObserved {
		return o, nil
	}
	r, err := s.Writer.CancelHumanReview(ctx, c)
	return s.save(ctx, c, r, err)
}
