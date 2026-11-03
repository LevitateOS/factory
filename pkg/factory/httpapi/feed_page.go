package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// ParseFeedQuery admits one combined feed query from its URL
// parameters: comma-separated repository scope, optional kinds, a
// bounded window and the resume cursor.
func ParseFeedQuery(r *http.Request) (factory.FeedQuery, error) {
	var query factory.FeedQuery
	seen := make(map[int64]bool)
	for _, field := range strings.Split(r.URL.Query().Get("repositories"), ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		repository, err := strconv.ParseInt(field, 10, 64)
		if err != nil || repository <= 0 {
			return factory.FeedQuery{}, factoryservice.ErrFeedQueryInvalid
		}
		if !seen[repository] {
			seen[repository] = true
			query.Repositories = append(query.Repositories, repository)
		}
	}
	if kinds := strings.TrimSpace(r.URL.Query().Get("kinds")); kinds != "" {
		query.Kinds = strings.Split(kinds, ",")
		for i, kind := range query.Kinds {
			query.Kinds[i] = strings.TrimSpace(kind)
		}
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return factory.FeedQuery{}, factoryservice.ErrFeedQueryInvalid
		}
		limit = parsed
	}
	query.Limit = limit
	query.View = strings.TrimSpace(r.URL.Query().Get("view"))
	query.Search = strings.TrimSpace(r.URL.Query().Get("search"))
	query.Role = strings.TrimSpace(r.URL.Query().Get("role"))
	query.Current = r.URL.Query().Get("current") == "true"
	query.Cursor = strings.TrimSpace(r.URL.Query().Get("cursor"))
	if err := query.Validate(); err != nil {
		return factory.FeedQuery{}, err
	}
	return query, nil
}

// page serves the caller's subscribed combined feed window:
// subscription narrowing, then per-entry visibility recheck. The
// subscriber is the host-admitted caller, never a body field.
func (s Feed) page(w http.ResponseWriter, r *http.Request) {
	query, err := ParseFeedQuery(r)
	if err != nil {
		writeError(w, 400, "invalid_request", "Combined feed query is invalid.")
		return
	}
	view, err := s.Service.Page(r.Context(), s.Actor, query, r.URL.Query().Get("scope") == "project", s.Visibility, s.Posts)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrFeedSubscriptionUnavailable):
			writeError(w, 503, "store_unavailable", "Could not read feed subscription.")
		case errors.Is(err, factoryservice.ErrFeedProjectScope):
			writeError(w, 400, "invalid_request", "Select one project.")
		case errors.Is(err, factoryservice.ErrFeedRepositoryUnavailable):
			writeError(w, 404, "repository_not_found", "Project is not visible.")
		default:
			writeError(w, 503, "store_unavailable", "Could not read combined feed.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
