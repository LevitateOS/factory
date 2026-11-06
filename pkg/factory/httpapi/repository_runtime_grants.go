package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	sstore "github.com/levitateos/factory/pkg/store"
)

func (s Repositories) runtimeGrantError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factory.ErrPlacement):
		writeError(w, 409, "placement_changed", "Select this computer as the active controller, then refresh the environment grant.")
	case errors.Is(err, factoryservice.ErrPlacementAdminRequired):
		writeError(w, 403, "repository_admin_required", "Current repository administration is required.")
	case errors.Is(err, factoryservice.ErrRuntimeCapacityInvalid):
		writeError(w, 400, "invalid_capacity", "Runtime capacity is incomplete or invalid.")
	case errors.Is(err, factoryservice.ErrRuntimeOperatorInvalid):
		writeError(w, 400, "invalid_operator_grant", "Operator grant is incomplete or invalid.")
	case errors.Is(err, factoryservice.ErrRuntimeEnvironmentInvalid):
		writeError(w, 400, "invalid_environment_grant", "Environment grant is incomplete or invalid.")
	case errors.Is(err, factoryservice.ErrRuntimeGrantUnavailable):
		CommandError(w, err)
	case errors.Is(err, factoryservice.ErrRuntimeGrantAccess):
		s.visibilityError(w, err)
	default:
		CommandError(w, err)
	}
}

func (s Repositories) capacity(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	if !s.current(w, r) {
		return
	}
	var in factoryservice.CapacityChange
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Runtime.ApplyCapacity(r.Context(), s.Actor, s.Principal, in)
	if err != nil {
		s.runtimeGrantError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Repositories) operatorGrant(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok || !s.current(w, r) {
		return
	}
	var in factoryservice.OperatorGrantChange
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Runtime.ApplyOperatorGrant(r.Context(), s.Actor, s.Principal, repository, in)
	if err != nil {
		s.runtimeGrantError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Repositories) environmentGrant(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok || !s.current(w, r) {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	if r.Method == http.MethodGet {
		grant, err := s.Store.EnvironmentGrant(r.Context(), int64(repository))
		var value any
		if err == nil {
			value = grant
		} else if !errors.Is(err, sstore.ErrNotFound) {
			CommandError(w, err)
			return
		}
		if s.current(w, r) {
			writeJSON(w, 200, map[string]any{"environment": value})
		}
		return
	}
	var in factoryservice.EnvironmentGrantChange
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Runtime.ApplyEnvironmentGrant(r.Context(), s.Actor, s.Principal, repository, in)
	if err != nil {
		s.runtimeGrantError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}
