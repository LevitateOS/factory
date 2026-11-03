package control

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// observeMergeEvidence binds the exact fresh evidence behind one merge
// submit: the independent approval on the exact head, the persisted
// ST11 pass under the current adopted definitions, and a fresh
// check verdict on the merge bracket so regressed checks cannot ride
// an older pass. Missing evidence and blocking reviews wait for a later
// pass; permanently refused or misidentified candidates fail. The bound
// intent is persisted before any submit.
func (c *Coordinator) observeMergeEvidence(ctx context.Context, m *factory.Merge, work factory.MergeWork, policy factory.RepositoryPolicy, revision int64, report *MergeReport) bool {
	observation, err := c.Merges.ObserveMerge(ctx, work)
	if err != nil {
		var refused *factory.PublicationRefusal
		// A blocking independent review leaves the shared attempt open
		// for its normal correction; no merge intent has been recorded.
		if errors.As(err, &refused) && refused.Reason != "changes_requested" {
			c.finishMerge(ctx, *m, factory.MergeFailed, factory.Failed, factory.MergeReasonInvalid, report)
		} else {
			c.mergeCallError(report, m.ID, err)
		}
		return false
	}
	if !checkMergeObservationIdentity(observation, m, revision, report) {
		return false
	}
	assessment, ok := c.loadMergeAssessment(ctx, m, report)
	if !ok {
		return false
	}
	work.AssessmentRevision, work.ReviewID = assessment.Revision, observation.ReviewID
	if !c.verifyStoredMergeEvidence(ctx, work, assessment, policy, m, report) {
		return false
	}
	if !c.verifyFreshMergeChecks(ctx, policy, observation, m, report) {
		return false
	}
	return c.persistMergeIntent(ctx, m, work, observation, report)
}

func checkMergeObservationIdentity(observation factory.MergeObservation, m *factory.Merge, revision int64, report *MergeReport) bool {
	if observation.NativeRev != revision {
		mergeWait(report, m.ID, "native_inputs_changed")
		return false
	}
	if observation.PRID != m.PRID || observation.PRNumber != m.PRNumber || observation.IssueID != m.IssueID ||
		observation.PRAuthorID != m.PRAuthorID || observation.HeadRef != m.HeadRef || observation.BaseRef != m.BaseRef ||
		observation.HeadOID != m.HeadOID || observation.BaseOID != m.BaseOID || observation.ReviewerID != m.ReviewerID || observation.ReviewID <= 0 {
		mergeWait(report, m.ID, "observation_invalid")
		return false
	}
	return true
}

func (c *Coordinator) loadMergeAssessment(ctx context.Context, m *factory.Merge, report *MergeReport) (factory.CheckAssessment, bool) {
	assessment, err := c.Store.CheckAssessment(ctx, m.Repository, m.PRNumber)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			mergeWait(report, m.ID, "check_evidence_missing")
		} else {
			mergeError(report, m.ID, "store_unavailable")
		}
		return factory.CheckAssessment{}, false
	}
	return assessment, true
}

func (c *Coordinator) verifyStoredMergeEvidence(ctx context.Context, work factory.MergeWork, assessment factory.CheckAssessment, policy factory.RepositoryPolicy, m *factory.Merge, report *MergeReport) bool {
	if err := factory.VerifyMergeCheckEvidence(work, assessment, policy); err != nil {
		// A failed verdict on the exact head is terminal: new code
		// means a new publication cycle. Anything else waits for a
		// fresh assessment on this head.
		if assessment.Verdict == factory.CheckFailed && assessment.HeadOID == m.HeadOID && assessment.BaseOID == m.BaseOID {
			c.finishMerge(ctx, *m, factory.MergeFailed, factory.Failed, factory.MergeReasonInvalid, report)
		} else {
			mergeWait(report, m.ID, "check_evidence_stale")
		}
		return false
	}
	return true
}

func (c *Coordinator) verifyFreshMergeChecks(ctx context.Context, policy factory.RepositoryPolicy, observation factory.MergeObservation, m *factory.Merge, report *MergeReport) bool {
	target := factory.CheckTarget{Repository: m.Repository, PRNumber: m.PRNumber, PRID: m.PRID, IssueID: m.IssueID, HeadRef: m.HeadRef, BaseRef: m.BaseRef, HeadOID: m.HeadOID, BaseOID: m.BaseOID}
	adopted := factory.AdoptedChecks{Checks: append([]string(nil), policy.Checks...), PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}
	fresh, err := factory.VerifyChecks(target, adopted, policy, observation.Checks, time.Now().Unix())
	if err != nil {
		mergeError(report, m.ID, "check_evidence_invalid")
		return false
	}
	switch fresh.Verdict {
	case factory.CheckPass:
		return true
	case factory.CheckPending:
		mergeWait(report, m.ID, "check_evidence_pending")
	case factory.CheckRefused:
		mergeWait(report, m.ID, "check_evidence_hidden")
	default:
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.Failed, factory.MergeReasonInvalid, report)
	}
	return false
}

func (c *Coordinator) persistMergeIntent(ctx context.Context, m *factory.Merge, work factory.MergeWork, observation factory.MergeObservation, report *MergeReport) bool {
	now := time.Now()
	notAfter, err := c.Store.CapAttemptDeadline(ctx, m.AssignmentID, now.Add(10*time.Minute).Unix())
	if err != nil {
		mergeWait(report, m.ID, "attempt_deadline_unavailable")
		return false
	}
	work.NativeRev, work.NotAfter = observation.NativeRev, notAfter
	intent := work.Intent()
	op := &m.Operation
	op.OperationID, op.Attempts, op.UpdatedUnix, op.Work = work.OperationID, 1, now.Unix(), &intent
	m.NativeRev, m.ObservedUnix = observation.NativeRev, now.Unix()
	return c.storeMerge(ctx, m, report)
}

// mergeAuthority validates Soda policy and accepted input at the native
// revision that the later merge observation must match.
func (c *Coordinator) mergeAuthority(ctx context.Context, m factory.Merge, report *MergeReport) (factory.RepositoryPolicy, int64, bool) {
	var empty factory.RepositoryPolicy
	effective, err := c.EffectiveAuthority(ctx, m.Repository)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return empty, 0, false
	}
	if !effective.Effective {
		mergeWait(report, m.ID, "authority_ineffective")
		return empty, 0, false
	}
	policy, err := c.Store.RepositoryPolicy(ctx, m.Repository)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return empty, 0, false
	}
	current := effective.Authority
	current.RequirementsID, err = c.Store.RequirementHead(ctx, m.ProjectID)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return empty, 0, false
	}
	current.ApprovalID, err = c.Store.ApprovalHead(ctx, m.ProjectID)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return empty, 0, false
	}
	if !current.SameGrants(m.Authority) || policy.TargetBranch != m.BaseRef || policy.Review.ActorID != m.ReviewerID {
		mergeWait(report, m.ID, "authority_changed")
		return empty, 0, false
	}
	status, err := c.AcceptanceStatus(ctx, m.Repository, strconv.FormatInt(m.Issue, 10))
	if err != nil {
		mergeWait(report, m.ID, "accepted_inputs_unavailable")
		return empty, 0, false
	}
	if !status.Valid || status.Acceptance == nil || status.Acceptance.ID != m.Acceptance {
		mergeWait(report, m.ID, "accepted_inputs_changed")
		return empty, 0, false
	}
	return policy, status.Revision, true
}

func (c *Coordinator) mergeWork(m factory.Merge, policy factory.RepositoryPolicy) factory.MergeWork {
	// The submit identity derives from the persisted authorization
	// generation: every generation drives its own native operation, so
	// a crash or lost reply reconciles by exact lookup and a
	// replacement never reuses a finalized identity.
	generation := 1
	if m.Authorization.Bound() {
		generation = m.Authorization.Generation.Generation
	}
	return factory.MergeWork{
		MergeID: m.ID, PublicationID: m.PublicationID,
		OperationID: factory.MergeOperationIDForGeneration(m.PublicationID, generation), AuthRevision: factory.MergeAuthRevision(m.AssignmentID, m.ID, m.Revision),
		HeadRef: m.HeadRef, BaseRef: m.BaseRef, HeadOID: m.HeadOID, BaseOID: m.BaseOID,
		Repository: m.Repository, Issue: m.Issue, PRNumber: m.PRNumber, PRID: m.PRID, IssueID: m.IssueID,
		PRAuthorID: m.PRAuthorID, ReviewerID: m.ReviewerID, ActorID: policy.Merge.ActorID,
	}
}

// completeMerge confirms the native bookkeeping behind a committed,
// completed merge before finishing: the PR merged to the exact head,
// the exact base tip equals or ancestrally contains it, and the issue
// closed. Confirmed completion
// then reassesses dependants so an eligible one becomes runnable.
func (c *Coordinator) completeMerge(ctx context.Context, m *factory.Merge, report *MergeReport) {
	op := &m.Operation
	if op.Completion != factory.OpCompletionComplete {
		mergeWait(report, m.ID, "native_completion_pending")
		return
	}
	if op.Work == nil {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		return
	}
	work := op.Work.Apply(c.mergeWork(*m, factory.RepositoryPolicy{}))
	confirmation, err := c.Merges.ObserveCompletion(ctx, work)
	if err != nil {
		var wait *factory.PublicationWait
		if errors.As(err, &wait) {
			mergeWait(report, m.ID, wait.Reason)
			return
		}
		var refused *factory.PublicationRefusal
		if errors.As(err, &refused) {
			c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		} else {
			c.mergeCallError(report, m.ID, err)
		}
		return
	}
	if confirmation.MergedCommit != m.HeadOID || confirmation.BaseTip == "" || confirmation.BaseTip != m.HeadOID && !confirmation.BaseContainsMergedCommit || confirmation.MergerID != work.ActorID || !confirmation.IssueClosed || confirmation.MergedUnix <= 0 || confirmation.ClosedUnix <= 0 {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		return
	}
	if !confirmation.OriginalClosed || !factory.ValidNativeOccurrence(confirmation.OriginalClosingOccurrence) || confirmation.OriginalClosedUnix <= 0 {
		mergeWait(report, m.ID, "original_issue_closure_pending")
		return
	}
	m.MergedCommit, m.MergedUnix, m.ClosedUnix = confirmation.MergedCommit, confirmation.MergedUnix, confirmation.ClosedUnix
	m.OriginalClosingOccurrence, m.OriginalClosedUnix = confirmation.OriginalClosingOccurrence, confirmation.OriginalClosedUnix
	if !c.finishMerge(ctx, *m, factory.MergeMerged, factory.Succeeded, factory.MergeReasonMerged, report) {
		return
	}
	// Optional lifecycle effect: completion note on the original issue.
	// Never gates default delivery.
	// Best-effort dependant release: confirmed completion reassesses
	// dependants, and queued work launches within current limits.
	// Failures wait for the next trigger; the merge above already
	// recorded.
	// UpdateMerge atomically queued the completion root. The outer
	// MergePass drains it once after its publication/settlement work.
	c.dispatchAfterIntake(ctx)
}
