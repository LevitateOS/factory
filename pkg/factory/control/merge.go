package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// MergeExecutor is the coordinator's narrow merge surface: evidence
// observation with fresh native reads, the single conditional merge
// with lost-reply reconciliation, receipt adoption, and completion
// confirmation. Implementations own all native effects; the
// coordinator owns stages, receipts and withdrawal. Every method
// reports its verdict as either an adopted outcome, a terminal
// PublicationRefusal, a retryable PublicationWait, or a transport
// error reconciled by a later pass.
// ObserveMerge and SubmitMerge must honor current provider branch rules. A
// shared-account COMMENT conveys agent inspection, never a required approval;
// observation must explicitly report complete, eligible provider evidence.
type MergeExecutor interface {
	ObserveMerge(ctx context.Context, work factory.MergeWork) (factory.MergeObservation, error)
	SubmitMerge(ctx context.Context, work factory.MergeWork) (factory.OperationOutcome, error)
	LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	AdoptMerge(ctx context.Context, work factory.MergeWork, outcome factory.OperationOutcome) (factory.MergeOutcome, error)
	ObserveCompletion(ctx context.Context, work factory.MergeWork) (factory.MergeConfirmation, error)
}

// mergePassLimit bounds one merge pass: recovered merges plus newly
// mergeable publications.
const mergePassLimit = 256

// MergeLink is one exact merged PR with its dispatch receipts.
type MergeLink struct {
	AssignmentID  string `json:"assignment_id"`
	PublicationID string `json:"publication_id"`
	MergeID       string `json:"merge_id"`
	MergedCommit  string `json:"merged_commit"`
	Repository    int64  `json:"repository,string"`
	Issue         int64  `json:"issue,string"`
	PRNumber      int64  `json:"pr_number,string"`
	PRID          int64  `json:"pr_id,string"`
}

// MergeWait is one merge that did not advance this pass and why.
type MergeWait struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// MergeError is one unexpected merge infrastructure failure. The
// affected merge keeps its recorded state; a later pass retries.
type MergeError struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// MergeReport is the durable outcome of one merge pass: the PRs it
// merged, the merges it fenced or withdrew, and every merge that
// waited or errored.
type MergeReport struct {
	Merged      []MergeLink  `json:"merged"`
	Fenced      []string     `json:"fenced,omitempty"`
	Withdrawn   []string     `json:"withdrawn,omitempty"`
	Waits       []MergeWait  `json:"waits,omitempty"`
	Errors      []MergeError `json:"errors,omitempty"`
	Unavailable bool         `json:"unavailable,omitempty"`
}

// MergePass reads recorded operations before considering any further
// write. The single merge phase has one immutable native authorization;
// a terminal refusal never changes identity or erases its receipt.
func (c *Coordinator) MergePass(ctx context.Context) MergeReport {
	ctx, unlock, err := c.progressPass(ctx)
	if err != nil {
		report := MergeReport{Merged: []MergeLink{}}
		mergeError(&report, "", "progress_pass_cancelled")
		return report
	}
	defer unlock()
	ctx, ownsPass, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	report := MergeReport{Merged: []MergeLink{}}
	if c.Merges == nil {
		report.Unavailable = true
		if ownsPass {
			_, _ = c.drainReadinessWork(ctx, "")
		}
		return report
	}
	pending, err := c.Store.OpenMerges(ctx, mergePassLimit)
	if err != nil {
		mergeError(&report, "", "store_unavailable")
		if ownsPass {
			_, _ = c.drainReadinessWork(ctx, "")
		}
		return report
	}
	for _, m := range pending {
		c.reconcileMerge(ctx, m, &report)
	}
	mergeable, err := c.Store.MergeablePublications(ctx, mergePassLimit)
	if err != nil {
		mergeError(&report, "", "store_unavailable")
		if ownsPass {
			_, _ = c.drainReadinessWork(ctx, "")
		}
		return report
	}
	for _, p := range mergeable {
		if updated, _, err := c.recordCleanDeliveryAcceptance(ctx, p); err == nil {
			p = updated
		}
		// Merge rows open only behind a passing verdict on the exact
		// current head and base (the review-cycle rule): opening one
		// early would strand the publication, since assessed checks
		// never revisit a publication that already holds a merge row
		// and a failed verdict on the row's head fails it terminally.
		if !c.checkCurrent(ctx, p) {
			continue
		}
		c.mergeOne(ctx, p, &report)
	}
	if ownsPass {
		_, _ = c.drainReadinessWork(ctx, "")
	}
	// Optional lifecycle recovery: bounded drain (see PublishPass).
	return report
}

func mergeError(report *MergeReport, id, reason string) {
	report.Errors = append(report.Errors, MergeError{ID: id, Reason: reason})
}

func mergeWait(report *MergeReport, id, reason string) {
	report.Waits = append(report.Waits, MergeWait{ID: id, Reason: reason})
}

func (c *Coordinator) mergeOne(ctx context.Context, p factory.Publication, report *MergeReport) {
	if p.Stage != factory.PublicationPublished || p.PRNumber <= 0 || p.PRID <= 0 || p.PRCreate.Work == nil {
		return
	}
	if _, err := c.Store.MergeByPublication(ctx, p.ID); err == nil {
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		mergeError(report, p.ID, "store_unavailable")
		return
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		mergeError(report, p.ID, "store_unavailable")
		return
	}
	m := factory.Merge{
		Operation: factory.MergeOperation{Kind: factory.OpMerge},
		Authority: p.Authority, ID: factory.NewID(), PublicationID: p.ID,
		AssignmentID: p.AssignmentID, ProjectID: p.ProjectID, Role: p.Role,
		Acceptance: p.Acceptance, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef,
		// The merge follows the latest published head: the creation tip
		// before any correction, the newest correction after one.
		HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID,
		Stage: factory.MergeOpen, Repository: p.Repository, Issue: p.Issue,
		PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID,
		PRAuthorID: p.PRCreate.Work.ActorID, ReviewerID: policy.Review.ActorID,
		CreatedUnix: time.Now().Unix(),
	}
	if err := c.Store.RecordMerge(ctx, m); err != nil {
		mergeError(report, p.ID, "store_unavailable")
		return
	}
	c.reconcileMerge(ctx, m, report)
}

func (c *Coordinator) mergeCallError(report *MergeReport, id string, err error) {
	var wait *factory.PublicationWait
	var refused *factory.PublicationRefusal
	switch {
	case errors.As(err, &wait):
		mergeWait(report, id, wait.Reason)
	case errors.As(err, &refused):
		mergeWait(report, id, refused.Reason)
	default:
		mergeError(report, id, "native_unavailable")
	}
}

func (c *Coordinator) storeMerge(ctx context.Context, m *factory.Merge, report *MergeReport) bool {
	m.Revision++
	if err := c.Store.UpdateMerge(ctx, *m); err != nil {
		mergeError(report, m.ID, "store_conflict")
		return false
	}
	return true
}

func (c *Coordinator) finishMerge(ctx context.Context, m factory.Merge, stage string, outcome factory.Outcome, reason string, report *MergeReport) bool {
	m.Stage, m.Outcome, m.Reason, m.FinishedUnix = stage, outcome, reason, time.Now().Unix()
	if !c.storeMerge(ctx, &m, report) {
		return false
	}
	switch stage {
	case factory.MergeMerged:
		report.Merged = append(report.Merged, MergeLink{AssignmentID: m.AssignmentID, PublicationID: m.PublicationID, MergeID: m.ID, MergedCommit: m.MergedCommit, Repository: m.Repository, Issue: m.Issue, PRNumber: m.PRNumber, PRID: m.PRID})
	case factory.MergeWithdrawn:
		report.Withdrawn = append(report.Withdrawn, m.ID)
	case factory.MergeFenced:
		report.Fenced = append(report.Fenced, m.ID)
	}
	return true
}
