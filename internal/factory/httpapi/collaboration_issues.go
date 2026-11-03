package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

// Full Markdown and canonical metadata may exceed the ordinary 64 KiB envelope.
// Native pages remain independently bounded; the frontend also caps this result.
const CollaborationIssueResponseLimit = 2 << 20

type CollaborationIssues struct {
	Service factoryservice.CollaborationIssues
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationIssues) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/factory/collaboration/repositories/{repository}/issues", "/api/factory/collaboration/repositories/{repository}/issues/{number}"} {
		mux.HandleFunc("GET "+path, s.read)
	}
	return mux
}
func (s CollaborationIssues) read(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
		return
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repository, ok := positiveID(r.PathValue("repository"))
	if !ok {
		writeError(w, 400, "invalid_request", "A repository is required.")
		return
	}
	query := factoryservice.CollaborationIssueQuery{Limit: 20}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid_request", "Invalid issue query.")
		return
	}
	for key, items := range values {
		if len(items) != 1 || key != "cursor" && key != "limit" && key != "state" {
			writeError(w, 400, "invalid_request", "Only one cursor, limit and state are accepted.")
			return
		}
	}
	query.Cursor = values.Get("cursor")
	query.State = values.Get("state")
	if raw, exists := values["limit"]; exists {
		query.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid issue limit.")
			return
		}
	}
	if raw := r.PathValue("number"); raw != "" {
		number, ok := positiveID(raw)
		if !ok || r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeError(w, 400, "invalid_request", "An issue number without query parameters is required.")
			return
		}
		query.Number, query.Limit = number, 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	page, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repository), query)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrCollaborationIssueQuery):
			writeError(w, 400, "invalid_request", "Invalid issue query.")
		case errors.Is(err, factoryservice.ErrCollaborationIssueMissing):
			writeError(w, 404, "issue_not_found", "Issue is not visible.")
		default:
			writeError(w, 503, "issues_unavailable", "Issue reads are unavailable.")
		}
		return
	}
	if query.Number > 0 {
		writeJSON(w, 200, page.Items[0])
		return
	}
	writeJSON(w, 200, page)
}
