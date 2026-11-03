package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrCollaborationIssueQuery       = errors.New("invalid collaboration issue query")
	ErrCollaborationIssueMissing     = errors.New("collaboration issue is not visible")
	ErrCollaborationIssueUnavailable = errors.New("collaboration issue reader unavailable")
)

// State selects open (default), closed or all. Number selects any-state detail.
// Cursors are provider facts, scoped by the explicit repository, never identity.
type CollaborationIssueQuery struct {
	Cursor string
	Number int64
	Limit  int
	State  string
}

func (q CollaborationIssueQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 50 || q.Number < 0 || q.Number > 0 && (q.Cursor != "" || q.Limit != 1 || q.State != "") || q.State != "" && q.State != "open" && q.State != "closed" && q.State != "all" || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) {
		return ErrCollaborationIssueQuery
	}
	return nil
}

type CollaborationIssueSource interface {
	ReadIssues(context.Context, collaboration.ActorID, collaboration.RepositoryID, CollaborationIssueQuery) (collaboration.Page[collaboration.Issue], error)
}

type CollaborationIssues struct{ Source CollaborationIssueSource }

func (s CollaborationIssues) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, query CollaborationIssueQuery) (collaboration.Page[collaboration.Issue], error) {
	var empty collaboration.Page[collaboration.Issue]
	if actor < 1 || repository < 1 || query.Validate() != nil {
		return empty, ErrCollaborationIssueQuery
	}
	if s.Source == nil {
		return empty, ErrCollaborationIssueUnavailable
	}
	page, err := s.Source.ReadIssues(ctx, actor, repository, query)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if page.Observation.State != collaboration.ReadAvailable || page.Observation.Revision == "" || page.Items == nil || len(page.Items) > query.Limit || page.Complete != (page.Next == "") {
		return empty, ErrCollaborationIssueUnavailable
	}
	seen := map[int64]bool{}
	for _, issue := range page.Items {
		if issue.Ref.Kind != collaboration.SubjectIssue || issue.Ref.Repository != repository || issue.Ref.ExternalID == "" || issue.Ref.Number < 1 || seen[issue.Ref.Number] || issue.Observation.State != collaboration.ReadAvailable || issue.Observation.Revision == "" || issue.State != "open" && issue.State != "closed" || query.Number > 0 && issue.Ref.Number != query.Number {
			return empty, ErrCollaborationIssueUnavailable
		}
		seen[issue.Ref.Number] = true
		if query.Number == 0 && query.State != "all" {
			state := query.State
			if state == "" {
				state = "open"
			}
			if issue.State != state {
				return empty, ErrCollaborationIssueUnavailable
			}
		}
	}
	if query.Number > 0 && len(page.Items) != 1 {
		return empty, ErrCollaborationIssueMissing
	}
	return page, nil
}
