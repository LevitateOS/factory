package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanPullCreate(ctx context.Context, capture factory.HumanPullCreateCapture) (factory.HumanPullCreateCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanPullCreateCapture, func(saved factory.HumanPullCreateCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanPullCreateCapture(ctx context.Context, q factory.HumanPullCreateCommand) (factory.HumanPullCreateCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanPullCreateCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanPullCreate, factory.DecodeHumanPullCreateCapture, func(saved factory.HumanPullCreateCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanPullCreate(ctx context.Context, capture factory.HumanPullCreateCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
