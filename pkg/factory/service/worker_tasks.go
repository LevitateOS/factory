package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/identity"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// WorkerTasks admits bounded commands to an existing task owner. The owner
// retains execution/replay; this service creates no submission or task journal.
type WorkerTasks struct {
	Store   *store.Store
	Inspect func(context.Context, execution.RunAddress) (execution.RunObservation, error)
	Command func(context.Context, execution.TaskRequest) (execution.TaskReceipt, error)
}

func (s WorkerTasks) current(ctx context.Context, cap factory.MCPCapability, action, taskID string) (factory.Run, error) {
	var empty factory.Run
	if s.Store == nil || cap.State != factory.MCPCapabilityActive || !factory.ValidPlanKey(taskID) {
		return empty, ErrWorkerBinding
	}
	phase := project.ExecutionImplement
	if action == "release_output" && cap.Profile == factory.MCPProfileFixer {
		phase = project.ExecutionCorrect
	} else if action != "start_task" || cap.Profile != factory.MCPProfileBuilder {
		return empty, errors.New("native task action differs from assigned role")
	}
	current, a, run, err := (WorkerAdmission{Store: s.Store}).current(ctx, cap)
	if err != nil {
		return empty, err
	}
	if current.State != factory.MCPCapabilityActive || a.Stage != factory.AssignmentAssigned || a.Role != project.RoleCoder || a.Phase != phase {
		return empty, ErrWorkerBinding
	}
	if _, frozen, err := s.Store.TerminalMCPProposal(ctx, a.ID); err != nil {
		return empty, err
	} else if frozen {
		return empty, store.ErrMCPSubmissionFrozen
	}
	if !run.SupportsTaskCommands() || run.Deadline.IsZero() || !time.Now().Before(run.Deadline) {
		return empty, errors.New("native task run has no current Muse binding")
	}
	if run.Native == nil {
		binding := run.IdentityBinding
		if binding == nil || binding.Validate() != nil || binding.ID != run.ID || binding.Project != run.ProjectID || binding.Kind != identity.Factory || binding.Generation != run.IdentityGeneration {
			return empty, ErrWorkerBinding
		}
	}
	prep, err := s.Store.Preparation(ctx, a.Preparation)
	if err != nil {
		return empty, err
	}
	if prep.Preparation.Project != a.ProjectID || prep.Preparation.Role != a.Role || prep.Preparation.SourceCommit != a.SourceCommit || (prep.Native == nil) != (run.Native == nil) {
		return empty, ErrWorkerBinding
	}
	if prep.Native != nil && (prep.Native.Profile != run.Native.Provenance.Profile || prep.Native.InstanceID != run.Native.Provenance.InstanceID || prep.Native.WorktreeID != run.Native.Provenance.WorktreeID) {
		return empty, ErrWorkerBinding
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	if err != nil || manifest.Run != run.ID || manifest.Phase != phase || !manifest.Deadline.Equal(run.Deadline) {
		return empty, errors.New("native task manifest differs from assigned run")
	}
	var plan factory.InvestigationPlan
	if json.Unmarshal(manifest.Plan, &plan) != nil || plan.Executable() != nil || plan.AttemptRoot != a.AttemptRoot || plan.Preparation != a.Preparation {
		return empty, errors.New("native task plan differs from assigned attempt")
	}
	head, generation, err := s.Store.PlanHead(ctx, a.AttemptRoot)
	if err != nil {
		return empty, err
	}
	retained, err := s.Store.InvestigationPlan(ctx, a.AttemptRoot, head)
	if err != nil {
		return empty, err
	}
	if head != plan.ID || generation != int64(plan.Generation) || retained.Digest != plan.Digest {
		return empty, errors.New("native task plan is no longer current")
	}
	for _, task := range plan.Tasks {
		if task.ID == taskID {
			return run, ctx.Err()
		}
	}
	return empty, errors.New("native task is absent from retained plan")
}

func workerTaskRequest(run factory.Run, observed execution.RunObservation, action, taskID string) (execution.TaskRequest, error) {
	request := execution.TaskRequest{Input: project.FactoryNativeTask{Project: run.ProjectID, Run: run.ID, InvocationID: run.InvocationID(), ManifestDigest: project.ExecutionManifestDigest(run.ExecutionManifest), Action: action, Arguments: project.FactoryNativeTaskArguments{TaskID: taskID}}, Kind: run.RuntimeKind()}
	if err := workerObservation(run, observed); err != nil {
		return request, err
	}
	if run.Native != nil {
		request.Native = run.Native
	} else {
		invocation := observed.Appliance.Invocation
		request.Appliance = &invocation
		if run.IdentityBinding.InvocationID != invocation.Invocation || run.IdentityGeneration != invocation.Generation {
			return request, ErrWorkerObservation
		}
	}
	return request, request.Validate()
}

func (s WorkerTasks) Execute(ctx context.Context, cap factory.MCPCapability, action, taskID string) (map[string]any, error) {
	if s.Inspect == nil || s.Command == nil {
		return nil, errors.New("live native task controller unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	run, err := s.current(bounded, cap, action, taskID)
	if err != nil {
		return nil, err
	}
	observeCtx, stop := context.WithTimeout(bounded, 5*time.Second)
	observed, err := s.Inspect(observeCtx, execution.RunAddress{Run: run.ID, Workspace: run.ProjectID})
	if err == nil {
		err = observeCtx.Err()
	}
	stop()
	if err != nil {
		return nil, err
	}
	request, err := workerTaskRequest(run, observed, action, taskID)
	if err != nil {
		return nil, err
	}
	latest, err := s.current(bounded, cap, action, taskID)
	if err != nil {
		return nil, err
	}
	current, err := workerTaskRequest(latest, observed, action, taskID)
	if err != nil || !request.Equal(current) || !bytes.Equal(run.ExecutionManifest, latest.ExecutionManifest) {
		return nil, execution.ErrTask
	}
	// Exact incarnation/manifest enforcement and durable replay stay with the
	// runtime task owner. An uncertain call is returned without implicit retry.
	requestCtx, finish := context.WithTimeout(bounded, 30*time.Second)
	receipt, err := s.Command(requestCtx, request)
	if err == nil {
		err = requestCtx.Err()
	}
	finish()
	if err != nil {
		return nil, err
	}
	if err := receipt.ValidateFor(request); err != nil {
		return nil, err
	}
	latest, err = s.current(bounded, cap, action, taskID)
	if err != nil {
		return nil, err
	}
	current, err = workerTaskRequest(latest, observed, action, taskID)
	if err != nil || !request.Equal(current) {
		return nil, execution.ErrTask
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return receipt.Result, nil
}
