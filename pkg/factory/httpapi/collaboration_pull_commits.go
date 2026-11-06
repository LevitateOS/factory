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

const CollaborationPullCommitsResponseLimit = 1 << 20

type CollaborationPullCommits struct {
	Service factoryservice.CollaborationPullCommits
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationPullCommits) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/commits", s.read)
	return mux
}
func (s CollaborationPullCommits) read(w http.ResponseWriter, r *http.Request) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repository, ok := positiveID(r.PathValue("repository"))
	number, valid := positiveID(r.PathValue("number"))
	if !ok || !valid {
		writeError(w, 400, "invalid_request", "A repository and PR number are required.")
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid_request", "Invalid commit query.")
		return
	}
	for key, items := range values {
		if len(items) != 1 || key != "head" && key != "cursor" && key != "limit" {
			writeError(w, 400, "invalid_request", "Only one head, cursor and limit are accepted.")
			return
		}
	}
	q := factoryservice.PullCommitsQuery{Head: collaboration.CommitID(values.Get("head")), Cursor: values.Get("cursor"), Limit: 20}
	if raw, exists := values["limit"]; exists {
		q.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid commit limit.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repository), number, q)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		if collaborationReadFailure(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrPullCommitsQuery):
			writeError(w, 400, "invalid_request", "Invalid commit query.")
		case errors.Is(err, factoryservice.ErrPullCommitsMissing):
			writeError(w, 404, "pull_not_found", "PR commits are not visible.")
		case errors.Is(err, factoryservice.ErrPullCommitsChanged):
			writeError(w, 409, "commits_changed", "The PR head changed. Reload its commits.")
		default:
			writeError(w, 503, "commits_unavailable", "PR commits are unavailable.")
		}
		return
	}
	writeJSON(w, 200, result)
}
