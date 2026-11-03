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

// Covers JSON escaping of 64 KiB original lock reason; other limits stay
// unchanged. Host body wrappers must admit this envelope on these two routes.
const HumanLockRequestLimit = 512 << 10

func IsHumanLockSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 9 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && (p[6] == "issues" || p[6] == "pulls") && p[8] == "locks"
}

type HumanLocks struct {
	Service factoryservice.HumanLocks
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanLocks) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"issues", "pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/locks"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanLocks) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid lock scope or query.")
		return
	}
	q := factory.HumanLockCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Subject: "issue", Number: number}
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
		limit := int64(64 << 10)
		if action == "submit" {
			limit = HumanLockRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Lock request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid lock JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "lock" && field != "reason" {
					writeError(w, 400, "invalid_request", "Invalid lock JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid lock command identity.")
				return
			}
			var lock *bool
			if json.Unmarshal(input["lock"], &lock) != nil || lock == nil {
				writeError(w, 400, "invalid_request", "Lock must be a boolean.")
				return
			}
			q.Lock = *lock
			if raw, present := input["reason"]; present {
				var reason *string
				if json.Unmarshal(raw, &reason) != nil || reason == nil {
					writeError(w, 400, "invalid_request", "Lock reason must be a string when supplied.")
					return
				}
				q.Reason = *reason
			}
		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid lock command, state or reason.")
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
		humanLockHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanLockHTTPError(w, factoryservice.ErrHumanLockUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained reason stay server-side. Discussion reads retain their normal
	// scope after a committed effect.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanLockFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanLockHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanLockQuery):
		writeError(w, 400, "invalid_request", "Invalid lock request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "lock_command_not_found", "Lock command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanLockAdmission):
		writeError(w, 403, "lock_admission_required", "Current human lock admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanLockConflict), errors.Is(err, factory.ErrHumanLockObservation):
		writeError(w, 409, "lock_command_conflict", "Lock command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanLockNamespace):
		writeError(w, 409, "lock_context_changed", "The captured lock context is no longer current.")
	default:
		writeError(w, 503, "lock_delivery_unavailable", "Lock delivery could not be reconciled. Keep the original command for lookup.")
	}
}
