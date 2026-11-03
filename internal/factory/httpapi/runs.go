package httpapi

import (
	"net/http"

	"github.com/levitateos/sodaos/internal/collaboration"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
	"github.com/levitateos/sodaos/internal/store"
)

// Runs serves recorded status and explicit interventions. Hosts bind the actor
// and command principal independently and retain current channel/account and
// execution/recovery authority. Construction starts no runtime or work.
type Runs struct {
	Store           *store.Store
	Status          factoryservice.RunStatus
	Commands        factoryservice.Interventions
	Host            collaboration.HostID
	Actor           collaboration.ActorID
	Principal       string
	Visibility      factoryservice.WorkVisibility
	Current         func(http.ResponseWriter, *http.Request) bool
	AuthorityError  func(http.ResponseWriter, error)
	VisibilityError func(http.ResponseWriter, error)
	HostError       func(http.ResponseWriter, error) bool
}

func (s Runs) Handler() http.Handler {
	mux := http.NewServeMux()
	register := func(method, path string, serve http.HandlerFunc) {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				w.Header().Set("Allow", method)
				writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
				return
			}
			if !s.current(w, r) {
				return
			}
			selected := s.Commands.Store
			if method == http.MethodGet {
				selected = s.Status.Store
			} else if s.Principal == "" {
				writeError(w, 503, "authority_unavailable", "Factory command authority is unavailable.")
				return
			}
			if s.Store == nil || selected != s.Store {
				writeError(w, 503, "store_unavailable", "Factory run storage is unavailable.")
				return
			}
			serve(w, r)
		})
	}
	register(http.MethodGet, "/api/factory/runs/{runID}", s.status)
	register(http.MethodPost, "/api/repositories/{repositoryID}/factory/actions", s.repositoryAction)
	register(http.MethodPost, "/api/factory/runs/{runID}/actions", s.runAction)
	return mux
}

func (s Runs) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func (s Runs) status(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	view, err := s.Status.Read(r.Context(), s.Actor, r.PathValue("runID"))
	if err != nil {
		s.statusError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

func (s Runs) repositoryAction(w http.ResponseWriter, r *http.Request) {
	id, valid := positiveID(r.PathValue("repositoryID"))
	if !valid {
		writeError(w, 404, "not_found", "Repository not found.")
		return
	}
	repository := collaboration.RepositoryID(id)
	if s.Visibility == nil {
		s.visibilityError(w, factoryservice.ErrInterventionUnavailable)
		return
	}
	if err := s.Visibility(r.Context(), s.Actor, repository); err != nil {
		if !s.hostError(w, err) {
			s.visibilityError(w, err)
		}
		return
	}
	var in factoryservice.RepositoryCommand
	if !decodeObject(w, r, &in) || !s.current(w, r) {
		return
	}
	result, err := s.Commands.Repository(r.Context(), s.Actor, s.Principal, repository, in)
	if err != nil {
		s.interventionError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Runs) runAction(w http.ResponseWriter, r *http.Request) {
	var in factoryservice.RunCommand
	if !decodeObject(w, r, &in) || !s.current(w, r) {
		return
	}
	result, err := s.Commands.Run(r.Context(), s.Actor, s.Principal, r.PathValue("runID"), in)
	if err != nil {
		s.interventionError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Runs) hostError(w http.ResponseWriter, err error) bool {
	return s.HostError != nil && s.HostError(w, err)
}

func (s Runs) authorityError(w http.ResponseWriter, err error) {
	if s.AuthorityError != nil {
		s.AuthorityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current execution authority is unavailable.")
}

func (s Runs) visibilityError(w http.ResponseWriter, err error) {
	if s.VisibilityError != nil {
		s.VisibilityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current repository visibility is unavailable.")
}
