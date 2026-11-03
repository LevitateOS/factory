package httpapi

import (
	"bytes"
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

// Thread commands carry bounded original context and no conversation prose.
// Every host retains its ordinary 64 KiB command envelope.
const HumanThreadRequestLimit = 64 << 10

type HumanThreads struct {
	Service factoryservice.HumanThreads
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanThreads) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/selection", s.selection)
	for _, kind := range []string{"pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/thread-resolutions"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanThreads) selection(w http.ResponseWriter, r *http.Request) {
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	repo, validRepo := positiveID(r.PathValue("repository"))
	number, validNumber := positiveID(r.PathValue("number"))
	q := factory.HumanThreadSelectionQuery{Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Number: number, ThreadID: r.PathValue("thread")}
	if !validRepo || !validNumber || q.Validate() != nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeError(w, 400, "invalid_request", "Invalid thread selection scope or query.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	selection, err := s.Service.Selection(ctx, q)
	if !currentAccount(w, r, s.Host, s.Actor, s.Current) {
		return
	}
	if err != nil {
		humanThreadHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, selection)
}

func (s HumanThreads) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid thread scope or query.")
		return
	}
	q := factory.HumanThreadCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Number: number}
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
			limit = HumanThreadRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Thread request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid thread JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "thread_id" && field != "resolved" && field != "anchor" {
					writeError(w, 400, "invalid_request", "Invalid thread JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid thread command identity.")
				return
			}
			var resolved *bool
			var thread *string
			anchor, validAnchor := decodeHumanThreadAnchor(input["anchor"])
			if json.Unmarshal(input["resolved"], &resolved) != nil || resolved == nil || json.Unmarshal(input["thread_id"], &thread) != nil || thread == nil || !validAnchor {
				writeError(w, 400, "invalid_request", "Thread identity, desired resolution and original anchor or explicit null are required.")
				return
			}
			q.Resolved, q.ThreadID, q.Anchor = *resolved, *thread, anchor

		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid thread command or original context.")
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
		humanThreadHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanThreadHTTPError(w, factoryservice.ErrHumanThreadUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained preparation stay server-side. Thread reads retain their normal
	// scope after a committed effect.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanThreadFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanThreadHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanThreadQuery):
		writeError(w, 400, "invalid_request", "Invalid thread request.")
	case errors.Is(err, factoryservice.ErrHumanThreadTooLarge):
		writeError(w, 413, "thread_too_large", "The complete thread exceeds the supported size.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "thread_command_not_found", "Thread command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanThreadAdmission):
		writeError(w, 403, "thread_admission_required", "Current human thread admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanThreadConflict), errors.Is(err, factory.ErrHumanThreadObservation):
		writeError(w, 409, "thread_command_conflict", "Thread command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanThreadNamespace):
		writeError(w, 409, "thread_context_changed", "The captured thread context is no longer current.")
	default:
		writeError(w, 503, "thread_delivery_unavailable", "Thread delivery could not be reconciled. Keep the original command for lookup.")
	}
}

// Explicit null retains unknown context; omission is never equivalent to null.
// Known context reuses the defining complete immutable-anchor decoder.
func decodeHumanThreadAnchor(raw json.RawMessage) (*factory.ReviewThreadAnchor, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true
	}
	anchor, valid := decodeHumanInlineAnchor(raw)
	if !valid {
		return nil, false
	}
	return &anchor, true
}
