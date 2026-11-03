package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func retryTestRun(t *testing.T, a factory.Assignment, run factory.Run) (factory.Run, factory.RunView) {
	t.Helper()
	id := factory.NewID()
	next := factory.Run{
		ID: id, ProjectID: a.ProjectID, Role: a.Role, InputSHA: run.InputSHA,
		Started: run.Started, Deadline: run.Deadline,
		Image: run.Image, Harness: run.Harness, Model: run.Model,
	}
	return next, factory.RunView{RunID: id, Repository: a.Repository, Issue: a.Issue, Attempt: a.ID}
}

func TestRecordRetryPacketBoundsAttemptsAndFinishes(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatal(err)
	}
	expected := dispatchTestControlFor(t, db, a)
	settle := func(id string) {
		t.Helper()
		stored, err := db.FactoryRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		stored.Outcome, stored.Reconciled, stored.Summary = factory.Failed, true, "superseded"
		if err := db.SaveFactoryRun(ctx, stored); err != nil {
			t.Fatal(err)
		}
	}
	second, secondView := retryTestRun(t, a, run)
	changed := expected
	changed.NativeRev++
	changed.Fingerprint = "3" + changed.Fingerprint[1:]
	if _, _, err := db.RecordIssueAssessment(ctx, changed, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordRetryPacket(ctx, a, expected, a.Authority, second, secondView, 30); !errors.Is(err, ErrDispatchControlStale) {
		t.Fatalf("stale control retry error = %v", err)
	}
	if _, err := db.FactoryRun(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale retry left a run: %v", err)
	}
	currentControl, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	expected = currentControl
	settle(run.ID)
	next, err := db.RecordRetryPacket(ctx, a, expected, a.Authority, second, secondView, 30)
	if err != nil || next.Attempts != 2 || next.Run != second.ID || len(next.RunHistory) != 2 {
		t.Fatalf("attempt not recorded: %+v %v", next, err)
	}
	if _, err = db.FactoryRun(ctx, second.ID); err != nil {
		t.Fatalf("retry run missing: %v", err)
	}
	if _, err = db.RecordRetryPacket(ctx, a, expected, a.Authority, second, secondView, 30); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale retry accepted: %v", err)
	}
	if _, err = db.RecordRetryPacket(ctx, next, expected, next.Authority, second, secondView, 30); err == nil {
		t.Fatal("reused run identity accepted")
	}
	// Recovery settles the superseded run before the next attempt, so it
	// stops counting against capacity.
	settle(second.ID)
	third, thirdView := retryTestRun(t, a, run)
	current, err := db.RecordRetryPacket(ctx, next, expected, next.Authority, third, thirdView, 30)
	if err != nil {
		t.Fatalf("third attempt refused: %v", err)
	}
	fourth, fourthView := retryTestRun(t, a, run)
	if _, err = db.RecordRetryPacket(ctx, current, expected, current.Authority, fourth, fourthView, 30); !errors.Is(err, ErrAssignmentActive) {
		t.Fatalf("fourth attempt accepted: %v", err)
	}
	finished, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
	finished.FinishedUnix = now.Add(time.Hour).Unix()
	result := factory.ResultSynthesized(finished.ID, finished.Run, "failed", "run failed", finished.FinishedUnix)
	finished.Result = &result
	if err = db.FinishAssignment(ctx, finished); err != nil {
		t.Fatalf("finish refused: %v", err)
	}
	if err = db.FinishAssignment(ctx, finished); err == nil {
		t.Fatal("second finish accepted")
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil || stored.Stage != factory.AssignmentFinished {
		t.Fatalf("finished assignment wrong: %+v %v", stored, err)
	}
}

func TestRecordRetryPacketPreservesExecutionActor(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, time.Now().UTC())
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	previous, err := db.FactoryRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous.Outcome, previous.Reconciled, previous.Summary = factory.Failed, true, "released for retry"
	if err := db.SaveFactoryRun(ctx, previous); err != nil {
		t.Fatal(err)
	}
	sponsorship, err := db.Sponsorship(ctx, a.Repository, a.Connection)
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.ActorID++
	if err := db.SaveSponsorship(ctx, sponsorship); err != nil {
		t.Fatal(err)
	}
	control, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	authority := a.Authority
	authority.Sponsorship++
	retry, retryView := retryTestRun(t, a, run)
	if _, err := db.RecordRetryPacket(ctx, a, control, authority, retry, retryView, 30); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatalf("retry changed the admitted execution actor: %v", err)
	}
	if _, err := db.FactoryRun(ctx, retry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected actor change left a run: %v", err)
	}
}

func TestRecordRetryPacketRefusesAnotherActiveRepositorySession(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, time.Now().UTC())
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	expected, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	retry, retryView := retryTestRun(t, a, run)
	if _, err := db.RecordRetryPacket(ctx, a, expected, a.Authority, retry, retryView, 30); !errors.Is(err, ErrRepositoryFull) {
		t.Fatalf("retry overlapping an active attributed run error = %v, want %v", err, ErrRepositoryFull)
	}
	if _, err := db.FactoryRun(ctx, retry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused retry left a run: %v", err)
	}
	current, err := db.Assignment(ctx, a.ID)
	if err != nil || current.Run != run.ID || current.Attempts != 1 {
		t.Fatalf("refused retry changed its assignment: %+v %v", current, err)
	}
}

func TestRecordRetryPacketReholdsAndRefusesLimits(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatal(err)
	}
	expected := dispatchTestControlFor(t, db, a)
	confirmedUnused, err := db.FactoryRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	confirmedUnused.Outcome, confirmedUnused.Reconciled, confirmedUnused.Summary = factory.Failed, true, "launch confirmed unused"
	if err := db.SaveFactoryRun(ctx, confirmedUnused); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	second, secondView := retryTestRun(t, a, run)
	next, err := db.RecordRetryPacket(ctx, a, expected, a.Authority, second, secondView, 45)
	if err != nil {
		t.Fatalf("retry refused: %v", err)
	}
	held, err := db.Reservation(ctx, a.ID)
	if err != nil || held.State != factory.ReservationHeld || held.PlannedMinutes != 45 {
		t.Fatalf("reservation not reheld: %+v %v", held, err)
	}
	if err := db.SaveCapacity(ctx, factory.Capacity{Revision: 1, UpdatedBy: 7, MaxConcurrentRuns: 1}); err != nil {
		t.Fatal(err)
	}
	b, r2, run2, view2 := dispatchTestPacket(t, now)
	b.ID, b.Issue, b.Run, b.RunHistory = factory.NewID(), 4, run2.ID, []string{run2.ID}
	b.Acceptance = "d" + strings.Repeat("b", 24)
	b.Subject.Issue.Issue, b.Subject.Issue.Acceptance = 4, b.Acceptance
	b.AttemptRoot, b.PublicationAssignment = b.ID, b.ID
	b.Authority.Capacity = 2
	r2.AssignmentID = b.ID
	view2.Issue, view2.Attempt = 4, b.ID
	if err := recordDispatchTestPacket(t, ctx, db, b, r2, run2, view2); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("over-capacity packet accepted: %v", err)
	}
	if _, err := db.Assignment(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused packet left an assignment: %v", err)
	}
	// A refused retry consumes no attempt either: the assignment still
	// retries once room frees.
	third, thirdView := retryTestRun(t, a, run)
	currentAuthority := next.Authority
	currentAuthority.Capacity = 2
	if _, err = db.RecordRetryPacket(ctx, next, expected, currentAuthority, third, thirdView, 45); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("over-capacity retry accepted: %v", err)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil || stored.Attempts != next.Attempts {
		t.Fatalf("retry attempts wrong: %+v %v", stored, err)
	}
}

func TestRecordRetryPacketRefusesWithdrawnOperatorAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	allowanceBefore, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := db.OperatorGrant(ctx, a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	operator.Active = false
	if err = db.SaveOperatorGrant(ctx, operator); err != nil {
		t.Fatal(err)
	}
	control, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	retry, retryView := retryTestRun(t, a, run)
	currentAuthority := a.Authority
	currentAuthority.Operator++
	if _, err = db.RecordRetryPacket(ctx, a, control, currentAuthority, retry, retryView, 30); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatalf("withdrawn-operator retry error = %v", err)
	}
	if _, err = db.FactoryRun(ctx, retry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused retry left a run: %v", err)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil || stored.Revision != a.Revision || stored.Run != a.Run || len(stored.RunHistory) != len(a.RunHistory) || stored.Attempts != a.Attempts {
		t.Fatalf("refused retry changed assignment: %+v, %v", stored, err)
	}
	storedReservation, err := db.Reservation(ctx, a.ID)
	if err != nil || storedReservation.State != factory.ReservationReleased {
		t.Fatalf("refused retry changed reservation: %+v, %v", storedReservation, err)
	}
	allowanceAfter, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || allowanceAfter.Limits != allowanceBefore.Limits ||
		allowanceAfter.RootAssignment != allowanceBefore.RootAssignment || allowanceAfter.Revision != allowanceBefore.Revision ||
		allowanceAfter.ActiveSeconds != allowanceBefore.ActiveSeconds || allowanceAfter.CheckpointUnix != allowanceBefore.CheckpointUnix ||
		allowanceAfter.Active != allowanceBefore.Active || !slices.Equal(allowanceAfter.Corrections, allowanceBefore.Corrections) {
		t.Fatalf("refused retry changed allowance: before=%+v after=%+v err=%v", allowanceBefore, allowanceAfter, err)
	}
}

func TestRecordRetryPacketRefusesRoleRemovedFromSponsorshipAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	sponsorship, err := db.Sponsorship(ctx, a.Repository, a.Connection)
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.Roles = []string{project.RoleReviewer}
	if err = db.SaveSponsorship(ctx, sponsorship); err != nil {
		t.Fatal(err)
	}
	control, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	retry, retryView := retryTestRun(t, a, run)
	currentAuthority := a.Authority
	currentAuthority.Sponsorship++
	if _, err = db.RecordRetryPacket(ctx, a, control, currentAuthority, retry, retryView, 30); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatalf("role-removed retry error = %v", err)
	}
	if _, err = db.FactoryRun(ctx, retry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused retry left a run: %v", err)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil || stored.Revision != a.Revision || stored.Run != a.Run || stored.Attempts != a.Attempts || !slices.Equal(stored.RunHistory, a.RunHistory) {
		t.Fatalf("refused retry changed assignment: %+v, %v", stored, err)
	}
	storedReservation, err := db.Reservation(ctx, a.ID)
	if err != nil || storedReservation.State != factory.ReservationReleased {
		t.Fatalf("refused retry changed reservation: %+v, %v", storedReservation, err)
	}
}

func TestRecordRetryPacketRefusesChangedEnvironmentAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	environment, err := db.EnvironmentGrant(ctx, a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	environment.Profile.Revision = "d" + strings.Repeat("e", 39)
	if err = db.SaveEnvironmentGrant(ctx, environment); err != nil {
		t.Fatal(err)
	}
	control, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	retry, retryView := retryTestRun(t, a, run)
	if _, err = db.RecordRetryPacket(ctx, a, control, a.Authority, retry, retryView, 30); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatalf("changed-environment retry error = %v", err)
	}
	if _, err = db.FactoryRun(ctx, retry.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused retry left a run: %v", err)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil || stored.Revision != a.Revision || stored.Run != a.Run || stored.Attempts != a.Attempts || !slices.Equal(stored.RunHistory, a.RunHistory) {
		t.Fatalf("refused retry changed assignment: %+v, %v", stored, err)
	}
	storedReservation, err := db.Reservation(ctx, a.ID)
	if err != nil || storedReservation.State != factory.ReservationReleased {
		t.Fatalf("refused retry changed reservation: %+v, %v", storedReservation, err)
	}
}

func TestSupervisorRunWritesPreserveAdmissionSnapshot(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, time.Now().UTC())
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	stored, err := db.FactoryRun(ctx, run.ID)
	if err != nil || stored.Admission == nil {
		t.Fatalf("dispatch omitted run admission: %+v, %v", stored.Admission, err)
	}
	want := *stored.Admission
	observed := stored
	observed.Admission = nil
	observed.Summary = "supervisor observation"
	if err = db.SaveFactoryRun(ctx, observed); err != nil {
		t.Fatalf("observational write refused: %v", err)
	}
	afterObservation, err := db.FactoryRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(afterObservation.Admission, &want) {
		t.Fatalf("omission cleared admission: %+v, %v", afterObservation.Admission, err)
	}
	changed := afterObservation
	changedAdmission := want
	changedProfile := *changedAdmission.Profile
	changedAdmission.Profile = &changedProfile
	changed.Admission = &changedAdmission
	changed.Admission.Profile.Revision = "d" + strings.Repeat("e", 39)
	if err = db.SaveFactoryRun(ctx, changed); err == nil {
		t.Fatal("supervisor overwrote immutable admission profile")
	}
	afterMutation, err := db.FactoryRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(afterMutation.Admission, &want) {
		t.Fatalf("rejected admission mutation persisted: %+v, %v", afterMutation.Admission, err)
	}
	forged := afterMutation
	forged.ID = factory.NewID()
	forgedAdmission := want
	forged.Admission = &forgedAdmission
	if err = db.RecordFactoryRun(ctx, forged); err == nil {
		t.Fatal("standalone run with copied admission was recorded")
	}
	if _, err = db.FactoryRun(ctx, forged.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forged admission left a run: %v", err)
	}
}
