package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// Reconciliation never depends on current actor credentials or open
// dispatch. Those are prerequisites for new writes, not for learning
// old effects.
func (c *Coordinator) reconcileMerge(ctx context.Context, m factory.Merge, report *MergeReport) {
	if m.Stage != factory.MergeOpen && m.Stage != factory.MergeFenced {
		return
	}
	p, ok := c.admitMergeRow(ctx, &m, report)
	if !ok {
		return
	}
	if !c.adoptObservedMergeOp(ctx, &m, report) {
		return
	}
	if !c.adoptMergeReceipt(ctx, &m, report) {
		return
	}
	if !c.recheckMergeGate(ctx, &m, report) {
		return
	}
	c.advanceMergeOperation(ctx, p, m, report)
}

// admitMergeRow loads the publication behind one merge and applies
// expiry and dispatch gates before any effect reconciliation.
func (c *Coordinator) admitMergeRow(ctx context.Context, m *factory.Merge, report *MergeReport) (factory.Publication, bool) {
	var none factory.Publication
	p, err := c.Store.PublicationByAssignment(ctx, m.AssignmentID)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return none, false
	}
	// Expiry withdraws an undriven merge even when review never arrives.
	// Recorded effects still go through the same cancellation and completion
	// reconciliation; closing a root cannot erase a committed native merge.
	closed, err := false, error(nil)
	if m.RetryCommand != "" {
		_, err = c.Store.MergeSendDeadline(ctx, *m, time.Now().Add(mergeIntentLifetime).Unix())
		closed = errors.Is(err, factory.ErrAttemptClosed)
		if closed {
			err = nil
		}
	} else {
		closed, err = c.Store.CloseExpiredAttempt(ctx, m.AssignmentID)
	}
	if err != nil {
		mergeError(report, m.ID, "attempt_expiry_unavailable")
		return none, false
	}
	if closed {
		c.withdrawMerge(ctx, m, report)
		return none, false
	}
	open, _, _, err := c.Store.DispatchState(ctx, m.Repository)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return none, false
	}
	if m.WithdrawRequested || !open {
		c.withdrawMerge(ctx, m, report)
		return none, false
	}
	return p, true
}

// adoptObservedMergeOp adopts the native outcome behind one driven
// merge operation.
func (c *Coordinator) adoptObservedMergeOp(ctx context.Context, m *factory.Merge, report *MergeReport) bool {
	op := &m.Operation
	if op.Attempts == 0 {
		return true
	}
	outcome, err := c.Merges.LookupOp(ctx, op.OperationID)
	if err != nil {
		c.mergeCallError(report, m.ID, err)
		return false
	}
	if outcome.NotObserved {
		return true
	}
	if op.SameOutcome(outcome) {
		return true
	}
	if !op.Adopt(outcome, time.Now().Unix()) {
		c.fenceUnattributedMerge(ctx, *m, report)
		return false
	}
	return c.storeMerge(ctx, m, report)
}

// recheckMergeGate rechecks the dispatch gate after recording,
// withdrawing on a concurrent withdrawal or closed gate.
func (c *Coordinator) recheckMergeGate(ctx context.Context, m *factory.Merge, report *MergeReport) bool {
	open, _, _, err := c.Store.DispatchState(ctx, m.Repository)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return false
	}
	if m.WithdrawRequested || !open {
		c.withdrawMerge(ctx, m, report)
		return false
	}
	return true
}

// advanceMergeOperation routes one open merge's settled effect, then
// drives its operation toward observe, submit and settle.
func (c *Coordinator) advanceMergeOperation(ctx context.Context, p factory.Publication, m factory.Merge, report *MergeReport) {
	if c.routeMergeEffect(ctx, &m, report) {
		return
	}
	if !c.reconcileMergeTarget(ctx, &m, p, report) {
		return
	}
	op := &m.Operation
	// A proved unallocated generation cannot send after its deadline. Hold
	// before provider I/O; a fresh explicit preparation owns any later send.
	// Recorded effects above still reconcile under their original identity.
	if m.Authorization.Bound() && op.Attempts == 0 && op.Work == nil && time.Now().Unix() >= m.Authorization.Generation.NotAfter {
		mergeWait(report, m.ID, "merge_authorization_expired")
		return
	}
	policy, revision, ok := c.admitMergeAuthority(ctx, &m, &p, op, report)
	if !ok {
		return
	}
	work := c.mergeWork(m, policy)
	if review, found := currentReviewOperation(p); found {
		work.ReviewEvent = review.Work.Event
	}
	if op.Work == nil {
		if !c.observeMergeEvidence(ctx, p, &m, work, policy, revision, report) {
			return
		}
	}
	work = op.Work.Apply(work)
	if work.ReviewEvent == "COMMENT" && !c.currentMergeReview(ctx, p, m) {
		mergeWait(report, m.ID, "agent_review_pending")
		return
	}
	// Recheck the local gate after recording. A concurrent withdrawal
	// cancels this same recorded identity, including a submit delayed
	// until after cancellation.
	if !c.recheckSubmitMergeGate(ctx, &m, report) {
		return
	}
	if !c.submitMergeOp(ctx, &m, op, work, report) {
		return
	}
	if !c.adoptMergeReceipt(ctx, &m, report) {
		return
	}
	c.settleMergeOp(ctx, m, op.Effect, report)
}

// routeMergeEffect completes, fails or fences one merge whose effect
// already settled, reopening a fenced stage for retry.
func (c *Coordinator) routeMergeEffect(ctx context.Context, m *factory.Merge, report *MergeReport) bool {
	// A committed merge is retained even if native completion remains
	// unfinished. The ref effect never finishes the bookkeeping alone.
	if m.Operation.Effect == factory.OpEffectCommitted {
		c.completeMerge(ctx, m, report)
		return true
	}
	if m.Operation.Effect == factory.OpEffectNotCommitted {
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.MergeReasonRefused, report)
		return true
	}
	if m.Operation.Effect == factory.OpEffectIndeterminate {
		// Already fenced for this reason: wait instead of rewriting the row.
		if settled := *m; !settled.Settle(factory.MergeFenced, factory.MergeReasonFenced, 0) {
			mergeWait(report, m.ID, factory.MergeReasonFenced)
			return true
		}
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.MergeReasonFenced, report)
		return true
	}
	if m.Reopen() {
		if !c.storeMerge(ctx, m, report) {
			return true
		}
	}
	return false
}

// reconcileMergeTarget follows the newest published head behind one
// undriven merge row, failing other target changes.
func (c *Coordinator) reconcileMergeTarget(ctx context.Context, m *factory.Merge, p factory.Publication, report *MergeReport) bool {
	if !factory.MergeTargetChanged(*m, p) {
		return true
	}
	// An undriven row follows the newest published head: a
	// correction supersedes the creation tip, so rebase the row
	// onto it instead of failing the merge. Any other target
	// change, or a row that already drove, keeps the invalid
	// failure.
	if !mergeRebaseMatches(*m, p) {
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.MergeReasonInvalid, report)
		return false
	}
	m.HeadOID = p.Candidate
	// A rebased row drops its stale binding: the authorization
	// never moves to another head, and the new head waits for
	// re-authorization behind fresh review and judgment.
	m.Authorization = factory.MergeAuthorization{}
	return c.storeMerge(ctx, m, report)
}

// mergeRebaseMatches reports whether one undriven merge row may follow
// the newest published head.
func mergeRebaseMatches(m factory.Merge, p factory.Publication) bool {
	op := m.Operation
	return op.Attempts == 0 && op.Work == nil && op.Effect == "" &&
		m.Repository == p.Repository && m.Issue == p.Issue &&
		m.PRNumber == p.PRNumber && m.PRID == p.PRID &&
		m.HeadRef == p.PullTarget().HeadRef && m.BaseRef == p.PullTarget().BaseRef &&
		m.BaseOID == p.PullTarget().BaseOID
}

// admitMergeAuthority resolves the authority bracket and union gate
// behind one open merge operation.
func (c *Coordinator) admitMergeAuthority(ctx context.Context, m *factory.Merge, p *factory.Publication, op *factory.MergeOperation, report *MergeReport) (factory.RepositoryPolicy, int64, bool) {
	var none factory.RepositoryPolicy
	if p.Stage != factory.PublicationPublished {
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.MergeReasonInvalid, report)
		return none, 0, false
	}
	// A superseded or closed attempt cannot acquire its first merge operation.
	// Check the local owner before accepted-input reads; recorded operations
	// still reconcile above under their original identity and receipts.
	if op.Attempts == 0 && op.Work == nil && op.Effect == "" {
		if !c.currentMergeAttempt(ctx, *m, report) {
			return none, 0, false
		}
		// A findings COMMENT needs a journaled funded acceptance. Polling
		// current inputs cannot supply that missing local receipt, and the
		// separate delivery producer remains responsible for creating it.
		if !m.Authorization.Bound() && !hasFundedDeliveryAcceptance(*p) {
			if review, found := currentReviewOperation(*p); found && currentReviewOpReady(*p, review) && review.Work.Event == "COMMENT" && review.Work.Disposition() == "REQUEST_CHANGES" {
				mergeWait(report, m.ID, "funded_delivery_unavailable")
				return none, 0, false
			}
		}
	}
	policy, revision, allowed := c.mergeAuthority(ctx, *m, report)
	if !allowed {
		return none, 0, false
	}
	if err := c.authorizeCleanDeliveryMerge(ctx, m, p, revision); err != nil {
		c.mergeCallError(report, m.ID, err)
		return none, 0, false
	}
	// The common merge union gate stands between authority and any new
	// native mutation: unbound rows wait for their authorization, and
	// stale bindings wait instead of merging another candidate. Recorded
	// effects above already reconciled without entering it.
	if err := mergeAuthorizationGate(*m, *p); err != nil {
		c.mergeCallError(report, m.ID, err)
		return none, 0, false
	}
	if op.Cancellation != "" && op.Cancellation != factory.OpCancelNone {
		mergeWait(report, m.ID, "cancellation_pending")
		return none, 0, false
	}
	if op.Effect == factory.OpEffectPending {
		mergeWait(report, m.ID, "merge_pending")
		return none, 0, false
	}
	return policy, revision, true
}

func (c *Coordinator) currentMergeAttempt(ctx context.Context, m factory.Merge, report *MergeReport) bool {
	latest, err := c.Store.LatestIssueAssignment(ctx, m.Repository, m.Issue)
	if err != nil {
		mergeError(report, m.ID, "issue_assignment_unavailable")
		return false
	}
	if latest.PublicationAssignment != m.AssignmentID || latest.Acceptance != m.Acceptance {
		mergeWait(report, m.ID, "authority_changed")
		return false
	}
	allowance, err := c.Store.AttemptAllowance(ctx, m.Repository, m.Issue)
	if err != nil {
		mergeError(report, m.ID, "attempt_allowance_unavailable")
		return false
	}
	if allowance.RootAssignment != latest.AttemptRoot {
		mergeWait(report, m.ID, "authority_changed")
		return false
	}
	// A fresh, finite merge-only command may use a closed inference allowance;
	// admitMergeRow already checks its separate send deadline and authority.
	if allowance.Closed && m.RetryCommand == "" {
		mergeWait(report, m.ID, factory.ErrAttemptClosed.Error())
		return false
	}
	control, err := c.Store.IssueControl(ctx, m.Repository, m.Issue)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		mergeError(report, m.ID, "issue_readiness_unavailable")
		return false
	}
	if err == nil && control.Acceptance == m.Acceptance && control.HoldsClosedIssue() {
		control, err = c.refreshReadiness(ctx, control)
		if err != nil {
			mergeWait(report, m.ID, "accepted_inputs_unavailable")
			return false
		}
		if control.HoldsClosedIssue() {
			mergeWait(report, m.ID, WaitIssueClosed)
			return false
		}
	}
	return true
}

// recheckSubmitMergeGate rechecks the local gate after recording the
// attempt, withdrawing on a concurrently closed gate.
func (c *Coordinator) recheckSubmitMergeGate(ctx context.Context, m *factory.Merge, report *MergeReport) bool {
	open, _, _, err := c.Store.DispatchState(ctx, m.Repository)
	if err != nil {
		mergeError(report, m.ID, "store_unavailable")
		return false
	}
	if !open {
		c.withdrawMerge(ctx, m, report)
		return false
	}
	return true
}

// submitMergeOp submits one recorded merge whose native effect is
// still empty.
func (c *Coordinator) submitMergeOp(ctx context.Context, m *factory.Merge, op *factory.MergeOperation, work factory.MergeWork, report *MergeReport) bool {
	if op.Effect != "" {
		return true
	}
	outcome, err := c.Merges.SubmitMerge(ctx, work)
	if err != nil {
		c.mergeCallError(report, m.ID, err)
		return false
	}
	if outcome.NotObserved {
		mergeWait(report, m.ID, "submit_unconfirmed")
		return false
	}
	if !op.Adopt(outcome, time.Now().Unix()) {
		c.fenceUnattributedMerge(ctx, *m, report)
		return false
	}
	return c.storeMerge(ctx, m, report)
}

// settleMergeOp settles one submitted merge by its effect.
func (c *Coordinator) settleMergeOp(ctx context.Context, m factory.Merge, effect collaboration.EffectState, report *MergeReport) {
	switch effect {
	case factory.OpEffectCommitted:
		c.completeMerge(ctx, &m, report)
	case factory.OpEffectNotCommitted:
		c.finishMerge(ctx, m, factory.MergeFailed, factory.MergeReasonRefused, report)
	case factory.OpEffectIndeterminate:
		c.finishMerge(ctx, m, factory.MergeFenced, factory.MergeReasonFenced, report)
	default:
		mergeWait(report, m.ID, "operation_pending")
	}
}

func (c *Coordinator) adoptMergeReceipt(ctx context.Context, m *factory.Merge, report *MergeReport) bool {
	op := &m.Operation
	if op.Effect != factory.OpEffectCommitted {
		return true
	}
	if op.Work == nil {
		c.fenceUnattributedMerge(ctx, *m, report)
		return false
	}
	work := op.Work.Apply(c.mergeWork(*m, factory.RepositoryPolicy{}))
	result, err := c.Merges.AdoptMerge(ctx, work, op.Outcome())
	if err != nil {
		c.fenceUnattributedMerge(ctx, *m, report)
		return false
	}
	method := result.Method
	if method == "" {
		method = factory.MergeFastForward
	}
	if method != op.Work.MergeMethod() || !op.Work.AcceptsResult(result.MergedCommit) ||
		result.PRNumber != work.PRNumber || result.PRID != work.PRID || result.IssueID != work.IssueID || result.ActorID != work.ActorID ||
		result.HeadRef != work.HeadRef || result.BaseRef != work.BaseRef || result.HeadOID != work.HeadOID || result.BaseOID != work.BaseOID {
		c.fenceUnattributedMerge(ctx, *m, report)
		return false
	}
	op.PRNumber, op.PRID, op.IssueID = result.PRNumber, result.PRID, result.IssueID
	op.HeadRef, op.BaseRef, op.HeadOID, op.BaseOID, op.MergedCommit = result.HeadRef, result.BaseRef, result.HeadOID, result.BaseOID, result.MergedCommit
	return c.storeMerge(ctx, m, report)
}
