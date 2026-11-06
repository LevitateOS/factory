package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanLockQuery       = errors.New("invalid human lock request")
	ErrHumanLockAdmission   = errors.New("current human lock admission required")
	ErrHumanLockConflict    = errors.New("human lock intent conflicts")
	ErrHumanLockUnavailable = errors.New("human lock provider unavailable")
	ErrHumanLockNamespace   = errors.New("captured lock installation differs from current provider")
)

// HumanLockWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanLockWriter interface {
	PrepareHumanLock(context.Context, factory.HumanLockCommand) (factory.HumanLockCapture, error)
	SubmitHumanLock(context.Context, factory.HumanLockCapture) (collaboration.OperationRecord, error)
	LookupHumanLock(context.Context, factory.HumanLockCapture) (collaboration.OperationLookup, error)
	CancelHumanLock(context.Context, factory.HumanLockCapture) (collaboration.OperationRecord, error)
}

// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanLocks struct {
	Store  *store.Store
	Writer HumanLockWriter
}

func (a Application) HumanLocks(writer HumanLockWriter) HumanLocks {
	return HumanLocks{Store: a.Store, Writer: writer}
}

// command resolves original server-retained lock state/reason only within its
// admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanLocks) command(ctx context.Context, scope factory.HumanLockCommand) (factory.HumanLockCommand, error) {
	if scope.ValidateScope() != nil || scope.Lock || scope.Reason != "" {
		return factory.HumanLockCommand{}, ErrHumanLockQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanLockCommand{}, ErrHumanLockUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanLockCommand{}, err
	}
	c, err := factory.DecodeHumanLockCapture(cmd)
	if err != nil {
		return factory.HumanLockCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Lock, actual.Reason = false, ""
	if !actual.SameRequest(scope) {
		return factory.HumanLockCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanLocks) LookupCommand(ctx context.Context, scope factory.HumanLockCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanLocks) CancelCommand(ctx context.Context, scope factory.HumanLockCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanLocks) capture(ctx context.Context, q factory.HumanLockCommand, prepare bool) (factory.HumanLockCapture, error) {
	var empty factory.HumanLockCapture
	if q.Validate() != nil {
		return empty, ErrHumanLockQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanLockUnavailable
	}
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanLockCapture, error) { return s.Store.HumanLockCapture(ctx, q) },
		func(ctx context.Context) (factory.HumanLockCapture, error) { return s.Writer.PrepareHumanLock(ctx, q) })
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanLockConflict
	}
	capture, _, err = s.Store.CaptureHumanLock(ctx, capture)
	return capture, err
}

func (s HumanLocks) effect(capture factory.HumanLockCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanLock(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanLock(ctx, capture, observation)
		},
	}
}

func (s HumanLocks) Submit(ctx context.Context, q factory.HumanLockCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanLock(ctx, capture)
	})
}

func (s HumanLocks) Lookup(ctx context.Context, q factory.HumanLockCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanLocks) Cancel(ctx context.Context, q factory.HumanLockCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanLock(ctx, capture)
	})
}
