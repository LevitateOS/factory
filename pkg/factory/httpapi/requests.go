package httpapi

import (
	"net/http"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Requests serves owner-private direction, its revisions, overview and verification allowance.
// Hosts supply authenticated identity and current admission; service ports keep
// write authority and source journals. No body/query selects this principal.
type Requests struct {
	Service factoryservice.Requests
	Host    collaboration.HostID
	Actor   collaboration.ActorID
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
				serve(w, r)
			}
		})
	}
	register(http.MethodPost, "/api/factory/intents", s.saveRevision)
	register(http.MethodGet, "/api/factory/intents/{intentID}", s.revision)
	register(http.MethodGet, "/api/factory/intents/{intentID}/revisions", s.scopeHistory)
	register(http.MethodGet, "/api/factory/intents/{intentID}/overview", s.overview)
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
