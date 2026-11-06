package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanReview(ctx context.Context, capture factory.HumanReviewCapture) (factory.HumanReviewCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanReviewCapture, func(saved factory.HumanReviewCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanReviewCapture(ctx context.Context, q factory.HumanReviewCommand) (factory.HumanReviewCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanReviewCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanReview, factory.DecodeHumanReviewCapture, func(saved factory.HumanReviewCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanReview(ctx context.Context, capture factory.HumanReviewCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
