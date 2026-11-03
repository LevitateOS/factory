package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// PublicationExecutor is the coordinator's narrow publication surface:
// candidate validation with fresh native observations, the two
// conditional operations with lost-reply reconciliation, and receipt
// adoption. Implementations own all native effects; the coordinator owns
// stages, receipts and withdrawal. Every method reports its verdict as
// either an adopted outcome, a terminal PublicationRefusal, a retryable
// PublicationWait, or a transport error reconciled by a later pass.
type PublicationExecutor interface {
	ObservePublication(ctx context.Context, work factory.PublicationWork) (factory.PublicationObservation, error)
	SubmitPublish(ctx context.Context, work factory.PublicationWork) (factory.OperationOutcome, error)
	PushBranch(ctx context.Context, work factory.PublicationWork) (factory.OperationOutcome, error)
	SubmitPRCreate(ctx context.Context, work factory.PublicationWork) (factory.OperationOutcome, error)
	LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error)
	AdoptBranch(ctx context.Context, work factory.PublicationWork, outcome factory.OperationOutcome) (factory.BranchOutcome, error)
	AdoptPRCreation(ctx context.Context, work factory.PublicationWork, outcome factory.OperationOutcome) (factory.PRCreationOutcome, error)
}

// publishPassLimit bounds one publication pass: recovered publications
// plus newly publishable assignments.
const publishPassLimit = 256

// PublishLink is one exact published PR with its dispatch receipts.
type PublishLink struct {
	AssignmentID  string `json:"assignment_id"`
	PublicationID string `json:"publication_id"`
	Repository    int64  `json:"repository,string"`
	Issue         int64  `json:"issue,string"`
	PRNumber      int64  `json:"pr_number,string"`
	PRID          int64  `json:"pr_id,string"`
}

// PublishWait is one publication that did not advance this pass and why.
type PublishWait struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// PublishError is one unexpected publication infrastructure failure. The
// affected publication keeps its recorded state; a later pass retries.
type PublishError struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// PublishReport is the durable outcome of one publication pass: the PRs
// it linked, the publications it fenced or withdrew, and every
// publication that waited or errored.
type PublishReport struct {
	Published   []PublishLink  `json:"published"`
	Fenced      []string       `json:"fenced,omitempty"`
	Withdrawn   []string       `json:"withdrawn,omitempty"`
	Waits       []PublishWait  `json:"waits,omitempty"`
	Errors      []PublishError `json:"errors,omitempty"`
	Unavailable bool           `json:"unavailable,omitempty"`
}

// PublishPass reads recorded operations before considering any further write.
// Each phase has one immutable native authorization; a terminal refusal never
// changes identity or erases its receipt.
func (c *Coordinator) PublishPass(ctx context.Context) PublishReport {
	ctx, unlock := c.progressPass(ctx)
	defer unlock()
	ctx, _, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	report := PublishReport{Published: []PublishLink{}}
	if c.Publication == nil {
		report.Unavailable = true
		return report
	}
	pending, err := c.Store.OpenPublications(ctx, publishPassLimit)
	if err != nil {
		publicationError(&report, "", "store_unavailable")
		return report
	}
	for _, p := range pending {
		c.reconcilePublication(ctx, p, &report)
	}
	assignments, err := c.Store.PublishableAssignments(ctx, publishPassLimit)
	if err != nil {
		publicationError(&report, "", "store_unavailable")
		return report
	}
	for _, a := range assignments {
		c.publishOne(ctx, a, &report)
	}
	c.producePublishedChildren(ctx, &report)
	c.consumePublicationChildren(ctx, &report)
	// Optional lifecycle recovery: bounded drain of journaled actions
	// left pending/uncertain by finished completions.
	return report
}

// consumePublicationChildren advances one bounded page of finished child
// results. The Store selector only returns children without a recorded native
// intent; once one is registered, the existing publication reconciliation
// path owns its lookup and replay.
func (c *Coordinator) consumePublicationChildren(ctx context.Context, report *PublishReport) {
	const childPageLimit = 64
	c.ownerMu.Lock()
	after := c.publicationChildCursor
	c.ownerMu.Unlock()
	children, err := c.Store.PendingPublicationChildren(ctx, after, childPageLimit)
	if err != nil {
		publicationError(report, "", "child_result_listing_unavailable")
		return
	}
	if len(children) == 0 && after != "" {
		c.ownerMu.Lock()
		c.publicationChildCursor = ""
		c.ownerMu.Unlock()
		children, err = c.Store.PendingPublicationChildren(ctx, "", childPageLimit)
		if err != nil {
			publicationError(report, "", "child_result_listing_unavailable")
			return
		}
	}
	for _, child := range children {
		c.ownerMu.Lock()
		c.publicationChildCursor = child.ID
		c.ownerMu.Unlock()
		switch child.Role {
		case project.RoleReviewer:
			if _, err := c.SubmitReviewForRun(ctx, child.Run); err != nil {
				publicationWait(report, child.PublicationAssignment, "review_child_pending")
				continue
			}
			c.progressAfterPublish(ctx, child.PublicationAssignment)
		case project.RoleCoder:
			correction := c.publishCorrectionChild(ctx, child)
			for _, wait := range correction.Waits {
				publicationWait(report, wait.ID, wait.Reason)
			}
			for _, failure := range correction.Errors {
				publicationError(report, failure.ID, failure.Reason)
			}
			for _, id := range correction.Fenced {
				report.Fenced = append(report.Fenced, id)
			}
			if len(correction.Corrected) != 0 {
				c.progressAfterPublish(ctx, child.PublicationAssignment)
			}
		}
	}
	if len(children) < childPageLimit {
		c.ownerMu.Lock()
		c.publicationChildCursor = ""
		c.ownerMu.Unlock()
	}
}

// publishCorrectionChild routes one settled corrector child to the
// report-carrying correction entry when settlement kept the corrector's
// parsed correction report. A corrector without its typed payload on a
// completed report refuses loudly; runs that have not reported yet stay
// on the wait path. The
// typed report journals its dispositions and agent-claimed fix evidence
// through the canonical gate with the publication's current check
// assessment; admission judges freshness itself. A missing assessment
// degrades to nil, but a real store failure surfaces instead of silently
// discarding check evidence. Agent-claimed evidence rides the journal
// only; the head advance still binds the settled accounted candidate,
// never the corrector's claims. This route never guesses dispositions.
func (c *Coordinator) publishCorrectionChild(ctx context.Context, child factory.Assignment) CorrectionReport {
	if child.Result == nil || child.Result.PhaseResult == nil || child.Result.PhaseResult.Output == nil ||
		child.Result.PhaseResult.Output.Correction == nil {
		if child.Phase == project.ExecutionCorrect && child.Result != nil && child.Result.Reported &&
			child.Result.Status == "completed" {
			bad := CorrectionReport{Corrected: []CorrectionLink{}}
			correctionError(&bad, child.Run, "correction_payload_missing")
			return bad
		}
		return c.PublishCorrection(ctx, child.Run)
	}
	report := *child.Result.PhaseResult.Output.Correction
	var checks *factory.CheckAssessment
	p, err := c.Store.PublicationByAssignment(ctx, child.PublicationAssignment)
	if err != nil {
		bad := CorrectionReport{Corrected: []CorrectionLink{}}
		correctionError(&bad, child.PublicationAssignment, "store_unavailable")
		return bad
	}
	assessment, err := c.Store.CheckAssessment(ctx, p.Repository, p.PRNumber)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			bad := CorrectionReport{Corrected: []CorrectionLink{}}
			correctionError(&bad, p.ID, "store_unavailable")
			return bad
		}
	} else {
		assessmentCopy := assessment
		checks = &assessmentCopy
	}
	return c.PublishCorrectionWithReport(ctx, child.Run, report, report.Evidence, checks)
}

func publicationError(report *PublishReport, id, reason string) {
	report.Errors = append(report.Errors, PublishError{ID: id, Reason: reason})
}

func publicationWait(report *PublishReport, id, reason string) {
	report.Waits = append(report.Waits, PublishWait{ID: id, Reason: reason})
}

func (c *Coordinator) resolvePublicationOwner(ctx context.Context, a factory.Assignment) (factory.Assignment, string, string, string, bool) {
	owner, candidate, runID, preparation := a, a.Result.Candidate, a.Run, a.Preparation
	if factory.ValidCommit(candidate) {
		return owner, candidate, runID, preparation, true
	}
	if a.Phase != project.ExecutionInvestigate || a.Subject.Issue == nil {
		return owner, candidate, runID, preparation, false
	}
	if advanceChildPlan(a) == nil {
		return owner, candidate, runID, preparation, false
	}
	// The first implementation has a deterministic advancement identity;
	// an explicit retry of a cancelled, never-launched child has its own
	// immutable owner-command identity. Only the current child can supply
	// this root's candidate, whichever normal admission produced it.
	child, err := c.Store.LatestIssueAttempt(ctx, a.Repository, a.Issue)
	if err != nil {
		return owner, candidate, runID, preparation, false
	}
	if child.Validate() != nil || child.Phase != project.ExecutionImplement || child.Role != project.RoleCoder ||
		child.ParentAssignment != a.ID || child.PublicationAssignment != a.ID || child.AttemptRoot != a.AttemptRoot ||
		child.Repository != a.Repository || child.Issue != a.Issue || child.Acceptance != a.Acceptance ||
		child.ProjectID != a.ProjectID || child.SourceCommit != a.SourceCommit || child.Preparation != a.Preparation ||
		child.Stage != factory.AssignmentFinished ||
		child.Outcome != factory.Succeeded || child.Result == nil || !child.Result.Reported ||
		child.Result.Status != "completed" || !factory.ValidCommit(child.Result.Candidate) {
		return owner, candidate, runID, preparation, false
	}
	return child, child.Result.Candidate, child.Run, child.Preparation, true
}

func (c *Coordinator) publishOne(ctx context.Context, a factory.Assignment, report *PublishReport) {
	if a.PublicationAssignment != a.ID || a.Role != project.RoleCoder || a.Result == nil || !a.Result.Reported || a.Result.Status != "completed" {
		return
	}
	// The publication owner is the root attempt, but a phased
	// investigation root carries the plan rather than the candidate:
	// resolve the advanced implement child that reported it and publish
	// its candidate under the root's slot. Legacy roots carry their own
	// candidate and publish directly.
	owner, candidate, runID, preparation, ok := c.resolvePublicationOwner(ctx, a)
	if !ok {
		return
	}
	if _, err := c.Store.PublicationByAssignment(ctx, a.ID); err == nil {
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		publicationError(report, a.ID, "store_unavailable")
		return
	}
	// FW15 join: a phased implement publishes only on completed plan task
	// evidence. The gate waits (never publishes) while tasks settle and
	// refuses without recording when evidence is conclusively unpublishable.
	if !c.gatePhasedPublication(ctx, owner, report) {
		return
	}
	policy, err := c.Store.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		publicationError(report, a.ID, "store_unavailable")
		return
	}
	// A retried implementation supplies its fresh admission authority while
	// the root keeps the publication identity and cumulative allowance.
	p := factory.Publication{
		Publish: factory.PublicationOperation{Kind: factory.OpRefPublish}, PRCreate: factory.PublicationOperation{Kind: factory.OpPRCreate},
		Authority: owner.Authority, ID: factory.NewID(), AssignmentID: a.ID, ProjectID: a.ProjectID, Role: a.Role,
		Acceptance: a.Acceptance, Preparation: preparation, Run: runID, Candidate: candidate,
		BaseSHA: a.SourceCommit, TargetBranch: policy.TargetBranch, Stage: factory.PublicationOpen,
		Repository: a.Repository, Issue: a.Issue, CreatedUnix: time.Now().Unix(),
	}
	if err := c.Store.RecordPublication(ctx, p); err != nil {
		publicationError(report, a.ID, "store_unavailable")
		return
	}
	c.reconcilePublication(ctx, p, report)
}

func (c *Coordinator) assignmentForPublication(ctx context.Context, p factory.Publication) (factory.Assignment, error) {
	a, err := c.Store.Assignment(ctx, p.AssignmentID)
	if err != nil {
		return factory.Assignment{}, err
	}
	if a.Repository != p.Repository || a.Issue != p.Issue {
		return factory.Assignment{}, store.ErrNotFound
	}
	return a, nil
}

func (c *Coordinator) completePublication(ctx context.Context, p *factory.Publication, report *PublishReport) {
	if p.Publish.Completion != factory.OpCompletionComplete || p.PRCreate.Completion != factory.OpCompletionComplete {
		publicationWait(report, p.ID, "native_completion_pending")
		return
	}
	c.finishPublication(ctx, *p, factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, report)
}

func (c *Coordinator) publicationCallError(report *PublishReport, id string, err error) {
	var wait *factory.PublicationWait
	var refused *factory.PublicationRefusal
	switch {
	case errors.As(err, &wait):
		publicationWait(report, id, wait.Reason)
	case errors.As(err, &refused):
		publicationWait(report, id, refused.Reason)
	default:
		publicationError(report, id, "native_unavailable")
	}
}

func (c *Coordinator) cancelRepositoryPublications(ctx context.Context, repository int64) factory.PublicationWithdrawal {
	return c.cancelPublications(ctx, repository, 0, "")
}

func (c *Coordinator) cancelAcceptancePublications(ctx context.Context, repository, issue int64, acceptance string) factory.PublicationWithdrawal {
	return c.cancelPublications(ctx, repository, issue, acceptance)
}

type exportTerminal struct{ Reason string }
