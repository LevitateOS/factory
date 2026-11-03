package factory

import "errors"

// CurrentReviewContext is the exact review/correction input granted by its
// recorded cycle. Carried findings come from the existing canonical collector;
// no coding conversation or earlier worker prompt is an input.
type CurrentReviewContext struct {
	Publication         CurrentPublicationContext  `json:"publication"`
	AdmittedTuple       CandidateTuple             `json:"admitted_tuple"`
	CurrentTuple        *CandidateTuple            `json:"current_tuple"`
	Cycle               ReviewCycle                `json:"cycle"`
	CarriedFindings     []Finding                  `json:"carried_findings"`
	Assessments         []CurrentFindingAssessment `json:"finding_assessments"`
	Corrections         []CurrentCorrectionFact    `json:"correction_facts"`
	Heads               []CurrentReviewHead        `json:"review_heads"`
	ClearCoverageDigest string                     `json:"clear_coverage_digest,omitempty"`
	Implementation      *CurrentPlanContext        `json:"implementation,omitempty"`
	CheckAssessment     *CheckAssessment           `json:"check_assessment,omitempty"`
}

type CurrentFindingAssessment struct {
	CycleID  string `json:"cycle_id"`
	Key      string `json:"key"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type CurrentCorrectionFact struct {
	CycleID      string   `json:"cycle_id"`
	RunID        string   `json:"run_id"`
	SetDigest    string   `json:"set_digest"`
	NewCandidate string   `json:"new_candidate"`
	Key          string   `json:"key"`
	Disposition  string   `json:"disposition"`
	ChangedPaths []string `json:"changed_paths"`
	Commits      []string `json:"commits"`
}

type CurrentReviewHead struct {
	CycleID     string                 `json:"cycle_id"`
	RunID       string                 `json:"run_id"`
	OperationID string                 `json:"operation_id"`
	HeadOID     string                 `json:"head_oid"`
	BaseOID     string                 `json:"base_oid"`
	Event       string                 `json:"event"`
	Operation   CurrentReviewOperation `json:"operation"`
}

type CurrentReviewOperation struct {
	Effect       string `json:"effect"`
	Cancellation string `json:"cancellation"`
	Completion   string `json:"completion"`
	Reason       string `json:"reason"`
	NotObserved  bool   `json:"not_observed"`
}

type CurrentPublicationContext struct {
	ID           string `json:"id"`
	AssignmentID string `json:"assignment_id"`
	Revision     int64  `json:"revision"`
	PRID         int64  `json:"pr_id"`
	PRNumber     int64  `json:"pr_number"`
	Stage        string `json:"stage"`
}

func (c CurrentReviewContext) ValidateFor(a Assignment) error {
	if c.Publication.AssignmentID != a.PublicationAssignment || c.Cycle.PublicationID != c.Publication.ID || c.Cycle.AttemptRoot != a.AttemptRoot || c.Cycle.Validate() != nil || c.AdmittedTuple.Validate() != nil || c.AdmittedTuple.Head != a.SourceCommit || c.Cycle.Head != a.SourceCommit || len(c.CarriedFindings) > MaxOutstandingFindings {
		return errors.New("required review context differs from assigned cycle")
	}
	if (a.Phase == "review" && c.Cycle.ReviewerAssignment != a.ID) || (a.Phase == "correct" && c.Cycle.CorrectionAssignment != a.ID) || (a.Phase != "review" && a.Phase != "correct") {
		return errors.New("required review context is not granted to this role")
	}
	seen := map[string]bool{}
	for _, finding := range c.CarriedFindings {
		if finding.Validate() != nil || seen[finding.Key] {
			return errors.New("required review context finding set is malformed")
		}
		seen[finding.Key] = true
	}
	return nil
}
