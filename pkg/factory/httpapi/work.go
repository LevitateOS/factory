package httpapi

import (
	"net/http"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Work serves recorded issue/journey, assignment and priority facts for a bound
// principal. Services own current resource visibility and factual projections;
// hosts retain account/channel admission and sanitized provider error rendering.
type Work struct {
	Reads           factoryservice.WorkReads
	Issues          factoryservice.Issues
	Host            collaboration.HostID
	Actor           collaboration.ActorID
	Current         func(http.ResponseWriter, *http.Request) bool
	VisibilityError func(http.ResponseWriter, error)
	StatusError     func(http.ResponseWriter, error)
}

// Handler starts no listener, runtime, workflow, provider client or model call.
func (s Work) Handler() http.Handler {
	mux := http.NewServeMux()
	register := func(path string, serve http.HandlerFunc) {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
				return
			}
			if s.current(w, r) {
				if s.Reads.Store != nil && s.Issues.Store != nil && s.Reads.Store != s.Issues.Store {
					writeError(w, 503, "store_unavailable", "Recorded work is unavailable.")
					return
				}
				serve(w, r)
			}
		})
	}
	base := "/api/repositories/{repositoryID}/factory/issues/{issueID}"
	register(base, s.issue)
	register(base+"/assignment", s.assignment)
	register(base+"/plan", s.priority)
	return mux
}

func (s Work) current(w http.ResponseWriter, r *http.Request) bool {
	return currentAccount(w, r, s.Host, s.Actor, s.Current)
}

func (s Work) scope(w http.ResponseWriter, r *http.Request) (collaboration.RepositoryID, int64, bool) {
	repository, ok := positiveID(r.PathValue("repositoryID"))
	if !ok {
		writeError(w, 404, "not_found", "Repository not found.")
		return 0, 0, false
	}
	issue, ok := positiveID(r.PathValue("issueID"))
	if !ok {
		writeError(w, 404, "not_found", "Factory issue not found.")
		return 0, 0, false
	}
	return collaboration.RepositoryID(repository), issue, true
}

func positiveID(value string) (int64, bool) {
	if len(value) == 0 || len(value) > 19 {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

func (s Work) visibilityError(w http.ResponseWriter, err error) {
	if s.VisibilityError != nil {
		s.VisibilityError(w, err)
		return
	}
	writeError(w, 503, "authority_unavailable", "Current repository visibility is unavailable.")
}

func (s Work) statusError(w http.ResponseWriter, err error) {
	if s.StatusError != nil {
		s.StatusError(w, err)
		return
	}
	writeError(w, 503, "issue_status_unavailable", "Current issue acceptance evidence is unavailable.")
}
