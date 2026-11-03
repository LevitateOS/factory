package httpapi

import (
	"context"
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

// Covers JSON escaping of 64 KiB original Markdown; other mutation limits stay
// unchanged. Host body wrappers must admit this envelope on these two routes.
const HumanCommentRequestLimit = 512 << 10

func IsHumanCommentSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 9 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && (p[6] == "issues" || p[6] == "pulls") && p[8] == "comments"
}

type HumanComments struct {
	Service factoryservice.HumanComments
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

type humanCommandResponse struct {
	CommandID    string                          `json:"command_id"`
	Observation  collaboration.LookupState       `json:"observation"`
	Effect       collaboration.EffectState       `json:"effect,omitempty"`
	Cancellation collaboration.CancellationState `json:"cancellation,omitempty"`
	Completion   collaboration.CompletionState   `json:"completion,omitempty"`
	Finished     bool                            `json:"finished"`
}

func (s HumanComments) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"issues", "pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/comments"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanComments) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
	if action == "lookup" && r.Method != http.MethodGet || action != "lookup" && r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "HTTP method not supported.")
		return
	}
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, validRepo := positiveID(r.PathValue("repository"))
	number, validNumber := positiveID(r.PathValue("number"))
	if !validRepo || !validNumber || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeError(w, 400, "invalid_request", "Invalid comment scope or query.")
		return
	}
	q := factory.HumanCommentCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Subject: "issue", Number: number}
	if kind == "pulls" {
		q.Subject = "pull"
	}
	if action != "lookup" {
		media, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8") {
			writeError(w, 415, "unsupported_media_type", "Send a UTF-8 application/json object.")
			return
		}
		var input struct {
			CommandID string `json:"command_id"`
			Body      string `json:"body"`
		}
		var target any = &input
		if action == "cancel" {
			target = &struct{}{}
		}
		limit := int64(64 << 10)
		if action == "submit" {
			limit = HumanCommentRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Comment request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid comment JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			q.ID, q.Body = input.CommandID, input.Body
		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid comment command or Markdown.")
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
		humanCommentHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanCommentHTTPError(w, factoryservice.ErrHumanCommentUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained Markdown stay server-side. Refresh the shared discussion read
	// after a committed effect to obtain the visible comment with normal read scope.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanCommentFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanCommentHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanCommentQuery):
		writeError(w, 400, "invalid_request", "Invalid comment request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "comment_command_not_found", "Comment command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanCommentAdmission):
		writeError(w, 403, "comment_admission_required", "Current human comment admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanCommentConflict), errors.Is(err, factory.ErrHumanCommentObservation):
		writeError(w, 409, "comment_command_conflict", "Comment command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanCommentNamespace):
		writeError(w, 409, "comment_context_changed", "The captured comment context is no longer current.")
	default:
		writeError(w, 503, "comment_delivery_unavailable", "Comment delivery could not be reconciled. Keep the original command for lookup.")
	}
}
