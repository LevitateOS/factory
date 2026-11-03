package httpapi

import (
	"errors"
	"net/http"

	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

func (s Repositories) access(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	view, err := s.Access.Read(r.Context(), s.Actor, repository)
	if err != nil {
		if s.hostError(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrRepositoryAccessUnavailable):
			writeError(w, 503, "store_unavailable", "Factory access runtime is unavailable.")
		case errors.Is(err, factoryservice.ErrRepositoryAccessVisibility), errors.Is(err, factoryservice.ErrRepositoryAccessViewer):
			s.visibilityError(w, err)
		default:
			writeError(w, 503, "store_unavailable", "Could not read Factory access.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
