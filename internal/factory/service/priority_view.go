package service

import (
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
)

// maxRenderedProbabilities bounds one rendered answer distribution. Stored
// answers are not revalidated on read, so rendering caps the map and
// reports the recorded count.
const maxRenderedProbabilities = 256

// priorityCandidateView renders one comparison candidate: identity,
// readiness, queue age and which optional evidence was known. Native
// text and blocker details stay out; cross-repository candidates are
// counted, never enumerated.
type priorityCandidateView struct {
	BlockerCodes []string `json:"blocker_codes,omitempty"`
	ID           string   `json:"id"`
	Repository   string   `json:"repository"`
	Issue        string   `json:"issue"`
	Readiness    string   `json:"readiness"`
	Reason       string   `json:"reason,omitempty"`
	Acceptance   string   `json:"acceptance,omitempty"`
	FirstSeen    int64    `json:"first_seen_unix"`
	Assessed     int64    `json:"assessed_unix"`
	AgeSeconds   int64    `json:"age_seconds"`
	ImpactKnown  bool     `json:"impact_known"`
	RiskKnown    bool     `json:"risk_known"`
	EffortKnown  bool     `json:"effort_known"`
}

func priorityCandidateDTO(c factory.PriorityCandidate, observed int64) priorityCandidateView {
	view := priorityCandidateView{
		ID:         c.ID,
		Repository: strconv.FormatInt(c.Control.Repository, 10),
		Issue:      strconv.FormatInt(c.Control.Issue, 10),
		Readiness:  c.Control.Readiness, Reason: c.Control.Reason, Acceptance: c.Control.Acceptance,
		FirstSeen: c.Control.FirstSeenUnix, Assessed: c.Control.AssessedUnix,
		ImpactKnown: c.Impact != "", RiskKnown: c.Risk != "", EffortKnown: c.Effort != "",
	}
	if age := observed - c.Control.FirstSeenUnix; age > 0 {
		view.AgeSeconds = age
	}
	for _, blocker := range c.Control.Blockers {
		view.BlockerCodes = append(view.BlockerCodes, blocker.Code)
	}
	return view
}

// priorityCallView renders one recorded judgment call: the deciding
// model, its choice, uncertainty and cost. The call's request
// candidates stay out; the comparison carries them once.
type priorityCallView struct {
	Selected     string  `json:"selected,omitempty"`
	Model        string  `json:"model,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
}

func priorityCallDTO(c factory.PriorityCall) priorityCallView {
	return priorityCallView{
		Selected: c.Answer.Selected, Model: c.Answer.Model,
		Confidence:  c.Answer.Confidence,
		InputTokens: c.Answer.InputTokens, OutputTokens: c.Answer.OutputTokens,
	}
}

// priorityComparisonView renders one recorded next-work comparison over
// the requested repository: the eligible candidates the journal named,
// the recorded choice and its uncertainty. Candidates from other
// repositories count toward the total without enumerating their
// identities; a masked choice renders empty with SelectedVisible false,
// and the deciding distribution covers rendered candidates only with
// ProbabilitiesFull reporting whether anything was withheld.
type priorityComparisonView struct {
	Candidates        []priorityCandidateView `json:"candidates"`
	Calls             []priorityCallView      `json:"calls"`
	Probabilities     map[string]float64      `json:"probabilities,omitempty"`
	Snapshot          string                  `json:"snapshot"`
	Selected          string                  `json:"selected,omitempty"`
	Outcome           string                  `json:"outcome"`
	CreatedUnix       int64                   `json:"created_unix"`
	CandidatesTotal   int                     `json:"candidates_total"`
	OtherCount        int                     `json:"other_count"`
	ProbabilityN      int                     `json:"probability_count"`
	SelectedVisible   bool                    `json:"selected_visible"`
	ProbabilitiesFull bool                    `json:"probabilities_full"`
}

func priorityComparisonDTO(r factory.PriorityReceipt, repository int64, observed int64) priorityComparisonView {
	view := priorityComparisonView{
		Candidates: []priorityCandidateView{}, Calls: []priorityCallView{},
		Snapshot: r.Snapshot, Selected: r.Selected, Outcome: r.Outcome,
		CreatedUnix: r.CreatedUnix, CandidatesTotal: len(r.Candidates),
	}
	visible := map[string]bool{}
	for _, candidate := range r.Candidates {
		if candidate.Control.Repository != repository {
			view.OtherCount++
			continue
		}
		visible[candidate.ID] = true
		view.Candidates = append(view.Candidates, priorityCandidateDTO(candidate, observed))
	}
	if view.Selected = r.Selected; r.Selected != "" && !visible[r.Selected] {
		view.Selected = ""
	} else {
		view.SelectedVisible = true
	}
	for _, call := range r.Calls {
		rendered := priorityCallDTO(call)
		if !visible[rendered.Selected] {
			rendered.Selected = ""
		}
		view.Calls = append(view.Calls, rendered)
	}
	if len(r.Calls) > 0 {
		final := r.Calls[len(r.Calls)-1].Answer
		view.ProbabilityN = len(final.Probabilities)
		if len(final.Probabilities) <= maxRenderedProbabilities {
			view.Probabilities = map[string]float64{}
			for id, probability := range final.Probabilities {
				if visible[id] {
					view.Probabilities[id] = probability
				}
			}
			view.ProbabilitiesFull = len(view.Probabilities) == len(final.Probabilities)
		}
	}
	return view
}

// IssuePriorityView is the priority read for one native issue: the
// recent recorded next-work comparisons naming the issue, newest first.
// Comparisons page by trailing cursor; empty pages are explicit.
type IssuePriorityView struct {
	Comparisons        []priorityComparisonView `json:"comparisons"`
	Repository         string                   `json:"repository"`
	Issue              string                   `json:"issue"`
	NextBeforeSnapshot string                   `json:"next_before_snapshot,omitempty"`
	ObservedUnix       int64                    `json:"observed_unix"`
	NextBeforeUnix     int64                    `json:"next_before_unix,omitempty"`
}
