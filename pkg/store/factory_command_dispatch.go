package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

var ErrCommandDispatchNotReady = errors.New("command dispatch requires a saved not-observed lookup")

const humanDispatchQuery = `SELECT id,type,target,principal,digest,payload,outcome,created,finished,dispatch_state FROM factory_commands WHERE id=$1`

// Typed captures retain their own validation and reducers. This is the single
// row-lock/reservation protocol for one-shot native human effects, not
// a second command journal or authority inferred from retained SQL.
type humanDispatch struct {
	host                 collaboration.HostID
	repository           collaboration.RepositoryID
	actor                collaboration.ActorID
	authority, operation string
	invalid              error
	validate             func(collaboration.OperationLookup) error
	finished             func(collaboration.OperationLookup) bool
	ready                func() bool
	save                 func(context.Context, *sql.Tx, factory.Command, collaboration.OperationLookup) (collaboration.OperationLookup, error)
}

func humanDispatchCommand(cmd factory.Command) (humanDispatch, error) {
	var s humanDispatch
	switch cmd.Type {
	case factory.CommandHumanComment:
		c, err := factory.DecodeHumanCommentCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanCommentObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanCommentObservation(c, o) }, finished: factory.HumanCommentFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanCommentObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanIssueCreate:
		c, err := factory.DecodeHumanIssueCreateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanIssueCreateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanIssueCreateObservation(c, o) }, finished: factory.HumanIssueCreateFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanIssueCreateObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanState:
		c, err := factory.DecodeHumanStateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanStateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanStateObservation(c, o) }, finished: factory.HumanStateFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanStateObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanEdit:
		c, err := factory.DecodeHumanEditCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanEditObservation,
			ready:    c.ExpectedCurrent,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanEditObservation(c, o) }, finished: factory.HumanEditFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanEditObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanLock:
		c, err := factory.DecodeHumanLockCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanLockObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanLockObservation(c, o) }, finished: factory.HumanLockFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanLockObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanThread:
		c, err := factory.DecodeHumanThreadCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanThreadObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanThreadObservation(c, o) }, finished: factory.HumanThreadFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanThreadObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanReviewRequest:
		c, err := factory.DecodeHumanReviewRequestCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanReviewRequestObservation,
			validate: func(o collaboration.OperationLookup) error {
				return factory.ValidateHumanReviewRequestObservation(c, o)
			}, finished: factory.HumanReviewRequestFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanReviewRequestObservation(ctx, tx, saved, c, o)
			}}
	case factory.CommandHumanPullCreate:
		c, err := factory.DecodeHumanPullCreateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanDispatch{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanPullCreateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanPullCreateObservation(c, o) }, finished: factory.HumanPullCreateFinished,
			save: func(ctx context.Context, tx *sql.Tx, saved factory.Command, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return saveHumanPullCreateObservation(ctx, tx, saved, c, o)
			}}
	default:
		return s, ErrCommandConflict
	}
	return s, nil
}
func scanHumanDispatch(row *sql.Row, want factory.Command) (factory.Command, factory.CommandDispatchState, error) {
	var saved factory.Command
	var state factory.CommandDispatchState
	err := row.Scan(&saved.ID, &saved.Type, &saved.Target, &saved.Principal, &saved.Digest, &saved.Payload, &saved.Outcome, &saved.Created, &saved.Finished, &state)
	if err != nil {
		return saved, "", err
	}
	if saved.ID != want.ID || saved.Type != want.Type || saved.Target != want.Target || saved.Principal != want.Principal || saved.Digest != want.Digest || saved.Payload != want.Payload {
		return saved, "", ErrCommandConflict
	}
	if _, err := humanDispatchCommand(saved); err != nil {
		return saved, "", err
	}
	switch state {
	case factory.DispatchUnclaimed, factory.DispatchReserved, factory.DispatchLegacyUnknown:
		return saved, state, nil
	default:
		return saved, "", errors.New("invalid command dispatch state")
	}
}
func (s *Store) humanDispatchState(ctx context.Context, cmd factory.Command) (factory.CommandDispatchState, error) {
	if _, err := humanDispatchCommand(cmd); err != nil {
		return "", err
	}
	_, state, err := scanHumanDispatch(s.db.QueryRowContext(ctx, humanDispatchQuery, cmd.ID), cmd)
	return state, err
}

// Only the caller receiving true after commit may send once. A lost commit
// reply never authorizes recovering ownership from the reserved column.
func (s *Store) reserveHumanDispatch(ctx context.Context, cmd factory.Command) (bool, error) {
	spec, err := humanDispatchCommand(cmd)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	saved, state, err := scanHumanDispatch(tx.QueryRowContext(ctx, humanDispatchQuery+` FOR UPDATE`, cmd.ID), cmd)
	if err != nil {
		return false, err
	}
	if state != factory.DispatchUnclaimed || saved.Finished != "" {
		return false, nil
	}
	if spec.ready != nil && !spec.ready() {
		return false, ErrCommandDispatchNotReady
	}
	var lookup collaboration.OperationLookup
	if json.Unmarshal([]byte(saved.Outcome), &lookup) != nil || spec.validate(lookup) != nil || lookup.State != collaboration.LookupNotObserved || lookup.Record != nil {
		return false, ErrCommandDispatchNotReady
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET dispatch_state='reserved' WHERE id=$1 AND dispatch_state='unclaimed' AND finished=''`, cmd.ID)
	if err := checkGrantRowsAffected(result, err, "human command dispatch reservation failed"); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// Applies only when EVERY effect uses this reservation protocol. The adapter
// first admits current caller/account/namespace. A remote idempotent journal
// keeps its own cancellation protocol. This never rolls back a provider effect.
func (s *Store) cancelHumanDispatch(ctx context.Context, cmd factory.Command) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	spec, err := humanDispatchCommand(cmd)
	if err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	saved, state, err := scanHumanDispatch(tx.QueryRowContext(ctx, humanDispatchQuery+` FOR UPDATE`, cmd.ID), cmd)
	if err != nil {
		return empty, err
	}
	lookup := collaboration.OperationLookup{Host: spec.host, Authority: spec.authority, OperationID: spec.operation, State: collaboration.LookupFound}
	record := &collaboration.OperationRecord{Host: spec.host, Authority: spec.authority, OperationID: spec.operation, Effect: collaboration.EffectNotCommitted, Cancellation: collaboration.CancellationCancelled, Completion: collaboration.CompletionComplete, Reason: "cancelled_before_dispatch"}
	if state != factory.DispatchUnclaimed {
		record.Binding = &collaboration.OperationBinding{Repository: spec.repository, Actor: spec.actor, Kind: collaboration.OperationKind(cmd.Type), Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}
		record.Effect, record.Cancellation, record.Completion = collaboration.EffectIndeterminate, collaboration.CancellationIndeterminate, collaboration.CompletionNeedsIntervention
		record.Reason = "dispatch_reserved"
		if state == factory.DispatchLegacyUnknown {
			record.Reason = "legacy_dispatch_unknown"
		}
	}
	lookup.Record = record
	if saved.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(saved.Outcome), &prior) != nil {
			return empty, spec.invalid
		}
		if err := spec.validate(prior); err != nil {
			return empty, err
		}
		if prior.Record != nil && (state == factory.DispatchUnclaimed || spec.finished(prior)) {
			lookup = prior
		}
	}
	lookup, err = spec.save(ctx, tx, saved, lookup)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return lookup, nil
}
