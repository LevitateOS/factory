package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrHumanReviewQuery       = errors.New("invalid human review request")
	ErrHumanReviewAdmission   = errors.New("current human review admission required")
	ErrHumanReviewConflict    = errors.New("human review intent conflicts")
	ErrHumanReviewUnavailable = errors.New("human review provider unavailable")
	ErrHumanReviewNamespace   = errors.New("captured review installation differs from current provider")
)

// HumanReviewWriter operates on the exact journaled capture. Prepare is a read,
// never a write grant. The application must commit its returned capture before
// Submit; transport failure must be reconciled under that capture, not reprepare.
// Submit must safely reconcile/replay this same identity without duplicate
// effects. Providers lacking that guarantee must retain uncertainty rather than
// expose an actionable not-observed state for a possibly submitted command.
type HumanReviewWriter interface {
	PrepareHumanReview(context.Context, factory.HumanReviewCommand) (factory.HumanReviewCapture, error)
	SubmitHumanReview(context.Context, factory.HumanReviewCapture) (collaboration.OperationRecord, error)
	LookupHumanReview(context.Context, factory.HumanReviewCapture) (collaboration.OperationLookup, error)
	CancelHumanReview(context.Context, factory.HumanReviewCapture) (collaboration.OperationRecord, error)
}

// A destination requires causal provider evidence and a fresh normal read.
// Hosts without that reader retain their existing command-only response.
type HumanReviewDestinationReader interface {
	ReadCreatedHumanReview(context.Context, factory.HumanReviewCapture, collaboration.OperationLookup) (collaboration.ReviewRef, error)
}

// Provider evidence stays internal; HTTP owns the safe public projection.
type HumanReviews struct {
	Store  *store.Store
	Writer HumanReviewWriter
}

func (a Application) HumanReviews(writer HumanReviewWriter) HumanReviews {
	return HumanReviews{Store: a.Store, Writer: writer}
}

func (s HumanReviews) CreatedReview(ctx context.Context, scope factory.HumanReviewCommand, outcome collaboration.OperationLookup) (collaboration.ReviewRef, error) {
	var empty collaboration.ReviewRef
	q, err := s.command(ctx, scope)
	if err != nil {
		return empty, err
	}
	c, err := s.capture(ctx, q, false)
	if err != nil {
		return empty, err
	}
	if factory.ValidateHumanReviewObservation(c, outcome) != nil || outcome.Record == nil || outcome.Record.Effect != collaboration.EffectCommitted {
		return empty, ErrHumanReviewQuery
	}
	reader, ok := s.Writer.(HumanReviewDestinationReader)
	if !ok {
		return empty, ErrHumanReviewUnavailable
	}
	ref, err := reader.ReadCreatedHumanReview(ctx, c, outcome)
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil || ref.Pull.Kind != collaboration.SubjectPull || ref.Pull.Repository != q.Repository || ref.Pull.Number != q.Number || !collaboration.ValidText(ref.Pull.ExternalID, 512) || !collaboration.ValidText(ref.ExternalID, 1024) {
		return empty, ErrHumanReviewUnavailable
	}
	return ref, nil
}

// command resolves original server-retained review event/prose only within its
// admitted host/actor/repository/parent. Lookup accepts no provider preparation.
func (s HumanReviews) command(ctx context.Context, scope factory.HumanReviewCommand) (factory.HumanReviewCommand, error) {
	if scope.ValidateScope() != nil || scope.Event != "" || scope.Body != "" || scope.ExpectedHead != "" {
		return factory.HumanReviewCommand{}, ErrHumanReviewQuery
	}
	if s.Store == nil || s.Writer == nil {
		return factory.HumanReviewCommand{}, ErrHumanReviewUnavailable
	}
	cmd, err := s.Store.FactoryCommand(ctx, scope.ID)
	if err != nil {
		return factory.HumanReviewCommand{}, err
	}
	c, err := factory.DecodeHumanReviewCapture(cmd)
	if err != nil {
		return factory.HumanReviewCommand{}, store.ErrNotFound
	}
	actual := c.Request
	actual.Event, actual.Body = "", ""
	actual.ExpectedHead = ""
	if !actual.SameRequest(scope) {
		return factory.HumanReviewCommand{}, store.ErrNotFound
	}
	return c.Request, nil
}

func (s HumanReviews) LookupCommand(ctx context.Context, scope factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Lookup(ctx, q)
}
func (s HumanReviews) CancelCommand(ctx context.Context, scope factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	q, err := s.command(ctx, scope)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.Cancel(ctx, q)
}

func (s HumanReviews) capture(ctx context.Context, q factory.HumanReviewCommand, prepare bool) (factory.HumanReviewCapture, error) {
	var empty factory.HumanReviewCapture
	if q.Validate() != nil {
		return empty, ErrHumanReviewQuery
	}
	if s.Store == nil || s.Writer == nil {
		return empty, ErrHumanReviewUnavailable
	}
	capture, fresh, err := loadOrPrepareHumanCapture(ctx, prepare,
		func(ctx context.Context) (factory.HumanReviewCapture, error) {
			return s.Store.HumanReviewCapture(ctx, q)
		},
		func(ctx context.Context) (factory.HumanReviewCapture, error) {
			return s.Writer.PrepareHumanReview(ctx, q)
		})
	if err != nil || !fresh {
		return capture, err
	}
	if !capture.Request.SameRequest(q) {
		return empty, ErrHumanReviewConflict
	}
	capture, _, err = s.Store.CaptureHumanReview(ctx, capture)
	return capture, err
}

func (s HumanReviews) effect(capture factory.HumanReviewCapture) humanEffect {
	return humanEffect{
		binding: collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID},
		lookup: func(ctx context.Context) (collaboration.OperationLookup, error) {
			return s.Writer.LookupHumanReview(ctx, capture)
		},
		persist: func(ctx context.Context, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return s.Store.ObserveHumanReview(ctx, capture, observation)
		},
	}
}

func (s HumanReviews) Submit(ctx context.Context, q factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).submit(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.SubmitHumanReview(ctx, capture)
	})
}

func (s HumanReviews) Lookup(ctx context.Context, q factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, false)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).reconcile(ctx)
}

func (s HumanReviews) Cancel(ctx context.Context, q factory.HumanReviewCommand) (collaboration.OperationLookup, error) {
	capture, err := s.capture(ctx, q, true)
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.effect(capture).cancel(ctx, func(ctx context.Context) (collaboration.OperationRecord, error) {
		return s.Writer.CancelHumanReview(ctx, capture)
	})
}
