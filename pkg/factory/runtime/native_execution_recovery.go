package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/store"
)

// This projection is private lifecycle evidence, never product or launch
// admission. Go defines the canonical address and retained placement; Rust
// compares them with its native receipt/custody before any recovery effects.
type nativeRecoveryRun struct {
	ID             string                  `json:"id"`
	Workspace      string                  `json:"workspace"`
	Role           string                  `json:"role"`
	SourceCommit   string                  `json:"source_commit"`
	Deadline       time.Time               `json:"deadline"`
	ContextPending bool                    `json:"context_pending"`
	Reconciled     bool                    `json:"reconciled"`
	Outcome        factory.Outcome         `json:"outcome"`
	Native         *execution.NativeRecord `json:"native"`
	Launch         *execution.NativeLaunch `json:"launch"`
}

func nativeExecutionRecovery(config NativeConfig, instance string, db *store.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
			nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
			return
		}
		brokerRun := strings.TrimPrefix(r.URL.Path, "/api/native/execution/recovery/")
		if r.Method != http.MethodGet || !collaboration.ValidID(brokerRun) || r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_recovery_read"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		run, err := db.NativeFactoryRun(ctx, config.Profile, brokerRun)
		if err != nil {
			status, code := 503, "native_recovery_unavailable"
			if errors.Is(err, store.ErrNotFound) {
				status, code = 404, "native_recovery_missing"
			} else if errors.Is(err, store.ErrAdmissionChanged) {
				status, code = 409, "native_recovery_conflict"
			}
			nativeResponse(w, status, map[string]string{"error": code})
			return
		}
		var launch *execution.NativeLaunch
		if run.Admission != nil {
			launch = run.Admission.Native
		}
		if ctx.Err() != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(struct {
			Kind     string                  `json:"kind"`
			Version  int                     `json:"version"`
			Profile  collaboration.ProfileID `json:"profile"`
			Instance string                  `json:"instance"`
			Run      nativeRecoveryRun       `json:"run"`
		}{"native_run_recovery", 1, config.Profile, instance, nativeRecoveryRun{
			ID: run.ID, Workspace: run.ProjectID, Role: run.Role, SourceCommit: run.InputSHA,
			Deadline: run.Deadline, ContextPending: run.ContextPending, Reconciled: run.Reconciled,
			Outcome: run.Outcome, Native: run.Native, Launch: launch,
		}})
	})
}
