package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanPullCreateDispatchState reads the exact capture's local fence. It proves
// neither current branch/permission facts nor ownership of an earlier claim.
func (s *Store) HumanPullCreateDispatchState(ctx context.Context, c factory.HumanPullCreateCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanPullCreateDispatch commits before one native creation attempt.
// Only receiving true permits one send. Reserved state cannot recover ownership.
func (s *Store) ReserveHumanPullCreateDispatch(ctx context.Context, c factory.HumanPullCreateCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanPullCreateDispatch uses the reservation row lock. Call only after
// current admission and when this fence guards every native creation attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanPullCreateDispatch(ctx context.Context, c factory.HumanPullCreateCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
