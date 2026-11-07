package control

import (
	"context"
	"time"

	"github.com/levitateos/factory/pkg/factory"
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
	closed, err := c.Store.CloseExpiredAttempt(ctx, m.AssignmentID)
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
	if !c.adoptMergeObserved(op, outcome, time.Now()) {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
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
	if work.ReviewEvent == "COMMENT" && !c.currentPublicationApproved(ctx, p) {
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
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.Failed, factory.MergeReasonRefused, report)
		return true
	}
	if m.Operation.Effect == factory.OpEffectIndeterminate {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonFenced, report)
		return true
	}
	if m.Stage == factory.MergeFenced {
		m.Stage, m.Outcome, m.Reason, m.FinishedUnix = factory.MergeOpen, "", "", 0
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
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.Failed, factory.MergeReasonInvalid, report)
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
		m.HeadRef == p.PRCreate.HeadRef && m.BaseRef == p.PRCreate.BaseRef &&
		m.BaseOID == p.PRCreate.BaseOID
}

// admitMergeAuthority resolves the authority bracket and union gate
// behind one open merge operation.
func (c *Coordinator) admitMergeAuthority(ctx context.Context, m *factory.Merge, p *factory.Publication, op *factory.MergeOperation, report *MergeReport) (factory.RepositoryPolicy, int64, bool) {
	var none factory.RepositoryPolicy
	if p.Stage != factory.PublicationPublished {
		c.finishMerge(ctx, *m, factory.MergeFailed, factory.Failed, factory.MergeReasonInvalid, report)
		return none, 0, false
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
	if !c.adoptMergeObserved(op, outcome, time.Now()) {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		return false
	}
	return c.storeMerge(ctx, m, report)
}

// settleMergeOp settles one submitted merge by its effect.
func (c *Coordinator) settleMergeOp(ctx context.Context, m factory.Merge, effect string, report *MergeReport) {
	switch effect {
	case factory.OpEffectCommitted:
		c.completeMerge(ctx, &m, report)
	case factory.OpEffectNotCommitted:
		c.finishMerge(ctx, m, factory.MergeFailed, factory.Failed, factory.MergeReasonRefused, report)
	case factory.OpEffectIndeterminate:
		c.finishMerge(ctx, m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonFenced, report)
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
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		return false
	}
	work := op.Work.Apply(c.mergeWork(*m, factory.RepositoryPolicy{}))
	result, err := c.Merges.AdoptMerge(ctx, work, mergeOperationOutcomeOf(*op))
	if err != nil {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		return false
	}
	method := result.Method
	if method == "" {
		method = factory.MergeFastForward
	}
	if method != op.Work.MergeMethod() || !op.Work.AcceptsResult(result.MergedCommit) ||
		result.PRNumber != work.PRNumber || result.PRID != work.PRID || result.IssueID != work.IssueID || result.ActorID != work.ActorID ||
		result.HeadRef != work.HeadRef || result.BaseRef != work.BaseRef || result.HeadOID != work.HeadOID || result.BaseOID != work.BaseOID {
		c.finishMerge(ctx, *m, factory.MergeFenced, factory.NeedsHuman, factory.MergeReasonUnattributed, report)
		return false
	}
	op.PRNumber, op.PRID, op.IssueID = result.PRNumber, result.PRID, result.IssueID
	op.HeadRef, op.BaseRef, op.HeadOID, op.BaseOID, op.MergedCommit = result.HeadRef, result.BaseRef, result.HeadOID, result.BaseOID, result.MergedCommit
	return c.storeMerge(ctx, m, report)
}

func (c *Coordinator) adoptMergeObserved(op *factory.MergeOperation, outcome factory.OperationOutcome, now time.Time) bool {
	if outcome.NotObserved || outcome.OperationID != op.OperationID || op.Work == nil {
		return false
	}
	if op.InstallationID != "" && op.InstallationID != outcome.InstallationID {
		return false
	}
	tombstone := outcome.Kind == "" && outcome.Effect == factory.OpEffectNotCommitted && outcome.Cancellation == factory.OpCancelCancelled
	if !tombstone && (outcome.Kind != op.Kind || outcome.ActorID != op.Work.ActorID || outcome.RepositoryID != op.Work.Repository || outcome.InstallationID == "") {
		return false
	}
	if (op.Effect == factory.OpEffectCommitted || op.Effect == factory.OpEffectNotCommitted) && op.Effect != outcome.Effect {
		return false
	}
	if op.Receipt != "" && op.Receipt != string(outcome.Receipt) {
		return false
	}
	op.Effect, op.Cancellation, op.Completion, op.Reason = outcome.Effect, outcome.Cancellation, outcome.Completion, outcome.Reason
	op.Receipt, op.UpdatedUnix = string(outcome.Receipt), now.Unix()
	op.InstallationID, op.ActorID, op.RepositoryID = outcome.InstallationID, outcome.ActorID, outcome.RepositoryID
	return true
}

func mergeOperationOutcomeOf(op factory.MergeOperation) factory.OperationOutcome {
	return factory.OperationOutcome{
		Receipt: []byte(op.Receipt), Effect: op.Effect, Cancellation: op.Cancellation, Completion: op.Completion, Reason: op.Reason,
		OperationID: op.OperationID, InstallationID: op.InstallationID, Kind: op.Kind, ActorID: op.ActorID, RepositoryID: op.RepositoryID,
	}
}
