package service

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

var (
	ErrHumanReviewRequestQuery       = errors.New("invalid human reviewer request")
	ErrHumanReviewRequestAdmission   = errors.New("current human reviewer admission required")
	ErrHumanReviewRequestConflict    = errors.New("human reviewer intent conflicts")
	ErrHumanReviewRequestUnavailable = errors.New("human reviewer provider unavailable")
	ErrHumanReviewRequestTooLarge    = errors.New("complete reviewer preparation exceeds provider limit")
	ErrHumanReviewRequestNamespace   = errors.New("captured reviewer installation differs from current provider")
)

// HumanReviewRequestWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanReviewRequestWriter interface {
	PrepareHumanReviewRequest(context.Context, factory.HumanReviewRequestCommand) (factory.HumanReviewRequestCapture, error)
	SubmitHumanReviewRequest(context.Context, factory.HumanReviewRequestCapture) (collaboration.OperationRecord, error)
	LookupHumanReviewRequest(context.Context, factory.HumanReviewRequestCapture) (collaboration.OperationLookup, error)
	CancelHumanReviewRequest(context.Context, factory.HumanReviewRequestCapture) (collaboration.OperationRecord, error)
}

// HumanReviewRequests runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanReviewRequests struct {
	Store  *store.Store
	Writer HumanReviewRequestWriter
}

func (a Application) HumanReviewRequests(writer HumanReviewRequestWriter) HumanReviewRequests {
	return HumanReviewRequests{Store: a.Store, Writer: writer}
}

// command resolves the original reviewer selection and desired state only within
// its admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanReviewRequests) command(ctx context.Context, scope factory.HumanReviewRequestCommand) (factory.HumanReviewRequestCommand, error) {
	if scope.ValidateScope() != nil || scope.Requested || scope.Reviewer != 0 || scope.Team != 0 {
		return factory.HumanReviewRequestCommand{}, ErrHumanReviewRequestQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanReviewRequestCommand{}, ErrHumanReviewRequestUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanReviewRequestCommand{}, err
	}
	c, err := factory.DecodeHumanReviewRequestCapture(cmd)
	if err != nil {
		return factory.HumanReviewRequestCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Requested, actual.Reviewer, actual.Team = false, 0, 0
	if !actual.SameRequest(scope) {
		return factory.HumanReviewRequestCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanReviewRequests) LookupCommand(ctx context.Context, scope factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanReviewRequests) CancelCommand(ctx context.Context, scope factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanReviewRequests) capture(ctx context.Context, q factory.HumanReviewRequestCommand, prepare bool) (factory.HumanReviewRequestCapture, error) {
	var empty factory.HumanReviewRequestCapture
	if q.Validate() != nil {
		return empty, ErrHumanReviewRequestQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanReviewRequestUnavailable
	}
	c, err := s.Store.HumanReviewRequestCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanReviewRequest(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanReviewRequestCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanReviewRequestConflict
	}
	c, _, err = s.Store.CaptureHumanReviewRequest(ctx, c)
	return c, err
}

func (s HumanReviewRequests) observe(ctx context.Context, c factory.HumanReviewRequestCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanReviewRequest(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanReviewRequest(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanReviewRequests) unknown(ctx context.Context, c factory.HumanReviewRequestCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanReviewRequest(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanReviewRequests) save(ctx context.Context, c factory.HumanReviewRequestCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanReviewRequest(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanReviewRequests) Submit(ctx context.Context, q factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanReviewRequest(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanReviewRequests) Lookup(ctx context.Context, q factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanReviewRequests) Cancel(ctx context.Context, q factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanReviewRequest(ctx, c)
	return s.save(ctx, c, r, err)
}
