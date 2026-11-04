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

type CollaborationRepositories struct {
	Service factoryservice.CollaborationRepositories
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationRepositories) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories", s.read)
	return mux
}
func (s CollaborationRepositories) read(w http.ResponseWriter, r *http.Request) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid_request", "Invalid repository query.")
		return
	}
	for key, items := range values {
		if len(items) != 1 || key != "query" && key != "cursor" && key != "limit" {
			writeError(w, 400, "invalid_request", "Only one query, cursor and limit are accepted.")
			return
		}
	}
	q := factoryservice.RepositoryCatalogQuery{Query: values.Get("query"), Cursor: values.Get("cursor"), Limit: 20}
	if raw, exists := values["limit"]; exists {
		q.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid repository limit.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	p, err := s.Service.Read(ctx, s.Host, s.Actor, q)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		if collaborationReadFailure(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrRepositoryCatalogQuery):
			writeError(w, 400, "invalid_request", "Invalid repository query.")
		case errors.Is(err, factoryservice.ErrRepositoryCatalogMissing):
			writeError(w, 404, "account_not_found", "Repository account is not visible.")
		case errors.Is(err, factoryservice.ErrRepositoryCatalogChanged):
			writeError(w, 409, "catalogue_changed", "Repository access changed. Reload the repositories.")
		default:
			writeError(w, 503, "repositories_unavailable", "Repository discovery is unavailable.")
		}
		return
	}
	writeJSON(w, 200, p)
}
