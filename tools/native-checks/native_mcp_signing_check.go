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
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	factoryruntime "github.com/levitateos/factory/pkg/factory/runtime"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
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
	if err := checkNativeRecovery(config, instance, unavailable); err != nil {
		return err
	}
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
	if mode == "recover" || mode == "recover_replay" || mode == "recover_started" {
		targetID, readErr := os.ReadFile(filepath.Join(profile, "native-recovery-target.id"))
		if readErr != nil {
			return readErr
		}
		provenance := run.Native.Provenance
		request := execution.WorkRecoveryRequest{Address: address, Native: &execution.NativeRecovery{
			Source: execution.NativeWorkspaceRef{Profile: provenance.Profile, InstanceID: provenance.InstanceID, WorktreeID: provenance.WorktreeID},
			Target: execution.NativeCheckoutRef{Profile: config.Profile, CheckoutID: string(targetID)},
		}}
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
		if os.Args[3] == "orphan-judgment-result" {
			err = orphanJudgmentFixture(os.Args[1], os.Args[4])
		} else if os.Args[3] == "prepare-judgment-result" || os.Args[3] == "check-judgment-result" {
			err = judgmentResultFixture(os.Args[1], os.Args[4], os.Args[3] == "prepare-judgment-result")
		} else if os.Args[3] == "check-judgment-journal" {
			err = judgmentJournalFixture(os.Args[1], os.Args[4])
		} else if os.Args[3] == "declare-request-route" {
			err = declaredRequestRoute(os.Args[1], os.Args[4])
		} else if strings.HasPrefix(os.Args[3], "check-background-") {
			err = backgroundInputsFixture(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
		} else if os.Args[3] == "check-owned-tool" {
			err = ownedToolFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-native-schema" {
			err = nativeSchemaFixture(os.Args[1], os.Args[4])
		} else if os.Args[3] == "check-native-preparation" {
			err = nativePreparationFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-owned-execution" {
			err = ownedExecutionFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-native-sponsorship" || os.Args[3] == "withdraw-native-sponsorship" {
			err = nativeSponsorshipFixture(os.Args[1], os.Args[2], os.Args[4], os.Args[3] == "check-native-sponsorship")
		} else if os.Args[3] == "launch-admitted-native" || os.Args[3] == "launch-admitted-tasks" || os.Args[3] == "check-admitted-native" || os.Args[3] == "settled-admitted-native" || os.Args[3] == "observe-admitted-native" || os.Args[3] == "acknowledge-native" || os.Args[3] == "reconcile-retained-native" || os.Args[3] == "withdraw-admitted-native" {
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

// This check declares only disabled local policy; account, issue history and
// branch evidence come from the production native connector, without a view.
func backgroundInputsFixture(profile, instance, action, path string) error {
	var input struct {
		Repository, Issue, Connection, Decision string
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
	if strings.HasPrefix(action, "check-background-publication") || strings.HasPrefix(action, "check-background-correction") || strings.HasPrefix(action, "check-background-merge") {
		fixtureTimeout = 120 * time.Second
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
		if !observed.Complete || observed.NativeRev <= 0 || observed.HeadTip != target.HeadOID || observed.BaseTip != target.BaseOID || len(observed.Checks) != 2 {
			return errors.New("native CI observation binding differs")
		}
		policy, err := db.RepositoryPolicy(ctx, repository)
		if err != nil {
			return err
		}
		policy.TargetBranch = target.BaseRef
		for name, expected := range map[string]string{"factory-native-observer/pass": factory.CheckPass, "factory-native-observer/fail": factory.CheckFailed, "factory-native-observer/missing": factory.CheckPending} {
			policy.Checks = []string{name}
			adopted := factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}
			assessment, err := factory.VerifyChecks(target, adopted, policy, observed, time.Now().Unix())
			if err != nil || assessment.Verdict != expected {
				return fmt.Errorf("native CI %s verdict %s: %w", name, assessment.Verdict, err)
			}
			if _, err = db.RecordCheckAssessment(ctx, assessment); err != nil {
				return err
			}
		}
		changed := target
		changed.HeadOID = strings.Repeat("c", 40)
		policy.Checks = []string{"factory-native-observer/pass"}
		assessment, err := factory.VerifyChecks(changed, factory.AdoptedChecks{Checks: policy.Checks, PolicyRevision: policy.Revision, Digest: factory.ChecksDigest(policy.Checks)}, policy, observed, time.Now().Unix())
		if err != nil || assessment.Verdict != factory.CheckRefused || assessment.Reason != factory.CheckReasonStaleHead {
			return errors.New("native CI evidence admitted a different candidate")
		}
		if _, err = source.ObserveChecks(ctx, target, int64(connection.Actor)+1); err == nil {
			return errors.New("native CI accepted another assessor actor")
		}
		fmt.Println("PASS: exact native GitHub PR/branch/status evidence, latest status selection, Go/PostgreSQL pass/failure/missing verdicts, stale-candidate and foreign-actor refusal; no workflow or merge proof")
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
	}
	if action != "check-background-publication" {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return err
		}
		for _, item := range []struct {
			Work    factory.PublicationWork
			Outcome factory.OperationOutcome
		}{{state.Branch, state.BranchOutcome}, {state.Pull, state.PullOutcome}} {
			prior, err := port.LookupOp(ctx, item.Work.OperationID)
			if err != nil || !reflect.DeepEqual(prior, item.Outcome) {
				return fmt.Errorf("retained native publication differs: %v", err)
			}
		}
		if _, err := port.AdoptBranch(ctx, state.Branch, state.BranchOutcome); err != nil {
			return err
		}
		if _, err := port.AdoptPRCreation(ctx, state.Pull, state.PullOutcome); err != nil {
			return err
		}
		fmt.Println("PASS: native branch/PR command receipts survive restart and exact credential withdrawal without sending again")
		return nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	publication := factory.Publication{ID: factory.NewID(), AssignmentID: factory.NewID(), Repository: repository, Issue: issue, Run: factory.NewID(), Stage: factory.PublicationOpen, Candidate: candidate, ProjectID: "p" + strings.Repeat("d", 24), Role: project.RoleCoder, Acceptance: "d" + strings.Repeat("a", 24), Preparation: "f" + strings.Repeat("b", 24), BaseSHA: base, TargetBranch: "refs/heads/main", CreatedUnix: now.Unix()}
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
	work := func(kind string) factory.PublicationWork {
		expected := "absent"
		if kind == factory.OpPRCreate {
			expected = candidate
		}
		return factory.PublicationWork{Bundle: bundle, AssignmentID: publication.AssignmentID, Publication: publication.ID, RunID: run.ID, Run: run, Candidate: candidate, BaseSHA: base, TargetBranch: publication.TargetBranch, OperationID: factory.PublicationOperationID(publication.ID, kind, 1), AuthRevision: "declared-native-publication-fixture", ExpectedOld: expected, ComparisonRef: publication.TargetBranch, PRTitle: "Disposable Factory native publication check", PRBody: "Disposable integration fixture. Candidate bytes and provider effects are real; run completion and local authority are declared inputs. No model inspection or merge is claimed.", Repository: repository, Issue: issue, ActorID: int64(actor)}
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
	encoded, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(statePath, encoded, 0600); err != nil {
		return err
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
	policy, err := db.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return err
	}
	policy.Checks = []string{"factory-native/merge"}
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
	w := factory.MergeWork{Method: "merge", MergeID: factory.NewID(), PublicationID: p.ID, OperationID: factory.MergeOperationIDForGeneration(p.ID, 1), AuthRevision: "declared-native-merge-fixture", Repository: p.Repository, Issue: p.Issue, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID, PRAuthorID: review.ReviewerID, ReviewerID: review.ReviewerID, ActorID: review.ReviewerID, HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID, ReviewID: review.ReviewID, ReviewEvent: "COMMENT", NotAfter: time.Now().Add(5 * time.Minute).Unix()}
	observed, err := port.ObserveMerge(ctx, w)
	if err != nil {
		return err
	}
	if !observed.MethodAvailable || !observed.ProviderComplete || !observed.ProviderEligible {
		return fmt.Errorf("native merge evidence unavailable: method=%v complete=%v eligible=%v", observed.MethodAvailable, observed.ProviderComplete, observed.ProviderEligible)
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
	_, err = conn.ExecContext(ctx, `INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, m.PublicationID, m.Repository, m.Issue, m.PRNumber, m.Stage, string(raw))
	return err
}

func finishNativeMergeFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeMerges, m factory.Merge, state nativeMergeFixtureState, path string) error {
	w := state.Work
	var err error
	state.Adopted, err = port.AdoptMerge(ctx, w, state.Outcome)
	if err != nil {
		return err
	}
	if state.Adopted.MergedCommit == w.HeadOID {
		return errors.New("merge fixture did not capture the generated result separately")
	}
	replay, err := port.SubmitMerge(ctx, w)
	if err != nil || !reflect.DeepEqual(replay, state.Outcome) {
		return errors.New("native merge replay changed")
	}
	state.Confirmation, err = port.ObserveCompletion(ctx, w)
	if err != nil {
		return err
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
	fmt.Println("PASS: actual native exact-head merge, distinct result adoption, issue/base completion, replay and cancellation; local workflow authorization declared, no model/full coordinator")
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

type initialIntakeSource struct{ view control.AcceptanceIssueView }

func (s *initialIntakeSource) ReadAcceptanceEvidence(context.Context, string, string, []string) (control.AcceptanceEvidence, error) {
	return control.AcceptanceEvidence{Revision: 1, Issue: s.view}, nil
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
	policy := factory.RepositoryPolicy{Repository: 42, GrantedBy: 7, TargetBranch: "refs/heads/main", MergeMethod: factory.MergeFastForward, Checks: []string{"fixture/build"},
		Roles:   map[string]factory.RoleSelection{project.RoleCoder: {Account: "chosen", Harness: project.FactoryHarnessMuse, Model: "declared"}, project.RoleReviewer: {Account: "chosen", Harness: project.FactoryHarnessMuse, Model: "declared"}},
		Publish: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpRefPublish}, Create: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpPRCreate},
		Review: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpReviewSubmit}, Merge: factory.ActorBindingRef{TokenID: 11, ActorID: 7, Kind: factory.OpMerge},
		AttemptLimits: factory.DefaultAttemptLimits(), ReassessmentMinutes: factory.DefaultReassessmentMinutes}
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	source := &initialIntakeSource{view: control.AcceptanceIssueView{ID: "101", Index: "1", PosterID: "7", CreatedUnix: 100,
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
		default:
			return errors.New("original native run has no confirmed completed/failed outcome")
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
			if stopTime == nil || !usage.EndedAt.Equal(*stopTime) || !stopTime.Before(prior.Deadline) || a.Outcome != factory.NeedsHuman {
				return errors.New("original early stop or incomplete product result was not retained")
			}
			if _, err = rawDB.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{native,provenance,process,stopped_at}',to_jsonb($2::text)) WHERE id=$1`, input.Run, stopTime.Add(time.Second).Format(time.RFC3339Nano)); err == nil || !strings.Contains(err.Error(), "native process stop cannot change") {
				return fmt.Errorf("database did not enforce the recorded stop timestamp: %v", err)
			}
			fmt.Println("PASS: original early-stopped process recovered without inference replay; usage ends at its retained immutable native stop, excluding offline delay; product result remains blocked")
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
			receipts, err := db.PlanTaskReceipts(ctx, plan.AttemptRoot, plan.ID)
			if err != nil {
				return err
			}
			if len(receipts) != len(plan.Tasks) {
				return errors.New("admitted task receipts missing")
			}
			for _, receipt := range receipts {
				if receipt.State != store.PlanTaskCompleted || !receipt.Parent || receipt.Commit == "" || len(receipt.Checks) != 1 {
					return errors.New("admitted sequential task receipt incomplete")
				}
			}
			fmt.Println("PASS: actual investigation-derived Go admission, role MCP edits/readback/retry, sequential task and integration checks, retained final receipt and candidate export")
		}
		cap, err := db.GetMCPCapability(ctx, input.Run)
		if err != nil || cap.State != factory.MCPCapabilityActive {
			return errors.New("actual worker never activated its MCP capability")
		}
		// Public Builder prompts have no fixture-only artifact requirement. Their
		// actual task checks and exported candidate above are the evidence.
		if input.Tasks && input.ParentRun != "" {
			return nil
		}
		var id string
		if err = rawDB.QueryRowContext(ctx, `SELECT id FROM factory_feed_artifacts WHERE assignment=$1 AND complete`, cap.AssignmentID).Scan(&id); err != nil {
			return err
		}
		artifact, content, err := db.FeedArtifact(ctx, repository, id)
		if err != nil || string(content) != "actual admitted native worker artifact\n" || artifact.Size != len(content) {
			return errors.New("actual worker artifact bytes were not retained")
		}
		fmt.Println("PASS: public Go-admitted native launch; live MCP activation, actual worker artifact bytes and terminal process/custody/root receipt")
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
	if prior, err := db.RepositoryPolicy(ctx, repository); err == nil {
		policy.Revision = prior.Revision
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
		db.SaveSponsorship(ctx, factory.Sponsorship{Repository: repository, GrantedBy: actor, ActorID: actor, ProjectID: p.Project, Connection: input.Connection, Native: &account, Roles: sponsoredRoles, AllowanceMinutes: 5, Active: true}),
	} {
		if err != nil {
			return err
		}
	}
	acceptance := factory.Acceptance{ID: "d" + factory.NewID()[:24], Repository: repository, IssueIndex: "3", Approver: actor, NativeRev: 1, TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), ContentVersion: 1}
	if err = db.AdmitAcceptanceDecision(ctx, acceptance); err != nil {
		return err
	}
	var queued factory.IssueControl
	if input.Tasks {
		if err = nativeStandingReviewer(ctx, db, port, prep); err != nil {
			return err
		}
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
		if input.Live {
			plan := nativeWorkspacePlan(p.SourceCommit)
			plan.Tasks = plan.Tasks[:1]
			plan.Tasks[0].OwnedPaths = []string{"native-task.txt"}
			plan.Lanes[0].TaskIDs = []string{"first"}
			plan.Checks = []factory.CheckSpec{
				{ID: "first", Argv: []string{"/usr/bin/grep", "-qx", "first", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeTask, Required: true},
				{ID: "integration", Argv: []string{"/usr/bin/grep", "-qx", "first", "native-task.txt"}, Cwd: ".", TimeoutSeconds: 5, Scope: factory.CheckScopeIntegration, Required: true},
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
			encoded, err = json.Marshal(map[string]any{"payload": map[string]any{"approach": "Sequential native task fixture", "evidence_refs": []string{"source:README.md"}, "tasks": authored}})
			if err != nil {
				return err
			}
			prompt = []byte("Disposable Factory Architect integration task. Read README.md with factory.read_source, inspect factory.context, and read factory.inbox. " + nativeFixtureArtifactPrompt + " Then call factory.finish_issue_plan with these JSON arguments: " + string(encoded) + ". payload and tasks must be JSON objects, never JSON-encoded strings. Omit submission_id for this new call. This is a declared one-file test objective and check; do not implement it in the investigation phase. Finish with OK after the terminal tool succeeds.")
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

// Uses the existing admitted local CLI fixture: no provider request or login
// mutation. The alternate source is declared metadata, never a fallback login.
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
	selected, err := db.Sponsorship(ctx, repository, connection)
	if err != nil {
		return err
	}
	coordinator := control.Coordinator{Store: db, Host: port, Broker: port}
	other := selected
	other.Connection, other.Revision = factory.NewID(), 0
	other.Native = &factory.NativeSponsorship{Profile: selected.Native.Profile, CredentialSHA256: strings.Repeat("a", 64)}
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
	selected.Active = false
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
	selected, err = db.Sponsorship(ctx, repository, connection)
	if err != nil {
		return err
	}
	selected.Active = true
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
	fmt.Println("PASS: selected-source withdrawal with another source active: immediate MCP refusal, actual process/root cancellation, original-source usage, no fallback or old-capability revival; local CLI fixture, no provider request/login mutation")
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
	return control.DispatchInputs{Revision: 1, Tip: r.source, TipRef: target, Comments: []control.DispatchComment{}, Issue: control.DispatchIssue{Title: "Native admitted execution fixture", Body: "positive-native-mcp-fixture", TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64), ContentVer: 1, TitleOccurrence: factory.OriginalOccurrence, LifecycleOccurrence: factory.OriginalOccurrence, Visible: true}}, ctx.Err()
}

func nativeStandingReviewer(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, source execution.PreparationRecord) error {
	if source.Native == nil || source.Native.InstanceID != port.Instance {
		return errors.New("standing reviewer requires the current native source owner")
	}
	records, err := db.StandingProjectPreparations(ctx, source.Preparation.Project)
	if err != nil {
		return err
	}
	for _, record := range records {
		p := record.Preparation
		if p.Role != project.RoleReviewer || p.SourceCommit != source.Preparation.SourceCommit || p.SetupDigest != source.Preparation.SetupDigest || !p.Requirements.Equal(source.Preparation.Requirements) || p.Approval != source.Preparation.Approval || record.Native == nil || record.Native.Profile != source.Native.Profile || record.Native.InstanceID != port.Instance {
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
	reviewer.Preparation.ID, reviewer.Preparation.Revision, reviewer.Preparation.Role = "f"+factory.NewID()[:24], 0, project.RoleReviewer
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
	if err = nativeStandingReviewer(ctx, db, port, source); err != nil {
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
		reviewerRun, err := db.FactoryRun(ctx, review.Run)
		if err != nil {
			return err
		}
		if review.Role != project.RoleReviewer || review.Phase != project.ExecutionReview || review.Preparation != id || reviewerRun.Native == nil || reviewerRun.Native.Provenance.WorktreeID != id || reviewerRun.Native.BundleSHA256 != prepared.Preparation.State.SourceBundleSHA256 || reviewerRun.InputSHA != target.HeadOID {
			return errors.New("reviewer native packet does not capture its prepared candidate")
		}
		if reviewerRun.Reconciled {
			review, err = db.Assignment(ctx, review.ID)
			if err != nil {
				return err
			}
			terminal, found, err := db.TerminalMCPProposal(ctx, review.ID)
			if err != nil {
				return err
			}
			if found && terminal.State == factory.MCPWorkAccepted && review.Stage == factory.AssignmentFinished {
				if review.Outcome != factory.NeedsHuman || !reviewerRun.Native.Settled() {
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
	fmt.Println("PASS: actual coordinator reviewer dispatch captures candidate workspace/bundle, launches the independent CLI worker, and settles its explicit incomplete result with native retirement; CI/provider facts declared, no model or GitHub effect")
	return nil
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
	in := factoryservice.SponsorshipChange{Roles: []string{run.Role}, AllowanceMinutes: 5, Active: active}
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
	if os.Getenv("SODA_NATIVE_CHECK_SETUP_BUDGET") == "1" {
		duration = 50 * time.Second
	}
	if os.Getenv("SODA_NATIVE_CHECK_PUBLIC_SETUP") == "1" || os.Getenv("SODA_NATIVE_CHECK_BUN_RELEASE") != "" {
		duration = 90 * time.Second
	}
	if os.Getenv("SODA_NATIVE_CHECK_PROJECT_TOOLCHAIN") != "" {
		duration = 330 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	db, err := store.OpenStandaloneContext(ctx, config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.InitializeEmptyCollaborationLedger(ctx); err != nil {
		return err
	}
	host, err := db.PutCollaborationHost(ctx, collaboration.Host{ID: collaboration.HostID(factory.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"})
	if err != nil {
		return err
	}
	repo, err := db.ObserveCollaborationRepository(ctx, host.ID, "native-source-fixture", "fixture/native-source")
	if err != nil {
		return err
	}
	actor, err := db.ObserveCollaborationActor(ctx, host.ID, "native-source-owner", "native-source-owner")
	if err != nil {
		return err
	}
	if err = db.PutCollaborationProfile(ctx, collaboration.Profile{ID: config.Profile, Name: "Native source fixture"}); err != nil {
		return err
	}
	placement, err := db.SelectPlacement(ctx, host.ID, repo.ID, actor.ID, collaboration.RuntimeRef{ID: collaboration.RuntimeID(config.Profile), Profile: config.Profile, Kind: collaboration.Native, Name: "Native source fixture"}, "", factory.PlacementActive)
	if err != nil {
		return err
	}
	native := &project.NativeEnvironment{Profile: config.Profile, Runtime: placement.Runtime, Generation: placement.Generation}
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
		install := fmt.Sprintf("test ! -e \"$HOME/.factory-setup-once\"\nprintf ready > \"$HOME/.factory-setup-once\"\nmkdir -p \"$HOME/.bun/bin\"\ncurl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 5 --max-time 45 'https://github.com/oven-sh/bun/releases/download/%s/bun-linux-x64.zip' -o \"$TMPDIR/bun.zip\"\nprintf '%%s  %%s\\n' '%s' \"$TMPDIR/bun.zip\" | sha256sum --check --status\nunzip -q \"$TMPDIR/bun.zip\" -d \"$TMPDIR/bun\"\ncp \"$TMPDIR/bun/bun-linux-x64/bun\" \"$HOME/.bun/bin/bun\"\nrm -rf \"$TMPDIR/bun\" \"$TMPDIR/bun.zip\"\ncp /run/factory-source/approved/package.json /run/factory-source/approved/package-lock.json .\nbun install --ignore-scripts --no-progress --no-cache\n", release.Version, release.SHA256)
		files["setup.sh"] = append(files["setup.sh"], []byte(install)...)
		files["check.sh"] = append(files["check.sh"], []byte(fmt.Sprintf("test \"$(bun --version)\" = '%s'\nbun -e 'if (!require(\"is-number\")(42)) process.exit(1)'\nprintf 'checked-by-native privately provisioned Bun %s and cold dependency\\n'\n", version, version))...)
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
	if os.Getenv("SODA_NATIVE_CHECK_REQUEST_SETUP") == "1" {
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
	state, err := port.PrepareWorkspace(ctx, request)
	if err != nil {
		return err
	}
	record.State = state
	if err = db.ObservePreparation(ctx, record); err != nil {
		return err
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
			if (mode == "output-limit" || mode == "time-limit" || mode == "network-off") && state.Phase == project.PrepareFailed {
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
	if mode == "output-limit" || mode == "time-limit" || mode == "network-off" {
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
		if err := nativeRequestSetupFixture(ctx, db, port, host.ID, actor.ID, int64(repo.ID), record, request); err != nil {
			return err
		}
	}
	if os.Getenv("SODA_NATIVE_KEEP_PREPARATION") == "1" {
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
		if err = declaredNativePreparation(ctx, config, instance, payload, &observed); err != nil {
			return err
		}
		for !observed.Ready {
			if observed.Phase != project.PrepareRunning {
				return fmt.Errorf("native candidate preparation: %s %s", observed.Phase, observed.Output)
			}
			time.Sleep(30 * time.Millisecond)
			if err = declaredNativePreparation(ctx, config, instance, map[string]any{"operation": "inspect", "address": derived.Address()}, &observed); err != nil {
				return err
			}
		}
		if observed.SourceCommit != candidate.SourceCommit || observed.SetupDigest != candidate.SetupDigest || observed.SourceBundleSHA256 == state.SourceBundleSHA256 || !strings.Contains(observed.Output, candidate.SourceCommit) || !strings.Contains(observed.Output, candidateText) {
			return errors.New("candidate setup/check did not observe the imported source")
		}
		if err = declaredNativePreparation(ctx, config, instance, payload, &observed); err != nil {
			return err
		}
		contextRequest := execution.PreparationContextRequest{Native: &owner, Role: candidate.Role, Input: project.FactoryPreparationContextRequest{Project: candidate.Project, ID: candidate.ID, SourceCommit: candidate.SourceCommit, ApprovedBase: source, DiffBase: source, Candidate: candidate.SourceCommit, Paths: []string{candidatePath}, NotAfter: time.Now().Add(10 * time.Second)}}
		var material execution.PreparationContext
		if err = declaredNativePreparation(ctx, config, instance, map[string]any{"operation": "context", "request": contextRequest}, &material); err != nil {
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
		if err = declaredNativePreparation(ctx, config, instance, map[string]any{"operation": "retire", "address": derived.Address()}, &observed); err != nil {
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

func nativeRequestSetupFixture(ctx context.Context, db *store.Store, port factoryruntime.NativeExecution, host collaboration.HostID, actor collaboration.ActorID, repository int64, record execution.PreparationRecord, request execution.StandingPreparationRequest) error {
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
	binding, err := db.BindCollaborationConnection(ctx, collaboration.ConnectionObservation{
		Profile:  collaboration.Profile{ID: port.Config.Profile, Name: "Native source fixture"},
		Provider: collaboration.GitHub, APIBase: "https://api.github.com", Connection: collaboration.ConnectionID(factory.NewID()),
		Account: "native-source-owner", Login: "native-source-owner",
	})
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

func declaredNativePreparation(ctx context.Context, config factoryruntime.NativeConfig, instance string, payload, result any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(config.Directory, "host.sock"))
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, "POST", "http://soda-native/api/native/preparation", strings.NewReader(string(body)))
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
		return fmt.Errorf("declared native preparation callback status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(result)
}

type declaredPostRouting struct{}

func orphanJudgmentFixture(profile, post string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	db, err := store.OpenStandaloneContext(context.Background(), config.DatabaseDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	detail, err := db.FeedPost(context.Background(), post)
	if err != nil {
		return err
	}
	_, err = db.DeleteFeedPost(context.Background(), post, detail.Record.Author, detail.Record.Revision)
	return err
}

func judgmentResultFixture(profile, post string, prepare bool) error {
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
	path := filepath.Join(profile, "judgment-fixture.json")
	var fixture struct {
		Capture factory.NativeJudgmentCapture `json:"capture"`
		Prompt  string                        `json:"prompt"`
		Digest  string                        `json:"digest"`
	}
	if !prepare {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &fixture); err != nil {
			return err
		}
		cmd, err := db.FactoryCommand(ctx, fixture.Capture.ID)
		if err != nil {
			return err
		}
		for cmd.Finished == "" && ctx.Err() == nil {
			time.Sleep(100 * time.Millisecond)
			cmd, err = db.FactoryCommand(ctx, fixture.Capture.ID)
			if err != nil {
				return err
			}
		}
		var result struct {
			Answer *factory.PriorityAnswer `json:"answer"`
		}
		detail, err := db.FeedPost(ctx, post)
		if err != nil {
			return err
		}
		if cmd.Finished == "" || json.Unmarshal([]byte(cmd.Outcome), &result) != nil ||
			(!detail.Record.Deleted && (result.Answer == nil || result.Answer.Selected != "discussion" || result.Answer.Model != fixture.Capture.Selection.Model)) ||
			(detail.Record.Deleted && result.Answer != nil) {
			return errors.New("actual native result was not adopted by the Go judgment owner")
		}
		usage, err := db.RunUsage(ctx, fixture.Capture.ID)
		if err != nil || usage.Validate() != nil || usage.Connection != fixture.Capture.Selection.Account {
			return errors.New("actual native judgment did not charge its original source")
		}
		if detail.Record.Deleted {
			fmt.Println("PASS: deleted source's original native result was accounted after restart without adopting its judgment; no inference replay")
		} else {
			fmt.Println("PASS: actual native process result crossed private IPC, passed Go choice validation, routed the real Feed post and charged its captured source; CLI/tool/provider answer declared, no model request")
		}
		return nil
	}
	detail, err := db.FeedPost(ctx, post)
	if err != nil {
		return err
	}
	policy, err := db.RepositoryPolicy(ctx, detail.Record.Repository)
	if err != nil || policy.Judgments == nil {
		return errors.New("fixture requires explicit judgment settings")
	}
	selection := *policy.Judgments
	selection.Model = "muse-spark-1.3-contributor"
	selection.Profile = factory.ExecutionProfile{Effort: "low", Contributor: true}
	policy.Judgments = &selection
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		return err
	}
	input, err := factoryservice.CollectPostJudgeInput(ctx, db, detail)
	if err != nil {
		return err
	}
	input, err = (factoryservice.PostContext{Posts: factoryservice.Posts{Store: db}}).Prepare(ctx, input)
	if err != nil {
		return err
	}
	prompt, err := factory.NativeJudgmentPrompt(factoryservice.PostJudgeState(input), factoryservice.PostRouteRubric, factoryservice.PostJudgmentChoices(input))
	if err != nil {
		return err
	}
	executable, err := os.ReadFile(os.Getenv("SODA_NATIVE_CHECK_JUDGMENT_EXECUTABLE"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(executable)
	capture := factory.NativeJudgmentCapture{ID: factory.NativeJudgmentID(config.Profile, detail.Record.Repository, post+":"+strconv.FormatInt(detail.Record.Revision, 10)), Profile: config.Profile, Repository: detail.Record.Repository, SourcePost: post, SourceRevision: detail.Record.Revision, Selection: selection, PromptSHA256: factory.MCPInputDigest([]byte(prompt)), ToolVersion: "fixture", ToolSHA256: hex.EncodeToString(sum[:]), DeadlineUnixMS: time.Now().Add(25 * time.Second).UnixMilli()}
	cmd, fresh, err := db.BeginNativeJudgment(ctx, capture)
	if err != nil || !fresh {
		return fmt.Errorf("declared tool admission: %v fresh=%v", err, fresh)
	}
	fixture.Capture, fixture.Prompt, fixture.Digest = capture, prompt, cmd.Digest
	raw, _ := json.Marshal(fixture)
	return os.WriteFile(path, raw, 0600)
}

func judgmentJournalFixture(profile, repository string) error {
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
	repo, err := strconv.ParseInt(repository, 10, 64)
	if err != nil {
		return err
	}
	policy, err := db.RepositoryPolicy(ctx, repo)
	if err != nil || policy.Judgments == nil {
		return errors.New("judgment selection absent")
	}
	source, err := db.Sponsorship(ctx, repo, policy.Judgments.Account)
	if err != nil {
		return err
	}
	capture := factory.NativeJudgmentCapture{ID: strings.Repeat("e", 32), Profile: config.Profile, Repository: repo, Selection: *policy.Judgments, PromptSHA256: strings.Repeat("a", 64), ToolVersion: "declared", ToolSHA256: strings.Repeat("b", 64), DeadlineUnixMS: time.Now().Add(25 * time.Second).UnixMilli()}
	if _, _, err = db.BeginNativeJudgment(ctx, capture); !errors.Is(err, store.ErrNativeJudgmentSource) {
		return errors.New("withdrawn source funded a judgment")
	}
	source.Active = true
	if _, err = db.ApplyFactoryGrant(ctx, "declared-source-owner", source); err != nil {
		return err
	}
	priorTotal, err := db.UsageTotal(ctx, repo, source.Connection)
	if err != nil {
		return err
	}
	command, fresh, err := db.BeginNativeJudgment(ctx, capture)
	if err != nil || !fresh {
		return fmt.Errorf("judgment admission: %v fresh=%v", err, fresh)
	}
	if replay, fresh, err := db.BeginNativeJudgment(ctx, capture); err != nil || fresh || replay.Digest != command.Digest {
		return errors.New("judgment replay reserved another call")
	}
	other := capture
	other.ID = strings.Repeat("f", 32)
	if _, _, err = db.BeginNativeJudgment(ctx, other); !errors.Is(err, store.ErrNativeJudgmentSource) {
		return errors.New("unsettled judgment did not retain its source reservation")
	}
	changed := capture
	changed.Selection.Model = "different-model"
	if _, _, err = db.BeginNativeJudgment(ctx, changed); !errors.Is(err, store.ErrNativeJudgmentSource) {
		return errors.New("judgment borrowed an unselected model")
	}
	start := time.Now().Add(-2 * time.Second).UTC()
	end := time.Now().UTC()
	usage := factory.Usage{RunID: capture.ID, Repository: repo, Connection: source.Connection, Minutes: 1, StartedAt: start, EndedAt: end}
	result := json.RawMessage(`{"provider_facts":"declared","selected":"new_issue"}`)
	if err = db.CompleteNativeJudgment(ctx, capture, result, usage); err != nil {
		return err
	}
	if err = db.CompleteNativeJudgment(ctx, capture, result, usage); err != nil {
		return err
	}
	if total, err := db.UsageTotal(ctx, repo, source.Connection); err != nil || total != priorTotal+1 {
		return errors.New("judgment result replay duplicated its source usage")
	}
	retained, err := db.FactoryCommand(ctx, capture.ID)
	if err != nil || retained.Finished == "" || retained.Outcome != string(result) {
		return errors.New("judgment result was not retained")
	}
	source, err = db.Sponsorship(ctx, repo, source.Connection)
	if err != nil {
		return err
	}
	source.Active = false
	if _, err = db.ApplyFactoryGrant(ctx, "declared-source-owner", source); err != nil {
		return err
	}
	if err = db.CheckNativeJudgmentSource(ctx, capture); !errors.Is(err, store.ErrNativeJudgmentSource) {
		return errors.New("withdrawn judgment source remained current")
	}
	fmt.Println("PASS: actual PostgreSQL judgment source admission, one pending call, exact selection, result/usage replay and withdrawal; provider/process facts declared, no native inference")
	return nil
}

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
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
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
			time.Sleep(120 * time.Second) // The real native owner must cancel this process.
			return errors.New("withdrawal fixture was not cancelled")
		}
		if strings.Contains(string(prompt), "native-tasks-investigation-fixture") {
			plan := nativeWorkspacePlan(strings.Repeat("0", 40))
			planBytes, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			var authored map[string]any
			if err := json.Unmarshal(planBytes, &authored); err != nil {
				return err
			}
			for _, field := range []string{"attempt_root", "source_commit", "integration_commit", "scope_digest", "input_digest", "preparation", "digest"} {
				delete(authored, field)
			}
			if _, err := workerMCPCall(client, "factory.read_source", map[string]any{"path": "README.md"}); err != nil {
				return err
			}
			if _, err := workerMCPCall(client, "factory.finish_issue_plan", map[string]any{"approach": plan.Approach, "tasks": authored, "evidence_refs": []string{"source:README.md"}}); err != nil {
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
			Content string `json:"content"`
		}
		if json.Unmarshal(read, &content) != nil || content.Content != id+"\n" {
			return errors.New("admitted workspace readback differs")
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
func workerMCPCallID(client *http.Client, tool string, payload any, operationID string) (json.RawMessage, error) {
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
		return err
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
	}{
		{"token", project.ExecutionImplement, 0}, {"muse_login", project.ExecutionImplement, 0},
		{"token", project.ExecutionImplement, 1}, {"muse_login", project.ExecutionCorrect, 0},
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
