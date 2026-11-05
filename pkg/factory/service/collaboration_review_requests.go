package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var (
	ErrReviewRequestReadQuery       = errors.New("invalid reviewer read scope")
	ErrReviewRequestReadMissing     = errors.New("review requests not visible")
	ErrReviewRequestReadUnavailable = errors.New("review request reads unavailable")
)

type CollaborationReviewRequestSource interface {
	ReadReviewRequests(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64) (collaboration.PullReviewRequests, error)
}
type CollaborationReviewRequests struct {
	Source CollaborationReviewRequestSource
}

func (s CollaborationReviewRequests) Read(ctx context.Context, actor collaboration.ActorID, repo collaboration.RepositoryID, number int64) (collaboration.PullReviewRequests, error) {
	var empty collaboration.PullReviewRequests
	if actor < 1 || repo < 1 || number < 1 {
		return empty, ErrReviewRequestReadQuery
	}
	if s.Source == nil {
		return empty, ErrReviewRequestReadUnavailable
	}
	p, err := s.Source.ReadReviewRequests(ctx, actor, repo, number)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if p.Pull.Kind != collaboration.SubjectPull || p.Pull.Repository != repo || p.Pull.Number != number || !collaboration.ValidText(p.Pull.ExternalID, 1024) || p.Observation.State != collaboration.ReadAvailable || p.Observation.Revision == "" || p.Items == nil || len(p.Items) > 1000 {
		return empty, ErrReviewRequestReadUnavailable
	}
	seen, nodes := map[string]bool{}, map[string]bool{}
	hidden := false
	for _, r := range p.Items {
		if !collaboration.ValidText(r.ExternalID, 1024) || seen[r.ExternalID] {
			return empty, ErrReviewRequestReadUnavailable
		}
		seen[r.ExternalID] = true
		if r.Reviewer == nil {
			hidden = true
			continue
		}
		t := r.Reviewer
		if !collaboration.ValidText(t.Label, 512) || t.ExternalID != nil && !collaboration.ValidText(*t.ExternalID, 512) {
			return empty, ErrReviewRequestReadUnavailable
		}
		if t.NodeID == nil {
			hidden = true
			if t.Kind != "other" {
				return empty, ErrReviewRequestReadUnavailable
			}
		} else {
			if !collaboration.ValidText(*t.NodeID, 1024) || nodes[*t.NodeID] {
				return empty, ErrReviewRequestReadUnavailable
			}
			nodes[*t.NodeID] = true
		}
		switch t.Kind {
		case "user", "bot":
			if t.Team != nil || t.ProviderKind != "" || (t.ExternalID == nil) != (t.Actor == nil) || t.Actor != nil && (t.Actor.ID <= 0 || t.Actor.ExternalID != *t.ExternalID || t.Actor.Login != t.Label) {
				return empty, ErrReviewRequestReadUnavailable
			}
		case "team":
			if t.Actor != nil || t.ProviderKind != "" || t.Team == nil || t.Team.ID <= 0 || t.ExternalID == nil || t.Team.ExternalID != *t.ExternalID || t.Team.Name != t.Label {
				return empty, ErrReviewRequestReadUnavailable
			}
		case "other":
			if t.Actor != nil || t.Team != nil || t.ExternalID != nil || !collaboration.ValidText(t.ProviderKind, 128) {
				return empty, ErrReviewRequestReadUnavailable
			}
		default:
			return empty, ErrReviewRequestReadUnavailable
		}
	}
	if p.AllTargetsVisible == hidden {
		return empty, ErrReviewRequestReadUnavailable
	}
	return p, nil
}
