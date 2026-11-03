package service

import (
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
)

type IssueView struct {
	Status       control.AcceptanceValidity `json:"status"`
	Attempt      *workAttemptView           `json:"attempt,omitempty"`
	Publication  *workPublicationView       `json:"publication,omitempty"`
	Closure      workClosureView            `json:"closure"`
	Capabilities WorkCapabilities           `json:"capabilities"`
	Requested    *WorkRequested             `json:"requested,omitempty"`
	Links        *IssueLinks                `json:"links,omitempty"`
	Repository   string                     `json:"repository"`
	Issue        string                     `json:"issue"`
	Journey      *IssueJourney              `json:"journey"`
	Readiness    *readinessView             `json:"readiness,omitempty"`
	Checks       *checksView                `json:"checks,omitempty"`
	Merge        *mergeView                 `json:"merge,omitempty"`
	ObservedUnix int64                      `json:"observed_unix"`
}

// readinessView is the durable readiness verdict for one native issue:
// its classification, primary reason, structured blockers, assessed
// acceptance and record revision. It carries IDs and codes only.
type readinessView struct {
	Blockers   []factory.Blocker `json:"blockers,omitempty"`
	Acceptance string            `json:"acceptance,omitempty"`
	Readiness  string            `json:"readiness"`
	Reason     string            `json:"reason"`
	Revision   int64             `json:"revision"`
	Assessed   int64             `json:"assessed_unix"`
}

// checkResultView renders one required check's verdict: its latest
// observed native state on the exact head and whether it passes.
type checkResultView struct {
	Context string `json:"context"`
	State   string `json:"state,omitempty"`
	Passed  bool   `json:"passed"`
}

// checksView is the latest check assessment for one native PR: whether
// the exact head and verified base satisfy every configured required
// check, with the per-check results behind the verdict. It carries IDs,
// commits, codes and observed states only. Stale marks an assessment
// whose head is outside the publication's known candidate history.
type checksView struct {
	Results    []checkResultView `json:"results"`
	Resolution string            `json:"resolution"`
	HeadOID    string            `json:"head_oid"`
	BaseOID    string            `json:"base_oid"`
	Verdict    string            `json:"verdict"`
	Reason     string            `json:"reason"`
	PRNumber   string            `json:"pr_number"`
	PRID       string            `json:"pr_id"`
	Policy     int64             `json:"policy_revision"`
	NativeRev  int64             `json:"native_revision"`
	Revision   int64             `json:"revision"`
	Assessed   int64             `json:"assessed_unix"`
	Stale      bool              `json:"stale,omitempty"`
}

// checksViewDTO renders one recorded assessment. Absence of a record
// leaves the issue without a verdict, never a guessed pass.
func checksViewDTO(a factory.CheckAssessment) *checksView {
	view := &checksView{
		Resolution: factory.CheckResolution(a.Reason),
		HeadOID:    a.HeadOID, BaseOID: a.BaseOID,
		Verdict: a.Verdict, Reason: a.Reason,
		PRNumber: strconv.FormatInt(a.PRNumber, 10), PRID: strconv.FormatInt(a.PRID, 10),
		Policy: a.PolicyRevision, NativeRev: a.NativeRev, Revision: a.Revision, Assessed: a.AssessedUnix,
	}
	for _, result := range a.Results {
		view.Results = append(view.Results, checkResultView{Context: result.Context, State: result.State, Passed: result.Passed})
	}
	return view
}

// mergeView is the latest merge for one repository issue: its stage,
// bounded reason, exact PR linkage and confirmed completion stamps. It
// carries IDs, commits and codes only. Stale marks an open merge whose
// head is outside the publication's known candidate history; terminal
// merges stand on the head they finished with.
type mergeView struct {
	Resolution string `json:"resolution"`
	HeadOID    string `json:"head_oid"`
	BaseOID    string `json:"base_oid"`
	Commit     string `json:"merged_commit,omitempty"`
	Stage      string `json:"stage"`
	Reason     string `json:"reason,omitempty"`
	PRNumber   string `json:"pr_number"`
	PRID       string `json:"pr_id"`
	Revision   int64  `json:"revision"`
	Finished   int64  `json:"finished_unix,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
}

// mergeViewDTO renders one recorded merge. Absence of a record leaves
// the issue without a merge verdict, never a guessed completion.
func mergeViewDTO(m factory.Merge) *mergeView {
	return &mergeView{
		Resolution: factory.MergeResolution(m.Reason),
		HeadOID:    m.HeadOID, BaseOID: m.BaseOID, Commit: m.MergedCommit,
		Stage: m.Stage, Reason: m.Reason,
		PRNumber: strconv.FormatInt(m.PRNumber, 10), PRID: strconv.FormatInt(m.PRID, 10),
		Revision: m.Revision, Finished: m.FinishedUnix,
	}
}

// workAttemptView is the latest publication-line dispatch attempt for one
// native issue: its role, stage and run binding. Prompt bytes and held
// capacity stay on the assignment route; this view carries identity and
// stage only. Absence of a record means no attempt was dispatched yet.
type workAttemptView struct {
	Outcome      factory.Outcome `json:"outcome,omitempty"`
	ID           string          `json:"id"`
	Role         string          `json:"role"`
	Stage        string          `json:"stage"`
	Reason       string          `json:"reason,omitempty"`
	Run          string          `json:"run"`
	Attempts     int             `json:"attempts"`
	CreatedUnix  int64           `json:"created_unix"`
	FinishedUnix int64           `json:"finished_unix,omitempty"`
}

func workAttemptDTO(a factory.Assignment) *workAttemptView {
	return &workAttemptView{
		Outcome: a.Outcome,
		ID:      a.ID, Role: a.Role, Stage: a.Stage, Reason: a.Reason, Run: a.Run,
		Attempts: a.Attempts, CreatedUnix: a.CreatedUnix, FinishedUnix: a.FinishedUnix,
	}
}

// workCorrectionView renders one native branch correction: the candidate
// it published and its operation effect. Receipt bytes stay out; the
// candidate commit identifies the corrected content.
type workCorrectionView struct {
	Candidate   string `json:"candidate,omitempty"`
	Effect      string `json:"effect,omitempty"`
	Completion  string `json:"completion,omitempty"`
	Reason      string `json:"reason,omitempty"`
	UpdatedUnix int64  `json:"updated_unix"`
}

// workReviewView renders one independent review run: the exact head it
// judged, its disposition and its native outcome. Review prose stays on
// the native PR; this view carries the disposition and effect only.
type workReviewView struct {
	Run        string `json:"run"`
	HeadOID    string `json:"head_oid"`
	Event      string `json:"event"`
	Effect     string `json:"effect,omitempty"`
	Completion string `json:"completion,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// maxWorkHistoryEntries bounds the rendered correction and review tails.
// Totals carry the full recorded counts; only the most recent entries
// render inline.
const maxWorkHistoryEntries = 20

// workPublicationView renders one attempt's publication: the exact linked
// PR once creation commits, the candidate and base it binds, and the
// bounded correction and review history behind it.
type workPublicationView struct {
	PublicationOperations
	Corrections      []workCorrectionView `json:"corrections,omitempty"`
	Reviews          []workReviewView     `json:"reviews,omitempty"`
	Candidate        string               `json:"candidate"`
	BaseSHA          string               `json:"base_sha"`
	TargetBranch     string               `json:"target_branch"`
	ID               string               `json:"id"`
	Run              string               `json:"run"`
	CorrectionsTotal int                  `json:"corrections_total"`
	ReviewsTotal     int                  `json:"reviews_total"`
	CreatedUnix      int64                `json:"created_unix"`
	FinishedUnix     int64                `json:"finished_unix,omitempty"`
}

func workPublicationDTO(p factory.Publication) *workPublicationView {
	view := &workPublicationView{
		PublicationOperations: *PublicationOperationsView(p),
		Candidate:             p.Candidate, BaseSHA: p.BaseSHA, TargetBranch: p.TargetBranch,
		ID: p.ID, Run: p.Run,
		CorrectionsTotal: len(p.Corrections), ReviewsTotal: len(p.ReviewOperations),
		CreatedUnix: p.CreatedUnix, FinishedUnix: p.FinishedUnix,
	}
	corrections := p.Corrections
	if len(corrections) > maxWorkHistoryEntries {
		corrections = corrections[len(corrections)-maxWorkHistoryEntries:]
	}
	for _, correction := range corrections {
		entry := workCorrectionView{
			Effect: correction.Effect, Completion: correction.Completion,
			Reason: correction.Reason, UpdatedUnix: correction.UpdatedUnix,
		}
		if correction.Work != nil {
			entry.Candidate = correction.Work.Candidate
		}
		view.Corrections = append(view.Corrections, entry)
	}
	reviews := p.ReviewOperations
	if len(reviews) > maxWorkHistoryEntries {
		reviews = reviews[len(reviews)-maxWorkHistoryEntries:]
	}
	for _, review := range reviews {
		view.Reviews = append(view.Reviews, workReviewView{
			Run: review.RunID, HeadOID: review.Work.HeadOID, Event: review.Work.Event,
			Effect: review.Outcome.Effect, Completion: review.Outcome.Completion,
			Reason: review.Outcome.Reason,
		})
	}
	return view
}

// workCandidateHistory collects the publication's known candidate commits:
// its initial candidate plus every committed correction. Review, check
// and merge evidence names the head it judged; heads outside this set
// are superseded.
func workCandidateHistory(p factory.Publication) map[string]bool {
	history := map[string]bool{}
	if p.Publish.Work != nil {
		history[p.Publish.Work.Candidate] = true
	} else if p.Candidate != "" {
		history[p.Candidate] = true
	}
	for _, correction := range p.Corrections {
		if correction.Effect == factory.OpEffectCommitted && correction.Work != nil && correction.Work.Candidate != "" {
			history[correction.Work.Candidate] = true
		}
	}
	delete(history, "")
	return history
}

// workClosureStates name the original-issue closure facts. Pending means
// a PR exists but original closure is unconfirmed; confirmed means the
// merge recorded the closing occurrence and its timestamp.
const (
	workClosureNone      = "none"
	workClosurePending   = "pending"
	workClosureConfirmed = "confirmed"
)

type workClosureView struct {
	Occurrence string `json:"occurrence,omitempty"`
	State      string `json:"state"`
	ClosedUnix int64  `json:"closed_unix,omitempty"`
}

func workClosureDTO(publication *factory.Publication, merged *factory.Merge) workClosureView {
	if merged != nil && merged.Stage == factory.MergeMerged && merged.OriginalClosedUnix > 0 && merged.OriginalClosingOccurrence != "" {
		return workClosureView{Occurrence: merged.OriginalClosingOccurrence, State: workClosureConfirmed, ClosedUnix: merged.OriginalClosedUnix}
	}
	if publication != nil && publication.PRNumber > 0 {
		return workClosureView{State: workClosurePending}
	}
	if merged != nil && merged.PRNumber > 0 {
		return workClosureView{State: workClosurePending}
	}
	return workClosureView{State: workClosureNone}
}

// IssueLinks carries validated host links for the issue and its recorded PR.
// Missing current navigation metadata leaves links absent.
type IssueLinks struct {
	Issue string `json:"issue_url"`
	Pull  string `json:"pull_url,omitempty"`
}

// applyWorkStaleness marks check and open-merge evidence whose head is
// outside the publication's known candidate history. Terminal merges
// stand on the head they finished with and are never marked stale.
func applyWorkStaleness(view *IssueView, publication *factory.Publication, merged *factory.Merge) {
	if publication == nil {
		return
	}
	history := workCandidateHistory(*publication)
	if len(history) == 0 {
		return
	}
	if view.Checks != nil && !history[view.Checks.HeadOID] {
		view.Checks.Stale = true
	}
	if view.Merge != nil && merged != nil && merged.Stage == factory.MergeOpen && !history[view.Merge.HeadOID] {
		view.Merge.Stale = true
	}
}
