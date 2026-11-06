package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanMerge(ctx context.Context, capture factory.HumanMergeCapture) (factory.HumanMergeCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanMergeCapture, func(saved factory.HumanMergeCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanMergeCapture(ctx context.Context, q factory.HumanMergeCommand) (factory.HumanMergeCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanMergeCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanMerge, factory.DecodeHumanMergeCapture, func(saved factory.HumanMergeCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanMerge(ctx context.Context, capture factory.HumanMergeCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
