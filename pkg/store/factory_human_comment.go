package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanComment(ctx context.Context, capture factory.HumanCommentCapture) (factory.HumanCommentCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanCommentCapture, func(saved factory.HumanCommentCapture) bool {
		return saved.Request == capture.Request
	})
}

func (s *Store) HumanCommentCapture(ctx context.Context, q factory.HumanCommentCommand) (factory.HumanCommentCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanCommentCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanComment, factory.DecodeHumanCommentCapture, func(saved factory.HumanCommentCapture) bool {
		return saved.Request == q
	})
}

func (s *Store) ObserveHumanComment(ctx context.Context, capture factory.HumanCommentCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
