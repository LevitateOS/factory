package runtime

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryhttp "github.com/levitateos/factory/pkg/factory/httpapi"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

type nativePeerKey struct{}

// NativeConnector composes credential-free observation ports after Store opens.
// The profile comes from Rust's validated bootstrap, never a browser payload.
// Construction must perform no provider I/O or select an active product actor.
type NativeConnector func(collaboration.Profile, *store.Store) http.Handler

// RunNative owns the Go network/store lifetime inside an existing Rust-owned
// native database profile. Rust owns provisioning and child supervision. This
// process recovers proved interrupted assessments before readiness, without
// adopting a ledger, reconciling unobserved runs or starting background work.
func RunNative(ctx context.Context, path string, connector NativeConnector) (runErr error) {
	return runNative(ctx, path, connector, nil)
}

// RunNativeWithBinding composes the existing per-request trusted product port.
// It supplies no actor from readiness/IPC: the binding must freshly establish
// its own account authority. Product binding remains inside coordinator admission.
func RunNativeWithBinding(ctx context.Context, path string, connector NativeConnector, binding NativeProductBinding) error {
	return runNative(ctx, path, connector, binding)
}

func runNative(ctx context.Context, path string, connector NativeConnector, binding NativeProductBinding) (runErr error) {
	config, err := LoadNative(path)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	listener, release, err := nativeListener(config)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, release()) }()
	startup, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()
	db, err := store.OpenContext(startup, config.DatabaseDSN())
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("native Factory database startup failed")
	}
	defer func() { runErr = errors.Join(runErr, db.Close()) }()
	application := NewNativeApplication(factoryservice.ApplicationPorts{Store: db}, binding)
	defer func() { runErr = errors.Join(runErr, application.Close()) }()
	if err := application.Recover(startup); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("native Factory retained recovery failed")
	}
	cancelStartup()
	instance := collaboration.NewID()
	application.application.Coordinator.MCPSecret = NativeMCPSecretDeriver(config, instance)
	var nativeProvider http.Handler
	if connector != nil {
		nativeProvider = connector(collaboration.Profile{ID: config.Profile, Name: "This computer"}, db)
	}
	var closing atomic.Bool
	server := &http.Server{Handler: nativeHandler(config, instance, application, nativeProvider, &closing),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384,
		BaseContext: func(net.Listener) context.Context { return ctx }, ConnContext: nativePeerContext,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	var serveErr error
	var serveStopped bool
	select {
	case serveErr = <-served:
		serveStopped = true
	case <-ctx.Done():
	}
	closing.Store(true)
	application.StopAdmission()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = server.Shutdown(shutdown)
	cancel()
	if err != nil {
		_ = server.Close()
	}
	if !serveStopped {
		serveErr = <-served
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	} else if serveErr != nil {
		serveErr = errors.New("native Factory listener stopped")
	}
	return errors.Join(serveErr, err)
}

func nativeHandler(config NativeConfig, instance string, application *NativeApplication, connector http.Handler, closing *atomic.Bool) http.Handler {
	key := []byte(hex.EncodeToString(config.IPCKey()))
	mux := http.NewServeMux()
	db := application.application.Store
	product := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
			nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
			return
		}
		admitted, ok := nativeOperationContext(r, config, instance)
		if !ok {
			nativeResponse(w, 401, map[string]string{"error": "invalid_native_operation"})
			return
		}
		r = admitted
		application.ServeHTTP(w, r)
	})
	mux.Handle("/api/factory/", product)
	mux.Handle("/api/repositories/", product)
	// Private retained evidence is readable before execution intake is admitted.
	// The outer handler still requires the native peer/key and rejects browsers.
	mux.Handle("/api/native/execution/recovery/", nativeExecutionRecovery(config, instance, db))
	mux.Handle("/api/native/execution/recovery/observations", nativeExecutionObservation(config, instance, db))
	mux.Handle("/api/native/execution/recovery/retired", nativeExecutionRetirement(config, instance, db))
	mux.Handle("/api/native/execution/recovery/pending", nativeExecutionPending(config, instance, db))
	if connector != nil {
		nativeConnector := application.application.Coordinator.AdmitHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
				nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
				return
			}
			connector.ServeHTTP(w, r)
		}))
		mux.Handle("/api/native/collaboration/observe", nativeConnector)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			nativeResponse(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			nativeResponse(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if closing.Load() || db.Ping(ctx) != nil {
			nativeResponse(w, 503, map[string]string{"error": "store_unavailable"})
			return
		}
		nativeResponse(w, 200, map[string]string{"status": "ready", "profile": string(config.Profile), "instance": instance})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// The Rust bridge owns this channel. Webviews never receive its key or
		// connect directly; a browser origin cannot establish native authority.
		for name := range r.Header {
			if strings.EqualFold(name, "Origin") || strings.HasPrefix(strings.ToLower(name), "sec-fetch-") {
				nativeResponse(w, 403, map[string]string{"error": "private_channel_required"})
				return
			}
		}
		auth := r.Header.Values("Authorization")
		var bearer string
		if len(auth) == 1 {
			if value, ok := strings.CutPrefix(auth[0], "Bearer "); ok {
				bearer = value
			}
		}
		peer, _ := r.Context().Value(nativePeerKey{}).(bool)
		if !peer || len(auth) != 1 || len(bearer) != len(key) || subtle.ConstantTimeCompare([]byte(bearer), key) != 1 {
			nativeResponse(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		if closing.Load() {
			nativeResponse(w, 503, map[string]string{"error": "shutting_down"})
			return
		}
		if r.URL.RawPath != "" {
			nativeResponse(w, 400, map[string]string{"error": "invalid_request"})
			return
		}
		limit := int64(65536)
		if r.URL.Path == "/api/native/collaboration/observe" {
			limit = 2 << 20
		} else if factoryhttp.IsHumanCommentSubmission(r) {
			limit = factoryhttp.HumanCommentRequestLimit
		} else if factoryhttp.IsHumanIssueCreateSubmission(r) {
			limit = factoryhttp.HumanIssueCreateRequestLimit
		} else if factoryhttp.IsHumanPullCreateSubmission(r) {
			limit = factoryhttp.HumanPullCreateRequestLimit
		} else if factoryhttp.IsHumanReviewSubmission(r) {
			limit = factoryhttp.HumanReviewRequestLimit
		} else if factoryhttp.IsHumanInlineSubmission(r) {
			limit = factoryhttp.HumanInlineRequestLimit
		} else if factoryhttp.IsHumanEditSubmission(r) {
			limit = factoryhttp.HumanEditRequestLimit
		} else if factoryhttp.IsHumanLockSubmission(r) {
			limit = factoryhttp.HumanLockRequestLimit
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		mux.ServeHTTP(w, r)
	})
}

func nativeResponse(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
