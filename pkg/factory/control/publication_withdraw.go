package control

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func collectWithdrawalOperations(p *factory.Publication, result *factory.PublicationWithdrawal) {
	for _, op := range []factory.PublicationOperation{p.Publish, p.PRCreate} {
		if op.OperationID != "" {
			result.Operations = append(result.Operations, op.OperationID)
		}
	}
	for _, op := range p.Corrections {
		if op.OperationID != "" {
			result.Operations = append(result.Operations, op.OperationID)
		}
	}
	for _, op := range p.ReviewOperations {
		if op.Work.OperationID != "" {
			result.Operations = append(result.Operations, op.Work.OperationID)
		}
	}
	for _, op := range p.ResolutionOperations {
		result.Operations = append(result.Operations, op.Work.OperationID)
	}
}

func (c *Coordinator) withdrawOnePublication(ctx context.Context, p *factory.Publication, result *factory.PublicationWithdrawal) {
	a, err := c.assignmentForPublication(ctx, *p)
	if err != nil {
		result.Pending = true
		return
	}
	var run factory.Run
	if p.External == nil {
		run, err = c.Store.FactoryRun(ctx, p.Run)
		if err != nil {
			result.Pending = true
			return
		}
	}
	report := PublishReport{Published: []PublishLink{}}
	if c.Publication == nil {
		result.Pending = true
		return
	}
	c.withdrawPublication(ctx, a, p, run, &report)
	current, err := c.Store.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil || current.Stage == factory.PublicationOpen || current.Stage == factory.PublicationFenced || len(report.Errors) > 0 || len(report.Waits) > 0 {
		result.Pending = true
	}
}

func (c *Coordinator) cancelPublications(ctx context.Context, repository, issue int64, acceptance string) factory.PublicationWithdrawal {
	result := factory.PublicationWithdrawal{Publications: []string{}, Operations: []string{}}
	pending, err := c.Store.OutstandingPublications(ctx, repository, publishPassLimit+1)
	if err != nil {
		result.Pending = true
		return result
	}
	if len(pending) > publishPassLimit {
		result.Pending = true
		pending = pending[:publishPassLimit]
	}
	for i := range pending {
		p := pending[i]
		if (issue != 0 && p.Issue != issue) || (acceptance != "" && p.Acceptance != acceptance) {
			continue
		}
		result.Publications = append(result.Publications, p.ID)
		collectWithdrawalOperations(&p, &result)
		c.withdrawOnePublication(ctx, &p, &result)
	}
	return result
}

// A successful host launch returns after the supervised CLI has retired. Only
// terminal replies enter settlement; a running duplicate remains supervised.
func (c *Coordinator) publishAfterDispatch(ctx context.Context, report DispatchReport) {
	if c.Publication == nil && c.Checks == nil && c.Merges == nil {
		return
	}
	bounded, stop := context.WithTimeout(ctx, 10*time.Minute)
	defer stop()
	for _, launched := range report.Launched {
		if launched.Phase != project.FactoryCompleted && launched.Phase != project.FactoryFailed && launched.Phase != project.FactoryStopped {
			continue
		}
		run, err := c.Store.FactoryRun(bounded, launched.RunID)
		if err == nil && !run.Reconciled {
			c.settleRun(bounded, run)
		}
	}
	publication := c.PublishPass(bounded)
	// Keep publication holds in the same bounded, scope-filtered diagnostic as
	// dispatch. The projector whitelists reason codes and never exposes Detail.
	// This local copy never changes the dispatch report used by scheduling.
	report.Waits, report.Errors = slices.Clone(report.Waits), slices.Clone(report.Errors)
	// Resolve only immutable scope during the existing cleanup reserve, so a
	// timed-out publication pass can still report its hold without new effects.
	scopeContext := bounded
	if budget := readinessBudgetFrom(bounded); budget != nil {
		deadline := time.Now().Add(readinessCleanupReserve)
		if budget.outerDeadline.Before(deadline) {
			deadline = budget.outerDeadline
		}
		var cancel context.CancelFunc
		scopeContext, cancel = context.WithDeadline(context.WithoutCancel(bounded), deadline)
		defer cancel()
	}
	for _, wait := range publication.Waits {
		p, err := c.Store.PublicationByID(scopeContext, wait.ID)
		if err == nil {
			report.Waits = append(report.Waits, DispatchWait{Repository: p.Repository, Issue: p.Issue, Reason: wait.Reason})
		}
	}
	for _, failure := range publication.Errors {
		p, err := c.Store.PublicationByID(scopeContext, failure.ID)
		if err == nil {
			report.Errors = append(report.Errors, DispatchError{Repository: p.Repository, Issue: p.Issue, Reason: failure.Reason})
		}
	}
	c.observeDispatch(ctx, report)
	c.CheckPass(bounded)
	c.MergePass(bounded)
}

// Withdrawal stays open while any cancellation or effect is unknown. Native
// committed receipts are adopted even when cancellation arrived too late.
func (c *Coordinator) cancelPrimaryWithdrawOps(ctx context.Context, p *factory.Publication) bool {
	confirmed := true
	for _, op := range []*factory.PublicationOperation{&p.Publish, &p.PRCreate} {
		if op.Attempts == 0 {
			continue
		}
		outcome, err := c.Publication.CancelOp(ctx, op.OperationID)
		if err != nil || outcome.NotObserved {
			confirmed = false
			continue
		}
		if !op.Adopt(outcome, time.Now().Unix()) {
			confirmed = false
			continue
		}
		if outcome.Effect != factory.OpEffectCommitted && outcome.Effect != factory.OpEffectNotCommitted {
			confirmed = false
		}
	}
	return confirmed
}

func (c *Coordinator) cancelCorrectionWithdrawOps(ctx context.Context, p *factory.Publication) bool {
	confirmed := true
	for i := range p.Corrections {
		op := &p.Corrections[i]
		if op.Attempts == 0 {
			continue
		}
		outcome, err := c.Publication.CancelOp(ctx, op.OperationID)
		if err != nil || outcome.NotObserved {
			confirmed = false
			continue
		}
		if !op.Adopt(outcome, time.Now().Unix()) {
			confirmed = false
			continue
		}
		if outcome.Effect != factory.OpEffectCommitted && outcome.Effect != factory.OpEffectNotCommitted {
			confirmed = false
		}
		if outcome.Effect == factory.OpEffectCommitted {
			if op.Work == nil {
				confirmed = false
				continue
			}
			p.Candidate = op.Work.Candidate
			p.Run = op.RunID
			work := op.Work.Apply(factory.PublicationWork{AssignmentID: p.AssignmentID, Publication: p.ID})
			if _, err := c.Publication.AdoptBranch(ctx, work, op.Outcome()); err != nil {
				confirmed = false
			}
		}
	}
	return confirmed
}

func (c *Coordinator) cancelReviewWithdrawOps(ctx context.Context, p *factory.Publication) bool {
	confirmed := true
	for i := range p.ReviewOperations {
		op := &p.ReviewOperations[i]
		if c.Reviews == nil {
			confirmed = false
			continue
		}
		outcome, err := c.Reviews.CancelOp(ctx, op.Work.OperationID)
		if err != nil || outcome.NotObserved {
			confirmed = false
			continue
		}
		updated, err := op.WithOutcome(outcome)
		if err != nil {
			confirmed = false
			continue
		}
		*op = updated
		if outcome.Effect != factory.OpEffectCommitted && outcome.Effect != factory.OpEffectNotCommitted {
			confirmed = false
		}
		if outcome.Effect == factory.OpEffectCommitted {
			adopted, err := c.Reviews.AdoptReview(ctx, op.Work, outcome)
			if err != nil || adopted.ReviewerID != op.Work.ActorID || adopted.Event != op.Work.Event || adopted.HeadOID != op.Work.HeadOID {
				confirmed = false
			}
		}
	}
	return confirmed
}

func (c *Coordinator) withdrawPublication(ctx context.Context, a factory.Assignment, p *factory.Publication, run factory.Run, report *PublishReport) {
	if !p.WithdrawRequested {
		p.WithdrawRequested = true
		if !c.storePublication(ctx, p, report) {
			return
		}
	}
	before, err := json.Marshal(p)
	if err != nil {
		publicationError(report, p.ID, "store_conflict")
		return
	}
	primary := c.cancelPrimaryWithdrawOps(ctx, p)
	corrections := c.cancelCorrectionWithdrawOps(ctx, p)
	reviews := c.cancelReviewWithdrawOps(ctx, p)
	resolutions := true
	for index := range p.ResolutionOperations {
		if err := c.reconcileDeliveryResolution(ctx, p, index, true); err != nil {
			resolutions = false
		}
		outcome := p.ResolutionOperations[index].Outcome
		if outcome.Effect != factory.OpEffectCommitted && outcome.Effect != factory.OpEffectNotCommitted {
			resolutions = false
		}
	}
	confirmed := primary && corrections && reviews && resolutions
	after, err := json.Marshal(p)
	if err != nil {
		publicationError(report, p.ID, "store_conflict")
		return
	}
	if !bytes.Equal(before, after) && !c.storePublication(ctx, p, report) {
		return
	}
	if !c.adoptPublicationReceipts(ctx, a, p, run, report) {
		return
	}
	for _, op := range p.Corrections {
		if op.Effect == factory.OpEffectCommitted && op.Completion != factory.OpCompletionComplete {
			publicationWait(report, p.ID, "branch_completion_pending")
			return
		}
	}
	for _, op := range p.ReviewOperations {
		if op.Outcome.Effect == factory.OpEffectCommitted && op.Outcome.Completion != factory.OpCompletionComplete {
			publicationWait(report, op.Work.OperationID, "review_completion_pending")
			return
		}
	}
	if !confirmed {
		publicationWait(report, p.ID, "cancellation_pending")
		return
	}
	if p.External != nil {
		// These operations own only Factory's continuation. The external PR
		// and its human text are retained; no creation receipt/run is needed.
		c.finishPublication(ctx, *p, factory.PublicationWithdrawn, factory.PublishReasonWithdrawn, report)
		return
	}
	if p.PRCreate.Effect == factory.OpEffectCommitted {
		c.completePublication(ctx, p, report)
		return
	}
	if p.Publish.Effect == factory.OpEffectCommitted && p.Publish.Completion != factory.OpCompletionComplete {
		publicationWait(report, p.ID, "branch_completion_pending")
		return
	}
	c.finishPublication(ctx, *p, factory.PublicationWithdrawn, factory.PublishReasonWithdrawn, report)
}

func (c *Coordinator) storePublication(ctx context.Context, p *factory.Publication, report *PublishReport) bool {
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, *p); err != nil {
		publicationError(report, p.ID, "store_conflict")
		return false
	}
	return true
}

func (c *Coordinator) finishPublication(ctx context.Context, p factory.Publication, stage, reason string, report *PublishReport) {
	// Every finish writes, even when stage and reason are unchanged.
	p.Settle(stage, reason, time.Now().Unix())
	if !c.storePublication(ctx, &p, report) {
		return
	}
	switch stage {
	case factory.PublicationPublished:
		report.Published = append(report.Published, PublishLink{AssignmentID: p.AssignmentID, PublicationID: p.ID, Repository: p.Repository, Issue: p.Issue, PRNumber: p.PRNumber, PRID: p.PRID})
		// Optional lifecycle effect: PR reviewer request. Fires only
		// after the published stage persists; never gates default.
	case factory.PublicationWithdrawn:
		report.Withdrawn = append(report.Withdrawn, p.ID)
	case factory.PublicationFenced:
		report.Fenced = append(report.Fenced, p.ID)
	}
}

// fenceUnattributedPublication fences one publication whose native
// observation cannot be attributed to its recorded operation. A publication
// already fenced for that reason waits instead of rewriting its row on every
// pass.
func (c *Coordinator) fenceUnattributedPublication(ctx context.Context, p factory.Publication, report *PublishReport) {
	if p.Stage == factory.PublicationFenced && p.Reason == factory.PublishReasonUnattributed {
		publicationWait(report, p.ID, factory.PublishReasonUnattributed)
		return
	}
	c.finishPublication(ctx, p, factory.PublicationFenced, factory.PublishReasonUnattributed, report)
}
