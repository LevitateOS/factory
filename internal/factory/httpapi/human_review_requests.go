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

// Reviewer commands select canonical users or teams without conversation prose.
// Every host retains its ordinary 64 KiB command envelope.
const HumanReviewerRequestLimit = 64 << 10

type HumanReviewRequests struct {
	Service factoryservice.HumanReviewRequests
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanReviewRequests) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/review-requests"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanReviewRequests) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid reviewer scope or query.")
		return
	}
	q := factory.HumanReviewRequestCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Number: number}
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
			limit = HumanReviewerRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Reviewer request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid reviewer JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "reviewer_kind" && field != "reviewer_id" && field != "requested" {
					writeError(w, 400, "invalid_request", "Invalid reviewer JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid reviewer command identity.")
				return
			}
			var kind, reviewer *string
			var requested *bool
			if json.Unmarshal(input["reviewer_kind"], &kind) != nil || kind == nil || json.Unmarshal(input["reviewer_id"], &reviewer) != nil || reviewer == nil || json.Unmarshal(input["requested"], &requested) != nil || requested == nil {
				writeError(w, 400, "invalid_request", "Reviewer kind, canonical reviewer identity and desired state are required.")
				return
			}
			target, valid := positiveID(*reviewer)
			if !valid || (*kind != "user" && *kind != "team") {
				writeError(w, 400, "invalid_request", "Select a canonical user or team reviewer.")
				return
			}
			q.Requested = *requested
			if *kind == "user" {
				q.Reviewer = collaboration.ActorID(target)
			} else {
				q.Team = collaboration.TeamID(target)
			}

		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid reviewer command or selection.")
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
		humanReviewRequestHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanReviewRequestHTTPError(w, factoryservice.ErrHumanReviewRequestUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained preparation stay server-side.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanReviewRequestFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanReviewRequestHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanReviewRequestQuery):
		writeError(w, 400, "invalid_request", "Invalid reviewer request.")
	case errors.Is(err, factoryservice.ErrHumanReviewRequestTooLarge):
		writeError(w, 413, "review_request_too_large", "The complete reviewer preparation exceeds the supported size.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "review_request_command_not_found", "Reviewer command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanReviewRequestAdmission):
		writeError(w, 403, "review_request_admission_required", "Current human reviewer admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanReviewRequestConflict), errors.Is(err, factory.ErrHumanReviewRequestObservation):
		writeError(w, 409, "review_request_command_conflict", "Reviewer command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanReviewRequestNamespace):
		writeError(w, 409, "review_request_context_changed", "The captured reviewer context is no longer current.")
	default:
		writeError(w, 503, "review_request_delivery_unavailable", "Reviewer delivery could not be reconciled. Keep the original command for lookup.")
	}
}
