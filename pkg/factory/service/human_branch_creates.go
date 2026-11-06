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
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanBranchCreateCapture, error) {
			return s.Store.HumanBranchCreateCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanBranchCreateCapture, error) {
			return s.Writer.PrepareHumanBranchCreate(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanBranchCreateConflict
	}
	capture, _, err = s.Store.CaptureHumanBranchCreate(ctx, capture)
	return capture, err
}

func (s HumanBranchCreates) effect(capture factory.HumanBranchCreateCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanBranchCreate(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanBranchCreate(ctx, capture, observation)
		},
	}
}

func (s HumanBranchCreates) Submit(ctx context.Context, q factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanBranchCreate(ctx, capture)
	})
}

func (s HumanBranchCreates) Lookup(ctx context.Context, q factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanBranchCreates) Cancel(ctx context.Context, q factory.HumanBranchCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanBranchCreate(ctx, capture)
	})
}
