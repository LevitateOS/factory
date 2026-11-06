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

func humanDispatchCommand(cmd factory.Command) (humanJournal, error) {
	if cmd.Type == factory.CommandHumanMerge {
		return humanJournal{}, ErrCommandConflict
	}
	return humanJournalCommand(cmd)
}
func scanHumanDispatch(row *sql.Row, want factory.Command) (factory.Command, factory.CommandDispatchState, error) {
	var saved factory.Command
	var state factory.CommandDispatchState
	err := row.Scan(&saved.ID, &saved.Type, &saved.Target, &saved.Principal, &saved.Digest, &saved.Payload, &saved.Outcome, &saved.Created, &saved.Finished, &state)
	if err != nil {
		return saved, "", err
	}
	if !sameHumanCommand(saved, want) {
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
	lookup, err = spec.saveObservation(ctx, tx, saved, lookup)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return lookup, nil
}
