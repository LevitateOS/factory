// A disposable Go-to-Rust native callback check. The fixture reads only its
// own synthetic profile key to compare the protocol HMAC; it prints no secret.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpserver"
	factoryruntime "github.com/levitateos/factory/pkg/factory/runtime"
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
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	if len(raw) > 32768 {
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
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	port := factoryruntime.NativeExecution{Config: config, Instance: instance, Store: db}
	address := execution.RunAddress{Run: run.ID, Workspace: run.ProjectID}
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
	if mode == "running" {
		stale := port
		stale.Instance = factory.NewID()
		if _, err := stale.InspectRun(ctx, address); !errors.Is(err, execution.ErrRunChanged) {
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
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		if err := workerProfileFixture(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 5 {
		var err error
		if os.Args[3] == "check-owned-tool" {
			err = ownedToolFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-native-preparation" {
			err = nativePreparationFixture(os.Args[1], os.Args[2], os.Args[4])
		} else if os.Args[3] == "check-owned-execution" {
			err = ownedExecutionFixture(os.Args[1], os.Args[2], os.Args[4])
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

// Declared Go permissions in a disposable ledger; the native source/setup work
// and its observation/CAS transactions are real. This is not provider consent.
func nativePreparationFixture(profile, instance, mode string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	db, err := store.OpenContext(ctx, config.DatabaseDSN())
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
	files := map[string][]byte{"setup.sh": setup, "check.sh": []byte("printf 'checked-by-native\\n'\n")}
	tools := []string{"git", "bash"}
	source := os.Getenv("SODA_NATIVE_CHECK_COMMIT")
	requirement := project.RequirementDecision{ID: "d" + factory.NewID()[:24], Project: grant.Project, Approver: int64(actor.ID), SourceCommit: source, SetupDigest: project.SetupDigestOf(files), InputsDigest: project.PreparationInputsDigestOf([]string{"setup.sh", "check.sh"}, nil, tools), Native: native}
	if err = db.AdmitRequirementDecision(ctx, requirement); err != nil {
		return err
	}
	approval := project.ApprovalDecision{ID: "d" + factory.NewID()[:24], Project: grant.Project, Requirement: requirement.ID, Approver: int64(actor.ID), EffectsDigest: strings.Repeat("b", 64), ReadinessDigest: strings.Repeat("c", 64), Verified: true, Native: native}
	if err = db.AdmitApprovalDecision(ctx, approval); err != nil {
		return err
	}
	prep := project.Preparation{ID: "f" + factory.NewID()[:24], Project: grant.Project, Role: project.RoleCoder, SourceCommit: source, SetupDigest: requirement.SetupDigest, Requirements: requirement.Ref(1), Approval: approval.Ref(1), Tools: tools}
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
			return fmt.Errorf("native source did not become ready: %s", state.Phase)
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
	if state.SetupExit == nil || *state.SetupExit != 0 || state.CheckExit == nil || *state.CheckExit != 0 || state.SourceBundleSHA256 == "" || !strings.Contains(state.Output, "checked-by-native") {
		return errors.New("native preparation lacks actual setup/check/source facts")
	}
	record, err = db.Preparation(ctx, prep.ID)
	if err != nil {
		return err
	}
	request.Input.Preparation.Revision = record.Preparation.Revision
	if _, err = port.PrepareWorkspace(ctx, request); err != nil {
		return err
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

// The existing native integration driver can itself act as the captured local
// CLI fixture. It consumes no provider token and calls only its loopback relay.
func workerProfileFixture() error {
	selected := map[string]string{}
	for i := 1; i+1 < len(os.Args); i++ {
		switch os.Args[i] {
		case "--model", "--reasoning-effort", "--contributor-mode":
			selected[os.Args[i]] = os.Args[i+1]
		}
	}
	if selected["--model"] != "muse-spark-1.3-contributor" || selected["--reasoning-effort"] != factory.DefaultEffort || selected["--contributor-mode"] != "off" {
		return errors.New("worker did not receive the captured model/effort/Contributor profile")
	}
	if _, err := bufio.NewReader(io.LimitReader(os.Stdin, 256)).ReadString('\n'); err != nil {
		return errors.New("worker placeholder input unavailable")
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"owned-native-fixture","version":"1"}}}`
	request, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:37377/mcp", strings.NewReader(body))
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
	if response.StatusCode != http.StatusForbidden {
		return errors.New("native worker without a current Go capability was not refused")
	}
	fmt.Println("captured-profile-low-off; native-worker-MCP-missing-capability-refused")
	time.Sleep(4 * time.Second)
	return nil
}

func ownedToolFixture(profile, instance, mode string) error {
	config, err := factoryruntime.LoadNative(filepath.Join(profile, "backend.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	port := factoryruntime.NativeExecution{Config: config, Instance: instance}
	observed, err := port.InspectTool(ctx, project.FactoryHarnessMuse)
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
