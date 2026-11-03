package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// This file owns the FW10 acceptance for the D02 durable priority-inventory
// cursor (contracts D02, cases T08/T09 under FW-REASSESS-002). It replaces the
// FW04 P3 starvation reproduction: the cursor observes members once into a
// durable scan and resumes across passes within the production per-pass
// bounds, so the complete set reaches the existing tournament instead of
// starving past the 156-RPC readiness budget. The P3 reproduction itself is
// retained in history (commit 85c9ecb4), not here.
//
// The D02 algorithm under test is unchanged: observePriorityInventory,
// completePrioritySelection and dispatchOne admission stay separable so the
// FW31 completion-event/fence join can gate packet admission later. No
// reassessment gate is implemented or asserted here.
//
// Seeding follows the serving coordinator shape of
// serving_planning_priority_test.go (coordinator wiring through dispatchDeps,
// which carries the production observe/refresh joints) and the
// traversal_test.go header release (each acceptance emits one intake root
// capped at 64 sources, so the emitted header is forgotten through the
// disposable fixture handle, keeping the acceptance head and queued control).

// countingObserver scripts idle native revisions for the durable inventory
// and counts observations. A stable script models an idle native; an
// advancing script models a concurrent insertion between membership capture
// and digest confirmation. Counts bound the "no second full native scan"
// assertions: the legacy path spent one native RPC per member per pass.
type countingObserver struct {
	revisions []int64
	calls     int
}

func (o *countingObserver) ObserveNativeRevision(context.Context) (int64, bool, error) {
	o.calls++
	if len(o.revisions) == 0 {
		return 41, true, nil
	}
	if o.calls <= len(o.revisions) {
		return o.revisions[o.calls-1], true, nil
	}
	return o.revisions[len(o.revisions)-1], true, nil
}

// seedPriorityQueue admits and queues count ready issues starting at
// startIssue. Every member matches the stable observer revision, so
// observation itself spends no native budget unless a test overrides the
// refresh joint to simulate bounded per-member work.
func seedPriorityQueue(t *testing.T, fx dispatchFixture, fixtureDB *sql.DB, startIssue int64, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		issue := startIssue + int64(i)
		decision := fx.accept(t, issue, fmt.Sprintf("d%024x", 9000+int(issue)))
		if _, err := fixtureDB.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id=$1`, fmt.Sprintf("root:%d/%d", fx.repo, issue)); err != nil {
			t.Fatalf("remove emitted fixture header for issue %d: %v", issue, err)
		}
		fx.queue(t, issue, decision.ID)
	}
}

// bumpPriorityRevision moves one member's inputs and recorded control to a
// newer native revision without changing its semantic fingerprint, modelling
// a settled observation refresh. The digest must not move.
func bumpPriorityRevision(t *testing.T, fx dispatchFixture, issue, revision int64) {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("%d/%d", fx.repo, issue)
	in := fx.reads.inputs[key]
	in.Revision = revision
	fx.reads.inputs[key] = in
	control, err := fx.db.IssueControl(ctx, fx.repo, issue)
	if err != nil {
		t.Fatal(err)
	}
	control.NativeRev = revision
	if _, _, err := fx.db.RecordIssueAssessment(ctx, control, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// chooserCoverage unions every tournament candidate the chooser saw. The
// tournament groups at most 255 alternatives per call; coverage proves the
// complete set reached selection instead of being truncated to fit a pass.
func chooserCoverage(chooser *priorityFixture) map[string]bool {
	seen := map[string]bool{}
	for _, call := range chooser.calls {
		for _, c := range call.Candidates {
			seen[c.ID] = true
		}
	}
	return seen
}

func hasDispatchError(report DispatchReport, reason string) bool {
	for _, failure := range report.Errors {
		if failure.Reason == reason {
			return true
		}
	}
	return false
}

// holdLaunchedRuns keeps the fake host coherent across passes: launched runs
// stay in flight from recovery's view, so their assignments keep excluding
// their issues and later passes advance selection. Without it the fake
// inspect denies every launch, recovery retries the same assignment, and no
// multi-pass test could observe progress.
func holdLaunchedRuns(fx dispatchFixture) {
	fx.host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
		return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryRunning}, nil
	}
}

// A host source read can recover after a source-host repair without any
// native repository write or grant change. Exercise that actual empty-scan
// refusal/retry boundary through PostgreSQL and the production dispatcher.
func TestPriorityRetriesRecoveredNativeContextPG(t *testing.T) {
	db, dsn := dispatchTestDB(t)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	fx := dispatchSeed(t, db)
	if err := db.SaveCapacity(context.Background(), factory.Capacity{Revision: 1, UpdatedBy: 7, MaxConcurrentRuns: 1}); err != nil {
		t.Fatal(err)
	}
	holdLaunchedRuns(fx)
	seedPriorityQueue(t, fx, fixtureDB, 100, 1)
	c := NewCoordinator(db, fx.host, fx.broker)
	c.DispatchReads = fx.reads
	c.Readiness = &countingObserver{revisions: []int64{41}}
	chooser := &priorityFixture{choose: priorityLast}
	c.Priority = &PriorityJudgments{Journal: db, Chooser: chooser, MaxCalls: 16}
	fx.host.contextRead = func(project.FactoryPreparationContextRequest, string) (project.FactoryPreparationContext, error) {
		return project.FactoryPreparationContext{}, errors.New("native source read refused")
	}
	ctx := context.Background()
	first := DispatchPass(ctx, c.dispatchDeps())
	if len(first.Launched) != 0 || len(first.Errors) != 0 {
		t.Fatalf("source refusal admitted work: %+v", first)
	}
	if len(first.Waits) != 1 || first.Waits[0].Reason != WaitSource {
		t.Fatalf("native source refusal disappeared from dispatch report: %+v", first.Waits)
	}
	classified, err := c.ServeStallRound(ctx, first)
	if err != nil || len(classified.Repositories) != 1 || classified.Repositories[0].Kind != StallRepositoryZeroWait || classified.Repositories[0].Investigate {
		t.Fatalf("source wait became fresh ready work or repair spend: %+v %v", classified, err)
	}
	if _, err := db.LoadPriorityScan(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("transient empty scan remained frozen: %v", err)
	}
	fx.host.contextRead = nil
	second := DispatchPass(ctx, c.dispatchDeps())
	if len(second.Launched) != 1 || len(second.Errors) != 0 {
		t.Fatalf("recovered native read did not launch: %+v", second)
	}
	selectionCalls := len(chooser.calls)

	// An authenticated terminal can finish investigation before its native
	// unit retires. The held reservation is still real capacity, but its
	// exclusion cannot freeze the admitted implementation after retirement.
	owner, err := db.Assignment(ctx, second.Launched[0].AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := db.FactoryRun(ctx, owner.Run)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProduceMCPCapabilityForLaunch(ctx, db, owner, run); err != nil {
		t.Fatal(err)
	}
	capability, err := db.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateMCPCapability(ctx, capability.ID, capability.Generation, capability.RunID, capability.AssignmentID, capability.Authority, time.Now().Unix(), false); err != nil {
		t.Fatal(err)
	}
	capability, _ = db.GetMCPCapability(ctx, capability.ID)
	plan := phaseTestPlan(t, owner.AttemptRoot, owner.SourceCommit, owner.Preparation)
	payload, _ := json.Marshal(map[string]any{"approach": plan.Approach, "tasks": plan, "evidence_refs": []string{"README.md"}})
	proposal := testMCPSubmission(capability, factory.MCPToolFinishIssuePlan, factory.NewID(), payload)
	if _, err := db.RecordMCPSubmission(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	if settled, err := SettleMCPTerminalProposal(ctx, db, owner.ID, time.Now()); err != nil || !settled {
		t.Fatalf("early valid plan did not settle: %t %v", settled, err)
	}
	waiting := DispatchPass(ctx, c.dispatchDeps())
	if len(waiting.Launched) != 0 || waitReason(waiting, owner.Issue) != WaitCapacity {
		t.Fatalf("live native unit lost its capacity wait: %+v", waiting)
	}
	if scan, err := db.LoadPriorityScan(ctx); err != nil || scan.Observations[store.PrioritySubjectKey(owner.Repository, owner.Issue)].Exclusion != WaitCapacity {
		t.Fatalf("unchanged live capacity lost its cached wait: %+v %v", scan, err)
	}
	run.Outcome, run.Reconciled = factory.Succeeded, true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	advanced := DispatchPass(ctx, c.dispatchDeps())
	if len(advanced.Launched) != 1 || len(advanced.Errors) != 1 || advanced.Errors[0].Reason != PrioritySnapshotChanging {
		t.Fatalf("retired native unit did not report its implementation continuation: %+v", advanced)
	}
	child, err := db.Assignment(ctx, advanced.Launched[0].AssignmentID)
	if err != nil || child.Phase != project.ExecutionImplement || child.ParentAssignment != owner.ID || child.AttemptRoot != owner.AttemptRoot {
		t.Fatalf("accepted plan lineage changed at implementation: %+v %v", child, err)
	}
	if len(fx.host.launches) != 2 {
		t.Fatalf("continuation duplicated investigation or native launch: %d", len(fx.host.launches))
	}
	if len(chooser.calls) != selectionCalls {
		t.Fatalf("implementation continuation spent another next-work selection: %d", len(chooser.calls))
	}
}

// TestServingPriorityDispatchCompletesBeyondReadinessBudget flips the P3
// reproduction to bounded-progress completion: 170 unassigned ready issues
// complete their durable scan on every production-budget pass and reach
// selection. One pass observes the whole set with three native revision
// observations (open, confirm, post-judgment recheck) instead of one RPC per
// member, so no pass starves. The repository admits a single session, so the
// first pass launches exactly the selected investigation and later passes
// hold the rest behind repository_full backpressure instead of starving.
func TestServingPriorityDispatchCompletesBeyondReadinessBudget(t *testing.T) {
	db, dsn := dispatchTestDB(t)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureDB.Close() }()
	ctx := context.Background()
	fx := dispatchSeed(t, db)
	holdLaunchedRuns(fx)
	const issues = 170
	seedPriorityQueue(t, fx, fixtureDB, 100, issues)
	source := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
	observer := &countingObserver{revisions: []int64{41}}
	c := NewCoordinator(db, fx.host, fx.broker)
	c.AcceptanceReads, c.DispatchReads = source, fx.reads
	c.Readiness = observer
	chooser := &priorityFixture{choose: priorityLast}
	c.Priority = &PriorityJudgments{Journal: db, Chooser: chooser, MaxCalls: 16}
	launched := map[int64]bool{}
	for pass := 0; pass < 3; pass++ {
		passCtx, _, cancel := c.readinessPass(ctx)
		report := DispatchPass(passCtx, c.dispatchDeps())
		cancel()
		if len(report.Errors) != 0 {
			t.Fatalf("pass %d errors: %+v", pass, report.Errors)
		}
		for _, launch := range report.Launched {
			if launch.Issue < 100 || launch.Issue >= 100+issues {
				t.Fatalf("pass %d launched outside the seeded set: %+v", pass, launch)
			}
			if launched[launch.Issue] {
				t.Fatalf("pass %d relaunched issue %d", pass, launch.Issue)
			}
			launched[launch.Issue] = true
		}
	}
	if len(launched) != 1 {
		t.Fatalf("want exactly the single repository session launched, got %v", launched)
	}
	if len(chooser.calls) == 0 || len(chooser.calls[0].Candidates) != issues {
		got := -1
		if len(chooser.calls) > 0 {
			got = len(chooser.calls[0].Candidates)
		}
		t.Fatalf("first tournament saw %d candidates, want complete set of %d (no truncation)", got, issues)
	}
	for _, call := range chooser.calls {
		if len(call.Candidates) > 255 {
			t.Fatalf("tournament call exceeded its bound with %d candidates", len(call.Candidates))
		}
	}
	// Zero candidates needs no judgment: stable backpressure must not spend
	// further consultations.
	if len(chooser.calls) != 1 {
		t.Fatalf("stable backpressure spent %d consultations, want exactly the first selection", len(chooser.calls))
	}
	// Three passes at three observations each; the legacy path spent one
	// native RPC per member per pass (170 against the 156 cap).
	if observer.calls > 9 {
		t.Fatalf("passes re-scanned natively: %d revision observations", observer.calls)
	}
	// The retained scan documents durable backpressure, not starvation: the
	// launched issue is held aside and the rest wait on the repository
	// session, all observed in full.
	scan, err := db.LoadPriorityScan(ctx)
	if err != nil {
		t.Fatalf("backpressure scan was not retained: %v", err)
	}
	reasons := map[string]int{}
	for _, obs := range scan.Observations {
		if obs.Exclusion != "" {
			reasons[obs.Exclusion]++
		}
	}
	if reasons["already_assigned"] != 1 || reasons["repository_full"] != issues-1 || len(scan.Observations) != issues {
		t.Fatalf("unexpected scan outcome: observations=%d exclusions=%v", len(scan.Observations), reasons)
	}
}

// TestPriorityLargeQueueCompletesAndSurvivesRestart covers T08: 256
// candidates run under the actual per-pass limits, complete without a second
// full native scan, and survive a coordinator restart between passes. The
// retired scan after selection is the restart contract: a fresh coordinator
// starts a new scan instead of resuming a stale cursor.
func TestPriorityLargeQueueCompletesAndSurvivesRestart(t *testing.T) {
	db, dsn := dispatchTestDB(t)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureDB.Close() }()
	ctx := context.Background()
	fx := dispatchSeed(t, db)
	holdLaunchedRuns(fx)
	const issues = 256
	seedPriorityQueue(t, fx, fixtureDB, 100, issues)
	source := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
	observer := &countingObserver{revisions: []int64{41}}
	c := NewCoordinator(db, fx.host, fx.broker)
	c.AcceptanceReads, c.DispatchReads = source, fx.reads
	c.Readiness = observer
	chooser := &priorityFixture{choose: priorityLast}
	c.Priority = &PriorityJudgments{Journal: db, Chooser: chooser, MaxCalls: 16}
	passCtx, _, cancel := c.readinessPass(ctx)
	first := DispatchPass(passCtx, c.dispatchDeps())
	cancel()
	if len(first.Errors) != 0 {
		t.Fatalf("first pass errors: %+v", first.Errors)
	}
	if len(first.Launched) != 1 {
		t.Fatalf("first pass launched %d, want exactly the selected investigation: %+v", len(first.Launched), first)
	}
	if got := len(chooserCoverage(chooser)); got != issues {
		t.Fatalf("tournament covered %d of %d candidates", got, issues)
	}
	if _, err := db.LoadPriorityScan(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("completed scan was not retired, restart would resume a stale cursor: %v", err)
	}
	// Restart: a fresh coordinator on the same database holds the in-flight
	// repository session instead of duplicating it. The repository admits a
	// single session, so the restarted pass launches nothing further; it must
	// still complete its scan without errors and without re-electing the
	// launched issue.
	restarted := NewCoordinator(db, fx.host, fx.broker)
	restarted.AcceptanceReads, restarted.DispatchReads = source, fx.reads
	restarted.Readiness = observer
	nextChooser := &priorityFixture{choose: priorityLast}
	restarted.Priority = &PriorityJudgments{Journal: db, Chooser: nextChooser, MaxCalls: 16}
	passCtx, _, cancel = restarted.readinessPass(ctx)
	second := DispatchPass(passCtx, restarted.dispatchDeps())
	cancel()
	if len(second.Errors) != 0 {
		t.Fatalf("restarted pass errors: %+v", second.Errors)
	}
	if len(second.Launched) != 0 {
		t.Fatalf("restarted pass duplicated the held session: %+v", second.Launched)
	}
	scan, err := db.LoadPriorityScan(ctx)
	if err != nil {
		t.Fatalf("restarted pass left no scan: %v", err)
	}
	reasons := map[string]int{}
	for _, obs := range scan.Observations {
		if obs.Exclusion != "" {
			reasons[obs.Exclusion]++
		}
	}
	if reasons["already_assigned"] != 1 || reasons["repository_full"] != issues-1 || len(scan.Observations) != issues {
		t.Fatalf("restarted pass lost members: observations=%d exclusions=%v", len(scan.Observations), reasons)
	}
}

// TestPriorityPartialObservationResumesAcrossPasses covers the T08/T09
// resume path: when bounded per-member work exhausts the pass budget
// mid-scan, the pass persists its cursor and the next pass resumes instead
// of restarting or electing a subset. The scripted refresh joint stands in
// for members with large prerequisite sets consuming the pass budget; the
// cursor machinery under test is the production code.
func TestPriorityPartialObservationResumesAcrossPasses(t *testing.T) {
	db, dsn := dispatchTestDB(t)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureDB.Close() }()
	ctx := context.Background()
	fx := dispatchSeed(t, db)
	const issues = 5
	seedPriorityQueue(t, fx, fixtureDB, 100, issues)
	source := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
	observer := &countingObserver{revisions: []int64{41}}
	c := NewCoordinator(db, fx.host, fx.broker)
	c.AcceptanceReads, c.DispatchReads = source, fx.reads
	c.Readiness = observer
	chooser := &priorityFixture{choose: priorityLast}
	c.Priority = &PriorityJudgments{Journal: db, Chooser: chooser, MaxCalls: 16}
	deps := c.dispatchDeps()
	remaining := 2
	deps.RefreshCandidate = func(_ context.Context, control factory.IssueControl, _ int64) (factory.IssueControl, bool, error) {
		if remaining <= 0 {
			return factory.IssueControl{}, false, nil
		}
		remaining--
		return control, true, nil
	}
	passCtx, _, cancel := c.readinessPass(ctx)
	paused := DispatchPass(passCtx, deps)
	cancel()
	if len(paused.Launched) != 0 || len(paused.Errors) != 0 {
		t.Fatalf("paused pass should wait silently: %+v", paused)
	}
	scan, err := db.LoadPriorityScan(ctx)
	if err != nil {
		t.Fatalf("paused scan was not persisted: %v", err)
	}
	if scan.Cursor != 2 || len(scan.Observations) != 2 {
		t.Fatalf("paused scan did not persist its cursor: cursor=%d observations=%d", scan.Cursor, len(scan.Observations))
	}
	// The pause wrote observations only; membership and semantic inputs are
	// untouched, so the next pass must resume instead of restarting.
	again, err := db.CapturePriorityInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.LocalDigest != scan.LocalDigest {
		t.Fatal("paused pass changed the inventory it was supposed to resume")
	}
	remaining = 10
	passCtx, _, cancel = c.readinessPass(ctx)
	resumed := DispatchPass(passCtx, deps)
	cancel()
	if len(resumed.Errors) != 0 {
		t.Fatalf("resumed pass errors: %+v", resumed.Errors)
	}
	if len(resumed.Launched) != 1 {
		t.Fatalf("resumed pass did not complete selection: %+v", resumed)
	}
	if got := len(chooserCoverage(chooser)); got != issues {
		t.Fatalf("resumed tournament covered %d of %d candidates", got, issues)
	}
	if _, err := db.LoadPriorityScan(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("completed scan was not retired: %v", err)
	}
}

// TestPriorityInventoryInvalidation covers T09: an insertion between
// membership capture and digest confirmation restarts instead of
// disappearing; semantic input changes invalidate while timestamp-only
// touches do not; and a revision advance after judgment refuses admission.
func TestPriorityInventoryInvalidation(t *testing.T) {
	t.Run("insertion restarts without disappearing", func(t *testing.T) {
		db, dsn := dispatchTestDB(t)
		fixtureDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fixtureDB.Close() }()
		ctx := context.Background()
		fx := dispatchSeed(t, db)
		seedPriorityQueue(t, fx, fixtureDB, 100, 2)
		source := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
		observer := &countingObserver{revisions: []int64{41, 42}}
		c := NewCoordinator(db, fx.host, fx.broker)
		c.AcceptanceReads, c.DispatchReads = source, fx.reads
		c.Readiness = observer
		chooser := &priorityFixture{choose: priorityLast}
		c.Priority = &PriorityJudgments{Journal: db, Chooser: chooser, MaxCalls: 16}
		passCtx, _, cancel := c.readinessPass(ctx)
		aborted := DispatchPass(passCtx, c.dispatchDeps())
		cancel()
		if len(aborted.Launched) != 0 {
			t.Fatalf("aborted pass launched: %+v", aborted)
		}
		if !hasDispatchError(aborted, PrioritySnapshotChanging) {
			t.Fatalf("aborted pass did not report its restart: %+v", aborted)
		}
		if _, err := db.LoadPriorityScan(ctx); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("aborted pass saved a scan the insertion could disappear from: %v", err)
		}
		// The insertion lands and revisions settle; the next pass must
		// include it.
		decision := fx.accept(t, 102, fmt.Sprintf("d%024x", 9000+102))
		if _, err := fixtureDB.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id=$1`, fmt.Sprintf("root:%d/%d", fx.repo, 102)); err != nil {
			t.Fatal(err)
		}
		for _, issue := range []int64{100, 101} {
			bumpPriorityRevision(t, fx, issue, 42)
		}
		key := fmt.Sprintf("%d/%d", fx.repo, 102)
		in := fx.reads.inputs[key]
		in.Revision = 42
		fx.reads.inputs[key] = in
		fx.queue(t, 102, decision.ID)
		passCtx, _, cancel = c.readinessPass(ctx)
		settled := DispatchPass(passCtx, c.dispatchDeps())
		cancel()
		if len(settled.Errors) != 0 {
			t.Fatalf("settled pass errors: %+v", settled.Errors)
		}
		if len(settled.Launched) != 1 {
			t.Fatalf("settled pass did not select: %+v", settled)
		}
		if got := len(chooserCoverage(chooser)); got != 3 {
			t.Fatalf("settled tournament covered %d of 3 candidates; the insertion disappeared", got)
		}
	})

	t.Run("semantic changes invalidate, timestamps do not", func(t *testing.T) {
		db, dsn := dispatchTestDB(t)
		fixtureDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fixtureDB.Close() }()
		ctx := context.Background()
		fx := dispatchSeed(t, db)
		seedPriorityQueue(t, fx, fixtureDB, 100, 2)
		first, err := db.CapturePriorityInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Observation-only refresh: same fingerprint at a newer native
		// revision. Ticking clocks and refreshes must not invalidate.
		control, err := fx.db.IssueControl(ctx, fx.repo, 100)
		if err != nil {
			t.Fatal(err)
		}
		control.NativeRev = 42
		if _, changed, err := fx.db.RecordIssueAssessment(ctx, control, time.Now()); err != nil || changed {
			t.Fatalf("refresh recorded a semantic change: changed=%t err=%v", changed, err)
		}
		second, err := db.CapturePriorityInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if first.LocalDigest != second.LocalDigest {
			t.Fatal("timestamp-only refresh invalidated the inventory digest")
		}
		// Changed member evidence invalidates.
		control.Fingerprint = strings.Repeat("3", 64)
		if _, changed, err := fx.db.RecordIssueAssessment(ctx, control, time.Now()); err != nil || !changed {
			t.Fatalf("evidence change was not recorded: changed=%t err=%v", changed, err)
		}
		third, err := db.CapturePriorityInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if third.LocalDigest == second.LocalDigest {
			t.Fatal("changed member evidence did not invalidate the inventory digest")
		}
	})

	t.Run("post-judgment revision advance refuses admission", func(t *testing.T) {
		db, dsn := dispatchTestDB(t)
		fixtureDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fixtureDB.Close() }()
		ctx := context.Background()
		fx := dispatchSeed(t, db)
		seedPriorityQueue(t, fx, fixtureDB, 100, 2)
		source := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
		observer := &countingObserver{revisions: []int64{41, 41, 42}}
		c := NewCoordinator(db, fx.host, fx.broker)
		c.AcceptanceReads, c.DispatchReads = source, fx.reads
		c.Readiness = observer
		chooser := &priorityFixture{choose: priorityLast}
		c.Priority = &PriorityJudgments{Journal: db, Chooser: chooser, MaxCalls: 16}
		passCtx, _, cancel := c.readinessPass(ctx)
		report := DispatchPass(passCtx, c.dispatchDeps())
		cancel()
		// Judgment ran (no unavailable waits), but the winner's evidence no
		// longer matches N, so nothing may admit.
		if len(report.Launched) != 0 {
			t.Fatalf("stale selection admitted: %+v", report)
		}
		if len(report.Waits) != 0 {
			t.Fatalf("selection should have completed before the revision moved: %+v", report)
		}
		if !hasDispatchError(report, PrioritySnapshotChanging) {
			t.Fatalf("stale selection did not report its invalidation: %+v", report)
		}
		if len(chooser.calls) != 1 {
			t.Fatalf("judgment did not run exactly once before the recheck: %d calls", len(chooser.calls))
		}
		if _, err := db.LoadPriorityScan(ctx); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("invalidated scan was not dropped: %v", err)
		}
	})
}
