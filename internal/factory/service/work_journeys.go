package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

type WorkVisibility func(context.Context, collaboration.ActorID, collaboration.RepositoryID) error

type WorkJourneys struct {
	Store      *store.Store
	Visibility WorkVisibility
}

var (
	ErrWorkJourneyInvalid     = errors.New("invalid issue-work journey scope")
	ErrWorkJourneyUnavailable = errors.New("issue-work journey is unavailable")
	ErrWorkJourneyVisibility  = errors.New("issue-work journey is not visible")
	ErrWorkJourneyScope       = errors.New("issue-work journey record conflicts with its scope")
)

// Read resolves its recorded owner/publication itself. Callers supply current
// admission, never a browser-selected assignment or claimed delivery evidence.
// No model, provider-effect or execution port is available to this read.
func (s WorkJourneys) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64) (*IssueJourney, error) {
	if actor < 1 || repository < 1 || issue < 1 {
		return nil, ErrWorkJourneyInvalid
	}
	if s.Store == nil || s.Visibility == nil {
		return nil, ErrWorkJourneyUnavailable
	}
	visible := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Visibility(ctx, actor, repository); err != nil {
			return errors.Join(ErrWorkJourneyVisibility, err)
		}
		return ctx.Err()
	}
	if err := visible(); err != nil {
		return nil, err
	}
	var owner *factory.Assignment
	if recorded, err := s.Store.LatestIssueAssignment(ctx, int64(repository), issue); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue {
			return nil, ErrWorkJourneyScope
		}
		owner = &recorded
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var publication *factory.Publication
	if owner != nil {
		if recorded, err := s.Store.PublicationByAssignment(ctx, owner.ID); err == nil {
			if recorded.Repository != int64(repository) || recorded.Issue != issue || recorded.AssignmentID != owner.ID {
				return nil, ErrWorkJourneyScope
			}
			publication = &recorded
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	view, err := s.assemble(ctx, int64(repository), issue, strconv.FormatInt(issue, 10), owner, publication, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	if err := visible(); err != nil {
		return nil, err
	}
	return view, nil
}
