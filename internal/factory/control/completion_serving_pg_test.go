package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

func TestCompletionRetryAfterSettledReassessmentPG(t *testing.T) {
	ctx := context.Background()
	chooser := &native92ReassessChooser{}
	fx, deps := native92DrivePGFixture(t, chooser)
	db, coord := fx.publish.db, fx.publish.coord
	coord.Host, coord.Broker = fx.publish.seed.host, fx.publish.seed.broker
	cycleID := pendingCycleID(t, fx)
	coord.DriveCompletions(ctx, deps)
	coord.DriveCompletions(ctx, deps)
	record, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil || len(record.Cycle.Ledger.CoderRuns) != 1 {
		t.Fatalf("reassessment was not admitted: %+v %v", record.Cycle, err)
	}
	run, err := db.FactoryRun(ctx, record.Cycle.Ledger.CoderRuns[0])
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Failed, "native coder failed", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	assignment, ok := AccountSettledRun(ctx, db, run, time.Now())
	if !ok || assignment.Stage != factory.AssignmentFinished || assignment.Outcome != factory.Failed {
		t.Fatalf("failed coder not reconciled: stage=%s outcome=%s recorded=%v", assignment.Stage, assignment.Outcome, ok)
	}
	coord.DriveCompletions(ctx, deps)
	attended, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil || attended.Cycle.State != factory.ReassessmentAttention {
		t.Fatalf("failed coder did not hold for attention: %+v %v", attended.Cycle, err)
	}
	commandID := factory.NewID()
	request := factory.CompletionRetryRequest{EventID: record.Cycle.EventID, ExpectedPriorCycle: cycleID}
	decision, err := coord.RetryCompletion(ctx, commandID, "native:1", fx.publish.seed.repo, request)
	if err != nil {
		t.Fatalf("explicit retry refused a reconciled failed coder: %v", err)
	}
	successor := decision.SuccessorCycle
	if !decision.Queued || decision.CommandID != commandID || successor != commandID || decision.PriorCycle != cycleID || decision.EventID != request.EventID || decision.Repository != fx.publish.seed.repo {
		t.Fatalf("owner command did not bind its one successor: %+v", decision)
	}
	admitted, err := db.FactoryCommand(ctx, commandID)
	if err != nil || admitted.Finished == "" || admitted.Outcome != "" {
		t.Fatalf("completion retry retained a response or did not commit: %+v %v", admitted, err)
	}
	if replay, err := coord.RetryCompletion(ctx, commandID, "native:1", fx.publish.seed.repo, request); err != nil || replay != decision {
		t.Fatalf("owner retry lost-reply replay changed the successor: %+v %v", replay, err)
	}
	if _, err := coord.RetryCompletion(ctx, commandID, "native:2", fx.publish.seed.repo, request); !errors.Is(err, store.ErrCommandConflict) {
		t.Fatalf("foreign principal replayed the owner retry: %v", err)
	}
	changed := request
	changed.EventID = factory.NewID()
	if _, err := coord.RetryCompletion(ctx, commandID, "native:1", fx.publish.seed.repo, changed); !errors.Is(err, store.ErrCommandConflict) {
		t.Fatalf("changed event replay did not conflict: %v", err)
	}
	if _, err := coord.RetryCompletion(ctx, factory.NewID(), "native:1", fx.publish.seed.repo+1, request); !errors.Is(err, store.ErrCompletionRetryIneligible) {
		t.Fatalf("another repository admitted the completion retry: %v", err)
	}
	prior, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil || prior.Cycle.State != factory.ReassessmentSuperseded || len(prior.Cycle.Ledger.CoderRuns) != 1 || len(prior.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("retry lost the charged prior: %+v %v", prior.Cycle, err)
	}
	coord.DriveCompletions(ctx, deps)
	coord.DriveCompletions(ctx, deps)
	next, err := db.ReassessmentRecord(ctx, successor)
	if err != nil || next.Cycle.State != factory.ReassessmentReassessing || len(next.Cycle.Ledger.CoderRuns) != 1 || next.Cycle.Ledger.CoderRuns[0] == run.ID || chooser.calls != 2 {
		t.Fatalf("retry did not admit its one fresh run: %+v %v calls=%d", next.Cycle, err, chooser.calls)
	}
}

// This supplement proves the Store-backed completion drive on the
// disposable PG fixture (SODA_PG_*; skips without it): a merged
// completion seals real bodies through the production local capture,
// the asserting chooser judges that substance and continues, and the
// release re-observes the native set before releasing. The
// file-backed serving proof runs the same joints here; this variant
// adds charging, fencing, refusal, retry and custody through real
// SQL. It is the supplement, not the proof.

// continueChooser asserts the funded decision input carries sealed
// substance, then answers continue once per consultation.
type continueChooser struct {
	calls int
}

func (f *continueChooser) Select(_ context.Context, state any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	f.calls++
	decision, ok := state.(ReassessmentDecisionState)
	if !ok {
		return factory.PriorityAnswer{}, errReassessmentTestState
	}
	if decision.Evidence == nil || decision.Issues != len(decision.Evidence) || !decision.Local.Sealed() || decision.Completion.ID == "" {
		return factory.PriorityAnswer{}, errReassessmentTestBodiless
	}
	for _, body := range decision.Evidence {
		if err := body.Validate(); err != nil {
			return factory.PriorityAnswer{}, err
		}
	}
	share := 0.9
	answer := factory.PriorityAnswer{Selected: factory.ReassessmentOptionContinue, Model: "supplement", Confidence: 0.9, Probabilities: map[string]float64{}}
	for id := range choices {
		answer.Probabilities[id] = (1 - share) / float64(len(choices)-1)
	}
	answer.Probabilities[factory.ReassessmentOptionContinue] = share
	return answer, nil
}

var (
	errReassessmentTestState    = errorString("decision state outside its substance shape")
	errReassessmentTestBodiless = errorString("decision judges hashes without bodies")
)

type errorString string

func (e errorString) Error() string { return string(e) }

// drivePGFixture merges one completion on an isolated PG database and
// wires the drive with the production store local capture plus the
// scripted native pages.
func drivePGFixture(t *testing.T) (*mergeFixture, *continueChooser, CompletionDriveDeps) {
	t.Helper()
	fx, p := mergeSeed(t, 3)
	exec := happyMerger()
	fx.wire(exec)
	fx.pass(t)
	open := fx.merge(t, p)
	if open.Stage != factory.MergeOpen {
		t.Fatalf("merge stage: %+v, want the open row before authorization", open.Stage)
	}
	// Deterministic union fixture: journal the synthetic clear-review
	// acceptance through the canonical helper before authorizing.
	ctx := context.Background()
	authorizeMergeFixture(t, ctx, fx.publish.db, fx.publish.coord, p)
	if report := fx.pass(t); len(report.Merged) != 1 {
		t.Fatalf("merge report: %+v", report)
	}
	m := fx.merge(t, p)
	if m.Stage != factory.MergeMerged {
		t.Fatalf("merge stage: %+v", m)
	}
	db := fx.publish.db
	chooser := &continueChooser{}
	fx.publish.coord.Planning = &PlanningJudgments{Chooser: chooser}
	deps := CompletionDriveDeps{Enumerator: twoPageEnumerator(t), CaptureLocal: db.CaptureCompletionLocal}
	return fx, chooser, deps
}

func pendingCycleID(t *testing.T, fx *mergeFixture) string {
	t.Helper()
	cycles, err := fx.publish.db.PendingReassessmentCycles(context.Background(), fx.publish.seed.repo)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("cycles=%v err=%v, want the one collecting cycle", cycles, err)
	}
	return cycles[0].Cycle.CycleID
}

// A merged completion seals bodies, judges substance, continues and
// releases, opening the fence with its decision receipt retaining
// the exact bodies.
func TestCompletionDriveReleasesContinuePG(t *testing.T) {
	ctx := context.Background()
	fx, chooser, deps := drivePGFixture(t)
	db := fx.publish.db
	cycleID := pendingCycleID(t, fx)

	report := fx.publish.coord.DriveCompletions(ctx, deps)
	if len(report.Sealed) != 1 || report.Sealed[0] != cycleID {
		t.Fatalf("report=%+v, want the sealed collecting cycle", report)
	}
	sealed, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Cycle.State != factory.ReassessmentDeciding || len(sealed.Evidence) != 3 || sealed.EvidenceIssues != 3 || sealed.EvidenceRevision != 41 {
		t.Fatalf("record=%+v, want the deciding seal with three bodies", sealed)
	}
	if sealed.Evidence[1].Title != "title 2" || sealed.Completion.MergedCommit == "" || !sealed.Local.Sealed() {
		t.Fatalf("record=%+v, want the sealed substance and bindings", sealed)
	}

	report = fx.publish.coord.DriveCompletions(ctx, deps)
	if len(report.Released) != 1 || report.Released[0] != cycleID {
		t.Fatalf("report=%+v, want the fresh continue release", report)
	}
	if chooser.calls != 1 {
		t.Fatalf("calls=%d, want one charged consultation for decide plus a free release", chooser.calls)
	}
	released, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Cycle.State != factory.ReassessmentReleased || released.Cycle.Decision != factory.ReassessmentOptionContinue {
		t.Fatalf("record=%+v, want the released continue", released)
	}
	if pending, err := db.CompletionPending(ctx, fx.publish.seed.repo); err != nil || len(pending) != 0 {
		t.Fatalf("pending=%v err=%v, want the open fence after release", pending, err)
	}
	// The funded receipt retains the exact judged bodies: custody,
	// not just the fingerprint.
	state, err := DecisionStateFromRecord(released)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := db.PriorityJudgment(ctx, fingerprintOf(state))
	if err != nil || len(receipt.Calls) != 1 {
		t.Fatalf("receipt=%+v err=%v, want the charged continue receipt", receipt, err)
	}
	if !strings.Contains(string(receipt.Calls[0].Request.State), "title 2") {
		t.Fatalf("receipt state=%s, want the retained issue bodies", receipt.Calls[0].Request.State)
	}

	// Closing the repository's last issue seals a complete empty set. Its
	// persisted record must survive all readers and charge just one decision.
	emptyFX, emptyChooser, emptyDeps := drivePGFixture(t)
	emptyDB, emptyCoord := emptyFX.publish.db, emptyFX.publish.coord
	emptyDeps.Enumerator = &fixtureEnumerator{
		revision: 41, idle: true,
		pages: map[string]NativeInventoryPage{"": {Issues: []NativeInventoryIssue{}, Complete: true}},
	}
	emptyID := pendingCycleID(t, emptyFX)
	if got := emptyCoord.DriveCompletions(ctx, emptyDeps); len(got.Sealed) != 1 || got.Sealed[0] != emptyID {
		t.Fatalf("empty inventory seal: %+v", got)
	}
	empty, err := emptyDB.ReassessmentRecord(ctx, emptyID)
	if err != nil || empty.Evidence == nil || len(empty.Evidence) != 0 || empty.EvidenceIssues != 0 {
		t.Fatalf("empty seal did not survive storage: %+v %v", empty, err)
	}
	raw, err := json.Marshal(empty)
	if err != nil || !strings.Contains(string(raw), `"evidence":[]`) {
		t.Fatalf("empty seal omitted its complete evidence list: %s %v", raw, err)
	}
	// A retained omitted/null list is the same complete empty inventory only
	// under its exact recorded empty digest; losing a positive set still fails.
	missing := sealed
	missing.Evidence, missing.EvidenceIssues = nil, 0
	if missing.Validate() == nil {
		t.Fatal("missing positive inventory accepted as empty under its old digest")
	}
	if _, err := DecisionStateFromRecord(missing); err == nil {
		t.Fatal("missing positive inventory admitted a funded decision")
	}
	empty.Evidence = nil
	if err := emptyDB.CasReassessmentRecord(ctx, emptyID, factory.ReassessmentDeciding, empty); err != nil {
		t.Fatalf("retained empty representation refused: %v", err)
	}
	journey, truncated, err := emptyDB.JourneyReassignmentCycles(ctx, emptyFX.publish.seed.repo, empty.Issue, 10)
	if err != nil || truncated || len(journey) != 1 || journey[0].Cycle.CycleID != emptyID {
		t.Fatalf("empty inventory broke issue journey: %+v %v %v", journey, truncated, err)
	}
	if got := emptyCoord.DriveCompletions(ctx, emptyDeps); len(got.Released) != 1 || got.Released[0] != emptyID {
		t.Fatalf("empty inventory did not release: %+v", got)
	}
	emptyReleased, err := emptyDB.ReassessmentRecord(ctx, emptyID)
	if err != nil || emptyReleased.Cycle.State != factory.ReassessmentReleased {
		t.Fatalf("empty release not retained: %+v %v", emptyReleased, err)
	}
	emptyState, err := DecisionStateFromRecord(emptyReleased)
	if err != nil || emptyState.Evidence == nil || len(emptyState.Evidence) != 0 {
		t.Fatalf("empty decision lost its complete typed list: %+v %v", emptyState, err)
	}
	emptyReceipt, err := emptyDB.PriorityJudgment(ctx, fingerprintOf(emptyState))
	if err != nil || len(emptyReceipt.Calls) != 1 {
		t.Fatalf("empty decision receipt lost custody: %+v %v", emptyReceipt, err)
	}
	var recorded struct {
		Evidence []json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(emptyReceipt.Calls[0].Request.State, &recorded); err != nil || recorded.Evidence == nil || len(recorded.Evidence) != 0 {
		t.Fatalf("empty decision receipt did not retain the complete empty list: %v", err)
	}
	emptyCoord.DriveCompletions(ctx, emptyDeps)
	if emptyChooser.calls != 1 {
		t.Fatalf("empty inventory repeated its funded call: %d", emptyChooser.calls)
	}
}

// An insertion after the seal refuses the stale release with
// attention; explicit Retry reseals the successor over the new set
// and releases it.
func TestCompletionDriveRefusesStaleReleasePG(t *testing.T) {
	ctx := context.Background()
	fx, chooser, deps := drivePGFixture(t)
	db := fx.publish.db
	coord := fx.publish.coord
	cycleID := pendingCycleID(t, fx)

	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 {
		t.Fatalf("report=%+v, want the seal over three issues", report)
	}
	enum, ok := deps.Enumerator.(*fixtureEnumerator)
	if !ok {
		t.Fatal("scripted pages lost their enumerator")
	}
	sealed, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	eventID := sealed.Cycle.EventID
	enum.pages["2"] = NativeInventoryPage{Issues: []NativeInventoryIssue{inventoryIssue(t, 3), inventoryIssue(t, 4)}, Complete: true}

	report := coord.DriveCompletions(ctx, deps)
	want := cycleID + ":completion evidence changed during reassessment"
	found := false
	for _, held := range report.Held {
		found = found || held == want
	}
	if !found {
		t.Fatalf("report=%+v, want %q", report, want)
	}
	attended, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if attended.Cycle.State != factory.ReassessmentAttention || attended.Cycle.Decision != factory.ReassessmentOptionContinue {
		t.Fatalf("record=%+v, want the recorded continue held with attention", attended)
	}

	successor, err := coord.StartCompletionRetry(ctx, factory.ReassessmentRetry{
		SuccessorCycleID: factory.NewID(), ExpectedPriorCycle: cycleID,
		CommandID: factory.NewID(), EventID: eventID,
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if again, err := coord.StartCompletionRetry(ctx, factory.ReassessmentRetry{
		SuccessorCycleID: successor, ExpectedPriorCycle: cycleID,
		CommandID: factory.NewID(), EventID: eventID,
	}); err != nil || again != successor {
		t.Fatalf("replay=%q err=%v, want the recorded successor without another cycle", again, err)
	}
	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 || report.Sealed[0] != successor {
		t.Fatalf("report=%+v, want the successor seal over four issues", report)
	}
	resealed, err := db.ReassessmentRecord(ctx, successor)
	if err != nil {
		t.Fatal(err)
	}
	if len(resealed.Evidence) != 4 {
		t.Fatalf("evidence=%+v, want the four resealed bodies", resealed.Evidence)
	}
	report = coord.DriveCompletions(ctx, deps)
	if len(report.Released) != 1 || report.Released[0] != successor {
		t.Fatalf("report=%+v, want the successor release", report)
	}
	if chooser.calls != 2 {
		t.Fatalf("calls=%d, want one charged consultation per cycle", chooser.calls)
	}
}

// A deferred release retries without charging again: the recorded
// continue decides once, and later drives only re-observe.
func TestCompletionDriveChargedReplayPG(t *testing.T) {
	ctx := context.Background()
	fx, chooser, deps := drivePGFixture(t)
	coord := fx.publish.coord
	cycleID := pendingCycleID(t, fx)

	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 {
		t.Fatalf("report=%+v, want the seal", report)
	}
	enum, ok := deps.Enumerator.(*fixtureEnumerator)
	if !ok {
		t.Fatal("scripted pages lost their enumerator")
	}
	enum.idle = false
	report := coord.DriveCompletions(ctx, deps)
	found := false
	for _, held := range report.Held {
		found = found || held == cycleID+":release_deferred"
	}
	if !found {
		t.Fatalf("report=%+v, want the deferred release with the decision standing", report)
	}
	if chooser.calls != 1 {
		t.Fatalf("calls=%d, want the single charged decision", chooser.calls)
	}
	enum.idle = true
	report = coord.DriveCompletions(ctx, deps)
	if len(report.Released) != 1 || chooser.calls != 1 {
		t.Fatalf("report=%+v calls=%d, want the free release without recharge", report, chooser.calls)
	}
}

// NATIVE92 (SUPERVISOR-20261017-105) paid-ledger durable proofs. The
// adapters and helpers below are defined inside this reserved file:
// small deterministic synthetic review/nativeReader judgments with no
// provider calls. They never touch the held untracked branch file.

// native92ReassessChooser judges the sealed substance exactly like the
// supplement continueChooser, then answers reassess once per
// consultation at valid confidence.
type native92ReassessChooser struct {
	calls int
}

func (f *native92ReassessChooser) Select(_ context.Context, state any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	f.calls++
	decision, ok := state.(ReassessmentDecisionState)
	if !ok {
		return factory.PriorityAnswer{}, errReassessmentTestState
	}
	if len(decision.Evidence) == 0 || !decision.Local.Sealed() || decision.Completion.ID == "" {
		return factory.PriorityAnswer{}, errReassessmentTestBodiless
	}
	for _, body := range decision.Evidence {
		if err := body.Validate(); err != nil {
			return factory.PriorityAnswer{}, err
		}
	}
	share := 0.9
	answer := factory.PriorityAnswer{Selected: factory.ReassessmentOptionReassess, Model: "native92-synthetic", Confidence: 0.9, Probabilities: map[string]float64{}}
	for id := range choices {
		answer.Probabilities[id] = (1 - share) / float64(len(choices)-1)
	}
	answer.Probabilities[factory.ReassessmentOptionReassess] = share
	return answer, nil
}

// native92LowConfidenceChooser judges the sealed substance, then
// answers a valid-shape but non-actionable low-confidence continue.
type native92LowConfidenceChooser struct {
	calls int
}

func (f *native92LowConfidenceChooser) Select(_ context.Context, state any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	f.calls++
	decision, ok := state.(ReassessmentDecisionState)
	if !ok {
		return factory.PriorityAnswer{}, errReassessmentTestState
	}
	if len(decision.Evidence) == 0 || !decision.Local.Sealed() || decision.Completion.ID == "" {
		return factory.PriorityAnswer{}, errReassessmentTestBodiless
	}
	for _, body := range decision.Evidence {
		if err := body.Validate(); err != nil {
			return factory.PriorityAnswer{}, err
		}
	}
	share := 0.9
	answer := factory.PriorityAnswer{Selected: factory.ReassessmentOptionContinue, Model: "native92-synthetic", Confidence: 0.49, Probabilities: map[string]float64{}}
	for id := range choices {
		answer.Probabilities[id] = (1 - share) / float64(len(choices)-1)
	}
	answer.Probabilities[factory.ReassessmentOptionContinue] = share
	return answer, nil
}

// native92FailingChooser judges the sealed substance, then fails the
// actual consultation: a real charged-failed receipt must survive.
type native92FailingChooser struct {
	calls int
}

func (f *native92FailingChooser) Select(_ context.Context, state any, _ string, _ map[string]any) (factory.PriorityAnswer, error) {
	f.calls++
	decision, ok := state.(ReassessmentDecisionState)
	if !ok {
		return factory.PriorityAnswer{}, errReassessmentTestState
	}
	if len(decision.Evidence) == 0 || !decision.Local.Sealed() || decision.Completion.ID == "" {
		return factory.PriorityAnswer{}, errReassessmentTestBodiless
	}
	return factory.PriorityAnswer{}, errorString("native92 synthetic chooser failure")
}

// native92DrivePGFixture mirrors drivePGFixture with the lane's own
// deterministic chooser wired through the canonical merge authorization
// path.
func native92DrivePGFixture(t *testing.T, chooser ProductJudgment) (*mergeFixture, CompletionDriveDeps) {
	t.Helper()
	fx, p := mergeSeed(t, 3)
	exec := happyMerger()
	fx.wire(exec)
	fx.pass(t)
	open := fx.merge(t, p)
	if open.Stage != factory.MergeOpen {
		t.Fatalf("merge stage: %+v, want the open row before authorization", open.Stage)
	}
	ctx := context.Background()
	authorizeMergeFixture(t, ctx, fx.publish.db, fx.publish.coord, p)
	if report := fx.pass(t); len(report.Merged) != 1 {
		t.Fatalf("merge report: %+v", report)
	}
	m := fx.merge(t, p)
	if m.Stage != factory.MergeMerged {
		t.Fatalf("merge stage: %+v", m)
	}
	db := fx.publish.db
	fx.publish.coord.Planning = &PlanningJudgments{Chooser: chooser}
	deps := CompletionDriveDeps{Enumerator: twoPageEnumerator(t), CaptureLocal: db.CaptureCompletionLocal}
	return fx, deps
}

func native92ReportHas(report []string, want string) bool {
	for _, got := range report {
		if got == want {
			return true
		}
	}
	return false
}

// A paid reassess decision rests recorded for the coder admission with
// its paid call intact: the root-090 receipt-vs-ledger comparison (one
// charged PriorityJudgment call against exactly one ledger call)
// holds, and replays rest without paying again. The global budget
// carries no exported reader, so the no-recharge replay plus the exact
// receipt counts below are the spend evidence.
func TestCompletionDrivePaidReassessKeepsLedgerPG(t *testing.T) {
	ctx := context.Background()
	chooser := &native92ReassessChooser{}
	fx, deps := native92DrivePGFixture(t, chooser)
	db := fx.publish.db
	coord := fx.publish.coord
	cycleID := pendingCycleID(t, fx)

	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 || report.Sealed[0] != cycleID {
		t.Fatalf("report=%+v, want the sealed collecting cycle", report)
	}
	before, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("BEFORE decision: state=%s decision=%q ledgerCalls=%d", before.Cycle.State, before.Cycle.Decision, len(before.Cycle.Ledger.DecisionCalls))

	report := coord.DriveCompletions(ctx, deps)
	if !native92ReportHas(report.Decided, cycleID) || !native92ReportHas(report.AwaitingCoder, cycleID) {
		t.Fatalf("report=%+v, want the decided cycle resting for the coder admission", report)
	}
	if chooser.calls != 1 {
		t.Fatalf("calls=%d, want the single paid consultation", chooser.calls)
	}
	after, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Cycle.Decision != factory.ReassessmentOptionReassess {
		t.Fatalf("record=%+v, want the recorded reassess decision", after)
	}
	if len(after.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("PAID LEDGER LOST: ledgerCalls=%d ledger=%v, want exactly the one paid call", len(after.Cycle.Ledger.DecisionCalls), after.Cycle.Ledger.DecisionCalls)
	}
	state, err := DecisionStateFromRecord(after)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := db.PriorityJudgment(ctx, fingerprintOf(state))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Calls) != 1 {
		t.Fatalf("receipt=%+v, want the one charged reassess receipt", receipt)
	}

	again := coord.DriveCompletions(ctx, deps)
	if !native92ReportHas(again.AwaitingCoder, cycleID) || len(again.Decided) != 0 {
		t.Fatalf("report=%+v, want the recorded reassess to rest without deciding again", again)
	}
	if chooser.calls != 1 {
		t.Fatalf("calls=%d, want no second consultation on replay", chooser.calls)
	}
	replayed, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("ledger=%v, want the one paid call after replay", replayed.Cycle.Ledger.DecisionCalls)
	}
	restate, err := DecisionStateFromRecord(replayed)
	if err != nil {
		t.Fatal(err)
	}
	rereceipt, err := db.PriorityJudgment(ctx, fingerprintOf(restate))
	if err != nil {
		t.Fatal(err)
	}
	if len(rereceipt.Calls) != 1 {
		t.Fatalf("receipt=%+v, want still the one charged receipt after replay", rereceipt)
	}
}

// A paid but non-actionable low-confidence continue holds with
// attention while its paid ledger and successful receipt survive; the
// replay waits for explicit retry without charging again.
func TestCompletionDrivePaidLowConfidenceHoldsAttentionPG(t *testing.T) {
	ctx := context.Background()
	chooser := &native92LowConfidenceChooser{}
	fx, deps := native92DrivePGFixture(t, chooser)
	db := fx.publish.db
	coord := fx.publish.coord
	cycleID := pendingCycleID(t, fx)

	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 {
		t.Fatalf("report=%+v, want the seal", report)
	}
	report := coord.DriveCompletions(ctx, deps)
	if !native92ReportHas(report.Held, cycleID+":decision_not_actionable") {
		t.Fatalf("report=%+v, want the non-actionable hold", report)
	}
	if chooser.calls != 1 {
		t.Fatalf("calls=%d, want the single paid consultation", chooser.calls)
	}
	attended, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if attended.Cycle.State != factory.ReassessmentAttention || attended.Cycle.Decision != "" {
		t.Fatalf("record=%+v, want attention with no inferred decision", attended)
	}
	if len(attended.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("PAID LEDGER LOST: ledgerCalls=%d ledger=%v, want exactly the one paid call", len(attended.Cycle.Ledger.DecisionCalls), attended.Cycle.Ledger.DecisionCalls)
	}
	state, err := DecisionStateFromRecord(attended)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := db.PriorityJudgment(ctx, fingerprintOf(state))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Calls) != 1 || receipt.Calls[0].Answer.Selected != factory.ReassessmentOptionContinue {
		t.Fatalf("receipt=%+v, want the one recorded low-confidence continue answer", receipt)
	}

	again := coord.DriveCompletions(ctx, deps)
	if !native92ReportHas(again.Held, cycleID+":awaiting-retry") {
		t.Fatalf("report=%+v, want the attention cycle to wait for explicit retry", again)
	}
	if chooser.calls != 1 {
		t.Fatalf("calls=%d, want no second consultation on replay", chooser.calls)
	}
	restill, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restill.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("ledger=%v, want the one paid call after replay", restill.Cycle.Ledger.DecisionCalls)
	}
}

// An actual chooser error records nothing and defers: the next drive asks
// again under the same ledger entry instead of holding for explicit retry.
func TestCompletionDrivePaidChooserErrorRetriesPG(t *testing.T) {
	ctx := context.Background()
	chooser := &native92FailingChooser{}
	fx, deps := native92DrivePGFixture(t, chooser)
	db := fx.publish.db
	coord := fx.publish.coord
	cycleID := pendingCycleID(t, fx)

	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 {
		t.Fatalf("report=%+v, want the seal", report)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		report := coord.DriveCompletions(ctx, deps)
		if !native92ReportHas(report.Held, cycleID+":decision_in_flight") {
			t.Fatalf("attempt %d report=%+v, want the deferred hold", attempt, report)
		}
		if chooser.calls != attempt {
			t.Fatalf("calls=%d, want one consultation per drive", chooser.calls)
		}
	}
	deferred, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if deferred.Cycle.State != factory.ReassessmentDeciding {
		t.Fatalf("record=%+v, want the cycle still deciding", deferred)
	}
	if len(deferred.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("ledger=%v, want the retry to reuse its one ledger entry", deferred.Cycle.Ledger.DecisionCalls)
	}
	state, err := DecisionStateFromRecord(deferred)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.PriorityJudgment(ctx, fingerprintOf(state)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed call recorded a receipt: %v", err)
	}
}
