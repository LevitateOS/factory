package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanReviewRequestDispatchState reads the exact local capture's fence. It does
// not prove current permission, provider absence or ownership of a prior claim.
func (s *Store) HumanReviewRequestDispatchState(ctx context.Context, c factory.HumanReviewRequestCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanReviewRequestDispatch commits before a one-shot native effect.
// Only receiving true permits one send; reserved state cannot recover ownership.
func (s *Store) ReserveHumanReviewRequestDispatch(ctx context.Context, c factory.HumanReviewRequestCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanReviewRequestDispatch shares the reservation row lock. Use only
// after current caller admission and when this fence guards EVERY native attempt.
// A remote host's idempotent journal retains its own cancellation protocol.
func (s *Store) CancelHumanReviewRequestDispatch(ctx context.Context, c factory.HumanReviewRequestCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
