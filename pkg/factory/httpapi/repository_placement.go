package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Repositories) placement(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	repository, ok := s.scope(w, r)
	if !ok || !s.current(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		canSelect := s.Placements.AdminAccess != nil && s.Placements.Store == s.Store && s.Placements.AdminAccess(r.Context(), s.Actor, repository) == nil
		result := map[string]any{"placement": nil, "runtime": s.Placements.Runtime.ID, "can_select": canSelect}
		p, err := s.Store.Placement(r.Context(), repository)
		if errors.Is(err, store.ErrNotFound) {
			if s.current(w, r) {
				writeJSON(w, 200, result)
			}
			return
		}
		if err != nil {
			CommandError(w, err)
			return
		}
		if s.current(w, r) {
			result["placement"] = p
			writeJSON(w, 200, result)
		}
		return
	}
	if s.Placements.Store != s.Store {
		CommandError(w, factoryservice.ErrPlacementUnavailable)
		return
	}
	var in factoryservice.PlacementChange
	if !decodeObject(w, r, &in) || !s.current(w, r) {
		return
	}
	p, err := s.Placements.Apply(r.Context(), s.Actor, repository, in)
	if err != nil {
		if errors.Is(err, factory.ErrPlacement) {
			writeError(w, 409, "placement_changed", "Refresh placement; retire existing work before selecting a new generation.")
		} else if errors.Is(err, factoryservice.ErrPlacementUnavailable) {
			writeError(w, 503, "placement_unavailable", "Runtime placement is unavailable.")
		} else if errors.Is(err, factoryservice.ErrPlacementAdminRequired) {
			writeError(w, 403, "repository_admin_required", "Current repository administration is required to select a controller.")
		} else {
			s.authorityError(w, err)
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, map[string]any{"placement": p})
	}
}
