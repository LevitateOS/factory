package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanEdit(ctx context.Context, capture factory.HumanEditCapture) (factory.HumanEditCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanEditCapture, func(saved factory.HumanEditCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanEditCapture(ctx context.Context, q factory.HumanEditCommand) (factory.HumanEditCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanEditCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanEdit, factory.DecodeHumanEditCapture, func(saved factory.HumanEditCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanEdit(ctx context.Context, capture factory.HumanEditCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
