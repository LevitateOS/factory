package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanMergeQuery       = errors.New("invalid human merge request")
	ErrHumanMergeAdmission   = errors.New("current human merge admission required")
	ErrHumanMergeConflict    = errors.New("human merge intent conflicts")
	ErrHumanMergeUnavailable = errors.New("human merge provider unavailable")
	ErrHumanMergeNamespace   = errors.New("captured merge installation differs from current provider")
)

// HumanMergeWriter consumes exact journaled preparation for ordinary merges and
// manual marking. Preparation is a read, not authority. Commit its returned
// capture before dispatch, and reconcile lost replies using that same capture.
// Lookup/cancel require current human admission even after permission loss.
type HumanMergeWriter interface {
	PrepareHumanMerge(context.Context, factory.HumanMergeCommand) (factory.HumanMergeCapture, error)
	SubmitHumanMerge(context.Context, factory.HumanMergeCapture) (collaboration.OperationRecord, error)
	LookupHumanMerge(context.Context, factory.HumanMergeCapture) (collaboration.OperationLookup, error)
	CancelHumanMerge(context.Context, factory.HumanMergeCapture) (collaboration.OperationRecord, error)
}

// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanMerges struct {
	Store  *store.Store
	Writer HumanMergeWriter
}

func (a Application) HumanMerges(writer HumanMergeWriter) HumanMerges {
	return HumanMerges{Store: a.Store, Writer: writer}
}

// command resolves the original method/message or selected commit only within
// its admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanMerges) command(ctx context.Context, scope factory.HumanMergeCommand) (factory.HumanMergeCommand, error) {
	if scope.ValidateScope() != nil || scope.Method != "" || scope.Message != "" || scope.CommitOID != "" || scope.ExpectedHead != "" {
		return factory.HumanMergeCommand{}, ErrHumanMergeQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanMergeCommand{}, ErrHumanMergeUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanMergeCommand{}, err
	}
	c, err := factory.DecodeHumanMergeCapture(cmd)
	if err != nil {
		return factory.HumanMergeCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Method, actual.Message, actual.CommitOID = "", "", ""
	actual.ExpectedHead = ""
	if !actual.SameRequest(scope) {
		return factory.HumanMergeCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanMerges) LookupCommand(ctx context.Context, scope factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanMerges) CancelCommand(ctx context.Context, scope factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanMerges) capture(ctx context.Context, q factory.HumanMergeCommand, prepare bool) (factory.HumanMergeCapture, error) {
	var empty factory.HumanMergeCapture
	if q.Validate() != nil {
		return empty, ErrHumanMergeQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanMergeUnavailable
	}
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanMergeCapture, error) { return s.Store.HumanMergeCapture(ctx, q) },
		func(ctx context.Context) (factory.HumanMergeCapture, error) {
			return s.Writer.PrepareHumanMerge(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanMergeConflict
	}
	capture, _, err = s.Store.CaptureHumanMerge(ctx, capture)
	return capture, err
}

func (s HumanMerges) effect(capture factory.HumanMergeCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanMerge(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanMerge(ctx, capture, observation)
		},
	}
}

func (s HumanMerges) Submit(ctx context.Context, q factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanMerge(ctx, capture)
	})
}

func (s HumanMerges) Lookup(ctx context.Context, q factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanMerges) Cancel(ctx context.Context, q factory.HumanMergeCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanMerge(ctx, capture)
	})
}
