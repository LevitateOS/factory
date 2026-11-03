package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func parseRevisionHistoryQuery(raw string) (int, bool) {
	if len(raw) > 512 {
		return 0, false
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, false
	}
	for key := range values {
		if key != "limit" {
			return 0, false
		}
	}
	limit := factoryservice.DefaultRevisionComparisons
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > factoryservice.MaxRevisionComparisons {
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

// scopeHistory projects recorded comparison facts after bound owner admission.
func (s Requests) scopeHistory(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseRevisionHistoryQuery(r.URL.RawQuery)
	if r.URL.ForceQuery || !ok {
		writeError(w, 400, "invalid_request", "Use limit 1..5.")
		return
	}
	view, err := s.Service.ScopeHistory(r.Context(), s.Actor, r.PathValue("intentID"), limit)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrRequestNotFound):
			writeError(w, 404, "not_found", "Build request not found.")
		case errors.Is(err, factoryservice.ErrScopeHistoryInput):
			writeError(w, 400, "invalid_request", "Use limit 1..5.")
		case errors.Is(err, factoryservice.ErrScopeComparisonInvalid):
			writeError(w, 500, "history_invalid", "Could not compare request revisions.")
		default:
			writeError(w, 503, "store_unavailable", "Could not read request history.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
