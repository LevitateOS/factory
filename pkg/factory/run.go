package factory

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/identity"
	"github.com/levitateos/factory/pkg/project"
)

var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidDigest(value string) bool { return digest.MatchString(value) }

// Run is one supervised factory execution. The supervisor reconciles captured
// process and credential facts against runtime truth; Reconciled marks the
// settled run exactly once.
type Run struct {
	// Native selects native execution explicitly. Appliance records keep
	// their original encoding; native records must have no appliance fields.
	Native *execution.NativeRecord `json:"native,omitempty"`
	// ApplianceInvocation preserves first observed host process identity even
	// while broker reads or retirement remain uncertain.
	ApplianceInvocation *execution.ApplianceInvocation `json:"appliance_invocation,omitempty"`
	// ContextPending records a staged dispatch packet whose context has not
	// admitted its first native launch. It is cleared durably before that call.
	ContextPending       bool                                    `json:"context_pending,omitempty"`
	Admission            *RunAdmission                           `json:"admission,omitempty"`
	ExecutionManifest    []byte                                  `json:"execution_manifest,omitempty"`
	VerificationManifest []byte                                  `json:"verification_manifest,omitempty"`
	VerificationChecks   *project.FactoryVerificationChecksState `json:"verification_checks,omitempty"`
	FinalCheckpoint      *execution.CheckpointReceipt            `json:"final_checkpoint,omitempty"`
	CredentialDelegated  bool                                    `json:"credential_delegated"`
	CredentialReturned   bool                                    `json:"credential_returned"`
	IdentityLeaseID      string                                  `json:"identity_lease_id,omitempty"`
	IdentityGeneration   int64                                   `json:"identity_generation,omitempty"`
	IdentityBinding      *identity.Binding                       `json:"identity_binding,omitempty"`
	ID                   string                                  `json:"id"`
	ProjectID            string                                  `json:"project_id"`
	Role                 string                                  `json:"role"`
	InputSHA             string                                  `json:"input_sha"`
	Started              time.Time                               `json:"started"`
	Deadline             time.Time                               `json:"deadline"`
	Outcome              Outcome                                 `json:"outcome,omitempty"`
	Summary              string                                  `json:"summary,omitempty"`
	Reconciled           bool                                    `json:"reconciled"`
	Image                string                                  `json:"image"`
	Harness              string                                  `json:"harness"`
	Model                string                                  `json:"model"`
}

// RunAdmission is the immutable authorization and execution profile captured
// when a dispatch packet admits this run. Standalone supervisor runs may omit
// it because they do not originate from factory dispatch.
//
// Tool captures the appliance bundle adopted at admission. Native.Tool owns
// the captured native executable instead. Older appliance admissions without
// proof validate the recorded policy version and never adopt today's selection.
// Native captures the placement-bound launch inputs; nil on a native metadata
// record preserves unplaced evidence and grants no launch authority.
type RunAdmission struct {
	Authority AuthorityRef               `json:"authority"`
	Policy    RepositoryPolicy           `json:"policy"`
	Profile   *project.Profile           `json:"profile,omitempty"`
	Tool      *project.FactoryHarnessPin `json:"tool,omitempty"`
	Native    *execution.NativeLaunch    `json:"native,omitempty"`
}

func (r Run) Validate() error {
	if !ValidID(r.ID) || !ValidCommit(r.InputSHA) {
		return errors.New("invalid factory execution identity")
	}
	if !ValidProjectID(r.ProjectID) || !role.MatchString(r.Role) {
		return errors.New("invalid factory execution address")
	}
	if err := r.validateDeadline(); err != nil {
		return err
	}
	if err := r.validateAdmission(); err != nil {
		return err
	}
	if len(r.ExecutionManifest) > 0 {
		manifest, err := project.ParseExecutionManifest(r.ExecutionManifest)
		if err != nil || manifest.Run != r.ID || !manifest.Deadline.Equal(r.Deadline) {
			return errors.New("execution manifest does not bind its recorded run")
		}
	}
	if r.FinalCheckpoint != nil {
		request := r.FinalCheckpoint.Request
		if r.CheckpointBinding(request) != nil || r.FinalCheckpoint.ValidateFor(request, true) != nil {
			return errors.New("final checkpoint does not bind its recorded run")
		}
	}
	if len(r.VerificationManifest) > 0 {
		manifest, err := project.ParseVerificationManifest(r.VerificationManifest)
		if err != nil || manifest.Run != r.ID || manifest.Project != r.ProjectID || manifest.SourceCommit != r.InputSHA || !manifest.Deadline.Equal(r.Deadline) || r.Role != project.RoleReviewer || len(r.ExecutionManifest) > 0 {
			return errors.New("verification manifest does not bind its recorded run")
		}
		if r.VerificationChecks != nil {
			state := r.VerificationChecks
			request := project.FactoryVerificationChecks{Project: r.ProjectID, Run: r.ID, Incarnation: state.Incarnation, ManifestDigest: project.ExecutionManifestDigest(r.VerificationManifest)}
			if state.ValidateFor(request) != nil || state.SourceCommit != manifest.SourceCommit || state.Preparation != manifest.Preparation || state.CheckDigest != manifest.CheckDigest {
				return errors.New("verification checks do not bind their recorded manifest")
			}
			if invocation := r.InvocationID(); invocation != "" && invocation != state.Incarnation {
				return errors.New("verification checks differ from the native invocation")
			}
		}
	} else if r.VerificationChecks != nil {
		return errors.New("verification receipt has no recorded manifest")
	}
	if err := r.validateProvenance(); err != nil {
		return err
	}
	if err := r.validateOutcome(); err != nil {
		return err
	}
	return r.validateCredentials()
}

// CheckpointBinding compares a receipt/request with captured run facts. An
// appliance's historic record may lack process observations; native requests
// require the complete captured native process anchor, never a current tool.
func (r Run) CheckpointBinding(request execution.CheckpointRequest) error {
	if len(r.ExecutionManifest) == 0 || request.ID != r.ID || request.Project != r.ProjectID || request.ManifestDigest != project.ExecutionManifestDigest(r.ExecutionManifest) || request.RuntimeKind() != r.RuntimeKind() {
		return execution.ErrCheckpoint
	}
	if r.Native != nil {
		if r.Native.Validate() != nil || !request.MatchesNative(r.Native.Provenance) {
			return execution.ErrCheckpoint
		}
		return nil
	}
	if p := r.ApplianceInvocation; p != nil && (p.Container != "" && request.Container != p.Container || request.Invocation != p.Invocation) {
		return execution.ErrCheckpoint
	}
	if b := r.IdentityBinding; b != nil && (b.Project != "" && request.Container != b.Project || b.InvocationID != "" && request.Invocation != b.InvocationID) {
		return execution.ErrCheckpoint
	}
	return nil
}

// AdoptedToolIdentity resolves the exact tool bundle a dispatch packet
// recorded: family and version from the assignment, executable digest and
// image from the assignment proof, cross-checked against the run image. It
// returns the zero pin when the packet predates recorded proof; callers keep
// those rows on the legacy policy-version rule and fail their recovery
// closed instead of falling back to the current selection.
func AdoptedToolIdentity(a Assignment, image string) (project.FactoryHarnessPin, error) {
	var none project.FactoryHarnessPin
	if a.AdoptedHarness == none {
		return none, nil
	}
	adopted := a.AdoptedHarness
	if err := adopted.Validate(); err != nil {
		return none, errors.New("recorded tool proof is invalid")
	}
	if a.Harness != adopted.Harness+"-"+adopted.Version || a.HarnessVers != adopted.Version {
		return none, errors.New("recorded tool proof does not match its assignment")
	}
	if image != adopted.Image {
		return none, errors.New("recorded tool proof does not match its run")
	}
	return adopted, nil
}

func (r Run) validateAdmission() error {
	if r.Admission == nil {
		return nil
	}
	a := r.Admission
	if err := a.Policy.Validate(); err != nil {
		return errors.New("invalid run admission policy")
	}
	if a.Policy.Revision != a.Authority.Policy {
		return errors.New("run admission policy revision does not match authority")
	}
	selection, ok := a.Policy.Roles[r.Role]
	if !ok || r.Model != selection.Model {
		return errors.New("run does not match admitted role selection")
	}
	if r.Native != nil {
		if a.Profile != nil || a.Tool != nil || r.Native.Tool.Harness != selection.Harness {
			return errors.New("native run does not match admitted role selection")
		}
		if a.Native != nil && !a.Native.MatchesRecord(*r.Native) {
			return errors.New("native run differs from its captured placement/executable")
		}
		return nil
	}
	if a.Native != nil {
		return errors.New("appliance run has native launch admission")
	}
	if a.Profile == nil || a.Profile.Validate() != nil {
		return errors.New("invalid run admission profile")
	}
	if a.Tool == nil || *a.Tool == (project.FactoryHarnessPin{}) {
		// Rows admitted before tool-identity proof existed: the recorded
		// policy version is the only recorded identity, so it must match
		// exactly. These rows never adopt the current version.
		if r.Harness != selection.Harness+"-"+selection.HarnessVers {
			return errors.New("run does not match admitted role selection")
		}
		return nil
	}
	// Rows with recorded proof bind it exactly: the adopted family must be
	// the approved selection family, and the run must carry the adopted
	// family-version and image byte-for-byte. Currency of the adopted
	// bundle is enforced at dispatch by the live host pin check (due-check
	// plus stale refusal); the executable digest itself is proved by the
	// host when it stages the bound bundle.
	if err := a.Tool.Validate(); err != nil {
		return errors.New("run does not match admitted role selection")
	}
	if a.Tool.Harness != selection.Harness {
		return errors.New("run does not match admitted role selection")
	}
	if r.Harness != a.Tool.Harness+"-"+a.Tool.Version || r.Image != a.Tool.Image {
		return errors.New("run does not match admitted role selection")
	}
	return nil
}

func (r Run) validateDeadline() error {
	if r.Started.IsZero() || !r.Deadline.After(r.Started) || r.Deadline.Sub(r.Started) > 3*time.Hour {
		return errors.New("invalid execution deadline")
	}
	return nil
}

func (r Run) validateProvenance() error {
	if r.Native != nil {
		if r.Native.Validate() != nil || r.ApplianceInvocation != nil || r.Image != "" || r.IdentityLeaseID != "" || r.IdentityGeneration != 0 || r.IdentityBinding != nil || r.CredentialDelegated || r.CredentialReturned ||
			r.Harness != r.Native.Tool.Harness+"-"+r.Native.Tool.Version {
			return errors.New("native execution has conflicting provenance")
		}
		if r.Native.Intent != "" {
			intent, err := execution.ParseNativeIntent(r.Native.Intent)
			if err != nil || intent.Run != r.ID || intent.Workspace != r.ProjectID ||
				intent.Role != r.Role || intent.SourceCommit != r.InputSHA || !intent.Deadline.Equal(r.Deadline) ||
				r.Admission != nil && (r.Admission.Native != nil && intent.Native != *r.Admission.Native ||
					r.Admission.Authority.SponsorshipConnection != "" && intent.Connection != r.Admission.Authority.SponsorshipConnection) {
				return execution.ErrNativeIntent
			}
		}
	} else {
		value, pinned := strings.CutPrefix(r.Image, "sha256:")
		if !pinned || !ValidDigest(value) {
			return errors.New("execution image is not pinned")
		}
		if p := r.ApplianceInvocation; p != nil {
			if p.ValidateFor(r.ProjectID, r.IdentityGeneration) != nil {
				return errors.New("appliance invocation does not bind its recorded run")
			}
			if b := r.IdentityBinding; b != nil && (p.Container != "" && b.Project != p.Container || b.InvocationID != "" && b.InvocationID != p.Invocation) {
				return errors.New("broker binding does not match the recorded invocation")
			}
		}
	}
	if r.Harness == "" || r.Model == "" || len(r.Harness) > 128 || len(r.Model) > 128 {
		return errors.New("execution harness/model provenance is incomplete")
	}
	return nil
}

func (r Run) validateOutcome() error {
	if !validOutcome(r.Outcome) || len(r.Summary) > 16<<10 || (r.Outcome == "" && r.Reconciled) {
		return errors.New("invalid execution outcome")
	}
	if r.Native != nil && r.Reconciled && !r.Native.Settled() {
		return errors.New("native run retirement or custody is unconfirmed")
	}
	return nil
}

func (r Run) validateCredentials() error {
	if r.Native != nil {
		return nil // Native custody is validated by its explicit variant.
	}
	if r.CredentialReturned && !r.CredentialDelegated {
		return errors.New("credential return has no delegation")
	}
	if r.IdentityLeaseID == "" {
		return r.validateUnleasedCredential()
	}
	if r.IdentityGeneration <= 0 {
		return errors.New("invalid credential generation")
	}
	if r.CredentialDelegated && r.IdentityBinding == nil {
		return errors.New("invalid credential delegation")
	}
	return r.validateIdentityBinding()
}

// MarshalJSON preserves appliance records byte-for-byte in field shape. The
// native variant omits appliance-only claims rather than inventing zero image,
// UID/container/broker facts. No alternate retained-record decoder is needed.
func (r Run) MarshalJSON() ([]byte, error) {
	type wireRun Run
	raw, err := json.Marshal(wireRun(r))
	if err != nil || r.Native == nil {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"image", "credential_delegated", "credential_returned", "identity_lease_id", "identity_generation", "identity_binding"} {
		delete(fields, key)
	}
	return json.Marshal(fields)
}

func (r Run) RuntimeKind() collaboration.RuntimeKind {
	if r.Native != nil {
		return collaboration.Native
	}
	return collaboration.Appliance
}

// SupportsTaskCommands identifies the captured Muse task controller. Runtime
// metadata without admitted tool/placement proof cannot advertise this bridge.
// Current capability, process and task admission remain application checks.
func (r Run) SupportsTaskCommands() bool {
	if r.Admission == nil || r.Validate() != nil {
		return false
	}
	if r.Native != nil {
		return r.Admission.Native != nil && r.Admission.Native.MatchesRecord(*r.Native) && r.Native.Tool.Harness == project.FactoryHarnessMuse && r.Harness == project.FactoryHarnessMuse+"-"+r.Native.Tool.Version
	}
	return r.Admission.Tool != nil && r.Admission.Tool.Harness == project.FactoryHarnessMuse && r.Harness == project.FactoryHarnessMuse+"-"+r.Admission.Tool.Version
}

func (r Run) InvocationID() string {
	if r.Native != nil {
		if r.Native.Provenance.Process != nil {
			return r.Native.Provenance.Process.InvocationID
		}
		return ""
	}
	if r.ApplianceInvocation != nil {
		return r.ApplianceInvocation.Invocation
	}
	if r.IdentityBinding != nil {
		return r.IdentityBinding.InvocationID
	}
	return ""
}

func (r Run) CredentialsDelegated() bool {
	if r.Native != nil {
		return r.Native.Provenance.Custody.AcquisitionID != ""
	}
	return r.CredentialDelegated
}

func (r Run) CredentialsReturned() bool {
	if r.Native != nil {
		return r.Native.Provenance.Custody.State == execution.Released
	}
	return r.CredentialReturned
}

func (r Run) HasExecutionIdentity() bool {
	return r.InvocationID() != "" || r.CredentialsDelegated() || r.IdentityBinding != nil || r.IdentityLeaseID != ""
}

func (r Run) validateUnleasedCredential() error {
	// Sealing may clear the broker lease while retaining its exact binding.
	// Attribution is still execution evidence; it is not credential return.
	if r.IdentityBinding != nil && r.CredentialDelegated && r.IdentityGeneration > 0 {
		return r.validateIdentityBinding()
	}
	if r.IdentityGeneration != 0 || r.IdentityBinding != nil || r.CredentialDelegated {
		return errors.New("credential authority has no lease")
	}
	return nil
}

func (r Run) validateIdentityBinding() error {
	b := r.IdentityBinding
	if b == nil {
		return nil
	}
	if b.Validate() != nil || b.Kind != identity.Factory || b.Generation != r.IdentityGeneration || b.ID != r.ID {
		return errors.New("invalid execution binding")
	}
	return nil
}
