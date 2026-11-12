package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

type WorkReads struct {
	Store      *store.Store
	Visibility WorkVisibility
}

var (
	ErrWorkReadUnavailable = errors.New("recorded work reads are unavailable")
	ErrWorkReadVisibility  = errors.New("recorded work is not visible")
	ErrWorkReadQuery       = errors.New("invalid recorded work query")
)

func (s WorkReads) visible(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64) error {
	if actor < 1 || repository < 1 || issue < 1 {
		return ErrWorkReadQuery
	}
	if s.Store == nil || s.Visibility == nil {
		return ErrWorkReadUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Visibility(ctx, actor, repository); err != nil {
		return errors.Join(ErrWorkReadVisibility, err)
	}
	return ctx.Err()
}

// Assignment returns exact recorded inputs/results and held capacity after
// current visibility admission. Stored prompt bytes never select credentials.
func (s WorkReads) Assignment(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64) (*AssignmentView, error) {
	if err := s.visible(ctx, actor, repository, issue); err != nil {
		return nil, err
	}
	current, err := s.Store.LatestIssueAssignment(ctx, int64(repository), issue)
	if err != nil {
		return nil, err
	}
	if current.Repository != int64(repository) || current.Issue != issue {
		return nil, ErrWorkJourneyScope
	}
	var reservation *factory.Reservation
	if held, err := s.Store.Reservation(ctx, current.ID); err == nil {
		if held.Repository != int64(repository) || held.AssignmentID != current.ID {
			return nil, ErrWorkJourneyScope
		}
		reservation = &held
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var publication *factory.Publication
	if recorded, err := s.Store.PublicationByAssignment(ctx, current.ID); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue || recorded.AssignmentID != current.ID {
			return nil, ErrWorkJourneyScope
		}
		publication = &recorded
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	view := assignmentDTO(current, reservation, publication)
	if err := s.visible(ctx, actor, repository, issue); err != nil {
		return nil, err
	}
	return &view, nil
}

// Priority reads existing journal comparisons, never a new comparison. Foreign
// candidates remain counted but their identities/selections are not exposed.
func (s WorkReads) Priority(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64, cursor store.PriorityComparisonCursor, limit int) (*IssuePriorityView, error) {
	if limit < 1 || limit > store.MaxPriorityComparisons || cursor.BeforeUnix < 0 || (cursor.BeforeUnix != 0 || cursor.BeforeSnapshot != "") && (cursor.BeforeUnix <= 0 || !factory.ValidDigest(cursor.BeforeSnapshot)) {
		return nil, ErrWorkReadQuery
	}
	if err := s.visible(ctx, actor, repository, issue); err != nil {
		return nil, err
	}
	view, err := s.priority(ctx, repository, issue, cursor, limit)
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, actor, repository, issue); err != nil {
		return nil, err
	}
	return view, nil
}

func (s WorkReads) priority(ctx context.Context, repository collaboration.RepositoryID, issue int64, cursor store.PriorityComparisonCursor, limit int) (*IssuePriorityView, error) {
	receipts, more, err := s.Store.PriorityReceiptsForIssue(ctx, int64(repository), issue, cursor, limit)
	if err != nil {
		return nil, err
	}
	observed := time.Now().Unix()
	view := &IssuePriorityView{Comparisons: []priorityComparisonView{}, Repository: strconv.FormatInt(int64(repository), 10), Issue: strconv.FormatInt(issue, 10), ObservedUnix: observed}
	for _, receipt := range receipts {
		view.Comparisons = append(view.Comparisons, priorityComparisonDTO(receipt, int64(repository), observed))
	}
	if more && len(receipts) > 0 {
		trailing := receipts[len(receipts)-1]
		view.NextBeforeUnix, view.NextBeforeSnapshot = trailing.CreatedUnix, trailing.Snapshot
	}
	return view, nil
}

// Progress projects only retained plan/readiness facts under one current repository
// visibility bracket. It performs no per-issue provider or command admission.
type WorkProgressItem struct {
	Issue     string             `json:"issue"`
	State     string             `json:"state"`
	Readiness *readinessView     `json:"readiness,omitempty"`
	Plan      *IssuePriorityView `json:"plan,omitempty"`
	PlanState string             `json:"plan_state"`
}
type WorkProgressPage struct {
	Repository string             `json:"repository"`
	Items      []WorkProgressItem `json:"items"`
	Next       int                `json:"next"`
}

func (s WorkReads) Progress(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issues []int64) (*WorkProgressPage, error) {
	if len(issues) == 0 || len(issues) > 20 {
		return nil, ErrWorkReadQuery
	}
	seen := map[int64]bool{}
	for _, issue := range issues {
		if issue < 1 || seen[issue] {
			return nil, ErrWorkReadQuery
		}
		seen[issue] = true
	}
	if err := s.visible(ctx, actor, repository, issues[0]); err != nil {
		return nil, err
	}
	page := &WorkProgressPage{Repository: strconv.FormatInt(int64(repository), 10), Items: []WorkProgressItem{}}
	byteCount := 0
	for _, issue := range issues {
		item := WorkProgressItem{Issue: strconv.FormatInt(issue, 10), State: "available", PlanState: "available"}
		recorded, err := s.Store.IssueControl(ctx, int64(repository), issue)
		if err == nil {
			if recorded.Repository != int64(repository) || recorded.Issue != issue {
				return nil, ErrWorkJourneyScope
			}
			item.Readiness = &readinessView{Blockers: recorded.Blockers, Acceptance: recorded.Acceptance, Readiness: recorded.Readiness, Reason: recorded.Reason, Revision: recorded.Revision, Assessed: recorded.AssessedUnix}
		} else if !errors.Is(err, store.ErrNotFound) {
			item.State = "unavailable"
		}
		item.Plan, err = s.priority(ctx, repository, issue, store.PriorityComparisonCursor{}, 5)
		if err != nil {
			item.PlanState = "unavailable"
		}
		encoded, encodeErr := json.Marshal(item)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if len(encoded) > 2*1024*1024-16384 {
			item.Plan = nil
			item.PlanState = "unavailable"
			encoded, _ = json.Marshal(item)
			if len(encoded) > 2*1024*1024-16384 {
				item.Readiness = nil
				item.State = "unavailable"
				encoded, _ = json.Marshal(item)
			}
		}
		if byteCount+len(encoded) > 2*1024*1024-16384 && len(page.Items) > 0 {
			break
		}
		byteCount += len(encoded)
		page.Items = append(page.Items, item)
		page.Next++
	}
	if err := s.visible(ctx, actor, repository, issues[0]); err != nil {
		return nil, err
	}
	return page, nil
}
