package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// TestStaleSourceCannotLaunchImplementation pins the R15 advancement
// currency clause: when the native tip moved past the investigated
// source commit, the finished investigation waits for fresh planning
// instead of admitting an implementation child. No child assignment may
// exist afterwards, and the recorded wait names the moved source.
func TestStaleSourceCannotLaunchImplementation(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	deps := fx.deps()

	decision := fx.accept(t, 7, "d"+strings.Repeat("e", 24))
	moved := strings.Repeat("d", 40)
	now := time.Now().Truncate(time.Second)
	prompt := []byte("investigate prompt before the source moved")
	sum := sha256.Sum256(prompt)
	assignmentID, runID := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		ID: assignmentID, AttemptRoot: assignmentID, PublicationAssignment: assignmentID,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 7, Acceptance: decision.ID},
		},
		Phase:     project.ExecutionInvestigate,
		ProjectID: fx.proj, ActorID: 7, Role: project.RoleCoder,
		Repository: fx.repo, Issue: 7, NativeRev: 41,
		Acceptance: decision.ID, Preparation: "f111111111111111111111111",
		Connection: "conn", SourceCommit: moved,
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(),
	}
	authority, err := deps.Authority(ctx, a.Repository)
	if err != nil || !authority.Effective {
		t.Fatalf("fixture authority: %+v %v", authority, err)
	}
	selection := fx.policy.Roles[a.Role]
	a.Harness, a.HarnessVers, a.Model = selection.Harness+"-"+selection.HarnessVers, selection.HarnessVers, selection.Model
	a.Authority = authority.Authority
	if a.Authority.RequirementsID, err = db.RequirementHead(ctx, a.ProjectID); err != nil {
		t.Fatal(err)
	}
	if a.Authority.ApprovalID, err = db.ApprovalHead(ctx, a.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("stale advancement fixture invalid: %v", err)
	}
	plan := phaseTestPlan(t, a.AttemptRoot, moved, a.Preparation)
	reservation := factory.Reservation{AssignmentID: a.ID, Repository: fx.repo, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: moved,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: fx.repo, Issue: 7, Attempt: a.ID}
	if err := db.RecordDispatchPacket(ctx, dispatchControlForAssignment(t, db, a), a, reservation, run, view); err != nil {
		t.Fatalf("stale advancement packet refused: %v", err)
	}
	finished, err := buildRunFinish(t, a, runWithOutcome(run, factory.Succeeded), phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	}), now)
	if err != nil {
		t.Fatalf("investigation finish refused: %v", err)
	}
	if finished.Outcome != factory.Succeeded || finished.Reason != factory.AssignReasonReported {
		t.Fatalf("investigation outcome = %v/%v", finished.Outcome, finished.Reason)
	}
	if err := db.FinishAssignment(ctx, finished); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFactoryRun(ctx, runWithOutcome(run, factory.Succeeded)); err != nil {
		t.Fatal(err)
	}
	latest, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}

	occupancy := &passOccupancy{byProject: map[string]int{}}
	var report DispatchReport
	if !maybeAdvanceCompletedPlanning(ctx, deps, occupancy, dispatchControlForAssignment(t, db, latest), latest, decision.ID, &report) {
		t.Fatal("stale advancement did not resolve")
	}
	if got := waitReason(report, 7); got != WaitInputsChanged {
		t.Fatalf("stale source wait = %q, report %+v", got, report)
	}
	for _, wait := range report.Waits {
		if wait.Issue == 7 && !strings.Contains(wait.Detail, "source moved after investigation") {
			t.Fatalf("stale wait names the wrong cause: %+v", wait)
		}
	}
	current, err := db.LatestIssueAttempt(ctx, fx.repo, 7)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != latest.ID {
		t.Fatalf("stale source admitted an implementation child: %v", current.ID)
	}
}

func runWithOutcome(run factory.Run, outcome factory.Outcome) factory.Run {
	run.Outcome, run.Summary, run.Reconciled = outcome, "worker settled", true
	return run
}
