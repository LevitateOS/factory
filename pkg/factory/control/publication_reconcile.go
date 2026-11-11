package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// Reconciliation never depends on an export, current actor credential, or open
// dispatch. Those are prerequisites for new writes, not for learning old effects.
func (c *Coordinator) reconcilePublication(ctx context.Context, p factory.Publication, report *PublishReport) {
	if p.External != nil {
		if p.Stage == factory.PublicationPublished || p.Stage == factory.PublicationFenced {
			c.reconcilePublishedPublication(ctx, p, report)
		}
		return
	}
	if p.Stage == factory.PublicationPublished {
		c.reconcilePublishedPublication(ctx, p, report)
		return
	}
	if p.Stage != factory.PublicationOpen && p.Stage != factory.PublicationFenced {
		return
	}
	a, run, open, ok := c.loadPublicationRunState(ctx, p, report)
	if !ok {
		return
	}
	if p.WithdrawRequested || !open {
		c.withdrawPublication(ctx, a, &p, run, report)
		return
	}
	if !c.adoptObservedOperations(ctx, &p, report) {
		return
	}
	if !c.adoptPublicationReceipts(ctx, a, &p, run, report) {
		return
	}
	if !c.recheckPublicationGate(ctx, a, &p, run, report) {
		return
	}
	c.advancePublicationOperation(ctx, a, p, run, report)
}

// loadPublicationRunState loads the assignment, run and dispatch gate
// behind one publication reconciliation.
func (c *Coordinator) loadPublicationRunState(ctx context.Context, p factory.Publication, report *PublishReport) (factory.Assignment, factory.Run, bool, bool) {
	a, err := c.assignmentForPublication(ctx, p)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return factory.Assignment{}, factory.Run{}, false, false
	}
	var run factory.Run
	if p.External == nil {
		run, err = c.Store.FactoryRun(ctx, p.Run)
		if err != nil {
			publicationError(report, p.ID, "store_unavailable")
			return factory.Assignment{}, factory.Run{}, false, false
		}
	}
	open, _, _, err := c.Store.DispatchState(ctx, p.Repository)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return factory.Assignment{}, factory.Run{}, false, false
	}
	return a, run, open, true
}

// reconcilePublishedPublication reconciles reviews and corrections
// behind one already-published publication.
func (c *Coordinator) reconcilePublishedPublication(ctx context.Context, p factory.Publication, report *PublishReport) {
	a, run, open, ok := c.loadPublicationRunState(ctx, p, report)
	if !ok {
		return
	}
	if p.WithdrawRequested || !open {
		c.withdrawPublication(ctx, a, &p, run, report)
		return
	}
	c.reconcileRecordedReviews(ctx, &p, report)
	for index := range p.ResolutionOperations {
		if err := c.reconcileDeliveryResolution(ctx, &p, index, false); err != nil {
			publicationWait(report, p.ID, "resolution_pending")
		}
	}
	corrections := CorrectionReport{}
	c.reconcileRecordedCorrection(ctx, &p, a, &corrections)
	reportCorrectionOutcomes(report, &corrections)
	if len(p.Corrections) > 0 {
		last := p.Corrections[len(p.Corrections)-1]
		if last.Effect == factory.OpEffectCommitted && last.Completion != factory.OpCompletionComplete &&
			len(corrections.Waits) == 0 && len(corrections.Errors) == 0 {
			publicationWait(report, p.ID, "branch_completion_pending")
		}
	}
}

// reportCorrectionOutcomes fans one correction report into waits,
// errors and fenced identities.
func reportCorrectionOutcomes(report *PublishReport, corrections *CorrectionReport) {
	for _, wait := range corrections.Waits {
		publicationWait(report, wait.ID, wait.Reason)
	}
	for _, failure := range corrections.Errors {
		publicationError(report, failure.ID, failure.Reason)
	}
	for _, id := range corrections.Fenced {
		report.Fenced = append(report.Fenced, id)
	}
}

// adoptObservedOperations adopts native outcomes observed behind
// recorded operation attempts.
func (c *Coordinator) adoptObservedOperations(ctx context.Context, p *factory.Publication, report *PublishReport) bool {
	for _, op := range []*factory.PublicationOperation{&p.Publish, &p.PRCreate} {
		if op.Attempts == 0 {
			continue
		}
		outcome, err := c.Publication.LookupOp(ctx, op.OperationID)
		if err != nil {
			c.publicationCallError(report, p.ID, err)
			return false
		}
		if !outcome.NotObserved {
			if op.SameOutcome(outcome) {
				continue
			}
			if !op.Adopt(outcome, time.Now().Unix()) {
				c.finishPublication(ctx, *p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonUnattributed, report)
				return false
			}
			if !c.storePublication(ctx, p, report) {
				return false
			}
		}
	}
	return true
}

// recheckPublicationGate rechecks the dispatch gate after recording,
// withdrawing on a concurrent withdrawal or closed gate.
func (c *Coordinator) recheckPublicationGate(ctx context.Context, a factory.Assignment, p *factory.Publication, run factory.Run, report *PublishReport) bool {
	open, _, _, err := c.Store.DispatchState(ctx, p.Repository)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return false
	}
	if p.WithdrawRequested || !open {
		c.withdrawPublication(ctx, a, p, run, report)
		return false
	}
	return true
}

// advancePublicationOperation routes one open publication's effect,
// then drives its active operation toward submit, push and settle.
func (c *Coordinator) advancePublicationOperation(ctx context.Context, a factory.Assignment, p factory.Publication, run factory.Run, report *PublishReport) {
	if c.routePublicationEffect(ctx, &p, report) {
		return
	}
	op, policy, revision, ok := c.selectPublicationOp(ctx, &p, report)
	if !ok {
		return
	}
	bundle, handled := c.resolvePublicationExport(ctx, p, run, op, report)
	if handled {
		return
	}
	work := c.publicationWork(a, p, run, bundle, policy, op.Kind)
	var done bool
	if work, done = c.observePublicationWork(ctx, a, &p, run, bundle, policy, op, revision, work, report); done {
		return
	}
	work = op.Work.Apply(work)
	// Recheck the local gate after recording. A concurrent withdrawal cancels this
	// same recorded identity, including a submit delayed until after cancellation.
	if !c.recheckSubmitGate(ctx, a, &p, run, report) {
		return
	}
	if !c.submitPublicationOp(ctx, &p, op, work, report) {
		return
	}
	if !c.pushPublicationBranch(ctx, &p, op, work, report) {
		return
	}
	if !c.adoptPublicationReceipts(ctx, a, &p, run, report) {
		return
	}
	c.settlePublicationOp(ctx, p, op, report)
}

// routePublicationEffect completes, fails or fences one publication
// whose effect already settled, reopening a fenced stage for retry.
func (c *Coordinator) routePublicationEffect(ctx context.Context, p *factory.Publication, report *PublishReport) bool {
	// A committed PR is retained even if native completion remains unfinished.
	if p.PRCreate.Effect == factory.OpEffectCommitted {
		c.completePublication(ctx, p, report)
		return true
	}
	if terminalPublicationEffect(p.Publish) || terminalPublicationEffect(p.PRCreate) {
		c.finishPublication(ctx, *p, factory.PublicationFailed, factory.Failed, factory.PublishReasonRefused, report)
		return true
	}
	if p.Publish.Effect == factory.OpEffectIndeterminate || p.PRCreate.Effect == factory.OpEffectIndeterminate {
		if p.Stage == factory.PublicationFenced && p.Outcome == factory.NeedsHuman && p.Reason == factory.PublishReasonFenced {
			publicationWait(report, p.ID, factory.PublishReasonFenced)
			return true
		}
		c.finishPublication(ctx, *p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, report)
		return true
	}
	if p.Stage == factory.PublicationFenced {
		p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationOpen, "", "", 0
		if !c.storePublication(ctx, p, report) {
			return true
		}
	}
	return false
}

// selectPublicationOp resolves the authority bracket and selects the
// active operation behind one open publication.
func (c *Coordinator) selectPublicationOp(ctx context.Context, p *factory.Publication, report *PublishReport) (*factory.PublicationOperation, factory.RepositoryPolicy, int64, bool) {
	policy, revision, allowed := c.publicationAuthority(ctx, *p, report)
	if !allowed {
		return nil, policy, revision, false
	}
	op := &p.Publish
	if op.Effect == factory.OpEffectCommitted {
		if op.Completion != factory.OpCompletionComplete {
			publicationWait(report, p.ID, "branch_completion_pending")
			return nil, policy, revision, false
		}
		op = &p.PRCreate
	}
	if op.Cancellation != "" && op.Cancellation != factory.OpCancelNone {
		publicationWait(report, p.ID, "cancellation_pending")
		return nil, policy, revision, false
	}
	if op.Kind == factory.OpPRCreate && op.Effect == factory.OpEffectPending {
		publicationWait(report, p.ID, "creation_pending")
		return nil, policy, revision, false
	}
	return op, policy, revision, true
}

// resolvePublicationExport resolves the export behind one active
// operation, waiting or failing on terminal export states.
func (c *Coordinator) resolvePublicationExport(ctx context.Context, p factory.Publication, run factory.Run, op *factory.PublicationOperation, report *PublishReport) ([]byte, bool) {
	bundle, terminal, failed := c.exportCandidate(ctx, p, run)
	if failed != nil {
		publicationWait(report, p.ID, failed.Reason)
		return nil, true
	}
	if terminal != nil {
		// A lost reply must be reconciled/cancelled before declaring failure.
		if op.Attempts > 0 {
			publicationWait(report, p.ID, terminal.Reason)
			return nil, true
		}
		c.finishPublication(ctx, p, factory.PublicationFailed, factory.Failed, terminal.Reason, report)
		return nil, true
	}
	return bundle, false
}

// observePublicationWork observes the native target behind one
// unobserved operation and records its first attempt.
func (c *Coordinator) observePublicationWork(ctx context.Context, a factory.Assignment, p *factory.Publication, run factory.Run, bundle []byte, policy factory.RepositoryPolicy, op *factory.PublicationOperation, revision int64, work factory.PublicationWork, report *PublishReport) (factory.PublicationWork, bool) {
	if op.Work != nil {
		return work, false
	}
	observation, err := c.Publication.ObservePublication(ctx, work)
	if err != nil {
		var refused *factory.PublicationRefusal
		if errors.As(err, &refused) {
			c.finishPublication(ctx, *p, factory.PublicationFailed, factory.Failed, factory.PublishReasonInvalid, report)
		} else {
			c.publicationCallError(report, p.ID, err)
		}
		return work, true
	}
	if !c.checkPublicationObservation(ctx, a, *p, op, observation, revision, report) {
		return work, true
	}
	now := time.Now()
	var notAfter int64
	if a.RetainedPublication != nil {
		notAfter = a.RetainedPublication.NotAfter
		if notAfter <= now.Unix() {
			err = factory.ErrAttemptTimeExhausted
		}
	} else {
		notAfter, err = c.Store.CapAttemptDeadline(ctx, p.AssignmentID, now.Add(5*time.Minute).Unix())
	}
	if err != nil {
		publicationWait(report, p.ID, "attempt_deadline_unavailable")
		return work, true
	}
	work.NativeRev, work.ComparisonOID, work.NotAfter = observation.NativeRev, observation.Comparison, notAfter
	intent := work.Intent()
	op.OperationID, op.Attempts, op.UpdatedUnix, op.Work = work.OperationID, 1, now.Unix(), &intent
	p.NativeRev, p.Comparison, p.TargetTip, p.ObservedUnix = observation.NativeRev, observation.Comparison, observation.TargetTip, now.Unix()
	if !c.storePublication(ctx, p, report) {
		return work, true
	}
	return work, false
}

// checkPublicationObservation matches one native observation against
// the accepted revision, branch and target tip.
func (c *Coordinator) checkPublicationObservation(ctx context.Context, a factory.Assignment, p factory.Publication, op *factory.PublicationOperation, observation factory.PublicationObservation, revision int64, report *PublishReport) bool {
	if observation.NativeRev != revision {
		publicationWait(report, p.ID, "native_inputs_changed")
		return false
	}
	if observation.TargetRef != factory.PublishBranchName(a.ID) || !factory.ValidCommit(observation.Comparison) {
		publicationWait(report, p.ID, "observation_invalid")
		return false
	}
	expected := ""
	if op.Kind == factory.OpPRCreate {
		expected = p.Candidate
	}
	if observation.TargetTip != expected {
		c.finishPublication(ctx, p, factory.PublicationFailed, factory.Failed, factory.PublishReasonTargetOccupied, report)
		return false
	}
	return true
}

// recheckSubmitGate rechecks the local gate after recording the
// attempt, withdrawing on a concurrently closed gate.
func (c *Coordinator) recheckSubmitGate(ctx context.Context, a factory.Assignment, p *factory.Publication, run factory.Run, report *PublishReport) bool {
	open, _, _, err := c.Store.DispatchState(ctx, p.Repository)
	if err != nil {
		publicationError(report, p.ID, "store_unavailable")
		return false
	}
	if !open {
		c.withdrawPublication(ctx, a, p, run, report)
		return false
	}
	return true
}

// submitPublicationOp submits one recorded operation whose native
// effect is still empty.
func (c *Coordinator) submitPublicationOp(ctx context.Context, p *factory.Publication, op *factory.PublicationOperation, work factory.PublicationWork, report *PublishReport) bool {
	if op.Effect != "" {
		return true
	}
	var outcome factory.OperationOutcome
	var err error
	if op.Kind == factory.OpRefPublish {
		outcome, err = c.Publication.SubmitPublish(ctx, work)
	} else {
		outcome, err = c.Publication.SubmitPRCreate(ctx, work)
	}
	if err != nil {
		c.publicationCallError(report, p.ID, err)
		return false
	}
	if outcome.NotObserved {
		publicationWait(report, p.ID, "submit_unconfirmed")
		return false
	}
	if !op.Adopt(outcome, time.Now().Unix()) {
		c.finishPublication(ctx, *p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonUnattributed, report)
		return false
	}
	return c.storePublication(ctx, p, report)
}

// pushPublicationBranch pushes one pending publish branch behind a
// recorded publish operation.
func (c *Coordinator) pushPublicationBranch(ctx context.Context, p *factory.Publication, op *factory.PublicationOperation, work factory.PublicationWork, report *PublishReport) bool {
	if op.Kind != factory.OpRefPublish || op.Effect != factory.OpEffectPending || (op.Cancellation != "" && op.Cancellation != factory.OpCancelNone) {
		return true
	}
	outcome, err := c.Publication.PushBranch(ctx, work)
	if err != nil {
		c.publicationCallError(report, p.ID, err)
		return false
	}
	if outcome.NotObserved {
		publicationWait(report, p.ID, "push_unconfirmed")
		return false
	}
	if !op.Adopt(outcome, time.Now().Unix()) {
		c.finishPublication(ctx, *p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonUnattributed, report)
		return false
	}
	return c.storePublication(ctx, p, report)
}

// settlePublicationOp settles one submitted operation by its effect,
// recursing into the distinct PR phase after a completed publish.
func (c *Coordinator) settlePublicationOp(ctx context.Context, p factory.Publication, op *factory.PublicationOperation, report *PublishReport) {
	switch op.Effect {
	case factory.OpEffectCommitted:
		if op.Kind == factory.OpPRCreate {
			c.completePublication(ctx, &p, report)
		} else if op.Completion == factory.OpCompletionComplete {
			// The distinct PR phase gets its own fresh acceptance/native bracket.
			c.reconcilePublication(ctx, p, report)
		} else {
			publicationWait(report, p.ID, "branch_completion_pending")
		}
	case factory.OpEffectNotCommitted:
		c.finishPublication(ctx, p, factory.PublicationFailed, factory.Failed, factory.PublishReasonRefused, report)
	case factory.OpEffectIndeterminate:
		c.finishPublication(ctx, p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, report)
	default:
		publicationWait(report, p.ID, "operation_pending")
	}
}

func terminalPublicationEffect(op factory.PublicationOperation) bool {
	return op.Effect == factory.OpEffectNotCommitted
}

func (c *Coordinator) adoptPublicationReceipts(ctx context.Context, a factory.Assignment, p *factory.Publication, run factory.Run, report *PublishReport) bool {
	changed := false
	for _, op := range []*factory.PublicationOperation{&p.Publish, &p.PRCreate} {
		if op.Effect != factory.OpEffectCommitted {
			continue
		}
		if op.Work == nil {
			c.finishPublication(ctx, *p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonUnattributed, report)
			return false
		}
		work := op.Work.Apply(c.publicationWork(a, *p, run, nil, factory.RepositoryPolicy{}, op.Kind))
		var err error
		if op.Kind == factory.OpRefPublish {
			_, err = c.Publication.AdoptBranch(ctx, work, op.Outcome())
		} else {
			var result factory.PRCreationOutcome
			result, err = c.Publication.AdoptPRCreation(ctx, work, op.Outcome())
			if err == nil {
				changed = changed || op.PRNumber != result.PRNumber || op.PRID != result.PRID || op.IssueID != result.IssueID ||
					op.HeadRef != result.HeadRef || op.BaseRef != result.BaseRef || op.HeadOID != result.HeadOID || op.BaseOID != result.BaseOID ||
					p.PRNumber != result.PRNumber || p.PRID != result.PRID
				op.PRNumber, op.PRID, op.IssueID = result.PRNumber, result.PRID, result.IssueID
				op.HeadRef, op.BaseRef, op.HeadOID, op.BaseOID = result.HeadRef, result.BaseRef, result.HeadOID, result.BaseOID
				p.PRNumber, p.PRID = result.PRNumber, result.PRID
			}
		}
		if err != nil {
			c.finishPublication(ctx, *p, factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonUnattributed, report)
			return false
		}
	}
	return !changed || c.storePublication(ctx, p, report)
}
