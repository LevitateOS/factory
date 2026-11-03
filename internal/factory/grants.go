// Factory standing grants: separately recorded repository policy, appliance
// capacity, operator permission and provider sponsorship. Each authority is
// checked separately; one grant cannot substitute for another. Native actor
// references name administrator-enrolled bindings by ID only; they never
// enroll or carry credentials.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// Operation kinds mirror the enrolled native binding kinds. Authorization
// for one kind never grants another.
const (
	OpRefPublish    = "git.ref.publish"
	OpPRCreate      = "pull_request.create"
	OpReviewSubmit  = "pull_request.review.submit"
	OpThreadResolve = "pull_request.review.thread.resolve"
	OpMerge         = "pull_request.merge"
)

// ValidOperationKind reports whether kind is a supported native operation kind.
func ValidOperationKind(kind string) bool {
	switch kind {
	case OpRefPublish, OpPRCreate, OpReviewSubmit, OpThreadResolve, OpMerge:
		return true
	default:
		return false
	}
}

// MergeFastForward is the only supported conditional merge method.
const MergeFastForward = "fast-forward-only"

// Queued actions and finite funding records use the command ledger. Standing
// settings use their own revisions directly.
const (
	CommandRetry                 = "retry"
	CommandFeedPostRetry         = "feed-post-retry"
	CommandRetryImplementation   = "retry_implementation"
	CommandVerificationAllowance = "verification-allowance"
	// CommandRepairGrant authorizes one repository's finite D15 stall
	// repair scope: the explicit sponsor, revision chain and run,
	// judgment and active-minute bounds. Repair funding never silently
	// debits a private intent owner's grant.
	CommandRepairGrant = "repair-grant"
	// CommandIntentExecutionLimit records one origin intent's explicit
	// D15 active-time aggregate bound beside existing intent funding.
	// All generated issues, attempts and reassessments share the origin
	// aggregate; the bound never refills on retry, revision or split.
	CommandIntentExecutionLimit = "intent-execution-limit"
	// CommandRepairCharge records one immutable D15 repair charge by run
	// or request identity on the shared idempotent ledger. Failure stays
	// charged; replaying the identity spends nothing.
	CommandRepairCharge   = "repair-charge"
	CommandDeliveryRepair = "delivery-repair"
)

// SettingsCommandType reports whether typ is a settings or queued-decision
// command. Pause, resume and stop operate directly on their current state.
func SettingsCommandType(typ string) bool {
	switch typ {
	case CommandRetry, CommandRetryImplementation, CommandCompletionRetry, CommandFeedPostRetry,
		CommandRepairGrant, CommandIntentExecutionLimit, CommandRepairCharge, CommandVerificationAllowance, CommandDeliveryRepair:
		return true
	default:
		return false
	}
}

// SettingsDigest binds a settings command to its exact normalized payload.
// Operator commands never use this digest.
func SettingsDigest(typ, target, payload string) string {
	sum := sha256.Sum256([]byte(typ + "\x00" + target + "\x00" + payload))
	return hex.EncodeToString(sum[:])
}

// ActorBindingRef references one administrator-enrolled native actor binding
// for the policy's repository. It holds IDs only, never secrets, and never
// selects an installation: the host derives installation identity at
// dispatch from authenticated transport.
type ActorBindingRef struct {
	TokenID int64  `json:"token_id,string"`
	ActorID int64  `json:"actor_id,string"`
	Kind    string `json:"kind"`
}

func (a ActorBindingRef) Validate() error {
	if a.TokenID <= 0 || a.ActorID <= 0 || !ValidOperationKind(a.Kind) {
		return errors.New("invalid native actor binding reference")
	}
	return nil
}

// RoleSelection pins the harness family, model and execution profile for
// one factory role. The harness version comes from the staged pin at
// dispatch; HarnessVers survives only on records saved before that, where
// it is the recorded identity of runs admitted without tool proof. An
// omitted profile resolves to the product defaults at policy creation;
// running snapshots are always explicit.
type RoleSelection struct {
	Profile     ExecutionProfile `json:"profile,omitempty"`
	Harness     string           `json:"harness"`
	HarnessVers string           `json:"harness_version,omitempty"`
	Model       string           `json:"model"`
}

func (s RoleSelection) Validate() error {
	if !project.ValidHarnessFamily(s.Harness) || (s.HarnessVers != "" && !project.ValidHarnessVersion(s.HarnessVers)) {
		return errors.New("invalid role harness selection")
	}
	if s.Model == "" || len(s.Model) > 128 || strings.IndexFunc(s.Model, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0 {
		return errors.New("invalid role model selection")
	}
	if s.Profile != (ExecutionProfile{}) {
		if err := s.Profile.Validate(s.Harness); err != nil {
			return err
		}
	}
	return nil
}

const (
	// MaxRequiredChecks bounds the nonempty native check set.
	MaxRequiredChecks = 32
	// MaxCheckName bounds one required check name.
	MaxCheckName = 128
)

// ValidTargetBranch reports whether branch is one full native branch ref.
func ValidTargetBranch(branch string) bool {
	name, ok := strings.CutPrefix(branch, "refs/heads/")
	if !ok || name == "" || len(branch) > 256 || branch != strings.TrimSpace(branch) {
		return false
	}
	return !strings.Contains(branch, "..") && strings.IndexFunc(branch, func(r rune) bool {
		return r <= 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*\\", r)
	}) < 0
}

// Frozen D13 reassessment policy: active minutes funded for one
// post-completion reassessment cycle. Admission records the admitted value
// explicitly, resolving omission to the default.
const (
	DefaultReassessmentMinutes = 30
	MinReassessmentMinutes     = 1
	MaxReassessmentMinutes     = 120
)

// EffectiveReassessmentMinutes resolves an omitted reassessment allowance
// to the product default at policy admission. Explicit values pass through
// for range validation; out-of-range values refuse there.
func EffectiveReassessmentMinutes(minutes int) int {
	if minutes == 0 {
		return DefaultReassessmentMinutes
	}
	return minutes
}

// RepositoryPolicy is the repository owner's standing factory authorization:
// enablement, target, role selections, required native checks, merge method,
// repository limits, the post-completion reassessment allowance and the
// enrolled native actor references. It grants no appliance capacity,
// provider sponsorship or environment permission.
type RepositoryPolicy struct {
	Roles               map[string]RoleSelection `json:"roles"`
	Publish             ActorBindingRef          `json:"publish_actor"`
	Create              ActorBindingRef          `json:"create_actor"`
	Review              ActorBindingRef          `json:"review_actor"`
	Resolve             ActorBindingRef          `json:"resolve_actor,omitempty"`
	Merge               ActorBindingRef          `json:"merge_actor"`
	Repository          int64                    `json:"repository,string"`
	Revision            int64                    `json:"revision"`
	GrantedBy           int64                    `json:"granted_by,string"`
	TargetBranch        string                   `json:"target_branch"`
	Checks              []string                 `json:"required_checks"`
	MergeMethod         string                   `json:"merge_method"`
	AttemptLimits       AttemptLimits            `json:"attempt_limits"`
	ReassessmentMinutes int                      `json:"reassessment_minutes"`
	IntentMinutes       int                      `json:"intent_minutes"`
	Enabled             bool                     `json:"enabled"`
	Paused              bool                     `json:"paused"`
}

func (p RepositoryPolicy) Validate() error {
	if p.Repository <= 0 || p.Revision < 0 || p.GrantedBy <= 0 {
		return errors.New("invalid repository policy identity")
	}
	if !ValidTargetBranch(p.TargetBranch) {
		return errors.New("invalid policy target branch")
	}
	if len(p.Roles) != 2 || p.Roles[project.RoleCoder].Validate() != nil || p.Roles[project.RoleReviewer].Validate() != nil {
		return errors.New("policy must select exactly the coding and review roles")
	}
	if err := checkPolicyCheckNames(p); err != nil {
		return err
	}
	if p.MergeMethod != MergeFastForward {
		return errors.New("unsupported merge method")
	}
	if err := checkPolicyBindings(p); err != nil {
		return err
	}
	if p.ReassessmentMinutes < MinReassessmentMinutes || p.ReassessmentMinutes > MaxReassessmentMinutes {
		return errors.New("policy reassessment minutes outside their bound")
	}
	// Intent minutes carry no omission fill and no upper cap: zero is the
	// valid unfunded posture, and funding enforces the finite-positive
	// bound from the allowance rule. Only a negative horizon refuses here.
	if p.IntentMinutes < 0 {
		return errors.New("policy intent minutes outside their bound")
	}
	if err := p.AttemptLimits.Validate(); err != nil {
		return err
	}
	return nil
}

func checkPolicyCheckNames(p RepositoryPolicy) error {
	if len(p.Checks) == 0 || len(p.Checks) > MaxRequiredChecks {
		return errors.New("policy requires a nonempty bounded check set")
	}
	seen := make(map[string]bool, len(p.Checks))
	for _, check := range p.Checks {
		if check == "" || len(check) > MaxCheckName || check != strings.TrimSpace(check) ||
			strings.IndexFunc(check, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || seen[check] {
			return errors.New("invalid required check name")
		}
		seen[check] = true
	}
	return nil
}

func checkPolicyBindings(p RepositoryPolicy) error {
	bindings := map[string]ActorBindingRef{
		OpRefPublish: p.Publish, OpPRCreate: p.Create, OpReviewSubmit: p.Review, OpMerge: p.Merge,
	}
	if p.Resolve != (ActorBindingRef{}) {
		bindings[OpThreadResolve] = p.Resolve
	}
	for slot, ref := range bindings {
		if err := ref.Validate(); err != nil || ref.Kind != slot {
			return errors.New("policy actor binding does not match its operation kind")
		}
	}
	return nil
}

// ResolveBinding returns the policy's enrolled thread-resolve actor
// binding. The resolve credential is its own operation authorization on
// the reviewer account identity: review permission is never a reusable
// resolve credential, and a missing binding refuses resolution instead of
// inheriting another kind. Keeper of the binding stays with policy
// admission; FW23 can require enrollment for new policies.
func (p RepositoryPolicy) ResolveBinding() (ActorBindingRef, error) {
	if p.Resolve == (ActorBindingRef{}) {
		return ActorBindingRef{}, errors.New("missing resolve binding")
	}
	if err := p.Resolve.Validate(); err != nil || p.Resolve.Kind != OpThreadResolve {
		return ActorBindingRef{}, errors.New("policy actor binding does not match its operation kind")
	}
	return p.Resolve, nil
}

// Capacity is the appliance-wide factory execution budget, held separately
// from any repository grant. Only the configured operator changes it.
type Capacity struct {
	Revision          int64 `json:"revision"`
	UpdatedBy         int64 `json:"updated_by,string"`
	MaxConcurrentRuns int   `json:"max_concurrent_runs"`
}

func (c Capacity) Validate() error {
	if c.Revision < 0 || c.UpdatedBy <= 0 {
		return errors.New("invalid capacity identity")
	}
	if c.MaxConcurrentRuns < 1 || c.MaxConcurrentRuns > 64 {
		return errors.New("invalid appliance capacity limits")
	}
	return nil
}

// OperatorGrant permits one repository to use appliance capacity. It
// grants no repository access or merge rights.
type OperatorGrant struct {
	Repository int64 `json:"repository,string"`
	Revision   int64 `json:"revision"`
	GrantedBy  int64 `json:"granted_by,string"`
	Active     bool  `json:"active"`
}

func (g OperatorGrant) Validate() error {
	if g.Repository <= 0 || g.Revision < 0 || g.GrantedBy <= 0 {
		return errors.New("invalid operator grant identity")
	}
	return nil
}

// AuthorityRef binds one dispatch decision to the exact grant, preparation
// and acceptance revisions behind it. Fountain treats these revisions as
// opaque. Each launch revalidates current grants; a retry records its fresh
// admission without renewing the existing attempt's allowance.
type AuthorityRef struct {
	RequirementsID        string `json:"requirements_id,omitempty"`
	ApprovalID            string `json:"approval_id,omitempty"`
	SponsorshipConnection string `json:"sponsorship_connection,omitempty"`
	Policy                int64  `json:"policy"`
	Operator              int64  `json:"operator"`
	Capacity              int64  `json:"capacity"`
	Sponsorship           int64  `json:"sponsorship"`
	Environment           int64  `json:"environment"`
}

// SameGrants reports whether current authority still names the grants
// bound into existing work: the same sponsored connection and the same
// requirement and approval decisions. Grant revisions are left out on
// purpose. Saving a number (an allowance, a limit, a check list) bumps a
// revision but withdraws nothing; disabling, pausing or withdrawing a grant
// closes the dispatch gate, which callers check as effective authority.
func (r AuthorityRef) SameGrants(bound AuthorityRef) bool {
	return r.SponsorshipConnection == bound.SponsorshipConnection &&
		r.RequirementsID == bound.RequirementsID && r.ApprovalID == bound.ApprovalID
}

// Missing-authority reason codes. Bounded and credential-free: controls
// expose which authorization is missing or withdrawn, never secrets.
const (
	MissingPolicy            = "policy_missing"
	MissingPolicyDisabled    = "policy_disabled"
	MissingPolicyPaused      = "policy_paused"
	MissingOperatorGrant     = "operator_grant_missing"
	MissingOperatorWithdrawn = "operator_grant_withdrawn"
	MissingCapacity          = "capacity_missing"
	MissingSponsorship       = "sponsorship_missing"
	MissingSponsorshipGone   = "sponsorship_withdrawn"
	MissingEnvironment       = "environment_grant_missing"
	MissingEnvironmentGone   = "environment_grant_withdrawn"
	MissingPreparation       = "preparation_not_ready"
	MissingDispatch          = "dispatch_closed"
)

// EffectiveAuthority is the visible enablement verdict for one repository:
// the bound revisions, whether dispatch may proceed, and every missing or
// withdrawn authorization. It carries IDs and revisions only.
type EffectiveAuthority struct {
	Missing      []string     `json:"missing"`
	Authority    AuthorityRef `json:"authority"`
	Effective    bool         `json:"effective"`
	DispatchOpen bool         `json:"dispatch_open"`
}

// GrantResult reports the saved revision, authority and requested withdrawal
// state. External outcomes remain on publication and merge records. Repair
// funding also returns its canonical grant identity as CommandID.
type GrantResult struct {
	PublicationsPending bool               `json:"publications_pending"`
	MergesPending       bool               `json:"merges_pending"`
	Effective           EffectiveAuthority `json:"effective"`
	CommandID           string             `json:"command_id,omitempty"`
	Revision            int64              `json:"revision"`
	Withdrawn           bool               `json:"withdrawn"`
}

// AuthorityInput gathers the current records for one repository. A nil
// record means no grant was ever recorded. PreparationReady applies only
// when a Project exists for the repository.
type AuthorityInput struct {
	Policy           *RepositoryPolicy
	Operator         *OperatorGrant
	Appliance        *Capacity
	Sponsorship      *Sponsorship
	Environment      *project.EnvironmentGrant
	ProjectExists    bool
	PreparationReady bool
	DispatchOpen     bool
}

// MaxActiveDispatchCauses bounds the cause labels retained on one gate.
// Labels remain bounded strings because grant withdrawal callers own their
// vocabulary; lifecycle controls do not reinterpret grant causes.
const MaxActiveDispatchCauses = 16

// Withdrawal is the durable state of one dispatch closure and its causes. Local
// withdrawal acknowledges requested cancellation, not a won native race.
type Withdrawal struct {
	ActiveCauses []string              `json:"active_causes"`
	Publications PublicationWithdrawal `json:"publications"`
	Merges       MergeWithdrawal       `json:"merges"`
	Repository   int64                 `json:"repository,string"`
	Revision     int64                 `json:"revision"`
	Cause        string                `json:"cause"`
	ClosedBy     string                `json:"closed_by"`
}

func (w Withdrawal) Validate() error {
	if w.Repository <= 0 || w.Revision < 0 || w.Cause == "" || len(w.Cause) > 256 || w.ClosedBy == "" || len(w.ClosedBy) > 128 {
		return errors.New("invalid dispatch withdrawal")
	}
	if len(w.ActiveCauses) > MaxActiveDispatchCauses {
		return errors.New("dispatch has too many active closure causes")
	}
	seenCauses := make(map[string]struct{}, len(w.ActiveCauses))
	for _, cause := range w.ActiveCauses {
		if cause == "" || len(cause) > 256 {
			return errors.New("invalid active dispatch cause")
		}
		if _, exists := seenCauses[cause]; exists {
			return errors.New("duplicate active dispatch cause")
		}
		seenCauses[cause] = struct{}{}
	}
	return nil
}
