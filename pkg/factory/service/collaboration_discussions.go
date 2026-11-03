package service

import (
	"context"
	"errors"
	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrDiscussionQuery       = errors.New("invalid discussion query")
	ErrDiscussionMissing     = errors.New("discussion is not visible")
	ErrDiscussionUnavailable = errors.New("discussion read unavailable")
)

type DiscussionQuery struct {
	Cursor string
	Limit  int
}

func (q DiscussionQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 50 || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) {
		return ErrDiscussionQuery
	}
	return nil
}

type CollaborationDiscussionSource interface {
	ReadIssueComments(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, DiscussionQuery) (collaboration.Page[collaboration.IssueComment], error)
	ReadPullComments(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, DiscussionQuery) (collaboration.Page[collaboration.PullComment], error)
}
type CollaborationDiscussions struct{ Source CollaborationDiscussionSource }

func (s CollaborationDiscussions) IssueComments(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q DiscussionQuery) (collaboration.Page[collaboration.IssueComment], error) {
	var empty collaboration.Page[collaboration.IssueComment]
	if actor < 1 || repo < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrDiscussionQuery
	}
	if s.Source == nil {
		return empty, ErrDiscussionUnavailable
	}
	page, err := s.Source.ReadIssueComments(ctx, actor, repo, number, q)
	if err != nil {
		return empty, err
	}
	return validateDiscussionPage(ctx, page, repo, number, collaboration.SubjectIssue, q, func(item collaboration.IssueComment) (collaboration.SubjectKind, collaboration.RepositoryID, int64, string, collaboration.Comment) {
		return item.Issue.Kind, item.Issue.Repository, item.Issue.Number, item.Issue.ExternalID, item.Comment
	})
}
func (s CollaborationDiscussions) PullComments(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q DiscussionQuery) (collaboration.Page[collaboration.PullComment], error) {
	var empty collaboration.Page[collaboration.PullComment]
	if actor < 1 || repo < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrDiscussionQuery
	}
	if s.Source == nil {
		return empty, ErrDiscussionUnavailable
	}
	page, err := s.Source.ReadPullComments(ctx, actor, repo, number, q)
	if err != nil {
		return empty, err
	}
	return validateDiscussionPage(ctx, page, repo, number, collaboration.SubjectPull, q, func(item collaboration.PullComment) (collaboration.SubjectKind, collaboration.RepositoryID, int64, string, collaboration.Comment) {
		return item.Pull.Kind, item.Pull.Repository, item.Pull.Number, item.Pull.ExternalID, item.Comment
	})
}
func validateDiscussionPage[T any](ctx context.Context, page collaboration.Page[T], repo collaboration.RepositoryID, number int64, kind collaboration.SubjectKind, q DiscussionQuery, record func(T) (collaboration.SubjectKind, collaboration.RepositoryID, int64, string, collaboration.Comment)) (collaboration.Page[T], error) {
	var empty collaboration.Page[T]
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if page.Observation.State != collaboration.ReadAvailable || page.Observation.Revision == "" || page.Items == nil || len(page.Items) > q.Limit || page.Complete != (page.Next == "") {
		return empty, ErrDiscussionUnavailable
	}
	seen := map[string]bool{}
	parent := ""
	for _, item := range page.Items {
		k, r, n, id, comment := record(item)
		if k != kind || r != repo || n != number || id == "" || parent != "" && parent != id || comment.ExternalID == "" || seen[comment.ExternalID] || comment.Observation.State != collaboration.ReadAvailable || comment.Observation.Revision == "" {
			return empty, ErrDiscussionUnavailable
		}
		parent = id
		seen[comment.ExternalID] = true
	}
	return page, nil
}
