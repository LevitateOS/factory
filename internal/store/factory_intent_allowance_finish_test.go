package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// Typed intent-root terminal finish (R11 settlement) against real
// PostgreSQL: a succeeded+reported decompose root closes its time-only
// allowance in the same terminal transaction with zero readiness work,
// an unsettled run keeps the clock, and a missing or mismatched typed
// allowance fails the finish instead of committing without accounting.
// Every test below opens its own owned disposable database.

func intentScopeOf(a factory.Assignment) factory.IntentAttemptScope {
	return factory.IntentAttemptScope{IntentID: a.Subject.Intent.IntentID, OwnerID: a.Subject.Intent.OwnerID}
}

// intentFinishRootFixture builds one assigned decompose fresh root for
// terminal-finish probes: repository slot, Issue zero, fresh attempt
// root, decompose phase, coder role, no parent, no publication, and the
// canonical intent subject.
func intentFinishRootFixture(t *testing.T, now time.Time, repository int64, intentID, revisionID string, owner int64) factory.Assignment {
	t.Helper()
	selection, ok := grantTestPolicy().Roles[project.RoleCoder]
	if !ok {
		t.Fatal("coder fixture role has no policy selection")
	}
	prompt := []byte("intent terminal accounting probe " + revisionID)
	sum := sha256.Sum256(prompt)
	root, run := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		ID: root, AttemptRoot: root, PublicationAssignment: "", ParentAssignment: "",
		ProjectID: dispatchTestProjectID(), Role: project.RoleCoder, Phase: project.ExecutionDecompose,
		Subject: factory.ExecutionSubject{
			Kind:   factory.SubjectIntent,
			Intent: &factory.IntentSubject{IntentID: intentID, RevisionID: revisionID, ScopeDigest: strings.Repeat("d", 64), OwnerID: owner},
		},
		ActorID:    7,
		Repository: repository, Issue: 0, Revision: 0, NativeRev: 5,
		Acceptance: "", Preparation: "f" + factory.NewID()[:24],
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: "conn", SourceCommit: strings.Repeat("c", 40),
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: run, RunHistory: []string{run}, Stage: factory.AssignmentAssigned, Attempts: 1,
		CreatedUnix: now.Unix(), Authority: dispatchTestAuthority(),
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("intent finish root fixture invalid: %v", err)
	}
	return a
}

func insertIntentFinishRootRow(t *testing.T, ctx context.Context, db *Store, a factory.Assignment) {
	t.Helper()
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(data)); err != nil {
		t.Fatal(err)
	}
}

func commitIntentInsert(t *testing.T, ctx context.Context, db *Store, a factory.Assignment, minutes int, deadline, now time.Time) {
	t.Helper()
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertIntentAttemptAllowanceTx(ctx, tx, a, minutes, deadline, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// intentFinishRunFixture builds the run row for one intent root.
func intentFinishRunFixture(t *testing.T, a factory.Assignment, now time.Time, reconciled bool) factory.Run {
	t.Helper()
	selection := grantTestPolicy().Roles[project.RoleCoder]
	run := factory.Run{
		ID: a.Run, ProjectID: a.ProjectID, Role: project.RoleCoder,
		InputSHA: strings.Repeat("c", 40),
		Started:  now.Add(-time.Minute), Deadline: now.Add(30 * time.Minute),
		Image:   "sha256:" + strings.Repeat("b", 64),
		Harness: selection.Harness + "-" + selection.HarnessVers, Model: selection.Model,
	}
	if reconciled {
		run.Outcome, run.Reconciled, run.Summary = factory.Succeeded, true, "run settled"
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("intent run fixture invalid: %v", err)
	}
	return run
}

func insertIntentFinishRunRow(t *testing.T, ctx context.Context, db *Store, a factory.Assignment, run factory.Run, active, settled bool) {
	t.Helper()
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,$2,$3,$4)`,
		run.ID, active, settled, string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4)`,
		run.ID, a.Repository, 0, a.ID); err != nil {
		t.Fatal(err)
	}
}

// intentTerminalResult builds a valid candidate-free decomposition
// report result for one intent root: completed, bound to the decompose
// phase with exactly the typed decomposition payload, and carrying no
// publishable candidate.
func intentTerminalResult(t *testing.T, a factory.Assignment, now time.Time) factory.AssignmentResult {
	t.Helper()
	subject := *a.Subject.Intent
	answer := factory.SemanticPlannerOutput{
		Intent: subject.IntentID, Revision: subject.RevisionID, Operation: factory.IntentPlanningPropose,
		Candidates: []factory.SemanticCandidate{{
			Key: "keep", Rationale: "Keep the recorded draft as the single review baseline.",
			Tasks: []factory.IntentTask{{
				Key: "d333333333333333333333333", Objective: "Plan the recorded draft",
				Outcomes: []string{"Draft is planned"}, IdeaIDs: []string{"d444444444444444444444444"},
			}},
		}},
	}
	if err := answer.Validate(); err != nil {
		t.Fatalf("terminal decomposition invalid: %v", err)
	}
	phased := factory.PhaseResult{
		Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "intent terminal accounting probe",
		Version: factory.PhaseResultVersion,
		Output:  &factory.PhaseOutput{Decomposition: &answer},
	}
	if err := phased.Validate(); err != nil {
		t.Fatalf("terminal phased result invalid: %v", err)
	}
	return factory.AssignmentResult{
		AssignmentID: a.ID, RunID: a.Run, Phase: project.ExecutionDecompose,
		Status: "completed", Summary: "intent terminal accounting probe",
		Reported: true, RecordedUnix: now.Unix(), PhaseResult: &phased,
	}
}

// finishIntentRoot marks one assigned intent root finished with the
// given outcome and reason, carrying a recorded result, and validates
// the finished shape before any store call.
func finishIntentRoot(t *testing.T, a factory.Assignment, now time.Time, outcome factory.Outcome, reason string) factory.Assignment {
	t.Helper()
	finished := a
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, outcome, reason
	finished.FinishedUnix = now.Unix()
	var result factory.AssignmentResult
	if outcome == factory.Succeeded {
		result = intentTerminalResult(t, finished, now)
	} else {
		result = factory.ResultSynthesized(finished.ID, finished.Run, "failed", "run failed", now.Unix())
	}
	finished.Result = &result
	if err := finished.Validate(); err != nil {
		t.Fatalf("finished intent root invalid: %v", err)
	}
	return finished
}

func TestIntentTerminalFinishSettledCheckpointsNoReadiness(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := intentFinishRootFixture(t, now, 7, "d111111111111111111111111", "d222222222222222222222222", 7)
	insertIntentFinishRootRow(t, ctx, db, a)
	commitIntentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	before, err := db.IntentAttemptAllowance(ctx, 7, intentScopeOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	insertIntentFinishRunRow(t, ctx, db, a, intentFinishRunFixture(t, a, now, true), false, true)
	sourcesBefore, generationBefore := readinessWorkCount(t, ctx, db)
	if err = db.FinishAssignment(ctx, finishIntentRoot(t, a, now, factory.Succeeded, factory.AssignReasonReported)); err != nil {
		t.Fatalf("settled intent finish refused: %v", err)
	}
	if got := storedAssignmentStage(t, ctx, db, a.ID); got != factory.AssignmentFinished {
		t.Fatalf("intent assignment stage = %q", got)
	}
	after, err := db.IntentAttemptAllowance(ctx, 7, intentScopeOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Closed || after.Active {
		t.Fatalf("settled terminal intent did not close+stop: %+v", after)
	}
	if after.ActiveSeconds != after.CheckpointUnix-before.CheckpointUnix {
		t.Fatalf("consumed %d != checkpoint advance %d", after.ActiveSeconds, after.CheckpointUnix-before.CheckpointUnix)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("terminal checkpoint revision = %d, want %d", after.Revision, before.Revision+1)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != sourcesBefore || generation != generationBefore || sources != 0 {
		t.Fatalf("intent finish enqueued readiness work: sources %d->%d generation %d->%d",
			sourcesBefore, sources, generationBefore, generation)
	}
}

func TestIntentTerminalFinishUnsettledKeepsClock(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := intentFinishRootFixture(t, now, 7, "d111111111111111111111111", "d222222222222222222222222", 7)
	insertIntentFinishRootRow(t, ctx, db, a)
	commitIntentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	before, err := db.IntentAttemptAllowance(ctx, 7, intentScopeOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A genuinely unsettled recorded run: active, unreconciled, with a
	// run view bound to this root. The active clock must survive it.
	insertIntentFinishRunRow(t, ctx, db, a, intentFinishRunFixture(t, a, now, false), true, false)
	if err = db.FinishAssignment(ctx, finishIntentRoot(t, a, now, factory.Succeeded, factory.AssignReasonReported)); err != nil {
		t.Fatalf("unsettled intent finish refused: %v", err)
	}
	after, err := db.IntentAttemptAllowance(ctx, 7, intentScopeOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Closed || !after.Active {
		t.Fatalf("unsettled terminal intent must close admission yet keep its clock: %+v", after)
	}
	if after.ActiveSeconds != 0 || after.CheckpointUnix != before.CheckpointUnix {
		t.Fatalf("unsettled finish stopped the clock early: %+v", after)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("close-only revision = %d, want %d", after.Revision, before.Revision+1)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != 0 || generation != 1 {
		t.Fatalf("unsettled intent finish enqueued readiness work: sources %d generation %d", sources, generation)
	}
}

func TestIntentFinishRefusesMissingAllowance(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := intentFinishRootFixture(t, now, 7, "d111111111111111111111111", "d222222222222222222222222", 7)
	insertIntentFinishRootRow(t, ctx, db, a)
	insertIntentFinishRunRow(t, ctx, db, a, intentFinishRunFixture(t, a, now, true), false, true)
	// No allowance was ever funded: terminal accounting is mandatory,
	// so the finish fails instead of committing without accounting.
	if err := db.FinishAssignment(ctx, finishIntentRoot(t, a, now, factory.Succeeded, factory.AssignReasonReported)); err == nil {
		t.Fatal("unfunded intent finish admitted")
	}
	if got := storedAssignmentStage(t, ctx, db, a.ID); got != factory.AssignmentAssigned {
		t.Fatalf("refused finish moved the assignment stage to %q", got)
	}
}

func TestIntentAttemptAllowanceTypedRead(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := intentFinishRootFixture(t, now, 7, "d111111111111111111111111", "d222222222222222222222222", 7)
	insertIntentFinishRootRow(t, ctx, db, a)
	commitIntentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	got, err := db.IntentAttemptAllowance(ctx, 7, intentScopeOf(a), a.ID)
	if err != nil {
		t.Fatalf("typed intent read refused: %v", err)
	}
	if got.Intent == nil || *got.Intent != intentScopeOf(a) || got.Limits.ActiveMinutes != 45 {
		t.Fatalf("typed intent read mis-scoped: %+v", got)
	}
	for name, scope := range map[string]factory.IntentAttemptScope{
		"wrong intent": {IntentID: "d555555555555555555555555", OwnerID: 7},
		"wrong owner":  {IntentID: "d111111111111111111111111", OwnerID: 9},
		"short intent": {IntentID: "short", OwnerID: 7},
		"zero owner":   {IntentID: "d111111111111111111111111", OwnerID: 0},
	} {
		if _, err = db.IntentAttemptAllowance(ctx, 7, scope, a.ID); err == nil {
			t.Fatalf("typed intent read answered %s", name)
		}
	}
	if _, err = db.IntentAttemptAllowance(ctx, 7, intentScopeOf(a), factory.NewID()); err == nil {
		t.Fatal("typed intent read answered a wrong root")
	}
}
