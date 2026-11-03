package factory

import (
	"errors"
)

// CheckAssessment is one recorded check verdict for an exact head and
// verified base: the adopted definitions, the bound native revision and
// the per-check results behind the verdict. ST12 consumes it as check
// evidence and must re-verify its head/base/policy bindings before use.
type CheckAssessment struct {
	Results          []CheckResult `json:"results"`
	Checks           []string      `json:"checks"`
	Repository       int64         `json:"repository,string"`
	PRNumber         int64         `json:"pr_number,string"`
	PRID             int64         `json:"pr_id,string"`
	IssueID          int64         `json:"issue_id,string"`
	PolicyRevision   int64         `json:"policy_revision"`
	NativeRev        int64         `json:"native_revision"`
	Revision         int64         `json:"revision"`
	AssessedUnix     int64         `json:"assessed_unix"`
	ObservedContexts int           `json:"observed_contexts"`
	HeadRef          string        `json:"head_ref"`
	BaseRef          string        `json:"base_ref"`
	HeadOID          string        `json:"head_oid"`
	BaseOID          string        `json:"base_oid"`
	ChecksDigest     string        `json:"checks_digest"`
	Verdict          string        `json:"verdict"`
	Reason           string        `json:"reason"`
}

// Validate rejects malformed assessments. A pass carries only passing
// results; any other verdict carries the results behind it.
func (a CheckAssessment) Validate() error {
	if err := a.validateCheckScope(); err != nil {
		return err
	}
	if err := a.validateCheckSet(); err != nil {
		return err
	}
	if err := a.validateCheckResults(); err != nil {
		return err
	}
	return a.validateCheckVerdict()
}

// validateCheckScope refuses assessments outside the admitted scope
// and binding.
func (a CheckAssessment) validateCheckScope() error {
	if a.Repository <= 0 || a.PRNumber <= 0 || a.PRID <= 0 || a.IssueID <= 0 {
		return errors.New("invalid check assessment scope")
	}
	if !ValidTargetBranch(a.HeadRef) || !ValidTargetBranch(a.BaseRef) || a.HeadRef == a.BaseRef {
		return errors.New("invalid check assessment refs")
	}
	if !ValidCommit(a.HeadOID) || !ValidCommit(a.BaseOID) {
		return errors.New("invalid check assessment commits")
	}
	if a.PolicyRevision < 0 || a.NativeRev < 1 || a.Revision < 0 || a.AssessedUnix <= 0 || a.ObservedContexts < 0 {
		return errors.New("invalid check assessment binding")
	}
	return nil
}

// validateCheckSet refuses empty, oversized, unnamed, duplicate or
// digest-divergent check sets.
func (a CheckAssessment) validateCheckSet() error {
	if len(a.Checks) == 0 || len(a.Checks) > MaxRequiredChecks {
		return errors.New("check assessment requires its nonempty check set")
	}
	seen := make(map[string]bool, len(a.Checks))
	for _, check := range a.Checks {
		if check == "" || len(check) > MaxCheckName || seen[check] {
			return errors.New("invalid check assessment check name")
		}
		seen[check] = true
	}
	if !ValidDigest(a.ChecksDigest) || a.ChecksDigest != ChecksDigest(a.Checks) {
		return errors.New("check assessment digest differs from its set")
	}
	return nil
}

// validateCheckResults refuses results that diverge from the admitted
// check set.
func (a CheckAssessment) validateCheckResults() error {
	if !ValidCheckVerdict(a.Verdict) || !validCheckReason(a.Reason) {
		return errors.New("invalid check assessment verdict")
	}
	if len(a.Results) != len(a.Checks) {
		return errors.New("check assessment results differ from its set")
	}
	for i, result := range a.Results {
		if err := result.Validate(); err != nil {
			return err
		}
		if result.Context != a.Checks[i] {
			return errors.New("check assessment results differ from its set")
		}
	}
	return nil
}

// validateCheckVerdict refuses verdicts whose reason or results
// disagree with the verdict.
func (a CheckAssessment) validateCheckVerdict() error {
	switch a.Verdict {
	case CheckPass:
		return validatePassedAssessment(a)
	case CheckPending:
		if a.Reason != CheckReasonPending {
			return errors.New("pending check assessment names its reason")
		}
	case CheckFailed:
		switch a.Reason {
		case CheckReasonFailed, CheckReasonError, CheckReasonCancelled,
			CheckReasonSkipped, CheckReasonWarning, CheckReasonUnknownState:
		default:
			return errors.New("failed check assessment names its failure")
		}
	case CheckRefused:
		switch a.Reason {
		case CheckReasonStaleHead, CheckReasonStaleBase, CheckReasonPolicyEmpty,
			CheckReasonDefinitionsChanged, CheckReasonEvidenceIncomplete,
			CheckReasonHidden:
		default:
			return errors.New("refused check assessment names its cause")
		}
	}
	return nil
}

// validatePassedAssessment refuses passing verdicts with a divergent
// reason or any non-passing result.
func validatePassedAssessment(a CheckAssessment) error {
	if a.Reason != CheckReasonPass {
		return errors.New("check pass names its reason")
	}
	for _, result := range a.Results {
		if !result.Passed {
			return errors.New("check pass carries only passing results")
		}
	}
	return nil
}

// VerifyChecks evaluates the adopted required-check set against one
// bracketed native observation of the exact head and verified base. The
// current policy must still carry exactly the adopted definitions;
// anything else refuses without consulting agent claims, which have no
// input here. Malformed inputs error; every semantic outcome is a
// recorded verdict, including refusals.
func VerifyChecks(target CheckTarget, adopted AdoptedChecks, current RepositoryPolicy, observed ObservedChecks, nowUnix int64) (CheckAssessment, error) {
	var empty CheckAssessment
	if err := target.Validate(); err != nil {
		return empty, err
	}
	if err := adopted.Validate(); err != nil {
		return empty, err
	}
	if err := observed.Validate(); err != nil {
		return empty, err
	}
	if nowUnix <= 0 {
		return empty, errors.New("check assessment needs its record time")
	}
	assessment := CheckAssessment{
		Checks: adopted.Checks, Repository: target.Repository,
		PRNumber: target.PRNumber, PRID: target.PRID, IssueID: target.IssueID,
		PolicyRevision: adopted.PolicyRevision, NativeRev: observed.NativeRev,
		AssessedUnix: nowUnix, ObservedContexts: observed.ObservedContexts,
		HeadRef: target.HeadRef, BaseRef: target.BaseRef,
		HeadOID: target.HeadOID, BaseOID: target.BaseOID,
		ChecksDigest: adopted.Digest,
	}
	for _, check := range adopted.Checks {
		assessment.Results = append(assessment.Results, CheckResult{Context: check})
	}
	// A missing policy arrives as the zero record: zero configured checks
	// can never authorize a merge.
	if current.Repository != target.Repository || len(current.Checks) == 0 {
		assessment.Verdict, assessment.Reason = CheckRefused, CheckReasonPolicyEmpty
		return assessment, nil
	}
	if current.Revision != adopted.PolicyRevision || ChecksDigest(current.Checks) != adopted.Digest {
		assessment.Verdict, assessment.Reason = CheckRefused, CheckReasonDefinitionsChanged
		return assessment, nil
	}
	if !observed.Complete {
		assessment.Verdict, assessment.Reason = CheckRefused, CheckReasonEvidenceIncomplete
		return assessment, nil
	}
	if observed.Hidden {
		assessment.Verdict, assessment.Reason = CheckRefused, CheckReasonHidden
		return assessment, nil
	}
	if observed.HeadTip != target.HeadOID {
		assessment.Verdict, assessment.Reason = CheckRefused, CheckReasonStaleHead
		return assessment, nil
	}
	if observed.BaseTip != target.BaseOID {
		assessment.Verdict, assessment.Reason = CheckRefused, CheckReasonStaleBase
		return assessment, nil
	}
	latest := make(map[string]string, len(observed.Checks))
	for _, check := range observed.Checks {
		latest[check.Context] = check.State
	}
	worst, reason := 0, CheckReasonPass
	for i, result := range assessment.Results {
		state := latest[result.Context]
		assessment.Results[i].State = state
		assessment.Results[i].Passed = state == CheckStateSuccess
		if rank, cause := checkStateSeverity(state); rank > worst {
			worst, reason = rank, cause
		}
	}
	switch {
	case worst == 0:
		assessment.Verdict, assessment.Reason = CheckPass, CheckReasonPass
	case worst <= 10:
		assessment.Verdict, assessment.Reason = CheckPending, CheckReasonPending
	default:
		assessment.Verdict, assessment.Reason = CheckFailed, reason
	}
	return assessment, nil
}
