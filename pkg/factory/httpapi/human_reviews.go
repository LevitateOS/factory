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

// Review submission admits JSON escaping of 64 KiB original Markdown through
// every host; retained command cancellation keeps the ordinary small envelope.
const HumanReviewRequestLimit = 512 << 10

func IsHumanReviewSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	if r.Method != http.MethodPost || len(p) != 9 || p[1] != "api" || p[2] != "factory" || p[3] != "collaboration" || p[4] != "repositories" || p[6] != "pulls" || p[8] != "reviews" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		return false
	}
	_, repo := positiveID(p[5])
	_, number := positiveID(p[7])
	return repo && number
}

type HumanReviews struct {
	Service factoryservice.HumanReviews
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanReviews) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/reviews"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanReviews) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid review scope or query.")
		return
	}
	q := factory.HumanReviewCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Number: number}
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
			limit = HumanReviewRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Review request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid review JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "event" && field != "body" {
					writeError(w, 400, "invalid_request", "Invalid review JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid review command identity.")
				return
			}
			var event, body *string
			if json.Unmarshal(input["event"], &event) != nil || event == nil || json.Unmarshal(input["body"], &body) != nil || body == nil {
				writeError(w, 400, "invalid_request", "Review event and Markdown body must be strings.")
				return
			}
			q.Event, q.Body = factory.HumanReviewEvent(*event), *body

		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid review command, event or Markdown.")
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
		humanReviewHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanReviewHTTPError(w, factoryservice.ErrHumanReviewUnavailable)
		return
	}
	// Only a currently readable canonical destination is projected. Preparation,
	// original writing and causal receipts stay private; a failed read does not
	// erase the separately recorded effect.
	response := humanReviewResponse{humanCommandResponse: humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanReviewFinished(o)}}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
		if o.Record.Effect == collaboration.EffectCommitted {
			scope := q
			scope.Event, scope.Body = "", ""
			if ref, readErr := s.Service.CreatedReview(ctx, scope, o); readErr == nil {
				response.Review = &ref
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

type humanReviewResponse struct {
	humanCommandResponse
	Review *collaboration.ReviewRef `json:"review"`
}

func humanReviewHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanReviewQuery):
		writeError(w, 400, "invalid_request", "Invalid review request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "review_command_not_found", "Review command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanReviewAdmission):
		writeError(w, 403, "review_admission_required", "Current human review admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanReviewConflict), errors.Is(err, factory.ErrHumanReviewObservation):
		writeError(w, 409, "review_command_conflict", "Review command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanReviewNamespace):
		writeError(w, 409, "review_context_changed", "The captured review context is no longer current.")
	default:
		writeError(w, 503, "review_delivery_unavailable", "Review delivery could not be reconciled. Keep the original command for lookup.")
	}
}
