// Helpers retained for the kept integration tests after unit-test removal.
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func readyDeliveryAnswer(fingerprint string) (factory.DeliveryAnswer, error) {
	return factory.DeliveryAnswer{
		Probabilities: map[string]float64{factory.DeliveryReviewAgain: 0.1, factory.DeliveryReadyToMerge: 0.9},
		Fingerprint:   fingerprint,
		Selected:      factory.DeliveryReadyToMerge,
		Model:         "jev-test",
		Probability:   0.9,
		Confidence:    0.85,
	}, nil
}

func reviewDeliveryAnswer(fingerprint string) (factory.DeliveryAnswer, error) {
	return factory.DeliveryAnswer{
		Probabilities: map[string]float64{factory.DeliveryReviewAgain: 0.9, factory.DeliveryReadyToMerge: 0.1},
		Fingerprint:   fingerprint,
		Selected:      factory.DeliveryReviewAgain,
		Model:         "jev-test",
		Probability:   0.9,
		Confidence:    0.9,
	}, nil
}

type fakeReviewer struct {
	observed     factory.ReviewObservation
	outcome      factory.OperationOutcome
	adopted      factory.ReviewOutcome
	works        []factory.ReviewWork
	submitted    []factory.ReviewWork
	lookups      []string
	results      map[string]factory.OperationOutcome
	submitErr    error
	beforeSubmit func(factory.ReviewWork)
	cancelled    []string
}

func (f *fakeReviewer) ObserveReview(ctx context.Context, w factory.ReviewWork) (factory.ReviewObservation, error) {
	f.works = append(f.works, w)
	return f.observed, nil
}

func (f *fakeReviewer) SubmitReview(ctx context.Context, w factory.ReviewWork) (factory.OperationOutcome, error) {
	f.submitted = append(f.submitted, w)
	if f.beforeSubmit != nil {
		f.beforeSubmit(w)
	}
	outcome := f.outcome
	outcome.OperationID, outcome.InstallationID, outcome.Kind = w.OperationID, "test-installation", factory.OpReviewSubmit
	outcome.ActorID, outcome.RepositoryID = w.ActorID, w.Repository
	if f.results == nil {
		f.results = make(map[string]factory.OperationOutcome)
	}
	f.results[w.OperationID] = outcome
	return outcome, f.submitErr
}

func (f *fakeReviewer) LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error) {
	f.lookups = append(f.lookups, operationID)
	if outcome, ok := f.results[operationID]; ok {
		return outcome, nil
	}
	return factory.OperationOutcome{NotObserved: true}, nil
}

func (f *fakeReviewer) CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error) {
	f.cancelled = append(f.cancelled, operationID)
	outcome, ok := f.results[operationID]
	if !ok {
		return factory.OperationOutcome{NotObserved: true}, nil
	}
	outcome.Effect, outcome.Cancellation = factory.OpEffectNotCommitted, factory.OpCancelCancelled
	f.results[operationID] = outcome
	return outcome, nil
}

func (f *fakeReviewer) AdoptReview(_ context.Context, w factory.ReviewWork, outcome factory.OperationOutcome) (factory.ReviewOutcome, error) {
	adopted := f.adopted
	adopted.Operation = outcome
	return adopted, nil
}

func reviewRunSeed(t *testing.T, settled bool) (*publishFixture, factory.Assignment, factory.Publication, factory.Run) {
	t.Helper()
	ctx := context.Background()
	fx := publishSeed(t)
	policy, err := fx.db.RepositoryPolicy(ctx, fx.seed.repo)
	if err != nil {
		t.Fatal(err)
	}
	policy.Review = factory.ActorBindingRef{TokenID: 2, ActorID: 6, Kind: factory.OpReviewSubmit}
	if err := fx.db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	sponsorship, err := fx.db.Sponsorship(ctx, fx.seed.repo, "conn")
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.Roles = append(sponsorship.Roles, project.RoleReviewer)
	if err := fx.db.SaveSponsorship(ctx, sponsorship); err != nil {
		t.Fatal(err)
	}
	a := fx.finishReported(t, 14)
	fx.wire(happyPublisher())
	fx.pass(t)
	p, err := fx.db.PublicationByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, run := fx.childRun(t, a, &p, project.RoleReviewer, p.Candidate, "f222222222222222222222222", settled)
	return fx, a, p, run
}

func phasedDecisionID(n int) string { return fmt.Sprintf("d%024x", 0xe000+n) }
func phaseTestRepairAssignment(t *testing.T, phase, role string, subject factory.ExecutionSubject) factory.Assignment {
	t.Helper()
	id, run := factory.NewID(), factory.NewID()
	prompt := []byte("prompt bytes for " + phase)
	sum := sha256.Sum256(prompt)
	a := factory.Assignment{
		ID: id, AttemptRoot: id,
		Subject: subject, Phase: phase,
		ProjectID: "p" + strings.Repeat("1", 24), Role: role,
		Repository: 7, NativeRev: 1,
		Preparation: "f" + strings.Repeat("d", 24),
		Harness:     "codex-1.2.3", HarnessVers: "1.2.3",
		Model: "test-model", Connection: "conn",
		SourceCommit: phaseTestCommit(), Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: run, RunHistory: []string{run}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: 1700000000, ActorID: 7,
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("repair assignment fixture invalid: %v", err)
	}
	return a
}

func phasedDeliverySubject() factory.ExecutionSubject {
	return factory.ExecutionSubject{
		Kind: factory.SubjectDelivery,
		Delivery: &factory.DeliverySubject{
			DeliveryID: strings.Repeat("7", 32), IntentID: phasedDecisionID(20),
			AcceptedRevision: phasedDecisionID(21), ScopeDigest: phaseTestSHA("scope"),
			SourceCommit: phaseTestCommit(), PreparationDigest: phaseTestSHA("prep"),
			VerificationManifestDigest: phaseTestSHA("manifest"),
		},
	}
}

func phaseTestSHA(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func phaseTestCommit() string { return strings.Repeat("c", 40) }

// fixtureEnumerator serves scripted pages with a stable revision
// bracket unless told to move it.
type fixtureEnumerator struct {
	pages    map[string]NativeInventoryPage
	revision int64
	idle     bool
	calls    int
	observes int
	moveRev  func(call int) int64
	err      error
}

func (f *fixtureEnumerator) ObserveRevision(context.Context) (int64, bool, error) {
	f.observes++
	if f.err != nil {
		return 0, false, f.err
	}
	revision := f.revision
	if f.moveRev != nil {
		revision = f.moveRev(f.calls)
	}
	idle := f.idle
	if !idle {
		return revision, false, nil
	}
	return revision, true, nil
}

func (f *fixtureEnumerator) EnumerateIssues(_ context.Context, _ int64, cursor string) (NativeInventoryPage, error) {
	f.calls++
	if f.err != nil {
		return NativeInventoryPage{}, f.err
	}
	page, ok := f.pages[cursor]
	if !ok {
		return NativeInventoryPage{}, errors.New("unexpected cursor")
	}
	revision := f.revision
	if f.moveRev != nil {
		revision = f.moveRev(f.calls)
	}
	page.Revision = revision
	page.Idle = f.idle
	return page, nil
}

func issueDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// inventoryIssue builds one canonical sealed payload through the real
// producer encoding, so the fixtures prove the exact bytes the seal
// persists and the decision assesses.
func inventoryIssue(t *testing.T, index int64) NativeInventoryIssue {
	t.Helper()
	title, body := fmt.Sprintf("title %d", index), fmt.Sprintf("body %d", index)
	data, err := factory.CompletionIssueEvidence{
		Index: index, Title: title, Body: body,
		TitleDigest: issueDigest(title), ContentDigest: issueDigest(body),
	}.CanonicalData()
	if err != nil {
		t.Fatal(err)
	}
	return NativeInventoryIssue{Index: index, Data: data}
}

func twoPageEnumerator(t *testing.T) *fixtureEnumerator {
	t.Helper()
	return &fixtureEnumerator{
		revision: 41,
		idle:     true,
		pages: map[string]NativeInventoryPage{
			"":  {Issues: []NativeInventoryIssue{inventoryIssue(t, 1), inventoryIssue(t, 2)}, NextCursor: "2"},
			"2": {Issues: []NativeInventoryIssue{inventoryIssue(t, 3)}, Complete: true},
		},
	}
}

func authorizeMergeFixture(t *testing.T, ctx context.Context, db *store.Store, coord *Coordinator, p factory.Publication) {
	t.Helper()
	authorizeMergeFixtureRound(t, ctx, db, coord, p, 1)
}

func phaseTestPlan(t *testing.T, root, source, prep string) factory.InvestigationPlan {
	t.Helper()
	plan := factory.InvestigationPlan{
		ID: factory.NewID(), AttemptRoot: root,
		ScopeDigest: phaseTestSHA("scope"), InputDigest: phaseTestSHA("input"),
		SourceCommit: source, IntegrationCommit: source,
		Preparation:        prep,
		Approach:           "inspect the selected source, then plan behind checks",
		AcceptanceCoverage: []string{"accept-one"},
		Tasks: []factory.PlanTask{{
			ID: "task-one", Objective: "inspect the selected source",
			Outputs: []string{"notes"}, AcceptanceRefs: []string{"accept-one"},
			Lane: "lane-one", OwnedPaths: []string{"internal/x.go"},
		}},
		Lanes: []factory.PlanLane{{ID: "lane-one", TaskIDs: []string{"task-one"}}},
		Checks: []factory.CheckSpec{{
			ID: "ci", Argv: []string{"ci"}, Cwd: ".",
			TimeoutSeconds: 60, Scope: factory.CheckScopeTask, Required: true,
		}},
	}
	digest, err := factory.PlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.Digest = digest
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan fixture invalid: %v", err)
	}
	if err := plan.Executable(); err != nil {
		t.Fatalf("plan fixture not executable: %v", err)
	}
	return plan
}

func phaseTestAssignment(t *testing.T, phase, role string, validate bool) factory.Assignment {
	t.Helper()
	id, run := factory.NewID(), factory.NewID()
	prompt := []byte("prompt bytes for " + phase)
	sum := sha256.Sum256(prompt)
	acceptance := "d" + strings.Repeat("a", 24)
	a := factory.Assignment{
		ID: id, AttemptRoot: id, PublicationAssignment: id,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 7, Acceptance: acceptance},
		},
		Phase:     phase,
		ProjectID: "p" + strings.Repeat("1", 24), Role: role,
		Repository: 7, Issue: 7, NativeRev: 1,
		Acceptance: acceptance, Preparation: "f" + strings.Repeat("d", 24),
		Harness: "codex-1.2.3", HarnessVers: "1.2.3",
		Model: "test-model", Connection: "conn",
		SourceCommit: phaseTestCommit(), Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: run, RunHistory: []string{run}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: 1700000000, ActorID: 7,
	}
	if validate {
		if err := a.Validate(); err != nil {
			t.Fatalf("assignment fixture invalid: %v", err)
		}
	}
	return a
}

// phaseFinish encodes one typed phase result as the worker's MCP finish
// for accountRunFinish, finishRunWith and buildRunFinish.
func phaseFinish(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// buildRunFinish maps one settled run plus the worker's finish the way the
// MCP settler does: a completed typed result maps through the shared
// builders (a refusal fences for attention), a non-completed one settles
// as finish_incomplete, and no finish settles from the run outcome alone.
func buildRunFinish(t *testing.T, a factory.Assignment, run factory.Run, finish string, now time.Time) (factory.Assignment, error) {
	t.Helper()
	if finish == "" || run.Outcome != factory.Succeeded {
		return finishAssignmentFromRun(a, run, now)
	}
	var parsed factory.PhaseResult
	if err := json.Unmarshal([]byte(finish), &parsed); err != nil {
		t.Fatalf("finish fixture is not a phase result: %v", err)
	}
	submission := factory.MCPSubmission{AssignmentID: a.ID, SubmissionID: factory.NewID(), Tool: factory.MCPToolFinishIncomplete}
	if parsed.Status != "completed" {
		return settleIncomplete(a, submission, map[string]string{"outcome": parsed.Status, "cause": parsed.Summary}, now), nil
	}
	if profile, err := mcpProfileForPhase(a.Phase); err == nil {
		submission.Tool = factory.MCPFinishToolOf(profile)
	}
	finished, err := finishFromMCPPhaseResult(a, parsed, now)
	if err != nil {
		finished = fenceMCPTerminal(a, submission, now, "terminal refused: "+err.Error())
	}
	return finished, nil
}

// finishRunWith finishes one assigned assignment from its settled run and
// the worker's finish through the normal result binding and persistence.
func finishRunWith(t *testing.T, ctx context.Context, db *store.Store, a factory.Assignment, run factory.Run, finish string, now time.Time) (factory.Assignment, error) {
	t.Helper()
	current, err := db.Assignment(ctx, a.ID)
	if err != nil || current.Stage != factory.AssignmentAssigned {
		return current, err
	}
	finished, err := buildRunFinish(t, current, run, finish, now)
	if err != nil {
		return factory.Assignment{}, err
	}
	return finished, db.FinishAssignment(ctx, finished)
}

// accountRunFinish accounts one settled run whose worker sent the given
// finish: usage and reservation as AccountSettledRun records them, then
// the finish settles the assignment.
func accountRunFinish(t *testing.T, ctx context.Context, db *store.Store, run factory.Run, finish string, now time.Time) (factory.Assignment, bool) {
	t.Helper()
	a, err := db.AssignmentByRun(ctx, run.ID)
	if err != nil || !run.Reconciled || a.Stage != factory.AssignmentAssigned || finish == "" {
		return AccountSettledRun(ctx, db, run, now)
	}
	if err := recordConfirmedUsage(ctx, db, a, run, now); err != nil {
		return factory.Assignment{}, false
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return factory.Assignment{}, false
	}
	finished, err := finishRunWith(t, ctx, db, a, run, finish, now)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return factory.Assignment{}, false
	}
	return finished, true
}

func authorizeMergeFixtureRound(t *testing.T, ctx context.Context, db *store.Store, coord *Coordinator, p factory.Publication, round int) {
	t.Helper()
	m, err := db.MergeByPublication(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Stage != factory.MergeOpen || m.Authorization.Bound() {
		t.Fatalf("merge row not awaiting authorization: %+v", m)
	}
	owner, err := db.Assignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	emptyContent, err := factory.FindingContentDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Phased fixtures record real review cycles before the merge round:
	// derive the round from the stored publication like production instead
	// of reusing the hardcoded first round.
	stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	round = maxReviewCycleRound(stored) + 1
	reviewer := factory.NewID()
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: owner.AttemptRoot,
		ID: factory.ReviewCycleID(p.ID, owner.AttemptRoot, round, "review"), Phase: "review",
		TriggerDecision: factory.DeliveryReviewAgain, TriggerReason: "fixture round needs its independent inspection",
		Head: p.Candidate, BaseOID: p.PRCreate.BaseOID, FindingDigest: emptyContent,
		ReviewerAssignment: reviewer, Round: round,
	}
	if err := cycle.Validate(); err != nil {
		t.Fatal(err)
	}
	cycled := stored
	cycled.ReviewCycles = append(cycled.ReviewCycles, cycle)
	cycled.Revision = stored.Revision + 1
	if err := db.UpdatePublication(ctx, cycled); err != nil {
		t.Fatal(err)
	}
	assessment, err := db.CheckAssessment(ctx, p.Repository, p.PRNumber)
	if err != nil {
		t.Fatal(err)
	}

	policy, err := db.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		t.Fatal(err)
	}
	journaled, acceptance, err := AdmitClearReview(ClearAdmissionInput{
		Publication: cycled, Cycle: cycle,
		Reviewer:       clearTestReviewer(t, reviewer, cycle.Head, cycle.BaseOID, cycle, nil, nil),
		Carried:        nil,
		ScopeDigest:    dispatchDigest("merge fixture scope"),
		PolicyRevision: strconv.FormatInt(policy.Revision, 10),
		CheckDigest:    assessment.ChecksDigest,
		DiffBase:       p.PRCreate.BaseOID,
	})
	if err != nil {
		t.Fatal(err)
	}
	journaled.Revision = cycled.Revision + 1
	if err := db.UpdatePublication(ctx, journaled); err != nil {
		t.Fatal(err)
	}
	if err := coord.AuthorizeMerge(ctx, p.ID, servingUnionBinding(t, m, acceptance, factory.DeliveryDecision{})); err != nil {
		t.Fatal(err)
	}
}

func testMCPCapability() factory.MCPCapability {
	digest := strings.Repeat("a", 64)
	return factory.MCPCapability{
		ID: factory.NewID(), AssignmentID: factory.NewID(),
		RunID: "run-1", Generation: 1,
		Profile: factory.MCPProfileBuilder, Subject: "implement:42", Repository: 7,
		Project:           "p" + strings.Repeat("d", 24),
		PreparationDigest: digest, SourceDigest: "abc", AcceptedRevision: "rev-1",
		Authority: digest, DeadlineUnix: time.Now().Add(time.Hour).Unix(),
		State: factory.MCPCapabilityActive,
	}
}

func testMCPObserved(capability factory.MCPCapability) MCPRunObservation {
	return MCPRunObservation{
		RunID: capability.RunID, Incarnation: 3,
		AssignmentID: capability.AssignmentID, Authority: capability.Authority,
	}
}

func testMCPSubmission(capability factory.MCPCapability, tool factory.MCPTool, id string, payload []byte) factory.MCPSubmission {
	return factory.MCPSubmission{
		AssignmentID: capability.AssignmentID, SubmissionID: id,
		CapabilityID: capability.ID, Tool: tool,
		Payload: payload,
	}
}

func unionSeedForBind(t *testing.T, ctx context.Context, issue int64) (*mergeFixture, factory.Publication, factory.Merge, factory.DeliveryAcceptance, factory.DeliveryDecision, string) {
	t.Helper()
	fx, p := mergeSeed(t, issue)
	db := fx.publish.db
	storedPub, acceptance, decision, fingerprint := unionPgAdmitJev(t, ctx, db, p, strings.Repeat("4", 32), nil)
	setup := MergeReport{Merged: []MergeLink{}}
	fx.publish.coord.mergeOne(ctx, storedPub, &setup)
	if len(setup.Errors) != 0 {
		t.Fatalf("merge row open errored: %+v", setup)
	}
	m, err := db.MergeByPublication(ctx, p.ID)
	if err != nil {
		t.Fatalf("merge row unopened: %v", err)
	}
	return fx, storedPub, m, acceptance, decision, fingerprint
}

// unionAuthForBind builds one union authorization for an open row behind
// a journaled acceptance.
func unionAuthForBind(t *testing.T, m factory.Merge, acceptance factory.DeliveryAcceptance, decision factory.DeliveryDecision, fingerprint string, confirmed bool) factory.MergeAuthorization {
	t.Helper()
	auth := factory.MergeAuthorization{
		Generation: factory.MergeGeneration{
			Authority: m.Authority, PublicationID: m.PublicationID,
			DecisionID:         acceptance.DecisionID,
			NativeOperationID:  factory.MergeGenerationOperationID(m.PublicationID, 1),
			Fingerprint:        fingerprint,
			Head:               m.HeadOID,
			Base:               m.BaseOID,
			DiffBase:           acceptance.Tuple.DiffBase,
			ChecksDigest:       strings.Repeat("c", 64),
			AcceptanceKind:     acceptance.Kind,
			AcceptanceID:       acceptance.ID,
			AttemptRoot:        acceptance.AttemptRoot,
			OriginReceipt:      acceptance.OriginReceipt,
			Generation:         1,
			ReviewRound:        acceptance.Round,
			AssessmentRevision: 2, ObservedNativeRev: 12, NotAfter: 1300,
		},
		Decision: decision, ResolutionsConfirmed: confirmed,
	}
	return auth
}

func correctionTestKey(n int) string { return fmt.Sprintf("%032x", 0xa0+n) }

func correctionTestFinding(n int, actionable bool, fixKind string) factory.Finding {
	return factory.Finding{
		Key: correctionTestKey(n), OriginRun: strings.Repeat("1", 32),
		Severity: factory.FindingSeverityMajor,
		Defect:   "missing bound", Evidence: "line 42 lacks a check", RequiredOutcome: "add the check",
		FixKind: fixKind, Actionable: actionable,
	}
}

func correctionTestDigest(t *testing.T, keys ...string) string {
	t.Helper()
	digest, err := factory.FindingSetDigest(keys)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func clearTestReviewer(t *testing.T, reviewer, head, base string, cycle factory.ReviewCycle, assessments []factory.FindingAssessment, carried []factory.Finding) factory.Assignment {
	t.Helper()
	content, err := factory.FindingContentDigest(carried)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := factory.ClearCoverageDigest(head, base, content)
	if err != nil {
		t.Fatal(err)
	}
	a := phaseTestAssignment(t, project.ExecutionReview, project.RoleReviewer, true)
	a.ID, a.SourceCommit = reviewer, head
	// The reviewer is a child of the attempt root: its lineage binds
	// the recorded publication owner and cycle attempt root.
	a.ParentAssignment = cycle.AttemptRoot
	a.AttemptRoot, a.PublicationAssignment = cycle.AttemptRoot, cycle.AttemptRoot
	a.Stage, a.Outcome, a.Reason, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, 1700000100
	a.Result = &factory.AssignmentResult{
		AssignmentID: reviewer, RunID: a.Run, Phase: project.ExecutionReview,
		Status: "completed", Summary: "verdict recorded", Reported: true, RecordedUnix: 1700000100,
		PhaseResult: &factory.PhaseResult{
			Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed",
			Summary: "verdict recorded",
			Output: &factory.PhaseOutput{Clear: &factory.ClearReviewReport{
				Assessments: assessments, CycleID: cycle.ID, Conclusion: factory.ClearReviewConclusion,
				Head: head, BaseOID: base, CoverageDigest: coverage,
			}},
		},
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("clear reviewer invalid: %v", err)
	}
	return a
}

func unionPgAdmitJev(t *testing.T, ctx context.Context, db *store.Store, p factory.Publication, reviewer string, intents []string) (factory.Publication, factory.DeliveryAcceptance, factory.DeliveryDecision, string) {
	t.Helper()
	owner, err := db.Assignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	finding := correctionTestFinding(1, true, factory.FindingFixVerification)
	digest := correctionTestDigest(t, finding.Key)
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: owner.AttemptRoot,
		ID:                 factory.ReviewCycleID(p.ID, owner.AttemptRoot, 1, "review"),
		Phase:              "review",
		TriggerDecision:    "initial-review",
		TriggerReason:      "first review of the published head",
		Head:               p.Candidate,
		BaseOID:            p.PRCreate.BaseOID,
		FindingDigest:      digest,
		ReviewerAssignment: reviewer,
		Round:              1,
	}
	p.ReviewCycles = append(p.ReviewCycles, cycle)
	scope, err := factory.FindingContentDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintInput := factory.DeliveryFingerprintInput{
		Reviews:       []string{reviewer},
		Conversations: []string{strings.Repeat("2", 64)},
		AttemptRoot:   owner.AttemptRoot,
		PublicationID: p.ID,
		ScopeDigest:   scope,
		FixDigest:     scope,
		DiffDigest:    scope,
		CheckDigest:   scope,
		TreeDigest:    scope,
		Head:          p.Candidate,
		Base:          p.PRCreate.BaseOID,
		Model:         "jev-test",
		Rubric:        factory.DeliveryRubric,
		Round:         1,
	}
	fingerprint, err := factory.DeliveryFingerprint(fingerprintInput)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := readyDeliveryAnswer(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	decision := factory.DeliveryDecision{Answer: answer, Effective: factory.DeliveryEffectiveReady, ReviewsFit: true}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	published, acceptance, err := AdmitJevAcceptance(JevAdmissionInput{
		Publication:       p,
		Cycle:             cycle,
		Carried:           []factory.Finding{finding},
		FingerprintInput:  fingerprintInput,
		Fingerprint:       fingerprint,
		Decision:          decision,
		DecisionID:        strings.Repeat("9", 32),
		ScopeDigest:       scope,
		PolicyRevision:    "policy-revision-9",
		CheckDigest:       scope,
		DiffBase:          p.PRCreate.BaseOID,
		ResolutionIntents: intents,
	})
	if err != nil {
		t.Fatal(err)
	}
	published.Revision = p.Revision + 1
	if err := db.UpdatePublication(ctx, published); err != nil {
		t.Fatal(err)
	}
	return published, acceptance, decision, fingerprint
}

func servingUnionBinding(t *testing.T, m factory.Merge, acceptance factory.DeliveryAcceptance, decision factory.DeliveryDecision) factory.MergeAuthorization {
	t.Helper()
	fingerprint := strings.Repeat("e", 64)
	decisionID := ""
	if acceptance.Kind == factory.AcceptanceJevAcceptance {
		fingerprint, decisionID = decision.Answer.Fingerprint, acceptance.DecisionID
	}
	return factory.MergeAuthorization{
		Generation: factory.MergeGeneration{
			Authority: m.Authority, PublicationID: m.PublicationID,
			DecisionID:        decisionID,
			NativeOperationID: factory.MergeGenerationOperationID(m.PublicationID, 1),
			Fingerprint:       fingerprint, Head: m.HeadOID, Base: m.BaseOID, DiffBase: m.BaseOID,
			ChecksDigest:   strings.Repeat("c", 64),
			AcceptanceKind: acceptance.Kind, AcceptanceID: acceptance.ID,
			AttemptRoot: acceptance.AttemptRoot, OriginReceipt: acceptance.OriginReceipt,
			Generation:         1,
			ReviewRound:        acceptance.Round,
			AssessmentRevision: 2, ObservedNativeRev: 12, NotAfter: 1300,
		},
		Decision:             decision,
		ResolutionsConfirmed: true,
	}
}

func grantPolicy() factory.RepositoryPolicy {
	return factory.RepositoryPolicy{
		Repository: 42, GrantedBy: 7, Enabled: true,
		TargetBranch: "refs/heads/main",
		Roles: map[string]factory.RoleSelection{
			project.RoleCoder:    {Harness: project.FactoryHarnessMuse, HarnessVers: "0.157.1", Model: "test"},
			project.RoleReviewer: {Harness: project.FactoryHarnessMuse, HarnessVers: "0.157.1", Model: "test"},
		},
		Checks:              []string{"native-ci/build"},
		MergeMethod:         factory.MergeFastForward,
		Publish:             factory.ActorBindingRef{TokenID: 11, ActorID: 12, Kind: factory.OpRefPublish},
		Create:              factory.ActorBindingRef{TokenID: 11, ActorID: 12, Kind: factory.OpPRCreate},
		Review:              factory.ActorBindingRef{TokenID: 13, ActorID: 14, Kind: factory.OpReviewSubmit},
		Merge:               factory.ActorBindingRef{TokenID: 15, ActorID: 16, Kind: factory.OpMerge},
		AttemptLimits:       factory.DefaultAttemptLimits(),
		ReassessmentMinutes: factory.DefaultReassessmentMinutes,
	}
}

func grantProfile() *project.Profile {
	return &project.Profile{
		ID: project.RockyHeadless, Distribution: "rocky", Version: "9.6", Interface: "headless",
		Architecture: "amd64", Image: "sha256:" + strings.Repeat("b", 64), Revision: strings.Repeat("c", 40),
	}
}

func grantProject(t *testing.T, c *Coordinator, id string) {
	t.Helper()
	ctx := context.Background()
	if err := c.Store.UpsertUser(ctx, store.User{ID: 7, Login: "soda-tester"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.CreateProject(ctx, store.Project{ID: id, Name: "repo", RepositoryID: 42, OwnerID: 7, Repository: "soda-tester/repo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
}

func grantFullAuthority(t *testing.T, c *Coordinator) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.ApplyPolicy(ctx, "native:7", grantPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyOperatorGrant(ctx, "os-uid:0",
		factory.OperatorGrant{Repository: 42, GrantedBy: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyCapacity(ctx, "os-uid:0",
		factory.Capacity{UpdatedBy: 1, MaxConcurrentRuns: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplySponsorship(ctx, "native:9",
		factory.Sponsorship{
			Repository: 42, GrantedBy: 9, ActorID: 9, ProjectID: "p" + strings.Repeat("a", 24), Connection: "conn-1", GrantID: "grant-1",
			Generation: 1, Roles: []string{project.RoleCoder}, AllowanceMinutes: 60, Active: true,
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApplyEnvironmentGrant(ctx, "native:7",
		project.EnvironmentGrant{Repository: 42, Owner: 7, Profile: grantProfile(), Active: true}); err != nil {
		t.Fatal(err)
	}
}

// finishRunWithErr is finishRunWith for callers that only need its error.
func finishRunWithErr(t *testing.T, ctx context.Context, db *store.Store, a factory.Assignment, run factory.Run, finish string, now time.Time) error {
	t.Helper()
	_, err := finishRunWith(t, ctx, db, a, run, finish, now)
	return err
}

// recordPhaseFinishMCP records the worker's finish for one assigned
// assignment as its MCP terminal proposal, under a freshly activated
// capability, so settlement consumes it exactly as in production.
func recordPhaseFinishMCP(t *testing.T, ctx context.Context, db *store.Store, a factory.Assignment, finish string) {
	t.Helper()
	var parsed factory.PhaseResult
	if err := json.Unmarshal([]byte(finish), &parsed); err != nil {
		t.Fatal(err)
	}
	profile, err := mcpProfileForPhase(a.Phase)
	if err != nil {
		t.Fatal(err)
	}
	var tool factory.MCPTool
	var payload map[string]any
	switch {
	case parsed.Status != "completed":
		tool, payload = factory.MCPToolFinishIncomplete, map[string]any{"outcome": parsed.Status, "cause": parsed.Summary}
	case parsed.Output != nil && parsed.Output.Plan != nil:
		tool, payload = factory.MCPToolFinishIssuePlan, map[string]any{"approach": parsed.Output.Plan.Approach, "tasks": parsed.Output.Plan, "evidence_refs": []string{"inspected source"}}
	case parsed.Output != nil && parsed.Output.Implementation != nil:
		tool, payload = factory.MCPToolFinishImplement, map[string]any{"candidate": parsed.Output.Implementation.Candidate}
	default:
		t.Fatalf("no MCP finish fixture for phase %s", parsed.Phase)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	staged := testMCPCapability()
	staged.AssignmentID, staged.RunID, staged.Profile, staged.State = a.ID, a.Run, profile, factory.MCPCapabilityStaged
	if err := db.StageMCPCapability(ctx, staged); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID, staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
		t.Fatal(err)
	}
	capability, err := db.GetMCPCapability(ctx, staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	submission := testMCPSubmission(capability, tool, factory.NewID(), raw)
	if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
		t.Fatal(err)
	}
}
