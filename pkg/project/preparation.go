// Fixed factory preparation identities, layout and wire records. This file is
// pure validation: no I/O, no SQL, no privilege. The JSON shapes below are the
// Unix-socket wire contract with the privileged host daemon; field names are
// frozen. Preparation/source changes always require a new preparation identity;
// the same ID with different content conflicts instead of reusing approval.
package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/levitateos/factory/pkg/strictjson"
	"maps"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// RoleCoder and RoleReviewer are the only factory role logins. They are
	// persistent non-login accounts, not memberships or provider identities.
	RoleCoder    = "soda-coder"
	RoleReviewer = "soda-reviewer"
)

// ValidFactoryRole reports whether role is one of the two fixed factory roles.
func ValidFactoryRole(role string) bool {
	return role == RoleCoder || role == RoleReviewer
}

const (
	// FactoryDir is the helper-owned protected state root inside the Project.
	// Roles may read snapshots and receipts; only the helper writes them.
	FactoryDir             = "/var/lib/soda/factory"
	FactoryPreparationsDir = FactoryDir + "/preparations"
	FactoryCredentialsDir  = FactoryDir + "/credentials"
	FactoryHoldFile        = FactoryDir + "/maintenance-hold"
	// FactoryHelper is the fixed bounded root helper. It accepts no
	// caller-supplied command, package recipe, image or path.
	FactoryHelper = "/usr/libexec/soda/project-factory-roles"
	// FactorySetupEntry and FactoryCheckEntry are the only executable approved
	// entry points, run from the protected snapshot with a fixed interpreter.
	FactorySetupEntry = "setup.sh"
	FactoryCheckEntry = "check.sh"
)

const (
	// MaxApprovedFiles bounds the effective approved setup inputs carried in
	// one request. Content travels the private daemon socket, never a path.
	MaxApprovedFiles    = 8
	MaxApprovedFileSize = 32 * 1024
	MaxApprovedTotal    = 128 * 1024
	// Source acquisition and preparation share this bounded repository pack.
	// This is independent of the expanded checkout and diagnostic limits.
	MaxSourceBundle = 64 * 1024 * 1024
	MaxSourceWire   = (MaxSourceBundle+2)/3*4 + 1024*1024
	// MaxPrepareTools bounds the required tool names per preparation.
	MaxPrepareTools = 8
)

const (
	// PrepareApproved means approved inputs are recorded but setup is not
	// started; repeating the identical request resumes this identity.
	PrepareApproved = "approved"
	// PrepareWaiting names a missing shared prerequisite; nothing started.
	PrepareWaiting = "waiting"
	// PrepareRunning means setup/checks are executing under the recorded identity.
	PrepareRunning = "running"
	// PrepareReady means role setup and checks passed for the recorded inputs.
	PrepareReady = "ready"
	// PrepareFailed means setup, checks or verification failed; see output.
	PrepareFailed = "failed"
	// PrepareStopped means a stop tombstone retired or barred this identity.
	PrepareStopped = "stopped"
	// PrepareInterrupted means the setup supervisor died without completing;
	// stop this identity and prepare again with a new one.
	PrepareInterrupted = "interrupted"
)

// ValidPreparePhase reports whether phase is a known preparation phase.
func ValidPreparePhase(phase string) bool {
	switch phase {
	case PrepareApproved, PrepareWaiting, PrepareRunning, PrepareReady, PrepareFailed, PrepareStopped, PrepareInterrupted:
		return true
	default:
		return false
	}
}

var (
	// ErrPreparationNotFound reports an exact native preparation identity that
	// has not been recorded. A caller may retry its immutable admitted request.
	ErrPreparationNotFound = errors.New("native preparation not found")

	preparationID = regexp.MustCompile(`^f[0-9a-f]{24}$`)
	decisionID    = regexp.MustCompile(`^d[0-9a-f]{24}$`)
	sha256Hex     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	gitCommit     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	approvedName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	toolName      = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]{0,31}$`)
)

// ValidPreparationID reports whether id is a well-formed preparation identity.
func ValidPreparationID(id string) bool { return preparationID.MatchString(id) }

// ValidDecisionID reports whether id is a well-formed approval decision identity.
func ValidDecisionID(id string) bool { return decisionID.MatchString(id) }

// ValidDigest reports whether d is a lowercase sha256 hex digest.
func ValidDigest(d string) bool { return sha256Hex.MatchString(d) }

// ValidCommit reports whether c is a full git commit identity.
func ValidCommit(c string) bool { return gitCommit.MatchString(c) }

// ValidApprovedName reports whether name is an admissible approved file name.
// Names are bare file names; directories and traversal are never accepted.
func ValidApprovedName(name string) bool {
	return approvedName.MatchString(name) && !strings.Contains(name, "..")
}

// ValidToolName reports whether name is an admissible required tool name.
func ValidToolName(name string) bool { return toolName.MatchString(name) }

// RequirementAcceptance is the code-write maintainer's exact-inputs acceptance
// reference. It specifies the desired environment, never root execution.
type RequirementAcceptance struct {
	ID           string            `json:"id"`
	Revision     int64             `json:"revision"`
	Approver     int64             `json:"approver"`
	SourceCommit string            `json:"source_commit"`
	Digest       string            `json:"digest"`
	SourceInputs map[string]string `json:"source_inputs,omitempty"`
}

func (a RequirementAcceptance) Validate() error {
	if !ValidDecisionID(a.ID) || a.Revision < 0 || a.Approver <= 0 || !ValidCommit(a.SourceCommit) || !ValidDigest(a.Digest) {
		return errors.New("invalid requirement acceptance reference")
	}
	return ValidateSourceInputs(a.SourceInputs)
}

// Equal includes the exact human-selected effective repository inputs.
func (a RequirementAcceptance) Equal(b RequirementAcceptance) bool {
	return a.ID == b.ID && a.Revision == b.Revision && a.Approver == b.Approver && a.SourceCommit == b.SourceCommit && a.Digest == b.Digest && maps.Equal(a.SourceInputs, b.SourceInputs)
}

const MaxSourceInputs = 64

// ValidateSourceInputs bounds the complete selection declared by a maintainer.
// Absence permits the exact approved source only; it cannot authorize refresh.
func ValidateSourceInputs(inputs map[string]string) error {
	if len(inputs) > MaxSourceInputs {
		return errors.New("approved source inputs exceed bounds")
	}
	for name, digest := range inputs {
		if !validFactoryContextPath(name) || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") || !ValidDigest(digest) {
			return errors.New("invalid approved source input")
		}
		for _, component := range strings.Split(name, "/") {
			if component == ".git" || component == ".soda-home" {
				return errors.New("private path is not an approved source input")
			}
		}
	}
	return nil
}

// AdminApproval is the separate native Project administrator approval of the
// reviewed privileged effects. Either reference alone authorizes nothing.
type AdminApproval struct {
	ID            string `json:"id"`
	Revision      int64  `json:"revision"`
	Approver      int64  `json:"approver"`
	EffectsDigest string `json:"effects_digest"`
}

func (a AdminApproval) Validate() error {
	if !ValidDecisionID(a.ID) || a.Revision < 0 || a.Approver <= 0 || !ValidDigest(a.EffectsDigest) {
		return errors.New("invalid privileged-effect approval reference")
	}
	return nil
}

// LifecycleGrant is the current owner's standing permission to create and
// start the Project environment automatically. It creates no membership.
type LifecycleGrant struct {
	Project  string   `json:"project"`
	Revision int64    `json:"revision"`
	Owner    int64    `json:"owner"`
	Profile  *Profile `json:"profile"`
	Active   bool     `json:"active"`
}

func (g LifecycleGrant) Validate() error {
	if !ValidID(g.Project) || g.Revision < 0 || g.Owner <= 0 || g.Profile == nil || g.Profile.Validate() != nil {
		return errors.New("invalid lifecycle grant")
	}
	return nil
}

// Preparation binds one Project, role and exact source to the two separate
// approvals. All execution paths derive from this identity; the request
// carries no caller-supplied checkout path, UID, image, mount or command.
type Preparation struct {
	ID           string                `json:"id"`
	Project      string                `json:"project"`
	Role         string                `json:"role"`
	Revision     int64                 `json:"revision"`
	Requirements RequirementAcceptance `json:"requirements"`
	Approval     AdminApproval         `json:"approval"`
	SourceCommit string                `json:"source_commit"`
	SetupDigest  string                `json:"setup_digest"`
	Tools        []string              `json:"tools"`
	Credential   string                `json:"credential,omitempty"`
}

func (p Preparation) Validate() error {
	if !ValidPreparationID(p.ID) || !ValidID(p.Project) || !ValidFactoryRole(p.Role) || p.Revision < 0 {
		return errors.New("invalid preparation identity")
	}
	if err := p.Requirements.Validate(); err != nil {
		return err
	}
	if err := p.Approval.Validate(); err != nil {
		return err
	}
	if !ValidCommit(p.SourceCommit) || !ValidDigest(p.SetupDigest) {
		return errors.New("invalid preparation source identity")
	}
	if len(p.Tools) > MaxPrepareTools {
		return errors.New("too many required tools")
	}
	for _, name := range p.Tools {
		if !ValidToolName(name) {
			return errors.New("invalid required tool name")
		}
	}
	if p.Credential != "" && !ValidApprovedName(p.Credential) {
		return errors.New("invalid credential reference")
	}
	return nil
}

// ApprovedSetup carries the bounded effective approved file bytes plus the
// verified source bundle. The daemon passes them to the fixed helper; the
// helper writes them root-owned and read-only before any role execution.
type ApprovedSetup struct {
	Files  map[string][]byte `json:"files"`
	Bundle []byte            `json:"bundle"`
}

func (s ApprovedSetup) Validate() error {
	if err := s.ValidateFiles(); err != nil {
		return err
	}
	if len(s.Bundle) == 0 || len(s.Bundle) > MaxSourceBundle {
		return errors.New("invalid source bundle size")
	}
	return nil
}

func (s ApprovedSetup) ValidateFiles() error {
	if len(s.Files) == 0 || len(s.Files) > MaxApprovedFiles {
		return errors.New("invalid approved file set")
	}
	if _, ok := s.Files[FactorySetupEntry]; !ok {
		return errors.New("approved setup entrypoint is required")
	}
	if _, ok := s.Files[FactoryCheckEntry]; !ok {
		return errors.New("approved check entrypoint is required")
	}
	total := 0
	for name, contents := range s.Files {
		if !ValidApprovedName(name) {
			return errors.New("invalid approved file name")
		}
		if len(contents) == 0 || len(contents) > MaxApprovedFileSize {
			return errors.New("invalid approved file size")
		}
		total += len(contents)
	}
	if total > MaxApprovedTotal {
		return errors.New("approved inputs exceed the bounded size")
	}
	if raw, exists := s.Files["setup-network.json"]; exists {
		var network struct {
			Access string `json:"access"`
		}
		if strictjson.Decode(bytes.NewReader(raw), &network) != nil || network.Access != "none" && network.Access != "public_https" {
			return errors.New("setup network access must be none or public_https")
		}
	}
	if raw, exists := s.Files["setup-limits.json"]; exists {
		var limits struct {
			Setup int `json:"setup_seconds"`
			Check int `json:"check_seconds"`
		}
		if strictjson.Decode(bytes.NewReader(raw), &limits) != nil || limits.Setup < 1 || limits.Setup > 1800 || limits.Check < 1 || limits.Check > 1800 {
			return errors.New("setup and check limits must be 1..1800 seconds")
		}
	}
	return nil
}

// Prepare is the host preparation request: the approved references plus the
// bounded effective inputs. The helper resolves them to fixed paths.
type Prepare struct {
	Preparation Preparation   `json:"preparation"`
	Setup       ApprovedSetup `json:"setup"`
}

func (p Prepare) Validate() error {
	if err := p.Preparation.Validate(); err != nil {
		return err
	}
	if err := p.Setup.Validate(); err != nil {
		return err
	}
	if p.Preparation.SetupDigest != SetupDigestOf(p.Setup.Files) {
		return errors.New("approved inputs do not match their digest")
	}
	return nil
}

// SetupDigestOf returns the canonical digest over the effective approved
// inputs: each file name, a zero byte, then its contents, names sorted. The
// privileged helper recomputes this same form before writing the snapshot.
func SetupDigestOf(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(files[name])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PreparationInputsDigestOf binds the complete maintainer-selected source
// inputs, protected setup files and required tool names. Sorting makes one
// exact selection independent of browser or map iteration order.
func PreparationInputsDigestOf(setupFiles []string, sourceInputs map[string]string, tools []string) string {
	type input struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
	}
	inputs := make([]input, 0, len(sourceInputs))
	for name, digest := range sourceInputs {
		inputs = append(inputs, input{name, digest})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	setupFiles, tools = append([]string{}, setupFiles...), append([]string{}, tools...)
	sort.Strings(setupFiles)
	sort.Strings(tools)
	data, _ := json.Marshal(struct {
		Inputs []input  `json:"source_inputs"`
		Setup  []string `json:"setup_files"`
		Tools  []string `json:"required_tools"`
	}{inputs, setupFiles, tools})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// ResolvedTool is one required tool resolved to an exact installed path and
// optional historical display label; preparation does not query version banners.
type ResolvedTool struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// PrepareState is the observed preparation outcome for one identity.
type PrepareState struct {
	ID           string         `json:"id"`
	Project      string         `json:"project"`
	Role         string         `json:"role"`
	Phase        string         `json:"phase"`
	Container    string         `json:"container"`
	SourceCommit string         `json:"source_commit"`
	SetupDigest  string         `json:"setup_digest"`
	Tools        []ResolvedTool `json:"tools,omitempty"`
	Missing      string         `json:"missing,omitempty"`
	SetupExit    *int           `json:"setup_exit,omitempty"`
	CheckExit    *int           `json:"check_exit,omitempty"`
	Output       string         `json:"output,omitempty"`
	Ready        bool           `json:"ready"`
	Stopped      bool           `json:"stopped"`
	Retirement   string         `json:"retirement,omitempty"`
}

// PrepareInspect addresses a preparation before or after completion.
type PrepareInspect struct {
	Project string `json:"project"`
	ID      string `json:"id"`
}

func (p PrepareInspect) Validate() error {
	if !ValidID(p.Project) || !ValidPreparationID(p.ID) {
		return errors.New("invalid preparation address")
	}
	return nil
}

// PrepareStop retires one preparation identity, even before it is observed.
type PrepareStop struct {
	Project string `json:"project"`
	ID      string `json:"id"`
}

// HoldState is the observed maintenance hold marker for one Project.
type HoldState struct {
	Active   bool  `json:"active"`
	Revision int64 `json:"revision"`
}

func (p PrepareStop) Validate() error {
	if !ValidID(p.Project) || !ValidPreparationID(p.ID) {
		return errors.New("invalid preparation address")
	}
	return nil
}

// PrepareHold sets or clears the Project maintenance hold marker. The store
// record is the source of truth; the marker enforces it natively.
type PrepareHold struct {
	Project  string `json:"project"`
	Hold     bool   `json:"hold"`
	Revision int64  `json:"revision"`
}

func (p PrepareHold) Validate() error {
	if !ValidID(p.Project) || p.Revision < 0 {
		return errors.New("invalid maintenance hold")
	}
	return nil
}

// MaintenanceHold is the durable Project maintenance hold with CAS revision.
type MaintenanceHold struct {
	Project  string `json:"project"`
	Revision int64  `json:"revision"`
	Hold     bool   `json:"hold"`
}

func (m MaintenanceHold) Validate() error {
	if !ValidID(m.Project) || m.Revision < 0 {
		return errors.New("invalid maintenance hold")
	}
	return nil
}
