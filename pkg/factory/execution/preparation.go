package execution

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

var (
	ErrPreparation            = errors.New("invalid execution preparation observation")
	ErrPreparationNotObserved = errors.New("execution preparation not observed")
)

type PreparationAddress struct {
	Workspace string              `json:"workspace"`
	ID        string              `json:"id"`
	Native    *NativeWorkspaceRef `json:"native,omitempty"`
}

func validWorkspaceRef(n NativeWorkspaceRef) bool {
	return collaboration.ValidID(string(n.Profile)) && collaboration.ValidID(n.InstanceID) && collaboration.ValidText(n.WorktreeID, 128)
}

func (a PreparationAddress) Validate() error {
	if !collaboration.ValidText(a.Workspace, 128) || !project.ValidPreparationID(a.ID) || a.Native != nil && !validWorkspaceRef(*a.Native) {
		return ErrPreparation
	}
	return nil
}

type PreparationObserver interface {
	InspectWorkspacePreparation(context.Context, PreparationAddress) (PreparationState, error)
	RetireWorkspacePreparation(context.Context, PreparationAddress) (PreparationState, error)
}

type StandingPreparationRequest struct {
	Input  project.Prepare     `json:"input"`
	Native *NativeWorkspaceRef `json:"native,omitempty"`
}

type CandidatePreparationRequest struct {
	Input  project.FactoryCandidate
	Native *NativeWorkspaceRef
}

func (r StandingPreparationRequest) Address() PreparationAddress {
	return PreparationAddress{Workspace: r.Input.Preparation.Project, ID: r.Input.Preparation.ID, Native: r.Native}
}

func (r CandidatePreparationRequest) Address() PreparationAddress {
	return PreparationAddress{Workspace: r.Input.Preparation.Project, ID: r.Input.Preparation.ID, Native: r.Native}
}

func (r StandingPreparationRequest) Validate() error {
	if r.Input.Validate() != nil || r.Address().Validate() != nil {
		return ErrPreparation
	}
	return nil
}

func (r CandidatePreparationRequest) Validate() error {
	if r.Input.Validate() != nil || r.Address().Validate() != nil {
		return ErrPreparation
	}
	return nil
}

type PreparationLauncher interface {
	PrepareWorkspace(context.Context, StandingPreparationRequest) (PreparationState, error)
	PrepareWorkspaceCandidate(context.Context, CandidatePreparationRequest) (PreparationState, error)
}

// PreparationState preserves the appliance's retained field shape. Native
// workspaces supply their captured owner reference and no container identity.
type PreparationState struct {
	ID                 string                 `json:"id"`
	Project            string                 `json:"project"`
	Role               string                 `json:"role"`
	Phase              string                 `json:"phase"`
	Container          string                 `json:"container"`
	SourceCommit       string                 `json:"source_commit"`
	SetupDigest        string                 `json:"setup_digest"`
	Tools              []project.ResolvedTool `json:"tools,omitempty"`
	Missing            string                 `json:"missing,omitempty"`
	SetupExit          *int                   `json:"setup_exit,omitempty"`
	CheckExit          *int                   `json:"check_exit,omitempty"`
	Output             string                 `json:"output,omitempty"`
	Ready              bool                   `json:"ready"`
	Stopped            bool                   `json:"stopped"`
	Retirement         string                 `json:"retirement,omitempty"`
	Native             *NativeWorkspaceRef    `json:"native,omitempty"`
	SourceBundleSHA256 string                 `json:"source_bundle_sha256,omitempty"`
}

func (s PreparationState) ValidateFor(a PreparationAddress) error {
	if s.SourceBundleSHA256 != "" && (a.Native == nil || !validDigest(s.SourceBundleSHA256)) {
		return ErrPreparation
	}
	if a.Validate() != nil || s.Project != a.Workspace || s.ID != a.ID ||
		(s.Phase == "" && !s.Stopped || s.Phase != "" && !project.ValidPreparePhase(s.Phase)) ||
		(s.Role != "" && !project.ValidFactoryRole(s.Role)) || s.Ready != (s.Phase == project.PrepareReady) || s.Ready && s.Stopped ||
		(s.SourceCommit != "" && !project.ValidCommit(s.SourceCommit)) || (s.SetupDigest != "" && !validDigest(s.SetupDigest)) ||
		len(s.Tools) > project.MaxPrepareTools || len(s.Output) > 256<<10 || len(s.Missing) > 4096 {
		return ErrPreparation
	}
	if s.Ready && (s.Role == "" || !project.ValidCommit(s.SourceCommit) || !validDigest(s.SetupDigest)) {
		return ErrPreparation
	}
	for _, exit := range []*int{s.SetupExit, s.CheckExit} {
		if exit != nil && (*exit < 0 || *exit > 255 || s.Ready && *exit != 0) {
			return ErrPreparation
		}
	}
	if a.Native == nil {
		if s.Native != nil || s.Container != "" && !project.ValidContainerID(s.Container) || s.Ready && s.Container == "" {
			return ErrPreparation
		}
	} else if s.Native == nil || s.Container != "" || !validWorkspaceRef(*s.Native) || *s.Native != *a.Native {
		return ErrPreparation
	}
	return nil
}

func (s PreparationState) RetirementState() Retirement {
	if s.Stopped && s.Retirement == "confirmed" {
		return Retired
	}
	if s.Phase == project.PrepareRunning && !s.Stopped {
		return Running
	}
	return Uncertain
}

func (s PreparationState) MarshalJSON() ([]byte, error) {
	type fields PreparationState
	raw, err := json.Marshal(fields(s))
	if err != nil || s.Native == nil {
		return raw, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	delete(data, "container")
	return json.Marshal(data)
}

// PreparationRecord is the canonical durable preparation: approved inputs,
// captured runtime owner and observed state. Reading it does not authorize setup
// or role execution.
type PreparationRecord struct {
	Preparation project.Preparation `json:"preparation"`
	State       PreparationState    `json:"state,omitempty"`
	Native      *NativeWorkspaceRef `json:"native,omitempty"`
}

func (r PreparationRecord) Address() PreparationAddress {
	return PreparationAddress{Workspace: r.Preparation.Project, ID: r.Preparation.ID, Native: r.Native}
}

func (r PreparationRecord) MarshalJSON() ([]byte, error) {
	if r.Native != nil && reflect.DeepEqual(r.State, PreparationState{}) {
		return json.Marshal(struct {
			Preparation project.Preparation `json:"preparation"`
			Native      *NativeWorkspaceRef `json:"native"`
		}{r.Preparation, r.Native})
	}
	type fields PreparationRecord
	return json.Marshal(fields(r))
}

func (r PreparationRecord) Validate() error {
	if r.Preparation.Validate() != nil || r.Native != nil && !validWorkspaceRef(*r.Native) {
		return ErrPreparation
	}
	if reflect.DeepEqual(r.State, PreparationState{}) {
		return nil
	}
	if r.State.ValidateFor(PreparationAddress{Workspace: r.Preparation.Project, ID: r.Preparation.ID, Native: r.Native}) != nil {
		return ErrPreparation
	}
	if r.State.Role != "" && r.State.Role != r.Preparation.Role || r.State.SourceCommit != "" && r.State.SourceCommit != r.Preparation.SourceCommit || r.State.SetupDigest != "" && r.State.SetupDigest != r.Preparation.SetupDigest {
		return ErrPreparation
	}
	return nil
}
