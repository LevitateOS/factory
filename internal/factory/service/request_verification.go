package service

import (
	"context"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// VerificationAllowance reads only the admitted request owner's allowance.
// Bound reads retain the same current repository administration requirement as
// increases. Neither stored ownership nor a saved grant replaces this check.
func (s Requests) VerificationAllowance(ctx context.Context, actor collaboration.ActorID, intent string) (factory.AppVerificationAllowance, error) {
	var empty factory.AppVerificationAllowance
	if actor < 1 {
		return empty, store.ErrNotFound
	}
	if s.Store == nil {
		return empty, ErrRequestUnavailable
	}
	view, err := s.Store.AppVerificationAllowance(ctx, intent, int64(actor), time.Now())
	if err != nil {
		return empty, err
	}
	if view.Repository > 0 {
		if err := s.authorizeWrite(ctx, actor, view.Repository); err != nil {
			return empty, err
		}
	}
	return view, nil
}

// IncreaseVerificationAllowance uses the existing command journal/CAS. The
// admitted principal is audit attribution supplied by the host separately from
// canonical ownership; it is never derived from a browser command body.
func (s Requests) IncreaseVerificationAllowance(ctx context.Context, actor collaboration.ActorID, intent, principal string, change factory.AppVerificationAllowanceChange) (factory.AppVerificationAllowance, error) {
	var empty factory.AppVerificationAllowance
	if actor < 1 {
		return empty, store.ErrNotFound
	}
	if s.Store == nil {
		return empty, ErrRequestUnavailable
	}
	repository, err := s.Store.AppVerificationAllowanceScope(ctx, intent, int64(actor))
	if err != nil {
		return empty, err
	}
	if repository > 0 {
		if err := s.authorizeWrite(ctx, actor, repository); err != nil {
			return empty, err
		}
	}
	return s.Store.IncreaseAppVerificationAllowance(ctx, intent, int64(actor), repository, principal, change, time.Now())
}
