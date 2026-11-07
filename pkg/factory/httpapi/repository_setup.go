package httpapi

import (
	"errors"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/strictjson"
	"net/http"
)

func (s Repositories) workspaceSetup(w http.ResponseWriter, r *http.Request) {
	repository, ok := s.scope(w, r)
	if !ok || !s.current(w, r) {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, 400, "invalid_request", "No query parameters are accepted.")
		return
	}
	if s.Setup == nil {
		writeError(w, 503, "setup_unavailable", "Workspace setup is managed by this host.")
		return
	}
	var view factoryservice.WorkspaceSetup
	var err error
	if r.Method == http.MethodGet {
		view, err = s.Setup.ReadSetup(r.Context(), s.Actor, repository, nil)
	} else if r.Method == http.MethodPost {
		var in struct {
			Paths []string `json:"paths"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 65536), &in) != nil || in.Paths == nil {
			writeError(w, 400, "invalid_setup", "Select repository-relative source paths to preview.")
			return
		}
		view, err = s.Setup.ReadSetup(r.Context(), s.Actor, repository, in.Paths)
	} else {
		var in factoryservice.WorkspaceSetupApproval
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 1024*1024), &in) != nil {
			writeError(w, 400, "invalid_setup", "Setup must contain bounded reviewed files and exact source references.")
			return
		}
		if !s.current(w, r) {
			return
		}
		view, err = s.Setup.ApproveSetup(r.Context(), s.Actor, repository, in)
	}
	if err != nil {
		if errors.Is(err, execution.ErrPreparation) {
			writeError(w, 400, "invalid_setup", "Setup requires a valid command, source, recipe and current workspace owner.")
			return
		}
		if !s.hostError(w, err) {
			CommandError(w, err)
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}
