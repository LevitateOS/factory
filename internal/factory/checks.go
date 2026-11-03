// Check assessments bind one native PR's exact head and verified base to
// the required-check verdict behind a merge decision (ST11, S-checks).
// Soda evaluates the nonempty complete configured check set from observed
// native state; native Actions schedules work on separately managed
// capacity, and no agent narrative can mark checks passing: VerifyChecks
// takes no success-claim input at all, only the exact target, the adopted
// approval definitions, the current policy and one bracketed native
// observation. ST12 consumes the persisted assessment as check evidence.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

// Check verdicts. Only CheckPass authorizes a merge decision; every other
// verdict names its reason and, for pending checks, what CI still owes.
const (
	// CheckPass means every adopted required check succeeded on the exact head.
	CheckPass = "pass"
	// CheckPending means only missing or pending evidence blocks; CI is
	// still running and the attempt waits without weakening verification.
	CheckPending = "pending"
	// CheckFailed means a required check reported a definitive non-pass
	// state; the failure is correction or intervention input, never a wait.
	CheckFailed = "failed"
	// CheckRefused means the assessment itself cannot authorize: the
	// head/base moved, the approval definitions changed without adoption,
	// or the evidence is incomplete or unreadable.
	CheckRefused = "refused"
)

// ValidCheckVerdict reports whether verdict is a known check verdict.
func ValidCheckVerdict(verdict string) bool {
	switch verdict {
	case CheckPass, CheckPending, CheckFailed, CheckRefused:
		return true
	default:
		return false
	}
}

// Check reason codes. Bounded; the view renders the static resolution.
const (
	CheckReasonPass               = "checks_pass"
	CheckReasonPending            = "checks_pending"
	CheckReasonFailed             = "check_failed"
	CheckReasonError              = "check_error"
	CheckReasonCancelled          = "check_cancelled"
	CheckReasonSkipped            = "check_skipped"
	CheckReasonWarning            = "check_warning"
	CheckReasonUnknownState       = "check_state_unknown"
	CheckReasonStaleHead          = "stale_head"
	CheckReasonStaleBase          = "stale_base"
	CheckReasonPolicyEmpty        = "policy_empty"
	CheckReasonDefinitionsChanged = "definitions_changed"
	CheckReasonEvidenceIncomplete = "evidence_incomplete"
	CheckReasonHidden             = "checks_hidden"
)

func validCheckReason(reason string) bool {
	switch reason {
	case CheckReasonPass, CheckReasonPending, CheckReasonFailed,
		CheckReasonError, CheckReasonCancelled, CheckReasonSkipped,
		CheckReasonWarning, CheckReasonUnknownState, CheckReasonStaleHead,
		CheckReasonStaleBase, CheckReasonPolicyEmpty,
		CheckReasonDefinitionsChanged, CheckReasonEvidenceIncomplete,
		CheckReasonHidden:
		return true
	default:
		return false
	}
}

// CheckResolution is the static maintainer-facing resolution for one check
// reason: what would resolve it. No free text enters resolutions.
func CheckResolution(reason string) string {
	switch reason {
	case CheckReasonPass:
		return "Every required check succeeded on the exact head."
	case CheckReasonPending:
		return "Wait for the pending checks to report; missing checks need a CI run on the exact head."
	case CheckReasonFailed:
		return "Correct the candidate so the failed check passes on a new head."
	case CheckReasonError:
		return "Resolve the check error and rerun CI on the exact head."
	case CheckReasonCancelled:
		return "Rerun the cancelled check on the exact head; cancellation is not a pass."
	case CheckReasonSkipped:
		return "A required check was skipped; run it on the exact head instead of waiving it."
	case CheckReasonWarning:
		return "Resolve the check warning so the check reports success on the exact head."
	case CheckReasonUnknownState:
		return "A required check reported an unrecognized state; rerun CI so it reports success."
	case CheckReasonStaleHead:
		return "The candidate moved; reassess the new head with fresh check evidence."
	case CheckReasonStaleBase:
		return "The target base moved; reverify the candidate against the new base."
	case CheckReasonPolicyEmpty:
		return "Configure a nonempty required-check set before automatic merge can proceed."
	case CheckReasonDefinitionsChanged:
		return "The approval definitions changed; a repository administrator must adopt the new set."
	case CheckReasonEvidenceIncomplete:
		return "Native check evidence is incomplete; reassess once the full set is readable."
	case CheckReasonHidden:
		return "The factory reader cannot see required check evidence; adjust native visibility."
	default:
		return ""
	}
}

// CheckTarget is the exact candidate and verified base one assessment
// binds: canonical repository scope, native PR linkage, and exact head/base
// commits. The observer resolves repository and assessor keys before access.
type CheckTarget struct {
	Repository int64  `json:"repository,string"`
	PRNumber   int64  `json:"pr_number,string"`
	PRID       int64  `json:"pr_id,string"`
	IssueID    int64  `json:"issue_id,string"`
	HeadRef    string `json:"head_ref"`
	BaseRef    string `json:"base_ref"`
	HeadOID    string `json:"head_oid"`
	BaseOID    string `json:"base_oid"`
}

// Validate rejects malformed targets. Shape only: observation verifies
// the PR linkage and tips against bracketed native evidence.
func (t CheckTarget) Validate() error {
	if t.Repository <= 0 || t.PRNumber <= 0 || t.PRID <= 0 || t.IssueID <= 0 {
		return errors.New("invalid check target scope")
	}
	if !ValidTargetBranch(t.HeadRef) || !ValidTargetBranch(t.BaseRef) || t.HeadRef == t.BaseRef {
		return errors.New("invalid check target refs")
	}
	if !ValidCommit(t.HeadOID) || !ValidCommit(t.BaseOID) {
		return errors.New("invalid check target commits")
	}
	return nil
}

// ChecksDigest binds one required-check set independent of order, so an
// adopted definition compares exactly against the current policy.
func ChecksDigest(checks []string) string {
	ordered := append([]string(nil), checks...)
	sort.Strings(ordered)
	sum := sha256.Sum256([]byte(strings.Join(ordered, "\x00")))
	return hex.EncodeToString(sum[:])
}

// AdoptedChecks are the approval definitions one attempt adopted: the
// policy revision and required-check set a repository administrator's
// adoption bound. Assessment refuses when the current policy differs.
type AdoptedChecks struct {
	Checks         []string `json:"checks"`
	PolicyRevision int64    `json:"policy_revision"`
	Digest         string   `json:"digest"`
}

// Validate rejects malformed adoptions. The set is nonempty, bounded and
// unique like the policy check set, and the digest binds exactly it.
func (a AdoptedChecks) Validate() error {
	if a.PolicyRevision < 0 {
		return errors.New("invalid adopted policy revision")
	}
	if len(a.Checks) == 0 || len(a.Checks) > MaxRequiredChecks {
		return errors.New("adopted checks require a nonempty bounded set")
	}
	seen := make(map[string]bool, len(a.Checks))
	for _, check := range a.Checks {
		if check == "" || len(check) > MaxCheckName || check != strings.TrimSpace(check) ||
			strings.IndexFunc(check, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || seen[check] {
			return errors.New("invalid adopted check name")
		}
		seen[check] = true
	}
	if !ValidDigest(a.Digest) || a.Digest != ChecksDigest(a.Checks) {
		return errors.New("adopted checks digest differs from their set")
	}
	return nil
}
