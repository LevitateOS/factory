package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/strictjson"
)

// OperatorPath serves the private operator commands on the backend's Unix
// socket. It is never reachable through the public HTTP mux.
const (
	OperatorPath      = "/operator/factory"
	operatorBodyLimit = 4096
)

type principalKey struct{}

// WithOperatorPrincipal carries the socket peer's OS identity ("os-uid:N")
// from the peer-credential listener to the operator handler. Clients cannot
// supply it; a missing principal is a server wiring failure.
func WithOperatorPrincipal(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func operatorPrincipal(r *http.Request) (string, bool) {
	principal, ok := r.Context().Value(principalKey{}).(string)
	return principal, ok && principal != ""
}

// OperatorRequest names an action on recorded runs.
type OperatorRequest struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
}

// OperatorHandler admits private operator commands into the coordinator.
// Unknown runs report 404.
func (c *Coordinator) OperatorHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(OperatorPath, c.serveOperator)
	return mux
}

func (c *Coordinator) serveOperator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	_, ok := operatorPrincipal(r)
	if !ok {
		http.Error(w, "operator principal unavailable", http.StatusInternalServerError)
		return
	}
	var in OperatorRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, operatorBodyLimit+1))
	if err != nil || len(body) > operatorBodyLimit {
		http.Error(w, "invalid operator command", http.StatusBadRequest)
		return
	}
	if err := strictjson.Decode(bytes.NewReader(body), &in); err != nil {
		http.Error(w, "invalid operator command", http.StatusBadRequest)
		return
	}
	switch in.Type {
	case factory.CommandStatus:
		c.serveStatus(w, r, in)
	case factory.CommandStop:
		c.serveStop(w, r, in)
	case factory.CommandReconcile:
		c.serveReconcile(w, r, in)
	default:
		http.Error(w, "unknown operator command", http.StatusBadRequest)
	}
}

func (c *Coordinator) serveStatus(w http.ResponseWriter, r *http.Request, in OperatorRequest) {
	if in.Target != "" && !factory.ValidID(in.Target) {
		http.Error(w, "invalid run identity", http.StatusBadRequest)
		return
	}
	runs, err := c.Status(r.Context(), in.Target)
	if err != nil {
		if errors.Is(err, factory.ErrNotFound) {
			http.Error(w, "factory run not found", http.StatusNotFound)
			return
		}
		http.Error(w, "operator status unavailable", http.StatusInternalServerError)
		return
	}
	writeOperator(w, map[string]any{"runs": runs})
}

func (c *Coordinator) serveStop(w http.ResponseWriter, r *http.Request, in OperatorRequest) {
	if !factory.ValidID(in.Target) {
		http.Error(w, "stop requires its recorded run", http.StatusBadRequest)
		return
	}
	result, err := c.Stop(r.Context(), in.Target)
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	writeOperator(w, result)
}

func (c *Coordinator) serveReconcile(w http.ResponseWriter, r *http.Request, in OperatorRequest) {
	if in.Target != "" {
		http.Error(w, "reconcile takes no target", http.StatusBadRequest)
		return
	}
	result, err := c.Reconcile(r.Context())
	if err != nil {
		writeOperatorError(w, err)
		return
	}
	writeOperator(w, result)
}

func writeOperatorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factory.ErrNotFound):
		http.Error(w, "factory run not found", http.StatusNotFound)
	default:
		http.Error(w, "operator command unavailable", http.StatusInternalServerError)
	}
}

func writeOperator(w http.ResponseWriter, out any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
