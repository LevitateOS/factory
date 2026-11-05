package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanIssueCreateDispatchState reads the exact capture's local fence; it proves
// neither remote absence nor current authority. Absence is ErrNotFound.
func (s *Store) HumanIssueCreateDispatchState(ctx context.Context, c factory.HumanIssueCreateCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanIssueCreateDispatch commits a one-shot claim before network use.
// Only true permits one attempt; a reserved read never recovers that permission.
func (s *Store) ReserveHumanIssueCreateDispatch(ctx context.Context, c factory.HumanIssueCreateCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanIssueCreateDispatch shares the reservation row lock. Call only
// after current admission and only for adapters using this fence for EVERY send.
func (s *Store) CancelHumanIssueCreateDispatch(ctx context.Context, c factory.HumanIssueCreateCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
