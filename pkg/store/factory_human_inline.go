package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanInline(ctx context.Context, capture factory.HumanInlineCapture) (factory.HumanInlineCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanInlineCapture, func(saved factory.HumanInlineCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanInlineCapture(ctx context.Context, q factory.HumanInlineCommand) (factory.HumanInlineCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanInlineCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanInline, factory.DecodeHumanInlineCapture, func(saved factory.HumanInlineCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanInline(ctx context.Context, capture factory.HumanInlineCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
