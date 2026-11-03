package service

import (
	"context"
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
	if err := s.visible(ctx, actor, repository, issue); err != nil {
		return nil, err
	}
	return view, nil
}
