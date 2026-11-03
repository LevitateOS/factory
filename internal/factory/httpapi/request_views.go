package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func (s Requests) departments(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		result, err := s.Service.Departments(r.Context(), s.Actor)
		if err != nil {
			CommandError(w, err)
			return
		}
		if s.current(w, r) {
			writeJSON(w, 200, result)
		}
		return
	}
	if !s.current(w, r) {
		return
	}
	var d factory.Department
	if !decodeObject(w, r, &d) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Service.CreateDepartment(r.Context(), s.Actor, d)
	if errors.Is(err, factoryservice.ErrDepartmentInputInvalid) {
		writeError(w, 400, "invalid_department", err.Error())
		return
	}
	if err != nil {
		CommandError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 201, result)
	}
}

func (s Requests) revision(w http.ResponseWriter, r *http.Request) {
	result, err := s.Service.Revision(r.Context(), s.Actor, r.PathValue("intentID"))
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

// savedRevision confirms only the exact persisted input requested by the owner.
// Current account admission is checked both by the route and before disclosure.
func (s Requests) savedRevision(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_revision", "No query parameters are accepted.")
		return
	}
	result, err := s.Service.SavedRevision(r.Context(), s.Actor, r.PathValue("intentID"), r.PathValue("revisionID"))
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Requests) catalogue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 20
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, 400, "invalid_list", "Invalid list limit.")
			return
		}
		limit = n
	}
	var repository int64
	if raw := q.Get("repository"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeError(w, 400, "invalid_list", "Invalid repository filter.")
			return
		}
		repository = n
	}
	f := factory.IntentListFilter{Cursor: q.Get("cursor"), State: q.Get("state"), Repository: repository, Department: q.Get("department"), Limit: limit}
	if err := f.Validate(); err != nil {
		writeError(w, 400, "invalid_list", err.Error())
		return
	}
	result, next, err := s.Service.Catalogue(r.Context(), s.Actor, f)
	if err != nil {
		CommandError(w, err)
		return
	}
	out := map[string]any{"intents": result}
	if next != "" {
		out["next_cursor"] = next
	}
	if s.current(w, r) {
		writeJSON(w, 200, out)
	}
}

func (s Requests) history(w http.ResponseWriter, r *http.Request) {
	intent := r.PathValue("intentID")
	if !project.ValidDecisionID(intent) {
		writeError(w, 404, "not_found", "Build request not found.")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, 400, "invalid_history", "Invalid history limit.")
			return
		}
		limit = n
	}
	result, err := s.Service.History(r.Context(), s.Actor, intent, limit)
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, map[string]any{"intent": intent, "revisions": result})
	}
}

func (s Requests) review(w http.ResponseWriter, r *http.Request) {
	read, err := s.Service.Review(r.Context(), s.Actor, r.PathValue("intentID"), r.URL.Query().Get("revision"))
	switch {
	case errors.Is(err, factoryservice.ErrRequestNotFound):
		writeError(w, 404, "not_found", "Build request not found.")
		return
	case errors.Is(err, factoryservice.ErrRequestRevisionInvalid):
		writeError(w, 400, "invalid_revision", "Invalid review revision.")
		return
	case err != nil:
		CommandError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, read)
	}
}

func (s Requests) saveRevision(w http.ResponseWriter, r *http.Request) {
	if !s.current(w, r) {
		return
	}
	var in factory.IntentRevision
	if !decodeObject(w, r, &in) {
		return
	}
	requests := s.Service
	admitted := s.Actor
	if !s.current(w, r) {
		return
	}
	result, err := requests.SaveRevision(r.Context(), admitted, in)
	switch {
	case errors.Is(err, factoryservice.ErrRequestInputInvalid):
		writeError(w, 400, "invalid_intent", err.Error())
		return
	case errors.Is(err, factoryservice.ErrRequestWriteAccess):
		s.authorityError(w, err)
		return
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 412, "stale_revision", "The reviewed build request changed.")
		return
	case err != nil:
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 201, result)
	}
}

// overview reads the admitted owner's stored request facts.
// GET never judges, provisions or dispatches.
func (s Requests) overview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	view, err := s.Service.Overview(r.Context(), s.Actor, r.PathValue("intentID"))
	if err != nil {
		if errors.Is(err, factoryservice.ErrRequestNotFound) {
			writeError(w, 404, "not_found", "Build request not found.")
		} else {
			writeError(w, 503, "store_unavailable", "Could not read build request overview.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

// Opening the exact proposal saves a draft only. Ordinary planning, acceptance
// and generation retain their later authority.
func (s Requests) reviewPost(w http.ResponseWriter, r *http.Request) {
	if !s.current(w, r) {
		return
	}
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Posts.ReviewProduct(r.Context(), s.Actor, r.PathValue("id"), in.Revision)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrPostProductNotFound), errors.Is(err, factoryservice.ErrPostNotFound):
			writeError(w, 404, "product_change_not_found", "Product change is not available.")
		case errors.Is(err, factoryservice.ErrRequestWriteAccess):
			s.authorityError(w, err)
		case errors.Is(err, factoryservice.ErrPostVisibility):
			s.visibilityError(w, err)
		case errors.Is(err, store.ErrStaleRevision):
			writeError(w, 412, "stale_revision", "The comment or accepted product changed. Refresh before reviewing.")
		default:
			CommandError(w, err)
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

// savedExploration confirms only the atomic transition from the private input.
func (s Requests) savedExploration(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	result, err := s.Service.SavedExploration(r.Context(), s.Actor, r.PathValue("intentID"), r.PathValue("revisionID"))
	if err != nil {
		RequestReadError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}
