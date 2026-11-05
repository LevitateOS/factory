package execution

import (
	"path/filepath"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/project"
)

// NativeTool is captured executable identity, not an appliance image or a
// mutable current-tool selector. Updates apply only to future admissions.
type NativeTool struct {
	Harness    string `json:"harness"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Executable string `json:"executable"`
}

func (t NativeTool) Validate() error {
	if !project.ValidHarnessFamily(t.Harness) || !project.ValidHarnessVersion(t.Version) || !validDigest(t.SHA256) || !filepath.IsAbs(t.Executable) || !collaboration.ValidText(t.Executable, 4096) {
		return ErrProvenance
	}
	return nil
}

// NativeRecord is retained run evidence before repository placement is bound.
// It supplies no invented runtime ID/generation and grants no dispatch authority.
// Sealed means this identity cannot acquire again; process retirement and
// retirement of this run's credential material remain separate facts.
type NativeRecord struct {
	// Exact Go-produced JSON, retained as a string so JSONB does not reorder
	// its bytes. Absence on history remains unknown, never synthesized.
	Intent       string           `json:"intent,omitempty"`
	Tool         NativeTool       `json:"tool"`
	BundleSHA256 string           `json:"bundle_sha256,omitempty"`
	PromptSHA256 string           `json:"prompt_sha256,omitempty"`
	Provenance   NativeProvenance `json:"provenance"`
	Retirement   Retirement       `json:"retirement"`
	Sealed       bool             `json:"sealed"`
}

func (r NativeRecord) Validate() error {
	if r.Tool.Validate() != nil || !validNative(r.Provenance) ||
		(r.Retirement != NotStarted && r.Retirement != Running && r.Retirement != Exited && r.Retirement != Retired && r.Retirement != Uncertain) {
		return ErrProvenance
	}
	if r.Provenance.TrustMode == "isolated_system_worker" {
		if !validDigest(r.BundleSHA256) || !validDigest(r.PromptSHA256) {
			return ErrProvenance
		}
	} else if r.BundleSHA256 != "" || r.PromptSHA256 != "" {
		return ErrProvenance
	}
	p := r.Provenance.Process
	if (r.Retirement == Running || r.Retirement == Exited) && p == nil || r.Retirement == NotStarted && p != nil {
		return ErrProvenance
	}
	if p != nil && (p.Executable != r.Tool.Executable || p.ExecutableSHA256 != r.Tool.SHA256) {
		return ErrProvenance
	}
	if r.Intent != "" {
		intent, err := ParseNativeIntent(r.Intent)
		if err != nil || !intent.Native.matchesRecord(r) ||
			r.Provenance.Custody.Connection != "" && intent.Connection != string(r.Provenance.Custody.Connection) {
			return ErrNativeIntent
		}
	}
	return nil
}

func (r NativeRecord) Settled() bool {
	return r.Validate() == nil && r.Retirement == Retired && r.Sealed &&
		(r.Provenance.Custody.State == Released || r.Provenance.Custody.State == NotAcquired)
}

// Advances prevents observations from replacing captured tool/process/custody
// identities. Unknown cleanup can resolve, but confirmed exit, release, seal and
// retirement cannot be erased by a stale observer or a recycled PID.
func (r NativeRecord) Advances(prior NativeRecord) bool {
	if r.Validate() != nil || prior.Validate() != nil || r.Intent != prior.Intent || r.Tool != prior.Tool || r.BundleSHA256 != prior.BundleSHA256 || r.PromptSHA256 != prior.PromptSHA256 ||
		r.Provenance.Profile != prior.Provenance.Profile || r.Provenance.InstanceID != prior.Provenance.InstanceID ||
		r.Provenance.WorktreeID != prior.Provenance.WorktreeID || r.Provenance.TrustMode != prior.Provenance.TrustMode || r.Provenance.BrokerRun != prior.Provenance.BrokerRun ||
		(prior.Provenance.Isolation != nil && !reflect.DeepEqual(r.Provenance.Isolation, prior.Provenance.Isolation)) ||
		(prior.Sealed && !r.Sealed) || (prior.Retirement == Retired && r.Retirement != Retired) ||
		(prior.Retirement == Exited && (r.Retirement == Running || r.Retirement == NotStarted)) {
		return false
	}
	old, next := prior.Provenance.Process, r.Provenance.Process
	if old != nil {
		if next == nil {
			return false
		}
		a, b := *old, *next
		a.ExitCode, a.ExitSignal, b.ExitCode, b.ExitSignal = nil, nil, nil, nil
		a.StartedAt, b.StartedAt = time.Time{}, time.Time{}
		if a != b || !old.StartedAt.Equal(next.StartedAt) || old.ExitCode != nil && !reflect.DeepEqual(old.ExitCode, next.ExitCode) || old.ExitSignal != nil && !reflect.DeepEqual(old.ExitSignal, next.ExitSignal) {
			return false
		}
	} else if prior.Retirement == Retired && next != nil {
		return false
	}
	c, n := prior.Provenance.Custody, r.Provenance.Custody
	if c.AcquisitionID != "" && (c.AcquisitionID != n.AcquisitionID || c.Connection != n.Connection || c.Generation != n.Generation) ||
		(c.State == Released && n.State != Released) || prior.Settled() && !reflect.DeepEqual(c, n) {
		return false
	}
	return true
}
