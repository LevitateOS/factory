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

const humanCommentDispatchQuery = `SELECT id,type,target,principal,digest,payload,outcome,created,finished,dispatch_state FROM factory_commands WHERE id=$1`

func scanHumanCommentDispatch(row *sql.Row, want factory.Command) (factory.Command, factory.CommandDispatchState, error) {
	var saved factory.Command
	var state factory.CommandDispatchState
	err := row.Scan(&saved.ID, &saved.Type, &saved.Target, &saved.Principal, &saved.Digest, &saved.Payload, &saved.Outcome, &saved.Created, &saved.Finished, &state)
	if err != nil {
		return saved, "", err
	}
	if saved.Type != want.Type || saved.Target != want.Target || saved.Principal != want.Principal || saved.Digest != want.Digest || saved.Payload != want.Payload {
		return saved, "", ErrCommandConflict
	}
	if _, err := factory.DecodeHumanCommentCapture(saved); err != nil {
		return saved, "", err
	}
	switch state {
	case factory.DispatchUnclaimed, factory.DispatchReserved, factory.DispatchLegacyUnknown:
		return saved, state, nil
	default:
		return saved, "", errors.New("invalid command dispatch state")
	}
}

// HumanCommentDispatchState returns the local fence for the exact winning
// capture. Absence is ErrNotFound, never evidence that a remote effect is absent.
func (s *Store) HumanCommentDispatchState(ctx context.Context, capture factory.HumanCommentCapture) (factory.CommandDispatchState, error) {
	cmd, err := capture.Command()
	if err != nil {
		return "", err
	}
	_, state, err := scanHumanCommentDispatch(s.db.QueryRowContext(ctx, humanCommentDispatchQuery, cmd.ID), cmd)
	return state, err
}

// ReserveHumanCommentDispatch commits the one-shot fence before network use.
// Only the caller receiving true may dispatch, once, without automatic retries.
// If commit succeeds but its reply is lost, the fence remains: callers must not
// infer ownership by reading a reserved state. This grants no provider authority
// and proves no effect.
// Adapters with remote idempotent journals can retain their existing protocol.
func (s *Store) ReserveHumanCommentDispatch(ctx context.Context, capture factory.HumanCommentCapture) (bool, error) {
	cmd, err := capture.Command()
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	saved, state, err := scanHumanCommentDispatch(tx.QueryRowContext(ctx, humanCommentDispatchQuery+` FOR UPDATE`, cmd.ID), cmd)
	if err != nil {
		return false, err
	}
	if state != factory.DispatchUnclaimed || saved.Finished != "" {
		return false, nil
	}
	var lookup collaboration.OperationLookup
	if json.Unmarshal([]byte(saved.Outcome), &lookup) != nil || factory.ValidateHumanCommentObservation(capture, lookup) != nil || lookup.State != collaboration.LookupNotObserved || lookup.Record != nil {
		return false, ErrCommandDispatchNotReady
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET dispatch_state='reserved' WHERE id=$1 AND dispatch_state='unclaimed' AND finished=''`, cmd.ID)
	if err := checkGrantRowsAffected(result, err, "human comment dispatch reservation failed"); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CancelHumanCommentDispatch is for adapters using this one-shot protocol for
// EVERY dispatch. It does not cancel a remote provider journal. The caller must
// first admit the current human and exact captured native account/namespace.
// Taking the same row lock as reservation makes pre-dispatch cancellation final
// before any winner can send. Reserved or legacy commands stay indeterminate;
// cancellation cannot turn a possibly sent request into a no-effect receipt.
func (s *Store) CancelHumanCommentDispatch(ctx context.Context, capture factory.HumanCommentCapture) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	cmd, err := capture.Command()
	if err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	saved, state, err := scanHumanCommentDispatch(tx.QueryRowContext(ctx, humanCommentDispatchQuery+` FOR UPDATE`, cmd.ID), cmd)
	if err != nil {
		return empty, err
	}
	lookup := collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID, State: collaboration.LookupFound}
	record := &collaboration.OperationRecord{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID,
		Effect: collaboration.EffectNotCommitted, Cancellation: collaboration.CancellationCancelled,
		Completion: collaboration.CompletionComplete, Reason: "cancelled_before_dispatch"}
	if state != factory.DispatchUnclaimed {
		record.Binding = &collaboration.OperationBinding{Repository: capture.Request.Repository, Actor: capture.Request.Actor, Kind: collaboration.CreateComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}
		record.Effect, record.Cancellation, record.Completion = collaboration.EffectIndeterminate, collaboration.CancellationIndeterminate, collaboration.CompletionNeedsIntervention
		record.Reason = "dispatch_reserved"
		if state == factory.DispatchLegacyUnknown {
			record.Reason = "legacy_dispatch_unknown"
		}
	}
	lookup.Record = record
	// A prior provider record is stronger than the local fence. Never infer a
	// pre-submit tombstone from an unclaimed column when another protocol has
	// already supplied evidence, and never replace a terminal provider result.
	if saved.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(saved.Outcome), &prior) != nil || factory.ValidateHumanCommentObservation(capture, prior) != nil {
			return empty, factory.ErrHumanCommentObservation
		}
		if prior.Record != nil && (state == factory.DispatchUnclaimed || factory.HumanCommentFinished(prior)) {
			lookup = prior
		}
	}
	lookup, err = saveHumanCommentObservation(ctx, tx, saved, capture, lookup)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return lookup, nil
}
