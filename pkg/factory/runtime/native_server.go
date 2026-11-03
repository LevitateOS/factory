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
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

type nativePeerKey struct{}

// NativeConnector composes private provider ports after the owned Store opens.
// The profile comes from Rust's validated bootstrap, never a browser payload.
// Construction must perform no provider I/O or select an active product actor.
type NativeConnector func(collaboration.Profile, *store.Store) http.Handler

// RunNative owns the Go network/store lifetime inside an existing Rust-owned
// native database profile. Rust owns provisioning and child supervision. This
// process recovers proved interrupted assessments before readiness, without
// adopting a ledger, reconciling unobserved runs or starting background work.
func RunNative(ctx context.Context, path string, connector NativeConnector) (runErr error) {
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
	application := NewNativeApplication(factoryservice.ApplicationPorts{Store: db}, nil)
	defer func() { runErr = errors.Join(runErr, application.Close()) }()
	if err := application.Recover(startup); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("native Factory retained recovery failed")
	}
	cancelStartup()
	var nativeProvider http.Handler
	if connector != nil {
		nativeProvider = connector(collaboration.Profile{ID: config.Profile, Name: "This computer"}, db)
	}
	var closing atomic.Bool
	server := &http.Server{Handler: nativeHandler(config, application, nativeProvider, &closing),
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

func nativeHandler(config NativeConfig, application *NativeApplication, connector http.Handler, closing *atomic.Bool) http.Handler {
	key := []byte(hex.EncodeToString(config.IPCKey()))
	instance := collaboration.NewID()
	mux := http.NewServeMux()
	db := application.application.Store
	mux.Handle("/api/factory/", application)
	mux.Handle("/api/repositories/", application)
	if connector != nil {
		nativeConnector := application.application.Coordinator.AdmitHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if values := r.Header.Values("X-Soda-Native-Instance"); len(values) != 1 || values[0] != instance {
				nativeResponse(w, 401, map[string]string{"error": "retired_native_instance"})
				return
			}
			connector.ServeHTTP(w, r)
		}))
		for _, path := range []string{"/api/native/github/read", "/api/native/github/device/start", "/api/native/github/device/wait", "/api/native/github/refresh"} {
			mux.Handle(path, nativeConnector)
		}
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
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		mux.ServeHTTP(w, r)
	})
}

func nativeResponse(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
