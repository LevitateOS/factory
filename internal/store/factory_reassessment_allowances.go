package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// This file owns typed completion-root ACTIVE TIME accounting (D13/FW32):
// one time-only attempt allowance per verified reassessment cycle, recorded
// in the existing factory_attempt_allowances table. There is no second
// ledger, no new table or column, and no public standalone spending
// shortcut. The private constructor below grants no admission itself: the
// future dispatch packet owns the dispatch-gate and current-cycle locks
// and calls it inside its own transaction. Generic ROOT-scoped
// checkpoint, freeze and close paths consume the recorded allowance
// through the shared joints in factory_attempt_allowances.go, which
// validate its immutable scope and admitted limits on every CAS save.

// validateReassessmentSubject rejects malformed completion identities
// before any storage read.
func validateReassessmentSubject(subject factory.CompletionSubject) error {
	if !factory.ValidID(subject.CompletionID) || !factory.ValidID(subject.CycleID) {
		return errors.New("invalid completion subject identity")
	}
	if !factory.ValidDigest(subject.InventoryDigest) {
		return errors.New("invalid completion subject inventory digest")
	}
	return nil
}

// validateReassessmentRootAssignment checks the exact root assignment shape
// for a completion reassessment cycle: the completed repository slot with
// Issue zero, a fresh attempt root, the reassess phase with the coder role,
// no parent and no publication assignment, and the identical completion
// subject IDs and inventory digest.
func validateReassessmentRootAssignment(a factory.Assignment, repository int64, subject factory.CompletionSubject, root string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if repository <= 0 || !factory.ValidID(root) {
		return errors.New("invalid reassessment allowance scope")
	}
	if a.ID != root || a.AttemptRoot != root || a.Repository != repository || a.Issue != 0 {
		return errors.New("reassessment root does not match its allowance scope")
	}
	if a.Phase != project.ExecutionReassess || a.Role != project.RoleCoder {
		return errors.New("reassessment root needs its reassess coder binding")
	}
	if a.ParentAssignment != "" || a.PublicationAssignment != "" {
		return errors.New("reassessment cycle is a fresh attempt root")
	}
	if a.Subject.Kind != factory.SubjectCompletion || a.Subject.Completion == nil {
		return errors.New("reassessment root lacks its completion subject")
	}
	recorded := *a.Subject.Completion
	if recorded.CompletionID != subject.CompletionID || recorded.CycleID != subject.CycleID ||
		recorded.InventoryDigest != subject.InventoryDigest {
		return errors.New("reassessment root does not match its completion subject")
	}
	return nil
}

// sameReassessmentRootScope reports whether stored and next describe the
// same reassessment root: identity, scope, phase, role, parentage and the
// identical completion subject.
func sameReassessmentRootScope(stored, next factory.Assignment) bool {
	if stored.ID != next.ID || stored.AttemptRoot != next.AttemptRoot ||
		stored.Repository != next.Repository || stored.Issue != next.Issue ||
		stored.Phase != next.Phase || stored.Role != next.Role ||
		stored.ParentAssignment != next.ParentAssignment ||
		stored.PublicationAssignment != next.PublicationAssignment ||
		stored.Subject.Kind != next.Subject.Kind {
		return false
	}
	if stored.Subject.Completion == nil || next.Subject.Completion == nil {
		return stored.Subject.Completion == nil && next.Subject.Completion == nil
	}
	return *stored.Subject.Completion == *next.Subject.Completion
}

// loadAttemptRootAssignmentTx reads the exact attempt-root assignment
// row. It is shared by both Issue-zero time-only alternatives
// (completion reassessment and private intent): it performs no subject
// check, the caller validates the locked row. Callers hold or take the
// row lock; there is no latest-per-repository fallback for these roots.
func loadAttemptRootAssignmentTx(ctx context.Context, tx *sql.Tx, root string, lock bool) (factory.Assignment, error) {
	var a factory.Assignment
	query := `SELECT data FROM factory_assignments WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, root).Scan(&raw); err != nil {
		return a, err
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, err
	}
	return a, nil
}

// lockReassessmentAttemptAllowanceTx locks the exact completion-root
// allowance row and rechecks it against its root assignment row: same
// repository, Issue zero, fresh root, reassess phase, coder role, no
// parent or publication, and the identical completion subject IDs and
// digest. A mismatched current root or scope refuses; there is no
// current or latest (repository, 0) fallback.
func lockReassessmentAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, repository int64, subject factory.CompletionSubject, root string) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	if repository <= 0 || !factory.ValidID(root) {
		return allowance, errors.New("invalid reassessment allowance scope")
	}
	if err := validateReassessmentSubject(subject); err != nil {
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
	if allowance.Completion == nil ||
		allowance.Completion.CompletionID != subject.CompletionID ||
		allowance.Completion.CycleID != subject.CycleID ||
		allowance.Completion.InventoryDigest != subject.InventoryDigest {
		return factory.AttemptAllowance{}, errors.New("reassessment allowance does not match its completion subject")
	}
	stored, err := loadAttemptRootAssignmentTx(ctx, tx, root, false)
	if err != nil {
		return factory.AttemptAllowance{}, err
	}
	if err = validateReassessmentRootAssignment(stored, repository, subject, root); err != nil {
		return factory.AttemptAllowance{}, err
	}
	return allowance, nil
}

// refuseDuplicateReassessmentAttemptTx enforces the one-funding rule for
// a completion operation across roots inside the packet transaction. The
// reassess guard already holds the reassessment cycle row lock, so
// concurrent same-cycle admissions serialize before this check. A prior
// allowance for the identical completion subject on another root refuses
// instead of refilling: an active prior reports the concurrent admission,
// any settled prior keeps its consumption and requires an explicit
// maintainer Retry. Same-root replay stays idempotent in the caller; a
// new cycle admits below.
func refuseDuplicateReassessmentAttemptTx(ctx context.Context, tx *sql.Tx, repository int64, root string, subject factory.CompletionSubject) error {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE repository=$1 AND issue=0 AND root_assignment<>$2`, repository, root)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var prior factory.AttemptAllowance
		if err := json.Unmarshal(raw, &prior); err != nil {
			return err
		}
		if prior.Completion == nil || *prior.Completion != subject {
			continue
		}
		if prior.Active {
			return ErrAssignmentActive
		}
		return factory.ErrAttemptClosed
	}
	return rows.Err()
}

// insertReassessmentAttemptAllowanceTx records the time-only allowance for
// one completion reassessment root. The exact root assignment row must
// already be recorded and must match the passed assignment on every scope
// field; minutes resolves omission to the admitted default and must land
// in the 1..120 bound. Replaying the same root returns its unchanged
// consumption without refill; a recorded row with different admitted
// minutes or a different completion subject conflicts instead of
// overwriting. This constructor performs no dispatch-gate, owner or
// current-cycle admission: the future packet owns those locks.
func admitReassessmentRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, subject factory.CompletionSubject) error {
	if err := validateReassessmentRootAssignment(a, a.Repository, subject, a.ID); err != nil {
		return err
	}
	stored, err := loadAttemptRootAssignmentTx(ctx, tx, a.ID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("reassessment root assignment is not recorded")
	}
	if err != nil {
		return err
	}
	if err = validateReassessmentRootAssignment(stored, a.Repository, subject, a.ID); err != nil {
		return err
	}
	if !sameReassessmentRootScope(stored, a) {
		return errors.New("reassessment root assignment does not match its recorded row")
	}
	return nil
}

func replayReassessmentAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, subject factory.CompletionSubject, admitted int) (bool, error) {
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
	if existing.Completion == nil || *existing.Completion != subject ||
		existing.Limits.ActiveMinutes != admitted {
		return false, errors.New("reassessment allowance does not match its recorded root")
	}
	return true, nil
}

func insertReassessmentAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, minutes int, deadline, now time.Time) error {
	admitted := factory.EffectiveReassessmentMinutes(minutes)
	if admitted < factory.MinReassessmentMinutes || admitted > factory.MaxReassessmentMinutes {
		return errors.New("reassessment minutes outside their bound")
	}
	if a.Subject.Kind != factory.SubjectCompletion || a.Subject.Completion == nil {
		return errors.New("reassessment root lacks its completion subject")
	}
	subject := *a.Subject.Completion
	if err := admitReassessmentRootTx(ctx, tx, a, subject); err != nil {
		return err
	}
	replayed, err := replayReassessmentAllowanceTx(ctx, tx, a, subject, admitted)
	if err != nil || replayed {
		return err
	}
	// Cross-root operation binding (R11 retry contract): the same
	// completion operation funds exactly once across roots. See
	// refuseDuplicateIntentAttemptTx for the active/settled refusal
	// contract; the reassess guard's cycle-row lock serializes
	// concurrent admissions here.
	if err := refuseDuplicateReassessmentAttemptTx(ctx, tx, a.Repository, a.ID, subject); err != nil {
		return err
	}
	allowance := factory.AttemptAllowance{
		Limits: factory.AttemptLimits{ActiveMinutes: admitted}, Completion: &subject,
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
	data, err := json.Marshal(allowance)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`,
		a.Repository, 0, a.ID, allowance.Revision, string(data))
	return err
}

// ReassessmentAttemptAllowance returns the recorded time-only allowance
// for one exact completion root. The root assignment row must still match
// the requested scope and completion subject; a wrong root, event, cycle
// or digest refuses, as does any positive-issue spoof of the same root.
func (s *Store) ReassessmentAttemptAllowance(ctx context.Context, repository int64, subject factory.CompletionSubject, root string) (factory.AttemptAllowance, error) {
	var allowance factory.AttemptAllowance
	if repository <= 0 || !factory.ValidID(root) {
		return allowance, errors.New("invalid reassessment allowance scope")
	}
	if err := validateReassessmentSubject(subject); err != nil {
		return allowance, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return allowance, err
	}
	defer func() { _ = tx.Rollback() }()
	allowance, err = lockReassessmentAttemptAllowanceTx(ctx, tx, repository, subject, root)
	if err != nil {
		return factory.AttemptAllowance{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.AttemptAllowance{}, err
	}
	return allowance, nil
}
