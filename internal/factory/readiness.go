// Factory readiness records bind one repository/issue to its durable
// readiness verdict: the assessed acceptance, classification, blockers and
// the fingerprint of every input behind them. Assessment binds native
// evidence, authority and preparation versions; unchanged blocked inputs do
// not buy another assessment. Native issues and dependency graphs remain
// authoritative; these records are views, never a second issue database.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// Readiness classifications from the factory transition contract. ST07
// assesses the pre-execution states; active, needs_intervention,
// cancelled and completed arrive with attempt/run dispatch.
const (
	ReadinessNotAuthorized     = "not_authorized"
	ReadinessBlocked           = "blocked"
	ReadinessQueued            = "queued"
	ReadinessActive            = "active"
	ReadinessNeedsIntervention = "needs_intervention"
	ReadinessCancelled         = "cancelled"
	ReadinessCompleted         = "completed"
)

// ValidReadiness reports whether state is a known readiness classification.
func ValidReadiness(state string) bool {
	switch state {
	case ReadinessNotAuthorized, ReadinessBlocked, ReadinessQueued,
		ReadinessActive, ReadinessNeedsIntervention,
		ReadinessCancelled, ReadinessCompleted:
		return true
	default:
		return false
	}
}

// Readiness reason and blocker codes. queued issues carry ReasonEligible
// with no blockers; every other verdict names each specific blocker plus
// what would resolve it. Codes are bounded; details carry only ST05/ST06
// reason codes or native ID paths, never native content.
const (
	ReasonEligible = "eligible"

	BlockerAcceptanceMissing  = "acceptance_missing"
	BlockerAcceptanceInvalid  = "acceptance_invalid"
	BlockerAuthorityMissing   = "authority_missing"
	BlockerIssueInaccessible  = "issue_inaccessible"
	BlockerEvidenceIncomplete = "evidence_incomplete"
	BlockerCodePending        = "prereq_code_pending"
	BlockerResultPending      = "prereq_result_pending"
	BlockerEndpointHidden     = "prereq_endpoint_hidden"
	BlockerCycle              = "prereq_cycle"
	BlockerDependencyDepth    = "dependency_depth_exceeded"
)

// Prerequisite detail values: why one prerequisite stays pending or why
// its recorded route no longer holds.
const (
	ResultDetailAwaitingClosure     = "awaiting_closure"
	ResultDetailNoResolution        = "no_accepted_resolution"
	ResultDetailReopened            = "reopened"
	ResultDetailAcceptanceWithdrawn = "prereq_acceptance_withdrawn"
	DetailPrereqAcceptanceInvalid   = "prereq_acceptance_invalid"
)

func validBlockerCode(code string) bool {
	switch code {
	case BlockerAcceptanceMissing, BlockerAcceptanceInvalid,
		BlockerAuthorityMissing, BlockerIssueInaccessible,
		BlockerEvidenceIncomplete, BlockerCodePending,
		BlockerResultPending, BlockerEndpointHidden,
		BlockerCycle, BlockerDependencyDepth:
		return true
	default:
		return false
	}
}

// BlockerResolution is the static maintainer-facing resolution for one
// blocker code: what would resolve it. No free text enters resolutions.
func BlockerResolution(code string) string {
	switch code {
	case BlockerAcceptanceMissing:
		return "A code-write maintainer must adopt the issue requirements."
	case BlockerAcceptanceInvalid:
		return "A code-write maintainer must adopt the current revisions."
	case BlockerAuthorityMissing:
		return "Restore the missing factory authorization."
	case BlockerIssueInaccessible:
		return "The factory reader cannot see this issue; adjust native visibility."
	case BlockerEvidenceIncomplete:
		return "The issue exceeds the bounded native read; reduce comments or edges under the limit."
	case BlockerCodePending:
		return "Deliver the prerequisite through the factory or record a maintainer resolution identifying the native result."
	case BlockerResultPending:
		return "Close the prerequisite with an accepted resolution describing the result."
	case BlockerEndpointHidden:
		return "The factory reader cannot see the prerequisite; adjust native visibility."
	case BlockerCycle:
		return "Remove the dependency cycle through native dependency edits and readoption."
	case BlockerDependencyDepth:
		return "The dependency graph exceeds the assessment bound; narrow the chain."
	default:
		return ""
	}
}

// Blocker is one specific readiness blocker: its bounded code, a bounded
// detail (an ST05/ST06 reason code or native ID path), the prerequisite
// endpoint when the blocker names one, and the static resolution.
type Blocker struct {
	Code          string `json:"code"`
	Detail        string `json:"detail,omitempty"`
	EndpointRepo  int64  `json:"endpoint_repository,string,omitempty"`
	EndpointIssue int64  `json:"endpoint_issue,string,omitempty"`
	Resolution    string `json:"resolution"`
}

func validBlockerDetail(detail string) bool {
	if len(detail) > 256 || strings.IndexFunc(detail, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0 {
		return false
	}
	return true
}

// Validate rejects malformed blockers. Shape only: assessment supplies
// codes from the bounded set with ID-only endpoints.
func (b Blocker) Validate() error {
	if !validBlockerCode(b.Code) {
		return errors.New("invalid readiness blocker code")
	}
	if !validBlockerDetail(b.Detail) {
		return errors.New("invalid readiness blocker detail")
	}
	if (b.EndpointRepo <= 0) != (b.EndpointIssue <= 0) {
		return errors.New("readiness blocker endpoint needs repository and issue together")
	}
	if b.EndpointRepo < 0 || b.EndpointIssue < 0 {
		return errors.New("invalid readiness blocker endpoint")
	}
	if b.Resolution != BlockerResolution(b.Code) {
		return errors.New("readiness blocker resolution does not match its code")
	}
	return nil
}

// PrereqSatisfaction binds one satisfied result prerequisite to the exact
// closure occurrence that satisfied it: the endpoint's native close event
// under one dependent acceptance head. A reopen ends the
// occurrence; closing again cannot revive it without a new acceptance head.
type PrereqSatisfaction struct {
	ClosingOccurrence string `json:"closing_occurrence"`
	Acceptance        string `json:"acceptance"`
}

func (s PrereqSatisfaction) Validate() error {
	if !decimalNativeID(s.ClosingOccurrence) || !project.ValidDecisionID(s.Acceptance) {
		return errors.New("invalid prerequisite satisfaction occurrence")
	}
	return nil
}

// IssueControl is the one current readiness record per repository/issue:
// assessed acceptance, classification, primary reason, structured blockers
// and the fingerprint of every input behind the verdict. NativeRev,
// Authority and EndpointHeads let reconciliation skip issues whose inputs
// cannot have changed without re-reading native state.
type IssueControl struct {
	Blockers      []Blocker                     `json:"blockers,omitempty"`
	EndpointHeads map[string]string             `json:"endpoint_heads,omitempty"`
	Satisfied     map[string]PrereqSatisfaction `json:"satisfied,omitempty"`
	Repository    int64                         `json:"repository,string"`
	Issue         int64                         `json:"issue,string"`
	Revision      int64                         `json:"revision"`
	NativeRev     int64                         `json:"native_revision"`
	FirstSeenUnix int64                         `json:"first_seen_unix"`
	AssessedUnix  int64                         `json:"assessed_unix"`
	Acceptance    string                        `json:"acceptance,omitempty"`
	Readiness     string                        `json:"readiness"`
	Reason        string                        `json:"reason"`
	Fingerprint   string                        `json:"fingerprint"`
	Authority     string                        `json:"authority"`
}

// MaxControlBlockers bounds the blockers recorded on one issue.
const MaxControlBlockers = 64

// Validate rejects malformed control records. Fingerprint and authority
// digests are required: every recorded verdict binds its exact inputs.
func (c IssueControl) Validate() error {
	if err := c.validateControlIdentity(); err != nil {
		return err
	}
	if err := c.validateControlReadiness(); err != nil {
		return err
	}
	if err := c.validateControlBlockers(); err != nil {
		return err
	}
	return c.validateControlEndpoints()
}

// validateControlIdentity refuses controls outside the admitted
// identity, acceptance, readiness and reason vocabulary.
func (c IssueControl) validateControlIdentity() error {
	if c.Repository <= 0 || c.Issue <= 0 || c.Revision < 0 || c.NativeRev < 0 ||
		c.FirstSeenUnix < 0 || c.AssessedUnix < 0 {
		return errors.New("invalid issue control identity")
	}
	if c.Acceptance != "" && !project.ValidDecisionID(c.Acceptance) {
		return errors.New("invalid issue control acceptance")
	}
	if !ValidReadiness(c.Readiness) {
		return errors.New("invalid issue control readiness")
	}
	if c.Reason != ReasonEligible && !validBlockerCode(c.Reason) {
		return errors.New("invalid issue control reason")
	}
	return nil
}

// validateControlReadiness refuses queued controls carrying blockers
// and unready controls naming none.
func (c IssueControl) validateControlReadiness() error {
	if c.Readiness == ReadinessQueued && (c.Reason != ReasonEligible || len(c.Blockers) != 0) {
		return errors.New("queued issue control carries no blockers")
	}
	if c.Readiness != ReadinessQueued && len(c.Blockers) == 0 {
		return errors.New("unready issue control names its blockers")
	}
	return nil
}

// validateControlBlockers refuses oversized or invalid blocker sets.
func (c IssueControl) validateControlBlockers() error {
	if len(c.Blockers) > MaxControlBlockers {
		return errors.New("too many issue control blockers")
	}
	for _, blocker := range c.Blockers {
		if err := blocker.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateControlEndpoints refuses controls with unbound assessment
// inputs or malformed endpoint heads and satisfactions.
func (c IssueControl) validateControlEndpoints() error {
	if !ValidDigest(c.Fingerprint) || !ValidDigest(c.Authority) {
		return errors.New("issue control must bind its assessment inputs")
	}
	for occurrence, head := range c.EndpointHeads {
		if !decimalNativeID(occurrence) || (head != "" && !project.ValidDecisionID(head)) {
			return errors.New("invalid issue control endpoint head")
		}
	}
	for occurrence, satisfied := range c.Satisfied {
		if !decimalNativeID(occurrence) || satisfied.Validate() != nil {
			return errors.New("invalid issue control satisfaction")
		}
	}
	return nil
}

// DependenceRef names one dependent repository/issue: an issue whose
// recorded acceptance declares the referenced endpoint as a prerequisite.
type DependenceRef struct {
	Repository int64 `json:"repository,string"`
	Issue      int64 `json:"issue,string"`
}

// FingerprintPrereq is one prerequisite's verdict input: its occurrence,
// outcome, current endpoint head, satisfaction and the blocker it carries,
// plus endpoint closure evidence for result prerequisites. Code
// prerequisites exclude closure evidence: closure alone cannot satisfy a
// code outcome, so it must not retrigger assessment either.
type FingerprintPrereq struct {
	ClosingOccurrence string
	Occurrence        string
	Outcome           string
	EndpointHead      string
	Blocker           string
	Detail            string
	Closed            bool
	Satisfied         bool
}

// FingerprintInput gathers every semantic input behind one verdict. The
// global native revision is deliberately absent: a changed revision alone
// requires fresh reads, never a new assessment record. Obscured marks
// verdicts recorded without readable issue evidence.
type FingerprintInput struct {
	Validity   []string
	Prereqs    []FingerprintPrereq
	Acceptance string
	Authority  string
	Cycle      string
	Obscured   string
}

// Fingerprint binds the verdict inputs into one digest. Equal inputs yield
// equal fingerprints, so unchanged blockers never trigger repeated work.
func (in FingerprintInput) Fingerprint() string {
	validity := append([]string(nil), in.Validity...)
	sort.Strings(validity)
	prereqs := append([]FingerprintPrereq(nil), in.Prereqs...)
	sort.Slice(prereqs, func(i, j int) bool { return prereqs[i].Occurrence < prereqs[j].Occurrence })
	var b strings.Builder
	b.WriteString(in.Acceptance)
	b.WriteString("\x00")
	b.WriteString(in.Authority)
	b.WriteString("\x00")
	b.WriteString(strings.Join(validity, ","))
	b.WriteString("\x00")
	b.WriteString(in.Cycle)
	b.WriteString("\x00")
	b.WriteString(in.Obscured)
	for _, prereq := range prereqs {
		b.WriteString("\x00")
		b.WriteString(prereq.Occurrence)
		b.WriteString("\x00")
		b.WriteString(prereq.Outcome)
		b.WriteString("\x00")
		b.WriteString(prereq.EndpointHead)
		b.WriteString("\x00")
		b.WriteString(prereq.Blocker)
		b.WriteString("\x00")
		b.WriteString(prereq.Detail)
		b.WriteString("\x00")
		b.WriteString(strconv.FormatBool(prereq.Satisfied))
		b.WriteString("\x00")
		b.WriteString(strconv.FormatBool(prereq.Closed))
		b.WriteString("\x00")
		b.WriteString(prereq.ClosingOccurrence)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// AuthorityFingerprint binds one effective-authority verdict: effectiveness,
// every missing authorization and the bound grant revisions. A changed
// grant closes dispatch and retriggers assessment; unchanged authority
// never does.
func AuthorityFingerprint(effective EffectiveAuthority) string {
	missing := append([]string(nil), effective.Missing...)
	sort.Strings(missing)
	var b strings.Builder
	b.WriteString(strconv.FormatBool(effective.Effective))
	b.WriteString("\x00")
	b.WriteString(strconv.FormatBool(effective.DispatchOpen))
	b.WriteString("\x00")
	b.WriteString(strings.Join(missing, ","))
	b.WriteString("\x00")
	b.WriteString(strconv.FormatInt(effective.Authority.Policy, 10))
	b.WriteString("\x00")
	b.WriteString(strconv.FormatInt(effective.Authority.Operator, 10))
	b.WriteString("\x00")
	b.WriteString(strconv.FormatInt(effective.Authority.Capacity, 10))
	b.WriteString("\x00")
	b.WriteString(strconv.FormatInt(effective.Authority.Sponsorship, 10))
	b.WriteString("\x00")
	b.WriteString(strconv.FormatInt(effective.Authority.Environment, 10))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
