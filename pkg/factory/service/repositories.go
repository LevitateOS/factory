package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrRepositoryCatalogueInvalid     = errors.New("invalid repository catalogue query")
	ErrRepositoryCatalogueUnavailable = errors.New("repository catalogue unavailable")
	ErrRepositoryHidden               = errors.New("repository is not visible to this actor")
)

// RepositoryVisibility supplies current authority and display metadata for a
// canonical identity. Only confirmed denial returns ErrRepositoryHidden; unknown
// access must return its error so an outage cannot masquerade as an empty list.
type RepositoryVisibility func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (string, error)

type Repositories struct {
	Store      *store.Store
	Visibility RepositoryVisibility
}

type RepositoryQuery struct {
	After collaboration.RepositoryID
	Limit int
}

type RepositoryChoice struct {
	Repository collaboration.RepositoryID `json:"repository,string"`
	Name       string                     `json:"name"`
}

// Complete describes this registered-ledger traversal, not all repositories on
// a provider. Provider discovery/enrollment belongs to its admitted adapter.
type RepositoryCatalogue struct {
	Items     []RepositoryChoice `json:"items"`
	NextAfter string             `json:"next_after"`
	Complete  bool               `json:"complete"`
}

func (s Repositories) Catalogue(ctx context.Context, actor collaboration.ActorID, query RepositoryQuery) (RepositoryCatalogue, error) {
	if actor < 1 || query.After < 0 || query.Limit < 1 || query.Limit > 100 {
		return RepositoryCatalogue{}, ErrRepositoryCatalogueInvalid
	}
	if s.Store == nil || s.Visibility == nil {
		return RepositoryCatalogue{}, ErrRepositoryCatalogueUnavailable
	}
	refs, err := s.Store.CollaborationRepositoryWindow(ctx, query.After, query.Limit)
	if err != nil {
		return RepositoryCatalogue{}, err
	}
	page := RepositoryCatalogue{Items: make([]RepositoryChoice, 0, query.Limit), Complete: len(refs) <= query.Limit}
	if !page.Complete {
		refs = refs[:query.Limit]
		page.NextAfter = strconv.FormatInt(int64(refs[len(refs)-1].ID), 10)
	}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return RepositoryCatalogue{}, err
		}
		name, err := s.Visibility(ctx, actor, ref.ID)
		if errors.Is(err, ErrRepositoryHidden) {
			continue
		}
		if err != nil {
			return RepositoryCatalogue{}, err
		}
		if !collaboration.ValidText(name, 512) {
			return RepositoryCatalogue{}, ErrRepositoryCatalogueUnavailable
		}
		page.Items = append(page.Items, RepositoryChoice{Repository: ref.ID, Name: name})
	}
	if err := ctx.Err(); err != nil {
		return RepositoryCatalogue{}, err
	}
	return page, nil
}
