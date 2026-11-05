package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
	"github.com/levitateos/factory/pkg/strictjson"
)

const HumanBranchCreateRequestLimit = 8192

type HumanBranchCreates struct {
	Service factoryservice.HumanBranchCreates
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

type humanBranchCreateResponse struct {
	humanCommandResponse
	// Nil means no currently readable destination; it is never no-effect proof.
	Branch *collaboration.GitRef `json:"branch"`
}

func (s HumanBranchCreates) Handler() http.Handler {
	mux := http.NewServeMux()
	base := "/api/factory/collaboration/repositories/{repository}/branches"
	command := "/api/factory/collaboration/repositories/{repository}/branch-creation/commands/{command}"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "submit") })
	mux.HandleFunc("GET "+command, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "lookup") })
	mux.HandleFunc("POST "+command+"/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "cancel") })
	return mux
}

func (s HumanBranchCreates) handle(w http.ResponseWriter, r *http.Request, action string) {
	if action == "lookup" && r.Method != http.MethodGet || action != "lookup" && r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
		return
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, valid := positiveID(r.PathValue("repository"))
	if !valid || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeError(w, 400, "invalid_request", "Invalid branch creation scope or query.")
		return
	}
	q := factory.HumanBranchCreateCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo)}
	if action != "lookup" {
		media, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8") {
			writeError(w, 415, "unsupported_media_type", "Send a UTF-8 application/json object.")
			return
		}
		var input map[string]json.RawMessage
		var target any = &input
		if action == "cancel" {
			target = &struct{}{}
		}
		limit := int64(HumanBranchCreateRequestLimit)
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Branch creation request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid branch creation JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "name" && field != "commit" {
					writeError(w, 400, "invalid_request", "Invalid branch creation JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid branch creation command identity.")
				return
			}
			decodeField := func(field string, required bool) (string, bool) {
				raw, present := input[field]
				if !present {
					return "", !required
				}
				var value *string
				if json.Unmarshal(raw, &value) != nil || value == nil {
					return "", false
				}
				return *value, true
			}
			var nameValid, commitValid bool
			q.Name, nameValid = decodeField("name", true)
			commit, validCommit := decodeField("commit", true)
			q.Commit, commitValid = collaboration.CommitID(commit), validCommit
			if !nameValid || !commitValid {
				writeError(w, 400, "invalid_request", "Branch name and commit must be strings.")
				return
			}
		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid branch creation command, name or commit.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var o collaboration.OperationLookup
	var err error
	switch action {
	case "submit":
		o, err = s.Service.Submit(ctx, q)
	case "lookup":
		o, err = s.Service.LookupCommand(ctx, q)
	case "cancel":
		o, err = s.Service.CancelCommand(ctx, q)
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		humanBranchCreateHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanBranchCreateHTTPError(w, factoryservice.ErrHumanBranchCreateUnavailable)
		return
	}
	// Preparation, raw receipts and original intent remain private. Only the
	// canonical reference checked against a fresh normal branch read is public.
	response := humanBranchCreateResponse{humanCommandResponse: humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanBranchCreateFinished(o)}}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
		if o.Record.Effect == collaboration.EffectCommitted {
			scope := q
			scope.Name, scope.Commit = "", ""
			if branch, readErr := s.Service.CreatedBranch(ctx, scope, o); readErr == nil {
				response.Branch = &branch
			}
		}
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanBranchCreateHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanBranchCreateQuery):
		writeError(w, 400, "invalid_request", "Invalid branch creation request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "branch_create_command_not_found", "Branch creation command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanBranchCreateAdmission):
		writeError(w, 403, "branch_create_admission_required", "Current human branch creation admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanBranchCreateConflict), errors.Is(err, factory.ErrHumanBranchCreateObservation):
		writeError(w, 409, "branch_create_command_conflict", "Branch creation command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanBranchCreateNamespace):
		writeError(w, 409, "branch_create_context_changed", "The captured branch creation context is no longer current.")
	default:
		writeError(w, 503, "branch_create_delivery_unavailable", "Branch creation could not be reconciled. Keep the original command for lookup.")
	}
}
