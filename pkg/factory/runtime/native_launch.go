package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/strictjson"
)

var _ execution.RunLauncher = NativeExecution{}

func (p NativeExecution) authorizeLaunch(ctx context.Context, request execution.LaunchRequest) (factory.MCPCapability, []string, error) {
	var empty factory.MCPCapability
	if p.Store == nil || request.Validate() != nil || request.Kind != collaboration.Native || request.Native.Authentication == "" && request.Native.CredentialSHA256 == "" || request.Native.Workspace.Profile != p.Config.Profile || request.Native.Workspace.InstanceID != p.Instance {
		return empty, nil, execution.ErrLaunch
	}
	if project.ExecutionManifestRequired(request.Input.Run.ExecutionPhase) {
		if !p.SupportsTaskSource(request.Native.Authentication) {
			return empty, nil, errors.Join(execution.ErrLaunch, execution.ErrTaskUnavailable)
		}
	}
	cap, err := p.Store.AuthorizeFactoryLaunch(ctx, request)
	if err != nil {
		return empty, nil, err
	}
	run, err := p.Store.FactoryRun(ctx, request.Address().Run)
	if err != nil {
		return empty, nil, err
	}
	assignment, err := p.Store.Assignment(ctx, cap.AssignmentID)
	if err != nil {
		return empty, nil, err
	}
	preparation, err := p.Store.Preparation(ctx, request.Input.Run.Preparation)
	if err != nil {
		return empty, nil, err
	}
	expected, err := control.BuildMCPCapability(assignment, run, preparation)
	if err != nil || expected.ScopeDigest() != cap.ScopeDigest() {
		return empty, nil, execution.ErrLaunch
	}
	valid, err := NativeMCPSecretVerifier(p.Config, p.Instance)(ctx, cap, request.Input.MCP.Secret)
	if err != nil || !valid {
		return empty, nil, execution.ErrLaunch
	}
	tools, err := mcpserver.BoundToolNames(ctx, p.workerDependencies(ctx), cap)
	if err != nil {
		return empty, nil, err
	}
	return cap, tools, nil
}

// Reuse the defining admission immediately before a host-controlled worker
// starts, after the remote claim I/O. This private route grants no new authority.
func nativeLaunchAdmission(p NativeExecution) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/native/execution/launch-admission" || r.URL.RawQuery != "" || r.URL.ForceQuery ||
			len(r.Header.Values("X-Soda-Native-Instance")) != 1 || r.Header.Get("X-Soda-Native-Instance") != p.Instance {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_launch_admission"})
			return
		}
		var input struct {
			Request    execution.LaunchRequest `json:"request"`
			Origin     string                  `json:"origin"`
			Actor      string                  `json:"actor"`
			Repository string                  `json:"repository"`
		}
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 2*1024*1024), &input) != nil || input.Request.Controller == nil {
			nativeResponse(w, 400, map[string]string{"error": "invalid_native_launch_admission"})
			return
		}
		request := input.Request
		cap, _, err := p.authorizeLaunch(r.Context(), request)
		if err != nil {
			nativeResponse(w, 403, map[string]string{"error": "native_launch_admission_unavailable"})
			return
		}
		connection, connectionErr := p.Store.CollaborationConnection(r.Context(), collaboration.ConnectionID(request.Controller.Connection))
		repository, repositoryErr := p.Store.CollaborationRepository(r.Context(), collaboration.RepositoryID(cap.Repository))
		actor, actorErr := p.Store.CollaborationActor(r.Context(), connection.Actor)
		host, hostErr := p.Store.CollaborationHost(r.Context(), connection.Host)
		run, runErr := p.Store.FactoryRun(r.Context(), cap.RunID)
		origin, originErr := collaboration.CanonicalAPIBase(input.Origin)
		if connectionErr != nil || repositoryErr != nil || actorErr != nil || hostErr != nil || runErr != nil || originErr != nil || run.Admission == nil || run.Admission.Policy.Execution == nil ||
			connection.Profile != p.Config.Profile || host.Provider != collaboration.Fountain || host.APIBase != origin || repository.Host != host.ID || actor.Host != host.ID ||
			actor.ID != collaboration.ActorID(run.Admission.Policy.Execution.ActorID) || actor.ExternalID != input.Actor || repository.ExternalID != input.Repository {
			nativeResponse(w, 403, map[string]string{"error": "native_launch_scope_unavailable"})
			return
		}
		intent, err := execution.EncodeNativeIntent(request)
		if err != nil {
			nativeResponse(w, 403, map[string]string{"error": "native_launch_admission_unavailable"})
			return
		}
		digest := sha256.Sum256([]byte(intent))
		nativeResponse(w, 200, map[string]string{"intent_digest": hex.EncodeToString(digest[:])})
	})
}

func (p NativeExecution) LaunchRun(ctx context.Context, request execution.LaunchRequest) (execution.RunObservation, error) {
	var empty execution.RunObservation
	cap, tools, err := p.authorizeLaunch(ctx, request)
	if err != nil {
		return empty, err
	}
	intent, err := execution.EncodeNativeIntent(request)
	if err != nil {
		return empty, err
	}
	var reply struct {
		IntentDigest string `json:"intent_digest"`
		Accepted     bool   `json:"accepted"`
	}
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation   string                   `json:"operation"`
		Intent      string                   `json:"intent"`
		Manifest    []byte                   `json:"manifest,omitempty"`
		Prompt      []byte                   `json:"prompt"`
		Capability  string                   `json:"capability"`
		Secret      string                   `json:"secret"`
		ScopeDigest string                   `json:"scope_digest"`
		Tools       []string                 `json:"tools"`
		Admission   *execution.LaunchRequest `json:"admission,omitempty"`
	}{"launch", intent, request.Input.ExecutionManifest, request.Input.Prompt, cap.ID, request.Input.MCP.Secret, cap.ScopeDigest(), tools, func() *execution.LaunchRequest {
		if request.Controller != nil {
			return &request
		}
		return nil
	}()}, &reply)
	if err != nil {
		return empty, err
	}
	digest := sha256.Sum256([]byte(intent))
	if !reply.Accepted || reply.IntentDigest != hex.EncodeToString(digest[:]) {
		return empty, execution.ErrLaunch
	}
	current, currentErr := p.Store.AuthorizeFactoryLaunch(ctx, request)
	if currentErr != nil || current.ScopeDigest() != cap.ScopeDigest() {
		// Admission changed during native I/O. Cancellation targets the captured
		// intent even if its first process observation has not arrived yet.
		var ignored nativeExecutionSnapshot
		cancelErr := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
			Operation string `json:"operation"`
			Intent    string `json:"intent"`
		}{"cancel", intent}, &ignored)
		return empty, errors.Join(execution.ErrLaunch, currentErr, cancelErr)
	}
	for {
		observed, observeErr := p.InspectRun(ctx, request.Address())
		if observeErr == nil && (observed.Live || observed.ExitCode != nil || observed.Retirement == execution.Retired) ||
			observeErr != nil && !errors.Is(observeErr, execution.ErrRunNotObserved) {
			return observed, observeErr
		}
		select {
		case <-ctx.Done():
			return empty, errors.Join(execution.ErrRunNotObserved, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
