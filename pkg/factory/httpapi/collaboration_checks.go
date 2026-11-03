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

type CollaborationChecks struct {
	Service factoryservice.CollaborationChecks
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationChecks) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/checks", s.read)
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/checks/{view}", s.read)
	return mux
}
func (s CollaborationChecks) read(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, 400, "invalid_request", "Invalid check query.")
		return
	}
	for key, items := range values {
		if len(items) != 1 || key != "head" && key != "cursor" && key != "limit" {
			writeError(w, 400, "invalid_request", "Only one head, cursor and limit are accepted.")
			return
		}
	}
	q := factoryservice.ChecksQuery{Head: collaboration.CommitID(values.Get("head")), Cursor: values.Get("cursor"), Limit: 20}
	if view := r.PathValue("view"); view != "" {
		if view != "jobs" {
			http.NotFound(w, r)
			return
		}
		q.Kind = "job"
	}
	if raw, exists := values["limit"]; exists {
		q.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid check limit.")
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
		switch {
		case errors.Is(err, factoryservice.ErrChecksQuery):
			writeError(w, 400, "invalid_request", "Invalid check query.")
		case errors.Is(err, factoryservice.ErrChecksMissing):
			writeError(w, 404, "pull_not_found", "PR checks are not visible.")
		case errors.Is(err, factoryservice.ErrChecksChanged):
			writeError(w, 409, "check_changed", "Checks or protection changed. Reload the checks.")
		case errors.Is(err, factoryservice.ErrChecksTooLarge):
			writeError(w, 413, "check_too_large", "The PR check list exceeds the read limit.")
		default:
			writeError(w, 503, "checks_unavailable", "PR checks are unavailable.")
		}
		return
	}
	writeJSON(w, 200, result)
}
