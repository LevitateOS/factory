package service

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrPullCommitsQuery       = errors.New("invalid pull commits query")
	ErrPullCommitsMissing     = errors.New("pull commits are not visible")
	ErrPullCommitsUnavailable = errors.New("pull commits unavailable")
	ErrPullCommitsChanged     = errors.New("pull commits changed")
)

type PullCommitsQuery struct {
	Head   collaboration.CommitID
	Cursor string
	Limit  int
}

func (q PullCommitsQuery) Validate() error {
	if !collaboration.ValidCommitID(q.Head) || q.Limit < 1 || q.Limit > 20 || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) {
		return ErrPullCommitsQuery
	}
	return nil
}

type CollaborationPullCommitsSource interface {
	ReadPullCommits(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, PullCommitsQuery) (collaboration.PullCommits, error)
}
type CollaborationPullCommits struct {
	Source CollaborationPullCommitsSource
}

func (s CollaborationPullCommits) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, number int64, q PullCommitsQuery) (collaboration.PullCommits, error) {
	var empty collaboration.PullCommits
	if actor < 1 || repository < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrPullCommitsQuery
	}
	if s.Source == nil {
		return empty, ErrPullCommitsUnavailable
	}
	result, err := s.Source.ReadPullCommits(ctx, actor, repository, number, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	page := result.Commits
	if result.Pull.Kind != collaboration.SubjectPull || result.Pull.Repository != repository || result.Pull.Number != number || result.Pull.ExternalID == "" || result.Head != q.Head || page.Items == nil || len(page.Items) > q.Limit || page.Complete != (page.Next == "") || page.Next != "" && (len(page.Items) == 0 || !collaboration.ValidText(page.Next, 4096)) || page.Observation.State != collaboration.ReadAvailable || page.Observation.Revision == "" {
		return empty, ErrPullCommitsUnavailable
	}
	seen := map[collaboration.CommitID]bool{}
	for _, item := range page.Items {
		if !collaboration.ValidCommitID(item.Commit) || seen[item.Commit] || len(item.Summary) > 4096 || !utf8.ValidString(item.Summary) || len(item.Author) > 1024 || !utf8.ValidString(item.Author) || item.AuthoredAt.IsZero() {
			return empty, ErrPullCommitsUnavailable
		}
		seen[item.Commit] = true
	}
	return result, nil
}
