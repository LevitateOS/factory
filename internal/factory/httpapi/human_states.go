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

// State changes use the ordinary small command envelope through every host.
const HumanStateRequestLimit = 64 << 10

type HumanStates struct {
	Service factoryservice.HumanStates
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanStates) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"issues", "pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/state"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanStates) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid state scope or query.")
		return
	}
	q := factory.HumanStateCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Subject: "issue", Number: number}
	if kind == "pulls" {
		q.Subject = "pull"
	}
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
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, HumanStateRequestLimit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "State request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid state JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "close" {
					writeError(w, 400, "invalid_request", "Invalid state JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid state command identity.")
				return
			}
			var close *bool
			if json.Unmarshal(input["close"], &close) != nil || close == nil {
				writeError(w, 400, "invalid_request", "Close must be a boolean.")
				return
			}
			q.Close = *close

		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid state command or desired state.")
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
		humanStateHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanStateHTTPError(w, factoryservice.ErrHumanStateUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained preparation stay server-side. Discussion reads retain their normal
	// scope after a committed effect.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanStateFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanStateHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanStateQuery):
		writeError(w, 400, "invalid_request", "Invalid state request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "state_command_not_found", "State command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanStateAdmission):
		writeError(w, 403, "state_admission_required", "Current human state admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanStateConflict), errors.Is(err, factory.ErrHumanStateObservation):
		writeError(w, 409, "state_command_conflict", "State command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanStateNamespace):
		writeError(w, 409, "state_context_changed", "The captured state context is no longer current.")
	default:
		writeError(w, 503, "state_delivery_unavailable", "State delivery could not be reconciled. Keep the original command for lookup.")
	}
}
