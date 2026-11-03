package control

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// recordCleanDeliveryAcceptance connects the settled independent clear
// result and its committed native review to the existing D07 constructor.
// It spends no delivery call. The acceptance binds the required check
// set while native checks may still be pending; passing current checks
// remain mandatory at merge authorization and native submission.
func (c *Coordinator) recordCleanDeliveryAcceptance(ctx context.Context, publication factory.Publication) (factory.Publication, *factory.DeliveryAcceptance, error) {
	verdict, ready, pending, refused := c.currentReviewReport(ctx, publication)
	if !ready || pending || refused || verdict.Clear == nil {
		return publication, nil, nil
	}
	cycle, ok := findReviewCycle(publication, verdict.Clear.CycleID)
	if !ok {
		return publication, nil, errors.New("clear delivery lacks its recorded review cycle")
	}
	reviewer, err := c.Store.Assignment(ctx, cycle.ReviewerAssignment)
	if err != nil {
		return publication, nil, err
	}
	run, err := c.Store.FactoryRun(ctx, reviewer.Run)
	if err != nil || !run.Reconciled || run.Outcome != factory.Succeeded {
		return publication, nil, errors.New("clear delivery reviewer retirement is unconfirmed")
	}
	if publication.Tuple == nil || publication.Tuple.Validate() != nil || publication.Tuple.Head != publication.Candidate || publication.Tuple.Base != publication.PRCreate.BaseOID {
		return publication, nil, errors.New("clear delivery lacks its observed current candidate tuple")
	}
	owner, err := c.Store.Assignment(ctx, publication.AssignmentID)
	if err != nil || owner.AttemptRoot != cycle.AttemptRoot || owner.Acceptance != publication.Acceptance {
		return publication, nil, errors.New("clear delivery lacks its publication owner")
	}
	head, _, err := c.Store.PlanHead(ctx, owner.AttemptRoot)
	if err != nil {
		return publication, nil, err
	}
	plan, err := c.Store.InvestigationPlan(ctx, owner.AttemptRoot, head)
	if err != nil || plan.Validate() != nil {
		return publication, nil, errors.New("clear delivery lacks its admitted plan scope")
	}
	policy, err := c.Store.RepositoryPolicy(ctx, publication.Repository)
	if err != nil {
		return publication, nil, err
	}
	carried, err := c.reviewCarriedFindings(ctx, publication, cycle)
	if err != nil {
		return publication, nil, err
	}
	updated, acceptance, err := AdmitClearReview(ClearAdmissionInput{
		Publication: publication, Cycle: cycle, Reviewer: reviewer, Carried: carried,
		ScopeDigest: plan.ScopeDigest, PolicyRevision: strconv.FormatInt(policy.Revision, 10),
		CheckDigest: factory.ChecksDigest(policy.Checks), DiffBase: publication.Tuple.DiffBase,
	})
	if err != nil {
		return publication, nil, err
	}
	if len(updated.DeliveryAcceptances) != len(publication.DeliveryAcceptances) {
		updated.Revision = publication.Revision + 1
		if err := c.Store.UpdatePublication(ctx, updated); err != nil {
			return publication, nil, err
		}
		publication = updated
	}
	return publication, &acceptance, nil
}

// authorizeCleanDeliveryMerge binds an undriven row to its already
// journaled clean acceptance after current authority and exact checks.
// Previously driven operations keep their original provenance and
// reconcile through the existing receipt path.
func (c *Coordinator) authorizeCleanDeliveryMerge(ctx context.Context, merge *factory.Merge, publication *factory.Publication, nativeRevision int64) error {
	if merge.Authorization.Bound() {
		if merge.Authorization.Generation.AcceptanceKind == factory.AcceptanceJevAcceptance && merge.Authorization.Decision.Answer.ProviderModel != "" {
			acceptance, decision, err := c.currentFundedDeliveryAcceptance(ctx, *publication)
			if err != nil || acceptance == nil || acceptance.ID != merge.Authorization.Generation.AcceptanceID || decision.Answer.Fingerprint != merge.Authorization.Generation.Fingerprint {
				return &factory.PublicationWait{Reason: "funded_delivery_changed"}
			}
		}
		return nil
	}
	if merge.Operation.Work != nil || merge.Operation.Attempts != 0 {
		return &factory.PublicationWait{Reason: "merge_judgment_unavailable"}
	}
	updated, acceptance, err := c.recordCleanDeliveryAcceptance(ctx, *publication)
	if err != nil {
		return &factory.PublicationWait{Reason: "clear_delivery_unavailable"}
	}
	*publication = updated
	decision := factory.DeliveryDecision{}
	if acceptance == nil {
		acceptance, decision, err = c.currentFundedDeliveryAcceptance(ctx, updated)
		if err != nil {
			return &factory.PublicationWait{Reason: "funded_delivery_unavailable"}
		}
	}
	if acceptance == nil {
		return &factory.PublicationWait{Reason: "merge_judgment_unavailable"}
	}
	assessment, ok := c.currentChildCheckAssessment(ctx, updated)
	if !ok || assessment.Verdict != factory.CheckPass || assessment.ChecksDigest != acceptance.CheckDigest {
		return &factory.PublicationWait{Reason: "check_evidence_pending"}
	}
	notAfter, err := c.Store.CapAttemptDeadline(ctx, merge.AssignmentID, time.Now().Add(10*time.Minute).Unix())
	if err != nil || notAfter <= time.Now().Unix() {
		return &factory.PublicationWait{Reason: "attempt_deadline_unavailable"}
	}
	authorization := factory.MergeAuthorization{
		Decision: decision,
		Generation: factory.MergeGeneration{
			Authority: merge.Authority, PublicationID: merge.PublicationID,
			NativeOperationID: factory.MergeGenerationOperationID(merge.PublicationID, 1),
			Fingerprint:       acceptance.CoverageDigest, Head: merge.HeadOID, Base: merge.BaseOID,
			DiffBase: acceptance.Tuple.DiffBase, ChecksDigest: assessment.ChecksDigest,
			AcceptanceKind: acceptance.Kind, AcceptanceID: acceptance.ID,
			AttemptRoot: acceptance.AttemptRoot, OriginReceipt: acceptance.OriginReceipt,
			Generation: 1, ReviewRound: acceptance.Round, AssessmentRevision: assessment.Revision,
			ObservedNativeRev: nativeRevision, NotAfter: notAfter,
		},
		ResolutionsConfirmed: true,
	}
	if acceptance.Kind == factory.AcceptanceJevAcceptance {
		authorization.Generation.Fingerprint = decision.Answer.Fingerprint
		authorization.Generation.DecisionID = acceptance.DecisionID
	}
	if err := c.AuthorizeMerge(ctx, merge.PublicationID, authorization); err != nil {
		return err
	}
	fresh, err := c.Store.MergeByPublication(ctx, merge.PublicationID)
	if err != nil {
		return err
	}
	*merge = fresh
	return nil
}
