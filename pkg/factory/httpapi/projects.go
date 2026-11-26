package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Projects serves the retained, profile-scoped project list. Only a host that
// supplies a ProjectScope mounts it; elsewhere the route is absent.
type Projects struct {
	Service factoryservice.ProjectSummaries
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s Projects) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/projects", s.list)
	mux.HandleFunc("POST /api/factory/projects/remove", s.remove)
	return mux
}

func (s Projects) list(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	list, err := s.Service.List(r.Context(), s.Actor)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		writeError(w, 503, "projects_unavailable", "Factory projects are unavailable.")
		return
	}
	writeJSON(w, 200, list)
}

func (s Projects) remove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Repository string `json:"repository"`
	}
	if !decodeObject(w, r, &in) || !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	id, valid := positiveID(in.Repository)
	if !valid {
		writeError(w, 400, "invalid_request", "A repository is required.")
		return
	}
	err := s.Service.Remove(r.Context(), s.Actor, collaboration.RepositoryID(id))
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrProjectNotListed):
		writeError(w, 404, "not_found", "This project is no longer listed.")
	case err != nil:
		writeError(w, 503, "projects_unavailable", "Factory projects are unavailable.")
	default:
		writeJSON(w, 200, struct {
			Removed bool `json:"removed"`
		}{true})
	}
}
