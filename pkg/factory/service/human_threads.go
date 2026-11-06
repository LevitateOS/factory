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
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanThreadCapture, error) {
			return s.Store.HumanThreadCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanThreadCapture, error) {
			return s.Writer.PrepareHumanThread(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanThreadConflict
	}
	capture, _, err = s.Store.CaptureHumanThread(ctx, capture)
	return capture, err
}

func (s HumanThreads) effect(capture factory.HumanThreadCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanThread(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanThread(ctx, capture, observation)
		},
	}
}

func (s HumanThreads) Submit(ctx context.Context, q factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanThread(ctx, capture)
	})
}

func (s HumanThreads) Lookup(ctx context.Context, q factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanThreads) Cancel(ctx context.Context, q factory.HumanThreadCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanThread(ctx, capture)
	})
}
