package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	"github.com/levitateos/factory/pkg/project"
)

var _ execution.RunLauncher = NativeExecution{}

func (p NativeExecution) LaunchRun(ctx context.Context, request execution.LaunchRequest) (execution.RunObservation, error) {
	var empty execution.RunObservation
	if p.Store == nil || request.Validate() != nil || request.Kind != collaboration.Native || request.Native.Authentication == "" && request.Native.CredentialSHA256 == "" || request.Native.Workspace.Profile != p.Config.Profile || request.Native.Workspace.InstanceID != p.Instance {
		return empty, execution.ErrLaunch
	}
	if project.ExecutionManifestRequired(request.Input.Run.ExecutionPhase) {
		if !p.SupportsTaskSource(request.Native.Authentication) {
			return empty, errors.Join(execution.ErrLaunch, execution.ErrTaskUnavailable)
		}
	}
	cap, err := p.Store.AuthorizeNativeLaunch(ctx, request)
	if err != nil {
		return empty, err
	}
	run, err := p.Store.FactoryRun(ctx, request.Address().Run)
	if err != nil {
		return empty, err
	}
	assignment, err := p.Store.Assignment(ctx, cap.AssignmentID)
	if err != nil {
		return empty, err
	}
	preparation, err := p.Store.Preparation(ctx, request.Input.Run.Preparation)
	if err != nil {
		return empty, err
	}
	expected, err := control.BuildMCPCapability(assignment, run, preparation)
	if err != nil || expected.ScopeDigest() != cap.ScopeDigest() {
		return empty, execution.ErrLaunch
	}
	valid, err := NativeMCPSecretVerifier(p.Config, p.Instance)(ctx, cap, request.Input.MCP.Secret)
	if err != nil || !valid {
		return empty, execution.ErrLaunch
	}
	tools, err := mcpserver.BoundToolNames(ctx, p.workerDependencies(ctx), cap)
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
		Operation   string   `json:"operation"`
		Intent      string   `json:"intent"`
		Manifest    []byte   `json:"manifest,omitempty"`
		Prompt      []byte   `json:"prompt"`
		Capability  string   `json:"capability"`
		Secret      string   `json:"secret"`
		ScopeDigest string   `json:"scope_digest"`
		Tools       []string `json:"tools"`
	}{"launch", intent, request.Input.ExecutionManifest, request.Input.Prompt, cap.ID, request.Input.MCP.Secret, cap.ScopeDigest(), tools}, &reply)
	if err != nil {
		return empty, err
	}
	digest := sha256.Sum256([]byte(intent))
	if !reply.Accepted || reply.IntentDigest != hex.EncodeToString(digest[:]) {
		return empty, execution.ErrLaunch
	}
	current, currentErr := p.Store.AuthorizeNativeLaunch(ctx, request)
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
