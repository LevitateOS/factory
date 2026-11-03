package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Requests) verificationAllowanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrRequestWriteAccess):
		s.authorityError(w, err)
	case errors.Is(err, factory.ErrVerificationAllowanceOwner):
		writeError(w, 403, "intent_owner_required", "Only this build request's owner can read or increase verification limits.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not_found", "Build request not found.")
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 409, "stale_revision", "Verification limits changed; refresh before submitting another increase.")
	case errors.Is(err, factory.ErrVerificationAllowanceIncrease):
		writeError(w, 409, "increase_required", "Verification limits can only increase; prior usage remains charged.")
	case errors.Is(err, factory.ErrVerificationAllowanceState):
		writeError(w, 409, "accepted_intent_required", "Accept this build request for a repository before increasing verification limits.")
	default:
		CommandError(w, err)
	}
}
