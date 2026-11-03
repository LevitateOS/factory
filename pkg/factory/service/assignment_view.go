package service

import (
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

// factoryAssignmentResult renders the recorded dispatch result: the
// harness status, summary, candidate reference and findings, plus
// whether the harness reported it or the supervisor synthesized it from
// the observed run outcome.
type factoryAssignmentResult struct {
	Findings     []string `json:"findings"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Candidate    string   `json:"candidate"`
	RunID        string   `json:"run_id"`
	ReviewPassed bool     `json:"review_passed"`
	Reported     bool     `json:"reported"`
	RecordedUnix int64    `json:"recorded_unix"`
}

// factoryAssignmentReservation renders the assignment's held capacity:
// its state and the planned minutes its deadline implied. Only
// confirmed actual usage is ever charged; the plan is not a charge.
type factoryAssignmentReservation struct {
	Connection     string `json:"connection"`
	State          string `json:"state"`
	PlannedMinutes int    `json:"planned_minutes"`
}

// AssignmentView renders one dispatch assignment with its exact
// bound inputs and recorded result. The prompt carries accepted native
// text plus IDs and digests only: the builder takes no credential
// input, so the recorded bytes stay inspectable without credentials.
type AssignmentView struct {
	Result       *factoryAssignmentResult      `json:"result,omitempty"`
	Reservation  *factoryAssignmentReservation `json:"reservation,omitempty"`
	Publication  *PublicationOperations        `json:"publication,omitempty"`
	Authority    factory.AuthorityRef          `json:"authority"`
	RunHistory   []string                      `json:"run_history"`
	ID           string                        `json:"id"`
	ProjectID    string                        `json:"project_id"`
	Role         string                        `json:"role"`
	Acceptance   string                        `json:"acceptance"`
	Preparation  string                        `json:"preparation"`
	Harness      string                        `json:"harness"`
	HarnessVers  string                        `json:"harness_version"`
	Model        string                        `json:"model"`
	Connection   string                        `json:"connection"`
	SourceCommit string                        `json:"source_commit"`
	PromptSHA    string                        `json:"prompt_sha"`
	Prompt       string                        `json:"prompt"`
	Run          string                        `json:"run"`
	Stage        string                        `json:"stage"`
	Outcome      factory.Outcome               `json:"outcome,omitempty"`
	Reason       string                        `json:"reason,omitempty"`
	Repository   string                        `json:"repository"`
	Issue        string                        `json:"issue"`
	Attempts     int                           `json:"attempts"`
	Revision     int64                         `json:"revision"`
	NativeRev    int64                         `json:"native_revision"`
	CreatedUnix  int64                         `json:"created_unix"`
	FinishedUnix int64                         `json:"finished_unix,omitempty"`
}

func assignmentDTO(a factory.Assignment, reservation *factory.Reservation, publication *factory.Publication) AssignmentView {
	view := AssignmentView{
		Authority: a.Authority, RunHistory: a.RunHistory,
		ID: a.ID, ProjectID: a.ProjectID, Role: a.Role, Acceptance: a.Acceptance,
		Preparation: a.Preparation, Harness: a.Harness, HarnessVers: a.HarnessVers,
		Model: a.Model, Connection: a.Connection, SourceCommit: a.SourceCommit,
		PromptSHA: a.PromptSHA, Prompt: string(a.Prompt), Run: a.Run, Stage: a.Stage,
		Outcome: a.Outcome, Reason: a.Reason,
		Repository: strconv.FormatInt(a.Repository, 10), Issue: strconv.FormatInt(a.Issue, 10),
		Attempts: a.Attempts, Revision: a.Revision, NativeRev: a.NativeRev,
		CreatedUnix: a.CreatedUnix, FinishedUnix: a.FinishedUnix,
	}
	if a.Result != nil {
		view.Result = &factoryAssignmentResult{
			Findings: a.Result.Findings, Status: a.Result.Status, Summary: a.Result.Summary,
			Candidate: a.Result.Candidate, RunID: a.Result.RunID,
			ReviewPassed: a.Result.ReviewPassed, Reported: a.Result.Reported,
			RecordedUnix: a.Result.RecordedUnix,
		}
	}
	if reservation != nil {
		view.Reservation = &factoryAssignmentReservation{
			Connection: reservation.Connection, State: reservation.State,
			PlannedMinutes: reservation.PlannedMinutes,
		}
	}
	if publication != nil {
		view.Publication = PublicationOperationsView(*publication)
	}
	return view
}
