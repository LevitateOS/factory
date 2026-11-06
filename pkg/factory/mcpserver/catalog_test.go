package mcpserver

// FW38/T47/T48/T51 catalog proof.
//
// The pinned SDK server is driven over a real native child on stdio
// per admitted harness and over the fixed HTTP route: discovery of
// the fourteen tools, capability-bound reads, immutable mutations
// with exact-replay receipts, conflicting-byte refusal, terminal
// freezing, cross-profile and
// unknown-tool refusal, unknown-field refusal, and the fixed-route
// shape (POST initializes as JSON, GET is 405 with no SSE, browser
// Origin refuses). The in-memory submission sink mirrors the store
// semantics the PG-gated store test proves against real PostgreSQL.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpwire"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// fakeCatalogCapability is the one admitted test capability.
func fakeCatalogCapability() factory.MCPCapability {
	digest := strings.Repeat("a", 64)
	return factory.MCPCapability{
		ID: strings.Repeat("a", 32), AssignmentID: strings.Repeat("b", 32),
		RunID: "run-test", Generation: 1,
		Profile: factory.MCPProfileBuilder, Subject: "implement:42", Repository: 7,
		Project:           "p" + strings.Repeat("d", 24),
		PreparationDigest: digest, SourceDigest: "abc", AcceptedRevision: "rev-1",
		Authority: digest, DeadlineUnix: time.Now().Add(time.Hour).Unix(),
		State: factory.MCPCapabilityActive,
	}
}

type fakeCatalogRecord struct {
	receipt  factory.MCPWorkStatus
	terminal bool
}

// fakeCatalogSink mirrors store.RecordMCPSubmission semantics in
// memory: a repeated post key returns its original work, and a terminal
// result prevents new work posts.
type fakeCatalogSink struct {
	mu          sync.Mutex
	capability  factory.MCPCapability
	submissions map[string]fakeCatalogRecord
	posts       map[string]factory.MCPSubmission
	assignments map[string]factory.Assignment
	receipts    map[string]factory.PriorityReceipt
	runs        map[string]factory.Run
}

func newFakeCatalogSink() *fakeCatalogSink {
	capability := fakeCatalogCapability()
	sink := &fakeCatalogSink{
		capability:  capability,
		submissions: map[string]fakeCatalogRecord{},
		posts:       map[string]factory.MCPSubmission{},
		receipts:    map[string]factory.PriorityReceipt{},
		runs:        map[string]factory.Run{},
		assignments: map[string]factory.Assignment{
			capability.AssignmentID: {
				ID: capability.AssignmentID, Phase: string(factory.MCPPhaseOf(capability.Profile)),
				Stage: factory.AssignmentAssigned, Role: project.RoleCoder, Run: strings.Repeat("a", 32),
				SourceCommit: strings.Repeat("c", 40), Preparation: "prep-1",
				PromptSHA: strings.Repeat("d", 64),
			},
		},
	}
	assignment := sink.assignments[capability.AssignmentID]
	request, _ := control.ContextBundleRequest(capability, assignment, false, false)
	sink.capability.ContextSelectionSnapshot = request.Snapshot
	answer := factory.PriorityAnswer{Selected: factory.ContextBundleCurrent, Confidence: 1, Model: "qualified-http-fixture", Probabilities: map[string]float64{factory.ContextBundleCurrent: 1, factory.ContextBundleSnapshotHold: 0}}
	sink.receipts[request.Snapshot] = factory.PriorityReceipt{Snapshot: request.Snapshot, Purpose: factory.JudgmentContextBundle, Outcome: "context-bundle", Selected: answer.Selected, Calls: []factory.PriorityCall{{Request: request, Answer: answer}}}
	return sink
}

// HTTP catalog fixtures model an already-qualified upstream input owner.
// Real source/grant/producer cases use *store.Store in the bound PG fixture.
func (f *fakeCatalogSink) FactoryCurrentContext(_ context.Context, cap factory.MCPCapability, _ time.Time) (factory.CurrentContext, error) {
	return factory.CurrentContext{Version: "context-current-v1", Evidence: factory.CurrentContextEvidence{RequiredStreams: []string{"required_current"}, Availability: "complete", MissingReasons: []string{}}, Role: factory.CurrentRoleContext{Kind: cap.Profile}}, nil
}

func (f *fakeCatalogSink) FactoryRun(_ context.Context, id string) (factory.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run, ok := f.runs[id]
	if !ok {
		return factory.Run{}, store.ErrNotFound
	}
	return run, nil
}

func (f *fakeCatalogSink) PriorityJudgment(_ context.Context, snapshot string) (factory.PriorityReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	receipt, ok := f.receipts[snapshot]
	if !ok {
		return factory.PriorityReceipt{}, store.ErrNotFound
	}
	return receipt, nil
}

func (f *fakeCatalogSink) Assignment(_ context.Context, id string) (factory.Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	assignment, ok := f.assignments[id]
	if !ok {
		return factory.Assignment{}, store.ErrNotFound
	}
	return assignment, nil
}

func (f *fakeCatalogSink) GetMCPCapability(_ context.Context, id string) (factory.MCPCapability, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.capability.ID {
		return factory.MCPCapability{}, store.ErrNotFound
	}
	return f.capability, nil
}

func (f *fakeCatalogSink) record(submission factory.MCPSubmission, receipt factory.MCPWorkStatus) (factory.MCPWorkStatus, error) {
	key := submission.AssignmentID + "/" + submission.SubmissionID
	if current, ok := f.submissions[key]; ok {
		return current.receipt, nil
	}
	for _, current := range f.submissions {
		if current.terminal {
			return factory.MCPWorkStatus{}, store.ErrMCPSubmissionFrozen
		}
	}
	f.submissions[key] = fakeCatalogRecord{
		receipt:  receipt,
		terminal: factory.MCPIsTerminal(submission.Tool),
	}
	return receipt, nil
}

func (f *fakeCatalogSink) RecordMCPSubmission(_ context.Context, submission factory.MCPSubmission) (factory.MCPWorkStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record(submission, factory.MCPWorkStatus{SubmissionID: submission.SubmissionID, Tool: submission.Tool, State: factory.MCPWorkRecorded})
}

func (f *fakeCatalogSink) RecordWorkerPost(_ context.Context, submission factory.MCPSubmission) (store.FeedPostDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := factory.MCPInputDigest([]byte(submission.AssignmentID + "\n" + submission.SubmissionID))[:32]
	if _, exists := f.posts[id]; !exists {
		f.posts[id] = submission
	}
	return store.FeedPostDetail{Record: factory.PostRecord{ID: id}, Assignment: submission.AssignmentID, Actor: string(f.capability.Profile)}, nil
}

func (f *fakeCatalogSink) TerminalMCPProposal(_ context.Context, assignmentID string) (factory.MCPWorkStatus, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, current := range f.submissions {
		if strings.HasPrefix(key, assignmentID+"/") && current.terminal {
			return current.receipt, true, nil
		}
	}
	return factory.MCPWorkStatus{}, false, nil
}

func (f *fakeCatalogSink) MCPSubmissionCount(_ context.Context, assignmentID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for key := range f.submissions {
		if strings.HasPrefix(key, assignmentID+"/") {
			count++
		}
	}
	return count, nil
}

func fixtureCatalogDependencies(sink *fakeCatalogSink) Dependencies {
	return Dependencies{Capabilities: sink, Submissions: sink, Assignments: sink, PriorityJudgments: sink, CurrentContext: sink, Posts: sink,
		Observed: func(factory.MCPCapability) (control.MCPRunObservation, error) {
			return control.MCPRunObservation{}, nil
		},
	}
}

func fixtureCatalogDependenciesWithRuns(sink *fakeCatalogSink) Dependencies {
	deps := fixtureCatalogDependencies(sink)
	deps.Runs = sink
	return deps
}

// Unbound protocol fixtures advertise every tool; serving construction always
// binds an admitted capability and supplies current runtime observations.
func newFixtureCatalogServer(deps Dependencies) *mcp.Server {
	return newCatalogServer(deps, factory.MCPTools)
}

// TestFactoryCatalogServerProcess is the stdio helper: catalog tests
// re-execute the test binary with SODA_FACTORY_MCP_FW38 set and
// speak MCP to this server over the child's stdin/stdout.
func TestFactoryCatalogServerProcess(t *testing.T) {
	if os.Getenv("SODA_FACTORY_MCP_FW38") == "" {
		return
	}
	sink := newFakeCatalogSink()
	server := newFixtureCatalogServer(fixtureCatalogDependencies(sink))
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		t.Logf("catalog helper server exited: %v", err)
	}
}

func catalogDial(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestFactoryCatalogServerProcess")
	cmd.Env = append(os.Environ(), "SODA_FACTORY_MCP_FW38=1")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "fw38-proof"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("initialize over stdio: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func catalogCall(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

func catalogRefused(res *mcp.CallToolResult, err error) bool {
	return err != nil || (res != nil && res.IsError)
}

func catalogText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("expected one content frame, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", res.Content[0])
	}
	return text.Text
}

// catalogPayload returns a caller payload bound to the actually
// admitted fixture capability: the caller passes the scope digest
// the server served for that capability, never a recomputed one.
// Recomputing from a fresh fakeCatalogCapability re-reads the wall
// clock, and ScopeDigest includes DeadlineUnix, so crossing a second
// between the fixture admission and the request produces a false
// stale-input refusal.
func catalogPayload(args map[string]any, scopeDigest string) (map[string]any, string) {
	return args, scopeDigest
}

// catalogScopeDigest reads the scope digest the server actually
// admitted: factory.context serves the recorded capability's current
// scope digest in its body.
func catalogScopeDigest(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(catalogText(t, res)), &body); err != nil {
		t.Fatalf("context body is not JSON: %v", err)
	}
	digest, _ := body["scope_digest"].(string)
	if len(digest) != 64 {
		t.Fatalf("context body carries no admitted scope digest: %q", body)
	}
	return digest
}

// TestFactoryCatalogTransportPerHarness proves T47/T48/T51 through
// the catalog: fourteen-tool discovery, capability-bound reads,
// immutable mutation with replay/conflict semantics, terminal
// freezing, receipt recovery and server-side refusals.
// The transport admits by recorded capability, not by harness
// string: P6 proved the exact harness pins, and the FW39 relay owns
// parent/harness authentication. Capability binding, profile
// admission and submission semantics are harness-independent here.
func TestFactoryCatalogTransportPerHarness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	session := catalogDial(t, ctx)
	capability := strings.Repeat("a", 32)

	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
		if tool.Name == string(factory.MCPToolFinishReassessment) {
			// Exercise the actual discovered schema, including the invalid
			// StallRepairReport outcome that fenced an installed native run.
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema jsonschema.Schema
			if err = json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			report := factory.StallRepairReport{StallID: strings.Repeat("1", 32), CycleID: strings.Repeat("2", 32), InventoryDigest: strings.Repeat("3", 64), SourceCommit: strings.Repeat("4", 40), PreparationDigest: strings.Repeat("5", 64), Inspected: []factory.InspectedSource{}, Causes: []factory.CheckFailure{{CheckRef: "missing_producer", Detail: "The manual prerequisite needs an authorized producer."}}, RetainedIssueIndexes: []int64{1, 2, 3}, IntentChanges: []factory.ReassessmentIntentChange{}, Coverage: []factory.ReassessmentCoverage{}, Attention: []factory.ReassessmentAttentionEntry{}}
			for _, outcome := range []string{factory.RepairOutcomeRepair, factory.RepairOutcomeVerifiedWait, factory.RepairOutcomeNoChange, factory.RepairOutcomeIncomplete, "needs_attention"} {
				report.Outcome = outcome
				payload := map[string]any{"capability_id": capability, "submission_id": strings.Repeat("7", 32), "payload": map[string]any{"subject": "stall:" + report.StallID, "report": report}}
				raw, _ := json.Marshal(payload)
				var proposal map[string]any
				if err = json.Unmarshal(raw, &proposal); err != nil {
					t.Fatal(err)
				}
				err = resolved.Validate(proposal)
				if outcome == "needs_attention" && err == nil {
					t.Fatal("discovered Strategist schema accepted an outcome outside the domain vocabulary")
				}
				if outcome != "needs_attention" && err != nil {
					t.Fatalf("discovered Strategist schema refused %s: %v", outcome, err)
				}
			}
		}
		if tool.Name == string(factory.MCPToolFinishIssuePlan) {
			// Validate through the schema actually returned over MCP, including
			// the malformed values observed in the installed Architect run.
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema jsonschema.Schema
			if err = json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			planSchema := schema.Properties["payload"].Properties["tasks"]
			if planSchema == nil || !strings.Contains(planSchema.Description, "Architect context intentionally omits verification_catalog") || !strings.Contains(planSchema.Description, "preserve all accepted task outcomes and source/check constraints") || !strings.Contains(planSchema.Properties["checks"].Items.Properties["argv"].Description, "plan-local CheckSpec") {
				t.Fatal("discovered Architect schema confuses issue checks with Product Lead catalog authority")
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			plan := factory.InvestigationPlan{
				ID: strings.Repeat("1", 32), AttemptRoot: strings.Repeat("2", 32),
				ScopeDigest: strings.Repeat("3", 64), InputDigest: strings.Repeat("4", 64),
				SourceCommit: strings.Repeat("5", 40), IntegrationCommit: strings.Repeat("5", 40),
				Preparation: "f" + strings.Repeat("6", 24), Approach: "Create one owned output", AcceptanceCoverage: []string{"requested output"},
				Tasks:  []factory.PlanTask{{ID: "proof", Objective: "Create proof", Outputs: []string{"proof.txt"}, AcceptanceRefs: []string{"requested output"}, Lane: "main", OwnedPaths: []string{"proof.txt"}, CheckIDs: []string{"native_check"}}},
				Lanes:  []factory.PlanLane{{ID: "main", TaskIDs: []string{"proof"}}},
				Checks: []factory.CheckSpec{{ID: "native_check", Argv: []string{"/bin/sh", "check.sh"}, Cwd: ".", TimeoutSeconds: 60, Scope: factory.CheckScopeIntegration, Required: true}},
			}
			plan.Digest, err = factory.PlanDigest(plan)
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"capability_id": capability, "submission_id": strings.Repeat("7", 32), "payload": map[string]any{"approach": plan.Approach, "tasks": plan, "evidence_refs": []string{}}}
			raw, _ = json.Marshal(payload)
			var valid map[string]any
			if err = json.Unmarshal(raw, &valid); err != nil {
				t.Fatal(err)
			}
			if err = resolved.Validate(valid); err != nil {
				t.Fatalf("discovered Architect schema refused a complete typed plan: %v", err)
			}
			// A worker can submit the same authored plan without echoing assignment bindings.
			var authored map[string]any
			_ = json.Unmarshal(raw, &authored)
			for _, field := range []string{"attempt_root", "source_commit", "integration_commit", "scope_digest", "input_digest", "preparation", "digest"} {
				delete(authored["payload"].(map[string]any)["tasks"].(map[string]any), field)
			}
			if err = resolved.Validate(authored); err != nil {
				t.Fatalf("discovered Architect schema required server-owned bindings: %v", err)
			}
			for _, field := range []string{"id", "integration_commit", "check_id", "check_scope", "verification_manifest_proposal", "catalog_id"} {
				var invalid map[string]any
				_ = json.Unmarshal(raw, &invalid)
				proposal := invalid["payload"].(map[string]any)["tasks"].(map[string]any)
				switch field {
				case "id":
					proposal[field] = "native-proof-plan"
				case "integration_commit":
					proposal[field] = ""
				case "check_id":
					proposal["checks"].([]any)[0].(map[string]any)["id"] = "rc-native/check.sh"
				case "check_scope":
					proposal["checks"].([]any)[0].(map[string]any)["scope"] = "proof"
				case "verification_manifest_proposal":
					invalid["payload"].(map[string]any)[field] = map[string]any{"checks": []any{}}
				case "catalog_id":
					proposal["checks"].([]any)[0].(map[string]any)[field] = factory.ApprovedAppCheckID
				}
				if err = resolved.Validate(invalid); err == nil {
					t.Fatalf("discovered Architect schema accepted the installed invalid %s", field)
				}
			}
		}
	}
	if len(seen) != len(factory.MCPTools) {
		t.Fatalf("discovery advertises %d tools, want %d", len(seen), len(factory.MCPTools))
	}
	for _, tool := range factory.MCPTools {
		if !seen[string(tool)] {
			t.Fatalf("catalog tool %s missing from discovery", tool)
		}
	}

	reads := map[string]any{"capability_id": capability}
	res, err := catalogCall(t, ctx, session, "factory.context", reads)
	if err != nil {
		t.Fatalf("allowed read: %v", err)
	}
	if body := catalogText(t, res); !strings.Contains(body, strings.Repeat("b", 32)) || !strings.Contains(body, "builder") {
		t.Fatalf("context body %q lacks assignment/profile", body)
	}
	// Bind every later mutation to the actually admitted fixture
	// capability's scope digest. Recomputing a fresh fixture digest
	// here would re-read the wall clock across a possible second
	// boundary and refuse as stale.
	admittedDigest := catalogScopeDigest(t, res)
	res, err = catalogCall(t, ctx, session, "factory.status", reads)
	if err != nil {
		t.Fatalf("status read: %v", err)
	}
	if body := catalogText(t, res); !strings.Contains(body, `"terminal":false`) {
		t.Fatalf("status body %q lacks live terminal state", body)
	}
	res, err = catalogCall(t, ctx, session, "factory.evidence", reads)
	if err != nil {
		t.Fatalf("evidence read: %v", err)
	}
	if body := catalogText(t, res); !strings.Contains(body, `"complete":false`) {
		t.Fatalf("evidence body %q is not an honest incomplete", body)
	}

	// Unknown capability refuses without disclosure.
	if res, err := catalogCall(t, ctx, session, "factory.context", map[string]any{"capability_id": strings.Repeat("c", 32)}); !catalogRefused(res, err) {
		t.Fatal("unknown capability admitted")
	}
	// Unknown tool refuses server-side.
	if res, err := catalogCall(t, ctx, session, "factory.merge", reads); !catalogRefused(res, err) {
		t.Fatal("unknown tool admitted")
	}
	// Unknown top-level field refuses server-side.
	extra := map[string]any{"capability_id": capability, "role": "admin"}
	if res, err := catalogCall(t, ctx, session, "factory.context", extra); !catalogRefused(res, err) {
		t.Fatal("unknown envelope field admitted")
	}
	// Cross-profile direct call refuses: builder cannot finish reviews.
	reviewPayload, _ := catalogPayload(map[string]any{
		"conclusion": "clear", "coverage": "full", "evidence_refs": []any{"r1"},
	}, admittedDigest)
	if res, err := catalogCall(t, ctx, session, "factory.finish_review", map[string]any{
		"capability_id": capability, "submission_id": strings.Repeat("1", 32),
		"payload": reviewPayload,
	}); !catalogRefused(res, err) {
		t.Fatal("cross-profile direct call admitted")
	}

	// Immutable mutation: record, replay, conflict, recovery.
	implPayload, _ := catalogPayload(map[string]any{"candidate": strings.Repeat("e", 40)}, admittedDigest)
	finishID := strings.Repeat("2", 32)
	finish := map[string]any{
		"capability_id": capability, "submission_id": finishID,
		"payload": implPayload,
	}
	first, err := catalogCall(t, ctx, session, "factory.finish_implementation", finish)
	if err != nil {
		t.Fatalf("typed finish: %v", err)
	}
	if body := catalogText(t, first); !strings.Contains(body, `"state":"recorded"`) {
		t.Fatalf("finish receipt %q not recorded", body)
	}
	replay, err := catalogCall(t, ctx, session, "factory.finish_implementation", finish)
	if err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if catalogText(t, replay) != catalogText(t, first) {
		t.Fatal("replay receipt differs from recorded receipt")
	}
	clashPayload, _ := catalogPayload(map[string]any{"unchanged": true}, admittedDigest)
	clash := map[string]any{
		"capability_id": capability, "submission_id": finishID,
		"payload": clashPayload,
	}
	if res, err := catalogCall(t, ctx, session, "factory.finish_implementation", clash); !catalogRefused(res, err) {
		t.Fatal("conflicting bytes under recorded ID admitted")
	}
	// Terminal freeze: a second terminal identity and post-terminal
	// output both refuse.
	otherPayload, _ := catalogPayload(map[string]any{"candidate": "cand-2"}, admittedDigest)
	if res, err := catalogCall(t, ctx, session, "factory.finish_implementation", map[string]any{
		"capability_id": capability, "submission_id": strings.Repeat("4", 32),
		"payload": otherPayload,
	}); !catalogRefused(res, err) {
		t.Fatal("second terminal identity admitted")
	}
	outputPayload, _ := catalogPayload(map[string]any{
		"task_id": "t1", "manifest_digest": strings.Repeat("d", 64),
		"output_digest": strings.Repeat("e", 64), "evidence_refs": []any{"r1"},
	}, admittedDigest)
	if res, err := catalogCall(t, ctx, session, "factory.submit_task_output", map[string]any{
		"capability_id": capability, "submission_id": strings.Repeat("5", 32),
		"payload": outputPayload,
	}); !catalogRefused(res, err) {
		t.Fatal("post-terminal output admitted")
	}

	// Manifest proposal without an approved catalog names the gate.
	proposalPayload, _ := catalogPayload(map[string]any{
		"candidates":    []any{"g1"},
		"evidence_refs": []any{"r1"},
		"coverage":      "both outcomes",
		"verification_manifest_proposal": map[string]any{
			"outcomes": []any{"checkout"},
			"checks": []any{map[string]any{
				"outcome": "checkout", "catalog_id": "web.journey.smoke.v3",
				"params":    map[string]any{"journey": "guest"},
				"artifacts": []any{"log"},
			}},
		},
	}, admittedDigest)
	product := map[string]any{
		"capability_id": capability, "submission_id": strings.Repeat("6", 32),
		"payload": proposalPayload,
	}
	// The test capability is a builder: product plans refuse on
	// profile first. The catalog-gate leg runs in-process below.
	if res, err := catalogCall(t, ctx, session, "factory.finish_product_plan", product); !catalogRefused(res, err) {
		t.Fatal("cross-profile product plan admitted")
	}
}

// TestFactoryCatalogReadsSpendNothingAndDiscloseNothing proves the R31
// read contract through the actual handlers: context, status, evidence
// and receipt record no submission rows, unknown capabilities refuse on
// every read, and evidence stays
// honestly incomplete instead of an empty success.
func TestFactoryCatalogReadsSpendNothingAndDiscloseNothing(t *testing.T) {
	sink := newFakeCatalogSink()
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	ctx := context.Background()
	admitted := sink.capability.ScopeDigest()
	reads := func(tool factory.MCPTool, capabilityID, submissionID string) mcpCallArgs {
		return mcpCallArgs{CapabilityID: capabilityID, SubmissionID: submissionID}
	}
	count := func() int {
		n, err := sink.MCPSubmissionCount(ctx, sink.capability.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tool := range []factory.MCPTool{factory.MCPToolContext, factory.MCPToolStatus, factory.MCPToolEvidence} {
		res, _, err := server.serve(ctx, tool, reads(tool, sink.capability.ID, ""))
		if err != nil {
			t.Fatalf("%s read refused: %v", tool, err)
		}
		if tool == factory.MCPToolEvidence {
			var body map[string]any
			text, ok := res.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("evidence content is %T, not text", res.Content[0])
			}
			if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
				t.Fatalf("evidence body is not JSON: %v", err)
			}
			if complete, _ := body["complete"].(bool); complete {
				t.Fatalf("evidence claims completeness: %q", text.Text)
			}
			if reason, _ := body["reason"].(string); reason == "" {
				t.Fatalf("incomplete evidence names no reason: %q", text.Text)
			}
		}
	}
	if count() != 0 {
		t.Fatalf("reads recorded %d submissions", count())
	}
	unknown := strings.Repeat("f", 32)
	for _, tool := range []factory.MCPTool{factory.MCPToolContext, factory.MCPToolStatus, factory.MCPToolEvidence} {
		if _, _, err := server.serve(ctx, tool, reads(tool, unknown, factory.NewID())); err == nil {
			t.Fatalf("%s read admitted an unknown capability", tool)
		}
	}
	if count() != 0 {
		t.Fatalf("refused reads recorded %d submissions", count())
	}
	submissionID := factory.NewID()
	mutPayload, _ := catalogPayload(map[string]any{
		"task_id": "t1", "manifest_digest": strings.Repeat("d", 64),
		"output_digest": strings.Repeat("e", 64), "evidence_refs": []any{"r1"},
	}, admitted)
	if _, _, err := server.serve(ctx, factory.MCPToolSubmitTaskOutput, mcpCallArgs{
		CapabilityID: sink.capability.ID, SubmissionID: submissionID,
		Payload: mutPayload,
	}); err != nil {
		t.Fatalf("recording mutation refused: %v", err)
	}
	if count() != 1 {
		t.Fatalf("recorded %d submissions, want exactly the mutation", count())
	}
}

// TestFactoryCatalogContextProjectsAssignmentCurrent proves the R31
// current-context projection through the actual handler: context serves
// the recorded assignment's prompt digest, exact source/preparation
// and, once set, outcome/reason — never prompt bytes, run history or
// actor identity. A capability without its assignment refuses instead
// of serving context without it.
func TestFactoryCatalogContextProjectsAssignmentCurrent(t *testing.T) {
	sink := newFakeCatalogSink()
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	ctx := context.Background()
	contextBody := func() map[string]any {
		t.Helper()
		res, _, err := server.serve(ctx, factory.MCPToolContext, mcpCallArgs{
			CapabilityID: sink.capability.ID,
		})
		if err != nil {
			t.Fatalf("context refused: %v", err)
		}
		text, ok := res.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("context content is %T, not text", res.Content[0])
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
			t.Fatalf("context body is not JSON: %v", err)
		}
		return body
	}
	body := contextBody()
	for key, want := range map[string]string{
		"prompt_sha":    strings.Repeat("d", 64),
		"source_commit": strings.Repeat("c", 40),
		"preparation":   "prep-1",
	} {
		if body[key] != want {
			t.Fatalf("context %s = %v, want %s", key, body[key], want)
		}
	}
	for _, absent := range []string{"outcome", "reason", "prompt", "run_history", "actor_id"} {
		if _, ok := body[absent]; ok {
			t.Fatalf("unset context carries %q: %v", absent, body[absent])
		}
	}
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.Outcome, stored.Reason = factory.Succeeded, "settled"
	sink.assignments[sink.capability.AssignmentID] = stored
	body = contextBody()
	if body["outcome"] != string(factory.Succeeded) || body["reason"] != "settled" {
		t.Fatalf("settled context lacks outcome/reason: %v", body)
	}
	delete(sink.assignments, sink.capability.AssignmentID)
	if _, _, err := server.serve(ctx, factory.MCPToolContext, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	}); err == nil {
		t.Fatal("context without its assignment admitted")
	}
}

// TestFactoryCatalogContextServesAttemptBundle proves the R31 attempt
// bundle through the actual handler: own-attempt run identities join
// as opaque IDs inside the attempt section, the bundle names attempt,
// and prompt bytes stay out of the projection.
func TestFactoryCatalogContextServesAttemptBundle(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"run-1", "run-2"}
	sink.assignments[sink.capability.AssignmentID] = stored
	storeSelectionReceipt(t, sink, stored, true, false, factory.ContextBundleAttempt)
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	res, _, err := server.serve(context.Background(), factory.MCPToolContext, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	})
	if err != nil {
		t.Fatalf("context refused: %v", err)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("context content is %T, not text", res.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
		t.Fatalf("context body is not JSON: %v", err)
	}
	attempt, ok := body["attempt"].(map[string]any)
	if !ok {
		t.Fatalf("attempt section missing: %v", body)
	}
	runs, ok := attempt["run_history"].([]any)
	if !ok || len(runs) != 2 || runs[0] != "run-1" || runs[1] != "run-2" {
		t.Fatalf("attempt run_history=%v", attempt["run_history"])
	}
	if body["bundle"] != factory.ContextBundleAttempt {
		t.Fatalf("bundle=%v, want attempt", body["bundle"])
	}
	for _, absent := range []string{"prompt", "run_history", "actor_id"} {
		if _, ok := body[absent]; ok {
			t.Fatalf("top level carries %q", absent)
		}
	}
}

// TestFactoryCatalogContextServesLineageBundle proves the R31 lineage
// bundle through the actual handler: the linked parent's outcome and
// reason join the lineage section, and the bundle names lineage.
func TestFactoryCatalogContextServesLineageBundle(t *testing.T) {
	sink := newFakeCatalogSink()
	parentID := factory.NewID()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.ParentAssignment = parentID
	stored.ActorID = 7
	stored.Repository, stored.ProjectID = sink.capability.Repository, sink.capability.Project
	stored.AttemptRoot = stored.ID
	sink.assignments[sink.capability.AssignmentID] = stored
	sink.assignments[parentID] = factory.Assignment{
		ID: parentID, ActorID: 7, Repository: stored.Repository, ProjectID: stored.ProjectID, AttemptRoot: stored.AttemptRoot, Outcome: factory.Succeeded, Reason: "parent settled",
	}
	storeSelectionReceipt(t, sink, stored, false, true, factory.ContextBundleLineage)
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	res, _, err := server.serve(context.Background(), factory.MCPToolContext, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	})
	if err != nil {
		t.Fatalf("context refused: %v", err)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("context content is %T, not text", res.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
		t.Fatalf("context body is not JSON: %v", err)
	}
	lineage, ok := body["lineage"].(map[string]any)
	if !ok {
		t.Fatalf("lineage section missing: %v", body)
	}
	if lineage["parent_assignment"] != parentID || lineage["parent_outcome"] != string(factory.Succeeded) || lineage["parent_reason"] != "parent settled" {
		t.Fatalf("lineage=%v", lineage)
	}
	if body["bundle"] != factory.ContextBundleLineage {
		t.Fatalf("bundle=%v, want lineage", body["bundle"])
	}
}

// TestFactoryCatalogContextMarksIncompleteLineage proves a linked but
// unloadable parent stays honest: the read still succeeds on current
// content with lineage_incomplete set, never a lineage section that
// claims parent facts.
func TestFactoryCatalogContextMarksIncompleteLineage(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.ParentAssignment = factory.NewID()
	sink.assignments[sink.capability.AssignmentID] = stored
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	res, _, err := server.serve(context.Background(), factory.MCPToolContext, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	})
	if err != nil {
		t.Fatalf("context refused: %v", err)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("context content is %T, not text", res.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
		t.Fatalf("context body is not JSON: %v", err)
	}
	if _, ok := body["lineage"]; ok {
		t.Fatalf("unloadable parent served lineage: %v", body["lineage"])
	}
	if body["lineage_incomplete"] != true {
		t.Fatalf("missing lineage_incomplete marker: %v", body)
	}
	if body["bundle"] != factory.ContextBundleCurrent {
		t.Fatalf("bundle=%v, want current", body["bundle"])
	}
}

// serveContextBody serves factory.context through the actual handler
// with the sink wired as every source, including selection receipts.
func serveContextBody(t *testing.T, sink *fakeCatalogSink) map[string]any {
	t.Helper()
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	res, _, err := server.serve(context.Background(), factory.MCPToolContext, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	})
	if err != nil {
		t.Fatalf("context refused: %v", err)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("context content is %T, not text", res.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
		t.Fatalf("context body is not JSON: %v", err)
	}
	return body
}

func storeSelectionReceipt(t *testing.T, sink *fakeCatalogSink, assignment factory.Assignment, attemptEligible, lineageEligible bool, selected string) {
	t.Helper()
	request, err := control.ContextBundleRequest(sink.capability, assignment, attemptEligible, lineageEligible)
	if err != nil {
		t.Fatalf("selection fingerprint: %v", err)
	}
	fingerprint := request.Snapshot
	sink.capability.ContextSelectionSnapshot = fingerprint
	options := []string{factory.ContextBundleCurrent, factory.ContextBundleSnapshotHold}
	if attemptEligible {
		options = append(options, factory.ContextBundleAttempt)
	}
	if lineageEligible {
		options = append(options, factory.ContextBundleLineage)
	}
	answer := factory.PriorityAnswer{Selected: selected, Model: "fixture", Confidence: 1, Probabilities: map[string]float64{}}
	for _, option := range options {
		answer.Probabilities[option] = 0
	}
	answer.Probabilities[selected] = 1
	sink.receipts[fingerprint] = factory.PriorityReceipt{Snapshot: fingerprint, Purpose: factory.JudgmentContextBundle, Outcome: "context-bundle", Selected: selected, Calls: []factory.PriorityCall{{Request: request, Answer: answer}}}
}

// TestFactoryCatalogContextForegroundsRecordedCurrent proves a
// preparation-time selection narrows the served sections to the
// selected bundle without spending: attempt content exists, but the
// recorded current selection serves current-only.
func TestFactoryCatalogContextForegroundsRecordedCurrent(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"run-1"}
	sink.assignments[sink.capability.AssignmentID] = stored
	storeSelectionReceipt(t, sink, stored, true, false, factory.ContextBundleCurrent)
	body := serveContextBody(t, sink)
	if _, ok := body["attempt"]; ok {
		t.Fatalf("foregrounded current serves attempt: %v", body["attempt"])
	}
	if body["bundle"] != factory.ContextBundleCurrent {
		t.Fatalf("bundle=%v, want current", body["bundle"])
	}
}

// TestFactoryCatalogContextForegroundsRecordedHold proves a
// snapshot_hold selection serves the deterministic current-only
// fallback.
func TestFactoryCatalogContextForegroundsRecordedHold(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"run-1"}
	sink.assignments[sink.capability.AssignmentID] = stored
	storeSelectionReceipt(t, sink, stored, true, false, factory.ContextBundleSnapshotHold)
	body := serveContextBody(t, sink)
	if _, ok := body["attempt"]; ok {
		t.Fatalf("hold serves attempt: %v", body["attempt"])
	}
	if body["bundle"] != factory.ContextBundleCurrent {
		t.Fatalf("bundle=%v, want current", body["bundle"])
	}
}

// TestFactoryCatalogContextForegroundsRecordedLineage proves a
// lineage selection serves the lineage section it chose.
func TestFactoryCatalogContextForegroundsRecordedLineage(t *testing.T) {
	sink := newFakeCatalogSink()
	parentID := factory.NewID()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.ParentAssignment = parentID
	stored.ActorID = 7
	stored.Repository, stored.ProjectID = sink.capability.Repository, sink.capability.Project
	stored.AttemptRoot = stored.ID
	sink.assignments[sink.capability.AssignmentID] = stored
	sink.assignments[parentID] = factory.Assignment{ID: parentID, ActorID: 7, Repository: stored.Repository, ProjectID: stored.ProjectID, AttemptRoot: stored.AttemptRoot, Outcome: factory.Succeeded}
	storeSelectionReceipt(t, sink, stored, false, true, factory.ContextBundleLineage)
	body := serveContextBody(t, sink)
	if _, ok := body["lineage"]; !ok {
		t.Fatalf("foregrounded lineage misses section: %v", body)
	}
	if body["bundle"] != factory.ContextBundleLineage {
		t.Fatalf("bundle=%v, want lineage", body["bundle"])
	}
}

// TestFactoryCatalogContextIgnoresForeignReceipt proves a receipt
// recorded for another purpose never foregrounds context: the widest
// eligible history is withheld with a hold instead.
func TestFactoryCatalogContextIgnoresForeignReceipt(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"run-1"}
	sink.assignments[sink.capability.AssignmentID] = stored
	fingerprint, err := control.ContextBundleFingerprint(sink.capability, stored, true, false)
	if err != nil {
		t.Fatalf("selection fingerprint: %v", err)
	}
	sink.receipts[fingerprint] = factory.PriorityReceipt{
		Snapshot: fingerprint, Purpose: factory.JudgmentConsolidation,
		Outcome: "consolidation", Selected: "a",
	}
	body := serveContextBody(t, sink)
	if _, ok := body["attempt"]; ok {
		t.Fatalf("foreign receipt broadened context: %v", body)
	}
	if body["bundle"] != factory.ContextBundleCurrent {
		t.Fatalf("bundle=%v, want current", body["bundle"])
	}
}

// TestFactoryCatalogContextIgnoresUnknownSelection proves an
// unrecognized selected bundle never foregrounds context.
func TestFactoryCatalogContextIgnoresUnknownSelection(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"run-1"}
	sink.assignments[sink.capability.AssignmentID] = stored
	storeSelectionReceipt(t, sink, stored, true, false, "invented")
	body := serveContextBody(t, sink)
	if _, ok := body["attempt"]; ok {
		t.Fatalf("unknown selection broadened context: %v", body)
	}
	if body["bundle"] != factory.ContextBundleCurrent {
		t.Fatalf("bundle=%v, want current", body["bundle"])
	}
}

// serveEvidenceBody serves factory.evidence through the actual
// handler with the sink wired as every source.
func serveEvidenceBody(t *testing.T, sink *fakeCatalogSink) map[string]any {
	t.Helper()
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependenciesWithRuns(sink),
		limiter: newMCPRateLimiter(),
	}
	res, _, err := server.serve(context.Background(), factory.MCPToolEvidence, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	})
	if err != nil {
		t.Fatalf("evidence refused: %v", err)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("evidence content is %T, not text", res.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text.Text), &body); err != nil {
		t.Fatalf("evidence body is not JSON: %v", err)
	}
	return body
}

// TestFactoryCatalogEvidenceServesRunProjections proves the R31
// bounded evidence projection: own-attempt runs serve as opaque
// identities with role and outcome only, while evidence stays
// honestly incomplete pending plan/review/inventory pages.
func TestFactoryCatalogEvidenceServesRunProjections(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"run-1", "run-2"}
	sink.assignments[sink.capability.AssignmentID] = stored
	sink.runs["run-1"] = factory.Run{ID: "run-1", Role: "coder", Outcome: factory.Succeeded, Summary: "must never surface", Model: "must never surface"}
	sink.runs["run-2"] = factory.Run{ID: "run-2", Role: "reviewer"}
	body := serveEvidenceBody(t, sink)
	runs, ok := body["runs"].([]any)
	if !ok || len(runs) != 2 {
		t.Fatalf("runs=%v", body["runs"])
	}
	first, ok := runs[0].(map[string]any)
	if !ok || first["id"] != "run-1" || first["role"] != "coder" || first["outcome"] != string(factory.Succeeded) {
		t.Fatalf("first run=%v", runs[0])
	}
	if len(first) != 3 {
		t.Fatalf("run projects %d keys, want only id/role/outcome: %v", len(first), first)
	}
	serialized, _ := json.Marshal(body)
	for _, leaked := range []string{"must never surface"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("evidence leaks run content: %s", serialized)
		}
	}
	if complete, _ := body["complete"].(bool); complete {
		t.Fatalf("evidence claims completeness: %s", serialized)
	}
	if reason, _ := body["reason"].(string); reason == "" {
		t.Fatalf("incomplete evidence names no reason: %s", serialized)
	}
	if body["assignment"] != sink.capability.AssignmentID {
		t.Fatalf("evidence unscoped: %v", body["assignment"])
	}
}

// TestFactoryCatalogEvidenceTruncatesLongHistory proves the page-ref
// bound: history beyond MCPMaxPageRefs marks runs_truncated instead
// of growing the page.
func TestFactoryCatalogEvidenceTruncatesLongHistory(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	for i := 0; i < factory.MCPMaxPageRefs+2; i++ {
		id := strings.Repeat("r", 31) + string(rune('a'+i))
		stored.RunHistory = append(stored.RunHistory, id)
		sink.runs[id] = factory.Run{ID: id, Role: "coder"}
	}
	sink.assignments[sink.capability.AssignmentID] = stored
	body := serveEvidenceBody(t, sink)
	runs, ok := body["runs"].([]any)
	if !ok || len(runs) != factory.MCPMaxPageRefs {
		t.Fatalf("runs=%d, want bound %d", len(runs), factory.MCPMaxPageRefs)
	}
	if body["runs_truncated"] != true {
		t.Fatalf("missing runs_truncated marker: %v", body)
	}
}

// TestFactoryCatalogEvidenceRefusesUnresolvableRun proves a recorded
// run without its row refuses rather than silently dropping it, and a
// missing assignment refuses instead of serving unscoped rows.
func TestFactoryCatalogEvidenceRefusesUnresolvableRun(t *testing.T) {
	sink := newFakeCatalogSink()
	stored := sink.assignments[sink.capability.AssignmentID]
	stored.RunHistory = []string{"ghost-run"}
	sink.assignments[sink.capability.AssignmentID] = stored
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependenciesWithRuns(sink),
		limiter: newMCPRateLimiter(),
	}
	if _, _, err := server.serve(context.Background(), factory.MCPToolEvidence, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	}); err == nil {
		t.Fatal("unresolvable recorded run admitted")
	}
	delete(sink.assignments, sink.capability.AssignmentID)
	if _, _, err := server.serve(context.Background(), factory.MCPToolEvidence, mcpCallArgs{
		CapabilityID: sink.capability.ID,
	}); err == nil {
		t.Fatal("evidence without its assignment admitted")
	}
}

// TestFactoryCatalogManifestGate proves the D16 release leg: a plan
// carrying a manifest proposal validates against the catalog, and a
// missing catalog names the unwired gate instead of inventing
// approval.
func TestFactoryCatalogManifestGate(t *testing.T) {
	sink := newFakeCatalogSink()
	sink.capability.Profile = factory.MCPProfileProductLead
	storeSelectionReceipt(t, sink, sink.assignments[sink.capability.AssignmentID], false, false, factory.ContextBundleCurrent)
	server := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(sink),
		limiter: newMCPRateLimiter(),
	}
	ctx := context.Background()
	argsFor := func(payload map[string]any) mcpCallArgs {
		return mcpCallArgs{
			CapabilityID: sink.capability.ID, SubmissionID: factory.NewID(),
			Payload: payload,
		}
	}
	plan := map[string]any{
		"candidates": []any{"g1"}, "evidence_refs": []any{"r1"}, "coverage": "all",
		"verification_manifest_proposal": map[string]any{
			"outcomes": []any{"checkout"},
			"checks": []any{map[string]any{
				"outcome": "checkout", "catalog_id": "web.journey.smoke.v3",
				"params": map[string]any{"journey": "guest"}, "artifacts": []any{"log"},
			}},
		},
	}
	if _, _, err := server.serve(ctx, factory.MCPToolFinishProductPlan, argsFor(plan)); err == nil {
		t.Fatal("manifest proposal approved without a catalog")
	} else if !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("refusal %q does not name the catalog gate", err)
	}
	plain := map[string]any{
		"candidates": []any{"g1"}, "evidence_refs": []any{"r1"}, "coverage": "all",
	}
	// A loose plan that settlement could not map is refused before it can
	// freeze the assignment.
	if _, _, err := server.serve(ctx, factory.MCPToolFinishProductPlan, argsFor(plain)); err == nil || !strings.Contains(err.Error(), "nothing recorded") {
		t.Fatalf("unmappable plan was not refused before recording: %v", err)
	}
	// The FJ-v3-553 optional-carrier rule: documented optional fields
	// ride the same finish without changing the required outcome. The
	// recorded plain terminal freezes its assignment, so a fresh
	// assignment proves the optional shape.
	optionalSink := newFakeCatalogSink()
	optionalSink.capability.Profile = factory.MCPProfileProductLead
	storeSelectionReceipt(t, optionalSink, optionalSink.assignments[optionalSink.capability.AssignmentID], false, false, factory.ContextBundleCurrent)
	optionalServer := &factoryCatalogServer{
		deps:    fixtureCatalogDependencies(optionalSink),
		limiter: newMCPRateLimiter(),
	}
	withOptional := map[string]any{
		"candidates": []any{"g1"}, "evidence_refs": []any{"r1"}, "coverage": "all",
		"questions": []any{"What remains open?"}, "component_id": "PL11",
	}
	if _, _, err := optionalServer.serve(ctx, factory.MCPToolFinishProductPlan, mcpCallArgs{
		CapabilityID: optionalSink.capability.ID, SubmissionID: factory.NewID(),
		Payload: withOptional,
	}); err == nil || strings.Contains(err.Error(), "unknown payload field") {
		t.Fatalf("documented optional carriers did not reach the settlement check: %v", err)
	}
	// The FJ-v3-526 worker inlined the admitted objective+facts as
	// top-level finish arguments and was refused before settlement:
	// unknown payload fields never reach the settler, so the 64 KiB
	// cap was never the gate. The worker-facing schema carries the
	// same finish-args guidance as the staged prompt.
	inlined := map[string]any{
		"candidates": []any{"g1"}, "evidence_refs": []any{"r1"}, "coverage": "all",
		"objective": "Record the draft plan.", "facts": []any{"Record the draft plan."},
	}
	if _, _, err := server.serve(ctx, factory.MCPToolFinishProductPlan, argsFor(inlined)); err == nil {
		t.Fatal("inlined objective/facts admitted as finish arguments")
	} else if !strings.Contains(err.Error(), "unknown payload field") {
		t.Fatalf("inline refusal %q does not name the unknown-field gate", err)
	}
	schema := mcpwire.Tool(factory.MCPToolFinishProductPlan, nil).InputSchema.(*jsonschema.Schema).Properties["payload"]
	if schema == nil {
		t.Fatal("finish_product_plan schema missing from worker discovery")
	}
	for _, key := range []string{"candidates", "walkthroughs"} {
		description := schema.Properties[key].Description
		if !strings.Contains(description, control.FinishProductPlanArgsGuidance) {
			t.Fatalf("%s schema omits the finish-args guidance the worker reads", key)
		}
	}
}

// TestFactoryCatalogRateLimit bounds traffic per run without
// recording receipts.
func TestFactoryCatalogRateLimit(t *testing.T) {
	limiter := newMCPRateLimiter()
	now := time.Now()
	limiter.now = func() time.Time { return now }
	var releases []func()
	for range 60 {
		release, err := limiter.admit("run-1")
		if err != nil {
			t.Fatalf("admitted call refused: %v", err)
		}
		releases = append(releases, release)
		for _, release := range releases {
			release()
		}
		releases = releases[:0]
	}
	if _, err := limiter.admit("run-1"); err == nil {
		t.Fatal("61st call in the minute admitted")
	}
	limiter.now = func() time.Time { return now.Add(61 * time.Second) }
	release, err := limiter.admit("run-1")
	if err != nil {
		t.Fatalf("fresh window refused: %v", err)
	}
	release()
	if _, err := limiter.admit("unknown"); err != nil {
		t.Fatalf("other run refused: %v", err)
	}
}

// TestFactoryCatalogHTTPRoute proves the fixed serving route: POST
// /mcp initializes through the SDK handler with a JSON response,
// GET returns 405 with no SSE stream, and browser Origin refuses.
func TestFactoryCatalogHTTPRoute(t *testing.T) {
	sink := newFakeCatalogSink()
	srv := httptest.NewServer(factoryCatalogMux(newFixtureCatalogServer(fixtureCatalogDependencies(sink))))
	defer srv.Close()

	if res, err := http.Get(srv.URL + factoryhttpapi.WorkerRoute); err != nil {
		t.Fatal(err)
	} else {
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET /mcp = %d, want 405", res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); strings.Contains(ct, "text/event-stream") {
			t.Fatalf("GET /mcp serves SSE content %q", ct)
		}
	}

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"fw38-proof","version":"1.0"}}}`
	post := func(origin string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, srv.URL+factoryhttpapi.WorkerRoute, strings.NewReader(initBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := post("")
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /mcp initialize = %d (%s), want 200", res.StatusCode, raw)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("POST /mcp content-type = %q, want JSON", ct)
	}
	res = post("https://example.test")
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /mcp with Origin = %d, want 403", res.StatusCode)
	}
}

// factoryCatalogMux serves the catalog on the fixed route only: POST
// reaches the SDK handler (stateless JSON, no SSE); every other
// method — including the SSE-establishing GET — returns 405.
// Requests carrying a browser Origin never belong on this private
// listener and refuse before the SDK sees them.
func factoryCatalogMux(server *mcp.Server) *http.ServeMux {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc(factoryhttpapi.WorkerRoute, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "origin refused", http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r)
	})
	return mux
}
