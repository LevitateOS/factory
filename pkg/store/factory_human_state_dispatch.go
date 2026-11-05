package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanStateDispatchState reads the exact capture's local fence; neither a
// retained row nor its absence establishes remote state or current authority.
func (s *Store) HumanStateDispatchState(ctx context.Context, c factory.HumanStateCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanStateDispatch commits before native effect dispatch. Only the
// caller receiving true may send once; a reserved read cannot recover ownership.
func (s *Store) ReserveHumanStateDispatch(ctx context.Context, c factory.HumanStateCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanStateDispatch shares the reservation lock. Call after current
// admission and only for adapters using this protocol for EVERY effect attempt.
func (s *Store) CancelHumanStateDispatch(ctx context.Context, c factory.HumanStateCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
