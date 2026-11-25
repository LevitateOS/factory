package httpapi

import (
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
