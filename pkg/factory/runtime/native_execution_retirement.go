package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/store"
	"github.com/levitateos/factory/pkg/strictjson"
)

func nativeExecutionRetirement(config NativeConfig, instance string, db *store.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
			nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
			return
		}
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_retirement"})
			return
		}
		var input struct {
			Intent         string `json:"intent"`
			Invocation     string `json:"invocation"`
			Acquisition    string `json:"acquisition_id"`
			ContextPending bool   `json:"context_pending,omitempty"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 65536), &input) != nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_retirement"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var err error
		if input.ContextPending {
			if input.Invocation != "" || input.Acquisition != "" {
				nativeResponse(w, 400, map[string]string{"error": "invalid_native_retirement"})
				return
			}
			err = db.RecordNativeContextNoStart(ctx, config.Profile, input.Intent)
		} else {
			err = db.RecordNativeRootRetirement(ctx, config.Profile, input.Intent, input.Invocation, input.Acquisition)
		}
		if err != nil {
			status, code := 503, "native_retirement_unavailable"
			if errors.Is(err, store.ErrNotFound) {
				status, code = 404, "native_retirement_missing"
			} else if errors.Is(err, store.ErrAdmissionChanged) || errors.Is(err, execution.ErrObservation) || errors.Is(err, execution.ErrRunChanged) {
				status, code = 409, "native_retirement_conflict"
			}
			nativeResponse(w, status, map[string]string{"error": code})
			return
		}
		bound, _ := execution.ParseNativeIntent(input.Intent)
		nativeResponse(w, 200, map[string]string{"status": "native_root_retirement_recorded", "profile": string(config.Profile), "instance": instance, "run": bound.Run})
	})
}

func nativeExecutionPending(config NativeConfig, instance string, db *store.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
			nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
			return
		}
		query := r.URL.Query()
		cursor := query.Get("cursor")
		after, err := strconv.ParseInt(cursor, 10, 64)
		if r.Method != http.MethodGet || r.URL.ForceQuery || len(query) != 1 || len(query["cursor"]) != 1 || err != nil || after < 0 || strconv.FormatInt(after, 10) != cursor || r.URL.RawQuery != "cursor="+cursor || r.ContentLength != 0 {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_recovery_cursor"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, next, err := db.NativeRecoveryCandidates(ctx, config.Profile, after)
		if err != nil {
			nativeResponse(w, 503, map[string]string{"error": "native_recovery_inventory_unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(struct {
			Kind     string                          `json:"kind"`
			Version  int                             `json:"version"`
			Profile  string                          `json:"profile"`
			Instance string                          `json:"instance"`
			Items    []store.NativeRecoveryCandidate `json:"items"`
			Next     string                          `json:"next"`
		}{"native_recovery_pending", 1, string(config.Profile), instance, items, next})
	})
}
