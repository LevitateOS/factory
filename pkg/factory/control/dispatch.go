package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/store"
)

// DispatchHost is the dispatch slice of the host surface: launch starts
// one recorded run under supervision, inspect observes one run without
// mutating it, stop fences a run identity, and tool inspection reports tagged
// executable/bundle identity. The coordinator passes its runtime adapter through.
type DispatchHost interface {
	execution.RunLauncher
	execution.RunObserver
	execution.PreparationContextReader
	execution.ToolObserver
}

// DispatchBroker is the dispatch slice of the broker surface: get reads
// one execution's nonsecret metadata and close fences late acquisition. Dispatch never acquires,
// registers or handles credential delivery.
type DispatchBroker interface {
	execution.CustodyObserver
}

// DispatchComment is one verified accepted native text section: the
// comment identity plus the content the bracketed read verified against
// its digest.
type DispatchComment struct {
	ID         string
	Content    string
	Digest     string
	ContentVer int
	Visible    bool
}

// DispatchIssue is the verified accepted objective: the exact title and
// body at one idle native revision plus the lifecycle state dispatch
// must respect.
type DispatchIssue struct {
	Title               string
	Body                string
	TitleDigest         string
	ContentDigest       string
	ContentVer          int
	TitleOccurrence     string
	ClosingOccurrence   string
	LifecycleOccurrence string
	ClosedUnix          int64
	Visible             bool
	Closed              bool
	Locked              bool
	IsPull              bool
}

// DispatchInputs is one revision-bound accepted-input read: the verified
// objective, every selected comment revision and the exact target tip
// the run must build on.
type DispatchInputs struct {
	Issue    DispatchIssue
	Comments []DispatchComment
	Tip      string
	TipRef   string
	Revision int64
}

// DispatchReads brackets one accepted-input read between equal idle
// native revision observations: the objective, the named comment
// revisions and the target branch tip. Transport failures arrive as
// acceptance refusals with a stale, incomplete, busy or unavailable
// reason; any other error reports an unusable source.
type DispatchReads interface {
	ReadDispatchInputs(ctx context.Context, repository, issue string, commentIDs []string, targetRef string) (DispatchInputs, error)
}

// Dispatch wait reasons. Bounded codes: reports expose why an issue did
// not launch, never the evidence bytes behind it. Terminal reasons
// (attempt_recorded, launch_exhausted) end the attempt for its head;
// every other reason retries on a later pass.
const (
	WaitAssigned          = "already_assigned"
	WaitAttemptRecorded   = "attempt_recorded"
	WaitAuthority         = "authority_ineffective"
	WaitDispatchClosed    = "dispatch_closed"
	WaitCapacity          = "capacity_full"
	WaitRepository        = "repository_full"
	WaitSponsorship       = "sponsorship_full"
	WaitSponsorshipRole   = "sponsorship_role_missing"
	WaitAllowance         = "allowance_exhausted"
	WaitPreparation       = "preparation_not_ready"
	WaitHarness           = "harness_unavailable"
	WaitInputsUnavailable = "inputs_unavailable"
	WaitInputsBusy        = "inputs_busy"
	WaitInputsStale       = "inputs_stale"
	WaitInputsIncomplete  = "inputs_incomplete"
	WaitInputsHidden      = "inputs_hidden"
	WaitInputsChanged     = "inputs_changed"
	WaitIssueClosed       = "issue_closed"
	WaitIssueLocked       = "issue_locked"
	WaitSource            = "source_unavailable"
	WaitPrompt            = "prompt_too_large"
	WaitLaunchRefused     = "launch_refused"
	WaitLaunchExhausted   = "launch_exhausted"
)

// Dispatch error reasons for unexpected infrastructure failures. The
// affected assignment keeps its held reservation; a later pass retries.
// DispatchErrPromptBound is the one deterministic validation refusal: an
// assembled intent prompt past its admitted phase bound. It never
// retries the same input; the selection must narrow instead.
const (
	DispatchErrStore       = "store_unavailable"
	DispatchErrHost        = "host_unavailable"
	DispatchErrFence       = "launch_fenced"
	DispatchErrPromptBound = "prompt_over_bound"
)

// DispatchWait is one issue that did not launch this pass and why.
type DispatchWait struct {
	Repository int64  `json:"repository,string"`
	Issue      int64  `json:"issue,string"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail,omitempty"`
}

// DispatchLaunch is one run this pass started.
type DispatchLaunch struct {
	Repository   int64  `json:"repository,string"`
	Issue        int64  `json:"issue,string"`
	AssignmentID string `json:"assignment_id"`
	RunID        string `json:"run_id"`
	Phase        string `json:"phase"`
}

// DispatchError is one unexpected infrastructure failure.
type DispatchError struct {
	Repository int64  `json:"repository,string"`
	Issue      int64  `json:"issue,string"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail,omitempty"`
}

// DispatchReport is the durable outcome of one dispatch pass: the runs
// it started, the interrupted assignments it resolved, and every issue
// that waited or errored. Triggers record it; nothing in it admits,
// spends or publishes.
type DispatchReport struct {
	Launched  []DispatchLaunch `json:"launched"`
	Recovered []string         `json:"recovered"`
	Waits     []DispatchWait   `json:"waits"`
	Errors    []DispatchError  `json:"errors"`
}

// DispatchObservation is a transient, bounded diagnosis of the serving pass.
// It contains no report Detail, prompts, run output or persisted authority.
// A new process has no observation until its serving coordinator advances.
type DispatchObservation struct {
	Stage      string                      `json:"stage"`
	StageAt    time.Time                   `json:"stage_at"`
	DispatchAt time.Time                   `json:"dispatch_at,omitempty"`
	Context    string                      `json:"context"`
	Waits      []DispatchObservationReason `json:"waits"`
	Errors     []DispatchObservationReason `json:"errors"`
	Truncated  bool                        `json:"truncated"`
}

type DispatchObservationReason struct {
	Repository int64  `json:"repository,string,omitempty"`
	Issue      int64  `json:"issue,string,omitempty"`
	Reason     string `json:"reason"`
	Stage      string `json:"stage"`
	// CoderTime marks a wait the coder-time allowance causes, which the
	// owner fixes by raising it, apart from attempt limits that need Retry.
	CoderTime bool `json:"coder_time,omitempty"`
}

// LatestDispatchObservation projects only the requested repository and global
// infrastructure reasons. Reads neither drive the pass nor refresh its age.
func (c *Coordinator) LatestDispatchObservation(repository int64) *DispatchObservation {
	current := c.dispatchObservation.Load()
	if current == nil || repository <= 0 {
		return nil
	}
	out := *current
	out.Waits, out.Errors = []DispatchObservationReason{}, []DispatchObservationReason{}
	for _, entry := range current.Waits {
		if entry.Repository == repository || entry.Repository == 0 {
			out.Waits = append(out.Waits, entry)
		}
	}
	for _, entry := range current.Errors {
		if entry.Repository == repository || entry.Repository == 0 {
			out.Errors = append(out.Errors, entry)
		}
	}
	return &out
}

func (c *Coordinator) observeProgressStage(stage string) {
	switch stage {
	case "settlement", "comments", "standing_preparation", "intent_graphs", "creations", "completions", "delivery_verification", "delivery_repair", "retry_preparation", "dispatch", "publication", "stall", "readiness", "idle":
	default:
		stage = "unavailable"
	}
	var next DispatchObservation
	if previous := c.dispatchObservation.Load(); previous != nil {
		next = *previous
	}
	next.Stage, next.StageAt = stage, time.Now()
	c.dispatchObservation.Store(&next)
}

func (c *Coordinator) observeDispatch(ctx context.Context, report DispatchReport) {
	var next DispatchObservation
	if previous := c.dispatchObservation.Load(); previous != nil {
		next = *previous
	}
	next.DispatchAt, next.Context = time.Now(), "active"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		next.Context = "deadline"
	} else if ctx.Err() != nil {
		next.Context = "cancelled"
	}
	next.Waits, next.Errors, next.Truncated = []DispatchObservationReason{}, []DispatchObservationReason{}, false
	const limit = 64
	for _, wait := range report.Waits {
		if len(next.Waits)+len(next.Errors) == limit {
			next.Truncated = true
			break
		}
		next.Waits = append(next.Waits, dispatchObservationReason(wait.Repository, wait.Issue, wait.Reason, wait.Detail))
	}
	for _, failure := range report.Errors {
		if len(next.Waits)+len(next.Errors) == limit {
			next.Truncated = true
			break
		}
		next.Errors = append(next.Errors, dispatchObservationReason(failure.Repository, failure.Issue, failure.Reason, failure.Detail))
	}
	c.dispatchObservation.Store(&next)
}

// coderTimeWait reports whether a wait is a coder-time sponsorship
// exhaustion: the owner fixes it by raising the allowance, apart from
// attempt limits that need Retry.
func coderTimeWait(reason, detail string) bool {
	return reason == WaitAllowance && detail == "sponsorship allowance is exhausted"
}

func dispatchObservationReason(repository, issue int64, reason, detail string) DispatchObservationReason {
	// Reason is whitelisted too: an unexpected producer string must not turn
	// this diagnostic into an indirect error/provider text channel.
	stage := "admission"
	switch reason {
	case WaitAssigned, WaitAttemptRecorded, WaitAuthority, WaitDispatchClosed, WaitSponsorship, WaitSponsorshipRole, WaitAllowance, WaitIssueClosed, WaitIssueLocked, WaitReassessmentPending, WaitLaunchRefused, WaitLaunchExhausted, DispatchErrFence:
	case WaitCapacity, WaitRepository:
		stage = "capacity"
	case WaitPreparation:
		stage = "preparation"
	case WaitHarness:
		stage = "harness"
	case WaitInputsUnavailable, WaitInputsBusy, WaitInputsStale, WaitInputsIncomplete, WaitInputsHidden, WaitInputsChanged:
		stage = "read"
	case WaitSource:
		stage = "source"
	case WaitPrompt, DispatchErrPromptBound:
		stage = "prompt"
	case "priority_incomplete", "priority_unavailable", PrioritySnapshotChanging:
		stage = "priority"
	case DispatchErrStore:
		stage = "store"
	case DispatchErrHost:
		stage = "host"
	default:
		reason, stage = "unavailable", "unavailable"
	}
	// Only these fixed owning-source descriptions refine an ambiguous stage.
	// The original Detail is never retained or serialized.
	switch detail {
	case "native revision unavailable", "native revision advanced during inventory capture", "native revision advanced after judgment":
		stage = "native_revision"
	case "priority inventory unavailable", "complete candidate inventory unavailable", "priority inventory unrecorded", "priority inventory changed; restarting durable scan", "priority inputs changed after judgment", "queued listing unavailable":
		stage = "inventory"
	case "priority scan unreadable", "priority progress unrecorded", "priority member unreadable", "candidate observation could not advance":
		stage = "priority_read"
	case "readiness refresh is unavailable":
		stage = "refresh"
	}
	if repository <= 0 {
		repository, issue = 0, 0
	} else if issue < 0 {
		issue = 0
	}
	out := DispatchObservationReason{Repository: repository, Issue: issue, Reason: reason, Stage: stage}
	out.CoderTime = coderTimeWait(reason, detail)
	return out
}

func newDispatchReport() DispatchReport {
	return DispatchReport{Launched: []DispatchLaunch{}, Recovered: []string{}, Waits: []DispatchWait{}, Errors: []DispatchError{}}
}

const (
	// MaxDispatchVisits bounds queued issues visited per pass. Visits run
	// oldest first in deterministic order; the rest wait for a later pass.
	MaxDispatchVisits = 64
	// MaxDispatchRecovery bounds interrupted assignments resolved per
	// pass. Each resolution is one bounded state transition.
	MaxDispatchRecovery = 64
	// MaxDispatchDeadline bounds one dispatched run in minutes. The host
	// refuses deadlines past three hours; the minute under keeps clock
	// skew from tipping a full allowance over the bound.
	MaxDispatchDeadline = 179
)

// DispatchDeps gathers one dispatch pass's dependencies. Authority
// derives the visible enablement verdict; the coordinator passes its own
// EffectiveRoleAuthority so each dispatch uses its role's paying account. Queue
// carries fair-rotation progress across passes; a nil queue visits from
// the beginning without advancing.
type DispatchDeps struct {
	MCPKey               []byte
	MCPSecret            factory.MCPSecretDeriver
	ContextVerification  currentVerificationSource
	VerificationManifest func(context.Context, factory.Assignment, factory.Run) ([]byte, error)
	Store                *store.Store
	Host                 DispatchHost
	Broker               DispatchBroker
	Reads                DispatchReads
	Authority            func(ctx context.Context, repository int64, role string) (factory.EffectiveAuthority, error)
	Refresh              func(context.Context, factory.IssueControl) (factory.IssueControl, error)
	PrepareRetry         func(context.Context, *attemptPlan, string) (bool, error)
	PrepareSource        func(context.Context, *attemptPlan) (bool, error)
	RetireRetry          func(context.Context, *attemptPlan) error
	Queue                *DispatchQueueCursor
	Priority             *PriorityJudgments
	ContextChooser       ProductJudgment
	// ObserveRevision reports one idle native revision for the durable
	// priority inventory. RefreshCandidate returns the observation-ready
	// control for one inventory member at the scan revision.
	ObserveRevision  func(ctx context.Context) (int64, bool, error)
	RefreshCandidate func(ctx context.Context, control factory.IssueControl, scanRevision int64) (factory.IssueControl, bool, error)
	// FenceCache memoizes one pass's per-repository completion fence so
	// each repository reads it once. A nil cache reads through every
	// time; the atomic packet transaction stays authoritative either
	// way. Maps share backing across the copied deps value, so entries
	// recorded by one issue are visible to later issues in the pass.
	FenceCache map[int64]bool
	// ReadCompletionFence lists pending completion generations for one
	// repository. Nil reads through the store; tests inject
	// deterministic fixtures.
	ReadCompletionFence func(ctx context.Context, repository int64) ([]int64, error)
	collectPlan         func(*attemptPlan)
	checkPlan           func(*attemptPlan) bool
}

func (d DispatchDeps) mcpConfigured() bool {
	return d.MCPSecret != nil || len(d.MCPKey) >= 32
}

func (d DispatchDeps) deriveMCPSecret(ctx context.Context, capability factory.MCPCapability) (string, error) {
	var secret string
	var err error
	if d.MCPSecret != nil {
		secret, err = d.MCPSecret(ctx, capability)
	} else {
		secret, err = factory.DeriveMCPCapabilitySecret(d.MCPKey, capability)
	}
	if err != nil || !factory.ValidMCPSecret(secret) {
		return "", errors.New("worker MCP derivation unavailable")
	}
	return secret, nil
}

func (c *Coordinator) mcpConfigured() bool {
	return c.MCPSecret != nil || len(c.MCPKey) >= 32
}

// Older public Coordinator/DispatchDeps callers provide one chooser through
// Priority. Resolve that compatibility at dispatch admission; launch consumes
// the explicit context port and never infers it from another workflow.
func admitDispatchDependencies(deps DispatchDeps) DispatchDeps {
	if deps.ContextChooser == nil && deps.Priority != nil {
		deps.ContextChooser, _ = deps.Priority.Chooser.(ProductJudgment)
	}
	return deps
}

// dispatchReady reports whether one pass has its required dependencies.
// Reads stay optional: an unwired source waits every issue instead of
// failing the pass.
func dispatchReady(deps DispatchDeps) bool {
	return deps.Store != nil && deps.Host != nil && deps.Broker != nil && deps.Authority != nil
}

// RecoverDispatch resolves interrupted assignments without dispatching
// new work: held capacity behind settled runs is consumed, launches
// that never reached the host release and retry bounded, and fenced
// runs stay held for reconcile. Coordinator startup calls this after
// settling runs; failures report instead of failing the caller.
func RecoverDispatch(ctx context.Context, deps DispatchDeps) DispatchReport {
	deps = admitDispatchDependencies(deps)
	report := newDispatchReport()
	if !dispatchReady(deps) {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "dispatch dependencies unavailable"})
		return report
	}
	recoverFinishedMCPAccounting(ctx, deps, &report)
	recoverAssigned(ctx, deps, &report)
	return report
}

// DispatchPass resolves interrupted assignments, then dispatches the
// oldest queued or authority-blocked accepted issues within current limits.
// Every operational failure lands in the report, and a later pass retries.
// An explicit Retry may retain bounded preparation custody while waiting;
// reservations release only capacity confirmed unused.
func DispatchPass(ctx context.Context, deps DispatchDeps) DispatchReport {
	deps = admitDispatchDependencies(deps)
	report := RecoverDispatch(ctx, deps)
	if !dispatchReady(deps) {
		return report
	}
	if deps.Priority != nil {
		return dispatchPriorityPass(ctx, deps, report)
	}
	firstSeen, repo, issue, hasCursor := deps.Queue.start()
	queued, err := deps.Store.DispatchControlsAfter(ctx, MaxDispatchVisits, firstSeen, repo, issue, hasCursor)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "queued listing unavailable"})
		return report
	}
	if hasCursor && len(queued) == 0 {
		// Rotation reached the end of the listing: wrap to the
		// beginning so this same pass still visits waiting work.
		deps.Queue.reset()
		queued, err = deps.Store.DispatchControlsAfter(ctx, MaxDispatchVisits, 0, 0, 0, false)
		if err != nil {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "queued listing unavailable"})
			return report
		}
	}
	occupancy, err := snapshotOccupancy(ctx, deps.Store)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "capacity accounting unavailable"})
		return report
	}
	for _, control := range queued {
		if err := ctx.Err(); err != nil {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "dispatch pass cancelled"})
			return report
		}
		dispatchOne(ctx, deps, &occupancy, control, &report)
	}
	// A short batch observed the end of the listing, so rotation
	// wraps; a full batch continues after the last visited control.
	if len(queued) > 0 {
		deps.Queue.advance(queued[len(queued)-1], len(queued) < MaxDispatchVisits)
	}
	return report
}

func (c *Coordinator) dispatchDeps() DispatchDeps {
	return admitDispatchDependencies(DispatchDeps{
		Store: c.Store, Host: c.Host, Broker: c.Broker, MCPKey: c.MCPKey, MCPSecret: c.MCPSecret, ContextVerification: c.ApplicationVerificationEvidence, VerificationManifest: c.stageDeliveryVerificationManifest,
		Reads: c.DispatchReads, Authority: c.EffectiveRoleAuthority,
		Refresh: c.refreshReadiness, PrepareRetry: c.prepareRetryCoder, PrepareSource: c.prepareSourceCoder, RetireRetry: c.retireRetryCoder,
		Queue:           &c.queue,
		Priority:        c.Priority,
		ContextChooser:  c.ContextChooser,
		ObserveRevision: c.observePriorityRevision, RefreshCandidate: c.refreshPriorityCandidate,
		FenceCache: map[int64]bool{},
	})
}

// noteAllowanceWaits leaves one durable Feed card per issue waiting on
// coder-time sponsorship. The transient dispatch observation is
// owner/admin-only until restart; the card is visible to every authorized
// reader. Best-effort by design: Dispatch never fails, and the observation
// still carries the wait when the store write cannot land.
func (c *Coordinator) noteAllowanceWaits(ctx context.Context, report DispatchReport) {
	if c.Store == nil {
		return
	}
	for _, wait := range report.Waits {
		if !coderTimeWait(wait.Reason, wait.Detail) || wait.Repository <= 0 || wait.Issue <= 0 {
			continue
		}
		_ = c.Store.NoteAllowanceExhausted(ctx, wait.Repository, wait.Issue, 0)
	}
}

// activeDispatchKey marks the Coordinator whose Dispatch owns a context.
type activeDispatchKey struct{}

// Dispatch resolves interrupted assignments, then dispatches the oldest
// queued issues within current limits. It never fails: every operational
// failure lands in the report, and a later pass retries.
func (c *Coordinator) Dispatch(ctx context.Context) DispatchReport {
	ctx, unlock, err := c.progressPass(ctx)
	if err != nil {
		report := newDispatchReport()
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "dispatch pass cancelled"})
		return report
	}
	defer unlock()
	defer c.observeProgressStage("idle")
	ctx = context.WithValue(ctx, activeDispatchKey{}, c)
	ctx, ownsPass, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	c.observeProgressStage("retry_preparation")
	preparationErr := c.reconcileRetryPreparations(ctx)
	c.observeProgressStage("dispatch")
	report := DispatchPass(ctx, c.dispatchDeps())
	if preparationErr != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrHost, Detail: "retry preparation retirement is unconfirmed"})
	}
	c.observeDispatch(ctx, report)
	c.noteAllowanceWaits(ctx, report)
	c.observeProgressStage("publication")
	c.publishAfterDispatch(ctx, report)
	if ownsPass {
		drain, err := c.drainReadinessWork(ctx, "")
		if (err != nil && !errors.Is(err, ErrReadinessPassPending)) || drain.failed {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "readiness work deferred"})
		}
	}
	return report
}

// dispatchAfterIntake runs one best-effort dispatch pass after a native
// intake assessment. Dispatch runs only while an accepted-input source
// is wired; an unwired coordinator keeps its assessment-only behavior.
func (c *Coordinator) dispatchAfterIntake(ctx context.Context) {
	if c.DispatchReads == nil || ctx.Value(activeDispatchKey{}) == c {
		return
	}
	_ = c.Dispatch(ctx)
}
