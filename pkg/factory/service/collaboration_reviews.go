package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrReviewQuery       = errors.New("invalid review query")
	ErrReviewMissing     = errors.New("review is not visible")
	ErrReviewUnavailable = errors.New("review reads unavailable")
)

type ReviewQuery struct {
	Cursor   string
	ReviewID string
	Limit    int
}

func (q ReviewQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 50 || q.Cursor != "" && !collaboration.ValidText(q.Cursor, 4096) || q.ReviewID != "" && (!collaboration.ValidText(q.ReviewID, 1024) || q.Cursor != "" || q.Limit != 1) {
		return ErrReviewQuery
	}
	return nil
}

type CollaborationReviewSource interface {
	ReadReviews(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, ReviewQuery) (collaboration.Page[collaboration.Review], error)
}
type CollaborationReviews struct{ Source CollaborationReviewSource }

func (s CollaborationReviews) Read(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64, q ReviewQuery) (collaboration.Page[collaboration.Review], error) {
	var empty collaboration.Page[collaboration.Review]
	if actor < 1 || repo < 1 || number < 1 || q.Validate() != nil {
		return empty, ErrReviewQuery
	}
	if s.Source == nil {
		return empty, ErrReviewUnavailable
	}
	page, err := s.Source.ReadReviews(ctx, actor, repo, number, q)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if page.Items == nil || len(page.Items) > q.Limit || page.Complete != (page.Next == "") || page.Next != "" && (len(page.Items) == 0 || !collaboration.ValidText(page.Next, 4096)) || page.Observation.State != collaboration.ReadAvailable || page.Observation.Revision == "" || q.ReviewID != "" && page.Next != "" {
		return empty, ErrReviewUnavailable
	}
	seen := map[string]bool{}
	parent := ""
	for _, r := range page.Items {
		if r.ExternalID == "" || seen[r.ExternalID] || q.ReviewID != "" && r.ExternalID != q.ReviewID || r.Pull.Kind != collaboration.SubjectPull || r.Pull.Repository != repo || r.Pull.Number != number || r.Pull.ExternalID == "" || parent != "" && parent != r.Pull.ExternalID || r.Observation.State != collaboration.ReadAvailable || r.Observation.Revision == "" || r.Commit != "" && !collaboration.ValidCommitID(r.Commit) || r.State == "pending" && r.SubmittedAt != nil {
			return empty, ErrReviewUnavailable
		}
		seen[r.ExternalID] = true
		parent = r.Pull.ExternalID
		switch r.State {
		case "pending", "approved", "changes_requested", "commented", "dismissed", "unknown":
		default:
			return empty, ErrReviewUnavailable
		}
	}
	if q.ReviewID != "" && len(page.Items) != 1 {
		return empty, ErrReviewMissing
	}
	return page, nil
}
