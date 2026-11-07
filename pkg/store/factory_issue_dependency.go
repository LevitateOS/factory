package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureIssueDependency(ctx context.Context, c factory.IssueDependencyCapture) (factory.IssueDependencyCapture, bool, error) {
	return captureHumanCommand(ctx, s, c, factory.DecodeIssueDependencyCapture, func(saved factory.IssueDependencyCapture) bool { return saved.Request.SameRequest(c.Request) })
}
func (s *Store) IssueDependencyCapture(ctx context.Context, q factory.IssueDependencyCommand) (factory.IssueDependencyCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.IssueDependencyCapture{}, err
	}
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandIssueDependency, factory.DecodeIssueDependencyCapture, func(c factory.IssueDependencyCapture) bool { return c.Request.SameRequest(q) })
}
func (s *Store) ObserveIssueDependency(ctx context.Context, c factory.IssueDependencyCapture, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, c, o)
}
func (s *Store) ReserveIssueDependency(ctx context.Context, c factory.IssueDependencyCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}
func (s *Store) CancelIssueDependency(ctx context.Context, c factory.IssueDependencyCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
