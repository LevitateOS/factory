package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureHumanIssueCreate(ctx context.Context, capture factory.HumanIssueCreateCapture) (factory.HumanIssueCreateCapture, bool, error) {
	return captureHumanCommand(ctx, s, capture, factory.DecodeHumanIssueCreateCapture, func(saved factory.HumanIssueCreateCapture) bool {
		return saved.Request.SameRequest(capture.Request)
	})
}

func (s *Store) HumanIssueCreateCapture(ctx context.Context, q factory.HumanIssueCreateCommand) (factory.HumanIssueCreateCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanIssueCreateCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandHumanIssueCreate, factory.DecodeHumanIssueCreateCapture, func(saved factory.HumanIssueCreateCapture) bool {
		return saved.Request.SameRequest(q)
	})
}

func (s *Store) ObserveHumanIssueCreate(ctx context.Context, capture factory.HumanIssueCreateCapture, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, capture, observation)
}
