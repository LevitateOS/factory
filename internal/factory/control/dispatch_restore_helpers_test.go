package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "forgejo.org/extension-sdk"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/forgejo"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
)

// Helpers for the restored dispatch/settlement integration tests. These
// seed through the real store on the disposable PostgreSQL fixture; the
// only doubles are external-system boundaries (Forgejo check observation).

type fakeCheckObserver struct {
	observe func(factory.CheckTarget) (factory.ObservedChecks, error)

	targets []factory.CheckTarget
	actors  []int64
}

func (f *fakeCheckObserver) ObserveChecks(ctx context.Context, target factory.CheckTarget, actorID int64) (factory.ObservedChecks, error) {
	f.targets = append(f.targets, target)
	f.actors = append(f.actors, actorID)
	return f.observe(target)
}

func happyCheckObserver() *fakeCheckObserver {
	return &fakeCheckObserver{
		observe: func(target factory.CheckTarget) (factory.ObservedChecks, error) {
			return factory.ObservedChecks{
				Checks:           []factory.ObservedCheck{{Context: "ci", State: factory.CheckStateSuccess}},
				NativeRev:        9,
				ObservedContexts: 1,
				HeadTip:          target.HeadOID,
				BaseTip:          target.BaseOID,
				Complete:         true,
			}, nil
		},
	}
}

// checkSeed finishes a reported assignment through publication on real PG
// and returns the published publication.
func checkSeed(t *testing.T, issue int64) (*publishFixture, factory.Publication) {
	t.Helper()
	fx := publishSeed(t)
	a := fx.finishReported(t, issue)
	fx.wire(happyPublisher())
	fx.pass(t)
	p, err := fx.db.PublicationByAssignment(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Stage != factory.PublicationPublished {
		t.Fatalf("check seed publication not published: %+v", p)
	}
	return fx, p
}

// reviewVerdictOutput settles one reviewer run with a typed v2 findings
// verdict over the given cycle and findings.
func reviewVerdictOutput(cycleID string, findings ...factory.Finding) string {
	result := factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionReview,
		Status: "completed", Summary: "review recorded",
		Output: &factory.PhaseOutput{Review: &factory.ReviewCycleReport{
			CycleID: cycleID, NewFindings: findings,
		}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// reviewerCycleID returns the review cycle that admitted the given reviewer
// child: the round carrying it as reviewer with no corrector yet.
func reviewerCycleID(publication factory.Publication, reviewer factory.Assignment) string {
	for _, cycle := range publication.ReviewCycles {
		if cycle.ReviewerAssignment == reviewer.ID && cycle.CorrectionAssignment == "" {
			return cycle.ID
		}
	}
	return ""
}

// correctionChildFixture settles one reviewer verdict under the owner and
// admits one corrector child under the finished reviewer. Phased corrector
// admission parents the corrector to the finished reviewer, never the
// publication owner.
func correctionChildFixture(t *testing.T, fx *publishFixture, owner factory.Assignment, publication *factory.Publication, settled bool) (factory.Assignment, factory.Run) {
	t.Helper()
	grantReviewerRoleKeepingRevision(t, fx, owner)
	reviewer, reviewRun := fx.childRun(t, owner, publication, project.RoleReviewer, publication.Candidate, "f222222222222222222222222", true)
	cycleID := reviewerCycleID(*publication, reviewer)
	if cycleID == "" {
		t.Fatalf("reviewer child admitted no review cycle: reviewer=%s publication=%+v", reviewer.ID, publication)
	}
	reviewer = accountChildResult(t, fx, reviewRun, reviewVerdictOutput(cycleID, correctionFinding()))
	commitReviewerOperation(t, fx, publication, reviewer)
	return fx.childRun(t, reviewer, publication, project.RoleCoder, publication.Candidate, owner.Preparation, settled)
}

// grantReviewerRoleKeepingRevision permits the reviewer role on the owner's
// connection without moving the sponsorship revision, so admissions recorded
// before the grant keep their authority snapshot.
func grantReviewerRoleKeepingRevision(t *testing.T, fx *publishFixture, owner factory.Assignment) {
	t.Helper()
	ctx := context.Background()
	raw, err := sql.Open("pgx", fx.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var data []byte
	if err := raw.QueryRowContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 AND connection=$2`, owner.Repository, owner.Connection).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var sponsorship factory.Sponsorship
	if err := json.Unmarshal(data, &sponsorship); err != nil {
		t.Fatal(err)
	}
	for _, role := range sponsorship.Roles {
		if role == project.RoleReviewer {
			return
		}
	}
	sponsorship.Roles = append(sponsorship.Roles, project.RoleReviewer)
	updated, err := json.Marshal(sponsorship)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE factory_sponsorships SET data=$1 WHERE repository=$2 AND connection=$3`, string(updated), owner.Repository, owner.Connection); err != nil {
		t.Fatal(err)
	}
}

// commitReviewerOperation records the finished reviewer's committed review
// operation, so a corrector admitted under it answers the newest committed
// verdict on the current head and base.
func commitReviewerOperation(t *testing.T, fx *publishFixture, publication *factory.Publication, reviewer factory.Assignment) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	stored, err := fx.db.PublicationByAssignment(ctx, reviewer.PublicationAssignment)
	if err != nil {
		t.Fatal(err)
	}
	actor := reviewer.ActorID
	if actor == stored.PRCreate.Work.ActorID {
		actor = stored.PRCreate.Work.ActorID + 1
	}
	stored.ReviewOperations = append(stored.ReviewOperations, factory.ReviewOperation{
		RunID: reviewer.Run,
		Work: factory.ReviewWork{
			OperationID:  "review-" + reviewer.Run,
			AuthRevision: factory.ReviewAuthRevision(stored.AssignmentID, reviewer.Run),
			Repository:   stored.Repository, ActorID: actor,
			PRNumber: stored.PRNumber, PRID: stored.PRID, IssueID: stored.PRCreate.IssueID,
			PRAuthorID: stored.PRCreate.Work.ActorID,
			HeadRef:    stored.PRCreate.HeadRef, BaseRef: stored.PRCreate.BaseRef,
			HeadOID: stored.Candidate, BaseOID: stored.PRCreate.BaseOID,
			NativeRev: stored.NativeRev, NotAfter: now.Add(10 * time.Minute).Unix(),
			Event: "REQUEST_CHANGES", Body: "independent review",
		},
		Outcome: factory.OperationOutcome{
			OperationID: "review-" + reviewer.Run, InstallationID: "installation-1",
			Kind: factory.OpReviewSubmit, ActorID: actor, RepositoryID: stored.Repository,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete,
			Receipt: []byte(`{"review_id":31}`),
		},
	})
	stored.Revision++
	if err := fx.db.UpdatePublication(ctx, stored); err != nil {
		t.Fatalf("review operation unrecorded: %v", err)
	}
	refreshed, err := fx.db.PublicationByAssignment(ctx, reviewer.PublicationAssignment)
	if err != nil {
		t.Fatal(err)
	}
	*publication = refreshed
}

// correctionRun settles one finished reviewer verdict and one coder corrector
// child carrying the given candidate.
func correctionRun(t *testing.T, fx *publishFixture, a factory.Assignment, candidate string) factory.Run {
	t.Helper()
	publication := fx.publication(t, a)
	_, run := correctionChildFixture(t, fx, a, &publication, true)
	accountChildResult(t, fx, run, correctionOutput(candidate))
	return run
}

// correctionFindingKey is the single fixed finding every phased corrector
// fixture answers: one key keeps the finding-set digest deterministic.
const correctionFindingKey = "000000000000000000000000000000a0"

func correctionFinding() factory.Finding {
	return factory.Finding{
		Key: correctionFindingKey, OriginRun: strings.Repeat("1", 32),
		Severity: factory.FindingSeverityMajor, Defect: "missing bound",
		Evidence: "line 7 lacks a check", RequiredOutcome: "add the check",
		FixKind: factory.FindingFixCode, Actionable: true,
	}
}

func correctionOutput(candidate string) string {
	finding := correctionFinding()
	digest, err := factory.FindingSetDigest([]string{finding.Key})
	if err != nil {
		panic(err)
	}
	result := factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionCorrect,
		Status: "completed", Summary: "correction recorded",
		Output: &factory.PhaseOutput{Correction: &factory.CorrectionReport{
			SetDigest: digest, NewCandidate: candidate,
			Findings:     []factory.Finding{finding},
			Dispositions: []factory.CorrectionDisposition{{Key: finding.Key, Disposition: factory.DispositionFixed}},
		}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// phaseTestRun builds a run record bound to the given assignment.
func phaseTestRun(a factory.Assignment, outcome factory.Outcome) factory.Run {
	return factory.Run{
		ID: a.Run, ProjectID: a.ProjectID, Role: a.Role, InputSHA: a.SourceCommit,
		Started: time.Unix(1700000000, 0), Deadline: time.Unix(1700003600, 0),
		Image: "img", Harness: a.Harness, Model: a.Model, Outcome: outcome,
	}
}

// Intent-graph doubles extracted for restored integration tests.

type generatedGraphFixture struct {
	forgejo.GraphCodec
	mu           sync.Mutex
	revision     int64
	actor        int64
	owner        bool
	records      map[string]sdk.OperationRecord
	issues       map[int64]sdk.NativeSnapshot
	submissions  int
	beforeSubmit func(factory.IntentGeneration) error
}

func (f *generatedGraphFixture) CheckIntentOwner(context.Context, int64, int64, int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.owner {
		return 0, errors.New("owner revoked")
	}
	return f.revision, nil
}

func (f *generatedGraphFixture) SubmitGraph(ctx context.Context, g factory.IntentGeneration, repo int64) (factory.GraphOperationOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beforeSubmit != nil {
		if err := f.beforeSubmit(g); err != nil {
			return factory.GraphOperationOutcome{}, err
		}
	}
	if old, ok := f.records[g.OperationID]; ok {
		return forgejo.GraphOutcomeFromNative(old), nil
	}
	var p sdk.IssueGraphPayload
	if err := json.Unmarshal(g.Payload, &p); err != nil {
		return factory.GraphOperationOutcome{}, err
	}
	if err := sdk.ValidateIssueGraphPayload(p); err != nil {
		return factory.GraphOperationOutcome{}, err
	}
	// Mirror the native writer: every edit anchor is validated before
	// anything is created, so a refused packet commits nothing.
	for _, edit := range p.Edits {
		issue := f.issues[edit.IssueID-100].Issue
		if issue == nil || issue.IsClosed || issue.Title != edit.ExpectedTitle || issue.Content != edit.ExpectedBody || issue.ContentVersion != edit.ExpectedContentVersion {
			return factory.GraphOperationOutcome{}, errors.New("invalid operation intent")
		}
	}
	f.submissions++
	f.revision++
	receipt := sdk.IssueGraphReceipt{ActorID: g.ActorID, RepositoryID: repo}
	ids := map[string]int64{}
	for _, c := range p.Creates {
		index := int64(len(f.issues) + 1)
		id := index + 100
		ids[c.Key] = id
		issue := &sdk.SnapshotIssue{ID: strconv.FormatInt(id, 10), Index: strconv.FormatInt(index, 10), Title: c.Title, Content: c.Body, TitleDigest: forgejo.ContentDigest(c.Title), ContentDigest: forgejo.ContentDigest(c.Body), Visible: true, Complete: true, Provenance: sdk.SnapshotCreationProvenance{PosterID: strconv.FormatInt(g.ActorID, 10)}}
		f.issues[index] = sdk.NativeSnapshot{RepositoryID: strconv.FormatInt(repo, 10), Issue: issue, Dependencies: &sdk.SnapshotDependencyPage{IssueID: issue.ID, Complete: true, Items: []sdk.SnapshotDependency{}}}
		receipt.Issues = append(receipt.Issues, sdk.IssueGraphIssueReceipt{Key: c.Key, IssueID: id, IssueIndex: index})
	}

	for _, edit := range p.Edits {
		index := edit.IssueID - 100
		snapshot := f.issues[index]
		issue := snapshot.Issue
		// Mirror the native writer guard: closed issues refuse edits
		// atomically, exactly like a title/body/version mismatch.
		if issue == nil || issue.IsClosed || issue.Title != edit.ExpectedTitle || issue.Content != edit.ExpectedBody || issue.ContentVersion != edit.ExpectedContentVersion {
			return factory.GraphOperationOutcome{}, errors.New("invalid operation intent")
		}
		if issue.Title != edit.Title {
			issue.Lifecycle = append(issue.Lifecycle, sdk.SnapshotLifecycleEvent{OccurrenceID: strconv.FormatInt(1000+f.revision, 10), Kind: "retitled", OldTitle: issue.Title, NewTitle: edit.Title})
		}
		issue.Title = edit.Title
		issue.TitleDigest = forgejo.ContentDigest(edit.Title)
		if issue.Content != edit.Body {
			issue.Content = edit.Body
			issue.ContentDigest = forgejo.ContentDigest(edit.Body)
			issue.ContentVersion++
		}
		receipt.Issues = append(receipt.Issues, sdk.IssueGraphIssueReceipt{IssueID: edit.IssueID, IssueIndex: index, ContentVersion: issue.ContentVersion})
	}
	resolve := func(r sdk.IssueGraphRef) int64 {
		if r.IssueID > 0 {
			return r.IssueID
		}
		return ids[r.Key]
	}
	for i, e := range p.Dependencies {
		id, dep := resolve(e.Issue), resolve(e.Prerequisite)
		snapshot := f.issues[id-100]
		if e.Remove {
			kept := []sdk.SnapshotDependency{}
			for _, old := range snapshot.Dependencies.Items {
				if old.DependencyID != strconv.FormatInt(dep, 10) {
					kept = append(kept, old)
				}
			}
			snapshot.Dependencies.Items = kept
		} else {
			snapshot.Dependencies.Items = append(snapshot.Dependencies.Items, sdk.SnapshotDependency{OccurrenceID: strconv.Itoa(i + 700 + int(f.revision)*100), IssueID: strconv.FormatInt(id, 10), DependencyID: strconv.FormatInt(dep, 10), Visible: true, Complete: true})
		}
		snapshot.Dependencies.Total = len(snapshot.Dependencies.Items)
		f.issues[id-100] = snapshot
		receipt.Dependencies = append(receipt.Dependencies, sdk.IssueGraphDependencyReceipt{IssueID: id, PrerequisiteID: dep, Removed: e.Remove})
	}
	raw, _ := json.Marshal(receipt)
	record := sdk.OperationRecord{OperationID: g.OperationID, ActorID: strconv.FormatInt(g.ActorID, 10), RepositoryID: strconv.FormatInt(repo, 10), Kind: sdk.OperationKindIssueGraph, Outcome: sdk.EffectCommitted, EffectState: sdk.EffectCommitted, CompletionState: sdk.CompletionComplete, Receipt: raw}
	f.records[g.OperationID] = record
	return forgejo.GraphOutcomeFromNative(record), nil
}

func (f *generatedGraphFixture) LookupGraph(_ context.Context, g factory.IntentGeneration, _ int64) (factory.GraphOperationLookup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.records[g.OperationID]
	if !ok {
		return forgejo.GraphLookupFromNative(sdk.OperationLookup{OperationID: g.OperationID, Status: sdk.BackgroundOutcomeNotObserved}), nil
	}
	return forgejo.GraphLookupFromNative(sdk.OperationLookup{OperationID: g.OperationID, Status: r.EffectState, Record: &r}), nil
}

func (f *generatedGraphFixture) CancelGraph(_ context.Context, id string) (factory.GraphOperationOutcome, error) {
	return forgejo.GraphOutcomeFromNative(sdk.OperationRecord{OperationID: id, EffectState: sdk.EffectNotCommitted, CancellationStatus: sdk.CancellationCancelled}), nil
}

func (f *generatedGraphFixture) ReadGeneratedIssue(_ context.Context, _, index, _ int64) (factory.GeneratedIssueObservation, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.issues[index]
	if !ok {
		return factory.GeneratedIssueObservation{}, 0, errors.New("missing issue")
	}
	observation, err := forgejo.GeneratedIssueObservationFromNative(s)
	return observation, f.revision, err
}

func (f *generatedGraphFixture) ReadAcceptanceEvidence(_ context.Context, _, index string, _ []string) (AcceptanceEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.ParseInt(index, 10, 64)
	s := f.issues[n]
	if s.Issue == nil {
		return AcceptanceEvidence{}, errors.New("missing issue")
	}
	i := s.Issue
	facts, err := forgejo.GeneratedIssueObservationFromNative(s)
	if err != nil {
		return AcceptanceEvidence{}, err
	}
	return AcceptanceEvidence{Revision: f.revision, Issue: AcceptanceIssueView{ID: i.ID, Index: i.Index, TitleDigest: i.TitleDigest, ContentDigest: i.ContentDigest, ContentVer: i.ContentVersion, TitleOccurrence: generatedTitleOccurrence(facts.Issue), Visible: true, PosterID: i.Provenance.PosterID}, Dependencies: generatedEdges(facts.Dependencies)}, nil
}

func broadJoinAcceptedRevision() factory.IntentRevision {
	idea := func(id, desc, consequence string) factory.ProductIdea {
		return factory.ProductIdea{ID: id, Description: desc, Consequence: consequence, Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
	}
	return factory.IntentRevision{
		ID: "d333333333333333333333333", Intent: "d444444444444444444444444",
		Owner: 7, Repository: 42, Objective: "Build a tracker", State: "accepted",
		MaxIssues: 10, PlanningCalls: 12, CreatedUnix: 1,
		Ideas: []factory.ProductIdea{
			idea("d555555555555555555555555", "Capture leave requests", "Requests can be recorded"),
			idea("d666666666666666666666666", "Managers review requests", "Decisions are visible"),
		},
		Tasks: []factory.IntentTask{
			{Key: choiceTaskKey(1), Objective: "Capture leave requests", Outcomes: []string{"Requests can be recorded"}, IdeaIDs: []string{"d555555555555555555555555"}},
			{Key: choiceTaskKey(2), Objective: "Validate leave requests", Outcomes: []string{"Invalid requests are rejected"}, IdeaIDs: []string{"d555555555555555555555555"}},
			{Key: choiceTaskKey(3), Objective: "Notify managers", Outcomes: []string{"Managers see pending reviews"}, IdeaIDs: []string{"d666666666666666666666666"}},
			{Key: choiceTaskKey(4), Objective: "Manager decision", Outcomes: []string{"Decisions are visible"}, IdeaIDs: []string{"d555555555555555555555555", "d666666666666666666666666"}, DependsOn: []string{choiceTaskKey(1), choiceTaskKey(2), choiceTaskKey(3)}},
		},
	}
}

func chosenFor(r factory.IntentRevision) ChosenIntentPlan {
	return ChosenIntentPlan{
		Intent: r.Intent, Revision: r.ID, Operation: factory.IntentPlanningOptions,
		Key: "candidate-one", Tasks: append([]factory.IntentTask(nil), r.Tasks...),
	}
}

func submittedKeys(tasks []factory.IntentTask) []string {
	keys := make([]string, 0, len(tasks))
	for _, task := range tasks {
		keys = append(keys, task.Key)
	}
	return keys
}

// The choice path adopts broad roots with a join exactly: every chosen key
// is submitted, every payload create names a chosen key, and no serving
// judgment evaluates.
// candidateChoiceStub is the deterministic stand-in for the funded
// candidate choice: it selects one planner alternative with an explicit
// confidence, counting calls so tests prove reservation caching (replays
// spend nothing further).
type candidateChoiceStub struct {
	selected   string
	confidence float64
	calls      int
	err        error
}

func (s *candidateChoiceStub) Select(_ context.Context, _ any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	s.calls++
	if s.err != nil {
		return factory.PriorityAnswer{}, s.err
	}
	selected := s.selected
	if _, ok := choices[selected]; !ok {
		selected = "cand-a"
	}
	share := (1 + s.confidence) / 2
	answer := factory.PriorityAnswer{Selected: selected, Model: "synthetic-model", Confidence: s.confidence, Probabilities: map[string]float64{}}
	for id := range choices {
		answer.Probabilities[id] = (1 - share) / float64(len(choices)-1)
	}
	answer.Probabilities[selected] = share
	return answer, nil
}

// choiceDraftHead builds the reviewed draft behind an accepted revision:
// same intent and choices, draft state, no tasks yet.
func choicePlannerOutput(draft factory.IntentRevision, keys ...string) factory.SemanticPlannerOutput {
	tasks := append([]factory.IntentTask(nil), broadJoinAcceptedRevision().Tasks...)
	out := factory.SemanticPlannerOutput{Intent: draft.Intent, Revision: draft.ID, Operation: factory.IntentPlanningOptions}
	for _, key := range keys {
		out.Candidates = append(out.Candidates, factory.SemanticCandidate{Key: key, Rationale: "Cohesive feature issues with independent roots joined once.", Tasks: tasks})
	}
	walkthroughs := choicePlanWalkthroughs(draft)
	out.Walkthroughs = &walkthroughs
	return out
}

// choicePlanWalkthroughs authors real-shaped planning-agent walkthroughs
// bound to the reviewed draft: facts preserved from the draft objective
// and two alternatives covering the authorized outcomes, or facts-only
// prose when the objective falls outside the authored representative
// coverage (nothing to consolidate then, matching the serving review
// rule). The content passes planning validation and the deterministic
// effect scan, so only the recorded review decides the serving gate.
func choicePlanWalkthroughs(draft factory.IntentRevision) factory.ProposalContent {
	set := factory.DescribeAlternatives(draft.Objective)
	basis, err := factory.ComposeProposalContent(draft)
	if err != nil {
		panic("choice walkthrough basis invalid: " + err.Error())
	}
	// The planning agent authors its own alternatives for any objective,
	// even outside the authored representative coverage: planning
	// validation requires supported walkthroughs, and consolidation can
	// only review an actual comparison.
	content := factory.ProposalContent{
		Producer: factory.PlanningProducer, Supported: true,
		Intent: draft.Intent, Revision: draft.ID, Objective: draft.Objective,
		Facts: basis.Facts, Excluded: basis.Excluded, Exclusions: basis.Exclusions,
		Unknowns: set.Unknowns,
	}
	for _, key := range []string{"guided", "list"} {
		content.Alternatives = append(content.Alternatives, factory.ProductAlternative{
			Key: key, Name: key + " leave review", Audience: "Managers and their team",
			Summary:      "Track leave requests with a visible review step.",
			Steps:        []factory.WalkthroughStep{{Actor: "The person", Action: "records the leave request", Result: "The request is tracked"}},
			Visibility:   "The person and the reviewer see the request.",
			Consequences: "Reviews take reviewer time.",
			Evidence:     "The recorded draft and inspected source.",
			Outcomes:     []string{draft.Objective}, IdeaIDs: append([]string(nil), draft.EffectiveOutcomes...),
		})
	}
	if err := content.Validate(); err != nil {
		panic("choice walkthroughs invalid: " + err.Error())
	}
	if err := factory.ValidatePlanningWalkthroughs(draft, content); err != nil {
		panic("choice walkthrough binding invalid: " + err.Error())
	}
	return content
}

// reviewSeedingStub records the explicit walkthrough check a serving test
// replays: deterministic selection keeps every receipt clean without
// funding further judgments.
type reviewSeedingStub struct{}

func (reviewSeedingStub) Select(_ context.Context, _ any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	selected := ""
	for key := range choices {
		if key == "consistent" {
			selected = key
			break
		}
		if selected == "" || key < selected {
			selected = key
		}
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

// seedChoiceWalkthroughReview records the explicit walkthrough check a
// serving test replays: consolidation and consistency run once through
// the deterministic stub, so ResolveAcceptedPlanChoice replays them clean
// without funding new judgments. The reservation proves the draft head,
// so seed before the accepted successor replaces it.
func seedChoiceWalkthroughReview(t *testing.T, ctx context.Context, c *Coordinator, head factory.IntentRevision, content factory.ProposalContent) {
	t.Helper()
	seed := &Coordinator{Store: c.Store, Planning: &PlanningJudgments{Chooser: reviewSeedingStub{}}}
	recommended, err := seed.JudgeConsolidation(ctx, head, content)
	if err != nil {
		t.Fatalf("walkthrough consolidation not recorded: %v", err)
	}
	if _, err := seed.JudgeConsistency(ctx, head, content, recommended); err != nil {
		t.Fatalf("walkthrough consistency not recorded: %v", err)
	}
}

func choiceTaskKey(n int) string { return fmt.Sprintf("d%024x", 0xc000+n) }

func settleStubs(host *stubHost, broker *stubBroker) {
	host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryStopped, Retirement: "confirmed", Reason: "stopped", LeaseID: "lease-" + in.ID, Generation: 3, CredentialReturned: true}, nil
	}
	broker.close = func(string, string) error { return nil }
	broker.get = func(kind, id string) (identity.Execution, error) {
		return identity.Execution{
			Kind: kind, ExecutionID: id, State: identity.ExecutionTerminal, Digest: "digest", LeaseID: "lease-" + id,
			Binding: &identity.Binding{Kind: identity.Factory, ID: id, Generation: 3},
		}, nil
	}
}
