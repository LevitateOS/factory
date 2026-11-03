package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// RecordRetryPacket advances one assigned assignment to its next run
// identity and records the attempt's run, display binding and held
// capacity atomically. Attempts are bounded; an exhausted assignment
// refuses with ErrAssignmentActive so the caller finishes it instead of
// launching. The expected revision guards concurrent retries: only the
// first packet at this revision records. Admission limits and fresh authority
// are rechecked inside the same transaction, so a retry never spends room a
// concurrent admission consumed.
// admitRetryPacket refuses retry packets with invalid runs, mismatched
// views or non-fresh runs before any transaction opens.
func admitRetryPacket(a factory.Assignment, run factory.Run, view factory.RunView) error {
	if err := run.Validate(); err != nil {
		return err
	}
	if err := view.Validate(); err != nil {
		return err
	}
	if run.ID == "" || run.ProjectID != a.ProjectID || run.Role != a.Role || run.InputSHA != a.SourceCommit ||
		view.RunID != run.ID || view.Repository != a.Repository || view.Issue != a.Issue || view.Attempt != a.ID {
		return errors.New("retry packet view does not match its attempt")
	}
	if run.Outcome != "" || run.Reconciled {
		return errors.New("retry packet carries a fresh run")
	}
	return nil
}

// checkLockedRetryAssignment refuses retries against stale, moved or
// no-longer-assignable attempts, including reused run identities. A retry is
// a new provider process: the calling snapshot may advance the recorded
// harness pin to the current selection, but every other binding stays exact.
func checkLockedRetryAssignment(current, a factory.Assignment, run factory.Run) error {
	if current.Revision != a.Revision {
		return ErrStaleRevision
	}
	if current.Repository != a.Repository || current.Issue != a.Issue || current.Run != a.Run || current.Connection != a.Connection || current.ActorID != a.ActorID ||
		run.ProjectID != current.ProjectID || run.Role != current.Role || run.InputSHA != current.SourceCommit {
		return ErrStaleRevision
	}
	if run.Model != a.Model || a.Model != current.Model {
		return ErrStaleRevision
	}
	// The new execution adopts the calling snapshot's pin: the run carries
	// it and the snapshot's pin is internally coherent. The stored pin may
	// lag behind; advancing it is the retry, never a revision conflict.
	if run.Harness != a.Harness {
		return ErrStaleRevision
	}
	if err := checkRetryPinAdvance(a); err != nil {
		return err
	}
	if a.AdoptedHarness != (project.FactoryHarnessPin{}) && run.Image != a.AdoptedHarness.Image {
		return ErrStaleRevision
	}
	if current.AttemptRoot != a.AttemptRoot || current.PublicationAssignment != a.PublicationAssignment {
		return ErrStaleRevision
	}
	if current.Stage != factory.AssignmentAssigned || current.Attempts >= factory.MaxDispatchAttempts {
		return ErrAssignmentActive
	}
	for _, seen := range current.RunHistory {
		if seen == run.ID {
			return errors.New("dispatch attempt reuses a run identity")
		}
	}
	return nil
}

// checkRetryPinAdvance requires the calling snapshot's pin to be internally
// coherent when it carries tool proof. Rows admitted before proof existed
// carry no pin and keep the legacy binding; advancing to a proved pin is
// the retry adopting current.
func checkRetryPinAdvance(a factory.Assignment) error {
	if a.AdoptedHarness == (project.FactoryHarnessPin{}) {
		return nil
	}
	if err := a.AdoptedHarness.Validate(); err != nil {
		return ErrStaleRevision
	}
	if a.Harness != a.AdoptedHarness.Harness+"-"+a.AdoptedHarness.Version || a.HarnessVers != a.AdoptedHarness.Version {
		return ErrStaleRevision
	}
	return nil
}

// checkLockedPreviousRun refuses retries when the previous run's
// admission policy changed under the attempt.
func checkLockedPreviousRun(previous factory.Run, current factory.Assignment, grants admissionGrants) error {
	if previous.Admission == nil || previous.Admission.Policy.TargetBranch != grants.policy.TargetBranch ||
		!slices.Equal(previous.Admission.Policy.Checks, grants.policy.Checks) ||
		previous.Admission.Policy.Roles[current.Role] != grants.policy.Roles[current.Role] ||
		previous.Admission.Profile == nil || grants.environment.Profile == nil || *previous.Admission.Profile != *grants.environment.Profile {
		return ErrAdmissionChanged
	}
	return nil
}

// admitLockedPreviousRunTx locks the previous run row, refuses changed
// admission policy and binds fresh authority to the retry run. The retry is
// a new execution, so its tool identity comes from the calling snapshot's
// current pin, never the stored assignment's lagging pin.
func admitLockedPreviousRunTx(ctx context.Context, tx *sql.Tx, current, retryA factory.Assignment, grants admissionGrants, authority factory.AuthorityRef, run *factory.Run) error {
	var previousData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, current.Run).Scan(&previousData); err != nil {
		return err
	}
	var previous factory.Run
	if err := json.Unmarshal(previousData, &previous); err != nil {
		return err
	}
	if err := checkLockedPreviousRun(previous, current, grants); err != nil {
		return err
	}
	tool, err := factory.AdoptedToolIdentity(retryA, run.Image)
	if err != nil {
		return err
	}
	run.Admission = &factory.RunAdmission{Authority: authority, Policy: grants.policy, Profile: grants.environment.Profile, Tool: &tool}
	return run.Validate()
}

// persistRetryAttemptTx writes the next attempt, its run row and its
// run view in lock order.
func persistRetryAttemptTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, next factory.Assignment, run factory.Run, view factory.RunView) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_assignments SET run=$1,revision=$2,data=$3 WHERE id=$4 AND stage='assigned'`,
		next.Run, next.Revision, string(data), a.ID); err != nil {
		return err
	}
	rundata, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,TRUE,FALSE,$2)`, run.ID, string(rundata)); err != nil {
		return fmt.Errorf("retry packet failed: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4)`,
		view.RunID, view.Repository, view.Issue, view.Attempt); err != nil {
		return fmt.Errorf("retry packet failed: %w", err)
	}
	return nil
}

// reholdReservationForRetryTx re-holds the released reservation behind
// one retry. A held reservation continues; anything else refuses.
func reholdReservationForRetryTx(ctx context.Context, tx *sql.Tx, assignmentID string, planned int) error {
	var reservation factory.Reservation
	var rdata []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_reservations WHERE assignment=$1 FOR UPDATE`, assignmentID).Scan(&rdata); err != nil {
		return err
	}
	if err := json.Unmarshal(rdata, &reservation); err != nil {
		return err
	}
	switch reservation.State {
	case factory.ReservationReleased:
		reservation.State, reservation.PlannedMinutes = factory.ReservationHeld, planned
		reservation.Revision++
		if err := reservation.Validate(); err != nil {
			return err
		}
		rehold, err := json.Marshal(reservation)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE factory_reservations SET state='held',data=$1 WHERE assignment=$2 AND state='released'`,
			string(rehold), assignmentID); err != nil {
			return fmt.Errorf("retry packet failed: %w", err)
		}
	case factory.ReservationHeld:
	default:
		return errors.New("reservation behind a retry is not held")
	}
	return nil
}

func (s *Store) RecordRetryPacket(ctx context.Context, a factory.Assignment, expected factory.IssueControl, authority factory.AuthorityRef, run factory.Run, view factory.RunView, planned int) (factory.Assignment, error) {
	if err := admitRetryPacket(a, run, view); err != nil {
		return factory.Assignment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Assignment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Match dispatch and grant-command lock order before reading the current
	// selected connection sponsorship and its budget.
	if _, _, _, err = ensureDispatchGateTx(ctx, tx, a.Repository); err != nil {
		return factory.Assignment{}, err
	}
	if err = checkQueuedControlTx(ctx, tx, expected, a.Repository, a.Issue, a.Acceptance); err != nil {
		return factory.Assignment{}, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, a.ID).Scan(&raw); err != nil {
		return factory.Assignment{}, err
	}
	var current factory.Assignment
	if err = json.Unmarshal(raw, &current); err != nil {
		return factory.Assignment{}, err
	}
	if err = checkLockedRetryAssignment(current, a, run); err != nil {
		return factory.Assignment{}, err
	}
	// D13 completion fence (FW31): the gate lock serializes this with
	// completion insertion.
	if err = checkCompletionFenceTx(ctx, tx, current.Repository); err != nil {
		return factory.Assignment{}, err
	}
	if err = validateAttemptPacketTx(ctx, tx, current, false); err != nil {
		return factory.Assignment{}, err
	}
	grants, err := loadAdmissionGrantsTx(ctx, tx, current.Repository, current.Connection)
	if err != nil {
		return factory.Assignment{}, err
	}
	if err = checkAssignmentAuthorityTx(ctx, tx, authority, current, grants); err != nil {
		return factory.Assignment{}, err
	}
	if run.Native != nil {
		return factory.Assignment{}, ErrAdmissionChanged
	}
	if err = checkApplianceRunPreparationTx(ctx, tx, current, true); err != nil {
		return factory.Assignment{}, err
	}
	if err = admitLockedPreviousRunTx(ctx, tx, current, a, grants, authority, &run); err != nil {
		return factory.Assignment{}, err
	}
	next := current
	next.Attempts++
	next.Revision++
	next.Run = run.ID
	next.RunHistory = append(append([]string(nil), current.RunHistory...), run.ID)
	// The new execution adopts the calling snapshot's current pin; every
	// other binding stays the stored one.
	next.Harness = a.Harness
	next.HarnessVers = a.HarnessVers
	next.AdoptedHarness = a.AdoptedHarness
	if err = next.Validate(); err != nil {
		return factory.Assignment{}, err
	}
	if err = persistRetryAttemptTx(ctx, tx, a, next, run, view); err != nil {
		return factory.Assignment{}, err
	}
	if err = reholdReservationForRetryTx(ctx, tx, a.ID, planned); err != nil {
		return factory.Assignment{}, err
	}
	if err = admitAttemptAllowanceTx(ctx, tx, a.Repository, a.Issue, current.AttemptRoot, nil, run.Deadline, time.Now()); err != nil {
		return factory.Assignment{}, err
	}
	if err = checkAdmissionLimitsTx(ctx, tx, current.Repository, current.Connection, current.ProjectID, grants); err != nil {
		return factory.Assignment{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.Assignment{}, err
	}
	return next, nil
}
