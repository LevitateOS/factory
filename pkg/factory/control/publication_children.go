package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"reflect"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

const publicationChildLimit = 256

// producePublishedChildren creates at most one reviewer or coder correction
// for each active publication head. First occurrences bind the publication,
// exact head and role (stable head-derived IDs); same-head repeats use durable D07
// review-cycle rounds with round-derived fresh IDs, so a new decision on the
// same head creates a fresh reviewer run while replay reuses its run. A later
// pass adopts an existing attempt instead of launching a duplicate.
func (c *Coordinator) producePublishedChildren(ctx context.Context, report *PublishReport) {
	if c.Store == nil || c.Host == nil || c.Broker == nil || c.Publication == nil {
		return
	}
	publications, err := c.Store.ActivePublishedPublications(ctx, publicationChildLimit)
	if err != nil {
		publicationError(report, "", "child_producer_listing_unavailable")
		return
	}
	logNew := func(waits, failures int) {
		for _, wait := range report.Waits[waits:] {
			if factory.ValidID(wait.ID) {
				log.Printf("publication child wait id=%q reason=%q", wait.ID, dispatchObservationReason(0, 0, wait.Reason, "").Reason)
			}
		}
		for _, failure := range report.Errors[failures:] {
			if factory.ValidID(failure.ID) {
				log.Printf("publication child error id=%q reason=%q", failure.ID, dispatchObservationReason(0, 0, failure.Reason, "").Reason)
			}
		}
	}
	for _, publication := range publications {
		waits, failures := len(report.Waits), len(report.Errors)
		if err := ctx.Err(); err != nil {
			publicationWait(report, publication.ID, "child_producer_cancelled")
			logNew(waits, failures)
			return
		}
		c.producePublicationChild(ctx, publication, report)
		logNew(waits, failures)
	}
}

func admitPublicationChildInput(p factory.Publication) bool {
	return p.Validate() == nil && p.Stage == factory.PublicationPublished && !p.WithdrawRequested &&
		p.HasPullTarget() && p.PullTarget().BaseOID != ""
}

func (c *Coordinator) loadPublicationChildOwner(ctx context.Context, p factory.Publication, report *PublishReport) (owner factory.Assignment, stop bool) {
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil || owner.Validate() != nil || owner.Role != project.RoleCoder ||
		owner.PublicationAssignment != owner.ID || owner.AttemptRoot == "" || owner.SourceCommit == "" {
		publicationError(report, p.ID, "publication_owner_unavailable")
		return factory.Assignment{}, true
	}
	return owner, false
}

func (c *Coordinator) gatePublicationChildAttempt(ctx context.Context, p factory.Publication, owner factory.Assignment, report *PublishReport) (factory.AttemptAllowance, bool) {
	if p.External != nil {
		policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
		if err != nil || policy.Revision != p.External.Policy || policy.Review.NativeProfile != p.External.Profile || policy.Review.NativeConnection != p.External.Connection {
			publicationWait(report, p.ID, "external_adoption_policy_changed")
			return factory.AttemptAllowance{}, true
		}
	}
	latest, err := c.Store.LatestIssueAssignment(ctx, p.Repository, p.Issue)
	if err != nil {
		publicationError(report, p.ID, "issue_assignment_unavailable")
		return factory.AttemptAllowance{}, true
	}
	if latest.AttemptRoot != owner.AttemptRoot || latest.Acceptance != p.Acceptance {
		return factory.AttemptAllowance{}, true
	}
	allowance, err := c.Store.AttemptAllowance(ctx, p.Repository, p.Issue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return factory.AttemptAllowance{}, true
		}
		publicationError(report, p.ID, "attempt_allowance_unavailable")
		return factory.AttemptAllowance{}, true
	}
	if allowance.RootAssignment != owner.AttemptRoot {
		return factory.AttemptAllowance{}, true
	}
	if allowance.Closed {
		if allowance.Active {
			if stopErr := c.stopCandidatePreparationsForRoot(ctx, p.Repository, p.ProjectID, p.Issue, owner.AttemptRoot); stopErr != nil {
				publicationWait(report, p.ID, "attempt is closed; native preparation retirement remains pending")
				return factory.AttemptAllowance{}, true
			}
		}
		publicationWait(report, p.ID, "attempt is closed; maintainer intervention is required")
		return factory.AttemptAllowance{}, true
	}
	if allowance.RemainingSeconds(time.Now()) <= 0 {
		closed, closeErr := c.Store.CloseExpiredAttempt(ctx, owner.ID)
		if closeErr != nil {
			publicationError(report, p.ID, "attempt_allowance_unavailable")
			return factory.AttemptAllowance{}, true
		}
		if closed {
			if stopErr := c.stopCandidatePreparationsForRoot(ctx, p.Repository, p.ProjectID, p.Issue, owner.AttemptRoot); stopErr != nil {
				publicationWait(report, p.ID, "attempt is closed; native preparation retirement remains pending")
				return factory.AttemptAllowance{}, true
			}
			publicationWait(report, p.ID, "attempt is closed; maintainer intervention is required")
		}
		return factory.AttemptAllowance{}, true
	}
	return allowance, false
}

type childReviewInput struct {
	review              ResolvedReviewVerdict
	hasReview           bool
	role                string
	checks              *factory.CheckAssessment
	policyReview        bool
	policyActor         int64
	deliveryDecisionID  string
	deliveryFingerprint string
}

func routePublicationChildCorrection(p factory.Publication, review ResolvedReviewVerdict, checks *factory.CheckAssessment, report *PublishReport) bool {
	// Correction-entry feedback routes only to a bounded correction
	// or to the withheld Jev decision; any other route is a
	// programming error, so it refuses loudly instead of spending.
	if route := routeDeliveryFeedback(publicationDeliveryFeedback(p, review, checks)); route == routeJev {
		publicationWait(report, p.ID, "delivery_judgment_required")
		return true
	} else if route != routeCorrection {
		publicationError(report, p.ID, "child_assignment_unrecorded")
		return true
	}
	return false
}

type childWire struct {
	phase   string
	phased  bool
	parent  string
	lineage attemptLineage
}

func (c *Coordinator) resolvePublicationChildReview(ctx context.Context, p factory.Publication, report *PublishReport) (childReviewInput, bool) {
	var in childReviewInput
	review, hasReview, reviewPending, reviewRefused := c.currentReviewReport(ctx, p)
	if reviewRefused {
		c.fenceChildPublication(ctx, p, report)
		return in, true
	}
	if reviewPending {
		return in, true
	}
	in.review, in.hasReview, in.role = review, hasReview, project.RoleReviewer
	if hasReview {
		assessment, ok := c.currentChildCheckAssessment(ctx, p)
		if !ok {
			return in, true
		}
		in.checks = assessment
		if (review.Verdict != "request-changes") && assessment.Verdict != factory.CheckFailed {
			return in, true
		}
		in.role = project.RoleCoder
	} else if assessment, ok := c.currentChildCheckAssessment(ctx, p); ok {
		in.checks = assessment
	}
	if in.role == project.RoleReviewer && c.Reviews == nil {
		publicationWait(report, p.ID, "review_submission_unavailable")
		return in, true
	}
	// D08 route before spending: a concrete unfixed defect takes the
	// bounded correction; optional-only, disputed or repetitive feedback
	// withholds the run pending the funded Jev decision, and optional
	// advice never forces a correction. The policy-required cause is
	// detected below for first inspections with passing checks.
	if in.role == project.RoleCoder && hasReview && review.Verdict == "request-changes" {
		if routeDeliveryFeedback(publicationDeliveryFeedback(p, review, in.checks)) == routeJev {
			decision, decisionID, err := c.advisePublicationDelivery(ctx, p, review)
			if err != nil {
				publicationWait(report, p.ID, "delivery_judgment_unavailable")
				return in, true
			}
			if decision.Effective != factory.DeliveryEffectiveReview {
				reason := decision.Effective
				if reason == factory.DeliveryEffectiveReady {
					reason = "delivery_accepted"
				}
				publicationWait(report, p.ID, reason)
				return in, true
			}
			in.role, in.deliveryDecisionID, in.deliveryFingerprint = project.RoleReviewer, decisionID, decision.Answer.Fingerprint
			if c.Reviews == nil {
				publicationWait(report, p.ID, "review_submission_unavailable")
				return in, true
			}
			return in, false
		}
		if stop := routePublicationChildCorrection(p, review, in.checks, report); stop {
			return in, true
		}
	}
	if in.role == project.RoleReviewer && !hasReview && in.checks != nil && in.checks.Verdict == factory.CheckPass {
		if policy, err := c.Store.RepositoryPolicy(ctx, p.Repository); err == nil && policy.Validate() == nil {
			in.policyActor = policy.Review.ActorID
			in.policyReview = policyCauseOwed(p, in.checks.Verdict, in.policyActor)
		}
	}
	return in, false
}

func (c *Coordinator) resolvePublicationChildWire(ctx context.Context, p factory.Publication, owner factory.Assignment, in childReviewInput, report *PublishReport) (childWire, bool) {
	// Children record their execution phase, issue subject and parent.
	// First reviews follow the owner; follow-up children follow the
	// evidence they answer.
	var wire childWire
	wire.phase = project.ExecutionCorrect
	if in.role == project.RoleReviewer {
		wire.phase = project.ExecutionReview
	}
	wire.phased = true
	if in.role == project.RoleReviewer {
		wire.parent = owner.ID
	} else {
		reviewer, ok := c.currentReviewerForCorrection(ctx, p, owner)
		if !ok {
			publicationWait(report, p.ID, "child_assignment_unavailable")
			return wire, true
		}
		wire.parent = reviewer.ID
	}
	wire.lineage = attemptLineage{phase: wire.phase, parent: wire.parent}
	return wire, false
}

func childCycleDigests(p factory.Publication, role string, in childReviewInput, report *PublishReport) (digest, inputDigest, triggerDecision, triggerReason string, stop bool) {
	// Durable D07 cycle: fresh independent reviews bind head/base only;
	// corrections and same-head re-reviews bind the full outstanding
	// review/check evidence. New digests open new rounds (fresh IDs);
	// identical digests replay their round (same IDs). Correction rounds
	// separate the two domains: the finding-set digest binds report
	// coverage while the complete input fingerprint drives round reuse,
	// so same-key changed inputs open a new round instead of silently
	// reusing the prior admitted correction.
	shortHead := p.Candidate
	if len(shortHead) > 8 {
		shortHead = shortHead[:8]
	}
	if role == project.RoleReviewer {
		if in.deliveryDecisionID != "" {
			digest = in.deliveryFingerprint
			triggerDecision, triggerReason = "funded-review-again", "funded delivery decision "+in.deliveryDecisionID+" requests another independent inspection"
		} else if in.policyReview {
			// The bounded policy-required pass carries its separately
			// recorded cause; the digest stays head/base-only so a
			// replay reuses its round and never double-charges.
			digest = reviewCycleInputDigest(p.Candidate, p.PullTarget().BaseOID, nil, nil)
			triggerDecision, triggerReason = policyReviewDecision, policyReviewReason(in.policyActor, p.Candidate)
		} else if in.hasReview {
			digest = reviewCycleEvidenceDigest(p.Candidate, p.PullTarget().BaseOID, in.review, in.checks)
			triggerDecision, triggerReason = "re-review-same-head", "another inspection of head "+shortHead+" after "+in.review.Verdict+" with outstanding findings"
		} else if len(p.ReviewCycles) == 0 {
			digest = reviewCycleInputDigest(p.Candidate, p.PullTarget().BaseOID, nil, nil)
			triggerDecision, triggerReason = "initial-review", "first review of published head "+shortHead
		} else if reviewCycleHeadSeen(p, p.Candidate) {
			digest = reviewCycleInputDigest(p.Candidate, p.PullTarget().BaseOID, nil, nil)
			triggerDecision, triggerReason = "re-review-same-head", "another inspection of head "+shortHead+" without prior verdict"
		} else {
			digest = reviewCycleInputDigest(p.Candidate, p.PullTarget().BaseOID, nil, nil)
			triggerDecision, triggerReason = "re-review-after-correction", "fresh review of corrected head "+shortHead
		}
		inputDigest = digest
	} else {
		// The correction round binds the finding set it answers, not
		// the review/check evidence envelope: the journaled SetDigest
		// must equal this digest or report admission cannot journal
		// the correction. The input fingerprint separately preserves
		// the broad evidence the round answers; checks still gate
		// correction entry through the current assessment as well.
		setDigest, setErr := factory.FindingSetDigest(correctionFindingKeys(in.review))
		if setErr != nil {
			publicationError(report, p.ID, "child_assignment_unrecorded")
			return "", "", "", "", true
		}
		digest = setDigest
		inputDigest = reviewCycleEvidenceDigest(p.Candidate, p.PullTarget().BaseOID, in.review, in.checks)
		triggerDecision, triggerReason = "correction-after-review", "address "+in.review.Verdict+" with "+in.checks.Verdict+" checks on head "+shortHead
	}
	return digest, inputDigest, triggerDecision, triggerReason, false
}

func (c *Coordinator) admitNewPublicationChild(ctx context.Context, p factory.Publication, owner factory.Assignment, role, childPhase, parentAssignment, childID string, allowance factory.AttemptAllowance, report *PublishReport) bool {
	existing, err := c.Store.Assignment(ctx, childID)
	if err == nil {
		if !existingMatchesPublicationChild(childID, existing, p, owner, role, childPhase, parentAssignment) {
			c.fenceChildPublication(ctx, p, report)
			return true
		}
		if existing.Finished() &&
			(existing.Outcome != factory.Succeeded || existing.Result == nil || !existing.Result.Reported || existing.Result.Status != "completed") {
			c.fenceChildPublication(ctx, p, report)
		}
		return true
	}
	if !errors.Is(err, store.ErrNotFound) {
		publicationError(report, p.ID, "child_assignment_unavailable")
		return true
	}
	if role == project.RoleCoder && !slices.Contains(allowance.Corrections, childID) &&
		len(allowance.Corrections) >= allowance.Limits.CorrectionCycles {
		c.fenceChildPublication(ctx, p, report)
		return true
	}
	return false
}

func (c *Coordinator) chargePublicationChildReview(ctx context.Context, p factory.Publication, owner factory.Assignment, childID string, policyReview bool, report *PublishReport) bool {
	// D08 positive evidence before buying a review: charge the
	// attempt-root allowance by the immutable reviewer assignment
	// identity before any native work or dispatch packet. Every
	// review consumes one ReviewRound; a discretionary re-review
	// of an inspected head additionally spends that head's single
	// extra pass, while the policy-required cause spends only the
	// round. Replays of the same assignment never recharge.
	// Exhaustion withholds the run instead of launching review
	// without cover.
	_, exhausted, chargeErr := c.Store.ChargeReviewDispatch(ctx, p.Repository, p.Issue, owner.AttemptRoot, childID, p.Candidate, policyReview)
	if chargeErr != nil {
		publicationError(report, p.ID, "review_allowance_unavailable")
		return true
	}
	if exhausted {
		publicationWait(report, p.ID, "review_allowance_exhausted")
		return true
	}
	return false
}

func (c *Coordinator) planPublicationChildDispatch(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, p factory.Publication, owner factory.Assignment, role, expectedPrepID, childID string, lineage attemptLineage, report *PublishReport) (*attemptPlan, string, execution.PreparationRecord, bool) {
	if deps.Authority == nil {
		publicationWait(report, p.ID, WaitAuthority)
		return nil, "", execution.PreparationRecord{}, true
	}
	occupancySnapshot, err := snapshotOccupancy(ctx, deps.Store)
	if err != nil {
		publicationError(report, p.ID, "capacity_accounting_unavailable")
		return nil, "", execution.PreparationRecord{}, true
	}
	*occupancy = occupancySnapshot
	nativeChild := (role == project.RoleReviewer && lineage.phase == project.ExecutionReview && lineage.parent == owner.ID ||
		role == project.RoleCoder && lineage.phase == project.ExecutionCorrect && lineage.parent != "") &&
		owner.NativeTool != nil && owner.Validate() == nil && owner.PublicationAssignment == owner.ID &&
		owner.Repository == p.Repository && owner.Issue == p.Issue && owner.Acceptance == p.Acceptance && owner.ProjectID == p.ProjectID
	control, err := deps.Store.IssueControl(ctx, p.Repository, p.Issue)
	preparationOnly := nativeChild && control.Readiness == factory.ReadinessNotAuthorized && control.Reason == factory.BlockerAuthorityMissing &&
		len(control.Blockers) == 1 && control.Blockers[0].Code == factory.BlockerAuthorityMissing && control.Blockers[0].Detail == factory.MissingPreparation
	if err == nil && control.Readiness != factory.ReadinessQueued && !preparationOnly {
		control, err = c.refreshReadiness(ctx, control)
	}
	if err != nil || control.Readiness != factory.ReadinessQueued && !preparationOnly || control.Acceptance != p.Acceptance {
		publicationWait(report, p.ID, "issue_readiness_not_current")
		return nil, "", execution.PreparationRecord{}, true
	}
	exactPreparation := ""
	if role == project.RoleCoder {
		exactPreparation = owner.Preparation
	}
	if nativeChild {
		standingAuthority := deps.Authority
		deps.Authority = func(ctx context.Context, repository int64, selectedRole string) (factory.EffectiveAuthority, error) {
			effective, err := standingAuthority(ctx, repository, selectedRole)
			if err != nil || repository != p.Repository || selectedRole != role ||
				len(effective.Missing) != 1 || effective.Missing[0] != factory.MissingPreparation {
				return effective, err
			}
			current, err := deps.Store.PublicationByAssignment(ctx, owner.ID)
			if err != nil {
				return effective, err
			}
			if !admitPublicationChildInput(current) || current.ID != p.ID || current.ProjectID != owner.ProjectID ||
				current.Repository != p.Repository || current.Issue != p.Issue || current.Acceptance != p.Acceptance ||
				current.Candidate != p.Candidate || current.PRNumber != p.PRNumber || current.PRID != p.PRID ||
				current.PullTarget() != p.PullTarget() || !reflect.DeepEqual(current.Tuple, p.Tuple) {
				return effective, nil
			}
			if role == project.RoleCoder {
				reviewer, ok := c.currentReviewerForCorrection(ctx, current, owner)
				if !ok || reviewer.ID != lineage.parent {
					return effective, nil
				}
			}
			// Only this published child may proceed to fresh candidate setup.
			// Selection and atomic admission still prove the exact recipe.
			effective.Missing, effective.Effective = []string{}, true
			return effective, nil
		}
	}
	plan, wait, failed := planAttemptSource(ctx, deps, occupancy, p.Repository, p.Issue, p.Acceptance, control, false, role, exactPreparation, true, lineage)
	if failed != nil {
		publicationWait(report, p.ID, failed.Reason)
		return nil, "", execution.PreparationRecord{}, true
	}
	if wait != nil {
		publicationWait(report, p.ID, wait.Reason)
		return nil, "", execution.PreparationRecord{}, true
	}
	if c.publicationChildPreparationHeld(ctx, p, owner, plan, expectedPrepID, childID, report) {
		return nil, "", execution.PreparationRecord{}, true
	}
	if nativeChild && plan.prep.Native != nil {
		control, err = c.refreshReadinessForAuthority(ctx, control, func(ctx context.Context, repository int64) (factory.EffectiveAuthority, error) {
			return deps.Authority(ctx, repository, role)
		})
	} else {
		control, err = c.refreshReadiness(ctx, control)
	}
	if err != nil || control.Readiness != factory.ReadinessQueued || control.Acceptance != p.Acceptance {
		publicationWait(report, p.ID, "issue_readiness_not_current")
		return nil, "", execution.PreparationRecord{}, true
	}
	plan.control = control
	plan, wait, failed = finishAttemptPlan(ctx, deps, plan, role, true, "")
	if failed != nil {
		publicationWait(report, p.ID, failed.Reason)
		return nil, "", execution.PreparationRecord{}, true
	}
	if wait != nil {
		publicationWait(report, p.ID, wait.Reason)
		return nil, "", execution.PreparationRecord{}, true
	}
	if plan.attemptRoot != owner.AttemptRoot || plan.projectID != owner.ProjectID ||
		plan.policy.Roles[role].Model == "" ||
		plan.inputs.Revision != control.NativeRev {
		publicationWait(report, p.ID, "child_dispatch_inputs_changed")
		return nil, "", execution.PreparationRecord{}, true
	}
	plan.inputs.Tip = p.Candidate
	preparationID := plan.prep.Preparation.ID
	contextPreparation := plan.prep
	if role == project.RoleReviewer || role == project.RoleCoder && plan.prep.Native != nil {
		if plan.prep.Native != nil {
			if err := c.retirePublicationPredecessor(ctx, p, owner, role); err != nil {
				report.Waits = append(report.Waits, PublishWait{ID: p.ID, Reason: "candidate_preparation_retirement_pending", Detail: err.Error()})
				return nil, "", execution.PreparationRecord{}, true
			}
		}
		if c.refuseUnavailableForkCorrection(ctx, p, role, report) {
			return nil, "", execution.PreparationRecord{}, true
		}
		prep, ready, prepErr := c.prepareReviewCandidate(ctx, p, owner, plan, report, expectedPrepID, childID)
		if prepErr != nil {
			report.Waits = append(report.Waits, PublishWait{ID: p.ID, Reason: "review_preparation_unavailable", Detail: prepErr.Error()})
			return nil, "", execution.PreparationRecord{}, true
		}
		if !ready {
			publicationWait(report, p.ID, "review_preparation_pending")
			return nil, "", execution.PreparationRecord{}, true
		}
		if prep.ID != expectedPrepID {
			publicationError(report, p.ID, "child_assignment_unrecorded")
			return nil, "", execution.PreparationRecord{}, true
		}
		preparationID = prep.ID
		contextPreparation, err = c.Store.Preparation(ctx, prep.ID)
		if err != nil || factory.DeliveryPreparationDigest(contextPreparation.Preparation) != factory.DeliveryPreparationDigest(prep) {
			publicationWait(report, p.ID, "review_preparation_unavailable")
			return nil, "", execution.PreparationRecord{}, true
		}
		plan.prep = contextPreparation
	}
	return plan, preparationID, contextPreparation, false
}

// An already admitted preparation owns its original observation/recovery path.
// While it is running or refused, fresh provider inputs cannot launch a child.
func (c *Coordinator) publicationChildPreparationHeld(ctx context.Context, p factory.Publication, owner factory.Assignment, plan *attemptPlan, preparationID, childID string, report *PublishReport) bool {
	if plan.prep.Native == nil {
		return false
	}
	_, prep, err := buildReviewCandidatePrep(plan, p, preparationID)
	if err != nil {
		publicationWait(report, p.ID, "review_preparation_unavailable")
		return true
	}
	prior, err := c.Store.CandidatePreparation(ctx, preparationID)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil || prior.Admission.PublicationID != p.ID || prior.Admission.OwnerAssignment != owner.ID || prior.Admission.ChildAssignment != childID || prior.Admission.AttemptRoot != owner.AttemptRoot {
		publicationWait(report, p.ID, "review_preparation_unavailable")
		return true
	}
	if err := c.verifyStoredReviewCandidatePrep(ctx, p, prep, prior.Preparation, report); err != nil || prior.Stopped || prior.Retirement == "confirmed" {
		publicationWait(report, p.ID, "review_preparation_unavailable")
		return true
	}
	if !prior.Preparation.State.Ready && prior.NotAfter <= time.Now().Unix() {
		c.fenceChildPublication(ctx, p, report)
		publicationWait(report, p.ID, "review_preparation_unavailable")
		return true
	}
	_, ready, err, proceed := c.reconcileStoredReviewCandidatePrep(ctx, p, prep, prior.Preparation, false, report)
	if err != nil {
		publicationWait(report, p.ID, "review_preparation_unavailable")
		return true
	}
	if !ready && !proceed {
		publicationWait(report, p.ID, "review_preparation_pending")
		return true
	}
	return false
}

// A finished native implementation/review/correction still owns its candidate preparation.
// Release that exact custody before admitting the next session, independently
// of the retained run, report and exported candidate evidence.
func (c *Coordinator) retirePublicationPredecessor(ctx context.Context, p factory.Publication, owner factory.Assignment, role string) error {
	if role == project.RoleReviewer && p.External != nil && p.Run == "" && owner.RetainedPublication == nil {
		if owner.ExternalPlanning != nil && owner.NativeTool != nil {
			return c.retirePublicationSource(ctx, p, owner, owner)
		}
		return nil
	}
	var prior factory.Assignment
	if role == project.RoleCoder {
		var ok bool
		prior, ok = c.currentReviewerForCorrection(ctx, p, owner)
		if !ok {
			return errors.New("current correction reviewer unavailable")
		}
	} else {
		var err error
		priorRun := p.Run
		if priorRun == "" && owner.RetainedPublication != nil {
			priorRun = owner.RetainedPublication.Run
		}
		prior, err = c.Store.AssignmentByRun(ctx, priorRun)
		if err != nil {
			return err
		}
	}
	if prior.NativeTool == nil || prior.Phase != project.ExecutionReview && prior.Phase != project.ExecutionCorrect {
		return nil
	}
	retirementPublication, retirementOwner := p, owner
	if prior.PublicationAssignment != owner.ID {
		history, err := c.Store.PublicationReviewHistory(ctx, p)
		if err != nil {
			return err
		}
		found := false
		for _, source := range history {
			if source.AssignmentID != prior.PublicationAssignment || source.Run != prior.Run {
				continue
			}
			retirementOwner, err = c.Store.Assignment(ctx, source.AssignmentID)
			if err != nil {
				return err
			}
			retirementPublication, found = source, true
			break
		}
		if !found || prior.AttemptRoot != retirementOwner.AttemptRoot {
			return errors.New("publication predecessor is outside admitted history")
		}
	}
	run, err := c.Store.FactoryRun(ctx, prior.Run)
	if err != nil || !run.Reconciled || run.Native == nil || !run.Native.Settled() || !prior.Succeeded() {
		return errors.New("publication predecessor retirement unconfirmed")
	}
	prepared, err := c.Store.CandidatePreparation(ctx, prior.Preparation)
	if err != nil {
		return err
	}
	meta := prepared.Admission
	if prepared.Preparation.Native == nil || meta.PublicationID != retirementPublication.ID || meta.OwnerAssignment != retirementOwner.ID || meta.ChildAssignment != prior.ID || meta.AttemptRoot != retirementOwner.AttemptRoot {
		return errors.New("publication predecessor preparation differs")
	}
	if prepared.Stopped && prepared.Retirement == "confirmed" {
		return nil
	}
	return c.retireCandidatePreparation(ctx, prepared.Preparation)
}

// A private source checkout belongs to the investigation root. A completed
// implementation child or external-planning Architect proves which original
// checkout may be released before independent review setup.
func (c *Coordinator) retirePublicationSource(ctx context.Context, p factory.Publication, owner, prior factory.Assignment) error {
	prepared, err := c.Store.CandidatePreparation(ctx, prior.Preparation)
	if err != nil {
		if owner.ExternalPlanning != nil {
			return err
		}
		// Standing recipes remain available for other work. Only the private
		// candidate admission above transfers custody to this issue root.
		standing, readErr := c.Store.StandingProjectPreparations(ctx, prior.ProjectID)
		if readErr != nil {
			return readErr
		}
		for _, recipe := range standing {
			if recipe.Preparation.ID == prior.Preparation {
				return nil
			}
		}
		return err
	}
	implementation := p.External == nil && prior.Phase == project.ExecutionImplement && prior.Run == p.Run && prior.ParentAssignment == owner.ID &&
		prior.Result != nil && prior.Result.Candidate == p.Candidate
	externalPlanning := p.External != nil && p.Run == "" && owner.ExternalPlanning != nil && prior.ID == owner.ID && prior.Phase == project.ExecutionInvestigate &&
		owner.ExternalPlanning.Validate(owner.ID) == nil && reflect.DeepEqual(p.External, &owner.ExternalPlanning.Origin) &&
		p.Candidate == owner.ExternalPlanning.Request.Target.HeadOID && p.BaseSHA == owner.SourceCommit
	if prior.Validate() != nil || !prior.Succeeded() || prior.Role != project.RoleCoder || !implementation && !externalPlanning ||
		owner.Phase != project.ExecutionInvestigate || !owner.Succeeded() || owner.ID != owner.AttemptRoot ||
		owner.Repository != p.Repository || owner.Issue != p.Issue || owner.ProjectID != p.ProjectID || owner.Acceptance != p.Acceptance ||
		prior.Repository != p.Repository || prior.Issue != p.Issue || prior.ProjectID != p.ProjectID || prior.Acceptance != p.Acceptance ||
		prior.PublicationAssignment != owner.ID || prior.AttemptRoot != owner.AttemptRoot ||
		prior.Preparation != owner.Preparation || p.Preparation != prior.Preparation || prior.SourceCommit != owner.SourceCommit ||
		prior.Result == nil || !prior.Result.Reported || prior.Result.Status != "completed" {
		return errors.New("published source predecessor differs")
	}
	meta, preparation := prepared.Admission, prepared.Preparation
	if externalPlanning && (meta.ExternalPlanning == nil || meta.ExternalPlanning.CheckedUnix != 0 ||
		!reflect.DeepEqual(meta.ExternalPlanning.Request, owner.ExternalPlanning.Request) || meta.ExternalPlanning.Origin != owner.ExternalPlanning.Origin || meta.ExternalPlanning.Principal != owner.ExternalPlanning.Principal) {
		return errors.New("external planning source capture differs")
	}
	if preparation.Native == nil || preparation.Native.WorktreeID != prior.Preparation || preparation.Preparation.ID != prior.Preparation ||
		preparation.Preparation.Project != owner.ProjectID || preparation.Preparation.Role != project.RoleCoder ||
		preparation.Preparation.SourceCommit != prior.SourceCommit || meta.Repository != p.Repository || meta.Issue != p.Issue ||
		meta.Project != owner.ProjectID || meta.PublicationID != "" || meta.OwnerAssignment != owner.ID ||
		meta.ChildAssignment != owner.ID || meta.AttemptRoot != owner.AttemptRoot || meta.ActorID != owner.ActorID ||
		meta.Connection != owner.Connection || !meta.Authority.SameGrants(owner.Authority) {
		return errors.New("implementation predecessor preparation differs")
	}
	run, err := c.Store.FactoryRun(ctx, prior.Run)
	if err != nil || !run.Reconciled || run.Outcome != factory.Succeeded || run.Native == nil || !run.Native.Settled() ||
		run.ProjectID != prior.ProjectID || run.Role != prior.Role || run.InputSHA != prior.SourceCommit ||
		run.Native.Provenance.Profile != preparation.Native.Profile || run.Native.Provenance.InstanceID != preparation.Native.InstanceID ||
		run.Native.Provenance.WorktreeID != preparation.Native.WorktreeID || !reflect.DeepEqual(prior.NativeTool, &run.Native.Tool) {
		return errors.New("implementation predecessor retirement unconfirmed")
	}
	if prepared.Stopped && prepared.Retirement == "confirmed" {
		return nil
	}
	// This enrolled workspace serves one repository session. A still
	// reconciling run, assigned consumer or held admission keeps its checkout.
	runs, err := c.Store.ProjectUnsettledRuns(ctx, prior.ProjectID, 1)
	if err != nil || len(runs) != 0 {
		return errors.New("implementation preparation has unsettled consumers")
	}
	assigned, err := c.Store.AssignedAssignments(ctx, store.MaxHeldReservations)
	if err != nil || len(assigned) == store.MaxHeldReservations {
		return errors.New("implementation preparation consumers unavailable")
	}
	for _, consumer := range assigned {
		if consumer.ProjectID == prior.ProjectID && consumer.Preparation == prior.Preparation {
			return errors.New("implementation preparation has an assigned consumer")
		}
	}
	held, err := c.Store.HeldReservations(ctx, store.MaxHeldReservations)
	if err != nil || len(held) == store.MaxHeldReservations {
		return errors.New("implementation preparation reservations unavailable")
	}
	for _, reservation := range held {
		if reservation.Repository == p.Repository {
			return errors.New("implementation preparation has a held admission")
		}
	}
	return c.retireCandidatePreparation(ctx, preparation)
}

func (c *Coordinator) promptPublicationChild(ctx context.Context, plan *attemptPlan, p factory.Publication, owner factory.Assignment, preparationID string, contextPreparation execution.PreparationRecord, role string, in childReviewInput, cycle factory.ReviewCycle, phasedChild bool, report *PublishReport) (*factory.InvestigationPlan, bool) {
	if !time.Now().Before(plan.deadline) {
		publicationWait(report, p.ID, "attempt_deadline_exhausted")
		return nil, true
	}
	// Prior verdict narratives go to corrections and same-head re-reviews.
	// Fresh independent reviewers receive no prior narrative; the canonical
	// carried finding set is separately retained as required typed context.
	var typedReview *factory.ReviewCycleReport
	var typedClear *factory.ClearReviewReport
	var checksPrompt *factory.CheckAssessment
	if role == project.RoleReviewer {
		checksPrompt = in.checks
		if in.hasReview {
			typedReview, typedClear = in.review.Review, in.review.Clear
		}
	} else {
		if in.hasReview {
			typedReview, typedClear = in.review.Review, in.review.Clear
		}
		checksPrompt = in.checks
	}
	repositoryContext, contextErr := readRepositoryContext(ctx, c.Host, plan, contextPreparation, role,
		owner.SourceCommit, p.CurrentDiffBase(), p.Candidate, false)
	if contextErr != nil {
		publicationWait(report, p.ID, "native prepared repository context is unavailable or changed")
		return nil, true
	}
	prompt, err := buildPublicationChildPrompt(plan, p, owner, preparationID, role, typedReview, typedClear, checksPrompt, repositoryContext)
	if err != nil {
		report.Waits = append(report.Waits, PublishWait{ID: p.ID, Reason: "child_prompt_invalid", Detail: err.Error()})
		return nil, true
	}
	plan.prompt, plan.promptSHA = prompt, project.FactoryPromptDigest(prompt)
	carried, carriedErr := c.reviewCarriedFindings(ctx, p, cycle)
	if carriedErr != nil {
		publicationWait(report, p.ID, "required_carried_context_unavailable")
		return nil, true
	}
	tuple := factory.CandidateTuple{Head: p.Candidate, Base: p.BaseSHA, DiffBase: p.CurrentDiffBase(), Revision: p.NativeRev}
	if p.Tuple != nil {
		tuple = *p.Tuple
	}
	plan.reviewContext = &factory.CurrentReviewContext{Publication: factory.CurrentPublicationContext{ID: p.ID, AssignmentID: p.AssignmentID, Revision: p.Revision, PRID: p.PRID, PRNumber: p.PRNumber, Stage: p.Stage}, AdmittedTuple: tuple, Cycle: cycle, CarriedFindings: carried}
	plan.reviewContext.External = p.External
	// A phased corrector executes the controller-derived singleton plan,
	// never a rebuilt or agent-selected one. An accepted-policy gap is a
	// visible planning blocker; any other derivation failure refuses
	// loudly instead of launching without a manifest.
	var correctionPlan *factory.InvestigationPlan
	if phasedChild && role == project.RoleCoder {
		derived, derivationErr := c.correctionPlanFor(ctx, plan, p, owner, cycle, in.review)
		if derivationErr != nil {
			if reason, blocked := correctionPlanWaitReason(derivationErr); blocked {
				publicationWait(report, p.ID, reason)
				return nil, true
			}
			publicationError(report, p.ID, "child_assignment_unrecorded")
			return nil, true
		}
		correctionPlan = derived
	}
	return correctionPlan, false
}

func (c *Coordinator) refuseUnavailableForkCorrection(ctx context.Context, p factory.Publication, role string, report *PublishReport) bool {
	if role != project.RoleCoder || p.External == nil || p.PullTarget().HeadRepository == p.Repository {
		return false
	}
	source, ok := c.Publication.(interface {
		AdmitCorrectionSource(context.Context, factory.Publication) error
	})
	if !ok || source.AdmitCorrectionSource(ctx, p) != nil {
		publicationWait(report, p.ID, "fork_correction_unavailable")
		return true
	}
	return false
}

func (c *Coordinator) producePublicationChild(ctx context.Context, p factory.Publication, report *PublishReport) {
	if !admitPublicationChildInput(p) {
		return
	}
	owner, stop := c.loadPublicationChildOwner(ctx, p, report)
	if stop {
		return
	}
	// Publication retained the exact Builder candidate or external Architect plan.
	// Release its completed private checkout independently of whether the
	// next role's current source, tool, checks or authority can admit work.
	if owner.NativeTool != nil && owner.RetainedPublication == nil && owner.ExternalPublication == nil &&
		(p.External == nil || p.Run == "" && owner.ExternalPlanning != nil) {
		sourceRun := p.Run
		if p.External != nil {
			sourceRun = owner.Run
		}
		prior, err := c.Store.AssignmentByRun(ctx, sourceRun)
		if err != nil {
			publicationWait(report, p.ID, "publication_predecessor_unavailable")
			return
		}
		if prior.NativeTool != nil && (prior.Phase == project.ExecutionImplement || prior.ID == owner.ID && prior.ExternalPlanning != nil) {
			if err := c.retirePublicationSource(ctx, p, owner, prior); err != nil {
				report.Waits = append(report.Waits, PublishWait{ID: p.ID, Reason: "candidate_preparation_retirement_pending", Detail: err.Error()})
				return
			}
		}
	}
	allowance, stop := c.gatePublicationChildAttempt(ctx, p, owner, report)
	if stop {
		return
	}
	head, err := c.Store.AcceptanceHead(ctx, p.Repository, p.Issue)
	if err != nil {
		publicationError(report, p.ID, "acceptance_unavailable")
		return
	}
	if head != p.Acceptance {
		return
	}

	in, stop := c.resolvePublicationChildReview(ctx, p, report)
	if stop {
		return
	}
	role := in.role
	policyReview := in.policyReview
	wire, stop := c.resolvePublicationChildWire(ctx, p, owner, in, report)
	if stop {
		return
	}
	childPhase, phasedChild := wire.phase, wire.phased
	lineage, parentAssignment := wire.lineage, wire.parent
	digest, inputDigest, triggerDecision, triggerReason, stop := childCycleDigests(p, role, in, report)
	if stop {
		return
	}
	cycle, childID, expectedPrepID, cycleErr := c.ensureReviewCycle(ctx, &p, owner, role, digest, inputDigest, triggerDecision, triggerReason)
	if cycleErr != nil {
		publicationWait(report, p.ID, "child_cycle_unrecorded")
		return
	}
	if c.admitNewPublicationChild(ctx, p, owner, role, childPhase, parentAssignment, childID, allowance, report) {
		return
	}
	if role == project.RoleReviewer {
		if c.chargePublicationChildReview(ctx, p, owner, childID, policyReview, report) {
			return
		}
	}
	deps := c.dispatchDeps()
	var occupancy passOccupancy
	plan, preparationID, contextPreparation, stop := c.planPublicationChildDispatch(ctx, deps, &occupancy, p, owner, role, expectedPrepID, childID, lineage, report)
	if stop {
		return
	}
	// CI needs the prepared candidate, but a paid reviewer needs its
	// settled exact-tuple assessment. Keep preparation ahead of this
	// wait so missing or pending CI cannot deadlock its own inputs.
	if role == project.RoleReviewer {
		current, ok := c.currentChildCheckAssessment(ctx, p)
		if in.checks == nil || !ok || current.Revision != in.checks.Revision {
			publicationWait(report, p.ID, "review_checks_pending")
			return
		}
	}
	correctionPlan, stop := c.promptPublicationChild(ctx, plan, p, owner, preparationID, contextPreparation, role, in, cycle, phasedChild, report)
	if stop {
		return
	}
	// Recheck the source, preparation, current tuple and allowance after
	// the waits above: a moved head, retired preparation, replaced
	// acceptance or spent allowance waits instead of launching stale
	// work, and a phased correction additionally rebinds its current
	// reviewer before any host call.
	if wait, recheckErr := c.recheckPublicationChild(ctx, p, owner, plan, phasedChild, parentAssignment); recheckErr != nil {
		publicationError(report, p.ID, "child_assignment_unavailable")
		return
	} else if wait != nil {
		publicationWait(report, p.ID, wait.Reason)
		return
	}
	if c.refuseUnavailableForkCorrection(ctx, p, role, report) {
		return
	}
	c.executePublicationChild(ctx, deps, &occupancy, plan, p, owner, childID, preparationID, role, correctionPlan, report)
}

// correctionPlanFor derives the singleton correction plan for one phased
// correction round from the accepted issue inputs and the owner's
// immutable recorded investigation plan.
func (c *Coordinator) correctionPlanFor(ctx context.Context, plan *attemptPlan, p factory.Publication, owner factory.Assignment, cycle factory.ReviewCycle, review ResolvedReviewVerdict) (*factory.InvestigationPlan, error) {
	origin, err := c.Store.PublicationExecutionOwner(ctx, p, owner)
	if err != nil || origin.Phase != project.ExecutionInvestigate || origin.Result == nil || origin.Result.PhaseResult == nil ||
		origin.Result.PhaseResult.Output == nil || origin.Result.PhaseResult.Output.Plan == nil {
		return nil, &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "publication owner carries no recorded investigation plan"}
	}
	if cycle.CorrectionAssignment == "" || cycle.Head != p.Candidate || cycle.BaseOID != p.PullTarget().BaseOID {
		return nil, &correctionPlanBlocked{reason: "correction_plan_unavailable", detail: "correction answers another round"}
	}
	return deriveCorrectionPlan(plan.acceptance, *origin.Result.PhaseResult.Output.Plan,
		owner.AttemptRoot, origin.AttemptRoot, p.Candidate, p.PullTarget().BaseOID, plan.prep.Preparation.ID,
		cycle.ID, cycle.FindingDigest, correctionFindingKeys(review), plan.policy.Checks)
}

// correctionFindingKeys returns the deterministic finding keys one phased
// correction answers: the typed verdict's new findings plus its carried
// assessments. Verified assessments stay out: the canonical carried set
// already retired those keys, so coverage derives from the outstanding
// set, not blindly all assessment keys. Legacy string verdicts carry no
// stable keys.
func correctionFindingKeys(review ResolvedReviewVerdict) []string {
	seen := map[string]bool{}
	var keys []string
	addAssessment := func(assessment factory.FindingAssessment) {
		if assessment.Status == factory.FindingVerified {
			return
		}
		if !seen[assessment.Key] {
			seen[assessment.Key] = true
			keys = append(keys, assessment.Key)
		}
	}
	if review.Review != nil {
		for _, finding := range review.Review.NewFindings {
			if !seen[finding.Key] {
				seen[finding.Key] = true
				keys = append(keys, finding.Key)
			}
		}
		for _, assessment := range review.Review.Assessments {
			addAssessment(assessment)
		}
	}
	if review.Clear != nil {
		for _, assessment := range review.Clear.Assessments {
			addAssessment(assessment)
		}
	}
	return keys
}

// recheckPublicationChild re-reads the publication, current tuple and
// allowance after the wait-inducing preparation and planning above. A
// changed candidate, fenced publication, replaced acceptance, retired
// allowance or rebound reviewer waits instead of launching.
func (c *Coordinator) recheckPublicationChild(ctx context.Context, p factory.Publication, owner factory.Assignment, plan *attemptPlan, phasedChild bool, parentAssignment string) (*planWait, error) {
	if p.External != nil {
		if c.Reviews == nil || plan.policy.Revision != p.External.Policy {
			return waitFor("child_dispatch_inputs_changed", "external adoption policy changed before launch"), nil
		}
		if request, automatic := p.AutomaticExternalAdoption(plan.policy); automatic {
			reader, ok := c.Reviews.(automaticExternalPullSource)
			if !ok {
				return waitFor("child_dispatch_inputs_changed", "automatic PR eligibility is unavailable"), nil
			}
			if err := reader.CheckAutomaticExternalPull(ctx, request); err != nil {
				return waitFor("child_dispatch_inputs_changed", "automatic PR author, accepted issue association or policy changed"), nil
			}
		}
		t := p.PullTarget()
		if _, err := c.Reviews.ObserveReview(ctx, factory.ReviewWork{Repository: p.Repository, HeadRepository: t.HeadRepository, ActorID: plan.policy.Review.ActorID, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: t.IssueID, PRAuthorID: t.AuthorID, HeadRef: t.HeadRef, BaseRef: t.BaseRef, HeadOID: p.Candidate, BaseOID: t.BaseOID}); err != nil {
			return waitFor("child_dispatch_inputs_changed", "external PR identity, head, base or permission changed before launch"), nil
		}
	}
	fresh, err := c.Store.PublicationByAssignment(ctx, owner.ID)
	if err != nil {
		return nil, err
	}
	if fresh.Stage != factory.PublicationPublished || fresh.WithdrawRequested ||
		fresh.Candidate != p.Candidate || fresh.PRNumber != p.PRNumber || fresh.PRID != p.PRID ||
		fresh.PullTarget().BaseOID != p.PullTarget().BaseOID {
		return waitFor("child_dispatch_inputs_changed", "published candidate changed before launch"), nil
	}
	head, err := c.Store.AcceptanceHead(ctx, p.Repository, p.Issue)
	if err != nil {
		return nil, err
	}
	if head != p.Acceptance {
		return waitFor("child_dispatch_inputs_changed", "acceptance head changed before launch"), nil
	}
	allowance, err := c.Store.AttemptAllowance(ctx, p.Repository, p.Issue)
	if err != nil {
		return nil, err
	}
	if allowance.RootAssignment != owner.AttemptRoot || allowance.Closed ||
		allowance.RemainingSeconds(time.Now()) <= 0 {
		return waitFor("child_dispatch_inputs_changed", "attempt allowance changed before launch"), nil
	}
	if plan.inputs.Tip != fresh.Candidate {
		return waitFor("child_dispatch_inputs_changed", "published candidate changed before launch"), nil
	}
	if phasedChild && plan.phase == project.ExecutionCorrect {
		reviewer, ok := c.currentReviewerForCorrection(ctx, fresh, owner)
		if !ok || reviewer.ID != parentAssignment {
			return waitFor("child_dispatch_inputs_changed", "current reviewer changed before launch"), nil
		}
	}
	return nil, nil
}

// currentReviewerForCorrection resolves the reviewer assignment behind the
// newest committed verdict on the publication's exact current head and
// base. A pending or refused current-head operation, a missing or stale
// reviewer, or any other head all report false with zero downstream
// effect: no native call happens before every check passes.
func currentReviewOpReady(p factory.Publication, op factory.ReviewOperation) bool {
	if op.Work.HeadOID != p.Candidate {
		return false
	}
	if op.Work.BaseOID != p.PullTarget().BaseOID {
		return false
	}
	if op.Outcome.Effect == factory.OpEffectPending || op.Outcome.Effect == factory.OpEffectIndeterminate ||
		op.Outcome.Completion == factory.OpCompletionPending {
		return false
	}
	return op.Outcome.Effect == factory.OpEffectCommitted && op.Outcome.Completion == factory.OpCompletionComplete
}

func reviewerMatchesPublication(reviewer factory.Assignment, p factory.Publication, owner factory.Assignment) bool {
	return reviewer.Validate() == nil && reviewer.Role == project.RoleReviewer &&
		reviewer.PublicationAssignment == owner.ID && reviewer.AttemptRoot == owner.AttemptRoot &&
		reviewer.SourceCommit == p.Candidate && reviewer.Repository == p.Repository &&
		reviewer.Issue == p.Issue && reviewer.Acceptance == p.Acceptance &&
		reviewer.Succeeded() &&
		reviewer.Result != nil && reviewer.Result.Reported && reviewer.Result.Status == "completed"
}

func (c *Coordinator) currentReviewerForCorrection(ctx context.Context, p factory.Publication, owner factory.Assignment) (factory.Assignment, bool) {
	var empty factory.Assignment
	for i := len(p.ReviewOperations) - 1; i >= 0; i-- {
		op := p.ReviewOperations[i]
		if op.Work.HeadOID != p.Candidate {
			continue
		}
		if !currentReviewOpReady(p, op) {
			return empty, false
		}
		reviewer, err := c.Store.AssignmentByRun(ctx, op.RunID)
		if err != nil || !reviewerMatchesPublication(reviewer, p, owner) {
			return empty, false
		}
		return reviewer, true
	}
	return empty, false
}

// currentReviewReport returns only evidence whose native operation completed
// for the publication's current exact head and base. A pending or refused
// current-head operation blocks replacement review/correction work. The
// verdict resolves through the single canonical consumer with the full
// head/base/cycle/reviewer/carried binding enforced before it routes any
// further work.
func currentReviewOpCurrent(p factory.Publication, op factory.ReviewOperation) (settled, pending bool) {
	if op.Work.BaseOID != p.PullTarget().BaseOID {
		return false, false
	}
	if op.Outcome.Effect == factory.OpEffectPending || op.Outcome.Effect == factory.OpEffectIndeterminate ||
		op.Outcome.Completion == factory.OpCompletionPending {
		return false, true
	}
	return op.Outcome.Effect == factory.OpEffectCommitted && op.Outcome.Completion == factory.OpCompletionComplete, false
}

func reviewAssignmentMatches(assignment factory.Assignment, p factory.Publication) bool {
	return assignment.Validate() == nil && assignment.Role == project.RoleReviewer &&
		assignment.PublicationAssignment == p.AssignmentID && assignment.SourceCommit == p.Candidate &&
		assignment.Succeeded() &&
		assignment.Result != nil && assignment.Result.Reported
}

func (c *Coordinator) resolveCurrentReviewReport(ctx context.Context, p factory.Publication, op factory.ReviewOperation, assignment factory.Assignment) (ResolvedReviewVerdict, bool, bool, bool) {
	var empty ResolvedReviewVerdict
	resolved, err := resolveAssignmentVerdict(assignment)
	if err != nil {
		return empty, false, false, true
	}
	wantEvent := "APPROVED"
	if resolved.Verdict == "request-changes" {
		wantEvent = "REQUEST_CHANGES"
	}
	if op.Work.Disposition() != wantEvent {
		return empty, false, false, true
	}
	if _, _, err := c.bindPhasedReviewVerdict(ctx, p, assignment, p.Candidate, resolved); err != nil {
		return empty, false, false, true
	}
	return resolved, true, false, false
}

func currentReviewOperation(p factory.Publication) (factory.ReviewOperation, bool) {
	for i := len(p.ReviewOperations) - 1; i >= 0; i-- {
		op := p.ReviewOperations[i]
		if op.Work.HeadOID == p.Candidate {
			return op, true
		}
	}
	return factory.ReviewOperation{}, false
}

func (c *Coordinator) currentReviewReport(ctx context.Context, p factory.Publication) (ResolvedReviewVerdict, bool, bool, bool) {
	var empty ResolvedReviewVerdict
	op, found := currentReviewOperation(p)
	if !found {
		return empty, false, false, false
	}
	settled, pending := currentReviewOpCurrent(p, op)
	if pending {
		return empty, false, true, false
	}
	if !settled {
		return empty, false, false, true
	}
	assignment, err := c.Store.AssignmentByRun(ctx, op.RunID)
	if err != nil || !reviewAssignmentMatches(assignment, p) {
		return empty, false, false, true
	}
	return c.resolveCurrentReviewReport(ctx, p, op, assignment)
}

// currentChildCheckAssessment accepts only the current policy's complete
// canonical verdict for this publication's exact current head/base.
func (c *Coordinator) currentChildCheckAssessment(ctx context.Context, p factory.Publication) (*factory.CheckAssessment, bool) {
	assessment, err := c.Store.CheckAssessment(ctx, p.Repository, p.PRNumber)
	if err != nil || assessment.Validate() != nil {
		return nil, false
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil || policy.Validate() != nil {
		return nil, false
	}
	if assessment.Repository != p.Repository || assessment.PRNumber != p.PRNumber || assessment.PRID != p.PRID ||
		assessment.IssueID != p.PullTarget().IssueID || assessment.HeadOID != p.Candidate ||
		assessment.BaseOID != p.PullTarget().BaseOID || assessment.HeadRef != p.PullTarget().HeadRef ||
		assessment.BaseRef != p.PullTarget().BaseRef || assessment.PolicyRevision != policy.Revision ||
		!slices.Equal(assessment.Checks, policy.Checks) || assessment.ChecksDigest != factory.ChecksDigest(policy.Checks) ||
		(assessment.Verdict != factory.CheckPass && assessment.Verdict != factory.CheckFailed) {
		return nil, false
	}
	return &assessment, true
}

func buildReviewCandidatePrep(plan *attemptPlan, p factory.Publication, expectedPrepID string) (source, prep project.Preparation, err error) {
	source = plan.prep.Preparation
	role, err := factory.PhaseRole(plan.phase)
	if err != nil || role != source.Role {
		return project.Preparation{}, project.Preparation{}, errors.New("candidate preparation role differs from its admitted phase")
	}
	prep = project.Preparation{
		ID: expectedPrepID, Project: p.ProjectID, Role: role,
		Requirements: source.Requirements, Approval: source.Approval, SourceCommit: p.Candidate,
		SetupDigest: source.SetupDigest, Tools: append([]string(nil), source.Tools...), Credential: source.Credential,
	}
	if err := prep.Validate(); err != nil {
		return project.Preparation{}, project.Preparation{}, err
	}
	return source, prep, nil
}

func (c *Coordinator) admitReviewCandidatePrep(ctx context.Context, plan *attemptPlan, p factory.Publication, owner factory.Assignment, prep project.Preparation, expectedChildID string, report *PublishReport) (execution.PreparationRecord, bool, error) {
	requested := time.Now().Add(10 * time.Minute).Unix()
	if plan.deadline.Unix() < requested {
		requested = plan.deadline.Unix()
	}
	registration := store.CandidatePreparationRegistration{
		Repository: p.Repository, Issue: p.Issue, ActorID: owner.ActorID, Project: p.ProjectID,
		OwnerAssignment: owner.ID, ChildAssignment: expectedChildID,
		AttemptRoot: owner.AttemptRoot, PublicationID: p.ID, Connection: plan.sponsorship.Connection,
		Authority: plan.effective.Authority, GateRevision: plan.gateRev, Control: plan.control,
		RequestedNotAfter: requested,
	}
	if p.External != nil {
		registration.SourcePull = p.PullSource()
	} else if prep.Role == project.RoleCoder {
		run, err := c.Store.FactoryRun(ctx, p.Run)
		if err != nil || run.Native == nil {
			return execution.PreparationRecord{}, false, errors.New("native correction candidate run unavailable")
		}
		registration.SourceRun = &project.FactoryExport{Project: p.ProjectID, ID: p.Run, Role: project.RoleCoder,
			Preparation: run.Native.Provenance.WorktreeID, Candidate: p.Candidate}
	}
	record := execution.PreparationRecord{Preparation: prep}
	if plan.prep.Native != nil {
		if target, available := c.Host.(execution.PreparationTarget); available {
			address, err := target.CandidatePreparationAddress(prep.Project, prep.ID)
			if err != nil || address.Native == nil || address.Native.Profile != plan.prep.Native.Profile {
				return execution.PreparationRecord{}, false, execution.ErrPreparation
			}
			record.Native = address.Native
		} else {
			ref := *plan.prep.Native
			ref.WorktreeID = prep.ID
			record.Native = &ref
		}
	}
	// Recovery reuses the original native custody and admission, including its
	// expiry. A current controller may inspect it, never replace its instance,
	// grants or control capture with today's values.
	if existing, readErr := c.Store.CandidatePreparation(ctx, prep.ID); readErr == nil {
		prior := existing.Admission
		if prior.Repository != registration.Repository || prior.Issue != registration.Issue || prior.ActorID != registration.ActorID || prior.Project != registration.Project || prior.OwnerAssignment != registration.OwnerAssignment || prior.ChildAssignment != registration.ChildAssignment || prior.AttemptRoot != registration.AttemptRoot || prior.PublicationID != registration.PublicationID || prior.Connection != registration.Connection {
			return execution.PreparationRecord{}, false, store.ErrAttemptRootMismatch
		}
		if existing.Stopped || existing.Retirement == "confirmed" {
			return execution.PreparationRecord{}, false, factory.ErrAttemptClosed
		}
		record.Native = existing.Preparation.Native
		registration = prior
		registration.RequestedNotAfter = existing.NotAfter
	} else if !errors.Is(readErr, store.ErrNotFound) {
		return execution.PreparationRecord{}, false, readErr
	}
	storedRecord, created, err := c.Store.AdmitCandidatePreparation(ctx, record, registration)
	if err != nil {
		// A changed control, lost capacity race or unavailable Store admits
		// nothing. The same owner retries; only a proved identity conflict
		// makes this publication terminal.
		if errors.Is(err, store.ErrCandidatePreparationConflict) || errors.Is(err, store.ErrAttemptRootMismatch) {
			c.fenceChildPublication(ctx, p, report)
		}
		return execution.PreparationRecord{}, false, err
	}
	return storedRecord.Preparation, created, nil
}

func (c *Coordinator) verifyStoredReviewCandidatePrep(ctx context.Context, p factory.Publication, prep project.Preparation, stored execution.PreparationRecord, report *PublishReport) error {
	if stored.Preparation.ID != prep.ID || stored.Preparation.Role != prep.Role ||
		stored.Preparation.Project != prep.Project || stored.Preparation.SourceCommit != prep.SourceCommit ||
		!stored.Preparation.Requirements.Equal(prep.Requirements) || stored.Preparation.Approval != prep.Approval ||
		stored.Preparation.SetupDigest != prep.SetupDigest || stored.Preparation.Credential != prep.Credential ||
		!slices.Equal(stored.Preparation.Tools, prep.Tools) {
		c.fenceChildPublication(ctx, p, report)
		return errors.New("review preparation identity differs")
	}
	return nil
}

func (c *Coordinator) prepareReviewCandidate(ctx context.Context, p factory.Publication, owner factory.Assignment, plan *attemptPlan, report *PublishReport, expectedPrepID, expectedChildID string) (project.Preparation, bool, error) {
	source, prep, err := buildReviewCandidatePrep(plan, p, expectedPrepID)
	if err != nil {
		return project.Preparation{}, false, err
	}
	stored, created, err := c.admitReviewCandidatePrep(ctx, plan, p, owner, prep, expectedChildID, report)
	if err != nil {
		return project.Preparation{}, false, err
	}
	if err := c.verifyStoredReviewCandidatePrep(ctx, p, prep, stored, report); err != nil {
		return project.Preparation{}, false, err
	}
	admitted, err := c.Store.CandidatePreparation(ctx, prep.ID)
	if err != nil {
		return project.Preparation{}, false, err
	}
	if !stored.State.Ready && admitted.NotAfter <= time.Now().Unix() {
		// The original send deadline is immutable. Fence and retire its exact
		// receiver identity rather than retrying it or extending its authority.
		c.fenceChildPublication(ctx, p, report)
		return project.Preparation{}, false, errors.New("review preparation deadline elapsed")
	}
	if out, ready, err, proceed := c.reconcileStoredReviewCandidatePrep(ctx, p, prep, stored, created, report); !proceed {
		return out, ready, err
	}
	var bundle []byte
	if prep.Role == project.RoleReviewer && p.External == nil {
		bundle, err = c.provePublishedCandidateForReview(ctx, p, owner, report)
		if err != nil {
			return project.Preparation{}, false, err
		}
	}
	return c.launchReviewCandidatePrep(ctx, p, prep, source, bundle, stored, report)
}

func (c *Coordinator) reconcileStoredReviewCandidatePrep(ctx context.Context, p factory.Publication, prep project.Preparation, stored execution.PreparationRecord, created bool, report *PublishReport) (project.Preparation, bool, error, bool) {
	if stored.State.Ready {
		if !candidatePreparationStateMatches(stored.State, prep) {
			c.fenceChildPublication(ctx, p, report)
			return project.Preparation{}, false, errors.New("review preparation state differs"), false
		}
		return prep, true, nil, false
	}
	if !created {
		state, inspectErr := c.Host.InspectWorkspacePreparation(ctx, stored.Address())
		if inspectErr == nil {
			if state.ID != "" && !candidatePreparationIdentityMatches(state, prep) {
				c.fenceChildPublication(ctx, p, report)
				return project.Preparation{}, false, errors.New("native review preparation differs"), false
			}
			if err := c.observeCandidatePreparation(ctx, stored, state); err != nil {
				return project.Preparation{}, false, err, false
			}
			if state.Ready {
				return prep, true, nil, false
			}
			if state.Phase == project.PrepareRunning {
				return project.Preparation{}, false, nil, false
			}
			if state.Phase == project.PrepareFailed || state.Phase == project.PrepareStopped || state.Phase == project.PrepareInterrupted {
				c.fenceChildPublication(ctx, p, report)
				return project.Preparation{}, false, errors.New("review preparation requires intervention"), false
			}
		} else if !errors.Is(inspectErr, execution.ErrPreparationNotObserved) {
			return project.Preparation{}, false, inspectErr, false
		}
	}
	return project.Preparation{}, false, nil, true
}

func (c *Coordinator) provePublishedCandidateForReview(ctx context.Context, p factory.Publication, owner factory.Assignment, report *PublishReport) ([]byte, error) {
	parentRun, err := c.Store.FactoryRun(ctx, p.Run)
	if err != nil || parentRun.Role != project.RoleCoder || !parentRun.Reconciled || parentRun.Outcome != factory.Succeeded {
		return nil, errors.New("published candidate run is not settled")
	}
	candidateAssignment, err := c.Store.AssignmentByRun(ctx, p.Run)
	origin := owner
	if owner.RetainedPublication != nil && p.Run == owner.RetainedPublication.Run {
		origin, err = c.Store.PublicationExecutionOwner(ctx, p, owner)
		if err != nil || p.Candidate != owner.RetainedPublication.Candidate {
			return nil, errors.New("retained candidate origin changed")
		}
	}
	if err != nil || candidateAssignment.Validate() != nil || candidateAssignment.Role != project.RoleCoder ||
		candidateAssignment.PublicationAssignment != origin.ID || candidateAssignment.AttemptRoot != origin.AttemptRoot ||
		!candidateAssignment.Succeeded() ||
		candidateAssignment.Result == nil || !candidateAssignment.Result.Reported || candidateAssignment.Result.Status != "completed" ||
		candidateAssignment.Result.Candidate != p.Candidate {
		return nil, errors.New("published candidate lacks its exact recorded report")
	}
	bundle, terminal, wait := c.exportCandidate(ctx, p, parentRun)
	if wait != nil {
		return nil, errors.New(wait.Detail)
	}
	if terminal != nil {
		c.fenceChildPublication(ctx, p, report)
		return nil, errors.New("published candidate export is invalid")
	}
	return bundle, nil
}

func (c *Coordinator) launchReviewCandidatePrep(ctx context.Context, p factory.Publication, prep, source project.Preparation, bundle []byte, stored execution.PreparationRecord, report *PublishReport) (project.Preparation, bool, error) {
	notAfter, err := c.Store.AuthorizeCandidatePreparation(ctx, prep.ID)
	if err != nil {
		return project.Preparation{}, false, err
	}
	deadline := time.Unix(notAfter, 0)
	if !time.Now().Before(deadline) {
		return project.Preparation{}, false, errors.New("review preparation deadline elapsed")
	}
	input := project.FactoryCandidate{Preparation: prep, SourcePreparation: source.ID, Bundle: bundle, Deadline: deadline}
	if prep.Role == project.RoleCoder || p.External != nil {
		admitted, err := c.Store.CandidatePreparation(ctx, prep.ID)
		if err != nil {
			return project.Preparation{}, false, err
		}
		input.SourceRun = admitted.Admission.SourceRun
		input.SourcePull = admitted.Admission.SourcePull
		if input.SourceRun == nil && input.SourcePull == nil {
			return project.Preparation{}, false, errors.New("native correction candidate source missing")
		}
	}
	if err := input.Validate(); err != nil {
		return project.Preparation{}, false, err
	}
	state, err := c.Host.PrepareWorkspaceCandidate(ctx, execution.CandidatePreparationRequest{Input: input, Native: stored.Native})
	if err != nil {
		return project.Preparation{}, false, err
	}
	if state.ID != prep.ID || state.Project != p.ProjectID || state.Role != prep.Role ||
		state.SourceCommit != p.Candidate || state.SetupDigest != prep.SetupDigest {
		c.fenceChildPublication(ctx, p, report)
		return project.Preparation{}, false, errors.New("native review preparation receipt differs")
	}
	if err := c.observeCandidatePreparation(ctx, stored, state); err != nil {
		return project.Preparation{}, false, err
	}
	if !state.Ready {
		if state.Phase == project.PrepareFailed || state.Phase == project.PrepareStopped || state.Phase == project.PrepareInterrupted {
			c.fenceChildPublication(ctx, p, report)
			return project.Preparation{}, false, errors.New("review preparation requires intervention")
		}
		return project.Preparation{}, false, nil
	}
	return prep, true, nil
}

func candidatePreparationStateMatches(state execution.PreparationState, prep project.Preparation) bool {
	return candidatePreparationIdentityMatches(state, prep) && state.Ready && !state.Stopped
}

func candidatePreparationIdentityMatches(state execution.PreparationState, prep project.Preparation) bool {
	return state.ID == prep.ID && state.Project == prep.Project && state.Role == prep.Role &&
		state.SourceCommit == prep.SourceCommit && state.SetupDigest == prep.SetupDigest
}

func (c *Coordinator) observeCandidatePreparation(ctx context.Context, prior execution.PreparationRecord, state execution.PreparationState) error {
	current, err := c.Store.Preparation(ctx, prior.Preparation.ID)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(current.State, state) {
		return nil
	}
	current.State = state
	if err := c.Store.ObservePreparation(ctx, current); err != nil {
		latest, readErr := c.Store.Preparation(ctx, prior.Preparation.ID)
		if readErr == nil && reflect.DeepEqual(latest.State, state) {
			return nil
		}
		return err
	}
	return nil
}

func (c *Coordinator) executePublicationChild(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, plan *attemptPlan, p factory.Publication, owner factory.Assignment, assignmentID, preparationID, role string, correctionPlan *factory.InvestigationPlan, report *PublishReport) {
	now := time.Now()
	runID := factory.NewID()
	// A phased corrector stages its controller-derived singleton plan as
	// the actual correct-phase manifest bytes (C15 wire) with no child
	// concurrency. Reviewers stage no manifest.
	var manifest []byte
	var manifestDigest string
	if plan.phase == project.ExecutionCorrect {
		if correctionPlan == nil {
			publicationError(report, p.ID, "child_assignment_unrecorded")
			return
		}
		selection := plan.policy.Roles[role]
		staged, err := stageExecutionManifest(correctionPlan, runID, plan.deadline, selection.Profile, project.ExecutionCorrect, 0)
		if err != nil {
			publicationError(report, p.ID, "child_assignment_unrecorded")
			return
		}
		manifest, manifestDigest = staged, project.ExecutionManifestDigest(staged)
	}
	authority := plan.effective.Authority
	authority.RequirementsID, authority.ApprovalID = plan.requirement, plan.approval
	assignment := factory.Assignment{
		AcceptedContext: plan.acceptedContext,
		ReviewContext:   plan.reviewContext,
		Authority:       authority,
		ID:              assignmentID, AttemptRoot: owner.AttemptRoot, PublicationAssignment: owner.ID,
		Subject: plan.subject, Phase: plan.phase, ParentAssignment: plan.parent,
		ProjectID: owner.ProjectID, Role: role,
		Repository: p.Repository, Issue: p.Issue, NativeRev: plan.inputs.Revision,
		Acceptance: owner.Acceptance, Preparation: preparationID,
		Harness: plan.tool.Harness + "-" + plan.tool.Version, HarnessVers: plan.tool.Version,
		AdoptedHarness: plan.tool.appliancePin(),
		Model:          plan.policy.Roles[role].Model, Connection: plan.sponsorship.Connection,
		SourceCommit: p.Candidate, Prompt: plan.prompt, PromptSHA: plan.promptSHA,
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(), ActorID: owner.ActorID,
	}
	reservation := factory.Reservation{
		AssignmentID: assignmentID, Repository: p.Repository, Connection: plan.sponsorship.Connection,
		State: factory.ReservationHeld, PlannedMinutes: plan.planned,
	}
	run := factory.Run{
		ID: runID, ProjectID: owner.ProjectID, Role: role, InputSHA: p.Candidate,
		ContextPending: deps.mcpConfigured(), ExecutionManifest: manifest,
		Started: now, Deadline: plan.deadline,
		Image: plan.tool.Image, Harness: assignment.Harness, Model: assignment.Model,
	}
	view := factory.RunView{RunID: runID, Repository: p.Repository, Issue: p.Issue, Attempt: assignmentID}
	if err := plan.recordPacket(ctx, deps.Store, assignment, reservation, run, view); err != nil {
		if errors.Is(err, store.ErrAssignmentActive) || errors.Is(err, store.ErrAdmissionChanged) || errors.Is(err, store.ErrDispatchControlStale) {
			report.Waits = append(report.Waits, PublishWait{ID: p.ID, Reason: "child_admission_changed", Detail: err.Error()})
			return
		}
		if wait := admissionWait(err); wait != nil {
			publicationWait(report, p.ID, wait.Reason)
			return
		}
		publicationError(report, p.ID, "child_assignment_unrecorded")
		return
	}
	occupancy.hold(reservation)
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: runID, Project: owner.ProjectID, Role: role,
			Preparation: preparationID, Harness: plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model: assignment.Model, Assignment: assignment.PromptSHA, SourceCommit: p.Candidate,
			Connection: plan.sponsorship.Connection, Actor: assignment.ActorID, Deadline: plan.deadline,
			ExecutionPhase: plan.phase, ManifestDigest: manifestDigest,
		},
		Prompt: plan.prompt, HarnessSHA256: plan.tool.SHA256,
		ExecutionManifest: manifest,
	}
	if err := launch.Validate(); err != nil {
		publicationError(report, p.ID, "child_launch_invalid")
		return
	}
	if plan.phase == project.ExecutionCorrect {
		if err := verifyStagedManifest(launch); err != nil {
			publicationError(report, p.ID, "child_launch_invalid")
			return
		}
	}
	tail := newDispatchReport()
	launchTail(ctx, deps, assignment, run, launch, &tail)
	for _, wait := range tail.Waits {
		publicationWait(report, p.ID, wait.Reason)
	}
	for _, failure := range tail.Errors {
		publicationError(report, p.ID, failure.Reason)
	}
}

func existingMatchesPublicationChild(expectedID string, a factory.Assignment, p factory.Publication, owner factory.Assignment, role, phase, parent string) bool {
	if !(a.Validate() == nil && a.ID == expectedID &&
		a.PublicationAssignment == owner.ID && a.AttemptRoot == owner.AttemptRoot &&
		a.Repository == p.Repository && a.Issue == p.Issue && a.Acceptance == p.Acceptance &&
		a.Role == role && a.SourceCommit == p.Candidate) {
		return false
	}
	return a.Phase == phase && a.ParentAssignment == parent &&
		a.Subject.Kind == factory.SubjectIssue && a.Subject.Issue != nil &&
		a.Subject.Issue.Issue == p.Issue && a.Subject.Issue.Acceptance == p.Acceptance
}

func publicationChildID(publication, head, role string) string {
	hash := sha256.Sum256([]byte("soda-factory-child-v1\x00" + publication + "\x00" + head + "\x00" + role))
	return hex.EncodeToString(hash[:16])
}

func publicationPreparationID(publication, head string) string {
	hash := sha256.Sum256([]byte("soda-factory-review-preparation-v1\x00" + publication + "\x00" + head))
	return "f" + hex.EncodeToString(hash[:12])
}

func buildPublicationChildPrompt(plan *attemptPlan, p factory.Publication, owner factory.Assignment, preparationID, role string, typedReview *factory.ReviewCycleReport, typedClear *factory.ClearReviewReport, checks *factory.CheckAssessment, repositoryContext *project.FactoryPreparationContext) ([]byte, error) {
	selection := plan.policy.Roles[role]
	// Publication children always serve a real execution phase, never an
	// empty phase: reviewer children inspect the published head and
	// coder children correct it. The prompt carries the admitted phase so
	// the template owns phased rendering and the recorded result binds it.
	phase := project.ExecutionCorrect
	if role == project.RoleReviewer {
		phase = project.ExecutionReview
	}
	inputs := factory.PromptInputs{
		Project: plan.projectID, Repository: p.Repository, Issue: p.Issue, NativeRev: plan.inputs.Revision,
		Phase:        phase,
		AcceptanceID: p.Acceptance, TargetBranch: plan.policy.TargetBranch,
		SourceCommit: p.Candidate, ApprovedBase: owner.SourceCommit, BaseCommit: p.CurrentDiffBase(),
		PRBase:                p.PullTarget().BaseOID,
		PublicationAssignment: p.AssignmentID, Preparation: preparationID,
		RequirementsID: plan.requirement, ApprovalID: plan.approval,
		Harness: plan.tool.Harness + "-" + plan.tool.Version, Model: selection.Model, Role: role,
		ProviderConnection: plan.sponsorship.Connection, RequiredChecks: plan.policy.Checks,
		AttemptLimits: plan.attemptLimits,
		Title:         plan.inputs.Issue.Title, Body: plan.inputs.Issue.Body,
		Sources:       promptSections(plan.acceptance.Sources, plan.inputs.Comments),
		Resolutions:   promptSections(plan.acceptance.Resolutions, plan.inputs.Comments),
		Prerequisites: plan.acceptance.Prerequisites, Control: plan.control,
		TypedReview: typedReview, TypedClear: typedClear, CheckAssessment: checks,
		RepositoryContext: repositoryContext,
	}
	if role == project.RoleCoder && plan.prep.Native != nil {
		inputs.PreparedSourceCommit = plan.prep.Preparation.SourceCommit
	}
	prompt, err := factory.BuildDispatchPrompt(inputs)
	if err == nil {
		plan.acceptedContext = factory.CaptureAcceptedContext(inputs)
	}
	return prompt, err
}

func (c *Coordinator) fenceChildPublication(ctx context.Context, p factory.Publication, report *PublishReport) {
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, time.Now().Unix()
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, p); err != nil {
		publicationError(report, p.ID, "child_result_store_conflict")
		return
	}
	report.Fenced = append(report.Fenced, p.ID)
	owner, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil || owner.Validate() != nil {
		publicationWait(report, p.ID, "candidate_preparation_owner_unavailable")
		return
	}
	if err := c.stopCandidatePreparationsForRoot(ctx, p.Repository, p.ProjectID, p.Issue, owner.AttemptRoot); err != nil {
		publicationWait(report, p.ID, "candidate_preparation_retirement_pending")
	}
}
