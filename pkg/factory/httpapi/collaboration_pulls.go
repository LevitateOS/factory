package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type CollaborationPulls struct {
	Service factoryservice.CollaborationPulls
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationPulls) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/factory/collaboration/repositories/{repository}/pulls", "/api/factory/collaboration/repositories/{repository}/pulls/{number}"} {
		mux.HandleFunc("GET "+path, s.read)
	}
	return mux
}
func (s CollaborationPulls) read(w http.ResponseWriter, r *http.Request) {
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
	selection, ok := readCollaborationQuery(w, r, collaboration.SubjectPull)
	if !ok {
		return
	}
	query := factoryservice.CollaborationPullQuery(selection)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	page, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repository), query)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		if collaborationReadFailure(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrCollaborationPullQuery):
			writeError(w, 400, "invalid_request", "Invalid pull query.")
		case errors.Is(err, factoryservice.ErrCollaborationPullMissing):
			writeError(w, 404, "pull_not_found", "PR is not visible.")
		default:
			writeError(w, 503, "pulls_unavailable", "PR reads are unavailable.")
		}
		return
	}
	if query.Number > 0 {
		writeJSON(w, 200, page.Items[0])
		return
	}
	writeJSON(w, 200, page)
}
