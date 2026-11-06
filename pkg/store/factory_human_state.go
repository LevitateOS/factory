package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanState(ctx context.Context, capture factory.HumanStateCapture) (factory.HumanStateCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanStateCapture, func(saved factory.HumanStateCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanStateCapture(ctx context.Context, q factory.HumanStateCommand) (factory.HumanStateCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanStateCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanState, factory.DecodeHumanStateCapture, func(saved factory.HumanStateCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanState(ctx context.Context, capture factory.HumanStateCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
