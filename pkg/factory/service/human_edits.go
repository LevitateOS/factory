package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanEditQuery       = errors.New("invalid human edit request")
	ErrHumanEditAdmission   = errors.New("current human edit admission required")
	ErrHumanEditConflict    = errors.New("human edit intent conflicts")
	ErrHumanEditUnavailable = errors.New("human edit provider unavailable")
	ErrHumanEditNamespace   = errors.New("captured edit installation differs from current provider")
	ErrHumanEditBaseChanged = errors.New("original human edit content differs from captured subject")
)

// HumanEditWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanEditWriter interface {
	PrepareHumanEdit(context.Context, factory.HumanEditCommand) (factory.HumanEditCapture, error)
	SubmitHumanEdit(context.Context, factory.HumanEditCapture) (collaboration.OperationRecord, error)
	LookupHumanEdit(context.Context, factory.HumanEditCapture) (collaboration.OperationLookup, error)
	CancelHumanEdit(context.Context, factory.HumanEditCapture) (collaboration.OperationRecord, error)
}

// HumanEdits runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanEdits struct {
	Store  *store.Store
	Writer HumanEditWriter
}

func (a Application) HumanEdits(writer HumanEditWriter) HumanEdits {
	return HumanEdits{Store: a.Store, Writer: writer}
}

// command resolves original server-retained writing only within the admitted
// host/actor/repository/parent. Public lookup never accepts provider preparation.
func (s HumanEdits) command(ctx context.Context, scope factory.HumanEditCommand) (factory.HumanEditCommand, error) {
	if scope.ValidateScope() != nil || scope.Title != nil || scope.Body != nil || scope.Expected != nil {
		return factory.HumanEditCommand{}, ErrHumanEditQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanEditCommand{}, ErrHumanEditUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanEditCommand{}, err
	}
	c, err := factory.DecodeHumanEditCapture(cmd)
	if err != nil {
		return factory.HumanEditCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Title, actual.Body = nil, nil
	actual.Expected = nil
	if !actual.SameRequest(scope) {
		return factory.HumanEditCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanEdits) LookupCommand(ctx context.Context, scope factory.HumanEditCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanEdits) CancelCommand(ctx context.Context, scope factory.HumanEditCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanEdits) capture(ctx context.Context, q factory.HumanEditCommand, prepare bool) (factory.HumanEditCapture, error) {
	var empty factory.HumanEditCapture
	if q.Validate() != nil {
		return empty, ErrHumanEditQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanEditUnavailable
	}
	c, err := s.Store.HumanEditCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanEdit(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanEditCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanEditConflict
	}
	c, _, err = s.Store.CaptureHumanEdit(ctx, c)
	return c, err
}

func (s HumanEdits) observe(ctx context.Context, c factory.HumanEditCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanEdit(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanEdit(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanEdits) unknown(ctx context.Context, c factory.HumanEditCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanEdit(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanEdits) save(ctx context.Context, c factory.HumanEditCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanEdit(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanEdits) Submit(ctx context.Context, q factory.HumanEditCommand) (collaboration.OperationLookup, error) {
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
	if !c.ExpectedCurrent() {
		// Persist first, then obtain a real provider cancellation tombstone. A
		// stale save must remain reconcilable and fence delayed original submits;
		// neither HTTP refusal nor a missing local command is no-effect proof.
		r, err := s.Writer.CancelHumanEdit(ctx, c)
		return s.save(ctx, c, r, err)
	}
	r, err := s.Writer.SubmitHumanEdit(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanEdits) Lookup(ctx context.Context, q factory.HumanEditCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanEdits) Cancel(ctx context.Context, q factory.HumanEditCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanEdit(ctx, c)
	return s.save(ctx, c, r, err)
}
