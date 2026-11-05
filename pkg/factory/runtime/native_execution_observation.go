package runtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/store"
	"github.com/levitateos/factory/pkg/strictjson"
)

func nativeExecutionObservation(config NativeConfig, instance string, db *store.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
			nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
			return
		}
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_observation"})
			return
		}
		var input struct {
			Intent  string                              `json:"intent"`
			Receipt execution.NativeReceiptObservation  `json:"receipt"`
			Custody *execution.NativeCustodyObservation `json:"custody"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 65536), &input) != nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_observation"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		err := db.ObserveNativeRecovery(ctx, config.Profile, input.Intent, input.Receipt, input.Custody, os.Geteuid())
		if err != nil {
			status, code := 503, "native_observation_unavailable"
			if errors.Is(err, store.ErrNotFound) {
				status, code = 404, "native_observation_missing"
			} else if errors.Is(err, store.ErrAdmissionChanged) || errors.Is(err, execution.ErrObservation) || errors.Is(err, execution.ErrRunChanged) || errors.Is(err, execution.ErrNativeIntent) {
				status, code = 409, "native_observation_conflict"
			}
			nativeResponse(w, status, map[string]string{"error": code})
			return
		}
		bound, _ := execution.ParseNativeIntent(input.Intent)
		nativeResponse(w, 200, map[string]string{"status": "native_observation_recorded", "instance": instance, "profile": string(config.Profile), "run": bound.Run})
	})
}
