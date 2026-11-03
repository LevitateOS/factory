package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
	"github.com/levitateos/sodaos/internal/store"
	"github.com/levitateos/sodaos/internal/strictjson"
)

// Permit the escaped 64 KiB Markdown envelope only on pull creation submit.
const HumanPullCreateRequestLimit = 512 << 10

func IsHumanPullCreateSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 7 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && p[6] == "pulls"
}

type HumanPullCreates struct {
	Service factoryservice.HumanPullCreates
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanPullCreates) Handler() http.Handler {
	mux := http.NewServeMux()
	base := "/api/factory/collaboration/repositories/{repository}/pulls"
	command := "/api/factory/collaboration/repositories/{repository}/pull-creation/commands/{command}"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "submit") })
	mux.HandleFunc("GET "+command, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "lookup") })
	mux.HandleFunc("POST "+command+"/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "cancel") })
	return mux
}

func (s HumanPullCreates) handle(w http.ResponseWriter, r *http.Request, action string) {
	if action == "lookup" && r.Method != http.MethodGet || action != "lookup" && r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
		return
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, valid := positiveID(r.PathValue("repository"))
	if !valid || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeError(w, 400, "invalid_request", "Invalid pull creation scope or query.")
		return
	}
	q := factory.HumanPullCreateCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo)}
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
		limit := int64(64 << 10)
		if action == "submit" {
			limit = HumanPullCreateRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Pull creation request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid pull creation JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "head_repository" && field != "base_branch" && field != "head_branch" && field != "title" && field != "body" && field != "allow_maintainer_edit" {
					writeError(w, 400, "invalid_request", "Invalid pull creation JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid pull creation command identity.")
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
			head, headValid := decodeField("head_repository", true)
			headID, positive := positiveID(head)
			q.HeadRepository = collaboration.RepositoryID(headID)
			var baseValid, branchValid bool
			q.BaseBranch, baseValid = decodeField("base_branch", true)
			q.HeadBranch, branchValid = decodeField("head_branch", true)
			if !headValid || !positive || !baseValid || !branchValid {
				writeError(w, 400, "invalid_request", "Select a source repository ID and branch names as strings.")
				return
			}
			if raw, present := input["allow_maintainer_edit"]; present {
				var value *bool
				if json.Unmarshal(raw, &value) != nil || value == nil {
					writeError(w, 400, "invalid_request", "Maintainer edit selection must be a boolean.")
					return
				}
				q.AllowMaintainerEdit = *value
			}
			var titleValid, bodyValid bool
			q.Title, titleValid = decodeField("title", true)
			q.Body, bodyValid = decodeField("body", false)
			if !titleValid || !bodyValid {
				writeError(w, 400, "invalid_request", "Pull title and supplied body must be strings.")
				return
			}
		}
	}
	if !factory.ValidID(q.ID) || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid pull creation command, title or Markdown.")
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
		humanPullCreateHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanPullCreateHTTPError(w, factoryservice.ErrHumanPullCreateUnavailable)
		return
	}
	// Receipts, provider IDs, captured preparation and original writing remain
	// private. Refresh normal PR reads under current visibility after commit.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanPullCreateFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanPullCreateHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanPullCreateQuery):
		writeError(w, 400, "invalid_request", "Invalid pull creation request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "pull_create_command_not_found", "Pull creation command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanPullCreateAdmission):
		writeError(w, 403, "pull_create_admission_required", "Current human pull creation admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanPullCreateConflict), errors.Is(err, factory.ErrHumanPullCreateObservation):
		writeError(w, 409, "pull_create_command_conflict", "Pull creation command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanPullCreateNamespace):
		writeError(w, 409, "pull_create_context_changed", "The captured pull creation context is no longer current.")
	default:
		writeError(w, 503, "pull_create_delivery_unavailable", "Pull creation could not be reconciled. Keep the original command for lookup.")
	}
}
