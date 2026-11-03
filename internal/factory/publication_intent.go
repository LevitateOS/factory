package factory

import (
	"errors"
	"fmt"
	"strings"
)

// PublicationWork is one executor call's exact bound inputs: the candidate
// bundle with its run/base identities, the policy-bound target and actor,
// the bracketed observation, the final PR title and body, and the
// persisted operation identity the call must use. No credential or
// credential-adjacent value enters the work record; secrets travel in
// restricted files outside it. Repository, ActorID and CorrectionAuthor are
// canonical Factory keys; provider adapters resolve their native subjects.
type PublicationWork struct {
	Bundle           []byte
	AssignmentID     string
	Publication      string
	RunID            string
	Run              Run
	Candidate        string
	BaseSHA          string
	TargetBranch     string
	OperationID      string
	AuthRevision     string
	ExpectedOld      string
	ComparisonRef    string
	ComparisonOID    string
	PRTitle          string
	PRBody           string
	Repository       int64
	Issue            int64
	ActorID          int64
	NativeRev        int64
	NotAfter         int64
	CorrectionNumber int64
	CorrectionAuthor int64
}

// MaxPublicationBody bounds the final PR body: attributable references
// only, never unbounded native text.
const MaxPublicationBody = 16 << 10

// PublicationIntent contains only the immutable native inputs. Bundles,
// supervisor credentials and changing run state stay outside this record.
type PublicationIntent struct {
	OperationID      string `json:"operation_id"`
	AuthRevision     string `json:"authorization_revision"`
	TargetBranch     string `json:"target_branch"`
	Candidate        string `json:"candidate"`
	ExpectedOld      string `json:"expected_old"`
	ComparisonRef    string `json:"comparison_ref"`
	ComparisonOID    string `json:"comparison_oid"`
	PRTitle          string `json:"pr_title"`
	PRBody           string `json:"pr_body"`
	Repository       int64  `json:"repository,string"`
	ActorID          int64  `json:"actor_id,string"`
	NativeRev        int64  `json:"native_revision"`
	NotAfter         int64  `json:"not_after"`
	CorrectionNumber int64  `json:"correction_number,omitempty"`
	CorrectionAuthor int64  `json:"correction_author,omitempty"`
}

// Intent takes the credential-free native inputs from an executor call.
func (w PublicationWork) Intent() PublicationIntent {
	return PublicationIntent{
		OperationID: w.OperationID, AuthRevision: w.AuthRevision,
		TargetBranch: w.TargetBranch, Candidate: w.Candidate, ExpectedOld: w.ExpectedOld,
		ComparisonRef: w.ComparisonRef, ComparisonOID: w.ComparisonOID,
		PRTitle: w.PRTitle, PRBody: w.PRBody, Repository: w.Repository, ActorID: w.ActorID,
		NativeRev: w.NativeRev, NotAfter: w.NotAfter,
		CorrectionNumber: w.CorrectionNumber, CorrectionAuthor: w.CorrectionAuthor,
	}
}

// Apply restores an exact native intent without changing local bundle/run inputs.
func (i PublicationIntent) Apply(w PublicationWork) PublicationWork {
	w.OperationID, w.AuthRevision = i.OperationID, i.AuthRevision
	w.TargetBranch, w.Candidate, w.ExpectedOld = i.TargetBranch, i.Candidate, i.ExpectedOld
	w.ComparisonRef, w.ComparisonOID = i.ComparisonRef, i.ComparisonOID
	w.PRTitle, w.PRBody, w.Repository, w.ActorID = i.PRTitle, i.PRBody, i.Repository, i.ActorID
	w.NativeRev, w.NotAfter = i.NativeRev, i.NotAfter
	w.CorrectionNumber, w.CorrectionAuthor = i.CorrectionNumber, i.CorrectionAuthor
	return w
}

// Validate rejects incomplete immutable native intents.
func (i PublicationIntent) Validate() error { return i.validate(true) }

func (i PublicationIntent) validate(observed bool) error {
	if err := i.checkPublicationIntentScope(); err != nil {
		return err
	}
	if err := i.checkPublicationIntentBinding(observed); err != nil {
		return err
	}
	return i.checkPublicationIntentContent(observed)
}

func (i PublicationIntent) checkPublicationIntentScope() error {
	if !ValidTargetBranch(i.TargetBranch) || !ValidCommit(i.Candidate) {
		return errors.New("invalid publication target or candidate")
	}
	if !ValidPublicationOperationID(i.OperationID) {
		return errors.New("invalid publication operation identity")
	}
	if i.Repository <= 0 || i.ActorID <= 0 {
		return errors.New("invalid publication work scope")
	}
	return nil
}

func (i PublicationIntent) checkPublicationIntentBinding(observed bool) error {
	if i.AuthRevision == "" || len(i.AuthRevision) > 512 {
		return errors.New("invalid publication authorization revision")
	}
	if i.NativeRev < 0 || i.NotAfter < 0 || (observed && (i.NativeRev == 0 || i.NotAfter == 0)) {
		return errors.New("invalid publication observation binding")
	}
	return nil
}

func (i PublicationIntent) checkPublicationIntentContent(observed bool) error {
	if i.ExpectedOld != "absent" && !ValidCommit(i.ExpectedOld) {
		return errors.New("invalid publication expected lease")
	}
	if !ValidTargetBranch(i.ComparisonRef) || ((observed || i.ComparisonOID != "") && !ValidCommit(i.ComparisonOID)) {
		return errors.New("invalid publication comparison binding")
	}
	if strings.TrimSpace(i.PRTitle) == "" || len(strings.TrimSpace(i.PRTitle)) > 255 {
		return errors.New("invalid publication PR title")
	}
	if len(i.PRBody) > MaxPublicationBody {
		return errors.New("publication PR body exceeds bounds")
	}
	if (i.CorrectionNumber == 0) != (i.CorrectionAuthor == 0) || i.CorrectionNumber < 0 || i.CorrectionAuthor < 0 {
		return errors.New("invalid publication correction binding")
	}
	if i.CorrectionNumber != 0 && i.ExpectedOld == "absent" {
		return errors.New("initial publication carries no correction")
	}
	return nil
}

// ValidateObservation checks candidate and identity inputs before native refs
// are observed; only the native revision and comparison tip may be absent.
func (w PublicationWork) ValidateObservation() error {
	if len(w.Bundle) == 0 || len(w.Bundle) > 4<<20 {
		return errors.New("publication bundle exceeds input limit")
	}
	if !ValidID(w.AssignmentID) || !ValidID(w.Publication) || !ValidID(w.RunID) || w.Issue <= 0 {
		return errors.New("invalid publication work identity")
	}
	if err := w.Run.Validate(); err != nil || w.Run.ID != w.RunID {
		return errors.New("invalid publication run")
	}
	if !ValidCommit(w.BaseSHA) || w.Candidate == w.Run.InputSHA || w.Candidate == w.BaseSHA {
		return errors.New("publication candidate is not a fresh exact commit")
	}
	if w.ComparisonRef == PublishBranchName(w.AssignmentID) {
		return errors.New("publication comparison is its own target")
	}
	return w.Intent().validate(false)
}

// Validate adds the exact native observation needed before submission.
func (w PublicationWork) Validate() error {
	if err := w.ValidateObservation(); err != nil {
		return err
	}
	return w.Intent().Validate()
}

// PRTitleFor derives the deterministic attributable PR title for one
// assignment's publication: the issue it answers, never untrusted text.
func PRTitleFor(issue int64) string {
	return fmt.Sprintf("Factory candidate for #%d", issue)
}

// PRBodyFor derives the deterministic attributable PR body: the exact
// bound references behind the candidate.
func PRBodyFor(issue int64, assignmentID, acceptance, runID, candidate, source string) string {
	if issue <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Automated factory publication.\n\n")
	b.WriteString(fmt.Sprintf("Closes #%d\n\n", issue))
	b.WriteString("Assignment: " + assignmentID + "\n")
	b.WriteString("Acceptance: " + acceptance + "\n")
	b.WriteString("Run: " + runID + "\n")
	b.WriteString("Candidate: " + candidate + "\n")
	b.WriteString("Source: " + source + "\n")
	return b.String()
}
