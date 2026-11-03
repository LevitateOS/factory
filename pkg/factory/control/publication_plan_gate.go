package control

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// FW15 phased publication join, Go side (T17).
//
// A finished phased implement assignment may publish only on completed plan
// task evidence: every planned task terminally completed, no failed or
// cancelled task, a valid candidate, and something new to publish. Missing
// evidence waits (the FW24 progress loop still owns persistence); conclusive
// evidence against publication refuses without recording anything, so no PR
// is ever fabricated and no duplicate write exists to replay.
//
// Native final-candidate receipts attest task ancestry, the exact clean tree
// and required integration checks. Go admits that receipt only for this run's
// retained manifest, observed invocation and reported candidate.

// Plan publication wait reasons. Both leave the assignment publishable for
// a later pass; neither records nor publishes anything.
const (
	// PlanWaitEvidenceUnavailable reports a missing plan head or revision:
	// the plan was never persisted or a replacement is in flight.
	PlanWaitEvidenceUnavailable = "plan_evidence_unavailable"
	// PlanWaitTasksIncomplete reports tasks that are missing, pending,
	// running or output-ready: descendants have not settled.
	PlanWaitTasksIncomplete    = "plan_tasks_incomplete"
	PlanWaitFinalIntegration   = "final_integration_pending"
	PlanRefuseFinalIntegration = "final_integration_failed"
)

// Plan publication refusal reasons. The assignment stays listed so a later
// terminal owner (FW31) can dispose it; publication records nothing.
const (
	// PlanRefuseTasksFailed reports a failed or cancelled task: incomplete
	// work cannot publish.
	PlanRefuseTasksFailed = "plan_tasks_failed"
	// PlanRefuseEvidence reports a candidate outside its admitted plan or
	// without completed task evidence.
	PlanRefuseEvidence = "plan_evidence_refused"
	// PlanRefuseUnchanged reports a wholly unchanged issue: no task
	// produced a commit and the candidate is the admitted base, so there
	// is no PR to make.
	PlanRefuseUnchanged = "no_changes_to_publish"
	// PlanRefuseCandidate reports a candidate that is not a commit.
	PlanRefuseCandidate = "candidate_invalid"
)

// PlanPublicationVerdict reports whether admitted tasks are ready to publish.
// Exactly one of Ready, WaitReason and RefuseReason is set.
type PlanPublicationVerdict struct {
	WaitReason   string
	RefuseReason string
	Ready        bool
}

// judgePlanPublication joins current task state to its admitted plan. The store
// validates plans, task identities and check references when saving them; its
// plan-scoped query and task primary key supply the matching, unique rows here.
// Native final-candidate checks own ancestry, tree and integration proof.
func judgePlanPublication(plan factory.InvestigationPlan, receipts []store.PlanTaskReceipt, candidate string) PlanPublicationVerdict {
	refuse := func(reason string) PlanPublicationVerdict { return PlanPublicationVerdict{RefuseReason: reason} }
	if !factory.ValidCommit(candidate) {
		return refuse(PlanRefuseCandidate)
	}
	tasks := make(map[string]store.PlanTaskReceipt, len(receipts))
	for _, receipt := range receipts {
		tasks[receipt.TaskID] = receipt
	}
	changed := false
	for _, task := range plan.Tasks {
		receipt, ok := tasks[task.ID]
		if !ok {
			return PlanPublicationVerdict{WaitReason: PlanWaitTasksIncomplete}
		}
		switch receipt.State {
		case store.PlanTaskFailed, store.PlanTaskCancelled:
			return refuse(PlanRefuseTasksFailed)
		case store.PlanTaskCompleted:
		default:
			return PlanPublicationVerdict{WaitReason: PlanWaitTasksIncomplete}
		}
		if receipt.Commit != "" {
			if receipt.Tree == "" {
				return refuse(PlanRefuseEvidence)
			}
			changed = true
		}
	}
	if !changed && candidate == plan.SourceCommit {
		return refuse(PlanRefuseUnchanged)
	}
	if !changed || candidate == plan.SourceCommit {
		return refuse(PlanRefuseEvidence)
	}
	return PlanPublicationVerdict{Ready: true}
}

// gatePhasedPublication requires completed plan task evidence before an
// implement assignment may publish. Other phases skip the gate. It
// reports false after recording the wait, so the caller publishes nothing
// and retries on a later pass.
func (c *Coordinator) gatePhasedPublication(ctx context.Context, a factory.Assignment, report *PublishReport) bool {
	if a.Phase != project.ExecutionImplement {
		return true
	}
	run, err := c.Store.FactoryRun(ctx, a.Run)
	if err != nil {
		publicationError(report, a.ID, "store_unavailable")
		return false
	}
	var nativeState execution.RunObservation
	if len(run.ExecutionManifest) > 0 {
		state, err := c.Host.InspectRun(ctx, execution.RunAddress{Workspace: run.ProjectID, Run: run.ID})
		if err != nil {
			publicationWait(report, a.ID, PlanWaitEvidenceUnavailable)
			return false
		}
		nativeState = state
		drained, _, err := c.importRunTaskEvents(ctx, run, state)
		if err != nil || !drained {
			publicationWait(report, a.ID, PlanWaitTasksIncomplete)
			return false
		}
	}
	headID, _, err := c.Store.PlanHead(ctx, a.AttemptRoot)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		publicationWait(report, a.ID, PlanWaitEvidenceUnavailable)
		return false
	default:
		publicationError(report, a.ID, "store_unavailable")
		return false
	}
	plan, err := c.Store.InvestigationPlan(ctx, a.AttemptRoot, headID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			publicationWait(report, a.ID, PlanWaitEvidenceUnavailable)
			return false
		}
		publicationError(report, a.ID, "store_unavailable")
		return false
	}
	// Completed receipts belong to the exact plan admitted to this run.
	// A replacement head must never make an older candidate publishable.
	if len(run.ExecutionManifest) > 0 {
		manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
		var admitted factory.InvestigationPlan
		if err != nil || manifest.Phase != project.ExecutionImplement || manifest.Run != a.Run ||
			json.Unmarshal(manifest.Plan, &admitted) != nil || admitted.AttemptRoot != a.AttemptRoot ||
			admitted.ID != plan.ID || admitted.Digest != plan.Digest || admitted.SourceCommit != a.SourceCommit {
			publicationWait(report, a.ID, PlanRefuseEvidence)
			return false
		}
	}
	receipts, err := c.Store.PlanTaskReceipts(ctx, a.AttemptRoot, headID)
	if err != nil {
		publicationError(report, a.ID, "store_unavailable")
		return false
	}
	verdict := judgePlanPublication(plan, receipts, a.Result.Candidate)
	if verdict.Ready {
		if reason := finalCheckpointGate(run, plan, nativeState, a.Result.Candidate); reason != "" {
			publicationWait(report, a.ID, reason)
			return false
		}
		return true
	}
	if verdict.RefuseReason != "" {
		publicationWait(report, a.ID, verdict.RefuseReason)
		return false
	}
	publicationWait(report, a.ID, verdict.WaitReason)
	return false
}

func finalCheckpointGate(run factory.Run, plan factory.InvestigationPlan, state execution.RunObservation, candidate string) string {
	if len(run.ExecutionManifest) == 0 {
		return ""
	}
	final := run.FinalCheckpoint
	if final == nil || final.Status == "pending" || final.Status == "busy" {
		return PlanWaitFinalIntegration
	}
	if final.Status == "failed" {
		return PlanRefuseFinalIntegration
	}
	request, err := checkpointRequest(run, state, candidate)
	if err != nil {
		return PlanRefuseEvidence
	}
	return finalCheckpointReceiptGate(run, plan, request)
}

// Retained terminal processing validates the frozen request without inventing
// a runtime observation. Publication additionally supplies a fresh observation.
func finalCheckpointReceiptGate(run factory.Run, plan factory.InvestigationPlan, request execution.CheckpointRequest) string {
	final := run.FinalCheckpoint
	if final == nil || final.Status == "pending" || final.Status == "busy" {
		return PlanWaitFinalIntegration
	}
	if final.Status == "failed" {
		return PlanRefuseFinalIntegration
	}
	if run.CheckpointBinding(request) != nil || final.ValidateFor(request, true) != nil || final.Status != "committed" {
		return PlanRefuseEvidence
	}
	passed := map[string]bool{}
	for _, check := range final.Checks {
		passed[check.ID] = check.Passed
	}
	for _, check := range plan.Checks {
		if check.Scope == factory.CheckScopeIntegration && check.Required && !passed[check.ID] {
			return PlanRefuseFinalIntegration
		}
	}
	return ""
}
