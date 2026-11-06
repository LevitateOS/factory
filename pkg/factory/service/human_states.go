package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
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
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanStateCapture, error) { return s.Store.HumanStateCapture(ctx, q) },
		func(ctx context.Context) (factory.HumanStateCapture, error) {
			return s.Writer.PrepareHumanState(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanStateConflict
	}
	capture, _, err = s.Store.CaptureHumanState(ctx, capture)
	return capture, err
}

func (s HumanStates) effect(capture factory.HumanStateCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanState(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanState(ctx, capture, observation)
		},
	}
}

func (s HumanStates) Submit(ctx context.Context, q factory.HumanStateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanState(ctx, capture)
	})
}

func (s HumanStates) Lookup(ctx context.Context, q factory.HumanStateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanStates) Cancel(ctx context.Context, q factory.HumanStateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanState(ctx, capture)
	})
}
