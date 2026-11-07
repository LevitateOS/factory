package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// Task child-concurrency bounds for one admitted implement run. The attempt
// policy (factory.AttemptLimits) mirrors this range; the manifest carries
// the admitted snapshot so the executor enforces the lesser of policy and
// qualified native capacity without re-reading policy. The parent run is
// separate; nested child spawning is refused.
const (
	MinTaskChildren     = 1
	MaxTaskChildren     = 4
	DefaultTaskChildren = 3
)

// Reasoning efforts admitted in a staged execution profile. Vocabulary here
// mirrors the factory policy domain; harness support for an effort rides
// the proved harness pin the executor admits, never this list alone.
const (
	TaskEffortLow    = "low"
	TaskEffortMedium = "medium"
	TaskEffortHigh   = "high"
	TaskEffortXHigh  = "xhigh"
)

// ValidTaskEffort reports whether effort is admitted profile vocabulary.
func ValidTaskEffort(effort string) bool {
	switch effort {
	case TaskEffortLow, TaskEffortMedium, TaskEffortHigh, TaskEffortXHigh:
		return true
	default:
		return false
	}
}

// ExecutionManifest is the immutable task-control envelope staged with an
// implement or correct launch (D05). It binds the exact serialized plan bytes and
// their digest to the admitted invocation, phase, run, deadline, execution
// profile snapshot and child limit. The executor verifies the staged bytes
// against the run's manifest digest, not a separately reconstructed
// cross-language serialization. FW14 stages this envelope at dispatch;
// planning phases stage no manifest at all.
type ExecutionManifest struct {
	Plan        []byte    `json:"plan"`
	PlanDigest  string    `json:"plan_digest"`
	Invocation  string    `json:"invocation"`
	Phase       string    `json:"phase"`
	Run         string    `json:"run"`
	Deadline    time.Time `json:"deadline"`
	Effort      string    `json:"effort,omitempty"`
	Contributor bool      `json:"contributor,omitempty"`
	MaxChildren int       `json:"max_children"`
}

// MarshalExecutionManifest encodes one manifest in its canonical form. Go
// struct encoding is field-ordered and deterministic, so both sides digest
// the same bytes.
func MarshalExecutionManifest(manifest ExecutionManifest) ([]byte, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxExecutionManifest {
		return nil, errors.New("execution manifest exceeds its bound")
	}
	return data, nil
}

// ParseExecutionManifest decodes one staged manifest with unknown fields
// refused. Length is checked before parsing so an oversized envelope never
// allocates its plan.
func ParseExecutionManifest(data []byte) (ExecutionManifest, error) {
	var manifest ExecutionManifest
	if len(data) == 0 || len(data) > MaxExecutionManifest {
		return manifest, errors.New("execution manifest exceeds its bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return ExecutionManifest{}, errors.New("invalid execution manifest")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ExecutionManifest{}, errors.New("invalid execution manifest")
	}
	if err := manifest.Validate(); err != nil {
		return ExecutionManifest{}, err
	}
	return manifest, nil
}

// Validate binds every envelope field. Only implement and correct runs
// carry a manifest. MaxChildren 0 selects one agent executing tasks sequentially;
// historical implement manifests retain their admitted child concurrency 1-4.
// Corrections remain a singleton task with no children. The phase, run and digest
// bindings below are checked against the launching run by
// ValidateManifestForRun.
func (m ExecutionManifest) Validate() error {
	if len(m.Plan) == 0 || len(m.Plan) > MaxExecutionManifest {
		return errors.New("execution manifest lacks its plan bytes")
	}
	if PlanDigest(m.Plan) != m.PlanDigest {
		return errors.New("execution manifest plan does not match its digest")
	}
	switch m.Phase {
	case ExecutionImplement, ExecutionCorrect:
	default:
		return errors.New("execution manifest phase is not implement")
	}
	if !ValidFactoryRunID(m.Run) {
		return errors.New("execution manifest run is invalid")
	}
	if !ValidInvocation(m.Invocation) {
		return errors.New("execution manifest invocation is invalid")
	}
	if m.Deadline.IsZero() {
		return errors.New("execution manifest deadline is required")
	}
	if !ValidTaskEffort(m.Effort) {
		return errors.New("invalid execution effort")
	}
	if m.Phase == ExecutionCorrect {
		if m.MaxChildren != 0 {
			return errors.New("execution manifest child limit is outside its range")
		}
	} else if m.MaxChildren < 0 || m.MaxChildren > MaxTaskChildren {
		return errors.New("execution manifest child limit is outside its range")
	}
	return nil
}

// ValidateManifestForRun binds a staged envelope to its launching run: the
// digest matches the staged bytes, and phase, run, deadline and child
// limit match the admitted record. Contributor mode is valid only for the
// Muse harness; children inherit the exact parent profile and account.
func (m ExecutionManifest) ValidateManifestForRun(run FactoryRun) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.Phase != run.ExecutionPhase || m.Run != run.ID {
		return errors.New("execution manifest does not match its run")
	}
	if !m.Deadline.Equal(run.Deadline) {
		return errors.New("execution manifest deadline does not match its run")
	}
	if m.Contributor && run.Harness != FactoryHarnessMuse {
		return errors.New("contributor mode is valid only for Muse")
	}
	if run.Effort != "" && (m.Effort != run.Effort || m.Contributor != run.Contributor) {
		return errors.New("execution manifest profile does not match its run")
	}
	return nil
}

// PlanDigest returns the canonical digest over exact serialized plan bytes.
func PlanDigest(plan []byte) string {
	sum := sha256.Sum256(plan)
	return hex.EncodeToString(sum[:])
}

// ValidInvocation admits the parent's observed native invocation
// identity. It mirrors the run sponsorship rules: present, bounded and
// free of control bytes. The systemd invocation shape is bound by the
// executor's observation, not redefined here.
func ValidInvocation(invocation string) bool {
	if invocation == "" || len(invocation) > 128 {
		return false
	}
	for i := 0; i < len(invocation); i++ {
		if invocation[i] < 0x20 || invocation[i] == 0x7f {
			return false
		}
	}
	return true
}
