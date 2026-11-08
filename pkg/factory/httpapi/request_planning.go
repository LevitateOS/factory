package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Requests) generate(w http.ResponseWriter, r *http.Request) {
	if !s.current(w, r) {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	var in struct {
		Revision string `json:"revision"`
	}
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	generation, wait, err := s.Planning.Generate(r.Context(), s.Actor, r.PathValue("intentID"), in.Revision)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrRequestNotFound):
			RequestReadError(w, err)
		case errors.Is(err, factoryservice.ErrRequestUnavailable), errors.Is(err, factoryservice.ErrRequestWriteUnavailable):
			CommandError(w, err)
		case errors.Is(err, factoryservice.ErrRequestInputInvalid):
			writeError(w, 400, "invalid_revision", "An accepted revision identity is required.")
		case errors.Is(err, store.ErrStaleRevision):
			writeError(w, 412, "stale_revision", "Review inputs changed.")
		case errors.Is(err, factoryservice.ErrRequestNotAccepted):
			writeError(w, 409, "intent_not_accepted", "Review the product direction before generating work.")
		case errors.Is(err, factoryservice.ErrRequestWriteAccess):
			s.authorityError(w, err)
		case errors.Is(err, factoryservice.ErrGraphUnavailable):
			writeError(w, 503, "native_graph_unavailable", "Native issue creation needs an enrolled graph actor.")
		case errors.Is(err, control.ErrPlanChoiceReview):
			writeError(w, 409, "intent_review_required", err.Error())
		case errors.Is(err, control.ErrPlanChoiceAttention):
			writeError(w, 409, "planning_attention", err.Error())
		case errors.Is(err, control.ErrPlanChoiceStale):
			writeError(w, 412, "stale_planning_choice", err.Error())
		case errors.Is(err, control.ErrPlanChoiceStore):
			CommandError(w, err)
		default:
			writeError(w, 409, "planning_choice_unavailable", err.Error())
		}
		return
	}
	if wait != nil {
		if s.current(w, r) {
			writeJSON(w, 202, map[string]any{"launched": false, "waits": []control.DispatchWait{*wait}})
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, generation)
	}
}

// plan adapts the shared admitted planning command. A browser response
// can disappear while approved context is read; the recorded attempt remains
// discoverable through status. Service/native callers retain their own context.
func (s Requests) plan(w http.ResponseWriter, r *http.Request) {
	if !s.current(w, r) {
		return
	}
	var in struct {
		Revision  string `json:"revision"`
		Operation string `json:"operation"`
	}
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancel()
	result, err := s.Planning.Plan(opCtx, s.Actor, r.PathValue("intentID"), in.Revision, in.Operation)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrRequestNotFound):
			RequestReadError(w, err)
		case errors.Is(err, store.ErrStaleRevision):
			writeError(w, 412, "stale_revision", "Review inputs changed.")
		case errors.Is(err, factoryservice.ErrRequestNotDraft):
			writeError(w, 409, "intent_not_draft", "Only draft direction starts a planning run; accepted direction uses generate.")
		case errors.Is(err, factoryservice.ErrRequestWriteAccess):
			s.authorityError(w, err)
		case errors.Is(err, factoryservice.ErrPlanningOperationInvalid):
			writeError(w, 400, "invalid_operation", "Unknown planning operation.")
		case errors.Is(err, factoryservice.ErrPlanningUnavailable):
			writeError(w, 503, "planning_unavailable", "No enrolled model account can answer the staged prompt.")
		case errors.Is(err, control.ErrIntentPlanningPromptBound):
			writeError(w, 400, "planning_prompt_over_bound", "Approved planning context exceeds the staged prompt bound; narrow the approved source selection and plan again.")
		case errors.Is(err, factoryservice.ErrPlanningInputsUnavailable):
			writeError(w, 503, "planning_unavailable", "Draft planning inputs are unavailable: "+err.Error())
		default:
			CommandError(w, err)
		}
		return
	}
	if result.Launch == nil {
		if s.current(w, r) {
			writeJSON(w, 202, map[string]any{"launched": false, "waits": result.Waits})
		}
		return
	}
	status := http.StatusCreated
	if result.Launch.LaunchConfirmed != nil && !*result.Launch.LaunchConfirmed {
		status = http.StatusAccepted
	}
	if s.current(w, r) {
		writeJSON(w, status, result.Launch)
	}
}

// admitPlanningRequest keeps current host and HTTP query admission at the
// transport boundary. Request ownership and planning facts belong to the service.
func (s Requests) admitPlanningRequest(w http.ResponseWriter, r *http.Request) (collaboration.ActorID, bool) {
	if !s.current(w, r) {
		return 0, false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return 0, false
	}
	return s.Actor, true
}

func (s Requests) planningReadError(w http.ResponseWriter, err error, review bool) {
	switch {
	case errors.Is(err, factoryservice.ErrRequestNotFound):
		RequestReadError(w, err)
	case errors.Is(err, factoryservice.ErrRequestNotDraft):
		writeError(w, 409, "intent_not_draft", "Only draft direction carries a planning status; accepted direction uses generation.")
	case errors.Is(err, factoryservice.ErrRequestWriteAccess):
		s.authorityError(w, err)
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 412, "stale_revision", "The direction changed while its walkthrough was checked.")
	case errors.Is(err, factoryservice.ErrRequestInputInvalid):
		writeError(w, 400, "invalid_request", "A review command identity must be 32 hexadecimal characters.")
	case errors.Is(err, factoryservice.ErrPlanningWalkthroughUnavailable):
		writeError(w, 409, "walkthrough_unavailable", "This plan has no completed authored walkthrough. Plan again to produce one.")
	case errors.Is(err, factoryservice.ErrPlanningWalkthroughInvalid) && !review:
		writeError(w, 409, "planning_walkthrough_invalid", "The saved walkthrough does not match this direction.")
	case review:
		reportJudgmentCallError(w, err, "Walkthrough review could not finish. Completed judgments are kept; try again or revise the direction.")
	default:
		CommandError(w, err)
	}
}

func (s Requests) planStatus(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.admitPlanningRequest(w, r)
	if !ok {
		return
	}
	// This instance has no network chooser. The shared status path also
	// excludes command choosers when replaying recorded advice.
	requests := s.Service
	requests.WriteAccess = nil
	result, err := (factoryservice.RequestExploration{Requests: requests}).Status(r.Context(), actor, r.PathValue("intentID"))
	if err != nil {
		s.planningReadError(w, err, false)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Requests) reviewPlan(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.admitPlanningRequest(w, r)
	if !ok {
		return
	}
	var in struct {
		Revision string `json:"revision"`
		Command  string `json:"command_id,omitempty"`
	}
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	exploration := s.Exploration
	exploration.RetryCommand = in.Command
	result, err := exploration.ReviewWalkthrough(r.Context(), actor, r.PathValue("intentID"), in.Revision)
	if err != nil {
		s.planningReadError(w, err, true)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Requests) generation(w http.ResponseWriter, r *http.Request) {
	generation, revision, err := s.Service.Generation(r.Context(), s.Actor, r.PathValue("intentID"))
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if generation == nil {
		if s.current(w, r) {
			writeJSON(w, 200, map[string]any{"state": "pending", "revision_id": revision})
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, generation)
	}
}

func (s Requests) savedGeneration(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	generation, err := s.Service.SavedGeneration(r.Context(), s.Actor, r.PathValue("intentID"), r.PathValue("revisionID"))
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if !s.current(w, r) {
		return
	}
	if generation == nil {
		writeJSON(w, 200, map[string]any{"intent_id": r.PathValue("intentID"), "revision_id": r.PathValue("revisionID"), "state": "not_requested"})
		return
	}
	writeJSON(w, 200, generation)
}

func (s Requests) savedPlanning(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	result, err := s.Service.SavedPlanning(r.Context(), s.Actor, r.PathValue("intentID"), r.PathValue("revisionID"))
	if err != nil {
		s.planningReadError(w, err, false)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func reportJudgmentCallError(w http.ResponseWriter, err error, unavailable string) {
	if errors.Is(err, factoryservice.ErrPostJudgmentPending) {
		writeError(w, 503, "judgment_pending", "The original judgment is still settling. Retry to recover its saved result.")
		return
	}
	if errors.Is(err, control.ErrPriorityUnavailable) {
		writeError(w, 503, "planning_unavailable", unavailable)
		return
	}
	CommandError(w, err)
}

func (s Requests) explore(w http.ResponseWriter, r *http.Request) {
	if !s.current(w, r) {
		return
	}
	var in struct {
		Revision string `json:"revision"`
	}
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Exploration.Explore(r.Context(), s.Actor, r.PathValue("intentID"), in.Revision)
	switch {
	case errors.Is(err, factoryservice.ErrRequestNotFound):
		RequestReadError(w, err)
		return
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 412, "stale_revision", "Review inputs changed.")
		return
	case errors.Is(err, factoryservice.ErrExplorationTaskLimit):
		writeError(w, 400, "invalid_proposal_task_limit", err.Error())
		return
	case errors.Is(err, factoryservice.ErrExplorationProposalInvalid):
		writeError(w, 400, "invalid_proposal", err.Error())
		return
	case err != nil:
		reportJudgmentCallError(w, err, "Exploration could not finish. Completed judgments are kept; try again.")
		return
	}
	if s.current(w, r) {
		writeJSON(w, 201, result)
	}
}
