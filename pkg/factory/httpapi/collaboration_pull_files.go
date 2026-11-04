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

type CollaborationPullFiles struct {
	Service factoryservice.CollaborationPullFiles
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationPullFiles) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/files", s.read)
	return mux
}
func (s CollaborationPullFiles) read(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, 400, "invalid_request", "Invalid diff query.")
		return
	}
	for key, items := range values {
		if len(items) != 1 || key != "base" && key != "head" && key != "cursor" && key != "limit" {
			writeError(w, 400, "invalid_request", "Only one base, head, cursor and limit are accepted.")
			return
		}
	}
	q := factoryservice.PullFilesQuery{Base: collaboration.CommitID(values.Get("base")), Head: collaboration.CommitID(values.Get("head")), Cursor: values.Get("cursor"), Limit: 10}
	if raw, exists := values["limit"]; exists {
		q.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid file limit.")
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
		case errors.Is(err, factoryservice.ErrPullFilesQuery):
			writeError(w, 400, "invalid_request", "Invalid diff query.")
		case errors.Is(err, factoryservice.ErrPullFilesMissing):
			writeError(w, 404, "pull_not_found", "PR files are not visible.")
		case errors.Is(err, factoryservice.ErrPullFilesChanged):
			writeError(w, 409, "diff_changed", "The PR diff changed. Reload its files.")
		case errors.Is(err, factoryservice.ErrPullFilesTooLarge):
			writeError(w, 413, "diff_too_large", "The PR file list exceeds the read limit.")
		default:
			writeError(w, 503, "files_unavailable", "PR files are unavailable.")
		}
		return
	}
	writeJSON(w, 200, result)
}
