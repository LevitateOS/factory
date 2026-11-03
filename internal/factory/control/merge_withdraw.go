package control

import (
	"context"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func (c *Coordinator) cancelRepositoryMerges(ctx context.Context, repository int64) factory.MergeWithdrawal {
	return c.cancelMerges(ctx, repository, 0, "")
}

func (c *Coordinator) cancelAcceptanceMerges(ctx context.Context, repository, issue int64, acceptance string) factory.MergeWithdrawal {
	return c.cancelMerges(ctx, repository, issue, acceptance)
}

func (c *Coordinator) cancelMerges(ctx context.Context, repository, issue int64, acceptance string) factory.MergeWithdrawal {
	result := factory.MergeWithdrawal{Merges: []string{}, Operations: []string{}}
	pending, err := c.Store.OutstandingMerges(ctx, repository, mergePassLimit+1)
	if err != nil {
		result.Pending = true
		return result
	}
	if len(pending) > mergePassLimit {
		result.Pending = true
		pending = pending[:mergePassLimit]
	}
	for _, m := range pending {
		if (issue != 0 && m.Issue != issue) || (acceptance != "" && m.Acceptance != acceptance) {
			continue
		}
		result.Merges = append(result.Merges, m.ID)
		if m.Operation.OperationID != "" {
			result.Operations = append(result.Operations, m.Operation.OperationID)
		}
		if c.Merges == nil {
			result.Pending = true
			continue
		}
		report := MergeReport{Merged: []MergeLink{}}
		c.withdrawMerge(ctx, &m, &report)
		current, err := c.Store.MergeByPublication(ctx, m.PublicationID)
		if err != nil || current.Stage == factory.MergeOpen || current.Stage == factory.MergeFenced || len(report.Errors) > 0 {
			result.Pending = true
		}
	}
	return result
}

// Withdrawal stays open while any cancellation or effect is unknown. A
// natively committed merge still confirms its completion: withdrawal
// cannot erase the effect, only finish its bookkeeping honestly.
func (c *Coordinator) withdrawMerge(ctx context.Context, m *factory.Merge, report *MergeReport) {
	m.WithdrawRequested = true
	if !c.storeMerge(ctx, m, report) {
		return
	}
	op := &m.Operation
	confirmed := true
	if op.Attempts != 0 {
		outcome, err := c.Merges.CancelOp(ctx, op.OperationID)
		if err != nil || outcome.NotObserved {
			confirmed = false
		} else {
			if !c.adoptMergeObserved(op, outcome, time.Now()) {
				confirmed = false
			}
			if outcome.Effect != factory.OpEffectCommitted && outcome.Effect != factory.OpEffectNotCommitted {
				confirmed = false
			}
		}
	}
	if !c.storeMerge(ctx, m, report) {
		return
	}
	if !c.adoptMergeReceipt(ctx, m, report) {
		return
	}
	if !confirmed {
		mergeWait(report, m.ID, "cancellation_pending")
		return
	}
	if op.Effect == factory.OpEffectCommitted {
		c.completeMerge(ctx, m, report)
		return
	}
	c.finishMerge(ctx, *m, factory.MergeWithdrawn, factory.Cancelled, factory.MergeReasonWithdrawn, report)
}
