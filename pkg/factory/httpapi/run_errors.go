package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Runs) statusError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrRunStatusUnavailable):
		writeError(w, 503, "runtime_unavailable", "Current run execution observation is unavailable.")
	case errors.Is(err, factoryservice.ErrRunStatusAccess):
		s.authorityError(w, err)
	case errors.Is(err, factory.ErrNotFound):
		writeError(w, 404, "not_found", "Factory run not found.")
	case errors.Is(err, factoryservice.ErrRunStatusScope):
		writeError(w, 409, "view_conflict", "Run binding names another repository; refresh the inventory.")
	case errors.Is(err, factoryservice.ErrRunStatusContext):
		writeError(w, 503, "context_unavailable", "Could not read the saved context hold.")
	case errors.Is(err, factoryservice.ErrRunStatusObservation):
		writeError(w, 503, "host_unavailable", "Run observation is unavailable.")
	case errors.Is(err, factoryservice.ErrRunStatusAuthority):
		writeError(w, 503, "authority_unavailable", "Could not read current verification authority.")
	default:
		writeError(w, 503, "store_unavailable", "Could not read run status.")
	}
}

func (s Runs) interventionError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrInterventionUnavailable):
		writeError(w, 503, "runtime_unavailable", "Current execution or recovery adapter is unavailable.")
	case errors.Is(err, factoryservice.ErrInterventionAccess):
		s.authorityError(w, err)
	case errors.Is(err, factoryservice.ErrInterventionAction):
		writeError(w, 400, "invalid_action", "Choose an available Factory intervention.")
	case errors.Is(err, factoryservice.ErrInterventionCommand):
		writeError(w, 400, "invalid_command", "Retries require a client-generated command identity.")
	case errors.Is(err, factoryservice.ErrInterventionPayload):
		writeError(w, 400, "invalid_request", "The action requires its exact saved context or completion scope; pause and resume do not use command IDs.")
	case errors.Is(err, factoryservice.ErrInterventionRecoveryTarget):
		writeError(w, 403, "membership_required", "A current admitted recovery destination is required before taking over this work.")
	default:
		lifecycleControlError(w, err)
	}
}

// Unknown runs remain private; pending or unconfirmed work never becomes success.
func lifecycleControlError(w http.ResponseWriter, err error) {
	var pending control.ErrPendingRuns
	switch {
	case errors.Is(err, factory.ErrNotFound):
		writeError(w, 404, "not_found", "Factory run not found.")
	case errors.As(err, &pending):
		writeJSON(w, 409, map[string]any{"error": "pending_runs", "runs": pending.Runs, "message": "Recorded runs are not settled; stop or reconcile them first."})
	case errors.Is(err, control.ErrTakeoverFailed):
		writeError(w, 502, "takeover_unconfirmed", "Takeover was not confirmed; refresh and retry.")
	case errors.Is(err, store.ErrTakeoverConflict):
		writeError(w, 409, "takeover_conflict", "Takeover identity reused for different content.")
	case errors.Is(err, store.ErrExplicitRetryIneligible):
		writeError(w, 409, "retry_ineligible", "The prior attempt is not eligible for retry; refresh its settlement and current inputs.")
	case errors.Is(err, store.ErrRetainedPublicationIneligible):
		writeError(w, 409, "retained_publication_ineligible", err.Error())
	case errors.Is(err, store.ErrContextResolutionIneligible):
		writeError(w, 409, "context_resolution_ineligible", "The held current context changed or cannot be selected; refresh this run.")
	case errors.Is(err, store.ErrCompletionRetryIneligible):
		writeError(w, 409, "completion_retry_ineligible", "The completion cycle changed or its prior work has not settled; refresh its details.")
	default:
		CommandError(w, err)
	}
}
