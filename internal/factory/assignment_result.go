package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

// AssignmentResult extends the retained harness result contract with its
// dispatch binding: which assignment attempt produced it, whether the
// harness reported it or the supervisor synthesized it from the observed
// run outcome, and when it was recorded. A reported candidate is
// syntax-checked only; publication validates it against fresh native
// observations before use.
type AssignmentResult struct {
	Review       *ReviewReport `json:"review,omitempty"`
	PhaseResult  *PhaseResult  `json:"phase_result,omitempty"`
	AssignmentID string        `json:"assignment_id"`
	RunID        string        `json:"run_id"`
	Phase        string        `json:"phase,omitempty"`
	Status       string        `json:"status"`
	Summary      string        `json:"summary"`
	Candidate    string        `json:"candidate"`
	Findings     []string      `json:"findings"`
	ReviewPassed bool          `json:"review_passed"`
	Reported     bool          `json:"reported"`
	RecordedUnix int64         `json:"recorded_unix"`
}

// Validate extends Result validation with the dispatch binding.
func (r AssignmentResult) Validate() error {
	if !ValidID(r.AssignmentID) || !ValidID(r.RunID) {
		return errors.New("invalid result dispatch binding")
	}
	if r.RecordedUnix <= 0 {
		return errors.New("invalid result record time")
	}
	if r.Review != nil {
		if err := r.Review.Validate(); err != nil {
			return err
		}
	}
	if r.PhaseResult != nil {
		if err := r.PhaseResult.Validate(); err != nil {
			return err
		}
	}
	if err := (Result{
		Status: r.Status, Summary: r.Summary, Candidate: r.Candidate,
		ReviewPassed: r.ReviewPassed, Findings: r.Findings,
	}.Validate()); err != nil {
		// Completed candidate-free phases expose no candidate: their
		// typed shapes (plan, decomposition, reassessment, review
		// verdict) carry no candidate field, so such a result can never
		// authorize effects. Completed corrections nominate their
		// resulting tree and validate as ordinary candidate results.
		// The exemption covers exactly the candidate rule; every other
		// shape rule still refuses.
		if r.Candidate != "" || r.Status != "completed" || !isCandidateFreePhaseResult(r.PhaseResult) {
			return err
		}
		if len(r.Summary) > 16<<10 || len(r.Findings) > 64 {
			return err
		}
		for _, finding := range r.Findings {
			if len(finding) > 4096 {
				return err
			}
		}
	}
	return nil
}

// isCandidateFreePhaseResult reports whether phased output belongs to a
// phase whose completed result never names a publishable candidate:
// investigate plans, chosen decompositions, reassessment reports, typed
// review verdicts, repair reports and app verification reports.
// Completed corrections nominate their resulting tree, so the correct
// phase left this exemption with the phased dispatch_result.go consumer
// and the typed correction payload. F7 joint cutover with the phased
// dispatch_result.go consumer and the PhaseOutput typed payloads;
// decompose arrived first, review rides the exemption, and repair/verify
// extend it to stall, delivery-repair and delivery subjects.
func isCandidateFreePhaseResult(phased *PhaseResult) bool {
	if phased == nil {
		return false
	}
	switch phased.Phase {
	case project.ExecutionInvestigate, project.ExecutionDecompose, project.ExecutionReassess,
		project.ExecutionReview,
		project.ExecutionRepair, project.ExecutionVerify:
		return true
	default:
		return false
	}
}

// ValidatePhaseBinding enforces the phase/role/result contract: the
// recorded phase matches the assignment phase and its admitted role,
// and the typed payload binds that same phase. Cross-phase payloads
// refuse; admission always records PhaseResult.
func (r AssignmentResult) ValidatePhaseBinding(role, assignmentPhase string) error {
	if r.PhaseResult == nil {
		return errors.New("phased result lacks its typed output")
	}
	if r.Phase != assignmentPhase {
		return errors.New("phased result does not match its assignment phase")
	}
	if !ValidPhaseRole(r.Phase, role) {
		return errors.New("phased result role does not match its phase")
	}
	if r.PhaseResult.Phase != r.Phase {
		return errors.New("phased result payload does not match its phase")
	}
	if err := r.PhaseResult.Validate(); err != nil {
		return err
	}
	if r.Phase == project.ExecutionCorrect {
		if r.Status != r.PhaseResult.Status {
			return errors.New("recorded correction status differs from its typed result")
		}
		candidate := ""
		if r.Status == "completed" {
			candidate = r.PhaseResult.Output.Correction.NewCandidate
		}
		if r.Candidate != candidate {
			return errors.New("recorded correction candidate differs from its typed result")
		}
	}
	return nil
}

// ValidateForAssignment enforces the role-specific result contract. A
// reviewer report is retained only for reviewer assignments and is bound to
// the exact candidate the assignment reviewed. A phased reviewer result
// carries the same binding through its typed v2 verdict instead: exactly
// one verdict payload, no candidate, no legacy string findings.
func (r AssignmentResult) ValidateForAssignment(role, sourceCommit string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if role == project.RoleReviewer {
		if r.Reported {
			return checkReportedReviewerResult(r, sourceCommit)
		}
		return checkSynthesizedReviewerResult(r)
	}
	if r.Review != nil {
		return errors.New("non-reviewer result carries a review report")
	}
	return checkRepairResultHead(r, sourceCommit)
}

func checkReportedReviewerResult(r AssignmentResult, sourceCommit string) error {
	if r.Review == nil && isPhasedReviewVerdict(r, sourceCommit) {
		return nil
	}
	if r.Review == nil && isPhasedVerifyVerdict(r) {
		return nil
	}
	if r.Review == nil || r.Status != "completed" || r.Candidate != sourceCommit || r.ReviewPassed || len(r.Findings) != 0 || r.Summary != r.Review.Summary {
		return errors.New("reported reviewer result does not match its candidate")
	}
	return nil
}

func checkSynthesizedReviewerResult(r AssignmentResult) error {
	if r.Review != nil || r.Status == "completed" || r.Candidate != "" || r.ReviewPassed || len(r.Findings) != 0 {
		return errors.New("synthesized reviewer result carries report evidence")
	}
	return nil
}

func checkRepairResultHead(r AssignmentResult, sourceCommit string) error {
	if r.PhaseResult != nil && r.PhaseResult.Phase == project.ExecutionRepair &&
		r.PhaseResult.Status == "completed" && r.PhaseResult.Output != nil &&
		r.PhaseResult.Output.Repair != nil &&
		r.PhaseResult.Output.Repair.SourceCommit != sourceCommit {
		return errors.New("stall repair result runs a head its assignment was not admitted against")
	}
	return nil
}

// isPhasedReviewVerdict reports whether a reported result is a phased
// reviewer result: completed, bound to the review phase with exactly one
// typed verdict payload, and carrying no legacy reviewer evidence. An
// explicit clear additionally binds the exact reviewed candidate: the
// reviewer finished on the assignment's source commit, never another
// head. Cycle/reviewer/carried-set binding belongs to the consumer,
// which sees the publication, its cycles and the canonical findings;
// deep verdict shape stays with PhaseBinding at finish. This gate only
// admits the phased wire so legacy pins keep refusing.
func isPhasedReviewVerdict(r AssignmentResult, sourceCommit string) bool {
	if r.PhaseResult == nil || r.PhaseResult.Phase != project.ExecutionReview ||
		r.Phase != project.ExecutionReview || r.Status != "completed" ||
		r.Candidate != "" || r.ReviewPassed || len(r.Findings) != 0 ||
		r.Summary != r.PhaseResult.Summary {
		return false
	}
	output := r.PhaseResult.Output
	if output == nil {
		return false
	}
	if (output.Review == nil) == (output.Clear == nil) {
		return false
	}
	if output.Clear != nil && output.Clear.Head != sourceCommit {
		return false
	}
	return true
}

// isPhasedVerifyVerdict reports whether a reported result is a phased
// verification result: completed, bound to the verify phase with exactly
// its app verification report, and carrying no legacy reviewer evidence
// and no candidate. Verification never authorizes publication, so unlike
// the explicit-clear review verdict there is no source-commit binding:
// the delivery and manifest binding rides assignment admission.
func isPhasedVerifyVerdict(r AssignmentResult) bool {
	if r.PhaseResult == nil || r.PhaseResult.Phase != project.ExecutionVerify ||
		r.Phase != project.ExecutionVerify || r.Status != "completed" ||
		r.Candidate != "" || r.ReviewPassed || len(r.Findings) != 0 ||
		r.Summary != r.PhaseResult.Summary {
		return false
	}
	output := r.PhaseResult.Output
	if output == nil || output.Verification == nil {
		return false
	}
	return output.nilTypedOutputExceptVerification()
}

// ToResult returns the retained harness result shape.
func (r AssignmentResult) ToResult() Result {
	return Result{
		Status: r.Status, Summary: r.Summary, Candidate: r.Candidate,
		ReviewPassed: r.ReviewPassed, Findings: r.Findings,
	}
}

// ResultFromHarness binds one validated harness-reported result to its
// attempt. Reported results arrive through the fenced report block only.
func ResultFromHarness(assignmentID, runID string, reported Result, recordedUnix int64) AssignmentResult {
	return AssignmentResult{
		AssignmentID: assignmentID, RunID: runID,
		Status: reported.Status, Summary: reported.Summary, Candidate: reported.Candidate,
		Findings: reported.Findings, ReviewPassed: reported.ReviewPassed,
		Reported: true, RecordedUnix: recordedUnix,
	}
}

// ResultDigest derives the deterministic identity input for one recorded
// attempt result: the hex SHA-256 over its canonical encoding. Automatic
// phase advancement keys on (parent_assignment, result digest, next phase),
// so the same completed parent replays to the same child instead of
// launching twice.
func ResultDigest(r AssignmentResult) string {
	encoded, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ResultSynthesized binds one supervisor-derived result to its attempt.
// The supervisor maps the observed host outcome when the harness reports
// nothing parseable; the candidate stays empty until validated.
func ResultSynthesized(assignmentID, runID, status, summary string, recordedUnix int64) AssignmentResult {
	return AssignmentResult{
		AssignmentID: assignmentID, RunID: runID,
		Status: status, Summary: summary, Findings: []string{},
		Reported: false, RecordedUnix: recordedUnix,
	}
}
