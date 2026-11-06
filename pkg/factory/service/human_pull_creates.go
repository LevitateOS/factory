package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanPullCreateQuery       = errors.New("invalid human pull creation request")
	ErrHumanPullCreateAdmission   = errors.New("current human pull creation admission required")
	ErrHumanPullCreateConflict    = errors.New("human pull creation intent conflicts")
	ErrHumanPullCreateUnavailable = errors.New("human pull creation provider unavailable")
	ErrHumanPullCreateNamespace   = errors.New("captured pull creation installation differs from current provider")
)

// HumanPullCreateWriter operates on the exact saved capture. Preparation is a
// read, never authority: the application must commit it before Submit. Providers
// reconcile/replay that identity without duplicate creation; they cannot reprepare
// or project actionable absence for a possibly submitted command without proof.
type HumanPullCreateWriter interface {
	PrepareHumanPullCreate(context.Context, factory.HumanPullCreateCommand) (factory.HumanPullCreateCapture, error)
	SubmitHumanPullCreate(context.Context, factory.HumanPullCreateCapture) (collaboration.OperationRecord, error)
	LookupHumanPullCreate(context.Context, factory.HumanPullCreateCapture) (collaboration.OperationLookup, error)
	CancelHumanPullCreate(context.Context, factory.HumanPullCreateCapture) (collaboration.OperationRecord, error)
}

// Destination reads are optional for hosts without a causal creation receipt.
// A current PR read cannot itself establish which command created that PR.
type HumanPullCreateDestinationReader interface {
	ReadCreatedHumanPull(context.Context, factory.HumanPullCreateCapture, collaboration.OperationLookup) (collaboration.PullRef, error)
}

// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanPullCreates struct {
	Store  *store.Store
	Writer HumanPullCreateWriter
}

func (a Application) HumanPullCreates(writer HumanPullCreateWriter) HumanPullCreates {
	return HumanPullCreates{Store: a.Store, Writer: writer}
}

// command recovers the original source, branches, writing and delegation only
// within the admitted host/base repository/actor. Cleanup accepts no replacement
// creation fields or provider preparation. The source may no longer be readable.
func (s HumanPullCreates) command(ctx context.Context, scope factory.HumanPullCreateCommand) (factory.HumanPullCreateCommand, error) {
	if !factory.ValidID(scope.ID) || !collaboration.ValidID(string(scope.Host)) || scope.Repository <= 0 || scope.Actor <= 0 ||
		scope.HeadRepository != 0 || scope.BaseBranch != "" || scope.HeadBranch != "" || scope.Title != "" || scope.Body != "" || scope.AllowMaintainerEdit {
		return factory.HumanPullCreateCommand{}, ErrHumanPullCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanPullCreateCommand{}, ErrHumanPullCreateUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanPullCreateCommand{}, err
	}
	c, err := factory.DecodeHumanPullCreateCapture(cmd)
	if err != nil {
		return factory.HumanPullCreateCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.HeadRepository, actual.BaseBranch, actual.HeadBranch = 0, "", ""
	actual.Title, actual.Body, actual.AllowMaintainerEdit = "", "", false
	if !actual.SameRequest(scope) {
		return factory.HumanPullCreateCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanPullCreates) LookupCommand(ctx context.Context, scope factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanPullCreates) CancelCommand(ctx context.Context, scope factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

// CreatedPull checks a fresh normal read without changing the recorded effect
// if the destination is no longer visible.
func (s HumanPullCreates) CreatedPull(ctx context.Context, scope factory.HumanPullCreateCommand, outcome collaboration.OperationLookup) (collaboration.PullRef, error) {
	var empty collaboration.PullRef
	q, err := s.command(ctx, scope)
	if err != nil {
		return empty, err
	}
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanPullCreateObservation(c, outcome) != nil || outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted {
		return empty, ErrHumanPullCreateQuery
	}
	reader, ok := s.Writer.(HumanPullCreateDestinationReader)
	if !ok {
		return empty, ErrHumanPullCreateUnavailable
	}
	pull, err := reader.ReadCreatedHumanPull(ctx, c, outcome)
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil || pull.Kind != collaboration.SubjectPull || pull.Repository != q.Repository || pull.Number <= 0 || !collaboration.ValidText(pull.ExternalID, 512) {
		return empty, ErrHumanPullCreateUnavailable
	}
	return pull, nil
}

func (s HumanPullCreates) capture(ctx context.Context, q factory.HumanPullCreateCommand, prepare bool) (factory.HumanPullCreateCapture, error) {
	var empty factory.HumanPullCreateCapture
	if q.Validate() != nil {
		return empty, ErrHumanPullCreateQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanPullCreateUnavailable
	}
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanPullCreateCapture, error) {
			return s.Store.HumanPullCreateCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanPullCreateCapture, error) {
			return s.Writer.PrepareHumanPullCreate(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanPullCreateConflict
	}
	capture, _, err = s.Store.CaptureHumanPullCreate(ctx, capture)
	return capture, err
}

func (s HumanPullCreates) effect(capture factory.HumanPullCreateCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanPullCreate(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanPullCreate(ctx, capture, observation)
		},
	}
}

func (s HumanPullCreates) Submit(ctx context.Context, q factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanPullCreate(ctx, capture)
	})
}

func (s HumanPullCreates) Lookup(ctx context.Context, q factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanPullCreates) Cancel(ctx context.Context, q factory.HumanPullCreateCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanPullCreate(ctx, capture)
	})
}
