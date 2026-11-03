package service

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

var (
	ErrHumanInlineQuery       = errors.New("invalid human inline request")
	ErrHumanInlineAdmission   = errors.New("current human inline admission required")
	ErrHumanInlineConflict    = errors.New("human inline intent conflicts")
	ErrHumanInlineUnavailable = errors.New("human inline provider unavailable")
	ErrHumanInlineNamespace   = errors.New("captured inline installation differs from current provider")
)

// HumanInlineWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanInlineWriter interface {
	PrepareHumanInline(context.Context, factory.HumanInlineCommand) (factory.HumanInlineCapture, error)
	SubmitHumanInline(context.Context, factory.HumanInlineCapture) (collaboration.OperationRecord, error)
	LookupHumanInline(context.Context, factory.HumanInlineCapture) (collaboration.OperationLookup, error)
	CancelHumanInline(context.Context, factory.HumanInlineCapture) (collaboration.OperationRecord, error)
}

// HumanInlines runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanInlines struct {
	Store  *store.Store
	Writer HumanInlineWriter
}

func (a Application) HumanInlines(writer HumanInlineWriter) HumanInlines {
	return HumanInlines{Store: a.Store, Writer: writer}
}

// command resolves original server-retained inline mode/anchor/prose only within its
// admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanInlines) command(ctx context.Context, scope factory.HumanInlineCommand) (factory.HumanInlineCommand, error) {
	if scope.ValidateScope() != nil || scope.Draft || scope.Anchor != (factory.ReviewThreadAnchor{}) || scope.Body != "" {
		return factory.HumanInlineCommand{}, ErrHumanInlineQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanInlineCommand{}, ErrHumanInlineUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanInlineCommand{}, err
	}
	c, err := factory.DecodeHumanInlineCapture(cmd)
	if err != nil {
		return factory.HumanInlineCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Draft, actual.Anchor, actual.Body = false, factory.ReviewThreadAnchor{}, ""
	if !actual.SameRequest(scope) {
		return factory.HumanInlineCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanInlines) LookupCommand(ctx context.Context, scope factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanInlines) CancelCommand(ctx context.Context, scope factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanInlines) capture(ctx context.Context, q factory.HumanInlineCommand, prepare bool) (factory.HumanInlineCapture, error) {
	var empty factory.HumanInlineCapture
	if q.Validate() != nil {
		return empty, ErrHumanInlineQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanInlineUnavailable
	}
	c, err := s.Store.HumanInlineCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanInline(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanInlineCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanInlineConflict
	}
	c, _, err = s.Store.CaptureHumanInline(ctx, c)
	return c, err
}

func (s HumanInlines) observe(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanInline(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanInline(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanInlines) unknown(ctx context.Context, c factory.HumanInlineCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanInline(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanInlines) save(ctx context.Context, c factory.HumanInlineCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanInline(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanInlines) Submit(ctx context.Context, q factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanInline(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanInlines) Lookup(ctx context.Context, q factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanInlines) Cancel(ctx context.Context, q factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanInline(ctx, c)
	return s.save(ctx, c, r, err)
}
