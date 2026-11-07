// Package mcpserver serves the role-filtered Factory worker MCP protocol using
// explicit application and runtime ports. Host composition owns listeners,
// credentials and process lifetime; the shared service and store own work state.
package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpwire"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// mcpCallArgs is the tool envelope. The bound server supplies capability
// identity and scope; the worker sends only its payload and, optionally, a
// submission_id to identify the submission. CapabilityID remains an internal
// admission field used by the unbound transport fixtures.
type mcpCallArgs struct {
	CapabilityID string         `json:"capability_id" jsonschema:"recorded capability identity"`
	SubmissionID string         `json:"submission_id,omitempty" jsonschema:"optional submission identity"`
	Payload      map[string]any `json:"payload,omitempty" jsonschema:"tool payload within 64 KiB"`
}

// mcpRateLimiter bounds traffic per run: 4 concurrent requests and
// 60 admitted calls per minute. Limits never extend deadlines;
// refusals are bounded protocol errors creating no receipt.
type mcpRateLimiter struct {
	mu     sync.Mutex
	active map[string]int
	window map[string]time.Time
	calls  map[string]int
	now    func() time.Time
}

func newMCPRateLimiter() *mcpRateLimiter {
	return &mcpRateLimiter{
		active: map[string]int{}, window: map[string]time.Time{},
		calls: map[string]int{}, now: time.Now,
	}
}

func (l *mcpRateLimiter) admit(runID string) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if start, ok := l.window[runID]; !ok || now.Sub(start) >= time.Minute {
		l.window[runID] = now
		l.calls[runID] = 0
	}
	if l.active[runID] >= factory.MCPMaxConcurrentRequests {
		return nil, errors.New("too many concurrent factory requests")
	}
	if l.calls[runID] >= factory.MCPMaxCallsPerMinute {
		return nil, errors.New("factory call rate exceeded")
	}
	l.calls[runID]++
	l.active[runID]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.active[runID]--
	}, nil
}

type factoryCatalogServer struct {
	deps    Dependencies
	limiter *mcpRateLimiter
}

func (s *factoryCatalogServer) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}

func (s *factoryCatalogServer) observed(capability factory.MCPCapability) (control.MCPRunObservation, error) {
	if s.deps.Observed == nil {
		return control.MCPRunObservation{}, errors.New("factory run observation unavailable")
	}
	observed, err := s.deps.Observed(capability)
	if err != nil {
		return control.MCPRunObservation{}, err
	}
	if s.deps.boundCapability != nil && (observed.RunID != capability.RunID || observed.AssignmentID != capability.AssignmentID || observed.Authority != capability.Authority) {
		return control.MCPRunObservation{}, errors.New("factory run observation differs from worker context")
	}
	return observed, nil
}

// admitCall runs the shared gate: capability load, profile/tool
// binding, freshness/authority and the rate limit. It returns the
// admitted capability and the rate release the caller defers.
func (s *factoryCatalogServer) admitCall(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (factory.MCPCapability, func(), error) {
	if bound := s.deps.boundCapability; bound != nil {
		if args.CapabilityID != "" && args.CapabilityID != bound.ID {
			return factory.MCPCapability{}, nil, errors.New("tool identity differs from this worker's context: omit capability_id, or pass the capability_id from factory.context")
		}
		args.CapabilityID = bound.ID
	}
	if !factory.ValidID(args.CapabilityID) {
		return factory.MCPCapability{}, nil, errors.New("factory capability refused: capability_id must be the 32-hex capability_id from factory.context")
	}
	capability, err := s.deps.Capabilities.GetMCPCapability(ctx, args.CapabilityID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return factory.MCPCapability{}, nil, errors.New("factory capability refused: no recorded capability has this capability_id; use the one from factory.context")
		}
		return factory.MCPCapability{}, nil, errors.New("factory capability unavailable: the capability store is unreachable; retry with the same capability_id")
	}
	if bound := s.deps.boundCapability; bound != nil && capability.ScopeDigest() != bound.ScopeDigest() {
		return factory.MCPCapability{}, nil, errors.New("worker context changed; reconnect to the assigned server")
	}
	observed, err := s.observed(capability)
	if err != nil {
		return factory.MCPCapability{}, nil, errors.New("factory run observation unavailable")
	}
	if err := control.AdmitMCPCapability(capability, tool, s.now(), observed); err != nil {
		return factory.MCPCapability{}, nil, err
	}
	release, err := s.limiter.admit(capability.RunID)
	if err != nil {
		return factory.MCPCapability{}, nil, err
	}
	return capability, release, nil
}

// admitMutation runs the shared mutation gate after admitCall:
// payload shape, required work inputs and the role's finish checks.
func (s *factoryCatalogServer) admitMutation(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (factory.MCPCapability, factory.MCPSubmission, func(), error) {
	capability, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return factory.MCPCapability{}, factory.MCPSubmission{}, nil, err
	}
	s.workerResults().Bind(ctx, capability, tool, args.Payload)
	raw, err := mcpwire.CanonicalPayload(args.Payload)
	if err != nil {
		release()
		return factory.MCPCapability{}, factory.MCPSubmission{}, nil, err
	}
	if err := mcpwire.CheckPayload(tool, args.Payload); err != nil {
		release()
		return factory.MCPCapability{}, factory.MCPSubmission{}, nil, err
	}
	submission := factory.MCPSubmission{
		AssignmentID: capability.AssignmentID, SubmissionID: args.SubmissionID,
		CapabilityID: capability.ID,
		Tool:         tool,
		Payload:      raw,
	}
	err = s.workerResults().CheckNewFinish(ctx, capability, submission, args.Payload)
	if err == nil && tool != factory.MCPToolFinishIncomplete {
		err = s.requireCurrentWork(ctx, capability)
	}
	if err != nil {
		release()
		return factory.MCPCapability{}, factory.MCPSubmission{}, nil, err
	}
	return capability, submission, release, nil
}

func mcpTextResult(text string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

func mcpJSONResult(value any) (*mcp.CallToolResult, any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > factory.MCPMaxResultPayloadBytes {
		return nil, nil, errors.New("factory result exceeds 64 KiB")
	}
	return mcpTextResult(string(raw))
}

func newCatalogServer(deps Dependencies, tools []factory.MCPTool) *mcp.Server {
	limiter := deps.limiter
	if limiter == nil {
		limiter = newMCPRateLimiter()
	}
	catalog := &factoryCatalogServer{deps: deps, limiter: limiter}
	name := "soda-factory"
	if deps.boundCapability != nil {
		name += "-" + string(deps.boundCapability.Profile)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	for _, tool := range tools {
		tool := tool
		if mcpwire.IsSystemOne(tool) {
			catalog.addSystemOneTool(server, tool)
			continue
		}
		schema := mcpwire.Tool(tool, deps.boundCapability)
		mcp.AddTool(server, schema, func(ctx context.Context, _ *mcp.CallToolRequest, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
			return catalog.serve(ctx, tool, args)
		})
	}
	return server
}

// BoundToolNames projects the canonical tool names for a captured worker before
// launch. Native hosts supply these names to the Rust provider policy; workers
// and model schemas never select this catalog. Use the same dependencies as the
// HTTP handler. This projection neither prepares nor activates a capability and
// grants no tool admission: the handler still checks current work and process
// observations on every request. Task-controller availability is read from the
// recorded run, using the same rule as protocol discovery.
func BoundToolNames(ctx context.Context, deps Dependencies, capability factory.MCPCapability) ([]string, error) {
	if err := capability.Validate(); err != nil {
		return nil, err
	}
	bound := bindCatalog(ctx, deps, capability)
	names := make([]string, 0, len(factory.MCPTools))
	for _, tool := range catalogTools(bound, capability) {
		names = append(names, string(tool))
	}
	slices.Sort(names)
	return names, nil
}

// Prelaunch projection and authenticated discovery use the same selection.
func catalogTools(deps Dependencies, capability factory.MCPCapability) []factory.MCPTool {
	tools := make([]factory.MCPTool, 0, len(factory.MCPTools))
	for _, tool := range factory.MCPTools {
		if !factory.MCPToolAdmitted(capability.Profile, tool) ||
			(mcpwire.IsNativeTask(tool) && deps.NativeTask == nil) ||
			(tool == factory.MCPToolReadSource && deps.ReadSource == nil) ||
			(tool == factory.MCPToolListSource && deps.ListSource == nil) {
			continue
		}
		tools = append(tools, tool)
	}
	return tools
}

func (s *factoryCatalogServer) serve(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	args.SubmissionID = mcpSubmissionID(args.SubmissionID, factory.MCPNeedsSubmission(tool))
	if factory.MCPIsLibraryRead(tool) {
		return s.serveLibraryRead(ctx, tool, args)
	}
	if factory.MCPIsCommunication(tool) || tool == factory.MCPToolResolvePost {
		return s.serveWorkerPost(ctx, tool, args)
	}
	switch tool {
	case factory.MCPToolReadSource, factory.MCPToolListSource:
		return s.serveSource(ctx, tool, args)
	case factory.MCPToolCreateIssue, factory.MCPToolRefineIssue:
		return s.serveIssueMaker(ctx, tool, args)
	case factory.MCPToolStartTask, factory.MCPToolReleaseOutput:
		return s.serveNativeTask(ctx, tool, args)
	case factory.MCPToolPublishArtifact:
		return s.serveArtifact(ctx, args)
	case factory.MCPToolVerificationEvidence:
		cap, release, err := s.admitCall(ctx, tool, args)
		if err != nil {
			return nil, nil, err
		}
		defer release()
		if s.deps.VerificationEvidence == nil {
			return nil, nil, errors.New("native verification evidence unavailable")
		}
		evidence, err := s.deps.VerificationEvidence(ctx, cap)
		if err != nil {
			return nil, nil, err
		}
		return mcpJSONResult(evidence)
	case factory.MCPToolContext, factory.MCPToolStatus:
		return s.serveRead(ctx, tool, args)
	case factory.MCPToolVerificationScope:
		return s.serveVerificationScope(ctx, args)
	case factory.MCPToolPreviewRepair:
		return s.serveRepairPreview(ctx, args)
	case factory.MCPToolReassessmentScope:
		return s.serveReassessmentScope(ctx, args)
	case factory.MCPToolEvidence:
		return s.serveEvidence(ctx, args)
	case factory.MCPToolInbox, factory.MCPToolDiscussion:
		return s.serveInbox(ctx, tool, args)
	default:
		return s.serveMutation(ctx, tool, args)
	}
}

// serveRead serves capability-bound reads: the immutable assignment
// summary (context) and the live capability plus submitted work's
// state (status). Reading never launches work and records no row.
func (s *factoryCatalogServer) serveRead(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	read, err := s.loadReadContext(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer read.release()
	foregrounded, selectionReason, selectionFingerprint := s.workerContext().Foreground(ctx, read.WorkerContextRead)
	body := read.Projection(foregrounded)
	body["route"] = factoryhttpapi.WorkerRoute
	if read.Capability.Profile == factory.MCPProfileProductLead {
		catalogView := map[string]any{"available": false, "reason": "approved check catalog is unavailable"}
		if s.deps.Catalog != nil {
			if catalog, err := s.deps.Catalog(ctx, read.Capability); err == nil {
				if check, ok := catalog.LookupCheck(factory.ApprovedAppCheckID); ok {
					catalogView = map[string]any{"available": true, "entries": []factory.ApprovedCheck{check}, "binding": catalog}
				}
			}
		}
		body["verification_catalog"] = catalogView
	}
	action := "serve"
	if selectionReason != "" {
		action = "hold"
	}
	if read.Frozen {
		action = "stop"
	}
	body["context_selection"] = map[string]any{"bundle": foregrounded, "action": action, "reason_code": selectionReason, "fingerprint": selectionFingerprint}
	if err := s.addCheckpointContext(ctx, read, body); err != nil {
		return nil, nil, err
	}
	if err := s.addCurrentContext(ctx, read, body, args); err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(body)
}

// serveMutation posts work for the existing graph, checkpoint, review and
// adoption controllers to process.
func (s *factoryCatalogServer) serveMutation(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	_, submission, release, err := s.admitMutation(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	recorded, err := s.deps.Submissions.RecordMCPSubmission(ctx, submission)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(s.workerResults().Status(ctx, submission.AssignmentID, recorded))
}

var (
	_ factoryservice.WorkerCapabilitySource       = (*store.Store)(nil)
	_ SubmissionSink                              = (*store.Store)(nil)
	_ factoryservice.WorkerAssignmentSource       = (*store.Store)(nil)
	_ factoryservice.WorkerPriorityJudgmentSource = (*store.Store)(nil)
	_ factoryservice.WorkerRunSource              = (*store.Store)(nil)
)

// mcpSubmissionID turns whatever the worker sent into the 32-hex submission
// identity: a valid identity passes, any other string is hashed so the same
// string identifies the same post, and a mutation without one gets a fresh
// identity. Workers never have to mint identifiers.
func mcpSubmissionID(id string, mutation bool) string {
	switch {
	case factory.ValidID(id):
		return id
	case id != "":
		sum := sha256.Sum256([]byte(id))
		return hex.EncodeToString(sum[:16])
	case mutation:
		return factory.NewID()
	}
	return ""
}

// readContext retains the transport rate slot around shared application reads.
type readContext struct {
	factoryservice.WorkerContextRead
	release func()
}

func (s *factoryCatalogServer) workerContext() factoryservice.WorkerContext {
	return factoryservice.WorkerContext{Terminal: s.deps.Submissions, Assignments: s.deps.Assignments, Runs: s.deps.Runs, Priority: s.deps.PriorityJudgments, Current: s.deps.CurrentContext, Now: s.now}
}

func (s *factoryCatalogServer) loadReadContext(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*readContext, error) {
	capability, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	loaded, err := s.workerContext().Load(ctx, capability)
	if err != nil {
		release()
		return nil, err
	}
	return &readContext{WorkerContextRead: loaded, release: release}, nil
}

func (s *factoryCatalogServer) serveEvidence(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	capability, release, err := s.admitCall(ctx, factory.MCPToolEvidence, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	body, err := s.workerContext().Evidence(ctx, capability)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(body)
}
