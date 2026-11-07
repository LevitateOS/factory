package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func (s *Store) CaptureRefPublication(ctx context.Context, c factory.RefPublicationCapture) (factory.RefPublicationCapture, bool, error) {
	return captureHumanCommand(ctx, s, c, factory.DecodeRefPublicationCapture, func(saved factory.RefPublicationCapture) bool {
		return saved.Request == c.Request && saved.ExpectedOld == c.ExpectedOld
	})
}
func (s *Store) RefPublicationCapture(ctx context.Context, q factory.HumanBranchCreateCommand, old string) (factory.RefPublicationCapture, error) {
	return lookupHumanCapture(ctx, s, q.ID, factory.CommandRefPublish, factory.DecodeRefPublicationCapture, func(c factory.RefPublicationCapture) bool { return c.Request == q && c.ExpectedOld == old })
}
func (s *Store) ObserveRefPublication(ctx context.Context, c factory.RefPublicationCapture, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	return observeHumanCommand(ctx, s, c, o)
}
func (s *Store) ReserveRefPublicationDispatch(ctx context.Context, c factory.RefPublicationCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}
func (s *Store) CancelRefPublicationDispatch(ctx context.Context, c factory.RefPublicationCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
