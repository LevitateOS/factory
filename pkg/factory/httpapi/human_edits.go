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

// Covers JSON escaping of 64 KiB original Markdown; other mutation limits stay
// unchanged. Host body wrappers must admit this envelope on these two routes.
const HumanEditRequestLimit = 512 << 10

func IsHumanEditSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 9 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && (p[6] == "issues" || p[6] == "pulls") && p[8] == "edits"
}

type HumanEdits struct {
	Service factoryservice.HumanEdits
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanEdits) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"issues", "pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/edits"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanEdits) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid edit scope or query.")
		return
	}
	q := factory.HumanEditCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Subject: "issue", Number: number}
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
			limit = HumanEditRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Edit request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid edit JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "title" && field != "body" && field != "expected" {
					writeError(w, 400, "invalid_request", "Invalid edit JSON fields or values.")
					return
				}
			}
			if err := json.Unmarshal(input["command_id"], &q.ID); err != nil {
				writeError(w, 400, "invalid_request", "Invalid edit command identity.")
				return
			}
			decodeField := func(raw json.RawMessage) (*string, bool) {
				if len(raw) == 0 {
					return nil, true
				}
				var value *string
				err := json.Unmarshal(raw, &value)
				return value, err == nil && value != nil
			}
			var titleValid, bodyValid bool
			q.Title, titleValid = decodeField(input["title"])
			q.Body, bodyValid = decodeField(input["body"])
			if !titleValid || !bodyValid {
				writeError(w, 400, "invalid_request", "Edit title and body must be strings when supplied.")
				return
			}
			if raw, present := input["expected"]; present {
				var fields map[string]json.RawMessage
				expected := new(factory.HumanEditBase)
				valid := strictjson.Decode(bytes.NewReader(raw), &fields) == nil && len(fields) == 3
				for name, target := range map[string]*string{"external_id": &expected.ExternalID, "title": &expected.Title, "body_digest": &expected.BodyDigest} {
					var value *string
					if json.Unmarshal(fields[name], &value) != nil || value == nil {
						valid = false
					} else {
						*target = *value
					}
				}
				if !valid || expected.Validate() != nil {
					writeError(w, 400, "invalid_request", "Original edit content must contain an external ID, title and SHA-256 body digest.")
					return
				}
				q.Expected = expected
			}
		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid edit command, title or Markdown.")
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
		humanEditHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanEditHTTPError(w, factoryservice.ErrHumanEditUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained Markdown stay server-side. Refresh the shared issue/PR read
	// after a committed effect to obtain the updated content with normal read scope.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanEditFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanEditHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanEditQuery):
		writeError(w, 400, "invalid_request", "Invalid edit request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "edit_command_not_found", "Edit command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanEditAdmission):
		writeError(w, 403, "edit_admission_required", "Current human edit admission is required.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanEditConflict), errors.Is(err, factory.ErrHumanEditObservation):
		writeError(w, 409, "edit_command_conflict", "Edit command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanEditNamespace):
		writeError(w, 409, "edit_context_changed", "The captured edit context is no longer current.")
	default:
		writeError(w, 503, "edit_delivery_unavailable", "Edit delivery could not be reconciled. Keep the original command for lookup.")
	}
}
