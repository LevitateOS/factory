package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "forgejo.org/extension-sdk"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// Serving admission/launch for Issue-zero time-only roots (review-018
// finding 2) against real PostgreSQL: DispatchTimeOnlyRoot plans,
// records, funds and launches intent decompose and completion
// reassess roots through the production route — never by calling the
// packet directly. A bounded host double proves persistence and
// funding precede the host call (the double reads the committed rows
// from inside the launch), the host call runs outside the transaction,
// and replay cannot create a second run or charge.

// timeOnlyOrderHost installs a launch double that proves ordering: it
// reads the committed assignment and funded allowance rows from inside
// the host call, which only the post-commit launch can observe.
func timeOnlyOrderHost(fx dispatchFixture, observed *bool, check func(ctx context.Context, in project.FactoryLaunch)) {
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		check(context.Background(), in)
		*observed = true
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
	}
}

func timeOnlyIntentSubject(digest string) factory.ExecutionSubject {
	return factory.ExecutionSubject{
		Kind:   factory.SubjectIntent,
		Intent: &factory.IntentSubject{IntentID: "d777777777777777777777777", RevisionID: "d888888888888888888888888", ScopeDigest: digest, OwnerID: 7},
	}
}

func recordTimeOnlyIntent(t *testing.T, ctx context.Context, db *store.Store) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	revision := factory.IntentRevision{
		ID: "d888888888888888888888888", Intent: "d777777777777777777777777", Owner: 7, Repository: 7,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: now.Unix(),
	}
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatalf("intent revision not recorded: %v", err)
	}
	return revision.Digest()
}

func fundTimeOnlyPolicy(t *testing.T, ctx context.Context, db *store.Store, repo int64) {
	t.Helper()
	policy, err := db.RepositoryPolicy(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.IntentMinutes = 45
	policy.ReassessmentMinutes = 30
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchTimeOnlyRootLaunchesIntent(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	digest := recordTimeOnlyIntent(t, ctx, db)
	deps := fx.deps()
	occupancy, err := snapshotOccupancy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var observed bool
	timeOnlyOrderHost(fx, &observed, func(ctx context.Context, in project.FactoryLaunch) {
		a, err := db.AssignmentByRun(ctx, in.Run.ID)
		if err != nil || a.Phase != project.ExecutionDecompose || a.Issue != 0 || a.AttemptRoot != a.ID || a.PublicationAssignment != "" {
			t.Fatalf("host call precedes its packet: assignment=%+v err=%v", a, err)
		}
		allowance, err := db.IntentAttemptAllowance(ctx, fx.repo, factory.IntentAttemptScope{IntentID: "d777777777777777777777777", OwnerID: 7}, a.ID)
		if err != nil || allowance.Limits.ActiveMinutes != 45 || !allowance.Active {
			t.Fatalf("host call precedes its funding: allowance=%+v err=%v", allowance, err)
		}
	})
	report := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, deps, &occupancy, fx.repo, timeOnlyIntentSubject(digest), strings.Repeat("c", 40), []byte("plan the recorded draft"), "", &report)
	if len(report.Errors) != 0 || len(report.Waits) != 0 || len(report.Launched) != 1 {
		t.Fatalf("route report: %+v", report)
	}
	// The report phase echoes the host-observed run phase; the
	// assignment phase was already bound inside the order proof.
	if launched := report.Launched[0]; launched.Issue != 0 || launched.AssignmentID == "" || launched.RunID == "" {
		t.Fatalf("launched = %+v", launched)
	}
	if !observed {
		t.Fatal("host double never ran")
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("host launches = %d", len(fx.host.launches))
	}
}

// planPromptFixtureHead is a valid corrected draft with attributed
// effective scope behind the FJ-v3 plan-dispatch diagnosis: accepted and
// edited choices carry explicit source and provenance so draft planning
// scope admits it exactly like the corrected production head.
func planPromptFixtureHead(repo int64, intent, revision string) factory.IntentRevision {
	idea := func(id, desc, consequence string) factory.ProductIdea {
		return factory.ProductIdea{ID: id, Description: desc, Consequence: consequence, Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
	}
	return factory.IntentRevision{
		ID: revision, Intent: intent, Owner: 7, Repository: repo,
		Objective:         "Build a leave approval workflow so managers can review time-off requests",
		State:             "draft",
		MaxIssues:         10,
		PlanningCalls:     12,
		CreatedUnix:       time.Now().UTC().Truncate(time.Second).Unix(),
		EffectiveOutcomes: []string{"d555555555555555555555555", "d666666666666666666666666"},
		Ideas: []factory.ProductIdea{
			idea("d555555555555555555555555", "Capture leave requests", "Requests can be recorded"),
			idea("d666666666666666666666666", "Managers review requests", "Decisions are visible"),
		},
	}
}

// planPromptFixtureContext serves the exact approved bytes per requested
// path so preparation-context digests verify, mirroring the native reader.
func planPromptFixtureContext(contents map[string]string) func(project.FactoryPreparationContextRequest, string) (project.FactoryPreparationContext, error) {
	return func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		files := make([]project.FactoryContextFile, 0, len(in.Paths))
		for _, path := range in.Paths {
			files = append(files, project.FactoryContextFile{Path: path, Content: []byte(contents[path])})
		}
		return project.FactoryPreparationContext{
			Project: in.Project, ID: in.ID, Role: role,
			SourceCommit: in.SourceCommit, ApprovedBase: in.ApprovedBase,
			DiffBase: in.DiffBase, Candidate: in.Candidate,
			Setup: []byte("synthetic approved setup instructions"),
			Check: []byte("synthetic required checks"),
			Files: files,
			Diff:  []byte("synthetic exact candidate diff"),
		}, nil
	}
}

func planPromptDigest(t *testing.T, content string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// oversizedPromptContents builds approved source bytes past the
// intent-decompose headroom: five 31 KiB files stay inside every
// per-file and per-read bound while their merged staged prompt exceeds
// the 128 KiB phase bound.
func oversizedPromptContents() map[string]string {
	big := strings.Repeat("0123456789abcdef", 1984)
	if len(big) != 31744 {
		panic("fixture file size drift")
	}
	return map[string]string{
		"README.md":    big + "a",
		"docs/a.md":    big + "b",
		"docs/b.md":    big + "c",
		"docs/c.md":    big + "d",
		"docs/main.go": big + "e",
	}
}

// headroomPromptContents builds approved source bytes inside the
// intent-decompose headroom but past the other-phase bound: two 31 KiB
// files stage near 85 KiB, the FJ-v3 production shape.
func headroomPromptContents() map[string]string {
	big := strings.Repeat("0123456789abcdef", 1984)
	return map[string]string{"README.md": big + "a", "docs/main.go": big + "b"}
}

// TestAssembleIntentPlanningInputRefusesOversizedPrompt proves the
// fail-fast layer: assembled input past the admitted decompose phase
// bound refuses at assembly with the typed prompt-bound sentinel,
// before any dispatch, funding or launch.
func TestAssembleIntentPlanningInputRefusesOversizedPrompt(t *testing.T) {
	ctx := context.Background()
	contents := oversizedPromptContents()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db, promptDigests(t, contents))
	fx.host.contextRead = planPromptFixtureContext(contents)
	head := planPromptFixtureHead(fx.repo, "d911911911911911911911911", "d91f71eb91f71eb91f71eb91f")
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatalf("corrected draft not recorded: %v", err)
	}
	c := &Coordinator{Store: db, Host: fx.host}
	_, _, wait, err := c.AssembleIntentPlanningInput(ctx, head, factory.IntentPlanningPropose)
	if wait != nil {
		t.Fatalf("oversized input waited: %+v", wait)
	}
	if !errors.Is(err, ErrIntentPlanningPromptBound) {
		t.Fatalf("assembly error = %v", err)
	}
	if _, err := db.IntentPlanAttempt(ctx, head.Intent, head.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused assembly admitted an attempt: %v", err)
	}
	if len(fx.host.launches) != 0 {
		t.Fatal("refused assembly reached the host launch")
	}
}

// TestDispatchIntentPlanRefusesOversizedPromptProvesReason pins the
// dispatch-layer guard behind assembly: input assembled past the
// admitted decompose phase bound refuses deterministically with the
// whitelisted prompt_over_bound reason before any packet, assignment,
// attempt or funding row exists.
func TestDispatchIntentPlanRefusesOversizedPromptProvesReason(t *testing.T) {
	ctx := context.Background()
	contents := oversizedPromptContents()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db, promptDigests(t, contents))
	fx.host.contextRead = planPromptFixtureContext(contents)
	head := planPromptFixtureHead(fx.repo, "d913913913913913913913913", "d913913913913913913913913")
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatalf("corrected draft not recorded: %v", err)
	}
	c := &Coordinator{Store: db, Host: fx.host}
	proj, prep, paths, planWait, err := c.resolvePlanningPreparation(ctx, head)
	if err != nil || planWait != nil {
		t.Fatalf("preparation err=%v wait=%+v", err, planWait)
	}
	merged, planWait, err := c.readPlanningContext(ctx, proj, prep, paths, time.Now().Add(2*time.Minute), fx.repo)
	if err != nil || planWait != nil {
		t.Fatalf("context err=%v wait=%+v", err, planWait)
	}
	in, planWait, err := assemblePlanningInput(head, factory.IntentPlanningPropose, prep.Preparation, paths, merged)
	if err != nil || planWait != nil {
		t.Fatalf("input err=%v wait=%+v", err, planWait)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("assembled input bytes = %d, decompose prompt bound = %d", len(raw), project.MaxDecomposePrompt)
	report := c.DispatchIntentPlan(ctx, head.Intent, head.Owner, factory.IntentPlanningPropose, in, prep.Preparation.SourceCommit, prep.Preparation.ID)
	if len(report.Launched) != 0 || len(report.Waits) != 0 || len(report.Errors) != 1 {
		t.Fatalf("dispatch report: %+v", report)
	}
	if got := report.Errors[0]; got.Reason != DispatchErrPromptBound || got.Detail != ErrIntentPlanningPromptBound.Error() {
		t.Fatalf("typed dispatch error = %+v", got)
	}
	if _, err := db.IntentPlanAttempt(ctx, head.Intent, head.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused dispatch admitted an attempt: %v", err)
	}
	if len(fx.host.launches) != 0 {
		t.Fatal("refused dispatch reached the host launch")
	}
}

// TestDispatchIntentPlanLaunchesDecomposeHeadroom proves the finite
// headroom end to end: approved context inside the 128 KiB
// intent-decompose bound but past the other-phase bound assembles,
// dispatches, funds and launches exactly one run with its complete
// approved evidence and digest intact.
func TestDispatchIntentPlanLaunchesDecomposeHeadroom(t *testing.T) {
	ctx := context.Background()
	contents := headroomPromptContents()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db, promptDigests(t, contents))
	fx.host.contextRead = planPromptFixtureContext(contents)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	head := planPromptFixtureHead(fx.repo, "d914914914914914914914914", "d914914914914914914914914")
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatalf("corrected draft not recorded: %v", err)
	}
	c := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker}
	in, prep, wait, err := c.AssembleIntentPlanningInput(ctx, head, factory.IntentPlanningPropose)
	if err != nil || wait != nil {
		t.Fatalf("assembly err=%v wait=%+v", err, wait)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= project.MaxFactoryPrompt {
		t.Fatalf("headroom fixture fits the old bound: %d bytes", len(raw))
	}
	report := c.DispatchIntentPlan(ctx, head.Intent, head.Owner, factory.IntentPlanningPropose, in, prep.SourceCommit, prep.ID)
	if len(report.Errors) != 0 || len(report.Waits) != 0 || len(report.Launched) != 1 {
		t.Fatalf("dispatch report: %+v", report)
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("host launches = %d", len(fx.host.launches))
	}
	a, err := db.AssignmentByRun(ctx, report.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Subject.Intent == nil || a.Subject.Intent.ScopeDigest != head.Digest() {
		t.Fatalf("launched assignment not bound to its head: %+v", a.Subject)
	}
	staged, planningStaged, err := ParseStagedPlanningInput(a.Prompt)
	if err != nil || !planningStaged {
		t.Fatalf("launched prompt lost its staged input: %v", err)
	}
	if len(staged.Inspected) != 2 {
		t.Fatalf("launched prompt lost approved evidence: %d refs", len(staged.Inspected))
	}
	for _, ref := range staged.Inspected {
		if want := contents[ref.Path]; ref.Evidence != want {
			t.Fatalf("launched prompt altered approved evidence for %s", ref.Path)
		}
		if ref.Commit != prep.SourceCommit {
			t.Fatalf("launched prompt lost its source commit for %s", ref.Path)
		}
	}
}

func promptDigests(t *testing.T, contents map[string]string) map[string]string {
	t.Helper()
	selected := map[string]string{}
	for path, content := range contents {
		selected[path] = planPromptDigest(t, content)
	}
	return selected
}

// TestDispatchIntentPlanLaunchesFittingPrompt is the control for the
// oversized-prompt diagnosis: the same assembly route with small approved
// context launches exactly one run.
func TestDispatchIntentPlanLaunchesFittingPrompt(t *testing.T) {
	ctx := context.Background()
	contents := map[string]string{"README.md": "small approved readme", "docs/main.go": "small approved source"}
	selected := map[string]string{}
	for path, content := range contents {
		selected[path] = planPromptDigest(t, content)
	}
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db, selected)
	fx.host.contextRead = planPromptFixtureContext(contents)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	head := planPromptFixtureHead(fx.repo, "d922922922922922922922922", "d922922922922922922922922")
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatalf("corrected draft not recorded: %v", err)
	}
	c := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker}
	in, prep, wait, err := c.AssembleIntentPlanningInput(ctx, head, factory.IntentPlanningPropose)
	if err != nil || wait != nil {
		t.Fatalf("assembly err=%v wait=%+v", err, wait)
	}
	report := c.DispatchIntentPlan(ctx, head.Intent, head.Owner, factory.IntentPlanningPropose, in, prep.SourceCommit, prep.ID)
	if len(report.Errors) != 0 || len(report.Waits) != 0 || len(report.Launched) != 1 {
		t.Fatalf("dispatch report: %+v", report)
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("host launches = %d", len(fx.host.launches))
	}
}

func TestDispatchTimeOnlyRootReplaySingleCharge(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	digest := recordTimeOnlyIntent(t, ctx, db)
	deps := fx.deps()
	occupancy, err := snapshotOccupancy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	first := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, deps, &occupancy, fx.repo, timeOnlyIntentSubject(digest), strings.Repeat("c", 40), []byte("plan the recorded draft"), "", &first)
	if len(first.Launched) != 1 {
		t.Fatalf("first route report: %+v", first)
	}
	// Settle the first run so the replay reaches the packet instead of
	// the unsettled-session wait: the finished assignment still blocks
	// any second root for the same subject.
	run, err := db.FactoryRun(ctx, first.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "planning done", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	second := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, deps, &occupancy, fx.repo, timeOnlyIntentSubject(digest), strings.Repeat("c", 40), []byte("plan the recorded draft"), "", &second)
	// The held first reservation waits at limits before any packet: no
	// second run launches and no second charge records. The cross-pass
	// race underneath is covered at the packet by the unfinished
	// completion assignment uniqueness (store concurrent test).
	if len(second.Launched) != 0 || len(second.Waits) == 0 {
		t.Fatalf("replay routed a second run: %+v", second)
	}
	root := first.Launched[0].AssignmentID
	allowance, err := db.IntentAttemptAllowance(ctx, fx.repo, factory.IntentAttemptScope{IntentID: "d777777777777777777777777", OwnerID: 7}, root)
	if err != nil || allowance.Limits.ActiveMinutes != 45 || !allowance.Active {
		t.Fatalf("replay disturbed the first charge: %+v %v", allowance, err)
	}
}

func TestDispatchTimeOnlyRootUnfundedWaits(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	digest := recordTimeOnlyIntent(t, ctx, db)
	deps := fx.deps()
	occupancy, err := snapshotOccupancy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	report := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, deps, &occupancy, fx.repo, timeOnlyIntentSubject(digest), strings.Repeat("c", 40), []byte("plan the recorded draft"), "", &report)
	if len(report.Launched) != 0 || len(report.Errors) != 0 || len(report.Waits) != 1 || report.Waits[0].Reason != WaitAllowance {
		t.Fatalf("unfunded route report: %+v", report)
	}
	if len(fx.host.launches) != 0 {
		t.Fatal("unfunded root reached the host")
	}
}

func TestDispatchTimeOnlyRootRefusesBadSubjects(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	deps := fx.deps()
	occupancy, err := snapshotOccupancy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	subjects := map[string]factory.ExecutionSubject{
		"unrecorded intent": {
			Kind:   factory.SubjectIntent,
			Intent: &factory.IntentSubject{IntentID: "d111111111111111111111111", RevisionID: "d222222222222222222222222", ScopeDigest: strings.Repeat("d", 64), OwnerID: 7},
		},
		"issue subject": {
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 3, Acceptance: "d333333333333333333333333"},
		},
		"unstaged completion": {
			Kind:       factory.SubjectCompletion,
			Completion: &factory.CompletionSubject{CompletionID: factory.NewID(), CycleID: factory.NewID(), InventoryDigest: strings.Repeat("f", 64)},
		},
	}
	for name, subject := range subjects {
		report := newDispatchReport()
		DispatchTimeOnlyRoot(ctx, deps, &occupancy, fx.repo, subject, strings.Repeat("c", 40), []byte("plan the recorded draft"), "", &report)
		if len(report.Launched) != 0 || len(report.Errors) == 0 {
			t.Fatalf("%s routed: %+v", name, report)
		}
	}
	if len(fx.host.launches) != 0 {
		t.Fatal("refused subjects reached the host")
	}
}

// timeOnlyEvidenceBody mirrors the sealed evidence shape with
// computed digests so the sealed cycle validates.
func timeOnlyEvidenceBody(t *testing.T) factory.CompletionIssueEvidence {
	t.Helper()
	sum := func(text string) string {
		digest := sha256.Sum256([]byte(text))
		return hex.EncodeToString(digest[:])
	}
	parsed, err := factory.ParseCompletionIssueEvidence(mustCanonicalBody(t, factory.CompletionIssueEvidence{
		Index: 9, Title: "completion title", Body: "completion body",
		TitleDigest: sum("completion title"), ContentDigest: sum("completion body"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustCanonicalBody(t *testing.T, body factory.CompletionIssueEvidence) string {
	t.Helper()
	data, err := body.CanonicalData()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// sealTimeOnlyCycle drives one collecting cycle to a sealed, decided
// reassess outcome through the production CAS joint with a captured
// native local, returning the sealed inventory digest and source.
func sealTimeOnlyCycle(t *testing.T, ctx context.Context, db *store.Store, repo int64) (eventID, cycleID, digest, source string) {
	t.Helper()
	cycles, err := db.PendingReassessmentCycles(ctx, repo)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("pending cycles: %+v %v", cycles, err)
	}
	eventID, cycleID = cycles[0].Cycle.EventID, cycles[0].Cycle.CycleID
	local, err := db.CaptureCompletionLocal(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	digest, source = strings.Repeat("d", 64), local.SourceCommit
	record, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	record.Local = local
	record.InventoryDigest = digest
	record.Evidence = []factory.CompletionIssueEvidence{timeOnlyEvidenceBody(t)}
	record.EvidenceIssues = 1
	record.EvidenceRevision = 41
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, record); err != nil {
		t.Fatalf("seal not recorded: %v", err)
	}
	decided, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	decided.Cycle.Decision = factory.ReassessmentOptionReassess
	decided.Cycle.State = factory.ReassessmentDeciding
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, decided); err != nil {
		t.Fatalf("decision not recorded: %v", err)
	}
	return eventID, cycleID, digest, source
}

// mergeTimeOnlyCompletion records one merged merge through the
// exported merge joints, returning the fixture holding its recorded
// completion event and staged reassessment cycle. It authorizes the
// union generation, records the committed native effect, then finishes
// the merge as merged: UpdateMerge atomically queues the completion
// root exactly like the production reconcile path.
func mergeTimeOnlyCompletion(t *testing.T, ctx context.Context) *mergeFixture {
	t.Helper()
	fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 63)
	if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, unionAuthForBind(t, m, acceptance, decision, fingerprint, true)); err != nil {
		t.Fatal(err)
	}
	driven := recordGenerationIntent(t, ctx, fx.publish.db, m)
	driven.Operation.Effect, driven.Operation.Cancellation = factory.OpEffectCommitted, factory.OpCancelNone
	driven.Operation.Completion = factory.OpCompletionComplete
	driven.Operation.MergedCommit = driven.HeadOID
	driven.Operation.PRNumber, driven.Operation.PRID, driven.Operation.IssueID = driven.PRNumber, driven.PRID, driven.IssueID
	driven.Operation.HeadRef, driven.Operation.BaseRef = driven.HeadRef, driven.BaseRef
	driven.Operation.HeadOID, driven.Operation.BaseOID = driven.HeadOID, driven.BaseOID
	driven.Revision++
	if err := fx.publish.db.UpdateMerge(ctx, driven); err != nil {
		t.Fatal("record commit:", err)
	}
	committed, err := fx.publish.db.MergeByPublication(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	committed.Stage, committed.Outcome, committed.Reason = factory.MergeMerged, factory.Succeeded, factory.MergeReasonMerged
	committed.MergedCommit = committed.HeadOID
	committed.FinishedUnix, committed.MergedUnix, committed.ClosedUnix = now, now, now
	committed.OriginalClosingOccurrence, committed.OriginalClosedUnix = "501", now
	committed.Revision++
	if err := fx.publish.db.UpdateMerge(ctx, committed); err != nil {
		t.Fatal("record merged:", err)
	}
	stored, err := fx.publish.db.MergeByPublication(ctx, p.ID)
	if err != nil || stored.Stage != factory.MergeMerged {
		t.Fatalf("merge not completed: %+v %v", stored, err)
	}
	return fx
}

func TestDispatchTimeOnlyRootLaunchesReassessment(t *testing.T) {
	ctx := context.Background()
	fx := mergeTimeOnlyCompletion(t, ctx)
	db := fx.publish.db
	repo := fx.publish.seed.repo
	fundTimeOnlyPolicy(t, ctx, db, repo)
	eventID, cycleID, digest, source := sealTimeOnlyCycle(t, ctx, db, repo)
	deps := fx.publish.seed.deps()
	occupancy, err := snapshotOccupancy(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	subject := factory.ExecutionSubject{
		Kind:       factory.SubjectCompletion,
		Completion: &factory.CompletionSubject{CompletionID: eventID, CycleID: cycleID, InventoryDigest: digest},
	}
	var observed bool
	fx.publish.seed.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		a, err := db.AssignmentByRun(context.Background(), in.Run.ID)
		if err != nil || a.Phase != project.ExecutionReassess || a.Issue != 0 || a.AttemptRoot != a.ID || a.PublicationAssignment != "" {
			t.Fatalf("host call precedes its packet: assignment=%+v err=%v", a, err)
		}
		allowance, err := db.ReassessmentAttemptAllowance(context.Background(), repo, *a.Subject.Completion, a.ID)
		if err != nil || allowance.Limits.ActiveMinutes != 30 || !allowance.Active {
			t.Fatalf("host call precedes its funding: allowance=%+v err=%v", allowance, err)
		}
		observed = true
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
	}
	report := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, deps, &occupancy, repo, subject, source, []byte("reassess the sealed inventory"), "", &report)
	if len(report.Errors) != 0 || len(report.Waits) != 0 || len(report.Launched) != 1 {
		t.Fatalf("route report: %+v", report)
	}
	// The report phase echoes the host-observed run phase; the
	// assignment phase was already bound inside the order proof.
	if launched := report.Launched[0]; launched.Issue != 0 || launched.AssignmentID == "" || launched.RunID == "" {
		t.Fatalf("launched = %+v", launched)
	}
	if !observed {
		t.Fatal("host double never ran")
	}
}

// The production completion driver reaches the existing admission path
// after its paid reassess choice. PostgreSQL proves the cycle ledger,
// packet and allowance commit before the native launch callback, and a
// later drive reuses that exact run rather than consulting or launching
// again. The native pages and worker are scripted; this is not a paid
// provider or installed-runtime acceptance claim.
func TestCompletionDriveLaunchesSingleReassessmentPG(t *testing.T) {
	ctx := context.Background()
	fx := mergeTimeOnlyCompletion(t, ctx)
	db, coord := fx.publish.db, fx.publish.coord
	fundTimeOnlyPolicy(t, ctx, db, fx.publish.seed.repo)
	coord.Host, coord.Broker = fx.publish.seed.host, fx.publish.seed.broker
	chooser := &reassessmentLifecycleChooser{}
	coord.Planning = &PlanningJudgments{Chooser: chooser}
	enumerator := &fixtureEnumerator{revision: 41, idle: true, pages: map[string]NativeInventoryPage{
		"":  {Issues: []NativeInventoryIssue{inventoryIssue(t, 2), inventoryIssue(t, 7)}, NextCursor: "7"},
		"7": {Issues: []NativeInventoryIssue{inventoryIssue(t, 9)}, Complete: true},
	}}
	deps := CompletionDriveDeps{Enumerator: enumerator, CaptureLocal: db.CaptureCompletionLocal}
	cycleID := pendingCycleID(t, fx)
	if report := coord.DriveCompletions(ctx, deps); len(report.Sealed) != 1 {
		t.Fatalf("inventory did not seal: %+v", report)
	}
	var launched string
	fx.publish.seed.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		for _, guidance := range []string{"factory.reassessment_scope", "factory.record_diagnosis", "factory.draft_repair", "factory.preview_repair", "payload_hash", "no extra model consultation", "Finish once"} {
			if !strings.Contains(string(in.Prompt), guidance) {
				t.Fatal("completion Strategist lost R40 tool guidance", guidance)
			}
		}
		record, err := db.ReassessmentRecord(ctx, cycleID)
		if err != nil || record.Cycle.State != factory.ReassessmentReassessing || len(record.Cycle.Ledger.CoderRuns) != 1 || record.Cycle.Ledger.CoderRuns[0] != in.Run.ID {
			t.Fatalf("launch preceded sole-run admission: %+v %v", record, err)
		}
		a, err := db.AssignmentByRun(ctx, in.Run.ID)
		if err != nil || a.Subject.Completion == nil || a.Subject.Completion.CycleID != cycleID {
			t.Fatalf("launch preceded assignment: %+v %v", a, err)
		}
		allowance, err := db.ReassessmentAttemptAllowance(ctx, a.Repository, *a.Subject.Completion, a.ID)
		if err != nil || allowance.Limits.ActiveMinutes != 30 || !allowance.Active {
			t.Fatalf("launch preceded funding: %+v %v", allowance, err)
		}
		for _, body := range record.Evidence {
			raw, err := body.CanonicalData()
			if err != nil || !strings.Contains(string(in.Prompt), raw) {
				t.Fatalf("launch lacks complete sealed body %d: %v", body.Index, err)
			}
		}
		launched = in.Run.ID
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryRunning}, nil
	}
	report := coord.DriveCompletions(ctx, deps)
	if launched == "" || len(report.Decided) != 1 || len(report.Held) != 0 || chooser.calls != 1 {
		t.Fatalf("reassess did not launch once: report=%+v calls=%d run=%q", report, chooser.calls, launched)
	}
	coord.DriveCompletions(ctx, deps)
	if len(fx.publish.seed.host.launches) != 1 || chooser.calls != 1 {
		t.Fatalf("drive repeated worker or decision: launches=%d calls=%d", len(fx.publish.seed.host.launches), chooser.calls)
	}
	current, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil || len(current.Cycle.Ledger.CoderRuns) != 1 || current.Cycle.Ledger.CoderRuns[0] != launched {
		t.Fatalf("replay changed the admitted run: %+v %v", current, err)
	}
	cap, err := db.GetMCPCapability(ctx, launched)
	if err != nil {
		t.Fatal(err)
	}
	required, err := db.FactoryCurrentContext(ctx, cap, time.Now())
	if err != nil || required.Role.Strategy == nil || !required.Role.Strategy.Complete || required.Role.Strategy.InventoryDigest != current.InventoryDigest || len(required.Role.Strategy.Issues) != len(current.Evidence) || !required.Role.Strategy.Local.Equal(current.Local) {
		t.Fatalf("strategy lost exact sealed input: %v", err)
	}
	localDigest, err := current.Local.Digest()
	if err != nil {
		t.Fatal(err)
	}
	retained := make([]int64, 0, len(current.Evidence))
	for _, body := range current.Evidence {
		retained = append(retained, body.Index)
	}
	proposal := factory.ReassessmentReport{
		CompletionID: current.Cycle.EventID, CycleID: cycleID, InventoryDigest: current.InventoryDigest,
		SourceCommit: current.Local.SourceCommit, PreparationDigest: current.Local.PreparationDigest,
		LocalInputDigest: localDigest, Summary: "The complete inventory remains coherent; retain its current issues.",
		RetainedIssueIndexes: retained,
	}
	// The worker can inspect a genuine no-change completion proposal without
	// buying another judgment or mutating the retained cycle/allowance.
	if err := db.ActivateObservedMCPCapability(ctx, cap); err != nil {
		t.Fatal(err)
	}
	cap, err = db.GetMCPCapability(ctx, launched)
	if err != nil {
		t.Fatal(err)
	}
	coord.CompletionEnumerator, coord.CompletionLocalCapture = enumerator, db.CaptureCompletionLocal
	before, _ := json.Marshal(current)
	previewSubject := factory.CompletionSubject{CompletionID: current.Cycle.EventID, CycleID: cycleID, InventoryDigest: current.InventoryDigest}
	allowanceBefore, err := db.ReassessmentAttemptAllowance(ctx, fx.publish.seed.repo, previewSubject, cap.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(proposal)
	for i := 0; i < 2; i++ {
		preview, err := coord.PreviewRepair(ctx, cap, encoded)
		if err != nil || !preview.Valid || preview.Validation == nil || len(preview.Effects) != 0 || preview.InputDigest != cap.ScopeDigest() {
			t.Fatalf("complete no-effect preview: %+v %v", preview, err)
		}
	}
	after, _ := db.ReassessmentRecord(ctx, cycleID)
	afterJSON, _ := json.Marshal(after)
	allowanceAfter, err := db.ReassessmentAttemptAllowance(ctx, fx.publish.seed.repo, previewSubject, cap.AssignmentID)
	oldAllowance, _ := json.Marshal(allowanceBefore)
	newAllowance, _ := json.Marshal(allowanceAfter)
	if err != nil || string(before) != string(afterJSON) || string(oldAllowance) != string(newAllowance) || chooser.calls != 1 || len(fx.publish.seed.host.launches) != 1 {
		t.Fatal("preview spent or changed canonical completion state", err)
	}
	diagnosis := factory.RepairDiagnosis{Subject: cap.Subject, InputDigest: cap.ScopeDigest(), SourceCommit: current.Local.SourceCommit, InventoryDigest: current.InventoryDigest, Title: "Completed inventory remains coherent", Summary: "No graph change is justified by the current completion evidence.", State: "supported", Causes: []factory.CheckFailure{{CheckRef: "completion_inventory", Detail: "The complete sealed inventory remains accounted for."}}, EvidenceRefs: []string{current.InventoryDigest}, Alternatives: []string{}, Uncertainty: []string{}}
	encoded, _ = json.Marshal(diagnosis)
	submission := testMCPSubmission(cap, factory.MCPToolRecordDiagnosis, factory.NewID(), encoded)
	diagnosisPost, err := db.RecordWorkerPost(ctx, submission)
	if err != nil || diagnosisPost.Record.ID == "" {
		t.Fatal("completion diagnosis lacked its post", err)
	}
	scope, err := db.ReassessmentScope(ctx, cap, 0)
	if err != nil || !scope.Complete || scope.Total != 1 || len(scope.Records) != 1 || scope.Records[0].ID != diagnosisPost.Record.ID || scope.InventoryDigest != current.InventoryDigest {
		t.Fatal("completion named scope lost its own diagnosis", err)
	}
	after, _ = db.ReassessmentRecord(ctx, cycleID)
	afterJSON, _ = json.Marshal(after)
	if string(before) != string(afterJSON) || chooser.calls != 1 || len(fx.publish.seed.host.launches) != 1 {
		t.Fatal("recording completion diagnosis granted effects or judgment")
	}
	reportJSON, _ := json.Marshal(proposal)
	draftJSON, _ := json.Marshal(factory.RepairDraft{Report: reportJSON, DiagnosisRefs: []string{"diagnosis:" + diagnosisPost.Record.ID}})
	draft := testMCPSubmission(cap, factory.MCPToolDraftRepair, factory.NewID(), draftJSON)
	if posted, err := db.RecordWorkerPost(ctx, draft); err != nil || posted.Record.ID == "" {
		t.Fatal("completion no-change draft was not durable", err)
	}
	scope, err = db.ReassessmentScope(ctx, cap, 0)
	if err != nil || scope.Total != 2 || len(scope.Records) != 2 || scope.Records[1].Tool != factory.MCPToolDraftRepair || chooser.calls != 1 {
		t.Fatal("completion scope did not retain separate diagnosis/draft stages", err)
	}
	run, err := db.FactoryRun(ctx, launched)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "reassessment completed", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	output := phaseFinish(t, factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionReassess, Status: "completed", Summary: proposal.Summary, Output: &factory.PhaseOutput{Reassessment: &proposal}})
	if assignment, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); !ok || assignment.Outcome != factory.Succeeded {
		t.Fatalf("typed reassessment result did not settle: %+v %v", assignment, ok)
	}
	coord.DriveCompletions(ctx, deps)             // settled coder -> proposal assessment
	coord.DriveCompletions(ctx, deps)             // funded proposal -> accepted
	released := coord.DriveCompletions(ctx, deps) // fresh no-change -> released
	if len(released.Released) != 1 || released.Released[0] != cycleID {
		t.Fatalf("confirmed no-change report did not release: %+v", released)
	}
	current, err = db.ReassessmentRecord(ctx, cycleID)
	if err != nil || current.Cycle.State != factory.ReassessmentReleased || len(current.Cycle.Ledger.DecisionCalls) != 2 || len(current.Cycle.Ledger.CoderRuns) != 1 || chooser.calls != 2 {
		t.Fatalf("reassessment lifecycle lost its bounded ledger: %+v err=%v calls=%d", current, err, chooser.calls)
	}
	coord.DriveCompletions(ctx, deps)
	if chooser.calls != 2 || len(fx.publish.seed.host.launches) != 1 {
		t.Fatal("released completion repeated provider or worker work")
	}
}

type reassessmentLifecycleChooser struct{ calls int }

func (c *reassessmentLifecycleChooser) Select(_ context.Context, state any, _ string, options map[string]any) (factory.PriorityAnswer, error) {
	c.calls++
	selected := factory.ReassessmentOptionReassess
	_, repair := state.(repairProposalState)
	_, deliveryRepair := state.(deliveryRepairProposalState)
	if _, ok := state.(ReassessmentProposalState); ok || repair || deliveryRepair {
		selected = factory.ReassessmentOptionAccept
	} else if _, ok := state.(ReassessmentDecisionState); !ok {
		return factory.PriorityAnswer{}, errReassessmentTestState
	}
	answer := factory.PriorityAnswer{Selected: selected, Confidence: 0.95, Model: "integration-fixture", Probabilities: map[string]float64{}}
	for option := range options {
		answer.Probabilities[option] = 0.05
	}
	answer.Probabilities[selected] = 0.95
	return answer, nil
}

type reassessmentGraphInventory struct{ graph *generatedGraphFixture }

func (e reassessmentGraphInventory) ObserveRevision(ctx context.Context) (int64, bool, error) {
	revision, err := e.graph.CheckIntentOwner(ctx, 7, 7, e.graph.actor)
	return revision, err == nil, err
}

func (e reassessmentGraphInventory) EnumerateIssues(_ context.Context, _ int64, cursor string) (NativeInventoryPage, error) {
	e.graph.mu.Lock()
	defer e.graph.mu.Unlock()
	page := NativeInventoryPage{Revision: e.graph.revision, Complete: true, Idle: true}
	for index, snapshot := range e.graph.issues {
		if snapshot.Issue.IsClosed {
			continue
		}
		body := factory.CompletionIssueEvidence{Index: index, Title: snapshot.Issue.Title, Body: snapshot.Issue.Content, TitleDigest: snapshot.Issue.TitleDigest, ContentDigest: snapshot.Issue.ContentDigest}
		for _, dependency := range snapshot.Dependencies.Items {
			body.Dependencies = append(body.Dependencies, dependency.DependencyID)
		}
		sort.Slice(body.Dependencies, func(i, j int) bool {
			left, right := body.Dependencies[i], body.Dependencies[j]
			return len(left) < len(right) || len(left) == len(right) && left < right
		})
		raw, err := body.CanonicalData()
		if err != nil {
			return NativeInventoryPage{}, err
		}
		page.Issues = append(page.Issues, NativeInventoryIssue{Index: index, Data: raw})
	}
	sort.Slice(page.Issues, func(i, j int) bool { return page.Issues[i].Index < page.Issues[j].Index })
	return page, nil
}

type reassessmentLostReplyGraph struct {
	*generatedGraphFixture
	loseReply bool
}

func (g *reassessmentLostReplyGraph) SubmitGraph(ctx context.Context, generation factory.IntentGeneration, repository int64) (factory.GraphOperationOutcome, error) {
	record, err := g.generatedGraphFixture.SubmitGraph(ctx, generation, repository)
	if err == nil && g.loseReply {
		g.loseReply = false
		return factory.GraphOperationOutcome{}, errors.New("scripted lost reply after committed native effect")
	}
	return record, err
}

// Real PostgreSQL drives an approved changing proposal through its
// durable serial journal and existing native-generation adoption. The
// graph endpoint is scripted, including an actual lost reply after its
// recorded effect. A separate retirement case performs no native
// mutation and keeps a manually changed protected task's exact binding.
func TestReassessmentSerialAdoptionPG(t *testing.T) {
	for _, mode := range []string{"edit_lost_reply", "retire_local_only", "two_intents_lost_reply", "two_intents_later_head_conflict"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			fx := mergeTimeOnlyCompletion(t, ctx)
			db, coord := fx.publish.db, fx.publish.coord
			fundTimeOnlyPolicy(t, ctx, db, fx.publish.seed.repo)
			coord.Host, coord.Broker = fx.publish.seed.host, fx.publish.seed.broker
			graph := &generatedGraphFixture{revision: 41, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
			coord.Graph, coord.GraphActorID, coord.AcceptanceReads = graph, graph.actor, graph
			revision := broadJoinAcceptedRevision()
			revision.Repository, revision.Tasks, revision.Ideas = fx.publish.seed.repo, revision.Tasks[:2], revision.Ideas[:1]
			if err := db.SaveIntentRevision(ctx, revision); err != nil {
				t.Fatal(err)
			}
			// Existing mechanics fixture owns only the initial accepted
			// graph; the reassessment below uses production provenance.
			if generation, err := generateChoiceMechanics(t, coord, ctx, revision.Intent, revision.Owner, chosenFor(revision)); err != nil || generation.State != "completed" {
				t.Fatalf("initial graph unavailable: %+v %v", generation, err)
			}
			initialBindings, err := db.IntentTaskBindings(ctx, revision.ID)
			if err != nil || len(initialBindings) != 2 {
				t.Fatalf("initial task bindings: %+v %v", initialBindings, err)
			}
			var second factory.IntentRevision
			multiple := strings.HasPrefix(mode, "two_intents_")
			if multiple {
				second = revision
				second.ID, second.Intent = "d777777777777777777777777", "d888888888888888888888888"
				if err := db.SaveIntentRevision(ctx, second); err != nil {
					t.Fatal(err)
				}
				if generation, err := generateChoiceMechanics(t, coord, ctx, second.Intent, second.Owner, chosenFor(second)); err != nil || generation.State != "completed" {
					t.Fatalf("second initial graph unavailable: state=%s err=%v", generation.State, err)
				}
			}
			// A human-edited task must survive unchanged under its old
			// acceptance, even while the other idle task is revised.
			graph.issues[2].Issue.Content += "\nRetained human note."
			graph.issues[2].Issue.ContentDigest = evidenceDigest(graph.issues[2].Issue.Content)
			graph.issues[2].Issue.ContentVersion++
			graph.revision++
			protectedBody := graph.issues[2].Issue.Content
			baselineSubmissions := graph.submissions
			coord.Graph = &reassessmentLostReplyGraph{generatedGraphFixture: graph, loseReply: mode != "retire_local_only"}
			enumerator := reassessmentGraphInventory{graph: graph}
			deps := CompletionDriveDeps{Enumerator: enumerator, CaptureLocal: db.CaptureCompletionLocal}
			coord.CompletionEnumerator, coord.CompletionLocalCapture = enumerator, db.CaptureCompletionLocal
			chooser := &reassessmentLifecycleChooser{}
			coord.Planning = &PlanningJudgments{Chooser: chooser}
			cycleID := pendingCycleID(t, fx)
			coord.DriveCompletions(ctx, deps)
			coord.DriveCompletions(ctx, deps)
			record, err := db.ReassessmentRecord(ctx, cycleID)
			if err != nil || len(record.Cycle.Ledger.CoderRuns) != 1 {
				t.Fatalf("reassessment coder missing: %+v %v", record, err)
			}
			localDigest, err := record.Local.Digest()
			if err != nil {
				t.Fatal(err)
			}
			tasks := append([]factory.IntentTask(nil), revision.Tasks...)
			if mode == "retire_local_only" {
				tasks = tasks[1:]
			} else {
				tasks[0].Objective += " with the clarified validation boundary"
			}
			keys := make([]string, 0, len(tasks))
			for _, task := range tasks {
				keys = append(keys, task.Key)
			}
			successorID := generatedDecisionID("revision", cycleID, revision.ID)
			proposal := factory.ReassessmentReport{
				CompletionID: record.Cycle.EventID, CycleID: cycleID, InventoryDigest: record.InventoryDigest,
				SourceCommit: record.Local.SourceCommit, PreparationDigest: record.Local.PreparationDigest,
				LocalInputDigest: localDigest, Summary: "Revise only the idle task and retain the protected task.",
				RetainedIssueIndexes: []int64{2},
				IntentChanges:        []factory.ReassessmentIntentChange{{IntentID: revision.Intent, ExpectedRevision: successorID, ScopeDigest: revision.Digest(), Reason: "The completed work clarified the remaining boundary.", Tasks: tasks}},
				Coverage:             []factory.ReassessmentCoverage{{IntentID: revision.Intent, OutcomeID: revision.Ideas[0].ID, TaskKeys: keys, DeliveredAcceptanceIDs: []string{record.Completion.AcceptanceID}}},
			}
			secondSuccessor := ""
			if multiple {
				secondTasks := append([]factory.IntentTask(nil), second.Tasks...)
				secondTasks[0].Objective += " with an independent clarified boundary"
				secondSuccessor = generatedDecisionID("revision", cycleID, second.ID)
				proposal.RetainedIssueIndexes = append(proposal.RetainedIssueIndexes, 4)
				proposal.IntentChanges = append(proposal.IntentChanges, factory.ReassessmentIntentChange{IntentID: second.Intent, ExpectedRevision: secondSuccessor, ScopeDigest: second.Digest(), Reason: "Clarify the other idle intent independently.", Tasks: secondTasks})
				proposal.Coverage = append(proposal.Coverage, factory.ReassessmentCoverage{IntentID: second.Intent, OutcomeID: second.Ideas[0].ID, TaskKeys: keys, DeliveredAcceptanceIDs: []string{record.Completion.AcceptanceID}})
			}
			if err := proposal.Validate(); err != nil {
				t.Fatalf("proposal fixture invalid: %v", err)
			}
			run, err := db.FactoryRun(ctx, record.Cycle.Ledger.CoderRuns[0])
			if err != nil {
				t.Fatal(err)
			}
			run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "reassessment completed", true
			if err := db.SaveFactoryRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			output := phaseFinish(t, factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionReassess, Status: "completed", Summary: proposal.Summary, Output: &factory.PhaseOutput{Reassessment: &proposal}})
			if assignment, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); !ok || assignment.Outcome != factory.Succeeded {
				t.Fatalf("typed proposal did not settle: outcome=%s reason=%s recorded=%v", assignment.Outcome, assignment.Reason, ok)
			}
			var last CompletionDriveReport
			restarted := false
			conflictID := ""
			for pass := 0; pass < 16; pass++ {
				last = coord.DriveCompletions(ctx, deps)
				record, err = db.ReassessmentRecord(ctx, cycleID)
				if err != nil {
					t.Fatal(err)
				}
				if multiple && !restarted && record.Cycle.State == factory.ReassessmentApplying {
					progress, err := loadReassessmentAdoption(coord.Graph, record)
					if err != nil {
						t.Fatal(err)
					}
					if progress.Journal.Cursor == 1 {
						// New controller instance reads only retained Store/native
						// state after the first exact operation is committed.
						fresh := NewCoordinator(db, coord.Host, coord.Broker)
						fresh.Graph, fresh.GraphActorID, fresh.AcceptanceReads = coord.Graph, coord.GraphActorID, coord.AcceptanceReads
						fresh.CompletionEnumerator, fresh.CompletionLocalCapture, fresh.Planning = enumerator, db.CaptureCompletionLocal, coord.Planning
						coord, restarted = fresh, true
						if mode == "two_intents_later_head_conflict" {
							manual := second
							manual.ID, manual.Predecessor, manual.State = generatedDecisionID("manual", cycleID, second.ID), second.ID, "draft"
							manual.Objective += " with a human revision"
							if err := db.SaveIntentRevision(ctx, manual); err != nil {
								t.Fatal(err)
							}
							conflictID = manual.ID
						}
					}
				}
				if conflictID != "" && record.WaitReason == "reassessment_inputs_changed_after_prefix" {
					break
				}
				if record.Cycle.State == factory.ReassessmentReleased {
					break
				}
			}
			if conflictID != "" {
				progress, progressErr := loadReassessmentAdoption(coord.Graph, record)
				first, firstErr := db.IntentGeneration(ctx, successorID)
				later, laterErr := db.IntentRevision(ctx, second.Intent, second.Owner)
				if progressErr != nil || firstErr != nil || laterErr != nil || record.Cycle.State != factory.ReassessmentApplying || record.WaitReason != "reassessment_inputs_changed_after_prefix" || progress.Journal.Cursor != 1 || first.State != "completed" || later.ID != conflictID || graph.submissions != baselineSubmissions+1 || chooser.calls != 2 {
					t.Fatalf("later conflict lost the committed prefix: state=%s wait=%s cursor=%d first=%s submissions=%d err=%v/%v/%v", record.Cycle.State, record.WaitReason, progress.Journal.Cursor, first.State, graph.submissions, progressErr, firstErr, laterErr)
				}
				if _, err := db.IntentGeneration(ctx, secondSuccessor); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("conflicting later successor activated: %v", err)
				}
				if _, err := coord.StartCompletionRetry(ctx, factory.ReassessmentRetry{ExpectedPriorCycle: cycleID, SuccessorCycleID: factory.NewID(), CommandID: factory.NewID(), EventID: record.Cycle.EventID}); err == nil {
					t.Fatal("Retry abandoned an incompletely adopted revision set")
				}
				return
			}
			if record.Cycle.State != factory.ReassessmentReleased {
				t.Fatalf("serial adoption did not release: state=%s wait=%s report=%+v progress=%s", record.Cycle.State, record.WaitReason, last, record.AdoptionProgress)
			}
			head, err := db.IntentRevision(ctx, revision.Intent, revision.Owner)
			if err != nil || head.ID != successorID {
				t.Fatalf("successor head missing: %+v %v", head, err)
			}
			bindings, err := db.IntentTaskBindings(ctx, successorID)
			if err != nil || len(bindings) != len(tasks) {
				t.Fatalf("successor adoption incomplete: %+v %v", bindings, err)
			}
			for _, binding := range bindings {
				if binding.TaskKey == revision.Tasks[1].Key && binding.AcceptanceID != initialBindings[1].AcceptanceID {
					t.Fatal("protected acceptance was replaced")
				}
			}
			if graph.issues[2].Issue.Content != protectedBody || chooser.calls != 2 {
				t.Fatalf("protected work or bounded calls changed: calls=%d", chooser.calls)
			}
			wantSubmissions := baselineSubmissions
			if mode != "retire_local_only" {
				wantSubmissions++
			}
			if multiple {
				wantSubmissions++
				secondHead, err := db.IntentRevision(ctx, second.Intent, second.Owner)
				if err != nil || secondHead.ID != secondSuccessor || !restarted {
					t.Fatalf("second successor did not survive restart: id=%s restarted=%v err=%v", secondHead.ID, restarted, err)
				}
			}
			if graph.submissions != wantSubmissions {
				t.Fatalf("native operation count=%s want=%d", strconv.Itoa(graph.submissions), wantSubmissions)
			}
			if _, err := db.CaptureCompletionLocal(ctx, revision.Repository); err != nil {
				t.Fatalf("adopted completion material is invalid: %v", err)
			}
		})
	}
}

// intentPlanHead returns a repository-bound draft head owned by the
// fixture operator for the R12 connector proof.
func intentPlanHead(now time.Time) factory.IntentRevision {
	return factory.IntentRevision{
		ID: "d888888888888888888888888", Intent: "d777777777777777777777777", Owner: 7, Repository: 7,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: now.Unix(),
		Ideas: []factory.ProductIdea{
			{ID: "d333333333333333333333333", Description: "Capture leave requests", Consequence: "Requests can be recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
		},
	}
}

// intentPlanInput stages the admitted planning input for the head:
// the requested operation with its inspected source investigation.
func intentPlanInput(t *testing.T, head factory.IntentRevision, op string) factory.SemanticPlannerInput {
	t.Helper()
	content, err := factory.ComposeProposalContent(head)
	if err != nil {
		t.Fatalf("proposal content: %v", err)
	}
	return factory.SemanticPlannerInput{
		Operation:    op,
		Head:         head,
		Content:      content,
		Alternatives: factory.DescribeAlternatives(head.Objective),
		Inspected: []factory.InspectedSource{
			{Path: "internal/factory/intent.go", Commit: strings.Repeat("c", 40), Evidence: "Draft head carries attributed effective scope with no tasks yet"},
		},
	}
}

// TestDispatchIntentPlanConnectsHeadToBoundResult proves the R12 source
// connection on real PostgreSQL: the connector admits the reviewed
// repository-bound head for the requested operation, stages the bounded
// prompt and launches through the shared route with a bounded host
// double; the host's answer settles through AccountSettledRun into the
// typed result bound to the assignment's exact intent and revision,
// while an answer for another revision refuses without a report.
func TestDispatchIntentPlanConnectsHeadToBoundResult(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	head := intentPlanHead(time.Now().UTC().Truncate(time.Second))
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatal(err)
	}
	in := intentPlanInput(t, head, factory.IntentPlanningPropose)
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.deps().Reads, MCPKey: []byte(strings.Repeat("k", 32)), Priority: &PriorityJudgments{Journal: db, Chooser: &bundleGapChooser{pick: factory.ContextBundleCurrent}}}
	var observed bool
	timeOnlyOrderHost(fx, &observed, func(ctx context.Context, in project.FactoryLaunch) {
		a, err := db.AssignmentByRun(ctx, in.Run.ID)
		if err != nil || a.Phase != project.ExecutionDecompose || a.Subject.Intent == nil || a.Subject.Intent.ScopeDigest != head.Digest() {
			t.Fatalf("host call precedes its bound admission: assignment=%+v err=%v", a, err)
		}
		if !strings.Contains(string(a.Prompt), head.Digest()) {
			t.Fatal("staged prompt does not carry the bound head digest")
		}
		if a.PlanningContext == nil || a.PlanningContext.ValidateAssignment(a) != nil || a.PlanningContext.Content.Objective != head.Objective || len(a.PlanningContext.Inspected) != 1 {
			t.Fatal("packet lost its typed admitted planning input")
		}
		cap, err := db.GetMCPCapability(ctx, a.Run)
		if err != nil {
			t.Fatal(err)
		}
		current, err := db.FactoryCurrentContext(ctx, cap, time.Now())
		if err != nil || current.Role.Planning == nil || current.Role.Planning.Content.Objective != head.Objective || current.Role.Product == nil || current.Role.Product.ScopeDigest != head.Digest() {
			t.Fatalf("required planning context unavailable: %v", err)
		}
	})
	report := coord.DispatchIntentPlan(ctx, head.Intent, 7, factory.IntentPlanningPropose, in, strings.Repeat("c", 40), "")
	if len(report.Errors) != 0 || len(report.Waits) != 0 || len(report.Launched) != 1 {
		t.Fatalf("plan route report: %+v", report)
	}
	if !observed {
		t.Fatal("host double never ran")
	}
	run, err := db.FactoryRun(ctx, report.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "planning done", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	finished, ok := accountRunFinish(t, ctx, db, run, settleIntentOutput(t, head.Intent, head.ID), time.Now())
	if !ok || finished.Outcome != factory.Succeeded || finished.Result == nil || !finished.Result.Reported {
		t.Fatalf("bound answer unsettled: %+v %v", finished, ok)
	}
	decomposition := finished.Result.PhaseResult.Output.Decomposition
	if decomposition == nil || decomposition.Intent != head.Intent || decomposition.Revision != head.ID {
		t.Fatalf("typed result not bound to its inputs: %+v", finished.Result)
	}
	if again, ok := accountRunFinish(t, ctx, db, run, settleIntentOutput(t, head.Intent, head.ID), time.Now()); ok || again.Stage != "" {
		t.Fatalf("second accounting = %+v %v", again, ok)
	}
	t.Run("incomplete planning terminal precedes native retirement", func(t *testing.T) {
		serving, _ := dispatchTestDB(t)
		fixture := dispatchSeed(t, serving)
		fundTimeOnlyPolicy(t, ctx, serving, fixture.repo)
		intent := intentPlanHead(time.Now().UTC().Truncate(time.Second))
		if err := serving.SaveIntentRevision(ctx, intent); err != nil {
			t.Fatal(err)
		}
		worker := &Coordinator{
			Store: serving, Host: fixture.host, Broker: fixture.broker, DispatchReads: fixture.deps().Reads,
			MCPKey: []byte(strings.Repeat("k", 32)), Priority: &PriorityJudgments{Journal: serving, Chooser: &bundleGapChooser{pick: factory.ContextBundleCurrent}},
		}
		launched := worker.DispatchIntentPlan(ctx, intent.Intent, 7, factory.IntentPlanningPropose, intentPlanInput(t, intent, factory.IntentPlanningPropose), strings.Repeat("c", 40), "")
		if len(launched.Launched) != 1 || len(launched.Errors) != 0 || len(launched.Waits) != 0 {
			t.Fatalf("normal intent admission failed: %+v", launched)
		}
		run, err := serving.FactoryRun(ctx, launched.Launched[0].RunID)
		if err != nil {
			t.Fatal(err)
		}
		capability, err := serving.GetMCPCapability(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := serving.ActivateObservedMCPCapability(ctx, capability); err != nil {
			t.Fatal(err)
		}
		capability, err = serving.GetMCPCapability(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		submission := testMCPSubmission(capability, factory.MCPToolFinishIncomplete, factory.NewID(), []byte(`{"outcome":"blocked","cause":"Planning evidence is incomplete."}`))
		if _, err := serving.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatal(err)
		}
		if settled, err := SettleMCPTerminalProposal(ctx, serving, capability.AssignmentID, time.Now()); err != nil || !settled {
			t.Fatal("early intent terminal did not settle", settled, err)
		}
		scope := factory.IntentAttemptScope{IntentID: intent.Intent, OwnerID: intent.Owner}
		before, err := serving.IntentAttemptAllowance(ctx, fixture.repo, scope, capability.AssignmentID)
		if err != nil || !before.Closed || !before.Active {
			t.Fatal("early intent terminal must keep its actual native clock", before, err)
		}
		run.Outcome, run.Reconciled = factory.Succeeded, true
		if err := serving.SaveFactoryRun(ctx, run); err != nil {
			t.Fatal("retired intent native outcome failed to save", err)
		}
		after, err := serving.IntentAttemptAllowance(ctx, fixture.repo, scope, capability.AssignmentID)
		if err != nil || after.Active || !after.Closed || after.Limits != before.Limits || after.ActiveSeconds < before.ActiveSeconds {
			t.Fatal("retired intent did not checkpoint the same closed allowance", after, err)
		}
		finished, err := serving.Assignment(ctx, capability.AssignmentID)
		if err != nil || finished.Outcome != factory.NeedsHuman || finished.Result == nil || finished.Result.Status != "blocked" {
			t.Fatal("native retirement changed the recorded planning result", err)
		}
		if err := serving.SaveFactoryRun(ctx, run); err != nil {
			t.Fatal("native intent save replay failed", err)
		}
		replayed, err := serving.IntentAttemptAllowance(ctx, fixture.repo, scope, capability.AssignmentID)
		if err != nil || replayed.Revision != after.Revision || replayed.ActiveSeconds != after.ActiveSeconds {
			t.Fatal("native intent replay charged or reopened its root", replayed, err)
		}
	})
}

// TestDispatchIntentPlanWaitsWhenPreparationChanged proves the pinned
// preparation binding: dispatching against a preparation that is no
// longer the ready match waits instead of funding a run against stale
// input, leaving no launch, run or funding behind.
func TestDispatchIntentPlanWaitsWhenPreparationChanged(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	head := intentPlanHead(time.Now().UTC().Truncate(time.Second))
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatal(err)
	}
	in := intentPlanInput(t, head, factory.IntentPlanningPropose)
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.deps().Reads}
	var observed bool
	timeOnlyOrderHost(fx, &observed, func(context.Context, project.FactoryLaunch) {
		t.Fatal("changed preparation reached the host")
	})
	report := coord.DispatchIntentPlan(ctx, head.Intent, 7, factory.IntentPlanningPropose, in, strings.Repeat("c", 40), "f999999999999999999999999")
	if len(report.Errors) != 0 || len(report.Launched) != 0 || len(report.Waits) != 1 {
		t.Fatalf("changed preparation did not wait: %+v", report)
	}
	if observed {
		t.Fatal("changed preparation launched a host run")
	}
}

// TestDispatchIntentPlanRefusesUnreviewedHead proves the connector's
// guards: an unassociated draft launches nothing, and a wrong owner
// cannot read another owner's head.
func TestDispatchIntentPlanRefusesUnreviewedHead(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	loose := intentPlanHead(time.Now().UTC().Truncate(time.Second))
	loose.Repository = 0
	if err := db.SaveIntentRevision(ctx, loose); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.deps().Reads}
	in := intentPlanInput(t, loose, factory.IntentPlanningPropose)
	unassociated := coord.DispatchIntentPlan(ctx, loose.Intent, 7, factory.IntentPlanningPropose, in, strings.Repeat("c", 40), "")
	if len(unassociated.Errors) == 0 || len(unassociated.Launched) != 0 {
		t.Fatalf("unassociated draft launched: %+v", unassociated)
	}
	head := intentPlanHead(time.Now().UTC().Truncate(time.Second))
	head.ID, head.Intent = "d777777777777777777777777", "d666666666666666666666666"
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatal(err)
	}
	foreign := coord.DispatchIntentPlan(ctx, head.Intent, 9, factory.IntentPlanningPropose, in, strings.Repeat("c", 40), "")
	if len(foreign.Errors) == 0 || len(foreign.Launched) != 0 {
		t.Fatalf("wrong owner launched: %+v", foreign)
	}
	if len(fx.host.launches) != 0 {
		t.Fatalf("guarded connector reached the host: %d", len(fx.host.launches))
	}
}

// TestDispatchIntentPlanRefusesMismatchedInspectedSource proves the
// common boundary binds evidence to source: inspected evidence for
// another commit refuses before admission, leaving no launch, run or
// funding behind.
func TestDispatchIntentPlanRefusesMismatchedInspectedSource(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	head := intentPlanHead(time.Now().UTC().Truncate(time.Second))
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatal(err)
	}
	content, err := factory.ComposeProposalContent(head)
	if err != nil {
		t.Fatal(err)
	}
	in := factory.SemanticPlannerInput{
		Operation: factory.IntentPlanningPropose, Head: head, Content: content,
		Alternatives: factory.DescribeAlternatives(head.Objective),
		Inspected: []factory.InspectedSource{
			{Path: "internal/factory/intent.go", Commit: strings.Repeat("d", 40), Evidence: "evidence for another commit"},
		},
	}
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.deps().Reads}
	report := coord.DispatchIntentPlan(ctx, head.Intent, 7, factory.IntentPlanningPropose, in, strings.Repeat("c", 40), "")
	if len(report.Errors) == 0 || len(report.Launched) != 0 {
		t.Fatalf("mismatched source admitted: %+v", report)
	}
	if len(fx.host.launches) != 0 {
		t.Fatalf("mismatched source reached the host: %d", len(fx.host.launches))
	}
}

// TestSettledDecomposeRefusesUnrecordedIdeaBinding proves settlement
// validates the reported answer against its recorded staged input: a
// decomposition binding an idea the recorded head never carried
// converts to needs-human with no report, while the executed run's
// usage and consumed reservation stay recorded.
func TestSettledDecomposeRefusesUnrecordedIdeaBinding(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	now := time.Now().UTC().Truncate(time.Second)
	head := factory.IntentRevision{
		ID: "d888888888888888888888888", Intent: "d777777777777777777777777", Owner: 7, Repository: fx.repo,
		Objective: "Decide vacation requests with manager review", State: "draft",
		MaxIssues: 10, CreatedUnix: now.Unix(),
		Ideas: []factory.ProductIdea{
			{ID: "d333333333333333333333333", Description: "Capture leave requests", Consequence: "Requests can be recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
		},
	}
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatal(err)
	}
	content, err := factory.ComposeProposalContent(head)
	if err != nil {
		t.Fatal(err)
	}
	in := factory.SemanticPlannerInput{
		Operation: factory.IntentPlanningPropose, Head: head, Content: content,
		Alternatives: factory.DescribeAlternatives(head.Objective),
		Inspected: []factory.InspectedSource{
			{Path: "internal/factory/intent.go", Commit: strings.Repeat("c", 40), Evidence: "recorded investigation"},
		},
	}
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.deps().Reads}
	var observed bool
	timeOnlyOrderHost(fx, &observed, func(context.Context, project.FactoryLaunch) {})
	report := coord.DispatchIntentPlan(ctx, head.Intent, 7, factory.IntentPlanningPropose, in, strings.Repeat("c", 40), "")
	if len(report.Errors) != 0 || len(report.Launched) != 1 {
		t.Fatalf("plan route report: %+v", report)
	}
	run, err := db.FactoryRun(ctx, report.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "planning done", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	stranger := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "single choice ready",
		Output: &factory.PhaseOutput{Decomposition: &factory.SemanticPlannerOutput{
			Intent: head.Intent, Revision: head.ID, Operation: factory.IntentPlanningPropose,
			Candidates: []factory.SemanticCandidate{{
				Key: "keep", Rationale: "Keep the recorded draft as the single review baseline.",
				Tasks: []factory.IntentTask{{
					Key: "d999999999999999999999999", Objective: "Decide the rollout order",
					Outcomes: []string{"Rollout is ordered"}, IdeaIDs: []string{"d666666666666666666666666"},
				}},
			}},
		}},
	})
	finished, ok := accountRunFinish(t, ctx, db, run, stranger, time.Now())
	if !ok || finished.Outcome != factory.NeedsHuman || finished.Result == nil || finished.Result.Reported {
		t.Fatalf("unbound answer settled: %+v %v", finished, ok)
	}
}
