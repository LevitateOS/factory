package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanBranchCreateDispatchState reads the exact capture's local fence. It proves
// neither current branch/permission facts nor ownership of an earlier claim.
func (s *Store) HumanBranchCreateDispatchState(ctx context.Context, c factory.HumanBranchCreateCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanBranchCreateDispatch commits before one native creation attempt.
// Only receiving true permits one send. Reserved state cannot recover ownership.
func (s *Store) ReserveHumanBranchCreateDispatch(ctx context.Context, c factory.HumanBranchCreateCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanBranchCreateDispatch uses the reservation row lock. Call only after
// current admission and when this fence guards every native creation attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanBranchCreateDispatch(ctx context.Context, c factory.HumanBranchCreateCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
