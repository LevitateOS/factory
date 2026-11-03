package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Thread pages include full Markdown and per-comment immutable anchors. Allow
// the bounded native page plus canonical observation/identity metadata.
const CollaborationThreadResponseLimit = 2 << 20

type CollaborationThreads struct {
	Service factoryservice.CollaborationThreads
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationThreads) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads", "/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}", "/api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/comments"} {
		mux.HandleFunc("GET "+path, s.read)
	}
	return mux
}
func (s CollaborationThreads) read(w http.ResponseWriter, r *http.Request) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, ok := positiveID(r.PathValue("repository"))
	number, valid := positiveID(r.PathValue("number"))
	if !ok || !valid {
		writeError(w, 400, "invalid_request", "A repository and PR number are required.")
		return
	}
	q := factoryservice.ThreadQuery{Limit: 20}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid_request", "Invalid thread query.")
		return
	}
	for key, items := range values {
		if len(items) != 1 || key != "cursor" && key != "limit" {
			writeError(w, 400, "invalid_request", "Only one cursor and limit are accepted.")
			return
		}
	}
	q.Cursor = values.Get("cursor")
	if raw, exists := values["limit"]; exists {
		q.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid thread limit.")
			return
		}
	}
	if id := r.PathValue("thread"); id != "" {
		if !strings.HasSuffix(r.URL.Path, "/comments") && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
			writeError(w, 400, "invalid_request", "Thread detail accepts no query parameters.")
			return
		}
		q.ThreadID = id
		if !strings.HasSuffix(r.URL.Path, "/comments") {
			q.Limit = 10
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	page, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repo), number, q)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrThreadQuery):
			writeError(w, 400, "invalid_request", "Invalid thread query.")
		case errors.Is(err, factoryservice.ErrThreadChanged):
			writeError(w, 409, "thread_changed", "Thread placement changed. Reload the conversation.")
		case errors.Is(err, factoryservice.ErrThreadsTooLarge):
			writeError(w, 413, "threads_too_large", "Thread catalogue exceeds the read limit.")
		case errors.Is(err, factoryservice.ErrThreadMissing):
			writeError(w, 404, "thread_not_found", "Thread is not visible.")
		default:
			writeError(w, 503, "threads_unavailable", "Thread reads are unavailable.")
		}
		return
	}
	if strings.HasSuffix(r.URL.Path, "/comments") {
		writeJSON(w, 200, page.Items[0].Comments)
		return
	}
	if q.ThreadID != "" {
		writeJSON(w, 200, page.Items[0])
		return
	}
	writeJSON(w, 200, page)
}
