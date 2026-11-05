package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanBranchCreateQuery       = errors.New("invalid human branch creation request")
	ErrHumanBranchCreateAdmission   = errors.New("current human branch creation admission required")
	ErrHumanBranchCreateConflict    = errors.New("human branch creation intent conflicts")
	ErrHumanBranchCreateUnavailable = errors.New("human branch creation provider unavailable")
	ErrHumanBranchCreateNamespace   = errors.New("captured branch creation installation differs from current provider")
)

// HumanBranchCreateWriter operates on the exact saved capture. Preparation is a
// read, never authority: the application must commit it before Submit. Providers
// reconcile/replay that identity without duplicate creation; they cannot reprepare
// or project actionable absence for a possibly submitted command without proof.
type HumanBranchCreateWriter interface {
	PrepareHumanBranchCreate(context.Context, factory.HumanBranchCreateCommand) (factory.HumanBranchCreateCapture, error)
	SubmitHumanBranchCreate(context.Context, factory.HumanBranchCreateCapture) (collaboration.OperationRecord, error)
	LookupHumanBranchCreate(context.Context, factory.HumanBranchCreateCapture) (collaboration.OperationLookup, error)
	CancelHumanBranchCreate(context.Context, factory.HumanBranchCreateCapture) (collaboration.OperationRecord, error)
	// ReadCreatedHumanBranch projects only an authenticated committed receipt and
	// rechecks current branch visibility. It never searches by original intent.
	ReadCreatedHumanBranch(context.Context, factory.HumanBranchCreateCapture, collaboration.OperationLookup) (collaboration.GitRef, error)
}

// HumanBranchCreates runs admitted human commands on the application's single store.
// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanBranchCreates struct {
	Store  *store.Store
	Writer HumanBranchCreateWriter
}

func (a Application) HumanBranchCreates(writer HumanBranchCreateWriter) HumanBranchCreates {
	return HumanBranchCreates{Store: a.Store, Writer: writer}
}

// command resolves original server-retained branch intent only within the admitted
// host/actor/repository. Public lookup never accepts provider preparation.
func (s HumanBranchCreates) command(ctx context.Context, scope factory.HumanBranchCreateCommand) (factory.HumanBranchCreateCommand, error) {
	if scope.ValidateScope() != nil || scope.Name != "" || scope.Commit != "" {
		return factory.HumanBranchCreateCommand{}, ErrHumanBranchCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanBranchCreateCommand{}, ErrHumanBranchCreateUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanBranchCreateCommand{}, err
	}
	c, err := factory.DecodeHumanBranchCreateCapture(cmd)
	if err != nil {
		return factory.HumanBranchCreateCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Name, actual.Commit = "", ""
	if !actual.SameRequest(scope) {
		return factory.HumanBranchCreateCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanBranchCreates) LookupCommand(ctx context.Context, scope factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanBranchCreates) CancelCommand(ctx context.Context, scope factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

// CreatedBranch resolves the retained capture in the caller's exact scope. A
// failed destination read cannot erase the separately recorded committed effect.
func (s HumanBranchCreates) CreatedBranch(ctx context.Context, scope factory.HumanBranchCreateCommand, outcome collaboration.OperationLookup) (collaboration.GitRef, error) {
	var empty collaboration.GitRef
	q, err := s.command(ctx, scope)
	if err != nil {
		return empty, err
	}
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanBranchCreateObservation(c, outcome) != nil || outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted {
		return empty, ErrHumanBranchCreateQuery
	}
	branch, err := s.Writer.ReadCreatedHumanBranch(ctx, c, outcome)
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil || branch.Repository == nil || branch.Repository.Host != q.Host || branch.Repository.ID != q.Repository || branch.Name != q.Name || branch.Commit == nil || !collaboration.ValidCommitID(*branch.Commit) {
		return empty, ErrHumanBranchCreateUnavailable
	}
	return branch, nil
}

func (s HumanBranchCreates) capture(ctx context.Context, q factory.HumanBranchCreateCommand, prepare bool) (factory.HumanBranchCreateCapture, error) {
	var empty factory.HumanBranchCreateCapture
	if q.Validate() != nil {
		return empty, ErrHumanBranchCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanBranchCreateUnavailable
	}
	c, err := s.Store.HumanBranchCreateCapture(ctx, q)
	if err == nil || !errors.Is(err, store.ErrNotFound) || !prepare {
		return c, err
	}
	c, err = s.Writer.PrepareHumanBranchCreate(ctx, q)
	if err != nil {
		// Another caller may have committed this capture while our initial
		// lookup/preparation was in flight. Recover its exact winner rather
		// than report a conflict for an otherwise identical concurrent retry.
		raced, lookupErr := s.Store.HumanBranchCreateCapture(ctx, q)
		if lookupErr == nil {
			return raced, nil
		}
		if !errors.Is(lookupErr, store.ErrNotFound) {
			return empty, lookupErr
		}
		return empty, err
	}
	if !c.Request.SameRequest(q) {
		return empty, ErrHumanBranchCreateConflict
	}
	c, _, err = s.Store.CaptureHumanBranchCreate(ctx, c)
	return c, err
}

func (s HumanBranchCreates) observe(ctx context.Context, c factory.HumanBranchCreateCapture) (collaboration.OperationLookup, error) {
	o, err := s.Writer.LookupHumanBranchCreate(ctx, c)
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	return s.Store.ObserveHumanBranchCreate(ctx, c, o)
}

// A failed/lost reply is not a submitted record or no-effect proof. Retain a
// scoped local unknown observation when the request is still alive; if cancelled,
// the already committed capture remains sufficient for subsequent reconciliation.
func (s HumanBranchCreates) unknown(ctx context.Context, c factory.HumanBranchCreateCapture) {
	if ctx.Err() != nil {
		return
	}
	_, _ = s.Store.ObserveHumanBranchCreate(ctx, c, collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown, Reason: "provider_reply_unavailable"})
}

func (s HumanBranchCreates) save(ctx context.Context, c factory.HumanBranchCreateCapture, r collaboration.OperationRecord, err error) (collaboration.OperationLookup, error) {
	if err != nil {
		s.unknown(ctx, c)
		return collaboration.OperationLookup{}, err
	}
	o := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
	return s.Store.ObserveHumanBranchCreate(ctx, c, o)
}

// Submit always reconciles this exact saved capture under current provider
// admission before deciding whether dispatch is needed. A known record is never
// submitted again; only admitted not-observed can dispatch the saved intent.
func (s HumanBranchCreates) Submit(ctx context.Context, q factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.SubmitHumanBranchCreate(ctx, c)
	return s.save(ctx, c, r, err)
}

// Lookup does not prepare another provider snapshot or interpret an absent local
// command as remote absence. Every returned receipt first faces live admission.
func (s HumanBranchCreates) Lookup(ctx context.Context, q factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.observe(ctx, c)
}

// Cancel can win before submit, committing the capture then a real native
// tombstone. A terminal primary needs no cancellation; its original outcome is
// immutable. Pending/uncertain operations use the saved provider cancellation.
func (s HumanBranchCreates) Cancel(ctx context.Context, q factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
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
	r, err := s.Writer.CancelHumanBranchCreate(ctx, c)
	return s.save(ctx, c, r, err)
}
