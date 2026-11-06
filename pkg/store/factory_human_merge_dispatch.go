package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanMergeDispatchState reads the exact capture's local fence. It proves
// neither current head/permission facts nor ownership of an earlier claim.
func (s *Store) HumanMergeDispatchState(ctx context.Context, c factory.HumanMergeCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanMergeDispatch commits before one native merge attempt.
// Only receiving true permits one send. Reserved state cannot recover ownership.
func (s *Store) ReserveHumanMergeDispatch(ctx context.Context, c factory.HumanMergeCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanMergeDispatch uses the reservation row lock. Call only after
// current admission and when this fence guards every native merge attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanMergeDispatch(ctx context.Context, c factory.HumanMergeCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
