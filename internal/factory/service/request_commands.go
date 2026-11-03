package service

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// RequestWriteAccess checks current repository authority for this admitted
// canonical actor. Saved ownership or an earlier successful command is not a
// replacement for this check, including on immutable revision replay.
type RequestWriteAccess func(context.Context, collaboration.ActorID, int64) error

var (
	ErrRequestWriteUnavailable = errors.New("request write authorization is unavailable")
	ErrRequestWriteAccess      = errors.New("request repository authorization failed")
	ErrRequestInputInvalid     = errors.New("invalid build request")
	ErrDepartmentInputInvalid  = errors.New("invalid department")
)

func (s Requests) authorizeWrite(ctx context.Context, actor collaboration.ActorID, repository int64) error {
	if s.WriteAccess == nil {
		return ErrRequestWriteUnavailable
	}
	if err := s.WriteAccess(ctx, actor, repository); err != nil {
		return errors.Join(ErrRequestWriteAccess, err)
	}
	return nil
}

// SaveRevision binds ownership and creation time server-side. Its existing
// store transaction owns immutable identity replay, head CAS, department
// ownership and the accepted request's pinned repository.
func (s Requests) SaveRevision(ctx context.Context, actor collaboration.ActorID, in factory.IntentRevision) (factory.IntentRevision, error) {
	var empty factory.IntentRevision
	if actor <= 0 {
		return empty, ErrRequestNotFound
	}
	if s.Store == nil {
		return empty, ErrRequestUnavailable
	}
	in.Owner, in.CreatedUnix = int64(actor), time.Now().Unix()
	current, readErr := s.Store.IntentRevision(ctx, in.Intent, int64(actor))
	if readErr != nil && !errors.Is(readErr, store.ErrNotFound) {
		return empty, readErr
	}
	if readErr == nil && current.ID == in.ID {
		in.CreatedUnix = current.CreatedUnix
	}
	if in.Repository > 0 {
		if err := s.authorizeWrite(ctx, actor, in.Repository); err != nil {
			return empty, err
		}
	}
	if err := in.Validate(); err != nil {
		return empty, errors.Join(ErrRequestInputInvalid, err)
	}
	if in.Predecessor != "" {
		if errors.Is(readErr, store.ErrNotFound) {
			return empty, ErrRequestNotFound
		}
		if current.ID != in.Predecessor && current.ID != in.ID {
			return empty, store.ErrStaleRevision
		}
	}
	if err := s.Store.SaveIntentRevision(ctx, in); err != nil {
		return empty, err
	}
	return in, nil
}

func (s Requests) CreateDepartment(ctx context.Context, actor collaboration.ActorID, in factory.Department) (factory.Department, error) {
	var empty factory.Department
	if actor <= 0 {
		return empty, ErrRequestNotFound
	}
	if s.Store == nil {
		return empty, ErrRequestUnavailable
	}
	in.Owner = int64(actor)
	if err := in.Validate(); err != nil {
		return empty, errors.Join(ErrDepartmentInputInvalid, err)
	}
	if err := s.Store.CreateDepartment(ctx, in); err != nil {
		return empty, err
	}
	return in, nil
}

func (s Requests) Departments(ctx context.Context, actor collaboration.ActorID) ([]factory.Department, error) {
	if actor <= 0 {
		return nil, ErrRequestNotFound
	}
	if s.Store == nil {
		return nil, ErrRequestUnavailable
	}
	return s.Store.Departments(ctx, int64(actor))
}

func (s Requests) Catalogue(ctx context.Context, actor collaboration.ActorID, filter factory.IntentListFilter) ([]factory.IntentSummary, string, error) {
	if actor <= 0 {
		return nil, "", ErrRequestNotFound
	}
	if s.Store == nil {
		return nil, "", ErrRequestUnavailable
	}
	if err := filter.Validate(); err != nil {
		return nil, "", errors.Join(ErrRequestInputInvalid, err)
	}
	return s.Store.ListIntentRevisions(ctx, int64(actor), filter)
}

func (s Requests) History(ctx context.Context, actor collaboration.ActorID, intent string, limit int) ([]factory.IntentRevision, error) {
	if _, err := s.Revision(ctx, actor, intent); err != nil {
		return nil, err
	}
	return s.Store.IntentHistory(ctx, intent, int64(actor), limit)
}
