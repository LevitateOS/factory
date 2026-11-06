package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanThread(ctx context.Context, capture factory.HumanThreadCapture) (factory.HumanThreadCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanThreadCapture, func(saved factory.HumanThreadCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanThreadCapture(ctx context.Context, q factory.HumanThreadCommand) (factory.HumanThreadCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanThreadCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanThread, factory.DecodeHumanThreadCapture, func(saved factory.HumanThreadCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanThread(ctx context.Context, capture factory.HumanThreadCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
