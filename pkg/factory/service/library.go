package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// LibrarySource reads the existing projection/revision journal. It neither
// admits a caller nor mutates knowledge, decisions, tasks or grants.
type LibrarySource interface {
	LibrarySearch(context.Context, int64, string, string, string, bool, bool) (factory.LibraryPage, error)
	LibraryRead(context.Context, int64, string, bool) (factory.LibraryRead, error)
}

type Library struct {
	Source     LibrarySource
	Visibility WorkVisibility
}

var (
	ErrLibraryUnavailable = errors.New("project library is unavailable")
	ErrLibraryVisibility  = errors.New("current project library visibility is required")
	ErrLibraryNotFound    = errors.New("project library item is unavailable")
	ErrLibraryScope       = errors.New("library observation does not match its requested scope")
)

// Browser scope is a string without rewriting retained domain/store JSON.
type LibraryItemView struct {
	factory.LibraryItem
	Repository string `json:"repository"`
}

type LibraryPageView struct {
	Items      []LibraryItemView `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Complete   bool              `json:"complete"`
}

type LibraryRevisionView struct {
	Item   LibraryItemView `json:"item"`
	Reason string          `json:"reason"`
	Actor  string          `json:"actor"`
}

type LibraryReadView struct {
	Item    LibraryItemView       `json:"item"`
	History []LibraryRevisionView `json:"history"`
}

func (s Library) visible(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if actor < 1 || repository < 1 {
		return factory.ErrLibraryQuery
	}
	if s.Source == nil || s.Visibility == nil {
		return ErrLibraryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Visibility(ctx, actor, repository); err != nil {
		return errors.Join(ErrLibraryVisibility, err)
	}
	return ctx.Err()
}

func libraryItemView(item factory.LibraryItem, repository collaboration.RepositoryID) (LibraryItemView, error) {
	if item.Repository != int64(repository) || !factory.ValidID(item.ID) || item.Revision < 1 || (item.Kind != "knowledge" && item.Kind != "decision") {
		return LibraryItemView{}, ErrLibraryScope
	}
	return LibraryItemView{LibraryItem: item, Repository: strconv.FormatInt(int64(repository), 10)}, nil
}

func (s Library) Search(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, query factory.LibraryQuery) (LibraryPageView, error) {
	var empty LibraryPageView
	if err := query.Validate(int64(repository)); err != nil {
		return empty, err
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	page, err := s.Source.LibrarySearch(ctx, int64(repository), query.Query, query.Kind, query.Cursor, query.History, query.CurrentOnly)
	if err != nil {
		return empty, err
	}
	if len(page.Items) > 50 || (page.Complete && page.NextCursor != "") || (!page.Complete && (len(page.Items) == 0 || page.NextCursor != page.Items[len(page.Items)-1].ID)) {
		return empty, ErrLibraryScope
	}
	out := LibraryPageView{Items: []LibraryItemView{}, NextCursor: page.NextCursor, Complete: page.Complete}
	previous := query.Cursor
	for _, item := range page.Items {
		if item.ID <= previous || (query.Kind != "" && item.Kind != query.Kind) || (!query.History && item.Removed) || (query.CurrentOnly && (item.Removed || item.State == "superseded" || item.State == "retracted")) {
			return empty, ErrLibraryScope
		}
		view, err := libraryItemView(item, repository)
		if err != nil {
			return empty, err
		}
		out.Items = append(out.Items, view)
		previous = item.ID
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	return out, nil
}

func (s Library) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, id string, history bool) (LibraryReadView, error) {
	var empty LibraryReadView
	if !factory.ValidID(id) {
		return empty, ErrLibraryNotFound
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	recorded, err := s.Source.LibraryRead(ctx, int64(repository), id, history)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrLibraryNotFound
	}
	if err != nil {
		return empty, err
	}
	if recorded.Item.ID != id || (!history && recorded.Item.Removed) || len(recorded.History) > 20 {
		return empty, ErrLibraryScope
	}
	item, err := libraryItemView(recorded.Item, repository)
	if err != nil {
		return empty, err
	}
	out := LibraryReadView{Item: item, History: []LibraryRevisionView{}}
	previous := int64(0)
	for index, revision := range recorded.History {
		if revision.Item.ID != id || revision.Item.Revision > recorded.Item.Revision || (index > 0 && revision.Item.Revision >= previous) {
			return empty, ErrLibraryScope
		}
		view, err := libraryItemView(revision.Item, repository)
		if err != nil {
			return empty, err
		}
		out.History = append(out.History, LibraryRevisionView{Item: view, Reason: revision.Reason, Actor: revision.Actor})
		previous = revision.Item.Revision
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	return out, nil
}
