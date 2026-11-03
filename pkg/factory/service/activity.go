package service

import (
	"context"
	"errors"
	"math"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// ActivityNavigation is current, host-owned navigation for admitted activity.
// The adapter validates its URLs; the store never assumes provider URL shapes.
type ActivityNavigation struct {
	RepositoryPath string
	ItemURL        func(factory.ActivityItem) string
}

type ActivityVisibility func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (ActivityNavigation, error)

type Activity struct {
	Store       *store.Store
	Visibility  ActivityVisibility
	WriteAccess RequestWriteAccess
}

var (
	ErrActivityUnavailable = errors.New("repository activity is unavailable")
	ErrActivityInvalid     = errors.New("invalid activity settings scope or revision")
	ErrActivityVisibility  = errors.New("repository activity is not visible")
	ErrActivityWriteAccess = errors.New("repository activity administration is required")
	ErrActivityNavigation  = errors.New("repository activity navigation is unavailable")
)

func (s Activity) navigation(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (ActivityNavigation, error) {
	var empty ActivityNavigation
	if actor < 1 || repository < 1 {
		return empty, ErrActivityInvalid
	}
	if s.Store == nil || s.Visibility == nil {
		return empty, ErrActivityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	links, err := s.Visibility(ctx, actor, repository)
	if err != nil {
		return empty, errors.Join(ErrActivityVisibility, err)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if links.RepositoryPath == "" || links.ItemURL == nil {
		return empty, ErrActivityNavigation
	}
	return links, nil
}

func (s Activity) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (factory.RepositoryActivity, error) {
	var empty factory.RepositoryActivity
	if _, err := s.navigation(ctx, actor, repository); err != nil {
		return empty, err
	}
	view, err := s.Store.RepositoryActivity(ctx, int64(repository))
	if err != nil {
		return empty, err
	}
	// Refresh authority and mutable navigation after assembling stored facts.
	links, err := s.navigation(ctx, actor, repository)
	if err != nil {
		return empty, err
	}
	view.RepositoryPath = links.RepositoryPath
	for i := range view.Items {
		view.Items[i].URL = links.ItemURL(view.Items[i])
		if view.Items[i].URL == "" {
			return empty, ErrActivityNavigation
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return view, nil
}

func (s Activity) Update(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, expected int64, settings factory.ActivitySettings) (factory.RepositoryActivity, error) {
	var empty factory.RepositoryActivity
	if expected < 0 || expected == math.MaxInt64 {
		return empty, ErrActivityInvalid
	}
	if _, err := s.navigation(ctx, actor, repository); err != nil {
		return empty, err
	}
	if s.WriteAccess == nil {
		return empty, ErrActivityUnavailable
	}
	if err := s.WriteAccess(ctx, actor, int64(repository)); err != nil {
		return empty, errors.Join(ErrActivityWriteAccess, err)
	}
	if err := s.Store.SetActivitySettings(ctx, int64(repository), int64(actor), expected, settings); err != nil {
		return empty, err
	}
	return s.Read(ctx, actor, repository)
}
