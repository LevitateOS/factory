package store

import (
	"context"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// HumanCommentDispatchState reads the exact capture's local fence. It proves
// neither remote absence nor current authority. Absence is ErrNotFound.
func (s *Store) HumanCommentDispatchState(ctx context.Context, c factory.HumanCommentCapture) (factory.CommandDispatchState, error) {
	cmd, err := c.Command()
	if err != nil {
		return "", err
	}
	return s.humanDispatchState(ctx, cmd)
}

// ReserveHumanCommentDispatch commits before network use. Only true permits one
// attempt; a reserved read never recovers permission after a lost commit reply.
func (s *Store) ReserveHumanCommentDispatch(ctx context.Context, c factory.HumanCommentCapture) (bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return false, err
	}
	return s.reserveHumanDispatch(ctx, cmd)
}

// CancelHumanCommentDispatch shares reservation's row lock. Call only after
// current admission, for adapters using this fence for EVERY send. It does not
// cancel a remote idempotent provider journal or erase possibly sent effects.
func (s *Store) CancelHumanCommentDispatch(ctx context.Context, c factory.HumanCommentCapture) (collaboration.OperationLookup, error) {
	cmd, err := c.Command()
	if err != nil {
		return collaboration.OperationLookup{}, err
	}
	return s.cancelHumanDispatch(ctx, cmd)
}
