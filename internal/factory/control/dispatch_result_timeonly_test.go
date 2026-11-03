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
	"github.com/levitateos/sodaos/internal/store"
)

// Serving settlement for Issue-zero time-only roots (R11 acceptance)
// against real PostgreSQL: a succeeded intent decompose run settles
// end to end through AccountSettledRun — the same serving chain as
// every other phase — closing its typed allowance with zero readiness
// work, and a second settlement refuses. Failed and unparseable runs
// stay charged through the shared joint. This proves the shared
// registration/assignment/reservation/run/view/result core serves
// intent subjects with no separate intent engine.

// settleIntentPacket records one funded intent decompose packet through
// the production packet boundary and settles its run as succeeded,
// returning the recorded assignment and settled run.
// settleIntentPacket records one funded intent decompose packet through
// the production packet boundary and settles its run under the given
// outcome, returning the recorded assignment and settled run. A
// reconciled run is immutable, so the outcome is settled exactly once.
func settleIntentPacket(t *testing.T, ctx context.Context, db *store.Store, fx dispatchFixture, coord *Coordinator, intentID, revisionID string, outcome factory.Outcome, now time.Time) (factory.Assignment, factory.Run) {
	return settleIntentPacketWithPrompt(t, ctx, db, fx, coord, intentID, revisionID, outcome, now, true)
}

// settleIntentPacketWithPrompt records one funded intent decompose packet
// like settleIntentPacket, optionally replacing the staged planning
// prompt with plain bytes to prove missing recorded input refuses.
func settleIntentPacketWithPrompt(t *testing.T, ctx context.Context, db *store.Store, fx dispatchFixture, coord *Coordinator, intentID, revisionID string, outcome factory.Outcome, now time.Time, stagePrompt bool) (factory.Assignment, factory.Run) {
	t.Helper()
	revision := factory.IntentRevision{
		ID: revisionID, Intent: intentID, Owner: 7, Repository: fx.repo,
		Objective: "plan the recorded draft", State: "draft",
		MaxIssues: 10, CreatedUnix: now.Unix(),
		Ideas: []factory.ProductIdea{
			{ID: "d333333333333333333333333", Description: "Capture leave requests", Consequence: "Requests can be recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
		},
	}
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatalf("intent revision not recorded: %v", err)
	}
	selection := fx.policy.Roles[project.RoleCoder]
	prompt := []byte("settling intent packet " + revisionID)
	if stagePrompt {
		content, err := factory.ComposeProposalContent(revision)
		if err != nil {
			t.Fatalf("proposal content: %v", err)
		}
		prompt, err = StageIntentPlanningPrompt(revision, factory.SemanticPlannerInput{
			Operation: factory.IntentPlanningPropose, Head: revision, Content: content,
			Alternatives: factory.DescribeAlternatives(revision.Objective),
			Inspected: []factory.InspectedSource{
				{Path: "internal/factory/intent.go", Commit: strings.Repeat("c", 40), Evidence: "recorded investigation"},
			},
		})
		if err != nil {
			t.Fatalf("staged prompt: %v", err)
		}
	}
	sum := sha256.Sum256(prompt)
	rootID, runID := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		ID: rootID, AttemptRoot: rootID, PublicationAssignment: "", ParentAssignment: "",
		Subject: factory.ExecutionSubject{
			Kind:   factory.SubjectIntent,
			Intent: &factory.IntentSubject{IntentID: intentID, RevisionID: revisionID, ScopeDigest: revision.Digest(), OwnerID: 7},
		},
		Phase:     project.ExecutionDecompose,
		ProjectID: fx.proj, ActorID: 7, Role: project.RoleCoder,
		Repository: fx.repo, Issue: 0, NativeRev: 5, Acceptance: "", Preparation: "f111111111111111111111111",
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: "conn", SourceCommit: strings.Repeat("c", 40),
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(),
	}
	authority, err := coord.EffectiveAuthority(ctx, a.Repository)
	if err != nil || !authority.Effective {
		t.Fatalf("fixture authority: %+v %v", authority, err)
	}
	a.Authority = authority.Authority
	if a.Authority.RequirementsID, err = db.RequirementHead(ctx, a.ProjectID); err != nil {
		t.Fatal(err)
	}
	if a.Authority.ApprovalID, err = db.ApprovalHead(ctx, a.ProjectID); err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("settling intent root invalid: %v", err)
	}
	expected := factory.IssueControl{
		Repository: fx.repo, Issue: 0, NativeRev: a.NativeRev,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	r := factory.Reservation{AssignmentID: a.ID, Repository: fx.repo, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 45}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: a.SourceCommit,
		Started: now, Deadline: now.Add(45 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: fx.repo, Issue: 0, Attempt: a.ID}
	if err := db.RecordDispatchPacket(ctx, expected, a, r, run, view); err != nil {
		t.Fatalf("settling intent packet refused: %v", err)
	}
	summary := "planning done"
	if outcome != factory.Succeeded {
		summary = "provider failed"
	}
	run.Outcome, run.Summary, run.Reconciled = outcome, summary, true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return a, run
}

func settleIntentOutput(t *testing.T, intentID, revisionID string) string {
	t.Helper()
	answer := factory.SemanticPlannerOutput{
		Intent: intentID, Revision: revisionID, Operation: factory.IntentPlanningPropose,
		Candidates: []factory.SemanticCandidate{{
			Key: "keep", Rationale: "Keep the recorded draft as the single review baseline.",
			Tasks: []factory.IntentTask{{
				Key: "d999999999999999999999999", Objective: "Plan the recorded draft",
				Outcomes: []string{"Draft is planned"}, IdeaIDs: []string{"d333333333333333333333333"},
			}},
		}},
	}
	return phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "single choice ready", Output: &factory.PhaseOutput{Decomposition: &answer},
	})
}

func TestAccountSettledRunSettlesIntentRoot(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	policy, err := db.RepositoryPolicy(ctx, fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.IntentMinutes = 45
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db, Host: fx.host}
	now := time.Now().UTC().Truncate(time.Second)
	intentID, revisionID := "d777777777777777777777777", "d888888888888888888888888"
	a, run := settleIntentPacket(t, ctx, db, fx, coord, intentID, revisionID, factory.Succeeded, now)
	finished, ok := accountRunFinish(t, ctx, db, run, settleIntentOutput(t, intentID, revisionID), time.Now())
	if !ok || finished.Outcome != factory.Succeeded || finished.Reason != factory.AssignReasonReported {
		t.Fatalf("accounted = %+v %v", finished, ok)
	}
	if finished.Result == nil || !finished.Result.Reported || finished.Result.Phase != project.ExecutionDecompose ||
		finished.Result.Candidate != "" || finished.Result.PhaseResult.Output.Decomposition == nil {
		t.Fatalf("intent result not exact: %+v", finished.Result)
	}
	if r, _ := db.Reservation(ctx, a.ID); r.State != factory.ReservationConsumed {
		t.Fatalf("reservation = %+v", r)
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total < 1 {
		t.Fatalf("usage = %d", total)
	}
	allowance, err := db.IntentAttemptAllowance(ctx, fx.repo, factory.IntentAttemptScope{IntentID: intentID, OwnerID: 7}, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !allowance.Closed || allowance.Active {
		t.Fatalf("settled intent did not close+stop: %+v", allowance)
	}
	if again, ok := accountRunFinish(t, ctx, db, run, settleIntentOutput(t, intentID, revisionID), time.Now()); ok || again.Stage != "" {
		t.Fatalf("second accounting = %+v %v", again, ok)
	}
}

func TestAccountSettledRunChargesFailedIntentRun(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	policy, err := db.RepositoryPolicy(ctx, fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.IntentMinutes = 45
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db, Host: fx.host}
	now := time.Now().UTC().Truncate(time.Second)
	a, run := settleIntentPacket(t, ctx, db, fx, coord, "d777777777777777777777777", "d888888888888888888888888", factory.Failed, now)
	finished, ok := AccountSettledRun(ctx, db, run, time.Now())
	if !ok || finished.Outcome != factory.Failed || finished.Reason != factory.AssignReasonRunFailed {
		t.Fatalf("accounted = %+v %v", finished, ok)
	}
	// Failed provider work stays charged: the typed allowance still
	// answers for this root, never refilled.
	if _, err := db.IntentAttemptAllowance(ctx, fx.repo, factory.IntentAttemptScope{IntentID: "d777777777777777777777777", OwnerID: 7}, a.ID); err != nil {
		t.Fatalf("failed intent lost its allowance: %v", err)
	}
}

func TestAccountSettledRunRefusesUnparseableIntentOutput(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	policy, err := db.RepositoryPolicy(ctx, fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.IntentMinutes = 45
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db, Host: fx.host}
	now := time.Now().UTC().Truncate(time.Second)
	_, run := settleIntentPacket(t, ctx, db, fx, coord, "d777777777777777777777777", "d888888888888888888888888", factory.Succeeded, now)
	finished, ok := AccountSettledRun(ctx, db, run, time.Now())
	if !ok || finished.Outcome != factory.NeedsHuman || finished.Result.Status != "blocked" {
		t.Fatalf("accounted = %+v %v", finished, ok)
	}
}

// TestAccountSettledRunRefusesMissingStagedIntentInput proves a reported
// semantic answer with no recorded staged prompt refuses at the serving
// settlement boundary: there is no compatibility path around the staged
// contract, while usage and consumption stay recorded.
func TestAccountSettledRunRefusesMissingStagedIntentInput(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	policy, err := db.RepositoryPolicy(ctx, fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.IntentMinutes = 45
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db, Host: fx.host}
	now := time.Now().UTC().Truncate(time.Second)
	a, run := settleIntentPacketWithPrompt(t, ctx, db, fx, coord, "d777777777777777777777777", "d888888888888888888888888", factory.Succeeded, now, false)
	finished, ok := accountRunFinish(t, ctx, db, run, settleIntentOutput(t, a.Subject.Intent.IntentID, a.Subject.Intent.RevisionID), time.Now())
	if !ok || finished.Outcome != factory.NeedsHuman || finished.Reason != factory.AssignReasonNoReport {
		t.Fatalf("unstaged answer settled: %+v %v", finished, ok)
	}
	if finished.Result == nil || finished.Result.Reported || finished.Result.PhaseResult.Output != nil {
		t.Fatalf("unstaged refusal carries a report: %+v", finished.Result)
	}
}

// semanticTwoScopeHead returns a draft head with two attributed accept
// ideas and both in its effective scope, for multi-candidate, attention
// and omitted-outcome settlement proofs.
func semanticTwoScopeHead(repo int64) factory.IntentRevision {
	return factory.IntentRevision{
		ID: "d888888888888888888888888", Intent: "d777777777777777777777777", Owner: 7, Repository: repo,
		Objective: "Decide vacation requests with manager review", State: "draft",
		MaxIssues: 10, CreatedUnix: time.Now().UTC().Truncate(time.Second).Unix(),
		Ideas: []factory.ProductIdea{
			{ID: "d333333333333333333333333", Description: "Capture leave requests", Consequence: "Requests can be recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
			{ID: "d444444444444444444444444", Description: "Managers review requests", Consequence: "Decisions are visible", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
		},
		EffectiveOutcomes: []string{"d333333333333333333333333", "d444444444444444444444444"},
	}
}

// settlePlanAnswer launches one bound plan run for the head through the
// production connector and settles it with the given fenced answer,
// returning the serving accounting result.
func settlePlanAnswer(t *testing.T, ctx context.Context, db *store.Store, fx dispatchFixture, head factory.IntentRevision, answer string) (factory.Assignment, bool) {
	t.Helper()
	return settlePlanAnswerWithOperation(t, ctx, db, fx, head, factory.IntentPlanningPropose, answer)
}

// launchPlanRun records the head and launches one bound plan run for the
// given operation through the production connector, returning the still
// open run for the caller to settle or leave pending.
func launchPlanRun(t *testing.T, ctx context.Context, db *store.Store, fx dispatchFixture, head factory.IntentRevision, operation string) factory.Run {
	t.Helper()
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	if err := db.SaveIntentRevision(ctx, head); err != nil {
		t.Fatalf("intent revision not recorded: %v", err)
	}
	content, err := factory.ComposeProposalContent(head)
	if err != nil {
		t.Fatalf("proposal content: %v", err)
	}
	in := factory.SemanticPlannerInput{
		Operation: operation, Head: head, Content: content,
		Alternatives: factory.DescribeAlternatives(head.Objective),
		Inspected: []factory.InspectedSource{
			{Path: "internal/factory/intent.go", Commit: strings.Repeat("c", 40), Evidence: "recorded investigation"},
		},
	}
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.deps().Reads}
	var observed bool
	timeOnlyOrderHost(fx, &observed, func(context.Context, project.FactoryLaunch) {})
	report := coord.DispatchIntentPlan(ctx, head.Intent, 7, operation, in, strings.Repeat("c", 40), "")
	if len(report.Errors) != 0 || len(report.Launched) != 1 {
		t.Fatalf("plan route report: %+v", report)
	}
	run, err := db.FactoryRun(ctx, report.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// settlePlanAnswerWithOperation launches one bound plan run for the given
// operation through the production connector and settles it with the
// given fenced answer, returning the serving accounting result.
func settlePlanAnswerWithOperation(t *testing.T, ctx context.Context, db *store.Store, fx dispatchFixture, head factory.IntentRevision, operation, answer string) (factory.Assignment, bool) {
	t.Helper()
	run := launchPlanRun(t, ctx, db, fx, head, operation)
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "planning done", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return accountRunFinish(t, ctx, db, run, answer, time.Now())
}

// TestSettledDecomposeRetainsMultipleCandidates proves the serving
// settlement keeps every assessed candidate: a broad two-task graph and
// a tiny single-task graph covering the same authorized scope both
// settle with no first-pick truncation.
func TestSettledDecomposeRetainsMultipleCandidates(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := semanticTwoScopeHead(fx.repo)
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "two views ready",
		Output: &factory.PhaseOutput{Decomposition: &factory.SemanticPlannerOutput{
			Intent: head.Intent, Revision: head.ID, Operation: factory.IntentPlanningPropose,
			Candidates: []factory.SemanticCandidate{
				{Key: "split", Rationale: "Split capture and review into per-outcome tasks to remove overload.", Tasks: []factory.IntentTask{
					{Key: "d999999999999999999999999", Objective: "Capture leave requests", Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d333333333333333333333333"}},
					{Key: "d999999999999999999999998", Objective: "Managers review requests", Outcomes: []string{"Decisions are visible"}, IdeaIDs: []string{"d444444444444444444444444"}, DependsOn: []string{"d999999999999999999999999"}},
				}},
				{Key: "together", Rationale: "Keep one joint task when the team prefers a single review queue.", Tasks: []factory.IntentTask{
					{Key: "d999999999999999999999997", Objective: "Decide vacation requests with manager review", Outcomes: []string{"Requests can be recorded", "Decisions are visible"}, IdeaIDs: []string{"d333333333333333333333333", "d444444444444444444444444"}},
				}},
			},
		}},
	})
	finished, ok := settlePlanAnswer(t, ctx, db, fx, head, answer)
	if !ok || finished.Outcome != factory.Succeeded || finished.Reason != factory.AssignReasonReported {
		t.Fatalf("multi-candidate answer unsettled: %+v %v", finished, ok)
	}
	decomposition := finished.Result.PhaseResult.Output.Decomposition
	if decomposition == nil || len(decomposition.Candidates) != 2 ||
		decomposition.Candidates[0].Key != "split" || decomposition.Candidates[1].Key != "together" ||
		len(decomposition.Candidates[0].Tasks) != 2 || len(decomposition.Candidates[1].Tasks) != 1 {
		t.Fatalf("candidates truncated: %+v", finished.Result)
	}
}

// TestSettledDecomposeRefusesOmittedAuthorizedOutcome adopts the
// independent review-027 probe at the serving boundary: an answer whose
// tasks omit an authorized effective outcome refuses even though every
// bound task resolves to a known idea.
func TestSettledDecomposeRefusesOmittedAuthorizedOutcome(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := semanticTwoScopeHead(fx.repo)
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "partial choice ready",
		Output: &factory.PhaseOutput{Decomposition: &factory.SemanticPlannerOutput{
			Intent: head.Intent, Revision: head.ID, Operation: factory.IntentPlanningPropose,
			Candidates: []factory.SemanticCandidate{{
				Key: "partial", Rationale: "Cover capture only.",
				Tasks: []factory.IntentTask{{
					Key: "d999999999999999999999999", Objective: "Capture leave requests",
					Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d333333333333333333333333"},
				}},
			}},
		}},
	})
	finished, ok := settlePlanAnswer(t, ctx, db, fx, head, answer)
	if !ok || finished.Outcome != factory.NeedsHuman || finished.Reason != factory.AssignReasonNoReport {
		t.Fatalf("omitted-outcome answer settled: %+v %v", finished, ok)
	}
	if finished.Result == nil || finished.Result.Reported {
		t.Fatalf("omitted-outcome refusal reported: %+v", finished.Result)
	}
}

// TestSettledDecomposeRefusesWrongOperation proves an answer for another
// planning operation refuses at settlement even though its shape and
// revision bind: the output must answer the staged operation.
func TestSettledDecomposeRefusesWrongOperation(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := semanticTwoScopeHead(fx.repo)
	answer := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionDecompose, Status: "completed",
		Summary: "options choice ready",
		Output: &factory.PhaseOutput{Decomposition: &factory.SemanticPlannerOutput{
			Intent: head.Intent, Revision: head.ID, Operation: factory.IntentPlanningOptions,
			Candidates: []factory.SemanticCandidate{{
				Key: "split", Rationale: "Split capture and review into per-outcome tasks to remove overload.",
				Tasks: []factory.IntentTask{
					{Key: "d999999999999999999999999", Objective: "Capture leave requests", Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d333333333333333333333333"}},
					{Key: "d999999999999999999999998", Objective: "Managers review requests", Outcomes: []string{"Decisions are visible"}, IdeaIDs: []string{"d444444444444444444444444"}, DependsOn: []string{"d999999999999999999999999"}},
				},
			}},
		}},
	})
	finished, ok := settlePlanAnswer(t, ctx, db, fx, head, answer)
	if !ok || finished.Outcome != factory.NeedsHuman || finished.Reason != factory.AssignReasonNoReport {
		t.Fatalf("wrong-operation answer settled: %+v %v", finished, ok)
	}
	if finished.Result == nil || finished.Result.Reported {
		t.Fatalf("wrong-operation refusal reported: %+v", finished.Result)
	}
}

// TestRecoveryRefusesMissingStagedInput adopts the independent
// review-028 probe: recovery finishing through finishFromRun refuses a
// shape-valid semantic answer with no recorded staged input, exactly
// like normal settlement.
func TestRecoveryRefusesMissingStagedInput(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	fundTimeOnlyPolicy(t, ctx, db, fx.repo)
	coord := &Coordinator{Store: db, Host: fx.host}
	now := time.Now().UTC().Truncate(time.Second)
	const intentID, revisionID = "d777777777777777777777777", "d888888888888888888888888"
	a, run := settleIntentPacketWithPrompt(t, ctx, db, fx, coord, intentID, revisionID, factory.Succeeded, now, false)
	if err := recordConfirmedUsage(ctx, db, a, run, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := finishRunWithErr(t, ctx, db, a, run, settleIntentOutput(t, intentID, revisionID), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != factory.NeedsHuman || got.Result == nil || got.Result.Reported {
		reported := got.Result != nil && got.Result.Reported
		t.Fatalf("recovery bypassed missing-input refusal: outcome=%s reported=%v", got.Outcome, reported)
	}
}

// TestRecoverySettlesStagedIntentAnswer proves the recovery path keeps
// settling a valid staged semantic answer: the same staged prompt,
// bound ideas and covering tasks report through finishFromRun with the
// typed result bound to the assignment's exact intent and revision.
func TestRecoverySettlesStagedIntentAnswer(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	policy, err := db.RepositoryPolicy(ctx, fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.IntentMinutes = 45
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db, Host: fx.host}
	now := time.Now().UTC().Truncate(time.Second)
	const intentID, revisionID = "d777777777777777777777777", "d888888888888888888888888"
	a, run := settleIntentPacket(t, ctx, db, fx, coord, intentID, revisionID, factory.Succeeded, now)
	if err := recordConfirmedUsage(ctx, db, a, run, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := finishRunWithErr(t, ctx, db, a, run, settleIntentOutput(t, intentID, revisionID), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != factory.Succeeded || got.Result == nil || !got.Result.Reported {
		t.Fatalf("staged recovery answer unsettled: %+v", got)
	}
	decomposition := got.Result.PhaseResult.Output.Decomposition
	if decomposition == nil || decomposition.Intent != intentID || decomposition.Revision != revisionID ||
		len(decomposition.Candidates) != 1 {
		t.Fatalf("recovery result not bound: %+v", got.Result)
	}
}
