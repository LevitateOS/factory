package httpapi

import (
	"net/http"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// Repositories serves status and admitted configuration commands over one
// canonical store. Hosts supply current authority/custody ports and a nonsecret
// command principal independently of the attributed canonical actor.
type Repositories struct {
	Store           *store.Store
	Status          factoryservice.RepositoryStatus
	Access          factoryservice.RepositoryAccess
	Activity        factoryservice.Activity
	Policies        factoryservice.Policies
	Runtime         factoryservice.RuntimeGrants
	Placements      factoryservice.Placements
	Sponsorships    factoryservice.Sponsorships
	Repair          factoryservice.RepairFunding
	Setup           factoryservice.WorkspaceSetupPort
	Host            collaboration.HostID
	Actor           collaboration.ActorID
	Principal       string
	Visibility      factoryservice.WorkVisibility
	Current         func(http.ResponseWriter, *http.Request) bool
	AuthorityError  func(http.ResponseWriter, error)
	VisibilityError func(http.ResponseWriter, error)
	// HostError renders only host-specific failures, returning true if handled.
	HostError func(http.ResponseWriter, error) bool
}

// Handler starts no listener, controller, model call or automatic work.
func (s Repositories) Handler() http.Handler {
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
			if s.Store == nil || method != http.MethodGet && s.Principal == "" {
				writeError(w, 503, "authority_unavailable", "Factory configuration is unavailable.")
				return
			}
			portStore := s.Store
			switch path {
			case "/api/repositories/{repositoryID}/factory":
				portStore = s.Status.Store
			case "/api/repositories/{repositoryID}/factory/access":
				portStore = s.Access.Store
			case "/api/repositories/{repositoryID}/factory/activity":
				portStore = s.Activity.Store
			case "/api/repositories/{repositoryID}/factory/repair":
				portStore = s.Repair.Store
			case "/api/repositories/{repositoryID}/factory/sponsorships/{connection}":
				portStore = s.Sponsorships.Store
			}
			if portStore != s.Store {
				writeError(w, 503, "store_unavailable", "Repository configuration is unavailable.")
				return
			}
			serve(w, r)
		})
	}
	base := "/api/repositories/{repositoryID}/factory"
	register(http.MethodGet, base, s.status)
	register(http.MethodGet, base+"/access", s.access)
	register(http.MethodGet, base+"/activity", s.activity)
	register(http.MethodPut, base+"/activity", s.activity)
	register(http.MethodPut, base+"/policy", s.policy)
	register(http.MethodGet, base+"/setup", s.workspaceSetup)
	register(http.MethodPost, base+"/setup", s.workspaceSetup)
	register(http.MethodPut, base+"/setup", s.workspaceSetup)
	register(http.MethodPut, base+"/operator-grant", s.operatorGrant)
	register(http.MethodPut, base+"/environment-grant", s.environmentGrant)
	register(http.MethodGet, base+"/environment-grant", s.environmentGrant)
	register(http.MethodGet, base+"/placement", s.placement)
	register(http.MethodPut, base+"/placement", s.placement)
	register(http.MethodPut, base+"/sponsorships/{connection}", s.sponsorship)
	register(http.MethodGet, base+"/repair", s.repair)
	register(http.MethodPut, base+"/repair", s.repair)
	register(http.MethodPut, "/api/factory/capacity", s.capacity)
	return mux
}

func (s Repositories) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func (s Repositories) scope(w http.ResponseWriter, r *http.Request) (collaboration.RepositoryID, bool) {
	id, valid := positiveID(r.PathValue("repositoryID"))
	if !valid {
		writeError(w, 404, "not_found", "Repository not found.")
		return 0, false
	}
	if s.Visibility == nil {
		writeError(w, 503, "authority_unavailable", "Current repository visibility is unavailable.")
		return 0, false
	}
	repository := collaboration.RepositoryID(id)
	if err := s.Visibility(r.Context(), s.Actor, repository); err != nil {
		if !s.hostError(w, err) {
			s.visibilityError(w, err)
		}
		return 0, false
	}
	return repository, true
}

func (s Repositories) hostError(w http.ResponseWriter, err error) bool {
	return s.HostError != nil && s.HostError(w, err)
}

func (s Repositories) authorityError(w http.ResponseWriter, err error) {
	if s.AuthorityError != nil {
		s.AuthorityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current configuration authority is unavailable.")
}

func (s Repositories) visibilityError(w http.ResponseWriter, err error) {
	if s.VisibilityError != nil {
		s.VisibilityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current repository visibility is unavailable.")
}
