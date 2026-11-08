package httpapi

import (
	"errors"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/strictjson"
	"net/http"
	"strings"
)

const WorkspaceSetupRequestLimit = 1024 * 1024

func IsWorkspaceSetupSubmission(r *http.Request) bool {
	if r.Method != http.MethodPut || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/repositories/")
	if !ok {
		return false
	}
	repository, ok := strings.CutSuffix(rest, "/factory/setup")
	_, positive := positiveID(repository)
	return ok && positive
}

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
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, WorkspaceSetupRequestLimit), &in) != nil {
			writeError(w, 400, "invalid_setup", "Setup must contain bounded reviewed files and exact source references.")
			return
		}
		if !s.current(w, r) {
			return
		}
		if in.Validate() != nil {
			err = factoryservice.ErrWorkspaceSetupInput
		} else {
			view, err = s.Setup.ApproveSetup(r.Context(), s.Actor, repository, in)
		}
	}
	if err != nil {
		if errors.Is(err, factoryservice.ErrWorkspaceSetupRecovery) {
			writeError(w, http.StatusConflict, "setup_recovery_unavailable", "This saved approval predates exact command receipts. Its outcome cannot be recovered automatically; its records are preserved and setup was not replayed.")
			return
		}
		if errors.Is(err, factoryservice.ErrWorkspaceSetupInput) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_setup_input", "Review the scripts, tools, source selection and limits. No setup approval was recorded.")
			return
		}
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
