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

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// Typed intent-root packet admission (D01/R11) against real PostgreSQL:
// one decompose fresh root binds its persisted head intent revision,
// funds its time-only allowance in the same packet transaction, and
// refuses unrecorded, superseded and foreign revisions. Every test below
// opens its own owned disposable database; nothing is borrowed.

// decomposeIntentRevisionFixture persists one draft intent revision on the fixture
// repository and returns its intent and revision IDs.
func decomposeIntentRevisionFixture(t *testing.T, db *Store, intentID, revisionID string, owner int64) {
	t.Helper()
	revision := factory.IntentRevision{
		ID: revisionID, Intent: intentID, Owner: owner, Repository: 7,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: time.Now().UTC().Truncate(time.Second).Unix(),
	}
	if err := db.SaveIntentRevision(context.Background(), revision); err != nil {
		t.Fatalf("intent revision not recorded: %v", err)
	}
}

// decomposeRevisionDigest reads one persisted intent revision row and
// returns its authoritative digest: the exact value admission binds
// the subject to. Placeholder digests never enter a packet.
func decomposeRevisionDigest(t *testing.T, db *Store, revisionID string) string {
	t.Helper()
	var raw []byte
	if err := db.db.QueryRowContext(context.Background(), `SELECT data FROM factory_intent_revisions WHERE id=$1`, revisionID).Scan(&raw); err != nil {
		t.Fatalf("intent revision unreadable: %v", err)
	}
	var revision factory.IntentRevision
	if err := json.Unmarshal(raw, &revision); err != nil {
		t.Fatal(err)
	}
	return revision.Digest()
}

// decomposeRootPacket builds one intent decompose fresh-root packet:
// repository slot, Issue zero, fresh attempt root, decompose phase,
// coder role, no parent, no publication, and the canonical intent
// subject bound to the persisted head revision.
func decomposeRootPacket(t *testing.T, ctx context.Context, db *Store, now time.Time, intentID, revisionID string, owner int64, source string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	return decomposeRootPacketWithDigest(t, ctx, db, now, intentID, revisionID, owner, source, "")
}

// decomposeRootPacketWithDigest builds the same packet with an explicit
// subject digest. An empty digest reads the persisted revision row; a
// test proving refusal for an unrecorded revision supplies the digest
// of its unsaved literal instead, since no row exists to read.
func decomposeRootPacketWithDigest(t *testing.T, ctx context.Context, db *Store, now time.Time, intentID, revisionID string, owner int64, source string, digest string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	selection, ok := grantTestPolicy().Roles[project.RoleCoder]
	if !ok {
		t.Fatal("coder fixture role has no policy selection")
	}
	if digest == "" {
		digest = decomposeRevisionDigest(t, db, revisionID)
	}
	prompt := []byte("intent root packet " + revisionID)
	sum := sha256.Sum256(prompt)
	rootID, runID := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		Authority: reviewCorrectAuthority(t, ctx, db, dispatchTestProjectID(), 7, "conn"),
		ID:        rootID, AttemptRoot: rootID, PublicationAssignment: "", ParentAssignment: "",
		Subject: factory.ExecutionSubject{
			Kind:   factory.SubjectIntent,
			Intent: &factory.IntentSubject{IntentID: intentID, RevisionID: revisionID, ScopeDigest: digest, OwnerID: owner},
		},
		Phase:     project.ExecutionDecompose,
		ProjectID: dispatchTestProjectID(), ActorID: 7, Role: project.RoleCoder,
		Repository: 7, Issue: 0, NativeRev: 5, Acceptance: "", Preparation: "f" + strings.Repeat("e", 24),
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: "conn", SourceCommit: source,
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(),
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("intent root invalid: %v", err)
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

func decomposeTestControl(a factory.Assignment) factory.IssueControl {
	return factory.IssueControl{
		Repository: a.Repository, Issue: a.Issue, Acceptance: a.Acceptance, NativeRev: a.NativeRev,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
}

func readIntentAllowance(t *testing.T, db *Store, root string) factory.AttemptAllowance {
	t.Helper()
	var data []byte
	if err := db.db.QueryRowContext(context.Background(), `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, root).Scan(&data); err != nil {
		t.Fatalf("intent allowance not funded: %v", err)
	}
	var allowance factory.AttemptAllowance
	if err := json.Unmarshal(data, &allowance); err != nil {
		t.Fatal(err)
	}
	return allowance
}

func TestDecomposeRootAdmitsThroughDecomposeGuard(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	intentID, revisionID := "d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	a, r, run, view := decomposeRootPacket(t, ctx, db, now, intentID, revisionID, 7, strings.Repeat("c", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("intent root refused: %v", err)
	}
	allowance := readIntentAllowance(t, db, a.ID)
	if allowance.Intent == nil || allowance.Intent.IntentID != intentID || allowance.Intent.OwnerID != 7 {
		t.Fatalf("intent allowance mis-scoped: %+v", allowance.Intent)
	}
	if allowance.Limits.ActiveMinutes != 30 || !allowance.Active || allowance.Closed {
		t.Fatalf("intent allowance mis-funded: %+v", allowance.Limits)
	}
}

func TestDecomposeRootRefusesUnrecordedRevision(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	unsaved := factory.IntentRevision{
		ID: "d" + strings.Repeat("a", 24), Intent: "d" + strings.Repeat("b", 24), Owner: 7, Repository: 7,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: now.Unix(),
	}
	a, r, run, view := decomposeRootPacketWithDigest(t, ctx, db, now, "d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24), 7, strings.Repeat("c", 40), unsaved.Digest())
	err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view)
	if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("unrecorded revision error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
}

func TestDecomposeRootRefusesForeignBindings(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	intentID, revisionID := "d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	advanced := factory.IntentRevision{
		ID: "d" + strings.Repeat("c", 24), Intent: intentID, Owner: 7, Repository: 7,
		Predecessor: revisionID, Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: now.Unix(),
	}
	if err := db.SaveIntentRevision(ctx, advanced); err != nil {
		t.Fatalf("advanced revision not recorded: %v", err)
	}
	cases := map[string]func(*factory.Assignment){
		"parent":      func(a *factory.Assignment) { a.ParentAssignment = factory.NewID() },
		"publication": func(a *factory.Assignment) { a.PublicationAssignment = factory.NewID() },
		"issue":       func(a *factory.Assignment) { a.Issue = 3 },
		"role":        func(a *factory.Assignment) { a.Role = project.RoleReviewer },
		"superseded":  func(a *factory.Assignment) { a.Subject.Intent.RevisionID = revisionID },
		"owner":       func(a *factory.Assignment) { a.Subject.Intent.OwnerID = 9 },
	}
	for name, mutate := range cases {
		a, r, run, view := decomposeRootPacket(t, ctx, db, now, intentID, advanced.ID, 7, strings.Repeat("c", 40))
		mutate(&a)
		if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err == nil {
			t.Fatalf("%s binding admitted", name)
		}
		assertDispatchPacketAbsent(t, db, a)
	}
}

func TestDecomposeRootReplayWritesNoRows(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	intentID, revisionID := "d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	a, r, run, view := decomposeRootPacket(t, ctx, db, now, intentID, revisionID, 7, strings.Repeat("c", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first intent packet refused: %v", err)
	}
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err == nil {
		t.Fatal("replayed intent packet admitted")
	}
	if count := countReassessAssignmentRows(t, db, a.ID); count != 1 {
		t.Fatalf("replay left %d assignment rows, want exactly one", count)
	}
}

// TestIntentDistinctRootCannotRefillSameRevision (real pg) proves the R11
// one-funding rule across roots: a second distinct root for the same
// intent/revision/owner triple refuses with the concurrent-admission
// error while the first root is still active, funds nothing, and leaves
// the first root allowance intact. Identity binds the replay, not
// incidental repository capacity.
func TestIntentDistinctRootCannotRefillSameRevision(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	intentID, revisionID := "d"+strings.Repeat("e", 24), "d"+strings.Repeat("f", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	first := time.Now().UTC().Truncate(time.Second)
	a, r, run, view := decomposeRootPacket(t, ctx, db, first, intentID, revisionID, 7, strings.Repeat("1", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first intent root refused: %v", err)
	}
	second := time.Now().UTC().Truncate(time.Second)
	a2, r2, run2, view2 := decomposeRootPacket(t, ctx, db, second, intentID, revisionID, 7, strings.Repeat("1", 40))
	err := db.RecordDispatchPacket(ctx, decomposeTestControl(a2), a2, r2, run2, view2)
	if !errors.Is(err, ErrAssignmentActive) {
		t.Fatalf("second distinct root did not report the concurrent admission: %v", err)
	}
	assertDispatchPacketAbsent(t, db, a2)
	prior := readIntentAllowance(t, db, a.ID)
	if !prior.Active || prior.Limits.ActiveMinutes != 30 {
		t.Fatalf("first intent allowance disturbed: %+v", prior)
	}
}

// TestIntentFailedRootCannotRefillSameRevision (real pg) proves failed
// work stays charged: after the first root reconciles failed, records
// usage, consumes its reservation and finishes failed, the same
// intent/revision/owner triple as a distinct root refuses with the
// settled-attempt error instead of funding a fresh allowance. Recorded
// consumption stands; no refund or reset path runs.
func TestIntentFailedRootCannotRefillSameRevision(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	intentID, revisionID := "d"+strings.Repeat("e", 24), "d"+strings.Repeat("f", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	first := time.Now().UTC().Truncate(time.Second)
	a, r, run, view := decomposeRootPacket(t, ctx, db, first, intentID, revisionID, 7, strings.Repeat("1", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first intent root refused: %v", err)
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
	if err := db.FinishAssignment(ctx, finishIntentRoot(t, a, ended, factory.Failed, factory.AssignReasonRunFailed)); err != nil {
		t.Fatalf("failed root not finished: %v", err)
	}
	prior := readIntentAllowance(t, db, a.ID)
	if prior.Active || !prior.Closed {
		t.Fatalf("failed intent allowance still open: %+v", prior)
	}
	second := time.Now().UTC().Truncate(time.Second)
	a2, r2, run2, view2 := decomposeRootPacket(t, ctx, db, second, intentID, revisionID, 7, strings.Repeat("1", 40))
	err := db.RecordDispatchPacket(ctx, decomposeTestControl(a2), a2, r2, run2, view2)
	if !errors.Is(err, factory.ErrAttemptClosed) {
		t.Fatalf("failed intent replay did not report the settled attempt: %v", err)
	}
	assertDispatchPacketAbsent(t, db, a2)
	settled := readIntentAllowance(t, db, a.ID)
	if settled.Active || !settled.Closed || settled.Limits.ActiveMinutes != 30 {
		t.Fatalf("settled intent allowance disturbed: %+v", settled)
	}
}

// TestIntentSucceededRootCannotRefillSameRevision (real pg) proves a
// success-settled attempt stays charged: after the first root finishes
// succeeded with recorded usage and a consumed reservation — the
// walkthrough-less completed plan case — the same intent/revision/owner
// triple as a distinct root refuses with the settled-attempt error
// instead of funding a fresh allowance, and the successor revision still
// admits its own funded attempt while the prior stays charged.
func TestIntentSucceededRootCannotRefillSameRevision(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	intentID, revisionID := "d"+strings.Repeat("e", 24), "d"+strings.Repeat("f", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	first := time.Now().UTC().Truncate(time.Second)
	a, r, run, view := decomposeRootPacket(t, ctx, db, first, intentID, revisionID, 7, strings.Repeat("1", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first intent root refused: %v", err)
	}
	ended := time.Now().UTC().Truncate(time.Second)
	finishedRun := run
	finishedRun.Outcome, finishedRun.Reconciled, finishedRun.Summary = factory.Succeeded, true, "planned"
	if err := db.SaveFactoryRun(ctx, finishedRun); err != nil {
		t.Fatalf("succeeded run not reconciled: %v", err)
	}
	if err := db.RecordRunUsage(ctx, factory.Usage{RunID: a.Run, Minutes: 1, Repository: 7, Connection: "conn", StartedAt: ended.Add(-time.Minute), EndedAt: ended}); err != nil {
		t.Fatalf("succeeded run usage not recorded: %v", err)
	}
	if err := db.ConsumeReservation(ctx, r.AssignmentID); err != nil {
		t.Fatalf("succeeded root reservation not consumed: %v", err)
	}
	if err := db.FinishAssignment(ctx, finishIntentRoot(t, a, ended, factory.Succeeded, factory.AssignReasonReported)); err != nil {
		t.Fatalf("succeeded root not finished: %v", err)
	}
	prior := readIntentAllowance(t, db, a.ID)
	if prior.Active || !prior.Closed {
		t.Fatalf("succeeded intent allowance still open: %+v", prior)
	}
	second := time.Now().UTC().Truncate(time.Second)
	a2, r2, run2, view2 := decomposeRootPacket(t, ctx, db, second, intentID, revisionID, 7, strings.Repeat("1", 40))
	err := db.RecordDispatchPacket(ctx, decomposeTestControl(a2), a2, r2, run2, view2)
	if !errors.Is(err, factory.ErrAttemptClosed) {
		t.Fatalf("succeeded intent replay did not report the settled attempt: %v", err)
	}
	assertDispatchPacketAbsent(t, db, a2)
	var funded int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_attempt_allowances WHERE root_assignment=$1`, a2.ID).Scan(&funded); err != nil {
		t.Fatal(err)
	}
	if funded != 0 {
		t.Fatalf("settled intent revision funded a second root: %d allowances", funded)
	}
	settled := readIntentAllowance(t, db, a.ID)
	if settled.Active || !settled.Closed || settled.Limits.ActiveMinutes != 30 {
		t.Fatalf("settled intent allowance disturbed: %+v", settled)
	}
	nextRevision := "d" + strings.Repeat("c", 24)
	next := factory.IntentRevision{
		ID: nextRevision, Intent: intentID, Owner: 7, Repository: 7, Predecessor: revisionID,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: time.Now().UTC().Truncate(time.Second).Unix(),
	}
	if err := db.SaveIntentRevision(ctx, next); err != nil {
		t.Fatalf("next intent revision not recorded: %v", err)
	}
	third := time.Now().UTC().Truncate(time.Second)
	a3, r3, run3, view3 := decomposeRootPacket(t, ctx, db, third, intentID, nextRevision, 7, strings.Repeat("1", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a3), a3, r3, run3, view3); err != nil {
		t.Fatalf("successor revision root refused: %v", err)
	}
	fundedNext := readIntentAllowance(t, db, a3.ID)
	if !fundedNext.Active || fundedNext.Limits.ActiveMinutes != 30 {
		t.Fatalf("successor revision allowance not funded: %+v", fundedNext)
	}
	kept := readIntentAllowance(t, db, a.ID)
	if kept.Active || !kept.Closed || kept.Limits.ActiveMinutes != 30 {
		t.Fatalf("prior success-settled allowance disturbed: %+v", kept)
	}
}

// TestIntentNewRevisionFundsNextAttempt (real pg) proves the legitimate
// next attempt still admits: after the first root settles failed, the
// intent owner publishing a new revision advances the head, and a fresh
// root bound to the new revision funds its own allowance while the
// settled prior revision stays charged.
func TestIntentNewRevisionFundsNextAttempt(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	intentID, revisionID := "d"+strings.Repeat("e", 24), "d"+strings.Repeat("f", 24)
	decomposeIntentRevisionFixture(t, db, intentID, revisionID, 7)
	first := time.Now().UTC().Truncate(time.Second)
	a, r, run, view := decomposeRootPacket(t, ctx, db, first, intentID, revisionID, 7, strings.Repeat("1", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatalf("first intent root refused: %v", err)
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
	if err := db.FinishAssignment(ctx, finishIntentRoot(t, a, ended, factory.Failed, factory.AssignReasonRunFailed)); err != nil {
		t.Fatalf("failed root not finished: %v", err)
	}
	nextRevision := "d" + strings.Repeat("c", 24)
	next := factory.IntentRevision{
		ID: nextRevision, Intent: intentID, Owner: 7, Repository: 7, Predecessor: revisionID,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: time.Now().UTC().Truncate(time.Second).Unix(),
	}
	if err := db.SaveIntentRevision(ctx, next); err != nil {
		t.Fatalf("next intent revision not recorded: %v", err)
	}
	second := time.Now().UTC().Truncate(time.Second)
	a2, r2, run2, view2 := decomposeRootPacket(t, ctx, db, second, intentID, nextRevision, 7, strings.Repeat("1", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a2), a2, r2, run2, view2); err != nil {
		t.Fatalf("next revision root refused: %v", err)
	}
	funded := readIntentAllowance(t, db, a2.ID)
	if !funded.Active || funded.Limits.ActiveMinutes != 30 {
		t.Fatalf("next revision allowance not funded: %+v", funded)
	}
	prior := readIntentAllowance(t, db, a.ID)
	if prior.Active || !prior.Closed || prior.Limits.ActiveMinutes != 30 {
		t.Fatalf("prior revision allowance disturbed: %+v", prior)
	}
}

// TestIntentDigestChangeCannotRefillSameRevision proves the funded
// operation is the recorded revision, not the caller-supplied snapshot:
// after failed settlement with consumption recorded, a second root for
// the same intent/revision/owner carrying only a different scope digest
// refuses instead of funding another root.
func TestIntentDigestChangeCannotRefillSameRevision(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	intent, revision := "d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24)
	decomposeIntentRevisionFixture(t, db, intent, revision, 7)
	a, r, run, view := decomposeRootPacket(t, ctx, db, now, intent, revision, 7, strings.Repeat("c", 40))
	if err := db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view); err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Reconciled, run.Summary = factory.Failed, true, "provider failed"
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	if err := db.RecordRunUsage(ctx, factory.Usage{RunID: run.ID, Repository: a.Repository, Connection: a.Connection, Minutes: 1, StartedAt: run.Started, EndedAt: ended}); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishAssignment(ctx, finishIntentRoot(t, a, ended, factory.Failed, factory.AssignReasonRunFailed)); err != nil {
		t.Fatal(err)
	}
	prior := readIntentAllowance(t, db, a.ID)
	if prior.Active || !prior.Closed {
		t.Fatalf("prior root not settled: %+v", prior)
	}
	a2, r2, run2, view2 := decomposeRootPacket(t, ctx, db, now, intent, revision, 7, strings.Repeat("c", 40))
	a2.Subject.Intent.ScopeDigest = strings.Repeat("e", 64)
	err := db.RecordDispatchPacket(ctx, decomposeTestControl(a2), a2, r2, run2, view2)
	if err == nil {
		first, second := readIntentAllowance(t, db, a.ID), readIntentAllowance(t, db, a2.ID)
		t.Fatalf("same intent revision admitted two distinct roots and funded both: first=%d second=%d active=%v/%v", first.Limits.ActiveMinutes, second.Limits.ActiveMinutes, first.Active, second.Active)
	}
	t.Logf("second distinct-root refusal: %v", err)
	assertDispatchPacketAbsent(t, db, a2)
}

// TestIntentHeadChangeBeforeAdmissionCommit proves the packet admits
// the authoritative head even when a new revision commits between the
// guard's unlocked read and the funding lock: the duplicate refusal
// revalidates the head under its lock, so the stale revision refuses
// atomically. The probe synchronizes on the packet's real post-head-
// validation capacity-lock wait instead of sleeping.
func TestIntentHeadChangeBeforeAdmissionCommit(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Second)
	intent, revision := "d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24)
	decomposeIntentRevisionFixture(t, db, intent, revision, 7)
	a, r, run, view := decomposeRootPacket(t, ctx, db, now, intent, revision, 7, strings.Repeat("c", 40))
	blocker, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	var capacity []byte
	if err := blocker.QueryRowContext(ctx, `SELECT data FROM factory_capacity WHERE id=1 FOR UPDATE`).Scan(&capacity); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- db.RecordDispatchPacket(ctx, decomposeTestControl(a), a, r, run, view) }()
	// Synchronize on the real packet waiting at its post-head-validation capacity lock.
	for {
		var waiting bool
		err := db.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'SELECT data FROM factory_capacity%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("packet ended before capacity lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	next := factory.IntentRevision{ID: "d" + strings.Repeat("f", 24), Intent: intent, Owner: 7, Repository: 7, Predecessor: revision, Objective: "changed draft", State: "draft", MaxIssues: 10, CreatedUnix: now.Unix()}
	publishCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	err = db.SaveIntentRevision(publishCtx, next)
	stop()
	if err != nil {
		t.Fatalf("new head publication failed: %v", err)
	}
	head, err := db.IntentRevision(ctx, intent, 7)
	if err != nil || head.ID != next.ID {
		t.Fatalf("new head not committed: %s %v", head.ID, err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	err = <-done
	if !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("stale revision error = %v, want root mismatch", err)
	}
	assertDispatchPacketAbsent(t, db, a)
}
