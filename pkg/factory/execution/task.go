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
		// Retained process facts deliberately do not assert live state. Both
		// the worker admission and the native task owner query current liveness.
		if n == nil || r.Appliance != nil || n.Validate() != nil || n.Sealed || (n.Retirement != Running && n.Retirement != Uncertain) || n.Provenance.Process == nil {
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

// SameTaskBinding permits only the two live observation states for one exact
// native binding. Inspection records Uncertain before the coordinator adopts
// Running; neither observation replaces the process or its captured authority.
func (r NativeRecord) SameTaskBinding(other NativeRecord) bool {
	if r.Validate() != nil || other.Validate() != nil || r.Sealed || other.Sealed ||
		(r.Retirement != Running && r.Retirement != Uncertain) || (other.Retirement != Running && other.Retirement != Uncertain) ||
		r.Provenance.Process == nil || other.Provenance.Process == nil ||
		r.Provenance.Process.StoppedAt != nil || other.Provenance.Process.StoppedAt != nil ||
		r.Provenance.Process.ExitCode != nil || r.Provenance.Process.ExitSignal != nil ||
		other.Provenance.Process.ExitCode != nil || other.Provenance.Process.ExitSignal != nil {
		return false
	}
	r.Retirement, other.Retirement = Running, Running
	return reflect.DeepEqual(r, other)
}

func (r TaskRequest) Equal(other TaskRequest) bool {
	if r.Kind != collaboration.Native || other.Kind != collaboration.Native {
		return reflect.DeepEqual(r, other)
	}
	if r.Native == nil || other.Native == nil || !r.Native.SameTaskBinding(*other.Native) {
		return false
	}
	r.Native, other.Native = nil, nil
	return reflect.DeepEqual(r, other)
}

// TaskReceipt preserves the task owner's result, including its replay receipt.
// It is not a second workflow or an assertion that the task has completed.
type TaskReceipt struct {
	Request TaskRequest
	Result  map[string]any
}

func (r TaskReceipt) ValidateFor(request TaskRequest) error {
	if request.Validate() != nil || !reflect.DeepEqual(r.Request, request) || r.Result == nil || r.Result["task_id"] != request.Input.Arguments.TaskID {
		return ErrTask
	}
	return nil
}

type TaskCommander interface {
	CommandTask(context.Context, TaskRequest) (TaskReceipt, error)
}

// TaskRuntime is the complete effect surface required by a manifest-driven
// agent. A plain CLI launcher cannot substitute for task/checkpoint ownership.
// Implementing these ports is availability, not qualification of their behavior.
type TaskRuntime interface {
	TaskCommander
	CheckpointWriter
	FinalCheckpointReader
}

// Sources may share the run owner while supplying different native effects.
// This advertises composition support, never gate qualification.
type NativeTaskSources interface {
	SupportsTaskSource(authentication string) bool
}
