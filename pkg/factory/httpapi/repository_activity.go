package httpapi

import (
	"errors"
	"net/http"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

func (s Repositories) activityError(w http.ResponseWriter, err error) {
	if s.hostError(w, err) {
		return
	}
	switch {
	case errors.Is(err, factoryservice.ErrActivityNavigation):
		writeError(w, 503, "invalid_native_metadata", "Repository metadata cannot produce a local link.")
	case errors.Is(err, factoryservice.ErrActivityVisibility):
		s.visibilityError(w, err)
	case errors.Is(err, factoryservice.ErrActivityWriteAccess):
		s.authorityError(w, err)
	case errors.Is(err, factoryservice.ErrActivityInvalid):
		writeError(w, 400, "invalid_activity_settings", "A current revision and all three activity settings are required.")
	case errors.Is(err, store.ErrStaleRevision):
		writeError(w, 412, "stale_revision", "Activity settings changed; refresh and retry.")
	default:
		writeError(w, 503, "store_unavailable", "Repository activity is unavailable.")
	}
}

func (s Repositories) activity(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	repository, ok := s.scope(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut && !s.current(w, r) {
		return
	}
	application := s.Activity
	var activity factory.RepositoryActivity
	var err error
	if r.Method == http.MethodPut {
		var in struct {
			ExpectedRevision *int64 `json:"expected_revision"`
			Settings         *struct {
				IssueCreated  *bool `json:"issue_created"`
				PRCreated     *bool `json:"pr_created"`
				VerifiedMerge *bool `json:"verified_merge"`
			} `json:"settings"`
		}
		if !decodeObject(w, r, &in) {
			return
		}
		if in.ExpectedRevision == nil || *in.ExpectedRevision < 0 || in.Settings == nil || in.Settings.IssueCreated == nil || in.Settings.PRCreated == nil || in.Settings.VerifiedMerge == nil {
			writeError(w, 400, "invalid_activity_settings", "A current revision and all three activity settings are required.")
			return
		}
		settings := factory.ActivitySettings{IssueCreated: *in.Settings.IssueCreated, PRCreated: *in.Settings.PRCreated, VerifiedMerge: *in.Settings.VerifiedMerge}
		if !s.current(w, r) {
			return
		}
		activity, err = application.Update(r.Context(), s.Actor, repository, *in.ExpectedRevision, settings)
	} else {
		activity, err = application.Read(r.Context(), s.Actor, repository)
	}
	if err != nil {
		s.activityError(w, err)
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, activity)
	}
}
