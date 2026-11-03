package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanCommentQuery       = errors.New("invalid human comment request")
	ErrHumanCommentAdmission   = errors.New("current human comment admission required")
	ErrHumanCommentConflict    = errors.New("human comment intent conflicts")
	ErrHumanCommentUnavailable = errors.New("human comment provider unavailable")
	ErrHumanCommentNamespace   = errors.New("captured comment installation differs from current provider")
)

// HumanCommentWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanCommentWriter interface {
	PrepareHumanComment(context.Context, factory.HumanCommentCommand) (factory.HumanCommentCapture, error)
	SubmitHumanComment(context.Context, factory.HumanCommentCapture) (collaboration.OperationRecord, error)
	LookupHumanComment(context.Context, factory.HumanCommentCapture) (collaboration.OperationLookup, error)
	CancelHumanComment(context.Context, factory.HumanCommentCapture) (collaboration.OperationRecord, error)
}

// HumanComments runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanComments struct {
	Store  *store.Store
	Writer HumanCommentWriter
}

func (a Application) HumanComments(writer HumanCommentWriter) HumanComments {
	return HumanComments{Store: a.Store, Writer: writer}
}

// command resolves original server-retained writing only within the admitted
// host/actor/repository/parent. Public lookup never accepts provider preparation.
func (s HumanComments) command(ctx context.Context, scope factory.HumanCommentCommand) (factory.HumanCommentCommand, error) {
	if scope.ValidateScope() != nil || scope.Body != "" {
		return factory.HumanCommentCommand{}, ErrHumanCommentQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanCommentCommand{}, ErrHumanCommentUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanCommentCommand{}, err
	}
	c, err := factory.DecodeHumanCommentCapture(cmd)
	if err != nil {
		return factory.HumanCommentCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Body = ""
	if actual != scope {
		return factory.HumanCommentCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanComments) LookupCommand(ctx context.Context, scope factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanComments) CancelCommand(ctx context.Context, scope factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanComments) capture(ctx context.Context, q factory.HumanCommentCommand, prepare bool) (factory.HumanCommentCapture, error) {
	var empty factory.HumanCommentCapture
	if q.Validate() != nil {
		return empty, ErrHumanCommentQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanCommentUnavailable
	}
	c, err := s.Store.HumanCommentCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanComment(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanCommentCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if c.Request != q {
		return empty, ErrHumanCommentConflict
	}
	c, _, err = s.Store.CaptureHumanComment(ctx, c)
	return c, err
}

func (s HumanComments) observe(ctx context.Context, c factory.HumanCommentCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanComment(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanComment(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanComments) unknown(ctx context.Context, c factory.HumanCommentCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanComment(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanComments) save(ctx context.Context, c factory.HumanCommentCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanComment(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanComments) Submit(ctx context.Context, q factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanComment(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanComments) Lookup(ctx context.Context, q factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanComments) Cancel(ctx context.Context, q factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanComment(ctx, c)
	return s.save(ctx, c, r, err)
}
