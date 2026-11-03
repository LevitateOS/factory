// Fixed supervised factory-run identities and wire records. This file is pure
// validation: no I/O, no SQL, no privilege. Run/source/assignment changes
// always require a new run identity; the same ID with different content
// conflicts instead of reusing approval. Only harnesses with their own M3
// proof are selectable; the executor refuses every other harness pin.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

// FactoryHarnessMuse is the only factory harness family: Muse Code CLI
// (`muse exec` runs backed by the native "muse" provider). Each released
// version needs its own proof before the executor admits its pin.
const FactoryHarnessMuse = "muse"

const (
	// FactoryApproved means the run intent is recorded but nothing started.
	FactoryApproved = "approved"
	// FactoryRunning means the CLI is executing under the recorded identity.
	FactoryRunning = "running"
	// FactoryCompleted means the CLI exited and credentials were returned.
	FactoryCompleted = "completed"
	// FactoryFailed means launch or execution failed without uncertainty.
	FactoryFailed = "failed"
	// FactoryStopped means a stop tombstone retired or barred this identity.
	FactoryStopped = "stopped"
	// FactoryUncertain means retirement or credential return is unconfirmed;
	// the run and its connection stay fenced until reconciled.
	FactoryUncertain = "uncertain"
)

// ValidFactoryPhase reports whether phase is a known factory-run phase.
func ValidFactoryPhase(phase string) bool {
	switch phase {
	case FactoryApproved, FactoryRunning, FactoryCompleted, FactoryFailed, FactoryStopped, FactoryUncertain:
		return true
	default:
		return false
	}
}

const (
	// ExecutionDecompose plans an intent subject into candidate issue graphs.
	ExecutionDecompose = "decompose"
	// ExecutionInvestigate inspects current source and writes the plan.
	ExecutionInvestigate = "investigate"
	// ExecutionImplement executes the validated plan toward a candidate.
	ExecutionImplement = "implement"
	// ExecutionReview reports native findings on a candidate head.
	ExecutionReview = "review"
	// ExecutionCorrect addresses the current finding set on the same head.
	ExecutionCorrect = "correct"
	// ExecutionReassess reviews the complete current issue inventory after
	// one verified completion (D13). It accepts only the completion subject
	// and the coder role, inspects without implementing, and proposes one
	// coherent revision set through the reassessment report.
	ExecutionReassess = "reassess"
	// ExecutionRepair investigates a stall or a failed verification and
	// proposes the smallest coherent repair or an evidence-based wait
	// (D15/D16). It accepts only the stall or delivery_repair subject
	// and the coder role, runs single-agent with no publication, and
	// carries no publishable candidate.
	ExecutionRepair = "repair"
	// ExecutionVerify checks one integrated accepted-intent delivery
	// against its approved manifest (D16). It accepts only the delivery
	// subject and the reviewer role, runs read-only with no publication
	// or children, and carries no publishable candidate.
	ExecutionVerify = "verify"
)

// ValidExecutionPhase reports whether phase is an admitted execution phase.
// Admission always records an explicit phase; the empty phase is invalid.
// Do not infer phase from role, parent equality or whether a PR happens
// to exist.
func ValidExecutionPhase(phase string) bool {
	switch phase {
	case ExecutionDecompose, ExecutionInvestigate, ExecutionImplement, ExecutionReview, ExecutionCorrect, ExecutionReassess, ExecutionRepair, ExecutionVerify:
		return true
	default:
		return false
	}
}

// ExecutionManifestRequired reports whether phase stages an immutable
// execution manifest. Implement runs are multi-child planned execution;
// correct runs are singleton re-execution of the admitted plan with no
// child concurrency. Every other phase remains a single-agent run without
// a manifest.
func ExecutionManifestRequired(phase string) bool {
	return phase == ExecutionImplement || phase == ExecutionCorrect
}

// MaxExecutionManifest bounds one staged execution manifest: exact serialized
// plan bytes, invocation, phase/run/deadline/profile and child limit. The
// manifest counts against the daemon's 1 MiB launch envelope together with
// the 64 KiB prompt.
const MaxExecutionManifest = 64 * 1024

var factoryRunID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// ValidFactoryRunID reports whether id is a well-formed run identity.
func ValidFactoryRunID(id string) bool { return factoryRunID.MatchString(id) }

var harnessVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// ValidHarnessVersion reports whether version is an admissible harness pin.
// The executor admits only versions with their own proof; the charset keeps
// staged guest paths fixed.
func ValidHarnessVersion(version string) bool { return harnessVersion.MatchString(version) }

// ValidHarnessFamily identifies the supported supervised CLI family. The
// factory is Muse-only; a stored selection naming any other family is
// refused and must be reconfigured.
func ValidHarnessFamily(family string) bool {
	return family == FactoryHarnessMuse
}

const (
	// MaxFactoryPrompt bounds one assignment's prompt bytes. Prompts travel
	// the private daemon socket and are staged verbatim for the fixed CLI.
	MaxFactoryPrompt = 64 * 1024
	// MaxReassessPrompt is the D13 reassess-only prompt exception: a
	// reassess prompt carries the exact complete <=256 KiB inventory inline
	// plus bounded instructions. Every other phase keeps MaxFactoryPrompt,
	// and every phase result keeps MaxFactoryOutput. Prompts use existing
	// stdin/file delivery, never command-line arguments.
	MaxReassessPrompt = 320 * 1024
	// MaxDecomposePrompt is the intent decompose prompt headroom: an
	// intent-subject decompose prompt carries the complete approved
	// planning input inline. Decompose admits only intent subjects, so
	// this bound is exactly intent-scoped; every other non-reassess
	// phase keeps MaxFactoryPrompt. Finite headroom, not a
	// maximal-input promise: larger selections refuse truthfully.
	MaxDecomposePrompt = 128 * 1024
	// MaxFactoryOutput bounds retained last-message output per run.
	MaxFactoryOutput = 64 * 1024
)

// MaxPromptForPhase returns the admitted prompt bound for phase: the
// reassess/repair 320 KiB exception for reassess and repair, the 128 KiB
// intent-decompose headroom for decompose, the standard bound otherwise.
// Decompose admits only intent subjects, so the decompose bound is
// exactly intent-scoped. The launch wire binds each exception to the
// recorded phase; the matching subject binding rides assignment
// admission.
func MaxPromptForPhase(phase string) int {
	if phase == ExecutionReassess || phase == ExecutionRepair {
		return MaxReassessPrompt
	}
	if phase == ExecutionDecompose {
		return MaxDecomposePrompt
	}
	return MaxFactoryPrompt
}

// FactoryRun binds one supervised CLI execution to its Project, role,
// preparation, source and broker sponsorship. The ID is also the broker
// execution ID; the assignment digest binds the immutable prompt bytes.
// ExecutionPhase names the admitted factory phase; ManifestDigest binds the
// staged execution manifest for implement and correct runs and stays empty
// otherwise. Effort and Contributor deliver the immutable Muse policy profile;
// an empty effort preserves launches recorded before profile delivery.
type FactoryRun struct {
	Deadline                   time.Time `json:"deadline"`
	Actor                      int64     `json:"actor"`
	ID                         string    `json:"id"`
	Project                    string    `json:"project"`
	Role                       string    `json:"role"`
	Preparation                string    `json:"preparation"`
	Harness                    string    `json:"harness"`
	HarnessVers                string    `json:"harness_version"`
	Model                      string    `json:"model,omitempty"`
	Effort                     string    `json:"effort,omitempty"`
	Contributor                bool      `json:"contributor,omitempty"`
	Assignment                 string    `json:"assignment"`
	SourceCommit               string    `json:"source_commit"`
	Connection                 string    `json:"connection"`
	ExecutionPhase             string    `json:"execution_phase,omitempty"`
	ManifestDigest             string    `json:"manifest_digest,omitempty"`
	VerificationManifestDigest string    `json:"verification_manifest_digest,omitempty"`
}

func (r FactoryRun) validateRunIdentity() error {
	if !ValidFactoryRunID(r.ID) || !ValidID(r.Project) || !ValidFactoryRole(r.Role) {
		return errors.New("invalid factory run identity")
	}
	if !ValidPreparationID(r.Preparation) {
		return errors.New("invalid run preparation reference")
	}
	if !ValidHarnessFamily(r.Harness) || !ValidHarnessVersion(r.HarnessVers) {
		return errors.New("unsupported factory harness")
	}
	if r.Effort != "" && (!ValidTaskEffort(r.Effort) || r.Model == "") || r.Contributor && r.Effort == "" {
		return errors.New("invalid Muse execution profile")
	}
	// Empty keeps the CLI default for older launches; a set model pins
	// spend to the dispatch-selected policy choice.
	if r.Model != "" {
		if len(r.Model) > 128 {
			return errors.New("invalid run model selection")
		}
		for i := 0; i < len(r.Model); i++ {
			if r.Model[i] < 0x20 || r.Model[i] == 0x7f {
				return errors.New("invalid run model selection")
			}
		}
	}
	if !ValidDigest(r.Assignment) || !ValidCommit(r.SourceCommit) {
		return errors.New("invalid run assignment or source identity")
	}
	if r.Connection == "" || len(r.Connection) > 128 || r.Actor <= 0 {
		return errors.New("invalid run sponsorship")
	}
	return nil
}

func (r FactoryRun) validateRunPhase() error {
	if r.Deadline.IsZero() {
		return errors.New("run deadline is required")
	}
	if !ValidExecutionPhase(r.ExecutionPhase) {
		return errors.New("invalid run execution phase")
	}
	if r.ExecutionPhase == ExecutionReassess && r.Role != RoleCoder {
		return errors.New("reassess run requires the coder role")
	}
	if r.ExecutionPhase == ExecutionRepair && r.Role != RoleCoder {
		return errors.New("repair run requires the coder role")
	}
	if r.ExecutionPhase == ExecutionVerify && r.Role != RoleReviewer {
		return errors.New("verify run requires the reviewer role")
	}
	if ExecutionManifestRequired(r.ExecutionPhase) {
		if !ValidDigest(r.ManifestDigest) {
			if r.ExecutionPhase == ExecutionCorrect {
				return errors.New("correct run lacks its manifest digest")
			}
			return errors.New("implement run lacks its manifest digest")
		}
	} else if r.ManifestDigest != "" {
		return errors.New("non-implement run carries a manifest digest")
	}
	if r.VerificationManifestDigest != "" && (r.ExecutionPhase != ExecutionVerify || !ValidDigest(r.VerificationManifestDigest)) {
		return errors.New("non-verification run carries a verification manifest digest")
	}
	return nil
}

func (r FactoryRun) Validate() error {
	if err := r.validateRunIdentity(); err != nil {
		return err
	}
	return r.validateRunPhase()
}

// FactoryPromptDigest returns the canonical digest over prompt bytes. The
// executor requires it to equal the run's recorded assignment digest.
func FactoryPromptDigest(prompt []byte) string {
	sum := sha256.Sum256(prompt)
	return hex.EncodeToString(sum[:])
}

// FactoryLaunch is the host run request: the recorded run identity, the exact
// prompt bytes and the caller-observed harness pin. The executor admits only
// its own pinned harness bytes. ExecutionManifest carries the immutable staged
// plan for implement and correct runs and stays absent for every other phase;
// the run's manifest digest must match its exact bytes.
// FactoryMCPBinding is delivered to the host-owned relay before the model starts.
// Its secret is never prompt content or user-visible run output.
type FactoryMCPBinding struct {
	Capability string `json:"capability"`
	Secret     string `json:"secret"`
}

type FactoryLaunch struct {
	MCP                  *FactoryMCPBinding `json:"mcp,omitempty"`
	Run                  FactoryRun         `json:"run"`
	Prompt               []byte             `json:"prompt"`
	HarnessSHA256        string             `json:"harness_sha256"`
	ExecutionManifest    []byte             `json:"execution_manifest,omitempty"`
	VerificationManifest []byte             `json:"verification_manifest,omitempty"`
}

// ExecutionManifestDigest returns the canonical digest over staged manifest
// bytes. The executor verifies the supplied bytes, not a separately
// reconstructed cross-language serialization.
func ExecutionManifestDigest(manifest []byte) string {
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}

func (l FactoryLaunch) Validate() error {
	if l.MCP != nil && (!ValidFactoryRunID(l.MCP.Capability) || !ValidDigest(l.MCP.Secret) || l.MCP.Capability != l.Run.ID) {
		return errors.New("invalid bound Factory MCP configuration")
	}
	if err := l.Run.Validate(); err != nil {
		return err
	}
	if len(l.Prompt) == 0 || len(l.Prompt) > MaxPromptForPhase(l.Run.ExecutionPhase) {
		return errors.New("invalid run prompt size")
	}
	if FactoryPromptDigest(l.Prompt) != l.Run.Assignment {
		return errors.New("prompt bytes do not match their digest")
	}
	if !ValidDigest(l.HarnessSHA256) {
		return errors.New("invalid harness pin")
	}
	if len(l.ExecutionManifest) > MaxExecutionManifest {
		return errors.New("execution manifest exceeds its bound")
	}
	if ExecutionManifestRequired(l.Run.ExecutionPhase) {
		if len(l.ExecutionManifest) == 0 {
			if l.Run.ExecutionPhase == ExecutionCorrect {
				return errors.New("correct run lacks its execution manifest")
			}
			return errors.New("implement run lacks its execution manifest")
		}
		if ExecutionManifestDigest(l.ExecutionManifest) != l.Run.ManifestDigest {
			return errors.New("execution manifest does not match its digest")
		}
	} else if len(l.ExecutionManifest) != 0 {
		return errors.New("non-implement run stages an execution manifest")
	}
	if len(l.VerificationManifest) > 0 || l.Run.VerificationManifestDigest != "" {
		manifest, err := ParseVerificationManifest(l.VerificationManifest)
		if err != nil || manifest.ValidateForRun(l.Run) != nil || ExecutionManifestDigest(l.VerificationManifest) != l.Run.VerificationManifestDigest {
			return errors.New("verification manifest does not match its admitted run")
		}
	}
	return nil
}

// FactoryState is the observed run outcome for one identity.
type FactoryState struct {
	ExitCode           *int                            `json:"exit_code,omitempty"`
	Generation         int64                           `json:"generation"`
	UID                int                             `json:"uid"`
	GID                int                             `json:"gid"`
	CredentialReturned bool                            `json:"credential_returned"`
	Live               bool                            `json:"live"`
	Delivered          bool                            `json:"delivered"`
	ID                 string                          `json:"id"`
	Project            string                          `json:"project"`
	Role               string                          `json:"role"`
	Phase              string                          `json:"phase"`
	Container          string                          `json:"container"`
	Unit               string                          `json:"unit"`
	Invocation         string                          `json:"invocation"`
	Login              string                          `json:"login"`
	LeaseID            string                          `json:"lease_id,omitempty"`
	Output             string                          `json:"output,omitempty"`
	Retirement         string                          `json:"retirement,omitempty"`
	Reason             string                          `json:"reason,omitempty"`
	VerificationChecks *FactoryVerificationChecksState `json:"verification_checks,omitempty"`
}

// FactoryInspect addresses a run before or after completion.
type FactoryInspect struct {
	Project string `json:"project"`
	ID      string `json:"id"`
}

func (p FactoryInspect) Validate() error {
	if !ValidID(p.Project) || !ValidFactoryRunID(p.ID) {
		return errors.New("invalid factory run address")
	}
	return nil
}

// FactoryStop retires one run identity, even before it is ever observed.
type FactoryStop struct {
	Project string `json:"project"`
	ID      string `json:"id"`
}

func (p FactoryStop) Validate() error {
	if !ValidID(p.Project) || !ValidFactoryRunID(p.ID) {
		return errors.New("invalid factory run address")
	}
	return nil
}
