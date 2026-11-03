package service

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
)

var (
	ErrCollaborationPullQuery       = errors.New("invalid collaboration pull query")
	ErrCollaborationPullMissing     = errors.New("collaboration pull is not visible")
	ErrCollaborationPullUnavailable = errors.New("collaboration pull reader unavailable")
)

// State selects open (default), closed or all. Number selects any-state detail.
// Cursors are provider facts, scoped by the explicit repository, never identity.
type CollaborationPullQuery struct {
	Cursor string
	Number int64
	Limit  int
	State  string
}

func (q CollaborationPullQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 50 || q.Number < 0 || q.Number > 0 && (q.Cursor != "" || q.Limit != 1 || q.State != "") || q.State != "" && q.State != "open" && q.State != "closed" && q.State != "all" || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) {
		return ErrCollaborationPullQuery
	}
	return nil
}

type CollaborationPullSource interface {
	ReadPulls(context.Context, collaboration.ActorID, collaboration.RepositoryID, CollaborationPullQuery) (collaboration.Page[collaboration.PullRequest], error)
}

type CollaborationPulls struct{ Source CollaborationPullSource }

func (s CollaborationPulls) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, query CollaborationPullQuery) (collaboration.Page[collaboration.PullRequest], error) {
	var empty collaboration.Page[collaboration.PullRequest]
	if actor < 1 || repository < 1 || query.Validate() != nil {
		return empty, ErrCollaborationPullQuery
	}
	if s.Source == nil {
		return empty, ErrCollaborationPullUnavailable
	}
	page, err := s.Source.ReadPulls(ctx, actor, repository, query)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if page.Observation.State != collaboration.ReadAvailable || page.Observation.Revision == "" || page.Items == nil || len(page.Items) > query.Limit || page.Complete != (page.Next == "") {
		return empty, ErrCollaborationPullUnavailable
	}
	seen := map[int64]bool{}
	external := map[string]bool{}
	for _, pull := range page.Items {
		if pull.Ref.Kind != collaboration.SubjectPull || pull.Ref.Repository != repository || pull.Ref.ExternalID == "" || external[pull.Ref.ExternalID] || pull.Ref.Number < 1 || seen[pull.Ref.Number] || pull.Observation.State != collaboration.ReadAvailable || pull.Observation.Revision == "" || pull.State != "open" && pull.State != "closed" && pull.State != "merged" || query.Number > 0 && pull.Ref.Number != query.Number {
			return empty, ErrCollaborationPullUnavailable
		}
		seen[pull.Ref.Number] = true
		external[pull.Ref.ExternalID] = true
		if query.Number == 0 && query.State != "all" {
			state := query.State
			if state == "" {
				state = "open"
			}
			if state == "open" && pull.State != "open" || state == "closed" && pull.State == "open" {
				return empty, ErrCollaborationPullUnavailable
			}
		}
	}
	if query.Number > 0 && len(page.Items) != 1 {
		return empty, ErrCollaborationPullMissing
	}
	return page, nil
}
