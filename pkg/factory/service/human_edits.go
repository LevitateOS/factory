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
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanEditCapture, error) { return s.Store.HumanEditCapture(ctx, q) },
		func(ctx context.Context) (factory.HumanEditCapture, error) { return s.Writer.PrepareHumanEdit(ctx, q) })
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanEditConflict
	}
	capture, _, err = s.Store.CaptureHumanEdit(ctx, capture)
	return capture, err
}

func (s HumanEdits) effect(capture factory.HumanEditCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanEdit(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanEdit(ctx, capture, observation)
		},
	}
}

func (s HumanEdits) Submit(ctx context.Context, q factory.HumanEditCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		// A stale captured edit obtains a real cancellation tombstone after
		// reconciliation; a refused edit alone cannot prove no effect.
		if !capture.ExpectedCurrent() {
			return s.Writer.CancelHumanEdit(ctx, capture)
		}
		return s.Writer.SubmitHumanEdit(ctx, capture)
	})
}

func (s HumanEdits) Lookup(ctx context.Context, q factory.HumanEditCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanEdits) Cancel(ctx context.Context, q factory.HumanEditCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanEdit(ctx, capture)
	})
}
