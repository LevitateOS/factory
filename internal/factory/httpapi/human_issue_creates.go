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

// Permit the escaped 64 KiB Markdown envelope only on issue creation submit.
const HumanIssueCreateRequestLimit = 512 << 10

func IsHumanIssueCreateSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 7 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && p[6] == "issues"
}

type HumanIssueCreates struct {
	Service factoryservice.HumanIssueCreates
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

type humanIssueCreateResponse struct {
	humanCommandResponse
	// Nil means no currently readable destination; it is never no-effect proof.
	Issue *collaboration.IssueRef `json:"issue"`
}

func (s HumanIssueCreates) Handler() http.Handler {
	mux := http.NewServeMux()
	base := "/api/factory/collaboration/repositories/{repository}/issues"
	command := "/api/factory/collaboration/repositories/{repository}/issue-creation/commands/{command}"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "submit") })
	mux.HandleFunc("GET "+command, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "lookup") })
	mux.HandleFunc("POST "+command+"/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, "cancel") })
	return mux
}

func (s HumanIssueCreates) handle(w http.ResponseWriter, r *http.Request, action string) {
	if action == "lookup" && r.Method != http.MethodGet || action != "lookup" && r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
		return
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, valid := positiveID(r.PathValue("repository"))
	if !valid || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeError(w, 400, "invalid_request", "Invalid issue creation scope or query.")
		return
	}
	q := factory.HumanIssueCreateCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo)}
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
			limit = HumanIssueCreateRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Issue creation request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid issue creation JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "title" && field != "body" {
					writeError(w, 400, "invalid_request", "Invalid issue creation JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid issue creation command identity.")
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
			var titleValid, bodyValid bool
			q.Title, titleValid = decodeField("title", true)
			q.Body, bodyValid = decodeField("body", false)
			if !titleValid || !bodyValid {
				writeError(w, 400, "invalid_request", "Issue title and supplied body must be strings.")
				return
			}
		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid issue creation command, title or Markdown.")
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
		humanIssueCreateHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanIssueCreateHTTPError(w, factoryservice.ErrHumanIssueCreateUnavailable)
		return
	}
	// Preparation, raw receipts and original writing remain private. Only the
	// canonical reference checked against a fresh normal issue read is public.
	response := humanIssueCreateResponse{humanCommandResponse: humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanIssueCreateFinished(o)}}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
		if o.Record.Effect == collaboration.EffectCommitted {
			scope := q
			scope.Title, scope.Body = "", ""
			if issue, readErr := s.Service.CreatedIssue(ctx, scope, o); readErr == nil {
				response.Issue = &issue
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

func humanIssueCreateHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanIssueCreateQuery):
		writeError(w, 400, "invalid_request", "Invalid issue creation request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "issue_create_command_not_found", "Issue creation command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanIssueCreateAdmission):
		writeError(w, 403, "issue_create_admission_required", "Current human issue creation admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanIssueCreateConflict), errors.Is(err, factory.ErrHumanIssueCreateObservation):
		writeError(w, 409, "issue_create_command_conflict", "Issue creation command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanIssueCreateNamespace):
		writeError(w, 409, "issue_create_context_changed", "The captured issue creation context is no longer current.")
	default:
		writeError(w, 503, "issue_create_delivery_unavailable", "Issue creation could not be reconciled. Keep the original command for lookup.")
	}
}
