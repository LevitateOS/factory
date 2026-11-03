package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// storeAssignedLimit bounds one unfinished-assignment listing.
const storeAssignedLimit = 1024

// Assignment returns one dispatch assignment by identity.
func (s *Store) Assignment(ctx context.Context, id string) (factory.Assignment, error) {
	var a factory.Assignment
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &a)
	}
	return a, err
}

// AssignmentByRun returns the assignment whose latest attempt runs under
// the given run identity. A missing run reports ErrNotFound like the
// other assignment readers, never a raw driver miss: callers outside
// the store must not import database/sql for this lookup.
func (s *Store) AssignmentByRun(ctx context.Context, runID string) (factory.Assignment, error) {
	var a factory.Assignment
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE run=$1`, runID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return factory.Assignment{}, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(data, &a)
	}
	return a, err
}

// AcceptanceAssignmentsAfter pages assignment lineage for one withdrawn
// acceptance. All stages remain relevant because RunHistory includes retries.
func (s *Store) AcceptanceAssignmentsAfter(ctx context.Context, repository, issue int64, decision, after string, limit int) ([]factory.Assignment, string, error) {
	if repository <= 0 || issue <= 0 || decision == "" || limit <= 0 || limit > 256 {
		return nil, after, errors.New("invalid acceptance assignment listing")
	}
	var cursor int64
	if after != "" {
		var err error
		cursor, err = strconv.ParseInt(after, 10, 64)
		if err != nil || cursor <= 0 {
			return nil, after, errors.New("invalid acceptance assignment cursor")
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,data FROM factory_assignments
		WHERE repository=$1 AND issue=$2 AND data->>'acceptance'=$3 AND seq>$4
		ORDER BY seq LIMIT $5`, repository, issue, decision, cursor, limit)
	if err != nil {
		return nil, after, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]factory.Assignment, 0, limit)
	next := after
	for rows.Next() {
		var seq int64
		var data []byte
		var a factory.Assignment
		if err = rows.Scan(&seq, &data); err != nil {
			return nil, after, err
		}
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, after, err
		}
		if a.Repository != repository || a.Issue != issue || a.Acceptance != decision {
			return nil, after, errors.New("acceptance assignment scope mismatch")
		}
		out = append(out, a)
		next = strconv.FormatInt(seq, 10)
	}
	if err = rows.Err(); err != nil {
		return nil, after, err
	}
	return out, next, nil
}

// sameAssignmentFinishInput reports whether next carries exactly the
// recorded immutable inputs of current: only the five terminal fields the
// serving caller sets (Stage, Outcome, Reason, FinishedUnix, Result) may
// differ. Both inputs are copied by value and only those five fields are
// cleared on the copies; the rest compares by reflect.DeepEqual, so nested
// payload values compare equal across independent allocations without
// pointer-identity comparison, and shared backing arrays are never
// mutated.
func sameAssignmentFinishInput(current, next factory.Assignment) bool {
	c, n := current, next
	c.Stage, n.Stage = "", ""
	c.Outcome, n.Outcome = "", ""
	c.Reason, n.Reason = "", ""
	c.FinishedUnix, n.FinishedUnix = 0, 0
	c.Result, n.Result = nil, nil
	return reflect.DeepEqual(c, n)
}

// FinishAssignment stores one assignment's terminal outcome exactly once.
// Only an assigned assignment at its recorded revision finishes; anything
// else reports stale instead of overwriting a concurrent finish.
func (s *Store) FinishAssignment(ctx context.Context, a factory.Assignment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.Stage != factory.AssignmentFinished {
		return errors.New("only a finished assignment finishes")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	paused, err := attemptPauseActiveTx(ctx, tx, a.Repository)
	if err != nil {
		return err
	}
	if err = checkAssignmentFinishFreshTx(ctx, tx, a); err != nil {
		return err
	}
	if err = writeAssignmentFinishTx(ctx, tx, a, data); err != nil {
		return err
	}
	if err = settleFinishedAssignmentTx(ctx, tx, a, paused); err != nil {
		return err
	}
	return tx.Commit()
}

func checkAssignmentFinishFreshTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	var currentData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, a.ID).Scan(&currentData); err != nil {
		return err
	}
	var current factory.Assignment
	if err := json.Unmarshal(currentData, &current); err != nil {
		return err
	}
	// All recorded inputs are immutable through finish: the current row
	// must still be assigned and the request must carry exactly its
	// inputs. Only the five terminal fields may differ; anything else
	// reports stale before the update, readiness and allowance
	// transition.
	if current.Stage != factory.AssignmentAssigned {
		return ErrNotFound
	}
	if !sameAssignmentFinishInput(current, a) {
		return ErrNotFound
	}
	return nil
}

func writeAssignmentFinishTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, data []byte) error {
	result, err := tx.ExecContext(ctx, `UPDATE factory_assignments SET stage='finished',data=$1
		WHERE id=$2 AND revision=$3 AND stage='assigned'
		AND data->>'attempt_root'=$4 AND data->>'publication_assignment'=$5`,
		string(data), a.ID, a.Revision, a.AttemptRoot, a.PublicationAssignment)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func settleFinishedAssignmentTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, paused bool) error {
	// A valid canonical completion root addresses its own typed
	// allowance through the shared transition joint in this same
	// transaction. It never enqueues ordinary issue readiness:
	// readiness refuses issue zero, and completion accounting is
	// per-root, never per-latest(repository, 0).
	isCompletion := a.Subject.Kind == factory.SubjectCompletion
	isIntent := a.Subject.Kind == factory.SubjectIntent
	if a.Outcome == factory.Succeeded && a.Reason == factory.AssignReasonReported {
		return settleReportedFinishTx(ctx, tx, a, isCompletion, isIntent)
	}
	closeRoot := a.Reason != factory.AssignReasonSuperseded && a.Reason != factory.AssignReasonWithdrawn && a.Reason != factory.AssignReasonCancelled
	if paused {
		closeRoot = false
	}
	if err := transitionAttemptAssignmentTx(ctx, tx, a.ID, closeRoot, time.Now()); err != nil {
		// Terminal completion and intent accounting is
		// mandatory: a missing or mismatched typed allowance
		// fails the finish instead of committing without
		// accounting. Ordinary issue paths keep the historical
		// absent-row tolerance.
		if !isCompletion && !isIntent && a.Subject.Kind != factory.SubjectStall && a.Subject.Kind != factory.SubjectDelivery && a.Subject.Kind != factory.SubjectDeliveryRepair && errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return nil
}

func settleReportedFinishTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, isCompletion, isIntent bool) error {
	if a.Subject.Kind == factory.SubjectDelivery || a.Subject.Kind == factory.SubjectStall || a.Subject.Kind == factory.SubjectDeliveryRepair {
		return transitionAttemptAssignmentTx(ctx, tx, a.ID, true, time.Now())
	}
	if isCompletion {
		// Successful+reported terminal completion closes its
		// coder-only root admission here; the shared joint
		// retains the active clock until actual recorded
		// run/preparation effects settle.
		return transitionAttemptAssignmentTx(ctx, tx, a.ID, true, time.Now())
	}
	if isIntent {
		// Successful+reported terminal intent closes its
		// decompose-only root admission here through the same
		// shared joint. It never enqueues ordinary issue
		// readiness: readiness refuses issue zero, and intent
		// accounting is per-root, never per-latest(repository, 0).
		return transitionAttemptAssignmentTx(ctx, tx, a.ID, true, time.Now())
	}
	return enqueueReadinessRootEventTx(ctx, tx, a.Repository, a.Issue)
}

// AssignedAssignments lists every unfinished dispatch assignment, oldest
// first, for crash recovery. Recovery resolves each one to consumed,
// released or finished; deferred work waits for a later pass.
func (s *Store) AssignedAssignments(ctx context.Context, limit int) ([]factory.Assignment, error) {
	if limit <= 0 || limit > storeAssignedLimit {
		return nil, errors.New("invalid assigned assignment listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_assignments WHERE stage='assigned' ORDER BY seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Assignment
	for rows.Next() {
		var data []byte
		var a factory.Assignment
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
