package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type CollaborationDiscussions struct {
	Service factoryservice.CollaborationDiscussions
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationDiscussions) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/issues/{number}/comments", func(w http.ResponseWriter, r *http.Request) { s.read(w, r, collaboration.SubjectIssue) })
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/comments", func(w http.ResponseWriter, r *http.Request) { s.read(w, r, collaboration.SubjectPull) })
	return mux
}
func (s CollaborationDiscussions) read(w http.ResponseWriter, r *http.Request, kind collaboration.SubjectKind) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
		return
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, ok := positiveID(r.PathValue("repository"))
	if !ok {
		writeError(w, 400, "invalid_request", "A repository is required.")
		return
	}
	number, ok := positiveID(r.PathValue("number"))
	if !ok {
		writeError(w, 400, "invalid_request", "A discussion number is required.")
		return
	}
	q, err := discussionWindow(r)
	if err != nil {
		discussionReadError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var page any
	if kind == collaboration.SubjectIssue {
		page, err = s.Service.IssueComments(ctx, s.Actor, collaboration.RepositoryID(repo), number, q)
	} else {
		page, err = s.Service.PullComments(ctx, s.Actor, collaboration.RepositoryID(repo), number, q)
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		discussionReadError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
func discussionWindow(r *http.Request) (factoryservice.DiscussionQuery, error) {
	q := factoryservice.DiscussionQuery{Limit: 20}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, factoryservice.ErrDiscussionQuery
	}
	for key, items := range values {
		if len(items) != 1 || key != "cursor" && key != "limit" {
			return q, factoryservice.ErrDiscussionQuery
		}
	}
	q.Cursor = values.Get("cursor")
	if raw, ok := values["limit"]; ok {
		q.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			return q, factoryservice.ErrDiscussionQuery
		}
	}
	return q, q.Validate()
}
func discussionReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrDiscussionQuery):
		writeError(w, 400, "invalid_request", "Invalid discussion query.")
	case errors.Is(err, factoryservice.ErrDiscussionMissing):
		writeError(w, 404, "discussion_not_found", "Discussion is not visible.")
	default:
		writeError(w, 503, "discussion_unavailable", "Discussion reads are unavailable.")
	}
}
