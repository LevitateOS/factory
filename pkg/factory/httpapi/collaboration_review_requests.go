package httpapi

import (
	"context"
	"errors"
	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"net/http"
	"time"
)

type CollaborationReviewRequests struct {
	Service factoryservice.CollaborationReviewRequests
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationReviewRequests) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/review-requests", s.read)
	return mux
}
func (s CollaborationReviewRequests) read(w http.ResponseWriter, r *http.Request) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, validRepo := positiveID(r.PathValue("repository"))
	number, validNumber := positiveID(r.PathValue("number"))
	if !validRepo || !validNumber || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeError(w, 400, "invalid_request", "Invalid reviewer read scope or query.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repo), number)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		if collaborationReadFailure(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrReviewRequestReadQuery):
			writeError(w, 400, "invalid_request", "Invalid reviewer read scope.")
		case errors.Is(err, factoryservice.ErrReviewRequestReadMissing):
			writeError(w, 404, "review_requests_not_found", "Review requests are not visible.")
		default:
			writeError(w, 503, "review_requests_unavailable", "Review request reads are unavailable.")
		}
		return
	}
	writeJSON(w, 200, snapshot)
}
