package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Requests) revision(w http.ResponseWriter, r *http.Request) {
	result, err := s.Service.Revision(r.Context(), s.Actor, r.PathValue("intentID"))
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Requests) saveRevision(w http.ResponseWriter, r *http.Request) {
	if !s.current(w, r) {
		return
	}
	var in factory.IntentRevision
	if !decodeObject(w, r, &in) {
		return
	}
	requests := s.Service
	admitted := s.Actor
	if !s.current(w, r) {
		return
	}
	result, err := requests.SaveRevision(r.Context(), admitted, in)
	switch {
	case errors.Is(err, factoryservice.ErrRequestInputInvalid):
		writeError(w, 400, "invalid_intent", err.Error())
		return
	case errors.Is(err, factoryservice.ErrRequestWriteAccess):
		s.authorityError(w, err)
		return
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 412, "stale_revision", "The reviewed build request changed.")
		return
	case err != nil:
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 201, result)
	}
}

// overview reads the admitted owner's stored request facts.
// GET never judges, provisions or dispatches.
func (s Requests) overview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	view, err := s.Service.Overview(r.Context(), s.Actor, r.PathValue("intentID"))
	if err != nil {
		if errors.Is(err, factoryservice.ErrRequestNotFound) {
			writeError(w, 404, "not_found", "Build request not found.")
		} else {
			writeError(w, 503, "store_unavailable", "Could not read build request overview.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
