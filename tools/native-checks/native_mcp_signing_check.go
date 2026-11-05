// A disposable Go-to-Rust native callback check. The fixture reads only its
// own synthetic profile key to compare the protocol HMAC; it prints no secret.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	factoryruntime "github.com/levitateos/factory/pkg/factory/runtime"
)

type syntheticCapability struct{ current factory.MCPCapability }

type syntheticCatalogRun struct{ run factory.Run }

func (s syntheticCatalogRun) FactoryRun(_ context.Context, id string) (factory.Run, error) {
	if s.run.ID != id {
		return factory.Run{}, errors.New("synthetic run unavailable")
	}
	return s.run, nil
}

func (s *syntheticCapability) GetMCPCapability(_ context.Context, id string) (factory.MCPCapability, error) {
	if id != s.current.ID {
		return factory.MCPCapability{}, errors.New("synthetic capability missing")
	}
	return s.current, nil
}

func check(profile, instance string, unavailable bool) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	capability := factory.MCPCapability{
		ID: "0123456789abcdef0123456789abcdef", AssignmentID: "fedcba9876543210fedcba9876543210",
		RunID: "synthetic-run", Generation: 1, Profile: factory.MCPProfileBuilder,
		Subject: "synthetic-subject", Repository: 1, Project: "p0123456789abcdef01234567",
		PreparationDigest: strings.Repeat("a", 64), SourceDigest: strings.Repeat("b", 64),
		AcceptedRevision: strings.Repeat("c", 40), Authority: strings.Repeat("d", 64),
		DeadlineUnix: time.Now().Add(time.Hour).Unix(), State: factory.MCPCapabilityStaged,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	secret, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(ctx, capability)
	if unavailable {
		if err == nil || secret != "" {
			return errors.New("retired native signer admitted a callback")
		}
		verifyCtx, cancelVerify := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancelVerify()
		if valid, err := factoryruntime.NativeMCPSecretVerifier(config, instance)(verifyCtx, capability, strings.Repeat("0", 64)); err == nil || valid {
			return errors.New("retired native verifier admitted a callback")
		}
		return nil
	}
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(profile, "native-mcp.key"))
	if err != nil {
		return err
	}
	expected, err := factory.DeriveMCPCapabilitySecret(key, capability)
	if err != nil || secret != expected {
		return errors.New("native signer disagrees with Go capability protocol")
	}
	verify := factoryruntime.NativeMCPSecretVerifier(config, instance)
	if valid, err := verify(ctx, capability, secret); err != nil || !valid {
		return errors.New("native verifier refused the matching capability secret")
	}
	wrong := "0" + secret[1:]
	if secret[0] == '0' {
		wrong = "1" + secret[1:]
	}
	if valid, err := verify(ctx, capability, wrong); err != nil || valid {
		return errors.New("native verifier admitted a wrong capability secret")
	}
	if valid, err := verify(ctx, capability, "invalid"); err != nil || valid {
		return errors.New("native verifier admitted an invalid capability secret")
	}
	current := capability
	current.State = factory.MCPCapabilityActive
	source := &syntheticCapability{current: current}
	workers := factoryhttpapi.Workers{
		Capabilities: source,
		Admit: func(_ context.Context, observed factory.MCPCapability) (factory.MCPCapability, error) {
			return observed, nil
		},
		Protocol: func(_ context.Context, _ factory.MCPCapability) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), nil
		},
	}
	server := httptest.NewServer(workers.HandlerWithSecretVerifier(verify))
	defer server.Close()
	send := func(bearer string, mutate func(*http.Request)) (int, error) {
		request, err := http.NewRequest(http.MethodPost, server.URL+factoryhttpapi.WorkerRoute, nil)
		if err != nil {
			return 0, err
		}
		request.Header.Set("X-Soda-Factory-Capability", current.ID)
		request.Header.Set("Authorization", "Bearer "+bearer)
		if mutate != nil {
			mutate(request)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		return response.StatusCode, nil
	}
	post := func(bearer string) (int, error) { return send(bearer, nil) }
	if status, err := post(secret); err != nil || status != http.StatusNoContent {
		return errors.New("native worker HTTP refused matching secret")
	}
	if status, err := post(wrong); err != nil || status != http.StatusUnauthorized {
		return errors.New("native worker HTTP admitted wrong secret")
	}
	if status, err := send(secret, func(request *http.Request) {
		request.Header.Add("Authorization", "Bearer "+wrong)
	}); err != nil || status != http.StatusUnauthorized {
		return errors.New("native worker HTTP admitted ambiguous bearer headers")
	}
	if status, err := send(secret, func(request *http.Request) {
		request.Header.Add("X-Soda-Factory-Capability", current.ID)
	}); err != nil || status != http.StatusUnauthorized {
		return errors.New("native worker HTTP admitted ambiguous capability headers")
	}
	if status, err := send(secret, func(request *http.Request) {
		request.Header.Add("Origin", "")
	}); err != nil || status != http.StatusForbidden {
		return errors.New("native worker HTTP admitted an Origin header")
	}
	if status, err := send(secret, func(request *http.Request) {
		request.URL.RawQuery = "extra=1"
	}); err != nil || status != http.StatusBadRequest {
		return errors.New("native worker HTTP admitted a noncanonical target")
	}
	prepare := func(_ context.Context, capability factory.MCPCapability) (factory.MCPCapability, error) {
		return capability, nil
	}
	observed := func(capability factory.MCPCapability) (control.MCPRunObservation, error) {
		return control.MCPRunObservation{
			RunID: capability.RunID, AssignmentID: capability.AssignmentID, Authority: capability.Authority,
		}, nil
	}
	bound := func(deps mcpserver.Dependencies) (int, error) {
		server := httptest.NewServer(mcpserver.NewHandlerWithSecretVerifier(deps, verify))
		defer server.Close()
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"native-bound-check","version":"1.0"}}}`
		request, err := http.NewRequest(http.MethodPost, server.URL+factoryhttpapi.WorkerRoute, strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		request.Header.Set("X-Soda-Factory-Capability", current.ID)
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		return response.StatusCode, nil
	}
	deps := mcpserver.Dependencies{Capabilities: source, Prepare: prepare, Observed: observed}
	if status, err := bound(deps); err != nil || status != http.StatusOK {
		return errors.New("bound native MCP refused exact current work and run")
	}
	deps.Prepare = nil
	if status, err := bound(deps); err != nil || status != http.StatusForbidden {
		return errors.New("bound native MCP admitted missing current work")
	}
	deps.Prepare = prepare
	deps.Observed = nil
	if status, err := bound(deps); err != nil || status != http.StatusForbidden {
		return errors.New("bound native MCP admitted missing run observation")
	}
	deps.Observed = func(factory.MCPCapability) (control.MCPRunObservation, error) {
		return control.MCPRunObservation{}, nil
	}
	if status, err := bound(deps); err != nil || status != http.StatusForbidden {
		return errors.New("bound native MCP admitted empty run observation")
	}
	deps.Observed = observed
	deps.Prepare = func(_ context.Context, capability factory.MCPCapability) (factory.MCPCapability, error) {
		capability.Authority = strings.Repeat("e", 64)
		return capability, nil
	}
	if status, err := bound(deps); err != nil || status != http.StatusForbidden {
		return errors.New("bound native MCP admitted changed work scope")
	}
	deps.Prepare = prepare
	deps.Observed = func(capability factory.MCPCapability) (control.MCPRunObservation, error) {
		result, _ := observed(capability)
		result.RunID = "other-run"
		return result, nil
	}
	if status, err := bound(deps); err != nil || status != http.StatusForbidden {
		return errors.New("bound native MCP admitted changed run observation")
	}
	if err := checkCatalogs(config, instance, capability); err != nil {
		return err
	}
	source.current.State = factory.MCPCapabilityRevoked
	if status, err := post(secret); err != nil || status != http.StatusForbidden {
		return errors.New("native worker HTTP admitted revoked capability")
	}
	source.current.State = factory.MCPCapabilityActive
	source.current.DeadlineUnix = time.Now().Add(-time.Second).Unix()
	expired, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(ctx, source.current)
	if err != nil {
		return err
	}
	if status, err := post(expired); err != nil || status != http.StatusForbidden {
		return errors.New("native worker HTTP admitted expired capability")
	}
	fmt.Println("PASS: Go native MCP clients, Rust verifier and synthetic worker HTTP admission agree")
	return nil
}

// Compare prelaunch names with actual authenticated SDK discovery. The run
// metadata below is deliberately unplaced and grants no task controller. This
// is catalog transport evidence, not native execution or capability activation.
func checkCatalogs(config factoryruntime.NativeConfig, instance string, capability factory.MCPCapability) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, profile := range factory.MCPProfiles {
		capability.Profile = profile
		capability.State = factory.MCPCapabilityStaged
		source := &syntheticCapability{current: capability}
		preparations, observations := 0, 0
		deps := mcpserver.Dependencies{
			Capabilities: source,
			Prepare: func(_ context.Context, cap factory.MCPCapability) (factory.MCPCapability, error) {
				preparations++
				return cap, nil
			},
			Observed: func(cap factory.MCPCapability) (control.MCPRunObservation, error) {
				observations++
				return control.MCPRunObservation{RunID: cap.RunID, AssignmentID: cap.AssignmentID, Authority: cap.Authority}, nil
			},
		}
		for _, mode := range []string{"no-port", "no-run-source", "missing-run", "unplaced-native"} {
			deps.NativeTask = nil
			deps.Runs = nil
			if mode != "no-port" {
				deps.NativeTask = func(context.Context, factory.MCPCapability, string, string) (map[string]any, error) {
					return nil, errors.New("catalog discovery must not execute a task")
				}
			}
			if mode == "missing-run" {
				deps.Runs = syntheticCatalogRun{}
			} else if mode == "unplaced-native" {
				deps.Runs = syntheticCatalogRun{run: factory.Run{ID: capability.RunID, Native: &execution.NativeRecord{}}}
			}
			capability.State = factory.MCPCapabilityStaged
			source.current = capability
			beforePreparations, beforeObservations := preparations, observations
			names, err := mcpserver.BoundToolNames(ctx, deps, capability)
			if err != nil || len(names) == 0 || source.current.State != factory.MCPCapabilityStaged || preparations != beforePreparations || observations != beforeObservations {
				return fmt.Errorf("prelaunch catalog failed for %s/%s: %v", profile, mode, err)
			}
			if slices.Contains(names, string(factory.MCPToolStartTask)) || slices.Contains(names, string(factory.MCPToolReleaseOutput)) {
				return errors.New("prelaunch catalog advertised an unavailable task controller")
			}
			secret, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(ctx, capability)
			if err != nil {
				return err
			}
			source.current.State = factory.MCPCapabilityActive
			if err := discoverCatalog(ctx, deps, factoryruntime.NativeMCPSecretVerifier(config, instance), capability.ID, secret, names); err != nil {
				return fmt.Errorf("catalog differs for %s/%s: %w", profile, mode, err)
			}
		}
	}
	if _, err := mcpserver.BoundToolNames(ctx, mcpserver.Dependencies{}, factory.MCPCapability{}); err == nil {
		return errors.New("prelaunch catalog accepted an invalid capability")
	}
	fmt.Println("PASS: all eight prelaunch role catalogs match Rust-authenticated MCP SDK discovery; unavailable task controllers stay absent")
	return nil
}

func discoverCatalog(ctx context.Context, deps mcpserver.Dependencies, verify factory.MCPSecretVerifier, id, secret string, expected []string) error {
	server := httptest.NewServer(mcpserver.NewHandlerWithSecretVerifier(deps, verify))
	defer server.Close()
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"native-catalog-check","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+factoryhttpapi.WorkerRoute, strings.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("X-Soda-Factory-Capability", id)
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-06-18")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return err
		}
		var result struct {
			Error  json.RawMessage `json:"error"`
			Result struct {
				NextCursor string `json:"nextCursor"`
				Tools      []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || len(result.Error) != 0 {
			return fmt.Errorf("SDK catalog request failed: status %d, decode %v", response.StatusCode, err)
		}
		if strings.Contains(body, `"tools/list"`) {
			names := make([]string, 0, len(result.Result.Tools))
			for _, tool := range result.Result.Tools {
				names = append(names, tool.Name)
			}
			slices.Sort(names)
			if result.Result.NextCursor != "" || !slices.Equal(names, expected) {
				return errors.New("SDK discovery differs from the trusted prelaunch tool names")
			}
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "profile and instance required")
		os.Exit(2)
	}
	if err := check(os.Args[1], os.Args[2], len(os.Args) == 4 && os.Args[3] == "retired"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
