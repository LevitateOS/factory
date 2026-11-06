// Package httpapi owns Factory HTTP transport boundaries. Hosts supply current
// admission and protocol construction without HTTP session impersonation.
package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// WorkerRoute is the fixed Factory-only MCP route. Listener/socket ownership
// belongs to the host; a worker cannot select it in its tool arguments.
const WorkerRoute = "/mcp"

type CapabilitySource = factoryservice.WorkerCapabilitySource

// Workers authenticates each request before current runtime/work admission.
// Admit must supply the host's current admission; active recorded state alone
// is not an implementation of that port. Protocol receives only its admitted
// capability. Model SDK, credential staging and listener policy stay outside.
type Workers struct {
	Capabilities CapabilitySource
	Admit        func(context.Context, factory.MCPCapability) (factory.MCPCapability, error)
	Protocol     func(context.Context, factory.MCPCapability) (http.Handler, error)
	Now          func() time.Time
}

func (s Workers) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Handler is the separate appliance key path. Native desktop hosts use
// HandlerWithSecretVerifier so expected secret bytes stay with the Rust owner.
func (s Workers) Handler(key []byte) http.Handler {
	key = append([]byte(nil), key...)
	var derive factory.MCPSecretDeriver
	if len(key) >= 32 {
		derive = func(_ context.Context, capability factory.MCPCapability) (string, error) {
			return factory.DeriveMCPCapabilitySecret(key, capability)
		}
	}
	return s.HandlerWithSecretDeriver(derive)
}

// HandlerWithSecretDeriver retains the older host adapter contract while
// keeping the native verifier path's current admission sequence identical.
func (s Workers) HandlerWithSecretDeriver(derive factory.MCPSecretDeriver) http.Handler {
	var verify factory.MCPSecretVerifier
	if derive != nil {
		verify = func(ctx context.Context, capability factory.MCPCapability, provided string) (bool, error) {
			secret, err := derive(ctx, capability)
			return err == nil && factory.ValidMCPSecret(secret) && subtle.ConstantTimeCompare([]byte(secret), []byte(provided)) == 1, err
		}
	}
	return s.HandlerWithSecretVerifier(verify)
}

// HandlerWithSecretVerifier authenticates through the native Rust owner, then
// independently admits the current Go capability and run before serving tools.
func (s Workers) HandlerWithSecretVerifier(verify factory.MCPSecretVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WorkerRoute {
			http.NotFound(w, r)
			return
		}
		if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
			http.Error(w, "invalid worker target", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, present := r.Header["Origin"]; present {
			http.Error(w, "origin refused", http.StatusForbidden)
			return
		}
		if s.Capabilities == nil || s.Admit == nil || s.Protocol == nil {
			http.Error(w, "factory MCP unavailable", http.StatusServiceUnavailable)
			return
		}
		ids := r.Header.Values("X-Soda-Factory-Capability")
		auth := r.Header.Values("Authorization")
		if len(ids) != 1 || len(auth) != 1 {
			http.Error(w, "worker authentication required", http.StatusUnauthorized)
			return
		}
		id := ids[0]
		provided, bearer := strings.CutPrefix(auth[0], "Bearer ")
		if verify == nil || !factory.ValidID(id) || !bearer || !factory.ValidMCPSecret(provided) {
			http.Error(w, "worker authentication required", http.StatusUnauthorized)
			return
		}
		capability, err := s.Capabilities.GetMCPCapability(r.Context(), id)
		if err != nil || capability.ID != id || capability.Validate() != nil {
			http.Error(w, "worker authentication refused", http.StatusUnauthorized)
			return
		}
		valid, keyErr := verify(r.Context(), capability, provided)
		if keyErr != nil || !valid || r.Context().Err() != nil {
			http.Error(w, "worker authentication refused", http.StatusUnauthorized)
			return
		}
		admitted, err := s.Admit(r.Context(), capability)
		if err != nil || r.Context().Err() != nil || admitted.Validate() != nil || admitted.ScopeDigest() != capability.ScopeDigest() || admitted.State != factory.MCPCapabilityActive || admitted.Expired(s.now()) {
			http.Error(w, "worker context is not active", http.StatusForbidden)
			return
		}
		protocol, err := s.Protocol(r.Context(), admitted)
		if err != nil || protocol == nil {
			http.Error(w, "factory MCP unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.Context().Err() != nil {
			http.Error(w, "worker context is not active", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, factory.MCPMaxSubmissionPayload+8192)
		protocol.ServeHTTP(w, r)
	})
}

// Unavailable returns a generic transport error without exposing configuration,
// credential-file paths or details from an underlying admission failure.
func Unavailable() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "factory MCP unavailable", http.StatusServiceUnavailable)
	})
}
