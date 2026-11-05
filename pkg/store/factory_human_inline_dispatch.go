package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanInlineDispatchState reads the exact capture's local fence. It proves
// neither current head/permission facts nor ownership of an earlier claim.
func (s *Store) HumanInlineDispatchState(ctx context.Context, c factory.HumanInlineCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanInlineDispatch commits before one native inline attempt.
// Only receiving true permits one send. Reserved state cannot recover ownership.
func (s *Store) ReserveHumanInlineDispatch(ctx context.Context, c factory.HumanInlineCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanInlineDispatch uses the reservation row lock. Call only after
// current admission and when this fence guards every native inline attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanInlineDispatch(ctx context.Context, c factory.HumanInlineCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
