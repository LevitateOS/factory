package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryhttp "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	"github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/strictjson"
)

// Use this same catalog for prelaunch delivery and the authenticated server.
// Optional catalog effects stay unavailable until their owners join.
func (p NativeExecution) workerDependencies(lifetime context.Context) mcpserver.Dependencies {
	admission := service.WorkerAdmission{Store: p.Store, Inspect: p.InspectRun}
	tasks := service.WorkerTasks{Store: p.Store, Inspect: p.InspectRun, Command: p.CommandTask}
	return mcpserver.Dependencies{Capabilities: p.Store, Assignments: p.Store, Submissions: p.Store, Runs: p.Store,
		SystemOneForWorker:  p.ConsultationsForWorker,
		ConsultationJournal: p.Store,
		Prepare:             admission.Prepare,
		NativeTask:          tasks.Execute,
		Workspace:           tasks.Workspace,
		ReadSource:          (service.WorkerSource{Store: p.Store, Reader: p}).Read,
		ListSource:          (service.WorkerSource{Store: p.Store, Reader: p}).List,
		Observed: func(cap factory.MCPCapability) (control.MCPRunObservation, error) {
			ctx, cancel := context.WithTimeout(lifetime, 5*time.Second)
			defer cancel()
			current, err := admission.Prepare(ctx, cap)
			if err != nil {
				return control.MCPRunObservation{}, err
			}
			return control.MCPRunObservation{RunID: current.RunID, AssignmentID: current.AssignmentID, Authority: current.Authority}, nil
		},
	}
}

// The outer native channel authenticates Rust's IPC request. The inner worker
// boundary separately authenticates its exact-run secret and current admission.
// Workers supply only JSON-RPC payload bytes; Rust captures every selector.
func nativeWorkerMCP(lifetime context.Context, p NativeExecution) http.Handler {
	worker := mcpserver.NewHandlerWithSecretVerifier(p.workerDependencies(lifetime), NativeMCPSecretVerifier(p.Config, p.Instance))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/native/execution/mcp" || r.URL.RawQuery != "" || r.URL.ForceQuery ||
			len(r.Header.Values("X-Soda-Native-Instance")) != 1 || r.Header.Get("X-Soda-Native-Instance") != p.Instance {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_worker_request"})
			return
		}
		var input struct {
			Intent     string          `json:"intent"`
			Capability string          `json:"capability"`
			Secret     string          `json:"secret"`
			Payload    json.RawMessage `json:"payload"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, factory.MCPMaxSubmissionPayload+40960), &input) != nil || len(input.Intent) > 16384 ||
			!factory.ValidMCPSecret(input.Secret) || !factory.ValidID(input.Capability) || len(input.Payload) == 0 || len(input.Payload) > factory.MCPMaxSubmissionPayload+8192 {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_worker_request"})
			return
		}
		cap, err := p.Store.GetMCPCapability(r.Context(), input.Capability)
		if err == nil {
			run, readErr := p.Store.FactoryRun(r.Context(), cap.RunID)
			err = readErr
			if err == nil && (run.Native == nil || run.Native.Intent != input.Intent || run.Native.Provenance.Profile != p.Config.Profile || run.Native.Provenance.InstanceID != p.Instance ||
				run.ID != cap.RunID || run.ProjectID != cap.Project || run.Reconciled) {
				err = errors.New("native worker binding changed")
			}
		}
		if err != nil {
			nativeResponse(w, 403, map[string]string{"error": "native_worker_binding_unavailable"})
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, factoryhttp.WorkerRoute, bytes.NewReader(input.Payload))
		if err != nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_worker_request"})
			return
		}
		request.Header.Set("Authorization", "Bearer "+input.Secret)
		request.Header.Set("X-Soda-Factory-Capability", input.Capability)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-06-18")
		worker.ServeHTTP(w, request)
	})
}
