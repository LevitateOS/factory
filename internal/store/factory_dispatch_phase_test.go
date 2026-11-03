package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
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

func TestPhasedInvestigateRootAdvancesImplementChild(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	root, reservation, run, view := phaseTestInvestigateRoot(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, root, reservation, run, view); err != nil {
		t.Fatalf("investigate root refused: %v", err)
	}
	allowance, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || allowance.RootAssignment != root.ID || len(allowance.Corrections) != 0 {
		t.Fatalf("investigate allowance not opened: %+v %v", allowance, err)
	}
	parent := finishInvestigateRoot(t, ctx, db, root, run, now)
	child, childReservation, childRun, childView := phaseTestImplementChild(t, now, parent)
	plan := *parent.Result.PhaseResult.Output.Plan
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	manifestFor := func(r factory.Run) []byte {
		encoded, err := project.MarshalExecutionManifest(project.ExecutionManifest{Plan: planBytes, PlanDigest: project.PlanDigest(planBytes), Invocation: "fixture-invocation", Phase: project.ExecutionImplement, Run: r.ID, Deadline: r.Deadline, Effort: project.TaskEffortHigh, MaxChildren: 1})
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	childRun.ContextPending = true
	childRun.ExecutionManifest = manifestFor(childRun)
	if err := recordDispatchTestPacket(t, ctx, db, child, childReservation, childRun, childView); err != nil {
		t.Fatalf("implement child refused: %v", err)
	}
	// The allowance is retained, not reset or recharged: same root, still
	// open, and only the correction phase spends a correction cycle.
	after, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || after.RootAssignment != root.ID || after.Closed || len(after.Corrections) != 0 {
		t.Fatalf("implement child reset allowance: %+v %v", after, err)
	}
	// Replay creates no second run: the same advancement identity refuses.
	// The replay carries its own coherent run and view so the packet
	// shape cannot refuse before the taken identity does.
	replay := child
	replayRun, replayView := childRun, childView
	replay.Run = factory.NewID()
	replay.RunHistory = []string{replay.Run}
	replayRun.ID, replayView.RunID = replay.Run, replay.Run
	replayRun.ExecutionManifest = manifestFor(replayRun)
	if err := recordDispatchTestPacket(t, ctx, db, replay, childReservation, replayRun, replayView); err == nil {
		t.Fatal("implement child replay admitted")
	} else if !isAssignmentActive(err) {
		t.Fatalf("wrong replay error: %v", err)
	}
	// A startup fence can retire this staged child before any native launch.
	// Explicit continuation retains its real parent plan and finite allowance;
	// it never changes the old run, capability or paid context point.
	if err = db.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err = db.AdvancePlanHead(ctx, root.ID, "", plan.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.RecordRunUsage(ctx, factory.Usage{RunID: run.ID, Repository: root.Repository, Connection: root.Connection, StartedAt: run.Started, EndedAt: run.Started}); err != nil {
		t.Fatal(err)
	}
	cap := testMCPCapabilityRow()
	cap.AssignmentID, cap.RunID, cap.Repository, cap.Project = child.ID, childRun.ID, child.Repository, child.ProjectID
	cap.DeadlineUnix = childRun.Deadline.Unix()
	if err = db.StageMCPCapability(ctx, cap); err != nil {
		t.Fatal(err)
	}
	childRun.Outcome, childRun.Reconciled, childRun.Summary = factory.Cancelled, true, "stop-before-start"
	if err = db.SaveFactoryRun(ctx, childRun); err != nil {
		t.Fatal(err)
	}
	if err = db.ReleaseReservation(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	finished := child
	finished.Stage, finished.Outcome, finished.Reason, finished.FinishedUnix = factory.AssignmentFinished, factory.Cancelled, factory.AssignReasonCancelled, now.Unix()
	result := factory.ResultSynthesized(finished.ID, finished.Run, "cancelled", "stop-before-start", now.Unix())
	result.Phase = project.ExecutionImplement
	finished.Result = &result
	if err = db.FinishAssignment(ctx, finished); err != nil {
		t.Fatal(err)
	}
	before, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || before.Active || before.Closed || before.RemainingSeconds(time.Now()) <= 0 {
		t.Fatalf("unused child did not retain frozen allowance: %+v %v", before, err)
	}
	decision := factory.RetryDecision{Prior: childRun.ID, CommandID: factory.NewID(), Queued: true, Reason: "unused implementation continuation"}
	payload, _ := json.Marshal(map[string]string{"prior_run": decision.Prior, "prior_outcome": string(factory.Cancelled)})
	cmd := factory.Command{ID: decision.CommandID, Type: factory.CommandRetryImplementation, Target: "run/" + decision.Prior, Principal: "native:7", Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if admitted, created, err := db.RecordImplementationRetryCommand(ctx, cmd, decision, root.Repository, root.Issue, root.Acceptance, time.Now()); err != nil || !created || admitted.Outcome != "" {
		t.Fatalf("continuation command refused: created=%v %v", created, err)
	}
	if _, created, err := db.RecordImplementationRetryCommand(ctx, cmd, decision, root.Repository, root.Issue, root.Acceptance, time.Now()); err != nil || created {
		t.Fatalf("continuation replay changed: created=%v %v", created, err)
	}
	pending, err := db.PendingImplementationRetry(ctx, root.Repository, root.Issue, root.Acceptance)
	if err != nil || pending.CommandID != decision.CommandID || pending.Prior != decision.Prior || !pending.Queued {
		t.Fatalf("pending continuation absent: %+v %v", pending, err)
	}
	if _, err = db.PendingImplementationRetry(ctx, root.Repository+1, root.Issue, root.Acceptance); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign continuation visible: %v", err)
	}
	next, nextReservation, nextRun, nextView := phaseTestImplementChild(t, time.Now().UTC(), parent)
	next.ID, nextReservation.AssignmentID, nextView.Attempt = decision.CommandID, decision.CommandID, decision.CommandID
	nextRun.ContextPending = true
	nextRun.Deadline = time.Now().UTC().Add(2 * time.Minute)
	nextRun.ExecutionManifest = manifestFor(nextRun)
	expected := dispatchTestControlFor(t, db, next)
	wrong := next
	wrong.SourceCommit = strings.Repeat("9", 40)
	if err = db.RecordImplementationRetryPacket(ctx, decision, expected, wrong, nextReservation, nextRun, nextView); err == nil {
		t.Fatal("changed continuation source admitted")
	}
	if err = db.RecordImplementationRetryPacket(ctx, decision, expected, next, nextReservation, nextRun, nextView); err != nil {
		t.Fatalf("continuation packet refused: %v", err)
	}
	afterContinuation, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || afterContinuation.RootAssignment != before.RootAssignment || afterContinuation.Limits != before.Limits || afterContinuation.ActiveSeconds != before.ActiveSeconds || !afterContinuation.Active || afterContinuation.Closed {
		t.Fatalf("continuation refilled allowance: before=%+v after=%+v %v", before, afterContinuation, err)
	}
	oldRun, err := db.FactoryRun(ctx, childRun.ID)
	if err != nil || !oldRun.Reconciled || oldRun.Outcome != factory.Cancelled || !oldRun.ContextPending || !oldRun.Deadline.Equal(childRun.Deadline) {
		t.Fatalf("continuation rewrote old run: %+v %v", oldRun, err)
	}
	oldCap, err := db.GetMCPCapability(ctx, cap.ID)
	if err != nil || oldCap != cap {
		t.Fatalf("continuation rewrote old capability: %+v %v", oldCap, err)
	}
	if _, err = db.PendingImplementationRetry(ctx, root.Repository, root.Issue, root.Acceptance); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consumed continuation remained pending: %v", err)
	}
	if err = db.RecordImplementationRetryPacket(ctx, decision, expected, next, nextReservation, nextRun, nextView); err == nil {
		t.Fatal("continuation duplicated packet")
	}
	// A later confirmed unused launch retry belongs to the already admitted
	// continuation. Its immutable command remains valid with an active root
	// and historical predecessor; it does not need another owner decision.
	recordedNext, err := db.FactoryRun(ctx, nextRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	recordedNext.ContextPending = false
	recordedNext.Outcome, recordedNext.Reconciled, recordedNext.Summary = factory.Cancelled, true, "stop-before-start"
	if err = db.SaveFactoryRun(ctx, recordedNext); err != nil {
		t.Fatal(err)
	}
	if err = db.ReleaseReservation(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	launchRetry, retryView := retryTestRun(t, next, nextRun)
	continued, err := db.RecordRetryPacket(ctx, next, expected, next.Authority, launchRetry, retryView, 2)
	if err != nil || continued.ID != next.ID || continued.AttemptRoot != parent.ID || continued.Attempts != 2 || len(continued.RunHistory) != 2 {
		t.Fatalf("admitted continuation launch retry refused: %+v %v", continued, err)
	}
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
