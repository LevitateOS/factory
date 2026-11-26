package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/store"
)

var ErrProjectSummariesUnavailable = errors.New("project summaries unavailable")

// ProjectScope admits one actor's retained project list and names the profile
// whose connections' opened repositories it may read. It is not repository
// visibility: rows are registry facts as of the read, never current access,
// and admission must not observe each repository at the provider.
type ProjectScope func(context.Context, collaboration.ActorID) (collaboration.ProfileID, error)

type ProjectSummaries struct {
	Store *store.Store
	Scope ProjectScope
}

// ProjectList is every row as of one read time, in Unix seconds.
type ProjectList struct {
	Projects []store.ProjectSummary `json:"projects"`
	AsOf     int64                  `json:"as_of"`
}

func (s ProjectSummaries) List(ctx context.Context, actor collaboration.ActorID) (ProjectList, error) {
	if actor < 1 || s.Store == nil || s.Scope == nil {
		return ProjectList{}, ErrProjectSummariesUnavailable
	}
	profile, err := s.Scope(ctx, actor)
	if err != nil {
		return ProjectList{}, err
	}
	rows, asOf, err := s.Store.ProjectSummaries(ctx, profile, store.ProjectSummaryLimit)
	if err != nil {
		return ProjectList{}, err
	}
	return ProjectList{Projects: rows, AsOf: asOf.Unix()}, ctx.Err()
}

// ErrProjectNotListed reports a removal for a repository the profile's list
// does not hold.
var ErrProjectNotListed = errors.New("project is not listed")

// Remove takes a repository out of the actor's profile list. It is a local
// list edit, not a provider action, and grants or withdraws nothing.
func (s ProjectSummaries) Remove(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if actor < 1 || repository < 1 || s.Store == nil || s.Scope == nil {
		return ErrProjectSummariesUnavailable
	}
	profile, err := s.Scope(ctx, actor)
	if err != nil {
		return err
	}
	removed, err := s.Store.RemoveProfileProject(ctx, profile, repository)
	if err != nil {
		return err
	}
	if !removed {
		return ErrProjectNotListed
	}
	return ctx.Err()
}
