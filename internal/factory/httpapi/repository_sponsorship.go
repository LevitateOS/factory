package httpapi

import (
	"errors"
	"net/http"

	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

func (s Repositories) sponsorshipError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrSponsorshipInvalid):
		writeError(w, 400, "invalid_sponsorship", "Sponsorship is incomplete or invalid.")
	case errors.Is(err, factoryservice.ErrSponsorshipConnectionMissing):
		writeError(w, 404, "not_found", "Provider connection not found.")
	case errors.Is(err, factoryservice.ErrSponsorshipOwnerRequired):
		writeError(w, 403, "connection_owner_required", "Only the connection owner can sponsor factory use.")
	case errors.Is(err, factoryservice.ErrSponsorshipGrantUnavailable):
		writeError(w, 409, "grant_unavailable", "Give this repository's Project access to the connection first.")
	case errors.Is(err, factoryservice.ErrSponsorshipBrokerUnavailable):
		writeError(w, 503, "identity_unavailable", "Identity service is unavailable.")
	case errors.Is(err, factoryservice.ErrSponsorshipAccess):
		s.visibilityError(w, err)
	default:
		CommandError(w, err)
	}
}

func (s Repositories) sponsorship(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok || !s.current(w, r) {
		return
	}
	var in factoryservice.SponsorshipChange
	if !decodeObject(w, r, &in) {
		return
	}
	if !s.current(w, r) {
		return
	}
	result, err := s.Sponsorships.Apply(r.Context(), s.Actor, s.Principal, repository, r.PathValue("connection"), in)
	if err != nil {
		s.sponsorshipError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}
