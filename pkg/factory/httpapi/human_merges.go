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

// Merge submission admits JSON escaping of 64 KiB original message through
// every host; retained command cancellation keeps the ordinary small envelope.
const HumanMergeRequestLimit = 512 << 10

func IsHumanMergeSubmission(r *http.Request) bool {
	p := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && len(p) == 9 && p[1] == "api" && p[2] == "factory" && p[3] == "collaboration" && p[4] == "repositories" && p[6] == "pulls" && p[8] == "merges"
}

type HumanMerges struct {
	Service factoryservice.HumanMerges
	Host    collaboration.HostID
	Actor   collaboration.ActorID
	Current func(http.ResponseWriter, *http.Request) bool
}

func (s HumanMerges) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, kind := range []string{"pulls"} {
		base := "/api/factory/collaboration/repositories/{repository}/" + kind + "/{number}/merges"
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "submit") })
		mux.HandleFunc("GET "+base+"/commands/{command}", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "lookup") })
		mux.HandleFunc("POST "+base+"/commands/{command}/cancel", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, kind, "cancel") })
	}
	return mux
}

func (s HumanMerges) handle(w http.ResponseWriter, r *http.Request, kind, action string) {
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
		writeError(w, 400, "invalid_request", "Invalid merge scope or query.")
		return
	}
	q := factory.HumanMergeCommand{ID: r.PathValue("command"), Host: s.Host, Actor: s.Actor, Repository: collaboration.RepositoryID(repo), Number: number}
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
			limit = HumanMergeRequestLimit
		}
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, limit), target); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, 413, "body_too_large", "Merge request exceeds the envelope limit.")
			} else {
				writeError(w, 400, "invalid_request", "Invalid merge JSON fields or values.")
			}
			return
		}
		if action == "submit" {
			for field := range input {
				if field != "command_id" && field != "method" && field != "message" && field != "commit_oid" && field != "expected_head" {
					writeError(w, 400, "invalid_request", "Invalid merge JSON fields or values.")
					return
				}
			}
			var method *string
			if json.Unmarshal(input["command_id"], &q.ID) != nil || json.Unmarshal(input["method"], &method) != nil || method == nil {
				writeError(w, 400, "invalid_request", "Merge command identity and method are required.")
				return
			}
			q.Method = *method
			if raw, present := input["expected_head"]; present {
				var head *collaboration.CommitID
				if json.Unmarshal(raw, &head) != nil || head == nil || !collaboration.ValidCommitID(*head) {
					writeError(w, 400, "invalid_request", "Expected head must be a commit identity.")
					return
				}
				q.ExpectedHead = *head
			}
			selected, forbidden := "message", "commit_oid"
			if q.Method == "manual" {
				selected, forbidden = "commit_oid", "message"
			}
			var value *string
			if _, mixed := input[forbidden]; mixed {
				writeError(w, 400, "invalid_request", "Select an ordinary merge message or a manual commit.")
				return
			}
			if json.Unmarshal(input[selected], &value) != nil || value == nil {
				writeError(w, 400, "invalid_request", "The selected merge message or commit is required.")
				return
			}
			if q.Method == "manual" {
				q.CommitOID = *value
			} else {
				q.Message = *value
			}
		}
	}
	if q.ValidateScope() != nil || action == "submit" && q.Validate() != nil {
		writeError(w, 400, "invalid_request", "Invalid merge command, event or Markdown.")
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
		humanMergeHTTPError(w, err)
		return
	}
	if o.Validate() != nil || o.Host != s.Host {
		humanMergeHTTPError(w, factoryservice.ErrHumanMergeUnavailable)
		return
	}
	// Provider receipts, native IDs, intent digests, installation/admission data
	// and retained preparation stay server-side. Merge reads retain their normal
	// scope after a committed effect.
	response := humanCommandResponse{CommandID: q.ID, Observation: o.State, Finished: factory.HumanMergeFinished(o)}
	if o.Record != nil {
		response.Effect, response.Cancellation, response.Completion = o.Record.Effect, o.Record.Cancellation, o.Record.Completion
	}
	status := http.StatusOK
	if action != "lookup" && !response.Finished {
		status = http.StatusAccepted
	}
	writeJSON(w, status, response)
}

func humanMergeHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factoryservice.ErrHumanMergeQuery):
		writeError(w, 400, "invalid_request", "Invalid merge request.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "merge_command_not_found", "Merge command is not visible.")
	case errors.Is(err, factoryservice.ErrHumanMergeAdmission):
		writeError(w, 403, "merge_admission_required", "Current human merge admission is required.")
	case errors.Is(err, factoryservice.ErrHumanMergeForbidden):
		writeError(w, 403, "merge_forbidden", "Current GitHub rules do not allow this merge.")
	case errors.Is(err, store.ErrCommandConflict), errors.Is(err, factoryservice.ErrHumanMergeConflict), errors.Is(err, factory.ErrHumanMergeObservation):
		writeError(w, 409, "merge_command_conflict", "Merge command conflicts with its original intent.")
	case errors.Is(err, factoryservice.ErrHumanMergeNamespace):
		writeError(w, 409, "merge_context_changed", "The captured merge context is no longer current.")
	default:
		writeError(w, 503, "merge_delivery_unavailable", "Merge delivery could not be reconciled. Keep the original command for lookup.")
	}
}
