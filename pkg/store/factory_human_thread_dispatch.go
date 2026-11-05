package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanThreadDispatchState reads the exact capture's local fence. It does not
// prove current authority, provider absence or safe replay of a resolution.
func (s *Store) HumanThreadDispatchState(ctx context.Context, c factory.HumanThreadCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanThreadDispatch commits before a one-shot native effect. Only the
// caller receiving true may send once; observing reserved cannot recover a claim.
func (s *Store) ReserveHumanThreadDispatch(ctx context.Context, c factory.HumanThreadCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanThreadDispatch uses the same row lock as reservation. Call only
// after current caller admission and when this fence guards EVERY native attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanThreadDispatch(ctx context.Context, c factory.HumanThreadCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
