package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanLockDispatchState reads the exact capture's local fence, not current
// authority, remote absence or proof that a lock can be safely replayed.
func (s *Store) HumanLockDispatchState(ctx context.Context, c factory.HumanLockCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanLockDispatch commits before native dispatch. Only receiving true
// grants one attempt; reserved state cannot recover lost claim ownership.
func (s *Store) ReserveHumanLockDispatch(ctx context.Context, c factory.HumanLockCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanLockDispatch shares the reservation lock. Use after current caller
// admission and only when this fence guards EVERY native effect attempt.
func (s *Store) CancelHumanLockDispatch(ctx context.Context, c factory.HumanLockCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
