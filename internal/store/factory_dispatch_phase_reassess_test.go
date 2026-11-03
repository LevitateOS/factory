package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

func countReassessAssignmentRows(t *testing.T, db *Store, id string) int {
	t.Helper()
	var count int
	if err := db.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM factory_assignments WHERE id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Typed completion-root packet admission (D13/FW32, R11) against real
// PostgreSQL: one reassessment fresh root binds its verified completion
// event and staged open cycle, funds its time-only allowance in the same
// packet transaction, and refuses unrecorded completions, unstaged
// cycles and foreign bindings. Every test below opens its own owned
// disposable database; nothing is borrowed.

// reassessCycleFixture records one verified completion on the fixture
// repository, seals its first cycle with a decided reassess outcome,
// and returns its event ID, cycle ID and merged commit. The sealed
// cycle carries inventory digest c*64 over one sealed body with local
// source m.MergedCommit, so an admitted root must bind all three.
func reassessCycleFixture(t *testing.T, db *Store) (eventID, cycleID, merged string) {
	t.Helper()
	m := mergedCompletionRecord(factory.NewID(), "501")
	if !recordTestCompletion(t, db, m) {
		t.Fatal("completion not recorded")
	}
	ctx := context.Background()
	row := db.db.QueryRowContext(ctx, `SELECT event,cycle FROM factory_reassessments WHERE repository=$1 ORDER BY generation DESC LIMIT 1`, m.Repository)
	if err := row.Scan(&eventID, &cycleID); err != nil {
		t.Fatalf("staged cycle not recorded: %v", err)
	}
	sealReassessmentCycle(t, ctx, db, cycleID, m.MergedCommit, factory.ReassessmentOptionReassess)
	return eventID, cycleID, m.MergedCommit
}

// sealReassessmentCycle drives one collecting cycle to a sealed,
// decided outcome through the production CAS joint: the collecting
// seal (local bindings, inventory digest, one body), the deciding
// decision. The packet owns the reassessing transition and sole-run
// ledger atomically with its funding. Every write carries the expected state,
// so the fixture exercises the same ordering the packet guard relies
// on. A continue decision stays in deciding: only reassess advances a
// coder root.
func sealReassessmentCycle(t *testing.T, ctx context.Context, db *Store, cycleID, source, decision string) {
	t.Helper()
	record, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Cycle.State != factory.ReassessmentCollecting {
		t.Fatalf("staged cycle state = %q", record.Cycle.State)
	}
	local := sealedTestLocal(t)
	local.SourceCommit = source
	record.Local = local
	record.InventoryDigest = strings.Repeat("c", 64)
	record.Evidence = []factory.CompletionIssueEvidence{sealedTestBody(t, 9, "title", "body")}
	record.EvidenceIssues = 1
	record.EvidenceRevision = 41
	if err := record.Validate(); err != nil {
		t.Fatalf("sealed cycle invalid: %v", err)
	}
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, record); err != nil {
		t.Fatalf("seal not recorded: %v", err)
	}
	decided, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	decided.Cycle.Decision = decision
	decided.Cycle.State = factory.ReassessmentDeciding
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, decided); err != nil {
		t.Fatalf("decision not recorded: %v", err)
	}
}

// countAttemptAllowanceRows reports recorded allowance rows for one
// attempt root: refusals must leave zero.
func countAttemptAllowanceRows(t *testing.T, db *Store, root string) int {
	t.Helper()
	var count int
	if err := db.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM factory_attempt_allowances WHERE root_assignment=$1`, root).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// reassessRootPacket builds one completion reassessment fresh-root
// packet: repository slot, Issue zero, fresh attempt root, reassess
// phase, coder role, no parent, no publication, and the canonical
// completion subject.
func reassessRootPacket(t *testing.T, ctx context.Context, db *Store, now time.Time, eventID, cycleID, source string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	selection, ok := grantTestPolicy().Roles[project.RoleCoder]
	if !ok {
		t.Fatal("coder fixture role has no policy selection")
	}
	prompt := []byte("reassessment root packet " + cycleID)
	sum := sha256.Sum256(prompt)
	rootID, runID := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		Authority: reviewCorrectAuthority(t, ctx, db, dispatchTestProjectID(), 7, "conn"),
		ID:        rootID, AttemptRoot: rootID, PublicationAssignment: "", ParentAssignment: "",
		Subject: factory.ExecutionSubject{
			Kind:       factory.SubjectCompletion,
			Completion: &factory.CompletionSubject{CompletionID: eventID, CycleID: cycleID, InventoryDigest: strings.Repeat("c", 64)},
		},
		Phase:     project.ExecutionReassess,
		ProjectID: dispatchTestProjectID(), ActorID: 7, Role: project.RoleCoder,
		Repository: 7, Issue: 0, NativeRev: 5, Acceptance: "", Preparation: "f" + strings.Repeat("e", 24),
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: "conn", SourceCommit: source,
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(),
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("reassessment root invalid: %v", err)
	}
	seedReadyPacketPreparation(t, db, a)
	r := factory.Reservation{AssignmentID: a.ID, Repository: 7, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: source,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: 7, Issue: 0, Attempt: a.ID}
	return a, r, run, view
}

func reassessTestControl(a factory.Assignment) factory.IssueControl {
	return factory.IssueControl{
		Repository: a.Repository, Issue: a.Issue, Acceptance: a.Acceptance, NativeRev: a.NativeRev,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
}

func TestReassessRootAdmitsThroughReassessGuard(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("reassessment root refused: %v", err)
	}
	allowance, err := db.ReassessmentAttemptAllowance(ctx, 7, *a.Subject.Completion, a.ID)
	if err != nil {
		t.Fatalf("reassessment allowance not funded: %v", err)
	}
	if allowance.Limits.ActiveMinutes != factory.DefaultReassessmentMinutes || !allowance.Active || allowance.Closed {
		t.Fatalf("reassessment allowance mis-funded: %+v", allowance)
	}
	cycle, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil || cycle.Cycle.State != factory.ReassessmentReassessing || len(cycle.Cycle.Ledger.CoderRuns) != 1 || cycle.Cycle.Ledger.CoderRuns[0] != run.ID {
		t.Fatalf("packet did not admit its sole coder: %+v %v", cycle, err)
	}
}

func TestReassessRootRefusesUnrecordedCompletion(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, factory.NewID(), factory.NewID(), strings.Repeat("c", 40))
	err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view)
	if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("unrecorded completion error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
}

func TestReassessRootRefusesForeignBindings(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	cases := map[string]func(*factory.Assignment){
		"parent":      func(a *factory.Assignment) { a.ParentAssignment = factory.NewID() },
		"publication": func(a *factory.Assignment) { a.PublicationAssignment = factory.NewID() },
		"issue":       func(a *factory.Assignment) { a.Issue = 3 },
		"role":        func(a *factory.Assignment) { a.Role = project.RoleReviewer },
		"cycle":       func(a *factory.Assignment) { a.Subject.Completion.CycleID = factory.NewID() },
	}
	for name, mutate := range cases {
		a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
		mutate(&a)
		if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err == nil {
			t.Fatalf("%s binding admitted", name)
		}
		assertDispatchPacketAbsent(t, db, a)
	}
}

func TestReassessRootReplayWritesNoRows(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first reassessment packet refused: %v", err)
	}
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err == nil {
		t.Fatal("replayed reassessment packet admitted")
	}
	count := countReassessAssignmentRows(t, db, a.ID)
	if count != 1 {
		t.Fatalf("replay left %d assignment rows, want exactly one", count)
	}
}

// TestReassessRootRefusesCollectingCycle proves the guard binds the
// seal, not the staging row: a freshly collected cycle with no sealed
// inventory, source or decision refuses with no packet or charge rows.
func TestReassessRootRefusesCollectingCycle(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	// A sealed decided cycle coexists on the same repository: only the
	// collecting one must refuse.
	_, _, _ = reassessCycleFixture(t, db)
	second := mergedCompletionRecord(factory.NewID(), "502")
	if !recordTestCompletion(t, db, second) {
		t.Fatal("second completion not recorded")
	}
	var eventID, cycleID string
	row := db.db.QueryRowContext(ctx, `SELECT event,cycle FROM factory_reassessments WHERE repository=$1 ORDER BY generation DESC LIMIT 1`, second.Repository)
	if err := row.Scan(&eventID, &cycleID); err != nil {
		t.Fatal(err)
	}
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, second.MergedCommit)
	err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view)
	if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("collecting cycle error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
	if count := countAttemptAllowanceRows(t, db, a.ID); count != 0 {
		t.Fatalf("refused collecting root funded %d allowance rows", count)
	}
}

// TestReassessRootRefusesSealMismatch proves the guard binds the exact
// sealed inventory digest and sealed source commit: either drift
// refuses with no packet or charge rows.
func TestReassessRootRefusesSealMismatch(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	a.Subject.Completion.InventoryDigest = strings.Repeat("e", 64)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err == nil {
		t.Fatal("foreign inventory digest admitted")
	} else if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("foreign digest error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
	if count := countAttemptAllowanceRows(t, db, a.ID); count != 0 {
		t.Fatalf("refused foreign digest funded %d allowance rows", count)
	}
	a, r, run, view = reassessRootPacket(t, ctx, db, now, eventID, cycleID, strings.Repeat("d", 40))
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err == nil {
		t.Fatal("foreign source commit admitted")
	} else if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("foreign source error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
	if count := countAttemptAllowanceRows(t, db, a.ID); count != 0 {
		t.Fatalf("refused foreign source funded %d allowance rows", count)
	}
}

// TestReassessRootRefusesIneligibleDecision proves only a decided
// reassess outcome in its reassessing state admits a coder root: a
// continue decision (staying in deciding) and a released terminal
// cycle both refuse with no packet or charge rows.
func TestReassessRootRefusesIneligibleDecision(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	m := mergedCompletionRecord(factory.NewID(), "503")
	if !recordTestCompletion(t, db, m) {
		t.Fatal("completion not recorded")
	}
	var eventID, cycleID string
	row := db.db.QueryRowContext(ctx, `SELECT event,cycle FROM factory_reassessments WHERE repository=$1 ORDER BY generation DESC LIMIT 1`, m.Repository)
	if err := row.Scan(&eventID, &cycleID); err != nil {
		t.Fatal(err)
	}
	sealReassessmentCycle(t, ctx, db, cycleID, m.MergedCommit, factory.ReassessmentOptionContinue)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, m.MergedCommit)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err == nil {
		t.Fatal("continue decision admitted a coder root")
	} else if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("continue decision error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
	if count := countAttemptAllowanceRows(t, db, a.ID); count != 0 {
		t.Fatalf("refused continue decision funded %d allowance rows", count)
	}
	released, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	released.Cycle.State = factory.ReassessmentReleased
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, released); err != nil {
		t.Fatalf("release not recorded: %v", err)
	}
	a, r, run, view = reassessRootPacket(t, ctx, db, now, eventID, cycleID, m.MergedCommit)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err == nil {
		t.Fatal("released cycle admitted a coder root")
	} else if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("released cycle error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
	if count := countAttemptAllowanceRows(t, db, a.ID); count != 0 {
		t.Fatalf("refused released cycle funded %d allowance rows", count)
	}
}

// TestReassessConcurrentRootsSingleCharge proves R11 one-run/charge
// acceptance across distinct roots for one cycle: the first fresh root
// admits and funds exactly once, and a concurrent second root for the
// same sealed cycle refuses with ErrAssignmentActive — the unfinished
// completion assignment uniqueness — leaving no second packet or
// charge rows while the first allowance stays intact.
func TestReassessConcurrentRootsSingleCharge(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	first, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(first), first, r, run, view); err != nil {
		t.Fatalf("first concurrent root refused: %v", err)
	}
	second, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	err := db.RecordDispatchPacket(ctx, reassessTestControl(second), second, r, run, view)
	if !errors.Is(err, ErrAssignmentActive) {
		t.Fatalf("concurrent root error = %v, want concurrent admission refusal", err)
	}
	assertDispatchPacketAbsent(t, db, second)
	if count := countAttemptAllowanceRows(t, db, second.ID); count != 0 {
		t.Fatalf("refused concurrent root funded %d allowance rows", count)
	}
	allowance, err := db.ReassessmentAttemptAllowance(ctx, 7, *first.Subject.Completion, first.ID)
	if err != nil {
		t.Fatalf("first root lost its allowance: %v", err)
	}
	if allowance.Limits.ActiveMinutes != factory.DefaultReassessmentMinutes || !allowance.Active || allowance.Closed {
		t.Fatalf("first root allowance disturbed: %+v", allowance)
	}
}

// TestReassessmentSettledSubjectCannotRefill (real pg) proves failed
// completion work stays charged: after the first root reconciles failed,
// consumes its reservation and finishes failed, the same completion
// subject as a distinct root refuses with the settled-attempt error
// instead of funding a fresh allowance. Recorded consumption stands.
func TestReassessmentSettledSubjectCannotRefill(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first reassessment root refused: %v", err)
	}
	ended := time.Now().UTC().Truncate(time.Second)
	finishedRun := run
	finishedRun.Outcome, finishedRun.Reconciled, finishedRun.Summary = factory.Failed, true, "run failed"
	if err := db.SaveFactoryRun(ctx, finishedRun); err != nil {
		t.Fatalf("failed run not reconciled: %v", err)
	}
	if err := db.RecordRunUsage(ctx, factory.Usage{RunID: a.Run, Minutes: 1, Repository: 7, Connection: "conn", StartedAt: ended.Add(-time.Minute), EndedAt: ended}); err != nil {
		t.Fatalf("failed run usage not recorded: %v", err)
	}
	if err := db.ConsumeReservation(ctx, r.AssignmentID); err != nil {
		t.Fatalf("failed root reservation not consumed: %v", err)
	}
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, a, ended, factory.Failed, factory.AssignReasonRunFailed)); err != nil {
		t.Fatalf("failed root not finished: %v", err)
	}
	prior, err := db.ReassessmentAttemptAllowance(ctx, 7, *a.Subject.Completion, a.ID)
	if err != nil {
		t.Fatalf("failed root lost its allowance: %v", err)
	}
	if prior.Active || !prior.Closed {
		t.Fatalf("failed completion allowance still open: %+v", prior)
	}
	a2, r2, run2, view2 := reassessRootPacket(t, ctx, db, ended, eventID, cycleID, merged)
	err = db.RecordDispatchPacket(ctx, reassessTestControl(a2), a2, r2, run2, view2)
	if !errors.Is(err, factory.ErrAttemptClosed) {
		t.Fatalf("failed completion replay did not report the settled attempt: %v", err)
	}
	assertDispatchPacketAbsent(t, db, a2)
	settled, err := db.ReassessmentAttemptAllowance(ctx, 7, *a.Subject.Completion, a.ID)
	if err != nil {
		t.Fatalf("settled root lost its allowance: %v", err)
	}
	if settled.Active || !settled.Closed || settled.Limits.ActiveMinutes != factory.DefaultReassessmentMinutes {
		t.Fatalf("settled completion allowance disturbed: %+v", settled)
	}
}

// TestReassessmentNewCycleFundsNextAttempt (real pg) proves the
// legitimate next completion attempt still admits: after the first root
// settles failed, a newly staged cycle carries a new completion subject,
// and a fresh root bound to it funds its own allowance while the settled
// prior cycle stays charged.
func TestReassessmentNewCycleFundsNextAttempt(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	eventID, cycleID, merged := reassessCycleFixture(t, db)
	a, r, run, view := reassessRootPacket(t, ctx, db, now, eventID, cycleID, merged)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first reassessment root refused: %v", err)
	}
	ended := time.Now().UTC().Truncate(time.Second)
	finishedRun := run
	finishedRun.Outcome, finishedRun.Reconciled, finishedRun.Summary = factory.Failed, true, "run failed"
	if err := db.SaveFactoryRun(ctx, finishedRun); err != nil {
		t.Fatalf("failed run not reconciled: %v", err)
	}
	if err := db.RecordRunUsage(ctx, factory.Usage{RunID: a.Run, Minutes: 1, Repository: 7, Connection: "conn", StartedAt: ended.Add(-time.Minute), EndedAt: ended}); err != nil {
		t.Fatalf("failed run usage not recorded: %v", err)
	}
	if err := db.ConsumeReservation(ctx, r.AssignmentID); err != nil {
		t.Fatalf("failed root reservation not consumed: %v", err)
	}
	if err := db.FinishAssignment(ctx, finishCompletionRoot(t, a, ended, factory.Failed, factory.AssignReasonRunFailed)); err != nil {
		t.Fatalf("failed root not finished: %v", err)
	}
	eventID2, cycleID2, merged2 := reassessCycleFixture(t, db)
	a2, r2, run2, view2 := reassessRootPacket(t, ctx, db, ended, eventID2, cycleID2, merged2)
	if err := db.RecordDispatchPacket(ctx, reassessTestControl(a2), a2, r2, run2, view2); err != nil {
		t.Fatalf("next cycle root refused: %v", err)
	}
	funded, err := db.ReassessmentAttemptAllowance(ctx, 7, *a2.Subject.Completion, a2.ID)
	if err != nil {
		t.Fatalf("next cycle allowance not funded: %v", err)
	}
	if !funded.Active || funded.Limits.ActiveMinutes != factory.DefaultReassessmentMinutes {
		t.Fatalf("next cycle allowance not funded: %+v", funded)
	}
	prior, err := db.ReassessmentAttemptAllowance(ctx, 7, *a.Subject.Completion, a.ID)
	if err != nil {
		t.Fatalf("prior cycle lost its allowance: %v", err)
	}
	if prior.Active || !prior.Closed || prior.Limits.ActiveMinutes != factory.DefaultReassessmentMinutes {
		t.Fatalf("prior cycle allowance disturbed: %+v", prior)
	}
}
