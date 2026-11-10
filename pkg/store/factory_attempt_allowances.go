package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

var (
	ErrAttemptRunsPending  = errors.New("attempt runs or preparations are not confirmed settled")
	ErrAttemptRootMismatch = errors.New("assignment does not match the recorded attempt root")
)

// AttemptAllowance returns the newest attempt allowance for the issue.
// Explicit maintainer retries keep earlier allowance rows in history.
// Unscoped issue-zero lookups refuse: completion roots are read only
// through the typed ReassessmentAttemptAllowance getter, never through
// the latest-per-issue path.
func (s *Store) AttemptAllowance(ctx context.Context, repository, issue int64) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	if issue <= 0 {
		return allowance, errors.New("attempt allowance lookup needs its issue scope")
	}
	var root string
	var revision int64
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT l.root_assignment,l.revision,l.data FROM factory_attempt_allowances l JOIN factory_assignments a ON a.id=l.root_assignment WHERE l.repository=$1 AND l.issue=$2 ORDER BY a.seq DESC LIMIT 1`, repository, issue).Scan(&root, &revision, &data)
	if err != nil {
		return allowance, err
	}
	if err = json.Unmarshal(data, &allowance); err != nil {
		return allowance, err
	}
	if err = validateAttemptAllowanceRow(allowance, repository, issue, root, revision); err != nil {
		return factory.AttemptAllowance{}, err
	}
	return allowance, nil
}

// CapAttemptDeadline returns the earlier of the requested absolute expiry and
// the exact assignment root's time bound. Issue attempts use a fresh per-run
// limit; issue-zero time-only grants retain their aggregate expiry. Native operations are
// admitted against this immutable wall-clock bound; existing intents are
// always replayed from their recorded NotAfter instead of calling this again.
func (s *Store) CapAttemptDeadline(ctx context.Context, assignmentID string, requested int64) (int64, error) {
	if !factory.ValidID(assignmentID) || requested <= 0 {
		return 0, factory.ErrAttemptTimeExhausted
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, assignmentID).Scan(&raw); err != nil {
		return 0, err
	}
	var assignment factory.Assignment
	if err = json.Unmarshal(raw, &assignment); err != nil {
		return 0, err
	}
	if err = assignment.Validate(); err != nil {
		return 0, err
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, factory.ErrAttemptTimeExhausted
	}
	if err != nil {
		return 0, err
	}
	now := time.Now()
	deadline, err := attemptDeadlineUnix(allowance, now)
	if err != nil {
		return 0, err
	}
	if requested < deadline {
		deadline = requested
	}
	if deadline <= now.Unix() {
		return 0, factory.ErrAttemptTimeExhausted
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return deadline, nil
}

// CloseExpiredAttempt deactivates an already closed root once its last
// descendant run is confirmed settled. Attempts no longer expire on time, so
// an open root stays open. It is safe to replay.
func (s *Store) CloseExpiredAttempt(ctx context.Context, assignmentID string) (bool, error) {
	return s.closeAttempt(ctx, assignmentID, false)
}

// CloseAttempt closes the current exact root for an explicit Retry, which
// then starts a fresh attempt. It is safe to replay.
func (s *Store) CloseAttempt(ctx context.Context, assignmentID string) (bool, error) {
	return s.closeAttempt(ctx, assignmentID, true)
}

func (s *Store) closeAttempt(ctx context.Context, assignmentID string, force bool) (bool, error) {
	if !factory.ValidID(assignmentID) {
		return false, errors.New("invalid attempt assignment")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, assignmentID).Scan(&raw); err != nil {
		return false, err
	}
	var assignment factory.Assignment
	if err = json.Unmarshal(raw, &assignment); err != nil {
		return false, err
	}
	if err = assignment.Validate(); err != nil {
		return false, err
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Serialize the current publication-owner check with dispatch/retry writes
	// on this root. Historical children of that owner share the same allowance.
	owner, err := currentAttemptOwnerTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) || err == nil && assignment.PublicationAssignment != owner {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now()
	if !allowance.Closed {
		if !force {
			return false, tx.Commit()
		}
		settled, err := attemptRootRunsSettledTx(ctx, tx, allowance.Repository, allowance.Issue, allowance.RootAssignment)
		if err != nil || !settled {
			return false, err
		}
	}
	if err = transitionLockedAttemptRootTx(ctx, tx, allowance, true, now); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func attemptDeadlineUnix(allowance factory.AttemptAllowance, now time.Time) (int64, error) {
	if err := allowance.Validate(); err != nil {
		return 0, factory.ErrAttemptTimeExhausted
	}
	if now.Unix() < allowance.CheckpointUnix {
		return 0, factory.ErrAllowanceClock
	}
	if allowance.Closed {
		return 0, factory.ErrAttemptClosed
	}
	if allowance.Issue == 0 && !allowance.Active {
		return 0, factory.ErrAttemptTimeExhausted
	}
	remaining := int64(allowance.Limits.ActiveMinutes) * 60
	start := now.Unix()
	if allowance.Issue == 0 {
		remaining -= allowance.ActiveSeconds
		start = allowance.CheckpointUnix
	}
	if remaining <= 0 || start > math.MaxInt64-remaining {
		return 0, factory.ErrAttemptTimeExhausted
	}
	deadline := start + remaining
	if deadline <= now.Unix() {
		return 0, factory.ErrAttemptTimeExhausted
	}
	return deadline, nil
}

func validateAttemptAllowanceRow(a factory.AttemptAllowance, repository, issue int64, root string, revision int64) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.Repository != repository || a.Issue != issue || a.RootAssignment != root || a.Revision != revision {
		return errors.New("attempt allowance row binding does not match its data")
	}
	return nil
}

func lockAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, repository, issue int64) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	if issue <= 0 {
		return allowance, errors.New("attempt allowance lookup needs its issue scope")
	}
	var root string
	var revision int64
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT l.root_assignment,l.revision,l.data FROM factory_attempt_allowances l JOIN factory_assignments a ON a.id=l.root_assignment WHERE l.repository=$1 AND l.issue=$2 ORDER BY a.seq DESC LIMIT 1 FOR UPDATE OF l`, repository, issue).Scan(&root, &revision, &data)
	if err != nil {
		return allowance, err
	}
	if err = json.Unmarshal(data, &allowance); err != nil {
		return allowance, err
	}
	if err = validateAttemptAllowanceRow(allowance, repository, issue, root, revision); err != nil {
		return factory.AttemptAllowance{}, err
	}
	return allowance, nil
}

func lockAttemptAllowanceRootTx(ctx context.Context, tx *sql.Tx, repository, issue int64, root string) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	var storedRoot string
	var revision int64
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT root_assignment,revision,data FROM factory_attempt_allowances WHERE repository=$1 AND issue=$2 AND root_assignment=$3 FOR UPDATE`, repository, issue, root).Scan(&storedRoot, &revision, &data)
	if err != nil {
		return allowance, err
	}
	if err = json.Unmarshal(data, &allowance); err != nil {
		return allowance, err
	}
	if err = validateAttemptAllowanceRow(allowance, repository, issue, storedRoot, revision); err != nil {
		return factory.AttemptAllowance{}, err
	}
	return allowance, nil
}

// attemptRootRunsSettledTx confirms every recorded run view is reconciled and
// every admitted native candidate preparation is quiescent. Retained history
// remains in scope.
func attemptRootRunsSettledTx(ctx context.Context, tx *sql.Tx, repository, issue int64, root string) (bool, error) {
	var settled bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM factory_runs r
		JOIN factory_run_views v ON v.run=r.id
		JOIN factory_assignments a ON a.id=v.attempt
		WHERE (r.active OR NOT r.settled OR COALESCE(r.data->>'reconciled','false') <> 'true')
		  AND a.repository=$1 AND a.issue=$2 AND a.data->>'attempt_root'=$3
		LIMIT 1
	) AND NOT EXISTS (
		SELECT 1 FROM project_preparations p
		WHERE p.project_id=(SELECT data->>'project_id' FROM factory_assignments WHERE id=$3)
		  AND p.data ? 'factory_admission'
		  AND (p.data->'factory_admission'->>'repository')::bigint=$1
		  AND (p.data->'factory_admission'->>'issue')::bigint=$2
		  AND p.data->'factory_admission'->>'attempt_root'=$3
		  AND NOT soda_preparation_quiescent(p.data,p.id,p.project_id,p.role)
		LIMIT 1
	)`, repository, issue, root).Scan(&settled)
	return settled, err
}

// transitionAttemptRootTx applies durable lifecycle state only after every
// descendant process has stopped. A terminal decision may close admission
// immediately while retaining its active clock/slot until settlement.
func transitionAttemptRootTx(ctx context.Context, tx *sql.Tx, repository, issue int64, root string, closeRoot bool, now time.Time) error {
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, repository, issue, root)
	if err != nil {
		return err
	}
	return transitionLockedAttemptRootTx(ctx, tx, allowance, closeRoot, now)
}

func transitionLockedAttemptRootTx(ctx context.Context, tx *sql.Tx, allowance factory.AttemptAllowance, closeRoot bool, now time.Time) error {
	var err error
	repository, issue, root := allowance.Repository, allowance.Issue, allowance.RootAssignment
	settled, err := attemptRootRunsSettledTx(ctx, tx, repository, issue, root)
	if err != nil {
		return err
	}
	prior := allowance
	if closeRoot {
		allowance.Closed = true
	}
	if settled && allowance.Active {
		allowance, err = allowance.Checkpoint(now, false)
		if err != nil {
			return err
		}
	}
	if allowance.Active == prior.Active && allowance.Closed == prior.Closed && allowance.ActiveSeconds == prior.ActiveSeconds && allowance.CheckpointUnix == prior.CheckpointUnix {
		return nil
	}
	allowance.Revision++
	return saveAttemptAllowanceTx(ctx, tx, allowance)
}

func transitionAttemptAssignmentTx(ctx context.Context, tx *sql.Tx, assignmentID string, closeRoot bool, now time.Time) error {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, assignmentID).Scan(&data); err != nil {
		return err
	}
	var assignment factory.Assignment
	if err := json.Unmarshal(data, &assignment); err != nil {
		return err
	}
	if err := assignment.Validate(); err != nil {
		return err
	}
	// Completion roots carry no publication owner and never consult the
	// latest (repository, 0) row: the exact typed allowance for this
	// root's own completion subject performs the settled-effect, clock
	// and CAS checks through the shared joint below. A missing or
	// mismatched typed allowance fails the transaction; it is never a
	// silent skip.
	if assignment.Subject.Kind == factory.SubjectCompletion {
		if assignment.Subject.Completion == nil {
			return errors.New("reassessment root lacks its completion subject")
		}
		typed, err := lockReassessmentAttemptAllowanceTx(ctx, tx, assignment.Repository, *assignment.Subject.Completion, assignment.AttemptRoot)
		if err != nil {
			return err
		}
		return transitionLockedAttemptRootTx(ctx, tx, typed, closeRoot, now)
	}
	// Intent roots carry no publication owner and never consult the
	// latest (repository, 0) row: the exact typed allowance for this
	// root's own stable intent scope performs the settled-effect, clock
	// and CAS checks through the shared joint below. A missing or
	// mismatched typed allowance fails the transaction; it is never a
	// silent skip.
	if assignment.Subject.Kind == factory.SubjectIntent {
		if assignment.Subject.Intent == nil {
			return errors.New("intent root lacks its intent subject")
		}
		scope := factory.IntentAttemptScope{IntentID: assignment.Subject.Intent.IntentID, OwnerID: assignment.Subject.Intent.OwnerID}
		typed, err := lockIntentAttemptAllowanceTx(ctx, tx, assignment.Repository, scope, assignment.AttemptRoot)
		if err != nil {
			return err
		}
		return transitionLockedAttemptRootTx(ctx, tx, typed, closeRoot, now)
	}
	if assignment.Subject.Kind == factory.SubjectDelivery {
		typed, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, 0, assignment.AttemptRoot)
		if err != nil {
			return err
		}
		if typed.Delivery == nil || assignment.Subject.Delivery == nil || *typed.Delivery != *assignment.Subject.Delivery {
			return ErrAttemptRootMismatch
		}
		return transitionLockedAttemptRootTx(ctx, tx, typed, closeRoot, now)
	}
	if assignment.Subject.Kind == factory.SubjectStall {
		typed, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, 0, assignment.AttemptRoot)
		if err != nil {
			return err
		}
		if typed.Stall == nil || assignment.Subject.Stall == nil || *typed.Stall != *assignment.Subject.Stall {
			return ErrAttemptRootMismatch
		}
		return transitionLockedAttemptRootTx(ctx, tx, typed, closeRoot, now)
	}
	if assignment.Subject.Kind == factory.SubjectDeliveryRepair {
		typed, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, 0, assignment.AttemptRoot)
		if err != nil {
			return err
		}
		if typed.DeliveryRepair == nil || assignment.Subject.DeliveryRepair == nil || *typed.DeliveryRepair != *assignment.Subject.DeliveryRepair {
			return ErrAttemptRootMismatch
		}
		return transitionLockedAttemptRootTx(ctx, tx, typed, closeRoot, now)
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil {
		return err
	}
	currentOwner, err := currentAttemptOwnerTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) || err == nil && assignment.PublicationAssignment != currentOwner {
		return nil
	}
	if err != nil {
		return err
	}
	return transitionLockedAttemptRootTx(ctx, tx, allowance, closeRoot, now)
}

func currentAttemptOwnerTx(ctx context.Context, tx *sql.Tx, repository, issue int64, root string) (string, error) {
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments
		WHERE repository=$1 AND issue=$2 ORDER BY seq DESC LIMIT 1`, repository, issue).Scan(&data)
	if err != nil {
		return "", err
	}
	var latest factory.Assignment
	if err = json.Unmarshal(data, &latest); err != nil {
		return "", err
	}
	if err = latest.Validate(); err != nil {
		return "", err
	}
	if latest.AttemptRoot != root {
		return "", sql.ErrNoRows
	}
	return latest.PublicationAssignment, nil
}

func attemptPauseActiveTx(ctx context.Context, tx *sql.Tx, repository int64) (bool, error) {
	open, revision, raw, err := ensureDispatchGateTx(ctx, tx, repository)
	if err != nil {
		return false, err
	}
	var withdrawal factory.Withdrawal
	if err = json.Unmarshal(raw, &withdrawal); err != nil {
		return false, err
	}
	if open && revision == 0 && withdrawal.Repository == 0 {
		return false, nil
	}
	if err = withdrawal.Validate(); err != nil {
		return false, err
	}
	for _, cause := range withdrawal.ActiveCauses {
		if cause == factory.CauseControlPaused || cause == factory.CauseProjectStop {
			return true, nil
		}
	}
	return false, nil
}

func ensureAttemptDeadlineForRegistrationTx(ctx context.Context, tx *sql.Tx, assignmentID string, deadlines ...int64) error {
	if len(deadlines) == 0 {
		return factory.ErrAttemptTimeExhausted
	}
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, assignmentID).Scan(&data); err != nil {
		return err
	}
	var assignment factory.Assignment
	if err := json.Unmarshal(data, &assignment); err != nil {
		return err
	}
	if err := assignment.Validate(); err != nil {
		return err
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return factory.ErrAttemptTimeExhausted
	}
	if err != nil {
		return err
	}
	deadline, err := attemptDeadlineUnix(allowance, time.Now())
	if err != nil {
		return err
	}
	for _, registered := range deadlines {
		if registered <= time.Now().Unix() || registered > deadline {
			return factory.ErrAttemptTimeExhausted
		}
	}
	return nil
}

func deactivateReleasedAttemptTx(ctx context.Context, tx *sql.Tx, assignmentID string, now time.Time) error {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, assignmentID).Scan(&data); err != nil {
		return err
	}
	var assignment factory.Assignment
	if err := json.Unmarshal(data, &assignment); err != nil {
		return err
	}
	if err := assignment.Validate(); err != nil {
		return err
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var latestID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM factory_assignments WHERE repository=$1 AND issue=$2 ORDER BY seq DESC LIMIT 1`, assignment.Repository, assignment.Issue).Scan(&latestID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && latestID != assignment.ID {
		return nil
	}
	if err != nil {
		return err
	}
	owner, err := currentAttemptOwnerTx(ctx, tx, assignment.Repository, assignment.Issue, assignment.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) || err == nil && assignment.PublicationAssignment != owner {
		return nil
	}
	if err != nil {
		return err
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM factory_reservations WHERE assignment=$1`, assignmentID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || err == nil && state != factory.ReservationReleased {
		return nil
	}
	if err != nil {
		return err
	}
	return transitionLockedAttemptRootTx(ctx, tx, allowance, false, now)
}

// sameAttemptAllowanceScope reports whether next keeps the immutable
// record scope of stored: repository, issue, root assignment, completion
// subject, stable intent scope and admitted limits. Only revision-gated
// consumption and clock state may advance across CAS saves, on issue,
// completion and intent roots alike. The stable intent binding compares by
// value (nil/present plus IntentID and OwnerID): mutable assignment
// snapshots such as RevisionID and ScopeDigest are never copied here and
// pointer identity never decides.
func sameAttemptAllowanceScope(stored, next factory.AttemptAllowance) bool {
	if stored.DeliveryRepair == nil || next.DeliveryRepair == nil {
		if stored.DeliveryRepair != nil || next.DeliveryRepair != nil {
			return false
		}
	} else if *stored.DeliveryRepair != *next.DeliveryRepair {
		return false
	}
	if stored.Stall == nil || next.Stall == nil {
		if stored.Stall != nil || next.Stall != nil {
			return false
		}
	} else if *stored.Stall != *next.Stall {
		return false
	}
	if stored.Repository != next.Repository || stored.Issue != next.Issue ||
		stored.RootAssignment != next.RootAssignment || stored.Limits != next.Limits {
		return false
	}
	if stored.Delivery == nil || next.Delivery == nil {
		if stored.Delivery != nil || next.Delivery != nil {
			return false
		}
	} else if *stored.Delivery != *next.Delivery {
		return false
	}
	if stored.Completion == nil || next.Completion == nil {
		if stored.Completion != nil || next.Completion != nil {
			return false
		}
	} else if *stored.Completion != *next.Completion {
		return false
	}
	if stored.Intent == nil || next.Intent == nil {
		return stored.Intent == nil && next.Intent == nil
	}
	return *stored.Intent == *next.Intent
}

// normalizeAllowanceSlices keeps stored allowance JSON free of null
// arrays: Go nil slices marshal as JSON null, which jsonb array expansion
// does not treat as empty. Queries guard with jsonb_typeof, and writes
// normalize to [] so old and new rows read back identically.
func normalizeAllowanceSlices(allowance factory.AttemptAllowance) factory.AttemptAllowance {
	if allowance.Corrections == nil {
		allowance.Corrections = []string{}
	}
	return allowance
}

func saveAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, allowance factory.AttemptAllowance) error {
	if err := allowance.Validate(); err != nil {
		return err
	}
	allowance = normalizeAllowanceSlices(allowance)
	var storedData []byte
	var storedRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision,data FROM factory_attempt_allowances WHERE repository=$1 AND issue=$2 AND root_assignment=$3 FOR UPDATE`,
		allowance.Repository, allowance.Issue, allowance.RootAssignment).Scan(&storedRevision, &storedData); err != nil {
		return err
	}
	var stored factory.AttemptAllowance
	if err := json.Unmarshal(storedData, &stored); err != nil {
		return err
	}
	if err := stored.Validate(); err != nil {
		return err
	}
	if !sameAttemptAllowanceScope(stored, allowance) {
		return errors.New("attempt allowance scope is immutable")
	}
	// Exact revision CAS: the writer must advance the stored revision by
	// exactly one. Stale, same, or skipped revisions refuse; the row-lock
	// alone never authorizes a rewrite.
	if allowance.Revision != storedRevision+1 {
		return ErrStaleRevision
	}
	if err := checkAllowanceNoRefill(stored, allowance); err != nil {
		return err
	}
	data, err := json.Marshal(allowance)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_attempt_allowances SET revision=$1,data=$2 WHERE repository=$3 AND issue=$4 AND root_assignment=$5 AND revision=$6`,
		allowance.Revision, string(data), allowance.Repository, allowance.Issue, allowance.RootAssignment, storedRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

// checkAllowanceNoRefill enforces cumulative accounting on one root: clock
// state never moves backward, a closed root never reopens, and every
// consumption record keeps its prior entries. A same-root policy, head or
// data edit can never refund prior charges; explicit retry uses a distinct
// authorized root and preserves the old row.
func checkAllowanceNoRefill(stored, next factory.AttemptAllowance) error {
	if next.ActiveSeconds < stored.ActiveSeconds || next.CheckpointUnix < stored.CheckpointUnix {
		return errors.New("attempt allowance clock moved backward")
	}
	if stored.Closed && !next.Closed {
		return errors.New("closed attempt allowance never reopens")
	}
	if !stringPrefix(next.Corrections, stored.Corrections) || !stringPrefix(next.Reviews, stored.Reviews) {
		return errors.New("attempt allowance consumption erased")
	}
	if !stringMapPrefix(next.ReviewedHeads, stored.ReviewedHeads) || !stringMapPrefix(next.UnchangedHeadExtra, stored.UnchangedHeadExtra) {
		return errors.New("attempt allowance consumption erased")
	}
	return nil
}

func stringPrefix(next, stored []string) bool {
	if len(next) < len(stored) {
		return false
	}
	for i := range stored {
		if next[i] != stored[i] {
			return false
		}
	}
	return true
}

func stringMapPrefix(next, stored map[string]string) bool {
	for k, v := range stored {
		if next[k] != v {
			return false
		}
	}
	return true
}

// FreezeAttemptAllowances closes active issue clocks only under the exact
// closed pause revision and after every repository run view is settled.
func admitFreezeGateTx(ctx context.Context, tx *sql.Tx, repository, expectedGateRevision int64) error {
	open, revision, raw, err := ensureDispatchGateTx(ctx, tx, repository)
	if err != nil {
		return err
	}
	if open || revision != expectedGateRevision {
		return ErrStaleRevision
	}
	var withdrawal factory.Withdrawal
	if err = json.Unmarshal(raw, &withdrawal); err != nil {
		return err
	}
	if err = withdrawal.Validate(); err != nil {
		return err
	}
	paused := false
	for _, cause := range withdrawal.ActiveCauses {
		if cause == factory.CauseControlPaused {
			paused = true
			break
		}
	}
	if !paused {
		return errors.New("attempt clocks can freeze only for an explicit repository pause")
	}
	return nil
}

func admitFreezeRunsSettledTx(ctx context.Context, tx *sql.Tx, repository int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT r.settled,r.data FROM factory_run_views v JOIN factory_runs r ON r.id=v.run WHERE v.repository=$1 ORDER BY v.seq FOR UPDATE OF r`, repository)
	if err != nil {
		return err
	}
	for rows.Next() {
		var settled bool
		var data []byte
		if err = rows.Scan(&settled, &data); err != nil {
			_ = rows.Close()
			return err
		}
		var run factory.Run
		if err = json.Unmarshal(data, &run); err != nil {
			_ = rows.Close()
			return err
		}
		if !settled || !run.Reconciled {
			_ = rows.Close()
			return ErrAttemptRunsPending
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	var preparationsPending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM project_preparations p
		WHERE (p.data->'factory_admission'->>'repository')::bigint=$1
		  AND p.data ? 'factory_admission'
		  AND NOT soda_preparation_quiescent(p.data,p.id,p.project_id,p.role)
		LIMIT 1)`, repository).Scan(&preparationsPending); err != nil {
		return err
	}
	if preparationsPending {
		return ErrAttemptRunsPending
	}
	return nil
}

func freezeAllowancePageTx(ctx context.Context, tx *sql.Tx, repository int64, afterRoot string, pageLimit int, now time.Time) (string, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT issue,root_assignment,revision,data FROM factory_attempt_allowances WHERE repository=$1 AND root_assignment>$2 ORDER BY root_assignment LIMIT $3 FOR UPDATE`, repository, afterRoot, pageLimit)
	if err != nil {
		return afterRoot, false, err
	}
	locked := make([]factory.AttemptAllowance, 0, pageLimit)
	for rows.Next() {
		var issue, revision int64
		var root string
		var data []byte
		if err = rows.Scan(&issue, &root, &revision, &data); err != nil {
			_ = rows.Close()
			return afterRoot, false, err
		}
		var allowance factory.AttemptAllowance
		if err = json.Unmarshal(data, &allowance); err != nil {
			_ = rows.Close()
			return afterRoot, false, err
		}
		if err = validateAttemptAllowanceRow(allowance, repository, issue, root, revision); err != nil {
			_ = rows.Close()
			return afterRoot, false, err
		}
		locked = append(locked, allowance)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return afterRoot, false, err
	}
	if err = rows.Close(); err != nil {
		return afterRoot, false, err
	}
	if len(locked) == 0 {
		return afterRoot, true, nil
	}
	for _, allowance := range locked {
		afterRoot = allowance.RootAssignment
		if !allowance.Active {
			continue
		}
		next, err := allowance.Checkpoint(now, false)
		if err != nil {
			return afterRoot, false, err
		}
		next.Revision++
		if err = saveAttemptAllowanceTx(ctx, tx, next); err != nil {
			return afterRoot, false, err
		}
	}
	return afterRoot, false, nil
}

func (s *Store) FreezeAttemptAllowances(ctx context.Context, repository, expectedGateRevision int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = admitFreezeGateTx(ctx, tx, repository, expectedGateRevision); err != nil {
		return err
	}
	if err = admitFreezeRunsSettledTx(ctx, tx, repository); err != nil {
		return err
	}
	const pageLimit = 128
	afterRoot := ""
	for {
		next, done, err := freezeAllowancePageTx(ctx, tx, repository, afterRoot, pageLimit, now)
		if err != nil {
			return err
		}
		afterRoot = next
		if done {
			break
		}
	}
	return tx.Commit()
}

// admitAttemptAllowanceTx checkpoints active time and ensures the exact run
// deadline fits the remaining recorded allowance. limits is supplied only
// when the packet may create the issue's initial allowance.
func admitAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, repository, issue int64, assignment string, limits *factory.AttemptLimits, deadline, now time.Time) error {
	if !deadline.After(now) {
		return factory.ErrAttemptTimeExhausted
	}
	allowance, err := lockAttemptAllowanceTx(ctx, tx, repository, issue)
	if errors.Is(err, sql.ErrNoRows) {
		if limits == nil {
			return factory.ErrAttemptTimeExhausted
		}
		return insertAttemptAllowanceTx(ctx, tx, repository, issue, assignment, *limits, deadline, now)
	}
	if err != nil {
		return err
	}
	if allowance.RootAssignment != assignment {
		return ErrAttemptRootMismatch
	}
	if allowance.Closed {
		return factory.ErrAttemptClosed
	}
	next, err := allowance.Checkpoint(now, true)
	if err != nil {
		return err
	}
	remaining := next.RemainingSeconds(now)
	if remaining == 0 || deadline.After(time.Unix(now.Unix()+remaining, 0)) {
		return factory.ErrAttemptTimeExhausted
	}
	if next.ActiveSeconds != allowance.ActiveSeconds || next.CheckpointUnix != allowance.CheckpointUnix || next.Active != allowance.Active {
		next.Revision++
		if err = saveAttemptAllowanceTx(ctx, tx, next); err != nil {
			return err
		}
	}
	return nil
}

// validateIntentRootAssignment checks the allowance-time shape for a
// private-intent planning cycle: the repository slot with Issue zero, a
// fresh attempt root, the decompose phase with the coder role, no parent
// and no publication assignment, and the stable intent scope (IntentID
// plus OwnerID). Mutable assignment snapshots such as RevisionID and
// ScopeDigest are deliberately not part of the scope: the allowance
// binds the stable association while the dispatch guard pins the exact
// persisted head revision.
func validateIntentRootAssignment(a factory.Assignment, repository int64, scope factory.IntentAttemptScope, root string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if repository <= 0 || !factory.ValidID(root) {
		return errors.New("invalid intent allowance scope")
	}
	if a.ID != root || a.AttemptRoot != root || a.Repository != repository || a.Issue != 0 {
		return errors.New("intent root does not match its allowance scope")
	}
	if a.Phase != project.ExecutionDecompose || a.Role != project.RoleCoder {
		return errors.New("intent root needs its decompose coder binding")
	}
	if a.ParentAssignment != "" || a.PublicationAssignment != "" {
		return errors.New("intent planning is a fresh attempt root")
	}
	if a.Subject.Kind != factory.SubjectIntent || a.Subject.Intent == nil {
		return errors.New("intent root lacks its intent subject")
	}
	recorded := *a.Subject.Intent
	if recorded.IntentID != scope.IntentID || recorded.OwnerID != scope.OwnerID {
		return errors.New("intent root does not match its intent scope")
	}
	return nil
}

// sameIntentRootScope reports whether stored and next describe the same
// intent root: identity, scope, phase, role, parentage and the identical
// intent subject snapshot.
func sameIntentRootScope(stored, next factory.Assignment) bool {
	if stored.ID != next.ID || stored.AttemptRoot != next.AttemptRoot ||
		stored.Repository != next.Repository || stored.Issue != next.Issue ||
		stored.Phase != next.Phase || stored.Role != next.Role ||
		stored.ParentAssignment != next.ParentAssignment ||
		stored.PublicationAssignment != next.PublicationAssignment ||
		stored.Subject.Kind != next.Subject.Kind {
		return false
	}
	if stored.Subject.Intent == nil || next.Subject.Intent == nil {
		return stored.Subject.Intent == next.Subject.Intent
	}
	return *stored.Subject.Intent == *next.Subject.Intent
}

// refuseDuplicateIntentAttemptTx enforces the one-funding rule for an
// intent operation across roots inside the packet transaction. It locks
// the intent head row so concurrent same-intent admissions serialize,
// revalidates the incoming revision against the locked head (a revision
// committed after the guard's unlocked read refuses as stale), then
// binds every other recorded root allowance with the same intent scope
// to its root assignment: an identical intent/revision/owner triple on
// another root refuses instead of refilling, whatever caller-supplied
// snapshot variation the subjects carry. An active prior reports the
// concurrent admission; any settled prior keeps its consumption and
// requires an explicit maintainer Retry. A new revision admits: its
// triple differs.
func refuseDuplicateIntentAttemptTx(ctx context.Context, tx *sql.Tx, repository int64, root string, intent *factory.IntentSubject) error {
	var head, headRevision string
	var headOwner int64
	if err := tx.QueryRowContext(ctx, `SELECT id, revision, owner_id FROM factory_intent_heads WHERE id=$1 FOR UPDATE`, intent.IntentID).Scan(&head, &headRevision, &headOwner); err != nil {
		// No head row means the caller bypassed the decompose guard
		// (direct test construction): the cross-root binding is
		// defined over guarded admissions, so there is nothing to
		// bind and the insert keeps its existing behavior.
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	// Authoritative revalidation under the head lock: the guard read
	// this head unlocked, so a revision committed afterwards must
	// refuse here rather than admit stale.
	if headOwner != intent.OwnerID || headRevision != intent.RevisionID {
		return fmt.Errorf("%w: intent root answers a superseded head", ErrAttemptRootMismatch)
	}
	rows, err := tx.QueryContext(ctx, `SELECT root_assignment, data FROM factory_attempt_allowances WHERE repository=$1 AND issue=0 AND root_assignment<>$2`, repository, root)
	if err != nil {
		return err
	}
	type priorAllowance struct {
		root      string
		allowance factory.AttemptAllowance
	}
	var priors []priorAllowance
	for rows.Next() {
		var prior priorAllowance
		var raw []byte
		if err := rows.Scan(&prior.root, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &prior.allowance); err != nil {
			_ = rows.Close()
			return err
		}
		priors = append(priors, prior)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	// Drain the row set before any follow-up query: the packet
	// transaction holds one connection, which stays busy until the
	// rows close.
	_ = rows.Close()
	for _, prior := range priors {
		if prior.allowance.Intent == nil || prior.allowance.Intent.IntentID != intent.IntentID || prior.allowance.Intent.OwnerID != intent.OwnerID {
			continue
		}
		priorAssignment, err := loadAttemptRootAssignmentTx(ctx, tx, prior.root, false)
		if err != nil {
			return err
		}
		// The binding is the funded operation triple, not the
		// caller-supplied snapshot: scope-digest variation never
		// redefines it.
		priorSubject := priorAssignment.Subject.Intent
		if priorSubject == nil || priorSubject.IntentID != intent.IntentID ||
			priorSubject.RevisionID != intent.RevisionID || priorSubject.OwnerID != intent.OwnerID {
			continue
		}
		if prior.allowance.Active {
			return ErrAssignmentActive
		}
		// Only the exact newly journaled source continuation may fund a fresh
		// planning root. Historical failed roots and charges remain immutable.
		incoming, err := loadAttemptRootAssignmentTx(ctx, tx, root, false)
		if err != nil {
			return err
		}
		if incoming.PlanningContext == nil || incoming.PlanningContext.IssueMaker == nil {
			return factory.ErrAttemptClosed
		}
		scope := *incoming.PlanningContext.IssueMaker
		if scope.Attempt != root || !factory.ValidID(scope.Attempt) {
			return factory.ErrAttemptClosed
		}
		if allowed, err := issueMakerConsentTx(ctx, tx, incoming); err != nil {
			return err
		} else if !allowed {
			return factory.ErrAttemptClosed
		}
		if priorAssignment.Stage != factory.AssignmentFinished || (priorAssignment.Outcome != factory.Failed && priorAssignment.Outcome != factory.Cancelled) {
			return factory.ErrAttemptClosed
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1`, priorAssignment.Run).Scan(&raw); err != nil {
			return err
		}
		var run factory.Run
		if json.Unmarshal(raw, &run) != nil || !run.Reconciled {
			return ErrAttemptRunsPending
		}
	}
	return nil
}

// insertIntentAttemptAllowanceTx records the time-only allowance for one
// intent decompose root. The exact root assignment row must already be
// recorded and must match the passed assignment on every scope field;
// minutes is the admitted policy horizon (no omission fill: zero is the
// valid unfunded posture and refuses here, funding enforces the
// finite-positive bound). Replaying the same root with the same admitted
// minutes returns without refill; a recorded row with different admitted
// minutes or a different intent scope conflicts instead of overwriting.
// This constructor performs no dispatch-gate, owner or head-revision
// admission: the packet guard owns those locks.
func admitIntentRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, scope factory.IntentAttemptScope) error {
	if err := validateIntentRootAssignment(a, a.Repository, scope, a.ID); err != nil {
		return err
	}
	stored, err := loadAttemptRootAssignmentTx(ctx, tx, a.ID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("intent root assignment is not recorded")
	}
	if err != nil {
		return err
	}
	if err = validateIntentRootAssignment(stored, a.Repository, scope, a.ID); err != nil {
		return err
	}
	if !sameIntentRootScope(stored, a) {
		return errors.New("intent root assignment does not match its recorded row")
	}
	return nil
}

func replayIntentAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, scope factory.IntentAttemptScope, minutes int) (bool, error) {
	var existingData []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE repository=$1 AND issue=0 AND root_assignment=$2`, a.Repository, a.ID).Scan(&existingData)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var existing factory.AttemptAllowance
	if err = json.Unmarshal(existingData, &existing); err != nil {
		return false, err
	}
	if err = validateAttemptAllowanceRow(existing, a.Repository, 0, a.ID, existing.Revision); err != nil {
		return false, err
	}
	if existing.Intent == nil || *existing.Intent != scope ||
		existing.Limits.ActiveMinutes != minutes {
		return false, errors.New("intent allowance does not match its recorded root")
	}
	return true, nil
}

func insertIntentAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, minutes int, deadline, now time.Time) error {
	if minutes < 1 || int64(minutes) > math.MaxInt64/int64(time.Minute) {
		return errors.New("intent minutes outside their bound")
	}
	if a.Subject.Kind != factory.SubjectIntent || a.Subject.Intent == nil {
		return errors.New("intent root lacks its intent subject")
	}
	scope := factory.IntentAttemptScope{IntentID: a.Subject.Intent.IntentID, OwnerID: a.Subject.Intent.OwnerID}
	if err := admitIntentRootTx(ctx, tx, a, scope); err != nil {
		return err
	}
	replayed, err := replayIntentAllowanceTx(ctx, tx, a, scope, minutes)
	if err != nil || replayed {
		return err
	}
	// Cross-root operation binding (R11 retry contract): the same intent
	// operation funds exactly once across roots. A prior allowance for the
	// identical intent/revision/owner triple on another root refuses
	// instead of refilling: an active prior means a concurrent attempt is
	// still running (ErrAssignmentActive, surfaced as a wait), a settled
	// prior keeps its consumption (factory.ErrAttemptClosed, surfaced as
	// the explicit-Retry wait). Same-root replay stays idempotent above;
	// a new revision admits below. The intent head row locks first so
	// concurrent same-intent admissions serialize on one row.
	if err := refuseDuplicateIntentAttemptTx(ctx, tx, a.Repository, a.ID, a.Subject.Intent); err != nil {
		return err
	}
	allowance := factory.AttemptAllowance{
		Limits: factory.AttemptLimits{ActiveMinutes: minutes}, Intent: &scope,
		RootAssignment: a.ID, Repository: a.Repository, Issue: 0,
		CheckpointUnix: now.Unix(), Active: true,
	}
	if err := allowance.Validate(); err != nil {
		return err
	}
	remaining := allowance.RemainingSeconds(now)
	if !deadline.After(now) || remaining == 0 || deadline.After(time.Unix(now.Unix()+remaining, 0)) {
		return factory.ErrAttemptTimeExhausted
	}
	allowance = normalizeAllowanceSlices(allowance)
	data, err := json.Marshal(allowance)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`,
		a.Repository, 0, a.ID, allowance.Revision, string(data))
	return err
}

// consumeCorrectionAttemptTx charges a correction child before its dispatch
// packet commits. The root row is locked and matched to the immutable child
// binding; replays of the same assignment are idempotent.
func consumeCorrectionAttemptTx(ctx context.Context, tx *sql.Tx, assignment factory.Assignment, now time.Time) error {
	allowance, err := lockAttemptAllowanceTx(ctx, tx, assignment.Repository, assignment.Issue)
	if err != nil {
		return err
	}
	if allowance.RootAssignment != assignment.AttemptRoot {
		return ErrAttemptRootMismatch
	}
	if allowance.Closed {
		return factory.ErrAttemptClosed
	}
	next, consumed, err := allowance.ConsumeCorrection(assignment.ID, now)
	if err != nil {
		return err
	}
	if consumed || next.ActiveSeconds != allowance.ActiveSeconds || next.CheckpointUnix != allowance.CheckpointUnix || next.Active != allowance.Active {
		next.Revision++
		return saveAttemptAllowanceTx(ctx, tx, next)
	}
	return nil
}

// ChargeReviewDispatch charges one independent reviewer dispatch against
// the attempt-root review allowance before its child packet commits, in
// one row-locked transaction. The charge keys on the immutable reviewer
// assignment identity: replays of the same assignment are idempotent and
// never consume a second round or pass, even after exhaustion. The first
// inspection of a new head consumes one ReviewRound but never the
// unchanged-head extra pass; a discretionary re-review of an inspected
// head additionally spends that head's single extra pass; a
// policy-required review consumes one ReviewRound and stays separately
// caused (D08).
//
// It reports (consumed=true) only when this call spent budget. A replay
// of an already charged assignment reports (consumed=false,
// exhausted=false) so the caller still dispatches. An exhausted, closed
// or time-spent root reports (consumed=false, exhausted=true) without an
// error so the caller waits with review_allowance_exhausted instead of
// launching a run it cannot cover.
func (s *Store) ChargeReviewDispatch(ctx context.Context, repository, issue int64, root, reviewerAssignment, head string, policy bool) (consumed, exhausted bool, err error) {
	if repository <= 0 || issue <= 0 || !factory.ValidID(root) || !factory.ValidID(reviewerAssignment) || !factory.ValidCommit(head) {
		return false, false, errors.New("invalid review dispatch charge")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, repository, issue, root)
	if err != nil {
		return false, false, err
	}
	if allowance.Closed {
		return false, true, nil
	}
	now := time.Now()
	next, spent, err := allowance.ConsumeReview(reviewerAssignment, head, policy, now)
	if err != nil {
		if errors.Is(err, factory.ErrReviewRoundsExhausted) || errors.Is(err, factory.ErrUnchangedHeadExtraExhausted) ||
			errors.Is(err, factory.ErrAttemptTimeExhausted) {
			return false, true, nil
		}
		return false, false, err
	}
	if !spent {
		return false, false, tx.Commit()
	}
	next.Revision++
	if err = saveAttemptAllowanceTx(ctx, tx, next); err != nil {
		return false, false, err
	}
	return true, false, tx.Commit()
}

// startFreshAttemptAllowanceTx is called only after the explicit retry
// command and prior run accounting have been rechecked under the gate.
func startFreshAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, repository, issue int64, assignment string, limits factory.AttemptLimits, deadline, now time.Time) error {
	prior, err := lockAttemptAllowanceTx(ctx, tx, repository, issue)
	if err != nil {
		return err
	}
	next, err := prior.Checkpoint(now, false)
	if err != nil {
		return err
	}
	next.Revision++
	next.Closed = true
	if err = saveAttemptAllowanceTx(ctx, tx, next); err != nil {
		return err
	}
	return insertAttemptAllowanceTx(ctx, tx, repository, issue, assignment, limits, deadline, now)
}

func insertAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, repository, issue int64, assignment string, limits factory.AttemptLimits, deadline, now time.Time) error {
	allowance := factory.AttemptAllowance{
		Limits: limits, RootAssignment: assignment, Repository: repository,
		Issue: issue, CheckpointUnix: now.Unix(), Active: true,
	}
	if err := allowance.Validate(); err != nil {
		return err
	}
	remaining := allowance.RemainingSeconds(now)
	if !deadline.After(now) || remaining == 0 || deadline.After(time.Unix(now.Unix()+remaining, 0)) {
		return factory.ErrAttemptTimeExhausted
	}
	allowance = normalizeAllowanceSlices(allowance)
	data, err := json.Marshal(allowance)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`,
		repository, issue, assignment, allowance.Revision, string(data))
	return err
}

// AdmitDeliveryJudgment checks the open attempt's actual remaining allowance.
func (s *Store) AdmitDeliveryJudgment(ctx context.Context, repository, issue int64, root string) (bool, error) {
	if repository <= 0 || issue <= 0 || !factory.ValidID(root) {
		return false, errors.New("invalid delivery scope")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, repository, issue, root)
	if err != nil {
		return false, err
	}
	if allowance.Closed || allowance.Intent != nil || allowance.Completion != nil || allowance.RemainingSeconds(time.Now()) == 0 {
		return false, nil
	}
	return true, tx.Commit()
}

// RecordDeliveryJudgment stores a completed decision without a placeholder.
// A completed decision for the same input key cannot be overwritten.
func (s *Store) RecordDeliveryJudgment(ctx context.Context, r factory.DeliveryReceipt) error {
	return s.recordJudgment(ctx, r.Fingerprint, r)
}

// DeliveryJudgment reads one recorded delivery receipt by its evidence
// fingerprint. Missing fingerprints and non-delivery journal rows
// report ErrNotFound.
func (s *Store) DeliveryJudgment(ctx context.Context, fingerprint string) (factory.DeliveryReceipt, error) {
	var r factory.DeliveryReceipt
	if !factory.ValidDigest(fingerprint) {
		return r, errors.New("invalid delivery fingerprint")
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_priority_judgments WHERE snapshot=$1 AND data IS NOT NULL`, fingerprint).Scan(&raw)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if r.Kind != factory.DeliveryReceiptKind || r.Fingerprint != fingerprint {
		return factory.DeliveryReceipt{}, ErrNotFound
	}
	return r, nil
}

// validateIntentScope checks the stable intent scope identity carried on
// one time-only intent allowance: the project decision identity of the
// planned intent and its positive owner. Mutable assignment snapshots
// such as RevisionID and ScopeDigest never appear here.
func validateIntentScope(scope factory.IntentAttemptScope) error {
	if !project.ValidDecisionID(scope.IntentID) {
		return errors.New("invalid intent scope identity")
	}
	if scope.OwnerID <= 0 {
		return errors.New("invalid intent scope owner")
	}
	return nil
}

func lockIntentAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, repository int64, scope factory.IntentAttemptScope, root string) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	if repository <= 0 || !factory.ValidID(root) {
		return allowance, errors.New("invalid intent allowance scope")
	}
	if err := validateIntentScope(scope); err != nil {
		return allowance, err
	}
	var storedRoot string
	var revision int64
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT root_assignment,revision,data FROM factory_attempt_allowances WHERE repository=$1 AND issue=0 AND root_assignment=$2 FOR UPDATE`, repository, root).Scan(&storedRoot, &revision, &data)
	if err != nil {
		return allowance, err
	}
	if err = json.Unmarshal(data, &allowance); err != nil {
		return allowance, err
	}
	if err = validateAttemptAllowanceRow(allowance, repository, 0, storedRoot, revision); err != nil {
		return factory.AttemptAllowance{}, err
	}
	if allowance.Intent == nil || *allowance.Intent != scope {
		return factory.AttemptAllowance{}, errors.New("intent allowance does not match its intent scope")
	}
	return allowance, nil
}

// IntentAttemptAllowance returns the recorded time-only allowance for
// one exact intent root. The root assignment row must still match the
// requested scope; a wrong root, intent or owner refuses, as does any
// positive-issue spoof of the same root.
func (s *Store) IntentAttemptAllowance(ctx context.Context, repository int64, scope factory.IntentAttemptScope, root string) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	if repository <= 0 || !factory.ValidID(root) {
		return allowance, errors.New("invalid intent allowance scope")
	}
	if err := validateIntentScope(scope); err != nil {
		return allowance, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return allowance, err
	}
	defer func() { _ = tx.Rollback() }()
	allowance, err = lockIntentAttemptAllowanceTx(ctx, tx, repository, scope, root)
	if err != nil {
		return factory.AttemptAllowance{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.AttemptAllowance{}, err
	}
	return allowance, nil
}
