package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

var _ execution.TaskRuntime = NativeExecution{}

func (NativeExecution) SupportsTaskSource(authentication string) bool {
	return authentication == execution.NativeMuseLogin
}

type nativeTaskParent struct {
	Invocation       string `json:"invocation"`
	SystemInstance   string `json:"system_instance"`
	StartIdentity    string `json:"start_identity"`
	PID              int    `json:"pid"`
	ExecutableSHA256 string `json:"executable_sha256"`
}

func taskParent(process *execution.NativeProcess) nativeTaskParent {
	return nativeTaskParent{process.InvocationID, process.SystemInstance, process.StartIdentity, process.PID, process.ExecutableSHA256}
}

type nativeTaskReply struct {
	IntentDigest  string `json:"intent_digest"`
	ReceiptDigest string `json:"receipt_digest"`
	Result        struct {
		Status       string                          `json:"status"`
		Head         string                          `json:"head,omitempty"`
		Tree         string                          `json:"tree,omitempty"`
		OutputDigest string                          `json:"output_digest,omitempty"`
		Content      *string                         `json:"content,omitempty"`
		FileDigest   string                          `json:"file_digest,omitempty"`
		TotalBytes   *int64                          `json:"total_bytes,omitempty"`
		NextOffset   *int64                          `json:"next_offset,omitempty"`
		Checks       []project.CheckpointCheckResult `json:"checks,omitempty"`
	} `json:"result"`
}

func (p NativeExecution) taskRun(ctx context.Context, projectID, runID string) (factory.Run, error) {
	if p.Store == nil {
		return factory.Run{}, execution.ErrTaskUnavailable
	}
	run, err := p.Store.FactoryRun(ctx, runID)
	if err != nil {
		return run, err
	}
	if run.ProjectID != projectID || run.Native == nil || run.Native.Validate() != nil || run.Native.Intent == "" ||
		run.Native.Provenance.Profile != p.Config.Profile || run.Native.Provenance.TrustMode != execution.NativeCLIProvider || run.Native.Provenance.Process == nil {
		return run, fmt.Errorf("native task stored binding: %w", execution.ErrTask)
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	intent, intentErr := execution.ParseNativeIntent(run.Native.Intent)
	if err != nil || intentErr != nil || manifest.MaxChildren != 0 || manifest.Run != runID || manifest.Phase != intent.ExecutionPhase || !manifest.Deadline.Equal(run.Deadline) ||
		intent.ManifestDigest != project.ExecutionManifestDigest(run.ExecutionManifest) {
		return run, execution.ErrTaskUnavailable
	}
	return run, nil
}
func (p NativeExecution) taskCall(ctx context.Context, run factory.Run, operation string, command map[string]any) (nativeTaskReply, error) {
	var reply nativeTaskReply
	err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation string           `json:"operation"`
		Intent    string           `json:"intent"`
		Parent    nativeTaskParent `json:"parent"`
		Command   map[string]any   `json:"command"`
	}{operation, run.Native.Intent, taskParent(run.Native.Provenance.Process), command}, &reply)
	if err != nil {
		return reply, err
	}
	hash := sha256.Sum256([]byte(run.Native.Intent))
	if reply.IntentDigest != hex.EncodeToString(hash[:]) || !project.ValidDigest(reply.ReceiptDigest) {
		return reply, fmt.Errorf("native task response binding: %w", execution.ErrTask)
	}
	return reply, nil
}
func (p NativeExecution) CommandTask(ctx context.Context, request execution.TaskRequest) (execution.TaskReceipt, error) {
	var empty execution.TaskReceipt
	if request.Validate() != nil || request.Kind != collaboration.Native {
		return empty, fmt.Errorf("native task request: %w", execution.ErrTask)
	}
	run, err := p.taskRun(ctx, request.Input.Project, request.Input.Run)
	if err != nil {
		return empty, err
	}
	if !reflect.DeepEqual(run.Native, request.Native) || request.Input.ManifestDigest != project.ExecutionManifestDigest(run.ExecutionManifest) ||
		run.Native.Provenance.InstanceID != p.Instance || run.Reconciled {
		return empty, fmt.Errorf("native task current run: %w", execution.ErrTask)
	}
	args := request.Input.Arguments
	command := map[string]any{"task": args.TaskID}
	switch request.Input.Action {
	case "start_task":
		observed, err := p.taskCall(ctx, run, "task", map[string]any{"action": "observe"})
		if err != nil {
			return empty, err
		}
		if observed.Result.Status != "ok" || !factory.ValidCommit(observed.Result.Head) {
			return empty, execution.ErrTaskUnavailable
		}
		// A known refusal at one head stays immutable. Once the previous
		// checkpoint commits, the next explicit call captures a different head
		// and command identity; uncertain old starts are never replayed.
		command["action"], command["expected_head"] = "start_after", observed.Result.Head
	case "release_output":
		command["action"] = "release"
	case "workspace":
		command["path"] = args.Path
		switch args.Operation {
		case "read":
			command["action"], command["offset"], command["limit"] = "read", args.Offset, args.Limit
		case "edit":
			command["action"], command["operation_id"], command["offset"], command["expected"], command["replacement"] = "edit", args.OperationID, args.Offset, args.Expected, args.Replacement
		case "mkdir":
			command["action"], command["operation_id"] = "make_directory", args.OperationID
		case "remove":
			command["action"], command["operation_id"], command["expected_digest"] = "remove", args.OperationID, args.ExpectedDigest
		}
	}
	reply, err := p.taskCall(ctx, run, "task", command)
	if err != nil {
		return empty, err
	}
	read := request.Input.Action == "workspace" && args.Operation == "read"
	missing := read && reply.Result.Status == "missing"
	if request.Input.Action == "start_task" {
		switch reply.Result.Status {
		case "task_busy", "dependency_pending", "stale_head":
			return empty, errors.New("task start is waiting for the prior committed checkpoint: inspect factory.context and factory.outputs, submit any ready task output and wait for its committed checkpoint before starting another task")
		case "task_completed":
			return empty, errors.New("task is already committed: inspect factory.context and choose the next unfinished task")
		}
	}
	if reply.Result.Status == "preimage_mismatch" && request.Input.Action == "workspace" && args.Operation == "edit" {
		return empty, errors.New("workspace edit preimage differs: expected must match at the zero-based UTF-8 byte offset; offset 0 is the beginning of the file, not a text search. Read the current file, then correct the offset or replace its complete read content at offset 0. Use a fresh operation_id; the refused edit changed nothing")
	}
	if reply.Result.Status != "ok" && !missing {
		return empty, execution.ErrTaskUnavailable
	}
	result := map[string]any{"task_id": args.TaskID, "execution_mode": "sequential_parent", "head": reply.Result.Head}
	if read {
		result["exists"] = !missing
	}
	if reply.Result.OutputDigest != "" {
		result["output_digest"] = reply.Result.OutputDigest
	}
	if reply.Result.Content != nil {
		result["content"] = *reply.Result.Content
		result["file_digest"] = reply.Result.FileDigest
		result["total_bytes"] = reply.Result.TotalBytes
		result["next_offset"] = reply.Result.NextOffset
	}
	return execution.TaskReceipt{Request: request, Result: result}, nil
}
func (p NativeExecution) CheckpointTask(ctx context.Context, request execution.CheckpointRequest) (execution.CheckpointReceipt, error) {
	return p.checkpoint(ctx, request, false, false)
}
func (p NativeExecution) CheckpointFinal(ctx context.Context, request execution.CheckpointRequest) (execution.CheckpointReceipt, error) {
	return p.checkpoint(ctx, request, true, false)
}
func (p NativeExecution) ReadFinalCheckpoint(ctx context.Context, request execution.CheckpointRequest) (execution.CheckpointReceipt, error) {
	return p.checkpoint(ctx, request, true, true)
}
func (p NativeExecution) checkpoint(ctx context.Context, request execution.CheckpointRequest, final, read bool) (execution.CheckpointReceipt, error) {
	var empty execution.CheckpointReceipt
	if request.Validate(final) != nil || request.Native == nil {
		return empty, execution.ErrCheckpoint
	}
	run, err := p.taskRun(ctx, request.Project, request.ID)
	if err != nil {
		return empty, err
	}
	if run.CheckpointBinding(request) != nil || !request.MatchesNative(run.Native.Provenance) {
		return empty, execution.ErrCheckpoint
	}
	if !read && (run.Reconciled || run.Native.Provenance.InstanceID != p.Instance || run.Native.Sealed) {
		return empty, execution.ErrCheckpoint
	}
	command := map[string]any{"action": "checkpoint", "task": request.TaskKey, "output_digest": request.OutputDigest, "expected_head": request.ExpectedHead}
	if final {
		command = map[string]any{"action": "final", "expected_head": request.ExpectedHead}
	}
	operation := "task"
	if read {
		operation = "read_task_receipt"
	}
	reply, err := p.taskCall(ctx, run, operation, command)
	if err != nil {
		return empty, err
	}
	result := execution.CheckpointReceipt{Request: request, FinalCandidate: final, Status: reply.Result.Status, Checks: reply.Result.Checks}
	if result.Status == "committed" {
		result.Candidate = reply.Result.Head
		result.Tree = reply.Result.Tree
		result.ReceiptDigest = reply.ReceiptDigest
	}
	return result, result.ValidateFor(request, final)
}
func (p NativeExecution) FactoryTaskEvents(ctx context.Context, request project.FactoryTaskEvents) (project.FactoryTaskEventsState, error) {
	result := project.FactoryTaskEventsState{ID: request.ID, Project: request.Project, NextSequence: request.AfterSequence + 1, Events: []project.TaskEvent{}}
	if err := request.Validate(); err != nil {
		return result, err
	}
	run, err := p.taskRun(ctx, request.Project, request.ID)
	if err != nil {
		return result, err
	}
	var reply struct {
		IntentDigest string              `json:"intent_digest"`
		Events       []project.TaskEvent `json:"events"`
	}
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation string           `json:"operation"`
		Intent    string           `json:"intent"`
		Parent    nativeTaskParent `json:"parent"`
	}{"task_events", run.Native.Intent, taskParent(run.Native.Provenance.Process)}, &reply)
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256([]byte(run.Native.Intent))
	if reply.IntentDigest != hex.EncodeToString(hash[:]) || len(reply.Events) > project.MaxTaskJournalEvents || request.AfterSequence > int64(len(reply.Events)) {
		return result, execution.ErrTask
	}
	budget := int64(0)
	previous := ""
	for i, event := range reply.Events {
		if event.Validate() != nil || event.Sequence != int64(i+1) || event.PrevDigest != previous || event.Invocation != run.InvocationID() || event.PlanDigest != project.ExecutionManifestDigest(run.ExecutionManifest) {
			return result, execution.ErrTask
		}
		previous = project.TaskEventDigest(event)
		if event.Sequence <= request.AfterSequence {
			continue
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return result, err
		}
		if len(result.Events) == project.MaxTaskEventsPerRead || budget+int64(len(encoded)) > request.ByteLimit {
			break
		}
		result.Events = append(result.Events, event)
		result.NextSequence = event.Sequence + 1
		budget += int64(len(encoded))
		result.Terminal = result.Terminal || event.Type == project.TaskEventRunStopped
	}
	if len(result.Events) == 0 && request.AfterSequence < int64(len(reply.Events)) {
		return result, errors.New("task event exceeds requested page bound")
	}
	return result, result.Validate()
}
