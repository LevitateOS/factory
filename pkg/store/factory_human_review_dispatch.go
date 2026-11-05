package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanReviewDispatchState reads the exact capture's local fence. It proves
// neither current head/permission facts nor ownership of an earlier claim.
func (s *Store) HumanReviewDispatchState(ctx context.Context, c factory.HumanReviewCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanReviewDispatch commits before one native review attempt.
// Only receiving true permits one send. Reserved state cannot recover ownership.
func (s *Store) ReserveHumanReviewDispatch(ctx context.Context, c factory.HumanReviewCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanReviewDispatch uses the reservation row lock. Call only after
// current admission and when this fence guards every native review attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanReviewDispatch(ctx context.Context, c factory.HumanReviewCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
