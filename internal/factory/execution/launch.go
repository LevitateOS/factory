package execution

import (
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/project"
)

var ErrLaunch = errors.New("execution launch lacks its captured runtime binding")

type ApplianceLaunch struct {
	Profile project.Profile `json:"profile"`
	Tool    ApplianceTool   `json:"tool"`
}

// NativeLaunch captures placement and executable identity for a new launch.
// An unplaced NativeRecord cannot synthesize these fields or gain authority by
// being observed. The admitting store/gateway owns the placement exclusion gate.
type NativeLaunch struct {
	Workspace           NativeWorkspaceRef      `json:"workspace"`
	Tool                NativeTool              `json:"tool"`
	Runtime             collaboration.RuntimeID `json:"runtime"`
	PlacementGeneration int64                   `json:"placement_generation,string"`
}

func (n NativeLaunch) Validate() error {
	if !validWorkspaceRef(n.Workspace) || n.Tool.Validate() != nil || !collaboration.ValidID(string(n.Runtime)) || n.PlacementGeneration <= 0 {
		return ErrLaunch
	}
	return nil
}

func (n NativeLaunch) MatchesRecord(record NativeRecord) bool {
	p := record.Provenance
	return n.Validate() == nil && record.Validate() == nil && n.Tool == record.Tool && n.Workspace.Profile == p.Profile && n.Workspace.InstanceID == p.InstanceID && n.Workspace.WorktreeID == p.WorktreeID
}

// LaunchRequest reuses the semantic prompt/manifest contract. Runtime-specific
// captured inputs stay in exactly one variant; no OS identity is a default.
type LaunchRequest struct {
	Input     project.FactoryLaunch     `json:"input"`
	Kind      collaboration.RuntimeKind `json:"kind"`
	Appliance *ApplianceLaunch          `json:"appliance,omitempty"`
	Native    *NativeLaunch             `json:"native,omitempty"`
}

func (r LaunchRequest) Address() RunAddress {
	return RunAddress{Run: r.Input.Run.ID, Workspace: r.Input.Run.Project}
}

func (r LaunchRequest) Validate() error {
	if r.Input.Validate() != nil || r.Address().Validate() != nil {
		return ErrLaunch
	}
	var family, version, digest string
	switch r.Kind {
	case collaboration.Appliance:
		if r.Appliance == nil || r.Native != nil || r.Appliance.Profile.Validate() != nil || r.Appliance.Tool.Validate() != nil {
			return ErrLaunch
		}
		family, version, digest = r.Appliance.Tool.Harness, r.Appliance.Tool.Version, r.Appliance.Tool.SHA256
	case collaboration.Native:
		if r.Native == nil || r.Appliance != nil || r.Native.Validate() != nil {
			return ErrLaunch
		}
		family, version, digest = r.Native.Tool.Harness, r.Native.Tool.Version, r.Native.Tool.SHA256
	default:
		return ErrLaunch
	}
	if r.Input.Run.Harness != family || r.Input.Run.HarnessVers != version || r.Input.HarnessSHA256 != digest {
		return ErrLaunch
	}
	return nil
}

func (r LaunchRequest) MatchesObservation(o RunObservation) bool {
	if r.Validate() != nil || o.ValidateFor(r.Address()) != nil || o.Kind != r.Kind || o.Role != r.Input.Run.Role && (o.Role != "" || o.Live) {
		return false
	}
	if r.Native != nil {
		n := o.Native
		if n == nil || n.Profile != r.Native.Workspace.Profile || n.InstanceID != r.Native.Workspace.InstanceID || n.WorktreeID != r.Native.Workspace.WorktreeID {
			return false
		}
		if p := n.Process; p != nil && (p.Executable != r.Native.Tool.Executable || p.ExecutableSHA256 != r.Native.Tool.SHA256) {
			return false
		}
	}
	return true
}
