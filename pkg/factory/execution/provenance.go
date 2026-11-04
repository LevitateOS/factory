// Package execution owns provider-neutral run evidence. Runtime adapters supply
// observations; these records do not launch processes or claim isolation.
package execution

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory/access"
)

type (
	Retirement   string
	CustodyState string
)

const (
	NotStarted       Retirement   = "not_started"
	Running          Retirement   = "running"
	Exited           Retirement   = "exited"
	Retired          Retirement   = "retired"
	Uncertain        Retirement   = "uncertain"
	NotAcquired      CustodyState = "not_acquired"
	Held             CustodyState = "held"
	Released         CustodyState = "released"
	CustodyUncertain CustodyState = "uncertain"
)

var ErrProvenance = errors.New("invalid factory execution provenance")

// Provenance is INTERNAL retained evidence with exactly one runtime variant.
// PlacementGeneration fences the dispatch; it is not the broker credential or
// appliance boundary generation. Unknown observations never become empty success.
type Provenance struct {
	RunID               string                    `json:"run_id"`
	Runtime             collaboration.RuntimeID   `json:"runtime"`
	PlacementGeneration int64                     `json:"placement_generation,string"`
	Kind                collaboration.RuntimeKind `json:"kind"`
	Retirement          Retirement                `json:"retirement"`
	Native              *NativeProvenance         `json:"native"`
	Appliance           *ApplianceProvenance      `json:"appliance"`
}

// NativeProvenance distinguishes historical trusted same-user execution from
// the isolated system worker. Isolation is observed only after a process starts;
// a selected trust mode alone never proves a worker launch or credential return.
// Native selectable-executor qualification remains the separate D3 gate.
type NativeProvenance struct {
	TrustMode  string                  `json:"trust_mode"`
	Profile    collaboration.ProfileID `json:"profile"`
	InstanceID string                  `json:"instance_id"`
	WorktreeID string                  `json:"worktree_id"`
	BrokerRun  string                  `json:"broker_run,omitempty"`
	Isolation  *NativeIsolation        `json:"isolation,omitempty"`
	Process    *NativeProcess          `json:"process"`
	Custody    NativeCustody           `json:"custody"`
}

// NativeIsolation retains distinct host identities observed after a brokered
// child launch. It is not proof that the worker was actually confined or that
// Go admitted this run. The process PID is inside that worker's private PID
// namespace, not the desktop host PID namespace.
type NativeIsolation struct {
	DesktopUID int `json:"desktop_uid"`
	WorkerUID  int `json:"worker_uid"`
}

// Local executable paths are server-side metadata, never browser instructions.
// The digest identifies the captured bytes of this invocation across updates.
type NativeProcess struct {
	InvocationID     string    `json:"invocation_id"`
	SystemInstance   string    `json:"system_instance"`
	StartIdentity    string    `json:"start_identity"`
	PID              int       `json:"pid"`
	StartedAt        time.Time `json:"started_at"`
	Executable       string    `json:"executable"`
	ExecutableSHA256 string    `json:"executable_sha256"`
	ExitCode         *int      `json:"exit_code"`
	ExitSignal       *string   `json:"exit_signal"`
}

// Released confirms retirement of THIS run's delivered credential material and
// acquisition. It cannot prove that a same-user child never copied a secret or
// that the user's global provider login was erased. Unconfirmed cleanup fences.
type NativeCustody struct {
	State         CustodyState                `json:"state"`
	AcquisitionID string                      `json:"acquisition_id"`
	Connection    access.ProviderConnectionID `json:"connection"`
	Generation    int64                       `json:"generation,string"`
}

// Appliance evidence preserves the existing host boundary and broker facts.
// Boundary can be absent for a pre-launch stop tombstone. Do not fill it from
// native host process fields or infer successful credential return from exit.
type ApplianceProvenance struct {
	Boundary           *ApplianceBoundary `json:"boundary"`
	Broker             BrokerObservation  `json:"broker"`
	CredentialReturned bool               `json:"credential_returned"`
}

type ApplianceBoundary struct {
	ApplianceInvocation
	Image string `json:"image"`
}

// ApplianceInvocation contains only the boundary facts actually returned by
// run inspection. An image is supplied separately by captured admission.
type ApplianceInvocation struct {
	Project    string `json:"project"`
	Generation int64  `json:"generation,string"`
	Container  string `json:"container"`
	Unit       string `json:"unit"`
	Invocation string `json:"invocation"`
	UID        int    `json:"uid"`
	GID        int    `json:"gid"`
	Login      string `json:"login"`
}

func (p ApplianceInvocation) ValidateFor(workspace string, generation int64) error {
	if p.Project != workspace || !collaboration.ValidText(p.Invocation, 128) ||
		p.Generation < 0 || p.UID < 0 || p.GID < 0 ||
		(p.Generation > 0 && generation > 0 && p.Generation != generation) ||
		!optionalText(p.Container, 128) || !optionalText(p.Unit, 256) || !optionalText(p.Login, 128) {
		return ErrProvenance
	}
	return nil
}

// Retain combines a later observation with already witnessed fields. Empty/zero
// boundary fields mean unknown, not permission to erase a known process fact.
func (p ApplianceInvocation) Retain(prior ApplianceInvocation) (ApplianceInvocation, error) {
	text := [][2]*string{{&p.Project, &prior.Project}, {&p.Container, &prior.Container}, {&p.Unit, &prior.Unit}, {&p.Invocation, &prior.Invocation}, {&p.Login, &prior.Login}}
	for _, pair := range text {
		if *pair[0] == "" {
			*pair[0] = *pair[1]
		}
	}
	if p.Generation == 0 {
		p.Generation = prior.Generation
	}
	if p.UID == 0 {
		p.UID = prior.UID
	}
	if p.GID == 0 {
		p.GID = prior.GID
	}
	if !p.Advances(prior) {
		return ApplianceInvocation{}, ErrRunChanged
	}
	return p, nil
}

func (p ApplianceInvocation) Advances(prior ApplianceInvocation) bool {
	for _, pair := range [][2]string{{prior.Project, p.Project}, {prior.Container, p.Container}, {prior.Unit, p.Unit}, {prior.Invocation, p.Invocation}, {prior.Login, p.Login}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	return p.Generation >= 0 && p.UID >= 0 && p.GID >= 0 && (prior.Generation == 0 || p.Generation == prior.Generation) && (prior.UID == 0 || p.UID == prior.UID) && (prior.GID == 0 || p.GID == prior.GID)
}

// BrokerObservation retains the separate credential boundary. A nil binding and
// empty lease are valid on a confirmed pre-acquisition terminal tombstone; an
// unreachable broker is represented by uncertain custody, never terminal proof.
type BrokerObservation struct {
	ExecutionID string         `json:"execution_id"`
	State       CustodyState   `json:"state"`
	Digest      string         `json:"digest"`
	LeaseID     string         `json:"lease_id"`
	Binding     *BrokerBinding `json:"binding"`
}

type BrokerBinding struct {
	Kind string `json:"kind,omitempty"`
	// Server-side retained metadata only; never a credential delivery or a
	// browser path selector. Original broker identity comparisons retain it.
	CredentialRoot string `json:"credential_root,omitempty"`
	ID             string `json:"id"`
	Generation     int64  `json:"generation,string"`
	Invocation     string `json:"invocation"`
	// Factory broker "project" is the container boundary, not a workspace.
	Boundary string `json:"project"`
	ChildID  string `json:"child_id"`
	Scope    string `json:"scope"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Login    string `json:"login"`
}

func (p Provenance) Validate() error {
	if !collaboration.ValidText(p.RunID, 128) || !collaboration.ValidID(string(p.Runtime)) || p.PlacementGeneration <= 0 ||
		(p.Retirement != NotStarted && p.Retirement != Running && p.Retirement != Exited && p.Retirement != Retired && p.Retirement != Uncertain) {
		return ErrProvenance
	}
	switch p.Kind {
	case collaboration.Native:
		if p.Native == nil || p.Appliance != nil || !validNative(*p.Native) ||
			((p.Retirement == Running || p.Retirement == Exited) && p.Native.Process == nil) {
			return ErrProvenance
		}
	case collaboration.Appliance:
		if p.Appliance == nil || p.Native != nil || !validAppliance(*p.Appliance, p.RunID) ||
			((p.Retirement == Running || p.Retirement == Exited) && p.Appliance.Boundary == nil) {
			return ErrProvenance
		}
	default:
		return ErrProvenance
	}
	return nil
}

// Settled reports recorded retirement AND custody evidence. It does not perform
// OS/broker inspection or authorize reservation release without reconciliation.
func (p Provenance) Settled() bool {
	if p.Validate() != nil || p.Retirement != Retired {
		return false
	}
	if p.Native != nil {
		return p.Native.Custody.State == Released || p.Native.Custody.State == NotAcquired
	}
	a := p.Appliance
	return a.Broker.State == Released && (a.CredentialReturned || (a.Boundary == nil && a.Broker.Binding == nil && a.Broker.LeaseID == ""))
}

func validNative(n NativeProvenance) bool {
	if !collaboration.ValidID(string(n.Profile)) ||
		!collaboration.ValidID(n.InstanceID) || !collaboration.ValidText(n.WorktreeID, 128) || !validCustody(n.Custody.State) {
		return false
	}
	switch n.TrustMode {
	case "same_user_trusted_host":
		if n.BrokerRun != "" || n.Isolation != nil {
			return false
		}
	case "isolated_system_worker":
		if !collaboration.ValidID(n.BrokerRun) {
			return false
		}
		if n.Process == nil {
			if n.Isolation != nil {
				return false
			}
		} else if n.Isolation == nil || !validNativeIsolation(*n.Isolation) {
			return false
		}
	default:
		return false
	}
	if !validNativeCustody(n.Custody) {
		return false
	}
	if n.Process == nil {
		return true
	}
	p := n.Process
	return collaboration.ValidID(p.InvocationID) && p.PID > 0 && !p.StartedAt.IsZero() &&
		collaboration.ValidText(p.SystemInstance, 128) && collaboration.ValidText(p.StartIdentity, 128) &&
		filepath.IsAbs(p.Executable) && collaboration.ValidText(p.Executable, 4096) && validDigest(p.ExecutableSHA256) &&
		(p.ExitCode == nil || *p.ExitCode >= 0 && *p.ExitCode <= 255) &&
		(p.ExitSignal == nil || collaboration.ValidText(*p.ExitSignal, 128)) &&
		(p.ExitCode == nil || p.ExitSignal == nil)
}

func validNativeIsolation(i NativeIsolation) bool {
	return i.DesktopUID > 0 && i.WorkerUID > 0 && i.WorkerUID != i.DesktopUID
}

func validNativeCustody(c NativeCustody) bool {
	if !validCustody(c.State) {
		return false
	}
	if c.State == NotAcquired {
		return c.AcquisitionID == "" && c.Connection == "" && c.Generation == 0
	}
	return collaboration.ValidText(c.AcquisitionID, 128) && collaboration.ValidText(string(c.Connection), 128) && c.Generation > 0
}

func validAppliance(a ApplianceProvenance, runID string) bool {
	b := a.Broker
	if b.ExecutionID != runID || !validCustody(b.State) || (b.Digest != "" && !validDigest(b.Digest)) ||
		!optionalText(b.LeaseID, 128) {
		return false
	}
	if b.State == Held && (b.Digest == "" || b.LeaseID == "" || b.Binding == nil) {
		return false
	}
	if b.Binding != nil {
		v := b.Binding
		if !collaboration.ValidText(v.ID, 128) || v.Generation <= 0 || !collaboration.ValidText(v.Boundary, 128) ||
			!optionalText(v.Invocation, 128) || !optionalText(v.ChildID, 128) || !optionalText(v.Scope, 256) ||
			v.UID < 0 || v.GID < 0 || !optionalText(v.Login, 128) {
			return false
		}
	}
	if a.Boundary == nil {
		return true
	}
	v := a.Boundary
	return collaboration.ValidText(v.Project, 128) && v.Generation > 0 && collaboration.ValidText(v.Image, 256) &&
		collaboration.ValidText(v.Container, 128) && collaboration.ValidText(v.Unit, 256) &&
		collaboration.ValidText(v.Invocation, 128) && v.UID > 0 && v.GID > 0 && collaboration.ValidText(v.Login, 128)
}

func validCustody(s CustodyState) bool {
	return s == NotAcquired || s == Held || s == Released || s == CustodyUncertain
}
func optionalText(s string, limit int) bool { return s == "" || collaboration.ValidText(s, limit) }
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
