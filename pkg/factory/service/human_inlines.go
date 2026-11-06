package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
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
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanInlineCapture, error) {
			return s.Store.HumanInlineCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanInlineCapture, error) {
			return s.Writer.PrepareHumanInline(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanInlineConflict
	}
	capture, _, err = s.Store.CaptureHumanInline(ctx, capture)
	return capture, err
}

func (s HumanInlines) effect(capture factory.HumanInlineCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanInline(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanInline(ctx, capture, observation)
		},
	}
}

func (s HumanInlines) Submit(ctx context.Context, q factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanInline(ctx, capture)
	})
}

func (s HumanInlines) Lookup(ctx context.Context, q factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanInlines) Cancel(ctx context.Context, q factory.HumanInlineCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanInline(ctx, capture)
	})
}
