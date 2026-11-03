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

// Inline submission admits JSON escaping of 64 KiB original Markdown through
// every host; retained command cancellation keeps the ordinary small envelope.
const HumanInlineRequestLimit = 512 << 10

func IsHumanInlineSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 9 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && p[6] == "pulls" && p[8] == "inline-comments"
}

type HumanInlines struct {
	Service factoryservice.HumanInlines
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanInlines) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/inline-comments"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanInlines) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid inline scope or query.")
		return
	}
	q := factory.HumanInlineCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Number: number}
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
			limit = HumanInlineRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Inline request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid inline JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "draft" && field != "body" && field != "anchor" {
					writeError(w, 400, "invalid_request", "Invalid inline JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid inline command identity.")
				return
			}
			var draft *bool
			var body *string
			anchor, validAnchor := decodeHumanInlineAnchor(input["anchor"])
			if json.Unmarshal(input["draft"], &draft) != nil || draft == nil || json.Unmarshal(input["body"], &body) != nil || body == nil || !validAnchor {
				writeError(w, 400, "invalid_request", "Inline mode, Markdown body and complete immutable anchor are required.")
				return
			}
			q.Draft, q.Body, q.Anchor = *draft, *body, anchor

		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid inline command, anchor or Markdown.")
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
		humanInlineHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanInlineHTTPError(w, factoryservice.ErrHumanInlineUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained preparation stay server-side. Inline reads retain their normal
	// scope after a committed effect.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanInlineFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanInlineHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanInlineQuery):
		writeError(w, 400, "invalid_request", "Invalid inline request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "inline_command_not_found", "Inline command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanInlineAdmission):
		writeError(w, 403, "inline_admission_required", "Current human inline admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanInlineConflict), errors.Is(err, factory.ErrHumanInlineObservation):
		writeError(w, 409, "inline_command_conflict", "Inline command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanInlineNamespace):
		writeError(w, 409, "inline_context_changed", "The captured inline context is no longer current.")
	default:
		writeError(w, 503, "inline_delivery_unavailable", "Inline delivery could not be reconciled. Keep the original command for lookup.")
	}
}

// Every anchor member is explicit, including zero line bounds on file comments.
// Exact property spelling avoids encoding/json accepting case-folded aliases.
func decodeHumanInlineAnchor(raw json.RawMessage) (factory.ReviewThreadAnchor, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 7 {
		return factory.ReviewThreadAnchor{}, false
	}
	for k := range fields {
		switch k {
		case "base", "head", "path", "side", "scope", "start_line", "end_line":
		default:
			return factory.ReviewThreadAnchor{}, false
		}
	}
	var a struct {
		Base      *string `json:"base"`
		Head      *string `json:"head"`
		Path      *string `json:"path"`
		Side      *string `json:"side"`
		Scope     *string `json:"scope"`
		StartLine *int64  `json:"start_line"`
		EndLine   *int64  `json:"end_line"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Base == nil || a.Head == nil || a.Path == nil || a.Side == nil || a.Scope == nil || a.StartLine == nil || a.EndLine == nil {
		return factory.ReviewThreadAnchor{}, false
	}
	anchor := factory.ReviewThreadAnchor{Base: *a.Base, Head: *a.Head, Path: *a.Path, Side: *a.Side, Scope: *a.Scope, StartLine: *a.StartLine, EndLine: *a.EndLine}
	return anchor, anchor.Validate() == nil
}
