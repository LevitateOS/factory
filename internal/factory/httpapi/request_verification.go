package httpapi

import (
	"net/http"

	"github.com/levitateos/sodaos/internal/factory"
)

func (s Requests) verificationAllowance(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	intent := r.PathValue("intentID")
	if r.Method == http.MethodGet {
		current, err := s.Service.VerificationAllowance(r.Context(), s.Actor, intent)
		if err != nil {
			s.verificationAllowanceError(w, err)
			return
		}
		if s.current(w, r) {
			writeJSON(w, 200, current)
		}
		return
	}
	if s.Principal == "" {
		writeError(w, 503, "authority_unavailable", "Verification command authority is unavailable.")
		return
	}
	var change factory.AppVerificationAllowanceChange
	if !decodeObject(w, r, &change) {
		return
	}
	if err := change.Validate(); err != nil {
		writeError(w, 400, "invalid_verification_allowance", err.Error())
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Service.IncreaseVerificationAllowance(r.Context(), s.Actor, intent, s.Principal, change)
	if err != nil {
		s.verificationAllowanceError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}
