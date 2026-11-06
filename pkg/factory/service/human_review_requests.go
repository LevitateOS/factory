package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanReviewRequestQuery       = errors.New("invalid human reviewer request")
	ErrHumanReviewRequestAdmission   = errors.New("current human reviewer admission required")
	ErrHumanReviewRequestConflict    = errors.New("human reviewer intent conflicts")
	ErrHumanReviewRequestUnavailable = errors.New("human reviewer provider unavailable")
	ErrHumanReviewRequestTooLarge    = errors.New("complete reviewer preparation exceeds provider limit")
	ErrHumanReviewRequestNamespace   = errors.New("captured reviewer installation differs from current provider")
)

// HumanReviewRequestWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanReviewRequestWriter interface {
	PrepareHumanReviewRequest(context.Context, factory.HumanReviewRequestCommand) (factory.HumanReviewRequestCapture, error)
	SubmitHumanReviewRequest(context.Context, factory.HumanReviewRequestCapture) (collaboration.OperationRecord, error)
	LookupHumanReviewRequest(context.Context, factory.HumanReviewRequestCapture) (collaboration.OperationLookup, error)
	CancelHumanReviewRequest(context.Context, factory.HumanReviewRequestCapture) (collaboration.OperationRecord, error)
}

// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanReviewRequests struct {
	Store  *store.Store
	Writer HumanReviewRequestWriter
}

func (a Application) HumanReviewRequests(writer HumanReviewRequestWriter) HumanReviewRequests {
	return HumanReviewRequests{Store: a.Store, Writer: writer}
}

// command resolves the original reviewer selection and desired state only within
// its admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanReviewRequests) command(ctx context.Context, scope factory.HumanReviewRequestCommand) (factory.HumanReviewRequestCommand, error) {
	if scope.ValidateScope() != nil || scope.Requested || scope.Reviewer != 0 || scope.Team != 0 {
		return factory.HumanReviewRequestCommand{}, ErrHumanReviewRequestQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanReviewRequestCommand{}, ErrHumanReviewRequestUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanReviewRequestCommand{}, err
	}
	c, err := factory.DecodeHumanReviewRequestCapture(cmd)
	if err != nil {
		return factory.HumanReviewRequestCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Requested, actual.Reviewer, actual.Team = false, 0, 0
	if !actual.SameRequest(scope) {
		return factory.HumanReviewRequestCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanReviewRequests) LookupCommand(ctx context.Context, scope factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanReviewRequests) CancelCommand(ctx context.Context, scope factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanReviewRequests) capture(ctx context.Context, q factory.HumanReviewRequestCommand, prepare bool) (factory.HumanReviewRequestCapture, error) {
	var empty factory.HumanReviewRequestCapture
	if q.Validate() != nil {
		return empty, ErrHumanReviewRequestQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanReviewRequestUnavailable
	}
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanReviewRequestCapture, error) {
			return s.Store.HumanReviewRequestCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanReviewRequestCapture, error) {
			return s.Writer.PrepareHumanReviewRequest(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanReviewRequestConflict
	}
	capture, _, err = s.Store.CaptureHumanReviewRequest(ctx, capture)
	return capture, err
}

func (s HumanReviewRequests) effect(capture factory.HumanReviewRequestCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanReviewRequest(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanReviewRequest(ctx, capture, observation)
		},
	}
}

func (s HumanReviewRequests) Submit(ctx context.Context, q factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanReviewRequest(ctx, capture)
	})
}

func (s HumanReviewRequests) Lookup(ctx context.Context, q factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanReviewRequests) Cancel(ctx context.Context, q factory.HumanReviewRequestCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanReviewRequest(ctx, capture)
	})
}
