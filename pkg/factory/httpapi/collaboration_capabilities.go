package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type CollaborationCapabilities struct {
	Service factoryservice.CollaborationCapabilities
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s CollaborationCapabilities) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/capabilities", s.read)
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/issues/{number}/capabilities", s.read)
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/capabilities", func(w http.ResponseWriter, r *http.Request) { s.readSubject(w, r, "pull") })
	return mux
}
func (s CollaborationCapabilities) read(w http.ResponseWriter, r *http.Request) {
	s.readSubject(w, r, "issue")
}
func (s CollaborationCapabilities) readSubject(w http.ResponseWriter, r *http.Request, subject string) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, ok := positiveID(r.PathValue("repository"))
	if !ok || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "A repository without query parameters is required.")
		return
	}
	q := factoryservice.CapabilityQuery{Subject: "repository"}
	if raw := r.PathValue("number"); raw != "" {
		number, ok := positiveID(raw)
		if !ok {
			writeError(w, 400, "invalid_request", "An issue number is required.")
			return
		}
		q.Subject, q.Number = subject, number
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result, err := s.Service.Read(ctx, s.Actor, collaboration.RepositoryID(repo), q)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		if collaborationReadFailure(w, err) {
			return
		}
		switch {
		case errors.Is(err, factoryservice.ErrCapabilitiesQuery):
			writeError(w, 400, "invalid_request", "Invalid capability query.")
		case errors.Is(err, factoryservice.ErrCapabilitiesMissing):
			writeError(w, 404, "resource_not_found", "Capability resource is not visible.")
		case errors.Is(err, factoryservice.ErrCapabilitiesChanged):
			writeError(w, 409, "capabilities_changed", "Permissions changed. Reload the capabilities.")
		default:
			writeError(w, 503, "capabilities_unavailable", "Capabilities are unavailable.")
		}
		return
	}
	writeJSON(w, 200, result)
}
