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

type CollaborationReviews struct {
	Service factoryservice.CollaborationReviews
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationReviews) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, path := range []string{"/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews", "/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews/{review}"} {
		mux.HandleFunc("GET "+path, s.read)
	}
	return mux
}
func (s CollaborationReviews) read(w http.ResponseWriter, r *http.Request) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, ok := positiveID(r.PathValue("repository"))
	number, valid := positiveID(r.PathValue("number"))
	if !ok || !valid {
		writeError(w, 400, "invalid_request", "A repository and PR number are required.")
		return
	}
	q := factoryservice.ReviewQuery{Limit: 20}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid_request", "Invalid review query.")
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
			writeError(w, 400, "invalid_request", "Invalid review limit.")
			return
		}
	}
	if id := r.PathValue("review"); id != "" {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeError(w, 400, "invalid_request", "Review detail accepts no query parameters.")
			return
		}
		q.ReviewID, q.Limit = id, 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	page, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repo), number, q)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		if collaborationReadFailure(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrReviewQuery):
			writeError(w, 400, "invalid_request", "Invalid review query.")
		case errors.Is(err, factoryservice.ErrReviewMissing):
			writeError(w, 404, "review_not_found", "Review is not visible.")
		default:
			writeError(w, 503, "reviews_unavailable", "Review reads are unavailable.")
		}
		return
	}
	if q.ReviewID != "" {
		writeJSON(w, 200, page.Items[0])
		return
	}
	writeJSON(w, 200, page)
}
