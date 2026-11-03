package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrRepositoryCatalogQuery       = errors.New("invalid repository catalogue query")
	ErrRepositoryCatalogMissing     = errors.New("repository catalogue account is not visible")
	ErrRepositoryCatalogUnavailable = errors.New("repository catalogue unavailable")
	ErrRepositoryCatalogChanged     = errors.New("repository catalogue changed during read")
)

type RepositoryCatalogQuery struct {
	Query, Cursor string
	Limit         int
}

func (q RepositoryCatalogQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 50 || q.Query != "" && !collaboration.ValidText(q.Query, 256) || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) {
		return ErrRepositoryCatalogQuery
	}
	return nil
}

type CollaborationRepositorySource interface {
	ReadRepositories(context.Context, collaboration.ActorID, RepositoryCatalogQuery) (collaboration.Page[collaboration.RepositoryRef], error)
}
type CollaborationRepositories struct{ Source CollaborationRepositorySource }

func (s CollaborationRepositories) Read(ctx context.Context, host collaboration.HostID, actor collaboration.ActorID, q RepositoryCatalogQuery) (collaboration.Page[collaboration.RepositoryRef], error) {
	var empty collaboration.Page[collaboration.RepositoryRef]
	if !collaboration.ValidID(string(host)) || actor < 1 || q.Validate() != nil {
		return empty, ErrRepositoryCatalogQuery
	}
	if s.Source == nil {
		return empty, ErrRepositoryCatalogUnavailable
	}
	p, err := s.Source.ReadRepositories(ctx, actor, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if p.Observation.State != collaboration.ReadAvailable || p.Observation.Revision == "" || p.Items == nil || len(p.Items) > q.Limit || p.Complete != (p.Next == "") || p.Next != "" && !collaboration.ValidText(p.Next, 4096) {
		return empty, ErrRepositoryCatalogUnavailable
	}
	seen := map[collaboration.RepositoryID]bool{}
	external := map[string]bool{}
	for _, r := range p.Items {
		if r.ID < 1 || seen[r.ID] || r.Host != host || !collaboration.ValidText(r.ExternalID, 1024) || external[r.ExternalID] || !collaboration.ValidText(r.Name, 512) {
			return empty, ErrRepositoryCatalogUnavailable
		}
		seen[r.ID], external[r.ExternalID] = true, true
	}
	return p, nil
}
