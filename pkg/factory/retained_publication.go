package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/project"
)

const CommandPublishRetained = "publish-retained-candidate"

// RetainedPublication identifies the original execution without pretending it
// ran again. NotAfter bounds only the new branch and PR creation authority.
// Review, correction and merge require their own current finite allowance.
type RetainedPublication struct {
	Publication string `json:"publication"`
	Assignment  string `json:"assignment"`
	Run         string `json:"run"`
	Candidate   string `json:"candidate"`
	Connection  string `json:"connection"`
	NotAfter    int64  `json:"not_after"`
}

func (a Assignment) validateRetainedPublication() error {
	r := a.RetainedPublication
	if r == nil || !ValidID(r.Publication) || !ValidID(r.Assignment) || !ValidID(r.Run) ||
		!ValidCommit(r.Candidate) || r.Candidate == a.SourceCommit || r.Connection == "" || len(r.Connection) > 128 || r.NotAfter <= a.CreatedUnix || r.NotAfter > a.CreatedUnix+300 ||
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
