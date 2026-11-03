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

// Typed completion-root ACTIVE TIME proof (D13/FW32) against the
// coordinator-owned schema 54 format: factory_assignments and
// factory_attempt_allowances admit Issue 0 only with the canonical
// completion subject, reassess phase and coder role. Every test below
// opens its own owned disposable database; nothing is borrowed.

// reassessmentRootFixture builds one valid completion reassessment root
// assignment: repository slot, Issue zero, fresh attempt root, reassess
// phase, coder role, no parent, no publication, and the canonical
// completion subject.
func reassessmentRootFixture(t *testing.T, now time.Time, repository int64, completion, cycle, digest string) factory.Assignment {
	t.Helper()
	selection := grantTestPolicy().Roles[project.RoleCoder]
	prompt := []byte("reassessment active-time probe " + cycle)
	sum := sha256.Sum256(prompt)
	root := factory.NewID()
	run := factory.NewID()
	a := factory.Assignment{
		ID: root, AttemptRoot: root, PublicationAssignment: "", ParentAssignment: "",
		ProjectID: dispatchTestProjectID(), Role: project.RoleCoder, Phase: project.ExecutionReassess,
		Subject: factory.ExecutionSubject{
			Kind:       factory.SubjectCompletion,
			Completion: &factory.CompletionSubject{CompletionID: completion, CycleID: cycle, InventoryDigest: digest},
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
		t.Fatalf("reassessment root fixture invalid: %v", err)
	}
	return a
}

func insertReassessmentRootRow(t *testing.T, ctx context.Context, db *Store, a factory.Assignment) {
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

func commitReassessmentInsert(t *testing.T, ctx context.Context, db *Store, a factory.Assignment, minutes int, deadline, now time.Time) {
	t.Helper()
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertReassessmentAttemptAllowanceTx(ctx, tx, a, minutes, deadline, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func reassessmentSubjectOf(a factory.Assignment) factory.CompletionSubject {
	return *a.Subject.Completion
}

// completionTerminalResult builds a valid candidate-free reassessment
// report result for one completion root: completed, bound to the
// reassess phase with exactly the typed reassessment payload, and
// carrying no publishable candidate.
func completionTerminalResult(t *testing.T, a factory.Assignment, now time.Time) factory.AssignmentResult {
	t.Helper()
	subject := reassessmentSubjectOf(a)
	report := factory.ReassessmentReport{
		CompletionID: subject.CompletionID, CycleID: subject.CycleID,
		InventoryDigest: subject.InventoryDigest, SourceCommit: a.SourceCommit,
		PreparationDigest: strings.Repeat("b", 64), LocalInputDigest: strings.Repeat("9", 64),
		Summary: "completion terminal accounting probe",
	}
	phased := factory.PhaseResult{
		Phase: project.ExecutionReassess, Status: "completed",
		Summary: "completion terminal accounting probe",
		Version: factory.PhaseResultVersion,
		Output:  &factory.PhaseOutput{Reassessment: &report},
	}
	if err := phased.Validate(); err != nil {
		t.Fatalf("terminal phased result invalid: %v", err)
	}
	return factory.AssignmentResult{
		AssignmentID: a.ID, RunID: a.Run, Phase: project.ExecutionReassess,
		Status: "completed", Summary: "completion terminal accounting probe",
		Reported: true, RecordedUnix: now.Unix(), PhaseResult: &phased,
	}
}

// finishCompletionRoot marks one assigned completion root finished with
// the given outcome and reason, carrying a recorded result, and
// validates the finished shape before any store call.
func finishCompletionRoot(t *testing.T, a factory.Assignment, now time.Time, outcome factory.Outcome, reason string) factory.Assignment {
	t.Helper()
	finished := a
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, outcome, reason
	finished.FinishedUnix = now.Unix()
	var result factory.AssignmentResult
	if outcome == factory.Succeeded {
		result = completionTerminalResult(t, finished, now)
	} else {
		result = factory.ResultSynthesized(finished.ID, finished.Run, "failed", "run failed", now.Unix())
	}
	finished.Result = &result
	if err := finished.Validate(); err != nil {
		t.Fatalf("finished completion root invalid: %v", err)
	}
	return finished
}

// completionRunFixture builds the run row for one completion root
// attempt. Reconciled runs carry their terminal outcome; active runs
// carry none.
func completionRunFixture(t *testing.T, a factory.Assignment, now time.Time, reconciled bool) factory.Run {
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
		t.Fatalf("completion run fixture invalid: %v", err)
	}
	return run
}

// insertCompletionRunRow records one run plus its run view for a
// completion root attempt with explicit active/settled state. Settled
// fixtures record settled reconciled rows; active-effect fixtures
// record genuinely unsettled rows. Nothing is inferred from absent
// rows.
func insertCompletionRunRow(t *testing.T, ctx context.Context, db *Store, a factory.Assignment, run factory.Run, active, settled bool) {
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

// readinessWorkCount reports recorded readiness sources and the budget
// generation, proving a terminal completion finish enqueues zero
// readiness work.
func readinessWorkCount(t *testing.T, ctx context.Context, db *Store) (sources, generation int64) {
	t.Helper()
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_sources`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT generation FROM factory_readiness_budget WHERE id=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return sources, generation
}

func storedAssignmentStage(t *testing.T, ctx context.Context, db *Store, id string) string {
	t.Helper()
	var stage string
	if err := db.db.QueryRowContext(ctx, `SELECT stage FROM factory_assignments WHERE id=$1`, id).Scan(&stage); err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestReassessmentAllowanceTypedRootInsertRead(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	got, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatalf("typed read refused: %v", err)
	}
	if got.Repository != 7 || got.Issue != 0 || got.RootAssignment != a.ID || got.Revision != 0 {
		t.Fatalf("typed scope = %+v", got)
	}
	if got.Limits != (factory.AttemptLimits{ActiveMinutes: 45}) {
		t.Fatalf("typed limits = %+v", got.Limits)
	}
	if got.Completion == nil || *got.Completion != reassessmentSubjectOf(a) {
		t.Fatalf("typed subject = %+v", got.Completion)
	}
	if !got.Active || got.Closed || got.ActiveSeconds != 0 || len(got.Corrections) != 0 ||
		len(got.Reviews) != 0 {
		t.Fatalf("fresh typed consumption = %+v", got)
	}
	// Omitted minutes resolve to the admitted default at construction.
	omitted := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("e", 64))
	insertReassessmentRootRow(t, ctx, db, omitted)
	commitReassessmentInsert(t, ctx, db, omitted, 0, now.Add(20*time.Minute), now)
	resolved, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(omitted), omitted.ID)
	if err != nil || resolved.Limits.ActiveMinutes != factory.DefaultReassessmentMinutes {
		t.Fatalf("omitted minutes = %+v, %v", resolved, err)
	}
}

func TestReassessmentSameRootReplayNoRefill(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	// Consume ten active minutes through the shared CAS joint.
	current, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := current.Checkpoint(now.Add(10*time.Minute), true)
	if err != nil {
		t.Fatal(err)
	}
	advanced.Revision++
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, advanced); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Replay the same root: no error, no refill, consumption unchanged.
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now.Add(10*time.Minute))
	replayed, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ActiveSeconds != 600 || replayed.Revision != 1 || !replayed.Active {
		t.Fatalf("replay refilled or lost consumption: %+v", replayed)
	}
	// A conflicting admitted value on the same root refuses instead of
	// overwriting the recorded allowance.
	tx, err = db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertReassessmentAttemptAllowanceTx(ctx, tx, a, 60, now.Add(30*time.Minute), now); err == nil {
		t.Fatal("conflicting replay minutes accepted")
	}
}

func TestReassessmentTwoCyclesDistinct(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	first := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	second := reassessmentRootFixture(t, now, 7, first.Subject.Completion.CompletionID, factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, first)
	insertReassessmentRootRow(t, ctx, db, second)
	commitReassessmentInsert(t, ctx, db, first, 45, now.Add(30*time.Minute), now)
	commitReassessmentInsert(t, ctx, db, second, 60, now.Add(30*time.Minute), now)
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_attempt_allowances WHERE repository=7 AND issue=0`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("typed rows = %d, %v", count, err)
	}
	for _, a := range []factory.Assignment{first, second} {
		got, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
		if err != nil || got.RootAssignment != a.ID || *got.Completion != reassessmentSubjectOf(a) {
			t.Fatalf("cycle read = %+v, %v", got, err)
		}
	}
}

func TestReassessmentMismatchRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	subject := reassessmentSubjectOf(a)
	wrongEvent := subject
	wrongEvent.CompletionID = factory.NewID()
	wrongCycle := subject
	wrongCycle.CycleID = factory.NewID()
	wrongDigest := subject
	wrongDigest.InventoryDigest = strings.Repeat("e", 64)
	for _, tc := range []struct {
		name       string
		repository int64
		subject    factory.CompletionSubject
		root       string
	}{
		{"unknown root", 7, subject, factory.NewID()},
		{"wrong event", 7, wrongEvent, a.ID},
		{"wrong cycle", 7, wrongCycle, a.ID},
		{"wrong digest", 7, wrongDigest, a.ID},
		{"wrong repository", 8, subject, a.ID},
	} {
		if _, err := db.ReassessmentAttemptAllowance(ctx, tc.repository, tc.subject, tc.root); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
	// A positive-issue root with the same repository never answers the
	// typed completion lookup.
	positive := dispatchStoreFixture(t)
	packet, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, positive, packet, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	if _, err := positive.ReassessmentAttemptAllowance(ctx, packet.Repository, subject, packet.ID); err == nil {
		t.Fatal("positive-issue root answered the typed completion lookup")
	}
	// Unscoped issue-zero lookups refuse explicitly instead of falling back
	// to a latest-per-issue row.
	if _, err := positive.AttemptAllowance(ctx, packet.Repository, 0); err == nil ||
		!strings.Contains(err.Error(), "issue scope") {
		t.Fatalf("unscoped issue-zero lookup = %v", err)
	}
	tx, err := positive.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = admitAttemptAllowanceTx(ctx, tx, packet.Repository, 0, packet.ID, nil, now.Add(time.Hour), now); err == nil ||
		!strings.Contains(err.Error(), "issue scope") {
		t.Fatalf("unscoped issue-zero admission = %v", err)
	}
}

func TestReassessmentMalformedInputsRefuse(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	subject := reassessmentSubjectOf(a)
	badSubject := subject
	badSubject.CompletionID = "short"
	badCycle := subject
	badCycle.CycleID = "short"
	badDigest := subject
	badDigest.InventoryDigest = "short"
	for _, tc := range []struct {
		name       string
		repository int64
		subject    factory.CompletionSubject
		root       string
	}{
		{"zero repository", 0, subject, a.ID},
		{"negative repository", -1, subject, a.ID},
		{"empty root", 7, subject, ""},
		{"short root", 7, subject, "short"},
		{"short completion", 7, badSubject, a.ID},
		{"short cycle", 7, badCycle, a.ID},
		{"short digest", 7, badDigest, a.ID},
		{"empty subject", 7, factory.CompletionSubject{}, a.ID},
	} {
		if _, err := db.ReassessmentAttemptAllowance(ctx, tc.repository, tc.subject, tc.root); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
	tryInsert := func(name string, mutate func(*factory.Assignment), minutes int, deadline time.Time) {
		t.Helper()
		candidate := a
		cloned := *a.Subject.Completion
		candidate.Subject.Completion = &cloned
		mutate(&candidate)
		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err = insertReassessmentAttemptAllowanceTx(ctx, tx, candidate, minutes, deadline, now); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	insertReassessmentRootRow(t, ctx, db, a)
	goodDeadline := now.Add(30 * time.Minute)
	tryInsert("minutes above bound", func(c *factory.Assignment) {}, 121, goodDeadline)
	tryInsert("minutes negative", func(c *factory.Assignment) {}, -5, goodDeadline)
	tryInsert("deadline not after now", func(c *factory.Assignment) {}, 45, now)
	tryInsert("deadline beyond remaining", func(c *factory.Assignment) {}, 45, now.Add(10*time.Hour))
	tryInsert("parented root", func(c *factory.Assignment) { c.ParentAssignment = factory.NewID() }, 45, goodDeadline)
	tryInsert("publishing root", func(c *factory.Assignment) { c.PublicationAssignment = factory.NewID() }, 45, goodDeadline)
	tryInsert("wrong phase", func(c *factory.Assignment) { c.Phase = project.ExecutionImplement }, 45, goodDeadline)
	tryInsert("wrong role", func(c *factory.Assignment) { c.Role = project.RoleReviewer }, 45, goodDeadline)
	tryInsert("positive issue", func(c *factory.Assignment) { c.Issue = 3 }, 45, goodDeadline)
	tryInsert("subject mismatch", func(c *factory.Assignment) { c.Subject.Completion.CycleID = factory.NewID() }, 45, goodDeadline)
	// A deadline beyond the admitted remaining time exhausts deterministically.
	beyondTx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertReassessmentAttemptAllowanceTx(ctx, beyondTx, a, 45, now.Add(10*time.Hour), now); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
		_ = beyondTx.Rollback()
		t.Fatalf("deadline beyond remaining = %v", err)
	}
	_ = beyondTx.Rollback()
	// A valid assignment with no recorded row refuses before insertion.
	unrecorded := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	tryInsertOn := func() {
		t.Helper()
		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err = insertReassessmentAttemptAllowanceTx(ctx, tx, unrecorded, 45, goodDeadline, now); err == nil {
			t.Fatal("unrecorded root accepted")
		}
	}
	tryInsertOn()
	// Out-of-bound explicit minutes refuse even on a recorded root.
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = insertReassessmentAttemptAllowanceTx(ctx, tx, a, 200, goodDeadline, now); err == nil {
		t.Fatal("two-hundred minutes accepted")
	}
}

func TestReassessmentLifecyclePreservesConsumption(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	// A no-op close on a root with remaining time preserves the record.
	if closed, err := db.CloseExpiredAttempt(ctx, a.ID); err != nil || closed {
		t.Fatalf("fresh completion root closed = %v, %v", closed, err)
	}
	// The generic ROOT-scoped checkpoint joint consumes the valid
	// completion record: five active minutes accrue, nothing else moves.
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = transitionAttemptRootTx(ctx, tx, 7, 0, a.ID, false, now.Add(5*time.Minute)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	checkpointed, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil || checkpointed.Active || checkpointed.ActiveSeconds != 300 || checkpointed.Revision != 1 ||
		len(checkpointed.Corrections) != 0 || len(checkpointed.Reviews) != 0 {
		t.Fatalf("checkpoint lost consumption: %+v, %v", checkpointed, err)
	}
	// The generic repository freeze preserves the same consumption while
	// stopping the clock.
	withdrawal, err := db.WithdrawDispatch(ctx, 7, factory.CauseControlPaused, "soda-tester")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.FreezeAttemptAllowances(ctx, 7, withdrawal.Revision, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	frozen, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil || frozen.Active || frozen.ActiveSeconds != 300 ||
		*checkpointed.Completion != *frozen.Completion || frozen.Limits != checkpointed.Limits {
		t.Fatalf("freeze lost consumption: %+v, %v", frozen, err)
	}
}

func TestReassessmentScopeImmutableOnCAS(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	current, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*factory.AttemptAllowance)
	}{
		{"admitted minutes", func(x *factory.AttemptAllowance) { x.Limits.ActiveMinutes = 60 }},
		{"forged budget", func(x *factory.AttemptAllowance) { x.Limits.ReviewRounds = 1 }},
		{"swapped subject", func(x *factory.AttemptAllowance) {
			cloned := *x.Completion
			cloned.CycleID = factory.NewID()
			x.Completion = &cloned
		}},
		{"swapped root", func(x *factory.AttemptAllowance) { x.RootAssignment = factory.NewID() }},
	} {
		forged := current
		tc.mutate(&forged)
		forged.Revision++
		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = saveAttemptAllowanceTx(ctx, tx, forged)
		_ = tx.Rollback()
		if err == nil {
			t.Fatalf("%s saved over immutable scope", tc.name)
		}
	}
	// The untouched record still reads back identically.
	untouched, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil || untouched.Revision != 0 || untouched.ActiveSeconds != 0 {
		t.Fatalf("refused CAS changed the record: %+v, %v", untouched, err)
	}
}

func saveReassessmentNext(t *testing.T, ctx context.Context, db *Store, next factory.AttemptAllowance) error {
	t.Helper()
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = saveAttemptAllowanceTx(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit()
}

func readReassessmentCurrent(t *testing.T, ctx context.Context, db *Store, a factory.Assignment) factory.AttemptAllowance {
	t.Helper()
	current, err := db.ReassessmentAttemptAllowance(ctx, a.Repository, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

// TestReassessmentStaleSnapshotSaveRefuses replays the independent 055
// counterexample: a two-transaction stale snapshot must refuse with
// ErrStaleRevision and preserve the committed revision/consumption, and
// no fresh revision may refill clock or erase charges.
func TestReassessmentStaleSnapshotSaveRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)

	// Genuine first save: rev0 -> rev1 with 600s spent. Commits.
	first := readReassessmentCurrent(t, ctx, db, a)
	first.Revision = 1
	first.ActiveSeconds = 600
	first.CheckpointUnix = now.Unix()
	if err := saveReassessmentNext(t, ctx, db, first); err != nil {
		t.Fatalf("genuine first save refused: %v", err)
	}
	// Stale snapshot: the pre-commit rev0 view checkpoints +60s and saves
	// as rev1 after rev1 already committed. Must refuse; durable stays
	// rev1/spent600.
	stale := readReassessmentCurrent(t, ctx, db, a)
	stale.ActiveSeconds = 60
	stale.CheckpointUnix = now.Unix()
	stale.Revision = 1
	if err := saveReassessmentNext(t, ctx, db, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale snapshot save = %v, want ErrStaleRevision", err)
	}
	// Same-revision replay and skipped revisions refuse as well.
	if err := saveReassessmentNext(t, ctx, db, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("same-revision save = %v, want ErrStaleRevision", err)
	}
	skipped := readReassessmentCurrent(t, ctx, db, a)
	skipped.Revision = 3
	skipped.ActiveSeconds = 601
	skipped.CheckpointUnix = now.Unix()
	if err := saveReassessmentNext(t, ctx, db, skipped); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("skipped-revision save = %v, want ErrStaleRevision", err)
	}
	durable := readReassessmentCurrent(t, ctx, db, a)
	if durable.Revision != 1 || durable.ActiveSeconds != 600 {
		t.Fatalf("durable state rewritten = rev%d spent%d", durable.Revision, durable.ActiveSeconds)
	}
	// Fresh-revision refill attempts refuse: clock rollback, closed-root
	// reopen, and consumption erasure on an otherwise valid next revision.
	rollback := durable
	rollback.Revision = 2
	rollback.ActiveSeconds = 60
	rollback.CheckpointUnix = now.Unix()
	if err := saveReassessmentNext(t, ctx, db, rollback); err == nil {
		t.Fatal("clock rollback admitted")
	}
	closed, err := durable.Checkpoint(now, false)
	if err != nil {
		t.Fatal(err)
	}
	closed.Active = false
	closed.Closed = true
	closed.Revision = 2
	if err := saveReassessmentNext(t, ctx, db, closed); err != nil {
		t.Fatalf("valid close refused: %v", err)
	}
	reopen := readReassessmentCurrent(t, ctx, db, a)
	reopen.Revision = 3
	reopen.Closed = false
	reopen.Active = true
	if err := saveReassessmentNext(t, ctx, db, reopen); err == nil {
		t.Fatal("closed-root reopen admitted")
	}
	// Valid next checkpoint on the closed root's successor state refuses
	// too (closed stays closed); a genuinely open root still advances.
	advance := readReassessmentCurrent(t, ctx, db, a)
	advance.Revision = 3
	advance.CheckpointUnix = now.Add(time.Minute).Unix()
	if err := saveReassessmentNext(t, ctx, db, advance); err != nil {
		t.Fatalf("valid next save refused: %v", err)
	}
	final := readReassessmentCurrent(t, ctx, db, a)
	if final.Revision != 3 || !final.Closed || final.ActiveSeconds != 600 {
		t.Fatalf("final state = %+v", final)
	}
}

func TestReassessmentPositiveIssueAllowanceRegression(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	allowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || allowance.RootAssignment != a.ID || allowance.Limits != factory.DefaultAttemptLimits() ||
		allowance.Completion != nil || !allowance.Active {
		t.Fatalf("positive-issue allowance = %+v, %v", allowance, err)
	}
	// Root-scoped lock, checkpoint and CAS save still round-trip positive
	// roots with no completion subject.
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := lockAttemptAllowanceRootTx(ctx, tx, a.Repository, a.Issue, a.ID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	// Deterministic anchor: the checkpoint instant derives from the
	// actually stored CheckpointUnix, so a Unix-second boundary between
	// test setup and packet insert cannot shrink the expected minute.
	anchor := time.Unix(locked.CheckpointUnix, 0)
	advanced, err := locked.Checkpoint(anchor.Add(time.Minute), true)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	advanced.Revision++
	if err = saveAttemptAllowanceTx(ctx, tx, advanced); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	reread, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || reread.ActiveSeconds != 60 || reread.Revision != 1 {
		t.Fatalf("positive-issue round trip = %+v, %v", reread, err)
	}
	// Spending an issue correction on the positive root still works, and
	// the typed completion getter never sees it.
	if err = func() error {
		inner, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = inner.Rollback() }()
		if err = consumeCorrectionAttemptTx(ctx, inner, a, anchor.Add(2*time.Minute)); err != nil {
			return err
		}
		return inner.Commit()
	}(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ReassessmentAttemptAllowance(ctx, a.Repository, factory.CompletionSubject{
		CompletionID: factory.NewID(), CycleID: factory.NewID(), InventoryDigest: strings.Repeat("d", 64),
	}, a.ID); err == nil {
		t.Fatal("typed completion lookup answered a positive-issue root")
	}
}

// TestReassessmentTerminalFinishSettledCheckpointsNoReadiness proves
// 062 defect 1 repaired: a valid candidate-free completion result,
// succeeded and reported, finishes atomically in the same terminal
// transaction with zero readiness work. Its recorded run is settled,
// so consumed time checkpoints exactly and the root closes.
func TestReassessmentTerminalFinishSettledCheckpointsNoReadiness(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	before, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	insertCompletionRunRow(t, ctx, db, a, completionRunFixture(t, a, now, true), false, true)
	sourcesBefore, generationBefore := readinessWorkCount(t, ctx, db)
	if err = db.FinishAssignment(ctx, finishCompletionRoot(t, a, now, factory.Succeeded, factory.AssignReasonReported)); err != nil {
		t.Fatalf("settled completion finish refused: %v", err)
	}
	if got := storedAssignmentStage(t, ctx, db, a.ID); got != factory.AssignmentFinished {
		t.Fatalf("completion assignment stage = %q", got)
	}
	after, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Closed || after.Active {
		t.Fatalf("settled terminal completion did not close+stop: %+v", after)
	}
	// Stored checkpoint arithmetic: the consumed seconds must exactly
	// equal the checkpoint advance, with no sleeps or tolerances.
	if after.ActiveSeconds != after.CheckpointUnix-before.CheckpointUnix {
		t.Fatalf("consumed %d != checkpoint advance %d", after.ActiveSeconds, after.CheckpointUnix-before.CheckpointUnix)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("terminal checkpoint revision = %d, want %d", after.Revision, before.Revision+1)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != sourcesBefore || generation != generationBefore || sources != 0 {
		t.Fatalf("completion finish enqueued readiness work: sources %d->%d generation %d->%d",
			sourcesBefore, sources, generationBefore, generation)
	}
}

// TestReassessmentTerminalFinishUnsettledKeepsClock proves the shared
// joint retains the active clock when the recorded run is still
// unsettled: the finish still commits atomically with zero readiness
// work, the coder-only admission closes, but no time is consumed and
// the checkpoint does not move.
func TestReassessmentTerminalFinishUnsettledKeepsClock(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	a := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, a)
	commitReassessmentInsert(t, ctx, db, a, 45, now.Add(30*time.Minute), now)
	before, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A genuinely unsettled recorded run: active, unreconciled, with a
	// run view bound to this root. The active clock must survive it.
	insertCompletionRunRow(t, ctx, db, a, completionRunFixture(t, a, now, false), true, false)
	if err = db.FinishAssignment(ctx, finishCompletionRoot(t, a, now, factory.Succeeded, factory.AssignReasonReported)); err != nil {
		t.Fatalf("unsettled completion finish refused: %v", err)
	}
	after, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(a), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Closed || !after.Active {
		t.Fatalf("unsettled terminal completion must close admission yet keep its clock: %+v", after)
	}
	if after.ActiveSeconds != 0 || after.CheckpointUnix != before.CheckpointUnix {
		t.Fatalf("unsettled finish stopped the clock early: %+v", after)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("close-only revision = %d, want %d", after.Revision, before.Revision+1)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != 0 || generation != 1 {
		t.Fatalf("unsettled completion finish enqueued readiness work: sources %d generation %d", sources, generation)
	}
}

// TestReassessmentTransitionAddressesOlderCycle proves 062 defect 2
// repaired at the shared joint: with a newer different cycle as the
// latest (repository, 0) row, settling the older root checkpoints and
// closes ONLY the older allowance at a deterministic instant, while
// the later root stays byte-identical. Before the repair the joint
// consulted latest(repository, 0) and silently skipped the older root.
func TestReassessmentTransitionAddressesOlderCycle(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	completion := factory.NewID()
	older := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	newer := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, older)
	insertReassessmentRootRow(t, ctx, db, newer)
	commitReassessmentInsert(t, ctx, db, older, 45, now.Add(30*time.Minute), now)
	commitReassessmentInsert(t, ctx, db, newer, 60, now.Add(30*time.Minute), now)
	olderBefore, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(older), older.ID)
	if err != nil {
		t.Fatal(err)
	}
	newerBefore, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(newer), newer.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The older root's recorded run is settled; the newer root keeps a
	// genuinely unsettled recorded run so per-root addressing is proven
	// on both sides.
	insertCompletionRunRow(t, ctx, db, older, completionRunFixture(t, older, now, true), false, true)
	insertCompletionRunRow(t, ctx, db, newer, completionRunFixture(t, newer, now, false), true, false)
	// Deterministic instant: exactly five active minutes past the older
	// root's recorded checkpoint.
	at := time.Unix(olderBefore.CheckpointUnix+300, 0)
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = transitionAttemptAssignmentTx(ctx, tx, older.ID, true, at); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	olderAfter, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(older), older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !olderAfter.Closed || olderAfter.Active || olderAfter.ActiveSeconds != 300 ||
		olderAfter.CheckpointUnix != olderBefore.CheckpointUnix+300 || olderAfter.Revision != olderBefore.Revision+1 {
		t.Fatalf("older root not settled exactly: %+v", olderAfter)
	}
	newerAfter, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(newer), newer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if newerAfter.Revision != newerBefore.Revision || newerAfter.ActiveSeconds != newerBefore.ActiveSeconds ||
		newerAfter.CheckpointUnix != newerBefore.CheckpointUnix || newerAfter.Active != newerBefore.Active ||
		newerAfter.Closed != newerBefore.Closed || newerAfter.Limits != newerBefore.Limits {
		t.Fatalf("newer root changed: before %+v after %+v", newerBefore, newerAfter)
	}
}

// TestReassessmentTerminalFinishNeedsTypedAllowance proves terminal
// assignment cannot commit without its accounting: a missing typed
// allowance and a mismatched typed subject both fail the finish and
// roll the assignment back to assigned.
func TestReassessmentTerminalFinishNeedsTypedAllowance(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	// Missing: recorded root and settled run, but no typed allowance row.
	missing := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, missing)
	insertCompletionRunRow(t, ctx, db, missing, completionRunFixture(t, missing, now, true), false, true)
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, missing, now, factory.Succeeded, factory.AssignReasonReported)); err == nil {
		t.Fatal("completion finish committed without its typed allowance")
	}
	if got := storedAssignmentStage(t, ctx, db, missing.ID); got != factory.AssignmentAssigned {
		t.Fatalf("missing-allowance finish left stage = %q", got)
	}
	// Mismatched: the recorded root and allowance bind cycle one, but
	// the finishing assignment carries cycle two under the same
	// completion event. The canonical lineage key still matches, so the
	// typed subject check is what must refuse.
	recorded := reassessmentRootFixture(t, now, 7, factory.NewID(), factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, recorded)
	commitReassessmentInsert(t, ctx, db, recorded, 45, now.Add(30*time.Minute), now)
	insertCompletionRunRow(t, ctx, db, recorded, completionRunFixture(t, recorded, now, true), false, true)
	other := recorded
	cloned := *other.Subject.Completion
	cloned.CycleID = factory.NewID()
	other.Subject.Completion = &cloned
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, other, now, factory.Succeeded, factory.AssignReasonReported)); err == nil {
		t.Fatal("completion finish committed with a mismatched typed allowance")
	}
	if got := storedAssignmentStage(t, ctx, db, recorded.ID); got != factory.AssignmentAssigned {
		t.Fatalf("mismatched-allowance finish left stage = %q", got)
	}
	// A failed non-success finish needs its accounting too: the
	// cancellation-family close policy is preserved, but the typed row
	// stays mandatory.
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, missing, now, factory.Failed, factory.AssignReasonRunFailed)); err == nil {
		t.Fatal("failed completion finish committed without its typed allowance")
	}
	if got := storedAssignmentStage(t, ctx, db, missing.ID); got != factory.AssignmentAssigned {
		t.Fatalf("failed missing-allowance finish left stage = %q", got)
	}
}

// TestReassessmentLateSettlementStopsClock proves the 067 late path: a
// completion root finished while its real recorded run was unsettled
// keeps an ACTIVE clock; settling that same run through the public
// SaveFactoryRun then checkpoints ONLY the original root while a newer
// other-cycle root in the same repository stays unchanged.
func TestReassessmentLateSettlementStopsClock(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	completion := factory.NewID()
	original := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	later := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, original)
	insertReassessmentRootRow(t, ctx, db, later)
	commitReassessmentInsert(t, ctx, db, original, 45, now.Add(30*time.Minute), now)
	commitReassessmentInsert(t, ctx, db, later, 60, now.Add(30*time.Minute), now)
	insertCompletionRunRow(t, ctx, db, original, completionRunFixture(t, original, now, false), true, false)
	insertCompletionRunRow(t, ctx, db, later, completionRunFixture(t, later, now, false), true, false)
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, original, now, factory.Succeeded, factory.AssignReasonReported)); err != nil {
		t.Fatalf("unsettled completion finish refused: %v", err)
	}
	closed, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(original), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.Closed || !closed.Active || closed.ActiveSeconds != 0 {
		t.Fatalf("finished unsettled root must stay active: %+v", closed)
	}
	laterBefore, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(later), later.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Settle the original recorded run through the public lifecycle:
	// terminal outcome plus reconciliation on the actually recorded row.
	stored, err := db.FactoryRun(ctx, original.Run)
	if err != nil {
		t.Fatal(err)
	}
	stored.Outcome, stored.Reconciled, stored.Summary = factory.Succeeded, true, "run settled"
	if err = db.SaveFactoryRun(ctx, stored); err != nil {
		t.Fatalf("late settlement refused: %v", err)
	}
	settled, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(original), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !settled.Closed || settled.Active {
		t.Fatalf("late settlement did not stop the typed clock: %+v", settled)
	}
	if settled.ActiveSeconds < closed.ActiveSeconds || settled.ActiveSeconds != settled.CheckpointUnix-closed.CheckpointUnix {
		t.Fatalf("late consumption invalid: %+v", settled)
	}
	if settled.Revision != closed.Revision+1 {
		t.Fatalf("late settlement revision = %d, want %d", settled.Revision, closed.Revision+1)
	}
	laterAfter, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(later), later.ID)
	if err != nil {
		t.Fatal(err)
	}
	if laterAfter.Revision != laterBefore.Revision || laterAfter.ActiveSeconds != laterBefore.ActiveSeconds ||
		laterAfter.CheckpointUnix != laterBefore.CheckpointUnix || laterAfter.Active != laterBefore.Active ||
		laterAfter.Closed != laterBefore.Closed || laterAfter.Limits != laterBefore.Limits {
		t.Fatalf("later root changed by foreign settlement: before %+v after %+v", laterBefore, laterAfter)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != 0 || generation != 1 {
		t.Fatalf("late settlement enqueued readiness work: sources %d generation %d", sources, generation)
	}
}

// TestReassessmentCancelledLateSettlementFreezesClock replays the
// independent 070 counterproof: a terminal cancelled completion keeps
// Closed false per policy, so the late-settlement branch must still
// reach the shared joint once actual effects settle — never skip the
// freeze forever and charge stopped time.
func TestReassessmentCancelledLateSettlementFreezesClock(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	completion := factory.NewID()
	original := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	later := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, original)
	insertReassessmentRootRow(t, ctx, db, later)
	commitReassessmentInsert(t, ctx, db, original, 45, now.Add(30*time.Minute), now)
	commitReassessmentInsert(t, ctx, db, later, 60, now.Add(30*time.Minute), now)
	insertCompletionRunRow(t, ctx, db, original, completionRunFixture(t, original, now, false), true, false)
	insertCompletionRunRow(t, ctx, db, later, completionRunFixture(t, later, now, false), true, false)
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, original, now, factory.Cancelled, factory.AssignReasonCancelled)); err != nil {
		t.Fatalf("cancelled completion finish refused: %v", err)
	}
	open, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(original), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open.Closed || !open.Active || open.ActiveSeconds != 0 {
		t.Fatalf("cancelled root must stay open and active: %+v", open)
	}
	laterBefore, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(later), later.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Reconcile the original recorded run through the public lifecycle
	// with its terminal cancelled outcome.
	stored, err := db.FactoryRun(ctx, original.Run)
	if err != nil {
		t.Fatal(err)
	}
	stored.Outcome, stored.Reconciled, stored.Summary = factory.Cancelled, true, "run cancelled"
	if err = db.SaveFactoryRun(ctx, stored); err != nil {
		t.Fatalf("cancelled late settlement refused: %v", err)
	}
	frozen, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(original), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Closed {
		t.Fatalf("cancelled root must keep Closed false: %+v", frozen)
	}
	if frozen.Active {
		t.Fatalf("settled cancelled root still charges time: %+v", frozen)
	}
	if frozen.ActiveSeconds < open.ActiveSeconds || frozen.ActiveSeconds != frozen.CheckpointUnix-open.CheckpointUnix {
		t.Fatalf("cancelled late consumption invalid: %+v", frozen)
	}
	if frozen.Revision != open.Revision+1 {
		t.Fatalf("cancelled late settlement revision = %d, want %d", frozen.Revision, open.Revision+1)
	}
	laterAfter, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(later), later.ID)
	if err != nil {
		t.Fatal(err)
	}
	if laterAfter.Revision != laterBefore.Revision || laterAfter.ActiveSeconds != laterBefore.ActiveSeconds ||
		laterAfter.CheckpointUnix != laterBefore.CheckpointUnix || laterAfter.Active != laterBefore.Active ||
		laterAfter.Closed != laterBefore.Closed || laterAfter.Limits != laterBefore.Limits {
		t.Fatalf("later root changed by foreign settlement: before %+v after %+v", laterBefore, laterAfter)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != 0 || generation != 1 {
		t.Fatalf("cancelled settlement enqueued readiness work: sources %d generation %d", sources, generation)
	}
}

// TestReassessmentClosedAssignedLateSettlementFreezesClock replays the
// independent 072 counterproof: a closed-but-still-assigned root
// (admission closed by the terminal transition while its run was
// unsettled) must reach the joint on late settlement — waiting on a
// finish row that may never come would charge stopped time forever.
func TestReassessmentClosedAssignedLateSettlementFreezesClock(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	completion := factory.NewID()
	original := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	later := reassessmentRootFixture(t, now, 7, completion, factory.NewID(), strings.Repeat("d", 64))
	insertReassessmentRootRow(t, ctx, db, original)
	insertReassessmentRootRow(t, ctx, db, later)
	commitReassessmentInsert(t, ctx, db, original, 45, now.Add(30*time.Minute), now)
	commitReassessmentInsert(t, ctx, db, later, 60, now.Add(30*time.Minute), now)
	insertCompletionRunRow(t, ctx, db, original, completionRunFixture(t, original, now, false), true, false)
	insertCompletionRunRow(t, ctx, db, later, completionRunFixture(t, later, now, false), true, false)
	// Close admission directly while the assignment stays ASSIGNED and
	// the run stays unsettled, mirroring the terminal transition.
	open, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(original), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	open.Closed = true
	open.Revision++
	if err := saveReassessmentNext(t, ctx, db, open); err != nil {
		t.Fatalf("admission close refused: %v", err)
	}
	laterBefore, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(later), later.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Reconcile the original recorded run (actual stop confirmed) with
	// no assignment finish row ever written.
	stored, err := db.FactoryRun(ctx, original.Run)
	if err != nil {
		t.Fatal(err)
	}
	stored.Outcome, stored.Reconciled, stored.Summary = factory.Cancelled, true, "run stopped"
	if err = db.SaveFactoryRun(ctx, stored); err != nil {
		t.Fatalf("closed-assigned late settlement refused: %v", err)
	}
	frozen, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(original), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.Closed || frozen.Active {
		t.Fatalf("closed-assigned root not frozen: %+v", frozen)
	}
	if frozen.ActiveSeconds < open.ActiveSeconds || frozen.ActiveSeconds != frozen.CheckpointUnix-open.CheckpointUnix {
		t.Fatalf("closed-assigned late consumption invalid: %+v", frozen)
	}
	if frozen.Revision != open.Revision+1 {
		t.Fatalf("closed-assigned late settlement revision = %d, want %d", frozen.Revision, open.Revision+1)
	}
	laterAfter, err := db.ReassessmentAttemptAllowance(ctx, 7, reassessmentSubjectOf(later), later.ID)
	if err != nil {
		t.Fatal(err)
	}
	if laterAfter.Revision != laterBefore.Revision || laterAfter.ActiveSeconds != laterBefore.ActiveSeconds ||
		laterAfter.CheckpointUnix != laterBefore.CheckpointUnix || laterAfter.Active != laterBefore.Active ||
		laterAfter.Closed != laterBefore.Closed || laterAfter.Limits != laterBefore.Limits {
		t.Fatalf("later root changed by foreign settlement: before %+v after %+v", laterBefore, laterAfter)
	}
	if sources, generation := readinessWorkCount(t, ctx, db); sources != 0 || generation != 1 {
		t.Fatalf("closed-assigned settlement enqueued readiness work: sources %d generation %d", sources, generation)
	}
}
