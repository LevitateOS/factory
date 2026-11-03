package httpapi

import (
	"errors"
	"net/http"

	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

func (s Repositories) policyAdmissionError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrPolicyAccess):
		s.authorityError(w, err)
	case errors.Is(err, factoryservice.ErrPolicyResolverActor):
		writeError(w, 400, "invalid_actor_binding", "Resolver must reference its separately enrolled binding on the independent reviewer account.")
	case errors.Is(err, factoryservice.ErrPolicyBindingInvalid):
		writeError(w, 400, "invalid_actor_binding", "Choose a current Factory actor binding in this provider instance.")
	case errors.Is(err, factoryservice.ErrPolicyBindingUnavailable):
		writeError(w, 503, "identity_unavailable", "Factory actor identity is unavailable.")
	case errors.Is(err, factoryservice.ErrPolicyInvalid):
		writeError(w, 400, "invalid_policy", "Repository policy is incomplete or invalid.")
	default:
		CommandError(w, err)
	}
}

func (s Repositories) policy(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok || !s.current(w, r) {
		return
	}
	if s.Policies.WriteAccess == nil {
		s.policyAdmissionError(w, factoryservice.ErrPolicyUnavailable)
		return
	}
	if err := s.Policies.WriteAccess(r.Context(), s.Actor, int64(repository)); err != nil {
		s.policyAdmissionError(w, errors.Join(factoryservice.ErrPolicyAccess, err))
		return
	}
	var in factoryservice.PolicyChange
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Policies.Apply(r.Context(), s.Actor, s.Principal, repository, in)
	if err != nil {
		s.policyAdmissionError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}
