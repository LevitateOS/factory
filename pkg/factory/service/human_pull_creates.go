package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanPullCreateQuery       = errors.New("invalid human pull creation request")
	ErrHumanPullCreateAdmission   = errors.New("current human pull creation admission required")
	ErrHumanPullCreateConflict    = errors.New("human pull creation intent conflicts")
	ErrHumanPullCreateUnavailable = errors.New("human pull creation provider unavailable")
	ErrHumanPullCreateNamespace   = errors.New("captured pull creation installation differs from current provider")
)

// HumanPullCreateWriter operates on the exact saved capture. Preparation is a
// read, never authority: the application must commit it before Submit. Providers
// reconcile/replay that identity without duplicate creation; they cannot reprepare
// or project actionable absence for a possibly submitted command without proof.
type HumanPullCreateWriter interface {
	PrepareHumanPullCreate(context.Context, factory.HumanPullCreateCommand) (factory.HumanPullCreateCapture, error)
	SubmitHumanPullCreate(context.Context, factory.HumanPullCreateCapture) (collaboration.OperationRecord, error)
	LookupHumanPullCreate(context.Context, factory.HumanPullCreateCapture) (collaboration.OperationLookup, error)
	CancelHumanPullCreate(context.Context, factory.HumanPullCreateCapture) (collaboration.OperationRecord, error)
}

// HumanPullCreates runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanPullCreates struct {
	Store  *store.Store
	Writer HumanPullCreateWriter
}

func (a Application) HumanPullCreates(writer HumanPullCreateWriter) HumanPullCreates {
	return HumanPullCreates{Store: a.Store, Writer: writer}
}

// command recovers the original source, branches, writing and delegation only
// within the admitted host/base repository/actor. Cleanup accepts no replacement
// creation fields or provider preparation. The source may no longer be readable.
func (s HumanPullCreates) command(ctx context.Context, scope factory.HumanPullCreateCommand) (factory.HumanPullCreateCommand, error) {
	if !factory.ValidID(scope.ID) || !collaboration.ValidID(string(scope.Host)) || scope.Repository <= 0 || scope.Actor <= 0 ||
		scope.HeadRepository != 0 || scope.BaseBranch != "" || scope.HeadBranch != "" || scope.Title != "" || scope.Body != "" || scope.AllowMaintainerEdit {
		return factory.HumanPullCreateCommand{}, ErrHumanPullCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanPullCreateCommand{}, ErrHumanPullCreateUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanPullCreateCommand{}, err
	}
	c, err := factory.DecodeHumanPullCreateCapture(cmd)
	if err != nil {
		return factory.HumanPullCreateCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.HeadRepository, actual.BaseBranch, actual.HeadBranch = 0, "", ""
	actual.Title, actual.Body, actual.AllowMaintainerEdit = "", "", false
	if !actual.SameRequest(scope) {
		return factory.HumanPullCreateCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanPullCreates) LookupCommand(ctx context.Context, scope factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanPullCreates) CancelCommand(ctx context.Context, scope factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanPullCreates) capture(ctx context.Context, q factory.HumanPullCreateCommand, prepare bool) (factory.HumanPullCreateCapture, error) {
	var empty factory.HumanPullCreateCapture
	if q.Validate() != nil {
		return empty, ErrHumanPullCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanPullCreateUnavailable
	}
	c, err := s.Store.HumanPullCreateCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanPullCreate(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanPullCreateCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanPullCreateConflict
	}
	c, _, err = s.Store.CaptureHumanPullCreate(ctx, c)
	return c, err
}

func (s HumanPullCreates) observe(ctx context.Context, c factory.HumanPullCreateCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanPullCreate(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanPullCreate(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanPullCreates) unknown(ctx context.Context, c factory.HumanPullCreateCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanPullCreate(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanPullCreates) save(ctx context.Context, c factory.HumanPullCreateCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanPullCreate(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanPullCreates) Submit(ctx context.Context, q factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanPullCreate(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanPullCreates) Lookup(ctx context.Context, q factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanPullCreates) Cancel(ctx context.Context, q factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanPullCreate(ctx, c)
	return s.save(ctx, c, r, err)
}
