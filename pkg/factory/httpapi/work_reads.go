package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Work) issue(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	repository, issue, ok := s.scope(w, r)
	if !ok {
		return
	}
	view, err := s.Issues.Read(r.Context(), s.Actor, repository, issue)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrIssueVisibility):
			s.visibilityError(w, err)
		case errors.Is(err, factoryservice.ErrIssueStatus):
			s.statusError(w, err)
		default:
			writeError(w, 503, "store_unavailable", "Could not read issue work.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

func (s Work) assignment(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	repository, issue, ok := s.scope(w, r)
	if !ok {
		return
	}
	view, err := s.Reads.Assignment(r.Context(), s.Actor, repository, issue)
	if err != nil {
		s.recordedReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

func parsePlanComparisonQuery(raw string) (store.PriorityComparisonCursor, int, bool) {
	var cursor store.PriorityComparisonCursor
	if len(raw) > 2048 {
		return cursor, 0, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return cursor, 0, false
	}
	for key := range values {
		if key != "limit" && key != "before_unix" && key != "before_snapshot" {
			return cursor, 0, false
		}
	}
	limit := store.DefaultPriorityComparisons
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > store.MaxPriorityComparisons {
			return cursor, 0, false
		}
		limit = parsed
	}
	beforeUnix := values.Get("before_unix")
	beforeSnapshot := values.Get("before_snapshot")
	if beforeUnix == "" && beforeSnapshot == "" {
		return cursor, limit, true
	}
	parsed, err := strconv.ParseInt(beforeUnix, 10, 64)
	if err != nil || parsed <= 0 || !factory.ValidDigest(beforeSnapshot) {
		return cursor, 0, false
	}
	return store.PriorityComparisonCursor{BeforeUnix: parsed, BeforeSnapshot: beforeSnapshot}, limit, true
}

func (s Work) priority(w http.ResponseWriter, r *http.Request) {
	cursor, limit, ok := parsePlanComparisonQuery(r.URL.RawQuery)
	if r.URL.ForceQuery || !ok {
		writeError(w, 400, "invalid_request", "Use limit 1..20 with an optional before_unix/before_snapshot cursor pair.")
		return
	}
	repository, issue, ok := s.scope(w, r)
	if !ok {
		return
	}
	view, err := s.Reads.Priority(r.Context(), s.Actor, repository, issue, cursor, limit)
	if err != nil {
		s.recordedReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

func (s Work) recordedReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrWorkReadVisibility):
		s.visibilityError(w, err)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not_found", "No dispatch assignment recorded this issue.")
	case errors.Is(err, factoryservice.ErrWorkReadQuery):
		writeError(w, 400, "invalid_request", "Recorded work query is invalid.")
	default:
		writeError(w, 503, "store_unavailable", "Could not read recorded work.")
	}
}

func (s Work) progress(w http.ResponseWriter, r *http.Request) {
	repository, ok := positiveID(r.PathValue("repositoryID"))
	values, err := url.ParseQuery(r.URL.RawQuery)
	if !ok || err != nil || len(r.URL.RawQuery) > 512 || len(values) != 1 || len(values["issues"]) != 1 || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "Use a page of 1..20 distinct issue numbers.")
		return
	}
	parts := strings.Split(values.Get("issues"), ",")
	issues := make([]int64, 0, len(parts))
	for _, part := range parts {
		issue, valid := positiveID(part)
		if !valid {
			writeError(w, 400, "invalid_request", "Invalid issue number.")
			return
		}
		issues = append(issues, issue)
	}
	view, err := s.Reads.Progress(r.Context(), s.Actor, collaboration.RepositoryID(repository), issues)
	if err != nil {
		s.recordedReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
