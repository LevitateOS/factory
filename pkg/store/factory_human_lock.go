package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanLock(ctx context.Context, capture factory.HumanLockCapture) (factory.HumanLockCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanLockCapture, func(saved factory.HumanLockCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanLockCapture(ctx context.Context, q factory.HumanLockCommand) (factory.HumanLockCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanLockCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanLock, factory.DecodeHumanLockCapture, func(saved factory.HumanLockCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanLock(ctx context.Context, capture factory.HumanLockCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
