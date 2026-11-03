package service

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

var (
	ErrHumanStateQuery       = errors.New("invalid human state request")
	ErrHumanStateAdmission   = errors.New("current human state admission required")
	ErrHumanStateConflict    = errors.New("human state intent conflicts")
	ErrHumanStateUnavailable = errors.New("human state provider unavailable")
	ErrHumanStateNamespace   = errors.New("captured state installation differs from current provider")
)

// HumanStateWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanStateWriter interface {
	PrepareHumanState(context.Context, factory.HumanStateCommand) (factory.HumanStateCapture, error)
	SubmitHumanState(context.Context, factory.HumanStateCapture) (collaboration.OperationRecord, error)
	LookupHumanState(context.Context, factory.HumanStateCapture) (collaboration.OperationLookup, error)
	CancelHumanState(context.Context, factory.HumanStateCapture) (collaboration.OperationRecord, error)
}

// HumanStates runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanStates struct {
	Store  *store.Store
	Writer HumanStateWriter
}

func (a Application) HumanStates(writer HumanStateWriter) HumanStates {
	return HumanStates{Store: a.Store, Writer: writer}
}

// command resolves original server-retained desired closed state only within its
// admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanStates) command(ctx context.Context, scope factory.HumanStateCommand) (factory.HumanStateCommand, error) {
	if scope.ValidateScope() != nil || scope.Close {
		return factory.HumanStateCommand{}, ErrHumanStateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanStateCommand{}, ErrHumanStateUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanStateCommand{}, err
	}
	c, err := factory.DecodeHumanStateCapture(cmd)
	if err != nil {
		return factory.HumanStateCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Close = false
	if !actual.SameRequest(scope) {
		return factory.HumanStateCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanStates) LookupCommand(ctx context.Context, scope factory.HumanStateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanStates) CancelCommand(ctx context.Context, scope factory.HumanStateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanStates) capture(ctx context.Context, q factory.HumanStateCommand, prepare bool) (factory.HumanStateCapture, error) {
	var empty factory.HumanStateCapture
	if q.Validate() != nil {
		return empty, ErrHumanStateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanStateUnavailable
	}
	c, err := s.Store.HumanStateCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanState(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanStateCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanStateConflict
	}
	c, _, err = s.Store.CaptureHumanState(ctx, c)
	return c, err
}

func (s HumanStates) observe(ctx context.Context, c factory.HumanStateCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanState(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanState(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanStates) unknown(ctx context.Context, c factory.HumanStateCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanState(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanStates) save(ctx context.Context, c factory.HumanStateCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanState(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanStates) Submit(ctx context.Context, q factory.HumanStateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanState(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanStates) Lookup(ctx context.Context, q factory.HumanStateCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanStates) Cancel(ctx context.Context, q factory.HumanStateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanState(ctx, c)
	return s.save(ctx, c, r, err)
}
