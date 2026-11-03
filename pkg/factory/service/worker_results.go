package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
	"github.com/levitateos/factory/pkg/strictjson"
)

// WorkerResults consumes an already admitted worker and canonical recorded
// facts. Binding fills existing scope; validation/readback never admits new
// work, writes a proposal, settles it or replaces the controller/store journal.
type WorkerResults struct {
	Assignments WorkerAssignmentSource
	Runs        WorkerRunSource
	Current     WorkerCurrentContextSource
	Receipts    WorkerCheckpointSource
	Catalog     func(context.Context, factory.MCPCapability) (factory.ApprovedCatalog, error)
	Now         func() time.Time
}

func (s WorkerResults) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type WorkerCheckpointSource interface {
	PlanTaskReceipts(context.Context, string, string) ([]store.PlanTaskReceipt, error)
}

// Bind fills the bindings SodaOS already records for this
// assignment, so a worker never copies them back: the issue plan's recorded
// fields, the review cycle tuple and clear coverage, the correction's finding
// digest, the task manifest digest with the observed task output and child,
// the committed final candidate, and the app-verification delivery and
// input digest. Any value the worker sent is overwritten. A missing source
// leaves the payload as it is, and settlement reports the gap.
func (s WorkerResults) Bind(ctx context.Context, capability factory.MCPCapability, tool factory.MCPTool, payload map[string]any) {
	if payload == nil {
		return
	}
	switch tool {
	case factory.MCPToolFinishIssuePlan:
		s.bindIssuePlan(ctx, capability, payload)
	case factory.MCPToolFinishReview:
		source := s.Current
		if source == nil {
			return
		}
		current, err := source.FactoryCurrentContext(ctx, capability, s.now())
		if err != nil || current.Role.Review == nil {
			return
		}
		review := current.Role.Review
		payload["tuple"] = map[string]any{"cycle_id": review.Cycle.ID, "head": review.Cycle.Head, "base_oid": review.Cycle.BaseOID}
		if review.ClearCoverageDigest != "" {
			payload["coverage"] = review.ClearCoverageDigest
		}
	case factory.MCPToolFinishCorrection:
		if s.Assignments == nil {
			return
		}
		if a, err := s.Assignments.Assignment(ctx, capability.AssignmentID); err == nil && a.ReviewContext != nil {
			payload["finding_digest"] = a.ReviewContext.Cycle.FindingDigest
		}
	case factory.MCPToolSubmitTaskOutput:
		if s.Runs == nil {
			return
		}
		if run, err := s.Runs.FactoryRun(ctx, capability.RunID); err == nil {
			payload["manifest_digest"] = project.ExecutionManifestDigest(run.ExecutionManifest)
			s.bindTaskOutput(ctx, run, payload)
		}
	case factory.MCPToolFinishImplement:
		if s.Runs == nil {
			return
		}
		if run, err := s.Runs.FactoryRun(ctx, capability.RunID); err == nil && run.FinalCheckpoint != nil &&
			run.FinalCheckpoint.Status == "committed" && factory.ValidCommit(run.FinalCheckpoint.Candidate) {
			payload["candidate"] = run.FinalCheckpoint.Candidate
		}
	case factory.MCPToolFinishAppVerify, factory.MCPToolRecordJourneyResult:
		payload["input_digest"] = capability.ScopeDigest()
		if s.Assignments == nil {
			return
		}
		if a, err := s.Assignments.Assignment(ctx, capability.AssignmentID); err == nil && a.Subject.Delivery != nil {
			payload["delivery_id"] = a.Subject.Delivery.DeliveryID
			if tool == factory.MCPToolRecordJourneyResult {
				payload["source_commit"] = a.SourceCommit
				payload["preparation"] = a.Preparation
				payload["manifest_digest"] = a.Subject.Delivery.VerificationManifestDigest
			}
		}
	}
}

// bindTaskOutput fills the observed output digest and native child of the
// named task from its output-ready plan-task receipt: the same record the
// checkpoint drive compares the submission against.
func (s WorkerResults) bindTaskOutput(ctx context.Context, run factory.Run, payload map[string]any) {
	taskID, _ := payload["task_id"].(string)
	source := s.Receipts
	if source == nil || taskID == "" || len(run.ExecutionManifest) == 0 {
		return
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	if err != nil {
		return
	}
	var plan factory.InvestigationPlan
	if json.Unmarshal(manifest.Plan, &plan) != nil {
		return
	}
	receipts, err := source.PlanTaskReceipts(ctx, plan.AttemptRoot, plan.ID)
	if err != nil {
		return
	}
	for _, receipt := range receipts {
		if receipt.TaskID != taskID || receipt.State != store.PlanTaskOutputReady {
			continue
		}
		payload["output_digest"] = receipt.OutputDigest
		if receipt.Child != "" {
			payload["native_child_ref"] = receipt.Child
		} else {
			delete(payload, "native_child_ref")
		}
	}
}

// bindIssuePlan fills the plan fields this assignment already records,
// then its digest, so a worker never hand-copies 64-digit identities.
// Workers dropped or changed digits copying them from factory.context
// and spent their finish calls on the refusals. A plan that does not
// decode is left untouched for the validation refusal to name.
func (s WorkerResults) bindIssuePlan(ctx context.Context, capability factory.MCPCapability, payload map[string]any) {
	tasks, ok := payload["tasks"].(map[string]any)
	if !ok || s.Assignments == nil {
		return
	}
	assigned, err := s.Assignments.Assignment(ctx, capability.AssignmentID)
	if err != nil || assigned.Phase != project.ExecutionInvestigate {
		return
	}
	tasks["attempt_root"] = assigned.AttemptRoot
	tasks["source_commit"] = assigned.SourceCommit
	tasks["preparation"] = assigned.Preparation
	tasks["scope_digest"] = capability.ScopeDigest()
	tasks["input_digest"] = assigned.PromptSHA
	tasks["digest"] = ""
	encoded, err := json.Marshal(tasks)
	if err != nil {
		return
	}
	var plan factory.InvestigationPlan
	if err := strictjson.Decode(bytes.NewReader(encoded), &plan); err != nil {
		return
	}
	if plan.Generation == 0 {
		plan.IntegrationCommit = plan.SourceCommit
		tasks["integration_commit"] = plan.SourceCommit
	}
	if digest, err := factory.PlanDigest(plan); err == nil {
		tasks["digest"] = digest
	}
}

// checkManifestProposal validates a product plan's
// verification_manifest_proposal against the approved check catalog:
// catalog references, parameter bounds and artifact/fixture requirements.
// Outcome coverage against the planning scope is checked by settlement.
func (s WorkerResults) checkManifestProposal(ctx context.Context, cap factory.MCPCapability, payload map[string]any) error {
	raw, present := payload["verification_manifest_proposal"]
	if !present {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return errors.New("manifest proposal is not JSON")
	}
	if len(encoded) > factory.MCPMaxManifestBytes {
		return errors.New("manifest proposal exceeds 64 KiB")
	}
	proposal, err := factory.ParseVerificationManifestProposal(encoded)
	if err != nil {
		return errors.New("manifest proposal shape refused")
	}
	if s.Catalog == nil {
		return errors.New("no approved check catalog")
	}
	catalog, err := s.Catalog(ctx, cap)
	if err != nil {
		return err
	}
	return factory.ValidateManifestProposal(proposal, catalog)
}

// CheckNewFinish refuses a new terminal finish that settlement could not
// settle, before its immutable proposal freezes the assignment. It runs the
// settler's own mapping, so the worker sees the exact defect while it can
// still correct the payload and finish again.
func (s WorkerResults) CheckNewFinish(ctx context.Context, capability factory.MCPCapability, submission factory.MCPSubmission, payload map[string]any) error {
	if !factory.MCPIsTerminal(submission.Tool) {
		return nil
	}
	if submission.Tool == factory.MCPToolFinishProductPlan {
		if err := s.checkManifestProposal(ctx, capability, payload); err != nil {
			return err
		}
	}
	if s.Assignments == nil {
		return errors.New("assignment unavailable; no terminal proposal recorded")
	}
	assigned, err := s.Assignments.Assignment(ctx, submission.AssignmentID)
	if err != nil {
		return err
	}
	return control.CheckMCPTerminalFinish(assigned, submission, s.now())
}

// Accepted means the immutable proposal was consumed. The result explicitly
// distinguishes successful work from a blocked or invalid consumed report.
func (s WorkerResults) Status(ctx context.Context, assignment string, status factory.MCPWorkStatus) map[string]any {
	body := map[string]any{
		"submission_id": status.SubmissionID, "state": status.State, "reason": status.Reason,
		"feed_post_id": status.FeedPostID,
	}
	if factory.MCPIsTerminal(status.Tool) && status.State == factory.MCPWorkAccepted && s.Assignments != nil {
		body["settlement"] = map[string]any{"availability": "unavailable"}
		if a, err := s.Assignments.Assignment(ctx, assignment); err == nil && a.Stage == factory.AssignmentFinished {
			settlement := map[string]any{"availability": "complete", "outcome": a.Outcome, "reason": a.Reason, "reported": a.Result != nil && a.Result.Reported}
			if a.Result != nil {
				settlement["status"] = a.Result.Status
				settlement["summary"] = a.Result.Summary
			}
			body["settlement"] = settlement
		}
	}
	return body
}

// CheckpointContext projects bounded native observations and retained
// receipts. Reading context never starts a checkpoint or spends a model call.
func (s WorkerResults) CheckpointContext(ctx context.Context, read WorkerContextRead) (map[string]any, error) {
	phase := factory.MCPPhaseOf(read.Capability.Profile)
	if phase != project.ExecutionImplement && phase != project.ExecutionCorrect {
		return nil, nil
	}
	source := s.Receipts
	if source == nil || s.Runs == nil {
		return nil, nil
	}
	run, err := s.Runs.FactoryRun(ctx, read.Capability.RunID)
	if err != nil {
		return nil, err
	}
	if len(run.ExecutionManifest) == 0 {
		return nil, nil
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	if err != nil {
		return nil, err
	}
	var plan factory.InvestigationPlan
	if json.Unmarshal(manifest.Plan, &plan) != nil || plan.Validate() != nil || plan.AttemptRoot != read.Assignment.AttemptRoot || manifest.Run != run.ID || manifest.Phase != phase {
		return nil, errors.New("native task context does not bind its assigned manifest")
	}
	receipts, err := source.PlanTaskReceipts(ctx, plan.AttemptRoot, plan.ID)
	if err != nil {
		return nil, err
	}
	byTask := map[string]store.PlanTaskReceipt{}
	head, last := plan.SourceCommit, int64(0)
	for _, receipt := range receipts {
		byTask[receipt.TaskID] = receipt
		if receipt.Commit == "" {
			continue
		}
		for _, ref := range receipt.Events {
			suffix, found := strings.CutPrefix(ref, project.TaskEventTaskCommitted+"@")
			if !found {
				continue
			}
			sequence, _, _ := strings.Cut(suffix, ":")
			number, err := strconv.ParseInt(sequence, 10, 64)
			if err == nil && number > last {
				last, head = number, receipt.Commit
			}
		}
	}
	tasks := make([]map[string]any, 0, len(plan.Tasks))
	ready := make([]map[string]any, 0, 4)
	for _, task := range plan.Tasks {
		receipt, exists := byTask[task.ID]
		state := store.PlanTaskPending
		if exists {
			state = receipt.State
		}
		tasks = append(tasks, map[string]any{"task_id": task.ID, "state": state})
		if state != store.PlanTaskOutputReady {
			continue
		}
		if len(ready) >= 4 {
			return nil, errors.New("native task output exceeds admitted concurrency")
		}
		output := map[string]any{"task_id": task.ID, "output_digest": receipt.OutputDigest}
		if receipt.Child != "" {
			output["native_child_ref"] = receipt.Child
		}
		ready = append(ready, output)
	}
	execution := map[string]any{
		"manifest_digest":  project.ExecutionManifestDigest(run.ExecutionManifest),
		"parent_only":      phase == project.ExecutionCorrect,
		"integration_head": head, "tasks": tasks, "ready_outputs": ready,
		"guidance": "Submit each ready output with factory.submit_task_output and its task_id; SodaOS fills the observed digest and native child. Read factory.status for checkpoint progress; successors need a committed native checkpoint. Do not write Git metadata. Finish implementation only after integration.status is committed; SodaOS fills the candidate from it.",
	}
	if run.FinalCheckpoint != nil {
		execution["integration"] = run.FinalCheckpoint
	}
	return execution, nil
}
