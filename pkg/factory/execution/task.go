package execution

import (
	"context"
	"errors"
	"reflect"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

var ErrTask = errors.New("task command differs from its captured runtime binding")
var ErrTaskUnavailable = errors.New("task and checkpoint runtime unavailable")

// TaskRequest addresses the existing task owner for a running worker. The
// adapter must condition the command on this exact process/manifest identity;
// observing a process grants no launch, placement or credential authority.
type TaskRequest struct {
	Input     project.FactoryNativeTask
	Kind      collaboration.RuntimeKind
	Appliance *ApplianceInvocation
	Native    *NativeRecord
}

func (r TaskRequest) Address() RunAddress {
	return RunAddress{Run: r.Input.Run, Workspace: r.Input.Project}
}

func (r TaskRequest) Validate() error {
	if r.Input.Validate() != nil {
		return ErrTask
	}
	switch r.Kind {
	case collaboration.Appliance:
		p := r.Appliance
		if p == nil || r.Native != nil || p.ValidateFor(r.Input.Project, p.Generation) != nil || p.Generation < 1 || p.Container == "" || p.Unit == "" || p.Invocation != r.Input.InvocationID {
			return ErrTask
		}
	case collaboration.Native:
		n := r.Native
		if n == nil || r.Appliance != nil || n.Validate() != nil || n.Sealed || n.Retirement != Running || n.Provenance.Process == nil {
			return ErrTask
		}
		p := n.Provenance.Process
		if p.InvocationID != r.Input.InvocationID || p.ExitCode != nil || p.ExitSignal != nil {
			return ErrTask
		}
	default:
		return ErrTask
	}
	return nil
}

func (r TaskRequest) Equal(other TaskRequest) bool { return reflect.DeepEqual(r, other) }

// TaskReceipt preserves the task owner's result, including its replay receipt.
// It is not a second workflow or an assertion that the task has completed.
type TaskReceipt struct {
	Request TaskRequest
	Result  map[string]any
}

func (r TaskReceipt) ValidateFor(request TaskRequest) error {
	if request.Validate() != nil || !r.Request.Equal(request) || r.Result == nil || r.Result["task_id"] != request.Input.Arguments.TaskID {
		return ErrTask
	}
	return nil
}

type TaskCommander interface {
	CommandTask(context.Context, TaskRequest) (TaskReceipt, error)
}

// TaskRuntime is the complete effect surface required by a manifest-driven
// parent. A plain CLI launcher cannot substitute for task/checkpoint ownership.
// Implementing these ports is availability, not qualification of their behavior.
type TaskRuntime interface {
	TaskCommander
	CheckpointWriter
	FinalCheckpointReader
}
