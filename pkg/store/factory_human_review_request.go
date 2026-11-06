package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanReviewRequest(ctx context.Context, capture factory.HumanReviewRequestCapture) (factory.HumanReviewRequestCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanReviewRequestCapture, func(saved factory.HumanReviewRequestCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanReviewRequestCapture(ctx context.Context, q factory.HumanReviewRequestCommand) (factory.HumanReviewRequestCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanReviewRequestCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanReviewRequest, factory.DecodeHumanReviewRequestCapture, func(saved factory.HumanReviewRequestCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanReviewRequest(ctx context.Context, capture factory.HumanReviewRequestCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
