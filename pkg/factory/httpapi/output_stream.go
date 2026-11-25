package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
)

// OutputStream serves one read-only attachment as a long-lived response of
// newline-delimited JSON frames, for hosts without a WebSocket upgrade. Frames
// match Output's. The host's request binding is the response's authority: it is
// observed once, and Current/attachment checks during the response must not go
// back to the provider. "renew" closes a response whose lifetime ended; the
// host may reattach at its cursor with a fresh binding.
type OutputStream struct {
	Service  factoryservice.RunOutput
	Current  func(context.Context) bool
	Lifetime time.Duration
}

func (s Runs) output(w http.ResponseWriter, r *http.Request) {
	stream := s.OutputStream
	if stream == nil || stream.Current == nil || stream.Lifetime <= 0 {
		writeError(w, 503, "output_unavailable", "Live run output is unavailable.")
		return
	}
	query := r.URL.Query()
	cursor, err := strconv.ParseInt(query.Get("cursor"), 10, 64)
	if len(query) != 1 || len(query["cursor"]) != 1 || err != nil || cursor < 0 || cursor > project.MaxFactoryOutputOffset ||
		strconv.FormatInt(cursor, 10) != query.Get("cursor") {
		writeError(w, 400, "invalid_request", "Only one canonical cursor is accepted.")
		return
	}
	attachment, err := stream.Service.Open(r.Context(), s.Actor, r.PathValue("runID"))
	if err != nil {
		switch {
		case errors.Is(err, factory.ErrNotFound), errors.Is(err, factoryservice.ErrRunOutputMissing):
			writeError(w, 404, "not_found", "Run not found.")
		case errors.Is(err, factoryservice.ErrRunOutputAccess):
			writeError(w, 403, "authority_unavailable", "Current run output authority is required.")
		default:
			writeError(w, 503, "output_unavailable", "Live run output is unavailable.")
		}
		return
	}
	if !s.current(w, r) || !stream.Current(r.Context()) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), stream.Lifetime+10*time.Second)
	defer cancel()
	control := http.NewResponseController(w)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	emit := func(ctx context.Context, frame any) error {
		body, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		if err = control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		if _, err = w.Write(append(body, '\n')); err != nil {
			return err
		}
		return control.Flush()
	}
	beat := func(ctx context.Context) bool {
		check, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		if !stream.Current(check) || attachment.Check(check) != nil {
			_ = emit(ctx, factoryservice.RunOutputClosed{Type: "closed", Reason: "authority_lost"})
			return false
		}
		return true
	}
	end := time.NewTimer(stream.Lifetime)
	defer end.Stop()
	pumpOutput(ctx, attachment, stream.Current, emit, beat, cursor, end.C)
}
