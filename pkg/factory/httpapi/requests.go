package httpapi

import (
	"net/http"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Requests serves owner-private direction, product review and admitted planning.
// Hosts supply authenticated identity and current admission; service ports keep
// write authority and source journals. No body/query selects this principal.
type Requests struct {
	Service     factoryservice.Requests
	Posts       factoryservice.Posts
	Planning    factoryservice.RequestPlanning
	Exploration factoryservice.RequestExploration
	Host        collaboration.HostID
	Actor       collaboration.ActorID
	// Principal is host-supplied audit attribution for allowance commands.
	Principal string
	Current   func(http.ResponseWriter, *http.Request) bool
	// Error presenters retain host-specific authority failures without importing
	// its SDK/session. Missing presenters return bounded unavailable responses.
	AuthorityError  func(http.ResponseWriter, error)
	VisibilityError func(http.ResponseWriter, error)
}

// Handler constructs no listener, model client, work driver or session.
func (s Requests) Handler() http.Handler {
	mux := http.NewServeMux()
	register := func(method, path string, serve http.HandlerFunc) {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				w.Header().Set("Allow", method)
				writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
				return
			}
			if s.current(w, r) {
				if method == http.MethodPost {
					switch path {
					case "/api/factory/intents/{intentID}/plan", "/api/factory/intents/{intentID}/generate":
						if s.Planning.Requests.Store == nil || s.Planning.Requests.Store != s.Service.Store {
							writeError(w, 503, "planning_unavailable", "Planning command transport is unavailable.")
							return
						}
					case "/api/factory/intents/{intentID}/explore", "/api/factory/intents/{intentID}/plan/review":
						if s.Exploration.Requests.Store == nil || s.Exploration.Requests.Store != s.Service.Store {
							writeError(w, 503, "planning_unavailable", "Exploration command transport is unavailable.")
							return
						}
					}
				}
				serve(w, r)
			}
		})
	}
	register(http.MethodGet, "/api/factory/departments", s.departments)
	register(http.MethodPost, "/api/factory/departments", s.departments)
	register(http.MethodGet, "/api/factory/intents", s.catalogue)
	register(http.MethodPost, "/api/factory/intents", s.saveRevision)
	register(http.MethodGet, "/api/factory/intents/{intentID}", s.revision)
	register(http.MethodGet, "/api/factory/intents/{intentID}/revisions/{revisionID}", s.savedRevision)
	register(http.MethodGet, "/api/factory/intents/{intentID}/revisions/{revisionID}/generation", s.savedGeneration)
	register(http.MethodGet, "/api/factory/intents/{intentID}/revisions/{revisionID}/plan", s.savedPlanning)
	register(http.MethodGet, "/api/factory/intents/{intentID}/revisions/{revisionID}/exploration", s.savedExploration)
	register(http.MethodGet, "/api/factory/intents/{intentID}/history", s.history)
	register(http.MethodGet, "/api/factory/intents/{intentID}/revisions", s.scopeHistory)
	register(http.MethodGet, "/api/factory/intents/{intentID}/review", s.review)
	register(http.MethodGet, "/api/factory/intents/{intentID}/overview", s.overview)
	register(http.MethodGet, "/api/factory/intents/{intentID}/generation", s.generation)
	register(http.MethodGet, "/api/factory/intents/{intentID}/verification-allowance", s.verificationAllowance)
	register(http.MethodPost, "/api/factory/intents/{intentID}/verification-allowance", s.verificationAllowance)
	register(http.MethodGet, "/api/factory/intents/{intentID}/plan", s.planStatus)
	register(http.MethodPost, "/api/factory/intents/{intentID}/plan", s.plan)
	register(http.MethodPost, "/api/factory/intents/{intentID}/generate", s.generate)
	register(http.MethodPost, "/api/factory/intents/{intentID}/plan/review", s.reviewPlan)
	register(http.MethodPost, "/api/factory/intents/{intentID}/explore", s.explore)
	register(http.MethodPost, "/api/factory/feed/posts/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		if s.Posts.Store == nil || s.Posts.Store != s.Service.Store {
			writeError(w, 503, "store_unavailable", "Product review transport is unavailable.")
			return
		}
		s.reviewPost(w, r)
	})
	return mux
}

func (s Requests) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func (s Requests) authorityError(w http.ResponseWriter, err error) {
	if s.AuthorityError != nil {
		s.AuthorityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current repository administration is unavailable.")
}

func (s Requests) visibilityError(w http.ResponseWriter, err error) {
	if s.VisibilityError != nil {
		s.VisibilityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current repository visibility is unavailable.")
}
