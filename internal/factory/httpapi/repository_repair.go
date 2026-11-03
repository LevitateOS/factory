package httpapi

import (
	"errors"
	"net/http"

	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

func (s Repositories) repairFundingError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrRepairFundingAccess):
		s.authorityError(w, err)
	case errors.Is(err, factoryservice.ErrRepairFundingCommand):
		writeError(w, 400, "invalid_command", "Settings changes require a client-generated command identity.")
	case errors.Is(err, factoryservice.ErrRepairFundingInvalid):
		writeError(w, 400, "invalid_repair_grant", "Repair limits exceed the finite repository allowance or are invalid.")
	case errors.Is(err, factoryservice.ErrRepairFundingIncrease):
		writeError(w, 400, "invalid_repair_grant", "An explicit repair limit increase must state its effect and preserves prior consumption.")
	case errors.Is(err, factoryservice.ErrRepairFundingSponsor):
		writeError(w, 403, "repair_sponsor_required", "Only the recorded repair sponsor can change this allowance.")
	default:
		CommandError(w, err)
	}
}

func (s Repositories) repair(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	repository, ok := s.scope(w, r)
	if !ok {
		return
	}
	application := s.Repair
	if r.Method == http.MethodGet {
		view, err := application.Read(r.Context(), s.Actor, repository)
		if err != nil {
			s.repairFundingError(w, err)
			return
		}
		if s.current(w, r) {
			writeJSON(w, 200, view)
		}
		return
	}
	if !s.current(w, r) {
		return
	}
	var in factoryservice.RepairFundingChange
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := application.Apply(r.Context(), s.Actor, s.Principal, repository, in)
	if err != nil {
		s.repairFundingError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}
