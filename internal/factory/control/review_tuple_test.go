package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory/execution"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

type deliveryPGHost struct{ HostFactory }

func (h deliveryPGHost) ReadWorkspaceContext(_ context.Context, scoped execution.PreparationContextRequest) (execution.PreparationContext, error) {
	in, role := scoped.Input, scoped.Role
	result := fixturePreparationContext(in, role)
	result.Diff = []byte("diff --git a/native-proof.txt b/native-proof.txt\n--- a/native-proof.txt\n+++ b/native-proof.txt\n@@ -1 +1 @@\n-old\n+the accepted behavior is implemented\n")
	return execution.PreparationContext{Material: result}, nil
}

type deliveryPGMerger struct {
	MergeExecutor
	material factory.DeliveryNativeMaterial
}

func (m *deliveryPGMerger) ReadDeliveryMaterial(_ context.Context, req factory.MergeWork) (factory.DeliveryNativeMaterial, error) {
	out := m.material
	out.Repository, out.PRID, out.IssueID = req.Repository, req.PRID, req.IssueID
	out.Head, out.Base = req.HeadOID, req.BaseOID
	return out, nil
}

type deliveryPGTransport struct {
	target *url.URL
	base   http.RoundTripper
}

type deliveryPGResolver struct {
	outcome factory.OperationOutcome
	cancels int
}

func (r *deliveryPGResolver) SubmitResolution(context.Context, factory.ResolveWork) (factory.OperationOutcome, error) {
	return r.outcome, nil
}

func (r *deliveryPGResolver) LookupResolution(context.Context, string) (factory.OperationOutcome, error) {
	return r.outcome, nil
}

func (r *deliveryPGResolver) CancelResolution(_ context.Context, id string) (factory.OperationOutcome, error) {
	r.cancels++
	return factory.OperationOutcome{OperationID: id, InstallationID: "native-fixture", Effect: factory.OpEffectNotCommitted, Cancellation: factory.OpCancelCancelled}, nil
}

func (r *deliveryPGResolver) ConfirmResolution(_ context.Context, operation factory.DeliveryResolutionOperation) (bool, error) {
	return operation.Outcome.Effect == factory.OpEffectCommitted, nil
}

// The native adapter has a separate actual retained-VM proof. This journey
// exercises the real publication CAS, recovery selectors, immutable work,
// native receipt adoption, confirmed readback and cancellation tombstone.
func TestDeliveryResolutionJournalRecoveryPG(t *testing.T) {
	ctx := context.Background()
	fx, p := mergeSeed(t, 43)
	db, coordinator := fx.publish.db, fx.publish.coord
	firstID, secondID := factory.NewID(), factory.NewID()
	p, acceptance, _, _ := unionPgAdmitJev(t, ctx, db, p, factory.NewID(), []string{firstID, secondID})
	request := factory.ResolveRequest{Repository: p.Repository, PRNumber: p.PRNumber, PRID: p.PRID, RootCommentID: 11, ActorID: 6, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID, Digest: phaseTestSHA("unchanged conversation"), Resolved: true}
	first := factory.DeliveryResolutionOperation{AcceptanceID: acceptance.ID, FindingKey: correctionTestKey(1), ResolvedDigest: phaseTestSHA("expected root-only resolved conversation"), Work: factory.ResolveWork{Request: request, Binding: factory.ActorBindingRef{ActorID: 6, TokenID: 2, Kind: factory.OpThreadResolve}, PRAuthorID: p.PRCreate.Work.ActorID, OperationID: firstID, AuthRevision: "delivery:" + acceptance.ID, NativeRevision: 9, NotAfter: time.Now().Add(time.Minute).Unix()}}
	p.ResolutionOperations = append(p.ResolutionOperations, first)
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	open, err := db.OpenPublications(ctx, 16)
	if err != nil || len(open) != 1 || open[0].ID != p.ID {
		t.Fatalf("pending resolution absent from restart work: count=%d err=%v", len(open), err)
	}
	outstanding, err := db.OutstandingPublications(ctx, p.Repository, 16)
	if err != nil || len(outstanding) != 1 || outstanding[0].ID != p.ID {
		t.Fatalf("pending resolution absent from withdrawal work: count=%d err=%v", len(outstanding), err)
	}
	resolver := &deliveryPGResolver{outcome: factory.OperationOutcome{OperationID: firstID, InstallationID: "native-fixture", Kind: factory.OpThreadResolve, ActorID: request.ActorID, RepositoryID: p.Repository, Effect: factory.OpEffectCommitted, Cancellation: factory.OpCancelNone, Completion: factory.OpCompletionComplete, Receipt: []byte(`{"root_comment_id":11,"resolved":true}`)}}
	coordinator.Resolutions = resolver
	if err := coordinator.reconcileDeliveryResolution(ctx, &p, 0, false); err != nil {
		t.Fatal(err)
	}
	stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil || !stored.ResolutionOperations[0].Confirmed || string(stored.ResolutionOperations[0].Outcome.Receipt) != string(resolver.outcome.Receipt) {
		t.Fatalf("reconciled native resolution lost: %+v err=%v", stored.ResolutionOperations, err)
	}
	changed := stored
	changed.ResolutionOperations = append([]factory.DeliveryResolutionOperation(nil), stored.ResolutionOperations...)
	changed.ResolutionOperations[0].Work.Request.Digest = phaseTestSHA("different conversation")
	changed.Revision++
	if err := db.UpdatePublication(ctx, changed); err == nil {
		t.Fatal("native resolution work changed after registration")
	}
	dropped := stored
	dropped.ResolutionOperations = nil
	dropped.Revision++
	if err := db.UpdatePublication(ctx, dropped); err == nil {
		t.Fatal("native resolution receipt disappeared")
	}
	second := first
	second.Work.OperationID, second.Work.Request.RootCommentID = secondID, 12
	stored.ResolutionOperations = append(stored.ResolutionOperations, second)
	stored.Revision++
	if err := db.UpdatePublication(ctx, stored); err != nil {
		t.Fatal(err)
	}
	resolver.outcome = factory.OperationOutcome{NotObserved: true}
	if err := coordinator.reconcileDeliveryResolution(ctx, &stored, 1, true); err != nil || resolver.cancels != 1 {
		t.Fatalf("unobserved resolution did not cancel by exact identity: err=%v calls=%d", err, resolver.cancels)
	}
	settled, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil || settled.ResolutionOperations[1].Outcome.Cancellation != factory.OpCancelCancelled || settled.ResolutionOperations[1].Confirmed {
		t.Fatalf("cancel tombstone not retained: %+v err=%v", settled.ResolutionOperations, err)
	}
	open, err = db.OpenPublications(ctx, 16)
	if err != nil || len(open) != 0 {
		t.Fatalf("settled resolution stayed restart work: count=%d err=%v", len(open), err)
	}
}

func (t deliveryPGTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host = t.target.Scheme, t.target.Host
	return t.base.RoundTrip(copy)
}

// Exercise the production full-material builder, two-choice HTTP adapter,
// PostgreSQL funding/receipt/acceptance and ordinary merge authorization.
// Native source/Forgejo observations are scripted; their installed owners
// are qualified separately, never claimed by this database journey.
func TestFundedDeliveryServingUsesCompleteMaterialPG(t *testing.T) {
	for _, choice := range []string{factory.DeliveryReadyToMerge, factory.DeliveryReviewAgain} {
		t.Run(choice, func(t *testing.T) { fundedDeliveryServingUsesCompleteMaterialPG(t, choice) })
	}
}

func fundedDeliveryServingUsesCompleteMaterialPG(t *testing.T, choice string) {
	ctx := context.Background()
	fx, owner, p, run := reviewRunSeed(t, true)
	plan := reportedInvestigationPlan(owner)
	if plan == nil {
		t.Fatal("owner has no admitted plan")
	}
	if err := fx.db.ReplacePlanHead(ctx, *plan, ""); err != nil {
		t.Fatal(err)
	}
	reviewer, err := fx.db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	finding := correctionTestFinding(1, false, factory.FindingFixCode)
	finding.Defect = "Optional wording refinement; the accepted behavior already works."
	cycleID := reviewerCycleID(p, reviewer)
	reviewer = accountChildResult(t, fx, run, reviewVerdictOutput(cycleID, finding))
	commitReviewerOperation(t, fx, &p, reviewer)
	p.Tuple = &factory.CandidateTuple{Head: p.Candidate, Base: p.PRCreate.BaseOID, DiffBase: p.PRCreate.BaseOID}
	p.Revision++
	if err := fx.db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	fx.coord.Reviews = &fakeReviewer{}
	fx.coord.Host = deliveryPGHost{HostFactory: fx.coord.Host}
	fx.coord.DispatchReads = fx.seed.reads
	key := strconv.FormatInt(p.Repository, 10) + "/" + strconv.FormatInt(p.Issue, 10)
	inputs := fx.seed.reads.inputs[key]
	inputs.Revision = 9
	inputs.Tip = p.PRCreate.BaseOID
	fx.seed.reads.inputs[key] = inputs
	fx.coord.Checks = happyCheckObserver()
	checks := fx.coord.AssessPublicationChecks(ctx, fx.coord.Checks, 7, owner.ID)
	if len(checks.Assessed) != 1 || len(checks.Errors) != 0 {
		t.Fatalf("checks: %+v", checks)
	}
	merger := &deliveryPGMerger{MergeExecutor: happyMerger(), material: factory.DeliveryNativeMaterial{Revision: 9, Title: "Actual PR title", Body: "The current PR implements the accepted outcome.", Eligible: true, Reviews: []factory.DeliveryNativeReview{{ID: "31", ActorID: "6", Type: "COMMENT", Content: finding.Defect, Digest: phaseTestSHA(finding.Defect)}}, Comments: []factory.DeliveryComment{{ID: "41", Content: finding.Defect, Digest: phaseTestSHA(finding.Defect)}}}}
	fx.coord.Merges = merger
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			State     deliveryJudgmentMaterial `json:"state"`
			Questions map[string]any           `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.State.validate() != nil || !strings.Contains(request.State.Source.Diff, "the accepted behavior is implemented") || request.State.Native.Reviews[0].Content != finding.Defect || len(request.State.Findings) != 1 || request.State.Accepted.Issue.Body == "" || len(request.Questions) != 1 {
			t.Errorf("provider did not receive complete semantic material: decode=%v", err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		probabilities := map[string]float64{factory.DeliveryReviewAgain: 0, factory.DeliveryReadyToMerge: 0}
		probabilities[choice] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-fixture-resolved", "answers": map[string]any{"priority": map[string]any{"type": "choice", "choice": choice, "confidence": 1, "probabilities": probabilities}}, "usage": map[string]any{"input_tokens": 17, "output_tokens": 5}})
	}))
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "typesafe.key")
	if err := os.WriteFile(keyPath, []byte("integration-only-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := url.Parse(server.URL)
	chooser := &TypeSafePriority{APIKeyFile: keyPath, Model: "jev-fixture", Client: &http.Client{Transport: deliveryPGTransport{target: endpoint, base: http.DefaultTransport}}}
	fx.coord.Planning = &PlanningJudgments{Chooser: chooser}
	review, ready, pending, refused := fx.coord.currentReviewReport(ctx, p)
	if !ready || pending || refused {
		t.Fatalf("current typed review: %v %v %v", ready, pending, refused)
	}
	if routeDeliveryFeedback(publicationDeliveryFeedback(p, review, nil)) != routeJev {
		t.Fatal("optional-only feedback forced a coder run")
	}
	decision, _, err := fx.coord.advisePublicationDelivery(ctx, p, review)
	if err != nil || decision.Effective != choice || calls != 1 || decision.Answer.ProviderModel != "jev-fixture-resolved" {
		material, _, _, materialErr := fx.coord.deliveryMaterial(ctx, p, review, chooser.DeliveryModel())
		t.Fatalf("production delivery: decision=%+v calls=%d err=%v material=%+v materialErr=%v", decision, calls, err, material, materialErr)
	}
	if choice == factory.DeliveryReviewAgain {
		in, stopped := fx.coord.resolvePublicationChildReview(ctx, p, &PublishReport{})
		if stopped || in.role != project.RoleReviewer || in.deliveryDecisionID == "" || calls != 1 {
			t.Fatalf("paid review choice did not reach independent reviewer route: %+v stop=%v calls=%d", in, stopped, calls)
		}
		report := PublishReport{}
		digest, inputDigest, trigger, reason, stopped := childCycleDigests(p, project.RoleReviewer, in, &report)
		if stopped || trigger != "funded-review-again" {
			t.Fatalf("paid choice did not bind a new review cause: %+v", report)
		}
		cycle, childID, _, err := fx.coord.ensureReviewCycle(ctx, &p, owner, project.RoleReviewer, digest, inputDigest, trigger, reason)
		if err != nil || cycle.ReviewerAssignment != childID || childID == reviewer.ID {
			t.Fatalf("paid choice reused old reviewer: %+v err=%v", cycle, err)
		}
		if fx.coord.chargePublicationChildReview(ctx, p, owner, childID, false, &report) || fx.coord.chargePublicationChildReview(ctx, p, owner, childID, false, &report) {
			t.Fatalf("funded reviewer allowance failed: %+v", report)
		}
		allowance, err := fx.db.AttemptAllowance(ctx, p.Repository, p.Issue)
		if err != nil || len(allowance.Reviews) != 1 || calls != 1 {
			t.Fatalf("review replay charged again: calls=%d allowance=%+v err=%v", calls, allowance, err)
		}
		stored, err := fx.db.PublicationByAssignment(ctx, owner.ID)
		if err != nil || len(stored.DeliveryAcceptances) != 0 {
			t.Fatalf("review_again manufactured acceptance: %+v err=%v", stored.DeliveryAcceptances, err)
		}
		return
	}
	stored, err := fx.db.PublicationByAssignment(ctx, owner.ID)
	if err != nil || len(stored.DeliveryAcceptances) != 1 || stored.DeliveryAcceptances[0].Kind != factory.AcceptanceJevAcceptance {
		t.Fatalf("funded acceptance missing: %+v err=%v", stored.DeliveryAcceptances, err)
	}
	if _, _, err := fx.coord.advisePublicationDelivery(ctx, stored, review); err != nil || calls != 1 {
		t.Fatalf("unchanged material paid again: calls=%d err=%v", calls, err)
	}
	acceptance, _, err := fx.coord.currentFundedDeliveryAcceptance(ctx, stored)
	if err != nil || acceptance == nil {
		t.Fatalf("current acceptance: %+v err=%v", acceptance, err)
	}
	merger.material.Comments[0].Content = "A new human reply changes coverage."
	merger.material.Comments[0].Digest = phaseTestSHA(merger.material.Comments[0].Content)
	if acceptance, _, err := fx.coord.currentFundedDeliveryAcceptance(ctx, stored); err == nil || acceptance != nil || calls != 1 {
		t.Fatal("changed semantic material reused the funded acceptance or paid during a read")
	}
	merger.material.Comments[0].Content, merger.material.Comments[0].Digest = finding.Defect, phaseTestSHA(finding.Defect)
	merger.material.Eligible, merger.material.Blocker = false, "protected_files"
	if _, _, err := fx.coord.advisePublicationDelivery(ctx, stored, review); err == nil || calls != 1 {
		t.Fatal("native protection bought another judgment")
	}
	merger.material.Eligible, merger.material.Blocker = true, ""
	merged := fx.coord.MergePass(ctx)
	if len(merged.Merged) != 1 || len(merged.Errors) != 0 {
		t.Fatalf("production funded acceptance did not authorize ordinary merge: %+v", merged)
	}
	row, err := fx.db.MergeByPublication(ctx, p.ID)
	if err != nil || row.Authorization.Generation.AcceptanceKind != factory.AcceptanceJevAcceptance || row.Authorization.Generation.DecisionID == "" || row.Authorization.Decision.Answer.ProviderModel != "jev-fixture-resolved" || calls != 1 {
		t.Fatalf("funded merge binding lost: %+v err=%v calls=%d", row.Authorization, err, calls)
	}
}

// Candidate-tuple seeding at review adoption (R20) against real
// PostgreSQL: the first honestly observed merge-base persists as the
// publication's candidate tuple at revision zero, a later review
// observing a new base advances it under CAS, and unobserved,
// moved-base receipts seed nothing. Every test below opens its own
// owned disposable database.

// tupleReviewSeed records the reviewer report and installs a reviewer
// double whose adopted receipt carries the given merge-base.
func tupleReviewSeed(t *testing.T, p factory.Publication, fx *publishFixture, runID, mergeBase string) *fakeReviewer {
	t.Helper()
	exec := &fakeReviewer{
		observed: factory.ReviewObservation{NativeRev: 9, ObservedUnix: time.Now().Unix()},
		outcome:  committedOutcome(`{"review_id":3}`),
		adopted: factory.ReviewOutcome{
			ReviewID: 3, CommentID: 4, ReviewerID: 6, PRID: p.PRID,
			PRNumber: p.PRNumber, IssueID: p.PRCreate.IssueID,
			HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID,
			CommitID: p.Candidate, Event: "APPROVED", MergeBaseOID: mergeBase,
		},
	}
	exec.adopted.Operation = exec.outcome
	fx.coord.Reviews = exec
	ctx := context.Background()
	view, err := fx.db.FactoryRunView(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := fx.db.Assignment(ctx, view.Attempt)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := fx.db.PublicationByAssignment(ctx, assignment.PublicationAssignment)
	if err != nil {
		t.Fatal(err)
	}
	cycleID := reviewerCycleID(pub, assignment)
	if cycleID == "" {
		t.Fatalf("reviewer admitted no review cycle: reviewer=%s", assignment.ID)
	}
	content, err := factory.FindingContentDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := factory.ClearCoverageDigest(assignment.SourceCommit, pub.PRCreate.BaseOID, content)
	if err != nil {
		t.Fatal(err)
	}
	output := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed",
		Summary: "solid",
		Output: &factory.PhaseOutput{Clear: &factory.ClearReviewReport{
			Conclusion: factory.ClearReviewConclusion, CycleID: cycleID,
			Head: assignment.SourceCommit, BaseOID: pub.PRCreate.BaseOID,
			CoverageDigest: coverage,
		}},
	})
	run, err := fx.db.FactoryRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	accountChildResult(t, fx, run, output)
	return exec
}

// A real PostgreSQL serving pass journals a settled independent clear
// before CI passes, then binds its zero-call acceptance and merges through
// the ordinary native executor after exact current checks pass. Native
// review/merge adapters are scripted; there is no test-only AuthorizeMerge
// call and no funded-delivery fixture standing in for production.
func TestCleanReviewServingAuthorizesMergeWithoutDeliveryCallsPG(t *testing.T) {
	ctx := context.Background()
	fx, owner, publication, run := reviewRunSeed(t, true)
	// This legacy publication fixture skips implementation admission;
	// retain the investigation plan that production admits before coding.
	plan := reportedInvestigationPlan(owner)
	if plan == nil {
		t.Fatal("publication owner has no investigation plan")
	}
	if err := fx.db.ReplacePlanHead(ctx, *plan, ""); err != nil {
		t.Fatal(err)
	}
	tupleReviewSeed(t, publication, fx, run.ID, strings.Repeat("d", 40))
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	child, err := fx.db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := fx.db.Preparation(ctx, child.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := BuildMCPCapability(child, run, preparation)
	if err != nil {
		t.Fatal(err)
	}
	if err = fx.db.StageMCPCapability(ctx, cap); err != nil {
		t.Fatal(err)
	}
	required, err := fx.db.FactoryCurrentContext(ctx, cap, time.Now())
	if err != nil || required.Role.Review == nil || len(required.Role.Review.Heads) != 1 || required.Role.Review.Heads[0].RunID != run.ID || required.Role.Review.Heads[0].HeadOID != publication.Candidate || required.Role.Review.CurrentTuple == nil || required.Role.Review.CurrentTuple.DiffBase != strings.Repeat("d", 40) {
		t.Fatalf("review context lost its authorized native operation: %v", err)
	}
	carriedDigest, err := factory.FindingContentDigest(required.Role.Review.CarriedFindings)
	if err != nil {
		t.Fatal(err)
	}
	coverageDigest, err := factory.ClearCoverageDigest(required.Role.Review.Cycle.Head, required.Role.Review.Cycle.BaseOID, carriedDigest)
	if err != nil || required.Role.Review.ClearCoverageDigest != coverageDigest || !factory.ValidDigest(coverageDigest) {
		t.Fatal("required review context omitted the exact canonical clear coverage binding")
	}
	merger := happyMerger()
	fx.coord.Merges = merger
	first := fx.coord.MergePass(ctx)
	if len(first.Merged) != 0 || len(first.Errors) != 0 {
		t.Fatalf("merge advanced without CI: %+v", first)
	}
	stored, err := fx.db.PublicationByAssignment(ctx, owner.ID)
	if err != nil || len(stored.DeliveryAcceptances) != 1 || stored.DeliveryAcceptances[0].Kind != factory.AcceptanceCleanReview {
		_, _, admissionErr := fx.coord.recordCleanDeliveryAcceptance(ctx, stored)
		t.Fatalf("clean acceptance not journaled before checks: count=%d err=%v admission=%v", len(stored.DeliveryAcceptances), err, admissionErr)
	}
	policy, err := fx.db.RepositoryPolicy(ctx, publication.Repository)
	if err != nil {
		t.Fatal(err)
	}
	assessment := factory.CheckAssessment{
		Results: []factory.CheckResult{{Context: "ci", State: factory.CheckStateSuccess, Passed: true}},
		Checks:  policy.Checks, Repository: publication.Repository, PRNumber: publication.PRNumber,
		PRID: publication.PRID, IssueID: publication.PRCreate.IssueID, PolicyRevision: policy.Revision,
		NativeRev: 9, AssessedUnix: time.Now().Unix(), ObservedContexts: 1,
		HeadRef: publication.PRCreate.HeadRef, BaseRef: publication.PRCreate.BaseRef,
		HeadOID: publication.Candidate, BaseOID: publication.PRCreate.BaseOID,
		ChecksDigest: factory.ChecksDigest(policy.Checks), Verdict: factory.CheckPass, Reason: factory.CheckReasonPass,
	}
	stale := assessment
	stale.PolicyRevision--
	if _, err := fx.db.RecordCheckAssessment(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if fx.coord.checkCurrent(ctx, stored) {
		t.Fatal("obsolete policy check assessment remained current")
	}
	staleContext, err := fx.db.FactoryCurrentContext(ctx, cap, time.Now())
	if err != nil || staleContext.Role.Review == nil || staleContext.Role.Review.CheckAssessment != nil {
		t.Fatalf("review context exposed stale-policy CI as current: %v", err)
	}
	beforeCI, err := factory.CurrentRequiredSemanticDigest(staleContext.Role)
	if err != nil {
		t.Fatal(err)
	}
	checks := happyCheckObserver()
	fx.coord.Checks, fx.coord.Merges = checks, nil
	checked := fx.coord.CheckPass(ctx)
	if len(checked.Assessed) != 1 || len(checked.Errors) != 0 || len(checks.targets) != 1 {
		t.Fatalf("check serving pass did not refresh obsolete policy evidence: %+v", checked)
	}
	freshContext, err := fx.db.FactoryCurrentContext(ctx, cap, time.Now())
	if err != nil || freshContext.Role.Review == nil || freshContext.Role.Review.CheckAssessment == nil || freshContext.Role.Review.CheckAssessment.PolicyRevision != policy.Revision || freshContext.Role.Review.CheckAssessment.Verdict != factory.CheckPass {
		t.Fatalf("review context omitted exact current native CI evidence: %v", err)
	}
	afterCI, err := factory.CurrentRequiredSemanticDigest(freshContext.Role)
	if err != nil || beforeCI != afterCI {
		t.Fatal("CI progress changed the admitted reviewer context choice binding")
	}
	fx.coord.Merges = merger
	second := fx.coord.MergePass(ctx)
	if len(second.Merged) != 1 || len(second.Errors) != 0 {
		current, _ := fx.db.PublicationByAssignment(ctx, owner.ID)
		_, _, admissionErr := fx.coord.recordCleanDeliveryAcceptance(ctx, current)
		t.Fatalf("clean acceptance did not authorize its merge: %+v admission=%v", second, admissionErr)
	}
	merged, err := fx.db.MergeByPublication(ctx, publication.ID)
	if err != nil || merged.Stage != factory.MergeMerged || !merged.Authorization.Bound() || merged.Authorization.Generation.AcceptanceKind != factory.AcceptanceCleanReview || merged.Authorization.Generation.DiffBase != strings.Repeat("d", 40) {
		t.Fatalf("merge lost its observed clean binding: %+v %v", merged.Authorization, err)
	}
	fx.coord.MergePass(ctx)
	if len(merger.submits) != 1 {
		t.Fatalf("replay submitted %d merge operations", len(merger.submits))
	}
}

func readTuple(t *testing.T, fx *publishFixture, assignmentID string) *factory.CandidateTuple {
	t.Helper()
	p, err := fx.db.PublicationByAssignment(context.Background(), assignmentID)
	if err != nil {
		t.Fatal(err)
	}
	return p.Tuple
}

func TestSubmitReviewForRunSeedsCandidateTuple(t *testing.T) {
	ctx := context.Background()
	fx, a, p, run := reviewRunSeed(t, true)
	tupleReviewSeed(t, p, fx, run.ID, strings.Repeat("d", 40))
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatalf("submit refused: %v", err)
	}
	tuple := readTuple(t, fx, a.ID)
	if tuple == nil {
		t.Fatal("observed merge-base seeded nothing")
	}
	want := factory.CandidateTuple{Head: p.Candidate, Base: p.PRCreate.BaseOID, DiffBase: strings.Repeat("d", 40), Revision: 0}
	if *tuple != want {
		t.Fatalf("tuple = %+v, want %+v", tuple, want)
	}
}

func TestSubmitReviewForRunSkipsTupleWithoutMergeBase(t *testing.T) {
	ctx := context.Background()
	fx, a, p, run := reviewRunSeed(t, true)
	tupleReviewSeed(t, p, fx, run.ID, "")
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatalf("submit refused: %v", err)
	}
	if tuple := readTuple(t, fx, a.ID); tuple != nil {
		t.Fatalf("unobserved merge-base seeded %+v", tuple)
	}
}

func TestSubmitReviewForRunAdvancesTupleWithoutChurn(t *testing.T) {
	ctx := context.Background()
	fx, a, p, run := reviewRunSeed(t, true)
	exec := tupleReviewSeed(t, p, fx, run.ID, strings.Repeat("d", 40))
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	exec.adopted.MergeBaseOID = strings.Repeat("e", 40)
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	tuple := readTuple(t, fx, a.ID)
	want := factory.CandidateTuple{Head: p.Candidate, Base: p.PRCreate.BaseOID, DiffBase: strings.Repeat("e", 40), Revision: 1}
	if tuple == nil || *tuple != want {
		t.Fatalf("tuple = %+v, want %+v", tuple, want)
	}
	before, err := fx.db.PublicationByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	after, err := fx.db.PublicationByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Tuple == nil || *after.Tuple != want {
		t.Fatalf("repeat submit churned: revision %d->%d tuple=%+v", before.Revision, after.Revision, after.Tuple)
	}
}

func TestSubmitReviewForRunSkipsTupleOnMovedBase(t *testing.T) {
	ctx := context.Background()
	fx, a, _, run := reviewRunSeed(t, true)
	p, err := fx.db.PublicationByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	exec := tupleReviewSeed(t, p, fx, run.ID, strings.Repeat("d", 40))
	exec.adopted.BaseOID = strings.Repeat("c", 40)
	if _, err := fx.coord.SubmitReviewForRun(ctx, run.ID); err != nil {
		t.Fatalf("submit refused: %v", err)
	}
	if tuple := readTuple(t, fx, a.ID); tuple != nil {
		t.Fatalf("moved base seeded %+v", tuple)
	}
}
