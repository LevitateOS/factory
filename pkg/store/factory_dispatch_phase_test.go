package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// FW11 packet tests (PG-gated): selected-issue planning first through the
// common admission boundary. An investigation root opens the phased attempt;
// its completed plan advances an implementation child without a publication
// or PR, retaining the allowance root unconsumed. Phase/parent mismatches
// refuse; only the correction phase spends a correction cycle.

func phaseTestPlan(t *testing.T, root, source, prep string) factory.InvestigationPlan {
	t.Helper()
	plan := factory.InvestigationPlan{
		ID: factory.NewID(), AttemptRoot: root,
		ScopeDigest: strings.Repeat("a", 64), InputDigest: strings.Repeat("b", 64),
		SourceCommit: source, IntegrationCommit: source,
		Preparation:        prep,
		Approach:           "inspect the selected source, then plan behind checks",
		AcceptanceCoverage: []string{"accept-one"},
		Tasks: []factory.PlanTask{{
			ID: "task-one", Objective: "inspect the selected source",
			Outputs: []string{"notes"}, AcceptanceRefs: []string{"accept-one"},
			Lane: "lane-one", OwnedPaths: []string{"internal/x.go"},
		}},
		Lanes: []factory.PlanLane{{ID: "lane-one", TaskIDs: []string{"task-one"}}},
	}
	digest, err := factory.PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Digest = digest
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan fixture invalid: %v", err)
	}
	return plan
}

func phaseTestInvestigateRoot(t *testing.T, now time.Time) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	a, r, run, view := dispatchTestPacket(t, now)
	a.Subject = factory.ExecutionSubject{
		Kind:  factory.SubjectIssue,
		Issue: &factory.IssueSubject{Issue: a.Issue, Acceptance: a.Acceptance},
	}
	a.Phase = project.ExecutionInvestigate
	if err := a.Validate(); err != nil {
		t.Fatalf("investigate root invalid: %v", err)
	}
	return a, r, run, view
}

func finishInvestigateRoot(t *testing.T, ctx context.Context, db *Store, a factory.Assignment, run factory.Run, now time.Time) factory.Assignment {
	t.Helper()
	plan := phaseTestPlan(t, a.AttemptRoot, a.SourceCommit, a.Preparation)
	// Retire the parent slot the way production settlement does: the run
	// goes inactive and the reservation is consumed. The finished
	// assignment alone still owns its process slot under the
	// one-session-per-repository invariant, which would refuse the child.
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "plan ready", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatalf("investigate run unsettled: %v", err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatalf("investigate reservation unconsumed: %v", err)
	}
	finished, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	phased := factory.PhaseResult{
		Version: 2, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	}
	result := factory.AssignmentResult{
		AssignmentID: a.ID, RunID: run.ID, Phase: project.ExecutionInvestigate,
		PhaseResult: &phased, Status: "completed", Summary: "plan ready",
		Findings: []string{}, Reported: true, RecordedUnix: now.Unix(),
	}
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
	finished.FinishedUnix = now.Unix()
	finished.Result = &result
	if err := db.FinishAssignment(ctx, finished); err != nil {
		t.Fatalf("investigate finish refused: %v", err)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func phaseTestImplementChild(t *testing.T, now time.Time, parent factory.Assignment) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	childID, err := factory.AdvancementID(parent.ID, factory.ResultDigest(*parent.Result), project.ExecutionImplement)
	if err != nil {
		t.Fatal(err)
	}
	prompt := dispatchTestPrompt(t)
	sum := sha256.Sum256(prompt)
	runID := factory.NewID()
	a := factory.Assignment{
		Authority: parent.Authority,
		ID:        childID, AttemptRoot: parent.AttemptRoot, ParentAssignment: parent.ID,
		PublicationAssignment: parent.PublicationAssignment,
		Subject:               parent.Subject, Phase: project.ExecutionImplement,
		ProjectID: parent.ProjectID, Role: project.RoleCoder,
		Repository: parent.Repository, Issue: parent.Issue, NativeRev: parent.NativeRev,
		Acceptance: parent.Acceptance, Preparation: parent.Preparation,
		Harness: parent.Harness, HarnessVers: parent.HarnessVers, Model: parent.Model,
		Connection: parent.Connection, SourceCommit: parent.SourceCommit,
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(), ActorID: parent.ActorID,
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("implement child invalid: %v", err)
	}
	r := factory.Reservation{AssignmentID: a.ID, Repository: a.Repository, Connection: a.Connection, State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: a.SourceCommit,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: a.Repository, Issue: a.Issue, Attempt: a.ID}
	return a, r, run, view
}

func TestPhasedPacketRefusals(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	t.Run("implement child needs its investigation parent", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		root, reservation, run, view := phaseTestInvestigateRoot(t, now)
		if err := recordDispatchTestPacket(t, ctx, db, root, reservation, run, view); err != nil {
			t.Fatal(err)
		}
		parent := finishInvestigateRoot(t, ctx, db, root, run, now)
		child, childReservation, childRun, childView := phaseTestImplementChild(t, now, parent)
		child.SourceCommit = strings.Repeat("d", 40)
		// Move the run with the assignment: the packet stays coherent so
		// the semantic parent binding (not packet shape) refuses.
		childRun.InputSHA = child.SourceCommit
		if err := recordDispatchTestPacket(t, ctx, db, child, childReservation, childRun, childView); !errors.Is(err, ErrAttemptRootMismatch) {
			t.Fatalf("moved-source child error = %v, want root mismatch", err)
		}
		assertDispatchPacketAbsent(t, db, child)
	})
	t.Run("phased review enters through its owning boundary", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		root, reservation, run, view := phaseTestInvestigateRoot(t, now)
		if err := recordDispatchTestPacket(t, ctx, db, root, reservation, run, view); err != nil {
			t.Fatal(err)
		}
		parent := finishInvestigateRoot(t, ctx, db, root, run, now)
		child, childReservation, childRun, childView := phaseTestImplementChild(t, now, parent)
		child.ID = factory.NewID()
		child.Phase, child.Role, child.ParentAssignment = project.ExecutionReview, project.RoleReviewer, parent.ID
		childRun.Role = project.RoleReviewer
		if err := child.Validate(); err != nil {
			t.Fatal(err)
		}
		childReservation.AssignmentID = child.ID
		childView.Attempt = child.ID
		if err := recordDispatchTestPacket(t, ctx, db, child, childReservation, childRun, childView); err == nil {
			t.Fatal("phased review child admitted without its boundary")
		}
		assertDispatchPacketAbsent(t, db, child)
	})
	t.Run("only investigation opens a phased root", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		a, r, run, view := dispatchTestPacket(t, now)
		a.Subject = factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: a.Issue, Acceptance: a.Acceptance},
		}
		a.Phase = project.ExecutionImplement
		if err := a.Validate(); err != nil {
			t.Fatal(err)
		}
		// The domain shape holds; the admission gate refuses it: only
		// investigation opens a phased attempt root.
		if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err == nil {
			t.Fatal("implement root admitted without its parent")
		}
		assertDispatchPacketAbsent(t, db, a)
	})
}
