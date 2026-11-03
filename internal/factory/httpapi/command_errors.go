package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/sodaos/internal/factory/control"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
	"github.com/levitateos/sodaos/internal/store"
)

// CommandError renders canonical Factory command outcomes for every host.
func CommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCommandConflict):
		writeError(w, 409, "command_conflict", "Command identity reused for different content.")
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 412, "stale_revision", "Grant changed while applying; refresh and retry.")
	case errors.Is(err, control.ErrCommandRunning):
		writeJSON(w, 202, map[string]any{"running": true})
	case errors.Is(err, control.ErrIneffectiveAuthority):
		writeError(w, 409, "authority_ineffective", "Current grants do not authorize this action.")
	default:
		writeError(w, 503, "store_unavailable", "Factory authority storage is unavailable.")
	}
}

// RequestReadError keeps owner-private missing requests undisclosed.
func RequestReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, factoryservice.ErrRequestNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Build request not found.")
		return
	}
	CommandError(w, err)
}
