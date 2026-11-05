package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanEditDispatchState reads an exact capture's local fence, not authority,
// remote absence or an atomic provider content condition.
func (s *Store) HumanEditDispatchState(ctx context.Context, c factory.HumanEditCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanEditDispatch commits before dispatch. Only the caller receiving
// true may send once. A reserved read cannot recover lost claim ownership.
func (s *Store) ReserveHumanEditDispatch(ctx context.Context, c factory.HumanEditCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanEditDispatch shares the reservation lock. Use after current
// admission and only for adapters using this protocol for EVERY effect attempt.
func (s *Store) CancelHumanEditDispatch(ctx context.Context, c factory.HumanEditCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
