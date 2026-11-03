package httpapi

import (
	"errors"
	"net/http"

	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s Repositories) status(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	view, err := s.Status.Read(r.Context(), s.Actor, repository)
	if err != nil {
		if s.hostError(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrRepositoryStatusVisibility):
			s.visibilityError(w, err)
		case errors.Is(err, factoryservice.ErrRepositoryStatusBindings):
			writeError(w, 503, "identity_unavailable", "Factory binding identity is unavailable.")
		default:
			writeError(w, 503, "store_unavailable", "Could not read repository status.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
