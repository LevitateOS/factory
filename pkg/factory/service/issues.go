package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// IssueStatusReader brackets current provider acceptance evidence. It reads
// only; it cannot infer acceptance or grant authority from a browser command.
type IssueStatusReader func(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64) (control.AcceptanceValidity, error)

// IssueNavigation supplies validated host URLs for the recorded issue/PR.
// Missing navigation leaves links absent rather than guessing provider paths.
type IssueNavigation func(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64, int64) (*IssueLinks, error)

type Issues struct {
	Store      *store.Store
	Visibility WorkVisibility
	Status     IssueStatusReader
	Authority  WorkControlAccess
	Membership WorkRuntimeMembership
	Navigation IssueNavigation
}

var (
	ErrIssueUnavailable = errors.New("issue-work read service is unavailable")
	ErrIssueVisibility  = errors.New("issue-work is not visible")
	ErrIssueStatus      = errors.New("current issue acceptance evidence is unavailable")
)

// Read assembles the complete work response around one recorded owner and
// publication. No provider mutation, judgment, funding or dispatch is performed.
func (s Issues) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64) (*IssueView, error) {
	if actor < 1 || repository < 1 || issue < 1 {
		return nil, ErrWorkJourneyInvalid
	}
	if s.Store == nil || s.Visibility == nil || s.Status == nil || s.Authority == nil || s.Membership == nil {
		return nil, ErrIssueUnavailable
	}
	visible := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Visibility(ctx, actor, repository); err != nil {
			return errors.Join(ErrIssueVisibility, err)
		}
		return ctx.Err()
	}
	if err := visible(); err != nil {
		return nil, err
	}
	status, err := s.Status(ctx, actor, repository, issue)
	if err != nil {
		return nil, errors.Join(ErrIssueStatus, err)
	}
	index := strconv.FormatInt(issue, 10)
	if status.Acceptance != nil && (status.Acceptance.Repository != int64(repository) || status.Acceptance.IssueIndex != index) {
		return nil, ErrWorkJourneyScope
	}
	view := &IssueView{Status: status, Repository: strconv.FormatInt(int64(repository), 10), Issue: index}
	acceptance := ""
	if recorded, err := s.Store.IssueControl(ctx, int64(repository), issue); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue {
			return nil, ErrWorkJourneyScope
		}
		acceptance = recorded.Acceptance
		view.Readiness = &readinessView{Blockers: recorded.Blockers, Acceptance: recorded.Acceptance, Readiness: recorded.Readiness, Reason: recorded.Reason, Revision: recorded.Revision, Assessed: recorded.AssessedUnix}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	var merged *factory.Merge
	if recorded, err := s.Store.MergeForIssue(ctx, int64(repository), issue); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue {
			return nil, ErrWorkJourneyScope
		}
		merged = &recorded
		view.Merge = mergeViewDTO(recorded)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var owner *factory.Assignment
	if recorded, err := s.Store.LatestIssueAssignment(ctx, int64(repository), issue); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue {
			return nil, ErrWorkJourneyScope
		}
		owner = &recorded
		view.Attempt = workAttemptDTO(recorded)
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
			view.Publication = workPublicationDTO(recorded)
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	// Check assessments are keyed by repository/PR, not issue number.
	pr, prID := int64(0), int64(0)
	if publication != nil {
		pr, prID = publication.PRNumber, publication.PRID
	} else if merged != nil {
		pr, prID = merged.PRNumber, merged.PRID
	}
	if pr > 0 {
		if recorded, err := s.Store.CheckAssessment(ctx, int64(repository), pr); err == nil {
			if recorded.Repository != int64(repository) || recorded.PRNumber != pr || prID > 0 && recorded.PRID != prID {
				return nil, ErrWorkJourneyScope
			}
			view.Checks = checksViewDTO(recorded)
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	applyWorkStaleness(view, publication, merged)
	view.Journey, err = (WorkJourneys{Store: s.Store}).assemble(ctx, int64(repository), issue, index, owner, publication, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	view.Closure = workClosureDTO(publication, merged)
	controls, err := (WorkControls{Store: s.Store, Authority: s.Authority, Membership: s.Membership}).project(ctx, actor, repository, issue, owner, acceptance)
	if err != nil {
		return nil, err
	}
	view.Capabilities, view.Requested = controls.Capabilities, controls.Requested
	if s.Navigation != nil {
		view.Links, err = s.Navigation(ctx, actor, repository, issue, pr)
		if err != nil {
			return nil, err
		}
	}
	if err := visible(); err != nil {
		return nil, err
	}
	view.ObservedUnix = time.Now().Unix()
	return view, nil
}
