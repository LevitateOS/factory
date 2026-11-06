package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanCommentQuery       = errors.New("invalid human comment request")
	ErrHumanCommentAdmission   = errors.New("current human comment admission required")
	ErrHumanCommentConflict    = errors.New("human comment intent conflicts")
	ErrHumanCommentUnavailable = errors.New("human comment provider unavailable")
	ErrHumanCommentNamespace   = errors.New("captured comment installation differs from current provider")
)

// HumanCommentWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanCommentWriter interface {
	PrepareHumanComment(context.Context, factory.HumanCommentCommand) (factory.HumanCommentCapture, error)
	SubmitHumanComment(context.Context, factory.HumanCommentCapture) (collaboration.OperationRecord, error)
	LookupHumanComment(context.Context, factory.HumanCommentCapture) (collaboration.OperationLookup, error)
	CancelHumanComment(context.Context, factory.HumanCommentCapture) (collaboration.OperationRecord, error)
}

// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanComments struct {
	Store  *store.Store
	Writer HumanCommentWriter
}

func (a Application) HumanComments(writer HumanCommentWriter) HumanComments {
	return HumanComments{Store: a.Store, Writer: writer}
}

// command resolves original server-retained writing only within the admitted
// host/actor/repository/parent. Public lookup never accepts provider preparation.
func (s HumanComments) command(ctx context.Context, scope factory.HumanCommentCommand) (factory.HumanCommentCommand, error) {
	if scope.ValidateScope() != nil || scope.Body != "" {
		return factory.HumanCommentCommand{}, ErrHumanCommentQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanCommentCommand{}, ErrHumanCommentUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanCommentCommand{}, err
	}
	c, err := factory.DecodeHumanCommentCapture(cmd)
	if err != nil {
		return factory.HumanCommentCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Body = ""
	if actual != scope {
		return factory.HumanCommentCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanComments) LookupCommand(ctx context.Context, scope factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanComments) CancelCommand(ctx context.Context, scope factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanComments) capture(ctx context.Context, q factory.HumanCommentCommand, prepare bool) (factory.HumanCommentCapture, error) {
	var empty factory.HumanCommentCapture
	if q.Validate() != nil {
		return empty, ErrHumanCommentQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanCommentUnavailable
	}
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanCommentCapture, error) {
			return s.Store.HumanCommentCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanCommentCapture, error) {
			return s.Writer.PrepareHumanComment(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if capture.Request != q {
		return empty, ErrHumanCommentConflict
	}
	capture, _, err = s.Store.CaptureHumanComment(ctx, capture)
	return capture, err
}

func (s HumanComments) effect(capture factory.HumanCommentCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanComment(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanComment(ctx, capture, observation)
		},
	}
}

func (s HumanComments) Submit(ctx context.Context, q factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanComment(ctx, capture)
	})
}

func (s HumanComments) Lookup(ctx context.Context, q factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanComments) Cancel(ctx context.Context, q factory.HumanCommentCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanComment(ctx, capture)
	})
}
