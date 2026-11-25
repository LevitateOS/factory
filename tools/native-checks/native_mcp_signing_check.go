// A disposable Go-to-Rust native callback check. The fixture reads only its
// own synthetic profile key to compare the protocol HMAC; it prints no secret.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/access"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	"github.com/levitateos/factory/pkg/factory/mcpwire"
	factoryruntime "github.com/levitateos/factory/pkg/factory/runtime"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

type syntheticCapability struct{ current factory.MCPCapability }

type pendingProductJudgment struct{}

func checkMCPRotation(profile, instance, mode string) error {
	seed := mode == "mcp-rotation-seed"
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	for index, state := range []string{factory.MCPCapabilityActive, factory.MCPCapabilityStaged, factory.MCPCapabilityRetired} {
		id := strings.Repeat(string(rune('a'+index)), 32)
		cap := factory.MCPCapability{ID: id, RunID: id, AssignmentID: id, Generation: 1,
			Profile: factory.MCPProfileBuilder, Subject: "synthetic-subject", Repository: 1, Project: "p0123456789abcdef01234567",
			PreparationDigest: strings.Repeat("a", 64), SourceDigest: strings.Repeat("b", 64),
			AcceptedRevision: strings.Repeat("c", 40), Authority: strings.Repeat("d", 64), DeadlineUnix: 4000000000, State: factory.MCPCapabilityStaged}
		if seed {
			if err = db.StageMCPCapability(ctx, cap); err != nil {
				return err
			}
			if state != factory.MCPCapabilityStaged {
				if err = db.ActivateMCPCapability(ctx, id, cap.Generation, id, id, cap.Authority, time.Now().Unix(), false); err != nil {
					return err
				}
			}
			if state == factory.MCPCapabilityRetired {
				if err = db.RetireMCPCapability(ctx, id); err != nil {
					return err
				}
			}
			if index == 0 {
				secret, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(ctx, cap)
				if err != nil {
					return err
				}
				if err = os.WriteFile(filepath.Join(profile, "rotation-old-secret"), []byte(secret), 0600); err != nil {
					return err
				}
			}
		} else {
			actual, readErr := db.GetMCPCapability(ctx, id)
			if readErr != nil {
				return readErr
			}
			wanted := factory.MCPCapabilityRevoked
			if state == factory.MCPCapabilityRetired {
				wanted = state
			}
			if actual.State != wanted || actual.ScopeDigest() != cap.ScopeDigest() {
				return errors.New("rotation lost state or immutable capability scope")
			}
			if secret, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(ctx, actual); err == nil || secret != "" {
				return errors.New("old capability received new-root authority")
			}
			if index == 0 {
				secret, err := os.ReadFile(filepath.Join(profile, "rotation-old-secret"))
				if err != nil {
					return err
				}
				if valid, err := factoryruntime.NativeMCPSecretVerifier(config, instance)(ctx, actual, string(secret)); err != nil || valid {
					return errors.New("old capability token survived real native root rotation")
				}
			}
			if err = db.StageMCPCapability(ctx, cap); err != nil {
				return err
			}
			if err = db.ActivateMCPCapability(ctx, id, cap.Generation, id, id, cap.Authority, time.Now().Unix(), false); !errors.Is(err, store.ErrMCPCapabilityState) {
				return errors.New("old run capability reactivated after root rotation")
			}
		}
	}
	if !seed {
		fresh := factory.MCPCapability{ID: strings.Repeat("d", 32), RunID: strings.Repeat("d", 32), AssignmentID: strings.Repeat("d", 32), Generation: 1,
			Profile: factory.MCPProfileBuilder, Subject: "fresh-synthetic-subject", Repository: 1, Project: "p0123456789abcdef01234567",
			PreparationDigest: strings.Repeat("a", 64), SourceDigest: strings.Repeat("b", 64), AcceptedRevision: strings.Repeat("c", 40), Authority: strings.Repeat("d", 64), DeadlineUnix: 4000000000, State: factory.MCPCapabilityStaged}
		if mode == "mcp-rotation-check" {
			if err = db.StageMCPCapability(ctx, fresh); err != nil {
				return err
			}
			if err = db.ActivateMCPCapability(ctx, fresh.ID, 1, fresh.RunID, fresh.AssignmentID, fresh.Authority, time.Now().Unix(), false); err != nil {
				return err
			}
		}
		actual, err := db.GetMCPCapability(ctx, fresh.ID)
		if err != nil || actual.State != factory.MCPCapabilityActive {
			return errors.New("rotation command replay revoked fresh authority")
		}
		secret, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(ctx, actual)
		if err != nil {
			return err
		}
		if valid, err := factoryruntime.NativeMCPSecretVerifier(config, instance)(ctx, actual, secret); err != nil || !valid {
			return errors.New("new native root failed fresh capability derivation/verification")
		}
		connection, err := sql.Open("pgx", config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer connection.Close()
		for _, table := range []string{"factory_runs", "factory_commands", "factory_usage"} {
			var count int
			if err = connection.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return errors.New("MCP maintenance changed unrelated work/accounting")
			}
		}
	}
	fmt.Println("PASS: real PostgreSQL MCP rotation preserves immutable scopes/history and refuses old-run reactivation; no inference, work or charges")
	return nil
}

// Public scope from the connected Fountain fixture; credentials stay in Rust.
type nativeControllerFixture struct {
	Origin, Connection, Actor, Repository, Grant string
}

func controllerFixture(profile string) (*nativeControllerFixture, error) {
	if os.Getenv("SODA_NATIVE_CHECK_CONTROLLER") != "1" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(profile, "controller-fixture.json"))
	var capture nativeControllerFixture
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &capture) != nil || capture.Origin == "" || capture.Actor == "" || capture.Repository == "" || !factory.ValidID(capture.Connection) || !factory.ValidID(capture.Grant) {
		return nil, errors.New("connected controller fixture scope missing")
	}
	return &capture, nil
}

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
	if err := checkNativeRecovery(config, instance, unavailable); err != nil {
		return err
	}
	if err := capability.Validate(); err != nil {
		return fmt.Errorf("signing fixture capability: %w", err)
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
	expiredContext, cancelExpired := context.WithTimeout(context.Background(), 6*time.Second)
	expired, err := factoryruntime.NativeMCPSecretDeriver(config, instance)(expiredContext, source.current)
	cancelExpired()
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
			deps.ReadSource = nil
			deps.ListSource = nil
			deps.Runs = nil
			if mode != "no-port" {
				deps.ReadSource = func(context.Context, factory.MCPCapability, project.FactoryContextReadPage) (map[string]any, error) {
					return nil, errors.New("catalog discovery must not read source")
				}
				deps.ListSource = func(context.Context, factory.MCPCapability, string) (map[string]any, error) {
					return nil, errors.New("catalog discovery must not list source")
				}
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
			if slices.Contains(names, string(factory.MCPToolReadSource)) != (deps.ReadSource != nil) {
				return errors.New("source discovery differs from reader availability")
			}
			if slices.Contains(names, string(factory.MCPToolListSource)) != (deps.ListSource != nil) {
				return errors.New("source discovery differs from directory reader availability")
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

// Uses the owned disposable PostgreSQL and actual private Go HTTP listener.
// The run metadata is declared fixture data, never native execution evidence.
func checkNativeRecovery(config factoryruntime.NativeConfig, instance string, unavailable bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", config.Socket)
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	read := func(selector, selectedInstance string, mutate func(*http.Request)) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://soda-native/api/native/execution/recovery/"+selector, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
		req.Header.Set("X-Soda-Native-Instance", selectedInstance)
		if mutate != nil {
			mutate(req)
		}
		response, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
		if len(raw) > 16384 {
			return 0, nil, errors.New("recovery projection exceeded its fixture bound")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			return 0, nil, errors.New("private recovery response was cacheable")
		}
		return response.StatusCode, raw, err
	}
	brokerRun := strings.Repeat("c7", 16)
	if unavailable {
		if status, _, err := read(brokerRun, instance, nil); err == nil || status == http.StatusOK {
			return errors.New("retired backend released recovery metadata")
		}
		return nil
	}
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	marker := filepath.Join(config.Directory, "check-recovery-run.id")
	retainedID, markerErr := os.ReadFile(marker)
	run, err := db.NativeFactoryRun(ctx, config.Profile, brokerRun)
	if errors.Is(err, store.ErrNotFound) {
		if !errors.Is(markerErr, os.ErrNotExist) {
			return errors.New("previous canonical recovery row disappeared across restart")
		}
		now := time.Now().UTC()
		run = factory.Run{ID: factory.NewID(), ProjectID: "p123456789012345678901234", Role: "reviewer", InputSHA: strings.Repeat("a", 40), Started: now, Deadline: now.Add(time.Hour), Harness: "muse-fixture-1", Model: "local-fixture", Summary: "fixture-private-summary-omitted"}
		run.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("b", 64), Executable: "/home/fixture/bin/muse"}, BundleSHA256: strings.Repeat("d", 64), PromptSHA256: strings.Repeat("e", 64), Provenance: execution.NativeProvenance{TrustMode: "isolated_system_worker", Profile: config.Profile, InstanceID: factory.NewID(), WorktreeID: "declared-native-checkout", BrokerRun: brokerRun, Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.NotStarted}
		if err = db.RecordFactoryRun(ctx, run); err != nil {
			return err
		}
		if err = os.WriteFile(marker, []byte(run.ID), 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if markerErr != nil || string(retainedID) != run.ID {
		return errors.New("canonical recovery mapping changed across restart")
	}
	if run.ID == brokerRun {
		return errors.New("fixture failed to distinguish canonical and broker IDs")
	}
	status, raw, err := read(brokerRun, instance, nil)
	if err != nil || status != 200 {
		return fmt.Errorf("native recovery read failed: %d %v", status, err)
	}
	var envelope struct {
		Kind     string
		Version  int
		Profile  collaboration.ProfileID
		Instance string
		Run      struct {
			ID, Workspace, Role string
			SourceCommit        string `json:"source_commit"`
			Native              *execution.NativeRecord
			Launch              *execution.NativeLaunch
			Reconciled          bool
		}
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Kind != "native_run_recovery" || envelope.Version != 1 || envelope.Profile != config.Profile || envelope.Instance != instance || envelope.Run.ID != run.ID || envelope.Run.Workspace != run.ProjectID || envelope.Run.Role != run.Role || envelope.Run.SourceCommit != run.InputSHA || !reflect.DeepEqual(envelope.Run.Native, run.Native) || envelope.Run.Launch != nil || envelope.Run.Reconciled || strings.Contains(string(raw), run.Summary) {
		return errors.New("native recovery projection changed canonical evidence or invented placement/admission")
	}
	for _, probe := range []struct {
		selector, instance string
		mutate             func(*http.Request)
		status             int
	}{
		{factory.NewID(), instance, nil, 404}, {"invalid", instance, nil, 400}, {brokerRun, factory.NewID(), nil, 401},
		{brokerRun, instance, func(r *http.Request) { r.URL.RawQuery = "extra=1" }, 400},
		{brokerRun, instance, func(r *http.Request) { r.Header.Set("Origin", "http://fixture.invalid") }, 403},
		{brokerRun, instance, func(r *http.Request) { r.Method = http.MethodPost }, 400},
		{brokerRun, instance, func(r *http.Request) { r.Header.Add("X-Soda-Native-Instance", instance) }, 401},
	} {
		if status, _, err := read(probe.selector, probe.instance, probe.mutate); err != nil || status != probe.status {
			return fmt.Errorf("recovery refusal differs: %d want %d, %v", status, probe.status, err)
		}
	}
	clone := func() factory.Run {
		next := run
		native := *run.Native
		next.Native = &native
		next.ID = factory.NewID()
		return next
	}
	other := clone()
	other.Native.Provenance.Profile = collaboration.ProfileID(factory.NewID())
	if err = db.RecordFactoryRun(ctx, other); err != nil {
		return err
	}
	if status, _, err := read(brokerRun, instance, nil); err != nil || status != 200 {
		return errors.New("another profile changed the selected recovery mapping")
	}
	if _, err := db.NativeFactoryRun(ctx, other.Native.Provenance.Profile, factory.NewID()); !errors.Is(err, store.ErrNotFound) {
		return errors.New("missing selector became observed")
	}
	settled := clone()
	settled.Native.Provenance.BrokerRun = factory.NewID()
	settled.Native.Retirement = execution.Retired
	settled.Native.Sealed = true
	if err = db.RecordFactoryRun(ctx, settled); err != nil {
		return err
	}
	settled.Outcome, settled.Reconciled = factory.Cancelled, true
	if err = db.SaveFactoryRun(ctx, settled); err != nil {
		return err
	}
	if status, raw, err := read(settled.Native.Provenance.BrokerRun, instance, nil); err != nil || status != 200 || !strings.Contains(string(raw), "\"reconciled\":true") {
		return errors.New("settled history disappeared from recovery reads")
	}
	ambiguous := clone()
	ambiguous.Native.Provenance.BrokerRun = factory.NewID()
	if err = db.RecordFactoryRun(ctx, ambiguous); err != nil {
		return err
	}
	duplicate := ambiguous
	duplicate.ID = factory.NewID()
	if err = db.RecordFactoryRun(ctx, duplicate); err != nil {
		return err
	}
	if status, _, err := read(ambiguous.Native.Provenance.BrokerRun, instance, nil); err != nil || status != 409 {
		return errors.New("ambiguous broker selector selected one canonical run")
	}
	// Simulated damaged rows in this disposable ledger must not become a
	// canonical selector match. No production record or broker is changed.
	damaged := clone()
	damaged.Native.Provenance.BrokerRun = factory.NewID()
	if err = db.RecordFactoryRun(ctx, damaged); err != nil {
		return err
	}
	rawDB, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	if _, err = rawDB.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{id}',to_jsonb($1::text)) WHERE id=$2`, factory.NewID(), damaged.ID); err != nil {
		return err
	}
	if status, _, err := read(damaged.Native.Provenance.BrokerRun, instance, nil); err != nil || status != 409 {
		return errors.New("damaged row ID became a canonical recovery mapping")
	}
	if _, err = rawDB.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(jsonb_set(data,'{id}',to_jsonb($1::text)),'{native,prompt_sha256}','""'::jsonb) WHERE id=$1`, damaged.ID); err != nil {
		return err
	}
	if status, _, err := read(damaged.Native.Provenance.BrokerRun, instance, nil); err != nil || status != 409 {
		return errors.New("invalid retained scope became recovery evidence")
	}

	after, err := db.FactoryRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(after, run) {
		return errors.New("recovery GET changed its retained run")
	}
	fmt.Println("PASS: real native ledger read preserves distinct canonical/broker scope and unplaced history; settled history remains readable; missing, stale, noncanonical, browser, ambiguous and damaged-record reads refuse; GET does not mutate the run")
	return nil
}

// Seed only a declared native record in the disposable integration
// profile. Production admission and outcome transitions are not exercised here.
func nativeReceiptFixture(profile, instance, operation, argument string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	path := filepath.Join(profile, "native-recovery-fixture.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) > 32768 {
		return errors.New("native receipt fixture too large")
	}
	var input struct {
		Run           factory.Run
		StartedUnixMS int64 `json:"started_unix_ms"`
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		return err
	}
	run := input.Run
	if run.Native == nil || run.Native.Provenance.Process == nil || run.Native.Provenance.Profile != config.Profile {
		return errors.New("native receipt fixture scope differs")
	}
	run.Started = time.UnixMilli(input.StartedUnixMS).UTC()
	run.Deadline = run.Started.Add(time.Hour)
	run.Native.Provenance.Process.StartedAt = run.Started
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	marker := filepath.Join(profile, "native-recovery-canonical.id")
	if operation == "seed-native-run" {
		if argument != path || run.Native.Provenance.InstanceID != instance {
			return errors.New("native receipt fixture owner differs")
		}
		if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			return errors.New("native receipt fixture already seeded")
		}
		run.ID = factory.NewID()
		if err = db.RecordFactoryRun(ctx, run); err != nil {
			return err
		}
		return os.WriteFile(marker, []byte(run.ID), 0600)
	}
	return errors.New("unknown native receipt fixture operation")
}

// Declare one complete effect binding before the real native fixture stages.
// No dispatch/placement permission or coordinator intake is granted here.
func nativeIntentFixture(profile, instance, operation, argument string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	path := filepath.Join(profile, "native-recovery-fixture.json")
	if argument != path {
		return errors.New("native intent fixture path differs")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) > project.MaxFactoryPrompt+32768 {
		return errors.New("native intent fixture too large")
	}
	var input struct {
		Run           factory.Run
		Prompt        string
		Connection    string
		StartedUnixMS int64 `json:"started_unix_ms"`
	}
	if json.Unmarshal(raw, &input) != nil || input.Run.Native == nil {
		return errors.New("native intent fixture invalid")
	}
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	marker := filepath.Join(profile, "native-recovery-canonical.id")
	if operation == "create-native-intent" {
		run := input.Run
		run.ID = factory.NewID()
		run.Started = time.Now().UTC()
		run.Deadline = run.Started.Add(time.Minute)
		if run.Native.Provenance.Profile != config.Profile || run.Native.Provenance.InstanceID != instance || run.Native.Provenance.Process != nil {
			return errors.New("native intent fixture scope differs")
		}
		launch := execution.NativeLaunch{Workspace: execution.NativeWorkspaceRef{Profile: config.Profile, InstanceID: instance, WorktreeID: run.Native.Provenance.WorktreeID}, Tool: run.Native.Tool, BundleSHA256: run.Native.BundleSHA256, PromptSHA256: run.Native.PromptSHA256, Runtime: collaboration.RuntimeID(strings.Repeat("e", 32)), PlacementGeneration: 7, TrustMode: "isolated_system_worker", BrokerRun: run.Native.Provenance.BrokerRun}
		account, err := (factoryruntime.NativeExecution{Config: config, Instance: instance}).ExecutionAccount(ctx, input.Connection)
		if err != nil {
			return err
		}
		launch.CredentialSHA256 = account.CredentialSHA256
		request := execution.LaunchRequest{Kind: collaboration.Native, Native: &launch, Input: project.FactoryLaunch{Run: project.FactoryRun{ID: run.ID, Project: run.ProjectID, Role: run.Role, Preparation: "f" + strings.Repeat("0", 24), Harness: run.Native.Tool.Harness, HarnessVers: run.Native.Tool.Version, Model: run.Model, Assignment: run.Native.PromptSHA256, SourceCommit: run.InputSHA, Connection: input.Connection, Actor: 1, Deadline: run.Deadline, ExecutionPhase: project.ExecutionReview}, Prompt: []byte(input.Prompt), HarnessSHA256: run.Native.Tool.SHA256}}
		request.Input.Run.Effort = factory.DefaultEffort
		request.Input.Run.Contributor = factory.DefaultContributor
		encoded, err := execution.EncodeNativeIntent(request)
		if err != nil {
			return err
		}
		run.Native.Intent = encoded
		if err = db.RecordFactoryRun(ctx, run); err != nil {
			return err
		}
		if err = os.WriteFile(marker, []byte(run.ID), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(profile, "native-recovery-intent.json"), []byte(encoded), 0600)
	}
	if operation != "finish-native-intent" {
		return errors.New("unknown native intent fixture operation")
	}
	id, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	prior, err := db.FactoryRun(ctx, string(id))
	if err != nil {
		return err
	}
	if prior.Native == nil || prior.Native.Intent == "" || input.Run.Native.Provenance.Process == nil {
		return errors.New("native intent fixture result lacks binding")
	}
	observed := input.Run.Native
	observed.Intent = prior.Native.Intent
	observed.Provenance.Process.StartedAt = time.UnixMilli(input.StartedUnixMS).UTC()
	prior.Native = observed
	if err = db.SaveFactoryRun(ctx, prior); err != nil {
		return err
	}
	// Both the API and the database must retain the exact encoded intent.
	changed := prior
	record := *prior.Native
	changed.Native = &record
	changed.Native.Intent += " "
	if db.SaveFactoryRun(ctx, changed) == nil {
		return errors.New("intent bytes changed through Store")
	}
	rawDB, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	if _, err = rawDB.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{native,intent}',to_jsonb($1::text)) WHERE id=$2`, changed.Native.Intent, prior.ID); err == nil {
		return errors.New("intent bytes changed through SQL")
	}
	fmt.Println("PASS: Go validates/retains the exact launch intent before effects; observation preserves it; Store and raw SQL refuse replacing its bytes")
	return nil
}

// Compare the actual native result with facts recorded by the production Rust
// RPC consumer. This helper performs no observation write through Store.
func checkNativeObservation(profile, instance, path string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	if path != filepath.Join(profile, "native-recovery-fixture.json") {
		return errors.New("observation fixture path differs")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var input struct {
		Run           factory.Run
		StartedUnixMS int64 `json:"started_unix_ms"`
	}
	if json.Unmarshal(raw, &input) != nil || input.Run.Native == nil || input.Run.Native.Provenance.Process == nil {
		return errors.New("observation fixture invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	id, err := os.ReadFile(filepath.Join(profile, "native-recovery-canonical.id"))
	if err != nil {
		return err
	}
	prior, err := db.FactoryRun(ctx, string(id))
	if err != nil {
		return err
	}
	expected := *input.Run.Native
	expected.Intent = prior.Native.Intent
	expected.Retirement = execution.Exited
	expected.Provenance.Process.StartedAt = time.UnixMilli(input.StartedUnixMS).UTC()
	if prior.Reconciled || prior.Outcome != "" || prior.Admission != nil || !reflect.DeepEqual(prior.Native, &expected) {
		return errors.New("native facts lost scope or promoted outcome/placement/retirement")
	}
	rawDB, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	var active, settled bool
	if err = rawDB.QueryRowContext(ctx, "SELECT active,settled FROM factory_runs WHERE id=$1", prior.ID).Scan(&active, &settled); err != nil || !active || settled {
		return errors.New("native observation changed active/settled projections")
	}
	p := expected.Provenance.Process
	receipt := execution.NativeReceiptObservation{WorkerUID: expected.Provenance.Isolation.WorkerUID, Invocation: p.InvocationID, SystemInstance: p.SystemInstance, StartIdentity: p.StartIdentity, PID: p.PID, StartedUnixMS: input.StartedUnixMS, ExitCode: p.ExitCode}
	custody := execution.NativeCustodyObservation{NativeCustody: expected.Provenance.Custody, Sealed: expected.Sealed}
	payload := func(receipt execution.NativeReceiptObservation, custody execution.NativeCustodyObservation, intent string) map[string]any {
		return map[string]any{"intent": intent, "receipt": receipt, "custody": custody}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", config.Socket)
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	post := func(body map[string]any, selected string, mutate func(*http.Request)) (int, error) {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://soda-native/api/native/execution/recovery/observations", strings.NewReader(string(encoded)))
		if err != nil {
			return 0, err
		}
		request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
		request.Header.Set("X-Soda-Native-Instance", selected)
		if mutate != nil {
			mutate(request)
		}
		response, err := client.Do(request)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode, nil
	}
	check := func(body map[string]any, selected string, mutate func(*http.Request), expected int) error {
		status, err := post(body, selected, mutate)
		if err != nil || status != expected {
			return fmt.Errorf("native observation refusal differs: %d want %d, %v", status, expected, err)
		}
		return nil
	}
	correct := payload(receipt, custody, expected.Intent)
	if err = check(correct, instance, nil, 200); err != nil {
		return err
	}
	if err = check(correct, factory.NewID(), nil, 401); err != nil {
		return err
	}
	if err = check(correct, instance, func(r *http.Request) { r.Header.Set("Origin", "http://fixture.invalid") }, 403); err != nil {
		return err
	}
	if err = check(correct, instance, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, 401); err != nil {
		return err
	}
	if err = check(correct, instance, func(r *http.Request) { r.URL.RawQuery = "extra=1" }, 400); err != nil {
		return err
	}
	if err = check(payload(receipt, custody, expected.Intent+" "), instance, nil, 409); err != nil {
		return err
	}
	for _, field := range []string{"root_retired", "outcome", "reconciled"} {
		body := payload(receipt, custody, expected.Intent)
		body[field] = true
		if err = check(body, instance, nil, 400); err != nil {
			return err
		}
	}
	changed := receipt
	changed.StartIdentity = "999999999999"
	if err = check(payload(changed, custody, expected.Intent), instance, nil, 409); err != nil {
		return err
	}
	altered := custody
	altered.AcquisitionID = factory.NewID()
	if err = check(payload(receipt, altered, expected.Intent), instance, nil, 409); err != nil {
		return err
	}
	altered = execution.NativeCustodyObservation{NativeCustody: execution.NativeCustody{State: execution.NotAcquired}, Sealed: true}
	if err = check(payload(receipt, altered, expected.Intent), instance, nil, 409); err != nil {
		return err
	}
	after, err := db.FactoryRun(ctx, prior.ID)
	if err != nil || !reflect.DeepEqual(after, prior) {
		return errors.New("replay or refused observation changed retained facts")
	}
	fmt.Println("PASS: production native observation RPC retains first process/acquisition facts atomically, replay is stable, active/unsettled state remains; stale/browser/changed identity and invented retirement/outcome/no-acquisition refuse")
	return nil
}

func startupRetirementFixture(profile, instance, operation, argument string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rawDB, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	id, err := os.ReadFile(filepath.Join(profile, "native-recovery-canonical.id"))
	if err != nil {
		return err
	}
	if operation == "hold-native-row" {
		tx, err := rawDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var found string
		if err = tx.QueryRowContext(ctx, "SELECT id FROM factory_runs WHERE id=$1 FOR UPDATE", string(id)).Scan(&found); err != nil {
			return err
		}
		if err = os.WriteFile(argument, []byte("locked"), 0600); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	if operation != "check-startup-retirement" {
		return errors.New("unknown startup fixture operation")
	}
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	run, err := db.FactoryRun(ctx, string(id))
	if err != nil {
		return err
	}
	if run.Native == nil || run.Native.Retirement != execution.Retired || !run.Native.Sealed || run.Native.Provenance.Custody.State != execution.Released || run.Native.Provenance.Process == nil || run.Reconciled || run.Outcome != "" || run.Admission != nil {
		return errors.New("startup retirement promoted outcome/placement or lost native proof")
	}
	n := run.Native
	intent, err := os.ReadFile(filepath.Join(profile, "native-run-intents", n.Provenance.BrokerRun+".json"))
	if err != nil || string(intent) != n.Intent {
		return errors.New("startup replaced original intent")
	}
	file, err := os.Open(filepath.Join(profile, "native-run-receipts", n.Provenance.BrokerRun+".receipt"))
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, err = io.ReadFull(file, header); err != nil {
		return err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16 | int(header[3])<<24
	if length < 1 || length > 2048 {
		return errors.New("receipt header fixture bound")
	}
	header = make([]byte, length)
	if _, err = io.ReadFull(file, header); err != nil {
		return err
	}
	var observed struct {
		Invocation    string
		WorkerUID     int    `json:"worker_uid"`
		StartIdentity string `json:"start_identity"`
	}
	if json.Unmarshal(header, &observed) != nil || observed.Invocation != n.Provenance.Process.InvocationID || observed.WorkerUID != n.Provenance.Isolation.WorkerUID || observed.StartIdentity != n.Provenance.Process.StartIdentity {
		return errors.New("startup process differs from original receipt")
	}
	var active, settled bool
	if err = rawDB.QueryRowContext(ctx, "SELECT active,settled FROM factory_runs WHERE id=$1", run.ID).Scan(&active, &settled); err != nil || !active || settled {
		return errors.New("startup settled the active run")
	}
	items, _, err := db.NativeRecoveryCandidates(ctx, config.Profile, 0)
	if err != nil || len(items) != 0 {
		return errors.New("root-retirement acknowledgement remains pending")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", config.Socket)
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request := func(method, path, selected string, body map[string]any, mutate func(*http.Request), want int) error {
		var payload io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				return err
			}
			payload = strings.NewReader(string(encoded))
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://soda-native"+path, payload)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
		req.Header.Set("X-Soda-Native-Instance", selected)
		if mutate != nil {
			mutate(req)
		}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode != want {
			return fmt.Errorf("native retirement fixture %s: status %d want %d", path, response.StatusCode, want)
		}
		return nil
	}
	retired := "/api/native/execution/recovery/retired"
	payload := func(intent, invocation, acquisition string) map[string]any {
		return map[string]any{"intent": intent, "invocation": invocation, "acquisition_id": acquisition}
	}
	correct := payload(n.Intent, n.Provenance.Process.InvocationID, n.Provenance.Custody.AcquisitionID)
	for _, check := range []struct {
		selected string
		body     map[string]any
		want     int
	}{
		{instance, correct, 200},
		{factory.NewID(), correct, 401},
		{instance, payload(n.Intent+" ", n.Provenance.Process.InvocationID, n.Provenance.Custody.AcquisitionID), 409},
		{instance, payload(n.Intent, "different-invocation", n.Provenance.Custody.AcquisitionID), 409},
		{instance, payload(n.Intent, n.Provenance.Process.InvocationID, factory.NewID()), 409},
	} {
		if err = request("POST", retired, check.selected, check.body, nil, check.want); err != nil {
			return err
		}
	}
	for _, field := range []string{"outcome", "reconciled"} {
		body := payload(n.Intent, n.Provenance.Process.InvocationID, n.Provenance.Custody.AcquisitionID)
		body[field] = true
		if err = request("POST", retired, instance, body, nil, 400); err != nil {
			return err
		}
	}
	if err = request("POST", retired, instance, correct, func(r *http.Request) { r.Header.Set("Origin", "http://fixture.invalid") }, 403); err != nil {
		return err
	}
	if err = request("POST", retired, instance, correct, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, 401); err != nil {
		return err
	}
	for _, cursor := range []string{"0", "01", "-1", "9223372036854775808", "0&extra=1", "0&cursor=0"} {
		want := 400
		if cursor == "0" {
			want = 200
		}
		if err = request("GET", "/api/native/execution/recovery/pending?cursor="+cursor, instance, nil, nil, want); err != nil {
			return err
		}
	}
	after, err := db.FactoryRun(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(after, run) {
		return errors.New("native retirement replay/refusal changed retained row")
	}
	fmt.Println("PASS: actual restart keeps original process/intent/acquisition, records only root resource retirement, drains pending acknowledgement and leaves outcome/active/unplaced state unchanged")
	fmt.Println("PASS: root-retirement replay is stable; stale/browser/wrong-key, changed intent/process/acquisition, invented outcome and invalid inventory cursors refuse")
	return nil
}

func ownedExecutionFixture(profile, instance, mode string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	marker := "native-recovery-canonical.id"
	if mode == "export" {
		marker = "native-candidate-canonical.id"
	}
	id, err := os.ReadFile(filepath.Join(profile, marker))
	if err != nil {
		return err
	}
	run, err := db.FactoryRun(ctx, string(id))
	if err != nil {
		return err
	}
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	address := execution.RunAddress{Run: run.ID, Workspace: run.ProjectID}
	if mode == "coordinator" {
		competitor := control.NewCoordinator(db, port, port)
		defer competitor.Close()
		bounded, stop := context.WithTimeout(ctx, 150*time.Millisecond)
		defer stop()
		if err := competitor.Start(bounded, filepath.Join(profile, "coordinator.lock")); err == nil {
			return errors.New("second coordinator acquired the native ledger")
		}
		if !run.Reconciled || run.Native == nil || !run.Native.Settled() {
			return errors.New("native coordinator startup did not settle the actual retired run")
		}
		fmt.Println("PASS: explicit native coordinator startup settled the actual local run and excludes a competing controller")
		return nil
	}
	if mode == "recover" || mode == "recover_replay" || mode == "recover_started" || mode == "recover_stale" {
		targetID, readErr := os.ReadFile(filepath.Join(profile, "native-recovery-target.id"))
		if readErr != nil {
			return readErr
		}
		provenance := run.Native.Provenance
		request := execution.WorkRecoveryRequest{Address: address, Native: &execution.NativeRecovery{
			Source: execution.NativeWorkspaceRef{Profile: provenance.Profile, InstanceID: provenance.InstanceID, WorktreeID: provenance.WorktreeID},
			Target: execution.NativeCheckoutRef{Profile: config.Profile, CheckoutID: string(targetID)},
		}}
		if mode == "recover_stale" {
			if _, err = port.RecoverWork(ctx, request); err == nil {
				return errors.New("stale native recovery destination admitted a cached receipt")
			}
			fmt.Println("PASS: stale registered native destination refused despite an existing recovery receipt")
			return nil
		}
		if mode == "recover" || mode == "recover_started" {
			coordinator := control.NewCoordinator(db, port, port)
			defer coordinator.Close()
			if mode == "recover" {
				if _, err = port.RecoverWork(ctx, request); !errors.Is(err, execution.ErrRecovery) {
					return errors.New("native work copied before reconciliation")
				}
				reconciled, reconcileErr := coordinator.Reconcile(ctx)
				if reconcileErr != nil || !slices.Contains(reconciled.Settled, run.ID) {
					return fmt.Errorf("local native reconciliation: %+v %v", reconciled, reconcileErr)
				}
			} else if !run.Reconciled || !run.Native.Settled() {
				return errors.New("native coordinator has not reconciled the recovery source")
			}
			copied, copyErr := coordinator.RecoverRunWork(ctx, run.ID, request.Target())
			if copyErr != nil {
				return copyErr
			}
			replayed, copyErr := coordinator.RecoverRunWork(ctx, run.ID, request.Target())
			if copyErr != nil || !reflect.DeepEqual(copied, replayed) {
				return errors.New("canonical native work receipt changed on replay")
			}
		}
		receipt, copyErr := port.RecoverWork(ctx, request)
		if copyErr != nil {
			return copyErr
		}
		if !receipt.Reused || receipt.ValidateFor(request) != nil {
			return errors.New("native work retry did not reuse the installed checkout")
		}
		fmt.Println("PASS: canonical local native reconciliation and work recovery; settled-source guard, durable receipt replay and native reuse without recopy")
		return nil
	}
	if mode == "launch_refused" {
		intent, parseErr := execution.ParseNativeIntent(run.Native.Intent)
		if parseErr != nil {
			return parseErr
		}
		var fixture struct{ Prompt string }
		raw, readErr := os.ReadFile(filepath.Join(profile, "native-recovery-fixture.json"))
		if readErr != nil {
			return readErr
		}
		if json.Unmarshal(raw, &fixture) != nil {
			return errors.New("owned launch fixture missing")
		}
		request := execution.LaunchRequest{Kind: collaboration.Native, Native: &intent.Native, Input: project.FactoryLaunch{
			Run: project.FactoryRun{ID: run.ID, Project: run.ProjectID, Role: run.Role, Preparation: run.Native.Provenance.WorktreeID, Harness: run.Native.Tool.Harness, HarnessVers: run.Native.Tool.Version, Model: intent.Model, Effort: intent.Effort, Contributor: intent.Contributor,
				Assignment: run.Native.PromptSHA256, SourceCommit: run.InputSHA, Connection: intent.Connection, Actor: 1, Deadline: run.Deadline, ExecutionPhase: project.ExecutionReview},
			Prompt: []byte(fixture.Prompt), HarnessSHA256: run.Native.Tool.SHA256, MCP: &project.FactoryMCPBinding{Capability: run.ID, Secret: strings.Repeat("a", 64)},
		}}
		if err = request.Validate(); err != nil {
			return err
		}
		if _, err = port.LaunchRun(ctx, request); err == nil {
			return errors.New("Go launched an unadmitted native fixture")
		}
		// A shape-valid immutable manifest must not be silently discarded by
		// the plain CLI adapter. Task policy/plan authority is not declared here.
		plan := []byte(`{"fixture":"unexecuted"}`)
		manifest, err := project.MarshalExecutionManifest(project.ExecutionManifest{Plan: plan, PlanDigest: project.PlanDigest(plan),
			Invocation: factory.NewID(), Phase: project.ExecutionImplement, Run: run.ID, Deadline: run.Deadline, Effort: intent.Effort, MaxChildren: 1})
		if err != nil {
			return err
		}
		request.Input.Run.Role, request.Input.Run.ExecutionPhase = project.RoleCoder, project.ExecutionImplement
		request.Input.ExecutionManifest = manifest
		request.Input.Run.ManifestDigest = project.ExecutionManifestDigest(manifest)
		if err := request.Validate(); err != nil {
			return err
		}
		if _, err := port.LaunchRun(ctx, request); !errors.Is(err, execution.ErrTaskUnavailable) {
			return errors.New("native plain CLI accepted or silently discarded a task manifest")
		}
		if err := writeTaskManifestBindings(profile, intent); err != nil {
			return err
		}
		fmt.Println("PASS: native task-manifest launch remains explicitly unavailable before native effects")
		fmt.Println("PASS: Go native launch refuses unadmitted work before native effects")
		return nil
	}
	if mode == "export" {
		var expected struct {
			Candidate string `json:"candidate"`
			Bundle    []byte `json:"bundle"`
		}
		raw, readErr := os.ReadFile(filepath.Join(profile, "native-candidate-fixture.json"))
		if readErr != nil {
			return readErr
		}
		if json.Unmarshal(raw, &expected) != nil || len(expected.Bundle) == 0 {
			return errors.New("native candidate fixture missing")
		}
		request := execution.CandidateRequest{Address: address, Role: run.Role, Preparation: run.Native.Provenance.WorktreeID, Candidate: expected.Candidate}
		exported, exportErr := port.ExportCandidate(ctx, request)
		if exportErr != nil {
			return exportErr
		}
		if exported.Address != address || exported.Candidate != expected.Candidate || !slices.Equal(exported.Bundle, expected.Bundle) {
			return errors.New("native export differs from actual worker bundle")
		}
		request.Candidate = run.InputSHA
		if _, err = port.ExportCandidate(ctx, request); !errors.Is(err, execution.ErrCandidateInvalid) {
			return errors.New("native export accepted a different candidate")
		}
		request.Candidate = expected.Candidate
		request.Preparation = "f" + strings.Repeat("9", 24)
		if _, err = port.ExportCandidate(ctx, request); err == nil {
			return errors.New("native export accepted a different preparation")
		}
		fmt.Println("PASS: Go exports exact isolated coder commit/bundle; different candidate and preparation refuse")
		return nil
	}
	if mode == "output-live" || mode == "output-terminal" {
		// The product attachment over the native output source: the same
		// service validation Watch frames pass, with controlled scope/access.
		source := factoryservice.RunOutput{Store: db,
			Scope: func(context.Context, collaboration.ActorID, factory.Run) (collaboration.RepositoryID, error) {
				return 1, nil
			},
			Access: func(context.Context, collaboration.ActorID, collaboration.RepositoryID, factory.Run) error {
				return nil
			},
			Source: func(ctx context.Context, _ collaboration.ActorID, r factory.Run, cursor int64, _ int) (project.FactoryOutputState, error) {
				return port.ReadOutput(ctx, r, cursor)
			}}
		attachment, openErr := source.Open(ctx, 1, run.ID)
		if openErr != nil {
			return openErr
		}
		var cursor, reads int64
		var last *factoryservice.RunOutputSlice
		for reads < 64 {
			slice, readErr := attachment.Read(ctx, cursor)
			if readErr != nil {
				return readErr
			}
			reads++
			last = slice
			if slice.Next == cursor {
				break
			}
			cursor = slice.Next
			if slice.Done {
				break
			}
		}
		live := mode == "output-live"
		if last == nil || cursor == 0 || last.Status.Live != live || last.Status.Terminal == live || last.Done == live ||
			live && last.Status.Phase != project.FactoryRunning || !live && last.Status.Phase != project.FactoryCompleted && last.Status.Phase != project.FactoryStopped {
			return fmt.Errorf("native output read differs: %+v after %d bytes", last, cursor)
		}
		fmt.Println("PASS: native Go output source", mode, "read", cursor, "bytes in", reads, "bounded reads through the product attachment")
		return nil
	}
	var observed execution.RunObservation
	if mode == "cancel" {
		observed, err = port.RetireRun(ctx, address)
	} else {
		observed, err = port.InspectRun(ctx, address)
	}
	if err != nil {
		return err
	}
	latest, err := db.FactoryRun(ctx, run.ID)
	if err != nil || latest.Native == nil || latest.Native.Provenance.Process == nil || latest.Reconciled || latest.Outcome != "" || latest.Native.Intent != run.Native.Intent {
		return errors.New("native callback changed outcome or lost process/intent")
	}
	if mode == "running" && (!observed.Live || observed.Retirement != execution.Running || observed.Phase != project.FactoryRunning || observed.Native.Custody.State != execution.Held) {
		return errors.New("native callback did not observe the live owned process")
	}
	if mode == "complete" && (observed.Live || observed.Retirement != execution.Retired || observed.Phase != project.FactoryCompleted || !observed.OutputComplete || !latest.Native.Settled()) {
		return errors.New("native callback did not retain exact terminal/custody/root facts")
	}
	if mode == "uncertain" && (observed.Phase != project.FactoryUncertain || observed.Retirement == execution.Retired || observed.OutputComplete) {
		return errors.New("cancelled native callback invented a complete result")
	}
	if mode == "stopped" && (observed.Live || observed.Retirement != execution.Retired || observed.Phase != project.FactoryStopped || observed.Reason != "cancelled" || !observed.OutputComplete || !latest.Native.Settled()) {
		return errors.New("native cancellation lost its stopped process/custody/retirement receipt")
	}
	if mode == "running" {
		stale := port
		stale.Instance = factory.NewID()
		if _, err := stale.InspectRun(ctx, address); !errors.Is(err, execution.ErrRunNotObserved) {
			return errors.New("stale native owner selected a process")
		}
		changed := address
		changed.Workspace = "p" + strings.Repeat("9", 24)
		if _, err := port.InspectRun(ctx, changed); !errors.Is(err, execution.ErrRunChanged) {
			return errors.New("wrong workspace selected a process")
		}
	}
	fmt.Println("PASS: native Go callback", mode, "retains exact process/custody facts without product settlement")
	return nil
}

func main() {
	if len(os.Args) == 4 && (os.Args[3] == "mcp-rotation-seed" || os.Args[3] == "mcp-rotation-check" || os.Args[3] == "mcp-rotation-replay") {
		if err := checkMCPRotation(os.Args[1], os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, "MCP rotation:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "check-builder-retirement" {
		if err := builderRetirementFixture(); err != nil {
			fmt.Fprintln(os.Stderr, "builder retirement:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "check-work-readback" {
		if err := workReadbackFixture(); err != nil {
			fmt.Fprintln(os.Stderr, "work readback:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "check-repository-priority" {
		if err := repositoryPriorityFixture(); err != nil {
			fmt.Fprintln(os.Stderr, "repository priority:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "offline-role-handoffs" {
		if err := offlineRoleHandoffs(); err != nil {
			fmt.Fprintln(os.Stderr, "offline role handoffs:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 6 && os.Args[1] == "retained-preparation-stop" {
		if err := retainedPreparationStop(os.Args[2], os.Args[3], os.Args[4], os.Args[5]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "retained-root-retry" {
		if err := retainedRootRetry(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "retired-plan-review" {
		if err := retiredPlanReview(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "progress-gate" {
		if err := progressGateFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "external-preparation" {
		if err := externalPreparationFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 6 && os.Args[1] == "workspace-manifest" {
		if err := workspaceManifest(os.Args[2], os.Args[3], os.Args[4], os.Args[5]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "workspace-events" {
		if err := workspaceEvents(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "task-bindings" {
		raw, err := os.ReadFile(os.Args[2])
		if err == nil {
			var intent execution.NativeIntent
			intent, err = execution.ParseNativeIntent(string(raw))
			if err == nil {
				err = writeTaskManifestBindings(os.Args[3], intent)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) == 2 && os.Args[1] == "check-initial-intake" {
		if err := initialIntakeFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		if err := workerProfileFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 5 {
		var err error
		if os.Args[3] == "check-review-policy" {
			err = nativeReviewPolicyFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if strings.HasSuffix(os.Args[3], "onboarded-setup") {
			expectedSources := 14
			if os.Args[3] == "check-ui-onboarded-setup" {
				expectedSources = 2
			} else if os.Args[3] == "check-ui64-onboarded-setup" {
				expectedSources = 64
			} else if os.Args[3] == "check-refused-onboarded-setup" {
				expectedSources = 0
			} else if os.Args[3] == "check-legacy-onboarded-setup" {
				expectedSources = -1
			}
			err = onboardedSetupFixture(os.Args[1], os.Args[2], os.Args[4], strings.HasPrefix(os.Args[3], "retire-"), expectedSources)
		} else if strings.HasPrefix(os.Args[3], "refine-native-request") {
			err = nativeRequestRefinementFixture(os.Args[1], os.Args[2], os.Args[4], os.Args[3])
		} else if strings.HasPrefix(os.Args[3], "native-graph-") {
			err = nativeGraphFixture(os.Args[1], os.Args[2], os.Args[4], os.Args[3])
		} else if os.Args[3] == "declare-request-route" {
			err = declaredRequestRoute(os.Args[1], os.Args[4])
		} else if strings.HasPrefix(os.Args[3], "check-background-") {
			err = backgroundInputsFixture(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
		} else if os.Args[3] == "check-owned-tool" {
			err = ownedToolFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-native-schema" {
			err = nativeSchemaFixture(os.Args[1], os.Args[4])
		} else if os.Args[3] == "seed-work-detail" {
			err = seedWorkDetail(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-native-preparation" {
			err = nativePreparationFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-owned-execution" {
			err = ownedExecutionFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-native-sponsorship" || os.Args[3] == "withdraw-native-sponsorship" {
			err = nativeSponsorshipFixture(os.Args[1], os.Args[2], os.Args[4], os.Args[3] == "check-native-sponsorship")
		} else if os.Args[3] == "launch-admitted-native" || os.Args[3] == "launch-admitted-tasks" || os.Args[3] == "check-admitted-native" || os.Args[3] == "settled-admitted-native" || os.Args[3] == "observe-admitted-native" || os.Args[3] == "acknowledge-native" || os.Args[3] == "reconcile-retained-native" || os.Args[3] == "withdraw-admitted-native" || os.Args[3] == "check-retained-native-fenced" {
			err = admittedNativeFixture(os.Args[1], os.Args[2], os.Args[4], os.Args[3])
		} else if os.Args[3] == "hold-native-row" || os.Args[3] == "check-startup-retirement" {
			err = startupRetirementFixture(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
		} else if os.Args[3] == "check-native-observation" {
			err = checkNativeObservation(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "create-native-intent" || os.Args[3] == "finish-native-intent" {
			err = nativeIntentFixture(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
		} else {
			err = nativeReceiptFixture(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "profile and instance required")
		os.Exit(2)
	}
	if err := check(os.Args[1], os.Args[2], len(os.Args) == 4 && os.Args[3] == "retired"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// This scenario exercises production role-result contracts and durable terminal
// settlement using controlled admission and settlement observations.
// It does not launch a worker, execute a check, contact a provider, publish a
// candidate or qualify native custody. Only its new native-PG driver profile is
// used. The driver's write/restart/recover phases retain the exact proposals.
type offlineRoleEntry struct {
	Assignment factory.Assignment    `json:"assignment"`
	Submission factory.MCPSubmission `json:"submission"`
	Outcome    factory.Outcome       `json:"outcome"`
	Candidate  string                `json:"candidate,omitempty"`
}

type offlineRoleState struct {
	Version string             `json:"version"`
	Entries []offlineRoleEntry `json:"entries"`
}

type offlineRoleFixture struct {
	db    *store.Store
	sql   *sql.DB
	state offlineRoleState
	path  string
}

// The current-context observation is controlled, while assignment/run and
// terminal persistence below use the actual disposable PostgreSQL store.
type offlineRoleCurrent struct{ db *store.Store }

func (s offlineRoleCurrent) FactoryCurrentContext(ctx context.Context, cap factory.MCPCapability, _ time.Time) (factory.CurrentContext, error) {
	a, err := s.db.Assignment(ctx, cap.AssignmentID)
	return factory.CurrentContext{Role: factory.CurrentRoleContext{Kind: cap.Profile, Review: a.ReviewContext}}, err
}

func (f *offlineRoleFixture) results() factoryservice.WorkerResults {
	return factoryservice.WorkerResults{Assignments: f.db, Runs: f.db, Current: offlineRoleCurrent{f.db}}
}

func offlineRoleAssignment(phase, role, source string) factory.Assignment {
	id, run := factory.NewID(), factory.NewID()
	prompt := []byte("Controlled offline role contract observation; no worker or provider execution.")
	return factory.Assignment{ID: id, AttemptRoot: id, PublicationAssignment: id,
		Subject:   factory.ExecutionSubject{Kind: factory.SubjectIssue, Issue: &factory.IssueSubject{Issue: 1, Acceptance: "d" + strings.Repeat("a", 24)}},
		ProjectID: "p" + strings.Repeat("d", 24), Repository: 714, Issue: 1, NativeRev: 1, ActorID: 7,
		Acceptance: "d" + strings.Repeat("a", 24), Role: role, Phase: phase, Preparation: "f" + strings.Repeat("b", 24),
		Harness: "controlled-offline-1", HarnessVers: "controlled-offline-1", Model: "controlled-no-model", Connection: strings.Repeat("c", 32),
		SourceCommit: source, Prompt: prompt, PromptSHA: factory.MCPInputDigest(prompt), Run: run, RunHistory: []string{run},
		Stage: factory.AssignmentAssigned, Attempts: 1, CreatedUnix: time.Now().Unix()}
}

func (f *offlineRoleFixture) admit(ctx context.Context, a factory.Assignment, profile factory.MCPProfile, plans ...factory.InvestigationPlan) (factory.MCPCapability, error) {
	var cap factory.MCPCapability
	if err := a.Validate(); err != nil {
		return cap, fmt.Errorf("controlled %s input: %w", profile, err)
	}
	raw, _ := json.Marshal(a)
	if _, err := f.sql.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
		return cap, fmt.Errorf("%s input assignment: %w", profile, err)
	}
	run := factory.Run{ID: a.Run, ProjectID: a.ProjectID, Role: a.Role, InputSHA: a.SourceCommit, Started: time.Now().UTC(), Deadline: time.Now().UTC().Add(15 * time.Minute), Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model}
	if err := f.db.RecordFactoryRun(ctx, run); err != nil {
		return cap, fmt.Errorf("%s controlled run admission: %w", profile, err)
	}
	if _, err := f.sql.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4)`, run.ID, a.Repository, a.Issue, a.ID); err != nil {
		return cap, fmt.Errorf("%s controlled run view: %w", profile, err)
	}
	if len(plans) > 0 {
		plan := plans[0]
		plan.AttemptRoot, plan.SourceCommit, plan.IntegrationCommit, plan.Preparation = a.AttemptRoot, a.SourceCommit, a.SourceCommit, a.Preparation
		var err error
		plan.Digest, err = factory.PlanDigest(plan)
		if err != nil {
			return cap, err
		}
		if err = plan.Executable(); err != nil {
			return cap, fmt.Errorf("%s controlled plan: %w", profile, err)
		}
		planRaw, _ := json.Marshal(plan)
		manifest, err := project.MarshalExecutionManifest(project.ExecutionManifest{Plan: planRaw, PlanDigest: project.PlanDigest(planRaw), Invocation: "controlled-offline", Phase: a.Phase, Run: a.Run, Deadline: run.Deadline, Effort: project.TaskEffortHigh, MaxChildren: 0})
		if err != nil {
			return cap, err
		}
		launch := project.FactoryLaunch{Run: project.FactoryRun{ID: a.Run, Project: a.ProjectID, Role: a.Role, Preparation: a.Preparation, Harness: project.FactoryHarnessMuse, HarnessVers: a.HarnessVers, Model: a.Model, Assignment: a.PromptSHA, SourceCommit: a.SourceCommit, Connection: a.Connection, Actor: a.ActorID, Deadline: run.Deadline, ExecutionPhase: a.Phase, ManifestDigest: project.ExecutionManifestDigest(manifest)}, Prompt: a.Prompt, HarnessSHA256: strings.Repeat("b", 64), ExecutionManifest: manifest}
		if err = f.db.BindFactoryExecutionManifest(ctx, launch); err != nil {
			return cap, fmt.Errorf("%s exact controlled manifest admission: %w", profile, err)
		}
	}
	cap = factory.MCPCapability{ID: a.Run, RunID: a.Run, AssignmentID: a.ID, Generation: 1, Profile: profile, Subject: a.Subject.SubjectKey(a.Repository),
		Repository: a.Repository, Project: a.ProjectID, PreparationDigest: strings.Repeat("a", 64), SourceDigest: a.SourceCommit, AcceptedRevision: "1", Authority: strings.Repeat("d", 64), DeadlineUnix: run.Deadline.Unix(), State: factory.MCPCapabilityStaged}
	if err := f.db.StageMCPCapability(ctx, cap); err != nil {
		return cap, fmt.Errorf("%s stage controlled capability: %w", profile, err)
	}
	if err := f.db.ActivateMCPCapability(ctx, cap.ID, cap.Generation, cap.RunID, cap.AssignmentID, cap.Authority, time.Now().Unix(), false); err != nil {
		return cap, fmt.Errorf("%s activate controlled capability: %w", profile, err)
	}
	cap.State = factory.MCPCapabilityActive
	return cap, nil
}

func (f *offlineRoleFixture) proposal(ctx context.Context, cap factory.MCPCapability, tool factory.MCPTool, payload map[string]any) (factory.MCPSubmission, error) {
	sub := factory.MCPSubmission{AssignmentID: cap.AssignmentID, CapabilityID: cap.ID, SubmissionID: factory.NewID(), Tool: tool}
	if err := control.AdmitMCPCapability(cap, tool, time.Now(), control.MCPRunObservation{RunID: cap.RunID, AssignmentID: cap.AssignmentID, Authority: cap.Authority}); err != nil {
		return sub, err
	}
	results := f.results()
	results.Bind(ctx, cap, tool, payload)
	if err := mcpwire.CheckPayload(tool, payload); err != nil {
		return sub, err
	}
	var err error
	sub.Payload, err = mcpwire.CanonicalPayload(payload)
	if err != nil {
		return sub, err
	}
	if err = results.CheckNewFinish(ctx, cap, sub, payload); err != nil {
		return sub, err
	}
	sub.Payload, err = mcpwire.CanonicalPayload(payload)
	return sub, err
}

func (f *offlineRoleFixture) post(ctx context.Context, a factory.Assignment, cap factory.MCPCapability, tool factory.MCPTool, bad, good map[string]any, candidate string) error {
	invalid, invalidErr := f.proposal(ctx, cap, tool, bad)
	if invalidErr == nil {
		return fmt.Errorf("%s malformed typed result admitted", cap.Profile)
	}
	if _, err := f.db.MCPWorkStatus(ctx, a.ID, invalid.SubmissionID); !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%s refused identity recorded: %w", cap.Profile, err)
	}
	if _, found, err := f.db.TerminalMCPProposal(ctx, a.ID); err != nil || found {
		return fmt.Errorf("%s refusal froze terminal: %w", cap.Profile, err)
	}
	other := factory.MCPToolFinishReview
	if tool == other {
		other = factory.MCPToolFinishImplement
	}
	if err := control.AdmitMCPCapability(cap, other, time.Now(), control.MCPRunObservation{RunID: cap.RunID, AssignmentID: cap.AssignmentID, Authority: cap.Authority}); !errors.Is(err, control.ErrMCPCapabilityBinding) {
		return fmt.Errorf("foreign role finish did not fail profile admission: %w", err)
	}
	sub, err := f.proposal(ctx, cap, tool, good)
	if err != nil {
		return fmt.Errorf("%s corrected result: %w", cap.Profile, err)
	}
	if sub.SubmissionID == invalid.SubmissionID {
		return errors.New("corrected result reused rejected identity")
	}
	if tool == factory.MCPToolFinishIssuePlan {
		var changed map[string]any
		if err := json.Unmarshal(sub.Payload, &changed); err != nil {
			return err
		}
		changed["plan_review"] = strings.Repeat("0", 64)
		unbound := sub
		unbound.SubmissionID = factory.NewID()
		unbound.Payload, _ = json.Marshal(changed)
		if _, err := f.db.RecordMCPSubmission(ctx, unbound); !errors.Is(err, store.ErrAdmissionChanged) {
			return fmt.Errorf("unbound selected-source opinion did not refuse recording: %w", err)
		}
	}
	first, err := f.db.RecordMCPSubmission(ctx, sub)
	if err != nil {
		return fmt.Errorf("%s record result: %w", cap.Profile, err)
	}
	replay, err := f.db.RecordMCPSubmission(ctx, sub)
	if err != nil || !reflect.DeepEqual(first, replay) {
		return errors.New("exact terminal replay changed its receipt")
	}
	changed := sub
	changed.Payload = append([]byte(nil), sub.Payload...)
	changed.Payload = append(changed.Payload, ' ')
	if _, err = f.db.RecordMCPSubmission(ctx, changed); !errors.Is(err, store.ErrMCPSubmissionConflict) {
		return fmt.Errorf("changed immutable terminal did not conflict: %w", err)
	}
	f.state.Entries = append(f.state.Entries, offlineRoleEntry{Assignment: a, Submission: sub, Outcome: factory.Succeeded, Candidate: candidate})
	return f.save()
}

func (f *offlineRoleFixture) save() error {
	raw, err := json.MarshalIndent(f.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, raw, 0600)
}

func (f *offlineRoleFixture) settle(ctx context.Context, index int) (factory.Assignment, error) {
	e := f.state.Entries[index]
	stored, found, err := f.db.TerminalMCPSubmission(ctx, e.Assignment.ID)
	if err != nil || !found || !reflect.DeepEqual(stored, e.Submission) {
		return factory.Assignment{}, errors.New("retained terminal differs from original manifest")
	}
	prior, err := f.db.Assignment(ctx, e.Assignment.ID)
	if err != nil {
		return prior, err
	}
	priorRaw, _ := json.Marshal(prior)
	if _, err = control.SettleMCPTerminalProposal(ctx, f.db, e.Assignment.ID, time.Now()); err != nil {
		return factory.Assignment{}, err
	}
	a, err := f.db.Assignment(ctx, e.Assignment.ID)
	if err != nil {
		return a, err
	}
	if prior.Stage == factory.AssignmentFinished {
		currentRaw, _ := json.Marshal(a)
		if !bytes.Equal(priorRaw, currentRaw) {
			return a, errors.New("restart settlement rewrote an already-finished assignment")
		}
	}
	if a.Stage != factory.AssignmentFinished || a.Outcome != e.Outcome || a.Result == nil || !a.Result.Reported || a.Result.Candidate != e.Candidate {
		return a, fmt.Errorf("%s typed settlement: outcome=%s reason=%s", a.Phase, a.Outcome, a.Reason)
	}
	before, _ := json.Marshal(a)
	if changed, err := control.SettleMCPTerminalProposal(ctx, f.db, a.ID, time.Now()); err != nil || changed {
		return a, errors.New("settled terminal was consumed twice")
	}
	status, err := f.db.RecordMCPSubmission(ctx, e.Submission)
	if err != nil || status.State != factory.MCPWorkAccepted {
		return a, fmt.Errorf("settled exact replay unavailable: %w", err)
	}
	after, err := f.db.Assignment(ctx, a.ID)
	afterRaw, _ := json.Marshal(after)
	if err != nil || !bytes.Equal(before, afterRaw) {
		return a, errors.New("terminal replay rewrote assignment")
	}
	return a, nil
}

func (f *offlineRoleFixture) timeOnlyAllowance(ctx context.Context, a factory.Assignment) error {
	allowance := factory.AttemptAllowance{RootAssignment: a.ID, Repository: a.Repository, CheckpointUnix: time.Now().Unix(), Limits: factory.AttemptLimits{ActiveMinutes: 5}}
	switch a.Subject.Kind {
	case factory.SubjectIntent:
		allowance.Intent = &factory.IntentAttemptScope{IntentID: a.Subject.Intent.IntentID, OwnerID: a.Subject.Intent.OwnerID}
	case factory.SubjectCompletion:
		allowance.Completion = a.Subject.Completion
	case factory.SubjectDelivery:
		allowance.Delivery = a.Subject.Delivery
	}
	if err := allowance.Validate(); err != nil {
		return err
	}
	raw, _ := json.Marshal(allowance)
	_, err := f.sql.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,0,$2,0,$3)`, a.Repository, a.ID, string(raw))
	return err
}

// These independent profiles use the same submission/settlement joint. Product
// walkthroughs, application receipts and completion inventory are declared
// inputs here, not evidence of model authorship, a checked app or graph adoption.
func (f *offlineRoleFixture) remainingProfiles(ctx context.Context) error {
	decision := func() string { return "d" + factory.NewID()[:24] }
	for _, profile := range []factory.MCPProfile{factory.MCPProfileIssueMaker, factory.MCPProfileProductLead} {
		a := offlineRoleAssignment(project.ExecutionDecompose, project.RoleCoder, strings.Repeat("1", 40))
		a.Issue, a.Acceptance, a.PublicationAssignment = 0, "", ""
		idea := factory.ProductIdea{ID: decision(), Description: "Record a personal request and read its result", Consequence: "The person sees the same request", Decision: "accept", Source: "requested", Provenance: "human", Actor: a.ActorID}
		head := factory.IntentRevision{ID: decision(), Intent: decision(), Owner: a.ActorID, Repository: a.Repository, Objective: "One person records a personal request and sees its recorded result directly.", State: "draft", MaxIssues: 1, CreatedUnix: time.Now().Unix(), Ideas: []factory.ProductIdea{idea}, EffectiveOutcomes: []string{idea.ID}}
		var maker *factory.IssueMakerScope
		if profile == factory.MCPProfileIssueMaker {
			post := factory.NewID()
			head.Intent, head.ID = factory.PostDecisionID("intent", post, 0), factory.PostDecisionID("revision", post, 0)
			maker = &factory.IssueMakerScope{SourcePost: post, Post: factory.IssueMakerPostID(post, 0), Issue: 81}
		}
		content, err := factory.ComposeProposalContent(head)
		if err != nil {
			return err
		}
		input := factory.SemanticPlannerInput{IssueMaker: maker, Operation: factory.IntentPlanningOptions, Head: head, Content: content, Alternatives: factory.DescribeAlternatives(head.Objective), Inspected: []factory.InspectedSource{{Path: "README.md", Commit: a.SourceCommit, Evidence: "controlled immutable source"}}, RequireWalkthroughs: profile == factory.MCPProfileProductLead}
		a.Subject = factory.ExecutionSubject{Kind: factory.SubjectIntent, Intent: &factory.IntentSubject{IntentID: head.Intent, RevisionID: head.ID, ScopeDigest: head.Digest(), OwnerID: head.Owner}}
		a.PlanningContext = &input
		a.Prompt, err = control.StageIntentPlanningPrompt(head, input)
		if err != nil {
			return err
		}
		a.PromptSHA = factory.MCPInputDigest(a.Prompt)
		cap, err := f.admit(ctx, a, profile)
		if err != nil {
			return err
		}
		if err = f.timeOnlyAllowance(ctx, a); err != nil {
			return err
		}
		good := map[string]any{"candidates": []factory.SemanticCandidate{{Key: "controlled", Rationale: "One controlled outcome", Tasks: []factory.IntentTask{{Key: decision(), Objective: head.Objective, Outcomes: []string{head.Objective}, IdeaIDs: []string{idea.ID}}}}}, "coverage": []string{idea.ID}, "evidence_refs": []string{"controlled:README.md"}}
		if profile == factory.MCPProfileProductLead {
			walkthrough := content
			walkthrough.Producer = factory.PlanningProducer
			for i := range walkthrough.Alternatives {
				walkthrough.Alternatives[i].IdeaIDs = []string{idea.ID}
			}
			good["walkthroughs"] = walkthrough
		}
		if err = f.post(ctx, a, cap, factory.MCPToolFinishProductPlan, map[string]any{"candidates": []string{"invalid"}, "coverage": []string{idea.ID}, "evidence_refs": []string{"controlled"}}, good, ""); err != nil {
			return err
		}
		if _, err = f.settle(ctx, len(f.state.Entries)-1); err != nil {
			return err
		}
	}
	strategist := offlineRoleAssignment(project.ExecutionReassess, project.RoleCoder, strings.Repeat("3", 40))
	strategist.Issue, strategist.Acceptance, strategist.PublicationAssignment = 0, "", ""
	subject := factory.CompletionSubject{CompletionID: factory.NewID(), CycleID: factory.NewID(), InventoryDigest: strings.Repeat("a", 64)}
	strategist.Subject = factory.ExecutionSubject{Kind: factory.SubjectCompletion, Completion: &subject}
	cap, err := f.admit(ctx, strategist, factory.MCPProfileStrategist)
	if err != nil {
		return err
	}
	if err = f.timeOnlyAllowance(ctx, strategist); err != nil {
		return err
	}
	reassessment := factory.ReassessmentReport{Summary: "Controlled no-change diagnosis", CompletionID: subject.CompletionID, CycleID: subject.CycleID, InventoryDigest: subject.InventoryDigest, SourceCommit: strategist.SourceCommit, PreparationDigest: cap.PreparationDigest, LocalInputDigest: strings.Repeat("b", 64), Inspected: []factory.InspectedSource{{Path: "README.md", Commit: strategist.SourceCommit, Evidence: "controlled sealed inventory"}}}
	if err = f.post(ctx, strategist, cap, factory.MCPToolFinishReassessment, map[string]any{"subject": cap.Subject, "report": "invalid"}, map[string]any{"subject": cap.Subject, "report": reassessment}, ""); err != nil {
		return err
	}
	if _, err = f.settle(ctx, len(f.state.Entries)-1); err != nil {
		return err
	}
	pilot := offlineRoleAssignment(project.ExecutionVerify, project.RoleReviewer, strings.Repeat("3", 40))
	pilot.Issue, pilot.Acceptance, pilot.PublicationAssignment = 0, "", ""
	delivery := factory.DeliverySubject{IntentID: decision(), AcceptedRevision: decision(), ScopeDigest: strings.Repeat("a", 64), SourceCommit: pilot.SourceCommit, PreparationDigest: strings.Repeat("a", 64), VerificationManifestDigest: strings.Repeat("b", 64)}
	delivery.DeliveryID = factory.DeliveryID(delivery)
	pilot.Subject = factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &delivery}
	cap, err = f.admit(ctx, pilot, factory.MCPProfileTestPilot)
	if err != nil {
		return err
	}
	if err = f.timeOnlyAllowance(ctx, pilot); err != nil {
		return err
	}
	coverage := []factory.ReassessmentCoverage{{IntentID: delivery.IntentID, OutcomeID: decision(), TaskKeys: []string{decision()}, DeliveredAcceptanceIDs: []string{decision()}}}
	good := map[string]any{"outcome": factory.AppVerificationVerified, "coverage": coverage, "check_receipts": []factory.VerificationCheckReceipt{{CheckID: "controlled-check", Outcome: factory.CheckReceiptPassed, LogExcerpt: "controlled input only; no check executed"}}}
	if err = f.post(ctx, pilot, cap, factory.MCPToolFinishAppVerify, map[string]any{"outcome": factory.AppVerificationVerified, "coverage": coverage}, good, ""); err != nil {
		return err
	}
	if _, err = f.settle(ctx, len(f.state.Entries)-1); err != nil {
		return err
	}
	fmt.Println("PASS: Issue Maker, Product Lead, Strategist and Test Pilot typed invalid→corrected results settle through the real PostgreSQL joint with exact time-only allowances; walkthroughs, inventory and receipt observations controlled, no issue/app/graph effects")
	return nil
}

func offlineRoleHandoffs() error {
	phase, path := os.Getenv("SODA_NATIVE_PG_PHASE"), os.Getenv("SODA_NATIVE_PG_POST_ID_FILE")+".role-handoffs.json"
	if phase != "write" && phase != "restart" && phase != "recover" {
		return errors.New("requires native PostgreSQL write/restart/recover driver")
	}
	dsnFile := os.Getenv("SODA_NATIVE_PG_DSN_FILE")
	if dsnFile == "" || os.Getenv("SODA_NATIVE_PG_POST_ID_FILE") == "" {
		return errors.New("requires new private native PostgreSQL fixture")
	}
	dsn, err := os.ReadFile(dsnFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, string(dsn))
	if err != nil {
		return err
	}
	defer db.Close()
	rawDB, err := sql.Open("pgx", string(dsn))
	if err != nil {
		return err
	}
	defer rawDB.Close()
	f := offlineRoleFixture{db: db, sql: rawDB, path: path, state: offlineRoleState{Version: "controlled-role-handoffs-v1"}}
	if phase == "write" {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("offline role scenario requires a new manifest")
		}
		selection := factory.RoleSelection{Account: strings.Repeat("c", 32), Harness: project.FactoryHarnessMuse, Model: "controlled-no-model"}
		ref := func(kind string) factory.ActorBindingRef {
			return factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: kind}
		}
		policy := factory.RepositoryPolicy{Repository: 714, GrantedBy: 7, TargetBranch: "refs/heads/main", MergeMethod: factory.MergeFastForward, Checks: []string{"controlled/check"}, Roles: map[string]factory.RoleSelection{project.RoleCoder: selection, project.RoleReviewer: selection}, Publish: ref(factory.OpRefPublish), Create: ref(factory.OpPRCreate), Review: ref(factory.OpReviewSubmit), Merge: ref(factory.OpMerge), AttemptLimits: factory.DefaultAttemptLimits(), ReassessmentMinutes: factory.DefaultReassessmentMinutes}
		if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
			return fmt.Errorf("controlled source policy setup: %w", err)
		}
		// A plan finish needs a JEV plan review and Factory has no JEV connection,
		// so the Architect journey ends here: the finish is refused before it can
		// freeze a terminal, and no implementation follows.
		a := offlineRoleAssignment(project.ExecutionInvestigate, project.RoleCoder, strings.Repeat("1", 40))
		cap, err := f.admit(ctx, a, factory.MCPProfileArchitect)
		if err != nil {
			return err
		}
		plan := nativeWorkspacePlan(a.SourceCommit)
		planRaw, _ := json.Marshal(plan)
		var authored map[string]any
		_ = json.Unmarshal(planRaw, &authored)
		if _, err = f.proposal(ctx, cap, factory.MCPToolFinishIssuePlan, map[string]any{"approach": plan.Approach, "tasks": authored, "evidence_refs": []string{"controlled:README.md"}}); err == nil {
			return errors.New("Architect plan finish was admitted without a plan review")
		}
		unreviewed, err := json.Marshal(map[string]any{"approach": plan.Approach, "tasks": authored, "evidence_refs": []string{"controlled:README.md"}, "plan_review": strings.Repeat("0", 64)})
		if err != nil {
			return err
		}
		if _, err = f.db.RecordMCPSubmission(ctx, factory.MCPSubmission{AssignmentID: a.ID, CapabilityID: cap.ID, SubmissionID: factory.NewID(), Tool: factory.MCPToolFinishIssuePlan, Payload: unreviewed}); !errors.Is(err, store.ErrAdmissionChanged) {
			return fmt.Errorf("unreviewed plan finish did not refuse recording: %w", err)
		}
		if _, found, err := f.db.TerminalMCPProposal(ctx, a.ID); err != nil || found {
			return fmt.Errorf("refused plan finish froze a terminal: %w", err)
		}
		if err = f.remainingProfiles(ctx); err != nil {
			return fmt.Errorf("remaining controlled profiles: %w", err)
		}
		fmt.Println("PASS: controlled Architect plan finish refused without a JEV plan review (nothing recorded, no terminal); Issue Maker, Product Lead, Strategist and Test Pilot terminals recorded for real PostgreSQL restart; no inference or check execution")
		return nil
	}
	manifest, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(manifest, &f.state); err != nil || f.state.Version != "controlled-role-handoffs-v1" {
		return errors.New("offline role manifest malformed")
	}
	if len(f.state.Entries) != 4 {
		return fmt.Errorf("incomplete controlled chain: %d terminals", len(f.state.Entries))
	}
	for i := range f.state.Entries {
		if _, err = f.settle(ctx, i); err != nil {
			return fmt.Errorf("%s role %d: %w", phase, i, err)
		}
	}
	fmt.Println("PASS: four controlled typed results and exact original terminal payloads survive PostgreSQL restart/WAL recovery; repeated settlement/replay changes no assignment; no worker, provider, publication or native execution qualification")
	return nil
}

// Only the final writer is controlled: actual HTTP decoding, stored owner/run
// resolution and current account/runtime/repository admission remain production.
type offlinePublicationActions struct {
	*control.Coordinator
	calls int
}

func (w *offlinePublicationActions) FundRetainedCandidate(_ context.Context, command, _ string, funding factory.RetainedCandidateFunding) (factory.RetainedCandidateFundingReceipt, error) {
	w.calls++
	return factory.RetainedCandidateFundingReceipt{CommandID: command, Funding: funding}, nil
}
func (w *offlinePublicationActions) PrepareUnsentMerge(_ context.Context, command, _ string, request factory.MergePreparation) (factory.MergePreparationReceipt, error) {
	w.calls++
	return factory.MergePreparationReceipt{CommandID: command, Request: request, MergeID: request.MergeID}, nil
}

// Retained-copy qualification: only confirmed capability projection retirement
// may change; no coordinator, provider or fixture phase mutation is constructed.
type retainedPlanAssignments struct{ db *sql.DB }

// Observe the actual original native retirement through the running owner,
// changing only a disposable store copy. No setup, retirement send, coordinator,
// model or provider is constructed by this qualification.
func retainedPreparationStop(dsnFile, profile, instance, id string) error {
	if !strings.Contains(dsnFile, "/current20-receipt-inspection/") {
		return errors.New("requires the disposable original-receipt inspection copy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn, err := os.ReadFile(dsnFile)
	if err != nil {
		return err
	}
	db, err := store.OpenStandaloneContext(ctx, string(dsn))
	if err != nil {
		return err
	}
	defer db.Close()
	record, err := db.CandidatePreparation(ctx, id)
	if err != nil || record.Stopped || record.Preparation.Native == nil || record.Preparation.Native.InstanceID == instance {
		return errors.New("requires original unprojected retirement from a prior native instance")
	}
	before, err := db.AttemptAllowance(ctx, record.Admission.Repository, record.Admission.Issue)
	if err != nil || before.Closed {
		return errors.New("original attempt must remain open")
	}
	pending, err := db.PendingCandidatePreparations(ctx, 256)
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range pending {
		if candidate.Preparation.Preparation.ID == id {
			found = !candidate.RetireClosed
		}
	}
	if !found {
		return errors.New("transferred native preparation was hidden from original-receipt recovery")
	}
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	state, err := port.InspectWorkspacePreparation(ctx, record.Preparation.Address())
	if err != nil {
		return err
	}
	if !state.Stopped || state.Retirement != "confirmed" || state.ValidateFor(record.Preparation.Address()) != nil {
		return errors.New("original native owner has no exact confirmed retirement")
	}
	for i := 0; i < 2; i++ {
		if err = db.RecordCandidatePreparationStop(ctx, id, state); err != nil {
			return err
		}
	}
	projected, err := db.CandidatePreparation(ctx, id)
	if err != nil || !projected.Stopped || projected.Retirement != "confirmed" {
		return errors.New("original retirement was not projected")
	}
	if _, err = db.CloseAttempt(ctx, record.Admission.OwnerAssignment); err != nil {
		return err
	}
	closed, err := db.AttemptAllowance(ctx, record.Admission.Repository, record.Admission.Issue)
	if err != nil || !closed.Closed || closed.Active {
		return errors.New("exact settled root remains blocked after confirmed retirement")
	}
	fmt.Println("PASS: transferred native preparation on an open settled root is visible; original prior-instance retirement is read/adopted idempotently; exact root can close without renewed setup, model, provider or retirement send; disposable store only")
	return nil
}

func (s retainedPlanAssignments) Assignment(ctx context.Context, id string) (factory.Assignment, error) {
	var raw []byte
	var owner factory.Assignment
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &owner)
	}
	return owner, err
}

func retainedRootRetry(dsnFile, assignment string) error {
	if !strings.Contains(dsnFile, "/current20-receipt-inspection/") {
		return errors.New("requires the disposable original-receipt inspection copy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dsn, err := os.ReadFile(dsnFile)
	if err != nil {
		return err
	}
	db, err := store.OpenStandaloneContext(ctx, string(dsn))
	if err != nil {
		return err
	}
	defer db.Close()
	owner, err := db.Assignment(ctx, assignment)
	if err != nil {
		return err
	}
	latest, err := db.LatestIssueAttempt(ctx, owner.Repository, owner.Issue)
	if err != nil || latest.ID == owner.ID || latest.AttemptRoot != owner.ID || latest.Stage != factory.AssignmentFinished {
		return errors.New("requires actual settled root and later child receipts")
	}
	run, err := db.FactoryRun(ctx, owner.Run)
	if err != nil || !run.Reconciled {
		return errors.New("original root is not reconciled")
	}
	if _, err = db.CloseAttempt(ctx, owner.ID); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"prior_run": run.ID, "prior_outcome": string(run.Outcome)})
	if err != nil {
		return err
	}
	cmd := factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "run/" + run.ID, Principal: "1", Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	decision, err := cmd.RetryDecision()
	if err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		stored, created, e := db.RecordExplicitRetryCommand(ctx, cmd, decision, owner.Repository, owner.Issue, owner.Acceptance, time.Now())
		if e != nil || created != (i == 0) || stored.ID != cmd.ID || stored.Finished == "" {
			return fmt.Errorf("root retry admission/replay: created=%v error=%v", created, e)
		}
	}
	pending, err := db.PendingExplicitRetry(ctx, owner.Repository, owner.Issue, owner.Acceptance)
	if err != nil || pending.CommandID != cmd.ID || pending.Prior != run.ID {
		return fmt.Errorf("root retry recovery: %v", err)
	}
	fmt.Println("PASS: original settled root with later failed child admits one fresh Retry and recovers the same command; disposable copy only, no coordinator, provider, model or dispatch")
	return nil
}

func retiredPlanReview(dsnFile, assignment string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dsn, err := os.ReadFile(dsnFile)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", string(dsn))
	if err != nil {
		return err
	}
	defer db.Close()
	before := map[string]string{}
	for _, table := range []string{"factory_runs", "factory_assignments", "factory_commands", "factory_usage", "factory_publications"} {
		var digest string
		if err = db.QueryRowContext(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM `+table+` t`).Scan(&digest); err != nil {
			return err
		}
		before[table] = digest
	}
	projection, err := store.OpenStandaloneContext(ctx, string(dsn))
	if err != nil {
		return err
	}
	defer projection.Close()
	if err = projection.RecoverNativeCapabilityRetirement(ctx); err != nil {
		return err
	}
	if err = projection.RecoverNativeCapabilityRetirement(ctx); err != nil {
		return err
	}
	assignments := retainedPlanAssignments{db}
	owner, err := assignments.Assignment(ctx, assignment)
	if err != nil || owner.Result == nil || owner.Result.PhaseResult == nil || owner.Result.PhaseResult.Output == nil || owner.Result.PhaseResult.Output.Plan == nil {
		return errors.New("original retained investigated plan required")
	}
	var cap factory.MCPCapability
	var capRaw []byte
	var capState string
	err = db.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1`, owner.Run).Scan(&capState, &capRaw)
	if err != nil || json.Unmarshal(capRaw, &cap) != nil || capState != factory.MCPCapabilityRetired && capState != factory.MCPCapabilityRevoked {
		return fmt.Errorf("actual revoked/retired original capability required: state=%q error=%v", capState, err)
	}
	cap.State = capState
	raw, err := json.Marshal(owner.Result.PhaseResult.Output.Plan)
	if err != nil {
		return err
	}
	var plan map[string]any
	if err = json.Unmarshal(raw, &plan); err != nil {
		return err
	}
	payload := map[string]any{"tasks": plan, "approach": plan["approach"], "evidence_refs": []any{"retained-plan"}}
	called := 0
	results := factoryservice.WorkerResults{Assignments: assignments, Consultations: func(factory.MCPCapability, string) factoryservice.WorkerConsultations {
		called++
		return factoryservice.WorkerConsultations{}
	}}
	results.Bind(ctx, cap, factory.MCPToolFinishIssuePlan, payload)
	raw, err = json.Marshal(payload)
	if err != nil {
		return err
	}
	submission := factory.MCPSubmission{AssignmentID: owner.ID, CapabilityID: cap.ID, SubmissionID: factory.NewID(), Tool: factory.MCPToolFinishIssuePlan, Payload: raw}
	if err = owner.Result.PhaseResult.Output.Plan.Executable(); err != nil {
		return fmt.Errorf("original recorded plan is not shape-valid: %w", err)
	}
	if err = results.CheckNewFinish(ctx, cap, submission, payload); err == nil || called != 0 || payload["plan_review"] != nil {
		return fmt.Errorf("retired plan borrowed review authority: calls=%d error=%v", called, err)
	}
	for table, expected := range before {
		var actual string
		if err = db.QueryRowContext(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM `+table+` t`).Scan(&actual); err != nil || actual != expected {
			return fmt.Errorf("original %s changed during capability retirement: %v", table, err)
		}
	}
	fmt.Println("PASS: sealed native receipt retires the original capability idempotently; original runs/assignments/commands/charges/publications unchanged; shape-valid retained plan cannot fund new review; no provider, coordinator, model or proposal write")
	return nil
}

func onboardedSetupFixture(profile, instance, repository string, retire bool, expectedSources int) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	var repo int64
	if _, err = fmt.Sscan(repository, &repo); err != nil {
		return err
	}
	grant, err := db.EnvironmentGrant(ctx, repo)
	if err != nil {
		return err
	}
	if expectedSources == 0 {
		requirements, err := db.RequirementDepth(ctx, grant.Project)
		if err != nil {
			return err
		}
		approvals, err := db.ApprovalDepth(ctx, grant.Project)
		if err != nil {
			return err
		}
		preparations, err := db.StandingProjectPreparations(ctx, grant.Project)
		if err != nil {
			return err
		}
		if requirements != 0 || approvals != 0 || len(preparations) != 0 {
			return errors.New("withdrawn setup source I/O recorded consent or preparation")
		}
		fmt.Println("PASS: authority withdrawn during source I/O left zero requirement, approval or preparation records")
		return nil
	}
	head, err := db.ApprovalHead(ctx, grant.Project)
	if err != nil {
		return err
	}
	approval, err := db.ApprovalDecision(ctx, head)
	if err != nil {
		return err
	}
	if !approval.Reviewed || approval.Verified || approval.ReadinessDigest != "" || approval.Native == nil {
		return errors.New("onboarding invented installed readiness evidence")
	}
	if expectedSources == 14 {
		requirements, err := db.RequirementDepth(ctx, grant.Project)
		if err != nil {
			return err
		}
		approvals, err := db.ApprovalDepth(ctx, grant.Project)
		if err != nil {
			return err
		}
		if requirements != 1 || approvals != 1 {
			return errors.New("initial onboarding recorded more than the one current reviewed decision")
		}
	}
	preparations, err := db.StandingProjectPreparations(ctx, grant.Project)
	if err != nil {
		return err
	}
	if expectedSources == -1 {
		requirement, err := db.RequirementDecision(ctx, approval.Requirement)
		if err != nil {
			return err
		}
		requirements, err := db.RequirementDepth(ctx, grant.Project)
		if err != nil {
			return err
		}
		approvals, err := db.ApprovalDepth(ctx, grant.Project)
		if err != nil {
			return err
		}
		if requirements != 1 || approvals != 1 || requirement.Validate() != nil || approval.Validate() != nil || requirement.Recipe == nil || requirement.Recipe.CommandDigest != "" || len(preparations) == 0 || len(preparations) > 2 {
			return errors.New("original legacy consent records changed or acquired command provenance")
		}
		for _, prep := range preparations {
			if prep.Native == nil || prep.Native.InstanceID == instance || prep.Preparation.Requirements.ID != requirement.ID || prep.Preparation.Approval.ID != approval.ID {
				return errors.New("legacy retry admitted a new native preparation")
			}
		}
		fmt.Println("PASS: original legacy requirement/approval remain valid without command provenance; only historical preparations, no new native admission")
		return nil
	}
	if expectedSources == 2 || expectedSources == 64 {
		current := preparations[:0]
		for _, prep := range preparations {
			if prep.Preparation.Approval.ID == approval.ID {
				current = append(current, prep)
			}
		}
		preparations = current
	}
	if len(preparations) != 2 {
		return errors.New("initial setup did not create exactly both role preparations")
	}
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	for _, prep := range preparations {
		var state execution.PreparationState
		if retire {
			state, err = port.RetireWorkspacePreparation(ctx, prep.Address())
		} else {
			state, err = port.InspectWorkspacePreparation(ctx, prep.Address())
		}
		if err != nil {
			return err
		}
		if retire {
			if !state.Stopped || state.Ready || state.Retirement != "confirmed" {
				return errors.New("onboarded workspace retirement incomplete")
			}
		} else if !state.Ready || state.SetupExit == nil || *state.SetupExit != 0 || state.CheckExit == nil || *state.CheckExit != 0 || state.SourceBundleSHA256 == "" {
			return errors.New("onboarding readiness lacks native source/setup/check observations")
		}
		if !retire {
			p := prep.Preparation
			if len(p.Requirements.SourceInputs) != expectedSources {
				return errors.New("onboarding lost reviewed planning source selection")
			}
			reader := control.NewCoordinator(db, port, port)
			material, readErr := reader.ReadApplicationVerificationContext(ctx, prep)
			if readErr != nil {
				return readErr
			}
			if expectedSources > 12 && material.ValidateContent(p.Role) == nil || material.ValidateSelectedContent(p.Role) != nil {
				return errors.New("combined source context weakened the native reply bound")
			}
			seen := map[string]bool{}
			for _, file := range material.Files {
				if wanted, selected := p.Requirements.SourceInputs[file.Path]; selected {
					sum := sha256.Sum256(file.Content)
					if hex.EncodeToString(sum[:]) != wanted {
						return errors.New("prepared planning context differs from reviewed source")
					}
					seen[file.Path] = true
				}
			}
			if len(seen) != expectedSources {
				return errors.New("prepared planning context omitted reviewed source")
			}
		}
		prep.State = state
		if err = db.ObservePreparation(ctx, prep); err != nil {
			return err
		}
	}
	if !retire {
		input, err := db.FactoryAuthorityInput(ctx, repo)
		if err != nil {
			return err
		}
		if !input.PreparationReady {
			return errors.New("reviewed native setup cannot reach canonical readiness")
		}
		if expectedSources == 2 {
			var fixture struct {
				AcceptInputs bool   `json:"accept_inputs"`
				Issue        int64  `json:"issue_number"`
				Comment      string `json:"comment_id"`
				CommentBody  string `json:"comment_body"`
				Prerequisite int64  `json:"prerequisite_issue"`
			}
			raw, err := os.ReadFile(filepath.Join(filepath.Dir(profile), "github-write-fixture.json"))
			if err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &fixture); err != nil {
				return err
			}
			if fixture.AcceptInputs {
				head, err := db.AcceptanceHead(ctx, repo, fixture.Issue)
				if err != nil {
					return err
				}
				accepted, err := db.AcceptanceDecision(ctx, head)
				if err != nil {
					return err
				}
				depth, err := db.AcceptanceDepth(ctx, repo, fixture.Issue)
				if err != nil {
					return err
				}
				if depth != 1 || accepted.Initial || accepted.Validate() != nil || len(accepted.Sources) != 1 || len(accepted.Prerequisites) != 1 || accepted.Sources[0].ID != fixture.Comment || accepted.Sources[0].Digest != factory.MCPInputDigest([]byte(fixture.CommentBody)) {
					return errors.New("native UI acceptance lost its exact comment or recorded a refused decision")
				}
				edge := accepted.Prerequisites[0]
				if edge.Outcome != factory.PrereqResult || edge.EndpointRepo != repo || edge.EndpointIssue != fixture.Prerequisite || edge.PrereqAcceptance != "" {
					return errors.New("native UI acceptance changed its reviewed prerequisite outcome")
				}
				source := factoryruntime.NativeInputSource{NativeExecution: port}
				policy, err := db.RepositoryPolicy(ctx, repo)
				if err != nil {
					return err
				}
				dispatch, err := source.ReadDispatchInputs(ctx, repository, strconv.FormatInt(fixture.Issue, 10), []string{fixture.Comment}, policy.TargetBranch)
				if err != nil {
					return err
				}
				if dispatch.Revision != accepted.NativeRev || dispatch.Issue.TitleDigest != accepted.TitleDigest || dispatch.Issue.ContentDigest != accepted.ContentDigest || len(dispatch.Comments) != 1 || dispatch.Comments[0].Content != fixture.CommentBody || dispatch.Comments[0].Digest != accepted.Sources[0].Digest {
					return errors.New("accepted planning inputs differ from actual native issue/comment revisions")
				}
				// Exercise the production source assembler with a fixture draft
				// derived from these actual accepted inputs; no draft or run is seeded.
				draft := factory.IntentRevision{ID: "d" + factory.NewID()[:24], Intent: "d" + factory.NewID()[:24], Owner: accepted.Approver, Repository: repo, State: "draft", MaxIssues: 1, CreatedUnix: time.Now().Unix(), Objective: dispatch.Issue.Title + "\n" + dispatch.Issue.Body + "\n" + dispatch.Comments[0].Content, Ideas: []factory.ProductIdea{}, Tasks: []factory.IntentTask{}}
				reader := control.NewCoordinator(db, port, port)
				planning, preparation, wait, err := reader.AssembleIntentPlanningInput(ctx, draft, factory.IntentPlanningPropose)
				if err != nil || wait != nil || len(planning.Inspected) != expectedSources || preparation.SourceCommit != dispatch.Tip {
					return fmt.Errorf("accepted native planning source assembly: wait=%+v err=%v", wait, err)
				}
				for _, file := range planning.Inspected {
					if file.Commit != dispatch.Tip || factory.MCPInputDigest([]byte(file.Evidence)) != preparation.Requirements.SourceInputs[file.Path] {
						return errors.New("planning source assembly differs from approved preparation")
					}
				}
				fmt.Println("PASS: actual UI accepted exactly one current native comment/prerequisite decision; refused inputs left no acceptance; exact native planning inputs and production approved-source assembly; fixture draft only, no model/run")
			}
		}
		fmt.Printf("PASS: production application-verification reader captured all %d approved source files from both native workspaces; individual native context bound remains 12 files\n", expectedSources)
	}
	fmt.Println("PASS: owner-reviewed initial setup retains Verified=false and no invented readiness digest; both roles have actual native setup/check/source observations", map[bool]string{true: "and confirmed retirement", false: "and canonical readiness"}[retire])
	return nil
}

// This check declares only disabled local policy; account, issue history and
// branch evidence come from the production native connector, without a view.
func backgroundInputsFixture(profile, instance, action, path string) error {
	var input struct {
		Repository, Issue, Connection, Decision string
		ExpectedChecks                          map[string]string
		ExpectedMaterialBlocker                 *string
		ExpectMaterialRefusal                   bool
		PairedRevision                          int64
		EvaluationCommit                        string
		Checks                                  factory.CheckTarget
		Correction                              struct {
			Target                        factory.CheckTarget
			Candidate, CancelledCandidate string
			Bundle, CancelledBundle       []byte
		}
		Publication struct {
			Base, Candidate string
			Bundle          []byte
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		return err
	}
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	fixtureTimeout := 35 * time.Second
	if strings.HasPrefix(action, "check-background-creation") {
		fixtureTimeout = 120 * time.Second
	}
	if action == "check-background-delivery-material" || action == "check-background-assembly-material" || action == "check-background-assembly-merge" {
		fixtureTimeout = 150 * time.Second
	}
	if strings.HasPrefix(action, "check-background-publication") || strings.HasPrefix(action, "check-background-correction") || strings.HasPrefix(action, "check-background-merge") {
		fixtureTimeout = 120 * time.Second
	}
	if strings.HasPrefix(action, "check-background-merge") {
		fixtureTimeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	connection, err := db.CollaborationConnection(ctx, collaboration.ConnectionID(input.Connection))
	if err != nil {
		return err
	}
	var repository, issue int64
	if _, err = fmt.Sscan(input.Repository, &repository); err != nil {
		return err
	}
	if _, err = fmt.Sscan(input.Issue, &issue); err != nil {
		return err
	}
	source := factoryruntime.NativeInputSource{NativeExecution: factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}}
	if action == "check-background-creation-policy-race" {
		return connectedCreationPolicyRace(ctx, source, connection, repository)
	}
	if strings.HasPrefix(action, "check-background-creation") {
		return connectedCreationFixture(ctx, source, int64(connection.Actor), connection.ID, repository, issue, profile, action)
	}
	if action == "check-background-assembly-material" || action == "check-background-assembly-merge" {
		// Dummy-only wire qualification uses the production collector. The
		// merge variant declares workflow rows and permits controlled writes;
		// neither variant provides an inference source or model execution.
		ref := func(kind string) factory.ActorBindingRef {
			return factory.ActorBindingRef{Kind: kind, ActorID: int64(connection.Actor), NativeProfile: config.Profile, NativeConnection: connection.ID}
		}
		policy := factory.RepositoryPolicy{Repository: repository, Revision: 1, Publish: ref(factory.OpRefPublish), Merge: ref(factory.OpMerge)}
		if action == "check-background-assembly-merge" {
			policy.GrantedBy, policy.TargetBranch, policy.MergeMethod = int64(connection.Actor), "refs/heads/main", "merge"
			policy.Checks, policy.ReassessmentMinutes, policy.AttemptLimits = []string{"build"}, factory.DefaultReassessmentMinutes, factory.DefaultAttemptLimits()
			policy.Create, policy.Review = ref(factory.OpPRCreate), ref(factory.OpReviewSubmit)
			policy.Roles = map[string]factory.RoleSelection{project.RoleCoder: {Harness: project.FactoryHarnessMuse, Model: "fixture-unavailable"}, project.RoleReviewer: {Harness: project.FactoryHarnessMuse, Model: "fixture-unavailable"}}
		}
		encoded, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		raw, err := sql.Open("pgx", config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer raw.Close()
		if _, err = raw.ExecContext(ctx, `INSERT INTO factory_policies(repository,revision,data) VALUES($1,1,$2) ON CONFLICT(repository) DO NOTHING`, repository, string(encoded)); err != nil {
			return err
		}
	}
	if action == "check-background-assembly-merge" {
		target := input.Checks
		target.Repository = repository
		return nativeReviewFixture(ctx, db, factoryruntime.NativeReviews{NativeInputSource: source}, connection.Actor, issue, target, path, "check-background-merge")
	}
	if action == "check-background-delivery-material" || action == "check-background-assembly-material" {
		port := factoryruntime.NativeMerges{NativeInputSource: source}
		w := factory.MergeWork{Method: "merge", Repository: repository, Issue: issue, PRNumber: input.Checks.PRNumber, PRID: input.Checks.PRID, IssueID: input.Checks.IssueID, ActorID: int64(connection.Actor), PRAuthorID: int64(connection.Actor), ReviewerID: int64(connection.Actor), ReviewEvent: "COMMENT", HeadRef: input.Checks.HeadRef, BaseRef: input.Checks.BaseRef, HeadOID: input.Checks.HeadOID, BaseOID: input.Checks.BaseOID}
		var material factory.DeliveryNativeMaterial
		if input.PairedRevision > 0 {
			p := declaredPublishedFixture(factory.CheckTarget{Repository: repository, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID}, connection.Actor, factory.NewID(), issue, input.PairedRevision, time.Now().Add(time.Minute).Unix())
			if err := p.Validate(); err != nil {
				return err
			}
			encoded, encodeErr := json.Marshal(p)
			if encodeErr != nil {
				return encodeErr
			}
			raw, openErr := sql.Open("pgx", config.DatabaseDSN())
			if openErr != nil {
				return openErr
			}
			defer raw.Close()
			if _, err = raw.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, p.AssignmentID, repository, issue, p.Run, p.Stage, string(encoded)); err != nil {
				return err
			}
			w.PublicationID, w.NativeRev = p.ID, input.PairedRevision
			var continuation control.DeliveryMergeObservation
			material, continuation, err = port.ReadDeliveryMaterialForMerge(ctx, p, w)
			if err == nil && continuation == nil {
				return errors.New("paired material omitted its merge observation")
			}
		} else {
			material, err = port.ReadDeliveryMaterial(ctx, w)
		}
		if err != nil {
			if action == "check-background-assembly-material" && input.ExpectMaterialRefusal {
				if !reflect.DeepEqual(material, factory.DeliveryNativeMaterial{}) {
					return errors.New("refused assembly exposed partial material")
				}
				fmt.Println("PASS: changed or withdrawn assembly refuses without partial material")
				return nil
			}
			return err
		}
		if input.ExpectMaterialRefusal {
			return errors.New("changed or withdrawn assembly supplied material")
		}
		if input.ExpectedMaterialBlocker != nil && (material.Blocker != *input.ExpectedMaterialBlocker || material.Eligible != (*input.ExpectedMaterialBlocker == "")) {
			return fmt.Errorf("assembly eligibility/blocker differs: eligible=%t blocker=%q", material.Eligible, material.Blocker)
		}
		if material.Revision <= 0 || material.Repository != repository || material.PRID != w.PRID || material.Head != w.HeadOID || material.Base != w.BaseOID || material.Title == "" || material.Comments == nil || material.Reviews == nil || material.Threads == nil {
			return errors.New("native delivery material lost its exact scope or complete collections")
		}
		for _, comment := range material.Comments {
			if comment.ContentVersion != nil || comment.Invalidated != nil || comment.Digest != factory.MCPInputDigest([]byte(comment.Content)) {
				return errors.New("GitHub delivery comment invented an edit counter/state or lost body bytes")
			}
		}
		for _, review := range material.Reviews {
			if review.Official != nil || review.Stale != nil || review.Digest != factory.MCPInputDigest([]byte(review.Content)) {
				return errors.New("GitHub delivery review invented an unavailable native flag")
			}
		}
		for _, thread := range material.Threads {
			if thread.ProviderID == "" || thread.OriginalAnchor != nil || thread.Digest == "" || len(thread.Members) == 0 {
				return errors.New("native delivery thread lost its provider identity or invented an original base")
			}
			if _, err := thread.ConversationDigest(material.Comments, true); err == nil {
				return errors.New("unknown GitHub edit/resolution facts authorized legacy resolution prediction")
			}
		}
		raw, err := json.Marshal(material)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "delivery-material.json"), raw, 0600); err != nil {
			return err
		}
		fmt.Printf("PASS: actual native delivery reader: %d comments, %d reviews, %d threads; exact bodies/identities, bounded current opinions and unknown flags retained; no judge or mutation\n", len(material.Comments), len(material.Reviews), len(material.Threads))
		return nil
	}
	if action == "check-background-merge-resume" {
		if _, e := os.Stat(filepath.Join(filepath.Dir(path), "native-merge.json")); e == nil {
			return nativeMergeRetained(ctx, db, factoryruntime.NativeMerges{NativeInputSource: source}, path)
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		conn, err := sql.Open("pgx", config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer conn.Close()
		var raw []byte
		if err = conn.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE repository=$1 AND data->>'pr_number'=$2`, repository, fmt.Sprint(input.Checks.PRNumber)).Scan(&raw); err != nil {
			return err
		}
		var p factory.Publication
		if err = json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if err = p.Validate(); err != nil {
			return err
		}
		if _, err = db.MergeByPublication(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
			m, e := db.MergeByPublication(ctx, p.ID)
			if e != nil {
				return e
			}
			if m.Operation.Work == nil {
				return errors.New("original merge has no frozen work")
			}
			w := m.Operation.Work.Apply(factory.MergeWork{MergeID: m.ID, PublicationID: m.PublicationID, Issue: m.Issue})
			port := factoryruntime.NativeMerges{NativeInputSource: source}
			out, e := port.LookupOp(ctx, w.OperationID)
			if e != nil {
				return e
			}
			if out.Effect != factory.OpEffectCommitted || out.Completion != factory.OpCompletionComplete {
				return errors.New("original merge is not attributable/complete; preserve its fence without resubmission")
			}
			fmt.Println("PASS: original command journal proves the completed merge; recovery performs no merge submit")
			return finishNativeMergeFixture(ctx, db, port, m, nativeMergeFixtureState{Work: w, Outcome: out}, path)
		}
		if len(p.ReviewOperations) != 1 {
			return errors.New("one original review required for merge resume")
		}
		op := p.ReviewOperations[0]
		review, err := (factoryruntime.NativeReviews{NativeInputSource: source}).AdoptReview(ctx, op.Work, op.Outcome)
		if err != nil {
			return err
		}
		return nativeMergeFixture(ctx, db, factoryruntime.NativeMerges{NativeInputSource: source}, p, review, path)
	}
	if action == "check-background-merge-retained" {
		return nativeMergeRetained(ctx, db, factoryruntime.NativeMerges{NativeInputSource: source}, path)
	}
	if action == "check-background-merge" {
		target := input.Checks
		target.Repository = repository
		return nativeReviewFixture(ctx, db, factoryruntime.NativeReviews{NativeInputSource: source}, connection.Actor, issue, target, path, action)
	}
	if strings.HasPrefix(action, "check-background-correction") {
		target := input.Correction.Target
		target.Repository = repository
		return nativeCorrectionFixture(ctx, db, factoryruntime.NativePublication{NativeInputSource: source}, connection.Actor, issue, target, input.Correction.Candidate, input.Correction.CancelledCandidate, input.Correction.Bundle, input.Correction.CancelledBundle, path, action)
	}
	if strings.HasPrefix(action, "check-background-publication") {
		return nativePublicationFixture(ctx, db, factoryruntime.NativePublication{NativeInputSource: source}, connection.Actor, repository, issue, input.Publication.Base, input.Publication.Candidate, input.Publication.Bundle, path, action)
	}
	if strings.HasPrefix(action, "check-background-review") {
		target := input.Checks
		target.Repository = repository
		return nativeReviewFixture(ctx, db, factoryruntime.NativeReviews{NativeInputSource: source}, connection.Actor, issue, target, path, action)
	}
	if action == "check-background-ci" {
		target := input.Checks
		target.Repository = repository
		observed, err := source.ObserveChecks(ctx, target, int64(connection.Actor))
		if err != nil {
			return err
		}
		if !observed.Complete || observed.NativeRev <= 0 || observed.HeadTip != target.HeadOID || observed.BaseTip != target.BaseOID || (input.ExpectedChecks == nil && len(observed.Checks) != 2) || (input.EvaluationCommit != "" && observed.EvaluationCommit != input.EvaluationCommit) {
			return errors.New("native CI observation binding differs")
		}
		policy, err := db.RepositoryPolicy(ctx, repository)
		if err != nil {
			return err
		}
		policy.TargetBranch = target.BaseRef
		expectations := input.ExpectedChecks
		if expectations == nil {
			expectations = map[string]string{"factory-native-observer/pass": factory.CheckPass, "factory-native-observer/fail": factory.CheckFailed, "factory-native-observer/missing": factory.CheckPending}
		}
		for name, expected := range expectations {
			policy.Checks = []string{name}
			adopted := factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}
			assessment, err := factory.VerifyChecks(target, adopted, policy, observed, time.Now().Unix())
			if err != nil || assessment.Verdict != expected {
				return fmt.Errorf("native CI %s verdict %s: %w", name, assessment.Verdict, err)
			}
			recorded, err := db.RecordCheckAssessment(ctx, assessment)
			if err != nil {
				return err
			}
			if recorded.EvaluationCommit != observed.EvaluationCommit {
				return errors.New("durable check evaluation differs")
			}
		}
		changed := target
		changed.HeadOID = strings.Repeat("c", 40)
		for name := range expectations {
			policy.Checks = []string{name}
			break
		}
		assessment, err := factory.VerifyChecks(changed, factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}, policy, observed, time.Now().Unix())
		if err != nil || assessment.Verdict != factory.CheckRefused || assessment.Reason != factory.CheckReasonStaleHead {
			return errors.New("native CI evidence admitted a different candidate")
		}
		if _, err = source.ObserveChecks(ctx, target, int64(connection.Actor)+1); err == nil {
			return errors.New("native CI accepted another assessor actor")
		}
		encoded, err := json.Marshal(observed)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(filepath.Dir(path), "observed-checks.json"), encoded, 0600); err != nil {
			return err
		}
		fmt.Println("PASS: exact native GitHub PR/branch/status evidence, latest status selection, Go/PostgreSQL expected context verdicts, stale-candidate and foreign-actor refusal; CI producer check only")
		return nil
	}
	if action == "check-background-inputs" {
		ref := func(kind string) factory.ActorBindingRef {
			return factory.ActorBindingRef{Kind: kind, ActorID: int64(connection.Actor), NativeProfile: config.Profile, NativeConnection: connection.ID}
		}
		policy := factory.RepositoryPolicy{
			Repository: repository, GrantedBy: int64(connection.Actor), TargetBranch: "refs/heads/main", MergeMethod: "merge",
			ReassessmentMinutes: factory.DefaultReassessmentMinutes, AttemptLimits: factory.DefaultAttemptLimits(), Checks: []string{"fixture/check"},
			Roles:   map[string]factory.RoleSelection{project.RoleCoder: {Harness: project.FactoryHarnessMuse, Model: "fixture-unavailable"}, project.RoleReviewer: {Harness: project.FactoryHarnessMuse, Model: "fixture-unavailable"}},
			Publish: ref(factory.OpRefPublish), Create: ref(factory.OpPRCreate), Review: ref(factory.OpReviewSubmit), Merge: ref(factory.OpMerge),
		}
		previous, e := db.RepositoryPolicy(ctx, repository)
		if e == nil {
			previous.Publish, previous.Create, previous.Review, previous.Merge = policy.Publish, policy.Create, policy.Review, policy.Merge
			policy = previous
		} else if !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
			return err
		}
	}
	if action == "check-background-inputs" && input.ExpectedChecks != nil {
		fmt.Println("PASS: declared disabled local policy for actual connected CI observer")
		return nil
	}
	if action == "check-background-readiness" {
		for {
			control, e := db.IssueControl(ctx, repository, issue)
			if e == nil && control.Acceptance == input.Decision && control.NativeRev > 0 && control.Readiness == factory.ReadinessNotAuthorized && control.Reason == factory.BlockerAuthorityMissing {
				fmt.Println("PASS: actual native application refreshes background issue readiness and keeps disabled, unfunded execution unauthorized")
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("background readiness did not settle: %+v: %v", control, e)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	evidence, err := source.ReadAcceptanceEvidence(ctx, input.Repository, input.Issue, nil)
	if action == "check-background-withdrawn" {
		if err == nil {
			return errors.New("withdrawn native account still supplied background inputs")
		}
		fmt.Println("PASS: withdrawn saved account refuses background evidence without choosing another account")
		return nil
	}
	if err != nil {
		return err
	}
	dispatch, err := source.ReadDispatchInputs(ctx, input.Repository, input.Issue, nil, "refs/heads/main")
	if err != nil {
		return err
	}
	if evidence.Revision <= 0 || evidence.Issue.Index != input.Issue || !evidence.Issue.Verified || dispatch.Revision != evidence.Revision || dispatch.TipRef != "refs/heads/main" || !factory.ValidCommit(dispatch.Tip) {
		return errors.New("background evidence and dispatch scope differ")
	}
	fmt.Println("PASS: actual saved native account reads exact GitHub issue/history and dispatch branch without a foreground operation")
	return nil
}

// Publication and independent verdict are declared here; GitHub target/account,
// native write, dispatch fence, causal receipt and retained SQL readback are real.
type nativeReviewWithoutRecovery struct{ control.ReviewExecutor }

// The fixture declares run completion and local policy authority. Candidate
// bytes are a real incremental Git bundle; transfer, journal, PR and receipts
// use production owners. This does not qualify execution or the full pipeline.
func nativePublicationFixture(ctx context.Context, db *store.Store, port factoryruntime.NativePublication, actor collaboration.ActorID, repository, issue int64, base, candidate string, bundle []byte, path, action string) error {
	statePath := filepath.Join(filepath.Dir(path), "native-publication.json")
	var state struct {
		Branch, Pull               factory.PublicationWork
		BranchOutcome, PullOutcome factory.OperationOutcome
		Review                     factory.ReviewWork
		ReviewOutcome              factory.OperationOutcome
	}
	if action == "check-background-publication-branch-recover-original" {
		rawDB, err := sql.Open("pgx", port.Config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer rawDB.Close()
		rows, err := rawDB.QueryContext(ctx, `SELECT data FROM factory_publications WHERE repository=$1 AND issue=$2 AND data->>'candidate'=$3`, repository, issue, candidate)
		if err != nil {
			return err
		}
		var original factory.Publication
		count := 0
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal(raw, &original); err != nil {
				rows.Close()
				return err
			}
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count != 1 || original.Publish.Work == nil || original.Candidate != candidate || original.BaseSHA != base {
			return errors.New("original publication is not uniquely recoverable")
		}
		run, err := db.FactoryRun(ctx, original.Run)
		if err != nil {
			return err
		}
		state.Branch = original.Publish.Work.Apply(factory.PublicationWork{AssignmentID: original.AssignmentID, Publication: original.ID, RunID: run.ID, Run: run, BaseSHA: original.BaseSHA, Issue: original.Issue})
		state.BranchOutcome, err = port.LookupOp(ctx, original.Publish.OperationID)
		if err != nil {
			return err
		}
		if state.BranchOutcome.Effect != factory.OpEffectCommitted || state.BranchOutcome.Completion != factory.OpCompletionComplete {
			return errors.New("original publication has no causal committed receipt")
		}
		if _, err = port.AdoptBranch(ctx, state.Branch, state.BranchOutcome); err != nil {
			return err
		}
		policy, err := db.RepositoryPolicy(ctx, repository)
		if err != nil {
			return err
		}
		for _, selection := range policy.Roles {
			if selection.Account != "fixture-unavailable" {
				return errors.New("original publication fixture policy changed")
			}
		}
		if policy.Enabled {
			policy.Enabled = false
			if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(statePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = file.Write(raw)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Println("PASS: retained original conditional publication and exact causal host receipt reopen/adopt without provider I/O or another send; only remaining fixture policy disable uses its current CAS revision")
		return nil
	}
	branchOnly := action == "check-background-publication-branch"
	if action != "check-background-publication" && !branchOnly {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return err
		}
		if action == "check-background-publication-merge" {
			reviews := factoryruntime.NativeReviews{NativeInputSource: port.NativeInputSource}
			publication, _, e := db.PublicationReviewOperation(ctx, state.Review.OperationID)
			if e != nil {
				return e
			}
			review, e := reviews.AdoptReview(ctx, state.Review, state.ReviewOutcome)
			if e != nil {
				return e
			}
			if e = nativeMergeFixture(ctx, db, factoryruntime.NativeMerges{NativeInputSource: port.NativeInputSource}, publication, review, path); e != nil {
				return e
			}
			policy, e := db.RepositoryPolicy(ctx, repository)
			if e != nil {
				return e
			}
			policy.Enabled = false
			return db.SaveRepositoryPolicy(ctx, policy)
		}
		for _, item := range []struct {
			Work    factory.PublicationWork
			Outcome factory.OperationOutcome
		}{{state.Branch, state.BranchOutcome}, {state.Pull, state.PullOutcome}} {
			if item.Work.OperationID == "" {
				continue
			}
			prior, err := port.LookupOp(ctx, item.Work.OperationID)
			if err != nil || !reflect.DeepEqual(prior, item.Outcome) {
				return fmt.Errorf("retained native publication differs: %v", err)
			}
		}
		if _, err := port.AdoptBranch(ctx, state.Branch, state.BranchOutcome); err != nil {
			return err
		}
		if state.Pull.OperationID != "" {
			if _, err := port.AdoptPRCreation(ctx, state.Pull, state.PullOutcome); err != nil {
				return err
			}
		}
		if state.Review.OperationID != "" {
			reviews := factoryruntime.NativeReviews{NativeInputSource: port.NativeInputSource}
			prior, e := reviews.LookupOp(ctx, state.Review.OperationID)
			if e != nil || !reflect.DeepEqual(prior, state.ReviewOutcome) {
				return fmt.Errorf("retained native review differs: %v", e)
			}
			if _, e = reviews.AdoptReview(ctx, state.Review, prior); e != nil {
				return e
			}
		}
		if os.Getenv("FACTORY_FOUNTAIN_CONTROLLER_MERGE") == "1" {
			if err := nativeMergeRetained(ctx, db, factoryruntime.NativeMerges{NativeInputSource: port.NativeInputSource}, path); err != nil {
				return err
			}
		}
		fmt.Println("PASS: native branch/PR command receipts survive restart and exact credential withdrawal without sending again")
		return nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	publication := factory.Publication{ID: factory.NewID(), AssignmentID: factory.NewID(), Repository: repository, Issue: issue, Run: factory.NewID(), Stage: factory.PublicationOpen, Candidate: candidate, ProjectID: "p" + strings.Repeat("d", 24), Role: project.RoleCoder, Acceptance: "d" + strings.Repeat("a", 24), Preparation: "f" + strings.Repeat("b", 24), BaseSHA: base, TargetBranch: "refs/heads/main", CreatedUnix: now.Unix()}
	policyTarget, err := db.RepositoryPolicy(ctx, repository)
	if err != nil {
		return err
	}
	publication.TargetBranch = policyTarget.TargetBranch
	publication.Publish = factory.PublicationOperation{Kind: factory.OpRefPublish}
	publication.PRCreate = factory.PublicationOperation{Kind: factory.OpPRCreate}
	run := factory.Run{ID: publication.Run, ProjectID: publication.ProjectID, Role: project.RoleCoder, InputSHA: base, Started: now.Add(-time.Minute), Deadline: now.Add(time.Minute), Outcome: factory.Succeeded, Reconciled: true, Image: "sha256:" + strings.Repeat("1", 64), Harness: "declared-fixture", Model: "declared-fixture"}
	if err := run.Validate(); err != nil {
		return fmt.Errorf("declared publication run: %w", err)
	}
	rawDB, err := sql.Open("pgx", port.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	encoded, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = rawDB.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,FALSE,TRUE,$2)`, run.ID, string(encoded)); err != nil {
		return err
	}
	if err = db.RecordPublication(ctx, publication); err != nil {
		return err
	}
	write := func() error {
		if err := publication.Validate(); err != nil {
			return fmt.Errorf("declared publication: %w", err)
		}
		raw, err := json.Marshal(publication)
		if err != nil {
			return err
		}
		_, err = rawDB.ExecContext(ctx, `UPDATE factory_publications SET stage=$2,revision=$3,data=$4 WHERE assignment=$1`, publication.AssignmentID, publication.Stage, publication.Revision, string(raw))
		return err
	}
	body := "Disposable integration fixture. Candidate bytes and provider effects are real; run completion and local authority are declared inputs. No model inspection or full coordinator journey is claimed."
	if os.Getenv("FACTORY_FOUNTAIN_CONTROLLER_MERGE") == "1" {
		body += fmt.Sprintf("\n\nCloses #%d", issue)
	}
	work := func(kind string) factory.PublicationWork {
		expected := "absent"
		if kind == factory.OpPRCreate {
			expected = candidate
		}
		return factory.PublicationWork{Bundle: bundle, AssignmentID: publication.AssignmentID, Publication: publication.ID, RunID: run.ID, Run: run, Candidate: candidate, BaseSHA: base, TargetBranch: publication.TargetBranch, OperationID: factory.PublicationOperationID(publication.ID, kind, 1), AuthRevision: "declared-native-publication-fixture", ExpectedOld: expected, ComparisonRef: publication.TargetBranch, PRTitle: "Disposable Factory native publication check", PRBody: body, Repository: repository, Issue: issue, ActorID: int64(actor)}
	}
	state.Branch = work(factory.OpRefPublish)
	observed, err := port.ObservePublication(ctx, state.Branch)
	if err != nil {
		return err
	}
	if observed.TargetTip != "" || observed.Comparison != base {
		return errors.New("publication fixture target differs")
	}
	state.Branch.NativeRev, state.Branch.ComparisonOID, state.Branch.NotAfter = observed.NativeRev, observed.Comparison, now.Add(2*time.Minute).Unix()
	intent := state.Branch.Intent()
	publication.Publish.OperationID = state.Branch.OperationID
	publication.Publish.Work = &intent
	publication.Publish.Attempts = 1
	publication.Publish.UpdatedUnix = now.Unix()
	publication.NativeRev = observed.NativeRev
	publication.ObservedUnix = now.Unix()
	publication.Comparison = base
	if err = write(); err != nil {
		return err
	}
	if _, err = port.SubmitPublish(ctx, state.Branch); err == nil {
		return errors.New("disabled policy authorized publication")
	}
	policy, err := db.RepositoryPolicy(ctx, repository)
	if err != nil {
		return err
	}
	policy.Enabled = true
	for role, selection := range policy.Roles {
		selection.Account = "fixture-unavailable"
		policy.Roles[role] = selection
	}
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	if controller, err := controllerFixture(port.Config.Directory); err != nil {
		return err
	} else if controller != nil {
		open, revision, withdrawal, err := db.DispatchState(ctx, repository)
		if err != nil {
			return err
		}
		if !open {
			if len(withdrawal.ActiveCauses) != 1 || withdrawal.ActiveCauses[0] != "policy_disabled" {
				return errors.New("unrelated publication fixture dispatch hold")
			}
			if _, _, err = db.ReopenDispatch(ctx, repository, revision); err != nil {
				return err
			}
		}
	}
	state.BranchOutcome, err = port.SubmitPublish(ctx, state.Branch)
	if err != nil {
		return err
	}
	if state.BranchOutcome.Effect != factory.OpEffectCommitted || state.BranchOutcome.Completion != factory.OpCompletionComplete {
		return fmt.Errorf("native branch outcome: %+v", state.BranchOutcome)
	}
	adopted, err := port.AdoptBranch(ctx, state.Branch, state.BranchOutcome)
	if err != nil {
		return err
	}
	if adopted.NewOID != candidate || adopted.OldOID != "absent" || adopted.Comparison != base {
		return errors.New("native branch receipt binding differs")
	}
	if replay, err := port.SubmitPublish(ctx, state.Branch); err != nil || !reflect.DeepEqual(replay, state.BranchOutcome) {
		return fmt.Errorf("native branch replay: %v", err)
	}
	record := func(op *factory.PublicationOperation, out factory.OperationOutcome) {
		op.InstallationID = out.InstallationID
		op.ActorID = out.ActorID
		op.RepositoryID = out.RepositoryID
		op.Effect = out.Effect
		op.Completion = out.Completion
		op.Cancellation = out.Cancellation
		op.Reason = out.Reason
		op.Receipt = string(out.Receipt)
	}
	record(&publication.Publish, state.BranchOutcome)
	if err = write(); err != nil {
		return err
	}
	if branchOnly {
		policy, err = db.RepositoryPolicy(ctx, repository)
		if err != nil {
			return err
		}
		policy.Enabled = false
		if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
			return err
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err = os.WriteFile(statePath, raw, 0o600); err != nil {
			return err
		}
		fmt.Println("PASS: real retained candidate bundle crosses Go publication capture, exact selected native controller grant and conditional host receive; exact branch/base/actor/digest receipt and original command replay, disabled policy refuses; run completion is declared, no model or full delivery")
		return nil
	}
	state.Pull = work(factory.OpPRCreate)
	observed, err = port.ObservePublication(ctx, state.Pull)
	if err != nil {
		return err
	}
	if observed.TargetTip != candidate || observed.Comparison != base {
		return errors.New("published branch observation differs")
	}
	state.Pull.NativeRev, state.Pull.ComparisonOID, state.Pull.NotAfter = observed.NativeRev, observed.Comparison, now.Add(2*time.Minute).Unix()
	pullIntent := state.Pull.Intent()
	publication.PRCreate.OperationID = state.Pull.OperationID
	publication.PRCreate.Work = &pullIntent
	publication.PRCreate.Attempts = 1
	publication.PRCreate.UpdatedUnix = now.Unix()
	publication.TargetTip = candidate
	if err = write(); err != nil {
		return err
	}
	state.PullOutcome, err = port.SubmitPRCreate(ctx, state.Pull)
	if err != nil {
		return err
	}
	if state.PullOutcome.Effect != factory.OpEffectCommitted || state.PullOutcome.Completion != factory.OpCompletionComplete {
		return fmt.Errorf("native PR outcome: %+v", state.PullOutcome)
	}
	linked, err := port.AdoptPRCreation(ctx, state.Pull, state.PullOutcome)
	if err != nil {
		return err
	}
	if linked.HeadOID != candidate || linked.BaseOID != base || linked.PRNumber <= 0 || linked.PRID <= 0 || linked.IssueID <= 0 || linked.AuthorID != int64(actor) {
		return errors.New("native PR receipt binding differs")
	}
	if replay, err := port.SubmitPRCreate(ctx, state.Pull); err != nil || !reflect.DeepEqual(replay, state.PullOutcome) {
		return fmt.Errorf("native PR replay: %v", err)
	}
	record(&publication.PRCreate, state.PullOutcome)
	publication.PRCreate.PRNumber, publication.PRCreate.PRID, publication.PRCreate.IssueID = linked.PRNumber, linked.PRID, linked.IssueID
	publication.PRCreate.HeadRef, publication.PRCreate.BaseRef, publication.PRCreate.HeadOID, publication.PRCreate.BaseOID = linked.HeadRef, linked.BaseRef, linked.HeadOID, linked.BaseOID
	publication.PRNumber, publication.PRID = linked.PRNumber, linked.PRID
	publication.Stage, publication.Outcome, publication.Reason, publication.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, time.Now().Unix()
	if err = write(); err != nil {
		return err
	}
	if os.Getenv("FACTORY_FOUNTAIN_CONTROLLER_REVIEW") == "1" {
		reviews := factoryruntime.NativeReviews{NativeInputSource: port.NativeInputSource}
		runID := factory.NewID()
		state.Review = factory.ReviewWork{OperationID: "review-" + runID, AuthRevision: factory.ReviewAuthRevision(publication.AssignmentID, runID), Repository: repository, ActorID: int64(actor), PRAuthorID: linked.AuthorID,
			PRID: linked.PRID, PRNumber: linked.PRNumber, IssueID: linked.IssueID, HeadRef: linked.HeadRef, BaseRef: linked.BaseRef, HeadOID: linked.HeadOID, BaseOID: linked.BaseOID, Event: "COMMENT", AgentVerdict: "APPROVED",
			Body: "Disposable connected native review. Independent verdict and run are declared fixture inputs; no model inference or human vote is claimed.", NotAfter: time.Now().Add(time.Minute).Unix()}
		observation, e := reviews.ObserveReview(ctx, state.Review)
		if e != nil {
			return e
		}
		state.Review.NativeRev = observation.NativeRev
		publication.ReviewOperations = append(publication.ReviewOperations, factory.ReviewOperation{RunID: runID, Work: state.Review})
		if err = write(); err != nil {
			return err
		}
		// Retain the original work before crossing its one-shot send boundary.
		raw, e := json.Marshal(state)
		if e != nil {
			return e
		}
		if e = os.WriteFile(statePath, raw, 0600); e != nil {
			return e
		}
		state.ReviewOutcome, e = reviews.SubmitReview(ctx, state.Review)
		if e != nil {
			return e
		}
		if state.ReviewOutcome.Effect != factory.OpEffectCommitted || state.ReviewOutcome.Completion != factory.OpCompletionComplete {
			return fmt.Errorf("native Fountain review outcome: %+v", state.ReviewOutcome)
		}
		adopted, e := reviews.AdoptReview(ctx, state.Review, state.ReviewOutcome)
		if e != nil || adopted.Event != "COMMENT" || adopted.HeadOID != candidate || adopted.BaseOID != base || !factory.ValidCommit(adopted.MergeBaseOID) {
			return fmt.Errorf("native Fountain COMMENT receipt differs: %v", e)
		}
		if replay, e := reviews.SubmitReview(ctx, state.Review); e != nil || !reflect.DeepEqual(replay, state.ReviewOutcome) {
			return fmt.Errorf("native Fountain review replay: %v", e)
		}
		publication.ReviewOperations[len(publication.ReviewOperations)-1].Outcome = state.ReviewOutcome
		if err = write(); err != nil {
			return err
		}
		fmt.Println("PASS: actual native same-account COMMENT review over published candidate, exact head/base/merge-base/actor/grant and causal host receipt, original-command replay; verdict/run declared, no human vote or model inference")
	}
	encoded, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(statePath, encoded, 0600); err != nil {
		return err
	}
	if controller, err := controllerFixture(port.Config.Directory); err != nil {
		return err
	} else if controller != nil {
		policy, err := db.RepositoryPolicy(ctx, repository)
		if err != nil {
			return err
		}
		policy.Enabled = false
		if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
			return err
		}
	}
	fmt.Println("PASS: actual Go/native Git pack transfer, conditional new branch, causal receipt, PR creation/identity adoption and journal replay; run and local authority declared, no model or merge")
	return nil
}

// Original publication/run/authority are declared. The correction's Git lease,
// native transfer, journal reservation/receipt and cancellation are real.
func nativeCorrectionFixture(ctx context.Context, db *store.Store, port factoryruntime.NativePublication, actor collaboration.ActorID, issue int64, target factory.CheckTarget, candidate, cancelledCandidate string, bundle, cancelledBundle []byte, path, action string) error {
	statePath := filepath.Join(filepath.Dir(path), "native-correction.json")
	var state struct {
		Work, Cancelled           factory.PublicationWork
		Outcome, CancelledOutcome factory.OperationOutcome
	}
	if action != "check-background-correction" {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return err
		}
		out, err := port.LookupOp(ctx, state.Work.OperationID)
		if err != nil || !reflect.DeepEqual(out, state.Outcome) {
			return fmt.Errorf("retained correction receipt: %v", err)
		}
		if _, err = port.AdoptBranch(ctx, state.Work, out); err != nil {
			return err
		}
		out, err = port.SubmitPublish(ctx, state.Cancelled)
		if err != nil || !reflect.DeepEqual(out, state.CancelledOutcome) {
			return fmt.Errorf("retained correction cancellation: %v", err)
		}
		fmt.Println("PASS: correction receipt/adoption and cancelled send survive native restart/credential withdrawal without replay")
		return nil
	}
	owner, found := strings.CutPrefix(target.HeadRef, "refs/heads/soda/factory/")
	if !found || !factory.ValidID(owner) {
		return errors.New("correction fixture needs its owned Factory branch")
	}
	evidence, err := port.ReadAcceptanceEvidence(ctx, fmt.Sprint(target.Repository), fmt.Sprint(issue), nil)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	publication := declaredPublishedFixture(target, actor, owner, issue, evidence.Revision, now.Add(2*time.Minute).Unix())
	if err = publication.Validate(); err != nil {
		return err
	}
	rawDB, err := sql.Open("pgx", port.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	write := func() error {
		if err := publication.Validate(); err != nil {
			return fmt.Errorf("declared correction publication: %w", err)
		}
		raw, e := json.Marshal(publication)
		if e != nil {
			return e
		}
		_, e = rawDB.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(assignment) DO UPDATE SET run=EXCLUDED.run,stage=EXCLUDED.stage,revision=EXCLUDED.revision,data=EXCLUDED.data`, publication.AssignmentID, publication.Repository, publication.Issue, publication.Run, publication.Stage, publication.Revision, string(raw))
		return e
	}
	makeWork := func(index int, old, next string, bytes []byte) (factory.PublicationWork, error) {
		run := factory.Run{ID: factory.NewID(), ProjectID: publication.ProjectID, Role: project.RoleCoder, InputSHA: old, Started: now.Add(-time.Minute), Deadline: now.Add(time.Minute), Outcome: factory.Succeeded, Reconciled: true, Image: "sha256:" + strings.Repeat("1", 64), Harness: "declared-fixture", Model: "declared-fixture"}
		if e := run.Validate(); e != nil {
			return factory.PublicationWork{}, e
		}
		raw, e := json.Marshal(run)
		if e != nil {
			return factory.PublicationWork{}, e
		}
		if _, e = rawDB.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,FALSE,TRUE,$2)`, run.ID, string(raw)); e != nil {
			return factory.PublicationWork{}, e
		}
		return factory.PublicationWork{Bundle: bytes, AssignmentID: owner, Publication: publication.ID, RunID: run.ID, Run: run, Candidate: next, BaseSHA: publication.BaseSHA, TargetBranch: publication.TargetBranch, OperationID: factory.PublicationOperationID(publication.ID, factory.OpRefPublish, index), AuthRevision: "declared-native-correction-fixture", ExpectedOld: old, ComparisonRef: publication.TargetBranch, PRTitle: publication.PRCreate.Work.PRTitle, PRBody: publication.PRCreate.Work.PRBody, Repository: target.Repository, Issue: issue, ActorID: int64(actor), CorrectionNumber: target.PRNumber, CorrectionAuthor: int64(actor)}, nil
	}
	if err = write(); err != nil {
		return err
	}
	state.Work, err = makeWork(2, target.HeadOID, candidate, bundle)
	if err != nil {
		return err
	}
	observeAndFreeze := func(w *factory.PublicationWork) error {
		observed, e := port.ObservePublication(ctx, *w)
		if e != nil {
			return e
		}
		if observed.TargetTip != w.ExpectedOld || observed.Comparison != target.BaseOID {
			return errors.New("correction target differs")
		}
		w.NativeRev, w.ComparisonOID, w.NotAfter = observed.NativeRev, observed.Comparison, now.Add(2*time.Minute).Unix()
		intent := w.Intent()
		publication.Corrections = append(publication.Corrections, factory.PublicationOperation{Kind: factory.OpRefPublish, OperationID: w.OperationID, RunID: w.RunID, Work: &intent, Attempts: 1, UpdatedUnix: now.Unix()})
		return write()
	}
	if err = observeAndFreeze(&state.Work); err != nil {
		return err
	}
	if _, err = port.SubmitPublish(ctx, state.Work); err == nil {
		return errors.New("disabled policy authorized correction")
	}
	policy, err := db.RepositoryPolicy(ctx, target.Repository)
	if err != nil {
		return err
	}
	policy.Enabled = true
	for role, selection := range policy.Roles {
		selection.Account = "fixture-unavailable"
		policy.Roles[role] = selection
	}
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	wrong := state.Work
	wrong.ExpectedOld = target.BaseOID
	if _, err = port.SubmitPublish(ctx, wrong); err == nil {
		return errors.New("changed frozen correction lease accepted")
	}
	if prior, e := port.LookupOp(ctx, state.Work.OperationID); e != nil || !prior.NotObserved {
		return errors.New("refused correction acquired a native send")
	}
	state.Outcome, err = port.SubmitPublish(ctx, state.Work)
	if err != nil {
		return err
	}
	if state.Outcome.Effect != factory.OpEffectCommitted || state.Outcome.Completion != factory.OpCompletionComplete {
		return fmt.Errorf("native correction outcome: %+v", state.Outcome)
	}
	adopted, err := port.AdoptBranch(ctx, state.Work, state.Outcome)
	if err != nil {
		return err
	}
	if adopted.Ref != target.HeadRef || adopted.OldOID != target.HeadOID || adopted.NewOID != candidate || adopted.Comparison != target.BaseOID {
		return errors.New("correction receipt binding differs")
	}
	if replay, e := port.SubmitPublish(ctx, state.Work); e != nil || !reflect.DeepEqual(replay, state.Outcome) {
		return fmt.Errorf("correction replay: %v", e)
	}
	op := &publication.Corrections[0]
	op.InstallationID, op.ActorID, op.RepositoryID = state.Outcome.InstallationID, state.Outcome.ActorID, state.Outcome.RepositoryID
	op.Effect, op.Cancellation, op.Completion, op.Reason, op.Receipt = state.Outcome.Effect, state.Outcome.Cancellation, state.Outcome.Completion, state.Outcome.Reason, string(state.Outcome.Receipt)
	publication.Candidate, publication.Run = candidate, state.Work.RunID
	if err = write(); err != nil {
		return err
	}
	state.Cancelled, err = makeWork(3, candidate, cancelledCandidate, cancelledBundle)
	if err != nil {
		return err
	}
	if err = observeAndFreeze(&state.Cancelled); err != nil {
		return err
	}
	state.CancelledOutcome, err = port.CancelOp(ctx, state.Cancelled.OperationID)
	if err != nil {
		return err
	}
	if state.CancelledOutcome.Effect != factory.OpEffectNotCommitted || state.CancelledOutcome.Cancellation != factory.OpCancelCancelled {
		return errors.New("conditional ref cancellation incomplete")
	}
	if replay, e := port.SubmitPublish(ctx, state.Cancelled); e != nil || !reflect.DeepEqual(replay, state.CancelledOutcome) {
		return fmt.Errorf("cancelled correction sent: %v", e)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(statePath, encoded, 0600); err != nil {
		return err
	}
	fmt.Println("PASS: actual conditional correction ref/PR-head update, original head lease, causal receipt/replay, frozen-lease refusal and cancellation before capture; original workflow/run/authority declared, no model or merge")
	return nil
}

func declaredPublishedFixture(target factory.CheckTarget, actor collaboration.ActorID, assignment string, issue, nativeRev, notAfter int64) factory.Publication {
	p := factory.Publication{ID: factory.NewID(), AssignmentID: assignment, Repository: target.Repository, Issue: issue, Run: factory.NewID(), Stage: factory.PublicationPublished, Candidate: target.HeadOID, PRID: target.PRID, PRNumber: target.PRNumber,
		Publish: factory.PublicationOperation{Work: &factory.PublicationIntent{Candidate: target.HeadOID}}, PRCreate: factory.PublicationOperation{Work: &factory.PublicationIntent{ActorID: int64(actor)}, IssueID: target.IssueID, HeadRef: target.HeadRef, BaseRef: target.BaseRef, BaseOID: target.BaseOID}}
	now := time.Now().Unix()
	p.ProjectID = "p" + strings.Repeat("d", 24)
	p.Role = project.RoleCoder
	p.Acceptance = "d" + strings.Repeat("a", 24)
	p.Preparation = "f" + strings.Repeat("b", 24)
	p.BaseSHA = target.BaseOID
	p.TargetBranch = target.BaseRef
	p.Comparison = target.BaseOID
	p.NativeRev = nativeRev
	p.ObservedUnix = now
	p.CreatedUnix = now
	p.FinishedUnix = now
	p.Outcome = factory.Succeeded
	p.Reason = factory.PublishReasonLinked
	for _, operation := range []*factory.PublicationOperation{&p.Publish, &p.PRCreate} {
		operation.Kind = factory.OpRefPublish
		if operation == &p.PRCreate {
			operation.Kind = factory.OpPRCreate
			operation.HeadOID = target.HeadOID
			operation.PRNumber, operation.PRID = target.PRNumber, target.PRID
		}
		operation.OperationID = factory.PublicationOperationID(p.ID, operation.Kind, 1)
		operation.Attempts = 1
		operation.UpdatedUnix = now
		operation.Effect = factory.OpEffectCommitted
		operation.Completion = factory.OpCompletionComplete
		operation.Receipt = "declared publication fixture"
		operation.Work = &factory.PublicationIntent{OperationID: operation.OperationID, AuthRevision: "declared-publication-fixture", Repository: target.Repository, ActorID: int64(actor), NativeRev: nativeRev, NotAfter: notAfter, Candidate: target.HeadOID, TargetBranch: target.BaseRef, ExpectedOld: "absent", ComparisonRef: target.BaseRef, ComparisonOID: target.BaseOID, PRTitle: "Disposable native review recovery fixture"}
		if operation.Kind == factory.OpPRCreate {
			operation.Work.ExpectedOld = target.HeadOID
		}
	}

	return p
}

func nativeReviewFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeReviews, actor collaboration.ActorID, originalIssue int64, target factory.CheckTarget, path, action string) error {
	statePath := filepath.Join(filepath.Dir(path), "native-review.json")
	var state struct {
		Work, Cancelled, Uncertain, Expired factory.ReviewWork
		Outcome                             factory.OperationOutcome
		MergeBase                           string
	}
	preparing := action == "check-background-review-recovery-prepare"
	recovering := action == "check-background-review-recovery"
	if action != "check-background-review" && action != "check-background-merge" && !preparing {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return err
		}
		if recovering || action == "check-background-review-recovery-retained" {
			coordinator := control.NewCoordinator(db, nil, nil)
			coordinator.Reviews = port
			report := coordinator.PublishPass(ctx)
			if !report.Unavailable || len(report.Errors) != 0 {
				return fmt.Errorf("native review coordinator recovery: %+v", report)
			}
			publication, op, err := db.PublicationReviewOperation(ctx, state.Work.OperationID)
			if err != nil {
				return err
			}
			if !factory.ReviewWorkEqual(op.Work, state.Work) || op.Outcome.Effect != factory.OpEffectCommitted || op.Outcome.Completion != factory.OpCompletionComplete {
				return errors.New("coordinator did not recover the exact recorded review")
			}
			if recovering {
				state.Outcome = op.Outcome
				raw, err := json.Marshal(state)
				if err != nil {
					return err
				}
				if err = os.WriteFile(statePath, raw, 0600); err != nil {
					return err
				}
			}
			for _, item := range publication.ReviewOperations {
				if item.Work.OperationID == state.Expired.OperationID && !factory.ReviewWorkEqual(item.Work, state.Expired) {
					return errors.New("recovery renewed expired review work")
				}
			}
			if expired, err := port.LookupOp(ctx, state.Expired.OperationID); err != nil || !expired.NotObserved {
				return errors.New("expired review acquired a native send")
			}
			fmt.Println("PASS: actual Go publication pass recovers the original review without a publication writer; cancelled, uncertain and expired work remains held")
		}
		prior, err := port.LookupOp(ctx, state.Work.OperationID)
		if err != nil || !reflect.DeepEqual(prior, state.Outcome) {
			return fmt.Errorf("retained native review receipt changed: %w", err)
		}
		if _, err = port.AdoptReview(ctx, state.Work, prior); err != nil {
			return err
		}
		if state.MergeBase != "" {
			publication, _, err := db.PublicationReviewOperation(ctx, state.Work.OperationID)
			if err != nil || publication.Tuple == nil || publication.Tuple.Head != state.Work.HeadOID || publication.Tuple.Base != state.Work.BaseOID || publication.Tuple.DiffBase != state.MergeBase {
				return fmt.Errorf("retained native review candidate tuple differs: %w", err)
			}
		}
		cancelled, err := port.LookupOp(ctx, state.Cancelled.OperationID)
		if err != nil || cancelled.Effect != factory.OpEffectNotCommitted || cancelled.Cancellation != factory.OpCancelCancelled {
			return errors.New("retained cancelled review lost its fence")
		}
		uncertain, err := port.LookupOp(ctx, state.Uncertain.OperationID)
		if err != nil || uncertain.Effect != factory.OpEffectIndeterminate {
			return errors.New("retained reserved review lost its uncertainty")
		}
		fmt.Println("PASS: native review receipt/adoption, cancellation and unknown-send fence survive restart/credential withdrawal without a provider replay")
		return nil
	}
	assignment, run := factory.NewID(), factory.NewID()
	w := factory.ReviewWork{OperationID: "review-" + run, AuthRevision: factory.ReviewAuthRevision(assignment, run), Repository: target.Repository, ActorID: int64(actor), PRAuthorID: int64(actor), PRNumber: target.PRNumber, PRID: target.PRID, IssueID: target.IssueID, HeadRef: target.HeadRef, BaseRef: target.BaseRef, HeadOID: target.HeadOID, BaseOID: target.BaseOID, Event: "COMMENT", AgentVerdict: "APPROVED", Body: "Disposable Factory native automation integration fixture. Publication and independent verdict are declared test inputs; no model inspection ran.", NotAfter: time.Now().Add(2 * time.Minute).Unix()}
	observed, err := port.ObserveReview(ctx, w)
	if err != nil {
		return err
	}
	w.NativeRev = observed.NativeRev
	if w.Validate() != nil || observed.PRAuthorID != int64(actor) || observed.HeadOID != target.HeadOID || observed.BaseOID != target.BaseOID {
		return errors.New("native review preflight binding differs")
	}
	p := declaredPublishedFixture(target, actor, assignment, originalIssue, w.NativeRev, w.NotAfter)
	p.ReviewOperations = []factory.ReviewOperation{{RunID: run, Work: w}}
	now := time.Now().Unix()
	if preparing {
		state.Work = w
		clone := func() factory.ReviewWork {
			next := w
			r := factory.NewID()
			next.OperationID = "review-" + r
			next.AuthRevision = factory.ReviewAuthRevision(assignment, r)
			p.ReviewOperations = append(p.ReviewOperations, factory.ReviewOperation{RunID: r, Work: next})
			return next
		}
		state.Cancelled = clone()
		state.Uncertain = clone()
		state.Expired = clone()
		state.Expired.NotAfter = now - 1
		p.ReviewOperations[len(p.ReviewOperations)-1].Work = state.Expired
	}
	if err = p.Validate(); err != nil {
		return fmt.Errorf("declared review publication shape: %w", err)
	}
	write := func() error {
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		conn, err := sql.Open("pgx", port.Config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6) ON CONFLICT(assignment) DO UPDATE SET data=EXCLUDED.data`, p.AssignmentID, p.Repository, p.Issue, p.Run, p.Stage, string(raw))
		return err
	}
	if err = write(); err != nil {
		return err
	}
	if _, err = port.SubmitReview(ctx, w); err == nil {
		return errors.New("disabled policy authorized automated review")
	}
	policy, err := db.RepositoryPolicy(ctx, target.Repository)
	if err != nil {
		return err
	}
	policy.Enabled = true
	policy.TargetBranch = target.BaseRef
	for role, selection := range policy.Roles {
		selection.Account = "fixture-unavailable"
		policy.Roles[role] = selection
	}
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	if preparing {
		if _, err = port.CancelOp(ctx, state.Cancelled.OperationID); err != nil {
			return err
		}
		repo, err := db.CollaborationRepository(ctx, collaboration.RepositoryID(w.Repository))
		if err != nil {
			return err
		}
		identity, err := db.CollaborationActor(ctx, actor)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte("factory.native.review\x00" + string(port.Config.Profile) + "\x00" + state.Uncertain.OperationID))
		q := factory.HumanReviewCommand{ID: hex.EncodeToString(sum[:16]), Host: repo.Host, Repository: repo.ID, Actor: actor, Number: w.PRNumber, Body: w.Body, Event: factory.HumanReviewComment, ExpectedHead: collaboration.CommitID(w.HeadOID)}
		capture := factory.HumanReviewCapture{Request: q, Authority: "native-github:" + string(port.Config.Profile) + ":" + identity.ExternalID, OperationID: q.ID, ProviderIntent: []byte(`{"declared_reserved_review":true}`)}
		capture, _, err = db.CaptureHumanReview(ctx, capture)
		if err != nil {
			return err
		}
		cmd, err := capture.Command()
		if err != nil {
			return err
		}
		lookup, err := db.LookupHumanCommandDispatch(ctx, cmd)
		if err != nil {
			return err
		}
		if _, err = db.ObserveHumanReview(ctx, capture, lookup); err != nil {
			return err
		}
		if won, err := db.ReserveHumanReviewDispatch(ctx, capture); err != nil || !won {
			return errors.New("declared unknown-send reservation failed")
		}
		coordinator := control.NewCoordinator(db, nil, nil)
		coordinator.Reviews = nativeReviewWithoutRecovery{port}
		if listed, err := db.OpenPublications(ctx, 256); err != nil || len(listed) != 1 {
			return fmt.Errorf("shared pending-review selection: %v", err)
		}
		report := coordinator.PublishPass(ctx)
		if !report.Unavailable || len(report.Errors) != 0 {
			return fmt.Errorf("generic review recovery failed: %+v", report)
		}
		if prior, err := port.LookupOp(ctx, w.OperationID); err != nil || !prior.NotObserved {
			return errors.New("generic lookup absence authorized a review send")
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err = os.WriteFile(statePath, raw, 0600); err != nil {
			return err
		}
		fmt.Println("PASS: exact unclaimed review retained before restart; generic lookup absence, cancelled, declared reserved and expired cases authorize no send")
		return nil
	}
	outcome, err := port.SubmitReview(ctx, w)
	if err != nil {
		return err
	}
	if outcome.Effect != factory.OpEffectCommitted || outcome.Completion != factory.OpCompletionComplete || len(outcome.Receipt) == 0 {
		return fmt.Errorf("native review was not attributable/complete: %s %s %s", outcome.Effect, outcome.Completion, outcome.Reason)
	}
	adopted, err := port.AdoptReview(ctx, w, outcome)
	if err != nil || adopted.ReviewID <= 0 || adopted.Event != "COMMENT" || adopted.ReviewerID != int64(actor) || adopted.BaseOID != w.BaseOID || adopted.CommitID != w.HeadOID || !factory.ValidCommit(adopted.MergeBaseOID) {
		return fmt.Errorf("native comment review adoption differs: %w", err)
	}
	replay, err := port.SubmitReview(ctx, w)
	if err != nil || !reflect.DeepEqual(replay, outcome) {
		return fmt.Errorf("native review replay changed or retried: %w", err)
	}
	if action == "check-background-merge" {
		p.ReviewOperations[0].Outcome = outcome
		// The native transport fixture declares a typed, settled reviewer. It
		// must satisfy the same current-cycle join as real role output; a
		// provider COMMENT alone is not an independent inspection verdict.
		a := offlineRoleAssignment(project.ExecutionReview, project.RoleReviewer, p.Candidate)
		a.PublicationAssignment, a.AttemptRoot, a.ParentAssignment = p.AssignmentID, p.AssignmentID, p.AssignmentID
		a.Repository, a.Issue, a.ActorID = p.Repository, p.Issue, int64(actor)
		a.Subject.Issue.Issue = p.Issue
		a.Run, a.RunHistory = run, []string{run}
		a.Stage, a.Outcome, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, now
		a.Reason = factory.AssignReasonReported
		cycle := factory.ReviewCycle{PublicationID: p.ID, AttemptRoot: p.AssignmentID, Round: 1, Phase: "review", TriggerDecision: "declared-native-fixture", TriggerReason: "Controlled transport qualification; no model inspection", Head: p.Candidate, BaseOID: target.BaseOID, FindingDigest: strings.Repeat("a", 64), ReviewerAssignment: a.ID}
		cycle.ID = factory.ReviewCycleID(p.ID, p.AssignmentID, 1, cycle.Phase)
		content, e := factory.FindingContentDigest(nil)
		if e != nil {
			return e
		}
		coverage, e := factory.ClearCoverageDigest(cycle.Head, cycle.BaseOID, content)
		if e != nil {
			return e
		}
		a.Result = &factory.AssignmentResult{AssignmentID: a.ID, RunID: run, Status: "completed", Summary: "Declared transport fixture verdict", Reported: true, RecordedUnix: now, Phase: project.ExecutionReview, PhaseResult: &factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed", Summary: "Declared transport fixture verdict", Output: &factory.PhaseOutput{Clear: &factory.ClearReviewReport{Conclusion: factory.ClearReviewConclusion, CycleID: cycle.ID, Head: cycle.Head, BaseOID: cycle.BaseOID, CoverageDigest: coverage}}}}
		if e = a.Validate(); e != nil {
			return e
		}
		p.ReviewCycles = []factory.ReviewCycle{cycle}
		raw, e := json.Marshal(a)
		if e != nil {
			return e
		}
		conn, e := sql.Open("pgx", port.Config.DatabaseDSN())
		if e != nil {
			return e
		}
		defer conn.Close()
		if _, e = conn.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, a.ID, a.Repository, a.Issue, a.Run, a.Stage, string(raw)); e != nil {
			return e
		}
		owner := offlineRoleAssignment(project.ExecutionImplement, project.RoleCoder, target.BaseOID)
		owner.ID, owner.AttemptRoot, owner.PublicationAssignment = p.AssignmentID, p.AssignmentID, p.AssignmentID
		owner.Repository, owner.Issue, owner.ActorID = p.Repository, p.Issue, int64(actor)
		owner.Subject.Issue.Issue = p.Issue
		if e = owner.Validate(); e != nil {
			return e
		}
		raw, e = json.Marshal(owner)
		if e != nil {
			return e
		}
		if _, e = conn.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, owner.ID, owner.Repository, owner.Issue, owner.Run, owner.Stage, string(raw)); e != nil {
			return e
		}
		if err = write(); err != nil {
			return err
		}
		return nativeMergeFixture(ctx, db, factoryruntime.NativeMerges{NativeInputSource: port.NativeInputSource}, p, adopted, path)
	}
	clone := func() factory.ReviewWork {
		next := w
		r := factory.NewID()
		next.OperationID = "review-" + r
		next.AuthRevision = factory.ReviewAuthRevision(assignment, r)
		p.ReviewOperations = append(p.ReviewOperations, factory.ReviewOperation{RunID: r, Work: next})
		return next
	}
	state.Work, state.Outcome = w, outcome
	state.MergeBase = adopted.MergeBaseOID
	p.ReviewOperations[0].Outcome = outcome
	state.Cancelled = clone()
	state.Uncertain = clone()
	if err = write(); err != nil {
		return err
	}
	cancelled, err := port.CancelOp(ctx, state.Cancelled.OperationID)
	if err != nil || cancelled.Effect != factory.OpEffectNotCommitted || cancelled.Cancellation != factory.OpCancelCancelled {
		return fmt.Errorf("native review pre-send cancellation: %w", err)
	}
	if repeated, err := port.SubmitReview(ctx, state.Cancelled); err != nil || repeated.Effect != factory.OpEffectNotCommitted {
		return errors.New("cancelled native review was submitted")
	}
	// Declare a lost send reply by reserving the existing exact native capture.
	// Neither this check nor recovery may turn that reservation into another send.
	repo, err := db.CollaborationRepository(ctx, collaboration.RepositoryID(w.Repository))
	if err != nil {
		return err
	}
	request := func(work factory.ReviewWork) factory.HumanReviewCommand {
		sum := sha256.Sum256([]byte("factory.native.review\x00" + string(port.Config.Profile) + "\x00" + work.OperationID))
		return factory.HumanReviewCommand{ID: hex.EncodeToString(sum[:16]), Host: repo.Host, Repository: repo.ID, Actor: actor, Number: work.PRNumber, Body: work.Body, Event: factory.HumanReviewComment, ExpectedHead: collaboration.CommitID(work.HeadOID)}
	}
	prior, err := db.HumanReviewCapture(ctx, request(w))
	if err != nil {
		return err
	}
	prior.Request = request(state.Uncertain)
	prior.OperationID = prior.Request.ID
	prior, _, err = db.CaptureHumanReview(ctx, prior)
	if err != nil {
		return err
	}
	cmd, err := prior.Command()
	if err != nil {
		return err
	}
	lookup, err := db.LookupHumanCommandDispatch(ctx, cmd)
	if err != nil {
		return err
	}
	if _, err = db.ObserveHumanReview(ctx, prior, lookup); err != nil {
		return err
	}
	if won, err := db.ReserveHumanReviewDispatch(ctx, prior); err != nil || !won {
		return errors.New("uncertain review fixture did not reserve")
	}
	if unknown, err := port.SubmitReview(ctx, state.Uncertain); err != nil || unknown.Effect != factory.OpEffectIndeterminate {
		return errors.New("reserved unknown native review retried")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(statePath, raw, 0600); err != nil {
		return err
	}
	coordinator := control.NewCoordinator(db, nil, nil)
	coordinator.Reviews = nativeReviewWithoutRecovery{port}
	report := coordinator.PublishPass(ctx)
	if !report.Unavailable || len(report.Errors) != 0 {
		return fmt.Errorf("native review tuple recovery: %+v", report)
	}
	publication, _, err := db.PublicationReviewOperation(ctx, w.OperationID)
	if err != nil || publication.Tuple == nil || publication.Tuple.Head != w.HeadOID || publication.Tuple.Base != w.BaseOID || publication.Tuple.DiffBase != adopted.MergeBaseOID {
		return fmt.Errorf("completed native receipt did not seed its candidate tuple: %w", err)
	}
	fmt.Println("PASS: actual native same-account COMMENT review and merge base, completed-receipt tuple recovery, replay, cancellation and unknown-send hold; publication/verdict fixture supplied")
	return nil
}

type nativeMergeFixtureState struct {
	Work, Cancelled factory.MergeWork
	Outcome         factory.OperationOutcome
	Adopted         factory.MergeOutcome
	Confirmation    factory.MergeConfirmation
}

func nativeMergeRetained(ctx context.Context, db *store.Store, port factoryruntime.NativeMerges, path string) error {
	var state nativeMergeFixtureState
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "native-merge.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	out, err := port.LookupOp(ctx, state.Work.OperationID)
	if err != nil || !reflect.DeepEqual(out, state.Outcome) {
		return errors.New("retained merge receipt differs")
	}
	adopted, err := port.AdoptMerge(ctx, state.Work, out)
	if err != nil || !reflect.DeepEqual(adopted, state.Adopted) {
		return fmt.Errorf("retained merge adoption differs: %w", err)
	}
	cancelled, err := port.SubmitMerge(ctx, state.Cancelled)
	if err != nil || cancelled.Effect != factory.OpEffectNotCommitted || cancelled.Cancellation != factory.OpCancelCancelled {
		return fmt.Errorf("cancelled merge acquired another send: %w", err)
	}
	fmt.Println("PASS: native merge receipt/adoption and pre-send cancellation survive restart/credential withdrawal without provider replay")
	return nil
}

func nativeMergeFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeMerges, p factory.Publication, review factory.ReviewOutcome, path string) error {
	var options struct {
		Checks struct {
			Paired      bool     `json:"paired"`
			MergeChecks []string `json:"merge_checks"`
			Method      string   `json:"method"`
			Phases      []struct {
				Name                                                string
				WantError                                           bool
				MethodAvailable, ProviderComplete, ProviderEligible bool
			} `json:"phases"`
		}
	}
	encodedOptions, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(encodedOptions, &options); err != nil {
		return err
	}
	if len(options.Checks.Phases) > 16 {
		return errors.New("too many connected merge phases")
	}
	policy, err := db.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return err
	}
	policy.Checks = options.Checks.MergeChecks
	if len(policy.Checks) == 0 {
		policy.Checks = []string{"factory-native/merge"}
	}
	if options.Checks.Method != "" {
		policy.MergeMethod = options.Checks.Method
	}
	policy.Enabled = true
	for role, selection := range policy.Roles {
		selection.Account = "fixture-unavailable"
		policy.Roles[role] = selection
	}
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	policy, err = db.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return err
	}
	if controller, e := controllerFixture(port.Config.Directory); e != nil {
		return e
	} else if controller != nil {
		open, revision, withdrawal, e := db.DispatchState(ctx, p.Repository)
		if e != nil {
			return e
		}
		if !open {
			if len(withdrawal.ActiveCauses) != 1 || withdrawal.ActiveCauses[0] != "policy_disabled" {
				return errors.New("unrelated native merge fixture dispatch hold")
			}
			if _, _, e = db.ReopenDispatch(ctx, p.Repository, revision); e != nil {
				return e
			}
		}
	}
	w := factory.MergeWork{Method: policy.MergeMethod, MergeID: factory.NewID(), PublicationID: p.ID, OperationID: factory.MergeOperationIDForGeneration(p.ID, 1), AuthRevision: "declared-native-merge-fixture", Repository: p.Repository, Issue: p.Issue, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID, PRAuthorID: review.ReviewerID, ReviewerID: review.ReviewerID, ActorID: review.ReviewerID, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID, ReviewID: review.ReviewID, ReviewEvent: "COMMENT", NotAfter: time.Now().Add(5 * time.Minute).Unix()}
	initial := factory.Merge{ID: w.MergeID, PublicationID: p.ID, AssignmentID: p.AssignmentID, ProjectID: p.ProjectID, Role: p.Role, Acceptance: p.Acceptance, Authority: p.Authority, Repository: w.Repository, Issue: w.Issue, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, PRAuthorID: w.PRAuthorID, ReviewerID: w.ReviewerID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID, Stage: factory.MergeOpen, CreatedUnix: time.Now().Unix(), NativeRev: p.NativeRev, ObservedUnix: time.Now().Unix()}
	initial.Operation.Kind = factory.OpMerge
	if err = db.RecordMerge(ctx, initial); err != nil {
		return err
	}
	for n, phase := range options.Checks.Phases {
		ready := filepath.Join(filepath.Dir(path), fmt.Sprintf("merge-phase-%02d", n))
		if err = os.WriteFile(ready+".ready", []byte(phase.Name), 0600); err != nil {
			return err
		}
		for {
			if _, err = os.Stat(ready + ".continue"); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		observed, observeErr := port.ObserveMerge(ctx, w)
		receipt := struct {
			Name        string
			Observation factory.MergeObservation
			Error       string
		}{Name: phase.Name, Observation: observed}
		if observeErr != nil {
			receipt.Error = observeErr.Error()
		}
		encoded, e := json.Marshal(receipt)
		if e != nil {
			return e
		}
		if e = os.WriteFile(ready+".json", encoded, 0600); e != nil {
			return e
		}
		if (observeErr != nil) != phase.WantError || observeErr == nil && (observed.MethodAvailable != phase.MethodAvailable || observed.ProviderComplete != phase.ProviderComplete || observed.ProviderEligible != phase.ProviderEligible) {
			return fmt.Errorf("connected merge phase %s differs: %+v %v", phase.Name, observed, observeErr)
		}
		fmt.Println("PASS: connected merge phase", phase.Name)
	}
	// Phase observations precede admission; freeze a new bounded lease only now.
	w.NotAfter = time.Now().Add(5 * time.Minute).Unix()
	var observed factory.MergeObservation
	if options.Checks.Paired {
		evidence, e := port.ReadAcceptanceEvidence(ctx, strconv.FormatInt(w.Repository, 10), strconv.FormatInt(w.Issue, 10), nil)
		if e != nil {
			return e
		}
		w.NativeRev = evidence.Revision
		material, continuation, e := port.ReadDeliveryMaterialForMerge(ctx, p, w)
		if e != nil {
			return e
		}
		if !material.Eligible || continuation == nil {
			return errors.New("paired merge material unavailable")
		}
		observed, err = continuation(w)
		if err == nil {
			if _, e = continuation(w); e == nil {
				return errors.New("paired merge observation replayed")
			}
		}
	} else {
		observed, err = port.ObserveMerge(ctx, w)
	}
	if os.Getenv("FACTORY_FOUNTAIN_CONTROLLER_MERGE") == "1" {
		for {
			if err == nil && observed.MethodAvailable && observed.ProviderComplete && observed.ProviderEligible {
				target := factory.CheckTarget{Repository: w.Repository, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID}
				assessment, e := factory.VerifyChecks(target, factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}, policy, observed.Checks, time.Now().Unix())
				if e == nil && assessment.Verdict == factory.CheckPass {
					break
				}
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("actual Actions did not reach eligible required checks: observed=%+v err=%v: %w", observed, err, ctx.Err())
			case <-time.After(500 * time.Millisecond):
			}
			observed, err = port.ObserveMerge(ctx, w)
		}
	}
	if err != nil {
		return err
	}
	if !observed.MethodAvailable || !observed.ProviderComplete || !observed.ProviderEligible {
		return fmt.Errorf("native merge evidence unavailable: method=%v complete=%v eligible=%v", observed.MethodAvailable, observed.ProviderComplete, observed.ProviderEligible)
	}
	if os.Getenv("FACTORY_FOUNTAIN_CONTROLLER_MERGE") == "1" {
		material, e := port.ReadDeliveryMaterial(ctx, w)
		if e != nil {
			return fmt.Errorf("actual native delivery discussion: %w", e)
		}
		if !material.Eligible || len(material.Reviews) == 0 || material.Head != w.HeadOID || material.Base != w.BaseOID {
			return errors.New("actual native delivery material omitted the reviewed candidate")
		}
		raw, e := json.Marshal(material)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(filepath.Dir(path), "native-delivery-material.json"), raw, 0600); e != nil {
			return e
		}
		fmt.Println("PASS: actual public Fountain discussion and current review opinions feed the canonical delivery material owner before merge; no model verdict declared as inference")
	}
	w.NativeRev = observed.NativeRev
	target := factory.CheckTarget{Repository: w.Repository, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID}
	assessment, err := factory.VerifyChecks(target, factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}, policy, observed.Checks, time.Now().Unix())
	if err != nil || assessment.Verdict != factory.CheckPass {
		return fmt.Errorf("native merge check evidence: %s %w", assessment.Verdict, err)
	}
	assessment, err = db.RecordCheckAssessment(ctx, assessment)
	if err != nil {
		return err
	}
	w.AssessmentRevision = assessment.Revision
	m := factory.Merge{ID: w.MergeID, PublicationID: p.ID, AssignmentID: p.AssignmentID, ProjectID: p.ProjectID, Role: p.Role, Acceptance: p.Acceptance, Authority: p.Authority, Repository: w.Repository, Issue: w.Issue, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, PRAuthorID: w.PRAuthorID, ReviewerID: w.ReviewerID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID, Stage: factory.MergeOpen, CreatedUnix: time.Now().Unix(), NativeRev: w.NativeRev, ObservedUnix: time.Now().Unix()}
	// Local generation/phase admission is declared. Provider rules, review,
	// check evidence, conditional mutation and causal receipts are real.
	m.Authority.Policy = policy.Revision
	m.Authorization = factory.MergeAuthorization{Generation: factory.MergeGeneration{Authority: m.Authority, PublicationID: p.ID, Generation: 1, NativeOperationID: factory.MergeGenerationOperationID(p.ID, 1), Fingerprint: strings.Repeat("a", 64), Head: w.HeadOID, Base: w.BaseOID, DiffBase: w.BaseOID, ReviewRound: 1, AssessmentRevision: assessment.Revision, ChecksDigest: factory.ChecksDigest(policy.Checks), ObservedNativeRev: w.NativeRev, NotAfter: w.NotAfter, AcceptanceID: factory.NewID(), AcceptanceKind: factory.AcceptanceCleanReview, AttemptRoot: p.AssignmentID, OriginReceipt: factory.NewID()}, ResolutionsConfirmed: true}
	intent := w.Intent()
	m.Operation = factory.MergeOperation{Kind: factory.OpMerge, OperationID: w.OperationID, Attempts: 1, UpdatedUnix: time.Now().Unix(), Work: &intent}
	if err = writeDeclaredNativeMerge(ctx, port, m); err != nil {
		return err
	}
	changed := w
	changed.HeadOID = strings.Repeat("c", 40)
	if _, err = port.SubmitMerge(ctx, changed); err == nil {
		return errors.New("native merge accepted changed frozen intent")
	}
	state := nativeMergeFixtureState{Work: w}
	state.Outcome, err = port.SubmitMerge(ctx, w)
	if err != nil {
		return err
	}
	if state.Outcome.Effect != factory.OpEffectCommitted || state.Outcome.Completion != factory.OpCompletionComplete {
		return fmt.Errorf("native merge effect: %s %s %s", state.Outcome.Effect, state.Outcome.Completion, state.Outcome.Reason)
	}
	return finishNativeMergeFixture(ctx, db, port, m, state, path)
}

func writeDeclaredNativeMerge(ctx context.Context, port factoryruntime.NativeMerges, m factory.Merge) error {
	if err := m.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	conn, err := sql.Open("pgx", port.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6) ON CONFLICT(publication) DO UPDATE SET data=EXCLUDED.data WHERE factory_merges.data->>'id'=EXCLUDED.data->>'id' AND COALESCE((factory_merges.data->'operation'->>'attempts')::int,0)=0`, m.PublicationID, m.Repository, m.Issue, m.PRNumber, m.Stage, string(raw))
	return err
}

func finishNativeMergeFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeMerges, m factory.Merge, state nativeMergeFixtureState, path string) error {
	w := state.Work
	var err error
	statePath := filepath.Join(filepath.Dir(path), "native-merge.json")
	if raw, e := os.ReadFile(statePath); e == nil {
		var prior nativeMergeFixtureState
		if json.Unmarshal(raw, &prior) != nil || !reflect.DeepEqual(prior.Work, state.Work) || !reflect.DeepEqual(prior.Outcome, state.Outcome) {
			return errors.New("original merge state differs; preserve it")
		}
		state = prior
		if prior.Cancelled.OperationID != "" {
			return nativeMergeRetained(ctx, db, port, path)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	state.Adopted, err = port.AdoptMerge(ctx, w, state.Outcome)
	if err != nil {
		return err
	}
	if (w.Method == factory.MergeFastForward) != (state.Adopted.MergedCommit == w.HeadOID) {
		return errors.New("merge fixture result does not match its exact selected method")
	}
	replay, err := port.SubmitMerge(ctx, w)
	if err != nil || !reflect.DeepEqual(replay, state.Outcome) {
		return errors.New("native merge replay changed")
	}
	rawProgress, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(statePath, rawProgress, 0600); err != nil {
		return err
	}
	for {
		state.Confirmation, err = port.ObserveCompletion(ctx, w)
		if err == nil && state.Confirmation.MergedCommit == state.Adopted.MergedCommit && state.Confirmation.BaseContainsMergedCommit && state.Confirmation.IssueClosed && state.Confirmation.OriginalClosed && state.Confirmation.OriginalClosingOccurrence != "" {
			break
		}
		if os.Getenv("FACTORY_ASSEMBLY_MERGE") == "1" {
			return fmt.Errorf("declared merge completion differs: observed=%+v err=%v", state.Confirmation, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("original committed merge completion remains held: observed=%+v err=%v: %w", state.Confirmation, err, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if state.Confirmation.MergedCommit != state.Adopted.MergedCommit || !state.Confirmation.BaseContainsMergedCommit || !state.Confirmation.IssueClosed || !state.Confirmation.OriginalClosed || state.Confirmation.OriginalClosingOccurrence == "" {
		return fmt.Errorf("native merge completion differs: %+v", state.Confirmation)
	}
	cancelled := m
	cancelled.ID, cancelled.PublicationID = factory.NewID(), factory.NewID()
	cancelled.Authorization.Generation.PublicationID = cancelled.PublicationID
	cancelled.Authorization.Generation.NativeOperationID = factory.MergeGenerationOperationID(cancelled.PublicationID, 1)
	state.Cancelled = w
	state.Cancelled.MergeID, state.Cancelled.PublicationID, state.Cancelled.OperationID = cancelled.ID, cancelled.PublicationID, factory.MergeOperationIDForGeneration(cancelled.PublicationID, 1)
	cancelledIntent := state.Cancelled.Intent()
	cancelled.Operation.OperationID, cancelled.Operation.Work = state.Cancelled.OperationID, &cancelledIntent
	if err = writeDeclaredNativeMerge(ctx, port, cancelled); err != nil {
		return err
	}
	// Reuse the existing unclaimed cancellation fixture to qualify the durable
	// policy fence. The completed merge above must never be sent again.
	policy, err := db.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return err
	}
	policy.Paused = true
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	_, refusal := port.SubmitMerge(ctx, state.Cancelled)
	var wait *factory.PublicationWait
	if !errors.As(refusal, &wait) || (wait.Reason != "merge_not_admitted" && wait.Reason != "dispatch_closed") {
		return fmt.Errorf("policy loss did not fence unclaimed merge: %v", refusal)
	}
	if prior, e := port.LookupOp(ctx, state.Cancelled.OperationID); e != nil || !prior.NotObserved {
		return fmt.Errorf("policy loss captured a provider command: %v", e)
	}
	policy, err = db.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return err
	}
	policy.Paused = false
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	policy, err = db.RepositoryPolicy(ctx, w.Repository)
	if err != nil {
		return err
	}
	assessment, err := db.CheckAssessment(ctx, w.Repository, w.PRNumber)
	if err != nil {
		return err
	}
	if factory.VerifyMergeCheckEvidence(state.Cancelled, assessment, policy) == nil {
		return errors.New("restored policy renewed old check authority")
	}
	policyReceipt, err := json.Marshal(struct {
		Operation, Reason             string
		AssessedPolicy, CurrentPolicy int64
	}{state.Cancelled.OperationID, wait.Reason, assessment.PolicyRevision, policy.Revision})
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), "native-policy-fence.json"), policyReceipt, 0600); err != nil {
		return err
	}
	if _, err = port.CancelOp(ctx, state.Cancelled.OperationID); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), "native-merge.json"), raw, 0600); err != nil {
		return err
	}
	if err = nativeMergeRetained(ctx, db, port, path); err != nil {
		return err
	}
	fmt.Println("PASS: actual native exact-head merge, method-bound result adoption, issue/base completion, replay and cancellation; local workflow authorization declared, no model/full coordinator")
	fmt.Println("PASS: durable policy pause refuses unclaimed merge before capture; policy restoration keeps old assessment stale and cancellation terminal")
	return nil
}

func nativeSchemaFixture(profile, phase string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	raw, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer raw.Close()
	var version, tables int
	var hosting, appliance bool
	if err = raw.QueryRowContext(ctx, `SELECT version,application_hosting,appliance_compatibility FROM schema_version`).Scan(&version, &hosting, &appliance); err != nil {
		return err
	}
	if version != store.SchemaVersion() || hosting || appliance {
		return errors.New("new native database enabled appliance application hosting")
	}
	if err = raw.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND (table_name='applications' OR table_name LIKE 'application\_%' ESCAPE '\' OR table_name LIKE 'identity\_%' ESCAPE '\' OR table_name IN ('users','keys','projects','memberships','project_member_access','grant_key_check','project_lifecycle_grants','project_maintenance'))`).Scan(&tables); err != nil {
		return err
	}
	if tables != 0 {
		return errors.New("standalone database created appliance compatibility tables")
	}
	observed, err := store.OpenObserve(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	err = observed.IntegrityCheck(ctx)
	observed.Close()
	if err != nil {
		return err
	}
	if projects, err := db.SpaceProjects(ctx); err != nil || len(projects) != 0 {
		return errors.New("standalone appliance association inventory failed")
	}
	if _, err := db.NativeProjectForCollaborationRepository(ctx, 1); !errors.Is(err, store.ErrNotFound) {
		return errors.New("standalone appliance association did not report no local mapping")
	}
	if err = raw.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE contype='f' AND confrelid='factory_native_workspaces'::regclass AND conrelid::regclass::text IN ('project_preparations','project_requirement_decisions','project_approval_decisions','project_requirement_heads','project_approval_heads')`).Scan(&tables); err != nil || tables != 5 {
		return errors.New("standalone setup journals lost their native workspace references")
	}
	if wrong, err := store.OpenContext(ctx, config.DatabaseDSN()); err == nil {
		wrong.Close()
		return errors.New("appliance opener accepted a standalone database")
	}
	if _, err = raw.ExecContext(ctx, `SELECT id,data FROM factory_runs LIMIT 0; SELECT id,repository FROM factory_native_workspaces LIMIT 0`); err != nil {
		return err
	}
	// Keep one product identity across the real native restart.
	if phase == "restart" {
		retained, err := db.CollaborationProfile(ctx, config.Profile)
		if err != nil || retained.Name != "retained standalone schema fixture" {
			return errors.New("native restart lost the standalone product identity")
		}
		fmt.Println("PASS: standalone schema and product identity survive actual native restart")
		return nil
	}
	if err = db.PutCollaborationProfile(ctx, collaboration.Profile{ID: config.Profile, Name: "retained standalone schema fixture"}); err != nil {
		return err
	}

	retained, dsn, cleanup, err := store.OpenEphemeral(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer cleanup()
	if err = retained.UpsertUser(ctx, store.User{ID: 71, Login: "retained-owner", Name: "Retained owner"}); err != nil {
		return err
	}
	if err = retained.CreateProject(ctx, store.Project{ID: "retained-project", Name: "Retained project", RepositoryID: 91, OwnerID: 71, Repository: "retained/project"}); err != nil {
		return err
	}
	if err = retained.AddKey(ctx, 71, "public-fixture-only", "fixture-fingerprint"); err != nil {
		return err
	}
	if err = retained.Join(ctx, "retained-project", 71, "retained-owner"); err != nil {
		return err
	}
	retained.Close()
	legacy, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer legacy.Close()
	if _, err = legacy.ExecContext(ctx, `BEGIN;
INSERT INTO applications(id,administrator_id,name,data_id,owner_team,origin,hostname,port,revision) VALUES('retained-app',71,'retained app','retained-data','retained-team','https://retained.invalid','retained.invalid',21001,1);
INSERT INTO application_revisions(application_id,revision,spec) VALUES('retained-app',1,'{"fixture":"preserve verbatim"}');
INSERT INTO identity_connections(id,owner_id,generation,state,data,credential) VALUES('retained-connection',71,1,'active','{"fixture":true}',decode('0102','hex'));
ALTER TABLE schema_version DROP COLUMN application_hosting;
ALTER TABLE schema_version DROP COLUMN appliance_compatibility;
UPDATE schema_version SET version=92;
COMMIT`); err != nil {
		return err
	}
	upgraded, err := store.OpenStandaloneContext(ctx, dsn)
	if err != nil {
		return err
	}
	user, err := upgraded.User(ctx, 71)
	if err != nil || user.Login != "retained-owner" || user.Name != "Retained owner" {
		return errors.New("retained schema upgrade changed appliance identity")
	}
	keys, err := upgraded.Keys(ctx, 71)
	if err != nil || len(keys) != 1 || keys[0].Public != "public-fixture-only" {
		return errors.New("retained upgrade changed saved key rows")
	}
	if login, err := upgraded.MemberLogin(ctx, "retained-project", 71); err != nil || login != "retained-owner" {
		return errors.New("retained upgrade changed membership")
	}
	upgraded.Close()
	observer, err := store.OpenObserve(ctx, dsn)
	if err != nil {
		return err
	}
	projects, err := observer.SpaceProjects(ctx)
	observer.Close()
	if err != nil || len(projects) != 1 || projects[0].Name != "Retained project" {
		return errors.New("read-only observer lost retained appliance schema composition")
	}
	var credential string
	if err = legacy.QueryRowContext(ctx, `SELECT encode(credential,'hex') FROM identity_connections WHERE id='retained-connection'`).Scan(&credential); err != nil || credential != "0102" {
		return errors.New("retained upgrade changed dummy credential bytes")
	}
	if err = legacy.QueryRowContext(ctx, `SELECT version,application_hosting,appliance_compatibility FROM schema_version`).Scan(&version, &hosting, &appliance); err != nil {
		return err
	}
	if version != store.SchemaVersion() || !hosting || !appliance {
		return errors.New("retained upgrade lost application hosting obligation")
	}
	var name, spec string
	if err = legacy.QueryRowContext(ctx, `SELECT a.name,r.spec->>'fixture' FROM applications a JOIN application_revisions r ON r.application_id=a.id AND r.revision=a.revision WHERE a.id='retained-app'`).Scan(&name, &spec); err != nil {
		return err
	}
	if name != "retained app" || spec != "preserve verbatim" {
		return errors.New("retained application rows changed")
	}
	if _, err = legacy.ExecContext(ctx, `DROP TRIGGER application_identity_immutable ON applications`); err != nil {
		return err
	}
	if invalid, err := store.OpenStandaloneContext(ctx, dsn); err == nil {
		invalid.Close()
		return errors.New("retained native open bypassed required application guard verification")
	}
	fmt.Println("PASS: actual schema 92 upgrade preserves identity/application rows and refuses missing retained hosting guard; standalone omits hosting and rejects appliance open")
	return nil
}

type initialIntakeSource struct {
	view          control.AcceptanceIssueView
	creatorWrite  bool
	duringRead    func()
	reads, checks int
}

func (s *initialIntakeSource) ReadAcceptanceEvidence(context.Context, string, string, []string) (control.AcceptanceEvidence, error) {
	s.reads++
	if s.duringRead != nil {
		change := s.duringRead
		s.duringRead = nil
		change()
	}
	return control.AcceptanceEvidence{Revision: 1, Issue: s.view}, nil
}

func (s *initialIntakeSource) CheckInitialCreator(context.Context, control.IntakeHint) error {
	s.checks++
	if !s.creatorWrite {
		return store.ErrAdmissionChanged
	}
	return nil
}

type repositoryPriorityChooser struct {
	repository int64
	payer      string
	calls      []factory.PriorityRequest
	admissions int
}

func (c *repositoryPriorityChooser) CheckPriority(_ context.Context, request factory.PriorityRequest) error {
	c.admissions++
	if request.Repository != c.repository || len(request.Candidates) != 2 {
		return errors.New("priority request did not retain its complete repository scope")
	}
	for _, candidate := range request.Candidates {
		if candidate.Control.Repository != c.repository || candidate.Authority.SponsorshipConnection != c.payer {
			return errors.New("priority request crossed the declared repository/paying source")
		}
	}
	return nil
}

func (c *repositoryPriorityChooser) Choose(ctx context.Context, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
	if err := c.CheckPriority(ctx, request); err != nil {
		return factory.PriorityAnswer{}, err
	}
	c.calls = append(c.calls, request)
	selected := request.Candidates[1].ID
	return factory.PriorityAnswer{Selected: selected, Confidence: 1, Model: c.payer,
		Probabilities: map[string]float64{request.Candidates[0].ID: 0, selected: 1}}, nil
}

type repositoryPriorityNoEffects struct{ calls int }

func (p *repositoryPriorityNoEffects) unexpected() error {
	p.calls++
	return errors.New("repository priority fixture reached an execution boundary")
}

func (p *repositoryPriorityNoEffects) LaunchRun(context.Context, execution.LaunchRequest) (execution.RunObservation, error) {
	return execution.RunObservation{}, p.unexpected()
}
func (p *repositoryPriorityNoEffects) InspectRun(context.Context, execution.RunAddress) (execution.RunObservation, error) {
	return execution.RunObservation{}, p.unexpected()
}
func (p *repositoryPriorityNoEffects) RetireRun(context.Context, execution.RunAddress) (execution.RunObservation, error) {
	return execution.RunObservation{}, p.unexpected()
}
func (p *repositoryPriorityNoEffects) InspectTool(context.Context, string) (execution.ToolObservation, error) {
	return execution.ToolObservation{}, p.unexpected()
}
func (p *repositoryPriorityNoEffects) ReadWorkspaceContext(context.Context, execution.PreparationContextRequest) (execution.PreparationContext, error) {
	return execution.PreparationContext{}, p.unexpected()
}
func (p *repositoryPriorityNoEffects) ReadCustody(context.Context, execution.RunAddress) (execution.CustodyObservation, error) {
	return execution.CustodyObservation{}, p.unexpected()
}
func (p *repositoryPriorityNoEffects) SealCustody(context.Context, execution.RunAddress) error {
	return p.unexpected()
}

// Declared provider availability, candidate evidence and payer choices exercise
// the actual dispatch rotation, durable scans and tournament on disposable PG.
// The selected issue stops at authority admission; execution ports reject calls.
func repositoryPriorityFixture() error {
	dsn, ok := store.TestFixtureDSN()
	if !ok {
		return errors.New("private PostgreSQL fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, retainedDSN, cleanup, err := store.OpenEphemeral(ctx, dsn)
	if err != nil {
		return err
	}
	defer cleanup()
	raw, err := sql.Open("pgx", retainedDSN)
	if err != nil {
		return err
	}
	defer raw.Close()
	const unavailable, ready, nativeRevision = int64(41), int64(42), int64(7)
	const payer = "declared-github-paying-source"
	now := time.Now()
	var candidates []factory.PriorityCandidate
	for _, subject := range [][2]int64{{unavailable, 1}, {ready, 1}, {ready, 2}} {
		repository, issue := subject[0], subject[1]
		acceptance := factory.Acceptance{ID: factory.InitialAcceptanceID(repository, strconv.FormatInt(issue, 10)),
			Repository: repository, IssueIndex: strconv.FormatInt(issue, 10), Approver: 7, NativeRev: nativeRevision,
			TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), ContentVersion: 1}
		if err = db.AdmitAcceptanceDecision(ctx, acceptance); err != nil {
			return err
		}
		seen := now
		if repository == unavailable {
			seen = now.Add(-time.Minute)
		}
		observed, _, e := db.RecordIssueAssessment(ctx, factory.IssueControl{Repository: repository, Issue: issue,
			Acceptance: acceptance.ID, NativeRev: nativeRevision, Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
			Fingerprint: factory.MCPInputDigest([]byte(fmt.Sprintf("declared-priority:%d/%d", repository, issue))), Authority: strings.Repeat("c", 64)}, seen)
		if e != nil {
			return e
		}
		if repository == ready {
			candidates = append(candidates, factory.PriorityCandidate{ID: store.PrioritySubjectKey(repository, issue), Control: observed,
				Acceptance: acceptance, Objective: fmt.Sprintf("Declared GitHub objective %d", issue),
				Authority: factory.AuthorityRef{SponsorshipConnection: payer}, Preparation: strings.Repeat("d", 32),
				PreparationRevision: 1, Source: strings.Repeat("e", 40), PlannedMinutes: 1, PromptDigest: strings.Repeat("f", 64)})
		}
	}
	reservation := factory.Reservation{AssignmentID: strings.Repeat("1", 32), Repository: unavailable,
		Connection: "declared-fountain-paying-source", State: factory.ReservationHeld, PlannedMinutes: 1, Revision: 3}
	if err = reservation.Validate(); err != nil {
		return err
	}
	reservationData, err := json.Marshal(reservation)
	if err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO factory_reservations(assignment,repository,connection,state,data) VALUES($1,$2,$3,$4,$5)`,
		reservation.AssignmentID, reservation.Repository, reservation.Connection, reservation.State, string(reservationData)); err != nil {
		return err
	}
	legacy, err := db.CapturePriorityInventory(ctx, 0)
	if err != nil {
		return err
	}
	if _, err = db.SavePriorityScan(ctx, 0, nativeRevision-1, legacy); err != nil {
		return err
	}
	if err = db.RecordPriorityProgress(ctx, 0, nativeRevision-1, legacy.LocalDigest,
		[]store.PriorityCandidateObservation{{Subject: store.PrioritySubjectKey(unavailable, 1), Repository: unavailable, Issue: 1, Exclusion: "declared_legacy_observation"}}, 1); err != nil {
		return err
	}
	retainedRows := func() (string, error) {
		var saved string
		e := raw.QueryRowContext(ctx, `SELECT jsonb_build_object(
			'control',(SELECT to_jsonb(c) FROM issue_controls c WHERE repository=$1 AND issue=1),
			'reservation',(SELECT to_jsonb(r) FROM factory_reservations r WHERE assignment=$2),
			'global_scan',(SELECT to_jsonb(s) FROM factory_priority_scans s WHERE scan='current'),
			'global_candidates',(SELECT jsonb_agg(to_jsonb(o) ORDER BY subject) FROM factory_priority_candidates o WHERE scan='current'))::text`,
			unavailable, reservation.AssignmentID).Scan(&saved)
		return saved, e
	}
	before, err := retainedRows()
	if err != nil {
		return err
	}
	inventory, err := db.CapturePriorityInventory(ctx, ready)
	if err != nil || len(inventory.Subjects) != 2 {
		return fmt.Errorf("ready repository inventory: subjects=%d: %w", len(inventory.Subjects), err)
	}
	if _, err = db.SavePriorityScan(ctx, ready, nativeRevision, inventory); err != nil {
		return err
	}
	var observations []store.PriorityCandidateObservation
	for _, candidate := range candidates {
		encoded, e := json.Marshal(candidate)
		if e != nil {
			return e
		}
		observations = append(observations, store.PriorityCandidateObservation{Subject: candidate.ID, Repository: ready,
			Issue: candidate.Control.Issue, Candidate: encoded, Fingerprint: candidate.Control.Fingerprint})
	}
	if err = db.RecordPriorityProgress(ctx, ready, nativeRevision, inventory.LocalDigest, observations, len(observations)); err != nil {
		return err
	}
	chooser := &repositoryPriorityChooser{repository: ready, payer: payer}
	ports := &repositoryPriorityNoEffects{}
	refreshes, authorities := 0, 0
	refreshFailure := false
	deps := control.DispatchDeps{Store: db, Host: ports, Broker: ports, Queue: control.NewDispatchQueueCursor(),
		Priority:        &control.PriorityJudgments{Journal: db, Chooser: chooser},
		ObserveRevision: func(context.Context) (int64, bool, error) { return nativeRevision, true, nil },
		RefreshCandidate: func(_ context.Context, observed factory.IssueControl, revision int64) (factory.IssueControl, bool, error) {
			refreshes++
			if observed.Repository != unavailable || revision != nativeRevision {
				return observed, false, errors.New("completed scoped observations were repeated")
			}
			if refreshFailure {
				return observed, false, errors.New("declared unavailable readiness source")
			}
			return observed, false, nil
		},
		Authority: func(_ context.Context, repository int64, role string) (factory.EffectiveAuthority, error) {
			authorities++
			if repository != ready || role != project.RoleCoder {
				return factory.EffectiveAuthority{}, errors.New("foreign dispatch reached authority admission")
			}
			return factory.EffectiveAuthority{}, nil
		}}
	checkPasses := func() error {
		blocked := control.DispatchPass(ctx, deps)
		if len(blocked.Launched) != 0 || !slices.ContainsFunc(blocked.Errors, func(e control.DispatchError) bool {
			return e.Repository == unavailable && e.Reason == "priority_incomplete"
		}) {
			return fmt.Errorf("older unavailable repository did not pause its own scan: %+v", blocked)
		}
		if refreshFailure && !slices.ContainsFunc(blocked.Errors, func(e control.DispatchError) bool {
			return e.Repository == unavailable && e.Issue == 1 && e.Reason == control.DispatchErrHost
		}) {
			return fmt.Errorf("priority readiness failure was hidden: %+v", blocked)
		}
		paused, e := db.LoadPriorityScan(ctx, unavailable)
		if e != nil || paused.Cursor != 0 || len(paused.Subjects) != 1 || len(paused.Observations) != 0 {
			return fmt.Errorf("unavailable repository scan advanced: %+v: %v", paused, e)
		}
		if len(blocked.Waits) != 1 || blocked.Waits[0].Repository != ready || blocked.Waits[0].Issue != 2 || blocked.Waits[0].Reason != control.WaitAuthority ||
			slices.ContainsFunc(blocked.Errors, func(e control.DispatchError) bool { return e.Repository == ready }) {
			return fmt.Errorf("independent repository did not reach selection in the same pass: %+v", blocked)
		}
		return nil
	}
	if err = checkPasses(); err != nil {
		return err
	}
	if len(chooser.calls) != 1 || refreshes != 1 || authorities != 1 || ports.calls != 0 {
		return fmt.Errorf("first rotation calls: choices=%d refreshes=%d authorities=%d execution=%d", len(chooser.calls), refreshes, authorities, ports.calls)
	}
	snapshot, err := factory.PrioritySnapshot(candidates)
	if err != nil {
		return err
	}
	receipt, err := db.PriorityJudgment(ctx, snapshot)
	if err != nil || receipt.Outcome != "selected" || receipt.Selected != candidates[1].ID || len(receipt.Candidates) != 2 ||
		len(receipt.Calls) != 1 || receipt.Calls[0].Request.Repository != ready || receipt.Calls[0].Answer.Model != payer {
		return fmt.Errorf("scoped tournament receipt missing: %+v: %v", receipt, err)
	}
	if err = db.Close(); err != nil {
		return err
	}
	db, err = store.Open(retainedDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	deps.Store, deps.Priority.Journal, deps.Queue = db, db, control.NewDispatchQueueCursor()
	refreshFailure = true
	admissions := chooser.admissions
	if err = checkPasses(); err != nil {
		return err
	}
	replayed, err := db.PriorityJudgment(ctx, snapshot)
	if err != nil || !reflect.DeepEqual(receipt, replayed) || len(chooser.calls) != 1 || chooser.admissions <= admissions ||
		refreshes != 2 || authorities != 2 || ports.calls != 0 {
		return fmt.Errorf("reopened receipt did not replay without another choice: choices=%d refreshes=%d authorities=%d execution=%d: %v", len(chooser.calls), refreshes, authorities, ports.calls, err)
	}
	after, err := retainedRows()
	if err != nil || before != after {
		return fmt.Errorf("foreign control, reservation or historical global scan changed: %v", err)
	}
	deps.Queue = control.NewDispatchQueueCursor()
	deps.RefreshCandidate = func(_ context.Context, observed factory.IssueControl, _ int64) (factory.IssueControl, bool, error) {
		return observed, true, nil
	}
	deps.Authority = func(context.Context, int64, string) (factory.EffectiveAuthority, error) {
		return factory.EffectiveAuthority{}, errors.New("declared local authority read failure")
	}
	failed := control.DispatchPass(ctx, deps)
	if len(failed.Launched) != 0 || !slices.ContainsFunc(failed.Errors, func(e control.DispatchError) bool {
		return e.Repository == unavailable && e.Issue == 1 && e.Reason == control.DispatchErrStore && e.Detail == "authority unreadable"
	}) || ports.calls != 0 || len(chooser.calls) != 1 {
		return fmt.Errorf("priority admission failure was hidden or drove effects: %+v", failed)
	}
	fmt.Println("PASS: unavailable older repository pauses alone; the same pass reaches the next repository's two-candidate tournament with its declared payer; reopen replays the receipt without another choice; foreign control/reservation and legacy global scan stay exact; no execution/provider/model calls")
	fmt.Println("PASS: priority readiness and local admission failures retain the exact repository/issue reason without launching, repeating a judgment or advancing the failed scan")
	return nil
}

// Declared assignments and provider acceptance exercise the real issue/control
// read services and PostgreSQL joins. No execution or provider port is available.
func workReadbackFixture() error {
	dsn, ok := store.TestFixtureDSN()
	if !ok {
		return errors.New("private PostgreSQL fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, privateDSN, cleanup, err := store.OpenEphemeral(ctx, dsn)
	if err != nil {
		return err
	}
	defer cleanup()
	raw, err := sql.Open("pgx", privateDSN)
	if err != nil {
		return err
	}
	defer raw.Close()
	now := time.Now().UTC()
	root := offlineRoleAssignment(project.ExecutionInvestigate, project.RoleCoder, strings.Repeat("1", 40))
	acceptance := factory.Acceptance{ID: root.Acceptance, Repository: root.Repository, IssueIndex: "1", Approver: 7, NativeRev: 1, TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), ContentVersion: 1}
	if err := db.AdmitAcceptanceDecision(ctx, acceptance); err != nil {
		return err
	}
	if _, _, err := db.RecordIssueAssessment(ctx, factory.IssueControl{Repository: root.Repository, Issue: root.Issue, Acceptance: root.Acceptance, Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible, Fingerprint: strings.Repeat("c", 64), Authority: strings.Repeat("d", 64)}, now); err != nil {
		return err
	}
	finish := func(assignment factory.Assignment) factory.Assignment {
		assignment.Stage, assignment.Outcome, assignment.Reason, assignment.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, now.Unix()
		assignment.Result = &factory.AssignmentResult{AssignmentID: assignment.ID, RunID: assignment.Run, Status: "completed", Summary: "Declared completed work", Candidate: strings.Repeat("2", 40), Reported: true, RecordedUnix: now.Unix()}
		return assignment
	}
	root = finish(root)
	writeAssignment := func(assignment factory.Assignment, insert bool) error {
		if err := assignment.Validate(); err != nil {
			return err
		}
		data, err := json.Marshal(assignment)
		if err != nil {
			return err
		}
		if insert {
			_, err = raw.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, assignment.ID, assignment.Repository, assignment.Issue, assignment.Run, assignment.Stage, assignment.Revision, string(data))
		} else {
			_, err = raw.ExecContext(ctx, `UPDATE factory_assignments SET stage=$2,data=$3 WHERE id=$1`, assignment.ID, assignment.Stage, string(data))
		}
		return err
	}
	writeRun := func(assignment factory.Assignment, settled bool) error {
		run := factory.Run{ID: assignment.Run, ProjectID: assignment.ProjectID, Role: assignment.Role, InputSHA: assignment.SourceCommit, Started: now, Deadline: now.Add(15 * time.Minute), Image: "sha256:" + strings.Repeat("b", 64), Harness: assignment.Harness, Model: assignment.Model, Reconciled: settled}
		if settled {
			run.Outcome = factory.Succeeded
		}
		if err := run.Validate(); err != nil {
			return err
		}
		data, err := json.Marshal(run)
		if err != nil {
			return err
		}
		if _, err = raw.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET active=excluded.active,settled=excluded.settled,data=excluded.data`, run.ID, !settled, settled, string(data)); err != nil {
			return err
		}
		_, err = raw.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4) ON CONFLICT(run) DO NOTHING`, run.ID, assignment.Repository, assignment.Issue, assignment.ID)
		return err
	}
	if err = writeAssignment(root, true); err != nil {
		return err
	}
	if err = writeRun(root, true); err != nil {
		return err
	}
	child := offlineRoleAssignment(project.ExecutionImplement, project.RoleCoder, root.SourceCommit)
	child.ParentAssignment, child.AttemptRoot, child.PublicationAssignment = root.ID, root.ID, root.ID
	if err = writeAssignment(child, true); err != nil {
		return err
	}
	if err = writeRun(child, false); err != nil {
		return err
	}
	publication := factory.Publication{ID: factory.NewID(), AssignmentID: root.ID, ProjectID: root.ProjectID, Role: root.Role, Acceptance: root.Acceptance, Preparation: root.Preparation, Run: root.Run, Repository: root.Repository, Issue: root.Issue, Candidate: strings.Repeat("2", 40), BaseSHA: root.SourceCommit, TargetBranch: "refs/heads/main", Stage: factory.PublicationOpen, CreatedUnix: now.Unix(), Publish: factory.PublicationOperation{Kind: factory.OpRefPublish}, PRCreate: factory.PublicationOperation{Kind: factory.OpPRCreate}}
	if err = db.RecordPublication(ctx, publication); err != nil {
		return err
	}
	visible := func(_ context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
		if actor != 7 || int64(repository) != root.Repository {
			return factoryservice.ErrWorkJourneyScope
		}
		return nil
	}
	membership := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, workspace string) (bool, error) {
		if err := visible(ctx, actor, repository); err != nil || workspace != root.ProjectID {
			return false, factoryservice.ErrWorkJourneyScope
		}
		return false, nil
	}
	authority := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (factoryservice.WorkAuthority, error) {
		return factoryservice.WorkAuthority{SessionCurrent: true}, visible(ctx, actor, repository)
	}
	issues := factoryservice.Issues{Store: db, Visibility: visible, Authority: authority, Membership: membership,
		Status: func(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64) (control.AcceptanceValidity, error) {
			return control.AcceptanceValidity{Acceptance: &acceptance, Revision: acceptance.NativeRev, Valid: true}, nil
		}}
	controls := factoryservice.WorkControls{Store: db, Visibility: visible, Authority: authority, Membership: membership}
	read := func(owner, current factory.Assignment, wantStop bool, publicationID string) error {
		view, err := issues.Read(ctx, 7, collaboration.RepositoryID(root.Repository), root.Issue)
		if err != nil {
			return err
		}
		if view.Attempt == nil || view.Attempt.ID != current.ID || view.Attempt.Run != current.Run || view.Attempt.Stage != current.Stage || view.Attempt.Outcome != current.Outcome || view.Capabilities.Stop.Available != wantStop || view.Journey == nil || view.Journey.Lineage.RootAssignment != owner.ID || view.Journey.Lineage.AttemptRoot != owner.AttemptRoot || view.Journey.Lineage.LatestAssignment != current.ID || view.Journey.Lineage.LatestRun != current.Run {
			return errors.New("current work execution and publication-owner lineage diverged")
		}
		if publicationID == "" && view.Publication != nil || publicationID != "" && (view.Publication == nil || view.Publication.ID != publicationID || view.Publication.Run != owner.Run) {
			return errors.New("current execution replaced its publication owner")
		}
		found := false
		for _, phase := range view.Journey.Phases {
			if phase.Phase == current.Phase && phase.Assignment == current.ID && phase.Run == current.Run && phase.AttemptRoot == owner.ID && phase.State == map[bool]string{false: "complete", true: "active"}[wantStop] {
				found = true
			}
		}
		if !found {
			return errors.New("current work phase lost its exact child/run binding")
		}
		projected, err := controls.Read(ctx, 7, collaboration.RepositoryID(root.Repository), root.Issue)
		if err != nil || projected.Capabilities.Stop.Available != wantStop {
			return fmt.Errorf("independent work controls missed the current execution: %w", err)
		}
		return nil
	}
	if err = read(root, child, true, publication.ID); err != nil {
		return fmt.Errorf("active Builder: %w", err)
	}
	child = finish(child)
	if err = writeAssignment(child, false); err != nil {
		return err
	}
	if err = writeRun(child, true); err != nil {
		return err
	}
	if err = read(root, child, false, publication.ID); err != nil {
		return fmt.Errorf("settled Builder: %w", err)
	}
	for _, mutate := range []func(*factory.Assignment){
		func(a *factory.Assignment) { a.Repository++ },
		func(a *factory.Assignment) { a.ProjectID = "p" + strings.Repeat("9", 24) },
		func(a *factory.Assignment) {
			a.Acceptance = "d" + strings.Repeat("f", 24)
			a.Subject.Issue.Acceptance = a.Acceptance
		},
		func(a *factory.Assignment) { a.AttemptRoot = factory.NewID() },
		func(a *factory.Assignment) { a.PublicationAssignment = factory.NewID() },
	} {
		altered := child
		subject := *child.Subject.Issue
		altered.Subject.Issue = &subject
		mutate(&altered)
		if err = writeAssignment(altered, false); err != nil {
			return err
		}
		if _, err = issues.Read(ctx, 7, collaboration.RepositoryID(root.Repository), root.Issue); !errors.Is(err, factoryservice.ErrWorkJourneyScope) {
			return fmt.Errorf("foreign current execution did not refuse issue read: %v", err)
		}
		if _, err = controls.Read(ctx, 7, collaboration.RepositoryID(root.Repository), root.Issue); !errors.Is(err, factoryservice.ErrWorkJourneyScope) {
			return fmt.Errorf("foreign current execution did not refuse controls: %v", err)
		}
		if err = writeAssignment(child, false); err != nil {
			return err
		}
	}
	newRoot := finish(offlineRoleAssignment(project.ExecutionInvestigate, project.RoleCoder, root.SourceCommit))
	if err = writeAssignment(newRoot, true); err != nil {
		return err
	}
	if err = writeRun(newRoot, true); err != nil {
		return err
	}
	newChild := offlineRoleAssignment(project.ExecutionImplement, project.RoleCoder, root.SourceCommit)
	newChild.ParentAssignment, newChild.AttemptRoot, newChild.PublicationAssignment = newRoot.ID, newRoot.ID, newRoot.ID
	if err = writeAssignment(newChild, true); err != nil {
		return err
	}
	if err = writeRun(newChild, false); err != nil {
		return err
	}
	if err = read(newRoot, newChild, true, ""); err != nil {
		return fmt.Errorf("new independently selected root: %w", err)
	}
	lateOldChild := finish(offlineRoleAssignment(project.ExecutionImplement, project.RoleCoder, root.SourceCommit))
	lateOldChild.ParentAssignment, lateOldChild.AttemptRoot, lateOldChild.PublicationAssignment = root.ID, root.ID, root.ID
	if err = writeAssignment(lateOldChild, true); err != nil {
		return err
	}
	if _, err = issues.Read(ctx, 7, collaboration.RepositoryID(root.Repository), root.Issue); !errors.Is(err, factoryservice.ErrWorkJourneyScope) {
		return fmt.Errorf("late old-root child redirected current work: %v", err)
	}
	fmt.Println("PASS: current Builder and Stop capability follow the active child, then its actual settlement; publication/journey ownership stays exact; newer root selects its own child; foreign bindings and late old-root children refuse; declared source facts, real PostgreSQL/service reads, no execution/provider/model calls")
	return nil
}

type builderRetirementHost struct {
	factoryruntime.NativeExecution
	state execution.PreparationState
	stops int
}

func (h *builderRetirementHost) RetireWorkspacePreparation(ctx context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	if h.state.ValidateFor(address) != nil {
		return execution.PreparationState{}, errors.New("retirement crossed the declared checkout owner")
	}
	h.stops++
	state := h.state
	state.Phase, state.Ready, state.Stopped, state.Retirement = project.PrepareStopped, false, true, "confirmed"
	return state, ctx.Err()
}

// Declared retained assignments, native custody and provider receipts exercise
// the real PublishPass/PG handoff. No host setup, execution or provider is wired.
func builderRetirementFixture() error {
	dsn, ok := store.TestFixtureDSN()
	if !ok {
		return errors.New("private PostgreSQL fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, privateDSN, cleanup, err := store.OpenEphemeral(ctx, dsn)
	if err != nil {
		return err
	}
	defer cleanup()
	raw, err := sql.Open("pgx", privateDSN)
	if err != nil {
		return err
	}
	defer raw.Close()
	now := time.Now().UTC()
	root := offlineRoleAssignment(project.ExecutionInvestigate, project.RoleCoder, strings.Repeat("1", 40))
	tool := execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("b", 64), Executable: "/home/fixture/bin/muse"}
	root.NativeTool, root.Harness, root.HarnessVers = &tool, tool.Harness+"-"+tool.Version, tool.Version
	requirement := project.RequirementDecision{ID: "d" + strings.Repeat("c", 24), Project: root.ProjectID, Approver: root.ActorID, SourceCommit: root.SourceCommit, SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64)}
	approval := project.ApprovalDecision{ID: "d" + strings.Repeat("e", 24), Project: root.ProjectID, Requirement: requirement.ID, Approver: root.ActorID, EffectsDigest: strings.Repeat("a", 64), ReadinessDigest: strings.Repeat("b", 64), Verified: true}
	root.Authority = factory.AuthorityRef{RequirementsID: requirement.ID, ApprovalID: approval.ID, SponsorshipConnection: root.Connection}
	if err = db.UpsertUser(ctx, store.User{ID: root.ActorID, Login: "retirement-fixture"}); err != nil {
		return err
	}
	if err = db.CreateProject(ctx, store.Project{ID: root.ProjectID, Name: "retirement", RepositoryID: root.Repository, OwnerID: root.ActorID, Repository: "fixture/retirement"}); err != nil {
		return err
	}
	if err = db.AdmitRequirementDecision(ctx, requirement); err != nil {
		return err
	}
	if err = db.AdmitApprovalDecision(ctx, approval); err != nil {
		return err
	}
	owner := &execution.NativeWorkspaceRef{Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: root.Preparation}
	zero := 0
	preparation := execution.PreparationRecord{Preparation: project.Preparation{ID: root.Preparation, Project: root.ProjectID, Role: root.Role, SourceCommit: root.SourceCommit, SetupDigest: requirement.SetupDigest, Requirements: requirement.Ref(1), Approval: approval.Ref(1)}, Native: owner,
		State: execution.PreparationState{ID: root.Preparation, Project: root.ProjectID, Role: root.Role, SourceCommit: root.SourceCommit, SetupDigest: requirement.SetupDigest, Phase: project.PrepareReady, Ready: true, Native: owner, SetupExit: &zero, CheckExit: &zero}}
	if _, _, err = db.AdmitPreparation(ctx, preparation); err != nil {
		return err
	}
	meta := map[string]any{"repository": root.Repository, "issue": root.Issue, "actor_id": strconv.FormatInt(root.ActorID, 10), "project": root.ProjectID, "owner_assignment": root.ID, "child_assignment": root.ID, "attempt_root": root.ID, "publication_id": "", "connection": root.Connection, "authority": root.Authority, "not_after_unix": now.Add(time.Minute).Unix()}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission}',$2::jsonb) WHERE id=$1`, root.Preparation, string(metaBytes)); err != nil {
		return err
	}
	finish := func(a factory.Assignment) factory.Assignment {
		a.Stage, a.Outcome, a.Reason, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, now.Unix()
		a.Result = &factory.AssignmentResult{AssignmentID: a.ID, RunID: a.Run, Status: "completed", Summary: "Declared retained report", Candidate: strings.Repeat("2", 40), Reported: true, RecordedUnix: now.Unix()}
		return a
	}
	root = finish(root)
	child := root
	child.ID, child.Run, child.Phase, child.ParentAssignment = factory.NewID(), factory.NewID(), project.ExecutionImplement, root.ID
	child.RunHistory = []string{child.Run}
	child = finish(child)
	for _, a := range []factory.Assignment{root, child} {
		if err = a.Validate(); err != nil {
			return err
		}
		data, _ := json.Marshal(a)
		if _, err = raw.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, a.ID, a.Repository, a.Issue, a.Run, a.Stage, string(data)); err != nil {
			return err
		}
		run := factory.Run{ID: a.Run, ProjectID: a.ProjectID, Role: a.Role, InputSHA: a.SourceCommit, Started: now.Add(-time.Minute), Deadline: now.Add(time.Minute), Harness: a.Harness, Model: a.Model, Outcome: factory.Succeeded, Reconciled: true,
			Native: &execution.NativeRecord{Tool: tool, Retirement: execution.Retired, Sealed: true, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: owner.Profile, InstanceID: owner.InstanceID, WorktreeID: owner.WorktreeID, Custody: execution.NativeCustody{State: execution.NotAcquired}}}}
		if err = run.Validate(); err != nil {
			return err
		}
		data, _ = json.Marshal(run)
		if _, err = raw.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, run.ID, string(data)); err != nil {
			return err
		}
		if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: run.ID, Repository: a.Repository, Issue: a.Issue, Attempt: a.ID}); err != nil {
			return err
		}
	}
	target := factory.CheckTarget{Repository: root.Repository, PRNumber: 60, PRID: 160, IssueID: 260, HeadRef: factory.PublishBranchName(root.ID), BaseRef: "refs/heads/main", HeadOID: child.Result.Candidate, BaseOID: root.SourceCommit}
	p := declaredPublishedFixture(target, collaboration.ActorID(root.ActorID), root.ID, root.Issue, 1, now.Add(time.Minute).Unix())
	p.ProjectID, p.Acceptance, p.Preparation, p.Run, p.Authority = root.ProjectID, root.Acceptance, root.Preparation, child.Run, root.Authority
	if err = p.Validate(); err != nil {
		return err
	}
	pBytes, _ := json.Marshal(p)
	if _, err = raw.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, root.ID, root.Repository, root.Issue, child.Run, p.Stage, string(pBytes)); err != nil {
		return err
	}
	allowance := factory.AttemptAllowance{RootAssignment: root.ID, Repository: root.Repository, Issue: root.Issue, Limits: factory.DefaultAttemptLimits(), Corrections: []string{}, CheckpointUnix: now.Unix(), Active: true}
	allowanceBytes, _ := json.Marshal(allowance)
	if _, err = raw.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,0,$4)`, root.Repository, root.Issue, root.ID, string(allowanceBytes)); err != nil {
		return err
	}
	reviewer := factory.NewID()
	if consumed, exhausted, e := db.ChargeReviewDispatch(ctx, root.Repository, root.Issue, root.ID, reviewer, p.Candidate, false); e != nil || !consumed || exhausted {
		return fmt.Errorf("declared first review allocation: %t/%t: %v", consumed, exhausted, e)
	}
	evidence := func() (string, error) {
		var saved string
		e := raw.QueryRowContext(ctx, `SELECT jsonb_build_object('assignments',(SELECT jsonb_agg(to_jsonb(a) ORDER BY seq) FROM factory_assignments a),'runs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY seq) FROM factory_runs r),'publications',(SELECT jsonb_agg(to_jsonb(p) ORDER BY seq) FROM factory_publications p))::text`).Scan(&saved)
		return saved, e
	}
	before, err := evidence()
	if err != nil {
		return err
	}
	host := &builderRetirementHost{state: preparation.State}
	c := control.NewCoordinator(db, host, host)
	c.Publication = factoryruntime.NativePublication{}
	defer c.Close()
	for _, field := range []string{"attempt_root", "child_assignment"} {
		if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,ARRAY['factory_admission',$2],to_jsonb($3::text)) WHERE id=$1`, root.Preparation, field, factory.NewID()); err != nil {
			return err
		}
		report := c.PublishPass(ctx)
		if host.stops != 0 || !slices.ContainsFunc(report.Waits, func(w control.PublishWait) bool {
			return w.ID == p.ID && w.Reason == "candidate_preparation_retirement_pending"
		}) {
			return fmt.Errorf("foreign %s did not hold retirement: %+v", field, report)
		}
		if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission}',$2::jsonb) WHERE id=$1`, root.Preparation, string(metaBytes)); err != nil {
			return err
		}
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET settled=false,active=true WHERE id=$1`, root.Run); err != nil {
		return err
	}
	report := c.PublishPass(ctx)
	if host.stops != 0 || !slices.ContainsFunc(report.Waits, func(w control.PublishWait) bool {
		return w.ID == p.ID && w.Reason == "candidate_preparation_retirement_pending"
	}) {
		return fmt.Errorf("unsettled consumer did not hold retirement: %+v", report)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET settled=true,active=false WHERE id=$1`, root.Run); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		report = c.PublishPass(ctx)
		stored, e := db.CandidatePreparation(ctx, root.Preparation)
		clock, clockErr := db.AttemptAllowance(ctx, root.Repository, root.Issue)
		if e != nil || clockErr != nil || host.stops != 1 || !stored.Stopped || stored.Retirement != "confirmed" || clock.Closed || clock.Active || len(clock.Reviews) != 1 || clock.Reviews[0] != reviewer || clock.ReviewedHeads[p.Candidate] != reviewer {
			return fmt.Errorf("early retirement/replay lost custody or open review grant: stop=%d clock=%+v report=%+v errors=%v/%v", host.stops, clock, report, e, clockErr)
		}
	}
	after, err := evidence()
	if err != nil || before != after {
		return fmt.Errorf("retirement changed retained report/run/publication evidence: %v", err)
	}
	fmt.Println("PASS: PublishPass releases the exact private Builder checkout before next-role gates; wrong root/child and unsettled consumer hold; replay stops once, keeps the attempt open and preserves its allocated review and retained report/run/publication; native custody declared, no setup/execution/provider/model calls")
	return nil
}

// Provider creation/permission facts are declared; coordinator admission,
// causal journals, edit retention and acceptance persistence use real PostgreSQL.
func initialIntakeFixture() error {
	dsn, ok := store.TestFixtureDSN()
	if !ok {
		return errors.New("private PostgreSQL fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, _, cleanup, err := store.OpenEphemeral(ctx, dsn)
	if err != nil {
		return err
	}
	defer cleanup()
	policy := factory.RepositoryPolicy{Repository: 42, GrantedBy: 7, Enabled: true, TargetBranch: "refs/heads/main", MergeMethod: factory.MergeFastForward, Checks: []string{"fixture/build"},
		Roles:   map[string]factory.RoleSelection{project.RoleCoder: {Account: "chosen", Harness: project.FactoryHarnessMuse, Model: "declared"}, project.RoleReviewer: {Account: "chosen", Harness: project.FactoryHarnessMuse, Model: "declared"}},
		Publish: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpRefPublish}, Create: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpPRCreate},
		Review: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpReviewSubmit}, Merge: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpMerge},
		AttemptLimits: factory.DefaultAttemptLimits(), ReassessmentMinutes: factory.DefaultReassessmentMinutes}
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	source := &initialIntakeSource{creatorWrite: true, view: control.AcceptanceIssueView{ID: "101", Index: "1", PosterID: "7", CreatedUnix: 100,
		TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), LifecycleOccurrence: factory.OriginalOccurrence, Visible: true, Verified: true}}
	c := control.Coordinator{Store: db, AcceptanceReads: source}
	hint := control.IntakeHint{Delivery: "initial-intake-fixture", Repository: 42, Issue: 1, Creator: 7, Created: true, CreatorWrite: true, CreationVerified: true, NativeIssueID: 101, CreatedUnix: 100, TitleDigest: source.view.TitleDigest, ContentDigest: source.view.ContentDigest}
	decision, err := c.AdmitInitialAcceptance(ctx, hint)
	if err != nil || !decision.Initial || decision.Approver != 7 {
		return fmt.Errorf("owner's own issue was not admitted: %w", err)
	}
	if _, err := c.AdmitInitialAcceptance(ctx, hint); err != nil {
		return fmt.Errorf("initial acceptance replay: %w", err)
	}
	post, _, err := db.SaveFeedPost(ctx, factory.PostRequest{Repository: 42, Body: "Create an automated issue", IdempotencyKey: "initial-origin"}, "7", "Declared owner", 100)
	if err != nil {
		return err
	}
	if err := db.BeginPostIssueCreation(ctx, post, 0); err != nil {
		return err
	}
	source.view.ID, source.view.Index = "102", "2"
	hint.NativeIssueID, hint.Issue = 102, 2
	if _, err := c.AdmitInitialAcceptance(ctx, hint); !errors.Is(err, store.ErrReadinessPending) {
		return fmt.Errorf("unresolved create did not retain pending intake: %v", err)
	}
	if _, err := db.EditFeedPost(ctx, post, "7", "Edited while provider result was unknown", 0, 101); err != nil {
		return err
	}
	if err := db.RecordPostIssueCreation(ctx, post, 0, 2); err != nil {
		return err
	}
	if err := db.RecordPostIssueCreation(ctx, post, 0, 3); err == nil {
		return errors.New("conflicting creation result replaced recorded issue")
	}
	_, err = c.AdmitInitialAcceptance(ctx, hint)
	var refusal *control.AcceptanceRefusal
	if !errors.As(err, &refusal) || refusal.Reason != control.RefusalCreationFactory {
		return fmt.Errorf("recorded automated issue was not excluded: %v", err)
	}
	if _, err := db.AcceptanceHead(ctx, 42, 2); !errors.Is(err, store.ErrNotFound) {
		return errors.New("automatic issue unexpectedly acquired an initial acceptance")
	}
	source.view.ID, source.view.Index = "103", "3"
	hint.NativeIssueID, hint.Issue = 103, 3
	if _, err := c.AdmitInitialAcceptance(ctx, hint); err != nil {
		return fmt.Errorf("unrelated owner issue remained blocked after reconciliation: %w", err)
	}
	if err := db.BeginPostIssueCreation(ctx, post, 1); err != nil {
		return err
	}
	if err := db.RecordPostIssueCreation(ctx, post, 1, -1); err != nil {
		return err
	}
	maker, _, err := db.SaveFeedPost(ctx, factory.PostRequest{Repository: 42, Body: "Issue Maker source", IdempotencyKey: "maker-origin"}, "7", "Declared owner", 101)
	if err != nil {
		return err
	}
	if err := db.RecordIssueMakerIntake(ctx, maker, 0); err != nil {
		return err
	}
	pipe, err := db.PostIntake(ctx, maker)
	if err != nil {
		return err
	}
	next := pipe
	next.Judgment, next.Route = factory.PostJudgmentApplied, factory.PostRouteNewIssue
	if err := db.AdvancePostIntake(ctx, pipe, next); err != nil {
		return err
	}
	postDetail, err := db.FeedPost(ctx, maker)
	if err != nil {
		return err
	}
	progress, err := db.StartPostIssueMaker(ctx, postDetail)
	if err != nil {
		return err
	}
	mutation := factory.IssueMakerMutation{Action: "create", Title: "Declared task", Body: "Declared task body"}
	mutation, err = db.PrepareIssueMakerMutation(ctx, progress.Scope.Post, "create-task", factory.MCPInputDigest([]byte("create task")), "task", mutation)
	if err != nil {
		return err
	}
	if generated, pending, err := db.AutomaticIssueOrigin(ctx, 42, 4); err != nil || generated || !pending {
		return fmt.Errorf("Issue Maker pending origin: %t %t %v", generated, pending, err)
	}
	if err := db.RecordIssueMakerIssue(ctx, progress.Scope, "task", mutation.Slot, 4, mutation.Title, mutation.Body); err != nil {
		return err
	}
	if generated, pending, err := db.AutomaticIssueOrigin(ctx, 42, 4); err != nil || !generated || pending {
		return fmt.Errorf("Issue Maker result/refused root origin: %t %t %v", generated, pending, err)
	}
	// The original creation hint was writable. Change current permission while
	// its evidence read is in flight, before the final creator-authority check.
	source.view.ID, source.view.Index = "105", "5"
	hint.NativeIssueID, hint.Issue = 105, 5
	beforeReads, beforeChecks := source.reads, source.checks
	source.duringRead = func() { source.creatorWrite = false }
	if _, err := c.AdmitInitialAcceptance(ctx, hint); !errors.Is(err, store.ErrAdmissionChanged) {
		return fmt.Errorf("creator revoked during evidence I/O admitted original work: %v", err)
	}
	if source.reads != beforeReads+1 || source.checks != beforeChecks+1 {
		return errors.New("creator withdrawal did not cross the evidence/final-authority boundary")
	}
	if _, err := db.AcceptanceHead(ctx, 42, hint.Issue); !errors.Is(err, store.ErrNotFound) {
		return errors.New("in-flight creator withdrawal retained an acceptance head")
	}
	if _, err := db.AcceptanceDecision(ctx, factory.InitialAcceptanceID(42, "105")); !errors.Is(err, store.ErrNotFound) {
		return errors.New("in-flight creator withdrawal retained a decision")
	}
	source.creatorWrite = true
	if recovered, err := c.AdmitInitialAcceptance(ctx, hint); err != nil || recovered.ID != factory.InitialAcceptanceID(42, "105") {
		return fmt.Errorf("fresh creator authority did not recover unconsumed original work: %v", err)
	}
	// Re-enrollment cannot renew a response captured by an older policy. Change
	// real PostgreSQL policy while the controlled provider response is in flight.
	for index, reenroll := range []bool{false, true} {
		number := int64(6 + index)
		source.view.ID, source.view.Index = strconv.FormatInt(100+number, 10), strconv.FormatInt(number, 10)
		hint.NativeIssueID, hint.Issue = 100+number, number
		var mutationErr error
		source.duringRead = func() {
			current, e := db.RepositoryPolicy(ctx, 42)
			if e == nil {
				current.Enabled = false
				e = db.SaveRepositoryPolicy(ctx, current)
			}
			if e == nil && reenroll {
				current, e = db.RepositoryPolicy(ctx, 42)
				if e == nil {
					current.Enabled = true
					e = db.SaveRepositoryPolicy(ctx, current)
				}
			}
			mutationErr = e
		}
		if _, err := c.AdmitInitialAcceptance(ctx, hint); mutationErr != nil || !errors.Is(err, store.ErrAdmissionChanged) {
			return fmt.Errorf("in-flight policy withdrawal/re-enrollment admitted old response: mutation=%v admission=%v", mutationErr, err)
		}
		if _, err := db.AcceptanceHead(ctx, 42, number); !errors.Is(err, store.ErrNotFound) {
			return errors.New("withdrawn policy retained an acceptance head")
		}
		if _, err := db.AcceptanceDecision(ctx, factory.InitialAcceptanceID(42, source.view.ID)); !errors.Is(err, store.ErrNotFound) {
			return errors.New("withdrawn policy retained an acceptance decision")
		}
		current, err := db.RepositoryPolicy(ctx, 42)
		if err != nil {
			return err
		}
		if !current.Enabled {
			current.Enabled = true
			if err := db.SaveRepositoryPolicy(ctx, current); err != nil {
				return err
			}
		}
		if recovered, err := c.AdmitInitialAcceptance(ctx, hint); err != nil || recovered.ID != factory.InitialAcceptanceID(42, source.view.ID) {
			return fmt.Errorf("fresh policy did not recover unconsumed original issue: %v", err)
		}
	}
	if runs, err := db.FactoryRuns(ctx, 1); err != nil || len(runs) != 0 {
		return errors.New("initial acceptance or its refused I/O race launched execution")
	}
	fmt.Println("PASS: actual PostgreSQL policy withdrawal and withdrawal/re-enrollment during declared evidence I/O refuse the old capture with no decision/head or model run; fresh authority recovers the same original issue")
	// Exercise the real Feed/Post projection over these retained rows. Provider
	// permission changes are declared at the initial/final observation boundary;
	// no native request, inference or write authority is configured.
	if err := db.RecordPostIntake(ctx, post, 1); err != nil {
		return err
	}
	otherPost, otherRevision, err := db.SaveFeedPost(ctx, factory.PostRequest{Repository: 43, Body: "Another repository's visible post", IdempotencyKey: "feed-bracket-other"}, "7", "Declared owner", 102)
	if err != nil {
		return err
	}
	if err := db.RecordPostIntake(ctx, otherPost, otherRevision); err != nil {
		return err
	}
	feed := factoryservice.Feed{Store: db}
	if err := feed.SaveSubscription(ctx, 7, 0, factory.FeedSubscription{Repositories: []int64{42, 43}}); err != nil {
		return err
	}
	query := factory.FeedQuery{Repositories: []int64{42, 43}, Kinds: []string{factory.FeedKindPost}, Limit: 20}
	posts := factoryservice.Posts{Store: db}
	var complete factoryservice.FeedPage
	for pass := 0; pass < 2; pass++ {
		reads := make(map[collaboration.RepositoryID]int)
		page, err := feed.Page(ctx, 7, query, false, func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (string, error) {
			if actor != 7 || repository != 42 && repository != 43 {
				return "", errors.New("feed escaped its admitted actor/repository")
			}
			reads[repository]++
			return fmt.Sprintf("Declared repository %d observation %d", repository, reads[repository]), ctx.Err()
		}, posts)
		if err != nil || len(page.Entries) < 3 || reads[42] != 2 || reads[43] != 2 {
			return fmt.Errorf("feed page repeated distinct-repository observations or reused a prior response: pass=%d reads=%v entries=%d err=%v", pass, reads, len(page.Entries), err)
		}
		for _, entry := range page.Entries {
			post, ok := entry.Post.(map[string]any)
			if !ok || post["post_id"] != entry.ID || entry.Project != fmt.Sprintf("Declared repository %d observation 2", entry.Repository) {
				return errors.New("feed did not complete post projection before using fresh final visibility")
			}
		}
		complete = page
	}
	unknownVisibility := errors.New("declared current provider authority unavailable")
	for _, unknown := range []bool{false, true} {
		reads := make(map[collaboration.RepositoryID]int)
		page, err := feed.Page(ctx, 7, query, false, func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (string, error) {
			reads[repository]++
			if repository == 42 && reads[repository] == 2 {
				if unknown {
					return "", unknownVisibility
				}
				return "", factoryservice.ErrRepositoryHidden
			}
			return "Declared visible repository", ctx.Err()
		}, posts)
		if unknown {
			if !errors.Is(err, unknownVisibility) || len(page.Entries) != 0 {
				return errors.New("unknown final feed authority exposed a partial page")
			}
			continue
		}
		if err != nil || len(page.Entries) == 0 || reads[42] != 2 || reads[43] != 2 || page.Next != complete.Next || page.Truncated != complete.Truncated {
			return fmt.Errorf("final feed withdrawal changed cursor or repeated observations: reads=%v err=%v", reads, err)
		}
		for _, entry := range page.Entries {
			if entry.Repository != 43 {
				return errors.New("feed exposed rows after final repository withdrawal")
			}
		}
	}
	fmt.Println("PASS: configured Feed allowance diagnostics add no per-row/per-repository visibility reads, respect the effective subscription, and seal holds even on an empty page; declared withdrawal during local projection omits all holds with explicit supported-empty [], unknown authority refuses the response, and callback absence omits the field. No provider or inference effect.")
	fmt.Println("PASS: real PostgreSQL Feed/Post page assembly makes exactly two current visibility observations per distinct repository regardless of row count, repeats both for every response, uses fresh final names, filters all rows after final withdrawal without changing cursors, and refuses the entire page on unknown final authority. Provider authority changes declared; no inference/native/provider effect.")
	fmt.Println("PASS: creator permission revoked during controlled evidence I/O refuses initial acceptance without a decision/head; a fresh authorized read recovers the same original issue (provider permission declared; actual coordinator/PostgreSQL)")
	fmt.Println("PASS: same-account human intake, replay, unknown-result hold, retained origin after edit, recursion refusal and unrelated issue admission (declared provider facts; actual coordinator/PostgreSQL)")
	return nil
}

// The provider/account/repository permissions are declared fixture facts.
// Preparation, packet admission, public Go launch, worker and MCP transport,
// artifact persistence and process/custody receipts use their actual owners.
const nativeFixtureArtifactPrompt = "Publish an artifact with upload_id 11111111111111111111111111111111, name native-worker.txt, media_type text/plain, content_base64 YWN0dWFsIGFkbWl0dGVkIG5hdGl2ZSB3b3JrZXIgYXJ0aWZhY3QK, final true."

func admittedNativeFixture(profile, instance, path string, action string) error {
	if path != filepath.Join(profile, "admitted-native-fixture.json") {
		return errors.New("admitted fixture path differs")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var input struct {
		Run, Connection, Preparation, Executable, ParentRun string
		MuseLogin, Live, Withdraw, Tasks                    bool
		DeadlineUnix                                        int64
	}
	if json.Unmarshal(raw, &input) != nil || !factory.ValidID(input.Run) {
		return errors.New("admitted fixture input invalid")
	}
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	fixtureTimeout := 30 * time.Second
	if action == "settled-admitted-native" && input.Tasks && input.ParentRun != "" {
		fixtureTimeout = 60 * time.Second
		if os.Getenv("SODA_NATIVE_CHECK_CORRECTION") == "1" {
			fixtureTimeout = 150 * time.Second
		}
	}
	if action == "check-admitted-native" && input.Tasks && input.ParentRun != "" {
		fixtureTimeout = min(130*time.Second, max(fixtureTimeout, time.Until(time.Unix(input.DeadlineUnix, 0).Add(5*time.Second))))
	}
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	if action == "check-retained-native-fenced" {
		prior, err := db.FactoryRun(ctx, input.Run)
		if err != nil || prior.Reconciled || prior.Native == nil || prior.Native.Settled() {
			return errors.New("incomplete original run is not fenced")
		}
		assignment, err := db.AssignmentByRun(ctx, input.Run)
		if err != nil {
			return err
		}
		reservation, err := db.Reservation(ctx, assignment.ID)
		if err != nil {
			return err
		}
		usage, usageErr := db.RunUsage(ctx, input.Run)
		if usageErr != nil && !errors.Is(usageErr, store.ErrNotFound) {
			return usageErr
		}
		c := control.NewCoordinator(db, port, port)
		defer c.Close()
		report, err := c.Reconcile(ctx)
		if err != nil || slices.Contains(report.Settled, prior.ID) || !slices.ContainsFunc(report.Fenced, func(v control.FencedRun) bool { return v.ID == prior.ID }) {
			return fmt.Errorf("incomplete original run was not refused: %+v %v", report, err)
		}
		current, err := db.FactoryRun(ctx, input.Run)
		if err != nil || !reflect.DeepEqual(prior, current) {
			return errors.New("incomplete recovery changed original run facts")
		}
		a, err := db.AssignmentByRun(ctx, input.Run)
		if err != nil || !reflect.DeepEqual(assignment, a) {
			return errors.New("incomplete recovery invented an assignment result")
		}
		afterReservation, err := db.Reservation(ctx, a.ID)
		if err != nil || !reflect.DeepEqual(reservation, afterReservation) {
			return errors.New("incomplete recovery changed reserved capacity")
		}
		afterUsage, afterUsageErr := db.RunUsage(ctx, input.Run)
		if !reflect.DeepEqual(usage, afterUsage) || (usageErr == nil) != (afterUsageErr == nil) || afterUsageErr != nil && !errors.Is(afterUsageErr, store.ErrNotFound) {
			return errors.New("incomplete recovery invented usage")
		}
		fmt.Println("PASS: unconfirmed native evidence stays fenced; original run, recorded result, usage and reservation unchanged")
		return nil
	}
	if action == "acknowledge-native" {
		return port.AcknowledgeSettledRuns(ctx)
	}
	if action == "reconcile-retained-native" {
		prior, err := db.FactoryRun(ctx, input.Run)
		if err != nil {
			return err
		}
		if !input.MuseLogin {
			if prior.Native == nil {
				return errors.New("original native binding missing")
			}
			fmt.Printf("Original native recovery state: retirement=%s sealed=%t process=%t custody=%s reconciled=%t\n", prior.Native.Retirement, prior.Native.Sealed, prior.Native.Provenance.Process != nil, prior.Native.Provenance.Custody.State, prior.Reconciled)
			if !prior.Native.Settled() || prior.Native.Provenance.Process == nil {
				return errors.New("original native resources were not recovered")
			}
			coordinator := control.Coordinator{Store: db, Host: port, Broker: port}
			stopped, err := coordinator.Stop(ctx, input.Run)
			if err != nil || !stopped.Confirmed || stopped.Uncertain {
				return fmt.Errorf("original native run remains unsettled: %v %+v", err, stopped)
			}
			current, err := db.FactoryRun(ctx, input.Run)
			if err != nil || !current.Reconciled || current.Native == nil || !current.Native.Settled() || current.Native.Intent != prior.Native.Intent || !reflect.DeepEqual(current.Native.Provenance.Process, prior.Native.Provenance.Process) {
				return errors.New("native recovery changed or did not settle the original process")
			}
			assignment, err := db.AssignmentByRun(ctx, input.Run)
			if err != nil || assignment.Stage != factory.AssignmentFinished || assignment.Outcome != factory.NeedsHuman {
				return errors.New("native recovery did not preserve the recorded blocked product result")
			}
			reservation, err := db.Reservation(ctx, assignment.ID)
			if err != nil || reservation.State != factory.ReservationConsumed {
				return errors.New("native recovery left its reservation unsettled")
			}
			usage, err := db.RunUsage(ctx, input.Run)
			if err != nil || usage.Validate() != nil || usage.Connection != assignment.Connection || usage.Minutes != 1 || !usage.StartedAt.Equal(current.Started) || !usage.EndedAt.Equal(current.UsageEnd(time.Now())) {
				return errors.New("native recovery usage differs from the original admitted source/run")
			}
			terminal, found, err := db.TerminalMCPProposal(ctx, assignment.ID)
			if err != nil || !found || terminal.State != factory.MCPWorkAccepted {
				return errors.New("native recovery lost the original terminal proposal")
			}
			fmt.Println("PASS: exact original native run reconciled without relaunch; retained process/retirement, blocked product result, consumed reservation and original-source usage preserved; outcome", stopped.Outcome)
			return port.AcknowledgeSettledRuns(ctx)
		}
		if prior.Native == nil || !prior.Native.Settled() || prior.Native.Provenance.TrustMode != execution.NativeCLIProvider || prior.Native.Provenance.Process == nil {
			return errors.New("original CLI resources were not recovered")
		}
		coordinator := control.Coordinator{Store: db, Host: port, Broker: port}
		observed, err := port.InspectRun(ctx, execution.RunAddress{Run: input.Run, Workspace: prior.ProjectID})
		if err != nil || observed.Retirement != execution.Retired {
			return fmt.Errorf("retained native outcome unavailable: %v", err)
		}
		var expectedOutcome factory.Outcome
		switch observed.Phase {
		case project.FactoryCompleted:
			expectedOutcome = factory.Succeeded
		case project.FactoryFailed:
			expectedOutcome = factory.Failed
		case project.FactoryStopped:
			expectedOutcome = factory.Cancelled
		default:
			return errors.New("original native run has no confirmed terminal outcome")
		}
		completedTasks := input.Tasks && prior.FinalCheckpoint != nil && prior.FinalCheckpoint.Status == "committed"
		stopped, err := coordinator.Stop(ctx, input.Run)
		if err != nil || !stopped.Confirmed || stopped.Uncertain || stopped.Outcome != string(expectedOutcome) {
			return fmt.Errorf("original retained run remains unsettled: %v %+v", err, stopped)
		}
		current, err := db.FactoryRun(ctx, input.Run)
		if err != nil {
			return err
		}
		if !current.Reconciled || !current.Native.Settled() || current.Native.Intent != prior.Native.Intent || !reflect.DeepEqual(current.Native.Provenance.Process, prior.Native.Provenance.Process) {
			return errors.New("recovery changed the original native identity")
		}
		a, err := db.AssignmentByRun(ctx, input.Run)
		if err != nil {
			return err
		}
		if a.Stage == factory.AssignmentAssigned {
			if _, err = control.SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); err != nil {
				return err
			}
			a, err = db.Assignment(ctx, a.ID)
			if err != nil {
				return err
			}
		}
		var rawUsage []byte
		rawDB, err := sql.Open("pgx", config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer rawDB.Close()
		if err = rawDB.QueryRowContext(ctx, `SELECT data FROM factory_usage WHERE run=$1`, input.Run).Scan(&rawUsage); err != nil {
			return err
		}
		var usage factory.Usage
		if json.Unmarshal(rawUsage, &usage) != nil || usage.Validate() != nil || usage.Connection != a.Connection || !usage.EndedAt.Equal(prior.UsageEnd(time.Now())) || !usage.StartedAt.Equal(prior.Started) {
			return errors.New("recovered usage includes offline settlement delay")
		}
		if !input.Live {
			stopTime := prior.Native.Provenance.Process.StoppedAt
			_, found, terminalErr := db.TerminalMCPProposal(ctx, a.ID)
			expectedProduct := factory.NeedsHuman
			if expectedOutcome == factory.Failed {
				expectedProduct = factory.Failed
			}
			if !found && expectedOutcome == factory.Cancelled {
				expectedProduct = factory.Cancelled
			}
			if terminalErr != nil || stopTime == nil || !usage.EndedAt.Equal(*stopTime) || !stopTime.Before(prior.Deadline) || a.Outcome != expectedProduct {
				return errors.New("original early stop or incomplete product result was not retained")
			}
			if _, err = rawDB.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{native,provenance,process,stopped_at}',to_jsonb($2::text)) WHERE id=$1`, input.Run, stopTime.Add(time.Second).Format(time.RFC3339Nano)); err == nil || !strings.Contains(err.Error(), "native process stop cannot change") {
				return fmt.Errorf("database did not enforce the recorded stop timestamp: %v", err)
			}
			fmt.Println("PASS: original early-stopped process recovered without inference replay; usage ends at its retained immutable native stop, excluding offline delay; product outcome", a.Outcome)
		} else if input.Tasks {
			terminal, found, err := db.TerminalMCPProposal(ctx, a.ID)
			reservation, reservationErr := db.Reservation(ctx, a.ID)
			expectedProduct := factory.NeedsHuman
			if !found && expectedOutcome == factory.Failed {
				expectedProduct = factory.Failed
			}
			if found && (completedTasks && terminal.Tool == factory.MCPToolFinishImplement || terminal.Tool == factory.MCPToolFinishIssuePlan) {
				expectedProduct = factory.Succeeded
			}
			if err != nil || reservationErr != nil || found && terminal.State != factory.MCPWorkAccepted || reservation.State != factory.ReservationConsumed || a.Stage != factory.AssignmentFinished || a.Outcome != expectedProduct {
				return fmt.Errorf("original incomplete task result/accounting was not retained: terminal=%s found=%t reservation=%s assignment=%s outcome=%s errors=%v/%v", terminal.State, found, reservation.State, a.Stage, a.Outcome, err, reservationErr)
			}
			fmt.Println("PASS: original Muse task settled without inference replay; exact process/root retirement, canonical result, consumed reservation and stop-based source usage retained; outcome", a.Outcome)
		} else {
			fmt.Println("PASS: exact original deadline run recovered and settled failed without relaunch; source usage remains one admitted minute")
		}
		return nil
	}
	if input.MuseLogin {
		source, sourceErr := port.MuseLoginSource(ctx)
		if sourceErr != nil {
			return sourceErr
		}
		input.Connection = source.Connection
	}
	prep, err := db.Preparation(ctx, input.Preparation)
	if err != nil || prep.Native == nil || !prep.State.Ready || prep.Preparation.Role != project.RoleCoder {
		return errors.New("actual native coder preparation unavailable")
	}
	p := prep.Preparation
	rawDB, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	var repository int64
	if err = rawDB.QueryRowContext(ctx, `SELECT repository FROM factory_native_workspaces WHERE id=$1`, p.Project).Scan(&repository); err != nil {
		return err
	}
	if action == "withdraw-admitted-native" {
		return withdrawAdmittedNative(ctx, db, rawDB, port, input.Run, repository, input.Connection)
	}
	if action == "launch-admitted-tasks" {
		if !input.Tasks || !input.MuseLogin {
			return errors.New("task fixture requires the Muse source")
		}
		parent, err := db.AssignmentByRun(ctx, input.Run)
		if err != nil {
			return err
		}
		parentRun, err := db.FactoryRun(ctx, input.Run)
		if err != nil {
			return err
		}
		if !parentRun.Reconciled || parentRun.Native == nil || !parentRun.Native.Settled() || parent.Stage != factory.AssignmentFinished || parent.Outcome != factory.Succeeded || parent.Result == nil || parent.Result.PhaseResult == nil || parent.Result.PhaseResult.Output == nil || parent.Result.PhaseResult.Output.Plan == nil {
			return errors.New("actual investigation has not produced and settled its plan")
		}
		childID, err := factory.AdvancementID(parent.ID, factory.ResultDigest(*parent.Result), project.ExecutionImplement)
		if err != nil {
			return err
		}
		host := nativeReviewPreparationHost{NativeExecution: port, tool: execution.ToolObservation{Kind: collaboration.Native, Native: &parentRun.Native.Tool}}
		c := control.NewCoordinator(db, host, port)
		defer c.Close()
		c.MCPSecret = factoryruntime.NativeMCPSecretDeriver(config, instance)
		c.AcceptanceReads, c.DispatchReads = nativeReviewChildReads{parent.ActorID, parent.SourceCommit}, nativeReviewChildReads{parent.ActorID, parent.SourceCommit}
		report := c.Dispatch(ctx)
		child, err := db.Assignment(ctx, childID)
		if err != nil {
			return fmt.Errorf("coordinator did not advance investigation: %+v: %w", report, err)
		}
		run, err := db.FactoryRun(ctx, child.Run)
		if err != nil {
			return err
		}
		if child.ParentAssignment != parent.ID || child.AttemptRoot != parent.AttemptRoot || child.Phase != project.ExecutionImplement || run.Native == nil || len(run.ExecutionManifest) == 0 || run.Native.Intent == "" {
			return errors.New("coordinator advancement did not capture and launch its actual native Builder")
		}
		input.ParentRun, input.Run, input.DeadlineUnix = input.Run, child.Run, run.Deadline.Unix()
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		if err = os.WriteFile(path, encoded, 0600); err != nil {
			return err
		}
		fmt.Println("PASS: actual public coordinator dispatch admits the investigated plan, advances to Builder and launches its native manifest without declared phase advancement")
		return nil
	}
	if action == "settled-admitted-native" {
		if input.Tasks && input.ParentRun != "" {
			for {
				current, err := db.FactoryRun(ctx, input.Run)
				if err != nil {
					return err
				}
				if current.Reconciled {
					assignment, err := db.AssignmentByRun(ctx, input.Run)
					if err != nil {
						return err
					}
					if assignment.Stage == factory.AssignmentAssigned {
						if _, err = control.SettleMCPTerminalProposal(ctx, db, assignment.ID, time.Now()); err != nil {
							return fmt.Errorf("original terminal settlement: %w", err)
						}
						assignment, err = db.Assignment(ctx, assignment.ID)
						if err != nil {
							return err
						}
					}
					terminal, found, err := db.TerminalMCPProposal(ctx, assignment.ID)
					if err != nil {
						return err
					}
					if assignment.Stage == factory.AssignmentFinished && found && terminal.State == factory.MCPWorkAccepted {
						break
					}
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
		run, err := db.FactoryRun(ctx, input.Run)
		if err != nil {
			return err
		}
		cap, err := db.GetMCPCapability(ctx, input.Run)
		if err != nil {
			return err
		}
		assignment, err := db.Assignment(ctx, cap.AssignmentID)
		if err != nil {
			return err
		}
		terminal, found, err := db.TerminalMCPProposal(ctx, assignment.ID)
		if err != nil {
			return err
		}
		if !run.Reconciled || run.Native == nil || !run.Native.Settled() || !found || terminal.State != factory.MCPWorkAccepted ||
			!input.Tasks && (assignment.Stage != factory.AssignmentFinished || assignment.Outcome != factory.NeedsHuman) || input.Tasks && (assignment.Result == nil || assignment.Result.PhaseResult == nil || assignment.Result.PhaseResult.Status != "completed") {
			return fmt.Errorf("coordinator did not retain the worker result and retired resources: reconciled=%t terminal=%s assignment=%s outcome=%s reason=%s", run.Reconciled, terminal.State, assignment.Stage, assignment.Outcome, assignment.Reason)
		}
		if input.MuseLogin {
			used, err := db.UsageTotal(ctx, repository, input.Connection)
			if err != nil || used < 1 || assignment.Connection != input.Connection || run.Native.Provenance.TrustMode != execution.NativeCLIProvider || run.Native.Provenance.Custody.State != execution.NotAcquired {
				return errors.New("CLI source usage or authentication provenance differs")
			}
			var rawUsage []byte
			if err = rawDB.QueryRowContext(ctx, `SELECT data FROM factory_usage WHERE run=$1`, input.Run).Scan(&rawUsage); err != nil {
				return err
			}
			var usage factory.Usage
			stopped := run.Native.Provenance.Process.StoppedAt
			if stopped == nil || json.Unmarshal(rawUsage, &usage) != nil || !usage.EndedAt.Equal(*stopped) || !run.UsageEnd(run.Deadline.Add(time.Hour)).Equal(*stopped) {
				return errors.New("CLI usage includes delay after the retained native stop")
			}
		}
		fmt.Println("PASS: actual coordinator settlement retains the recorded worker phase result and exact retired native resources")
		if os.Getenv("SODA_NATIVE_CHECK_REVIEW_CHILD") == "1" && input.Tasks && input.ParentRun != "" {
			if err := nativeReviewChildFixture(ctx, db, port, assignment, run); err != nil {
				return err
			}
		}
		return port.AcknowledgeSettledRuns(ctx)
	}
	if action == "check-admitted-native" {
		var observed execution.RunObservation
		for {
			observed, err = port.InspectRun(ctx, execution.RunAddress{Run: input.Run, Workspace: p.Project})
			if !input.Tasks || input.ParentRun == "" || err == nil && observed.Retirement == execution.Retired || ctx.Err() != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			return err
		}
		if observed.Phase != project.FactoryCompleted || observed.Live || !observed.OutputComplete || observed.Retirement != execution.Retired {
			return errors.New("admitted worker terminal receipt incomplete")
		}
		if input.MuseLogin {
			if !input.Live && observed.Output != "Declared local CLI fixture; no model request" || input.Live && strings.TrimSpace(observed.Output) == "" || observed.Native.Connector == nil || observed.Native.Connector.UID != os.Geteuid() || observed.Native.Isolation != nil || observed.Native.Custody.State != execution.NotAcquired {
				return errors.New("CLI source final text or host-process provenance differs")
			}
		}
		if input.Tasks && input.ParentRun != "" {
			current, err := db.FactoryRun(ctx, input.Run)
			if err != nil {
				return err
			}
			if current.FinalCheckpoint == nil || current.FinalCheckpoint.Status != "committed" {
				return errors.New("admitted task run lacks final integration checkpoint")
			}
			final, err := port.ReadFinalCheckpoint(ctx, current.FinalCheckpoint.Request)
			if err != nil || final.Status != "committed" || final.Candidate != current.FinalCheckpoint.Candidate {
				return fmt.Errorf("admitted final readback: %v", err)
			}
			exported, err := port.ExportCandidate(ctx, execution.CandidateRequest{Address: execution.RunAddress{Run: input.Run, Workspace: p.Project}, Preparation: p.ID, Role: project.RoleCoder, Candidate: final.Candidate})
			if err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(profile, "native-task-candidate.bundle"), exported.Bundle, 0600); err != nil {
				return err
			}
			manifest, err := project.ParseExecutionManifest(current.ExecutionManifest)
			if err != nil {
				return err
			}
			var plan factory.InvestigationPlan
			if err = json.Unmarshal(manifest.Plan, &plan); err != nil {
				return err
			}
			if input.Live && (len(plan.Tasks) != 2 || !slices.Equal(plan.Tasks[1].DependsOn, []string{plan.Tasks[0].ID})) {
				return errors.New("live Builder check requires both dependent tasks, not a single-task plan")
			}
			receipts, err := db.PlanTaskReceipts(ctx, plan.AttemptRoot, plan.ID)
			if err != nil {
				return err
			}
			if len(receipts) != len(plan.Tasks) {
				return errors.New("admitted task receipts missing")
			}
			for _, task := range plan.Tasks {
				var matched *store.PlanTaskReceipt
				for i := range receipts {
					if receipts[i].TaskID == task.ID {
						matched = &receipts[i]
					}
				}
				if matched == nil || matched.AttemptRoot != plan.AttemptRoot || matched.PlanID != plan.ID || matched.State != store.PlanTaskCompleted || !matched.Parent || !factory.ValidCommit(matched.Commit) || !factory.ValidDigest(matched.OutputDigest) || !slices.Equal(matched.Checks, task.CheckIDs) {
					return errors.New("admitted sequential task receipt incomplete")
				}
			}
			fmt.Println("PASS: actual investigation-derived Go admission, role MCP edits/readback/retry, sequential task and integration checks, retained final receipt and candidate export")
		}
		cap, err := db.GetMCPCapability(ctx, input.Run)
		// Canonical settlement retires an activated capability; its retained
		// state still proves activation after the worker and resources settle.
		if err != nil || cap.State != factory.MCPCapabilityActive && cap.State != factory.MCPCapabilityRetired {
			return errors.New("actual worker never activated its MCP capability")
		}
		// Public Builder prompts have no fixture-only artifact requirement. Their
		// actual task checks and exported candidate above are the evidence.
		if input.Tasks && input.ParentRun != "" {
			return nil
		}
		var id string
		if err = rawDB.QueryRowContext(ctx, `SELECT id FROM factory_feed_artifacts WHERE assignment=$1 AND name='native-worker.txt' AND complete`, cap.AssignmentID).Scan(&id); err != nil {
			return err
		}
		artifact, content, err := db.FeedArtifact(ctx, repository, id)
		if err != nil || string(content) != "actual admitted native worker artifact\n" || artifact.Size != len(content) {
			return errors.New("actual worker artifact bytes were not retained")
		}
		fmt.Println("PASS: public Go-admitted native launch; live MCP activation, actual worker artifact bytes and terminal process/custody/root receipt")
		if os.Getenv("SODA_NATIVE_CHECK_CONTROLLER") == "1" {
			// A declared binary row above the 2 MiB JSON response ceiling beside the
			// worker's text artifact; the Rust check reads both through native custody.
			large := make([]byte, 2*1024*1024+4099)
			for i := range large {
				large[i] = byte(i*31 + i/251)
			}
			binary := factory.MCPInputDigest([]byte("artifact\n" + cap.AssignmentID + "\nnative-worker-binary"))[:32]
			if _, err = rawDB.ExecContext(ctx, `INSERT INTO factory_feed_artifacts(id,repository,assignment,name,media_type,content,complete) VALUES($1,$2,$3,'native-worker.bin','application/octet-stream',$4,true) ON CONFLICT(id) DO NOTHING`, binary, repository, cap.AssignmentID, large); err != nil {
				return err
			}
			encoded, _ := json.Marshal(map[string]string{"text": id, "binary": binary})
			if err = os.WriteFile(filepath.Join(profile, "native-worker-artifacts.json"), encoded, 0o600); err != nil {
				return err
			}
		}
		return nil
	}
	if action == "observe-admitted-native" {
		observed, err := port.InspectRun(ctx, execution.RunAddress{Run: input.Run, Workspace: p.Project})
		if err != nil {
			return err
		}
		if observed.Live || observed.Retirement != execution.Retired {
			return errors.New("failed native execution retirement remains unconfirmed")
		}
		fmt.Println("Recorded exact failed native process/root facts; product result remains unfinished")
		return nil
	}
	environment, err := db.EnvironmentGrant(ctx, repository)
	if err != nil {
		return err
	}
	actor := environment.Owner
	account, err := port.ExecutionAccount(ctx, input.Connection)
	if err != nil {
		return err
	}
	binding, err := db.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{Profile: collaboration.Profile{ID: config.Profile, Name: "Native source fixture"},
		Provider: collaboration.GitHub, APIBase: "https://api.github.com", Connection: collaboration.ConnectionID(factory.NewID()), Account: "native-source-owner", Login: "native-source-owner"})
	controller, controllerErr := controllerFixture(profile)
	if controllerErr != nil {
		return controllerErr
	}
	if controller != nil {
		binding, err = db.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{Profile: collaboration.Profile{ID: config.Profile, Name: "Native source fixture"}, Provider: collaboration.Fountain, APIBase: controller.Origin, Connection: collaboration.ConnectionID(controller.Connection), Account: controller.Actor, Login: "user2"})
	}
	if err != nil || int64(binding.Actor.ID) != actor {
		return errors.New("declared native actor mapping changed")
	}
	ref := func(kind string) factory.ActorBindingRef {
		return factory.ActorBindingRef{NativeProfile: config.Profile, NativeConnection: binding.Connection.ID, ActorID: actor, Kind: kind}
	}
	roles := map[string]factory.RoleSelection{}
	for _, role := range []string{project.RoleCoder, project.RoleReviewer} {
		roles[role] = factory.RoleSelection{Account: input.Connection, Harness: project.FactoryHarnessMuse, Model: "muse-spark-1.3-contributor", Profile: factory.ExecutionProfile{Effort: factory.DefaultEffort, Contributor: factory.DefaultContributor}}
	}
	policy := factory.RepositoryPolicy{Repository: repository, GrantedBy: actor, Enabled: true, TargetBranch: "refs/heads/main", Roles: roles, Checks: []string{"fixture/check"}, MergeMethod: factory.MergeFastForward,
		Publish: ref(factory.OpRefPublish), Create: ref(factory.OpPRCreate), Review: ref(factory.OpReviewSubmit), Merge: ref(factory.OpMerge), AttemptLimits: factory.DefaultAttemptLimits(), ReassessmentMinutes: factory.DefaultReassessmentMinutes}
	if controller != nil {
		executionRef := ref("execution.launch")
		executionRef.ControllerGrantID = controller.Grant
		policy.Execution = &executionRef
	}
	if prior, err := db.RepositoryPolicy(ctx, repository); err == nil {
		policy.Revision = prior.Revision
		if controller != nil {
			policy.Publish, policy.Create, policy.Review, policy.Merge = prior.Publish, prior.Create, prior.Review, prior.Merge
			policy.TargetBranch, policy.Checks, policy.MergeMethod = prior.TargetBranch, prior.Checks, prior.MergeMethod
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	sponsoredRoles := []string{project.RoleCoder}
	if os.Getenv("SODA_NATIVE_CHECK_REVIEW_CHILD") == "1" {
		sponsoredRoles = append(sponsoredRoles, project.RoleReviewer)
	}
	for _, err := range []error{
		db.SaveRepositoryPolicy(ctx, policy), db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: actor, MaxConcurrentRuns: 1}),
		db.SaveOperatorGrant(ctx, factory.OperatorGrant{Repository: repository, GrantedBy: actor, Active: true}),
		db.SaveSponsorship(ctx, factory.Sponsorship{Repository: repository, GrantedBy: actor, ActorID: actor, ProjectID: p.Project, Connection: input.Connection, Native: &account, Roles: sponsoredRoles, Active: true}),
	} {
		if err != nil {
			return err
		}
	}
	if controller != nil {
		// This is a declared single-role worker, not a full automation resume.
		// The preceding real policy save deliberately disabled dispatch. Clear
		// only that exact fixture hold with the existing CAS owner; any other
		// closure still refuses this fixture rather than losing its cause.
		open, revision, withdrawal, err := db.DispatchState(ctx, repository)
		if err != nil {
			return err
		}
		if !open {
			if len(withdrawal.ActiveCauses) != 1 || withdrawal.ActiveCauses[0] != "policy_disabled" {
				return errors.New("unrelated controller fixture dispatch hold")
			}
			if _, _, err = db.ReopenDispatch(ctx, repository, revision); err != nil {
				return err
			}
		}
	}
	acceptance := factory.Acceptance{ID: "d" + factory.NewID()[:24], Repository: repository, IssueIndex: "3", Approver: actor, NativeRev: 1, TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), ContentVersion: 1}
	if err = db.AdmitAcceptanceDecision(ctx, acceptance); err != nil {
		return err
	}
	var queued factory.IssueControl
	if input.Tasks || os.Getenv("SODA_NATIVE_CHECK_CONTROL") != "" {
		if err = nativeStandingRole(ctx, db, port, prep, project.RoleReviewer); err != nil {
			return err
		}
	}
	if input.Tasks {
		assessment := control.NewCoordinator(db, port, port)
		assessment.AcceptanceReads = nativeReviewChildReads{actor, p.SourceCommit}
		queued, _, err = assessment.ObserveIssueEvent(ctx, control.IntakeHint{Delivery: factory.NewID(), Repository: repository, Issue: 3})
		assessment.Close()
		if err != nil || queued.Readiness != factory.ReadinessQueued {
			return fmt.Errorf("actual native issue readiness: %s %s: %v", queued.Readiness, queued.Reason, err)
		}
	} else {
		queued, _, err = db.RecordIssueAssessment(ctx, factory.IssueControl{Repository: repository, Issue: 3, Acceptance: acceptance.ID, NativeRev: 1, Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible, Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64)}, time.Now())
		if err != nil {
			return err
		}
	}
	prompt, err := factory.BuildDispatchPrompt(factory.PromptInputs{Repository: repository, Issue: 3, NativeRev: 1, Phase: project.ExecutionInvestigate, AcceptanceID: acceptance.ID, TargetBranch: policy.TargetBranch, SourceCommit: p.SourceCommit, Preparation: p.ID,
		Harness: project.FactoryHarnessMuse, Model: roles[project.RoleCoder].Model, Role: project.RoleCoder, ProviderConnection: input.Connection, RequiredChecks: policy.Checks, AttemptLimits: policy.AttemptLimits,
		RequirementsID: p.Requirements.ID, ApprovalID: p.Approval.ID, Title: "Native admitted execution fixture", Body: "positive-native-mcp-fixture"})
	if err != nil {
		return err
	}
	if input.Live {
		prompt = []byte("Disposable Factory connector integration task. Use the available Factory MCP tools only. Publish an artifact with upload_id 11111111111111111111111111111111, name native-worker.txt, media_type text/plain, content_base64 YWN0dWFsIGFkbWl0dGVkIG5hdGl2ZSB3b3JrZXIgYXJ0aWZhY3QK, final true. Then call finish_incomplete with outcome blocked and cause local_fixture_has_no_provider_result. This intentionally records an incomplete product result; do not invent an implementation, plan or provider permissions. Finish with OK after both tools succeed.")
	}
	if input.Withdraw {
		prompt = append(prompt, []byte("\nwithdraw-source-fixture\n")...)
	}
	if !input.Live && os.Getenv("SODA_NATIVE_CHECK_READ_SOURCE") == "1" {
		prompt = append(prompt, []byte("\nread-source-fixture\n")...)
	}
	if input.Tasks {
		prompt = append(prompt, []byte("\nnative-tasks-investigation-fixture\n")...)
		if os.Getenv("SODA_NATIVE_CHECK_CORRECTION") == "1" {
			if input.Live {
				return errors.New("correction check uses only the local CLI fixture")
			}
			prompt = append(prompt, []byte("\nnative-correction-fixture\n")...)
		}
		if input.Live {
			plan := nativeWorkspacePlan(p.SourceCommit)
			for i := range plan.Tasks {
				plan.Tasks[i].OwnedPaths = []string{"native-task.txt"}
			}
			plan.Approach += ". Builder: use factory.context payload.after_execution with its preceding execution_revision to wait for ready_outputs, committed task receipts and the final integration checkpoint. Submit first task output with submission_id native-task-first-output. After both tasks and final integration commit, deliberately try finish_implementation with that same submission_id; it must refuse different work. Then omit submission_id for the actual finish."
			plan.Checks = []factory.CheckSpec{
				{ID: "first", Argv: []string{"/usr/bin/grep", "-qx", "first", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeTask, Required: true},
				{ID: "second", Argv: []string{"/usr/bin/grep", "-qx", "second", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeTask, Required: true},
				{ID: "integration", Argv: []string{"/usr/bin/grep", "-qx", "second", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeIntegration, Required: true},
			}
			encoded, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			var authored map[string]any
			if err = json.Unmarshal(encoded, &authored); err != nil {
				return err
			}
			for _, field := range []string{"attempt_root", "source_commit", "integration_commit", "scope_digest", "input_digest", "preparation", "digest"} {
				delete(authored, field)
			}
			encoded, err = json.Marshal(map[string]any{"payload": map[string]any{"approach": plan.Approach, "evidence_refs": []string{"source:README.md"}, "tasks": authored}})
			if err != nil {
				return err
			}
			prompt = []byte("Disposable Factory Architect integration task. Read README.md with factory.read_source, inspect factory.context, and read factory.inbox. " + nativeFixtureArtifactPrompt + " Then call factory.finish_issue_plan with these JSON arguments: " + string(encoded) + ". payload and tasks must be JSON objects, never JSON-encoded strings. Omit submission_id for this new call. This is a declared one-file, two-task test objective and checks; do not implement it in the investigation phase. Finish with OK after the terminal tool succeeds.")
		}
	}
	var acceptedContext *factory.AcceptedContext
	if input.Tasks {
		material, err := port.ReadWorkspaceContext(ctx, execution.PreparationContextRequest{Native: prep.Native, Role: p.Role,
			Input: project.FactoryPreparationContextRequest{Project: p.Project, ID: p.ID, SourceCommit: p.SourceCommit, ApprovedBase: p.SourceCommit, DiffBase: p.SourceCommit, Candidate: p.SourceCommit, Paths: []string{"README.md"}, NotAfter: time.Now().Add(15 * time.Second)}})
		if err != nil {
			return err
		}
		acceptedContext = factory.CaptureAcceptedContext(factory.PromptInputs{Repository: repository, Issue: 3, NativeRev: 1, AcceptanceID: acceptance.ID, SourceCommit: p.SourceCommit, Preparation: p.ID, Title: "Native admitted execution fixture", Body: "Produce native-task.txt with the captured task checks", RepositoryContext: &material.Material, RequiredChecks: policy.Checks})
	}
	toolBytes, err := os.ReadFile(input.Executable)
	if err != nil {
		return err
	}
	toolSum := sha256.Sum256(toolBytes)
	tool := execution.NativeTool{Harness: project.FactoryHarnessMuse, Version: "fixture", SHA256: hex.EncodeToString(toolSum[:]), Executable: input.Executable}
	if input.Live {
		observed, err := port.InspectTool(ctx, project.FactoryHarnessMuse)
		if err != nil {
			return err
		}
		tool = *observed.Native
	}
	now := time.Now().UTC().Truncate(time.Second)
	aid := factory.NewID()
	authority := factory.AuthorityRef{Policy: policy.Revision + 1, Operator: 1, Capacity: 1, Sponsorship: 1, SponsorshipConnection: input.Connection, Environment: environment.Revision, RequirementsID: p.Requirements.ID, ApprovalID: p.Approval.ID}
	a := factory.Assignment{ID: aid, AttemptRoot: aid, PublicationAssignment: aid, ProjectID: p.Project, Role: project.RoleCoder, ActorID: actor, Repository: repository, Issue: 3, NativeRev: 1, Acceptance: acceptance.ID, Preparation: p.ID,
		Subject: factory.ExecutionSubject{Kind: factory.SubjectIssue, Issue: &factory.IssueSubject{Issue: 3, Acceptance: acceptance.ID}}, Phase: project.ExecutionInvestigate,
		Harness: "muse-" + tool.Version, HarnessVers: tool.Version, NativeTool: &tool, Model: roles[project.RoleCoder].Model, Connection: input.Connection, SourceCommit: p.SourceCommit, Prompt: prompt, PromptSHA: project.FactoryPromptDigest(prompt),
		Run: input.Run, RunHistory: []string{input.Run}, Stage: factory.AssignmentAssigned, Attempts: 1, CreatedUnix: now.Unix(), Authority: authority, AcceptedContext: acceptedContext}
	native := execution.NativeLaunch{Workspace: *prep.Native, Tool: tool, BundleSHA256: prep.State.SourceBundleSHA256, PromptSHA256: a.PromptSHA, CredentialSHA256: account.CredentialSHA256,
		Runtime: environment.Native.Runtime, PlacementGeneration: environment.Native.Generation, TrustMode: "isolated_system_worker", BrokerRun: input.Run}
	if input.MuseLogin {
		native.Authentication = account.Authentication
		native.TrustMode = execution.NativeCLIProvider
	}
	run := factory.Run{ID: input.Run, ProjectID: p.Project, Role: a.Role, InputSHA: p.SourceCommit, Started: now, Deadline: now.Add(time.Minute), Harness: a.Harness, Model: a.Model,
		Native: &execution.NativeRecord{Tool: tool, BundleSHA256: native.BundleSHA256, PromptSHA256: a.PromptSHA, Retirement: execution.NotStarted,
			Provenance: execution.NativeProvenance{TrustMode: native.TrustMode, Profile: config.Profile, InstanceID: instance, WorktreeID: p.ID, BrokerRun: input.Run, Custody: execution.NativeCustody{State: execution.NotAcquired}}}}
	if err = db.RecordNativeDispatchPacket(ctx, queued, a, factory.Reservation{AssignmentID: aid, Repository: repository, Connection: input.Connection, State: factory.ReservationHeld, PlannedMinutes: 1}, run,
		factory.RunView{RunID: run.ID, Repository: repository, Issue: 3, Attempt: aid}, native); err != nil {
		return err
	}
	input.DeadlineUnix = run.Deadline.Unix()
	fixture, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, fixture, 0600); err != nil {
		return err
	}
	return launchNativeFixtureRun(ctx, db, port, run, a, prep)
}

func launchNativeFixtureRun(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, run factory.Run, a factory.Assignment, prep execution.PreparationRecord) error {
	run, err := db.FactoryRun(ctx, run.ID)
	if err != nil {
		return err
	}
	cap, err := control.BuildMCPCapability(a, run, prep)
	if err != nil {
		return err
	}
	if err = db.StageMCPCapability(ctx, cap); err != nil {
		return err
	}
	secret, err := factoryruntime.NativeMCPSecretDeriver(port.Config, port.Instance)(ctx, cap)
	if err != nil {
		return err
	}
	manifestDigest := ""
	if len(run.ExecutionManifest) != 0 {
		manifestDigest = project.ExecutionManifestDigest(run.ExecutionManifest)
	}
	request, err := run.LaunchRequest(a, project.FactoryLaunch{Run: project.FactoryRun{ID: run.ID, Project: a.ProjectID, Role: a.Role, Preparation: a.Preparation, Harness: run.Native.Tool.Harness, HarnessVers: run.Native.Tool.Version, Model: a.Model,
		Effort: factory.DefaultEffort, Contributor: factory.DefaultContributor, Assignment: a.PromptSHA, SourceCommit: a.SourceCommit, Connection: a.Connection, Actor: a.ActorID, Deadline: run.Deadline, ExecutionPhase: a.Phase, ManifestDigest: manifestDigest}, Prompt: a.Prompt, HarnessSHA256: run.Native.Tool.SHA256,
		MCP: &project.FactoryMCPBinding{Capability: cap.ID, Secret: secret}, ExecutionManifest: run.ExecutionManifest})
	if err != nil {
		return err
	}
	if os.Getenv("SODA_NATIVE_CHECK_LAUNCH_LOSS") == "1" {
		return launchNativeWithoutReply(ctx, db, port, request)
	}
	observed, err := port.LaunchRun(ctx, request)
	if err != nil {
		return err
	}
	if !observed.Live {
		return errors.New("public Go launch did not observe its actual live worker")
	}
	fmt.Println("PASS: actual native packet/capability admitted and launched through the public Go execution port")
	return nil
}

// Drop only the real native launch response. All admission/signing requests
// still use the public Go launcher and authenticated native owner unchanged.
func launchNativeWithoutReply(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, request execution.LaunchRequest) error {
	directory, err := os.MkdirTemp(port.Config.Directory, "launch-loss-")
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", filepath.Join(directory, "host.sock"))
	if err != nil {
		return err
	}
	if err = os.Chmod(listener.Addr().String(), 0600); err != nil {
		listener.Close()
		return err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(port.Config.Directory, "host.sock"))
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	dropped := make(chan bool, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, (6*project.MaxReassessPrompt+32*1024)+1))
		if err != nil || len(body) > (6*project.MaxReassessPrompt+32*1024) {
			http.Error(w, "bounded request unavailable", 400)
			return
		}
		var command struct{ Operation string }
		_ = json.Unmarshal(body, &command)
		forward := r.Clone(ctx)
		forward.RequestURI, forward.URL.Scheme, forward.URL.Host = "", "http", "soda-native"
		forward.Body = io.NopCloser(bytes.NewReader(body))
		response, err := transport.RoundTrip(forward)
		if err != nil {
			http.Error(w, "native owner unavailable", 502)
			return
		}
		defer response.Body.Close()
		if command.Operation == "launch" {
			var receipt struct{ Accepted bool }
			err := json.NewDecoder(response.Body).Decode(&receipt)
			dropped <- err == nil && response.StatusCode == 200 && receipt.Accepted
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})}
	joined := make(chan struct{})
	go func() { defer close(joined); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-joined }()
	probe := port
	probe.Config.Directory = directory
	_, launchErr := probe.LaunchRun(ctx, request)
	select {
	case accepted := <-dropped:
		if !accepted || launchErr == nil {
			return errors.New("initial launch reply was not lost after actual native acceptance")
		}
	default:
		return fmt.Errorf("public launch never reached native acceptance: %v", launchErr)
	}
	run, err := db.FactoryRun(ctx, request.Address().Run)
	if err != nil || run.Reconciled || run.Native == nil || run.Native.Intent == "" || run.Native.Provenance.Process != nil || run.Native.Provenance.Connector != nil {
		return errors.New("lost launch reply invented process evidence or settlement")
	}
	fmt.Println("PASS: public Go launch loses the real accepted native reply; exact admitted intent/reservation retained without a product process observation or retry")
	return nil
}

// Exercise the shared intervention service against the admitted, running local
// CLI fixture. Authority is declared local fixture scope, not a GitHub claim.
func controlAdmittedNative(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, run factory.Run, repository int64, action string) error {
	if action != factory.ActionPause && action != factory.ActionStop {
		return errors.New("unsupported native control check")
	}
	a, err := db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	c := control.NewCoordinator(db, port, port)
	defer c.Close()
	c.AcceptanceReads = nativeReviewChildReads{a.ActorID, a.SourceCommit}
	actor := collaboration.ActorID(a.ActorID)
	current := true
	s := factoryservice.Interventions{Store: db, Writer: c,
		WriteAccess: func(ctx context.Context, selected collaboration.ActorID, repo collaboration.RepositoryID) error {
			if !current || selected != actor || int64(repo) != repository {
				return factoryservice.ErrInterventionAccess
			}
			return ctx.Err()
		},
		RunRepository: func(context.Context, collaboration.ActorID, factory.Run) (collaboration.RepositoryID, error) {
			return collaboration.RepositoryID(repository), nil
		},
		RecoveryTarget: func(ctx context.Context, _ collaboration.ActorID, _ collaboration.RepositoryID, _ factory.Run) (execution.RecoveryTarget, error) {
			var target execution.NativeCheckoutRef
			if err := nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/execution", map[string]any{"operation": "recovery_target"}, &target); err != nil {
				return execution.RecoveryTarget{}, factoryservice.ErrInterventionRecoveryTarget
			}
			return execution.RecoveryTarget{Native: &target}, nil
		}}
	command := func(in factoryservice.RunCommand) (any, error) {
		return s.Run(ctx, actor, "native-control-fixture", run.ID, in)
	}
	repositoryCommand := func(action string) (any, error) {
		return s.Repository(ctx, actor, "native-control-fixture", collaboration.RepositoryID(repository), factoryservice.RepositoryCommand{Action: action})
	}
	current = false
	if _, err = command(factoryservice.RunCommand{Action: factory.ActionStop}); !errors.Is(err, factoryservice.ErrInterventionAccess) {
		return errors.New("stale account admitted a stop")
	}
	current = true
	unavailable := s
	unavailable.Writer = nil
	if _, err = unavailable.Run(ctx, actor, "native-control-fixture", run.ID, factoryservice.RunCommand{Action: factory.ActionStop}); !errors.Is(err, factoryservice.ErrInterventionUnavailable) {
		return errors.New("missing execution writer did not refuse")
	}
	if _, err = repositoryCommand(factory.ActionResume); err == nil {
		return errors.New("resume admitted an unsettled worker")
	}
	if _, err = command(factoryservice.RunCommand{Action: factory.ActionTakeover}); err == nil {
		return errors.New("takeover admitted an unsettled worker")
	}
	if state, err := port.InspectRun(ctx, execution.RunAddress{Run: run.ID, Workspace: run.ProjectID}); err != nil || !state.Live {
		return errors.New("refused control changed the live worker")
	}
	switch action {
	case factory.ActionPause:
		for {
			receipt, err := repositoryCommand(factory.ActionPause)
			if err != nil {
				return fmt.Errorf("pause worker: %w", err)
			}
			paused, ok := receipt.(factory.PauseResult)
			if !ok || !paused.Paused || len(paused.Runs) != 1 || paused.Runs[0].ID != run.ID {
				return fmt.Errorf("pause lost the exact worker receipt: %+v", receipt)
			}
			if paused.Runs[0].Confirmed && !paused.Runs[0].Uncertain && paused.Runs[0].Outcome == string(factory.Cancelled) {
				break
			}
			if ctx.Err() != nil {
				return fmt.Errorf("pause did not confirm the exact worker: %+v", receipt)
			}
			time.Sleep(20 * time.Millisecond)
		}
	default:
		for {
			receipt, err := command(factoryservice.RunCommand{Action: factory.ActionStop})
			if err != nil {
				return err
			}
			stopped, ok := receipt.(control.StopResult)
			if !ok || stopped.RunID != run.ID {
				return fmt.Errorf("stop lost the exact worker receipt: %+v", receipt)
			}
			if stopped.Confirmed && !stopped.Uncertain && stopped.Outcome == string(factory.Cancelled) {
				break
			}
			if ctx.Err() != nil {
				return fmt.Errorf("stop did not confirm the exact worker: %+v", receipt)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	settled, err := db.FactoryRun(ctx, run.ID)
	if err != nil || !settled.Reconciled || settled.Outcome != factory.Cancelled || settled.Native == nil || !settled.Native.Settled() || settled.Native.Provenance.Process == nil || settled.Native.Provenance.Process.StoppedAt == nil || settled.Native.Provenance.Custody.State != execution.NotAcquired {
		return errors.New("control did not retain actual cancelled process/custody retirement")
	}
	usage, err := db.RunUsage(ctx, run.ID)
	if err != nil || usage.Validate() != nil || usage.Connection != a.Connection || !usage.StartedAt.Equal(run.Started) || !usage.EndedAt.Equal(*settled.Native.Provenance.Process.StoppedAt) {
		return errors.New("control usage is not bound to the original source and exact stop")
	}
	reservation, err := db.Reservation(ctx, a.ID)
	if err != nil || reservation.State != factory.ReservationConsumed {
		return errors.New("control left an unsettled reservation")
	}
	if _, err = repositoryCommand(factory.ActionResume); err != nil {
		return fmt.Errorf("resume after retirement: %w", err)
	}
	missing := s
	missing.RecoveryTarget = nil
	if _, err = missing.Run(ctx, actor, "native-control-fixture", run.ID, factoryservice.RunCommand{Action: factory.ActionTakeover}); !errors.Is(err, factoryservice.ErrInterventionUnavailable) {
		return errors.New("missing recovery target did not refuse")
	}
	recovered, err := command(factoryservice.RunCommand{Action: factory.ActionTakeover})
	if err != nil {
		return fmt.Errorf("takeover after retirement: %w", err)
	}
	record, ok := recovered.(factory.TakeoverRecord)
	if !ok || record.Run != run.ID || record.Native == nil || record.Validate() != nil {
		return errors.New("takeover lost its exact native receipt")
	}
	human := filepath.Join(record.Dest, "README.md")
	if err = os.WriteFile(human, []byte("Human edit after native takeover\n"), 0600); err != nil {
		return err
	}
	replayed, err := command(factoryservice.RunCommand{Action: factory.ActionTakeover})
	if err != nil || !reflect.DeepEqual(recovered, replayed) {
		return errors.New("takeover receipt changed on replay")
	}
	if text, err := os.ReadFile(human); err != nil || string(text) != "Human edit after native takeover\n" {
		return errors.New("takeover retry overwrote human work")
	}
	retry := factoryservice.RunCommand{Action: factory.ActionRetry, CommandID: factory.NewID()}
	queued, err := command(retry)
	if err != nil {
		return fmt.Errorf("retry after retirement: %w", err)
	}
	decision, ok := queued.(factory.RetryDecision)
	if !ok || !decision.Queued || decision.Prior != run.ID || decision.CommandID != retry.CommandID {
		return errors.New("retry lost its exact queued receipt")
	}
	replayed, err = command(retry)
	if err != nil || !reflect.DeepEqual(queued, replayed) {
		return errors.New("retry receipt changed on replay")
	}
	if err = port.AcknowledgeSettledRuns(ctx); err != nil {
		return err
	}
	fmt.Println("PASS: admitted native", action, "through shared controls; stale account/missing adapters and unsettled resume/takeover refused, exact cancellation/stop-based usage/reservation, resume, native takeover/human-edit replay and queued retry receipt")
	return nil
}

// Uses the existing admitted local CLI fixture: no provider request or login
// mutation. The alternate local credential is actually enrolled, never a
// fallback login or evidence of provider authentication.
func withdrawAdmittedNative(ctx context.Context, db *store.Store, rawDB *sql.DB, port factoryruntime.NativeExecution, runID string, repository int64, connection string) error {
	var active factory.MCPCapability
	until := time.Now().Add(12 * time.Second)
	for {
		cap, err := db.GetMCPCapability(ctx, runID)
		if err != nil {
			return err
		}
		var artifact bool
		if err = rawDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_artifacts WHERE assignment=$1 AND complete)`, cap.AssignmentID).Scan(&artifact); err != nil {
			return err
		}
		if cap.State == factory.MCPCapabilityActive && artifact {
			active = cap
			break
		}
		if time.Now().After(until) || ctx.Err() != nil {
			return errors.New("withdrawal fixture never reached a live admitted artifact")
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, err := db.FactoryRun(ctx, runID)
	if err != nil {
		return err
	}
	observed, err := port.InspectRun(ctx, execution.RunAddress{Run: runID, Workspace: run.ProjectID})
	if err != nil || !observed.Live {
		return errors.New("withdrawal fixture is not actually live")
	}
	if action := os.Getenv("SODA_NATIVE_CHECK_CONTROL"); action != "" {
		if action == "owner-exit" || action == "owner-active" {
			if action == "owner-active" {
				if observed.Native == nil || observed.Native.Connector == nil {
					return errors.New("active CLI service identity missing")
				}
				unit := "soda-factory-muse-" + run.ID + "-" + observed.Native.Connector.Service + ".service"
				command := exec.CommandContext(ctx, "systemctl", "--user", "kill", "--kill-whom=all", "--signal=STOP", unit)
				command.Env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+strconv.Itoa(os.Getuid())+"/bus")
				if output, err := command.CombinedOutput(); err != nil {
					return fmt.Errorf("hold actual CLI service: %v %s", err, output)
				}
				if err = os.WriteFile(filepath.Join(port.Config.Directory, "check-active-muse-unit"), []byte(unit), 0600); err != nil {
					return err
				}
			}
			fmt.Println("PASS: actual admitted CLI worker, active capability and published artifact observed before abrupt owner exit")
			return nil
		}
		return controlAdmittedNative(ctx, db, port, run, repository, action)
	}
	selected, err := db.Sponsorship(ctx, repository, connection)
	if err != nil {
		return err
	}
	coordinator := control.Coordinator{Store: db, Host: port, Broker: port}
	other := selected
	id, err := os.ReadFile(filepath.Join(port.Config.Directory, "check-withdrawal-source.id"))
	if err != nil || !factory.ValidID(string(id)) || string(id) == connection {
		return errors.New("alternate enrolled source identity missing")
	}
	other.Connection, other.Revision = string(id), 0
	account, err := port.ExecutionAccount(ctx, other.Connection)
	if err != nil {
		return fmt.Errorf("alternate source is not actually available: %w", err)
	}
	other.Native = &account
	other.Roles = []string{project.RoleReviewer}
	if _, err = coordinator.ApplySponsorship(ctx, "native-source-fixture", other); err != nil {
		return err
	}
	policy, err := db.RepositoryPolicy(ctx, repository)
	if err != nil {
		return err
	}
	reviewer := policy.Roles[project.RoleReviewer]
	reviewer.Account = other.Connection
	policy.Roles[project.RoleReviewer] = reviewer
	if _, err = coordinator.ApplyPolicy(ctx, "native-source-fixture", policy); err != nil {
		return err
	}
	if cap, err := db.GetMCPCapability(ctx, runID); err != nil || cap.State != factory.MCPCapabilityActive {
		return errors.New("unrelated reviewer selection revoked the coder")
	}
	selectedRoles := append([]string(nil), selected.Roles...)
	if os.Getenv("SODA_NATIVE_CHECK_WITHDRAW_ROLE") == "1" {
		selected.Roles = []string{project.RoleReviewer}
	} else {
		selected.Active = false
	}
	result, err := coordinator.ApplySponsorship(ctx, "native-source-fixture", selected)
	if err != nil {
		return err
	}
	var open bool
	if err = rawDB.QueryRowContext(ctx, `SELECT open FROM factory_dispatch WHERE repository=$1`, repository).Scan(&open); err != nil || !open || result.Withdrawn {
		return fmt.Errorf("alternate active source did not preserve repository dispatch: %v", err)
	}
	if cap, err := db.GetMCPCapability(ctx, runID); err != nil || cap.State != factory.MCPCapabilityRevoked {
		return errors.New("selected source withdrawal did not durably revoke its run")
	}
	admission := factoryservice.WorkerAdmission{Store: db, Inspect: port.InspectRun}
	if _, err = admission.Prepare(ctx, active); err == nil {
		return errors.New("withdrawn source still admits a cached worker capability")
	}
	submission := factory.MCPSubmission{AssignmentID: active.AssignmentID, SubmissionID: factory.NewID(), CapabilityID: active.ID, Tool: factory.MCPToolFinishIncomplete, Payload: []byte(`{"outcome":"blocked","cause":"must_be_refused"}`)}
	if _, err = db.RecordMCPSubmission(ctx, submission); err == nil {
		return errors.New("withdrawn source recorded new worker work")
	}
	var recorded bool
	if err = rawDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE idempotency=$1)`, submission.SubmissionID).Scan(&recorded); err != nil || recorded {
		return errors.New("refused withdrawal submission left a work post")
	}
	until = time.Now().Add(12 * time.Second)
	for {
		run, err = db.FactoryRun(ctx, runID)
		if err != nil {
			return err
		}
		if run.Reconciled {
			break
		}
		if time.Now().After(until) || ctx.Err() != nil {
			return errors.New("withdrawn native run retirement remains unconfirmed")
		}
		if _, err = coordinator.Stop(ctx, runID); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if run.Outcome != factory.Cancelled || run.Native == nil || !run.Native.Settled() || run.Native.Provenance.Custody.State != execution.NotAcquired {
		return fmt.Errorf("withdrawn run did not retain actual cancelled/retired source facts: %s", run.Outcome)
	}
	used, err := db.UsageTotal(ctx, repository, connection)
	if err != nil || used < 1 {
		return errors.New("withdrawn source usage was not attributed to its selected connection")
	}
	if used, err = db.UsageTotal(ctx, repository, other.Connection); err != nil || used != 0 {
		return errors.New("withdrawal switched spending sources")
	}
	available, err := port.ExecutionAccount(ctx, other.Connection)
	if err != nil || !reflect.DeepEqual(account, available) {
		return errors.New("withdrawal made the alternate enrolled source unavailable")
	}
	selected, err = db.Sponsorship(ctx, repository, connection)
	if err != nil {
		return err
	}
	selected.Active = true
	selected.Roles = selectedRoles
	if _, err = coordinator.ApplySponsorship(ctx, "native-source-fixture", selected); err != nil {
		return err
	}
	if err = db.ActivateObservedMCPCapability(ctx, active); err == nil {
		return errors.New("re-enabling a source revived its old run capability")
	}
	source, err := port.MuseLoginSource(ctx)
	if err != nil || source.Connection != connection {
		return errors.New("Factory withdrawal changed the Muse login source")
	}
	withdrawn := "selected-source"
	if os.Getenv("SODA_NATIVE_CHECK_WITHDRAW_ROLE") == "1" {
		withdrawn = "selected-role"
	}
	fmt.Println("PASS:", withdrawn, "withdrawal with another source active: immediate MCP refusal, actual process/root cancellation, original-source usage, no fallback or old-capability revival; local CLI fixture, no provider request/login mutation")
	return port.AcknowledgeSettledRuns(ctx)
}

// Provider issue/PR facts are declared; preparation, workspace bytes, SQL
// admission and the coordinator child producer are real. No model is called.
type nativeReviewChildReads struct {
	actor  int64
	source string
}

type nativeReviewPreparationHost struct {
	factoryruntime.NativeExecution
	tool execution.ToolObservation
}

func (h nativeReviewPreparationHost) InspectTool(ctx context.Context, family string) (execution.ToolObservation, error) {
	return h.tool, h.tool.ValidateFor(family)
}
func (h nativeReviewPreparationHost) NativeSourceAvailable(ctx context.Context, authentication string) (bool, error) {
	return authentication == factory.NativeMuseLogin, ctx.Err()
}
func (r nativeReviewChildReads) ReadAcceptanceEvidence(ctx context.Context, repository, issue string, comments []string) (control.AcceptanceEvidence, error) {
	return control.AcceptanceEvidence{Revision: 1, Comments: []control.AcceptanceComment{}, Dependencies: []control.AcceptanceEdge{}, Issue: control.AcceptanceIssueView{ID: "19", Index: issue, TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64), ContentVer: 1, TitleOccurrence: factory.OriginalOccurrence, LifecycleOccurrence: factory.OriginalOccurrence, PosterID: fmt.Sprint(r.actor), CreatedUnix: time.Now().Add(-time.Minute).Unix(), Visible: true, Verified: true}}, ctx.Err()
}
func (r nativeReviewChildReads) ReadDispatchInputs(ctx context.Context, repository, issue string, comments []string, target string) (control.DispatchInputs, error) {
	body := "positive-native-mcp-fixture"
	if os.Getenv("SODA_NATIVE_CHECK_CORRECTION") == "1" {
		body += " native-correction-fixture"
	}
	return control.DispatchInputs{Revision: 1, Tip: r.source, TipRef: target, Comments: []control.DispatchComment{}, Issue: control.DispatchIssue{Title: "Native admitted execution fixture", Body: body, TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64), ContentVer: 1, TitleOccurrence: factory.OriginalOccurrence, LifecycleOccurrence: factory.OriginalOccurrence, Visible: true}}, ctx.Err()
}

func nativeStandingRole(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, source execution.PreparationRecord, role string) error {
	if source.Native == nil || source.Native.Profile != port.Config.Profile || !project.ValidFactoryRole(role) {
		return errors.New("standing preparation requires the same native source owner")
	}
	records, err := db.StandingProjectPreparations(ctx, source.Preparation.Project)
	if err != nil {
		return err
	}
	for _, record := range records {
		p := record.Preparation
		if p.Role != role || p.SourceCommit != source.Preparation.SourceCommit || p.SetupDigest != source.Preparation.SetupDigest || !p.Requirements.Equal(source.Preparation.Requirements) || p.Approval != source.Preparation.Approval || record.Native == nil || record.Native.Profile != source.Native.Profile || record.Native.InstanceID != port.Instance {
			continue
		}
		state, err := port.InspectWorkspacePreparation(ctx, record.Address())
		if err == nil && state.Ready && !state.Stopped && state.ValidateFor(record.Address()) == nil {
			return nil
		}
	}
	var request execution.StandingPreparationRequest
	entries, err := os.ReadDir(filepath.Join(port.Config.Directory, "native-preparations"))
	if err != nil {
		return err
	}
	matched := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(port.Config.Directory, "native-preparations", entry.Name(), "request.json"))
		if e != nil {
			return e
		}
		var saved execution.StandingPreparationRequest
		if e = json.Unmarshal(raw, &saved); e != nil {
			return e
		}
		if saved.Input.Preparation.ID == source.Preparation.ID {
			request = saved
			matched++
		}
	}
	if matched != 1 {
		return errors.New("exact retained source preparation request missing or ambiguous")
	}
	reviewer := source
	reviewer.Preparation.ID, reviewer.Preparation.Revision, reviewer.Preparation.Role = "f"+factory.NewID()[:24], 0, role
	reviewer.State = execution.PreparationState{}
	ref := *source.Native
	ref.InstanceID = port.Instance
	ref.WorktreeID = reviewer.Preparation.ID
	reviewer.Native = &ref
	reviewer, _, err = db.AdmitStandingPreparation(ctx, reviewer)
	if err != nil {
		return err
	}
	request.Input.Preparation, request.Native = reviewer.Preparation, reviewer.Native
	state, err := port.PrepareWorkspace(ctx, request)
	for err == nil && !state.Ready && state.Phase == project.PrepareRunning {
		time.Sleep(100 * time.Millisecond)
		state, err = port.InspectWorkspacePreparation(ctx, reviewer.Address())
	}
	if err != nil {
		return err
	}
	if !state.Ready {
		return fmt.Errorf("reviewer standing setup: %s %s", state.Phase, state.Output)
	}
	reviewer.State = state
	if err = db.ObservePreparation(ctx, reviewer); err != nil {
		return err
	}
	return nil
}

func nativeReviewChildFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, child factory.Assignment, run factory.Run) error {
	correction := os.Getenv("SODA_NATIVE_CHECK_CORRECTION") == "1"
	if correction {
		if id, err := os.ReadFile(filepath.Join(port.Config.Directory, "check-native-correction.id")); err == nil {
			return checkNativeCorrection(ctx, db, port, string(id), child.Result.Candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	owner, err := db.Assignment(ctx, child.PublicationAssignment)
	if err != nil {
		return err
	}
	source, err := db.Preparation(ctx, child.Preparation)
	if err != nil {
		return err
	}
	if source.Native == nil {
		return errors.New("native review source has no captured owner")
	}
	if source.Native.InstanceID != port.Instance {
		records, err := db.ProjectPreparations(ctx, owner.ProjectID)
		if err != nil {
			return err
		}
		var matches []execution.PreparationRecord
		for _, record := range records {
			if record.Preparation.Role == project.RoleReviewer && record.Preparation.SourceCommit == child.Result.Candidate {
				matches = append(matches, record)
			}
		}
		if len(matches) != 1 {
			return errors.New("retained review admission missing or ambiguous")
		}
		prepared := matches[0]
		admitted, err := db.CandidatePreparation(ctx, prepared.Preparation.ID)
		if err != nil {
			return err
		}
		if admitted.Admission.OwnerAssignment != owner.ID || admitted.Admission.AttemptRoot != owner.AttemptRoot || prepared.Native == nil || prepared.Native.InstanceID != source.Native.InstanceID || prepared.Native.Profile != source.Native.Profile || prepared.Native.WorktreeID != prepared.Preparation.ID || !(prepared.State.Ready || admitted.Stopped && admitted.Retirement == "confirmed") {
			return errors.New("retained review admission differs from the original candidate/owner")
		}
		fmt.Println("PASS: retained reviewer admission keeps its original candidate, native owner and recorded setup/retirement facts; no preparation replay or live-resource recovery proof")
		return nil
	}
	if err = nativeStandingRole(ctx, db, port, source, project.RoleReviewer); err != nil {
		return err
	}
	var state execution.PreparationState
	target := factory.CheckTarget{Repository: owner.Repository, PRNumber: 41, PRID: 43, IssueID: 47, HeadRef: factory.PublishBranchName(owner.ID), BaseRef: "refs/heads/main", HeadOID: child.Result.Candidate, BaseOID: owner.SourceCommit}
	p := declaredPublishedFixture(target, collaboration.ActorID(owner.ActorID), owner.ID, owner.Issue, owner.NativeRev, run.Deadline.Unix())
	p.ProjectID, p.Acceptance, p.Preparation, p.Run, p.Authority = owner.ProjectID, owner.Acceptance, child.Preparation, run.ID, owner.Authority
	prior, priorErr := db.PublicationByAssignment(ctx, owner.ID)
	declared := priorErr == nil && prior.Stage == factory.PublicationPublished && prior.Run == run.ID && prior.Candidate == target.HeadOID && prior.PRNumber == target.PRNumber && prior.PRID == target.PRID && prior.Publish.Receipt == "declared publication fixture" && prior.PRCreate.Receipt == "declared publication fixture"
	if declared {
		p = prior
	} else if priorErr == nil {
		if prior.Stage != factory.PublicationOpen || prior.Run != run.ID || prior.Candidate != target.HeadOID || prior.Publish.Attempts != 0 || prior.PRCreate.Attempts != 0 || prior.Publish.Work != nil || prior.PRCreate.Work != nil {
			return errors.New("existing publication has driven work; preserve its original journal")
		}
		p.ID, p.Revision = prior.ID, prior.Revision
		before, e := json.Marshal(prior)
		if e != nil {
			return e
		}
		file, e := os.OpenFile(filepath.Join(port.Config.Directory, "review-child-publication-before.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = file.Write(before)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !errors.Is(priorErr, store.ErrNotFound) {
		return priorErr
	}
	if err = p.Validate(); err != nil {
		return err
	}
	conn, err := sql.Open("pgx", port.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if declared {
		// Resume the same declared provider boundary without replacing its
		// subsequently admitted child/cycle history.
	} else if priorErr == nil {
		var changed sql.Result
		changed, err = conn.ExecContext(ctx, `UPDATE factory_publications SET stage=$2,data=$3 WHERE assignment=$1 AND revision=$4`, p.AssignmentID, p.Stage, string(raw), prior.Revision)
		if err == nil {
			var count int64
			count, err = changed.RowsAffected()
			if err == nil && count != 1 {
				err = errors.New("publication changed before the declared fixture boundary")
			}
		}
	} else {
		_, err = conn.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, p.AssignmentID, p.Repository, p.Issue, p.Run, p.Stage, string(raw))
	}
	if err != nil {
		return err
	}
	currentTool := run.Native.Tool
	toolBytes, err := os.ReadFile(currentTool.Executable)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(toolBytes)
	currentTool.SHA256 = hex.EncodeToString(sum[:])
	host := nativeReviewPreparationHost{NativeExecution: port, tool: execution.ToolObservation{Kind: collaboration.Native, Native: &currentTool}}
	c := control.NewCoordinator(db, host, port)
	defer c.Close()
	c.MCPSecret = factoryruntime.NativeMCPSecretDeriver(port.Config, port.Instance)
	c.AcceptanceReads, c.DispatchReads = nativeReviewChildReads{owner.ActorID, owner.SourceCommit}, nativeReviewChildReads{owner.ActorID, owner.SourceCommit}
	c.Publication = factoryruntime.NativePublication{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: port}}
	c.Reviews = factoryruntime.NativeReviews{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: port}}
	if correction {
		c.Reviews = &nativeDeclaredReview{}
	}
	report := c.PublishPass(ctx)
	if len(report.Errors) != 0 || len(report.Fenced) != 0 {
		return fmt.Errorf("native child producer: %+v", report)
	}
	var id string
	if err = conn.QueryRowContext(ctx, `SELECT id FROM project_preparations WHERE data#>>'{factory_admission,publication_id}'=$1`, p.ID).Scan(&id); err != nil {
		return fmt.Errorf("native child admission absent: %+v: %w", report, err)
	}
	prepared, err := db.CandidatePreparation(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("Recorded review preparation: current_native_owner=%t phase=%s\n", prepared.Preparation.Native != nil && prepared.Preparation.Native.InstanceID == port.Instance, prepared.Preparation.State.Phase)
	for !prepared.Preparation.State.Ready {
		state, err = port.InspectWorkspacePreparation(ctx, prepared.Preparation.Address())
		if err != nil {
			return fmt.Errorf("native review candidate inspection: %w", err)
		}
		if state.Phase != project.PrepareRunning && !state.Ready {
			return fmt.Errorf("native review candidate: %s %s", state.Phase, state.Output)
		}
		if state.Ready {
			break
		}
		time.Sleep(100 * time.Millisecond)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	prepared.Preparation.State = state
	if err = db.ObservePreparation(ctx, prepared.Preparation); err != nil {
		return err
	}
	prepared, err = db.CandidatePreparation(ctx, id)
	if err != nil {
		return err
	}
	n := prepared.Preparation.Native
	if n == nil || n.Profile != port.Config.Profile || n.InstanceID != port.Instance || n.WorktreeID != id || prepared.Preparation.Preparation.Role != project.RoleReviewer || !prepared.Preparation.State.Ready || prepared.Preparation.Preparation.SourceCommit != target.HeadOID {
		return errors.New("coordinator did not admit the exact native review candidate")
	}
	fmt.Println("PASS: Go publication child producer admits and prepares the actual native candidate for an independent reviewer; provider publication and authority facts declared, no model/complete review")
	policy, err := db.RepositoryPolicy(ctx, owner.Repository)
	if err != nil {
		return err
	}
	checks := factory.ObservedChecks{NativeRev: p.NativeRev, HeadTip: target.HeadOID, BaseTip: target.BaseOID, Complete: true, ObservedContexts: len(policy.Checks)}
	for _, name := range policy.Checks {
		checks.Checks = append(checks.Checks, factory.ObservedCheck{Context: name, State: factory.CheckStateSuccess})
	}
	assessment, err := factory.VerifyChecks(target, factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}, policy, checks, time.Now().Unix())
	if err != nil || assessment.Verdict != factory.CheckPass {
		return fmt.Errorf("declared reviewer prerequisite checks: %s: %w", assessment.Verdict, err)
	}
	if _, err = db.RecordCheckAssessment(ctx, assessment); err != nil {
		return err
	}
	report = c.PublishPass(ctx)
	if len(report.Errors) != 0 || len(report.Fenced) != 0 {
		return fmt.Errorf("native reviewer dispatch: %+v", report)
	}
	p, err = db.PublicationByAssignment(ctx, owner.ID)
	if err != nil || len(p.ReviewCycles) != 1 {
		return fmt.Errorf("reviewer cycle unavailable: %w", err)
	}
	review, err := db.Assignment(ctx, p.ReviewCycles[0].ReviewerAssignment)
	if err != nil {
		return fmt.Errorf("reviewer assignment absent: %+v: %w", report, err)
	}
	for {
		if err = settleRetiredNativeFixture(ctx, c, port, review.Run, review.ProjectID); err != nil {
			return err
		}
		reviewerRun, err := db.FactoryRun(ctx, review.Run)
		if err != nil {
			return err
		}
		if review.Role != project.RoleReviewer || review.Phase != project.ExecutionReview || review.Preparation != id || reviewerRun.Native == nil || reviewerRun.Native.Provenance.WorktreeID != id || reviewerRun.Native.BundleSHA256 != prepared.Preparation.State.SourceBundleSHA256 || reviewerRun.InputSHA != target.HeadOID {
			return errors.New("reviewer native packet does not capture its prepared candidate")
		}
		if reviewerRun.Reconciled {
			if _, err = control.SettleMCPTerminalProposal(ctx, db, review.ID, time.Now()); err != nil {
				return err
			}
			review, err = db.Assignment(ctx, review.ID)
			if err != nil {
				return err
			}
			terminal, found, err := db.TerminalMCPProposal(ctx, review.ID)
			if err != nil {
				return err
			}
			if found && terminal.State == factory.MCPWorkAccepted && review.Stage == factory.AssignmentFinished {
				expected := factory.NeedsHuman
				if correction {
					expected = factory.Succeeded
				}
				if review.Outcome != expected || !reviewerRun.Native.Settled() {
					return errors.New("incomplete reviewer result or native retirement differs")
				}
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if correction {
		fmt.Println("PASS: actual independent reviewer finding and terminal proposal settled with native retirement")
		if _, err = c.SubmitReviewForRun(ctx, review.Run); err != nil {
			return fmt.Errorf("declared provider boundary for actual reviewer report: %w", err)
		}
		lastWait := ""
		for {
			report = c.PublishPass(ctx)
			if waiting := fmt.Sprint(report.Waits); waiting != lastWait {
				fmt.Println("Native correction dispatch waits:", waiting)
				lastWait = waiting
			}
			p, err = db.PublicationByAssignment(ctx, owner.ID)
			if err != nil {
				return err
			}
			last := p.ReviewCycles[len(p.ReviewCycles)-1]
			if last.CorrectionAssignment != "" {
				corrector, found := db.Assignment(ctx, last.CorrectionAssignment)
				if found == nil {
					if err = os.WriteFile(filepath.Join(port.Config.Directory, "check-native-correction.id"), []byte(corrector.Run), 0600); err != nil {
						return err
					}
					return checkNativeCorrection(ctx, db, port, corrector.Run, child.Result.Candidate)
				}
			}
			if len(report.Errors) != 0 || len(report.Fenced) != 0 || ctx.Err() != nil {
				return fmt.Errorf("actual native correction dispatch: %+v: %v", report, ctx.Err())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	fmt.Println("PASS: actual coordinator reviewer dispatch captures candidate workspace/bundle, launches the independent CLI worker, and settles its explicit incomplete result with native retirement; CI/provider facts declared, no model or GitHub effect")
	return nil
}

// Only the provider facts are declared. The review body is produced by the
// admitted reviewer, and the coordinator retains its real work and receipt.
type nativeDeclaredReview struct{ work factory.ReviewWork }

func (*nativeDeclaredReview) ObserveReview(ctx context.Context, w factory.ReviewWork) (factory.ReviewObservation, error) {
	return factory.ReviewObservation{NativeRev: 1, ObservedUnix: time.Now().Unix(), PRID: w.PRID, PRNumber: w.PRNumber, IssueID: w.IssueID, PRAuthorID: w.PRAuthorID, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID}, ctx.Err()
}
func (r *nativeDeclaredReview) SubmitReview(ctx context.Context, w factory.ReviewWork) (factory.OperationOutcome, error) {
	if w.Validate() != nil || ctx.Err() != nil {
		return factory.OperationOutcome{}, errors.New("declared review work invalid")
	}
	r.work = w
	return r.LookupOp(ctx, w.OperationID)
}
func (r *nativeDeclaredReview) LookupOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	if id != r.work.OperationID {
		return factory.OperationOutcome{OperationID: id, NotObserved: true}, ctx.Err()
	}
	w := r.work
	return factory.OperationOutcome{OperationID: id, InstallationID: "declared-local-provider", Kind: factory.OpReviewSubmit, ActorID: w.ActorID, RepositoryID: w.Repository, Receipt: []byte("declared provider fixture; no remote review"), Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete}, ctx.Err()
}
func (r *nativeDeclaredReview) CancelOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	return r.LookupOp(ctx, id)
}
func (r *nativeDeclaredReview) AdoptReview(ctx context.Context, w factory.ReviewWork, o factory.OperationOutcome) (factory.ReviewOutcome, error) {
	if !reflect.DeepEqual(w, r.work) || o.Effect != factory.OpEffectCommitted || o.OperationID != w.OperationID {
		return factory.ReviewOutcome{}, errors.New("declared review receipt differs")
	}
	return factory.ReviewOutcome{Operation: o, ReviewID: 101, ReviewerID: w.ActorID, PRID: w.PRID, PRNumber: w.PRNumber, IssueID: w.IssueID, HeadOID: w.HeadOID, BaseOID: w.BaseOID, CommitID: w.HeadOID, MergeBaseOID: w.BaseOID, Event: w.Event}, ctx.Err()
}

func checkNativeCorrection(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, id, source string) error {
	c := control.NewCoordinator(db, port, port)
	defer c.Close()
	for {
		run, err := db.FactoryRun(ctx, id)
		if err != nil {
			return err
		}
		a, err := db.AssignmentByRun(ctx, id)
		if err != nil {
			return err
		}
		if a.Phase != project.ExecutionCorrect || a.SourceCommit != source || a.Preparation != factory.NativeCorrectionPreparationID(a.ID) || run.Native == nil {
			return errors.New("correction did not bind its admitted candidate preparation")
		}
		if !run.Reconciled {
			if err = settleRetiredNativeFixture(ctx, c, port, id, a.ProjectID); err != nil {
				return err
			}
		}
		if run.Reconciled {
			if _, err = control.SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); err != nil {
				return err
			}
			if a.Stage != factory.AssignmentFinished || a.Outcome != factory.Succeeded || run.FinalCheckpoint == nil || run.FinalCheckpoint.Status != "committed" || a.Result == nil || a.Result.Candidate != run.FinalCheckpoint.Candidate || a.Result.Candidate == source || !run.Native.Settled() {
				return fmt.Errorf("actual correction did not complete: outcome=%s reason=%s run=%s summary=%s", a.Outcome, a.Reason, run.Outcome, run.Summary)
			}
			if _, err = port.ExportCandidate(ctx, execution.CandidateRequest{Address: execution.RunAddress{Workspace: a.ProjectID, Run: id}, Preparation: a.Preparation, Role: a.Role, Candidate: a.Result.Candidate}); err != nil {
				return err
			}
			fmt.Println("PASS: actual coordinator correction dispatch, singleton MCP edit/checkpoint, changed retained candidate, native retirement and canonical settlement; provider review facts declared, no model or remote write")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func settleRetiredNativeFixture(ctx context.Context, c *control.Coordinator, port factoryruntime.NativeExecution, id, workspace string) error {
	observed, err := port.InspectRun(ctx, execution.RunAddress{Run: id, Workspace: workspace})
	if err == nil && observed.Retirement == execution.Retired {
		_, err = c.Reconcile(ctx)
		return err
	}
	return ctx.Err()
}

// Declared repository visibility; actual native keyring/owner observation,
// canonical grant transaction and withdrawal after key removal.
func nativeSponsorshipFixture(profile, instance, connection string, active bool) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	id, err := os.ReadFile(filepath.Join(profile, "native-recovery-canonical.id"))
	if err != nil {
		return err
	}
	run, err := db.FactoryRun(ctx, string(id))
	if err != nil {
		return err
	}
	intent, err := execution.ParseNativeIntent(run.Native.Intent)
	if err != nil || intent.Version != 3 || intent.Connection != connection {
		return errors.New("native sponsorship fixture lacks exact credential binding")
	}
	rawDB, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer rawDB.Close()
	var repository collaboration.RepositoryID
	if err = rawDB.QueryRowContext(ctx, `SELECT repository FROM factory_native_workspaces WHERE id=$1`, run.ProjectID).Scan(&repository); err != nil {
		return err
	}
	environment, err := db.EnvironmentGrant(ctx, int64(repository))
	if err != nil {
		return err
	}
	actor := collaboration.ActorID(environment.Owner)
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	coordinator := control.NewCoordinator(db, port, port)
	defer coordinator.Close()
	read := 0
	service := factoryservice.Sponsorships{Store: db, Writer: coordinator,
		Visibility: func(ctx context.Context, selected collaboration.ActorID, repo collaboration.RepositoryID) error {
			if selected != actor || repo != repository {
				return errors.New("declared fixture visibility changed")
			}
			return ctx.Err()
		}, Custody: func(ctx context.Context, _ collaboration.ActorID, _ collaboration.RepositoryID, selected string) (factoryservice.SponsorshipCustody, error) {
			read++
			account, err := port.ExecutionAccount(ctx, selected)
			if err != nil {
				return factoryservice.SponsorshipCustody{}, err
			}
			return factoryservice.SponsorshipCustody{Repository: repository, Connection: selected, Project: run.ProjectID, Native: &account}, nil
		}}
	in := factoryservice.SponsorshipChange{Roles: []string{run.Role}, Active: active}
	if !active {
		prior, err := db.Sponsorship(ctx, int64(repository), connection)
		if err != nil {
			return err
		}
		in.ExpectedRevision = prior.Revision
		if _, err := port.ExecutionAccount(ctx, connection); err == nil {
			return errors.New("removed native credential remained available")
		}
	}
	if _, err := service.Apply(ctx, actor, "native-source-fixture", repository, connection, in); err != nil {
		return err
	}
	saved, err := db.Sponsorship(ctx, int64(repository), connection)
	if err != nil {
		return err
	}
	if saved.Validate() != nil || saved.Native == nil || saved.Native.Profile != config.Profile || saved.Native.CredentialSHA256 != intent.Native.CredentialSHA256 || saved.ActorID != int64(actor) || saved.Generation != 0 || saved.GrantID != "" || saved.Active != active || active && read != 1 || !active && read != 0 {
		return errors.New("native sponsorship lost credential identity or invented a broker grant")
	}
	if active {
		// Provider identity/administration are declared here. The canonical
		// account mapping, policy service and persisted native references are real.
		binding, err := db.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{
			Profile:  collaboration.Profile{ID: config.Profile, Name: "Native source fixture"},
			Provider: collaboration.GitHub, APIBase: "https://api.github.com", Connection: collaboration.ConnectionID(factory.NewID()),
			Account: "native-source-owner", Login: "native-source-owner",
		})
		if err != nil || binding.Actor.ID != actor {
			return errors.New("native policy fixture actor mapping changed")
		}
		selected := factoryservice.PolicyActor{NativeProfile: config.Profile, NativeConnection: binding.Connection.ID, ActorID: int64(actor)}
		change := factoryservice.PolicyChange{Enabled: true, TargetBranch: "refs/heads/main", MergeMethod: factory.MergeFastForward,
			RequiredChecks: []string{"fixture/check"}, PublishActor: selected, CreateActor: selected, ReviewActor: selected, MergeActor: selected,
			Roles: map[string]factory.RoleSelection{project.RoleCoder: {Account: connection, Harness: project.FactoryHarnessMuse, Model: run.Model}, project.RoleReviewer: {Account: connection, Harness: project.FactoryHarnessMuse, Model: run.Model}}}
		if prior, err := db.RepositoryPolicy(ctx, int64(repository)); err == nil {
			change.ExpectedRevision = prior.Revision
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		policies := factoryservice.Policies{Writer: coordinator,
			WriteAccess: func(ctx context.Context, viewer collaboration.ActorID, repo int64) error {
				if viewer != actor || repo != int64(repository) {
					return errors.New("declared fixture administration changed")
				}
				return ctx.Err()
			}, BindingAccess: func(ctx context.Context, viewer collaboration.ActorID, repo collaboration.RepositoryID, ref factory.ActorBindingRef) error {
				if viewer != actor || repo != repository || ref.NativeProfile != config.Profile || ref.NativeConnection != binding.Connection.ID || ref.ActorID != int64(actor) || ref.TokenID != 0 {
					return factoryservice.ErrPolicyBindingUnavailable
				}
				return ctx.Err()
			}}
		mixed := change
		mixed.PublishActor.TokenID = 1
		if _, err = policies.Apply(ctx, actor, "native-source-fixture", repository, mixed); !errors.Is(err, factoryservice.ErrPolicyBindingInvalid) {
			return errors.New("policy mixed desktop and appliance actor authority")
		}
		if _, err = policies.Apply(ctx, actor, "native-source-fixture", repository, change); err != nil {
			return err
		}
	}
	policy, err := db.RepositoryPolicy(ctx, int64(repository))
	if err != nil {
		return err
	}
	for _, ref := range []factory.ActorBindingRef{policy.Publish, policy.Create, policy.Review, policy.Merge} {
		if ref.Validate() != nil || ref.NativeProfile != config.Profile || ref.NativeConnection == "" || ref.TokenID != 0 || ref.ActorID != int64(actor) {
			return errors.New("native policy lost its account binding across persistence/restart")
		}
	}
	fmt.Println("PASS: canonical native policy bindings; mixed appliance authority refused; retained native account references")
	fmt.Println("PASS: actual native credential binding and canonical sponsorship; active:", active, "; withdrawal needs no removed key")
	return nil
}

// Extend the existing admitted setup journey with an official toolchain and a cold public dependency.
func nativeProjectToolchain(files map[string][]byte, tools []string) ([]string, error) {
	path := os.Getenv("SODA_NATIVE_CHECK_PROJECT_TOOLCHAIN")
	if path == "" {
		return tools, nil
	}
	var selected struct {
		Kind, Version, Date, Dependency string
		Assets                          map[string]string
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &selected); err != nil {
		return nil, err
	}
	numeric := func(value string, extra rune) bool {
		if value == "" || len(value) > 30 {
			return false
		}
		for _, c := range value {
			if !(c >= '0' && c <= '9' || c == '.' || c == extra) {
				return false
			}
		}
		return true
	}
	if !numeric(selected.Version, '.') {
		return nil, errors.New("toolchain version invalid")
	}
	for _, digest := range selected.Assets {
		if !project.ValidDigest(digest) {
			return nil, errors.New("toolchain digest invalid")
		}
	}
	files["setup-network.json"] = []byte(`{"access":"public_https"}`)
	files["setup-limits.json"] = []byte(`{"setup_seconds":240,"check_seconds":60}`)
	install := "test ! -e \"$HOME/.factory-setup-once\"\nprintf ready > \"$HOME/.factory-setup-once\"\nmkdir -p \"$HOME/.local/bin\"\n"
	var check string
	switch selected.Kind {
	case "go":
		if len(selected.Assets) != 1 || !project.ValidDigest(selected.Assets["go"]) || !strings.HasPrefix(selected.Dependency, "v") || !numeric(strings.TrimPrefix(selected.Dependency, "v"), '.') {
			return nil, errors.New("Go toolchain selection invalid")
		}
		tools = append(tools, "curl", "tar", "go")
		install += fmt.Sprintf(`curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 90 'https://go.dev/dl/go%s.linux-amd64.tar.gz' -o "$TMPDIR/go.tar.gz"
printf '%%s  %%s\n' '%s' "$TMPDIR/go.tar.gz" | sha256sum --check --status
tar -xzf "$TMPDIR/go.tar.gz" -C "$HOME/.local"
ln -s ../go/bin/go "$HOME/.local/bin/go"
rm "$TMPDIR/go.tar.gz"
mkdir -p .factory-build/go-project
cp /run/factory-source/approved/go.mod /run/factory-source/approved/main.go .factory-build/go-project/
chmod 600 .factory-build/go-project/go.mod .factory-build/go-project/main.go
cd .factory-build/go-project
go mod tidy
go build -trimpath -o app .
test "$(./app)" = 'go-project-ok'
`, selected.Version, selected.Assets["go"])
		files["go.mod"] = []byte(fmt.Sprintf("module factory-setup-fixture\n\ngo 1.27.0\n\nrequire github.com/google/uuid %s\n", selected.Dependency))
		files["main.go"] = []byte(`package main
import ("fmt"; "github.com/google/uuid")
func main() { if uuid.MustParse("00000000-0000-0000-0000-000000000000") != uuid.Nil { panic("uuid") }; fmt.Print("go-project-ok") }
`)
		check = fmt.Sprintf(`test "$(go version)" = 'go version go%s linux/amd64'
cd .factory-build/go-project
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go build -trimpath -o app .
test "$(./app)" = 'go-project-ok'
printf 'checked-by-native private Go toolchain and cold module build\n'
`, selected.Version)
	case "rust":
		if len(selected.Assets) != 3 || !numeric(selected.Date, '-') || !numeric(selected.Dependency, '.') {
			return nil, errors.New("Rust toolchain selection invalid")
		}
		tools = append(tools, "curl", "tar", "cargo", "rustc", "cc")
		for _, name := range []string{"rustc", "rust-std", "cargo"} {
			if !project.ValidDigest(selected.Assets[name]) {
				return nil, errors.New("Rust component digest missing")
			}
			install += fmt.Sprintf(`curl --fail --silent --show-error --proto '=https' --max-time 90 --dump-header "$TMPDIR/component-headers" 'https://static.rust-lang.org/dist/%s/%s-%s-x86_64-unknown-linux-gnu.tar.xz' -o "$TMPDIR/component.tar.xz" || { cat "$TMPDIR/component-headers"; exit 1; }
printf '%%s  %%s\n' '%s' "$TMPDIR/component.tar.xz" | sha256sum --check --status
tar -xJf "$TMPDIR/component.tar.xz" --strip-components=2 -C "$HOME/.local" "%s-%s-x86_64-unknown-linux-gnu/%s"
rm "$TMPDIR/component.tar.xz"
`, selected.Date, name, selected.Version, selected.Assets[name], name, selected.Version, map[string]string{"rustc": "rustc", "cargo": "cargo", "rust-std": "rust-std-x86_64-unknown-linux-gnu"}[name])
		}
		files["Cargo.toml"] = []byte(fmt.Sprintf("[package]\nname = \"factory-setup-fixture\"\nversion = \"0.1.0\"\nedition = \"2024\"\n[dependencies]\nitoa = \"=%s\"\n", selected.Dependency))
		files["main.rs"] = []byte(`fn main() { assert_eq!(itoa::Buffer::new().format(42), "42"); print!("rust-project-ok"); }
`)
		install += `mkdir -p .factory-build/rust-project/src
cp /run/factory-source/approved/Cargo.toml .factory-build/rust-project/
cp /run/factory-source/approved/main.rs .factory-build/rust-project/src/
chmod 600 .factory-build/rust-project/Cargo.toml .factory-build/rust-project/src/main.rs
cd .factory-build/rust-project
cargo build
test "$(./target/debug/factory-setup-fixture)" = 'rust-project-ok'
`
		check = fmt.Sprintf(`rustc --version | grep -F 'rustc %s '
cd .factory-build/rust-project
cargo build --offline --locked
test "$(./target/debug/factory-setup-fixture)" = 'rust-project-ok'
printf 'checked-by-native private Rust toolchain and cold crate build\n'
`, selected.Version)
	default:
		return nil, errors.New("unsupported project toolchain fixture")
	}
	files["setup.sh"] = append(files["setup.sh"], []byte(install)...)
	files["check.sh"] = append(files["check.sh"], []byte(check)...)
	return tools, nil
}

// Declared Go permissions; source/setup and observation/CAS transactions are real.
// This fixture supplies no provider consent.
func nativePreparationFixture(profile, instance, mode string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	duration := 25 * time.Second
	if mode == "completion" {
		duration = 55 * time.Second
	}
	if os.Getenv("SODA_NATIVE_CHECK_SETUP_BUDGET") == "1" {
		duration = 50 * time.Second
	}
	if os.Getenv("SODA_NATIVE_CHECK_PUBLIC_SETUP") == "1" || os.Getenv("SODA_NATIVE_CHECK_BUN_RELEASE") != "" {
		duration = 90 * time.Second
	}
	if os.Getenv("SODA_NATIVE_CHECK_PROJECT_TOOLCHAIN") != "" {
		duration = 330 * time.Second
	}
	if os.Getenv("SODA_NATIVE_CHECK_FACTORY_BUILD") == "1" {
		duration = 180 * time.Second
	}
	if mode == "network-limit" {
		duration = 150 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	if mode == "fences-retained" {
		var addresses []execution.PreparationAddress
		raw, err := os.ReadFile(filepath.Join(profile, "preparation-fences.json"))
		if err != nil || json.Unmarshal(raw, &addresses) != nil || len(addresses) != 64 {
			return errors.New("original no-start fences missing")
		}
		port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
		for _, address := range addresses {
			state, err := port.InspectWorkspacePreparation(ctx, address)
			if err != nil || address.Native.InstanceID == instance || !state.Stopped || state.Retirement != "confirmed" {
				return fmt.Errorf("original no-start fence unavailable after reopen: %v", err)
			}
		}
		fmt.Println("PASS: all original no-start preparation fences remain confirmed across native restart without setup or claim replay")
		return nil
	}
	if err = db.InitializeEmptyCollaborationLedger(ctx); err != nil {
		return err
	}
	if mode == "retained" {
		var fixture struct{ Preparation string }
		raw, err := os.ReadFile(filepath.Join(profile, "admitted-native-fixture.json"))
		if err != nil || json.Unmarshal(raw, &fixture) != nil || !project.ValidPreparationID(fixture.Preparation) {
			return errors.New("original preparation fixture is unavailable")
		}
		record, err := db.Preparation(ctx, fixture.Preparation)
		if err != nil || record.Native == nil || record.Native.InstanceID == instance {
			return errors.New("original preparation instance was not retained")
		}
		port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
		state, err := port.InspectWorkspacePreparation(ctx, record.Address())
		if err != nil {
			return err
		}
		if !state.Stopped || state.Retirement != "confirmed" || state.Native == nil || *state.Native != *record.Native || state.SourceBundleSHA256 != record.State.SourceBundleSHA256 {
			return errors.New("reopen did not adopt the exact original physical retirement")
		}
		entries, err := os.ReadDir(filepath.Join(profile, "native-preparations"))
		if err != nil {
			return err
		}
		var original json.RawMessage
		for _, entry := range entries {
			raw, err := os.ReadFile(filepath.Join(profile, "native-preparations", entry.Name(), "request.json"))
			if err != nil {
				return err
			}
			var request execution.StandingPreparationRequest
			if json.Unmarshal(raw, &request) != nil {
				return errors.New("retained preparation request is invalid")
			}
			if request.Input.Preparation.ID == fixture.Preparation {
				original = raw
			}
		}
		if len(original) == 0 {
			return errors.New("original preparation request was lost")
		}
		var ignored execution.PreparationState
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "prepare", "request": original}, &ignored); err == nil {
			return errors.New("old preparation constructor regained authority on reopen")
		}
		after, err := os.ReadDir(filepath.Join(profile, "native-preparations"))
		if err != nil || len(after) != len(entries) {
			return errors.New("refused old preparation created another native owner")
		}
		if os.Getenv("SODA_NATIVE_CHECK_RETIRED_SOURCE") == "1" {
			if err := nativeRetiredSourceFixture(ctx, db, port, record, state); err != nil {
				return err
			}
		}
		fmt.Println("PASS: native reopen adopts exact old preparation/host retirement; its original instance/request cannot reconstruct or replay setup")
		return nil
	}
	var host collaboration.Host
	var repo collaboration.RepositoryRef
	var actor collaboration.ActorRef
	var completionConnection collaboration.ConnectionID
	if mode == "completion" {
		detail, e := db.FeedPost(ctx, os.Getenv("SODA_NATIVE_CHECK_COMPLETION_POST"))
		if e != nil {
			return e
		}
		repo, err = db.CollaborationRepository(ctx, collaboration.RepositoryID(detail.Record.Repository))
		if err != nil {
			return err
		}
		host, err = db.CollaborationHost(ctx, repo.Host)
		if err != nil {
			return err
		}
		id, e := strconv.ParseInt(detail.Record.Author, 10, 64)
		if e != nil {
			return e
		}
		actor, err = db.CollaborationActor(ctx, collaboration.ActorID(id))
		if err != nil {
			return err
		}
		proof, e := db.IssueIntakeGrant(ctx, detail.Record.ID, detail.Record.Revision)
		if e != nil {
			return e
		}
		var grant struct {
			Connection collaboration.ConnectionID `json:"connection"`
		}
		if json.Unmarshal(proof, &grant) != nil || grant.Connection == "" {
			return errors.New("completion fixture lacks captured account")
		}
		completionConnection = grant.Connection
	} else if controller, e := controllerFixture(profile); e != nil {
		return e
	} else if controller != nil {
		binding, e := db.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{Profile: collaboration.Profile{ID: config.Profile, Name: "Native source fixture"}, Provider: collaboration.Fountain, APIBase: controller.Origin, Connection: collaboration.ConnectionID(controller.Connection), Account: controller.Actor, Login: "user2"})
		if e != nil {
			return e
		}
		host, actor = binding.Host, binding.Actor
		repo, err = db.ObserveCollaborationRepository(ctx, host.ID, controller.Repository, "user2/repo1")
		if err != nil {
			return err
		}
	} else {
		host, err = db.PutCollaborationHost(ctx, collaboration.Host{ID: collaboration.HostID(factory.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"})
		if err != nil {
			return err
		}
		repo, err = db.ObserveCollaborationRepository(ctx, host.ID, "native-source-fixture", "fixture/native-source")
		if err != nil {
			return err
		}
		actor, err = db.ObserveCollaborationActor(ctx, host.ID, "native-source-owner", "native-source-owner")
		if err != nil {
			return err
		}
	}
	if err = db.PutCollaborationProfile(ctx, collaboration.Profile{ID: config.Profile, Name: "Native source fixture"}); err != nil {
		return err
	}
	var placement factory.Placement
	if controller, e := controllerFixture(profile); e != nil {
		return e
	} else if controller != nil {
		placement, err = db.Placement(ctx, repo.ID)
		if err == nil && (placement.Coordination != factory.FountainCentral || placement.RequireDispatch(host.ID, repo.ID, collaboration.RuntimeID(config.Profile), placement.Generation) != nil) {
			return factory.ErrPlacement
		}
	} else {
		placement, err = db.SelectPlacement(ctx, host.ID, repo.ID, actor.ID, collaboration.RuntimeRef{ID: collaboration.RuntimeID(config.Profile), Profile: config.Profile, Kind: collaboration.Native, Name: "Native source fixture"}, "", factory.PlacementActive)
	}
	if err != nil {
		return err
	}
	native := &project.NativeEnvironment{Profile: config.Profile, Runtime: placement.Runtime, Generation: placement.Generation}
	if controller, err := controllerFixture(profile); err != nil {
		return err
	} else if controller != nil {
		native.ControllerConnection, native.ControllerGrantID = collaboration.ConnectionID(controller.Connection), controller.Grant
	}
	if _, err = db.ApplyFactoryGrant(ctx, "native-source-fixture", project.EnvironmentGrant{Repository: int64(repo.ID), Owner: int64(actor.ID), Native: native, Active: true}); err != nil {
		return err
	}
	grant, err := db.EnvironmentGrant(ctx, int64(repo.ID))
	if err != nil {
		return err
	}
	setup := []byte("printf 'prepared-by-native\\n'\n")
	if mode == "pending" {
		setup = []byte("sleep 8\n")
	}
	check := []byte("printf 'checked-by-native\\n'\n")
	candidatePath, candidateText := "docs/note.md", "native-context-change"
	if selected := os.Getenv("SODA_NATIVE_CANDIDATE_PATH"); selected != "" {
		candidatePath, candidateText = selected, os.Getenv("SODA_NATIVE_CANDIDATE_TEXT")
		if candidateText == "" {
			return errors.New("candidate file needs its independently observed content")
		}
	}
	if os.Getenv("SODA_NATIVE_PREPARE_CANDIDATE") == "1" {
		quoted := "'" + strings.ReplaceAll(candidatePath, "'", "'\\''") + "'"
		check = []byte("printf 'checked-by-native\\n'\ngit rev-parse --verify HEAD\nif test -f " + quoted + "; then cat -- " + quoted + "; fi\n")
	}
	if os.Getenv("SODA_NATIVE_KEEP_PREPARATION") == "1" {
		setup = []byte("mkdir -p .factory-build\nprintf 'prepared dependency\\n' > .factory-build/dependency\nprintf 'prepared HOME\\n' > \"$HOME/factory-env\"\nchmod 600 \"$HOME/factory-env\" .factory-build/dependency\n")
		check = []byte("test \"$(cat .factory-build/dependency)\" = 'prepared dependency'\ntest \"$(cat \"$HOME/factory-env\")\" = 'prepared HOME'\nprintf 'checked-by-native\\n'\n")
	}
	if os.Getenv("SODA_NATIVE_CHECK_RETIRED_SOURCE") == "1" {
		check = append(check, []byte("git rev-parse --verify HEAD\ncat docs/note.md\n")...)
	}
	if os.Getenv("SODA_NATIVE_CHECK_SETUP_RESOURCES") == "1" {
		setup = append(setup, []byte("node -e 'const fs=require(\"fs\"); const memory=Buffer.alloc(192*1024*1024, 7); const artifact=memory.subarray(0,80*1024*1024); fs.writeFileSync(\".factory-build/large\",artifact); fs.writeFileSync(process.env.HOME+\"/large\",artifact); if(memory[191*1024*1024]!==7) process.exit(1)'\n")...)
		check = append(check, []byte("test \"$(wc -c < .factory-build/large)\" -eq 83886080\ntest \"$(wc -c < \"$HOME/large\")\" -eq 83886080\nprintf 'checked-by-native prepared trees exceed historical byte and memory ceilings\\n'\n")...)
	}
	files := map[string][]byte{"setup.sh": setup, "check.sh": check}
	tools := []string{"git", "bash"}
	if os.Getenv("SODA_NATIVE_CHECK_SETUP_RESOURCES") == "1" {
		tools = append(tools, "node")
	}
	if os.Getenv("SODA_NATIVE_CHECK_PUBLIC_SETUP") == "1" || os.Getenv("SODA_NATIVE_CHECK_BUN_RELEASE") != "" {
		files["package.json"] = []byte(`{"name":"factory-setup-fixture","version":"1.0.0","private":true,"dependencies":{"is-number":"7.0.0"}}`)
		files["package-lock.json"] = []byte(`{"name":"factory-setup-fixture","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"factory-setup-fixture","version":"1.0.0","dependencies":{"is-number":"7.0.0"}},"node_modules/is-number":{"version":"7.0.0","resolved":"https://registry.npmjs.org/is-number/-/is-number-7.0.0.tgz","integrity":"sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng==","engines":{"node":">=0.12.0"}}}}`)
	}
	if os.Getenv("SODA_NATIVE_CHECK_PUBLIC_SETUP") == "1" {
		tools = append(tools, "node", "npm", "curl")

		prefix := ""
		if mode != "network-off" {
			files["setup-network.json"] = []byte(`{"access":"public_https"}`)
			prefix = "for target in https://127.0.0.1/ https://localhost/ 'https://[::1]/' http://registry.npmjs.org/is-number; do\n  if curl --fail --silent --show-error --max-time 4 --proxy \"$HTTPS_PROXY\" \"$target\"; then echo 'unexpected forbidden network access'; exit 1; fi\ndone\n"
			prefix += "status=$(curl --silent --max-time 6 --proxy \"$HTTPS_PROXY\" --write-out '%{http_connect}' --output /dev/null https://factory-setup-check.invalid/ || true)\ncase \"$status\" in 502|504) ;; *) echo 'DNS failure mislabeled as policy denial'; exit 1 ;; esac\n"
		}
		files["setup-limits.json"] = []byte(`{"setup_seconds":30,"check_seconds":5}`)
		files["setup.sh"] = []byte(prefix + "cp /run/factory-source/approved/package.json /run/factory-source/approved/package-lock.json .\n: > \"$HOME/user.npmrc\"\n: > \"$HOME/global.npmrc\"\nnpm ci --ignore-scripts --no-audit --no-fund --progress=false --fetch-retries=0 --fetch-timeout=5000 --cache \"$HOME/.npm\" --userconfig=\"$HOME/user.npmrc\" --globalconfig=\"$HOME/global.npmrc\"\n")
		files["check.sh"] = []byte("node -e 'if (!require(\"is-number\")(42)) process.exit(1)'\nprintf 'checked-by-native cold declared npm dependency\\n'\n")
	}
	if os.Getenv("SODA_NATIVE_CHECK_NODE_SETUP") == "1" {
		tools = append(tools, "node")
		files["setup.sh"] = []byte("node build.cjs\n")
		files["check.sh"] = []byte("test \"$(wc -c < dist/app.cjs)\" -gt 262144\ntest \"$(node dist/app.cjs)\" = 'native-node-build'\nprintf 'checked-by-native Node artifact larger than log ceiling\\n'\n")
		if mode == "output-limit" {
			files["setup.sh"] = []byte("node -e 'process.stdout.write(\"x\".repeat(262145))'\n")
		}
	}
	if metadata := os.Getenv("SODA_NATIVE_CHECK_BUN_RELEASE"); metadata != "" {
		var release struct {
			Version string
			SHA256  string
		}
		data, err := os.ReadFile(metadata)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &release); err != nil {
			return err
		}
		version := strings.TrimPrefix(release.Version, "bun-v")
		if version == release.Version || len(version) < 5 || len(version) > 24 || !project.ValidDigest(release.SHA256) {
			return errors.New("Bun release fixture metadata invalid")
		}
		for _, c := range version {
			if !(c >= '0' && c <= '9' || c == '.') {
				return errors.New("Bun version fixture invalid")
			}
		}
		tools = append(tools, "curl", "unzip", "bun")
		files["setup-network.json"] = []byte(`{"access":"public_https"}`)
		files["setup-limits.json"] = []byte(`{"setup_seconds":75,"check_seconds":10}`)
		install := fmt.Sprintf("test ! -e \"$HOME/.factory-setup-once\"\nprintf ready > \"$HOME/.factory-setup-once\"\nmkdir -p \"$HOME/.bun/bin\"\ncurl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 5 --max-time 45 --retry 2 --retry-delay 1 --retry-max-time 45 'https://github.com/oven-sh/bun/releases/download/%s/bun-linux-x64.zip' -o \"$TMPDIR/bun.zip\"\nprintf '%%s  %%s\\n' '%s' \"$TMPDIR/bun.zip\" | sha256sum --check --status\nunzip -q \"$TMPDIR/bun.zip\" -d \"$TMPDIR/bun\"\ncp \"$TMPDIR/bun/bun-linux-x64/bun\" \"$HOME/.bun/bin/bun\"\nrm -rf \"$TMPDIR/bun\" \"$TMPDIR/bun.zip\"\ncp /run/factory-source/approved/package.json /run/factory-source/approved/package-lock.json .\nbun install --ignore-scripts --no-progress --no-cache\n", release.Version, release.SHA256)
		if os.Getenv("SODA_NATIVE_CHECK_FACTORY_BUILD") == "1" {
			// Build the actual shared UI source bundle, including its ordinary
			// root lifecycle script, rather than generating a larger toy file.
			files["package.json"] = []byte(`{"name":"factory-preparation-check","private":true,"type":"module","scripts":{"postinstall":"bun scripts/build-factory-ui.ts --out .artifacts/factory-ui"},"dependencies":{"lit":"3.3.3","@pierre/diffs":"1.6.4","marked":"18.1.3","dompurify":"3.4.17"}}`)
			delete(files, "package-lock.json")
			install = strings.Replace(install, "cp /run/factory-source/approved/package.json /run/factory-source/approved/package-lock.json .\nbun install --ignore-scripts", "cp /run/factory-source/approved/package.json .\nbun install", 1)
			files["setup-limits.json"] = []byte(`{"setup_seconds":120,"check_seconds":30}`)
		}
		files["setup.sh"] = append(files["setup.sh"], []byte(install)...)
		if mode == "network-limit" {
			tools = []string{"git", "bash", "curl"}
			files["setup-limits.json"] = []byte(`{"setup_seconds":120,"check_seconds":1}`)
			files["setup.sh"] = []byte(fmt.Sprintf("for attempt in $(seq 1 32); do\n curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 5 --max-time 30 --retry 2 --retry-delay 1 --retry-max-time 30 'https://github.com/oven-sh/bun/releases/download/%s/bun-linux-x64.zip' -o \"$TMPDIR/bun.zip\"\n printf '%%s  %%s\\n' '%s' \"$TMPDIR/bun.zip\" | sha256sum --check --status\n printf 'verified artifact %%s\\n' \"$attempt\"\ndone\nexit 1\n", release.Version, release.SHA256))
			files["check.sh"] = []byte("exit 1\n")
		}
		if mode == "network-pending" {
			tools = []string{"git", "bash", "curl"}
			files["setup-limits.json"] = []byte(`{"setup_seconds":120,"check_seconds":1}`)
			files["setup.sh"] = []byte(fmt.Sprintf("curl --fail --show-error --progress-bar --location --proto '=https' --proto-redir '=https' --connect-timeout 5 --max-time 110 --limit-rate 128k --retry 1 --retry-delay 1 --retry-max-time 15 'https://github.com/oven-sh/bun/releases/download/%s/bun-linux-x64.zip' -o \"$TMPDIR/bun.zip\"\nprintf '%%s  %%s\\n' '%s' \"$TMPDIR/bun.zip\" | sha256sum --check --status\n", release.Version, release.SHA256))
			files["check.sh"] = []byte("printf 'checked-by-native\\n'\n")
		}
		if os.Getenv("SODA_NATIVE_CHECK_FACTORY_BUILD") == "1" {
			files["check.sh"] = append(files["check.sh"], []byte("test -s .artifacts/factory-ui/factory-app.js\ntest -s .artifacts/factory-ui/factory.css\nbun scripts/build-factory-ui.ts --out .artifacts/factory-ui-offline\ncmp .artifacts/factory-ui/factory-app.js .artifacts/factory-ui-offline/factory-app.js\nprintf 'checked-by-native actual shared UI lifecycle build and offline snapshot rebuild\\n'\n")...)
		} else {
			files["check.sh"] = append(files["check.sh"], []byte(fmt.Sprintf("test \"$(bun --version)\" = '%s'\nbun -e 'if (!require(\"is-number\")(42)) process.exit(1)'\nprintf 'checked-by-native privately provisioned Bun %s and cold dependency\\n'\n", version, version))...)
		}
	}
	tools, err = nativeProjectToolchain(files, tools)
	if err != nil {
		return err
	}
	if os.Getenv("SODA_NATIVE_CHECK_WORKER_BUDGET") == "1" {
		files["setup-limits.json"] = []byte(`{"setup_seconds":10,"check_seconds":20}`)
		files["check.sh"] = append([]byte("sleep 11\n"), files["check.sh"]...)
	}
	if os.Getenv("SODA_NATIVE_CHECK_SETUP_BUDGET") == "1" {
		files["setup.sh"] = []byte("sleep 31\nprintf 'prepared-after-old-deadlines\\n'\n")
		files["setup-limits.json"] = []byte(`{"setup_seconds":40,"check_seconds":5}`)
		if mode == "time-limit" {
			files["setup.sh"] = []byte("sleep 3\nprintf 'must-not-finish\\n'\n")
			files["setup-limits.json"] = []byte(`{"setup_seconds":1,"check_seconds":5}`)
		}
	}
	source := os.Getenv("SODA_NATIVE_CHECK_COMMIT")
	var sourceInputs map[string]string
	if os.Getenv("SODA_NATIVE_CHECK_REQUEST_SETUP") == "1" || os.Getenv("SODA_NATIVE_CHECK_RETIRED_SOURCE") == "1" {
		content, err := os.ReadFile(os.Getenv("SODA_NATIVE_CHECK_SOURCE_README"))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		sourceInputs = map[string]string{"README.md": hex.EncodeToString(sum[:])}
	}
	setupFiles := make([]string, 0, len(files))
	for name := range files {
		setupFiles = append(setupFiles, name)
	}
	requirement := project.RequirementDecision{ID: "d" + factory.NewID()[:24], Project: grant.Project, Approver: int64(actor.ID), SourceCommit: source, SetupDigest: project.SetupDigestOf(files), SourceInputs: sourceInputs, InputsDigest: project.PreparationInputsDigestOf(setupFiles, sourceInputs, tools), Native: native}
	if err = db.AdmitRequirementDecision(ctx, requirement); err != nil {
		return err
	}
	approval := project.ApprovalDecision{ID: "d" + factory.NewID()[:24], Project: grant.Project, Requirement: requirement.ID, Approver: int64(actor.ID), EffectsDigest: strings.Repeat("b", 64), ReadinessDigest: strings.Repeat("c", 64), Verified: true, Native: native}
	if err = db.AdmitApprovalDecision(ctx, approval); err != nil {
		return err
	}
	prep := project.Preparation{ID: "f" + factory.NewID()[:24], Project: grant.Project, Role: project.RoleCoder, SourceCommit: source, SetupDigest: requirement.SetupDigest, Requirements: requirement.Ref(1), Approval: approval.Ref(1), Tools: tools}
	if os.Getenv("SODA_NATIVE_PREPARATION_ROLE") == project.RoleReviewer {
		prep.Role = project.RoleReviewer
	}
	ref := &execution.NativeWorkspaceRef{Profile: config.Profile, InstanceID: instance, WorktreeID: prep.ID}
	record, _, err := db.AdmitStandingPreparation(ctx, execution.PreparationRecord{Preparation: prep, Native: ref})
	if err != nil {
		return err
	}
	bundle, err := os.ReadFile(os.Getenv("SODA_NATIVE_CHECK_BUNDLE"))
	if err != nil {
		return err
	}
	request := execution.StandingPreparationRequest{Input: project.Prepare{Preparation: record.Preparation, Setup: project.ApprovedSetup{Files: files, Bundle: bundle}}, Native: ref}
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	if mode == "withdrawn" {
		current, err := db.EnvironmentGrant(ctx, grant.Repository)
		if err != nil {
			return err
		}
		current.Active = false
		if _, err = db.ApplyFactoryGrant(ctx, "native-preparation-withdrawal", current); err != nil {
			return err
		}
		if err = db.AuthorizeStandingPreparation(ctx, record); err == nil {
			return errors.New("withdrawn workspace permission still authorizes saved standing preparation")
		}
		if _, err = port.PrepareWorkspace(ctx, request); err == nil {
			return errors.New("withdrawn standing preparation reached native launch")
		}
		entries, err := os.ReadDir(filepath.Join(profile, "native-preparations"))
		if err != nil && !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
			return errors.New("withdrawn standing preparation created a native job")
		}
		fmt.Println("PASS: saved human decisions grant no standing preparation after workspace withdrawal; refusal before native staging, no inference")
		return nil
	}
	if os.Getenv("SODA_NATIVE_CHECK_PREPARATION_FENCES") == "1" && mode == "ready" {
		addresses := []execution.PreparationAddress{}
		for index := 0; index < 64; index++ {
			cancelled := request
			cancelled.Input.Preparation.ID = fmt.Sprintf("f%024x", index+1)
			ref := *request.Native
			ref.WorktreeID = cancelled.Input.Preparation.ID
			cancelled.Native = &ref
			admitted, _, err := db.AdmitStandingPreparation(ctx, execution.PreparationRecord{Preparation: cancelled.Input.Preparation, Native: cancelled.Native})
			if err != nil {
				return err
			}
			cancelled.Input.Preparation = admitted.Preparation
			address := cancelled.Address()
			stopped, err := port.RetireWorkspacePreparation(ctx, address)
			if err != nil || !stopped.Stopped || stopped.Retirement != "confirmed" || stopped.SourceCommit != "" {
				return fmt.Errorf("unstarted preparation did not retain a scoped no-start fence: %v", err)
			}
			observed, err := port.InspectWorkspacePreparation(ctx, address)
			if err != nil || !reflect.DeepEqual(observed, stopped) {
				return errors.New("original no-start preparation receipt changed on lookup")
			}
			var ignored execution.PreparationState
			if err := nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "prepare", "request": cancelled}, &ignored); err == nil {
				return errors.New("no-start preparation fence admitted its delayed original constructor")
			}
			foreign := address
			foreign.Workspace = "p" + strings.Repeat("e", 24)
			if _, err := port.RetireWorkspacePreparation(ctx, foreign); err == nil {
				return errors.New("no-start preparation receipt admitted another workspace scope")
			}
			addresses = append(addresses, address)
		}
		raw, err := json.Marshal(addresses)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(profile, "preparation-fences.json"), raw, 0600); err != nil {
			return err
		}
		fmt.Println("PASS: 64 exact no-start native preparation fences preserve lookup, refuse delayed constructors and reject a different workspace; ready setup must now pass without deleting history")
	}
	state, err := port.PrepareWorkspace(ctx, request)
	if err != nil {
		return err
	}
	record.State = state
	if err = db.ObservePreparation(ctx, record); err != nil {
		return err
	}
	if mode == "network-pending" {
		second := prep
		second.ID, second.Role = "f"+factory.NewID()[:24], project.RoleReviewer
		secondRef := &execution.NativeWorkspaceRef{Profile: config.Profile, InstanceID: instance, WorktreeID: second.ID}
		secondRecord, _, err := db.AdmitStandingPreparation(ctx, execution.PreparationRecord{Preparation: second, Native: secondRef})
		if err != nil {
			return err
		}
		secondRequest := execution.StandingPreparationRequest{Input: project.Prepare{Preparation: secondRecord.Preparation, Setup: project.ApprovedSetup{Files: files, Bundle: bundle}}, Native: secondRef}
		secondState, err := port.PrepareWorkspace(ctx, secondRequest)
		if err != nil {
			return err
		}
		secondRecord.State = secondState
		if err = db.ObservePreparation(ctx, secondRecord); err != nil {
			return err
		}
		if state.Ready || secondState.Ready || state.Phase != project.PrepareRunning || secondState.Phase != project.PrepareRunning {
			return errors.New("overlapping network preparations were not admitted as running")
		}
		fmt.Println("PASS: two role preparations admitted; native check must observe actual transfer bytes before Quit")
		return nil
	}
	if mode == "pending" {
		if state.Ready {
			return errors.New("pending setup falsely became ready")
		}
		fmt.Println("PASS: Go admitted and recorded a retained pending native setup")
		return nil
	}
	for !state.Ready {
		if state.Phase != project.PrepareRunning {
			if (mode == "output-limit" || mode == "time-limit" || mode == "network-off") && state.Phase == project.PrepareFailed || mode == "network-limit" && state.Phase == project.PrepareInterrupted {
				if mode == "network-limit" && !strings.Contains(state.Output, "setup HTTPS transfer budget exceeded") {
					return fmt.Errorf("network setup failed without exact native budget evidence: %s", state.Output)
				}
				if mode == "network-off" && (state.SetupExit == nil || *state.SetupExit == 0 || state.CheckExit != nil || !(strings.Contains(state.Output, "ENOTFOUND") || strings.Contains(state.Output, "EAI_AGAIN") || strings.Contains(state.Output, "ENETUNREACH"))) {
					return fmt.Errorf("offline dependency install did not retain network refusal: %s", state.Output)
				}
				if mode == "time-limit" && (state.SetupExit == nil || *state.SetupExit != 124 || state.CheckExit != nil || strings.Contains(state.Output, "must-not-finish")) {
					return errors.New("setup deadline did not retain timeout status and skip check")
				}
				if mode == "output-limit" && !strings.Contains(state.Output, "setup diagnostic output limit exceeded") {
					return fmt.Errorf("preparation failed without diagnostic-limit evidence: %s", state.Output)
				}
				state, err = port.RetireWorkspacePreparation(ctx, request.Address())
				if err != nil {
					return err
				}
				if !state.Stopped || state.Ready || state.Retirement != "confirmed" {
					return errors.New("output-limited preparation retirement unconfirmed")
				}
				record, err = db.Preparation(ctx, prep.ID)
				if err != nil {
					return err
				}
				record.State = state
				if err = db.ObservePreparation(ctx, record); err != nil {
					return err
				}
				fmt.Println("PASS: setup", mode, "refused readiness and actual preparation resources retired")
				return nil
			}
			return fmt.Errorf("native source did not become ready: %s %s", state.Phase, state.Output)
		}
		time.Sleep(30 * time.Millisecond)
		state, err = port.InspectWorkspacePreparation(ctx, request.Address())
		if err != nil {
			return err
		}
		record, err = db.Preparation(ctx, prep.ID)
		if err != nil {
			return err
		}
		record.State = state
		if err = db.ObservePreparation(ctx, record); err != nil {
			return err
		}
	}
	if mode == "output-limit" || mode == "time-limit" || mode == "network-off" || mode == "network-limit" {
		return errors.New("limited setup falsely became ready")
	}
	if state.SetupExit == nil || *state.SetupExit != 0 || state.CheckExit == nil || *state.CheckExit != 0 || state.SourceBundleSHA256 == "" || !strings.Contains(state.Output, "checked-by-native") {
		return errors.New("native preparation lacks actual setup/check/source facts")
	}
	if os.Getenv("SODA_NATIVE_CHECK_PROJECT_TOOLCHAIN") != "" {
		fmt.Println("PASS: official private toolchain installed with verified hashes; cold declared dependency and actual project build passed")
	}
	if os.Getenv("SODA_NATIVE_CHECK_BUN_RELEASE") != "" {
		resolved := false
		for _, tool := range state.Tools {
			if tool.Name == "bun" && tool.Path == "/run/factory-output/home/.bun/bin/bun" {
				resolved = true
			}
		}
		if !resolved {
			return errors.New("Bun setup did not retain its actual private installed tool path")
		}
		fmt.Println("PASS: current Bun acquired by approved setup; exact SHA verified; required tool discovered after install; cold package install and actual check passed in private HOME")
	}
	record, err = db.Preparation(ctx, prep.ID)
	if err != nil {
		return err
	}
	request.Input.Preparation.Revision = record.Preparation.Revision
	if _, err = port.PrepareWorkspace(ctx, request); err != nil {
		return err
	}
	if os.Getenv("SODA_NATIVE_CHECK_REQUEST_SETUP") == "1" {
		if err := nativeRequestSetupFixture(ctx, db, port, host.ID, actor.ID, int64(repo.ID), record, request, completionConnection); err != nil {
			return err
		}
	}
	if os.Getenv("SODA_NATIVE_KEEP_PREPARATION") == "1" {
		if os.Getenv("SODA_NATIVE_CHECK_RETIRED_SOURCE") == "1" {
			retained, err := json.Marshal(struct{ Preparation string }{prep.ID})
			if err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(profile, "admitted-native-fixture.json"), retained, 0600); err != nil {
				return err
			}
		}
		fmt.Println("PASS: retained admitted source preparation with actual build output and private HOME file")
		return nil
	}
	if os.Getenv("SODA_NATIVE_CONTEXT_CHECK") == "1" {
		base := os.Getenv("SODA_NATIVE_CHECK_BASE")
		contextRequest := execution.PreparationContextRequest{Native: ref, Role: prep.Role, Input: project.FactoryPreparationContextRequest{
			Project: prep.Project, ID: prep.ID, SourceCommit: source, ApprovedBase: base, DiffBase: base, Candidate: source,
			Paths: []string{"docs/note.md"}, NotAfter: time.Now().Add(20 * time.Second),
		}}
		material, readErr := port.ReadWorkspaceContext(ctx, contextRequest)
		if readErr != nil {
			return readErr
		}
		if string(material.Material.Setup) != string(files["setup.sh"]) || string(material.Material.Check) != string(files["check.sh"]) || !strings.Contains(string(material.Material.Diff), "+native-context-change") {
			return errors.New("native context differs from approved scripts or actual commit diff")
		}
		wanted := map[string]string{"AGENTS.md": "Root instructions\n", "docs/AGENTS.md": "Nested instructions\n", "docs/note.md": "native-context-change\n"}
		for _, file := range material.Material.Files {
			if string(file.Content) != wanted[file.Path] {
				return errors.New("native context returned unexpected repository bytes")
			}
			delete(wanted, file.Path)
		}
		if len(wanted) != 0 {
			return errors.New("native context missed selected file or ancestor instructions")
		}
		contextRequest.Input.Paths = []string{"docs"}
		if _, err = port.ReadWorkspaceContext(ctx, contextRequest); err == nil {
			return errors.New("native context accepted directory as file")
		}
		contextRequest.Input.Paths = []string{"docs/note.md"}
		contextRequest.Input.NotAfter = time.Now().Add(-time.Second)
		if _, err = port.ReadWorkspaceContext(ctx, contextRequest); err == nil {
			return errors.New("expired native context read succeeded")
		}
		fmt.Println("PASS: actual read-only native Git context, exact approved scripts, selected file, ancestor instructions, commit diff and directory/deadline refusals")
	}
	if os.Getenv("SODA_NATIVE_PREPARE_CANDIDATE") == "1" {
		delta, readErr := os.ReadFile(os.Getenv("SODA_NATIVE_CANDIDATE_BUNDLE"))
		if readErr != nil {
			return readErr
		}
		// Read a corrected run's incremental candidate against the original
		// protected preparation, before allocating another preparation/setup.
		// Run/export provenance is declared here; the native importer and read
		// execute in the actual isolated context owner.
		candidateCommit := os.Getenv("SODA_NATIVE_CANDIDATE_COMMIT")
		candidateContext := execution.PreparationContextRequest{Native: ref, Role: prep.Role,
			SourceRun: &project.FactoryExport{Project: prep.Project, ID: factory.NewID(), Role: project.RoleCoder, Preparation: prep.ID, Candidate: candidateCommit},
			Input:     project.FactoryPreparationContextRequest{Project: prep.Project, ID: prep.ID, SourceCommit: source, ApprovedBase: source, DiffBase: source, Candidate: candidateCommit, Paths: []string{candidatePath}, NotAfter: time.Now().Add(20 * time.Second)}}
		if _, err = port.ReadWorkspaceContext(ctx, candidateContext); err == nil {
			return errors.New("candidate context accepted an unrecorded source run")
		}
		candidateContext.Bundle = delta
		if _, err = port.ReadWorkspaceContext(ctx, candidateContext); err == nil {
			return errors.New("candidate context accepted caller-supplied source bytes")
		}
		beforeContext, err := port.InspectWorkspacePreparation(ctx, request.Address())
		if err != nil {
			return err
		}
		var correctedContext execution.PreparationContext
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "context", "request": candidateContext}, &correctedContext); err != nil {
			return fmt.Errorf("native corrected-candidate context: %w", err)
		}
		if correctedContext.ValidateFor(candidateContext) != nil || !strings.Contains(string(correctedContext.Material.Diff), "+"+candidateText) || !bytes.Equal(correctedContext.Material.Setup, files["setup.sh"]) || !bytes.Equal(correctedContext.Material.Check, files["check.sh"]) {
			return errors.New("corrected-candidate context differs from exact source/diff/approved scripts")
		}
		foundCandidate := false
		for _, file := range correctedContext.Material.Files {
			foundCandidate = foundCandidate || file.Path == candidatePath && strings.Contains(string(file.Content), candidateText)
		}
		if !foundCandidate {
			return errors.New("corrected-candidate context omitted current candidate bytes")
		}
		candidateContext.SourceRun.Preparation = "f" + factory.NewID()[:24]
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "context", "request": candidateContext}, &correctedContext); err == nil {
			return errors.New("candidate context accepted another preparation's export")
		}
		if after, err := port.InspectWorkspacePreparation(ctx, request.Address()); err != nil || !reflect.DeepEqual(after, beforeContext) {
			return errors.New("candidate context changed original source/setup/preparation state")
		}
		fmt.Println("PASS: existing isolated context imports a bound corrected delta and reads exact new candidate/files/diff without another preparation/setup; unrecorded Go source/caller bytes and mismatched native preparation refuse, original source/setup state unchanged. Source-run export provenance declared; no model/provider send.")
		candidate := record.Preparation
		candidate.ID, candidate.Revision, candidate.Role, candidate.SourceCommit = "f"+factory.NewID()[:24], 0, project.RoleReviewer, os.Getenv("SODA_NATIVE_CANDIDATE_COMMIT")
		owner := *ref
		owner.WorktreeID = candidate.ID
		derived := execution.CandidatePreparationRequest{Native: &owner, Input: project.FactoryCandidate{Preparation: candidate, SourcePreparation: prep.ID, Bundle: delta, Deadline: time.Now().Add(15 * time.Second)}}
		if _, err = port.PrepareWorkspaceCandidate(ctx, derived); err == nil {
			return errors.New("Go prepared a candidate without recorded admission")
		}
		// Declare native-side admission to exercise the producer. This does not
		// prove Go retry/publication authority; its normal missing-row refusal ran above.
		payload := map[string]any{"operation": "candidate", "request": derived, "source": request.Address()}
		var observed execution.PreparationState
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", payload, &observed); err != nil {
			return err
		}
		for !observed.Ready {
			if observed.Phase != project.PrepareRunning {
				return fmt.Errorf("native candidate preparation: %s %s", observed.Phase, observed.Output)
			}
			time.Sleep(30 * time.Millisecond)
			if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "inspect", "address": derived.Address()}, &observed); err != nil {
				return err
			}
		}
		if observed.SourceCommit != candidate.SourceCommit || observed.SetupDigest != candidate.SetupDigest || observed.SourceBundleSHA256 == state.SourceBundleSHA256 || !strings.Contains(observed.Output, candidate.SourceCommit) || !strings.Contains(observed.Output, candidateText) {
			return errors.New("candidate setup/check did not observe the imported source")
		}
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", payload, &observed); err != nil {
			return err
		}
		contextRequest := execution.PreparationContextRequest{Native: &owner, Role: candidate.Role, Input: project.FactoryPreparationContextRequest{Project: candidate.Project, ID: candidate.ID, SourceCommit: candidate.SourceCommit, ApprovedBase: source, DiffBase: source, Candidate: candidate.SourceCommit, Paths: []string{candidatePath}, NotAfter: time.Now().Add(10 * time.Second)}}
		var material execution.PreparationContext
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "context", "request": contextRequest}, &material); err != nil {
			return err
		}
		if err = material.ValidateFor(contextRequest); err != nil {
			return err
		}
		if !strings.Contains(string(material.Material.Diff), "+"+candidateText) {
			return errors.New("prepared candidate context lost its real diff")
		}
		found := false
		for _, file := range material.Material.Files {
			if file.Path == candidatePath && strings.Contains(string(file.Content), candidateText) {
				found = true
			}
		}
		if !found {
			return errors.New("prepared candidate context lost its actual file contents")
		}
		if err = nativeFixtureCall(ctx, config, instance, "/api/native/preparation", map[string]any{"operation": "retire", "address": derived.Address()}, &observed); err != nil {
			return err
		}
		if !observed.Stopped || observed.Retirement != "confirmed" {
			return errors.New("candidate retirement was not confirmed")
		}
		fmt.Println("PASS: isolated incremental candidate import, approved setup/check on exact new HEAD, replay, actual context/diff and exact retirement; Go refuses absent candidate admission")
	}
	state, err = port.RetireWorkspacePreparation(ctx, request.Address())
	if err != nil {
		return err
	}
	if !state.Stopped || state.Ready || state.Retirement != "confirmed" {
		return errors.New("native preparation retirement unconfirmed")
	}
	record.State = state
	if err = db.ObservePreparation(ctx, record); err != nil {
		return err
	}
	if _, err = port.PrepareWorkspace(ctx, request); err == nil {
		return errors.New("stopped native preparation restarted")
	}
	fmt.Println("PASS: actual Go/native source preparation, real setup/check exits, source digest, observed state CAS, stable replay and exact retirement")
	return nil
}

func nativeRetiredSourceFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, record execution.PreparationRecord, retired execution.PreparationState) error {
	record.State = retired
	if !record.RetiredRecipeCandidate() {
		return errors.New("retained source lacks successful setup and confirmed retirement")
	}
	delta, err := os.ReadFile(os.Getenv("SODA_NATIVE_CANDIDATE_BUNDLE"))
	if err != nil {
		return err
	}
	selected, expected := "docs/note.md", "native-context-change"
	if value := os.Getenv("SODA_NATIVE_CANDIDATE_PATH"); value != "" {
		selected, expected = value, os.Getenv("SODA_NATIVE_CANDIDATE_TEXT")
	}
	read := execution.PreparationContextRequest{Native: record.Native, Role: project.RoleCoder, Bundle: delta,
		SourceRun: &project.FactoryExport{Project: record.Preparation.Project, ID: factory.NewID(), Role: project.RoleCoder, Preparation: record.Preparation.ID, Candidate: os.Getenv("SODA_NATIVE_CANDIDATE_COMMIT")},
		Input:     project.FactoryPreparationContextRequest{Project: record.Preparation.Project, ID: record.Preparation.ID, SourceCommit: record.Preparation.SourceCommit, ApprovedBase: record.Preparation.SourceCommit, DiffBase: record.Preparation.SourceCommit, Candidate: os.Getenv("SODA_NATIVE_CANDIDATE_COMMIT"), Paths: []string{selected}, NotAfter: time.Now().Add(20 * time.Second)}}
	var material execution.PreparationContext
	if err = nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/preparation", map[string]any{"operation": "context", "request": read}, &material); err != nil {
		return fmt.Errorf("retired original-owner candidate context: %w", err)
	}
	if material.ValidateFor(read) != nil || !strings.Contains(string(material.Material.Diff), "+"+expected) {
		return errors.New("retired recipe context lost exact corrected candidate diff")
	}
	read.SourceRun, read.Bundle = nil, nil
	if err = nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/preparation", map[string]any{"operation": "context", "request": read}, &material); err == nil {
		return errors.New("retired preparation supplied ordinary current-checkout context")
	}
	if after, err := port.InspectWorkspacePreparation(ctx, record.Address()); err != nil || !reflect.DeepEqual(after, retired) {
		return errors.New("retired candidate context changed original source/setup retirement")
	}
	fmt.Println("PASS: prior-instance confirmed-retired successful recipe reads exact corrected candidate/diff in readonly isolation without setup or retirement renewal; source-run export provenance declared")
	bundle, err := os.ReadFile(os.Getenv("SODA_NATIVE_CANDIDATE_FULL_BUNDLE"))
	if err != nil {
		return err
	}
	for _, role := range []string{project.RoleCoder, project.RoleReviewer} {
		candidate := record.Preparation
		candidate.ID, candidate.Revision, candidate.Role, candidate.SourceCommit = "f"+factory.NewID()[:24], 0, role, os.Getenv("SODA_NATIVE_CANDIDATE_COMMIT")
		address, err := port.CandidatePreparationAddress(candidate.Project, candidate.ID)
		if err != nil || address.Native.InstanceID == record.Native.InstanceID {
			return errors.New("fresh candidate did not use the current native owner")
		}
		source := &project.FactoryMergedSource{Export: project.FactoryExport{Project: candidate.Project, ID: factory.NewID(), Role: project.RoleCoder, Preparation: record.Preparation.ID, Candidate: candidate.SourceCommit}, MergeID: factory.NewID(), Commit: candidate.SourceCommit, Repository: 1, Target: "refs/heads/main"}
		derived := execution.CandidatePreparationRequest{Native: address.Native, Input: project.FactoryCandidate{Preparation: candidate, SourcePreparation: record.Preparation.ID, SourceMerge: source, Deadline: time.Now().Add(20 * time.Second)}}
		if role == project.RoleReviewer {
			derived.Input.SourceMerge, derived.Input.Bundle = nil, bundle
		}
		if _, err = port.PrepareWorkspaceCandidate(ctx, derived); err == nil {
			return errors.New("retired recipe supplied candidate authority without its journal")
		}
		// Only native import admission is declared. Production Go admission above
		// still refuses; exact merge/grant joins are checked by the PG source check.
		derived.Input.Bundle = bundle
		payload := map[string]any{"operation": "candidate", "request": derived, "source": record.Address()}
		var state execution.PreparationState
		bad := derived
		bad.Input.Bundle = []byte(strings.Replace(string(bundle), "# v2 git bundle\n", "# v2 git bundle\n-"+record.Preparation.SourceCommit+" protected source\n", 1))
		if err = nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/preparation", map[string]any{"operation": "candidate", "request": bad, "source": record.Address()}, &state); err == nil {
			return errors.New("retired source accepted a prerequisite delta")
		}
		if err = nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/preparation", payload, &state); err != nil {
			return err
		}
		for !state.Ready {
			if state.Phase != project.PrepareRunning {
				return fmt.Errorf("fresh retired-source setup: %s %s", state.Phase, state.Output)
			}
			time.Sleep(30 * time.Millisecond)
			if err = nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/preparation", map[string]any{"operation": "inspect", "address": derived.Address()}, &state); err != nil {
				return err
			}
		}
		if state.SourceCommit != candidate.SourceCommit || state.SetupDigest != candidate.SetupDigest || state.SetupExit == nil || *state.SetupExit != 0 || state.CheckExit == nil || *state.CheckExit != 0 || !strings.Contains(state.Output, candidate.SourceCommit) {
			return errors.New("fresh preparation did not independently execute approved setup/check at the exact new source")
		}
		if err = nativeFixtureCall(ctx, port.Config, port.Instance, "/api/native/preparation", map[string]any{"operation": "retire", "address": derived.Address()}, &state); err != nil || !state.Stopped || state.Retirement != "confirmed" {
			return errors.Join(errors.New("fresh preparation retirement unconfirmed"), err)
		}
		after, err := port.InspectWorkspacePreparation(ctx, record.Address())
		if err != nil || !reflect.DeepEqual(after, retired) {
			return errors.New("fresh preparation changed its original retired source")
		}
	}
	fmt.Println("PASS: actual native Quit/restart consumes only retired approved recipe and complete source bundle; independent current coder and reviewer addresses/deadlines execute setup/check, prerequisite delta and absent Go admission refuse, original source stays retired; merge/publication/provider facts declared, no model/provider request")
	return nil
}

func nativeRequestSetupFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, host collaboration.HostID, actor collaboration.ActorID, repository int64, record execution.PreparationRecord, request execution.StandingPreparationRequest, completionConnection collaboration.ConnectionID) error {
	requests := factoryservice.Requests{Store: db, Setup: port.RequestSetup,
		WriteAccess: func(ctx context.Context, selected collaboration.ActorID, repo int64) error {
			if selected != actor || repo != repository {
				return errors.New("declared request fixture authority changed")
			}
			return ctx.Err()
		}}
	revision, err := requests.SaveRevision(ctx, actor, factory.IntentRevision{
		ID: "d" + factory.NewID()[:24], Intent: "d" + factory.NewID()[:24], Repository: repository,
		Objective: "Read this saved native request while execution setup is incomplete.", State: "draft", MaxIssues: 1,
		Ideas: []factory.ProductIdea{}, Tasks: []factory.IntentTask{},
	})
	if err != nil {
		return err
	}
	check := func(setup factoryservice.RequestSetupReader, state string) error {
		service := requests
		service.Setup = setup
		product := factoryhttpapi.Product{Store: db, Host: host, Actor: actor,
			Current:  func(http.ResponseWriter, *http.Request) bool { return true },
			Requests: factoryhttpapi.Requests{Service: service}}
		response := httptest.NewRecorder()
		product.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/factory/intents/"+revision.Intent+"/overview", nil).WithContext(ctx))
		if response.Code != http.StatusOK {
			return fmt.Errorf("native request overview HTTP %d: %s", response.Code, response.Body.String())
		}
		var view factoryservice.RequestOverview
		if json.Unmarshal(response.Body.Bytes(), &view) != nil || view.Intent != revision.Intent || view.Revision != revision.ID || view.Setup.State != state {
			return fmt.Errorf("native request overview setup: %s want %s", view.Setup.State, state)
		}
		return nil
	}
	if err := check(port.RequestSetup, factoryservice.RequestSetupPreparing); err != nil {
		return err
	}
	if err := check(nil, factoryservice.RequestSetupUnavailable); err != nil {
		return err
	}
	other := record
	other.State = execution.PreparationState{}
	other.Preparation.ID = "f" + factory.NewID()[:24]
	other.Preparation.Revision = 0
	other.Preparation.Role = project.RoleReviewer
	if record.Preparation.Role == project.RoleReviewer {
		other.Preparation.Role = project.RoleCoder
	}
	ref := *record.Native
	ref.WorktreeID = other.Preparation.ID
	other.Native = &ref
	other, _, err = db.AdmitStandingPreparation(ctx, other)
	if err != nil {
		return err
	}
	request.Input.Preparation, request.Native = other.Preparation, other.Native
	state, err := port.PrepareWorkspace(ctx, request)
	for err == nil {
		other, err = db.Preparation(ctx, other.Preparation.ID)
		if err != nil {
			break
		}
		other.State = state
		if err = db.ObservePreparation(ctx, other); err != nil || state.Ready {
			break
		}
		if state.Phase != project.PrepareRunning {
			return errors.New("other pipeline role setup failed")
		}
		time.Sleep(30 * time.Millisecond)
		state, err = port.InspectWorkspacePreparation(ctx, request.Address())
	}
	if err != nil {
		return err
	}
	if err := check(port.RequestSetup, factoryservice.RequestSetupReady); err != nil {
		return err
	}
	coordinator := control.NewCoordinator(db, port, port)
	planning, preparation, wait, err := coordinator.AssembleIntentPlanningInput(ctx, revision, factory.IntentPlanningPropose)
	if err != nil || wait != nil || preparation.Project != record.Preparation.Project || len(planning.Inspected) != len(record.Preparation.Requirements.SourceInputs) {
		return fmt.Errorf("native workspace planning input: wait=%+v err=%v", wait, err)
	}
	for _, inspected := range planning.Inspected {
		sum := sha256.Sum256([]byte(inspected.Evidence))
		if inspected.Commit != record.Preparation.SourceCommit || hex.EncodeToString(sum[:]) != record.Preparation.Requirements.SourceInputs[inspected.Path] {
			return errors.New("native planning input differs from actual approved source")
		}
	}
	// Completion material needs an explicit repository policy. This fixture
	// declares its account authority; it never enables automated dispatch.
	observation := collaboration.ConnectionObservation{
		Profile:  collaboration.Profile{ID: port.Config.Profile, Name: "Native source fixture"},
		Provider: collaboration.GitHub, APIBase: "https://api.github.com", Connection: collaboration.ConnectionID(factory.NewID()),
		Account: "native-source-owner", Login: "native-source-owner",
	}
	if completionConnection != "" {
		observedActor, e := db.CollaborationActor(ctx, actor)
		if e != nil {
			return e
		}
		observation.Connection = completionConnection
		observation.Account = observedActor.ExternalID
		observation.Login = observedActor.Login
	}
	binding, err := db.BindCollaborationConnection(ctx, observation)
	if err != nil || binding.Actor.ID != actor {
		return errors.New("completion fixture account mapping changed")
	}
	refFor := func(kind string) factory.ActorBindingRef {
		return factory.ActorBindingRef{Kind: kind, ActorID: int64(actor), NativeProfile: port.Config.Profile, NativeConnection: binding.Connection.ID}
	}
	if err := db.SaveRepositoryPolicy(ctx, factory.RepositoryPolicy{
		Repository: repository, GrantedBy: int64(actor), TargetBranch: "refs/heads/main", MergeMethod: factory.MergeFastForward,
		ReassessmentMinutes: factory.EffectiveReassessmentMinutes(0), AttemptLimits: factory.EffectiveAttemptLimits(nil),
		Checks: []string{"fixture/check"}, Roles: map[string]factory.RoleSelection{
			project.RoleCoder:    {Account: "fixture-unavailable", Harness: project.FactoryHarnessMuse, Model: "fixture"},
			project.RoleReviewer: {Account: "fixture-unavailable", Harness: project.FactoryHarnessMuse, Model: "fixture"}},
		Publish: refFor(factory.OpRefPublish), Create: refFor(factory.OpPRCreate), Review: refFor(factory.OpReviewSubmit), Merge: refFor(factory.OpMerge),
	}); err != nil {
		return err
	}
	authority, err := db.CompletionSourceContext(ctx, repository)
	if err != nil || authority.ProjectID != record.Preparation.Project {
		return fmt.Errorf("native completion workspace authority: %w", err)
	}
	if _, err := db.CaptureCompletionLocalAtSource(ctx, repository, authority, record.Preparation.SourceCommit); err != nil {
		return fmt.Errorf("native completion source material: %w", err)
	}
	if completionConnection != "" {
		tipFile := os.Getenv("SODA_NATIVE_CHECK_COMPLETION_TIP_FILE")
		if tipFile == "" {
			return errors.New("completion fixture requires its declared provider tip file")
		}
		if err := os.WriteFile(tipFile, []byte(record.Preparation.SourceCommit), 0600); err != nil {
			return err
		}
		input := factoryruntime.NativeInputSource{NativeExecution: port}
		captured, err := input.CaptureCompletionLocal(ctx, repository)
		if err != nil || captured.SourceCommit != record.Preparation.SourceCommit {
			return fmt.Errorf("native current completion capture: %w", err)
		}
		issues, err := nativeCompleteObservation(ctx, input, repository)
		if err != nil || len(issues) != 1 {
			return fmt.Errorf("native complete issue inventory: count=%d err=%w", len(issues), err)
		}
		if _, err := factory.ParseCompletionIssueEvidence(issues[0].Data); err != nil {
			return err
		}
		if err := os.WriteFile(tipFile, []byte(strings.Repeat("d", 40)), 0600); err != nil {
			return err
		}
		if _, err := input.CaptureCompletionLocal(ctx, repository); !errors.Is(err, store.ErrCompletionSourceHold) {
			return fmt.Errorf("unapproved completion source did not hold: %v", err)
		}
		if err := os.WriteFile(tipFile, []byte(record.Preparation.SourceCommit), 0600); err != nil {
			return err
		}
		if err := nativeCompletionInventoryFixture(ctx, db, input, repository, record.Preparation.SourceCommit, tipFile); err != nil {
			return err
		}
		fmt.Println("PASS: native current-tip completion capture uses actual approved workspace/preparation, complete issue/dependency read and refuses changed unapproved source; provider facts declared")
	}
	fmt.Println("PASS: native draft planning reads actual approved source and completion capture resolves native workspace without appliance Project")
	stale := port
	stale.Instance = factory.NewID()
	if err := check(stale.RequestSetup, factoryservice.RequestSetupPreparing); err != nil {
		return err
	}
	foreign := port
	foreign.Config.Profile = collaboration.ProfileID(factory.NewID())
	if err := check(foreign.RequestSetup, factoryservice.RequestSetupUnavailable); err != nil {
		return err
	}
	state, err = port.RetireWorkspacePreparation(ctx, request.Address())
	if err != nil {
		return err
	}
	other, err = db.Preparation(ctx, other.Preparation.ID)
	if err != nil {
		return err
	}
	other.State = state
	if err := db.ObservePreparation(ctx, other); err != nil {
		return err
	}
	if err := check(port.RequestSetup, factoryservice.RequestSetupPreparing); err != nil {
		return err
	}
	after, err := requests.Revision(ctx, actor, revision.Intent)
	if err != nil || !reflect.DeepEqual(after, revision) {
		return errors.New("setup overview changed the saved request")
	}
	fmt.Println("PASS: saved native request HTTP overview remains readable with absent setup port; actual coder/reviewer setup, stale-instance/profile refusal and retirement retain truthful readiness without changing the draft")
	return nil
}

// Only the prior merge/completion is declared. Inventory capture, source reads,
// mismatch holds and the existing SQL seal transition run through real owners.
func nativeCompletionInventoryFixture(ctx context.Context, db *store.Store, input factoryruntime.NativeInputSource, repository int64, source, tipFile string) error {
	event := factory.CompletionEvent{ID: factory.NewID(), PublicationID: factory.NewID(), Repository: repository, Issue: 17, Generation: 1, MergedCommit: source, ClosingOccurrence: "declared-prior-completion", AcceptanceID: "d" + strings.Repeat("7", 24)}
	if err := event.Validate(); err != nil {
		return err
	}
	cycle := factory.ReassessmentCycle{EventID: event.ID, CycleID: factory.NewID(), State: factory.ReassessmentCollecting, Repository: repository, Generation: 1}
	record := store.ReassessmentRecord{Cycle: cycle, Completion: event, Issue: 17}
	if err := record.Validate(); err != nil {
		return err
	}
	conn, err := sql.Open("pgx", input.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	eventJSON, _ := json.Marshal(event)
	recordJSON, _ := json.Marshal(record)
	if _, err = conn.ExecContext(ctx, `INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES($1,1,$2,$3,$4,$5)`, repository, event.ID, event.PublicationID, event.ClosingOccurrence, string(eventJSON)); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO factory_reassessments(cycle,repository,generation,event,state,data) VALUES($1,$2,1,$3,$4,$5)`, cycle.CycleID, repository, event.ID, cycle.State, string(recordJSON)); err != nil {
		return err
	}
	coordinator := control.NewCoordinator(db, nil, nil)
	calls := 0
	changeFile := filepath.Join(filepath.Dir(tipFile), "completion-body-change")
	deps := control.CompletionDriveDeps{CaptureLocal: input.CaptureCompletionLocal, ReadPage: func(ctx context.Context, repo int64, cursor string) (control.IssueObservationPage, error) {
		issues, err := input.ReadCompletionIssuePage(ctx, repo, cursor)
		calls++
		if calls == 1 && err == nil {
			err = os.WriteFile(changeFile, []byte("declared concurrent edit"), 0600)
		}
		return issues, err
	}}
	coordinator.DriveCompletions(ctx, deps)
	held, err := db.ReassessmentRecord(ctx, cycle.CycleID)
	if err != nil || held.Cycle.State != factory.ReassessmentCollecting || held.WaitReason != control.HoldInventoryRevision || held.InventoryDigest != "" {
		return fmt.Errorf("changed complete inventory did not hold: state=%s reason=%s err=%v", held.Cycle.State, held.WaitReason, err)
	}
	if err = os.Remove(changeFile); err != nil {
		return err
	}
	deps.ReadPage = input.ReadCompletionIssuePage
	report := coordinator.DriveCompletions(ctx, deps)
	sealed, err := db.ReassessmentRecord(ctx, cycle.CycleID)
	if err != nil || len(report.Sealed) != 1 || sealed.Cycle.State != factory.ReassessmentDeciding || sealed.InventoryDigest == "" || sealed.EvidenceIssues != 1 || len(sealed.Evidence) != 1 || sealed.Local.SourceCommit != source {
		return fmt.Errorf("matching complete inventories did not seal: report=%+v state=%s reason=%s err=%v", report, sealed.Cycle.State, sealed.WaitReason, err)
	}
	fmt.Println("PASS: actual native complete issue reads through the canonical completion drive hold on a changed second observation, then persist the matching whole inventory and local source in PostgreSQL; prior completion/provider declared, no funded decision")
	return nil
}

func nativeFixtureCall(ctx context.Context, config factoryruntime.NativeConfig, instance, route string, payload, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(config.Directory, "host.sock"))
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, "POST", "http://soda-native"+route, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
	request.Header.Set("X-Soda-Native-Instance", instance)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("native fixture callback status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(result)
}

type declaredPostRouting struct{}

func (declaredPostRouting) JudgePost(context.Context, factoryservice.PostJudgeInput) (factoryservice.PostJudgment, error) {
	return factoryservice.PostJudgment{Route: factory.PostRouteNewIssue}, nil
}

// Only the routing answer is declared. Native HTTP captured the actual source,
// account and issue context; the normal background owner performs creation.
func declaredRequestRoute(profile, post string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	pipe, err := db.PostIntake(ctx, post)
	if err != nil {
		return err
	}
	if grant, err := db.IssueIntakeGrant(ctx, post, pipe.Revision); err != nil || !json.Valid(grant) {
		return errors.New("native request did not capture its admitted source/account")
	}
	captured := factoryservice.PostContext{Posts: factoryservice.Posts{Store: db}}
	response, err := factoryservice.DrivePostIntake(ctx, db, &factoryservice.PostCollaborators{Judge: declaredPostRouting{}, Prepare: captured.Prepare}, post)
	if err != nil || response.Judgment != factory.PostJudgmentApplied || response.Route != factory.PostRouteNewIssue {
		return fmt.Errorf("declared routing prerequisite: %+v: %v", response, err)
	}
	fmt.Println("PASS: native source/account/context captured; routing answer declared, no model request")
	return nil
}

// The existing native integration driver can itself act as the captured local
// CLI fixture. It consumes no provider token and calls only its loopback relay.
func workerProfileFixture() error {
	selected := map[string]string{}
	for i := 1; i+1 < len(os.Args); i++ {
		switch os.Args[i] {
		case "--model", "--reasoning-effort", "--contributor-mode", "--workspace", "--prompt-file", "--preset":
			selected[os.Args[i]] = os.Args[i+1]
		}
	}
	if selected["--model"] != "muse-spark-1.3-contributor" || selected["--reasoning-effort"] != factory.DefaultEffort || selected["--contributor-mode"] != "off" {
		return errors.New("worker did not receive the captured model/effort/Contributor profile")
	}
	cli := selected["--preset"] == "empty"
	if cli {
		if os.Getenv("META_API_KEY") != "" {
			return errors.New("CLI source inherited a provider credential")
		}
	} else {
		if _, err := bufio.NewReader(io.LimitReader(os.Stdin, 256)).ReadString('\n'); err != nil {
			return errors.New("worker placeholder input unavailable")
		}
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"owned-native-fixture","version":"1"}}}`
	request, err := http.NewRequest(http.MethodPost, workerMCPURL(), strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", os.Getenv("SODA_FACTORY_RUN_AUTH"))
	request.Header.Set("Content-Type", "application/json")
	// Let the native relay return within its own 20-second bound before this
	// finite client times out; each operation still sends exactly once.
	client := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("native worker MCP relay unavailable")
	}
	defer response.Body.Close()
	prompt, err := os.ReadFile(selected["--prompt-file"])
	if err != nil {
		return err
	}
	admitted := strings.Contains(string(prompt), "positive-native-mcp-fixture")
	var serverName string
	if !admitted && response.StatusCode != http.StatusForbidden {
		return errors.New("native worker without a current Go capability was not refused")
	}
	if admitted {
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("admitted native MCP initialize refused: %d", response.StatusCode)
		}
		var initialized struct {
			Error  json.RawMessage `json:"error"`
			Result struct {
				ServerInfo struct {
					Name string `json:"name"`
				} `json:"serverInfo"`
			} `json:"result"`
		}
		if json.NewDecoder(response.Body).Decode(&initialized) != nil || len(initialized.Error) != 0 {
			return errors.New("admitted MCP initialization failed")
		}
		serverName = initialized.Result.ServerInfo.Name
		response.Body.Close()
		if strings.Contains(string(prompt), "read-source-fixture") {
			head, err := exec.Command("/usr/bin/git", "-C", selected["--workspace"], "rev-parse", "HEAD").Output()
			if err != nil {
				return err
			}
			for directory, wanted := range map[string]map[string]string{
				"":     {".gitignore": "file", "AGENTS.md": "file", "README.md": "file", "docs": "directory"},
				"docs": {"docs/AGENTS.md": "file", "docs/note.md": "file", "docs/fixture-link": "symlink", "docs/large.go": "file"},
			} {
				result, err := workerMCPCall(client, "factory.list_source", map[string]any{"path": directory})
				if err != nil {
					return err
				}
				var listing struct {
					Commit    string                        `json:"source_commit"`
					Directory string                        `json:"directory"`
					Entries   []project.FactoryContextEntry `json:"entries"`
				}
				if json.Unmarshal(result, &listing) != nil || listing.Commit != strings.TrimSpace(string(head)) || listing.Directory != directory || len(listing.Entries) != len(wanted) {
					return errors.New("worker source listing differs from tracked fixture directory")
				}
				for _, entry := range listing.Entries {
					if wanted[entry.Path] != entry.Kind {
						return errors.New("worker source entry name or kind differs")
					}
					delete(wanted, entry.Path)
				}
				if len(wanted) != 0 {
					return errors.New("worker source listing omitted entries")
				}
				fmt.Println("actual-admitted-worker-source-directory", listing.Commit, directory)
			}
			if _, err := workerMCPCall(client, "factory.list_source", map[string]any{"path": "missing-fixture-directory"}); err == nil {
				return errors.New("missing source directory returned a successful listing")
			}
			result, err := workerMCPCall(client, "factory.read_source", map[string]any{"path": "docs/note.md"})
			if err != nil {
				return err
			}
			var source struct {
				Commit string `json:"source_commit"`
				Files  []struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				} `json:"files"`
			}
			if json.Unmarshal(result, &source) != nil {
				return errors.New("worker source reply invalid")
			}
			if source.Commit != strings.TrimSpace(string(head)) {
				return errors.New("worker source commit differs from prepared repository")
			}
			wanted := map[string]bool{"docs/note.md": false, "AGENTS.md": false, "docs/AGENTS.md": false}
			for _, file := range source.Files {
				if _, ok := wanted[file.Path]; !ok || wanted[file.Path] {
					return errors.New("worker source returned unexpected or duplicate files")
				}
				content, err := os.ReadFile(filepath.Join(selected["--workspace"], file.Path))
				if err != nil || file.Content != string(content) {
					return errors.New("worker source bytes differ from prepared file")
				}
				wanted[file.Path] = true
			}
			for _, found := range wanted {
				if !found {
					return errors.New("worker source omitted its file or ancestor instructions")
				}
			}
			if _, err := workerMCPCall(client, "factory.read_source", map[string]any{"path": "missing-fixture.txt"}); err == nil {
				return errors.New("missing source file returned a successful read")
			}
			fmt.Println("actual-admitted-worker-source-read", source.Commit)
			original, err := os.ReadFile(filepath.Join(selected["--workspace"], "docs/large.go"))
			if err != nil || len(original) <= project.MaxFactoryContextFileBytes {
				return errors.New("source paging fixture needs a source file above the whole-file bound")
			}
			var collected []byte
			pages := 0
			for offset := int64(0); ; {
				pages++
				if pages > 16 {
					return errors.New("source paging failed to terminate")
				}
				result, err := workerMCPCall(client, "factory.read_source", map[string]any{"path": "docs/large.go", "offset": offset})
				if err != nil {
					return err
				}
				var view struct {
					Commit string                      `json:"source_commit"`
					Page   *project.FactoryContextPage `json:"page"`
					Files  []struct {
						Path    string `json:"path"`
						Content string `json:"content"`
					} `json:"files"`
				}
				if json.Unmarshal(result, &view) != nil || view.Commit != source.Commit || view.Page == nil || view.Page.Path != "docs/large.go" || view.Page.Offset != offset || view.Page.TotalBytes != int64(len(original)) {
					return errors.New("source page changed the captured file binding")
				}
				found := false
				for _, file := range view.Files {
					if file.Path != "docs/large.go" {
						continue
					}
					if found || len(file.Content) > project.DefaultFactorySourcePageBytes {
						return errors.New("source page exceeds its requested bound")
					}
					collected = append(collected, file.Content...)
					found = true
				}
				if !found || !bytes.HasPrefix(original, collected) {
					return errors.New("source paging lost, duplicated or changed UTF-8 bytes")
				}
				if view.Page.NextOffset == nil {
					break
				}
				if *view.Page.NextOffset != int64(len(collected)) || *view.Page.NextOffset <= offset {
					return errors.New("source paging cursor did not advance")
				}
				offset = *view.Page.NextOffset
			}
			if !bytes.Equal(original, collected) || pages < 2 {
				return errors.New("source paging did not read the complete large file")
			}
			insideCharacter := bytes.Index(original, []byte("Ω")) + 1
			if insideCharacter < 1 {
				return errors.New("source paging fixture omitted multibyte text")
			}
			if _, err := workerMCPCall(client, "factory.read_source", map[string]any{"path": "docs/large.go", "offset": insideCharacter}); err == nil {
				return errors.New("source paging admitted an offset inside a UTF-8 character")
			}
			fmt.Println("actual-admitted-worker-source-pages", source.Commit, len(collected), pages)
		}
		result, err := workerMCPCall(client, "factory.publish_artifact", factory.ArtifactUpload{
			UploadID: strings.Repeat("1", 32), Name: "native-worker.txt", MediaType: "text/plain", ContentBase64: base64.StdEncoding.EncodeToString([]byte("actual admitted native worker artifact\n")), Final: true})
		if err != nil {
			return err
		}
		var artifact struct {
			Complete   bool   `json:"complete"`
			ArtifactID string `json:"artifact_id"`
		}
		if json.Unmarshal(result, &artifact) != nil || !artifact.Complete || !factory.ValidID(artifact.ArtifactID) {
			return errors.New("worker artifact receipt incomplete")
		}
		fmt.Println("actual-admitted-worker-artifact", artifact.ArtifactID)
	}
	if cli {
		if !admitted {
			return errors.New("CLI fixture has no captured Go admission")
		}
		if strings.Contains(string(prompt), "withdraw-source-fixture") {
			if err := os.WriteFile("native-fixture-ready", []byte("actual admitted artifact published\n"), 0600); err != nil {
				return err
			}
			time.Sleep(120 * time.Second) // The real native owner must cancel this process.
			return errors.New("withdrawal fixture was not cancelled")
		}
		if strings.Contains(string(prompt), "native-tasks-investigation-fixture") {
			payload, err := nativeTaskPlanPayload(strings.Contains(string(prompt), "native-correction-fixture"))
			if err != nil {
				return err
			}
			if _, err := workerMCPCall(client, "factory.read_source", map[string]any{"path": "README.md"}); err != nil {
				return err
			}
			if _, err := workerMCPCall(client, "factory.finish_issue_plan", payload); err != nil {
				return err
			}
		} else if strings.Contains(string(prompt), "native-correction-fixture") && serverName == "soda-factory-"+string(factory.MCPProfileInspector) {
			raw, err := workerMCPCall(client, "factory.context", map[string]any{})
			if err != nil {
				return err
			}
			var current struct {
				Run string `json:"run"`
			}
			if json.Unmarshal(raw, &current) != nil || !factory.ValidID(current.Run) {
				return errors.New("reviewer current run missing")
			}
			if _, err = workerMCPCall(client, "factory.workspace", map[string]any{"task_id": "correct", "operation": "read", "path": "native-task-dir/kept.txt"}); err == nil {
				return errors.New("independent reviewer acquired coder workspace access")
			}
			finding := factory.Finding{Key: strings.Repeat("f", 32), OriginRun: current.Run, Severity: factory.FindingSeverityMajor, Actionable: true, FixKind: factory.FindingFixCode,
				Defect: "kept.txt contains the disposable placeholder", Evidence: "native-task-dir/kept.txt contains kept", RequiredOutcome: "Replace kept with corrected in native-task-dir/kept.txt"}
			if _, err = workerMCPCall(client, "factory.finish_review", map[string]any{"conclusion": "findings", "findings": []factory.Finding{finding}, "evidence_refs": []string{"native-task-dir/kept.txt"}}); err != nil {
				return err
			}
		} else if strings.Contains(string(prompt), "native-correction-fixture") && serverName == "soda-factory-"+string(factory.MCPProfileFixer) {
			if err := workerNativeCorrection(client); err != nil {
				return err
			}
		} else if serverName == "soda-factory-"+string(factory.MCPProfileBuilder) {
			if err := workerSequentialTasks(client); err != nil {
				return err
			}
			if _, err := workerMCPCall(client, "factory.finish_implementation", map[string]any{}); err != nil {
				return err
			}
		} else if _, err := workerMCPCall(client, "factory.finish_incomplete", map[string]any{"outcome": "blocked", "cause": "local_fixture_has_no_provider_result"}); err != nil {
			return err
		}
		fmt.Println("CLI fixture completed admitted MCP artifact and phase result; no model request")
		fmt.Println(`{"schema_version":1,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":"Declared local CLI fixture; no model request"}}`)
		time.Sleep(time.Second)
		return nil
	}
	dependency, readErr := os.ReadFile(filepath.Join(selected["--workspace"], ".factory-build/dependency"))
	if readErr != nil || string(dependency) != "prepared dependency\n" {
		return errors.New("worker lost prepared workspace dependency")
	}
	home, readErr := os.ReadFile(filepath.Join(os.Getenv("HOME"), "factory-env"))
	if readErr != nil || string(home) != "prepared HOME\n" {
		return errors.New("worker lost prepared HOME")
	}
	if _, err := os.Stat(".git"); err == nil {
		if err = os.WriteFile("native-candidate.txt", []byte("Actual isolated coder candidate\n"), 0600); err != nil {
			return err
		}
		for _, args := range [][]string{
			{"add", "--", "native-candidate.txt"},
			{"-c", "user.name=Native fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-qm", "Capture actual native fixture candidate"},
		} {
			if output, gitErr := exec.Command("/usr/bin/git", args...).CombinedOutput(); gitErr != nil {
				return fmt.Errorf("native fixture Git commit failed: %s", output)
			}
		}
	}
	if admitted {
		if _, err := workerMCPCall(client, "factory.finish_incomplete", map[string]any{"outcome": "blocked", "cause": "local_fixture_has_no_provider_result"}); err != nil {
			return err
		}
		fmt.Println("captured-profile-low-off; native-worker-MCP-artifact-and-explicit-incomplete-result")
	} else {
		fmt.Println("captured-profile-low-off; native-worker-MCP-missing-capability-refused")
	}
	time.Sleep(4 * time.Second)
	return nil
}

func workerNativeCorrection(client *http.Client) error {
	const path = "native-task-dir/kept.txt"
	if _, err := workerMCPCallID(client, "factory.workspace", map[string]any{"task_id": "correct", "operation": "edit", "path": path, "expected": "kept\n", "replacement": "corrected\n"}, "native-correction-edit"); err != nil {
		return err
	}
	raw, err := workerMCPCall(client, "factory.workspace", map[string]any{"task_id": "correct", "operation": "read", "path": path})
	var file struct {
		Content string `json:"content"`
	}
	if err != nil || json.Unmarshal(raw, &file) != nil || file.Content != "corrected\n" {
		return errors.New("actual correction workspace readback differs")
	}
	if _, err := workerMCPCall(client, "factory.release_output", map[string]any{"task_id": "correct"}); err != nil {
		return err
	}
	for _, final := range []bool{false, true} {
		until := time.Now().Add(35 * time.Second)
		for {
			raw, err := workerMCPCall(client, "factory.context", map[string]any{})
			if err != nil {
				return err
			}
			var current struct {
				Execution struct {
					Tasks []struct {
						ID    string `json:"task_id"`
						State string `json:"state"`
					} `json:"tasks"`
					Integration *struct {
						Status    string `json:"status"`
						Candidate string `json:"candidate"`
					} `json:"integration"`
				} `json:"execution"`
			}
			if json.Unmarshal(raw, &current) != nil {
				return errors.New("correction context invalid")
			}
			if final && current.Execution.Integration != nil && current.Execution.Integration.Status == "committed" {
				candidate := current.Execution.Integration.Candidate
				_, err = workerMCPCallID(client, "factory.finish_correction", map[string]any{"candidate": candidate,
					"dispositions": []factory.CorrectionDisposition{{Key: strings.Repeat("f", 32), Disposition: factory.DispositionFixed}},
					"fix_evidence": []factory.CorrectionFixEvidence{{Key: strings.Repeat("f", 32), ChangedPaths: []string{path}, Commits: []string{candidate}}}, "evidence_refs": []string{path}}, "native-correction-result")
				return err
			}
			ready := false
			for _, task := range current.Execution.Tasks {
				if task.State == store.PlanTaskFailed {
					return errors.New("actual correction check failed")
				}
				ready = ready || task.ID == "correct" && task.State == store.PlanTaskOutputReady
			}
			if !final && ready {
				if _, err = workerMCPCallID(client, "factory.submit_task_output", map[string]any{"task_id": "correct", "evidence_refs": []string{path}}, "native-correction-output"); err != nil {
					return err
				}
				break
			}
			if time.Now().After(until) {
				return errors.New("actual correction checkpoint wait expired")
			}
			time.Sleep(time.Second)
		}
	}
	return errors.New("correction result missing")
}

func workerSequentialTasks(client *http.Client) error {
	if _, err := workerMCPCall(client, "factory.finish_implementation", map[string]any{"candidate": strings.Repeat("e", 40)}); err == nil || !strings.Contains(err.Error(), "final integration checkpoint is not committed") {
		return errors.New("Builder finish before final integration was not refused")
	}
	wait := func(task, state string, final bool) error {
		until := time.Now().Add(25 * time.Second)
		for {
			raw, err := workerMCPCall(client, "factory.context", map[string]any{})
			if err != nil {
				return err
			}
			var context struct {
				Execution struct {
					Tasks []struct {
						ID    string `json:"task_id"`
						State string `json:"state"`
					} `json:"tasks"`
					Integration *struct {
						Status    string `json:"status"`
						Candidate string `json:"candidate"`
					} `json:"integration"`
				} `json:"execution"`
			}
			if err = json.Unmarshal(raw, &context); err != nil {
				return err
			}
			if final && context.Execution.Integration != nil && context.Execution.Integration.Status == "committed" {
				return nil
			}
			for _, item := range context.Execution.Tasks {
				if item.ID == task && item.State == state {
					return nil
				}
				if item.ID == task && item.State == store.PlanTaskFailed {
					return errors.New("native task check failed")
				}
			}
			if time.Now().After(until) {
				return fmt.Errorf("native task wait expired for %s/%s", task, state)
			}
			time.Sleep(3 * time.Second)
		}
	}
	for i, id := range []string{"first", "second"} {
		if _, err := workerMCPCall(client, "factory.start_task", map[string]any{"task_id": id}); err != nil {
			return err
		}
		if i == 0 {
			missingRead, readErr := workerMCPCall(client, "factory.workspace", map[string]any{"task_id": id, "operation": "read", "path": "native-task.txt"})
			if readErr != nil {
				return readErr
			}
			var missing struct {
				Exists  *bool   `json:"exists"`
				Content *string `json:"content"`
			}
			if json.Unmarshal(missingRead, &missing) != nil || missing.Exists == nil || *missing.Exists || missing.Content != nil {
				return errors.New("missing workspace file did not return explicit absence")
			}
			for _, operation := range []map[string]any{
				{"operation": "mkdir", "path": "native-task-dir"},
				{"operation": "edit", "path": "native-task-dir/kept.txt", "expected": "", "replacement": "kept\n"},
				{"operation": "edit", "path": "native-task-dir/to-remove", "expected": "", "replacement": "discard\n"},
			} {
				operation["task_id"] = id
				if _, err := workerMCPCallID(client, "factory.workspace", operation, "workspace-"+operation["path"].(string)); err != nil {
					return err
				}
			}
			read, err := workerMCPCall(client, "factory.workspace", map[string]any{"task_id": id, "operation": "read", "path": "native-task-dir/to-remove"})
			if err != nil {
				return err
			}
			var file struct {
				Digest string `json:"file_digest"`
			}
			if json.Unmarshal(read, &file) != nil || !factory.ValidDigest(file.Digest) {
				return errors.New("workspace read omitted its file digest")
			}
			if _, err = workerMCPCallID(client, "factory.workspace", map[string]any{"task_id": id, "operation": "remove", "path": "native-task-dir/to-remove", "expected_digest": file.Digest}, "workspace-remove"); err != nil {
				return err
			}
		}
		expected := ""
		if i == 1 {
			expected = "first\n"
		}
		payload := map[string]any{"task_id": id, "operation": "edit", "path": "native-task.txt", "expected": expected, "replacement": id + "\n"}
		key := "native-task-" + id + "-edit"
		if i == 0 {
			for _, extra := range []map[string]any{{"limit": 8192}, {"expected_digest": strings.Repeat("a", 64)}} {
				invalid := maps.Clone(payload)
				for field, value := range extra {
					invalid[field] = value
				}
				if _, err := workerMCPCallID(client, "factory.workspace", invalid, factory.NewID()); err == nil || !strings.Contains(err.Error(), "workspace edit does not accept limit or expected_digest") {
					return fmt.Errorf("workspace edit read-field refusal was not actionable: %v", err)
				}
			}
			if err := workerLoseEditReply(client, payload, key); err != nil {
				return err
			}
		}
		first, err := workerMCPCallID(client, "factory.workspace", payload, key)
		if err != nil {
			return err
		}
		replay, err := workerMCPCallID(client, "factory.workspace", payload, key)
		if err != nil || !bytes.Equal(first, replay) {
			return errors.New("workspace exact edit replay differs")
		}
		read, err := workerMCPCall(client, "factory.workspace", map[string]any{"task_id": id, "operation": "read", "path": "native-task.txt"})
		if err != nil {
			return err
		}
		var content struct {
			Content    string `json:"content"`
			Digest     string `json:"file_digest"`
			Executable *bool  `json:"executable"`
		}
		if json.Unmarshal(read, &content) != nil || content.Content != id+"\n" {
			return errors.New("admitted workspace readback differs")
		}
		if i == 0 {
			if content.Executable == nil || *content.Executable || !factory.ValidDigest(content.Digest) {
				return errors.New("new workspace text file has no explicit nonexecutable readback")
			}
			mutation := map[string]any{"task_id": id, "operation": "executable", "path": "native-task.txt", "expected_digest": strings.Repeat("0", 64), "executable": true}
			if _, err = workerMCPCallID(client, "factory.workspace", mutation, "native-mode-refused"); err == nil || !strings.Contains(err.Error(), "preimage differs") {
				return errors.New("workspace executable stale digest was not refused")
			}
			mutation["expected_digest"] = content.Digest
			changed, err := workerMCPCallID(client, "factory.workspace", mutation, "native-mode-enabled")
			if err != nil {
				return err
			}
			replay, err := workerMCPCallID(client, "factory.workspace", mutation, "native-mode-enabled")
			if err != nil || !bytes.Equal(changed, replay) {
				return errors.New("MCP executable exact receipt replay differs")
			}
			read, err = workerMCPCall(client, "factory.workspace", map[string]any{"task_id": id, "operation": "read", "path": "native-task.txt"})
			if err != nil || json.Unmarshal(read, &content) != nil || content.Executable == nil || !*content.Executable || content.Content != id+"\n" {
				return errors.New("MCP executable change/readback differs")
			}
		}
		if _, err := workerMCPCall(client, "factory.submit_task_output", map[string]any{"task_id": id, "output_digest": strings.Repeat("e", 64), "evidence_refs": []string{"native-task.txt"}}); err == nil || !strings.Contains(err.Error(), "task output is not ready") {
			return errors.New("unreleased task output was not refused before recording")
		}
		if _, err = workerMCPCall(client, "factory.release_output", map[string]any{"task_id": id}); err != nil {
			return err
		}
		if err = wait(id, store.PlanTaskOutputReady, false); err != nil {
			return err
		}
		if i == 0 {
			if _, startErr := workerMCPCall(client, "factory.start_task", map[string]any{"task_id": "second"}); startErr == nil || !strings.Contains(startErr.Error(), "task start is waiting") {
				return errors.New("premature next task did not return its recoverable checkpoint wait")
			}
		}
		if _, err = workerMCPCallID(client, "factory.submit_task_output", map[string]any{"task_id": id, "evidence_refs": []string{"native-task.txt"}}, "native-task-"+id+"-output"); err != nil {
			return err
		}
		if err = wait(id, store.PlanTaskCompleted, false); err != nil {
			return err
		}
	}
	if err := wait("", "", true); err != nil {
		return err
	}
	if _, err := workerMCPCallID(client, "factory.finish_implementation", map[string]any{}, "native-task-second-output"); err == nil || !strings.Contains(err.Error(), "submission_id already records different work") {
		return errors.New("different tool under an existing task output identity was not refused")
	}
	fmt.Println("actual-admitted-sequential-native-tasks-and-final-checkpoint")
	return nil
}

func workerMCPCall(client *http.Client, tool string, payload any) (json.RawMessage, error) {
	return workerMCPCallID(client, tool, payload, "")
}

// Send the actual admitted edit and deliberately read no response. Independent
// workspace readback proves the effect before closing this transport; replay
// must recover its receipt because the edit's old-content precondition is gone.
func workerLoseEditReply(client *http.Client, payload map[string]any, operationID string) error {
	request, err := workerMCPRequest("factory.workspace", payload, operationID)
	if err != nil {
		return err
	}
	connection, err := net.DialTimeout("tcp", request.URL.Host, 2*time.Second)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	if err = request.Write(connection); err != nil {
		return err
	}
	until := time.Now().Add(6 * time.Second)
	for {
		raw, err := workerMCPCall(client, "factory.workspace", map[string]any{"task_id": payload["task_id"], "operation": "read", "path": payload["path"]})
		if err != nil {
			// The native workspace may still hold its bounded command gate for
			// the outstanding edit. Retry only this observation, never the edit.
			if strings.Contains(err.Error(), execution.ErrTaskUnavailable.Error()) && time.Now().Before(until) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return err
		}
		var file struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &file) != nil {
			return errors.New("lost-reply workspace readback invalid")
		}
		if file.Content == payload["replacement"] {
			fmt.Println("actual-admitted-workspace-write-before-lost-transport-reply")
			return connection.Close()
		}
		if time.Now().After(until) {
			return errors.New("lost-reply edit never took effect; no retry was submitted")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func workerMCPRequest(tool string, payload any, operationID string) (*http.Request, error) {
	arguments := map[string]any{"payload": payload}
	if operationID != "" {
		sum := sha256.Sum256([]byte("native-integration:" + operationID))
		arguments["submission_id"] = hex.EncodeToString(sum[:16])
	}
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": arguments}})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, workerMCPURL(), strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", os.Getenv("SODA_FACTORY_RUN_AUTH"))
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func workerMCPCallID(client *http.Client, tool string, payload any, operationID string) (json.RawMessage, error) {
	request, err := workerMCPRequest(tool, payload, operationID)
	if err != nil {
		return nil, err
	}
	reply, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer reply.Body.Close()
	var result struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(reply.Body, factory.MCPMaxResultPayloadBytes+8192)).Decode(&result) != nil || reply.StatusCode != http.StatusOK || len(result.Error) != 0 || result.Result.IsError || len(result.Result.Content) != 1 || !json.Valid([]byte(result.Result.Content[0].Text)) {
		return nil, fmt.Errorf("actual admitted worker tool failed: %s: error=%s content=%v", tool, result.Error, result.Result.Content)
	}
	return json.RawMessage(result.Result.Content[0].Text), nil
}

func workerMCPURL() string {
	for _, argument := range os.Args[1:] {
		if value, ok := strings.CutPrefix(argument, "mcpServers.factory="); ok {
			var server struct {
				URL string `json:"url"`
			}
			if json.Unmarshal([]byte(value), &server) == nil && strings.HasPrefix(server.URL, "http://127.0.0.1:") && strings.HasSuffix(server.URL, "/mcp") {
				return server.URL
			}
			return ""
		}
	}
	return "http://127.0.0.1:37377/mcp"
}

func ownedToolFixture(profile, instance, mode string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	timeout := 8 * time.Second
	if mode == "discovery" {
		timeout = 45 * time.Second
	}
	if mode == "update" {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	port := factoryruntime.NativeExecution{Config: config, Instance: instance}
	observed, err := port.InspectTool(ctx, project.FactoryHarnessMuse)
	for (mode == "discovery" || mode == "update") && err != nil && ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		observed, err = port.InspectTool(ctx, project.FactoryHarnessMuse)
	}
	if mode == "unavailable" {
		if err == nil {
			return errors.New("unselected native tool became available")
		}
		fmt.Println("PASS: unselected native executable remains unavailable")
		return nil
	}
	if err != nil {
		source, sourceErr := port.MuseLoginSource(context.WithoutCancel(ctx))
		return fmt.Errorf("native tool %s: %w; source status %+v (%v)", mode, err, source.ToolStatus, sourceErr)
	}
	if mode == "update" && filepath.Dir(observed.Native.Executable) != filepath.Join(profile, "native-tools", "muse") {
		return errors.New("missing installed version was not acquired into the private Factory cache")
	}
	if mode == "update" {
		source, err := port.MuseLoginSource(ctx)
		if err != nil || source.ToolStatus == nil || source.ToolStatus.State != "ready" || source.ToolStatus.SelectedVersion != observed.Native.Version || source.ToolStatus.Origin != "factory_cache" || source.ToolStatus.CheckedAtUnix <= 0 {
			return fmt.Errorf("native source did not project the verified tool status: %v", err)
		}
	}
	file, err := os.Open(observed.Native.Executable)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil || observed.Native.SHA256 != hex.EncodeToString(digest.Sum(nil)) {
		return errors.New("native tool observation differs from actual installed bytes")
	}
	if _, err = port.InspectTool(ctx, "unsupported"); err == nil {
		return errors.New("unknown native harness became available")
	}
	fmt.Println("PASS: actual Go/native tool callback verifies official stable Muse", observed.Native.Version, "ELF", observed.Native.SHA256)
	return nil
}

// The result owner supplies every source/scope binding and computes the digest.
func nativeTaskPlanPayload(correction bool) (map[string]any, error) {
	plan := nativeWorkspacePlan(strings.Repeat("0", 40))
	if correction {
		// Retain a final-tree invariant for the controller's correction checks;
		// the first task's exact bytes are checked by worker readback.
		plan.Checks[0].Argv = []string{"/usr/bin/bash", "-eu", "-c", "test -f native-task-dir/kept.txt; test ! -e native-task-dir/to-remove; test -f native-task.txt"}
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var authored map[string]any
	if err = json.Unmarshal(raw, &authored); err != nil {
		return nil, err
	}
	for _, field := range []string{"attempt_root", "source_commit", "integration_commit", "scope_digest", "input_digest", "preparation", "digest"} {
		delete(authored, field)
	}
	return map[string]any{"approach": plan.Approach, "tasks": authored, "evidence_refs": []string{"source:README.md"}}, nil
}

// Cross-language binding fixtures grant no task or provider admission.
func nativeWorkspacePlan(candidate string) factory.InvestigationPlan {
	checks := []factory.CheckSpec{
		{ID: "first", Argv: []string{"/usr/bin/bash", "-eu", "-c", "test -f native-task-dir/kept.txt; test ! -e native-task-dir/to-remove; grep -qx first native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeTask, Required: true},
		{ID: "second", Argv: []string{"/usr/bin/grep", "-qx", "second", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeTask, Required: true},
		{ID: "integration", Argv: []string{"/usr/bin/grep", "-qx", "second", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeIntegration, Required: true},
	}
	plan := factory.InvestigationPlan{ID: factory.NewID(), AttemptRoot: factory.NewID(),
		ScopeDigest: strings.Repeat("1", 64), InputDigest: strings.Repeat("2", 64), SourceCommit: candidate,
		IntegrationCommit: candidate, Preparation: "f" + strings.Repeat("3", 24),
		Approach: "Sequential native task fixture", AcceptanceCoverage: []string{"native-workspace"},
		Checks: checks, Lanes: []factory.PlanLane{{ID: "builder", TaskIDs: []string{"first", "second"}}},
	}
	for i, id := range []string{"first", "second"} {
		task := factory.PlanTask{ID: id, Objective: "Produce " + id + " text", Outputs: []string{"native-task.txt"},
			AcceptanceRefs: []string{"native-workspace"}, Lane: "builder", OwnedPaths: []string{"native-task.txt", "native-task-dir/kept.txt", "native-task-dir/to-remove"}, CheckIDs: []string{id}}
		if i == 1 {
			task.DependsOn = []string{"first"}
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	return plan
}
func workspaceManifest(root, run, candidate, mode string) error {
	plan := nativeWorkspacePlan(candidate)
	if mode == "failed" {
		plan.Checks[0].Argv = []string{"/usr/bin/false"}
	} else if mode == "cancel" {
		plan.Checks[0].Argv = []string{"/usr/bin/sleep", "30"}
		plan.Checks[0].TimeoutSeconds = 35
	}
	var err error
	plan.Digest, err = factory.PlanDigest(plan)
	if err != nil {
		return err
	}
	if err := plan.Executable(); err != nil {
		return err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	manifest, err := project.MarshalExecutionManifest(project.ExecutionManifest{Plan: raw, PlanDigest: project.PlanDigest(raw),
		Invocation: "staged-claim", Phase: project.ExecutionImplement, Run: run, Deadline: time.Now().UTC().Add(time.Minute),
		Effort: project.TaskEffortHigh, MaxChildren: 0})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "workspace.plan.json"), raw, 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "workspace.manifest"), manifest, 0600)
}

func workspaceEvents(root string) error {
	manifest, err := os.ReadFile(filepath.Join(root, "workspace.manifest"))
	if err != nil {
		return err
	}
	m, err := project.ParseExecutionManifest(manifest)
	if err != nil {
		return err
	}
	var plan factory.InvestigationPlan
	if err := json.Unmarshal(m.Plan, &plan); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "workspace.events.json"))
	if err != nil {
		return err
	}
	var events []project.TaskEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return err
	}
	if len(events) != 8 {
		return fmt.Errorf("expected two complete task event sequences, got %d", len(events))
	}
	imp := control.TaskEventImport{AttemptRoot: plan.AttemptRoot, PlanID: plan.ID, Invocation: "native-workspace-fixture",
		PlanDigest: project.ExecutionManifestDigest(manifest), MaxChildren: 0, Phase: project.ExecutionImplement, Events: events}
	result, err := control.ImportTaskEvents(plan, nil, imp)
	if err != nil {
		return err
	}
	if result.Stop || result.NextSequence != 8 {
		return errors.New("native workspace journal did not complete")
	}
	completed := map[string]store.PlanTaskReceipt{}
	for _, receipt := range result.Writes {
		completed[receipt.TaskID] = receipt
	}
	if len(completed) != 2 {
		return errors.New("native workspace task receipts missing")
	}
	receipts := []store.PlanTaskReceipt{}
	for _, receipt := range completed {
		if receipt.State != store.PlanTaskCompleted || !receipt.Parent || receipt.Commit == "" || receipt.Tree == "" || len(receipt.Checks) != 1 {
			return errors.New("native workspace task evidence incomplete")
		}
		receipts = append(receipts, receipt)
	}
	replay, err := control.ImportTaskEvents(plan, receipts, imp)
	if err != nil || len(replay.Writes) != 0 {
		return fmt.Errorf("native workspace exact event replay changed state: %v", err)
	}
	fmt.Println("PASS: native task journal imports both sequential checkpoints into Go receipts; exact replay emits no writes")
	return nil
}

func writeTaskManifestBindings(profile string, intent execution.NativeIntent) error {
	plan := []byte(`{"fixture":"unexecuted"}`)
	bindings := []map[string]any{}
	for _, mode := range []struct {
		authentication, phase string
		children              int
		controller            bool
	}{
		{"token", project.ExecutionImplement, 0, false}, {"muse_login", project.ExecutionImplement, 0, false},
		{"token", project.ExecutionImplement, 1, false}, {"muse_login", project.ExecutionCorrect, 0, false},
		{"muse_login", project.ExecutionImplement, 0, true}, {"muse_login", project.ExecutionCorrect, 0, true},
	} {
		bound := intent
		bound.Version, bound.Run, bound.Role = 5, factory.NewID(), project.RoleCoder
		bound.Native.BrokerRun = bound.Run
		if bound.Native.CredentialSHA256 == "" {
			bound.Native.CredentialSHA256 = strings.Repeat("0", 64)
		}
		phase, children := mode.phase, mode.children
		bound.Native.Authentication, bound.Native.TrustMode = "", "isolated_system_worker"
		if mode.authentication == "muse_login" {
			bound.Native.Authentication, bound.Native.CredentialSHA256, bound.Native.TrustMode = execution.NativeMuseLogin, "", execution.NativeCLIProvider
		}
		exact, encodeErr := project.MarshalExecutionManifest(project.ExecutionManifest{Plan: plan, PlanDigest: project.PlanDigest(plan), Invocation: bound.Run,
			Phase: phase, Run: bound.Run, Deadline: bound.Deadline, Effort: bound.Effort, Contributor: bound.Contributor, MaxChildren: children})
		if encodeErr != nil {
			return encodeErr
		}
		bound.ExecutionPhase, bound.ManifestDigest = phase, project.ExecutionManifestDigest(exact)
		if mode.controller {
			bound.Version = 6
			bound.Controller = &execution.NativeController{Connection: strings.Repeat("c", 32), GrantID: strings.Repeat("d", 32), ManifestDigest: strings.Repeat("e", 64)}
			old := bound
			old.Version = 5
			if _, err := old.Encode(); err == nil {
				return errors.New("historical intent acquired controller authority")
			}
			missing := bound
			missing.Controller = nil
			if _, err := missing.Encode(); err == nil {
				return errors.New("controller intent lost its authority capture")
			}
		}
		encoded, encodeErr := bound.Encode()
		if encodeErr != nil {
			return encodeErr
		}
		altered := bound
		altered.ManifestDigest = ""
		if _, encodeErr = altered.Encode(); encodeErr == nil {
			return errors.New("task intent accepted an absent manifest binding")
		}
		bindings = append(bindings, map[string]any{"intent": encoded, "manifest": exact})
	}
	data, encodeErr := json.Marshal(bindings)
	if encodeErr != nil {
		return encodeErr
	}
	if err := os.WriteFile(filepath.Join(profile, "task-manifest-bindings.json"), data, 0600); err != nil {
		return err
	}
	return nil
}

// Uses the running native product's persisted policy and real dispatch owner.
// The publication and foreign author are declared; every case must stop before
// provider observation/preparation, so this grants no connected vote evidence.
func nativeReviewPolicyFixture(profile, instance, repository string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	repo, err := strconv.ParseInt(repository, 10, 64)
	if err != nil {
		return err
	}
	policy, err := db.RepositoryPolicy(ctx, repo)
	if err != nil {
		return err
	}
	if policy.AutomationReviewer != nil || policy.AutomaticReviewEvent(policy.Review.ActorID+1, "APPROVED") != "COMMENT" {
		return errors.New("personal account policy permits votes")
	}
	original := policy
	defer func() {
		if current, e := db.RepositoryPolicy(context.Background(), repo); e == nil {
			original.Revision = current.Revision
			_ = db.SaveRepositoryPolicy(context.Background(), original)
		}
	}()
	save := func() error {
		if e := db.SaveRepositoryPolicy(ctx, policy); e != nil {
			return e
		}
		var e error
		policy, e = db.RepositoryPolicy(ctx, repo)
		return e
	}
	// Enable only the local declared policy. No coordinator is running here.
	policy.Enabled = true
	if err = save(); err != nil {
		return err
	}
	other := policy.Review.ActorID + 1
	target := factory.CheckTarget{Repository: repo, PRNumber: 7, PRID: 1700, IssueID: 1701, HeadRef: "refs/heads/fixture", BaseRef: "refs/heads/main", HeadOID: strings.Repeat("a", 40), BaseOID: strings.Repeat("b", 40)}
	assignment, run := factory.NewID(), factory.NewID()
	w := factory.ReviewWork{OperationID: "review-" + run, AuthRevision: factory.ReviewAuthRevision(assignment, run), Repository: repo, ActorID: policy.Review.ActorID, PRAuthorID: other, PRNumber: target.PRNumber, PRID: target.PRID, IssueID: target.IssueID, HeadRef: target.HeadRef, BaseRef: target.BaseRef, HeadOID: target.HeadOID, BaseOID: target.BaseOID, Event: "APPROVED", Body: "Declared review policy fixture", NativeRev: 1, NotAfter: time.Now().Add(time.Minute).Unix()}
	binding := policy.Review
	w.VoteBinding = &binding
	p := declaredPublishedFixture(target, collaboration.ActorID(other), assignment, 7, 1, w.NotAfter)
	p.ReviewOperations = []factory.ReviewOperation{{RunID: run, Work: w}}
	if err = p.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	conn, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, p.AssignmentID, p.Repository, p.Issue, p.Run, p.Stage, string(raw)); err != nil {
		return err
	}
	port := factoryruntime.NativeReviews{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: factoryruntime.NativeExecution{Store: db, Config: config, Instance: instance}}}
	refuse := func(label string) error {
		_, e := port.SubmitReview(ctx, w)
		var refusal *factory.PublicationRefusal
		if !errors.As(e, &refusal) || refusal.Reason != "automation_reviewer_not_authorized" {
			return fmt.Errorf("%s vote not refused before native admission: %w", label, e)
		}
		lookup, e := port.LookupOp(ctx, w.OperationID)
		if e != nil || !lookup.NotObserved {
			return errors.New("refused vote acquired native command history")
		}
		return nil
	}
	if err = refuse("personal"); err != nil {
		return err
	}
	policy.AutomationReviewer = &binding
	if err = save(); err != nil {
		return err
	}
	saved, err := db.RepositoryPolicy(ctx, repo)
	if err != nil {
		return err
	}
	if saved.AutomationReviewer == nil || *saved.AutomationReviewer != binding || saved.AutomaticReviewEvent(other, "REQUEST_CHANGES") != "REQUEST_CHANGES" || saved.AutomaticReviewEvent(binding.ActorID, "APPROVED") != "COMMENT" {
		return errors.New("automation designation lost identity or permits self vote")
	}
	comment := w
	comment.Event = "COMMENT"
	comment.AgentVerdict = "APPROVED"
	comment.VoteBinding = nil
	if comment.Validate() != nil {
		return errors.New("foreign-author personal inspection comment rejected")
	}
	comment.AgentVerdict = ""
	if comment.Validate() == nil {
		return errors.New("comment lost its independent verdict")
	}
	// Change only the saved connection; neither the designation nor frozen work
	// can silently authorize the replacement identity.
	changed := binding
	changed.NativeConnection = collaboration.ConnectionID(factory.NewID())
	policy.Review = changed
	if policy.Validate() == nil {
		return errors.New("designation silently follows a changed review binding")
	}
	policy.AutomationReviewer = &changed
	if err = save(); err != nil {
		return err
	}
	if err = refuse("replaced connection"); err != nil {
		return err
	}
	policy.Review = binding
	policy.AutomationReviewer = nil
	if err = save(); err != nil {
		return err
	}
	if err = refuse("withdrawn designation"); err != nil {
		return err
	}
	fmt.Println("PASS: actual Go/PostgreSQL/native dispatch refuses personal, withdrawn and replaced-identity queued votes before capture; explicit designation survives persistence, self votes stay comments, foreign-author inspection preserves its verdict; provider publication declared, no vote or merge qualification")
	return nil
}

// Uses the same captured-source and append-only owner as actual Issue Maker
// work. Only authored content is declared; this starts no model or worker run.
func nativeRequestRefinementFixture(profile, instance, post, phase string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	detail, err := db.FeedPost(ctx, post)
	if err != nil {
		return err
	}
	progress, err := db.PostIssueMaker(ctx, post, detail.Record.Revision)
	if err != nil {
		return err
	}
	effects := factoryruntime.NewNativeIssueEffects(factoryruntime.NativeInputSource{NativeExecution: factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}})
	title, body := "Refined request π", "A declared refined task.\nKeep exact bytes 🍵."
	if phase == "refine-native-request-blocked" {
		if err = effects.AuthorizeIssueMaker(ctx, progress.Scope); err != nil {
			return fmt.Errorf("human writing prevented safe append-only admission: %w", err)
		}
		for range 2 {
			if _, err = effects.Mutate(ctx, progress.Scope, "refine", "request", "Additional refinement", "Must preserve human writing.", "declared-after-human-edit"); err != nil {
				return err
			}
		}
		if _, err = effects.Mutate(ctx, progress.Scope, "refine", "unowned", "Unowned issue", "Must not edit an unowned issue.", "declared-unowned-edit"); err == nil {
			return errors.New("refinement admitted an issue without request ownership")
		}
		fmt.Println("PASS: human-edited issue accepts one journaled append-only refinement/replay; unowned issue key refused; no replacement or model")
		return nil
	}
	if err = effects.AuthorizeIssueMaker(ctx, progress.Scope); err != nil {
		return err
	}
	for range 2 {
		if _, err := effects.Mutate(ctx, progress.Scope, "refine", "request", title, body, "declared-refinement"); err != nil {
			return err
		}
	}
	current, err := db.PostIssueMaker(ctx, post, detail.Record.Revision)
	if err != nil {
		return err
	}
	found := false
	for _, issue := range current.Issues {
		if issue.Key == "request" && issue.Index == progress.Scope.Issue && issue.Title == title {
			found = true
		}
	}
	if !found {
		return errors.New("append-only refinement lost its original issue identity or semantic receipt")
	}
	fmt.Println("PASS: native request appends one journaled refinement; exact-step replay preserves issue identity and original creation; provider facts declared, no model")
	return nil
}

// Exercises native graph mechanics. Accepted direction and planner ownership are
// explicit fixture inputs; this does not qualify real planning or task execution.
func nativeGraphFixture(profile, instance, post, phase string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	detail, err := db.FeedPost(ctx, post)
	if err != nil {
		return err
	}
	owner, err := strconv.ParseInt(detail.Record.Author, 10, 64)
	if err != nil {
		return err
	}
	native := factoryruntime.NativeExecution{Store: db, Config: config, Instance: instance}
	graph := factoryruntime.NativeGraphs{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: native}}
	decision := func(kind string) string {
		return "d" + factory.MCPInputDigest([]byte("native-graph-fixture:" + post + ":" + kind))[:24]
	}
	revisionID := decision("positive-revision")
	acceptanceID := decision("partial-acceptance")
	if phase == "native-graph-partial" || phase == "native-graph-discovery" {
		proof, err := db.IssueIntakeGrant(ctx, post, detail.Record.Revision)
		if err != nil {
			return err
		}
		var grant struct {
			Connection collaboration.ConnectionID `json:"connection"`
		}
		if json.Unmarshal(proof, &grant) != nil || grant.Connection == "" {
			return errors.New("native graph fixture lacks original captured account")
		}
		ref := func(kind string) factory.ActorBindingRef {
			return factory.ActorBindingRef{ActorID: owner, NativeProfile: config.Profile, NativeConnection: grant.Connection, Kind: kind}
		}
		policy := factory.RepositoryPolicy{Repository: detail.Record.Repository, GrantedBy: owner, TargetBranch: "refs/heads/main", MergeMethod: "squash", Checks: []string{"fixture/check"}, Roles: map[string]factory.RoleSelection{project.RoleCoder: {Harness: "muse", Model: "fixture-unavailable"}, project.RoleReviewer: {Harness: "muse", Model: "fixture-unavailable"}}, AttemptLimits: factory.DefaultAttemptLimits(), ReassessmentMinutes: factory.DefaultReassessmentMinutes, Publish: ref(factory.OpRefPublish), Create: ref(factory.OpPRCreate), Review: ref(factory.OpReviewSubmit), Merge: ref(factory.OpMerge)}
		if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
			return err
		}
		policy, err = db.RepositoryPolicy(ctx, policy.Repository)
		if err != nil {
			return err
		}
		if phase == "native-graph-discovery" {
			return nativeCreationDiscoveryFixture(ctx, graph.NativeInputSource, policy, profile)
		}
		idea := factory.ProductIdea{ID: decision("idea"), Description: "Two declared fixture outcomes", Consequence: "Create two fixture issues with one dependency.", Decision: "accept", Source: "requested", Provenance: "human", Actor: owner}
		tasks := []factory.IntentTask{{Key: decision("first"), Objective: "First declared native graph task", Outcomes: []string{"First fixture outcome"}, IdeaIDs: []string{idea.ID}}, {Key: decision("second"), Objective: "Second declared native graph task", Outcomes: []string{"Second fixture outcome"}, IdeaIDs: []string{idea.ID}, DependsOn: []string{decision("first")}}}
		revision := factory.IntentRevision{ID: revisionID, Intent: decision("positive-intent"), Owner: owner, Repository: policy.Repository, Objective: "Verify the native graph journal", State: "accepted", MaxIssues: 2, CreatedUnix: time.Now().Unix(), Ideas: []factory.ProductIdea{idea}, Tasks: tasks}
		if err = db.SaveIntentRevision(ctx, revision); err != nil {
			return err
		}
		plan, err := control.IntentGraphPlan(revision)
		if err != nil {
			return err
		}
		payload, err := graph.EncodeGraph(plan, revision.Repository)
		if err != nil {
			return err
		}
		generation := factory.IntentGeneration{RevisionID: revision.ID, IntentID: revision.Intent, OperationID: "intent-" + revision.ID, ActorID: owner, NativeRev: policy.Revision, AuthRevision: revision.ID + ":" + revision.Digest(), NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: payload, IntentDigest: revision.Digest(), State: "pending", SubmittedTasks: tasks, PlanAssignment: factory.NewID()}
		// Planner output/ownership is declared here, not manufactured proof of a
		// successfully settled model plan. The ordinary native guard/send/read path
		// and original command journal are exercised below.
		raw, err := json.Marshal(generation)
		if err != nil {
			return err
		}
		conn, err := sql.Open("pgx", config.DatabaseDSN())
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err = conn.ExecContext(ctx, `INSERT INTO factory_intent_generations(revision,data) VALUES($1,$2)`, revision.ID, string(raw)); err != nil {
			return err
		}
		baseline, err := nativeCompleteObservation(ctx, graph.NativeInputSource, revision.Repository)
		if err != nil {
			return err
		}
		baselineJSON, err := json.Marshal(baseline)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(profile, "native-graph-prefix-"+generation.RevisionID+".json"), baselineJSON, 0600); err != nil {
			return err
		}
		outcome, err := graph.SubmitGraph(ctx, generation, revision.Repository)
		if err != nil {
			return err
		}
		if outcome.Effect != collaboration.EffectIndeterminate || outcome.Reason != "graph_steps_pending" {
			return errors.New("partial native graph claimed complete or lost its prefix")
		}
		if err = nativeGraphPrefixFixture(ctx, graph, generation, revision.Repository, profile, 1); err != nil {
			return err
		}
		lookup, err := graph.LookupGraph(ctx, generation, revision.Repository)
		if err != nil || !lookup.Resumable {
			return fmt.Errorf("known partial graph is not resumable: %w", err)
		}
		commandID := factory.MCPInputDigest([]byte("factory.native.graph\x00" + generation.OperationID + "\x000"))[:32]
		cmd, err := db.FactoryCommand(ctx, commandID)
		if err != nil {
			return err
		}
		observed, err := db.LookupHumanCommandDispatch(ctx, cmd)
		if err != nil || observed.Record == nil || observed.Record.Receipt == nil {
			return errors.New("partial native graph lost its original creation receipt")
		}
		var created struct {
			Issue struct {
				ExternalID string `json:"external_id"`
				Number     int64  `json:"number"`
			} `json:"issue"`
		}
		if json.Unmarshal(observed.Record.Receipt.Payload, &created) != nil {
			return errors.New("native graph creation projection absent")
		}
		id, err := strconv.ParseInt(created.Issue.ExternalID, 10, 64)
		if err != nil {
			return err
		}
		if err = db.BindIntentTaskAcceptance(ctx, factory.IntentTaskBinding{RevisionID: revision.ID, TaskKey: tasks[0].Key, IssueID: id, IssueIndex: created.Issue.Number, ContentVersion: 0, AcceptanceID: acceptanceID, CreatedUnix: time.Now().Unix()}); err != nil {
			return err
		}
		current, generated, err := db.GeneratedIntentCurrent(ctx, acceptanceID)
		if err != nil || !generated || current {
			return errors.New("partially adopted native graph became executable")
		}
		if err = nativeGraphLegacyFixture(ctx, db, config, revision, generation, created.Issue.ExternalID, created.Issue.Number, post, true); err != nil {
			return err
		}
		fmt.Println("PASS: first native graph write is retained, exact prefix is resumable, and a partial task binding stays ineffective; accepted direction/planner facts declared")
		return nil
	}
	generation, err := db.IntentGeneration(ctx, revisionID)
	if err != nil {
		return err
	}
	if err = nativeGraphLegacyFixture(ctx, db, config, factory.IntentRevision{}, generation, "", 0, post, false); err != nil {
		return err
	}
	lookup, err := graph.LookupGraph(ctx, generation, detail.Record.Repository)
	if err != nil {
		return err
	}
	if !lookup.Resumable {
		return errors.New("native graph lost its committed prefix after restart")
	}
	if err = nativeGraphPrefixFixture(ctx, graph, generation, detail.Record.Repository, profile, 1); err != nil {
		return err
	}
	for index := range 2 {
		outcome, err := graph.ResumeGraph(ctx, generation, detail.Record.Repository)
		if err != nil {
			return err
		}
		if err = nativeGraphPrefixFixture(ctx, graph, generation, detail.Record.Repository, profile, index+2); err != nil {
			return err
		}
		if outcome.Effect == collaboration.EffectCommitted {
			facts, err := graph.DecodeGraphReceipt(ctx, outcome.Receipt)
			if err != nil {
				return err
			}
			if len(facts.Creates) != 2 || len(facts.Edits) != 0 {
				return errors.New("native graph original receipt projection differs")
			}
			generation.State, generation.Receipt = "committed", outcome.Receipt
			if err = db.RecordIntentGeneration(ctx, generation, facts); err != nil {
				return err
			}
		}
	}
	if generation.State != "committed" {
		return errors.New("complete native graph never reached verified commit")
	}
	retained, err := graph.SubmitGraph(ctx, generation, detail.Record.Repository)
	if err != nil || retained.Effect != collaboration.EffectCommitted {
		return fmt.Errorf("native graph replay lost its receipt: %w", err)
	}
	current, generated, err := db.GeneratedIntentCurrent(ctx, acceptanceID)
	if err != nil || !generated || current {
		return errors.New("unadopted native graph released execution before completed generation")
	}
	raw, _ := json.MarshalIndent(map[string]any{"generation": generation, "execution_held": true, "planner": "declared; no accepted planner qualification", "provider": "declared local fixture"}, "", "  ")
	if err = os.WriteFile(filepath.Join(profile, "native-graph-fixture-result.json"), raw, 0600); err != nil {
		return err
	}
	fmt.Println("PASS: native graph resumes after supervisor restart, creates only remaining issue, adds its exact dependency through the native connector, verifies complete contents/edges, reuses original receipts without new writes and holds execution until full adoption; planner/provider facts declared, no model or full pipeline")
	if os.Getenv("SODA_NATIVE_CHECK_GRAPH_REVISION") == "1" {
		return nativeGraphRevisionFixture(ctx, graph, generation, detail.Record.Repository, profile)
	}
	return nil
}

// Reuse the installed owner's credential custody for real read-only discovery.
// Only the disposable database's policy is changed; original product state,
// credentials, funding and pending operations remain with the installed owner.
func connectedCreationPolicyRace(ctx context.Context, original factoryruntime.NativeInputSource, connection collaboration.ConnectionRef, repository int64) error {
	db, dsn, cleanup, err := store.OpenEphemeral(ctx, original.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer cleanup()
	host, err := original.Store.CollaborationHost(ctx, connection.Host)
	if err != nil {
		return err
	}
	actor, err := original.Store.CollaborationActor(ctx, connection.Actor)
	if err != nil {
		return err
	}
	profile, err := original.Store.CollaborationProfile(ctx, connection.Profile)
	if err != nil {
		return err
	}
	repo, err := original.Store.CollaborationRepository(ctx, collaboration.RepositoryID(repository))
	if err != nil {
		return err
	}
	binding, err := db.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{Profile: profile, Provider: host.Provider, APIBase: host.APIBase, Connection: connection.ID, Account: actor.ExternalID, Login: actor.Login})
	if err != nil {
		return err
	}
	local, err := db.ObserveCollaborationConnectionRepository(ctx, binding.Connection, repo.ExternalID, repo.Name)
	if err != nil {
		return err
	}
	policy, err := original.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return err
	}
	policy.Repository, policy.GrantedBy, policy.Revision = int64(local.ID), int64(binding.Actor.ID), 0
	policy.Enabled, policy.Paused = true, false
	for _, ref := range []*factory.ActorBindingRef{&policy.Publish, &policy.Create, &policy.Review, &policy.Merge} {
		ref.ActorID = int64(binding.Actor.ID)
	}
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	input := original
	input.Store = db
	after, err := original.Store.CreationRecoveryCursor(ctx, repository)
	if err != nil {
		return err
	}
	if err = db.EnqueueReadinessCreationPage(ctx, policy.Repository, 0, after, nil); err != nil {
		return err
	}
	for _, reenroll := range []bool{false, true} {
		captured, err := db.RepositoryPolicy(ctx, policy.Repository)
		if err != nil {
			return err
		}
		page, err := input.ReadCreations(ctx, policy.Repository, after, 16)
		if err != nil || !page.ProvenanceComplete || page.SourceCursor == nil || len(page.Events) != 0 {
			return fmt.Errorf("connected discovery requires a complete empty retained fixture page: %v", err)
		}
		// Hold this real reply at the coordinator's persistence boundary.
		withdrawn := captured
		withdrawn.Enabled = false
		if err = db.SaveRepositoryPolicy(ctx, withdrawn); err != nil {
			return err
		}
		withdrawn, err = db.RepositoryPolicy(ctx, policy.Repository)
		if err != nil {
			return err
		}
		if reenroll {
			withdrawn.Enabled = true
			if err = db.SaveRepositoryPolicy(ctx, withdrawn); err != nil {
				return err
			}
		}
		if err = db.EnqueueReadinessCreationScanPage(ctx, policy.Repository, after, page.NextPosition, nil, captured.Revision, page.ExpectedSourceCursor, *page.SourceCursor); !errors.Is(err, store.ErrAdmissionChanged) {
			return fmt.Errorf("withdrawal/re-enrollment admitted original connected page: %v", err)
		}
		if cursor, err := db.CreationRecoveryCursor(ctx, policy.Repository); err != nil || cursor != after {
			return errors.New("withdrawal consumed original connected cursor")
		}
		if !reenroll {
			withdrawn.Enabled = true
			if err = db.SaveRepositoryPolicy(ctx, withdrawn); err != nil {
				return err
			}
		}
	}
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer raw.Close()
	var effects int
	if err = raw.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_runs)+(SELECT count(*) FROM factory_usage)+(SELECT count(*) FROM factory_assignments)+(SELECT count(*) FROM issue_acceptance_decisions)+(SELECT count(*) FROM factory_readiness_sources)`).Scan(&effects); err != nil || effects != 0 {
		return fmt.Errorf("connected refused intake created execution, spending or work: count=%d err=%v", effects, err)
	}
	fmt.Println("PASS: real installed native GitHub empty discovery replies held at the persistence boundary are refused after actual disposable-PG policy withdrawal and withdrawal/re-enrollment; cursor unchanged, zero work/acceptances/runs/charges; original profile/policy/credentials untouched. Provider reads real, race timing controlled; positive original-issue admission races use the separate controlled check; no inference or remote write.")
	return nil
}

func connectedCreationFixture(ctx context.Context, input factoryruntime.NativeInputSource, actor int64, connection collaboration.ConnectionID, repository, issue int64, profile, action string) error {
	path := filepath.Join(profile, "native-creation-connected.json")
	var saved struct {
		Hint     control.IntakeHint
		Decision string
	}
	if action != "check-background-creation" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &saved) != nil {
			return errors.New("original connected creation result absent")
		}
		if action == "check-background-creation-withdrawn" {
			if _, err = input.ReadCreations(ctx, repository, issue-1, 1); err == nil {
				return errors.New("withdrawn native account still discovers issues")
			}
			if err = input.CheckInitialCreator(ctx, saved.Hint); err == nil {
				return errors.New("withdrawn native account still grants new initial acceptance")
			}
			decision, err := input.Store.AcceptanceDecision(ctx, saved.Decision)
			if err != nil || decision.ID != saved.Decision {
				return errors.New("withdrawal changed original acceptance history")
			}
			fmt.Println("PASS: withdrawn local native grant refuses discovery/new creator authorization and preserves original acceptance")
			return nil
		}
	} else {
		ref := func(kind string) factory.ActorBindingRef {
			return factory.ActorBindingRef{ActorID: actor, NativeProfile: input.Config.Profile, NativeConnection: connection, Kind: kind}
		}
		policy := factory.RepositoryPolicy{Repository: repository, GrantedBy: actor, Enabled: true, TargetBranch: "refs/heads/main", MergeMethod: "squash", Checks: []string{"fixture/unavailable"}, Roles: map[string]factory.RoleSelection{}, AttemptLimits: factory.DefaultAttemptLimits(), ReassessmentMinutes: factory.DefaultReassessmentMinutes, Publish: ref(factory.OpRefPublish), Create: ref(factory.OpPRCreate), Review: ref(factory.OpReviewSubmit), Merge: ref(factory.OpMerge)}
		for _, role := range []string{project.RoleCoder, project.RoleReviewer} {
			policy.Roles[role] = factory.RoleSelection{Account: factory.NewID(), Harness: project.FactoryHarnessMuse, Model: "fixture-unavailable"}
		}
		if err := input.Store.SaveRepositoryPolicy(ctx, policy); err != nil {
			return err
		}
		page, err := input.ReadCreations(ctx, repository, issue-1, 1)
		if err != nil {
			return err
		}
		if len(page.Events) != 1 || page.Events[0].Issue != issue || page.NextPosition != issue || !page.ProvenanceComplete {
			return errors.New("connected original issue did not produce complete creation facts")
		}
		event := page.Events[0]
		if event.Creator != actor || !event.CreatorWrite || !event.Verified {
			return errors.New("connected creation lacks current native author/write proof")
		}
		saved.Hint = control.IntakeHint{Delivery: event.Delivery, Repository: repository, Issue: issue, Created: true, Creator: event.Creator, CreatorWrite: event.CreatorWrite, CreationVerified: event.Verified, NativeIssueID: event.OriginalIssueID, CreatedUnix: event.CreatedUnix, TitleDigest: event.TitleDigest, ContentDigest: event.ContentDigest}
	}
	c := control.NewCoordinator(input.Store, input.NativeExecution, input.NativeExecution)
	c.AcceptanceReads = input
	accepted, err := c.AdmitInitialAcceptance(ctx, saved.Hint)
	if err != nil {
		return err
	}
	if action != "check-background-creation" && accepted.ID != saved.Decision {
		return errors.New("connected retained acceptance changed identity")
	}
	saved.Decision = accepted.ID
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	if action == "check-background-creation" {
		fmt.Println("PASS: actual GitHub original issue/current creator write permission produce automatic initial acceptance through Rust/Go/PostgreSQL; local policy declared, no model or remote write")
	} else {
		page, err := input.ReadCreations(ctx, repository, issue-1, 1)
		if err != nil {
			return err
		}
		if len(page.Events) != 0 || page.NextPosition != issue {
			return errors.New("retained explicit acceptance was replaced by discovery")
		}
		fmt.Println("PASS: original initial acceptance survives supervisor restart and discovery leaves its retained head intact")
	}
	return nil
}

func nativeCreationDiscoveryFixture(ctx context.Context, input factoryruntime.NativeInputSource, policy factory.RepositoryPolicy, profile string) error {
	disabled, err := input.ReadCreations(ctx, policy.Repository, 0, 16)
	if err != nil || len(disabled.Events) != 0 || disabled.NextPosition != 0 {
		return errors.New("disabled native intake observed new work")
	}
	policy.Enabled = true
	for role, selection := range policy.Roles {
		selection.Account = factory.NewID()
		policy.Roles[role] = selection
	}
	if err = input.Store.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	policy, err = input.Store.RepositoryPolicy(ctx, policy.Repository)
	if err != nil {
		return err
	}
	page := factory.CreationRecoveryBatch{Events: []factory.OriginalCreationEvent{}}
	var position int64
	for step := 0; step < 16; step++ {
		observed, e := input.ReadCreations(ctx, policy.Repository, position, 16)
		if e != nil {
			return e
		}
		if observed.SourceCursor == nil {
			return errors.New("native discovery cursor absent")
		}
		if e = input.Store.EnqueueReadinessCreationScanPage(ctx, policy.Repository, 0, 0, nil, policy.Revision, observed.ExpectedSourceCursor, *observed.SourceCursor); e != nil {
			return e
		}
		page.Events = append(page.Events, observed.Events...)
		page.NextPosition = observed.NextPosition
		page.ReachedRetainedEnd = observed.ReachedRetainedEnd
		page.ProvenanceComplete = observed.ProvenanceComplete
		position = observed.NextPosition
		if observed.ReachedRetainedEnd {
			break
		}
	}
	if err != nil {
		return err
	}
	if len(page.Events) != 3 || page.NextPosition != 20 || !page.ReachedRetainedEnd || !page.ProvenanceComplete {
		return fmt.Errorf("native creation discovery facts differ: count=%d cursor=%d", len(page.Events), page.NextPosition)
	}
	byNumber := map[int64]factory.OriginalCreationEvent{}
	for _, event := range page.Events {
		byNumber[event.Issue] = event
	}
	if _, found := byNumber[19]; found || !byNumber[18].CreatorWrite || byNumber[20].CreatorWrite {
		return errors.New("native discovery invented original/writer evidence")
	}
	hint := func(event factory.OriginalCreationEvent) control.IntakeHint {
		return control.IntakeHint{Delivery: event.Delivery, Repository: event.Repository, Issue: event.Issue, Created: true, Creator: event.Creator, CreatorWrite: event.CreatorWrite, CreationVerified: event.Verified, NativeIssueID: event.OriginalIssueID, CreatedUnix: event.CreatedUnix, TitleDigest: event.TitleDigest, ContentDigest: event.ContentDigest}
	}
	c := control.NewCoordinator(input.Store, input.NativeExecution, input.NativeExecution)
	c.AcceptanceReads = input
	c.Creations = input
	refuse := func(event factory.OriginalCreationEvent, reason string) error {
		_, err := c.AdmitInitialAcceptance(ctx, hint(event))
		var refusal *control.AcceptanceRefusal
		if !errors.As(err, &refusal) || refusal.Reason != reason {
			return fmt.Errorf("native initial acceptance refused incorrectly: reason=%s err=%v", reason, err)
		}
		return nil
	}
	if err = refuse(byNumber[17], control.RefusalCreationFactory); err != nil {
		return err
	}
	if err = refuse(byNumber[20], control.RefusalCreationUnauthorized); err != nil {
		return err
	}
	policy.Enabled = false
	if err = input.Store.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	policy, err = input.Store.RepositoryPolicy(ctx, policy.Repository)
	if err != nil {
		return err
	}
	if err = refuse(byNumber[18], control.RefusalCreationUnauthorized); err != nil {
		return err
	}
	if _, err = input.Store.AcceptanceHead(ctx, policy.Repository, 18); !errors.Is(err, store.ErrNotFound) {
		return errors.New("disabled policy left a queued initial acceptance")
	}
	policy.Enabled = true
	if err = input.Store.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	permissionFile := filepath.Join(filepath.Dir(profile), "io", "creator-permission")
	if err = os.WriteFile(permissionFile, []byte("read"), 0600); err != nil {
		return err
	}
	if err = refuse(byNumber[18], control.RefusalCreationUnauthorized); err != nil {
		return err
	}
	if _, err = input.Store.AcceptanceHead(ctx, policy.Repository, 18); !errors.Is(err, store.ErrNotFound) {
		return errors.New("withdrawn creator permission left an acceptance")
	}
	if err = os.WriteFile(permissionFile, []byte("unknown"), 0600); err != nil {
		return err
	}
	if _, err = input.ReadCreations(ctx, policy.Repository, 17, 1); err == nil {
		return errors.New("unknown creator permission became an intake fact")
	}
	if cursor, err := input.Store.CreationRecoveryCursor(ctx, policy.Repository); err != nil || cursor != 0 {
		return errors.New("unverified native discovery advanced cursor")
	}
	if err = os.WriteFile(permissionFile, []byte("write"), 0600); err != nil {
		return err
	}
	accepted, err := c.AdmitInitialAcceptance(ctx, hint(byNumber[18]))
	if err != nil {
		return err
	}
	replay, err := c.AdmitInitialAcceptance(ctx, hint(byNumber[18]))
	if err != nil || replay.ID != accepted.ID {
		return errors.New("native original acceptance replay changed identity")
	}
	if err = c.Start(ctx, filepath.Join(profile, "native-intake-check.lock")); err != nil {
		return err
	}
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		cursor, e := input.Store.CreationRecoveryCursor(ctx, policy.Repository)
		if e != nil {
			return e
		}
		if cursor == 20 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err = c.Close(); err != nil {
		return err
	}
	if cursor, err := input.Store.CreationRecoveryCursor(ctx, policy.Repository); err != nil || cursor != 20 {
		return fmt.Errorf("native serving discovery cursor did not settle: cursor=%d err=%v", cursor, err)
	}
	remaining, err := input.ReadCreations(ctx, policy.Repository, 20, 16)
	if err != nil || len(remaining.Events) != 0 || remaining.NextPosition != 20 {
		return errors.New("native discovery replay changed retained position")
	}
	if _, err = input.Store.AcceptanceHead(ctx, policy.Repository, 17); !errors.Is(err, store.ErrNotFound) {
		return errors.New("native intake accepted its own generated issue")
	}
	fmt.Println("PASS: native original-issue discovery/current creator permission, disabled-policy refusal, edited/read-only exclusion, unknown-permission hold, pre-admission withdrawal, same-account acceptance/replay, factory-origin refusal and actual serving cursor persistence; provider/authority declared, no model or full issue-to-PR pipeline")
	return nil
}

func nativeGraphPrefixFixture(ctx context.Context, graph factoryruntime.NativeGraphs, g factory.IntentGeneration, repository int64, profile string, steps int) error {
	encoded, err := os.ReadFile(filepath.Join(profile, "native-graph-prefix-"+g.RevisionID+".json"))
	if err != nil {
		return err
	}
	var raw []control.NativeInventoryIssue
	if json.Unmarshal(encoded, &raw) != nil {
		return errors.New("native graph baseline absent")
	}
	baseline := []factory.CompletionIssueEvidence{}
	for _, issue := range raw {
		body, e := factory.ParseCompletionIssueEvidence(issue.Data)
		if e != nil {
			return e
		}
		baseline = append(baseline, body)
	}
	prefix, err := graph.ReadGraphPrefix(ctx, g, repository)
	if err != nil || prefix.Repository != repository || prefix.Actor != g.ActorID || len(prefix.Effects) != steps {
		return fmt.Errorf("native graph prefix identity/count: steps=%d err=%v", len(prefix.Effects), err)
	}
	expected, err := prefix.ApplyTo(baseline)
	if err != nil {
		return err
	}
	current, err := nativeCompleteObservation(ctx, graph.NativeInputSource, repository)
	if err != nil {
		return err
	}
	actual := []factory.CompletionIssueEvidence{}
	for _, issue := range current {
		body, e := factory.ParseCompletionIssueEvidence(issue.Data)
		if e != nil {
			return e
		}
		actual = append(actual, body)
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if !bytes.Equal(a, b) {
		return errors.New("original graph step receipts do not predict the exact current inventory")
	}
	if steps == 3 {
		fmt.Println("PASS: original confirmed graph steps predict exact native inventories without adopting partial tasks or replacing the baseline; provider/planner declared")
	}
	return nil
}

func nativeGraphRevisionFixture(ctx context.Context, graph factoryruntime.NativeGraphs, prior factory.IntentGeneration, repository int64, profile string) error {
	revision, err := graph.Store.IntentGenerationHead(ctx, prior.RevisionID)
	if err != nil {
		return err
	}
	facts, err := graph.DecodeGraphReceipt(ctx, prior.Receipt)
	if err != nil {
		return err
	}
	next := revision
	next.ID = "d" + factory.NewID()[:24]
	next.Predecessor = revision.ID
	next.CreatedUnix = time.Now().Unix()
	next.Tasks = append([]factory.IntentTask(nil), revision.Tasks...)
	next.Tasks[1].Objective = "Revised second native graph task"
	next.Tasks[1].DependsOn = nil
	if err = graph.Store.SaveIntentRevision(ctx, next); err != nil {
		return err
	}
	desired, err := control.IntentGraphPlan(next)
	if err != nil {
		return err
	}
	plan := collaboration.GraphIntent{}
	refs := []collaboration.IssueRef{}
	ids := map[string]int64{}
	for index, task := range next.Tasks {
		identity := facts.Creates[task.Key]
		observed, _, e := graph.ReadGeneratedIssue(ctx, repository, identity.IssueIndex, prior.ActorID)
		if e != nil {
			return e
		}
		ref := collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: collaboration.RepositoryID(repository), ExternalID: strconv.FormatInt(identity.IssueID, 10), Number: identity.IssueIndex}
		refs = append(refs, ref)
		ids[task.Key] = identity.IssueID
		plan.Edits = append(plan.Edits, collaboration.GraphEdit{Issue: ref, Expected: collaboration.IssueContent{Title: observed.Issue.Title, Body: observed.Issue.Content, Revision: collaboration.Revision(strconv.Itoa(observed.Issue.ContentVersion))}, Title: desired.Creates[index].Title, Body: desired.Creates[index].Body})
	}
	plan.Dependencies = []collaboration.GraphDependency{{Issue: collaboration.GraphRef{Issue: &refs[1]}, Prerequisite: collaboration.GraphRef{Issue: &refs[0]}, Remove: true}}
	payload, err := graph.EncodeGraph(plan, repository)
	if err != nil {
		return err
	}
	g := factory.IntentGeneration{RevisionID: next.ID, IntentID: next.Intent, OperationID: "intent-" + next.ID, ActorID: prior.ActorID, NativeRev: prior.NativeRev, AuthRevision: next.ID + ":" + next.Digest(), NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: payload, IntentDigest: next.Digest(), State: "pending", SubmittedTasks: next.Tasks, TaskIssueIDs: ids, PlanAssignment: factory.NewID()}
	raw, _ := json.Marshal(g)
	conn, err := sql.Open("pgx", graph.Config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `INSERT INTO factory_intent_generations(revision,data) VALUES($1,$2)`, g.RevisionID, string(raw)); err != nil {
		return err
	}
	baseline, err := nativeCompleteObservation(ctx, graph.NativeInputSource, repository)
	if err != nil {
		return err
	}
	raw, _ = json.Marshal(baseline)
	if err = os.WriteFile(filepath.Join(profile, "native-graph-prefix-"+g.RevisionID+".json"), raw, 0600); err != nil {
		return err
	}
	for index := range 3 {
		outcome, e := graph.ResumeGraph(ctx, g, repository)
		if e != nil {
			return e
		}
		if err = nativeGraphPrefixFixture(ctx, graph, g, repository, profile, index+1); err != nil {
			return err
		}
		if index < 2 && outcome.Reason != "graph_steps_pending" {
			return errors.New("partial native edit graph claimed complete")
		}
		if index == 2 {
			if outcome.Effect != collaboration.EffectCommitted {
				return errors.New("native revised graph did not verify")
			}
			receipt, e := graph.DecodeGraphReceipt(ctx, outcome.Receipt)
			if e != nil {
				return e
			}
			if len(receipt.Edits) != 2 || len(receipt.Creates) != 0 {
				return errors.New("native revised graph receipt differs")
			}
			g.State, g.Receipt = "committed", outcome.Receipt
			if err = graph.Store.RecordIntentGeneration(ctx, g, receipt); err != nil {
				return err
			}
		}
	}
	if _, err = graph.SubmitGraph(ctx, g, repository); err != nil {
		return err
	}
	fmt.Println("PASS: native conditional edits and dependency removal retain exact original baseline/receipts, predict every intermediate inventory and verify the revised graph without duplicate writes; accepted plan/provider declared, no full protected lifecycle")
	return nil
}

// Retained-format fixture: declare one unclaimed legacy graph command, restart
// through the real schema opener, then prove its payload/uncertainty are kept.
func nativeGraphLegacyFixture(ctx context.Context, db *store.Store, config factoryruntime.NativeConfig, revision factory.IntentRevision, generation factory.IntentGeneration, blockingID string, blockingNumber int64, post string, prepare bool) error {
	path := filepath.Join(config.Directory, "legacy-graph-command.json")
	var old factory.Command
	if !prepare {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &old); err != nil {
			return err
		}
		current, err := db.FactoryCommand(ctx, old.ID)
		if err != nil {
			return err
		}
		if current.Payload != old.Payload || current.Digest != old.Digest || current.Type != old.Type || current.Finished != "" {
			return errors.New("legacy graph command changed during upgrade")
		}
		captured, err := factory.DecodeIssueDependencyCapture(current)
		if err != nil {
			return err
		}
		if won, err := db.ReserveIssueDependency(ctx, captured); err != nil || won {
			return errors.New("legacy graph command acquired new dispatch ownership")
		}
		id := "d" + factory.MCPInputDigest([]byte("native-legacy-graph:" + post))[:24]
		g, err := db.IntentGeneration(ctx, id)
		if err != nil {
			return err
		}
		graph := factoryruntime.NativeGraphs{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: factoryruntime.NativeExecution{Store: db, Config: config, Instance: os.Args[2]}}}
		observed, err := graph.LookupGraph(ctx, g, int64(captured.Request.Issue.Repository))
		if err != nil {
			return err
		}
		if observed.Resumable || observed.Record == nil || observed.Record.Effect != collaboration.EffectIndeterminate || observed.Record.Reason != "graph_step_legacy_dispatch_unknown" {
			return errors.New("native graph treated legacy send uncertainty as resumable absence")
		}
		prefix, err := graph.ReadGraphPrefix(ctx, g, int64(captured.Request.Issue.Repository))
		if err != nil || len(prefix.Effects) != 0 {
			return errors.New("legacy unknown send became a confirmed graph effect")
		}
		held, err := graph.ResumeGraph(ctx, g, int64(captured.Request.Issue.Repository))
		if err != nil || held.Effect != collaboration.EffectIndeterminate {
			return errors.New("native graph replay failed to hold original uncertainty")
		}
		cancelled, err := graph.CancelGraph(ctx, g.OperationID)
		if err != nil || cancelled.Effect != collaboration.EffectIndeterminate || cancelled.Cancellation != collaboration.CancellationIndeterminate {
			return errors.New("native graph cancellation erased send uncertainty")
		}
		fmt.Println("PASS: actual retained schema-95 upgrade preserves original graph command bytes, fences legacy send history and refuses graph resume/cancellation claims of absence")
		return nil
	}
	originalID := factory.MCPInputDigest([]byte("factory.native.issue-intake\x00" + string(config.Profile) + "\x00" + post + "\x000"))[:32]
	original, err := db.FactoryCommand(ctx, originalID)
	if err != nil {
		return err
	}
	capture, err := factory.DecodeHumanIssueCreateCapture(original)
	if err != nil {
		return err
	}
	lookup, err := db.LookupHumanCommandDispatch(ctx, original)
	if err != nil || lookup.Record == nil || lookup.Record.Receipt == nil {
		return errors.New("legacy fixture lacks original issue receipt")
	}
	var issue struct {
		Issue struct {
			ID     string `json:"external_id"`
			Number int64  `json:"number"`
		} `json:"issue"`
	}
	if json.Unmarshal(lookup.Record.Receipt.Payload, &issue) != nil {
		return errors.New("legacy fixture issue identity absent")
	}
	revision.ID = "d" + factory.MCPInputDigest([]byte("native-legacy-graph:" + post))[:24]
	revision.Intent = "d" + factory.MCPInputDigest([]byte("native-legacy-intent:" + post))[:24]
	if err = db.SaveIntentRevision(ctx, revision); err != nil {
		return err
	}
	root := collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: capture.Request.Repository, ExternalID: issue.Issue.ID, Number: issue.Issue.Number}
	blocking := collaboration.IssueRef{Kind: collaboration.SubjectIssue, Repository: root.Repository, ExternalID: blockingID, Number: blockingNumber}
	graph := factoryruntime.NativeGraphs{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: factoryruntime.NativeExecution{Store: db, Config: config, Instance: os.Args[2]}}}
	payload, err := graph.EncodeGraph(collaboration.GraphIntent{Dependencies: []collaboration.GraphDependency{{Issue: collaboration.GraphRef{Issue: &root}, Prerequisite: collaboration.GraphRef{Issue: &blocking}}}}, revision.Repository)
	if err != nil {
		return err
	}
	generation.RevisionID, generation.IntentID, generation.OperationID, generation.AuthRevision, generation.IntentDigest = revision.ID, revision.Intent, "intent-"+revision.ID, revision.ID+":"+revision.Digest(), revision.Digest()
	generation.Payload = payload
	generation.TaskIssueIDs = map[string]int64{}
	raw, err := json.Marshal(generation)
	if err != nil {
		return err
	}
	conn, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `INSERT INTO factory_intent_generations(revision,data) VALUES($1,$2)`, revision.ID, string(raw)); err != nil {
		return err
	}
	id := factory.MCPInputDigest([]byte("factory.native.graph\x00" + generation.OperationID + "\x000"))[:32]
	legacy := factory.IssueDependencyCapture{Request: factory.IssueDependencyCommand{ID: id, Host: capture.Request.Host, Actor: capture.Request.Actor, Issue: root, Blocking: blocking, Expected: []string{}}, Authority: capture.Authority, OperationID: id, ProviderIntent: []byte(`{"declared_legacy_graph_preparation":true}`)}
	if _, _, err = db.CaptureIssueDependency(ctx, legacy); err != nil {
		return err
	}
	old, err = db.FactoryCommand(ctx, id)
	if err != nil {
		return err
	}
	raw, _ = json.Marshal(old)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	// This private fixture declares retained pre-protocol history; it does not
	// downgrade a user profile or claim an installed upgrade is qualified.
	_, err = conn.ExecContext(ctx, `UPDATE schema_version SET version=95`)
	return err
}

// Declared history for the composed native HTTP journey, never worker evidence.
func seedWorkDetail(profile, instance, argument string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	repository, err := strconv.ParseInt(argument, 10, 64)
	if err != nil {
		return err
	}
	workspace, native, err := db.ExecutionWorkspace(ctx, collaboration.RepositoryID(repository))
	if err != nil || native == nil || native.Profile != config.Profile {
		return errors.New("fixture native enrollment absent")
	}
	now := time.Now().UTC()
	run := factory.Run{ID: strings.Repeat("a", 32), ProjectID: workspace, Role: "reviewer", InputSHA: strings.Repeat("a", 40), Started: now, Deadline: now.Add(time.Hour), Harness: "muse-fixture-1", Model: "local-fixture"}
	run.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("b", 64), Executable: "/home/fixture/bin/muse"}, BundleSHA256: strings.Repeat("d", 64), PromptSHA256: strings.Repeat("e", 64), Provenance: execution.NativeProvenance{TrustMode: "isolated_system_worker", Profile: config.Profile, InstanceID: instance, WorktreeID: "declared-native-checkout", BrokerRun: strings.Repeat("c", 32), Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.NotStarted}
	if err = db.RecordFactoryRun(ctx, run); err != nil {
		return err
	}
	run.Native.Sealed, run.Native.Retirement, run.Reconciled, run.Outcome = true, execution.Retired, true, factory.Cancelled
	if err = db.SaveFactoryRun(ctx, run); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: run.ID, Repository: repository, Issue: 12}); err != nil {
		return err
	}
	c := control.NewCoordinator(db, nil, nil)
	unavailable := factoryservice.RunStatus{Store: db, WriteAccess: func(context.Context, collaboration.ActorID, collaboration.RepositoryID) error { return nil }, RunRepository: func(context.Context, collaboration.ActorID, factory.Run) (collaboration.RepositoryID, error) {
		return collaboration.RepositoryID(repository), nil
	}}
	if _, e := unavailable.Read(ctx, 1, run.ID); !errors.Is(e, factoryservice.ErrRunStatusUnavailable) {
		return errors.New("missing inspector admitted run read")
	}
	commands := factoryservice.Interventions{Store: db, Writer: c}
	if _, e := commands.Repository(ctx, 1, "1", collaboration.RepositoryID(repository), factoryservice.RepositoryCommand{Action: factory.ActionPause}); !errors.Is(e, factoryservice.ErrInterventionUnavailable) {
		return errors.New("missing authority admitted mutation")
	}
	_, _, err = db.RecordIssueAssessment(ctx, factory.IssueControl{Repository: repository, Issue: 12, Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible, Fingerprint: strings.Repeat("a", 64), Authority: strings.Repeat("b", 64)}, now)
	return err
}

// Declared observations exercise the production Coordinator over disposable PG.
type progressCheckObserver func(context.Context, factory.CheckTarget, int64) (factory.ObservedChecks, error)

func (f progressCheckObserver) ObserveChecks(ctx context.Context, target factory.CheckTarget, actor int64) (factory.ObservedChecks, error) {
	return f(ctx, target, actor)
}

type progressGraphObserver struct {
	factoryruntime.NativeGraphs
	observe func(context.Context) (int64, error)
}

type progressMergeObserver struct {
	factoryruntime.NativeMerges
	lookup func(string) (factory.OperationOutcome, error)
	cancel func(string) (factory.OperationOutcome, error)
}

type progressClosedIssueReads struct {
	closed               bool
	acceptance, dispatch int
}

func (r *progressClosedIssueReads) ObserveNativeRevision(context.Context) (int64, bool, error) {
	return 1, true, nil
}

func (r *progressClosedIssueReads) ReadAcceptanceEvidence(_ context.Context, _, issue string, _ []string) (control.AcceptanceEvidence, error) {
	r.acceptance++
	return control.AcceptanceEvidence{Revision: 1, Issue: control.AcceptanceIssueView{ID: "13", Index: issue, TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64), ContentVer: 1, TitleOccurrence: factory.OriginalOccurrence, LifecycleOccurrence: "3", ClosingOccurrence: "3", Visible: true, Closed: r.closed}}, nil
}

func (r *progressClosedIssueReads) ReadDispatchInputs(context.Context, string, string, []string, string) (control.DispatchInputs, error) {
	r.dispatch++
	return control.DispatchInputs{}, errors.New("closed issue reached dispatch inputs")
}

func (m progressMergeObserver) LookupOp(_ context.Context, id string) (factory.OperationOutcome, error) {
	return m.lookup(id)
}

func (m progressMergeObserver) CancelOp(ctx context.Context, id string) (factory.OperationOutcome, error) {
	if m.cancel != nil {
		return m.cancel(id)
	}
	return m.NativeMerges.CancelOp(ctx, id)
}

func (g progressGraphObserver) GraphActor(context.Context, int64, int64) (int64, error) {
	return 1, nil
}
func (g progressGraphObserver) CheckIntentOwner(ctx context.Context, _, _, _ int64) (int64, error) {
	return g.observe(ctx)
}

// A real native application and private HTTP admission use declared provider
// replies only. No coordinator Start, execution, provider or model is wired.
func progressRepositoryReadCaptureFixture(ctx context.Context, config factoryruntime.NativeConfig) (result error) {
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	connection := factoryruntime.NativeConnection{Connection: strings.Repeat("a", 32), Provider: "github", Origin: "https://github.com", Account: "321"}
	observed := collaboration.ConnectionObservation{Profile: collaboration.Profile{ID: config.Profile, Name: "Declared read capture"}, Provider: collaboration.GitHub, APIBase: "https://api.github.com", Connection: collaboration.ConnectionID(connection.Connection), Account: connection.Account, Login: "declared"}
	mapping, err := db.ApplyCollaborationObservation(ctx, collaboration.ConnectionObservationBatch{Connection: observed, Repositories: []collaboration.ObservedRepository{{ExternalID: "123", Name: "fixture/read-capture"}}})
	if err != nil || len(mapping.Repositories) != 1 {
		return fmt.Errorf("read-capture canonical fixture: %w", err)
	}
	repository, operation := mapping.Repositories[0].ID, strings.Repeat("b", 32)
	// Declared retired history exercises the real run-status HTTP composition;
	// no worker, credential acquisition or run observation is fabricated as live.
	workspace, runID := "p"+factory.NewID()[:24], factory.NewID()
	if err = db.PutCollaborationRuntime(ctx, collaboration.RuntimeRef{ID: collaboration.RuntimeID(config.Profile), Profile: config.Profile, Kind: collaboration.Native, Name: "Declared read capture"}); err != nil {
		return err
	}
	seed, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer seed.Close()
	if _, err = seed.ExecContext(ctx, `INSERT INTO factory_native_workspaces(id,repository,runtime,profile) VALUES($1,$2,$3,$4)`, workspace, repository, string(config.Profile), string(config.Profile)); err != nil {
		return err
	}
	run := factory.Run{ID: runID, ProjectID: workspace, Role: project.RoleCoder, InputSHA: strings.Repeat("c", 40), Started: time.Now().Add(-time.Minute), Deadline: time.Now().Add(time.Minute), Harness: "muse-fixture-1", Model: "controlled-no-model", Outcome: factory.Succeeded, Reconciled: true, Native: &execution.NativeRecord{Tool: execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/muse"}, Retirement: execution.Retired, Sealed: true, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: config.Profile, InstanceID: factory.NewID(), WorktreeID: workspace, Custody: execution.NativeCustody{State: execution.NotAcquired}}}}
	if err = run.Validate(); err != nil {
		return err
	}
	encodedRun, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = seed.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, runID, string(encodedRun)); err != nil {
		return err
	}
	listener, err := net.Listen("unix", filepath.Join(config.Directory, "host.sock"))
	if err != nil {
		return err
	}
	if err = os.Chmod(listener.Addr().String(), 0600); err != nil {
		listener.Close()
		return err
	}
	var calls atomic.Int64
	var identities atomic.Int64
	var denyAt atomic.Int64
	var denied atomic.Int64
	var inventoryMode atomic.Bool
	var inventoryLists atomic.Int64
	var inventoryShift atomic.Bool
	var inventoryMissing atomic.Bool
	var inventoryEdited atomic.Bool
	var materialMode atomic.Bool
	var materialVersion atomic.Int64
	var materialReads atomic.Int64
	var closingReadiness atomic.Int64
	var nonAdmin atomic.Bool
	var feedback progressFeedbackProvider
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command struct {
			Operation                       string
			Connection, Account, Repository string
			Call                            struct {
				Kind    string
				Version uint8
				Query   struct {
					Kind, Repository    string
					Page, Limit, Number uint32
					Cursor              string
				}
				CommandID string `json:"command_id"`
				Digest    string
				Comment   struct {
					SubjectID string `json:"subject_id"`
					Number    uint32
					Body      string
				}
			}
		}
		if r.Header.Get("Authorization") != "Bearer "+hex.EncodeToString(config.IPCKey()) || !factory.ValidID(r.Header.Get("X-Soda-Native-Instance")) || json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&command) != nil {
			w.WriteHeader(503)
			return
		}
		foreground := r.URL.Path == "/api/native/collaboration" && command.Operation == operation
		background := materialMode.Load() && r.URL.Path == "/api/native/collaboration/background" && command.Connection == connection.Connection && command.Account == "321" && command.Repository == "123"
		if !foreground && !background {
			w.WriteHeader(503)
			return
		}
		var observation any
		account := collaboration.ObservedActor{ExternalID: "321", Login: "declared"}
		switch {
		case command.Call.Kind == "current":
			if feedback.on.Load() {
				feedback.currents.Add(1)
			}
		case command.Call.Kind == "read" && command.Call.Query.Kind == "identity":
			identities.Add(1)
			observation = map[string]any{"kind": "identity", "account": account}
		case command.Call.Kind == "read" && command.Call.Query.Kind == "repository" && command.Call.Query.Repository == "123":
			if calls.Add(1) == denyAt.Load() {
				denied.Add(1)
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]string{"error": "github_not_visible"})
				return
			}
			observation = map[string]any{"kind": "repository", "account": account, "repository": map[string]any{"external_id": "123", "name": "fixture/read-capture", "private": true, "default_branch": "main", "push": true, "admin": !nonAdmin.Load(), "merge_methods": map[string]bool{"merge": true, "squash": true, "rebase": true}}}
		case background && feedback.on.Load() && command.Call.Kind == "read" && command.Call.Query.Kind == "issue" && command.Call.Query.Repository == "123" && command.Call.Query.Number == 12:
			feedback.reads.Add(1)
			issue := map[string]any{"external_id": "1312", "number": 12, "title": "Declared objective 12", "body": "Declared body", "state": "open", "locked": false, "author": account, "assignees": []any{}, "labels": []any{}, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
			observation = map[string]any{"version": 1, "kind": "issue", "account": account, "payload": map[string]any{"repository": "123", "name": "fixture/read-capture", "items": []any{issue}, "next_page": 0}}
		case background && feedback.on.Load() && command.Call.Kind == "create_comment" && command.Call.Comment.SubjectID == "1312" && command.Call.Comment.Number == 12:
			feedback.writes.Add(1)
			reply := map[string]any{"command_id": command.Call.CommandID, "digest": command.Call.Digest, "state": "not_sent", "receipt": nil, "failure": map[string]any{"status": 403, "code": "github_forbidden", "retry_after": nil, "reset": nil}}
			if !feedback.refuse.Load() {
				sum := sha256.Sum256([]byte(command.Call.Comment.Body))
				reply["state"], reply["failure"] = "created", nil
				reply["receipt"] = map[string]any{"account": account, "repository": "123", "subject": "issue", "subject_id": "1312", "subject_node_id": "I_declared12", "number": 12,
					"comment":           map[string]any{"external_id": strconv.FormatInt(9000+feedback.writes.Load(), 10), "node_id": "IC_declared", "author_node_id": "U_declared", "author": account, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z", "minimized_reason": nil, "body_sha256": hex.EncodeToString(sum[:]), "url": "https://github.com/fixture/read-capture/issues/12#issuecomment-9001", "issue_url": "https://github.com/fixture/read-capture/issues/12"},
					"provider_response": map[string]any{"protocol": "github_graphql_add_comment", "status": 200, "request_id": nil, "response_sha256": strings.Repeat("e", 64)}}
			}
			observation = reply
		case background && command.Call.Kind == "read" && command.Call.Query.Repository == "123":
			kind := command.Call.Query.Kind
			if kind == "merge_readiness" {
				closingReadiness.Add(1)
				w.WriteHeader(409)
				json.NewEncoder(w).Encode(map[string]string{"error": "declared_closing_permission_withdrawn"})
				return
			}
			materialReads.Add(1)
			payload := map[string]any{"repository": "123", "name": "fixture/read-capture", "items": []any{}, "next_page": 0}
			field := ""
			pull := map[string]any{"kind": "pull", "external_id": "700", "number": 7}
			switch kind {
			case "completion_issues":
				field = "inputs"
				if !inventoryMode.Load() {
					w.WriteHeader(503)
					return
				}
				if inventoryMissing.Load() {
					w.WriteHeader(404)
					json.NewEncoder(w).Encode(map[string]string{"error": "github_not_visible"})
					return
				}
				inventoryLists.Add(1)
				first := 1
				if command.Call.Query.Cursor == "after-50" {
					first = 51
				} else if command.Call.Query.Cursor != "" {
					w.WriteHeader(503)
					return
				}
				inputs := []any{}
				for n := first; n < first+50 && n <= 60; n++ {
					inputs = append(inputs, map[string]any{"issue": map[string]any{"id": strconv.Itoa(1300 + n), "number": n, "title": fmt.Sprintf("Declared objective %d", n), "body": "Declared body", "version": 0, "title_occurrence": "original", "lifecycle_occurrence": "original", "author": account, "created_at": "2026-01-01T00:00:00Z", "closed": false, "locked": false}, "comments": []any{}, "dependencies": []any{}, "tip": strings.Repeat("b", 40), "tip_ref": "refs/heads/main"})
				}
				next := "after-50"
				if first == 51 {
					next = ""
				}
				payload = map[string]any{"repository": "123", "target_ref": "refs/heads/main", "tip": strings.Repeat("b", 40), "items": inputs, "next_cursor": next, "complete": next == ""}
			case "issues":
				field = "issues"
				if !inventoryMode.Load() {
					w.WriteHeader(503)
					return
				}
				inventoryLists.Add(1)
				list := []any{}
				start := int(command.Call.Query.Page-1)*50 + 1
				if inventoryShift.Load() {
					start++
				}
				for n := start; n < start+50 && n <= 60; n++ {
					list = append(list, map[string]any{"external_id": strconv.Itoa(1300 + n), "number": n, "title": fmt.Sprintf("Declared objective %d", n), "body": "Declared body", "state": "open", "locked": false, "author": account, "assignees": []any{}, "labels": []any{}, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"})
				}
				payload["items"] = list
				if start+50 <= 60 {
					payload["next_page"] = command.Call.Query.Page + 1
				}
			case "issue_creator_permission":
				field = "permission"
				payload = map[string]any{"repository": "123", "issue_id": strconv.Itoa(1300 + int(command.Call.Query.Number)), "number": command.Call.Query.Number, "creator": "321", "write": true}
			case "automation_inputs":
				field = "inputs"
				payload = map[string]any{"issue": map[string]any{"id": "1300", "number": 12, "title": "Declared objective", "body": "Declared body", "version": 0, "title_occurrence": "original", "lifecycle_occurrence": "original", "author": account, "created_at": "2026-01-01T00:00:00Z", "closed": false, "locked": false}, "comments": []any{}, "dependencies": []any{}, "tip": strings.Repeat("b", 40), "tip_ref": "refs/heads/main"}
				if inventoryMode.Load() {
					if inventoryMissing.Load() {
						w.WriteHeader(404)
						json.NewEncoder(w).Encode(map[string]string{"error": "github_not_visible"})
						return
					}
					i := payload["issue"].(map[string]any)
					i["id"] = strconv.Itoa(1300 + int(command.Call.Query.Number))
					i["number"] = command.Call.Query.Number
					i["title"] = fmt.Sprintf("Declared objective %d", command.Call.Query.Number)
					if inventoryEdited.Load() {
						i["title"] = "Concurrent human edit"
					}
				}
			case "pull":
				field = "pulls"
				branch := func(name, commit string) any {
					return map[string]any{"name": name, "commit": strings.Repeat(commit, 40), "repository": map[string]any{"external_id": "123", "name": "fixture/read-capture"}}
				}
				payload["items"] = []any{map[string]any{"external_id": "700", "number": 7, "title": "Declared pull", "body": "Declared body", "state": "open", "author": account, "assignees": []any{}, "draft": false, "locked": false, "base": branch("main", "b"), "head": branch("topic", "a")}}
			case "delivery_discussion":
				field = "delivery_discussion"
				branch := func(name, commit string) any {
					return map[string]any{"name": name, "commit": strings.Repeat(commit, 40), "repository": map[string]any{"external_id": "123", "name": "fixture/read-capture"}}
				}
				pages := []any{}
				for _, kind := range []string{"pull", "pull_comments", "pull_reviews", "pull_threads"} {
					q := map[string]any{"kind": kind, "repository": "123", "number": 7}
					data := map[string]any{"repository": "123", "name": "fixture/read-capture", "items": []any{}, "next_page": 0}
					if kind == "pull" {
						data["items"] = []any{map[string]any{"external_id": "700", "number": 7, "title": "Declared pull", "body": "Declared body", "state": "open", "author": account, "assignees": []any{}, "draft": false, "locked": false, "base": branch("main", "b"), "head": branch("topic", "a")}}
					} else {
						q["page"], q["limit"] = 1, 50
						if kind == "pull_comments" {
							data["subject"] = pull
						} else {
							data["pull"] = pull
						}
						if kind == "pull_threads" {
							q["limit"], q["thread_id"], data["next_cursor"] = 20, "", ""
						}
					}
					pages = append(pages, map[string]any{"query": q, "payload": data})
				}
				payload = map[string]any{"pages": pages}
			case "pull_comments":
				field, payload["subject"] = "discussion", pull
			case "pull_reviews":
				field, payload["pull"] = "reviews", pull
			case "pull_threads":
				field, payload["pull"] = "threads", pull
			case "delivery_opinions":
				field = "delivery_opinions"
				payload = map[string]any{"repository": "123", "pull_id": "700", "number": 7, "head": strings.Repeat("a", 40), "base": strings.Repeat("b", 40), "items": []any{}}
			default:
				w.WriteHeader(503)
				return
			}
			if command.Call.Version != 1 {
				w.WriteHeader(503)
				return
			}
			switch materialVersion.Load() {
			case 0:
				observation = map[string]any{"kind": kind, "account": account, field: payload}
			case 1:
				observation = map[string]any{"version": 1, "kind": kind, "account": account, "payload": payload}
			case 2:
				observation = map[string]any{"version": 2, "kind": kind, "account": account, "payload": payload}
			case 3:
				observation = map[string]any{"version": 1, "kind": kind, "account": account, "payload": payload, field: payload}
			}
		default:
			w.WriteHeader(503)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"connection": connection, "repository": "123", "observation": observation})
	})}
	joined := make(chan struct{})
	go func() { defer close(joined); _ = host.Serve(listener) }()
	defer func() { _ = host.Close(); <-joined }()
	lifetime, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- factoryruntime.RunNativeWithBinding(lifetime, filepath.Join(config.Directory, "backend.json"), nil, func(ctx context.Context, application factoryservice.Application) (factoryhttpapi.Product, error) {
			product, err := factoryruntime.BindNativeProduct(ctx, application)
			if err == nil {
				product.Work.Issues.Status = func(context.Context, collaboration.ActorID, collaboration.RepositoryID, int64) (control.AcceptanceValidity, error) {
					return control.AcceptanceValidity{}, nil
				}
				for _, scope := range [][2]int64{{int64(product.Actor) + 1, int64(repository)}, {int64(product.Actor), int64(repository) + 1}} {
					if _, scopeErr := product.Repositories.Status.Bindings(ctx, collaboration.ActorID(scope[0]), collaboration.RepositoryID(scope[1])); scopeErr == nil {
						return factoryhttpapi.Product{}, errors.New("read capture expanded policy binding scope")
					}
					if _, scopeErr := product.Repositories.Access.Viewer(ctx, collaboration.ActorID(scope[0]), collaboration.RepositoryID(scope[1])); scopeErr == nil {
						return factoryhttpapi.Product{}, errors.New("read capture expanded viewer scope")
					}
				}
			}
			return product, err
		})
	}()
	defer func() { cancel(); result = errors.Join(result, <-done) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", config.Socket)
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	instance := ""
	request := func(method, path string, payload []byte) (int, map[string]any, error) {
		request, err := http.NewRequestWithContext(ctx, method, "http://soda-native"+path, bytes.NewReader(payload))
		if err != nil {
			return 0, nil, err
		}
		request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
		request.Header.Set("X-Soda-Native-Instance", instance)
		request.Header.Set("X-Soda-Native-Operation", operation)
		response, err := (&http.Client{Transport: transport}).Do(request)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		var body map[string]any
		err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body)
		return response.StatusCode, body, err
	}
	read := func(path string) (int, map[string]any, error) { return request(http.MethodGet, path, nil) }
	for instance == "" {
		status, body, err := read("/healthz")
		if err == nil && status == 200 {
			instance, _ = body["instance"].(string)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if instance == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, suffix := range []string{"", "/access"} {
		for _, withdrawn := range []bool{false, true} {
			calls.Store(0)
			identities.Store(0)
			denyAt.Store(0)
			if withdrawn {
				denyAt.Store(2)
			}
			status, _, err := read(fmt.Sprintf("/api/repositories/%d/factory%s", repository, suffix))
			if err != nil || calls.Load() != 2 || identities.Load() != 0 || !withdrawn && status != 200 || withdrawn && status == 200 {
				return fmt.Errorf("request-owned repository capture suffix=%q withdrawn=%t calls=%d status=%d: %w", suffix, withdrawn, calls.Load(), status, err)
			}
		}
	}
	for _, withdrawn := range []bool{false, true} {
		calls.Store(0)
		identities.Store(0)
		denyAt.Store(0)
		if withdrawn {
			denyAt.Store(2)
		}
		status, _, err := read(fmt.Sprintf("/api/repositories/%d/factory/issues/12", repository))
		if err != nil || calls.Load() != 2 || identities.Load() != 0 || !withdrawn && status != 200 || withdrawn && status == 200 {
			return fmt.Errorf("request-owned Work capture withdrawn=%t calls=%d identities=%d status=%d: %w", withdrawn, calls.Load(), identities.Load(), status, err)
		}
	}
	for _, withdrawn := range []bool{false, true} {
		calls.Store(0)
		identities.Store(0)
		denyAt.Store(0)
		if withdrawn {
			denyAt.Store(2)
		}
		status, body, e := read("/api/factory/runs/" + runID)
		if e != nil || calls.Load() != 2 || identities.Load() != 0 || (!withdrawn && status != 200) || (withdrawn && status == 200) {
			return fmt.Errorf("request-owned run capture withdrawn=%t calls=%d status=%d body=%v: %w", withdrawn, calls.Load(), status, body, e)
		}
	}
	for _, withdrawn := range []bool{false, true} {
		calls.Store(0)
		denied.Store(0)
		denyAt.Store(0)
		if withdrawn {
			denyAt.Store(3)
		}
		status, body, err := read(fmt.Sprintf("/api/factory/feed?repositories=%d&limit=20&scope=project", repository))
		holds, explicit := body["allowance_holds"].([]any)
		entries, page := body["entries"].([]any)
		if err != nil || status != 200 || calls.Load() != 3 || !explicit || len(holds) != 0 || !page || len(entries) != 0 || withdrawn && denied.Load() != 1 || !withdrawn && denied.Load() != 0 {
			return fmt.Errorf("request-owned Feed capture withdrawn=%t calls=%d denied=%d status=%d explicit=%t page=%t: %w", withdrawn, calls.Load(), denied.Load(), status, explicit, page, err)
		}
	}
	// The retained project list rides the binding's one selection read for one
	// or several opened repositories, and refuses whole when that read fails.
	ask, reply := factory.NewID(), factory.NewID()
	post := func(id, parent, kind, title string) error {
		data, _ := json.Marshal(map[string]any{"parent_id": parent, "actor": "builder", "at": 1767225600, "communication": map[string]any{"kind": kind, "title": title, "parent_id": parent}})
		_, err := seed.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data) VALUES($1,$2,0,'worker:fixture',$3,$1,$4)`, id, repository, title, string(data))
		return err
	}
	for _, phase := range []struct {
		added          []collaboration.ObservedRepository
		latest, title  string
		needs, entries int
	}{{nil, ask, "Declared question", 1, 1}, {[]collaboration.ObservedRepository{{ExternalID: "124", Name: "fixture/second"}, {ExternalID: "125", Name: "fixture/third"}}, reply, "Declared answer", 0, 3}} {
		if phase.added == nil {
			err = post(ask, "", "ask", phase.title)
		} else if _, err = db.ApplyCollaborationObservation(ctx, collaboration.ConnectionObservationBatch{Connection: observed, Repositories: phase.added}); err == nil {
			err = post(reply, ask, "answer", phase.title)
		}
		if err != nil {
			return fmt.Errorf("project list fixture: %w", err)
		}
		for _, withdrawn := range []bool{false, true} {
			calls.Store(0)
			identities.Store(0)
			denyAt.Store(0)
			if withdrawn {
				denyAt.Store(1)
			}
			status, body, err := read("/api/factory/projects")
			rows, listed := body["projects"].([]any)
			valid := err == nil && calls.Load() == 1 && identities.Load() == 0
			if withdrawn {
				valid = valid && status != 200 && !listed
			} else if valid = valid && status == 200 && listed && len(rows) == phase.entries; valid {
				first, _ := rows[0].(map[string]any)
				latest, _ := first["latest_post"].(map[string]any)
				valid = first["repository"] == strconv.FormatInt(int64(repository), 10) && first["external_id"] == "123" && first["needs_you"] == float64(phase.needs) && latest["id"] == phase.latest && latest["title"] == phase.title && latest["actor"] == "builder"
				for i, row := range rows[1:] {
					other, _ := row.(map[string]any)
					valid = valid && other["external_id"] == phase.added[i].ExternalID && other["dispatch"] == "none" && other["needs_you"] == float64(0) && other["latest_post"] == nil && other["connection"] == connection.Connection
				}
			}
			if !valid {
				return fmt.Errorf("project list repositories=%d withdrawn=%t calls=%d identities=%d status=%d body=%v: %v", phase.entries, withdrawn, calls.Load(), identities.Load(), status, body, err)
			}
		}
	}
	calls.Store(0)
	denyAt.Store(0)
	status, _, err := read(fmt.Sprintf("/api/repositories/%d/factory", repository+1))
	if err != nil || status != 404 || calls.Load() != 1 {
		return fmt.Errorf("read capture expanded canonical repository: status=%d calls=%d: %w", status, calls.Load(), err)
	}
	calls.Store(0)
	denyAt.Store(2)
	status, _, err = request(http.MethodPut, fmt.Sprintf("/api/repositories/%d/factory/operator-grant", repository), []byte(`{"expected_revision":0,"active":true}`))
	if err != nil || status == 200 || calls.Load() != 2 {
		return fmt.Errorf("writer reused read-only selection after withdrawal: status=%d calls=%d error=%v", status, calls.Load(), err)
	}
	if _, err = db.OperatorGrant(ctx, int64(repository)); !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("withdrawn writer recorded an operator grant: %v", err)
	}
	denyAt.Store(0)
	// The real material reader must finish both complete traversals before
	// requesting one closing paired CI/protection observation. Its declared
	// closing permission refusal exposes no material and cannot call a model.
	actor := int64(mapping.Binding.Actor.ID)
	ref := func(kind string) factory.ActorBindingRef {
		return factory.ActorBindingRef{Kind: kind, ActorID: actor, NativeProfile: config.Profile, NativeConnection: collaboration.ConnectionID(connection.Connection)}
	}
	policy := factory.RepositoryPolicy{Repository: int64(repository), Revision: 1, Publish: ref(factory.OpRefPublish), Merge: ref(factory.OpMerge)}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	raw, err := sql.Open("pgx", config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer raw.Close()
	if _, err = raw.ExecContext(ctx, `INSERT INTO factory_policies(repository,revision,data) VALUES($1,1,$2)`, repository, string(encoded)); err != nil {
		return err
	}
	calls.Store(0)
	identities.Store(0)
	materialMode.Store(true)
	defer materialMode.Store(false)
	native := factoryruntime.NativeMerges{NativeInputSource: factoryruntime.NativeInputSource{NativeExecution: factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}}}
	w := factory.MergeWork{Method: "merge", Repository: int64(repository), Issue: 12, PRNumber: 7, PRID: 700, IssueID: 1300, ActorID: actor, PRAuthorID: actor, ReviewerID: actor, ReviewEvent: "COMMENT", HeadRef: "refs/heads/topic", BaseRef: "refs/heads/main", HeadOID: strings.Repeat("a", 40), BaseOID: strings.Repeat("b", 40)}
	for _, version := range []int64{0, 1, 2, 3} {
		materialVersion.Store(version)
		calls.Store(0)
		identities.Store(0)
		materialReads.Store(0)
		closingReadiness.Store(0)
		material, readErr := native.ReadDeliveryMaterial(ctx, w)
		wantReads, wantClosing := int64(2), int64(1)
		if version == 0 {
			wantClosing = 0
		}
		if version >= 2 {
			wantReads, wantClosing = 1, 0
		}
		if readErr == nil || !reflect.DeepEqual(material, factory.DeliveryNativeMaterial{}) || calls.Load() != 1 || identities.Load() != 0 || materialReads.Load() != wantReads || closingReadiness.Load() != wantClosing {
			return fmt.Errorf("material envelope mode=%d calls=%d identities=%d reads=%d closing=%d: %v", version, calls.Load(), identities.Load(), materialReads.Load(), closingReadiness.Load(), readErr)
		}
	}
	materialVersion.Store(1)
	inventoryMode.Store(true)
	policy.Enabled = true
	policy.TargetBranch = "refs/heads/main"
	encoded, _ = json.Marshal(policy)
	if _, err = raw.ExecContext(ctx, "UPDATE factory_policies SET data=$2 WHERE repository=$1", repository, string(encoded)); err != nil {
		return err
	}
	if err = progressNativeInventoryFixture(ctx, db, native.NativeInputSource, int64(repository), policy.Revision, &inventoryLists, &inventoryShift, &inventoryMissing, &inventoryEdited); err != nil {
		return err
	}
	inventoryMode.Store(false)
	if err = progressActivityRepairCapture(ctx, db, raw, request, repository, mapping.Binding.Actor.ID, &calls, &denyAt, &nonAdmin); err != nil {
		return err
	}
	feedback.on.Store(true)
	err = progressPostFeedbackCapture(ctx, db, factoryruntime.NewNativeIssueEffects(native.NativeInputSource), config.Profile, connection.Connection, repository, mapping.Binding.Actor.ID, &feedback)
	feedback.on.Store(false)
	if err != nil {
		return err
	}
	fmt.Println("PASS: actual Go native HTTP/PG activity and repair-funding reads each use two repository reads (selection and closing) and final withdrawal refuses; activity links follow the provider path shape. An administrator's activity save uses three (selection, fresh administration, closing) and a repair save two (selection, fresh pre-write administration); a non-administrator is refused 403 after two and one reads with nothing recorded. Provider replies declared.")
	fmt.Println("PASS: real Go material reader accepts version1 compound pages, refuses a compound kind absent from the frozen prior contract, rejects unknown versions/mixed payloads at the first input read, and preserves closing withdrawal refusal with no provider/model/write.")
	fmt.Println("PASS: production material reader projects complete native-paired discussion pages through existing decoders, then exactly one closing paired CI/protection/current-opinion read; closing permission withdrawal exposes no material. Three declared native reads, no provider/model/write.")
	fmt.Println("PASS: actual Go native HTTP/PG project list for one and three opened repositories uses exactly one repository read (the binding's selection) and no identity read, and each row carries its provider repository id; an open ask counts as needs-you until a reply arrives; a failed selection read refuses the whole list. Provider replies declared.")
	fmt.Println("PASS: actual Go native HTTP/PG status/access, Work and retired run status each use exactly two repository reads (selection and closing visibility); inner/initial checks add none, final withdrawal refuses, and foreign actor/repository scope cannot use the capture. Empty Feed retains three reads and final withdrawal exposes no advisory. Provider/acceptance replies declared; no coordinator Start, provider, model or execution.")
	return nil
}

type progressFeedbackProvider struct {
	on, refuse              atomic.Bool
	reads, writes, currents atomic.Int64
}

type declaredExistingWork struct{}

func (declaredExistingWork) JudgePost(context.Context, factoryservice.PostJudgeInput) (factoryservice.PostJudgment, error) {
	return factoryservice.PostJudgment{Route: factory.PostRouteExistingWork, Issue: 12}, nil
}

// progressPostFeedbackCapture drives existing-work posts from a declared JEV
// judgment through the composed post pipeline and the native captured-author
// comment writer, counting declared provider issue reads and comment writes.
func progressPostFeedbackCapture(ctx context.Context, db *store.Store, effects factoryservice.IssueMakerEffects, profile collaboration.ProfileID, connection string, repository collaboration.RepositoryID, actor collaboration.ActorID, provider *progressFeedbackProvider) error {
	wired := factoryservice.Application{Store: db, IssueEffects: effects}.Background().Collaborators()
	if wired.Feedback == nil {
		return errors.New("native application did not bind existing-work feedback")
	}
	wired.Judge = declaredExistingWork{}
	submit := func(key string) (string, error) {
		id, revision, err := db.SaveFeedPost(ctx, factory.PostRequest{Repository: int64(repository), Issue: 12, Body: "The login redirect still drops the return path (" + key + ").", IdempotencyKey: key}, strconv.FormatInt(int64(actor), 10), "declared", time.Now().Unix())
		if err == nil {
			err = db.RecordIssueMakerIntake(ctx, id, revision)
		}
		if err != nil {
			return "", err
		}
		detail, err := db.FeedPost(ctx, id)
		if err != nil {
			return "", err
		}
		// Declared in place of native HTTP capture: the same proof shape the
		// host issues when the author saves the post.
		scope := factory.IssueIntakeScope{Source: id, Revision: revision, Actor: actor, Repository: repository, Digest: factory.MCPInputDigest([]byte(detail.Body)), Expires: time.Now().Add(5 * time.Minute).Unix()}
		proof, err := json.Marshal(map[string]any{"version": 1, "profile": profile, "connection": connection, "account": "321", "repository": "123", "scope": scope})
		if err != nil {
			return "", err
		}
		return id, db.SaveIssueIntakeGrant(ctx, id, revision, scope, proof)
	}
	drive := func(name, id string, reads, writes int64, effect string, attempts int) error {
		provider.reads.Store(0)
		provider.writes.Store(0)
		response, err := factoryservice.DrivePostIntake(ctx, db, wired, id)
		if err != nil || provider.reads.Load() != reads || provider.writes.Load() != writes || response.Route != factory.PostRouteExistingWork || response.ExistingIssue != 12 || response.Effect != effect || response.Attempts != attempts {
			return fmt.Errorf("%s: reads=%d writes=%d want %d/%d response=%+v: %v", name, provider.reads.Load(), provider.writes.Load(), reads, writes, response, err)
		}
		return nil
	}
	delivered, err := submit("feedback-delivered")
	if err != nil {
		return err
	}
	provider.currents.Store(0)
	if err = drive("existing-work delivery", delivered, 1, 1, factory.PostEffectApplied, 0); err != nil {
		return err
	}
	currents := provider.currents.Load()
	if err = drive("settled replay", delivered, 0, 0, factory.PostEffectApplied, 0); err != nil {
		return err
	}
	// A pipeline advance lost after the send repeats delivery: it reconciles
	// the original command and neither prepares nor sends again.
	detail, err := db.FeedPost(ctx, delivered)
	if err != nil {
		return err
	}
	if err = wired.Feedback.DeliverPostFeedback(ctx, detail, 12); err != nil || provider.reads.Load() != 0 || provider.writes.Load() != 0 {
		return fmt.Errorf("repeated delivery reads=%d writes=%d: %v", provider.reads.Load(), provider.writes.Load(), err)
	}
	provider.refuse.Store(true)
	defer provider.refuse.Store(false)
	refused, err := submit("feedback-refused")
	if err != nil {
		return err
	}
	if err = drive("refused delivery", refused, 1, 1, factory.PostEffectPending, factory.PostAttemptLimit); err != nil {
		return err
	}
	if err = drive("refused replay", refused, 0, 0, factory.PostEffectPending, factory.PostAttemptLimit); err != nil {
		return err
	}
	if detail, err = db.FeedPost(ctx, refused); err != nil {
		return err
	}
	if err = wired.Feedback.DeliverPostFeedback(ctx, detail, 12); !errors.Is(err, factoryservice.ErrPostFeedbackRefused) || provider.reads.Load() != 0 || provider.writes.Load() != 0 {
		return fmt.Errorf("repeated refused delivery reads=%d writes=%d: %v", provider.reads.Load(), provider.writes.Load(), err)
	}
	pipe, err := db.PostIntake(ctx, refused)
	if err != nil || !pipe.RetryAllowed() {
		return fmt.Errorf("refused feedback is not author-retryable: %+v: %v", pipe, err)
	}
	queued, err := db.PendingPostIntakes(ctx, 64)
	if err != nil {
		return err
	}
	for _, pending := range queued {
		if pending.SourcePost == refused {
			return errors.New("exhausted feedback stayed in the service queue")
		}
	}
	fmt.Printf("PASS: existing-work post goes from a declared JEV judgment to the author's own issue comment through the composed native post pipeline with one issue read and one comment write (plus %d native current-connection checks); settled and lost-advance replays make no provider call; a refused write is sent once, settles the request author-retryable (attempts %d/%d) at once, and its replays make no provider call. Provider replies declared.\n", currents, factory.PostAttemptLimit, factory.PostAttemptLimit)
	return nil
}

// progressActivityRepairCapture drives the native activity and repair-funding
// routes against the capture's declared provider, counting repository reads.
func progressActivityRepairCapture(ctx context.Context, db *store.Store, raw *sql.DB, request func(string, string, []byte) (int, map[string]any, error), repository collaboration.RepositoryID, actor collaboration.ActorID, calls, denyAt *atomic.Int64, nonAdmin *atomic.Bool) error {
	target := factory.CheckTarget{Repository: int64(repository), PRNumber: 3, PRID: 11, IssueID: 13, HeadRef: "refs/heads/factory/check", BaseRef: "refs/heads/main", HeadOID: strings.Repeat("a", 40), BaseOID: strings.Repeat("b", 40)}
	published := declaredPublishedFixture(target, actor, factory.NewID(), 12, 1, time.Now().Add(time.Minute).Unix())
	encoded, err := json.Marshal(published)
	if err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,12,$3,'published',0,$4)`, published.AssignmentID, repository, published.Run, string(encoded)); err != nil {
		return err
	}
	base := fmt.Sprintf("/api/repositories/%d/factory", repository)
	call := func(name, method, path, payload string, admin bool, deny, want, wantStatus int64) (map[string]any, error) {
		calls.Store(0)
		denyAt.Store(deny)
		nonAdmin.Store(!admin)
		defer denyAt.Store(0)
		defer nonAdmin.Store(false)
		var body []byte
		if payload != "" {
			body = []byte(payload)
		}
		status, out, err := request(method, base+path, body)
		if err != nil || calls.Load() != want || int64(status) != wantStatus {
			return nil, fmt.Errorf("%s: status=%d calls=%d want status=%d calls=%d body=%v: %v", name, status, calls.Load(), wantStatus, want, out, err)
		}
		if wantStatus == 403 {
			if failure, _ := out["error"].(map[string]any); failure["code"] != "repository_admin_required" {
				return nil, fmt.Errorf("%s refusal body=%v", name, out)
			}
		}
		return out, nil
	}
	activity, err := call("activity read", http.MethodGet, "/activity", "", true, 0, 2, 200)
	if err != nil {
		return err
	}
	settings, _ := activity["settings"].(map[string]any)
	items, _ := activity["items"].([]any)
	if activity["repository"] != strconv.FormatInt(int64(repository), 10) || activity["repository_path"] != "/fixture/read-capture" || activity["revision"] != float64(0) ||
		settings["issue_created"] != true || settings["pr_created"] != true || settings["verified_merge"] != true || len(items) != 1 {
		return fmt.Errorf("activity read shape: %v", activity)
	}
	if item, _ := items[0].(map[string]any); item["kind"] != "pr_created" || item["issue"] != "12" || item["pr"] != "3" || item["url"] != "/fixture/read-capture/pull/3" {
		return fmt.Errorf("activity item shape: %v", item)
	}
	if _, err = call("withdrawn activity read", http.MethodGet, "/activity", "", true, 2, 2, 404); err != nil {
		return err
	}
	save := `{"expected_revision":0,"settings":{"issue_created":true,"pr_created":false,"verified_merge":true}}`
	if _, err = call("non-administrator activity save", http.MethodPut, "/activity", save, false, 0, 2, 403); err != nil {
		return err
	}
	if _, revision, err := db.ActivitySettings(ctx, int64(repository)); err != nil || revision != 0 {
		return fmt.Errorf("refused activity save recorded revision=%d: %v", revision, err)
	}
	if activity, err = call("administrator activity save", http.MethodPut, "/activity", save, true, 0, 3, 200); err != nil {
		return err
	}
	if items, _ = activity["items"].([]any); activity["revision"] != float64(1) || len(items) != 0 {
		return fmt.Errorf("activity save readback: %v", activity)
	}
	if _, err = call("non-administrator repair read", http.MethodGet, "/repair", "", false, 0, 1, 403); err != nil {
		return err
	}
	repair, err := call("repair read", http.MethodGet, "/repair", "", true, 0, 2, 200)
	if err != nil {
		return err
	}
	if repair["revision"] != float64(0) || repair["grant"] != nil {
		return fmt.Errorf("repair read shape: %v", repair)
	}
	if _, err = call("withdrawn repair read", http.MethodGet, "/repair", "", true, 2, 2, 404); err != nil {
		return err
	}
	command := factory.NewID()
	change := fmt.Sprintf(`{"command_id":%q,"expected_revision":0,"investigator_runs":1,"proposal_judgments":1,"active_minutes":5,"effect":"","active":true}`, command)
	if _, err = call("non-administrator repair save", http.MethodPut, "/repair", change, false, 0, 1, 403); err != nil {
		return err
	}
	if _, err = db.RepairGrantCommand(ctx, int64(repository)); !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("refused repair save recorded a grant: %v", err)
	}
	if repair, err = call("administrator repair save", http.MethodPut, "/repair", change, true, 0, 2, 200); err != nil {
		return err
	}
	grant, err := db.RepairGrantCommand(ctx, int64(repository))
	if err != nil || repair["command_id"] != command || repair["revision"] != float64(1) || grant.Sponsor != int64(actor) || grant.ActiveMinutes != 5 {
		return fmt.Errorf("repair save result=%v grant=%+v: %v", repair, grant, err)
	}
	return nil
}

func progressGateFixture() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := factoryruntime.LoadNative(os.Getenv("SODA_NATIVE_FACTORY_CONFIG"))
	if err != nil {
		return err
	}
	if err := progressRepositoryReadCaptureFixture(ctx, config); err != nil {
		return err
	}
	db, dsn, cleanup, err := store.OpenEphemeral(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer cleanup()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer raw.Close()
	if err := progressPagedInventoryFixture(ctx, db, raw); err != nil {
		return err
	}
	target := factory.CheckTarget{Repository: 7, PRNumber: 3, PRID: 11, IssueID: 13, HeadRef: "refs/heads/factory/check", BaseRef: "refs/heads/main", HeadOID: strings.Repeat("a", 40), BaseOID: strings.Repeat("b", 40)}
	owner := offlineRoleAssignment(project.ExecutionImplement, project.RoleCoder, target.BaseOID)
	owner.ID, owner.AttemptRoot, owner.PublicationAssignment = strings.Repeat("b", 32), strings.Repeat("b", 32), strings.Repeat("b", 32)
	owner.Repository, owner.Issue, owner.Subject.Issue.Issue = target.Repository, 12, 12
	if err := owner.Validate(); err != nil {
		return err
	}
	p := declaredPublishedFixture(target, 7, owner.ID, owner.Issue, 1, time.Now().Add(time.Minute).Unix())
	p.Run = owner.Run
	if err := p.Validate(); err != nil {
		return err
	}
	allowance := factory.AttemptAllowance{RootAssignment: owner.ID, Repository: owner.Repository, Issue: owner.Issue, Limits: factory.DefaultAttemptLimits(), CheckpointUnix: time.Now().Unix()}
	if err := allowance.Validate(); err != nil {
		return err
	}
	policy := factory.RepositoryPolicy{Repository: 7, Revision: 1, Checks: []string{"ci/gate"}}
	for _, row := range []struct {
		query string
		value any
	}{
		{`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',7,12,'declared-run','assigned',0,$1)`, owner},
		{`INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES(7,12,'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',0,$1)`, allowance},
		{`INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',7,12,'declared-run','published',0,$1)`, p},
		{`INSERT INTO factory_policies(repository,revision,data) VALUES(7,1,$1)`, policy},
	} {
		encoded, e := json.Marshal(row.value)
		if e != nil {
			return e
		}
		if _, e = raw.ExecContext(ctx, row.query, string(encoded)); e != nil {
			return e
		}
	}
	c := control.NewCoordinator(db, nil, nil)
	entered := make(chan context.Context, 2)
	release := make(chan struct{}, 2)
	c.Checks = progressCheckObserver(func(pass context.Context, target factory.CheckTarget, _ int64) (factory.ObservedChecks, error) {
		// This nested public pass must borrow the internal live owner, not deadlock.
		if nested := c.PublishPass(pass); !nested.Unavailable || len(nested.Errors) != 0 {
			return factory.ObservedChecks{}, errors.New("nested pass lost admission")
		}
		first, settle := control.ReserveProviderSends(pass, 64)
		if first != 64 {
			return factory.ObservedChecks{}, errors.New("first native allowance unavailable")
		}
		settle(5)
		settle(0) // Repeated settlement cannot refund the five attempted sends.
		second, settleSecond := control.ReserveProviderSends(pass, 64)
		if second != 64 {
			return factory.ObservedChecks{}, errors.New("actual native usage did not return unused allocation")
		}
		settleSecond(7)
		lost, settleLost := control.ReserveProviderSends(pass, 64)
		if lost != 64 {
			return factory.ObservedChecks{}, errors.New("lost native call allocation unavailable")
		}
		settleLost(-1) // Missing/invalid usage keeps the entire uncertain allocation.
		remaining, _ := control.ReserveProviderSends(pass, 384)
		exhausted, _ := control.ReserveProviderSends(pass, 1)
		if remaining != 80 || exhausted != 0 {
			return factory.ObservedChecks{}, errors.New("native pass allowance exceeded after actual and uncertain usage")
		}
		entered <- context.WithoutCancel(pass)
		select {
		case <-release:
		case <-pass.Done():
			return factory.ObservedChecks{}, pass.Err()
		}
		return factory.ObservedChecks{NativeRev: 1, ObservedContexts: 1, HeadTip: target.HeadOID, BaseTip: target.BaseOID, Complete: true, Checks: []factory.ObservedCheck{{Context: "ci/gate", State: "pending"}}}, nil
	})
	done := make(chan control.CheckReport, 1)
	go func() { done <- c.CheckPass(ctx) }()
	var retained context.Context
	select {
	case retained = <-entered:
	case <-ctx.Done():
		return errors.New("slow check observation was not reached")
	}
	deadline, expire := context.WithTimeout(ctx, 50*time.Millisecond)
	defer expire()
	waiter := make(chan error, 1)
	go func() {
		_, e := c.RetryCompletion(deadline, strings.Repeat("c", 32), "1", 7, factory.CompletionRetryRequest{EventID: strings.Repeat("d", 32), ExpectedPriorCycle: strings.Repeat("e", 32)})
		waiter <- e
	}()
	select {
	case e := <-waiter:
		if !errors.Is(e, context.DeadlineExceeded) {
			return fmt.Errorf("expired waiter did not return deadline: %v", e)
		}
	case <-time.After(3 * time.Second):
		return errors.New("expired waiter remained behind observation")
	}
	if _, e := db.CheckAssessment(ctx, 7, 3); !errors.Is(e, store.ErrNotFound) {
		return errors.New("blocked observation recorded an assessment")
	}
	var commands int
	if e := raw.QueryRowContext(ctx, `SELECT count(*) FROM factory_commands`).Scan(&commands); e != nil || commands != 0 {
		return errors.New("expired waiter recorded a command")
	}
	expired, expireNow := context.WithCancel(ctx)
	expireNow()
	if e := c.Start(expired, ""); !errors.Is(e, context.Canceled) {
		return errors.New("cancelled Start attempted admission")
	}
	if len(c.Dispatch(expired).Errors) != 1 || len(c.CheckPass(expired).Errors) != 1 || len(c.PublishPass(expired).Errors) != 1 || len(c.MergePass(expired).Errors) != 1 || len(c.DriveCompletions(expired, control.CompletionDriveDeps{}).Held) != 1 {
		return errors.New("cancelled progress caller lost refusal")
	}
	if _, e := c.StartCompletionRetry(expired, factory.ReassessmentRetry{}); !errors.Is(e, context.Canceled) {
		return errors.New("cancelled completion retry attempted work")
	}
	if _, e := c.GenerateAcceptedIntent(expired, "", "", 1); !errors.Is(e, context.Canceled) {
		return errors.New("cancelled graph attempted observation")
	}
	release <- struct{}{}
	first := <-done
	if len(first.Errors) != 0 || len(first.Assessed) != 1 || first.Assessed[0].Revision != 1 {
		return fmt.Errorf("current check pass failed: %+v", first)
	}
	// The same passive tick reuses a recent nonpassing assessment without
	// invoking the native observer. Advance only the fixture's recorded time.
	if passive := c.CheckPass(ctx); len(passive.Errors) != 0 || len(passive.Assessed) != 0 {
		return fmt.Errorf("passive check repeated the recent observation: %+v", passive)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE factory_check_assessments SET data=jsonb_set(data,'{assessed_unix}',to_jsonb($1::bigint)) WHERE repository=7 AND pr=3`, time.Now().Add(-time.Minute).Unix()); err != nil {
		return err
	}
	// A context captured by a completed pass no longer confers ownership.
	go func() { done <- c.CheckPass(ctx) }()
	select {
	case <-entered:
	case <-ctx.Done():
		return errors.New("later valid pass did not reach observation")
	}
	staleCtx, stopStale := context.WithTimeout(retained, 50*time.Millisecond)
	defer stopStale()
	staleDone := make(chan control.PublishReport, 1)
	go func() { staleDone <- c.PublishPass(staleCtx) }()
	select {
	case result := <-staleDone:
		if len(result.Errors) != 1 || result.Errors[0].Reason != "progress_pass_cancelled" {
			return fmt.Errorf("retired pass context bypassed admission: %+v", result)
		}
	case <-time.After(3 * time.Second):
		return errors.New("retired pass context did not cancel")
	}
	release <- struct{}{}
	second := <-done
	if len(second.Errors) != 0 || len(second.Assessed) != 1 || second.Assessed[0].Revision != 2 {
		return fmt.Errorf("later valid request failed: %+v", second)
	}
	// Explicit assessment bypasses passive cadence. Captured policy/head/base
	// changes must also become due immediately, without waiting a wall minute.
	observations := 0
	c.Checks = progressCheckObserver(func(_ context.Context, target factory.CheckTarget, _ int64) (factory.ObservedChecks, error) {
		observations++
		return factory.ObservedChecks{NativeRev: 1, ObservedContexts: 1, HeadTip: target.HeadOID, BaseTip: target.BaseOID, Complete: true, Checks: []factory.ObservedCheck{{Context: "ci/gate", State: "pending"}}}, nil
	})
	if explicit := c.AssessPublicationChecks(ctx, c.Checks, 7, p.AssignmentID); len(explicit.Errors) != 0 || len(explicit.Assessed) != 1 || observations != 1 {
		return fmt.Errorf("explicit assessment was delayed: %+v, observations=%d", explicit, observations)
	}
	policy.Revision++
	encoded, _ := json.Marshal(policy)
	if _, err := raw.ExecContext(ctx, `UPDATE factory_policies SET revision=$1,data=$2 WHERE repository=7`, policy.Revision, string(encoded)); err != nil {
		return err
	}
	if changed := c.CheckPass(ctx); len(changed.Errors) != 0 || len(changed.Assessed) != 1 || observations != 2 {
		return fmt.Errorf("changed policy did not refresh immediately: %+v, observations=%d", changed, observations)
	}
	for _, change := range []string{"head", "base"} {
		if change == "head" {
			p.Candidate = strings.Repeat("c", 40)
		} else {
			p.PRCreate.BaseOID = strings.Repeat("d", 40)
		}
		encoded, _ := json.Marshal(p)
		if _, err := raw.ExecContext(ctx, `UPDATE factory_publications SET data=$1 WHERE assignment=$2`, string(encoded), p.AssignmentID); err != nil {
			return err
		}
		before := observations
		if changed := c.CheckPass(ctx); len(changed.Errors) != 0 || len(changed.Assessed) != 1 || observations != before+1 {
			return fmt.Errorf("changed %s did not refresh immediately: %+v, observations=%d", change, changed, observations)
		}
		if passive := c.CheckPass(ctx); len(passive.Errors) != 0 || len(passive.Assessed) != 0 || observations != before+1 {
			return fmt.Errorf("unchanged %s was polled before due: %+v, observations=%d", change, passive, observations)
		}
	}
	beforeFailure, err := db.CheckAssessment(ctx, p.Repository, p.PRNumber)
	if err != nil {
		return err
	}
	failures := 0
	c.Checks = progressCheckObserver(func(context.Context, factory.CheckTarget, int64) (factory.ObservedChecks, error) {
		failures++
		return factory.ObservedChecks{}, errors.New("declared unreadable native checks")
	})
	for _, change := range []string{"policy", "head", "base"} {
		if change == "policy" {
			policy.Revision++
			encoded, _ := json.Marshal(policy)
			_, err = raw.ExecContext(ctx, `UPDATE factory_policies SET revision=$1,data=$2 WHERE repository=7`, policy.Revision, string(encoded))
		} else {
			if change == "head" {
				p.Candidate = strings.Repeat("e", 40)
			} else {
				p.PRCreate.BaseOID = strings.Repeat("f", 40)
			}
			encoded, _ := json.Marshal(p)
			_, err = raw.ExecContext(ctx, `UPDATE factory_publications SET data=$1 WHERE assignment=$2`, string(encoded), p.AssignmentID)
		}
		if err != nil {
			return err
		}
		before := failures
		if failed := c.CheckPass(ctx); len(failed.Errors) != 1 || len(failed.Assessed) != 0 || failures != before+1 {
			return fmt.Errorf("unreadable changed %s did not observe immediately: %+v failures=%d", change, failed, failures)
		}
		if held := c.CheckPass(ctx); len(held.Errors) != 0 || len(held.Assessed) != 0 || failures != before+1 {
			return fmt.Errorf("unreadable %s repeated passive I/O before due: %+v failures=%d", change, held, failures)
		}
	}
	before := failures
	if explicit := c.AssessPublicationChecks(ctx, c.Checks, 7, p.AssignmentID); len(explicit.Errors) != 1 || failures != before+1 {
		return fmt.Errorf("explicit check did not bypass unreadable passive wait: %+v failures=%d", explicit, failures)
	}
	if stored, err := db.CheckAssessment(ctx, p.Repository, p.PRNumber); err != nil || !reflect.DeepEqual(stored, beforeFailure) {
		return errors.New("unreadable check retry manufactured or renewed stored evidence")
	}
	if err := progressClosedIssueFixture(ctx, db, raw, p, owner, allowance); err != nil {
		return err
	}
	if err := progressMergeAttemptFixture(ctx, db, raw, p, owner, allowance); err != nil {
		return err
	}
	// A slow authority read runs outside admission; a changed accepted head
	// invalidates it before any generation journal or native effect is created.
	idea := factory.ProductIdea{ID: "d" + strings.Repeat("1", 24), Description: "Declared gate check", Consequence: "Check revision admission", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}
	r := factory.IntentRevision{ID: "d" + strings.Repeat("2", 24), Intent: "d" + strings.Repeat("3", 24), Owner: 1, Repository: 7, Objective: "Check graph observation admission", State: "accepted", MaxIssues: 1, CreatedUnix: time.Now().Unix(), Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{{Key: "d" + strings.Repeat("5", 24), Objective: "Check gate", Outcomes: []string{"No stale effect"}, IdeaIDs: []string{idea.ID}}}}
	if e := db.SaveIntentRevision(ctx, r); e != nil {
		return e
	}
	graphEntered := make(chan struct{})
	graphRelease := make(chan struct{})
	c.Graph = progressGraphObserver{observe: func(observeCtx context.Context) (int64, error) {
		close(graphEntered)
		select {
		case <-graphRelease:
			return 1, nil
		case <-observeCtx.Done():
			return 0, observeCtx.Err()
		}
	}}
	graphDone := make(chan error, 1)
	go func() { _, e := c.GenerateAcceptedIntent(ctx, r.Intent, r.ID, r.Owner); graphDone <- e }()
	select {
	case <-graphEntered:
	case <-ctx.Done():
		return errors.New("graph authority read did not start")
	}
	unrelated, stopUnrelated := context.WithTimeout(ctx, time.Second)
	free := c.PublishPass(unrelated)
	stopUnrelated()
	if !free.Unavailable || len(free.Errors) != 0 {
		return errors.New("pure graph observation held progress admission")
	}
	successor := r
	successor.Predecessor = r.ID
	successor.ID = "d" + strings.Repeat("4", 24)
	successor.Objective = "Changed accepted direction"
	if e := db.SaveIntentRevision(ctx, successor); e != nil {
		return e
	}
	close(graphRelease)
	if e := <-graphDone; !errors.Is(e, store.ErrStaleRevision) {
		return fmt.Errorf("changed head did not invalidate outside observation: %v", e)
	}
	if _, e := db.IntentGeneration(ctx, r.ID); !errors.Is(e, store.ErrNotFound) {
		return errors.New("stale observation created a generation")
	}
	fmt.Println("PASS: real PG coordinator holds a slow check observation; expired waiter exits without commands/assessments, live nested passes reuse admission, retired contexts cannot bypass it; passive checks wait one minute on the exact recorded tuple, elapsed/policy/head/base changes refresh immediately and explicit assessment bypasses the wait; graph observation permits independent admission and changed accepted revision refuses before journal/effects. Native observations declared.")
	return nil
}

func progressClosedIssueFixture(ctx context.Context, db *store.Store, raw *sql.DB, p factory.Publication, owner factory.Assignment, allowance factory.AttemptAllowance) error {
	priorPublication := p
	p.Candidate, p.PRCreate.BaseOID = p.Publish.Work.Candidate, p.BaseSHA
	if err := p.Validate(); err != nil {
		return err
	}
	encoded, _ := json.Marshal(p)
	if _, err := raw.ExecContext(ctx, `UPDATE factory_publications SET data=$1 WHERE assignment=$2`, string(encoded), p.AssignmentID); err != nil {
		return err
	}
	acceptance := factory.Acceptance{ID: owner.Acceptance, Repository: p.Repository, IssueIndex: fmt.Sprint(p.Issue), Approver: 7, NativeRev: 1, TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64), ContentVersion: 1, TitleOccurrence: factory.OriginalOccurrence}
	if err := db.AdmitAcceptanceDecision(ctx, acceptance); err != nil {
		return err
	}
	reads := &progressClosedIssueReads{}
	native := factoryruntime.NativeExecution{Store: db}
	c := control.NewCoordinator(db, native, native)
	c.Publication, c.Reviews = factoryruntime.NativePublication{}, factoryruntime.NativeReviews{}
	c.AcceptanceReads, c.DispatchReads = reads, reads
	if _, _, err := c.ObserveIssueEvent(ctx, control.IntakeHint{Delivery: "declared-open-issue", Repository: p.Repository, Issue: p.Issue}); err != nil {
		return err
	}
	// This disposable queued observation has no effective local grants. Neither
	// direct entry nor the combined background pass may fetch provider inputs.
	localHold, err := db.IssueControl(ctx, p.Repository, p.Issue)
	if err != nil {
		return err
	}
	localHold.Readiness, localHold.Reason = factory.ReadinessQueued, factory.ReasonEligible
	localHold.Blockers = nil
	localHold.Fingerprint = factory.MCPInputDigest([]byte("declared-locally-held-publication-child"))
	if _, _, err := db.RecordIssueAssessment(ctx, localHold, time.Now()); err != nil {
		return err
	}
	reads.acceptance, reads.dispatch = 0, 0
	c.PublishPass(ctx)
	c.CheckPass(ctx)
	c.Dispatch(ctx)
	if reads.acceptance != 0 || reads.dispatch != 0 {
		return fmt.Errorf("locally held child fetched native inputs: %+v", reads)
	}
	reads.closed, reads.acceptance = true, 0
	if _, _, err := c.ObserveIssueEvent(ctx, control.IntakeHint{Delivery: "declared-closed-issue", Repository: p.Repository, Issue: p.Issue}); err != nil {
		return err
	}
	first := c.PublishPass(ctx)
	closed, err := db.IssueControl(ctx, p.Repository, p.Issue)
	if err != nil || !closed.HoldsClosedIssue() || reads.acceptance != 1 || reads.dispatch != 0 {
		return fmt.Errorf("first closed child assessment failed: %+v reads=%+v control=%+v error=%v", first, reads, closed, err)
	}
	c.PublishPass(ctx)
	c.CheckPass(ctx)
	if reads.acceptance != 1 || reads.dispatch != 0 {
		return fmt.Errorf("recent closed child repeated native inputs: %+v", reads)
	}
	status, err := c.AcceptanceStatus(ctx, p.Repository, fmt.Sprint(p.Issue))
	if err != nil || !status.Valid {
		return fmt.Errorf("closed issue invalidated accepted prerequisite requirements: %+v %v", status, err)
	}
	reads.acceptance = 0
	c.Readiness = reads // Due closed polling must refresh even at the same native revision.
	m := factory.Merge{ID: factory.NewID(), PublicationID: p.ID, AssignmentID: p.AssignmentID, ProjectID: p.ProjectID, Role: p.Role, Acceptance: p.Acceptance, Repository: p.Repository, Issue: p.Issue,
		PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PullTarget().IssueID, PRAuthorID: 7, ReviewerID: 7, HeadRef: p.PullTarget().HeadRef, BaseRef: p.PullTarget().BaseRef, HeadOID: p.Candidate, BaseOID: p.PullTarget().BaseOID,
		Authority: factory.AuthorityRef{Policy: 1}, Operation: factory.MergeOperation{Kind: factory.OpMerge}, Stage: factory.MergeOpen, CreatedUnix: time.Now().Unix()}
	if err := db.RecordMerge(ctx, m); err != nil {
		return err
	}
	c.Merges = progressMergeObserver{lookup: func(string) (factory.OperationOutcome, error) {
		return factory.OperationOutcome{}, errors.New("pristine closed merge reached original lookup")
	}}
	assertMergeHeld := func() error {
		report := c.MergePass(ctx)
		if len(report.Errors) != 0 || len(report.Waits) != 1 || report.Waits[0].Reason != control.WaitIssueClosed {
			return fmt.Errorf("closed pristine merge did not hold before inputs: %+v", report)
		}
		return nil
	}
	if err := assertMergeHeld(); err != nil || reads.acceptance != 0 {
		return fmt.Errorf("recent closed merge repeated inputs: %+v %v", reads, err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE issue_controls SET data=jsonb_set(data,'{assessed_unix}',to_jsonb($1::bigint)) WHERE repository=7 AND issue=12`, time.Now().Add(-time.Minute).Unix()); err != nil {
		return err
	}
	if err := assertMergeHeld(); err != nil {
		return err
	}
	refreshed, err := db.IssueControl(ctx, p.Repository, p.Issue)
	if err != nil || reads.acceptance != 1 || refreshed.Revision != closed.Revision || refreshed.FirstSeenUnix != closed.FirstSeenUnix || refreshed.Fingerprint != closed.Fingerprint || refreshed.AssessedUnix < time.Now().Unix()-2 {
		return fmt.Errorf("unchanged closed observation changed semantic authority or did not refresh poll time: %+v before=%+v reads=%+v error=%v", refreshed, closed, reads, err)
	}
	if stored, err := db.MergeByPublication(ctx, p.ID); err != nil || !reflect.DeepEqual(stored, m) {
		return errors.New("closed readiness refresh changed original merge authority or receipt")
	}
	c.PublishPass(ctx)
	if err := assertMergeHeld(); err != nil || reads.acceptance != 1 || reads.dispatch != 0 {
		return fmt.Errorf("fresh closed negative was not reused by child and merge: %+v %v", reads, err)
	}
	reads.closed = false
	c.DispatchReads = nil // Only the existing explicit intake assessment runs.
	reopened, _, err := c.ObserveIssueEvent(ctx, control.IntakeHint{Delivery: "declared-closed-issue-reopened", Repository: p.Repository, Issue: p.Issue})
	if err != nil || reopened.HoldsClosedIssue() || reopened.Revision != closed.Revision+1 || reads.acceptance != 2 {
		return fmt.Errorf("explicit reopen did not bypass recent negative observation: %+v reads=%+v error=%v", reopened, reads, err)
	}
	// Restore only this disposable fixture's declared publication/budget so the
	// existing original-lookup scenarios below retain their independent scope.
	for _, row := range []struct {
		query string
		value any
	}{
		{`UPDATE factory_publications SET revision=0,data=$1 WHERE assignment='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`, priorPublication},
		{`UPDATE factory_attempt_allowances SET revision=0,data=$1 WHERE root_assignment='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`, allowance},
	} {
		encoded, err := json.Marshal(row.value)
		if err != nil {
			return err
		}
		if _, err := raw.ExecContext(ctx, row.query, string(encoded)); err != nil {
			return err
		}
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM factory_merges WHERE publication=$1`, p.ID); err != nil {
		return err
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM issue_controls WHERE repository=7 AND issue=12`); err != nil {
		return err
	}
	fmt.Println("PASS: real PG closed issue assessment stops child before dispatch inputs; Publish/Check/Merge reuse the recent negative, due same-closed refresh advances only observation time, accepted prerequisites remain valid, direct reopen bypasses the wait, and original merge authority/receipts remain unchanged. Native issue observations declared.")
	return nil
}

func progressMergeAttemptFixture(ctx context.Context, db *store.Store, raw *sql.DB, p factory.Publication, owner factory.Assignment, allowance factory.AttemptAllowance) error {
	// This declared publication's review must name its actual recorded head.
	p.Candidate, p.PRCreate.BaseOID = p.InitialCandidate(), p.BaseSHA
	m := factory.Merge{ID: factory.NewID(), PublicationID: p.ID, AssignmentID: p.AssignmentID, ProjectID: p.ProjectID, Role: p.Role, Acceptance: p.Acceptance, Repository: p.Repository, Issue: p.Issue,
		PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PullTarget().IssueID, PRAuthorID: 7, ReviewerID: 7, HeadRef: p.PullTarget().HeadRef, BaseRef: p.PullTarget().BaseRef, HeadOID: p.Candidate, BaseOID: p.PullTarget().BaseOID,
		Authority: factory.AuthorityRef{Policy: 1}, Operation: factory.MergeOperation{Kind: factory.OpMerge}, Stage: factory.MergeOpen, CreatedUnix: time.Now().Unix()}
	if err := db.RecordMerge(ctx, m); err != nil {
		return err
	}
	write := func(query string, value any, key string) error {
		encoded, err := json.Marshal(value)
		if err == nil {
			_, err = raw.ExecContext(ctx, query, string(encoded), key)
		}
		return err
	}
	writeMerge := func(row factory.Merge) error {
		if err := row.Validate(); err != nil {
			return err
		}
		return write(`UPDATE factory_merges SET stage='open',revision=0,data=$1 WHERE publication=$2`, row, p.ID)
	}
	writeAssignment := func(a factory.Assignment) error {
		a.Stage, a.Outcome, a.Reason, a.FinishedUnix = factory.AssignmentFinished, factory.Cancelled, factory.AssignReasonCancelled, time.Now().Unix()
		a.Result = &factory.AssignmentResult{AssignmentID: a.ID, RunID: a.Run, Status: "cancelled", Summary: "Declared settled merge-gate history", RecordedUnix: a.FinishedUnix}
		if err := a.Validate(); err != nil {
			return err
		}
		return write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($2,7,12,'declared-merge-run','finished',0,$1)`, a, a.ID)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := write(`UPDATE factory_publications SET data=$1 WHERE assignment=$2`, p, p.AssignmentID); err != nil {
		return err
	}
	lookups := 0
	var phaseOrder []string
	c := control.NewCoordinator(db, nil, nil)
	c.Merges = progressMergeObserver{lookup: func(id string) (factory.OperationOutcome, error) {
		lookups++
		phaseOrder = append(phaseOrder, "merge")
		if id != m.Operation.OperationID {
			return factory.OperationOutcome{}, errors.New("lookup changed the original merge identity")
		}
		return factory.OperationOutcome{}, errors.New("declared original lookup unavailable")
	}}
	assertWait := func(reason string) error {
		report := c.MergePass(ctx)
		if len(report.Errors) != 0 || len(report.Waits) != 1 || report.Waits[0].Reason != reason || lookups != 0 {
			return fmt.Errorf("local merge gate want %q: %+v lookups=%d", reason, report, lookups)
		}
		return nil
	}
	// The newest ordinary root supersedes an old pristine row without reading
	// accepted inputs. The original merge journal remains byte-for-byte intact.
	newer := owner
	newer.ID, newer.AttemptRoot, newer.PublicationAssignment = factory.NewID(), "", ""
	newer.AttemptRoot, newer.PublicationAssignment = newer.ID, newer.ID
	newer.Run = factory.NewID()
	newer.RunHistory = []string{newer.Run}
	if err := writeAssignment(newer); err != nil {
		return err
	}
	if err := assertWait("authority_changed"); err != nil {
		return err
	}
	if checkable, err := db.CheckablePublications(ctx, 256); err != nil || len(checkable) != 0 {
		return errors.New("superseded publication root remained in passive check inventory")
	}
	if stored, err := db.MergeByPublication(ctx, p.ID); err != nil || !reflect.DeepEqual(stored, m) {
		return errors.New("passive superseded merge changed its original journal")
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM factory_assignments WHERE id=$1`, newer.ID); err != nil {
		return err
	}
	// A more recent child of the same root must not conceal its publication
	// owner. It passes this gate and reaches the separate standing-authority hold.
	child := owner
	child.ID, child.Run, child.ParentAssignment = factory.NewID(), factory.NewID(), owner.ID
	child.RunHistory = []string{child.Run}
	if err := writeAssignment(child); err != nil {
		return err
	}
	if err := assertWait("authority_ineffective"); err != nil {
		return err
	}
	if checkable, err := db.CheckablePublications(ctx, 256); err != nil || len(checkable) != 1 || checkable[0].ID != p.ID {
		return errors.New("same-root child concealed the current publication from checks")
	}
	// A declared settled findings COMMENT has no funded acceptance. The merge
	// consumer cannot create that receipt by refreshing provider evidence.
	findings := p
	w := factory.ReviewWork{OperationID: "review-" + owner.Run, AuthRevision: factory.ReviewAuthRevision(p.AssignmentID, owner.Run), Repository: p.Repository, ActorID: 7, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PullTarget().IssueID, PRAuthorID: p.PullTarget().AuthorID, HeadRef: p.PullTarget().HeadRef, BaseRef: p.PullTarget().BaseRef, HeadOID: p.Candidate, BaseOID: p.PullTarget().BaseOID, Event: "COMMENT", AgentVerdict: "REQUEST_CHANGES", Body: "Declared findings receipt", NativeRev: 1, NotAfter: time.Now().Add(time.Minute).Unix()}
	findings.ReviewOperations = []factory.ReviewOperation{{RunID: owner.Run, Work: w, Outcome: factory.OperationOutcome{OperationID: w.OperationID, InstallationID: "declared", Kind: factory.OpReviewSubmit, ActorID: w.ActorID, RepositoryID: w.Repository, Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, Receipt: []byte("declared native receipt")}}}
	if err := findings.Validate(); err != nil {
		return err
	}
	if err := write(`UPDATE factory_publications SET data=$1 WHERE assignment=$2`, findings, p.AssignmentID); err != nil {
		return err
	}
	reads := &progressClosedIssueReads{}
	c.AcceptanceReads = reads
	if err := assertWait("funded_delivery_unavailable"); err != nil || reads.acceptance != 0 {
		return fmt.Errorf("missing funded acceptance fetched native inputs: reads=%d error=%v", reads.acceptance, err)
	}
	if stored, err := db.MergeByPublication(ctx, p.ID); err != nil || !reflect.DeepEqual(stored, m) {
		return errors.New("missing funded acceptance changed its original merge journal")
	}
	if err := write(`UPDATE factory_publications SET data=$1 WHERE assignment=$2`, p, p.AssignmentID); err != nil {
		return err
	}
	c.AcceptanceReads = nil
	allowance.Closed = true
	if err := write(`UPDATE factory_attempt_allowances SET data=$1 WHERE root_assignment=$2`, allowance, allowance.RootAssignment); err != nil {
		return err
	}
	if report := c.MergePass(ctx); len(report.Errors) != 0 || len(report.Withdrawn) != 1 || lookups != 0 {
		return fmt.Errorf("closed ordinary merge did not withdraw locally: %+v", report)
	}
	// Declare the existing finite merge-only recovery capture on that closed
	// inference root; it reaches standing authority, never renews the allowance.
	prior := m
	prior.Settle(factory.MergeWithdrawn, factory.MergeReasonWithdrawn, time.Now().Unix())
	prior.WithdrawRequested = true
	request := factory.MergePreparation{RetryNoSend: true, Publication: p.ID, MergeID: prior.ID, Policy: m.Authority.Policy, Head: m.HeadOID, Base: m.BaseOID}
	if err := request.Validate(); err != nil {
		return err
	}
	m.ID = factory.NewID()
	m.RetryCommand, m.RetryNotAfter = m.ID, m.CreatedUnix+300
	priorJSON, _ := json.Marshal(prior)
	outcome, _ := json.Marshal(struct {
		Receipt factory.MergePreparationReceipt `json:"receipt"`
		Prior   string                          `json:"prior"`
	}{factory.MergePreparationReceipt{CommandID: m.ID, MergeID: m.ID, NotAfter: m.RetryNotAfter, Request: request}, string(priorJSON)})
	payload, _ := json.Marshal(request)
	if _, err := raw.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,outcome,created,finished) VALUES($1,$2,$3,'declared', $4,$5,$6,'declared','declared')`, m.ID, factory.CommandPrepareMerge, "publication/"+p.ID, factory.SettingsDigest(factory.CommandPrepareMerge, "publication/"+p.ID, string(payload)), string(payload), string(outcome)); err != nil {
		return err
	}
	if err := writeMerge(m); err != nil {
		return err
	}
	if err := assertWait("authority_ineffective"); err != nil {
		return fmt.Errorf("finite closed-root merge continuation: %w", err)
	}
	if checkable, err := db.CheckablePublications(ctx, 256); err != nil || len(checkable) != 1 || checkable[0].ID != p.ID {
		return errors.New("current finite merge-only continuation lost passive checks")
	}
	// A recorded send still looks up its original operation after supersession.
	intent := factory.MergeIntent{OperationID: factory.MergeOperationIDForGeneration(p.ID, 1), AuthRevision: "declared merge gate", HeadRef: m.HeadRef, BaseRef: m.BaseRef, HeadOID: m.HeadOID, BaseOID: m.BaseOID, Repository: m.Repository, ActorID: 7, PRNumber: m.PRNumber, PRID: m.PRID, IssueID: m.IssueID, PRAuthorID: 7, ReviewerID: 7, ReviewEvent: "COMMENT", NativeRev: 1, NotAfter: m.RetryNotAfter, AssessmentRevision: 1, ReviewID: 1}
	m.Operation.OperationID, m.Operation.Work, m.Operation.Attempts, m.Operation.UpdatedUnix = intent.OperationID, &intent, 1, time.Now().Unix()
	if err := writeMerge(m); err != nil {
		return err
	}
	if err := writeAssignment(newer); err != nil {
		return err
	}
	if report := c.MergePass(ctx); len(report.Errors) != 1 || lookups != 1 {
		return fmt.Errorf("recorded merge lookup was suppressed: %+v lookups=%d", report, lookups)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM factory_assignments WHERE id=$1`, newer.ID); err != nil {
		return err
	}
	phaseOrder = nil
	c.Checks = progressCheckObserver(func(context.Context, factory.CheckTarget, int64) (factory.ObservedChecks, error) {
		phaseOrder = append(phaseOrder, "checks")
		return factory.ObservedChecks{}, errors.New("declared check observation unavailable")
	})
	c.Dispatch(ctx)
	if len(phaseOrder) != 2 || phaseOrder[0] != "merge" || phaseOrder[1] != "checks" {
		return fmt.Errorf("recorded merge did not receive budget before passive checks: %v", phaseOrder)
	}
	// An expired allocated intent must use its causal cancellation owner before
	// any new material/permission traversal. Missing lookup alone proves nothing.
	for _, definitive := range []bool{false, true} {
		expired := m
		work := *m.Operation.Work
		work.NotAfter = time.Now().Unix() - 1
		expired.Operation.Work = &work
		if err := writeMerge(expired); err != nil {
			return err
		}
		cancelled := 0
		c.AcceptanceReads = reads
		c.Merges = progressMergeObserver{
			lookup: func(id string) (factory.OperationOutcome, error) {
				return factory.OperationOutcome{OperationID: id, NotObserved: true}, nil
			},
			cancel: func(id string) (factory.OperationOutcome, error) {
				cancelled++
				if id != work.OperationID {
					return factory.OperationOutcome{}, errors.New("expired merge cancellation changed original identity")
				}
				o := factory.OperationOutcome{OperationID: id, InstallationID: "declared", Kind: factory.OpMerge, ActorID: 7, RepositoryID: 7, Effect: factory.OpEffectIndeterminate, Cancellation: factory.OpCancelIndeterminate, Completion: factory.OpCompletionNeedsIntervention, Reason: "dispatch_reserved"}
				if definitive {
					o.Effect, o.Cancellation, o.Completion, o.Reason = factory.OpEffectNotCommitted, factory.OpCancelCancelled, factory.OpCompletionComplete, "cancelled_before_dispatch"
				}
				return o, nil
			},
		}
		report := c.MergePass(ctx)
		stored, err := db.MergeByPublication(ctx, p.ID)
		if err != nil || len(report.Errors) != 0 || cancelled != 1 || reads.acceptance != 0 || reads.dispatch != 0 || !stored.WithdrawRequested || !reflect.DeepEqual(stored.Operation.Work, &work) {
			return fmt.Errorf("expired merge did not preserve original cancellation: definitive=%v cancelled=%d reads=%+v report=%+v error=%v", definitive, cancelled, reads, report, err)
		}
		if definitive && (stored.Stage != factory.MergeWithdrawn || stored.Operation.Effect != factory.OpEffectNotCommitted) || !definitive && (stored.Stage != factory.MergeOpen || stored.Operation.Effect != factory.OpEffectIndeterminate) {
			return fmt.Errorf("expired merge confused missing lookup with confirmed no-send: definitive=%v stage=%s effect=%s", definitive, stored.Stage, stored.Operation.Effect)
		}
	}
	c.Host, c.Broker = factoryruntime.NativeExecution{}, factoryruntime.NativeExecution{}
	c.Publication = factoryruntime.NativePublication{}
	allowance.Closed = false
	if err := write(`UPDATE factory_attempt_allowances SET data=$1 WHERE root_assignment=$2`, allowance, allowance.RootAssignment); err != nil {
		return err
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM factory_assignments WHERE id=$1`, child.ID); err != nil {
		return err
	}
	publicationReport := c.PublishPass(ctx)
	if len(publicationReport.Errors) != 0 || reads.acceptance != 0 || reads.dispatch != 0 || len(publicationReport.Waits) != 1 || publicationReport.Waits[0].Reason != "merge_preparation_required" {
		return fmt.Errorf("finalized merge repeated publication-child material reads: report=%+v reads=%+v", publicationReport, reads)
	}
	if checkable, err := db.CheckablePublications(ctx, 256); err != nil || len(checkable) != 0 {
		return fmt.Errorf("finalized exact candidate remained in passive CI inventory: %v", err)
	}
	fmt.Println("PASS: real PG holds a pristine superseded merge and a findings COMMENT without a funded acceptance before provider inputs without changing original journals; current-root child reaches standing authority, closed ordinary root withdraws locally, finite merge-only continuation preserves its closed inference root, and allocated original operation still looks up after supersession. Histories declared; no provider or inference.")
	fmt.Println("PASS: expired allocated merge cancels its original identity before provider/material reads; a declared reserved unknown stays open, and only its owner's confirmed no-send withdraws. Original work/deadline retained; no provider or inference.")
	fmt.Println("PASS: actual PublishPass and passive CI inventory hold the finalized exact candidate, leaving original cancellation and allowance intact until a fresh explicit merge command. No provider/model.")
	return nil
}

type externalDiscoveryFixture struct {
	control.HostFactory
	control.BrokerExecution
	control.ReviewExecutor
	selection             control.AutomaticExternalPullSelection
	polls, offers, checks int
	observation           *factory.ReviewObservation
	observations          int
}

func (f *externalDiscoveryFixture) ReadAutomaticExternalPulls(context.Context, int64, string) (control.AutomaticExternalPullPage, error) {
	f.polls++
	return control.AutomaticExternalPullPage{Selections: []control.AutomaticExternalPullSelection{f.selection}, Complete: true}, nil
}
func (f *externalDiscoveryFixture) CheckAutomaticExternalPull(context.Context, factory.ExternalPullAdoption) error {
	f.checks++
	if f.observation != nil {
		return nil
	}
	return errors.New("declared discovery must not authorize a new launch")
}
func (f *externalDiscoveryFixture) ObserveReview(context.Context, factory.ReviewWork) (factory.ReviewObservation, error) {
	f.observations++
	if f.observation == nil {
		return factory.ReviewObservation{}, errors.New("declared target observation unavailable")
	}
	return *f.observation, nil
}
func (f *externalDiscoveryFixture) ReadExternalPullTarget(context.Context, int64, int64) (factory.ReviewObservation, error) {
	f.offers++
	return factory.ReviewObservation{}, errors.New("declared unseen head stops at fresh offer")
}
func (f *externalDiscoveryFixture) ReadCreations(context.Context, int64, int64, int) (factory.CreationRecoveryBatch, error) {
	return factory.CreationRecoveryBatch{}, errors.New("declared discovery-only pass")
}

// Provider, setup and predecessor observations are declared. The existing
// standalone PostgreSQL owners admit actual grants, preparation and packet;
// this fixture makes no host call, provider request or inference.
func externalPreparationFixture() error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	config, err := factoryruntime.LoadNative(os.Getenv("SODA_NATIVE_FACTORY_CONFIG"))
	if err != nil {
		return err
	}
	db, dsn, cleanup, err := store.OpenEphemeral(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer cleanup()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer raw.Close()
	if err = db.InitializeEmptyCollaborationLedger(ctx); err != nil {
		return err
	}
	host, err := db.PutCollaborationHost(ctx, collaboration.Host{ID: collaboration.HostID(factory.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"})
	if err != nil {
		return err
	}
	repo, err := db.ObserveCollaborationRepository(ctx, host.ID, "declared-external-setup", "fixture/external-setup")
	if err != nil {
		return err
	}
	actor, err := db.ObserveCollaborationActor(ctx, host.ID, "declared-maintainer", "maintainer")
	if err != nil {
		return err
	}
	profile := collaboration.Profile{ID: collaboration.ProfileID(factory.NewID()), Name: "Declared external setup"}
	if err = db.PutCollaborationProfile(ctx, profile); err != nil {
		return err
	}
	account := collaboration.ConnectionRef{ID: collaboration.ConnectionID(factory.NewID()), Profile: profile.ID, Host: host.ID, Actor: actor.ID}
	if err = db.PutCollaborationConnection(ctx, account); err != nil {
		return err
	}
	runtime := collaboration.RuntimeRef{ID: collaboration.RuntimeID(profile.ID), Profile: profile.ID, Kind: collaboration.Native, Name: profile.Name}
	placement, err := db.SelectPlacement(ctx, host.ID, repo.ID, actor.ID, runtime, "", factory.PlacementActive)
	if err != nil {
		return err
	}
	environment := &project.NativeEnvironment{Profile: profile.ID, Runtime: runtime.ID, Generation: placement.Generation}
	if _, err = db.ApplyFactoryGrant(ctx, "external-preparation-fixture", project.EnvironmentGrant{Repository: int64(repo.ID), Owner: int64(actor.ID), Native: environment, Active: true}); err != nil {
		return err
	}
	grant, err := db.EnvironmentGrant(ctx, int64(repo.ID))
	if err != nil {
		return err
	}
	base := strings.Repeat("b", 40)
	requirement := project.RequirementDecision{ID: "d" + factory.NewID()[:24], Project: grant.Project, Approver: int64(actor.ID), SourceCommit: base, SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64), Native: environment}
	if err = db.AdmitRequirementDecision(ctx, requirement); err != nil {
		return err
	}
	approval := project.ApprovalDecision{ID: "d" + factory.NewID()[:24], Project: grant.Project, Requirement: requirement.ID, Approver: int64(actor.ID), EffectsDigest: strings.Repeat("c", 64), ReadinessDigest: strings.Repeat("d", 64), Verified: true, Native: environment}
	if err = db.AdmitApprovalDecision(ctx, approval); err != nil {
		return err
	}
	selection := factory.RoleSelection{Account: string(account.ID), Harness: project.FactoryHarnessMuse, HarnessVers: "declared-1", Model: "declared-no-model"}
	ref := func(kind string) factory.ActorBindingRef {
		return factory.ActorBindingRef{NativeProfile: profile.ID, NativeConnection: account.ID, ActorID: int64(actor.ID), Kind: kind}
	}
	policy := factory.RepositoryPolicy{Repository: int64(repo.ID), GrantedBy: int64(actor.ID), Enabled: true, AutomaticExternalPulls: true, TargetBranch: "refs/heads/main", MergeMethod: factory.MergeFastForward, Checks: []string{"declared/check"}, Roles: map[string]factory.RoleSelection{project.RoleCoder: selection, project.RoleReviewer: selection}, Publish: ref(factory.OpRefPublish), Create: ref(factory.OpPRCreate), Review: ref(factory.OpReviewSubmit), Merge: ref(factory.OpMerge), AttemptLimits: factory.AttemptLimits{ActiveMinutes: 15, ReviewRounds: 2, CorrectionCycles: 1, MaxChildren: 1}, ReassessmentMinutes: factory.DefaultReassessmentMinutes}
	for _, err = range []error{
		db.SaveRepositoryPolicy(ctx, policy),
		db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: int64(actor.ID), MaxConcurrentRuns: 2}),
		db.SaveOperatorGrant(ctx, factory.OperatorGrant{Repository: int64(repo.ID), GrantedBy: int64(actor.ID), Active: true}),
		db.SaveSponsorship(ctx, factory.Sponsorship{Repository: int64(repo.ID), GrantedBy: int64(actor.ID), ActorID: int64(actor.ID), ProjectID: grant.Project, Connection: string(account.ID), Native: &factory.NativeSponsorship{Profile: profile.ID, CredentialSHA256: strings.Repeat("a", 64)}, Roles: []string{project.RoleCoder, project.RoleReviewer}, Active: true}),
	} {
		if err != nil {
			return err
		}
	}
	policy, err = db.RepositoryPolicy(ctx, int64(repo.ID))
	if err != nil {
		return err
	}
	acceptance := factory.Acceptance{ID: "d" + factory.NewID()[:24], Repository: int64(repo.ID), IssueIndex: "1", Approver: int64(actor.ID), NativeRev: 1, TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), ContentVersion: 1}
	if err = db.AdmitAcceptanceDecision(ctx, acceptance); err != nil {
		return err
	}
	controlRow, _, err := db.RecordIssueAssessment(ctx, factory.IssueControl{Repository: int64(repo.ID), Issue: 1, Acceptance: acceptance.ID, NativeRev: 1, Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible, Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64)}, time.Now())
	if err != nil {
		return err
	}
	authority := factory.AuthorityRef{Policy: policy.Revision, Operator: 1, Capacity: 1, Sponsorship: 1, SponsorshipConnection: string(account.ID), Environment: grant.Revision, RequirementsID: requirement.ID, ApprovalID: approval.ID}
	write := func(query string, value any, args ...any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = raw.ExecContext(ctx, query, append(args, string(encoded))...)
		return err
	}
	// An existing finished root remains retained and closed, with fully settled
	// native evidence. It is not an explicit Retry or a new source of authority.
	prior := offlineRoleAssignment(project.ExecutionInvestigate, project.RoleCoder, base)
	prior.Repository, prior.Issue, prior.ProjectID, prior.ActorID = int64(repo.ID), 1, grant.Project, int64(actor.ID)
	prior.Acceptance, prior.Subject.Issue.Acceptance, prior.Connection, prior.Authority = acceptance.ID, acceptance.ID, string(account.ID), authority
	prior.Stage, prior.Outcome, prior.Reason, prior.FinishedUnix = factory.AssignmentFinished, factory.Cancelled, factory.AssignReasonCancelled, time.Now().Unix()
	prior.Result = &factory.AssignmentResult{AssignmentID: prior.ID, RunID: prior.Run, Status: "cancelled", Summary: "Declared settled predecessor", RecordedUnix: prior.FinishedUnix}
	if err = prior.Validate(); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,1,$3,'finished',0,$4)`, prior, prior.ID, repo.ID, prior.Run); err != nil {
		return err
	}
	oldAllowance := factory.AttemptAllowance{RootAssignment: prior.ID, Repository: int64(repo.ID), Issue: 1, Limits: policy.AttemptLimits, CheckpointUnix: time.Now().Unix(), Closed: true}
	if err = write(`INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,1,$2,0,$3)`, oldAllowance, repo.ID, prior.ID); err != nil {
		return err
	}
	tool := execution.NativeTool{Harness: project.FactoryHarnessMuse, Version: selection.HarnessVers, SHA256: strings.Repeat("e", 64), Executable: "/home/fixture/bin/muse"}
	oldRun := factory.Run{ID: prior.Run, ProjectID: grant.Project, Role: prior.Role, InputSHA: base, Started: time.Now().Add(-time.Minute), Deadline: time.Now().Add(time.Minute), Harness: tool.Harness + "-" + tool.Version, Model: prior.Model, Outcome: factory.Failed, Reconciled: true, Native: &execution.NativeRecord{Tool: tool, Retirement: execution.Retired, Sealed: true, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: profile.ID, InstanceID: factory.NewID(), WorktreeID: prior.Preparation, Custody: execution.NativeCustody{State: execution.NotAcquired}}}}
	if err = oldRun.Validate(); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, oldRun, oldRun.ID); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: prior.Run, Repository: prior.Repository, Issue: 1, Attempt: prior.ID}); err != nil {
		return err
	}
	request := factory.ExternalPullAdoption{Policy: policy.Revision, Repository: int64(repo.ID), Issue: 1, Acceptance: acceptance.ID, PRNumber: 3, PRID: 13, Connection: string(account.ID), Limits: policy.AttemptLimits, Automatic: true, Target: factory.PublicationPullTarget{AuthorID: int64(actor.ID) + 1, IssueID: 23, HeadRepository: int64(repo.ID), HeadRef: "refs/heads/external", BaseRef: policy.TargetBranch, HeadOID: strings.Repeat("c", 40), BaseOID: base}}
	consentFork, err := db.ObserveCollaborationRepository(ctx, host.ID, "declared-external-fork", "fixture/fork")
	if err != nil {
		return err
	}
	var beforeAdmission int
	countAdmission := `SELECT (SELECT count(*) FROM project_preparations)+(SELECT count(*) FROM factory_commands)+(SELECT count(*) FROM factory_assignments)+(SELECT count(*) FROM factory_runs)+(SELECT count(*) FROM factory_usage)`
	if err = raw.QueryRowContext(ctx, countAdmission).Scan(&beforeAdmission); err != nil {
		return err
	}
	for _, scenario := range []string{"missing-fork-consent", "automatic-fork", "consented-fork-head-changed", "automatic-head-changed", "automatic-base-changed"} {
		captured := request
		if strings.Contains(scenario, "fork") {
			captured.Target.HeadRepository = int64(consentFork.ID)
			captured.Automatic = scenario == "automatic-fork"
			captured.ForkConsent = scenario != "missing-fork-consent"
		}
		observed := factory.ReviewObservation{PRNumber: captured.PRNumber, PRID: captured.PRID, IssueID: captured.Target.IssueID, PRAuthorID: captured.Target.AuthorID,
			HeadRepository: captured.Target.HeadRepository, HeadRef: captured.Target.HeadRef, BaseRef: captured.Target.BaseRef,
			HeadOID: captured.Target.HeadOID, BaseOID: captured.Target.BaseOID, NativeRev: 1, ObservedUnix: time.Now().Unix()}
		if strings.HasSuffix(scenario, "head-changed") {
			observed.HeadOID = strings.Repeat("d", 40)
		} else if scenario == "automatic-base-changed" {
			observed.BaseOID = strings.Repeat("e", 40)
		}
		reader := &externalDiscoveryFixture{observation: &observed}
		coordinator := control.NewCoordinator(db, reader, reader)
		coordinator.Reviews = reader
		if _, err = coordinator.AdoptExternalPull(ctx, factory.NewID(), "declared-stale-adoption", captured); !errors.Is(err, store.ErrAdmissionChanged) {
			return fmt.Errorf("external adoption admitted %s: %v", scenario, err)
		}
		wantedReads := 1
		if scenario == "missing-fork-consent" || scenario == "automatic-fork" {
			wantedReads = 0
		}
		if reader.observations != wantedReads {
			return fmt.Errorf("external %s did not stop at its consent/closing-target boundary: reads=%d", scenario, reader.observations)
		}
		var afterAdmission int
		if err = raw.QueryRowContext(ctx, countAdmission).Scan(&afterAdmission); err != nil || afterAdmission != beforeAdmission {
			return fmt.Errorf("external %s retained setup, command, execution or spending: %v", scenario, err)
		}
	}
	fmt.Println("PASS: actual coordinator external-adoption entry refuses missing fork consent and automatic fork execution before target reads; consented fork/automatic changed head and automatic changed base refuse at the closing observation before setup/command/run/spending. Provider observations declared; real PostgreSQL, no inference/send.")
	commandSum := sha256.Sum256([]byte("automatic-external-pr\x00" + strconv.FormatInt(request.Repository, 10) + "\x00" + strconv.FormatInt(request.PRID, 10) + "\x00" + request.Target.HeadOID))
	command := hex.EncodeToString(commandSum[:16])
	revision, err := db.ObserveNativeReview(ctx, int64(repo.ID), request.PRNumber, strings.Repeat("f", 64))
	if err != nil {
		return err
	}
	planning := &factory.ExternalPullPlanning{Request: request, Principal: "automatic-external-pr", Origin: factory.ExternalPublicationOrigin{CommandID: command, Automatic: true, Host: host.ID, Profile: profile.ID, Connection: account.ID, ActorID: int64(actor.ID), Policy: policy.Revision, Target: request.Target, ObservationDigest: strings.Repeat("f", 64), ObservationRevision: revision, ObservedUnix: time.Now().Unix()}}
	idSum := sha256.Sum256([]byte("fresh-coder\x00" + command + "\x00" + base))
	preparation := execution.PreparationRecord{Preparation: project.Preparation{ID: "f" + hex.EncodeToString(idSum[:12]), Project: grant.Project, Role: project.RoleCoder, SourceCommit: base, SetupDigest: requirement.SetupDigest, Requirements: requirement.Ref(1), Approval: approval.Ref(1)}}
	preparation.Native = &execution.NativeWorkspaceRef{Profile: profile.ID, InstanceID: factory.NewID(), WorktreeID: preparation.Preparation.ID}
	open, gate, _, err := db.DispatchState(ctx, int64(repo.ID))
	if err != nil || !open {
		return fmt.Errorf("declared dispatch gate unavailable: %v", err)
	}
	registration := store.CandidatePreparationRegistration{Repository: int64(repo.ID), Issue: 1, ActorID: int64(actor.ID), Project: grant.Project, OwnerAssignment: command, ChildAssignment: command, AttemptRoot: command, Connection: string(account.ID), Authority: authority, GateRevision: gate, Control: controlRow, RequestedNotAfter: time.Now().Add(2 * time.Minute).Unix(), ExternalPlanning: planning}
	admitted, created, err := db.AdmitCandidatePreparation(ctx, preparation, registration)
	if err != nil || !created || admitted.Admission.ExternalPlanning.CheckedUnix != 0 {
		return fmt.Errorf("external source setup not admitted before packet: created=%t %v", created, err)
	}
	if _, err = db.FactoryCommand(ctx, command); !errors.Is(err, store.ErrNotFound) {
		return errors.New("source setup prematurely admitted model command")
	}
	if _, err = db.AuthorizeCandidatePreparation(ctx, preparation.Preparation.ID); err != nil {
		return fmt.Errorf("current external setup before-host guard: %w", err)
	}
	// Withdrawal and unsettled predecessor checks use actual current database
	// predicates; restore only their declared fixture observations afterward.
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET active=true WHERE id=$1`, prior.Run); err != nil {
		return err
	}
	if _, err = db.AuthorizeCandidatePreparation(ctx, preparation.Preparation.ID); !errors.Is(err, store.ErrAttemptRunsPending) {
		return fmt.Errorf("unsettled predecessor admitted setup: %v", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET active=false WHERE id=$1`, prior.Run); err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_policies SET data=jsonb_set(data,'{paused}','true') WHERE repository=$1`, repo.ID); err != nil {
		return err
	}
	if _, err = db.AuthorizeCandidatePreparation(ctx, preparation.Preparation.ID); !errors.Is(err, store.ErrAdmissionChanged) {
		return fmt.Errorf("withdrawn policy admitted setup: %v", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_policies SET data=jsonb_set(data,'{paused}','false') WHERE repository=$1`, repo.ID); err != nil {
		return err
	}
	changed := registration
	changedPlanning := *planning
	changedPlanning.Principal = "different-command-principal"
	changed.ExternalPlanning = &changedPlanning
	if _, _, err = db.AdmitCandidatePreparation(ctx, preparation, changed); !errors.Is(err, store.ErrCandidatePreparationConflict) {
		return fmt.Errorf("changed command reused preparation: %v", err)
	}
	// Declare an older first observation to exercise setup duration without a
	// real expiry sleep. Preserve its original absolute send deadline/address.
	planning.Origin.ObservedUnix = time.Now().Add(-time.Minute).Unix()
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,external_planning,origin,observed_unix}',to_jsonb($2::bigint)) WHERE id=$1`, preparation.Preparation.ID, planning.Origin.ObservedUnix); err != nil {
		return err
	}
	registration.RequestedNotAfter = time.Now().Add(10 * time.Minute).Unix()
	recovered, created, err := db.AdmitCandidatePreparation(ctx, preparation, registration)
	if err != nil || created || recovered.NotAfter != admitted.NotAfter || !reflect.DeepEqual(recovered.Preparation.Native, admitted.Preparation.Native) || !reflect.DeepEqual(recovered.Admission.ExternalPlanning, planning) {
		return fmt.Errorf("recovery renewed setup capture/deadline/address: created=%t %v", created, err)
	}
	if _, err = db.AuthorizeCandidatePreparation(ctx, preparation.Preparation.ID); err != nil {
		return fmt.Errorf("retained setup lost original authority: %w", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,not_after_unix}',to_jsonb($2::bigint)) WHERE id=$1`, preparation.Preparation.ID, time.Now().Add(-time.Second).Unix()); err != nil {
		return err
	}
	if _, err = db.AuthorizeCandidatePreparation(ctx, preparation.Preparation.ID); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
		return fmt.Errorf("expired setup was renewed: %v", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,not_after_unix}',to_jsonb($2::bigint)) WHERE id=$1`, preparation.Preparation.ID, admitted.NotAfter); err != nil {
		return err
	}
	zero := 0
	preparation.State = execution.PreparationState{ID: preparation.Preparation.ID, Project: grant.Project, Role: project.RoleCoder, SourceCommit: base, SetupDigest: requirement.SetupDigest, Phase: project.PrepareReady, Ready: true, Native: preparation.Native, SourceBundleSHA256: strings.Repeat("f", 64), SetupExit: &zero, CheckExit: &zero}
	if err = db.ObservePreparation(ctx, preparation); err != nil {
		return err
	}
	a := offlineRoleAssignment(project.ExecutionInvestigate, project.RoleCoder, base)
	a.ID, a.AttemptRoot, a.PublicationAssignment = command, command, command
	a.Repository, a.Issue, a.ProjectID, a.ActorID = int64(repo.ID), 1, grant.Project, int64(actor.ID)
	a.Acceptance, a.Subject.Issue.Acceptance, a.Connection, a.Authority = acceptance.ID, acceptance.ID, string(account.ID), authority
	a.Preparation, a.Harness, a.HarnessVers, a.Model, a.NativeTool = preparation.Preparation.ID, project.FactoryHarnessMuse+"-"+tool.Version, tool.Version, selection.Model, &tool
	a.ExternalPlanning = planning
	run := factory.Run{ID: a.Run, ProjectID: a.ProjectID, Role: a.Role, InputSHA: base, Started: time.Now(), Deadline: time.Now().Add(time.Minute), Harness: a.Harness, Model: a.Model, Native: &execution.NativeRecord{Tool: tool, BundleSHA256: preparation.State.SourceBundleSHA256, PromptSHA256: a.PromptSHA, Retirement: execution.NotStarted, Provenance: execution.NativeProvenance{TrustMode: "isolated_system_worker", Profile: profile.ID, InstanceID: preparation.Native.InstanceID, WorktreeID: preparation.Native.WorktreeID, BrokerRun: factory.NewID(), Custody: execution.NativeCustody{State: execution.NotAcquired}}}}
	launch := execution.NativeLaunch{CredentialSHA256: strings.Repeat("a", 64), Workspace: *preparation.Native, Tool: tool, BundleSHA256: preparation.State.SourceBundleSHA256, PromptSHA256: a.PromptSHA, Runtime: runtime.ID, PlacementGeneration: placement.Generation, TrustMode: run.Native.Provenance.TrustMode, BrokerRun: run.Native.Provenance.BrokerRun}
	reservation := factory.Reservation{AssignmentID: command, Repository: a.Repository, Connection: a.Connection, State: factory.ReservationHeld, PlannedMinutes: 1}
	view := factory.RunView{RunID: a.Run, Repository: a.Repository, Issue: 1, Attempt: command}
	if err = db.RecordNativeDispatchPacket(ctx, controlRow, a, reservation, run, view, launch); !errors.Is(err, store.ErrAdmissionChanged) {
		return fmt.Errorf("old capture without fresh check admitted packet: %v", err)
	}
	fresh := *planning
	fresh.CheckedUnix = time.Now().Add(time.Minute).Unix()
	a.ExternalPlanning = &fresh
	if err = db.RecordNativeDispatchPacket(ctx, controlRow, a, reservation, run, view, launch); err == nil {
		return errors.New("future fresh-check time admitted packet")
	}
	fresh.CheckedUnix = time.Now().Unix()
	a.CreatedUnix = fresh.CheckedUnix
	if err = db.RecordNativeDispatchPacket(ctx, controlRow, a, reservation, run, view, launch); err != nil {
		return fmt.Errorf("fresh same-origin external packet refused: %w", err)
	}
	stored, err := db.Assignment(ctx, command)
	if err != nil || stored.ExternalPlanning.Origin != planning.Origin || stored.ExternalPlanning.CheckedUnix != fresh.CheckedUnix {
		return errors.New("packet replaced original external origin")
	}
	retained, err := db.CandidatePreparation(ctx, preparation.Preparation.ID)
	if err != nil || retained.NotAfter != admitted.NotAfter || retained.Admission.ExternalPlanning.CheckedUnix != 0 {
		return errors.New("packet renewed preparation authority")
	}
	var old factory.AttemptAllowance
	var oldData []byte
	err = raw.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, prior.ID).Scan(&oldData)
	if err == nil {
		err = json.Unmarshal(oldData, &old)
	}
	if err != nil || !reflect.DeepEqual(old, oldAllowance) {
		return fmt.Errorf("external packet changed old allowance: %+v %v", old, err)
	}
	if cmd, err := db.FactoryCommand(ctx, command); err != nil || cmd.Finished == "" || cmd.Principal != planning.Principal {
		return errors.New("external packet lost original command receipt")
	}
	// Complete only this declared Architect. Its source checkout remains ready
	// while the process/custody receipt is sealed, retired and reconciled.
	plan := nativeWorkspacePlan(base)
	plan.AttemptRoot, plan.Preparation = command, preparation.Preparation.ID
	plan.Digest, err = factory.PlanDigest(plan)
	if err != nil {
		return err
	}
	if err = db.SaveInvestigationPlan(ctx, plan); err != nil {
		return err
	}
	if err = db.AdvancePlanHead(ctx, command, "", plan.ID); err != nil {
		return err
	}
	stored.Stage, stored.Outcome, stored.Reason, stored.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, time.Now().Unix()
	stored.Result = &factory.AssignmentResult{AssignmentID: command, RunID: stored.Run, Status: "completed", Summary: "Declared completed external Architect", Reported: true, RecordedUnix: stored.FinishedUnix, Phase: project.ExecutionInvestigate, PhaseResult: &factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed", Summary: "Declared completed external Architect", Output: &factory.PhaseOutput{Plan: &plan, PlanReview: strings.Repeat("d", 64)}}}
	if err = db.FinishAssignment(ctx, stored); err != nil {
		return err
	}
	finishedRun, err := db.FactoryRun(ctx, stored.Run)
	if err != nil {
		return err
	}
	finishedRun.Outcome, finishedRun.Reconciled, finishedRun.Summary = factory.Succeeded, true, "Declared settled external Architect"
	finishedRun.Native.Retirement, finishedRun.Native.Sealed = execution.Retired, true
	if err = db.SaveFactoryRun(ctx, finishedRun); err != nil {
		return err
	}
	if err = db.ReleaseReservation(ctx, command); err != nil {
		return err
	}
	now := time.Now()
	promoted := factory.Publication{ID: command, AssignmentID: command, ProjectID: stored.ProjectID, Role: project.RoleCoder, Authority: stored.Authority, Acceptance: stored.Acceptance, Preparation: stored.Preparation, External: &stored.ExternalPlanning.Origin, Candidate: request.Target.HeadOID, BaseSHA: base, TargetBranch: request.Target.BaseRef, PRNumber: request.PRNumber, PRID: request.PRID, Stage: factory.PublicationPublished, Repository: stored.Repository, Issue: stored.Issue, Publish: factory.PublicationOperation{Kind: factory.OpRefPublish}, PRCreate: factory.PublicationOperation{Kind: factory.OpPRCreate}, CreatedUnix: now.Unix(), FinishedUnix: now.Unix(), Outcome: factory.Succeeded, Reason: factory.PublishReasonLinked}
	// An additional same-root preparation may never disappear into promotion.
	extra := preparation
	extra.Preparation.ID = "f" + factory.NewID()[:24]
	extraNative := *preparation.Native
	extraNative.WorktreeID = extra.Preparation.ID
	extra.Native, extra.State.Native, extra.State.ID = &extraNative, &extraNative, extra.Preparation.ID
	if _, _, err = db.AdmitPreparation(ctx, extra); err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission}',(SELECT data->'factory_admission' FROM project_preparations WHERE id=$2)) WHERE id=$1`, extra.Preparation.ID, preparation.Preparation.ID); err != nil {
		return err
	}
	if _, err = db.RecordExternalPlanningPublication(ctx, stored, promoted, now); !errors.Is(err, store.ErrAttemptRunsPending) {
		return fmt.Errorf("unsettled additional root preparation admitted promotion: %v", err)
	}
	if _, err = raw.ExecContext(ctx, `DELETE FROM project_preparations WHERE id=$1`, extra.Preparation.ID); err != nil {
		return err
	}
	// Normal restart may retain the successful source receipt while observing
	// confirmed retirement. Neither that nor setup expiry renews authority.
	stop := execution.PreparationState{ID: preparation.Preparation.ID, Project: grant.Project, Native: preparation.Native, Stopped: true, Retirement: "uncertain"}
	if err = db.RecordCandidatePreparationStop(ctx, preparation.Preparation.ID, stop); err != nil {
		return err
	}
	if _, err = db.RecordExternalPlanningPublication(ctx, stored, promoted, now); !errors.Is(err, store.ErrAttemptRunsPending) {
		return fmt.Errorf("uncertain source retirement admitted promotion: %v", err)
	}
	stop.Retirement = "confirmed"
	if err = db.RecordCandidatePreparationStop(ctx, preparation.Preparation.ID, stop); err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,not_after_unix}',to_jsonb($2::bigint)) WHERE id=$1`, preparation.Preparation.ID, time.Now().Add(-time.Second).Unix()); err != nil {
		return err
	}
	beforePromotion, err := db.CandidatePreparation(ctx, preparation.Preparation.ID)
	if err != nil {
		return err
	}
	allowanceBefore, err := db.AttemptAllowance(ctx, stored.Repository, stored.Issue)
	if err != nil {
		return err
	}
	if _, err = db.RecordExternalPlanningPublication(ctx, stored, promoted, now); err != nil {
		return fmt.Errorf("completed Architect with retained source did not promote: %w", err)
	}
	afterPromotion, err := db.CandidatePreparation(ctx, preparation.Preparation.ID)
	if err != nil || !reflect.DeepEqual(beforePromotion, afterPromotion) {
		return errors.New("promotion renewed original preparation authority or custody")
	}
	allowanceAfter, err := db.AttemptAllowance(ctx, stored.Repository, stored.Issue)
	if err != nil || !reflect.DeepEqual(allowanceBefore, allowanceAfter) {
		return errors.New("promotion replaced or renewed the original allowance")
	}
	if replay, err := db.RecordExternalPlanningPublication(ctx, stored, promoted, now); err != nil || !reflect.DeepEqual(replay, promoted) {
		return fmt.Errorf("original promotion receipt did not replay: %v", err)
	}
	// Run the ordinary startup discovery owner over this retained command.
	// Discovery facts are declared; any attempted offer/authority read is counted.
	lockDir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "external-discovery-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(lockDir)
	discovery := &externalDiscoveryFixture{selection: control.AutomaticExternalPullSelection{ExternalPullSelection: factory.ExternalPullSelection{Issue: request.Issue, Number: request.PRNumber}, PRID: request.PRID, Head: request.Target.HeadOID}}
	for pass := 0; pass < 2; pass++ {
		c := control.NewCoordinator(db, discovery, discovery)
		c.Reviews, c.Creations = discovery, discovery
		if err = c.Start(ctx, filepath.Join(lockDir, "coordinator.lock")); err != nil {
			_ = c.Close()
			return err
		}
		if err = c.Close(); err != nil {
			return err
		}
		if pass == 0 {
			if discovery.polls != 1 || discovery.offers != 0 || discovery.checks != 0 {
				return errors.New("consumed external command repeated provider offer or authority reads")
			}
			discovery.selection.Head = strings.Repeat("d", 40)
		}
	}
	if discovery.polls != 2 || discovery.offers != 1 || discovery.checks != 0 {
		return errors.New("new external head bypassed its fresh offer")
	}
	if after, err := db.Assignment(ctx, command); err != nil || !reflect.DeepEqual(after, stored) {
		return errors.New("discovery changed the original external assignment")
	}
	if after, err := db.PublicationByID(ctx, command); err != nil || !reflect.DeepEqual(after, promoted) {
		return errors.New("discovery changed the original external publication receipt")
	}
	// The external Architect owns its publication directly; equal IDs do not
	// turn its captured external candidate into a retained Builder result.
	merge := factory.Merge{ID: factory.NewID(), PublicationID: promoted.ID, AssignmentID: stored.ID, ProjectID: stored.ProjectID, Role: project.RoleCoder, Authority: stored.Authority, Acceptance: stored.Acceptance, Repository: stored.Repository, Issue: stored.Issue, PRNumber: request.PRNumber, PRID: request.PRID, IssueID: request.Target.IssueID, PRAuthorID: request.Target.AuthorID, ReviewerID: int64(actor.ID), HeadRef: request.Target.HeadRef, BaseRef: request.Target.BaseRef, HeadOID: request.Target.HeadOID, BaseOID: request.Target.BaseOID, Stage: factory.MergeOpen, CreatedUnix: time.Now().Add(-2 * time.Minute).Unix(), Operation: factory.MergeOperation{Kind: factory.OpMerge}}
	if err = db.RecordMerge(ctx, merge); err != nil {
		return err
	}
	// Registration creates an unbound row. Only this disposable fixture then
	// declares the expired historical binding; no fresh authority is issued.
	merge.Authorization = factory.MergeAuthorization{Generation: factory.MergeGeneration{Authority: merge.Authority, PublicationID: promoted.ID, Generation: 1, NativeOperationID: factory.MergeGenerationOperationID(promoted.ID, 1), Fingerprint: strings.Repeat("e", 64), Head: merge.HeadOID, Base: merge.BaseOID, DiffBase: merge.BaseOID, ReviewRound: 1, AssessmentRevision: 1, ChecksDigest: factory.ChecksDigest(policy.Checks), ObservedNativeRev: revision, NotAfter: time.Now().Add(-time.Second).Unix(), AcceptanceID: factory.NewID(), AcceptanceKind: factory.AcceptanceCleanReview, AttemptRoot: command, OriginReceipt: factory.NewID()}, ResolutionsConfirmed: true}
	if err = merge.Validate(); err != nil {
		return err
	}
	if err = write(`UPDATE factory_merges SET data=$2 WHERE publication=$1`, merge, promoted.ID); err != nil {
		return err
	}
	offer, err := db.UnsentMergePreparation(ctx, promoted.ID)
	if err != nil || !offer.RetryNoSend || offer.MergeID != merge.ID || offer.Head != merge.HeadOID || offer.Base != merge.BaseOID {
		return fmt.Errorf("external Architect merge-only preparation unavailable: %+v %v", offer, err)
	}
	mergeCommand := factory.NewID()
	preparedMerge, err := db.PrepareUnsentMerge(ctx, mergeCommand, "declared-merge-only", offer, time.Now())
	if err != nil || preparedMerge.MergeID != mergeCommand || preparedMerge.NotAfter <= time.Now().Unix() || preparedMerge.NotAfter > time.Now().Add(5*time.Minute).Unix() {
		return fmt.Errorf("external Architect finite merge preparation failed: %+v %v", preparedMerge, err)
	}
	currentMerge, err := db.MergeByPublication(ctx, promoted.ID)
	if err != nil || currentMerge.RetryCommand != mergeCommand || len(currentMerge.History) != 1 || !reflect.DeepEqual(currentMerge.History[0].Authorization, merge.Authorization) || !reflect.DeepEqual(currentMerge.History[0].Operation, merge.Operation) || currentMerge.Operation.Attempts != 0 || currentMerge.Operation.Work != nil {
		return errors.New("fresh external merge preparation changed expired operation or authority")
	}
	if after, err := db.AttemptAllowance(ctx, stored.Repository, stored.Issue); err != nil || !reflect.DeepEqual(after, allowanceAfter) {
		return errors.New("merge-only preparation changed inference allowance")
	}
	if after, err := db.Assignment(ctx, command); err != nil || !reflect.DeepEqual(after, stored) {
		return errors.New("merge-only preparation replayed external planning")
	}
	if after, err := db.PublicationByID(ctx, command); err != nil || !reflect.DeepEqual(after, promoted) {
		return errors.New("merge-only preparation changed external publication")
	}
	if after, err := db.CandidatePreparation(ctx, preparation.Preparation.ID); err != nil || !reflect.DeepEqual(after, afterPromotion) {
		return errors.New("merge-only preparation renewed source preparation")
	}
	if replay, err := db.PrepareUnsentMerge(ctx, mergeCommand, "declared-merge-only", offer, time.Now()); err != nil || replay != preparedMerge {
		return fmt.Errorf("external merge-only command did not replay: %v", err)
	}
	// Declare a separate retained direct-adoption receipt using the same real
	// fixture Architect provenance. This owner never had a Builder or model run.
	directID := factory.NewID()
	directOrigin := *promoted.External
	directOrigin.CommandID, directOrigin.Automatic = directID, false
	directOrigin.Target.HeadOID = strings.Repeat("d", 40)
	direct := factory.Assignment{ID: directID, AttemptRoot: directID, PublicationAssignment: directID, ProjectID: stored.ProjectID, Role: stored.Role, Authority: stored.Authority, ActorID: stored.ActorID, Acceptance: stored.Acceptance, Preparation: stored.Preparation, SourceCommit: stored.SourceCommit, Stage: factory.AssignmentRetained, Repository: stored.Repository, Issue: stored.Issue, NativeRev: stored.NativeRev, CreatedUnix: time.Now().Unix(), Subject: stored.Subject, ExternalPublication: &factory.ExternalPublicationOwner{PlanAssignment: stored.ID, Connection: stored.Connection, Origin: directOrigin}}
	directPublication := promoted
	directPublication.ID, directPublication.AssignmentID, directPublication.External = directID, directID, &directOrigin
	directPublication.PRNumber, directPublication.PRID, directPublication.Candidate = 4, 14, directOrigin.Target.HeadOID
	if direct.Validate() != nil || directPublication.Validate() != nil {
		return errors.New("declared direct external owner or publication is invalid")
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'','retained',0,$4)`, direct, directID, direct.Repository, direct.Issue); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'','published',0,$4)`, directPublication, directID, direct.Repository, direct.Issue); err != nil {
		return err
	}
	directAllowance := factory.AttemptAllowance{RootAssignment: directID, Repository: direct.Repository, Issue: direct.Issue, Limits: request.Limits, CheckpointUnix: time.Now().Unix()}
	if err = write(`INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,0,$4)`, directAllowance, direct.Repository, direct.Issue, directID); err != nil {
		return err
	}
	directMerge := merge
	directMerge.ID, directMerge.PublicationID, directMerge.AssignmentID = factory.NewID(), directID, directID
	directMerge.PRNumber, directMerge.PRID, directMerge.HeadOID, directMerge.Authorization = directPublication.PRNumber, directPublication.PRID, directPublication.Candidate, factory.MergeAuthorization{}
	if err = db.RecordMerge(ctx, directMerge); err != nil {
		return err
	}
	directMerge.Authorization = merge.Authorization
	directMerge.Authorization.Generation.PublicationID, directMerge.Authorization.Generation.AttemptRoot = directID, directID
	directMerge.Authorization.Generation.NativeOperationID, directMerge.Authorization.Generation.Head = factory.MergeGenerationOperationID(directID, 1), directMerge.HeadOID
	intent := factory.MergeIntent{OperationID: factory.MergeOperationIDForGeneration(directID, 1), AuthRevision: "declared expired allocated generation", Repository: directMerge.Repository, ActorID: directMerge.ReviewerID, PRNumber: directMerge.PRNumber, PRID: directMerge.PRID, IssueID: directMerge.IssueID, PRAuthorID: directMerge.PRAuthorID, ReviewerID: directMerge.ReviewerID, HeadRef: directMerge.HeadRef, BaseRef: directMerge.BaseRef, HeadOID: directMerge.HeadOID, BaseOID: directMerge.BaseOID, ReviewEvent: "COMMENT", ReviewID: 1, NativeRev: revision, AssessmentRevision: 1, NotAfter: directMerge.Authorization.Generation.NotAfter}
	directMerge.Operation = factory.MergeOperation{Kind: factory.OpMerge, Work: &intent, OperationID: intent.OperationID, Attempts: 1, InstallationID: "declared", ActorID: intent.ActorID, RepositoryID: intent.Repository, Effect: factory.OpEffectNotCommitted, Cancellation: factory.OpCancelCancelled, Completion: factory.OpCompletionComplete, Reason: "cancelled_before_dispatch", UpdatedUnix: time.Now().Unix()}
	directMerge.WithdrawRequested = true
	directMerge.Settle(factory.MergeWithdrawn, factory.MergeReasonWithdrawn, time.Now().Unix())
	if err = directMerge.Validate(); err != nil {
		return fmt.Errorf("declared expired allocated no-send invalid: %w", err)
	}
	for _, uncertainty := range []bool{true, false} {
		negative := directMerge
		if uncertainty {
			negative.Stage, negative.Outcome, negative.Reason, negative.FinishedUnix = factory.MergeOpen, "", "", 0
			negative.Operation.Effect, negative.Operation.Cancellation, negative.Operation.Completion, negative.Operation.Reason = factory.OpEffectIndeterminate, factory.OpCancelIndeterminate, factory.OpCompletionNeedsIntervention, "dispatch_reserved"
		} else {
			live := intent
			live.NotAfter = time.Now().Add(time.Minute).Unix()
			negative.Operation.Work = &live
			negative.Authorization.Generation.NotAfter = live.NotAfter
		}
		if err = negative.Validate(); err != nil {
			return fmt.Errorf("declared cancellation refusal invalid: %w", err)
		}
		if err = write(`UPDATE factory_merges SET stage=$2,data=$3 WHERE publication=$1`, negative, directID, negative.Stage); err != nil {
			return err
		}
		if _, err = db.UnsentMergePreparation(ctx, directID); !errors.Is(err, store.ErrMergeConflict) {
			return fmt.Errorf("unknown or unexpired cancellation offered replacement: uncertainty=%v error=%v", uncertainty, err)
		}
	}
	if err = write(`UPDATE factory_merges SET stage=$2,data=$3 WHERE publication=$1`, directMerge, directID, directMerge.Stage); err != nil {
		return err
	}
	directOffer, err := db.UnsentMergePreparation(ctx, directID)
	if err != nil || !directOffer.RetryNoSend || directOffer.MergeID != directMerge.ID {
		return fmt.Errorf("direct external owner merge-only preparation unavailable: %+v %v", directOffer, err)
	}
	wakeCoordinator := control.NewCoordinator(db, discovery, discovery)
	wakeCoordinator.Merges = progressMergeObserver{lookup: func(id string) (factory.OperationOutcome, error) {
		if id != directMerge.Operation.OperationID {
			return factory.OperationOutcome{}, errors.New("declared merge lookup unavailable")
		}
		o := directMerge.Operation
		return factory.OperationOutcome{OperationID: o.OperationID, InstallationID: o.InstallationID, ActorID: o.ActorID, RepositoryID: o.RepositoryID, Kind: o.Kind, Effect: o.Effect, Cancellation: o.Cancellation, Completion: o.Completion, Reason: o.Reason}, nil
	}}
	woken := make(chan struct{}, 2)
	var judgmentRecoveries atomic.Int64
	var judgmentSettled atomic.Bool
	wakeCoordinator.JudgmentRecovery = func(context.Context) bool {
		judgmentRecoveries.Add(1)
		return judgmentSettled.Swap(false)
	}
	wakeCoordinator.CommentDrive = func(context.Context) {
		select {
		case woken <- struct{}{}:
		default:
		}
	}
	if err = wakeCoordinator.Start(ctx, filepath.Join(lockDir, "coordinator.lock")); err != nil {
		_ = wakeCoordinator.Close()
		return err
	}
	defer wakeCoordinator.Close()
	<-woken // startup completed before the fresh command
	initialRecoveries := judgmentRecoveries.Load()
	if receipt, err := wakeCoordinator.PrepareUnsentMerge(ctx, factory.NewID(), "declared-direct-merge", directOffer); err != nil || receipt.NotAfter <= time.Now().Unix() {
		return fmt.Errorf("direct external owner finite merge-only preparation failed: %v", err)
	}
	// Declare withdrawal on only the newly admitted disposable row. Its next
	// progress tick must settle that row without driving unrelated consumers.
	requested, err := db.MergeByPublication(ctx, directID)
	if err != nil {
		return err
	}
	requested.WithdrawRequested = true
	requested.Revision++
	if err = db.UpdateMerge(ctx, requested); err != nil {
		return err
	}
	for {
		current, err := db.MergeByPublication(ctx, directID)
		if err != nil {
			return err
		}
		if current.Stage == factory.MergeWithdrawn {
			if judgmentRecoveries.Load() <= initialRecoveries {
				return errors.New("local judgment recovery waited behind passive provider polling")
			}
			break
		}
		select {
		case <-woken:
			return errors.New("merge-only command woke unrelated comment/intake polling")
		case <-ctx.Done():
			return errors.New("fresh merge-only command waited behind passive provider polling")
		case <-time.After(20 * time.Millisecond):
		}
	}
	judgmentSettled.Store(true)
	select {
	case <-woken:
		fmt.Println("PASS: local judgment recovery runs during the passive hold and a newly settled receipt wakes consumers on the next worker tick, without polling unrelated providers while pending.")
	case <-time.After(15 * time.Second):
		return errors.New("settled judgment did not wake its consumers before passive polling")
	}
	if err = wakeCoordinator.Close(); err != nil {
		return err
	}
	fmt.Println("PASS: fresh merge-only command advances only its exact saved row on the next ten-second tick despite the five-minute passive hold; declared withdrawal settles without waking unrelated comment/intake polling, no provider/model.")
	if after, err := db.Assignment(ctx, directID); err != nil || !reflect.DeepEqual(after, direct) || after.Run != "" || after.Connection != "" || after.Result != nil {
		return errors.New("direct external merge preparation fabricated or changed an execution")
	}
	if after, err := db.AttemptAllowance(ctx, direct.Repository, direct.Issue); err != nil || !reflect.DeepEqual(after, directAllowance) {
		return errors.New("direct external merge preparation renewed inference allowance")
	}
	if after, err := db.MergeByPublication(ctx, directID); err != nil || len(after.History) != 1 || !reflect.DeepEqual(after.History[0].Authorization, directMerge.Authorization) || !reflect.DeepEqual(after.History[0].Operation, directMerge.Operation) || after.Operation.Attempts != 0 || !after.WithdrawRequested || after.Stage != factory.MergeWithdrawn {
		return errors.New("fresh explicit no-send preparation changed its original cancelled generation")
	}
	fmt.Println("PASS: real PostgreSQL fresh explicit merge-only command recovers an expired allocated causal cancellation with its inference allowance still open; unknown or unexpired withdrawals refuse, original work/deadline/cancellation are archived exactly, allowance and external owner unchanged, no model/provider.")
	// Separate declared history reproduces a completed fork Architect whose
	// command-owned source is still ready. Never reopen the retired source above.
	fork, err := db.ObserveCollaborationRepository(ctx, host.ID, "declared-external-fork", "fixture/external-fork")
	if err != nil {
		return err
	}
	handoffOwner := stored
	handoffOwner.ID, handoffOwner.Run, handoffOwner.Preparation = factory.NewID(), factory.NewID(), "f"+factory.NewID()[:24]
	handoffOwner.AttemptRoot, handoffOwner.PublicationAssignment, handoffOwner.RunHistory = handoffOwner.ID, handoffOwner.ID, []string{handoffOwner.Run}
	handoffPlanning := *stored.ExternalPlanning
	handoffPlanning.Request.Automatic, handoffPlanning.Request.ForkConsent = false, true
	handoffPlanning.Request.PRNumber, handoffPlanning.Request.PRID = 5, 15
	handoffPlanning.Request.Target.HeadRepository, handoffPlanning.Request.Target.HeadOID = int64(fork.ID), strings.Repeat("e", 40)
	handoffPlanning.Origin.CommandID, handoffPlanning.Origin.Automatic, handoffPlanning.Origin.ForkConsent = handoffOwner.ID, false, handoffOwner.ID
	handoffPlanning.Origin.Target = handoffPlanning.Request.Target
	handoffOwner.ExternalPlanning = &handoffPlanning
	handoffPlan := plan
	handoffPlan.AttemptRoot, handoffPlan.Preparation = handoffOwner.ID, handoffOwner.Preparation
	handoffPlan.Digest, err = factory.PlanDigest(handoffPlan)
	if err != nil {
		return err
	}
	handoffOwner.Result = &factory.AssignmentResult{AssignmentID: handoffOwner.ID, RunID: handoffOwner.Run, Status: "completed", Summary: "Declared completed fork Architect", Reported: true, RecordedUnix: handoffOwner.FinishedUnix, Phase: project.ExecutionInvestigate, PhaseResult: &factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed", Summary: "Declared completed fork Architect", Output: &factory.PhaseOutput{Plan: &handoffPlan, PlanReview: strings.Repeat("d", 64)}}}
	if err = handoffOwner.Validate(); err != nil {
		return err
	}
	handoffSource := preparation
	handoffSource.Preparation.ID, handoffSource.State.ID = handoffOwner.Preparation, handoffOwner.Preparation
	handoffNative := *preparation.Native
	handoffNative.WorktreeID = handoffOwner.Preparation
	handoffSource.Native, handoffSource.State.Native = &handoffNative, &handoffNative
	if _, _, err = db.AdmitPreparation(ctx, handoffSource); err != nil {
		return err
	}
	var handoffMeta map[string]any
	var sourceMeta []byte
	if err = raw.QueryRowContext(ctx, `SELECT data->'factory_admission' FROM project_preparations WHERE id=$1`, preparation.Preparation.ID).Scan(&sourceMeta); err != nil {
		return err
	}
	if err = json.Unmarshal(sourceMeta, &handoffMeta); err != nil {
		return err
	}
	handoffMeta["owner_assignment"], handoffMeta["child_assignment"], handoffMeta["attempt_root"] = handoffOwner.ID, handoffOwner.ID, handoffOwner.ID
	handoffCapture := handoffPlanning
	handoffCapture.CheckedUnix = 0
	handoffMeta["external_planning"], handoffMeta["not_after_unix"], handoffMeta["stopped"], handoffMeta["retirement"] = handoffCapture, time.Now().Add(time.Minute).Unix(), false, ""
	if err = write(`UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission}',$2::jsonb) WHERE id=$1`, handoffMeta, handoffOwner.Preparation); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,'finished',0,$5)`, handoffOwner, handoffOwner.ID, handoffOwner.Repository, handoffOwner.Issue, handoffOwner.Run); err != nil {
		return err
	}
	if err = db.SaveInvestigationPlan(ctx, handoffPlan); err != nil {
		return err
	}
	if err = db.AdvancePlanHead(ctx, handoffOwner.ID, "", handoffPlan.ID); err != nil {
		return err
	}
	handoffRunNative := *finishedRun.Native
	handoffRunNative.Provenance.WorktreeID = handoffOwner.Preparation
	handoffRunNative.Intent, handoffRunNative.Provenance.BrokerRun = "", factory.NewID()
	handoffRun := factory.Run{ID: handoffOwner.Run, ProjectID: handoffOwner.ProjectID, Role: handoffOwner.Role, InputSHA: handoffOwner.SourceCommit, Started: finishedRun.Started, Deadline: finishedRun.Deadline, Harness: handoffOwner.Harness, Model: handoffOwner.Model, Outcome: factory.Succeeded, Reconciled: true, Native: &handoffRunNative}
	handoffAdmission, handoffLaunch := *finishedRun.Admission, *finishedRun.Admission.Native
	handoffLaunch.Workspace, handoffLaunch.BrokerRun = *handoffSource.Native, handoffRunNative.Provenance.BrokerRun
	handoffAdmission.Native, handoffRun.Admission = &handoffLaunch, &handoffAdmission
	if err = handoffRun.Validate(); err != nil || !handoffRun.Native.Settled() {
		return fmt.Errorf("declared external Architect retirement differs: %v", err)
	}
	if err = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, handoffRun, handoffRun.ID); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: handoffRun.ID, Repository: handoffOwner.Repository, Issue: handoffOwner.Issue, Attempt: handoffOwner.ID}); err != nil {
		return err
	}
	handoffPublication := promoted
	handoffPublication.ID, handoffPublication.AssignmentID, handoffPublication.Preparation, handoffPublication.External = handoffOwner.ID, handoffOwner.ID, handoffOwner.Preparation, &handoffPlanning.Origin
	handoffPublication.PRNumber, handoffPublication.PRID, handoffPublication.Candidate = handoffPlanning.Request.PRNumber, handoffPlanning.Request.PRID, handoffPlanning.Request.Target.HeadOID
	reviewerID := factory.NewID()
	cycle := factory.ReviewCycle{PublicationID: handoffPublication.ID, AttemptRoot: handoffOwner.ID, Phase: "review", Round: 1, TriggerDecision: "initial-review", TriggerReason: "Declared first fork review", Head: handoffPublication.Candidate, BaseOID: base, FindingDigest: strings.Repeat("f", 64), ReviewerAssignment: reviewerID}
	cycle.ID = factory.ReviewCycleID(cycle.PublicationID, cycle.AttemptRoot, cycle.Round, cycle.Phase)
	handoffPublication.ReviewCycles = []factory.ReviewCycle{cycle}
	if err = handoffPublication.Validate(); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'','published',0,$4)`, handoffPublication, handoffOwner.ID, handoffOwner.Repository, handoffOwner.Issue); err != nil {
		return err
	}
	handoffAllowance := factory.AttemptAllowance{RootAssignment: handoffOwner.ID, Repository: handoffOwner.Repository, Issue: handoffOwner.Issue, Limits: request.Limits, CheckpointUnix: time.Now().Unix()}
	if err = write(`INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,0,$4)`, handoffAllowance, handoffOwner.Repository, handoffOwner.Issue, handoffOwner.ID); err != nil {
		return err
	}
	if _, exhausted, err := db.ChargeReviewDispatch(ctx, handoffOwner.Repository, handoffOwner.Issue, handoffOwner.ID, reviewerID, handoffPublication.Candidate, false); err != nil || exhausted {
		return fmt.Errorf("declared review allocation failed: %v", err)
	}
	handoffAllowance, err = db.AttemptAllowance(ctx, handoffOwner.Repository, handoffOwner.Issue)
	if err != nil {
		return err
	}
	reviewerPrep := execution.PreparationRecord{Preparation: handoffSource.Preparation}
	reviewerPrep.Preparation.ID, reviewerPrep.Preparation.Role, reviewerPrep.Preparation.SourceCommit = "f"+factory.NewID()[:24], project.RoleReviewer, handoffPublication.Candidate
	reviewerNative := handoffNative
	reviewerNative.WorktreeID = reviewerPrep.Preparation.ID
	reviewerPrep.Native = &reviewerNative
	reviewerRegistration := store.CandidatePreparationRegistration{Repository: handoffOwner.Repository, Issue: handoffOwner.Issue, ActorID: handoffOwner.ActorID, Project: handoffOwner.ProjectID, OwnerAssignment: handoffOwner.ID, ChildAssignment: reviewerID, AttemptRoot: handoffOwner.ID, PublicationID: handoffPublication.ID, Connection: handoffOwner.Connection, Authority: authority, GateRevision: gate, Control: controlRow, RequestedNotAfter: time.Now().Add(time.Minute).Unix(), SourcePull: handoffPublication.PullSource()}
	if _, _, err = db.AdmitCandidatePreparation(ctx, reviewerPrep, reviewerRegistration); !errors.Is(err, store.ErrRepositoryFull) {
		return fmt.Errorf("unretired Architect did not retain its repository slot: %v", err)
	}
	beforeHandoff, err := db.CandidatePreparation(ctx, handoffOwner.Preparation)
	if err != nil {
		return err
	}
	handoffHost := &builderRetirementHost{state: handoffSource.State}
	handoffCoordinator := control.NewCoordinator(db, handoffHost, handoffHost)
	handoffCoordinator.Publication = factoryruntime.NativePublication{}
	handoffReport := handoffCoordinator.PublishPass(ctx)
	handoffReplay := handoffCoordinator.PublishPass(ctx)
	if err = handoffCoordinator.Close(); err != nil {
		return err
	}
	retiredSource, err := db.CandidatePreparation(ctx, handoffOwner.Preparation)
	if err != nil || handoffHost.stops != 1 || !retiredSource.Stopped || retiredSource.Retirement != "confirmed" || retiredSource.NotAfter != beforeHandoff.NotAfter || !reflect.DeepEqual(retiredSource.Admission, beforeHandoff.Admission) {
		return fmt.Errorf("external Architect source did not retire once under original custody/deadline: stops=%d reports=%+v/%+v %v", handoffHost.stops, handoffReport, handoffReplay, err)
	}
	if _, created, err := db.AdmitCandidatePreparation(ctx, reviewerPrep, reviewerRegistration); err != nil || !created {
		return fmt.Errorf("retired external Architect still blocked first reviewer admission: %v", err)
	}
	if after, err := db.AttemptAllowance(ctx, handoffOwner.Repository, handoffOwner.Issue); err != nil || !reflect.DeepEqual(after, handoffAllowance) {
		return errors.New("external source retirement changed existing review allocation or allowance")
	}

	// Continue the same fork history after a declared completed Inspector,
	// a confirmed retired unlaunched correction and a closed finite allowance.
	// Only the PostgreSQL admission is exercised; role/native/provider facts are
	// declared inputs, not a second Architect, inference or provider send.
	inspector := handoffOwner
	inspector.ID, inspector.Run, inspector.Preparation = reviewerID, factory.NewID(), reviewerPrep.Preparation.ID
	inspector.RunHistory = []string{inspector.Run}
	inspector.AttemptRoot, inspector.PublicationAssignment, inspector.ParentAssignment = handoffOwner.ID, handoffOwner.ID, handoffOwner.ID
	inspector.Phase, inspector.Role, inspector.SourceCommit, inspector.ExternalPlanning = project.ExecutionReview, project.RoleReviewer, handoffPublication.Candidate, nil
	inspector.ReviewContext = &factory.CurrentReviewContext{External: handoffPublication.External, Publication: factory.CurrentPublicationContext{ID: handoffPublication.ID, AssignmentID: handoffOwner.ID}, AdmittedTuple: factory.CandidateTuple{Head: handoffPublication.Candidate, Base: base, DiffBase: base, Revision: 1}, Cycle: cycle}
	finding := factory.Finding{Key: strings.Repeat("7", 32), OriginRun: inspector.Run, Severity: factory.FindingSeverityMajor, Actionable: true, FixKind: factory.FindingFixCode, Defect: "Declared fork fixture needs a bound", Evidence: "declared:fixture.txt", RequiredOutcome: "Add the missing bound"}
	inspector.Result = &factory.AssignmentResult{AssignmentID: inspector.ID, RunID: inspector.Run, Status: "completed", Summary: "Declared committed Inspector findings", Reported: true, RecordedUnix: inspector.FinishedUnix, Phase: project.ExecutionReview, PhaseResult: &factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed", Summary: "Declared committed Inspector findings", Output: &factory.PhaseOutput{Review: &factory.ReviewCycleReport{CycleID: cycle.ID, NewFindings: []factory.Finding{finding}}}}}
	if err = inspector.Validate(); err != nil {
		return fmt.Errorf("declared external Inspector: %w", err)
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,'finished',0,$5)`, inspector, inspector.ID, inspector.Repository, inspector.Issue, inspector.Run); err != nil {
		return err
	}
	inspectorRun := handoffRun
	inspectorRun.ID, inspectorRun.Role, inspectorRun.InputSHA = inspector.Run, inspector.Role, inspector.SourceCommit
	inspectorNative := *handoffRun.Native
	inspectorNative.Provenance.WorktreeID, inspectorNative.Provenance.BrokerRun = reviewerPrep.Native.WorktreeID, factory.NewID()
	inspectorRun.Native = &inspectorNative
	inspectorAdmission, inspectorLaunch := *handoffRun.Admission, *handoffRun.Admission.Native
	inspectorLaunch.Workspace, inspectorLaunch.BrokerRun = *reviewerPrep.Native, inspectorNative.Provenance.BrokerRun
	inspectorAdmission.Native, inspectorRun.Admission = &inspectorLaunch, &inspectorAdmission
	if err = inspectorRun.Validate(); err != nil {
		return fmt.Errorf("declared Inspector custody: %w", err)
	}
	if err = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, inspectorRun, inspectorRun.ID); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: inspectorRun.ID, Repository: inspector.Repository, Issue: inspector.Issue, Attempt: inspector.ID}); err != nil {
		return err
	}
	for _, observedRun := range []factory.Run{handoffRun, inspectorRun} {
		if err = db.RecordRunUsage(ctx, factory.Usage{RunID: observedRun.ID, Repository: handoffOwner.Repository, Connection: handoffOwner.Connection, Minutes: 1, StartedAt: observedRun.Started, EndedAt: time.Now()}); err != nil {
			return err
		}
	}
	if err = db.RecordCandidatePreparationStop(ctx, reviewerPrep.Preparation.ID, execution.PreparationState{ID: reviewerPrep.Preparation.ID, Project: grant.Project, Native: reviewerPrep.Native, Stopped: true, Retirement: "confirmed"}); err != nil {
		return err
	}
	work := factory.ReviewWork{OperationID: "review-" + inspector.Run, AuthRevision: factory.ReviewAuthRevision(handoffOwner.ID, inspector.Run), Repository: inspector.Repository, HeadRepository: int64(fork.ID), ActorID: inspector.ActorID, PRNumber: handoffPublication.PRNumber, PRID: handoffPublication.PRID, IssueID: handoffPublication.PullTarget().IssueID, PRAuthorID: handoffPublication.PullTarget().AuthorID, HeadRef: handoffPublication.PullTarget().HeadRef, BaseRef: handoffPublication.PullTarget().BaseRef, HeadOID: handoffPublication.Candidate, BaseOID: base, Event: "COMMENT", AgentVerdict: "REQUEST_CHANGES", Body: "Declared genuine Inspector receipt", NativeRev: 1, NotAfter: time.Now().Add(time.Minute).Unix()}
	handoffPublication.ReviewOperations = []factory.ReviewOperation{{RunID: inspector.Run, Work: work, Outcome: factory.OperationOutcome{OperationID: work.OperationID, InstallationID: "declared", Kind: factory.OpReviewSubmit, ActorID: work.ActorID, RepositoryID: work.Repository, Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, Receipt: []byte("declared committed Inspector COMMENT")}}}
	oldCorrectorID := factory.NewID()
	findingDigest, err := factory.FindingSetDigest([]string{finding.Key})
	if err != nil {
		return err
	}
	oldCorrectionCycle := cycle
	oldCorrectionCycle.ID, oldCorrectionCycle.Phase = factory.ReviewCycleID(handoffPublication.ID, handoffOwner.ID, 1, "correct"), "correct"
	oldCorrectionCycle.CorrectionAssignment, oldCorrectionCycle.FindingDigest = oldCorrectorID, findingDigest
	handoffPublication.ReviewCycles = append(handoffPublication.ReviewCycles, oldCorrectionCycle)
	if err = handoffPublication.Validate(); err != nil {
		return err
	}
	if err = write(`UPDATE factory_publications SET data=$2 WHERE assignment=$1`, handoffPublication, handoffOwner.ID); err != nil {
		return err
	}
	oldCorrectionPrep := execution.PreparationRecord{Preparation: handoffSource.Preparation}
	oldCorrectionPrep.Preparation.ID, oldCorrectionPrep.Preparation.SourceCommit = factory.NativeCorrectionPreparationID(oldCorrectorID), handoffPublication.Candidate
	oldCorrectionNative := handoffNative
	oldCorrectionNative.WorktreeID = oldCorrectionPrep.Preparation.ID
	oldCorrectionPrep.Native = &oldCorrectionNative
	oldCorrectionRegistration := reviewerRegistration
	oldCorrectionRegistration.ChildAssignment, oldCorrectionRegistration.SourcePull = oldCorrectorID, handoffPublication.PullSource()
	if _, _, err = db.AdmitCandidatePreparation(ctx, oldCorrectionPrep, oldCorrectionRegistration); err != nil {
		return fmt.Errorf("declared old unlaunched correction preparation: %w", err)
	}
	if err = db.RecordCandidatePreparationStop(ctx, oldCorrectionPrep.Preparation.ID, execution.PreparationState{ID: oldCorrectionPrep.Preparation.ID, Project: grant.Project, Native: oldCorrectionPrep.Native, Stopped: true, Retirement: "confirmed"}); err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,not_after_unix}',to_jsonb($2::bigint)) WHERE id=$1`, oldCorrectionPrep.Preparation.ID, time.Now().Add(-time.Second).Unix()); err != nil {
		return err
	}
	if _, err = db.Assignment(ctx, oldCorrectorID); !errors.Is(err, store.ErrNotFound) {
		return errors.New("unlaunched correction acquired an assignment")
	}
	// Seed the unrelated Inspector before funding: historical records cannot
	// become the newest assignment after a fresh owner has been admitted.
	unrelated := inspector
	unrelated.ID, unrelated.Run = factory.NewID(), factory.NewID()
	unrelated.RunHistory = []string{unrelated.Run}
	unrelatedContext := *inspector.ReviewContext
	unrelatedContext.Cycle.Round, unrelatedContext.Cycle.ReviewerAssignment = 2, unrelated.ID
	unrelatedContext.Cycle.ID = factory.ReviewCycleID(handoffPublication.ID, handoffOwner.ID, 2, "review")
	unrelated.ReviewContext = &unrelatedContext
	unrelatedFinding := finding
	unrelatedFinding.OriginRun = unrelated.Run
	unrelated.Result = &factory.AssignmentResult{AssignmentID: unrelated.ID, RunID: unrelated.Run, Status: "completed", Summary: "Declared unrelated historical Inspector", Reported: true, RecordedUnix: unrelated.FinishedUnix, Phase: project.ExecutionReview, PhaseResult: &factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed", Summary: "Declared unrelated historical Inspector", Output: &factory.PhaseOutput{Review: &factory.ReviewCycleReport{CycleID: unrelatedContext.Cycle.ID, NewFindings: []factory.Finding{unrelatedFinding}}}}}
	if err = unrelated.Validate(); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,'finished',0,$5)`, unrelated, unrelated.ID, unrelated.Repository, unrelated.Issue, unrelated.Run); err != nil {
		return err
	}
	unrelatedRun := inspectorRun
	unrelatedRun.ID = unrelated.Run
	if err = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, unrelatedRun, unrelatedRun.ID); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: unrelated.Run, Repository: unrelated.Repository, Issue: unrelated.Issue, Attempt: unrelated.ID}); err != nil {
		return err
	}
	if closed, err := db.CloseAttempt(ctx, handoffOwner.ID); err != nil || !closed {
		return fmt.Errorf("close declared external allowance: %v", err)
	}
	closedAllowance, err := db.AttemptAllowance(ctx, handoffOwner.Repository, handoffOwner.Issue)
	if err != nil {
		return err
	}
	usageBefore, err := db.UsageTotal(ctx, handoffOwner.Repository, handoffOwner.Connection)
	if err != nil || usageBefore != 2 {
		return fmt.Errorf("declared original exact-source charge: %d %v", usageBefore, err)
	}
	type retainedRow struct{ table, key, id, before string }
	originalRows := []retainedRow{
		{"factory_assignments", "id", handoffOwner.ID, ""}, {"factory_assignments", "id", inspector.ID, ""},
		{"factory_runs", "id", handoffRun.ID, ""}, {"factory_runs", "id", inspectorRun.ID, ""},
		{"factory_assignments", "id", unrelated.ID, ""}, {"factory_runs", "id", unrelated.Run, ""},
		{"factory_publications", "assignment", handoffOwner.ID, ""},
		{"project_preparations", "id", handoffOwner.Preparation, ""}, {"project_preparations", "id", reviewerPrep.Preparation.ID, ""}, {"project_preparations", "id", oldCorrectionPrep.Preparation.ID, ""},
		{"factory_attempt_allowances", "root_assignment", handoffOwner.ID, ""},
		{"factory_usage", "run", handoffRun.ID, ""}, {"factory_usage", "run", inspectorRun.ID, ""},
	}
	for i := range originalRows {
		row := &originalRows[i]
		if err = raw.QueryRowContext(ctx, "SELECT data::text FROM "+row.table+" WHERE "+row.key+"=$1", row.id).Scan(&row.before); err != nil {
			return err
		}
	}
	originalCommand, err := db.FactoryCommand(ctx, command)
	if err != nil {
		return err
	}
	funding, err := db.RetainedCandidateFundingOffer(ctx, handoffPublication.ID)
	if err != nil || !funding.CarryUnused || funding.PreviousRoot != handoffOwner.ID || funding.Connection != handoffOwner.Connection || funding.Requirements != requirement.ID || funding.Approval != approval.ID || funding.Acceptance != acceptance.ID {
		return fmt.Errorf("completed external Architect/Inspector continuation offer: %+v %v", funding, err)
	}
	freshCommand := factory.NewID()
	observed := *handoffPublication.External
	observed.CommandID, observed.ForkConsent, observed.Automatic, observed.ObservedUnix = freshCommand, freshCommand, false, time.Now().Unix()
	observed.ObservationRevision, err = db.ObserveNativeReview(ctx, handoffOwner.Repository, handoffPublication.PRNumber, observed.ObservationDigest)
	if err != nil {
		return err
	}
	for _, drift := range []string{"source", "head", "base"} {
		changedFunding := funding
		switch drift {
		case "source":
			changedFunding.Connection = factory.NewID()
		case "head":
			changedFunding.Candidate = strings.Repeat("1", 40)
		case "base":
			changedFunding.Base = strings.Repeat("2", 40)
		}
		changedCommand, changedOrigin := factory.NewID(), observed
		changedOrigin.CommandID, changedOrigin.ForkConsent = changedCommand, changedCommand
		if _, err = db.FundRetainedCandidateObserved(ctx, changedCommand, "declared-fork-continuation", changedFunding, &changedOrigin, time.Now()); err == nil {
			return fmt.Errorf("changed %s funded an external continuation", drift)
		}
	}
	receipt, err := db.FundRetainedCandidateObserved(ctx, freshCommand, "declared-fork-continuation", funding, &observed, time.Now())
	if err != nil {
		return fmt.Errorf("fresh external correction funding: %w", err)
	}
	freshOwner, err := db.Assignment(ctx, freshCommand)
	if err != nil || freshOwner.ExternalPublication == nil || freshOwner.ExternalPublication.AdoptedFrom != handoffPublication.ID || freshOwner.ExternalPublication.PlanAssignment != handoffOwner.ID || freshOwner.Run != "" {
		return fmt.Errorf("fresh external continuation fabricated execution provenance: %v", err)
	}
	freshPublication, err := db.PublicationByID(ctx, freshCommand)
	if err != nil || freshPublication.Run != "" || len(freshPublication.ReviewCycles)+len(freshPublication.ReviewOperations) != 0 {
		return fmt.Errorf("fresh continuation copied old cycles/receipts into its authority: %v", err)
	}
	freshAllowance, err := db.AttemptAllowance(ctx, handoffOwner.Repository, handoffOwner.Issue)
	if err != nil || freshAllowance.RootAssignment != freshCommand || freshAllowance.Closed || freshAllowance.Limits != closedAllowance.Limits || freshAllowance.ActiveSeconds != closedAllowance.ActiveSeconds || !reflect.DeepEqual(freshAllowance.Reviews, closedAllowance.Reviews) || len(freshAllowance.Corrections) != 0 {
		return fmt.Errorf("fresh external funding reset spent review or old charge: %+v %v", freshAllowance, err)
	}
	if replay, err := db.FundRetainedCandidateObserved(ctx, freshCommand, "declared-fork-continuation", funding, &observed, time.Now().Add(time.Hour)); err != nil || replay != receipt {
		return fmt.Errorf("funding replay changed or renewed original authority: %v", err)
	}
	if after, err := db.Assignment(ctx, freshCommand); err != nil || !reflect.DeepEqual(after, freshOwner) {
		return errors.New("funding replay renewed fresh owner authority")
	}
	if after, err := db.PublicationByID(ctx, freshCommand); err != nil || !reflect.DeepEqual(after, freshPublication) {
		return errors.New("funding replay renewed fresh publication observation")
	}
	if after, err := db.AttemptAllowance(ctx, handoffOwner.Repository, handoffOwner.Issue); err != nil || !reflect.DeepEqual(after, freshAllowance) {
		return errors.New("funding replay renewed the new finite allowance")
	}
	if _, err = db.AuthorizeCandidatePreparation(ctx, oldCorrectionPrep.Preparation.ID); err == nil {
		return errors.New("fresh funding renewed expired retired correction authority")
	}
	corrector := offlineRoleAssignment(project.ExecutionCorrect, project.RoleCoder, freshPublication.Candidate)
	corrector.AttemptRoot, corrector.PublicationAssignment, corrector.ParentAssignment = freshOwner.ID, freshOwner.ID, inspector.ID
	corrector.Repository, corrector.Issue, corrector.ProjectID, corrector.ActorID = handoffOwner.Repository, handoffOwner.Issue, handoffOwner.ProjectID, handoffOwner.ActorID
	corrector.Subject, corrector.Acceptance, corrector.Connection, corrector.Authority = handoffOwner.Subject, handoffOwner.Acceptance, handoffOwner.Connection, freshOwner.Authority
	corrector.Preparation, corrector.Harness, corrector.HarnessVers, corrector.Model, corrector.NativeTool = factory.NativeCorrectionPreparationID(corrector.ID), handoffOwner.Harness, handoffOwner.HarnessVers, handoffOwner.Model, &tool
	corrector.AcceptedContext = factory.CaptureAcceptedContext(factory.PromptInputs{Repository: corrector.Repository, Issue: corrector.Issue, NativeRev: corrector.NativeRev, AcceptanceID: corrector.Acceptance, SourceCommit: corrector.SourceCommit, Preparation: corrector.Preparation, Title: "Declared external correction", Body: finding.RequiredOutcome})
	correctionCycle := oldCorrectionCycle
	correctionCycle.PublicationID, correctionCycle.AttemptRoot, correctionCycle.CorrectionAssignment = freshPublication.ID, freshOwner.ID, corrector.ID
	correctionCycle.ID = factory.ReviewCycleID(freshPublication.ID, freshOwner.ID, 1, "correct")
	freshPublication.ReviewCycles = []factory.ReviewCycle{correctionCycle}
	corrector.ReviewContext = &factory.CurrentReviewContext{External: freshPublication.External, Publication: factory.CurrentPublicationContext{ID: freshPublication.ID, AssignmentID: freshOwner.ID}, AdmittedTuple: factory.CandidateTuple{Head: freshPublication.Candidate, Base: base, DiffBase: base, Revision: 1}, Cycle: correctionCycle, CarriedFindings: []factory.Finding{finding}}
	if err = freshPublication.Validate(); err != nil {
		return err
	}
	if err = write(`UPDATE factory_publications SET data=$2 WHERE assignment=$1`, freshPublication, freshOwner.ID); err != nil {
		return err
	}
	correctorPrep := execution.PreparationRecord{Preparation: oldCorrectionPrep.Preparation}
	correctorPrep.Preparation.ID = corrector.Preparation
	correctorNative := oldCorrectionNative
	correctorNative.WorktreeID = corrector.Preparation
	correctorPrep.Native = &correctorNative
	correctorRegistration := oldCorrectionRegistration
	correctorRegistration.OwnerAssignment, correctorRegistration.ChildAssignment, correctorRegistration.AttemptRoot, correctorRegistration.PublicationID = freshOwner.ID, corrector.ID, freshOwner.ID, freshPublication.ID
	correctorRegistration.Authority, correctorRegistration.SourcePull, correctorRegistration.RequestedNotAfter = freshOwner.Authority, freshPublication.PullSource(), time.Now().Add(time.Minute).Unix()
	if _, _, err = db.AdmitCandidatePreparation(ctx, correctorPrep, correctorRegistration); err != nil {
		return fmt.Errorf("fresh external Corrector preparation: %w", err)
	}
	correctorPrep.State = execution.PreparationState{ID: corrector.Preparation, Project: corrector.ProjectID, Role: corrector.Role, SourceCommit: corrector.SourceCommit, SetupDigest: correctorPrep.Preparation.SetupDigest, Phase: project.PrepareReady, Ready: true, Native: correctorPrep.Native, SourceBundleSHA256: strings.Repeat("f", 64), SetupExit: &zero, CheckExit: &zero}
	if err = db.ObservePreparation(ctx, correctorPrep); err != nil {
		return err
	}
	correctorRun := factory.Run{ID: corrector.Run, ProjectID: corrector.ProjectID, Role: corrector.Role, InputSHA: corrector.SourceCommit, Started: time.Now(), Deadline: time.Now().Add(30 * time.Second), Harness: corrector.Harness, Model: corrector.Model, ContextPending: true, Native: &execution.NativeRecord{Tool: tool, BundleSHA256: correctorPrep.State.SourceBundleSHA256, PromptSHA256: corrector.PromptSHA, Retirement: execution.NotStarted, Provenance: execution.NativeProvenance{TrustMode: "isolated_system_worker", Profile: profile.ID, InstanceID: correctorNative.InstanceID, WorktreeID: correctorNative.WorktreeID, BrokerRun: factory.NewID(), Custody: execution.NativeCustody{State: execution.NotAcquired}}}}

	// Unlike the Architect, this correct phase requires the same singleton
	// manifest envelope used by the existing controlled Fixer fixture.
	correctionPlan := handoffPlan
	correctionPlan.ID, correctionPlan.AttemptRoot, correctionPlan.Preparation = factory.NewID(), corrector.AttemptRoot, corrector.Preparation
	correctionPlan.SourceCommit, correctionPlan.IntegrationCommit = corrector.SourceCommit, corrector.SourceCommit
	correctionPlan.Tasks = []factory.PlanTask{{ID: "correct", Objective: finding.RequiredOutcome, Outputs: []string{"native-task.txt"}, AcceptanceRefs: correctionPlan.AcceptanceCoverage, Lane: "builder", OwnedPaths: []string{"native-task.txt"}, CheckIDs: []string{"integration"}}}
	correctionPlan.Lanes = []factory.PlanLane{{ID: "builder", TaskIDs: []string{"correct"}}}
	correctionPlan.Checks = []factory.CheckSpec{handoffPlan.Checks[len(handoffPlan.Checks)-1]}
	correctionPlan.Digest, err = factory.PlanDigest(correctionPlan)
	if err != nil {
		return err
	}
	correctionPlanBytes, err := json.Marshal(correctionPlan)
	if err != nil {
		return err
	}
	correctorProfile := policy.Roles[corrector.Role].Profile
	correctorRun.ExecutionManifest, err = project.MarshalExecutionManifest(project.ExecutionManifest{Plan: correctionPlanBytes, PlanDigest: project.PlanDigest(correctionPlanBytes), Invocation: correctorRun.Native.Provenance.BrokerRun, Phase: corrector.Phase, Run: corrector.Run, Deadline: correctorRun.Deadline, Effort: correctorProfile.Effort, Contributor: correctorProfile.Contributor, MaxChildren: 0})
	if err != nil {
		return err
	}
	correctorLaunch := execution.NativeLaunch{CredentialSHA256: strings.Repeat("a", 64), Workspace: correctorNative, Tool: tool, BundleSHA256: correctorPrep.State.SourceBundleSHA256, PromptSHA256: corrector.PromptSHA, Runtime: runtime.ID, PlacementGeneration: placement.Generation, TrustMode: correctorRun.Native.Provenance.TrustMode, BrokerRun: correctorRun.Native.Provenance.BrokerRun}
	correctorReservation := factory.Reservation{AssignmentID: corrector.ID, Repository: corrector.Repository, Connection: corrector.Connection, State: factory.ReservationHeld, PlannedMinutes: 1}
	correctorView := factory.RunView{RunID: corrector.Run, Repository: corrector.Repository, Issue: corrector.Issue, Attempt: corrector.ID}
	unrelatedChild := corrector
	unrelatedChild.ParentAssignment = unrelated.ID
	if err = db.RecordNativeDispatchPacket(ctx, controlRow, unrelatedChild, correctorReservation, correctorRun, correctorView, correctorLaunch); err == nil {
		return errors.New("unrelated historical Inspector admitted fresh Corrector")
	}
	if err = db.RecordNativeDispatchPacket(ctx, controlRow, corrector, correctorReservation, correctorRun, correctorView, correctorLaunch); err != nil {
		return fmt.Errorf("original committed Inspector did not admit fresh Corrector: %w", err)
	}
	if admittedCorrector, err := db.Assignment(ctx, corrector.ID); err != nil || admittedCorrector.ParentAssignment != inspector.ID || admittedCorrector.AttemptRoot != freshCommand || admittedCorrector.Preparation == oldCorrectionPrep.Preparation.ID {
		return errors.New("fresh Corrector replaced its original Inspector or reused retired preparation")
	}
	correctorCapability, err := control.BuildMCPCapability(corrector, correctorRun, correctorPrep)
	if err != nil {
		return err
	}
	if err = db.StageMCPCapability(ctx, correctorCapability); err != nil {
		return err
	}
	if err = db.SaveInvestigationPlan(ctx, correctionPlan); err != nil {
		return err
	}
	currentCorrector, err := db.FactoryCurrentContext(ctx, correctorCapability, time.Now())
	if err != nil || currentCorrector.Evidence.Availability != "complete" || currentCorrector.Role.Plan == nil || currentCorrector.Role.Plan.CurrentPlanHead == nil || currentCorrector.Role.Plan.CurrentPlanHead.PlanID != correctionPlan.ID || currentCorrector.Role.Review == nil || currentCorrector.Role.Review.External == nil {
		return fmt.Errorf("fresh Corrector required current context incomplete: %+v %v", currentCorrector.Evidence, err)
	}
	var syntheticPlanHead bool
	if err = raw.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_heads WHERE attempt_root=$1)`, freshOwner.ID).Scan(&syntheticPlanHead); err != nil || syntheticPlanHead {
		return errors.New("correction context fabricated an Architect plan head")
	}
	// Declare capability activation only. Task admission must reach the live
	// inspector for this fresh owner without executing the retired source or
	// requiring a fabricated Architect result/head on the continuation root.
	if _, err = raw.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state='active' WHERE id=$1`, correctorCapability.ID); err != nil {
		return err
	}
	activeCorrectorCapability := correctorCapability
	activeCorrectorCapability.State = factory.MCPCapabilityActive
	probeObserved := errors.New("declared live-observation boundary")
	inspections, commands := 0, 0
	taskProbe := factoryservice.WorkerTasks{Store: db,
		Inspect: func(context.Context, execution.RunAddress) (execution.RunObservation, error) {
			inspections++
			return execution.RunObservation{}, probeObserved
		},
		Command: func(context.Context, execution.TaskRequest) (execution.TaskReceipt, error) {
			commands++
			return execution.TaskReceipt{}, errors.New("unexpected native task send")
		}}
	if _, err = taskProbe.Execute(ctx, activeCorrectorCapability, "start_task", "correct"); !errors.Is(err, probeObserved) || inspections != 1 || commands != 0 {
		return fmt.Errorf("fresh correction task admission did not reach observation: %v", err)
	}
	if _, err = taskProbe.Workspace(ctx, activeCorrectorCapability, project.FactoryNativeTaskArguments{TaskID: "correct", Operation: "read", Path: "native-task.txt", Limit: 64}); !errors.Is(err, probeObserved) || inspections != 2 || commands != 0 {
		return fmt.Errorf("fresh correction workspace admission did not reach observation: %v", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state='staged' WHERE id=$1`, correctorCapability.ID); err != nil {
		return err
	}
	for _, row := range originalRows {
		var after string
		if err = raw.QueryRowContext(ctx, "SELECT data::text FROM "+row.table+" WHERE "+row.key+"=$1", row.id).Scan(&after); err != nil || after != row.before {
			return fmt.Errorf("fresh Corrector changed original %s/%s: %v", row.table, row.id, err)
		}
	}
	if after, err := db.FactoryCommand(ctx, command); err != nil || !reflect.DeepEqual(after, originalCommand) {
		return errors.New("fresh correction changed original external command receipt")
	}
	if after, err := db.UsageTotal(ctx, handoffOwner.Repository, handoffOwner.Connection); err != nil || after != usageBefore {
		return errors.New("fresh correction changed original settled exact-source charges")
	}
	// A positive prelaunch marker permits only an explicit startup no-start
	// acknowledgement, never inference or a second send of the recorded intent.
	pendingRun, err := db.FactoryRun(ctx, corrector.Run)
	if err != nil || !pendingRun.ContextPending || pendingRun.Native == nil || pendingRun.Native.Intent == "" {
		return errors.New("declared pending correction lacks its captured intent")
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{context_pending}','false') WHERE id=$1`, pendingRun.ID); err != nil {
		return err
	}
	if err = db.RecordNativeContextNoStart(ctx, profile.ID, pendingRun.Native.Intent); err == nil {
		return errors.New("cleared prelaunch marker authorized no-start settlement")
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{context_pending}','true') WHERE id=$1`, pendingRun.ID); err != nil {
		return err
	}
	changedIntent := strings.Replace(pendingRun.Native.Intent, pendingRun.ID, factory.NewID(), 1)
	if err = db.RecordNativeContextNoStart(ctx, profile.ID, changedIntent); err == nil {
		return errors.New("changed native intent authorized no-start settlement")
	}
	for _, probeKind := range []string{"acquired", "missing-zero-usage"} {
		probe := pendingRun
		probe.ID = factory.NewID()
		probeNative := *pendingRun.Native
		probe.Native = &probeNative
		probeIntent, e := execution.ParseNativeIntent(pendingRun.Native.Intent)
		if e != nil {
			return e
		}
		probeIntent.Run, probeIntent.Native.BrokerRun = probe.ID, factory.NewID()
		probe.Native.Provenance.BrokerRun = probeIntent.Native.BrokerRun
		probeManifest, e := project.ParseExecutionManifest(pendingRun.ExecutionManifest)
		if e != nil {
			return e
		}
		probeManifest.Run, probeManifest.Invocation = probe.ID, probeIntent.Native.BrokerRun
		probe.ExecutionManifest, e = project.MarshalExecutionManifest(probeManifest)
		if e != nil {
			return e
		}
		probeIntent.ManifestDigest = project.ExecutionManifestDigest(probe.ExecutionManifest)
		probe.Native.Intent, e = probeIntent.Encode()
		if e != nil {
			return e
		}
		probeAdmission := *pendingRun.Admission
		probeAdmission.Native = &probeIntent.Native
		probe.Admission = &probeAdmission
		if probeKind == "acquired" {
			probe.Native.Provenance.Custody = execution.NativeCustody{State: execution.Held, AcquisitionID: factory.NewID(), Connection: access.ProviderConnectionID(corrector.Connection), Generation: 1}
		} else {
			probe.Native.Retirement, probe.Native.Sealed, probe.Outcome, probe.Reconciled = execution.Retired, true, factory.Cancelled, true
		}
		if e = probe.Validate(); e != nil {
			return fmt.Errorf("declared no-start %s refusal probe: %w", probeKind, e)
		}
		if e = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, probe, probe.ID); e != nil {
			return e
		}
		if e = db.RecordNativeContextNoStart(ctx, profile.ID, probe.Native.Intent); e == nil {
			return fmt.Errorf("%s evidence authorized no-start settlement", probeKind)
		}
	}
	if err = db.RecordNativeContextNoStart(ctx, profile.ID, pendingRun.Native.Intent); err != nil {
		return fmt.Errorf("explicit pending-context no-start acknowledgement: %w", err)
	}
	retiredPending, err := db.FactoryRun(ctx, pendingRun.ID)
	if err != nil || !retiredPending.Native.Settled() || retiredPending.HasExecutionIdentity() || retiredPending.Outcome != factory.Cancelled || !retiredPending.Reconciled || !retiredPending.ContextPending ||
		retiredPending.Native.Intent != pendingRun.Native.Intent || !retiredPending.Deadline.Equal(pendingRun.Deadline) || !reflect.DeepEqual(retiredPending.Admission, pendingRun.Admission) || !bytes.Equal(retiredPending.ExecutionManifest, pendingRun.ExecutionManifest) {
		return fmt.Errorf("no-start acknowledgement changed its original authority: %v", err)
	}
	unusedUsage, err := db.RunUsage(ctx, pendingRun.ID)
	if err != nil || unusedUsage.Minutes != 0 || !unusedUsage.StartedAt.Equal(pendingRun.Started) || !unusedUsage.EndedAt.Equal(pendingRun.Started) {
		return errors.New("never-started correction acquired an inference charge")
	}
	// Declare the postcommit capability-retirement interruption, then replay
	// the same acknowledgement. Its terminal run and zero charge stay exact.
	if _, err = raw.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state='staged' WHERE id=$1`, pendingRun.ID); err != nil {
		return err
	}
	if err = db.RecordNativeContextNoStart(ctx, profile.ID, pendingRun.Native.Intent); err != nil {
		return err
	}
	if cap, err := db.GetMCPCapability(ctx, pendingRun.ID); err != nil || cap.State != factory.MCPCapabilityRevoked {
		return errors.New("no-start replay did not finish original capability retirement")
	}
	if _, finished := control.AccountSettledRun(ctx, db, retiredPending, time.Now()); !finished {
		return errors.New("no-start correction did not finish through normal accounting")
	}
	if err = db.RecordCandidatePreparationStop(ctx, correctorPrep.Preparation.ID, execution.PreparationState{ID: correctorPrep.Preparation.ID, Project: grant.Project, Native: correctorPrep.Native, Stopped: true, Retirement: "confirmed"}); err != nil {
		return err
	}
	spentBefore, err := db.AttemptAllowance(ctx, corrector.Repository, corrector.Issue)
	if err != nil || len(spentBefore.Corrections) != 1 || spentBefore.Corrections[0] != corrector.ID {
		return errors.New("no-start settlement erased the admitted correction identity")
	}
	nextFunding, err := db.RetainedCandidateFundingOffer(ctx, freshPublication.ID)
	if err != nil || nextFunding.CarryUnused || nextFunding.PreviousRoot != freshOwner.ID {
		return fmt.Errorf("definitive no-start correction did not offer fresh bounded funding: %w", err)
	}
	nextCommand := factory.NewID()
	nextOrigin := *freshPublication.External
	nextOrigin.CommandID, nextOrigin.ForkConsent, nextOrigin.ObservedUnix = nextCommand, nextCommand, time.Now().Unix()
	if _, err = db.FundRetainedCandidateObserved(ctx, nextCommand, "declared-no-start-continuation", nextFunding, &nextOrigin, time.Now()); err != nil {
		return fmt.Errorf("fresh bounded command after definitive no-start: %w", err)
	}
	var priorAllowanceBytes []byte
	if err = raw.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, freshOwner.ID).Scan(&priorAllowanceBytes); err != nil {
		return err
	}
	var spentAfter factory.AttemptAllowance
	if json.Unmarshal(priorAllowanceBytes, &spentAfter) != nil || !spentAfter.Closed || !reflect.DeepEqual(spentBefore.Corrections, spentAfter.Corrections) || spentAfter.Limits != spentBefore.Limits {
		return errors.New("new funding renewed the old spent correction authority")
	}
	if after, err := db.FactoryRun(ctx, pendingRun.ID); err != nil || !reflect.DeepEqual(after, retiredPending) {
		return errors.New("fresh funding rewrote the original no-start run")
	}
	// Declare a later, fully accounted failed Corrector on the next explicit
	// root. Its partial workspace remains retained; no report or publication
	// operation has accepted a replacement for the still-published head.
	failedPublication, err := db.PublicationByID(ctx, nextCommand)
	if err != nil {
		return err
	}
	failedChild := corrector
	failedChild.ID, failedChild.Run = factory.NewID(), factory.NewID()
	failedChild.AttemptRoot, failedChild.PublicationAssignment = nextCommand, nextCommand
	failedChild.RunHistory = []string{failedChild.Run}
	failedChild.Stage, failedChild.Outcome, failedChild.Reason, failedChild.FinishedUnix = factory.AssignmentFinished, factory.NeedsHuman, factory.AssignReasonNoReport, time.Now().Unix()
	failedCycle := correctionCycle
	failedCycle.PublicationID, failedCycle.AttemptRoot, failedCycle.CorrectionAssignment = nextCommand, nextCommand, failedChild.ID
	failedCycle.ID = factory.ReviewCycleID(nextCommand, nextCommand, failedCycle.Round, failedCycle.Phase)
	failedContext := *corrector.ReviewContext
	failedContext.External, failedContext.Publication, failedContext.Cycle = failedPublication.External, factory.CurrentPublicationContext{ID: nextCommand, AssignmentID: nextCommand}, failedCycle
	failedChild.ReviewContext = &failedContext
	failedChild.Result = &factory.AssignmentResult{AssignmentID: failedChild.ID, RunID: failedChild.Run, Phase: project.ExecutionCorrect, Status: "blocked", Summary: "Declared missing native task binding", RecordedUnix: failedChild.FinishedUnix}
	if err = failedChild.Validate(); err != nil {
		return fmt.Errorf("declared failed Corrector: %w", err)
	}
	failedRun := retiredPending
	failedRun.ID, failedRun.ContextPending, failedRun.Outcome = failedChild.Run, false, factory.Failed
	failedNative := *retiredPending.Native
	failedRun.Native = &failedNative
	failedIntent, err := execution.ParseNativeIntent(failedNative.Intent)
	if err != nil {
		return err
	}
	failedIntent.Run, failedIntent.Native.BrokerRun = failedRun.ID, factory.NewID()
	failedNative.Provenance.BrokerRun = failedIntent.Native.BrokerRun
	failedPlan := correctionPlan
	failedPlan.ID, failedPlan.AttemptRoot = factory.NewID(), nextCommand
	failedPlan.Digest, err = factory.PlanDigest(failedPlan)
	if err != nil {
		return err
	}
	failedPlanBytes, _ := json.Marshal(failedPlan)
	failedManifest, err := project.ParseExecutionManifest(failedRun.ExecutionManifest)
	if err != nil {
		return err
	}
	failedManifest.Plan, failedManifest.PlanDigest, failedManifest.Run, failedManifest.Invocation = failedPlanBytes, project.PlanDigest(failedPlanBytes), failedRun.ID, failedIntent.Native.BrokerRun
	failedRun.ExecutionManifest, err = project.MarshalExecutionManifest(failedManifest)
	if err != nil {
		return err
	}
	failedIntent.ManifestDigest = project.ExecutionManifestDigest(failedRun.ExecutionManifest)
	failedNative.Intent, err = failedIntent.Encode()
	if err != nil {
		return err
	}
	failedAdmission := *retiredPending.Admission
	failedAdmission.Native = &failedIntent.Native
	failedRun.Admission = &failedAdmission
	failedStop := failedRun.Started.Add(time.Second)
	failedExit := 1
	failedNative.Provenance.Isolation = &execution.NativeIsolation{DesktopUID: 1000, WorkerUID: 1001}
	failedNative.Provenance.Process = &execution.NativeProcess{InvocationID: factory.NewID(), SystemInstance: "12345678-1234-1234-1234-123456789abc", StartIdentity: "1", PID: 123, StartedAt: failedRun.Started, StoppedAt: &failedStop, Executable: tool.Executable, ExecutableSHA256: tool.SHA256, ExitCode: &failedExit}
	if err = failedRun.Validate(); err != nil {
		return fmt.Errorf("declared failed Corrector custody: %w", err)
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,'finished',0,$5)`, failedChild, failedChild.ID, failedChild.Repository, failedChild.Issue, failedChild.Run); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, failedRun, failedRun.ID); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: failedRun.ID, Repository: failedChild.Repository, Issue: failedChild.Issue, Attempt: failedChild.ID}); err != nil {
		return err
	}
	failedUsage := factory.Usage{RunID: failedRun.ID, Repository: failedChild.Repository, Connection: failedChild.Connection, Minutes: 1, StartedAt: failedRun.Started, EndedAt: failedStop}
	if err = db.RecordRunUsage(ctx, failedUsage); err != nil {
		return err
	}
	failedPublication.ReviewCycles = []factory.ReviewCycle{failedCycle}
	failedPublication.Stage, failedPublication.Outcome, failedPublication.Reason, failedPublication.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, time.Now().Unix()
	if err = failedPublication.Validate(); err != nil {
		return err
	}
	if err = write(`UPDATE factory_publications SET data=$2 WHERE assignment=$1`, failedPublication, nextCommand); err != nil {
		return err
	}
	failedAllowance, err := db.AttemptAllowance(ctx, failedChild.Repository, failedChild.Issue)
	if err != nil {
		return err
	}
	failedAllowance.Corrections = []string{failedChild.ID}
	failedAllowance.Revision++
	if err = write(`UPDATE factory_attempt_allowances SET data=$2,revision=revision+1 WHERE root_assignment=$1`, failedAllowance, nextCommand); err != nil {
		return err
	}
	if closed, err := db.CloseAttempt(ctx, nextCommand); err != nil || !closed {
		return fmt.Errorf("close failed Corrector allowance: %v", err)
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET settled=false WHERE id=$1`, failedRun.ID); err != nil {
		return err
	}
	if _, err = db.RetainedCandidateFundingOffer(ctx, nextCommand); err == nil {
		return errors.New("unsettled failed Corrector offered fresh funding")
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_runs SET settled=true WHERE id=$1`, failedRun.ID); err != nil {
		return err
	}
	unknownPublication := failedPublication
	unknownIntent := factory.PublicationIntent{OperationID: factory.PublicationOperationID(nextCommand, factory.OpRefPublish, 2), AuthRevision: factory.AuthRevisionFor(nextCommand, nextCommand, failedPublication.Revision), TargetBranch: failedPublication.TargetBranch, Candidate: strings.Repeat("1", 40), ExpectedOld: failedPublication.Candidate, ComparisonRef: failedPublication.TargetBranch, ComparisonOID: failedPublication.BaseSHA, PRTitle: "Declared uncertain correction", Repository: failedPublication.Repository, ActorID: failedChild.ActorID, NativeRev: 1, NotAfter: time.Now().Add(time.Minute).Unix(), CorrectionNumber: failedPublication.PRNumber, CorrectionAuthor: failedPublication.PullTarget().AuthorID}
	unknownPublication.Corrections = factory.CorrectionOps{{Kind: factory.OpRefPublish, RunID: failedRun.ID, Work: &unknownIntent, OperationID: unknownIntent.OperationID, Attempts: 1, UpdatedUnix: time.Now().Unix()}}
	if err = unknownPublication.Validate(); err != nil {
		return err
	}
	if err = write(`UPDATE factory_publications SET data=$2 WHERE assignment=$1`, unknownPublication, nextCommand); err != nil {
		return err
	}
	if _, err = db.RetainedCandidateFundingOffer(ctx, nextCommand); err == nil {
		return errors.New("uncertain failed-Corrector publication offered replacement authority")
	}
	if err = write(`UPDATE factory_publications SET data=$2 WHERE assignment=$1`, failedPublication, nextCommand); err != nil {
		return err
	}
	failedOffer, err := db.RetainedCandidateFundingOffer(ctx, nextCommand)
	if err != nil || failedOffer.CarryUnused {
		return fmt.Errorf("accounted failed Corrector did not offer fresh finite funding: %v", err)
	}
	retryCommand, retryOrigin := factory.NewID(), *failedPublication.External
	retryOrigin.CommandID, retryOrigin.ForkConsent, retryOrigin.ObservedUnix = retryCommand, retryCommand, time.Now().Unix()
	if _, err = db.FundRetainedCandidateObserved(ctx, retryCommand, "declared-failed-corrector-continuation", failedOffer, &retryOrigin, time.Now()); err != nil {
		return fmt.Errorf("explicit fresh funding after failed Corrector: %w", err)
	}
	if after, err := db.FactoryRun(ctx, failedRun.ID); err != nil || !reflect.DeepEqual(after, failedRun) {
		return errors.New("failed-Corrector funding rewrote retained workspace/capture/deadline")
	}
	if after, err := db.RunUsage(ctx, failedRun.ID); err != nil || !reflect.DeepEqual(after, failedUsage) {
		return errors.New("failed-Corrector funding changed its original charge")
	}
	// Keep this ordinary Builder Retry on a separate issue control, after the
	// external journey. The role can record a blocked missing report before
	// its independently observed native process fails.
	retryAcceptance := acceptance
	retryAcceptance.ID, retryAcceptance.IssueIndex = "d"+factory.NewID()[:24], "2"
	if err = db.AdmitAcceptanceDecision(ctx, retryAcceptance); err != nil {
		return err
	}
	if _, _, err = db.RecordIssueAssessment(ctx, factory.IssueControl{Repository: int64(repo.ID), Issue: 2, Acceptance: retryAcceptance.ID, NativeRev: 1, Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible, Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64)}, time.Now()); err != nil {
		return err
	}
	retryOwner := offlineRoleAssignment(project.ExecutionImplement, project.RoleCoder, base)
	retryOwner.Repository, retryOwner.Issue, retryOwner.ProjectID, retryOwner.ActorID = int64(repo.ID), 2, grant.Project, int64(actor.ID)
	retryOwner.Acceptance, retryOwner.Subject.Issue.Acceptance, retryOwner.Subject.Issue.Issue = retryAcceptance.ID, retryAcceptance.ID, 2
	retryOwner.Connection, retryOwner.Authority = string(account.ID), authority
	retryOwner.Stage, retryOwner.Outcome, retryOwner.Reason, retryOwner.FinishedUnix = factory.AssignmentFinished, factory.NeedsHuman, factory.AssignReasonNoReport, time.Now().Unix()
	retryOwner.Result = &factory.AssignmentResult{AssignmentID: retryOwner.ID, RunID: retryOwner.Run, Status: "blocked", Summary: "Declared missing Builder report before native failure", RecordedUnix: retryOwner.FinishedUnix}
	if err = retryOwner.Validate(); err != nil {
		return err
	}
	if err = write(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,2,$3,'finished',0,$4)`, retryOwner, retryOwner.ID, repo.ID, retryOwner.Run); err != nil {
		return err
	}
	retryAllowance := oldAllowance
	retryAllowance.RootAssignment, retryAllowance.Issue = retryOwner.ID, 2
	if err = write(`INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,2,$2,0,$3)`, retryAllowance, repo.ID, retryOwner.ID); err != nil {
		return err
	}
	retryRun := oldRun
	retryRun.ID = retryOwner.Run
	if err = write(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,false,true,$2)`, retryRun, retryRun.ID); err != nil {
		return err
	}
	if _, _, err = db.RecordFactoryRunView(ctx, factory.RunView{RunID: retryRun.ID, Repository: retryOwner.Repository, Issue: 2, Attempt: retryOwner.ID}); err != nil {
		return err
	}
	retryReservation := factory.Reservation{AssignmentID: retryOwner.ID, Repository: retryOwner.Repository, Connection: retryOwner.Connection, State: factory.ReservationReleased, PlannedMinutes: 1}
	if err = write(`INSERT INTO factory_reservations(assignment,repository,connection,state,data) VALUES($1,$2,$3,'released',$4)`, retryReservation, retryOwner.ID, retryOwner.Repository, retryOwner.Connection); err != nil {
		return err
	}
	// Compare the retained baseline, not time.Now's process-local monotonic
	// clock/location metadata, which is intentionally absent from JSONB.
	retryRun, err = db.FactoryRun(ctx, retryRun.ID)
	if err != nil {
		return err
	}
	retryPayload, _ := json.Marshal(map[string]string{"prior_run": retryRun.ID, "prior_outcome": string(retryRun.Outcome)})
	missingReportRetry := factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "run/" + retryRun.ID, Principal: "declared-owner", Payload: string(retryPayload)}
	missingReportRetry.Digest = factory.SettingsDigest(missingReportRetry.Type, missingReportRetry.Target, missingReportRetry.Payload)
	missingReportDecision, err := missingReportRetry.RetryDecision()
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		storedRetry, created, err := db.RecordExplicitRetryCommand(ctx, missingReportRetry, missingReportDecision, retryOwner.Repository, retryOwner.Issue, retryOwner.Acceptance, time.Now())
		if err != nil || created != (attempt == 0) || storedRetry.ID != missingReportRetry.ID || storedRetry.Finished == "" {
			return fmt.Errorf("missing-report Builder/native-failed Retry admission/replay: %v", err)
		}
	}
	if after, err := db.Assignment(ctx, retryOwner.ID); err != nil || !reflect.DeepEqual(after, retryOwner) {
		return errors.New("explicit Builder Retry changed the original role result")
	}
	if after, err := db.FactoryRun(ctx, retryRun.ID); err != nil || !reflect.DeepEqual(after, retryRun) {
		return errors.New("explicit Builder Retry changed the original native outcome/deadline")
	}
	// Exercise only the real registration transaction. Candidate/native facts
	// are declared; no connector, model or provider send is configured here.
	correctionRegistration, err := db.PublicationByID(ctx, retryCommand)
	if err != nil {
		return err
	}
	registrationOwner, err := db.Assignment(ctx, retryCommand)
	if err != nil || registrationOwner.Connection != "" || registrationOwner.ExternalPublication == nil || registrationOwner.ExternalPublication.Connection != handoffOwner.Connection {
		return fmt.Errorf("declared external registration lacks its captured payer: %v", err)
	}
	registrationCandidate := strings.Repeat("1", 40)
	if registrationCandidate == correctionRegistration.Candidate {
		registrationCandidate = strings.Repeat("2", 40)
	}
	registrationIntent := factory.PublicationIntent{OperationID: factory.PublicationOperationID(correctionRegistration.ID, factory.OpRefPublish, 2), AuthRevision: factory.AuthRevisionFor(correctionRegistration.ID, correctionRegistration.ID, correctionRegistration.Revision), TargetBranch: correctionRegistration.TargetBranch, Candidate: registrationCandidate, ExpectedOld: correctionRegistration.Candidate, ComparisonRef: correctionRegistration.TargetBranch, ComparisonOID: correctionRegistration.BaseSHA, PRTitle: "Declared correction registration", Repository: correctionRegistration.Repository, ActorID: registrationOwner.ActorID, NativeRev: 1, NotAfter: time.Now().Add(30 * time.Second).Unix(), CorrectionNumber: correctionRegistration.PRNumber, CorrectionAuthor: correctionRegistration.PullTarget().AuthorID}
	correctionRegistration.Corrections = factory.CorrectionOps{{Kind: factory.OpRefPublish, RunID: factory.NewID(), Work: &registrationIntent, OperationID: registrationIntent.OperationID, Attempts: 1, UpdatedUnix: time.Now().Unix()}}
	correctionRegistration.Revision++
	if err = correctionRegistration.Validate(); err != nil {
		return fmt.Errorf("declared correction registration: %w", err)
	}
	var sponsorshipBefore []byte
	if err = raw.QueryRowContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 AND connection=$2`, correctionRegistration.Repository, handoffOwner.Connection).Scan(&sponsorshipBefore); err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_sponsorships SET data=jsonb_set(data,'{active}','false') WHERE repository=$1 AND connection=$2`, correctionRegistration.Repository, handoffOwner.Connection); err != nil {
		return err
	}
	withdrawnRegistration := db.UpdatePublication(ctx, correctionRegistration)
	if _, err = raw.ExecContext(ctx, `UPDATE factory_sponsorships SET data=$1 WHERE repository=$2 AND connection=$3`, string(sponsorshipBefore), correctionRegistration.Repository, handoffOwner.Connection); err != nil {
		return err
	}
	if withdrawnRegistration == nil {
		return errors.New("withdrawn external payer admitted correction registration")
	}
	if err = db.UpdatePublication(ctx, correctionRegistration); err != nil {
		return fmt.Errorf("captured external payer did not admit correction registration: %w", err)
	}
	if after, err := db.Assignment(ctx, retryCommand); err != nil || !reflect.DeepEqual(after, registrationOwner) {
		return errors.New("correction registration changed its external payer capture")
	}
	if err = db.Close(); err != nil {
		return err
	}
	reopened, err := store.OpenContext(ctx, dsn)
	if err != nil {
		return err
	}
	defer reopened.Close()
	if err = reopened.RecordNativeContextNoStart(ctx, profile.ID, pendingRun.Native.Intent); err != nil {
		return fmt.Errorf("retained no-start acknowledgement after reopen: %w", err)
	}
	if after, err := reopened.FactoryRun(ctx, pendingRun.ID); err != nil || !reflect.DeepEqual(after, retiredPending) {
		return errors.New("reopen renewed the original no-start authority")
	}
	if after, err := reopened.UsageTotal(ctx, handoffOwner.Repository, handoffOwner.Connection); err != nil || after != usageBefore+failedUsage.Minutes {
		return errors.New("no-start recovery/reopen changed settled exact-source charges")
	}
	fmt.Println("PASS: exact pending-context no-start acknowledgement preserves intent/deadline/manifest/source, retires with zero charge and resumes interrupted capability retirement; cleared marker/acquisition/changed intent/missing zero usage refuse, while a fresh explicit bounded funding command preserves the spent correction identity without model replay. Native absence acknowledgement declared; actual Store/accounting transactions.")
	fmt.Println("PASS: task/workspace admission follows the authentic original Architect head and current-root retained correction singleton; a settled nonreported failed Corrector with an exact retained charge permits a fresh explicit finite command without unused-budget transfer, while unsettled execution or uncertain publication refuse. Activation/process facts declared; native task calls stop at the observation boundary, no inference or send.")
	fmt.Println("PASS: an ordinary Builder's blocked/nonreported needs-human result and independently failed retired native run admit one explicit Retry command and exact replay, preserving the original result/outcome/deadline; confirmed-unused reservation declared, no inference or dispatch.")
	fmt.Println("PASS: real correction registration uses the external continuation's captured source despite its empty execution connection, refuses withdrawn sponsorship, and preserves the owner; candidate/native facts declared, no inference or provider send.")
	fmt.Println("PASS: closed external Architect/committed Inspector history with a retired expired unlaunched correction admits one explicit same-source bounded continuation and fresh Corrector using the original Inspector; spent reviews/charges and old rows/deadlines/receipts remain exact, replay never renews authority, and changed source/head/base or unrelated historical parent refuse. Role/native/provider facts declared; no inference or host send.")
	fmt.Println("PASS: real standalone PG admits external-command-owned source setup after a settled closed root; unsettled predecessor, withdrawn policy and changed principal refuse; replay preserves first origin/native address/deadline, expiry refuses, old or future check time cannot admit a packet, and a fresh same-origin packet preserves preparation authority and the old allowance. Native/provider/process facts and elapsed capture are declared; no host send or model.")
	fmt.Println("PASS: completed declared external Architect promotes with its exact retained ready source after setup expiry/confirmed retirement; another unsettled same-root preparation refuses, original publication receipt replays, and preparation/allowance remain unchanged. Result/plan/opinion/process observations declared; no inference or native send.")
	fmt.Println("PASS: ordinary startup discovery skips a retained automatic PR/head command before fresh offer/authority reads, while a changed head reaches a fresh offer; original assignment and publication remain unchanged. Discovery facts declared; no provider, model or native send.")
	fmt.Println("PASS: external Architect publication offers and admits an explicit five-minute merge-only command after a declared unallocated generation expires; original authorization/operation are retained, command replay is idempotent, and assignment/publication/source preparation/inference allowance remain unchanged. Expired merge/review binding declared; no inference or native send.")
	fmt.Println("PASS: declared non-executing direct external publication reuses exact completed Architect provenance for finite merge-only preparation without a Builder run or changing its retained inference allowance. Original adoption/expired merge receipts declared; no inference or native send.")
	fmt.Println("PASS: normal PublishPass retires the exact completed external fork Architect's private source once before first reviewer preparation admission; capacity remains enforced before handoff, and original source capture/deadline and reserved review allowance remain unchanged. Custody/retirement/provider history declared; no model or provider send.")
	return nil
}

func progressPagedInventoryFixture(ctx context.Context, db *store.Store, raw *sql.DB) error {
	const repository int64 = 811
	digest := strings.Repeat("a", 64)
	authority := factory.CompletionAuthority{Effective: factory.EffectiveAuthority{Missing: []string{}, Authority: factory.AuthorityRef{RequirementsID: "d" + strings.Repeat("1", 24), ApprovalID: "d" + strings.Repeat("2", 24)}}, ProjectID: "p" + strings.Repeat("3", 24), RequirementSourceCommit: strings.Repeat("b", 40), RequirementSetupDigest: digest, RequirementInputsDigest: digest, ApprovalEffectsDigest: digest, ApprovalReadinessDigest: digest, TargetBranch: "refs/heads/main", Roles: []string{}, ReassessmentMinutes: factory.EffectiveReassessmentMinutes(0)}
	model := map[string]any{"repository": strconv.FormatInt(repository, 10), "authority": authority}
	for _, key := range []string{"intents", "acceptances", "withdrawals", "controls", "preparations", "plans", "plan_heads", "plan_tasks", "assignments", "publications", "merges", "checks"} {
		model[key] = []any{}
	}
	data, _ := json.Marshal(model)
	var material factory.CompletionMaterial
	if err := json.Unmarshal(data, &material); err != nil {
		return err
	}
	scope, err := material.Digest()
	if err != nil {
		return err
	}
	local := factory.CompletionLocal{Material: &material, SourceCommit: authority.RequirementSourceCommit, PreparationDigest: digest, ScopeDigest: scope, AuthorityFingerprint: "declared-inventory"}
	if err := local.Validate(); err != nil {
		return err
	}
	event := factory.CompletionEvent{ID: factory.NewID(), PublicationID: factory.NewID(), Repository: repository, Issue: 1, Generation: 1, MergedCommit: local.SourceCommit, ClosingOccurrence: "declared-paged-completion", AcceptanceID: "d" + strings.Repeat("4", 24)}
	record := store.ReassessmentRecord{Cycle: factory.ReassessmentCycle{EventID: event.ID, CycleID: factory.NewID(), State: factory.ReassessmentCollecting, Repository: repository, Generation: 1}, Completion: event, Issue: 1}
	if err := record.Validate(); err != nil {
		return err
	}
	for _, row := range []struct {
		query string
		data  any
	}{
		{`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(811,1,$1,$2,$3,$4)`, event},
		{`INSERT INTO factory_reassessments(cycle,repository,generation,event,state,data) VALUES($1,811,1,$2,$3,$4)`, record},
	} {
		encoded, _ := json.Marshal(row.data)
		var err error
		if _, ok := row.data.(factory.CompletionEvent); ok {
			_, err = raw.ExecContext(ctx, row.query, event.ID, event.PublicationID, event.ClosingOccurrence, string(encoded))
		} else {
			_, err = raw.ExecContext(ctx, row.query, record.Cycle.CycleID, event.ID, record.Cycle.State, string(encoded))
		}
		if err != nil {
			return err
		}
	}
	reads, traversals, edits := 0, 0, false
	unavailable := false
	deps := control.CompletionDriveDeps{CaptureLocal: func(context.Context, int64) (factory.CompletionLocal, error) { return local, nil }, ReadPage: func(pass context.Context, repo int64, cursor string) (control.IssueObservationPage, error) {
		if unavailable {
			reads++
			return control.IssueObservationPage{}, errors.New("declared transport unavailable")
		}
		if repo != repository {
			return control.IssueObservationPage{}, errors.New("inventory scope changed")
		}
		n := 1
		if cursor != "" {
			var err error
			n, err = strconv.Atoi(cursor)
			if err != nil {
				return control.IssueObservationPage{}, err
			}
		} else {
			traversals++
		}
		reads++
		index := n
		if traversals%2 == 1 && n < 3 {
			index = 3 - n
		}
		title := fmt.Sprintf("Issue %d", index)
		if edits && traversals == 2 && n == 3 {
			title = "Concurrent human edit"
		}
		body := factory.CompletionIssueEvidence{Index: int64(index), Title: title, Body: "Retained body", TitleDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(title))), ContentDigest: fmt.Sprintf("%x", sha256.Sum256([]byte("Retained body")))}
		encoded, err := body.CanonicalData()
		if err != nil {
			return control.IssueObservationPage{}, err
		}
		next := ""
		if n < 20 {
			next = strconv.Itoa(n + 1)
		}
		return control.IssueObservationPage{Issues: []control.NativeInventoryIssue{{Index: int64(index), Data: encoded}}, Next: next, Complete: next == ""}, nil
	}}
	// Persist actual collection progress, then replace the coordinator. The next
	// callback must continue at9, rather than silently restarting at1.
	coordinator := control.NewCoordinator(db, nil, nil)
	first := coordinator.DriveCompletions(ctx, deps)
	saved, err := db.ReassessmentRecord(ctx, record.Cycle.CycleID)
	if err != nil || len(first.Sealed) != 0 || len(saved.CollectorProgress) == 0 || reads != 8 {
		return fmt.Errorf("paged inventory prefix: reads=%d saved=%d err=%v", reads, len(saved.CollectorProgress), err)
	}
	var progress control.InventoryCollector
	if json.Unmarshal(saved.CollectorProgress, &progress) != nil || progress.Observation == nil || progress.Observation.Cursor != "9" || len(progress.Issues) != 8 {
		return errors.New("paged inventory lost its retained prefix")
	}
	unavailable = true
	coordinator.DriveCompletions(ctx, deps)
	failures := reads
	coordinator.DriveCompletions(ctx, deps)
	saved, err = db.ReassessmentRecord(ctx, record.Cycle.CycleID)
	if err != nil || reads != failures || json.Unmarshal(saved.CollectorProgress, &progress) != nil || progress.Observation == nil || progress.Observation.Cursor != "9" || progress.Observation.NotBefore <= time.Now().Unix() {
		return errors.New("failed active inventory spun or lost its pending step")
	}
	// Advance only recorded scheduling time in this controlled fixture.
	progress.Observation.NotBefore = 0
	saved.CollectorProgress, _ = json.Marshal(progress)
	if err = db.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentCollecting, saved); err != nil {
		return err
	}
	unavailable = false

	edits = true
	for attempt := 0; attempt < 5; attempt++ {
		before := reads
		coordinator = control.NewCoordinator(db, nil, nil)
		coordinator.DriveCompletions(ctx, deps)
		if reads-before > 8 {
			return errors.New("paged inventory exceeded one pass quantum")
		}
		saved, err = db.ReassessmentRecord(ctx, record.Cycle.CycleID)
		if err != nil {
			return err
		}
		if saved.WaitReason == control.HoldInventoryRevision {
			break
		}
	}
	if saved.Cycle.State != factory.ReassessmentCollecting || saved.WaitReason != control.HoldInventoryRevision || len(saved.CollectorProgress) != 0 || saved.InventoryDigest != "" {
		return errors.New("changed complete inventory sealed or retained a mixed prefix")
	}
	edits = false
	traversals = 0
	reads = 0
	for attempt := 0; attempt < 8; attempt++ {
		coordinator = control.NewCoordinator(db, nil, nil)
		report := coordinator.DriveCompletions(ctx, deps)
		if len(report.Sealed) > 0 {
			break
		}
	}
	saved, err = db.ReassessmentRecord(ctx, record.Cycle.CycleID)
	if err != nil || saved.Cycle.State != factory.ReassessmentDeciding || saved.EvidenceIssues != 20 || reads != 40 || traversals != 2 || len(saved.CollectorProgress) != 0 {
		return fmt.Errorf("two full paged inventories did not seal: reads=%d traversals=%d state=%s reason=%s err=%v", reads, traversals, saved.Cycle.State, saved.WaitReason, err)
	}

	visibilityReads := 0
	work := factoryhttpapi.Work{Host: collaboration.HostID(strings.Repeat("a", 32)), Actor: 1, Current: func(http.ResponseWriter, *http.Request) bool { return true }, Reads: factoryservice.WorkReads{Store: db, Visibility: func(_ context.Context, _ collaboration.ActorID, selected collaboration.RepositoryID) error {
		visibilityReads++
		if int64(selected) != repository {
			return errors.New("wrong progress scope")
		}
		return nil
	}}}
	request := httptest.NewRequest(http.MethodGet, "/api/repositories/"+strconv.FormatInt(repository, 10)+"/factory/issues/progress?issues=1,2,3", nil)
	response := httptest.NewRecorder()
	work.Handler().ServeHTTP(response, request)
	var page factoryservice.WorkProgressPage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 3 || visibilityReads != 2 {
		return fmt.Errorf("recorded progress did not use one visibility bracket: status=%d visibility=%d body=%s", response.Code, visibilityReads, response.Body.String())
	}
	for _, item := range page.Items {
		if item.State != "available" || item.Plan == nil {
			return errors.New("recorded progress omitted item state or plan")
		}
	}
	visibilityReads = 0
	for _, query := range []string{"issues=1,1", "issues=0", "issues=1&issues=2", "issues=1&other=2"} {
		response = httptest.NewRecorder()
		work.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/repositories/"+strconv.FormatInt(repository, 10)+"/factory/issues/progress?"+query, nil))
		if response.Code != 400 {
			return fmt.Errorf("invalid progress page accepted: %s", query)
		}
	}
	if visibilityReads != 0 {
		return errors.New("invalid progress pages reached provider visibility")
	}
	fmt.Println("PASS: local recorded work page projects distinct item/plan states behind one opening/closing visibility bracket; invalid pages fail before provider I/O")
	newFeedback := func(key string, legacy bool) (factory.PostFeedbackIntent, error) {
		id, revision, err := db.SaveFeedPost(ctx, factory.PostRequest{Repository: repository, Body: "Retained human feedback", IdempotencyKey: key}, "1", "Human", time.Now().Unix())
		if err != nil {
			return factory.PostFeedbackIntent{}, err
		}
		if err = db.RecordPostIntake(ctx, id, revision); err != nil {
			return factory.PostFeedbackIntent{}, err
		}
		pipe, err := db.PostIntake(ctx, id)
		if err != nil {
			return factory.PostFeedbackIntent{}, err
		}
		next := pipe
		next.Judgment = factory.PostJudgmentApplied
		next.Route = factory.PostRouteExistingWork
		next.Issue = 3
		if err = db.AdvancePostIntake(ctx, pipe, next); err != nil {
			return factory.PostFeedbackIntent{}, err
		}
		if legacy {
			if _, err = raw.ExecContext(ctx, `UPDATE factory_post_intakes SET data=data-'feedback_version' WHERE post=$1`, id); err != nil {
				return factory.PostFeedbackIntent{}, err
			}
		}
		marker := "<!-- soda-feed:" + id + ":r0 -->"
		body := "Retained human feedback"
		return factory.PostFeedbackIntent{Host: strings.Repeat("a", 32), SourcePost: id, Revision: revision, Repository: repository, Issue: 3, Actor: 1, Marker: marker, Body: body, Digest: factory.PostFeedbackDigest(marker + "\n" + body)}, nil
	}
	intent, err := newFeedback("feedback-fresh", false)
	if err != nil {
		return err
	}
	type permit struct {
		delivery factory.PostFeedbackDelivery
		send     bool
		err      error
	}
	permits := make(chan permit, 2)
	for n := 0; n < 2; n++ {
		go func() { d, send, e := db.BeginPostFeedback(ctx, intent); permits <- permit{d, send, e} }()
	}
	sends := 0
	var original factory.PostFeedbackDelivery
	for n := 0; n < 2; n++ {
		result := <-permits
		if result.err != nil {
			return result.err
		}
		if result.send {
			sends++
		}
		original = result.delivery
	}
	if sends != 1 || original.Legacy || original.NotBefore <= time.Now().Unix() {
		return errors.New("feedback journal granted duplicate dispatch or lost first-send recovery hold")
	}
	if _, err = db.EditFeedPost(ctx, intent.SourcePost, "1", "Concurrent human edit", 0, time.Now().Unix()); err != nil {
		return err
	}
	confirmed := original
	confirmed.Receipt = "12"
	confirmed.Complete = true
	if err = db.SavePostFeedback(ctx, original, confirmed); err != nil {
		return err
	}
	if saved, e := db.PostFeedback(ctx, intent.SourcePost, 0); e != nil || saved != confirmed {
		return errors.New("original feedback receipt did not survive source edit")
	}
	if post, e := db.FeedPost(ctx, intent.SourcePost); e != nil || post.Body != "Concurrent human edit" {
		return errors.New("feedback settlement rewrote human text")
	}
	legacy, err := newFeedback("feedback-legacy", true)
	if err != nil {
		return err
	}
	old, send, err := db.BeginPostFeedback(ctx, legacy)
	if err != nil || send || !old.Legacy {
		return errors.New("legacy marker history gained dispatch authority")
	}
	progressed := old
	progressed.Cursor = 3
	progressed.NotBefore = time.Now().Add(5 * time.Minute).Unix()
	if err = db.SavePostFeedback(ctx, old, progressed); err != nil {
		return err
	}
	stale := old
	stale.Cursor = 5
	if err = db.SavePostFeedback(ctx, old, stale); err == nil {
		return errors.New("concurrent lookup replaced retained cursor")
	}
	absent := progressed
	absent.Complete = true
	absent.Attention = "not_found"
	if err = db.SavePostFeedback(ctx, progressed, absent); err != nil {
		return err
	}
	if _, send, e := db.BeginPostFeedback(ctx, legacy); e != nil || send {
		return errors.New("completed legacy absence authorized another send")
	}
	late := absent
	late.Receipt = "13"
	late.Attention = ""
	if err = db.SavePostFeedback(ctx, absent, late); err != nil {
		return err
	}
	fmt.Println("PASS: realPG source effect journal grants one concurrent fresh send, defers recovery, preserves original receipt through human edits, CAS-resumes lookup-only legacy cursors, and never turns absence into send authority")
	workPost, _, err := db.SaveFeedPost(ctx, factory.PostRequest{Repository: repository, Body: "Retained worker output", IdempotencyKey: "submitted-work-readback"}, "worker:"+strings.Repeat("c", 32), "Fixer", time.Now().Unix())
	if err != nil {
		return err
	}
	if _, err = raw.ExecContext(ctx, `UPDATE factory_feed_posts SET data=data || '{"actor":"fixer","assignment":"cccccccccccccccccccccccccccccccc","work":{"state":"applied"}}'::jsonb WHERE id=$1`, workPost); err != nil {
		return err
	}
	postView, err := (factoryservice.Posts{Store: db, Visibility: func(context.Context, collaboration.ActorID, int64) error { return nil }}).Read(ctx, 1, workPost)
	if err != nil || postView["body"] != "Retained worker output" || postView["status"] != "applied" || postView["can_retry"] != false {
		return fmt.Errorf("submitted work was treated as a human intake: %v", err)
	}
	if _, err = db.PostIntake(ctx, workPost); !errors.Is(err, store.ErrNotFound) {
		return errors.New("readback created human intake authority for worker work")
	}
	fmt.Println("PASS: retained worker work posts read their original body/status without a human intake row or new retry authority")
	fmt.Println("PASS: real PG paged completion retains8-step prefix across coordinator restart, rejects changed confirming inventory, and seals only two whole equal20-issue observations; no provider/model/effects")
	return nil
}

func progressNativeInventoryFixture(ctx context.Context, db *store.Store, input factoryruntime.NativeInputSource, repo, revision int64, lists *atomic.Int64, shifted, missing, edited *atomic.Bool) error {
	lists.Store(0)
	first, err := input.ReadCompletionIssuePage(ctx, repo, "")
	if err != nil || first.Complete || len(first.Issues) != 50 || first.Next == "" {
		return fmt.Errorf("native first inventory step: %+v %v", first, err)
	}
	second, err := input.ReadCompletionIssuePage(ctx, repo, first.Next)
	if err != nil || len(second.Issues) != 10 || second.Issues[0].Index != 51 || !second.Complete || lists.Load() != 2 {
		return fmt.Errorf("native inventory repeated list page: lists=%d err=%v", lists.Load(), err)
	}
	missing.Store(true)
	_, err = input.ReadCompletionIssuePage(ctx, repo, first.Next)
	missing.Store(false)
	if err == nil || err.Error() != control.HoldInventoryRevision {
		return fmt.Errorf("deleted cached member did not invalidate progress: %v", err)
	}
	// Store only observation progress until two whole memberships corroborate.
	// A deletion at a page boundary must not checkpoint a later issue over a gap.
	checkpoint := func(after int64, page factory.CreationRecoveryBatch) error {
		works := []store.ReadinessWork{}
		for _, event := range page.Events {
			works = append(works, store.ReadinessWork{ID: "declared:" + event.Delivery, Delivery: event.Delivery, Root: factory.DependenceRef{Repository: repo, Issue: event.Issue}, CreationVerified: event.Verified, CreatorID: event.Creator, CreatorWrite: event.CreatorWrite, NativeIssueID: event.OriginalIssueID, CreatedUnix: event.CreatedUnix, TitleDigest: event.TitleDigest, ContentDigest: event.ContentDigest})
		}
		if page.SourceCursor == nil {
			return errors.New("native creation omitted retained cursor")
		}
		return db.EnqueueReadinessCreationScanPage(ctx, repo, after, page.NextPosition, works, revision, page.ExpectedSourceCursor, *page.SourceCursor)
	}
	page, err := input.ReadCreations(ctx, repo, 0, 16)
	if err != nil || len(page.Events) != 0 || page.NextPosition != 0 || page.ReachedRetainedEnd {
		return fmt.Errorf("uncorroborated intake advanced: %+v %v", page, err)
	}
	if err = checkpoint(0, page); err != nil {
		return err
	}
	// Exact stale concurrent scan checkpoint refuses even with unchanged task_id.
	if err = checkpoint(0, page); !errors.Is(err, store.ErrStaleRevision) {
		return fmt.Errorf("stale source cursor overwrote current progress: %v", err)
	}
	shifted.Store(true)
	page, err = input.ReadCreations(ctx, repo, 0, 16)
	shifted.Store(false)
	if err != nil || len(page.Events) != 0 || page.NextPosition != 0 {
		return fmt.Errorf("page-boundary shift admitted skipped membership: %+v %v", page, err)
	}
	if err = checkpoint(0, page); err != nil {
		return err
	}
	var after int64
	lists.Store(0)
	for step := 0; step < 6; step++ {
		page, err = input.ReadCreations(ctx, repo, after, 16)
		if err != nil {
			return fmt.Errorf("retained native intake step %d: %v", step, err)
		}
		if err = checkpoint(after, page); err != nil {
			return err
		}
		after = page.NextPosition
		if after == 2 {
			break
		}
	}
	if after != 2 || lists.Load() != 4 {
		return fmt.Errorf("corroborated membership recollected while processing candidates: cursor=%d lists=%d", after, lists.Load())
	}
	saved, err := db.CreationRecoveryScanCursor(ctx, repo)
	if err != nil || saved == "" {
		return errors.New("native source cursor not retained")
	}
	edited.Store(true)
	page, err = input.ReadCreations(ctx, repo, after, 16)
	edited.Store(false)
	if err != nil || len(page.Events) != 0 || page.NextPosition != after || page.SourceCursor == nil || *page.SourceCursor != "" {
		return fmt.Errorf("changed cached creation did not reset observation only: %+v %v", page, err)
	}
	if err = checkpoint(after, page); err != nil {
		return err
	}
	for n := int64(1); n <= 2; n++ {
		if _, err = db.ReadinessWork(ctx, "declared:github-original-issue:"+strconv.FormatInt(1300+n, 10)); err != nil {
			return fmt.Errorf("scan reset lost admitted original work: %v", err)
		}
	}
	fmt.Println("PASS: native Go inventory reads each list page once, cached404 invalidates; real PG intake retains two corroborated memberships across steps, refuses stale concurrent checkpoints and deletion gaps, processes candidates without recollection, and resets changed observations without losing queued work. Native replies declared; no provider/model/write.")
	return nil
}

// Explicit full collection belongs only to this controlled check, never one
// production RPC. Production persists every bounded source step.
func nativeCompleteObservation(ctx context.Context, input factoryruntime.NativeInputSource, repository int64) ([]control.NativeInventoryIssue, error) {
	items := []control.NativeInventoryIssue{}
	cursor := ""
	for steps := 0; steps < factory.MaxReassessmentIssues+256; steps++ {
		page, err := input.ReadCompletionIssuePage(ctx, repository, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Issues...)
		if page.Complete {
			return items, nil
		}
		cursor = page.Next
	}
	return nil, errors.New(control.HoldInventoryOversize)
}
