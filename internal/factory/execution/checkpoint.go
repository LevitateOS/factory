package execution

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/project"
)

var ErrCheckpoint = errors.New("invalid execution checkpoint binding")

type CheckpointWriter interface {
	CheckpointTask(context.Context, CheckpointRequest) (CheckpointReceipt, error)
	CheckpointFinal(context.Context, CheckpointRequest) (CheckpointReceipt, error)
}

// FinalCheckpointReader reads an already retained receipt. It never starts a
// check or revives a retired process while recovering a lost reply.
type FinalCheckpointReader interface {
	ReadFinalCheckpoint(context.Context, CheckpointRequest) (CheckpointReceipt, error)
}

// NativeCheckpointAnchor names captured process/worktree ownership. None of its
// fields select a caller-supplied path, container, UID or current tool version.
type NativeCheckpointAnchor struct {
	Profile          collaboration.ProfileID `json:"profile"`
	InstanceID       string                  `json:"instance_id"`
	WorktreeID       string                  `json:"worktree_id"`
	SystemInstance   string                  `json:"system_instance"`
	StartIdentity    string                  `json:"start_identity"`
	PID              int                     `json:"pid"`
	ExecutableSHA256 string                  `json:"executable_sha256"`
}

// Appliance fields preserve the original retained encoding. Native selects a
// separate anchor and omits Container; no alternative record decoder is used.
type CheckpointRequest struct {
	Project        string                  `json:"project"`
	ID             string                  `json:"id"`
	Container      string                  `json:"container,omitempty"`
	Invocation     string                  `json:"invocation"`
	ManifestDigest string                  `json:"manifest_digest"`
	TaskKey        string                  `json:"task_key,omitempty"`
	OutputDigest   string                  `json:"output_digest,omitempty"`
	ExpectedHead   string                  `json:"expected_head"`
	Native         *NativeCheckpointAnchor `json:"native,omitempty"`
}

func NativeCheckpoint(provenance NativeProvenance) (*NativeCheckpointAnchor, error) {
	if !validNative(provenance) || provenance.Process == nil {
		return nil, ErrCheckpoint
	}
	p := provenance.Process
	return &NativeCheckpointAnchor{Profile: provenance.Profile, InstanceID: provenance.InstanceID, WorktreeID: provenance.WorktreeID, SystemInstance: p.SystemInstance, StartIdentity: p.StartIdentity, PID: p.PID, ExecutableSHA256: p.ExecutableSHA256}, nil
}

func (r CheckpointRequest) RuntimeKind() collaboration.RuntimeKind {
	if r.Native != nil {
		return collaboration.Native
	}
	return collaboration.Appliance
}

func (r CheckpointRequest) Equal(other CheckpointRequest) bool {
	a, b := r, other
	a.Native, b.Native = nil, nil
	return a == b && (r.Native == nil && other.Native == nil || r.Native != nil && other.Native != nil && *r.Native == *other.Native)
}

func (r CheckpointRequest) MatchesNative(provenance NativeProvenance) bool {
	anchor, err := NativeCheckpoint(provenance)
	return err == nil && r.Native != nil && *r.Native == *anchor && r.Invocation == provenance.Process.InvocationID
}

func (r CheckpointRequest) Validate(final bool) error {
	if (RunAddress{Run: r.ID, Workspace: r.Project}).Validate() != nil || !project.ValidFactoryRunID(r.ID) || !project.ValidInvocation(r.Invocation) || !validDigest(r.ManifestDigest) || !project.ValidCommit(r.ExpectedHead) {
		return ErrCheckpoint
	}
	if r.Native == nil {
		if !project.ValidID(r.Project) || !validDigest(r.Container) {
			return ErrCheckpoint
		}
	} else {
		n := r.Native
		if r.Container != "" || !collaboration.ValidID(string(n.Profile)) || !collaboration.ValidID(n.InstanceID) || !collaboration.ValidText(n.WorktreeID, 128) || !collaboration.ValidText(n.SystemInstance, 128) || !collaboration.ValidText(n.StartIdentity, 128) || n.PID <= 0 || !validDigest(n.ExecutableSHA256) {
			return ErrCheckpoint
		}
	}
	if final {
		if r.TaskKey != "" || r.OutputDigest != "" {
			return ErrCheckpoint
		}
	} else if !project.ValidTaskKey(r.TaskKey) || !validDigest(r.OutputDigest) {
		return ErrCheckpoint
	}
	return nil
}

type CheckpointReceipt struct {
	Request        CheckpointRequest               `json:"request"`
	FinalCandidate bool                            `json:"final_candidate"`
	Status         string                          `json:"status"`
	Candidate      string                          `json:"candidate,omitempty"`
	Tree           string                          `json:"tree,omitempty"`
	ReceiptDigest  string                          `json:"receipt_digest,omitempty"`
	Checks         []project.CheckpointCheckResult `json:"checks,omitempty"`
	Reason         string                          `json:"reason,omitempty"`
}

func (s CheckpointReceipt) ValidateFor(request CheckpointRequest, final bool) error {
	if request.Validate(final) != nil || !s.Request.Equal(request) || s.FinalCandidate != final || len(s.Reason) > 256 || len(s.Checks) > 16 {
		return ErrCheckpoint
	}
	switch s.Status {
	case "pending", "failed", "busy":
	case "committed":
		if !project.ValidCommit(s.Candidate) || !project.ValidCommit(s.Tree) || !validDigest(s.ReceiptDigest) || final && s.Candidate != request.ExpectedHead {
			return ErrCheckpoint
		}
	default:
		return ErrCheckpoint
	}
	if s.Candidate != "" && !project.ValidCommit(s.Candidate) || s.Tree != "" && !project.ValidCommit(s.Tree) || s.ReceiptDigest != "" && !validDigest(s.ReceiptDigest) {
		return ErrCheckpoint
	}
	seen := map[string]bool{}
	for _, check := range s.Checks {
		if !project.ValidTaskKey(check.ID) || seen[check.ID] || !validDigest(check.LogDigest) {
			return ErrCheckpoint
		}
		seen[check.ID] = true
	}
	return nil
}
