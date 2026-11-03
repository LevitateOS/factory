package control

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/factory/execution"

	sdk "forgejo.org/extension-sdk"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/forgejo"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// choicePlanIDs pins the intent, reviewed draft and accepted successor
// for the recorded-plan choice route.
var choicePlanIDs = struct {
	intent   string
	draft    string
	accepted string
}{"d100000000000000000000000", "d200000000000000000000000", "d300000000000000000000000"}

// choicePlanDraft is the reviewed draft behind the accepted revision:
// the options-supported objective with attributed effective scope.
func choicePlanDraft(repo int64) factory.IntentRevision {
	idea := func(id, desc, consequence string) factory.ProductIdea {
		return factory.ProductIdea{ID: id, Description: desc, Consequence: consequence, Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
	}
	return factory.IntentRevision{
		ID: choicePlanIDs.draft, Intent: choicePlanIDs.intent, Owner: 7, Repository: repo,
		Objective:         "Build a leave approval workflow so managers can review time-off requests",
		State:             "draft",
		MaxIssues:         10,
		PlanningCalls:     12,
		CreatedUnix:       1,
		EffectiveOutcomes: []string{"d555555555555555555555555", "d666666666666666666666666"},
		Ideas: []factory.ProductIdea{
			idea("d555555555555555555555555", "Capture leave requests", "Requests can be recorded"),
			idea("d666666666666666666666666", "Managers review requests", "Decisions are visible"),
		},
	}
}

// choicePlanAccepted carries the reviewed draft as its predecessor,
// preserving the reviewed substantive scope exactly: the objective,
// repository and product choices match the draft, while the authored
// tasks differ from any planner candidate so adoption proves the
// choice came from the recorded answer.
func choicePlanAccepted(t *testing.T, repo int64) factory.IntentRevision {
	t.Helper()
	r := broadJoinAcceptedRevision()
	r.ID, r.Intent, r.Repository, r.Predecessor = choicePlanIDs.accepted, choicePlanIDs.intent, repo, choicePlanIDs.draft
	r.Objective = choicePlanDraft(repo).Objective
	if err := r.Validate(); err != nil {
		t.Fatalf("accepted head invalid: %v", err)
	}
	return r
}

// choicePlanCoordinator wires funded comparative judgments behind the
// counting stub; the graph stays unwired unless the test drives the
// full generate tail.
func choicePlanCoordinator(t *testing.T, db *store.Store, fx dispatchFixture, stub ProductJudgment) *Coordinator {
	t.Helper()
	c := &Coordinator{Store: db, Host: fx.host}
	c.Planning = &PlanningJudgments{Chooser: stub}
	return c
}

func choicePlanAnswer(t *testing.T, draft factory.IntentRevision, status string, keys ...string) string {
	t.Helper()
	out := choicePlannerOutput(draft, keys...)
	return phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: status,
		Summary: "recorded choice ready", Output: &factory.PhaseOutput{Decomposition: &out},
	})
}

// A single recorded candidate resolves with zero comparative calls:
// the funded chooser never evaluates.
func TestResolveAcceptedPlanChoiceSingleNeedsNoComparativeCall(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait != nil {
		t.Fatalf("resolve err=%v wait=%+v", err, wait)
	}
	if chosen.Key != "cand-a" || chosen.Intent != choicePlanIDs.intent || chosen.Revision != choicePlanIDs.draft || stub.calls != 0 {
		t.Fatalf("choice=%+v fundedCalls=%d", chosen, stub.calls)
	}
}

// Multiple recorded alternatives resolve through exactly one funded
// comparative choice, and a second resolution replays cached without
// spending further.
func TestResolveAcceptedPlanChoiceMultiUsesOneFundedChoice(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a", "cand-b")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "cand-b", confidence: 0.9}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait != nil {
		t.Fatalf("resolve err=%v wait=%+v", err, wait)
	}
	if chosen.Key != "cand-b" || stub.calls != 1 {
		t.Fatalf("choice=%+v fundedCalls=%d", chosen, stub.calls)
	}
	again, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait != nil || again.Key != "cand-b" || stub.calls != 1 {
		t.Fatalf("replay choice=%+v err=%v wait=%+v fundedCalls=%d", again, err, wait, stub.calls)
	}
}

// A still-running recorded plan waits truthfully instead of refusing
// or funding a choice around the outstanding run.
func TestResolveAcceptedPlanChoicePendingWaits(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	launchPlanRun(t, ctx, db, fx, draft, factory.IntentPlanningOptions)
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait == nil {
		t.Fatalf("pending plan must wait: choice=%+v wait=%+v err=%v", chosen, wait, err)
	}
	if stub.calls != 0 {
		t.Fatalf("pending plan funded %d comparative calls", stub.calls)
	}
}

// An accepted direction whose reviewed draft never planned refuses
// without funding: there is no completed result to consume.
func TestResolveAcceptedPlanChoiceMissingRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if err := db.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	if _, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted); err == nil ||
		errors.Is(err, ErrPlanChoiceStale) || errors.Is(err, ErrPlanChoiceAttention) {
		t.Fatalf("missing plan must refuse plainly: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("missing plan funded %d comparative calls", stub.calls)
	}
}

// An accepted direction with no reviewed draft refuses: Generate never
// invents a plan the review never saw.
func TestResolveAcceptedPlanChoiceWithoutPredecessorRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	accepted := choicePlanAccepted(t, fx.repo)
	accepted.Predecessor = ""
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	if _, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted); err == nil {
		t.Fatal("predecessor-free acceptance admitted a plan choice")
	}
	if stub.calls != 0 {
		t.Fatalf("predecessor-free acceptance funded %d comparative calls", stub.calls)
	}
}

// A failed recorded run refuses with its recorded summary: failure is
// retried at the plan, never papered over by a choice.
func TestResolveAcceptedPlanChoiceFailedRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	run := launchPlanRun(t, ctx, db, fx, draft, factory.IntentPlanningOptions)
	run.Outcome, run.Summary, run.Reconciled = factory.Failed, "provider failed", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	finished, ok := AccountSettledRun(ctx, db, run, time.Now())
	if !ok || finished.Outcome != factory.Failed {
		t.Fatalf("failed run not charged: %+v %v", finished, ok)
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("failed plan must refuse: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("failed plan funded %d comparative calls", stub.calls)
	}
}

// A source move after settlement refuses as stale: the recorded plan
// inspects a commit the current preparation no longer carries.
func TestResolveAcceptedPlanChoiceStaleSourceRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	rotateChoicePlanSource(t, ctx, db, fx, strings.Repeat("d", 40))
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceStale) {
		t.Fatalf("moved source must refuse as stale: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("stale source funded %d comparative calls", stub.calls)
	}
}

// rotateChoicePlanSource admits a fresh requirement, approval and ready
// coder preparation on the given commit so the recorded plan goes stale.
func rotateChoicePlanSource(t *testing.T, ctx context.Context, db *store.Store, fx dispatchFixture, commit string) {
	t.Helper()
	digest := strings.Repeat("e", 64)
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{
		ID: "d444444444444444444444444", Project: fx.proj, Approver: 7,
		Predecessor:  "d111111111111111111111111",
		SourceCommit: commit, SetupDigest: digest, InputsDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{
		ID: "d555555555555555555555555", Project: fx.proj, Requirement: "d444444444444444444444444",
		Predecessor: "d222222222222222222222222",
		Approver:    7, EffectsDigest: digest, ReadinessDigest: digest, Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	prep := execution.PreparationRecord{Preparation: project.Preparation{
		ID: "f333333333333333333333333", Project: fx.proj, Role: project.RoleCoder,
		Requirements: project.RequirementAcceptance{ID: "d444444444444444444444444", Revision: 2, Approver: 7, SourceCommit: commit, Digest: digest},
		Approval:     project.AdminApproval{ID: "d555555555555555555555555", Revision: 2, Approver: 7, EffectsDigest: digest},
		SourceCommit: commit, SetupDigest: digest, Tools: []string{"python3"},
	}}
	prep.State = execution.PreparationState{Container: strings.Repeat("c", 64), SourceCommit: prep.Preparation.SourceCommit, SetupDigest: prep.Preparation.SetupDigest, ID: prep.Preparation.ID, Project: fx.proj, Role: project.RoleCoder, Phase: project.PrepareReady, Ready: true}
	if _, _, err := db.AdmitPreparation(ctx, prep); err != nil {
		t.Fatal(err)
	}
}

// rotatingGraphFixture rotates the recorded source once during the first
// external ownership read, proving late source movement between choice
// resolution and native submission refuses before any native effect.
type rotatingGraphFixture struct {
	forgejo.GraphCodec
	inner   *generatedGraphFixture
	rotated bool
	rotate  func()
}

func (f *rotatingGraphFixture) CheckIntentOwner(ctx context.Context, repo, owner, actor int64) (int64, error) {
	if !f.rotated {
		f.rotated = true
		f.rotate()
	}
	return f.inner.CheckIntentOwner(ctx, repo, owner, actor)
}

func (f *rotatingGraphFixture) SubmitGraph(ctx context.Context, g factory.IntentGeneration, repo int64) (factory.GraphOperationOutcome, error) {
	return f.inner.SubmitGraph(ctx, g, repo)
}

func (f *rotatingGraphFixture) LookupGraph(ctx context.Context, g factory.IntentGeneration, repo int64) (factory.GraphOperationLookup, error) {
	return f.inner.LookupGraph(ctx, g, repo)
}

func (f *rotatingGraphFixture) CancelGraph(ctx context.Context, id string) (factory.GraphOperationOutcome, error) {
	return f.inner.CancelGraph(ctx, id)
}

func (f *rotatingGraphFixture) ReadGeneratedIssue(ctx context.Context, repo, index, actor int64) (factory.GeneratedIssueObservation, int64, error) {
	return f.inner.ReadGeneratedIssue(ctx, repo, index, actor)
}

func (f *rotatingGraphFixture) ReadAcceptanceEvidence(ctx context.Context, revision, issue string, edges []string) (AcceptanceEvidence, error) {
	return f.inner.ReadAcceptanceEvidence(ctx, revision, issue, edges)
}

// A source rotation landing during the first external ownership read —
// after choice resolution but before native submission — refuses as
// stale with zero native submissions: the pre-submit boundary
// re-proves the stamped binding after the slow external reads.
func TestRecordedPlanSourceChangesBeforeNativeSubmit(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	inner := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	graph := &rotatingGraphFixture{inner: inner, rotate: func() {
		rotateChoicePlanSource(t, ctx, db, fx, strings.Repeat("d", 40))
	}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = graph, 91, graph
	_, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7)
	if err == nil || (!strings.Contains(err.Error(), "source") && !strings.Contains(err.Error(), "preparation")) {
		t.Fatalf("late source movement must refuse as stale: %v", err)
	}
	if inner.submissions != 0 {
		t.Fatalf("late source movement submitted %d native operations", inner.submissions)
	}
	if stub.calls != 0 {
		t.Fatalf("late source movement funded %d comparative calls", stub.calls)
	}
}

// A pre-binding pending operation behind a recorded plan resumes
// through recovery with its durable original binding stamped: the
// resumed submission validates the same recorded assignment and
// completes exactly once.
func TestRecoverIntentGraphsPreservesOriginalPendingPlan(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	inner := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	submitted := false
	inner.beforeSubmit = func(factory.IntentGeneration) error {
		if !submitted {
			submitted = true
			return errors.New("transient native failure")
		}
		return nil
	}
	c.Graph, c.GraphActorID, c.AcceptanceReads = inner, 91, inner
	if _, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7); err == nil {
		t.Fatal("expected transient native failure")
	}
	frozen, err := db.IntentGeneration(ctx, accepted.ID)
	if err != nil || frozen.State != "pending" || frozen.PlanAssignment == "" {
		t.Fatalf("legacy row not frozen pending and unbound: %+v %v", frozen, err)
	}
	if err := c.RecoverIntentGraphs(ctx); err != nil {
		t.Fatalf("recovery bind failed: %v", err)
	}
	resumed, err := db.IntentGeneration(ctx, accepted.ID)
	if err != nil || resumed.State != "completed" {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
	attempt, err := db.IntentPlanAttempt(ctx, choicePlanIDs.intent, choicePlanIDs.draft)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.PlanAssignment != attempt.ID {
		t.Fatalf("resumed binding=%q want recorded attempt %q", resumed.PlanAssignment, attempt.ID)
	}
	if inner.submissions != 1 {
		t.Fatalf("resumed submissions=%d want exactly one native effect", inner.submissions)
	}
}

// A pre-binding pending operation with no recorded plan behind its
// accepted head refuses through recovery instead of submitting: the
// unprovable operation is cancelled and reported, never observed.
func TestRecoverIntentGraphsRefusesUnboundPlanlessLegacy(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if err := db.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	inner := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	submitted := false
	inner.beforeSubmit = func(factory.IntentGeneration) error {
		if !submitted {
			submitted = true
			return errors.New("transient native failure")
		}
		return nil
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	c.Graph, c.GraphActorID, c.AcceptanceReads = inner, 91, inner
	seedUnboundGeneration(t, c, accepted)
	if err := c.RecoverIntentGraphs(ctx); err == nil || !strings.Contains(err.Error(), "no recorded plan binding") {
		t.Fatalf("unbound operation must refuse: %v", err)
	}
	if inner.submissions != 0 {
		t.Fatalf("plan-less legacy submitted %d native operations", inner.submissions)
	}
	if stub.calls != 0 {
		t.Fatalf("plan-less legacy funded %d comparative calls", stub.calls)
	}
	refused, err := db.IntentGeneration(ctx, accepted.ID)
	if err != nil || refused.State != "refused" {
		t.Fatalf("plan-less row=%+v err=%v want terminal refusal", refused, err)
	}
}

// Resolve through generate adopts exactly the recorded single-candidate
// graph with no comparative call: the bound connector runs the full
// serving tail behind the resolved choice.
func TestGenerateFromResolvedPlanChoiceAdoptsExactly(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	out := choicePlannerOutput(draft, "cand-a")
	proposal := &factory.VerificationManifestProposal{Outcomes: append([]string(nil), draft.EffectiveOutcomes...)}
	for _, outcome := range proposal.Outcomes {
		proposal.Checks = append(proposal.Checks, factory.ManifestCheckRef{Outcome: outcome, CatalogID: factory.ApprovedAppCheckID, Artifacts: []string{factory.ProjectCheckReceiptArtifact}, Fixtures: []string{factory.ProjectCheckPreparedFixture}})
	}
	out.VerificationManifestProposal = proposal
	answer := phaseFinish(t, factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed", Summary: "recorded graph and approved check proposal", Output: &factory.PhaseOutput{Decomposition: &out}})
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, answer); !ok {
		t.Fatal("recorded plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	preparations, err := db.StandingProjectPreparations(ctx, fx.proj)
	if err != nil {
		t.Fatal(err)
	}
	var p project.Preparation
	for _, preparation := range preparations {
		if preparation.Preparation.Role == project.RoleCoder {
			p = preparation.Preparation
			break
		}
	}
	if p.ID == "" {
		t.Fatal("standing coder preparation missing")
	}
	zero := 0
	fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) {
		return execution.PreparationState{Container: strings.Repeat("c", 64), ID: p.ID, Project: p.Project, Role: p.Role, SourceCommit: p.SourceCommit, SetupDigest: p.SetupDigest, Ready: true, Phase: project.PrepareReady, SetupExit: &zero, CheckExit: &zero}, nil
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	g, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7)
	if err != nil || wait != nil {
		t.Fatalf("generate err=%v wait=%+v", err, wait)
	}
	if g.State != "completed" || stub.calls != 0 {
		t.Fatalf("state=%s fundedCalls=%d", g.State, stub.calls)
	}
	if keys := submittedKeys(g.SubmittedTasks); len(keys) == 0 || keys[0] != choiceTaskKey(1) {
		t.Fatalf("submitted=%v want recorded candidate tasks", keys)
	}
	if g.VerificationManifest == nil || g.VerificationManifestDigest != g.VerificationManifest.Digest() || g.VerificationManifest.ValidateFor(accepted) != nil || g.VerificationManifest.Proposal.Digest() != proposal.Digest() || g.VerificationManifest.Catalog.CheckDigest != factory.MCPInputDigest([]byte("synthetic required checks")) {
		t.Fatalf("adopted graph lost its exact approved manifest: %+v", g.VerificationManifest)
	}
	storedGeneration, err := db.IntentGeneration(ctx, g.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(storedGeneration)
	replayed, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7)
	current, _ := json.Marshal(replayed)
	if err != nil || wait != nil || string(old) != string(current) {
		t.Fatalf("graph/manifest replay changed adoption: state=%s wait=%+v err=%v", replayed.State, wait, err)
	}
	changed := g
	manifest := *g.VerificationManifest
	manifest.Catalog.CheckDigest = strings.Repeat("f", 64)
	changed.VerificationManifest = &manifest
	changed.VerificationManifestDigest = manifest.Digest()
	if err := db.RecordIntentGeneration(ctx, changed, factory.GraphReceiptFacts{}); !errors.Is(err, store.ErrCommandConflict) {
		t.Fatalf("adopted catalog was replaceable: %v", err)
	}
	fx.host.contextRead = func(request project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		material := fixturePreparationContext(request, role)
		material.Check = []byte("changed protected check")
		return material, nil
	}
	if _, err := c.validateGenerationBinding(ctx, g); err == nil {
		t.Fatal("changed protected check retained native effect authority")
	}
	next := accepted
	next.ID, next.Predecessor = "d"+strings.Repeat("e", 24), accepted.ID
	if err := db.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	omitted := g
	omitted.RevisionID, omitted.IntentDigest, omitted.State, omitted.Receipt = next.ID, next.Digest(), "pending", nil
	omitted.OperationID = "intent-" + next.ID
	omitted.AuthRevision = next.ID + ":" + next.Digest()
	omitted.VerificationManifest, omitted.VerificationManifestDigest = nil, ""
	if err := db.BeginIntentGeneration(ctx, omitted, factory.GraphPacketFacts{Canonical: omitted.Payload}); err == nil || !strings.Contains(err.Error(), "omitted its prior adopted") {
		t.Fatalf("later graph silently dropped its adopted manifest: %v", err)
	}
}

// A revised objective after review refuses before any funded choice:
// changing leave approvals to publishing payroll is another product
// direction, not planning currency the connector may spend on.
func TestResolveAcceptedPlanChoiceRevisedObjectiveRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a", "cand-b")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	accepted.Objective = "Publish payroll figures for every department each month"
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-b", confidence: 0.9}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceStale) || !strings.Contains(err.Error(), "reviewed objective") {
		t.Fatalf("revised objective must refuse as stale: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("revised objective funded %d comparative calls", stub.calls)
	}
}

// A replacement preparation at the same commit still refuses: the
// recorded plan answers the exact preparation identity it settled,
// not merely any preparation carrying the same source commit.
func TestResolveAcceptedPlanChoiceReplacedPreparationRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	rotateChoicePlanSource(t, ctx, db, fx, strings.Repeat("c", 40))
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceStale) || !strings.Contains(err.Error(), "another preparation") {
		t.Fatalf("replaced preparation must refuse as stale: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("replaced preparation funded %d comparative calls", stub.calls)
	}
}

// A head that moves during the funded comparison refuses after it:
// the choice resolved against a superseded direction never reaches
// native effects.
func TestResolveAcceptedPlanChoiceMovedHeadDuringChoiceRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a", "cand-b")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	stub := &candidateChoiceStub{selected: "cand-b", confidence: 0.9}
	c := choicePlanCoordinator(t, db, fx, &movingChoiceStub{T: t, store: db, accepted: accepted, inner: stub})
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceStale) {
		t.Fatalf("moved head must refuse as stale: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("comparison ran %d times, want exactly the refused one", stub.calls)
	}
	if _, err := db.IntentGeneration(ctx, accepted.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("moved head recorded a generation: %v", err)
	}
}

// movingChoiceStub delegates the comparison to the counting stub, then
// supersedes the accepted head before returning the answer — proving
// the post-comparison currency recheck observes the move.
type movingChoiceStub struct {
	T        *testing.T
	store    *store.Store
	accepted factory.IntentRevision
	inner    *candidateChoiceStub
}

func (s *movingChoiceStub) Select(ctx context.Context, state any, rubric string, choices map[string]any) (factory.PriorityAnswer, error) {
	answer, err := s.inner.Select(ctx, state, rubric, choices)
	if err != nil {
		return answer, err
	}
	next := s.accepted
	next.ID = "d333333333333333333333333"
	next.Predecessor = s.accepted.ID
	if err := s.store.SaveIntentRevision(ctx, next); err != nil {
		s.T.Fatalf("superseding head not recorded: %v", err)
	}
	return answer, nil
}

// A genuine assessed proposal can supply the recorded graph for an authored
// objective outside the canned alternatives, under the same source/head guards.
func TestResolveAcceptedProposalAdoptsRecordedGraph(t *testing.T) {
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	draft.Objective = "Build a private team task board with saved priority"
	draft.Ideas = draft.Ideas[:1]
	draft.Ideas[0].Description, draft.Ideas[0].Consequence = "Store team tasks and priority", "Team task state persists"
	draft.EffectiveOutcomes = []string{draft.Ideas[0].ID}
	draft.MaxIssues = 1
	if factory.DescribeAlternatives(draft.Objective).Supported {
		t.Fatal("authored task-board scope must not acquire a canned workflow")
	}
	proposalWalkthroughs := choicePlanWalkthroughs(draft)
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "propose ready",
		Output: &factory.PhaseOutput{Decomposition: &factory.SemanticPlannerOutput{
			Intent: draft.Intent, Revision: draft.ID, Operation: factory.IntentPlanningPropose,
			Walkthroughs: &proposalWalkthroughs,
			Candidates: []factory.SemanticCandidate{{
				Key: "keep", Rationale: "Keep the recorded draft as the single review baseline.",
				Tasks: []factory.IntentTask{{
					Key: "d999999999999999999999999", Objective: draft.Objective,
					Outcomes: []string{"Team task state and priority persist"}, IdeaIDs: []string{draft.Ideas[0].ID},
				}},
			}},
		}},
	})
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningPropose, answer); !ok {
		t.Fatal("recorded propose plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "keep", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, proposalWalkthroughs)
	accepted := draft
	accepted.ID, accepted.Predecessor, accepted.State = choicePlanIDs.accepted, draft.ID, "accepted"
	accepted.Tasks = []factory.IntentTask{{Key: "d888888888888888888888888", Objective: draft.Objective, Outcomes: []string{"Team task state persists"}, IdeaIDs: []string{draft.Ideas[0].ID}}}
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	generated, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, draft.Intent, accepted.ID, draft.Owner)
	if err != nil || wait != nil || generated.State != "completed" || len(generated.SubmittedTasks) != 1 || generated.SubmittedTasks[0].Key != "d999999999999999999999999" {
		t.Fatalf("assessed proposal did not generate its exact recorded graph: %+v wait=%+v err=%v", generated, wait, err)
	}
	if stub.calls != 0 {
		t.Fatalf("propose answer funded %d comparative calls", stub.calls)
	}
	bindings, err := db.IntentTaskBindings(ctx, accepted.ID)
	if err != nil || len(bindings) != 1 || bindings[0].TaskKey != generated.SubmittedTasks[0].Key {
		t.Fatalf("recorded graph adoption is unbound: %+v %v", bindings, err)
	}
	if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16); err != nil || len(candidates) != 0 {
		t.Fatalf("unmerged generated graph admitted delivery: %+v %v", candidates, err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	binding := bindings[0]
	event := factory.CompletionEvent{ID: factory.NewID(), Repository: fx.repo, Issue: binding.IssueIndex, Generation: 1, PublicationID: factory.NewID(), AcceptanceID: binding.AcceptanceID, MergedCommit: fx.tip, ClosingOccurrence: "1"}
	raw, _ := json.Marshal(event)
	if _, err = sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES($1,1,$2,$3,'1',$4)`, fx.repo, event.ID, event.PublicationID, string(raw)); err != nil {
		t.Fatal(err)
	}
	candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16)
	if err != nil || len(candidates) != 1 || candidates[0].Revision.ID != accepted.ID || candidates[0].FirstIssue != binding.IssueIndex {
		t.Fatalf("merged recorded graph lost application verification: %+v %v", candidates, err)
	}
	for _, invalid := range []string{"pending", "stale-scope", "uncovered-outcome"} {
		changed := generated
		switch invalid {
		case "pending":
			changed.State = "pending"
		case "stale-scope":
			changed.IntentDigest = strings.Repeat("f", 64)
		case "uncovered-outcome":
			changed.SubmittedTasks = append([]factory.IntentTask(nil), generated.SubmittedTasks...)
			changed.SubmittedTasks[0].IdeaIDs = nil
		}
		raw, _ := json.Marshal(changed)
		if _, err := sqlDB.Exec(`UPDATE factory_intent_generations SET data=$2 WHERE revision=$1`, accepted.ID, string(raw)); err != nil {
			t.Fatal(err)
		}
		if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16); err != nil || len(candidates) != 0 {
			t.Fatalf("%s graph admitted application verification: %+v %v", invalid, candidates, err)
		}
	}
	raw, _ = json.Marshal(generated)
	if _, err := sqlDB.Exec(`UPDATE factory_intent_generations SET data=$2 WHERE revision=$1`, accepted.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	changedBinding := binding
	changedBinding.TaskKey = accepted.Tasks[0].Key
	raw, _ = json.Marshal(changedBinding)
	if _, err := sqlDB.Exec(`UPDATE factory_intent_tasks SET data=$3 WHERE revision=$1 AND task=$2`, accepted.ID, binding.TaskKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16); err != nil || len(candidates) != 0 {
		t.Fatalf("mismatched native task binding admitted application verification: %+v %v", candidates, err)
	}
}

// A recorded exclusion the acceptance drops refuses: flipping a
// reviewed accept to decline revises the reviewed product choice, so
// the recorded coverage no longer answers the accepted direction.
func TestResolveAcceptedPlanChoiceDroppedExclusionRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	accepted.Ideas[1].Decision = "decline"
	accepted.Tasks = []factory.IntentTask{{
		Key: choiceTaskKey(9), Objective: "Capture leave requests",
		Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d555555555555555555555555"},
	}}
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceStale) || !strings.Contains(err.Error(), "reviewed product choice") {
		t.Fatalf("dropped exclusion must refuse as stale: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("dropped exclusion funded %d comparative calls", stub.calls)
	}
}

// An acceptance that narrows the authorized outcomes refuses when the
// recorded candidates exceed them: coverage is proven before funding,
// not only at downstream graph validation.
func TestResolveAcceptedPlanChoiceNarrowedScopeRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	accepted.EffectiveOutcomes = []string{"d555555555555555555555555"}
	accepted.Tasks = []factory.IntentTask{{
		Key: choiceTaskKey(9), Objective: "Capture leave requests",
		Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d555555555555555555555555"},
	}}
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceStale) || !strings.Contains(err.Error(), "exceeds accepted scope") {
		t.Fatalf("narrowed scope must refuse as stale: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("narrowed scope funded %d comparative calls", stub.calls)
	}
}

// A recorded plan that never authored its walkthrough refuses before any
// funded choice or native effect: retained drafts from before walkthrough
// enforcement replan instead of building. The refusal funds nothing and
// records no generation.
func TestResolveAcceptedPlanChoiceWithoutWalkthroughRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	bare := choicePlannerOutput(draft, "cand-a")
	bare.Walkthroughs = nil
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "recorded choice ready", Output: &factory.PhaseOutput{Decomposition: &bare},
	})
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, answer); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	if _, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted); !errors.Is(err, ErrPlanChoiceReview) {
		t.Fatalf("walkthrough-less plan must refuse review: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("walkthrough-less plan funded %d comparative calls", stub.calls)
	}
	if _, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7); !errors.Is(err, ErrPlanChoiceReview) {
		t.Fatalf("walkthrough-less plan must refuse its build: %v", err)
	}
	if _, err := db.IntentGeneration(ctx, accepted.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("refused build recorded a generation: %v", err)
	}
}

// A recorded walkthrough nobody checked refuses before any funded choice:
// the person runs the explicit walkthrough check first, then native work
// may proceed. The replay attempt itself funds nothing.
func TestResolveAcceptedPlanChoiceWithoutReviewRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, choicePlanAnswer(t, draft, "completed", "cand-a")); !ok {
		t.Fatal("recorded plan did not settle")
	}
	accepted := choicePlanAccepted(t, fx.repo)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	_, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if !errors.Is(err, ErrPlanChoiceReview) || !strings.Contains(err.Error(), "no checked review") {
		t.Fatalf("unchecked walkthrough must refuse review: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("unchecked walkthrough funded %d calls", stub.calls)
	}
}

// The product-review bridge rejoins the actual recorded-plan generator. Native
// graph I/O and planner answers are explicit doubles; PostgreSQL, admission,
// lineage, adoption and replay are production owners.
func TestFinanceCommentReviewGeneratesOnlyNewWorkAndKeepsDeliveredHistory(t *testing.T) {
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	fx := dispatchSeed(t, db, map[string]string{"README.md": dispatchDigest("synthetic selected source: README.md")})
	initial := choicePlanDraft(fx.repo)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, initial, factory.IntentPlanningOptions, choicePlanAnswer(t, initial, "completed", "cand-a")); !ok {
		t.Fatal("first recorded plan did not settle")
	}
	c := choicePlanCoordinator(t, db, fx, &candidateChoiceStub{selected: "cand-a", confidence: 1})
	seedChoiceWalkthroughReview(t, ctx, c, initial, choicePlanWalkthroughs(initial))
	accepted := choicePlanAccepted(t, fx.repo)
	accepted.MaxIssues = 4
	accepted.EffectiveOutcomes = append([]string(nil), initial.EffectiveOutcomes...)
	accepted.Tasks = []factory.IntentTask{{Key: "d888888888888888888888888", Objective: "Deliver the reviewed workflow", Outcomes: []string{"Requests and approval history are retained"}, IdeaIDs: append([]string(nil), accepted.EffectiveOutcomes...)}}
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	complete := func(binding factory.IntentTaskBinding, generation int64) {
		t.Helper()
		event := factory.CompletionEvent{ID: factory.NewID(), Repository: fx.repo, Issue: binding.IssueIndex, Generation: generation, PublicationID: factory.NewID(), AcceptanceID: binding.AcceptanceID, MergedCommit: fx.tip, ClosingOccurrence: fmt.Sprint(generation)}
		raw, _ := json.Marshal(event)
		if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES($1,$2,$3,$4,$5,$6)`, fx.repo, generation, event.ID, event.PublicationID, event.ClosingOccurrence, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	first, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, accepted.Intent, accepted.ID, accepted.Owner)
	if err != nil || wait != nil || first.State != "completed" {
		t.Fatalf("first adoption: %+v %+v %v", first, wait, err)
	}
	oldBindings, err := c.Store.IntentTaskBindings(ctx, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, binding := range oldBindings {
		complete(binding, int64(i+1))
	}
	for _, native := range f.issues {
		native.Issue.IsClosed = true
	}
	before, _ := json.Marshal(f.issues)
	// Resolve the accepted decision post through the same native source reader
	// used by HTTP comments; no caller-supplied product identity is admitted.
	key := fmt.Sprintf("factory:%d:F11:intent-revision:%s", accepted.Repository, accepted.ID)
	sum := sha256.Sum256([]byte(key))
	post := hex.EncodeToString(sum[:16])
	source, err := c.Store.ResolvePostSource(ctx, accepted.Repository, factory.PostSourcePost, post)
	if err != nil || source.Product == nil {
		t.Fatalf("canonical product source: %+v %v", source, err)
	}
	comment, _, err := c.Store.SaveFeedPostWithSource(ctx, factory.PostRequest{Repository: accepted.Repository, Body: "Require Finance approval above EUR 1000 and retain existing requests.", SourceKind: source.Kind, SourceID: source.ID, IdempotencyKey: "finance-change"}, "43", "Robin", 2, source)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Store.RecordPostIntake(ctx, comment, 0); err != nil {
		t.Fatal(err)
	}
	pipe, err := c.Store.PostIntake(ctx, comment)
	if err != nil {
		t.Fatal(err)
	}
	routed := pipe
	routed.Judgment = factory.PostJudgmentApplied
	routed.Route = factory.PostRouteNewIssue
	if err = c.Store.AdvancePostIntake(ctx, pipe, routed); err != nil {
		t.Fatal(err)
	}
	draft, err := c.Store.ReviewPostProductChange(ctx, comment, 0, accepted.Owner, 3)
	if err != nil {
		t.Fatal(err)
	}
	if f.submissions != 1 {
		t.Fatal("review created detached native work")
	}
	baselineJSON, _ := json.Marshal(accepted.Tasks)
	generatedJSON, _ := json.Marshal(first.SubmittedTasks)
	draftJSON, _ := json.Marshal(draft.Tasks)
	if string(baselineJSON) == string(generatedJSON) || string(draftJSON) != string(generatedJSON) {
		t.Fatalf("change-review draft must carry the actual generated tasks, not the different accepted review baseline: baseline=%s generated=%s draft=%s", baselineJSON, generatedJSON, draftJSON)
	}
	// Mirror the actual UI's bounds-only Update proposal: retain attributed
	// ideas, effective outcomes and recorded task keys in a new draft before
	// planning. No exploration remints a baseline graph here.
	previousDraft := draft
	draft.ID = factory.PostDecisionID("finance-bounds", comment, 0)
	draft.Predecessor = previousDraft.ID
	draft.MaxIssues = len(draft.Tasks) + 1
	draft.CreatedUnix = 4
	if err = c.Store.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	input, _, planningWait, err := c.AssembleIntentPlanningInput(ctx, draft, factory.IntentPlanningOptions)
	if err != nil || planningWait != nil {
		t.Fatalf("actual successor planning input: wait=%+v err=%v", planningWait, err)
	}
	planningJSON, _ := json.Marshal(input.Head.Tasks)
	if string(planningJSON) != string(generatedJSON) || len(input.Head.EffectiveOutcomes) != len(accepted.EffectiveOutcomes)+1 {
		t.Fatal("native planning input omitted the retained generated task identities or Finance outcome")
	}
	idea := draft.Ideas[len(draft.Ideas)-1]
	tasks := append(append([]factory.IntentTask(nil), input.Head.Tasks...), factory.IntentTask{Key: "d777777777777777777777777", Objective: "Apply Finance approval above EUR 1000", Outcomes: []string{"EUR 1000 keeps manager approval; EUR 1001 requires Finance approval; existing requests persist"}, IdeaIDs: []string{idea.ID}, DependsOn: []string{input.Head.Tasks[len(input.Head.Tasks)-1].Key}})
	proposal := &factory.VerificationManifestProposal{Outcomes: append([]string(nil), draft.EffectiveOutcomes...)}
	for _, outcome := range proposal.Outcomes {
		proposal.Checks = append(proposal.Checks, factory.ManifestCheckRef{Outcome: outcome, CatalogID: factory.ApprovedAppCheckID, Artifacts: []string{factory.ProjectCheckReceiptArtifact}, Fixtures: []string{factory.ProjectCheckPreparedFixture}})
	}
	financeWalkthroughs := choicePlanWalkthroughs(draft)
	out := factory.SemanticPlannerOutput{Intent: draft.Intent, Revision: draft.ID, Operation: factory.IntentPlanningOptions, Walkthroughs: &financeWalkthroughs, Candidates: []factory.SemanticCandidate{{Key: "finance", Rationale: "Keep delivered tasks and add only the requested approval rule.", Tasks: tasks}}, VerificationManifestProposal: proposal}
	answer := phaseFinish(t, factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed", Summary: "Finance change planned against the retained product", Output: &factory.PhaseOutput{Decomposition: &out}})
	if _, ok := settlePlanAnswerWithOperation(t, ctx, c.Store, fx, draft, factory.IntentPlanningOptions, answer); !ok {
		t.Fatal("second recorded planner did not settle")
	}
	seedChoiceWalkthroughReview(t, ctx, c, draft, financeWalkthroughs)
	next := draft
	next.ID = factory.PostDecisionID("finance-accept", comment, 0)
	next.Predecessor = draft.ID
	next.State = "accepted"
	next.Tasks = tasks
	next.CreatedUnix = 4
	if err = c.Store.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	preparations, err := c.Store.StandingProjectPreparations(ctx, fx.proj)
	if err != nil {
		t.Fatal(err)
	}
	var prep project.Preparation
	for _, p := range preparations {
		if p.Preparation.Role == project.RoleCoder {
			prep = p.Preparation
		}
	}
	zero := 0
	fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) {
		return execution.PreparationState{Container: strings.Repeat("c", 64), ID: prep.ID, Project: prep.Project, Role: prep.Role, SourceCommit: prep.SourceCommit, SetupDigest: prep.SetupDigest, Ready: true, Phase: project.PrepareReady, SetupExit: &zero, CheckExit: &zero}, nil
	}
	// A delivered issue reopened after graph preparation must refuse adoption.
	// The first retained binding may already exist; restoring the native fact
	// exercises the exact replay path without submitting a second graph.
	f.beforeSubmit = func(factory.IntentGeneration) error {
		f.issues[2].Issue.IsClosed = false
		return nil
	}
	interrupted, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, next.Intent, next.ID, next.Owner)
	if err == nil || wait != nil || interrupted.State != "committed" || f.submissions != 2 {
		t.Fatalf("reopened delivered record retained authority: state=%s wait=%+v err=%v", interrupted.State, wait, err)
	}
	f.issues[2].Issue.IsClosed = true
	f.beforeSubmit = nil
	second, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, next.Intent, next.ID, next.Owner)
	if err != nil || wait != nil || second.State != "completed" {
		t.Fatalf("second native adoption: state=%s wait=%+v err=%v", second.State, wait, err)
	}
	var payload sdk.IssueGraphPayload
	if err = json.Unmarshal(second.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if f.submissions != 2 || len(payload.Creates) != 1 || payload.Creates[0].Key != tasks[len(tasks)-1].Key || len(payload.Edits) != 0 || len(second.Delivered) != len(first.SubmittedTasks) {
		t.Fatalf("delivered work duplicated or rewritten: submits=%d payload=%+v delivered=%+v", f.submissions, payload, second.Delivered)
	}
	retained := map[int64]sdk.NativeSnapshot{}
	for index := range f.issues {
		if index <= int64(len(first.SubmittedTasks)) {
			retained[index] = f.issues[index]
		}
	}
	after, _ := json.Marshal(retained)
	if string(before) != string(after) {
		t.Fatal("closed first-version native history changed")
	}
	bindings, err := c.Store.IntentTaskBindings(ctx, next.ID)
	if err != nil || len(bindings) != len(tasks) {
		t.Fatalf("new scope not completely adopted: %+v %v", bindings, err)
	}
	for _, old := range oldBindings {
		found := false
		for _, b := range bindings {
			if b.TaskKey == old.TaskKey {
				found = b.IssueID == old.IssueID && b.IssueIndex == old.IssueIndex && b.AcceptanceID == old.AcceptanceID
			}
		}
		if !found {
			t.Fatal("delivered native identity or acceptance pin lost", old)
		}
	}
	if second.VerificationManifest == nil || second.VerificationManifest.ValidateFor(next) != nil || second.VerificationManifest.Proposal.Digest() != proposal.Digest() {
		t.Fatal("new accepted scope lacks its exact application verification manifest")
	}
	if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16); err != nil || len(candidates) != 0 {
		t.Fatalf("new Finance work was considered delivered early: %+v %v", candidates, err)
	}
	for _, binding := range bindings {
		if binding.TaskKey == tasks[len(tasks)-1].Key {
			complete(binding, int64(len(oldBindings)+1))
		}
	}
	if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16); err != nil || len(candidates) != 1 || candidates[0].Revision.ID != next.ID {
		t.Fatalf("new completion did not admit exact v2 Test Pilot scope: %+v %v", candidates, err)
	}
	replay, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, next.Intent, next.ID, next.Owner)
	if err != nil || wait != nil || replay.OperationID != second.OperationID || f.submissions != 2 {
		t.Fatal("retry duplicated the change graph", err)
	}
	detail, err := c.Store.FeedPost(ctx, comment)
	if err != nil {
		t.Fatal(err)
	}
	view, err := c.Store.PostProductChange(ctx, detail, routed, next.Owner)
	if err != nil || view.State != "accepted" || view.AcceptedRevision != accepted.ID || view.DraftRevision != previousDraft.ID {
		t.Fatalf("comment lost exact adopted lineage: %+v %v", view, err)
	}
}

// With an explicit resolved subset the adoption consumer resolves the
// exact-subset candidate with zero comparative surprises and replays
// stably, while the wide candidate binding the decided idea outside the
// subset refuses assessment with its exact-scope error before any funded
// choice. No native graph is consulted: the proof runs on the recorded
// staged input and the single settled answer.
func TestResolveAcceptedPlanChoiceExactSubsetAdopts(t *testing.T) {
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	fx := dispatchSeed(t, db, map[string]string{"README.md": dispatchDigest("synthetic selected source: README.md")})
	draft := choicePlanDraft(fx.repo)
	draft.EffectiveOutcomes = []string{"d555555555555555555555555"}
	if err := db.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	in, _, planWait, err := c.AssembleIntentPlanningInput(ctx, draft, factory.IntentPlanningOptions)
	if err != nil || planWait != nil {
		t.Fatalf("exact-subset planning input: wait=%+v err=%v", planWait, err)
	}
	wide := choicePlannerOutput(draft, "cand-a")
	if err := factory.AssessSemanticOutput(in, wide); err == nil || !strings.Contains(err.Error(), "exceeds effective authorized outcomes") {
		t.Fatalf("outside-subset candidate must refuse with its exact-scope error: %v", err)
	}
	exact := choicePlannerOutput(draft, "cand-a")
	exact.Candidates = []factory.SemanticCandidate{{
		Key: "cand-a", Rationale: "Bind only the explicitly authorized outcome.",
		Tasks: []factory.IntentTask{{
			Key: choiceTaskKey(1), Objective: "Capture leave requests",
			Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d555555555555555555555555"},
		}},
	}}
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "exact subset ready", Output: &factory.PhaseOutput{Decomposition: &exact},
	})
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, answer); !ok {
		t.Fatal("exact-subset plan did not settle")
	}
	seedChoiceWalkthroughReview(t, ctx, c, draft, choicePlanWalkthroughs(draft))
	// The 391 positive case: the accepted successor canonically carries the
	// explicit subset with its tasks constrained to A — no absent subset,
	// no B content.
	accepted := choicePlanAccepted(t, fx.repo)
	accepted.EffectiveOutcomes = []string{"d555555555555555555555555"}
	accepted.Tasks = []factory.IntentTask{{
		Key: choiceTaskKey(1), Objective: "Capture leave requests",
		Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d555555555555555555555555"},
	}}
	if err := accepted.Validate(); err != nil {
		t.Fatalf("explicit-subset acceptance invalid: %v", err)
	}
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait != nil {
		t.Fatalf("resolve err=%v wait=%+v", err, wait)
	}
	if chosen.Key != "cand-a" || len(chosen.Tasks) != 1 || chosen.Tasks[0].Key != choiceTaskKey(1) ||
		len(chosen.Tasks[0].Outcomes) != 1 || chosen.Tasks[0].Outcomes[0] != "Requests can be recorded" ||
		len(chosen.Tasks[0].IdeaIDs) != 1 || chosen.Tasks[0].IdeaIDs[0] != "d555555555555555555555555" || stub.calls != 0 {
		t.Fatalf("choice=%+v fundedCalls=%d", chosen, stub.calls)
	}
	again, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait != nil || again.Key != chosen.Key || len(again.Tasks) != len(chosen.Tasks) || again.Tasks[0].Key != chosen.Tasks[0].Key || stub.calls != 0 {
		t.Fatalf("replay choice=%+v err=%v wait=%+v fundedCalls=%d", again, err, wait, stub.calls)
	}
	// Final adoption submits precisely the authorized outcome: the standing
	// coder preparation stays bound, the graph carries exactly the A task,
	// and a replayed adoption leaves the saved generation row bit-identical.
	preparations, err := db.StandingProjectPreparations(ctx, fx.proj)
	if err != nil {
		t.Fatal(err)
	}
	var p project.Preparation
	for _, preparation := range preparations {
		if preparation.Preparation.Role == project.RoleCoder {
			p = preparation.Preparation
			break
		}
	}
	if p.ID == "" {
		t.Fatal("standing coder preparation missing")
	}
	zero := 0
	fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) {
		return execution.PreparationState{Container: strings.Repeat("c", 64), ID: p.ID, Project: p.Project, Role: p.Role, SourceCommit: p.SourceCommit, SetupDigest: p.SetupDigest, Ready: true, Phase: project.PrepareReady, SetupExit: &zero, CheckExit: &zero}, nil
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	g, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7)
	if err != nil || wait != nil {
		t.Fatalf("generate err=%v wait=%+v", err, wait)
	}
	if g.State != "completed" || len(g.SubmittedTasks) != 1 || g.SubmittedTasks[0].Key != choiceTaskKey(1) ||
		len(g.SubmittedTasks[0].IdeaIDs) != 1 || g.SubmittedTasks[0].IdeaIDs[0] != "d555555555555555555555555" || stub.calls != 0 {
		t.Fatalf("adopted graph not precisely A: %+v fundedCalls=%d", g, stub.calls)
	}
	bindings, err := db.IntentTaskBindings(ctx, accepted.ID)
	if err != nil || len(bindings) != 1 || bindings[0].TaskKey != choiceTaskKey(1) {
		t.Fatalf("adopted graph binding not precisely A: %+v %v", bindings, err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var adoptedBefore []byte
	if err := sqlDB.QueryRow(`SELECT data FROM factory_intent_generations WHERE revision=$1`, accepted.ID).Scan(&adoptedBefore); err != nil {
		t.Fatal(err)
	}
	replayed, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, choicePlanIDs.intent, choicePlanIDs.accepted, 7)
	if err != nil || wait != nil || replayed.OperationID != g.OperationID {
		t.Fatalf("adoption replay diverged: state=%s wait=%+v err=%v", replayed.State, wait, err)
	}
	var adoptedAfter []byte
	if err := sqlDB.QueryRow(`SELECT data FROM factory_intent_generations WHERE revision=$1`, accepted.ID).Scan(&adoptedAfter); err != nil || string(adoptedAfter) != string(adoptedBefore) {
		t.Fatalf("adoption replay rewrote its saved generation bytes: %v", err)
	}
}

// persistedCompatDraft returns the reviewed draft behind the FJ-v3-591
// persisted proof: the choicePlanDraft choices with a byte-window
// multibyte objective (over 4000 bytes, under 4000 runes) so the
// released 586 byte rule substitutes Outcomes[0] while facts[0] stays
// byte-verbatim. IDs reuse the choicePlanIDs lane; each test owns an
// isolated PG fixture.
func persistedCompatDraft(repo int64) factory.IntentRevision {
	draft := choicePlanDraft(repo)
	signal := "Approve leave requests with a visible reviewer decision. "
	draft.Objective = signal + strings.Repeat("é", 2000)
	return draft
}

// persistedCompatWalkthroughs authors the persisted old-valid shape:
// PlanningProducer, verbatim facts[0], substituted Outcomes[0] under the
// canonical byte rule, IdeaIDs bound to the resolved scope. It mirrors
// the 586 in-memory construction through ComposeProposalContent so the
// persisted replay proves the same bytes.
func persistedCompatWalkthroughs(t *testing.T, draft factory.IntentRevision) factory.ProposalContent {
	t.Helper()
	basis, err := factory.ComposeProposalContent(draft)
	if err != nil {
		t.Fatalf("compat basis refused: %v", err)
	}
	if !basis.Supported || len(basis.Alternatives) != 2 || len(basis.Alternatives[0].Outcomes) == 0 {
		t.Fatal("compat head left supported coverage")
	}
	for _, alternative := range basis.Alternatives {
		if alternative.Outcomes[0] != "Implement the recorded product objective" {
			t.Fatalf("compat basis lost its byte-rule label: %q", alternative.Outcomes[0])
		}
	}
	content := factory.ProposalContent{
		Producer: factory.PlanningProducer, Supported: true,
		Intent: draft.Intent, Revision: draft.ID, Objective: draft.Objective,
		Facts: basis.Facts, Excluded: basis.Excluded, Exclusions: basis.Exclusions,
		Unknowns: basis.Unknowns,
	}
	for _, alternative := range basis.Alternatives {
		alternative.IdeaIDs = append([]string(nil), factory.ResolvedScope(draft)...)
		content.Alternatives = append(content.Alternatives, alternative)
	}
	if content.Facts[0] != draft.Objective || len(content.Facts[0]) != len(draft.Objective) {
		t.Fatal("compat walkthroughs lost their byte-verbatim facts")
	}
	if err := content.Validate(); err != nil {
		t.Fatalf("compat content refused: %v", err)
	}
	if err := factory.ValidatePlanningWalkthroughs(draft, content); err != nil {
		t.Fatalf("compat walkthrough binding refused: %v", err)
	}
	return content
}

// persistedCompatAnswer fences one single-candidate answer carrying the
// persisted walkthroughs with the recorded broad-join tasks.
func persistedCompatAnswer(t *testing.T, draft factory.IntentRevision, walkthroughs factory.ProposalContent, key string) string {
	t.Helper()
	tasks := append([]factory.IntentTask(nil), broadJoinAcceptedRevision().Tasks...)
	out := factory.SemanticPlannerOutput{Intent: draft.Intent, Revision: draft.ID, Operation: factory.IntentPlanningOptions, Walkthroughs: &walkthroughs}
	out.Candidates = append(out.Candidates, factory.SemanticCandidate{Key: key, Rationale: "Cohesive feature issues with independent roots joined once.", Tasks: tasks})
	return phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "recorded compat choice ready", Output: &factory.PhaseOutput{Decomposition: &out},
	})
}

// persistedCompatAccepted carries the byte-window draft as predecessor,
// preserving the reviewed substantive scope exactly like
// choicePlanAccepted but with the compat objective.
func persistedCompatAccepted(t *testing.T, repo int64, draft factory.IntentRevision) factory.IntentRevision {
	t.Helper()
	accepted := choicePlanAccepted(t, repo)
	accepted.Objective = draft.Objective
	if err := accepted.Validate(); err != nil {
		t.Fatalf("compat accepted head invalid: %v", err)
	}
	return accepted
}

// TestResolveAcceptedPlanChoicePersistedCompatByteWindow persists the
// FJ-v3-586 old-valid multibyte walkthrough and replays it through
// save → review → admitRecordedWalkthroughReview → adoption against an
// isolated PG fixture, asserting unchanged identity/digest end to end.
// Facts[0] stays byte-verbatim, Outcomes[0] stays substituted under the
// canonical byte rule; no digest/authority/history mutation, no
// normalization, no arbitrary-mismatch acceptance.
func TestResolveAcceptedPlanChoicePersistedCompatByteWindow(t *testing.T) {
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := persistedCompatDraft(fx.repo)
	if len(draft.Objective) <= 4000 || utf8.RuneCountInString(draft.Objective) >= 4000 {
		t.Fatalf("compat objective outside its byte window: %d bytes, %d runes", len(draft.Objective), utf8.RuneCountInString(draft.Objective))
	}
	if err := draft.Validate(); err != nil {
		t.Fatalf("compat draft invalid: %v", err)
	}
	compatDigest := draft.Digest()
	walkthroughs := persistedCompatWalkthroughs(t, draft)
	if walkthroughs.Facts[0] != draft.Objective || len(walkthroughs.Facts[0]) != len(draft.Objective) {
		t.Fatal("persisted walkthroughs lost byte-verbatim facts")
	}
	for _, alternative := range walkthroughs.Alternatives {
		if alternative.Outcomes[0] != "Implement the recorded product objective" {
			t.Fatalf("persisted walkthrough lost byte-rule label: %q", alternative.Outcomes[0])
		}
	}
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, persistedCompatAnswer(t, draft, walkthroughs, "cand-a")); !ok {
		t.Fatal("persisted compat plan did not settle")
	}
	attempt, err := db.IntentPlanAttempt(ctx, draft.Intent, draft.ID)
	if err != nil {
		t.Fatalf("persisted attempt unreadable: %v", err)
	}
	stored := attempt.Result.PhaseResult.Output.Decomposition
	if stored == nil || stored.Walkthroughs == nil {
		t.Fatal("persisted attempt lost its walkthroughs")
	}
	if stored.Walkthroughs.Facts[0] != draft.Objective || len(stored.Walkthroughs.Facts[0]) != len(draft.Objective) {
		t.Fatal("persisted walkthroughs changed facts through save")
	}
	for _, alternative := range stored.Walkthroughs.Alternatives {
		if alternative.Outcomes[0] != "Implement the recorded product objective" {
			t.Fatalf("persisted walkthrough changed its label through save: %q", alternative.Outcomes[0])
		}
	}
	if stored.Intent != draft.Intent || stored.Revision != draft.ID {
		t.Fatal("persisted walkthrough changed identity through save")
	}
	if err := factory.ValidatePlanningWalkthroughs(draft, *stored.Walkthroughs); err != nil {
		t.Fatalf("persisted walkthrough refused acceptance after save: %v", err)
	}
	proposal, err := factory.ConsolidateContent(*stored.Walkthroughs, stored.Walkthroughs.Alternatives[0].Key, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("persisted compat consolidation refused: %v", err)
	}
	if proposal.Outcomes[0] != "Implement the recorded product objective" {
		t.Fatalf("persisted compat proposal lost its label: %q", proposal.Outcomes[0])
	}
	if mismatches := factory.CheckProposalEffects(*stored.Walkthroughs, proposal); len(mismatches) != 0 {
		t.Fatalf("persisted compat walkthrough emitted mismatches: %+v", mismatches)
	}
	if draft.Digest() != compatDigest {
		t.Fatal("acceptance mutated identity or digest")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, walkthroughs)
	accepted := persistedCompatAccepted(t, fx.repo, draft)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Digest() == compatDigest {
		t.Fatal("accepted head unexpectedly kept its draft digest")
	}
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if err != nil || wait != nil {
		t.Fatalf("persisted compat resolve err=%v wait=%+v", err, wait)
	}
	if chosen.Key != "cand-a" || chosen.Intent != draft.Intent || chosen.Revision != draft.ID || stub.calls != 0 {
		t.Fatalf("persisted compat choice=%+v fundedCalls=%d", chosen, stub.calls)
	}
	preparations, err := db.StandingProjectPreparations(ctx, fx.proj)
	if err != nil {
		t.Fatal(err)
	}
	var prep project.Preparation
	for _, preparation := range preparations {
		if preparation.Preparation.Role == project.RoleCoder {
			prep = preparation.Preparation
			break
		}
	}
	if prep.ID == "" {
		t.Fatal("standing coder preparation missing")
	}
	zero := 0
	fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) {
		return execution.PreparationState{Container: strings.Repeat("c", 64), ID: prep.ID, Project: prep.Project, Role: prep.Role, SourceCommit: prep.SourceCommit, SetupDigest: prep.SetupDigest, Ready: true, Phase: project.PrepareReady, SetupExit: &zero, CheckExit: &zero}, nil
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	g, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, draft.Intent, accepted.ID, 7)
	if err != nil || wait != nil {
		t.Fatalf("persisted compat generate err=%v wait=%+v", err, wait)
	}
	if g.State != "completed" || g.RevisionID != accepted.ID || g.IntentID != draft.Intent || g.IntentDigest != accepted.Digest() || stub.calls != 0 {
		t.Fatalf("persisted adoption lost identity or digest: %+v fundedCalls=%d", g, stub.calls)
	}
	if g.PlanAssignment == "" {
		t.Fatal("persisted adoption lost its recorded plan binding")
	}
	if keys := submittedKeys(g.SubmittedTasks); len(keys) == 0 || keys[0] != choiceTaskKey(1) {
		t.Fatalf("persisted submitted=%v want recorded candidate tasks", keys)
	}
	bindings, err := db.IntentTaskBindings(ctx, accepted.ID)
	if err != nil || len(bindings) == 0 {
		t.Fatalf("persisted adoption is unbound: %+v %v", bindings, err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	var adoptedBefore []byte
	if err := sqlDB.QueryRow(`SELECT data FROM factory_intent_generations WHERE revision=$1`, accepted.ID).Scan(&adoptedBefore); err != nil {
		t.Fatal(err)
	}
	replayed, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, draft.Intent, accepted.ID, 7)
	if err != nil || wait != nil || replayed.OperationID != g.OperationID || f.submissions != 1 {
		t.Fatalf("persisted adoption replay diverged: state=%s wait=%+v err=%v submits=%d", replayed.State, wait, err, f.submissions)
	}
	var adoptedAfter []byte
	if err := sqlDB.QueryRow(`SELECT data FROM factory_intent_generations WHERE revision=$1`, accepted.ID).Scan(&adoptedAfter); err != nil || string(adoptedAfter) != string(adoptedBefore) {
		t.Fatalf("persisted adoption replay rewrote its saved generation bytes: %v", err)
	}
	if replayed.IntentDigest != accepted.Digest() || replayed.RevisionID != accepted.ID {
		t.Fatal("persisted replay changed identity or digest")
	}
}

// TestResolveAcceptedPlanChoicePersistedCompatMismatchRefuses proves the
// same persisted harness refuses an unrelated substituted-outcome
// mismatch with the exact released messages and writes nothing durable.
// The head is persisted and reloaded; validator, comparator and admit
// probes all run against that reloaded identity with byte-verbatim facts.
func TestResolveAcceptedPlanChoicePersistedCompatMismatchRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := persistedCompatDraft(fx.repo)
	if err := db.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatalf("compat head not persisted: %v", err)
	}
	reloaded, err := db.IntentRevision(ctx, draft.Intent, 7)
	if err != nil {
		t.Fatalf("persisted head unreadable: %v", err)
	}
	if reloaded.Digest() != draft.Digest() || reloaded.Objective != draft.Objective {
		t.Fatal("persisted head changed identity or digest through save")
	}
	valid := persistedCompatWalkthroughs(t, reloaded)
	unrelated := valid
	unrelated.Alternatives = append([]factory.ProductAlternative(nil), valid.Alternatives...)
	unrelated.Alternatives[0].Outcomes = append([]string{"An unrelated invented outcome"}, unrelated.Alternatives[0].Outcomes[1:]...)
	verbatim := valid
	verbatim.Alternatives = nil
	for _, alternative := range valid.Alternatives {
		alternative.Outcomes = append([]string{reloaded.Objective}, alternative.Outcomes[1:]...)
		verbatim.Alternatives = append(verbatim.Alternatives, alternative)
	}
	if err := factory.ValidatePlanningWalkthroughs(reloaded, unrelated); err == nil ||
		err.Error() != "planning walkthrough needs distinct alternatives preserving the objective" {
		t.Fatalf("unrelated mismatch misreported: %v", err)
	}
	if err := factory.ValidatePlanningWalkthroughs(reloaded, verbatim); err == nil ||
		err.Error() != "planning walkthrough needs distinct alternatives preserving the objective" {
		t.Fatalf("verbatim long Outcomes[0] misreported: %v", err)
	}
	unrelatedProposal, err := factory.ConsolidateContent(factory.ProposalContent{
		Producer: unrelated.Producer, Supported: unrelated.Supported,
		Intent: unrelated.Intent, Revision: unrelated.Revision, Objective: unrelated.Objective,
		Facts: unrelated.Facts, Excluded: unrelated.Excluded, Exclusions: unrelated.Exclusions,
		Unknowns:     unrelated.Unknowns,
		Alternatives: append([]factory.ProductAlternative{unrelated.Alternatives[0]}, valid.Alternatives[1:]...),
	}, unrelated.Alternatives[0].Key, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("unrelated consolidation refused: %v", err)
	}
	mismatches := factory.CheckProposalEffects(valid, unrelatedProposal)
	if len(mismatches) != 1 || mismatches[0].Class != factory.MismatchOutcomeGap ||
		mismatches[0].Detail != "The first outcome does not preserve the requested objective." {
		t.Fatalf("unrelated mismatch misreported: %+v", mismatches)
	}
	if valid.Alternatives[0].Outcomes[0] != "Implement the recorded product objective" {
		t.Fatal("refusal probe mutated the recorded walkthrough")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	tasks := append([]factory.IntentTask(nil), broadJoinAcceptedRevision().Tasks...)
	unrelatedOut := factory.SemanticPlannerOutput{Intent: reloaded.Intent, Revision: reloaded.ID, Operation: factory.IntentPlanningOptions, Walkthroughs: &unrelated}
	unrelatedOut.Candidates = append(unrelatedOut.Candidates, factory.SemanticCandidate{Key: "cand-a", Rationale: "Cohesive feature issues with independent roots joined once.", Tasks: tasks})
	if err := c.admitRecordedWalkthroughReview(ctx, reloaded, unrelatedOut); !errors.Is(err, ErrPlanChoiceReview) ||
		!strings.Contains(err.Error(), "planning walkthrough needs distinct alternatives preserving the objective") {
		t.Fatalf("unrelated admit misreported: %v", err)
	}
	verbatimOut := factory.SemanticPlannerOutput{Intent: reloaded.Intent, Revision: reloaded.ID, Operation: factory.IntentPlanningOptions, Walkthroughs: &verbatim}
	verbatimOut.Candidates = append(verbatimOut.Candidates, factory.SemanticCandidate{Key: "cand-a", Rationale: "Cohesive feature issues with independent roots joined once.", Tasks: tasks})
	if err := c.admitRecordedWalkthroughReview(ctx, reloaded, verbatimOut); !errors.Is(err, ErrPlanChoiceReview) ||
		!strings.Contains(err.Error(), "planning walkthrough needs distinct alternatives preserving the objective") {
		t.Fatalf("verbatim admit misreported: %v", err)
	}
	accepted := persistedCompatAccepted(t, fx.repo, reloaded)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted); err == nil {
		t.Fatal("unrelated mismatch admitted a plan choice")
	}
	if stub.calls != 0 {
		t.Fatalf("unrelated mismatch funded %d comparative calls", stub.calls)
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	if _, _, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, draft.Intent, accepted.ID, 7); err == nil {
		t.Fatal("unrelated mismatch admitted native adoption")
	}
	if f.submissions != 0 {
		t.Fatalf("unrelated mismatch submitted %d native operations", f.submissions)
	}
	if _, err := db.IntentGeneration(ctx, accepted.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unrelated mismatch recorded a generation: %v", err)
	}
}

// TestResolveAcceptedPlanChoicePersistedMalformedSettleRefuses closes the
// FJ-v3-599 gap on the malformed-attempt side: a genuinely malformed
// planning attempt (unrelated substituted Outcomes[0] bound to the real
// saved compat head/revision) is persisted through the real admission
// path. The settle boundary refuses it into a blocked needs-human attempt
// carrying the exact validator message with no decomposition, and the
// save → review → admit → resolve/generate path then refuses with the
// exact attention surface — never ErrPlanChoiceReview, never a funded
// choice, never a native submission, never a generation row.
func TestResolveAcceptedPlanChoicePersistedMalformedSettleRefuses(t *testing.T) {
	const mismatchDetail = "planning walkthrough needs distinct alternatives preserving the objective"
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := persistedCompatDraft(fx.repo)
	valid := persistedCompatWalkthroughs(t, draft)
	unrelated := valid
	unrelated.Alternatives = append([]factory.ProductAlternative(nil), valid.Alternatives...)
	unrelated.Alternatives[0].Outcomes = append([]string{"An unrelated invented outcome"}, unrelated.Alternatives[0].Outcomes[1:]...)
	tasks := append([]factory.IntentTask(nil), broadJoinAcceptedRevision().Tasks...)
	out := factory.SemanticPlannerOutput{Intent: draft.Intent, Revision: draft.ID, Operation: factory.IntentPlanningOptions, Walkthroughs: &unrelated}
	out.Candidates = append(out.Candidates, factory.SemanticCandidate{Key: "cand-a", Rationale: "Cohesive feature issues with independent roots joined once.", Tasks: tasks})
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "malformed compat choice", Output: &factory.PhaseOutput{Decomposition: &out},
	})
	finished, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, answer)
	if !ok {
		t.Fatal("malformed settle left no finished assignment")
	}
	if finished.Outcome != factory.NeedsHuman || finished.Reason != factory.AssignReasonNoReport {
		t.Fatalf("malformed settle outcome=%q reason=%q, want blocked needs-human", finished.Outcome, finished.Reason)
	}
	if finished.Result == nil || finished.Result.Reported {
		t.Fatalf("malformed settle reported a decomposition: %+v", finished.Result)
	}
	phased := finished.Result.PhaseResult
	if phased == nil || phased.Status != "blocked" || !strings.Contains(phased.Summary, mismatchDetail) {
		t.Fatalf("malformed settle status=%+v, want blocked with the exact validator message", phased)
	}
	if phased.Output != nil && phased.Output.Decomposition != nil {
		t.Fatalf("malformed settle kept a decomposition: %+v", phased.Output.Decomposition)
	}
	attempt, err := db.IntentPlanAttempt(ctx, draft.Intent, draft.ID)
	if err != nil {
		t.Fatalf("malformed attempt unreadable: %v", err)
	}
	if attempt.Outcome != factory.NeedsHuman || attempt.Result == nil || attempt.Result.Reported {
		t.Fatalf("malformed attempt not persisted blocked: %+v", attempt)
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedChoiceWalkthroughReview(t, ctx, c, draft, valid)
	accepted := persistedCompatAccepted(t, fx.repo, draft)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	_, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if wait != nil || !errors.Is(err, ErrPlanChoiceAttention) || !strings.Contains(err.Error(), "needs a product decision first") {
		t.Fatalf("malformed resolve surface: wait=%+v err=%v", wait, err)
	}
	if errors.Is(err, ErrPlanChoiceReview) {
		t.Fatalf("malformed resolve misreported as review: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("malformed resolve funded %d comparative calls", stub.calls)
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	if _, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, draft.Intent, accepted.ID, 7); wait != nil || !errors.Is(err, ErrPlanChoiceAttention) || !strings.Contains(err.Error(), "needs a product decision first") {
		t.Fatalf("malformed generate surface: wait=%+v err=%v", wait, err)
	}
	if f.submissions != 0 {
		t.Fatalf("malformed generate submitted %d native operations", f.submissions)
	}
	if stub.calls != 0 {
		t.Fatalf("malformed generate funded %d comparative calls", stub.calls)
	}
	if _, err := db.IntentGeneration(ctx, accepted.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("malformed generate recorded a generation: %v", err)
	}
}

// mismatchVerdictStub records one funded outcome-gap consistency verdict: it
// stands in for a JEV mismatch finding the way reviewSeedingStub stands in
// for a clean one, so the recorded review genuinely carries mismatches
// through the production JudgeConsistency path.
type mismatchVerdictStub struct{}

func (mismatchVerdictStub) Select(_ context.Context, _ any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	selected := "outcome_gap"
	if _, ok := choices[selected]; !ok {
		selected = "consistent"
	}
	probabilities := map[string]float64{}
	rest := 0.0
	if len(choices) > 1 {
		rest = 0.1 / float64(len(choices)-1)
	}
	sum := 0.0
	for key := range choices {
		if key == selected {
			continue
		}
		probabilities[key] = rest
		sum += rest
	}
	probabilities[selected] = 1 - sum
	return factory.PriorityAnswer{Selected: selected, Model: "synthetic-model", Confidence: 0.9, Probabilities: probabilities}, nil
}

// TestResolveAcceptedPlanChoicePersistedMismatchReviewRefuses closes the
// FJ-v3-599 gap on the review side end to end: the valid compat attempt
// persists completed through the real settle path, then a funded
// outcome-gap consistency verdict is recorded through the production
// JudgeConsistency path. The same save → review → admit →
// resolve/generate path then refuses with the exact ErrPlanChoiceReview
// "needs correction" surface — replaying the durable receipts, funding no
// choice, submitting nothing, recording no generation.
func TestResolveAcceptedPlanChoicePersistedMismatchReviewRefuses(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	draft := persistedCompatDraft(fx.repo)
	walkthroughs := persistedCompatWalkthroughs(t, draft)
	if _, ok := settlePlanAnswerWithOperation(t, ctx, db, fx, draft, factory.IntentPlanningOptions, persistedCompatAnswer(t, draft, walkthroughs, "cand-a")); !ok {
		t.Fatal("compat plan did not settle")
	}
	stub := &candidateChoiceStub{selected: "cand-a", confidence: 1}
	c := choicePlanCoordinator(t, db, fx, stub)
	seedCon := &Coordinator{Store: c.Store, Planning: &PlanningJudgments{Chooser: reviewSeedingStub{}}}
	recommended, err := seedCon.JudgeConsolidation(ctx, draft, walkthroughs)
	if err != nil {
		t.Fatalf("mismatch consolidation not recorded: %v", err)
	}
	seedMis := &Coordinator{Store: c.Store, Planning: &PlanningJudgments{Chooser: mismatchVerdictStub{}}}
	report, err := seedMis.JudgeConsistency(ctx, draft, walkthroughs, recommended)
	if err != nil {
		t.Fatalf("mismatch consistency not recorded: %v", err)
	}
	if len(report.Mismatches) != 1 || report.Mismatches[0].Class != factory.MismatchOutcomeGap {
		t.Fatalf("recorded review carries no outcome-gap mismatch: %+v", report)
	}
	accepted := persistedCompatAccepted(t, fx.repo, draft)
	if err := db.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	_, wait, err := c.ResolveAcceptedPlanChoice(ctx, 7, accepted)
	if wait != nil || !errors.Is(err, ErrPlanChoiceReview) || !strings.Contains(err.Error(), "needs correction") {
		t.Fatalf("mismatch resolve surface: wait=%+v err=%v", wait, err)
	}
	if stub.calls != 0 {
		t.Fatalf("mismatch resolve funded %d comparative calls", stub.calls)
	}
	f := &generatedGraphFixture{revision: 10, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	c.Graph, c.GraphActorID, c.AcceptanceReads = f, 91, f
	if _, wait, err := c.GenerateAcceptedIntentFromRecordedPlan(ctx, draft.Intent, accepted.ID, 7); wait != nil || !errors.Is(err, ErrPlanChoiceReview) || !strings.Contains(err.Error(), "needs correction") {
		t.Fatalf("mismatch generate surface: wait=%+v err=%v", wait, err)
	}
	if f.submissions != 0 {
		t.Fatalf("mismatch generate submitted %d native operations", f.submissions)
	}
	if stub.calls != 0 {
		t.Fatalf("mismatch generate funded %d comparative calls", stub.calls)
	}
	if _, err := db.IntentGeneration(ctx, accepted.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("mismatch generate recorded a generation: %v", err)
	}
}
