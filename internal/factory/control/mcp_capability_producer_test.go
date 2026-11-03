package control

// Capability preparation and replay use recorded assignment scope. Staging
// alone never activates tools: the authenticated API additionally observes the
// live native run and checks current work/stop state (mcp_prelaunch_test.go).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// producerTestDB opens one isolated ephemeral database and fails
// instead of skipping when the disposable fixture is absent.
func producerTestDB(t *testing.T) *store.Store {
	t.Helper()
	admin, ok := store.TestFixtureDSN()
	if !ok {
		t.Fatal("SODA_PG_* disposable fixture required: no skips on this lane")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, _, cleanup, err := store.OpenEphemeral(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return db
}

// producerDispatch runs one real dispatch pass and returns its
// launched assignment and run identities.
func producerDispatch(t *testing.T, ctx context.Context, db *store.Store) (factory.Assignment, factory.Run) {
	t.Helper()
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 1 {
		t.Fatalf("dispatch pass = %+v %+v, want one launch", report.Launched, report.Waits)
	}
	launched := report.Launched[0]
	assignment, err := db.Assignment(ctx, launched.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := db.FactoryRun(ctx, launched.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return assignment, run
}

// TestMCPCapabilityProducerStagesDispatchedHold proves the staged
// hold: the dispatched run records a staged capability bound to its
// scope, and the served handler gate refuses it inactive for both
// read and mutation tools, so zero submission rows can follow.
func TestMCPCapabilityProducerStagesDispatchedHold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db := producerTestDB(t)
	assignment, run := producerDispatch(t, ctx, db)

	capability, err := db.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatalf("produced capability unreadable: %v", err)
	}
	if capability.State != factory.MCPCapabilityStaged {
		t.Fatalf("produced capability state = %s, want staged", capability.State)
	}
	if capability.AssignmentID != assignment.ID || capability.RunID != run.ID ||
		capability.Generation != 1 || capability.Repository != assignment.Repository ||
		capability.Project != assignment.ProjectID {
		t.Fatalf("produced capability binds the wrong recorded scope: %+v", capability)
	}
	if err := capability.Validate(); err != nil {
		t.Fatalf("produced capability invalid: %v", err)
	}
	// Fresh dispatch investigates, so the recorded profile is the
	// architect: reads plus the issue-plan terminal.
	if capability.Profile != factory.MCPProfileArchitect {
		t.Fatalf("produced profile = %s, want architect", capability.Profile)
	}

	// The served gate refuses the staged scope inactive for read and
	// mutation tools alike: no admission, no durable row.
	now := time.Now()
	if err := AdmitMCPCapability(capability, factory.MCPToolStatus, now, MCPRunObservation{}); !errors.Is(err, ErrMCPCapabilityInactive) {
		t.Fatalf("staged status admission = %v, want inactive refusal", err)
	}
	if err := AdmitMCPCapability(capability, factory.MCPToolFinishIssuePlan, now, MCPRunObservation{}); !errors.Is(err, ErrMCPCapabilityInactive) {
		t.Fatalf("staged mutation admission = %v, want inactive refusal", err)
	}
	if count, err := db.MCPSubmissionCount(ctx, capability.AssignmentID); err != nil || count != 0 {
		t.Fatalf("submission count = %d err = %v, want zero durable rows", count, err)
	}
	if live, err := db.GetMCPCapability(ctx, capability.ID); err != nil || live.State != factory.MCPCapabilityStaged {
		t.Fatalf("capability after hold = %+v err = %v, want still staged", live, err)
	}
}

// TestMCPCapabilityProducerConflictAndReplay proves the
// store-backed guards the producer preserves: exact restart replay
// of identical staged bytes succeeds with no new authority, a
// concurrent conflicting second stage refuses on every contender,
// and stop intent stages retained history without activating.
func TestMCPCapabilityProducerConflictAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db := producerTestDB(t)
	assignment, run := producerDispatch(t, ctx, db)

	preparation, err := db.Preparation(ctx, assignment.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuildMCPCapability(assignment, run, preparation)
	if err != nil {
		t.Fatalf("rebuild from recorded state refused: %v", err)
	}
	produced, err := db.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if built.ScopeDigest() != produced.ScopeDigest() {
		t.Fatal("rebuilt scope differs from the dispatch-produced scope")
	}
	// Exact restart replay succeeds with no new authority: identical
	// staged bytes re-stage as a no-op.
	if err := StageMCPCapabilityForLaunch(ctx, db, built); err != nil {
		t.Fatalf("restart replay refused: %v", err)
	}

	// A concurrent second stage under the same identity with a
	// changed scope refuses on every contender.
	clash := built
	clash.PreparationDigest = strings.Repeat("b", 64)
	start := make(chan struct{})
	const contenders = 8
	errs := make([]error, contenders)
	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = db.StageMCPCapability(ctx, clash)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, store.ErrMCPCapabilityConflict) {
			t.Fatalf("contender %d stage = %v, want scope-conflict refusal", i, err)
		}
	}
	kept, err := db.GetMCPCapability(ctx, built.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.ScopeDigest() != built.ScopeDigest() || kept.State != factory.MCPCapabilityStaged {
		t.Fatalf("recorded row moved under conflict: %+v", kept)
	}

	// The full produce path on an open dispatch stages the retained
	// history and succeeds; the row stays staged and the served gate
	// keeps refusing it.
	if err := ProduceMCPCapabilityForLaunch(ctx, db, assignment, run); err != nil {
		t.Fatalf("open produce refused: %v", err)
	}
	if live, err := db.GetMCPCapability(ctx, built.ID); err != nil || live.State != factory.MCPCapabilityStaged {
		t.Fatalf("capability after produce = %+v err = %v, want still staged", live, err)
	}
	if err := AdmitMCPCapability(kept, factory.MCPToolFinishIssuePlan, time.Now(), MCPRunObservation{}); !errors.Is(err, ErrMCPCapabilityInactive) {
		t.Fatalf("mutation admission = %v, want inactive refusal", err)
	}
	if count, err := db.MCPSubmissionCount(ctx, built.AssignmentID); err != nil || count != 0 {
		t.Fatalf("submission count = %d err = %v, want zero durable rows", count, err)
	}
}

// TestMCPCapabilityProducerStopBarrierHold is the deterministic unit
// counterpart of the independent stop-during-activation race: a
// capability staged while stop intent is recorded stays inactive,
// every admission refuses, and zero submission rows follow.
func TestMCPCapabilityProducerStopBarrierHold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db := producerTestDB(t)
	assignment, run := producerDispatch(t, ctx, db)

	preparation, err := db.Preparation(ctx, assignment.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	built, err := BuildMCPCapability(assignment, run, preparation)
	if err != nil {
		t.Fatalf("rebuild from recorded state refused: %v", err)
	}
	if err := StageMCPCapabilityForLaunch(ctx, db, built); err != nil {
		t.Fatalf("stopped stage refused: %v", err)
	}
	held, err := db.GetMCPCapability(ctx, built.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held.State != factory.MCPCapabilityStaged {
		t.Fatalf("stopped capability state = %s, want staged", held.State)
	}
	now := time.Now()
	for _, tool := range []factory.MCPTool{factory.MCPToolStatus, factory.MCPToolFinishIssuePlan} {
		if err := AdmitMCPCapability(held, tool, now, MCPRunObservation{}); !errors.Is(err, ErrMCPCapabilityInactive) {
			t.Fatalf("stopped admission for %s = %v, want inactive refusal", tool, err)
		}
	}
	if count, err := db.MCPSubmissionCount(ctx, built.AssignmentID); err != nil || count != 0 {
		t.Fatalf("submission count = %d err = %v, want zero durable post-stop rows", count, err)
	}
}

// TestBuildMCPCapabilityRefusesUnrecorded proves the builder
// refuses rather than invents: no recorded run identity,
// preparation digest, source or deadline ever stages.
func TestBuildMCPCapabilityRefusesUnrecorded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db := producerTestDB(t)
	assignment, run := producerDispatch(t, ctx, db)
	preparation, err := db.Preparation(ctx, assignment.Preparation)
	if err != nil {
		t.Fatal(err)
	}

	badRun := run
	badRun.ID = "not-an-identity"
	if _, err := BuildMCPCapability(assignment, badRun, preparation); err == nil {
		t.Fatal("unrecorded run identity built")
	}
	badPrep := preparation
	badPrep.Preparation.SetupDigest = "short"
	if _, err := BuildMCPCapability(assignment, run, badPrep); err == nil {
		t.Fatal("unrecorded preparation digest built")
	}
	badSource := assignment
	badSource.SourceCommit = ""
	badInput := run
	badInput.InputSHA = ""
	if _, err := BuildMCPCapability(badSource, badInput, preparation); err == nil {
		t.Fatal("unsourced run built")
	}
	badDeadline := run
	badDeadline.Deadline = time.Time{}
	if _, err := BuildMCPCapability(assignment, badDeadline, preparation); err == nil {
		t.Fatal("deadlineless run built")
	}
}

// TestMCPProfileForPhase proves the exact canonical phase/profile
// mapping: every admitted execution phase maps once, and unknown,
// legacy, or pre-phase input carries no MCP authority — nothing
// defaults to Builder or Inspector by role.
func TestMCPProfileForPhase(t *testing.T) {
	cases := map[string]factory.MCPProfile{
		project.ExecutionDecompose:   factory.MCPProfileProductLead,
		project.ExecutionInvestigate: factory.MCPProfileArchitect,
		project.ExecutionImplement:   factory.MCPProfileBuilder,
		project.ExecutionReview:      factory.MCPProfileInspector,
		project.ExecutionCorrect:     factory.MCPProfileFixer,
		project.ExecutionReassess:    factory.MCPProfileStrategist,
		project.ExecutionRepair:      factory.MCPProfileStrategist,
		project.ExecutionVerify:      factory.MCPProfileTestPilot,
	}
	for phase, want := range cases {
		got, err := mcpProfileForPhase(phase)
		if err != nil {
			t.Fatalf("phase %q refused: %v", phase, err)
		}
		if got != want {
			t.Fatalf("phase %q maps to %s, want %s", phase, got, want)
		}
		if factory.MCPPhaseOf(want) == "" {
			t.Fatalf("mapped profile %s serves no phase", want)
		}
	}
	for _, phase := range []string{"", "legacy", "plan", "publish"} {
		if got, err := mcpProfileForPhase(phase); err == nil {
			t.Fatalf("phase %q produced %s, want no MCP authority", phase, got)
		}
	}
}
