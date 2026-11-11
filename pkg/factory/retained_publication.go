package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/project"
)

const CommandPublishRetained = "publish-retained-candidate"
const CommandFundRetained = "fund-retained-candidate"
const CommandRetryReview = "retry-unsent-review"

// ReviewRetry authorizes a provider submission of an existing reviewer result.
// It has no model allowance and cannot replace an uncertain send.
type ReviewRetry struct {
	Publication string `json:"publication"`
	Run string `json:"run"`
	Operation string `json:"operation"`
	Head string `json:"head"`
	Base string `json:"base"`
	Policy int64 `json:"policy"`
}

func (r ReviewRetry) Validate() error {
	if !ValidID(r.Publication) || !ValidID(r.Run) || !ValidPublicationOperationID(r.Operation) ||
		!ValidCommit(r.Head) || !ValidCommit(r.Base) || r.Head == r.Base || r.Policy < 1 {
		return errors.New("invalid unsent review retry scope")
	}
	return nil
}

func (op ReviewOperation) DefinitiveNoSend() bool {
	o := op.Outcome
	return !o.NotObserved && o.OperationID == op.Work.OperationID && o.InstallationID != "" &&
		o.Kind == OpReviewSubmit && o.RepositoryID == op.Work.Repository && o.ActorID == op.Work.ActorID &&
		o.Effect == OpEffectNotCommitted && o.Completion == OpCompletionComplete
}

// RetainedCandidateFunding authorizes only subsequent review/correction on the
// original selected source. It never renews publication or replays execution.
type RetainedCandidateFunding struct {
	Native       *NativeSponsorship `json:"native,omitempty"`
	Publication  string             `json:"publication"`
	PreviousRoot string             `json:"previous_root,omitempty"`
	CarryUnused  bool               `json:"carry_unused,omitempty"`
	Candidate    string             `json:"candidate"`
	Base         string             `json:"base"`
	Acceptance   string             `json:"acceptance"`
	Connection   string             `json:"connection"`
	GrantID      string             `json:"grant_id"`
	Generation   int64              `json:"generation"`
	Policy       int64              `json:"policy"`
	Requirements string             `json:"requirements,omitempty"`
	Approval     string             `json:"approval,omitempty"`
	Limits       AttemptLimits      `json:"limits"`
}

func (f RetainedCandidateFunding) Validate() error {
	if (f.Requirements != "" || f.Approval != "") && (!project.ValidDecisionID(f.Requirements) || !project.ValidDecisionID(f.Approval)) {
		return errors.New("invalid continuation setup capture")
	}
	if f.CarryUnused && f.PreviousRoot == "" {
		return errors.New("unused review budget requires its prior owner")
	}
	if f.PreviousRoot != "" && !ValidID(f.PreviousRoot) {
		return errors.New("continuation must capture a valid previous owner")
	}
	if !ValidID(f.Publication) || !ValidCommit(f.Candidate) || !ValidCommit(f.Base) || f.Candidate == f.Base || !project.ValidDecisionID(f.Acceptance) || f.Connection == "" || len(f.Connection) > 128 || f.Policy < 1 {
		return errors.New("invalid retained candidate funding scope")
	}
	if f.Native != nil {
		if f.Native.Validate() != nil || !ValidID(f.Connection) || f.GrantID != "" || f.Generation != 0 {
			return errors.New("invalid native continuation source")
		}
	} else if f.GrantID == "" || len(f.GrantID) > 128 || f.Generation < 1 {
		return errors.New("invalid broker continuation source")
	}
	return f.Limits.Validate()
}

func (f RetainedCandidateFunding) MatchesSource(s Sponsorship) bool {
	return f.Connection == s.Connection && f.GrantID == s.GrantID && f.Generation == s.Generation &&
		(f.Native == nil && s.Native == nil || f.Native != nil && s.Native != nil && *f.Native == *s.Native)
}

type RetainedCandidateFundingReceipt struct {
	CommandID string                   `json:"command_id"`
	Funding   RetainedCandidateFunding `json:"funding"`
}

// PublicationOwnerReady recognizes an explicit non-executing owner without
// inventing a successful run. Dispatch still requires its separate allowance.
func (a Assignment) PublicationOwnerReady() bool {
	return a.ExternalPublication != nil && a.validateExternalPublicationOwner() == nil || a.RetainedPublication != nil && a.validateRetainedPublication() == nil || a.Succeeded()
}

// RetainedPublication identifies the original execution without pretending it
// ran again. NotAfter bounds only the new branch and PR creation authority.
// Review, correction and merge require their own current finite allowance.
type RetainedPublication struct {
	// AdoptedFrom admits review of an already published candidate. It carries
	// no branch/PR send authority; the prior complete receipts remain exact.
	AdoptedFrom string `json:"adopted_from,omitempty"`
	Publication string `json:"publication"`
	Assignment  string `json:"assignment"`
	Run         string `json:"run"`
	Candidate   string `json:"candidate"`
	Connection  string `json:"connection"`
	NotAfter    int64  `json:"not_after"`
}

func (a Assignment) validateRetainedPublication() error {
	r := a.RetainedPublication
	if r != nil && (r.AdoptedFrom == "" && (r.NotAfter <= a.CreatedUnix || r.NotAfter > a.CreatedUnix+300) || r.AdoptedFrom != "" && (!ValidID(r.AdoptedFrom) || r.AdoptedFrom == a.ID || r.NotAfter != 0)) {
		return errors.New("invalid retained publication send authority")
	}
	if a.ExternalPublication != nil || r == nil || !ValidID(r.Publication) || !ValidID(r.Assignment) || !ValidID(r.Run) ||
		!ValidCommit(r.Candidate) || r.Candidate == a.SourceCommit || r.Connection == "" || len(r.Connection) > 128 ||
		!ValidID(a.ID) || a.AttemptRoot != a.ID || a.PublicationAssignment != a.ID || a.ParentAssignment != "" ||
		a.ID == r.Assignment || a.Stage != AssignmentRetained || a.Revision != 0 || a.ActorID <= 0 || a.CreatedUnix <= 0 ||
		!ValidProjectID(a.ProjectID) || a.Role != project.RoleCoder || !ValidPreparationRef(a.Preparation) ||
		!ValidCommit(a.SourceCommit) || a.Subject.Kind != SubjectIssue || a.Subject.Validate() != nil || checkIssueAssignmentScope(a) != nil ||
		a.NativeRev < 1 || a.Phase != "" || a.Run != "" || len(a.RunHistory) != 0 || a.Attempts != 0 ||
		a.Result != nil || len(a.Prompt) != 0 || a.PromptSHA != "" || a.Model != "" || a.Connection != "" ||
		a.Harness != "" || a.HarnessVers != "" || a.NativeTool != nil || a.AdoptedHarness != (project.FactoryHarnessPin{}) ||
		a.Outcome != "" || a.Reason != "" || a.FinishedUnix != 0 || a.AcceptedContext != nil || a.ReviewContext != nil || a.PlanningContext != nil {
		return errors.New("invalid non-executing retained publication owner")
	}
	return nil
}
