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

// TestFinishTerminalBindsPhasedResult pins the R10 ingress parity for
// terminal finishes: a phased assignment finished without a run report
// (exhausted attempts, superseded acceptance, withdrawn grants) must
// still record its execution phase in a bound typed result, exactly as
// run-derived failed/cancelled finishes do through synthesizePhased.
// A phaseless legacy result on a finished phased assignment leaves the
// phase-role-subject-result matrix with an unnamed row.
func TestFinishTerminalBindsPhasedResult(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	deps := fx.deps()

	decision := fx.accept(t, 7, "d"+strings.Repeat("a", 24))
	now := time.Now().Truncate(time.Second)
	prompt := []byte("phased investigate prompt for terminal finish")
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
		Connection: "conn", SourceCommit: fx.tip,
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
		t.Fatalf("phased terminal fixture invalid: %v", err)
	}
	reservation := factory.Reservation{AssignmentID: a.ID, Repository: fx.repo, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: a.SourceCommit,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: fx.repo, Issue: 7, Attempt: a.ID}
	if err := db.RecordDispatchPacket(ctx, dispatchControlForAssignment(t, db, a), a, reservation, run, view); err != nil {
		t.Fatalf("phased terminal packet refused: %v", err)
	}

	var report DispatchReport
	finishTerminal(ctx, deps, a, factory.Failed, factory.AssignReasonExhausted, "dispatch launch refused", &report)
	if len(report.Recovered) != 1 || report.Recovered[0] != a.ID {
		t.Fatalf("terminal finish recovered %+v", report)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.Validate(); err != nil {
		t.Fatalf("finished phased assignment invalid: %v", err)
	}
	if stored.Result == nil {
		t.Fatal("finished phased assignment carries no result")
	}
	if stored.Result.Phase != project.ExecutionInvestigate {
		t.Fatalf("terminal finish dropped its execution phase: %+v", stored.Result)
	}
	phased := stored.Result.PhaseResult
	if phased == nil || phased.Phase != project.ExecutionInvestigate || phased.Status != "failed" {
		t.Fatalf("terminal finish lacks its bound typed result: %+v", stored.Result)
	}
	if err := stored.Result.ValidatePhaseBinding(project.RoleCoder, project.ExecutionInvestigate); err != nil {
		t.Fatalf("terminal result does not bind its assignment phase: %v", err)
	}
}

// TestFinishTerminalKeepsPhasedResult pins the terminal arm: a phased
// assignment finished terminally keeps its phased shape through the
// synthesizePhased fallback.
func TestFinishTerminalKeepsPhasedResult(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	deps := fx.deps()

	decision := fx.accept(t, 9, "d"+strings.Repeat("b", 24))
	now := time.Now().Truncate(time.Second)
	prompt := []byte("phased prompt for terminal finish")
	sum := sha256.Sum256(prompt)
	assignmentID, runID := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		ID: assignmentID, AttemptRoot: assignmentID, PublicationAssignment: assignmentID,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 9, Acceptance: decision.ID},
		},
		Phase:     project.ExecutionInvestigate,
		ProjectID: fx.proj, ActorID: 7, Role: project.RoleCoder,
		Repository: fx.repo, Issue: 9, NativeRev: 41,
		Acceptance: decision.ID, Preparation: "f111111111111111111111111",
		Connection: "conn", SourceCommit: fx.tip,
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
		t.Fatalf("phased terminal fixture invalid: %v", err)
	}
	reservation := factory.Reservation{AssignmentID: a.ID, Repository: fx.repo, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: a.SourceCommit,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: fx.repo, Issue: 9, Attempt: a.ID}
	if err := db.RecordDispatchPacket(ctx, dispatchControlForAssignment(t, db, a), a, reservation, run, view); err != nil {
		t.Fatalf("phased terminal packet refused: %v", err)
	}

	var report DispatchReport
	finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "acceptance no longer authorizes this attempt", &report)
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.Validate(); err != nil {
		t.Fatalf("finished phased assignment invalid: %v", err)
	}
	if stored.Result == nil || stored.Result.Phase != project.ExecutionInvestigate || stored.Result.PhaseResult == nil || stored.Result.PhaseResult.Phase != project.ExecutionInvestigate {
		t.Fatalf("terminal finish changed the phased shape: %+v", stored.Result)
	}
	if stored.Result.Status != "cancelled" || stored.Outcome != factory.Cancelled {
		t.Fatalf("terminal finish mapping changed: %+v %+v", stored.Result, stored.Outcome)
	}
}
