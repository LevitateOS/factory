package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanBranchCreate(ctx context.Context, capture factory.HumanBranchCreateCapture) (factory.HumanBranchCreateCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanBranchCreateCapture, func(saved factory.HumanBranchCreateCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanBranchCreateCapture(ctx context.Context, q factory.HumanBranchCreateCommand) (factory.HumanBranchCreateCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanBranchCreateCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanBranchCreate, factory.DecodeHumanBranchCreateCapture, func(saved factory.HumanBranchCreateCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanBranchCreate(ctx context.Context, capture factory.HumanBranchCreateCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
