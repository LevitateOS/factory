package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanThreadQuery       = errors.New("invalid human thread request")
	ErrHumanThreadAdmission   = errors.New("current human thread admission required")
	ErrHumanThreadConflict    = errors.New("human thread intent conflicts")
	ErrHumanThreadUnavailable = errors.New("human thread provider unavailable")
	ErrHumanThreadTooLarge    = errors.New("complete human thread exceeds provider limit")
	ErrHumanThreadNamespace   = errors.New("captured thread installation differs from current provider")
)

// HumanThreadWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanThreadWriter interface {
	PrepareHumanThread(context.Context, factory.HumanThreadCommand) (factory.HumanThreadCapture, error)
	SubmitHumanThread(context.Context, factory.HumanThreadCapture) (collaboration.OperationRecord, error)
	LookupHumanThread(context.Context, factory.HumanThreadCapture) (collaboration.OperationLookup, error)
	CancelHumanThread(context.Context, factory.HumanThreadCapture) (collaboration.OperationRecord, error)
}

// HumanThreadReader returns safe selection facts without preparing an operation
// or allocating a command. Write admission always observes the provider again.
type HumanThreadReader interface {
	ReadHumanThreadSelection(context.Context, factory.HumanThreadSelectionQuery) (factory.HumanThreadSelection, error)
}

// HumanThreads runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanThreads struct {
	Store  *store.Store
	Writer HumanThreadWriter
	Reader HumanThreadReader
}

func (a Application) HumanThreads(writer HumanThreadWriter) HumanThreads {
	return HumanThreads{Store: a.Store, Writer: writer}
}

func (s HumanThreads) Selection(ctx context.Context, q factory.HumanThreadSelectionQuery) (factory.HumanThreadSelection, error) {
	if q.Validate() != nil {
		return factory.HumanThreadSelection{}, ErrHumanThreadQuery
	}
	if s.Reader == nil {
		return factory.HumanThreadSelection{}, ErrHumanThreadUnavailable
	}
	selection, err := s.Reader.ReadHumanThreadSelection(ctx, q)
	if err != nil {
		return factory.HumanThreadSelection{}, err
	}
	if selection.Validate(q) != nil {
		return factory.HumanThreadSelection{}, ErrHumanThreadUnavailable
	}
	return selection, nil
}

// command resolves original server-retained thread/state/context only within its
// admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanThreads) command(ctx context.Context, scope factory.HumanThreadCommand) (factory.HumanThreadCommand, error) {
	if scope.ValidateScope() != nil || scope.Resolved || scope.Anchor != nil || scope.ThreadID != "" {
		return factory.HumanThreadCommand{}, ErrHumanThreadQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanThreadCommand{}, ErrHumanThreadUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanThreadCommand{}, err
	}
	c, err := factory.DecodeHumanThreadCapture(cmd)
	if err != nil {
		return factory.HumanThreadCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Resolved, actual.Anchor, actual.ThreadID = false, nil, ""
	if !actual.SameRequest(scope) {
		return factory.HumanThreadCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanThreads) LookupCommand(ctx context.Context, scope factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanThreads) CancelCommand(ctx context.Context, scope factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanThreads) capture(ctx context.Context, q factory.HumanThreadCommand, prepare bool) (factory.HumanThreadCapture, error) {
	var empty factory.HumanThreadCapture
	if q.Validate() != nil {
		return empty, ErrHumanThreadQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanThreadUnavailable
	}
	c, err := s.Store.HumanThreadCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanThread(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanThreadCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanThreadConflict
	}
	c, _, err = s.Store.CaptureHumanThread(ctx, c)
	return c, err
}

func (s HumanThreads) observe(ctx context.Context, c factory.HumanThreadCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanThread(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanThread(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanThreads) unknown(ctx context.Context, c factory.HumanThreadCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanThread(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanThreads) save(ctx context.Context, c factory.HumanThreadCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanThread(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanThreads) Submit(ctx context.Context, q factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanThread(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanThreads) Lookup(ctx context.Context, q factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanThreads) Cancel(ctx context.Context, q factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanThread(ctx, c)
	return s.save(ctx, c, r, err)
}
