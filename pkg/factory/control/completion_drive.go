package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// HoldCompletionWiring reports a collecting cycle that cannot advance
// because the native-page or local-capture binding is unwired. The
// FW32 handoff binds both; until then the fence holds honestly instead
// of sealing from incomplete evidence.
const HoldCompletionWiring = "completion_reads_unwired"

// CompletionDriveDeps carries the two FW32-handoff bindings behind one
// completion drive: native inventory pages and the local capture. A
// nil binding holds collecting cycles with HoldCompletionWiring; it
// never seals, decides or releases.
type CompletionDriveDeps struct {
	Enumerator   CompletionIssueEnumerator
	ReadIssues   func(context.Context, int64) ([]NativeInventoryIssue, error)
	CaptureLocal func(ctx context.Context, repository int64) (factory.CompletionLocal, error)
}

func (d CompletionDriveDeps) available() bool {
	return d.CaptureLocal != nil && (d.Enumerator != nil || d.ReadIssues != nil)
}

func (c *Coordinator) completionDriveDeps() CompletionDriveDeps {
	return CompletionDriveDeps{Enumerator: c.CompletionEnumerator, ReadIssues: c.CompletionIssues, CaptureLocal: c.CompletionLocalCapture}
}

// CompletionDriveReport is the durable outcome of one completion
// drive: cycle IDs sealed, decided, released or held for FW32, plus
// resumable hold reasons. It spends nothing by itself; funded calls
// flow through the decision path with their own receipts.
type CompletionDriveReport struct {
	Sealed        []string
	Decided       []string
	Released      []string
	AwaitingCoder []string
	Held          []string
}

// SealEvidenceFingerprint binds the funded-decision cache to the exact
// sealed evidence: the sealed inventory digest plus the distinct
// completion and cycle IDs. Two completions never share a decision
// cache entry, even with identical inventories.
func SealEvidenceFingerprint(inventoryDigest, completionID, cycleID string) string {
	sum := sha256.Sum256([]byte(inventoryDigest + "\x00" + completionID + "\x00" + cycleID))
	return hex.EncodeToString(sum[:])
}

// DriveCompletions advances every unresolved completion cycle once, in
// repository and generation order. Collecting cycles seal their full
// inventory; deciding cycles take exactly one funded decision each;
// valid continue decisions release after freshness, recheck and
// snapshot invalidation; valid reassess decisions dispatch their one
// funded coder. Attention and superseded cycles are never
// touched: no poll, input edit or policy edit resumes them, and no
// automatic re-run ever starts. Reassessing consumes the typed result,
// assessing funds the proposal judgment, and applying advances the
// exact serial adoption journal. Every failure holds with
// its resumable step; a later drive retries from persisted progress.
func (c *Coordinator) DriveCompletions(ctx context.Context, deps CompletionDriveDeps) CompletionDriveReport {
	var report CompletionDriveReport
	if c.Store == nil {
		return report
	}
	ctx, unlock, err := c.progressPass(ctx)
	if err != nil {
		report.Held = append(report.Held, "progress_pass_cancelled")
		return report
	}
	defer unlock()
	// The drive owns no new budget: it reuses the caller's readiness
	// pass when one exists (the production advanceProgressPage pass) and
	// creates it when called standalone, so the bracket's fixed-8
	// prepayment (two revision observations plus both serving captures)
	// always charges through the existing reserveNativeRPC accounting.
	// Reuse returns a no-op cancel; ownership stays here.
	ctx, _, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	repositories, err := c.Store.PendingCompletionRepositories(ctx)
	if err != nil {
		return report
	}
	for _, repository := range repositories {
		cycles, err := c.Store.PendingReassessmentCycles(ctx, repository)
		if err != nil {
			continue
		}
		for _, record := range cycles {
			switch routeCompletionCycle(record.Cycle.State) {
			case driveCollect:
				c.driveCollecting(ctx, deps, record, &report)
			case driveDecide:
				c.driveDeciding(ctx, deps, record, &report)
			case driveCoder:
				switch record.Cycle.State {
				case factory.ReassessmentReassessing:
					c.driveReassessmentCoder(ctx, record, &report)
				case factory.ReassessmentAssessing:
					c.driveReassessmentProposal(ctx, deps, record, &report)
				case factory.ReassessmentApplying:
					c.driveReassessmentAdoption(ctx, deps, record, &report)
				default:
					report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_adoption_pending")
				}
			default:
				report.Held = append(report.Held, record.Cycle.CycleID+":awaiting-retry")
			}
		}
	}
	return report
}

// driveRoute names one cycle's handling in a drive. Collecting seals,
// deciding decides, reassessing and later advance the typed proposal
// and its serial adoption, and attention rests for explicit Retry:
// the drive never resumes them and never starts an automatic re-run.
type driveRoute int

const (
	driveHold driveRoute = iota
	driveCollect
	driveDecide
	driveCoder
)

func routeCompletionCycle(state string) driveRoute {
	switch state {
	case factory.ReassessmentCollecting:
		return driveCollect
	case factory.ReassessmentDeciding:
		return driveDecide
	case factory.ReassessmentReassessing, factory.ReassessmentAssessing, factory.ReassessmentApplying:
		return driveCoder
	default:
		return driveHold
	}
}

// HoldInventoryMaterial names a collecting cycle whose recaptured
// local moved under its sealed-but-unconfirmed inventory: the seal is
// deferred, the cursor resets, and the next drive recollects from
// scratch. It carries no evidence bytes.
const HoldInventoryMaterial = "inventory_material_changed"

// driveCollecting seals one collecting cycle or persists its resumable
// progress with the hold reason.
//
// The collecting path brackets the SAME-RR local capture with the
// native inventory coherently: it observes the idle native revision
// before capture, collects under it, recaptures the local after
// collection, and confirms the idle native revision after that
// recapture. It seals only if both local values are Equal and the
// native revision stayed fixed. Both bracket observations are
// reserved upfront as two units through the existing readiness pass;
// with fewer than two units free the drive holds pending immediately
// with the saved cursor kept and zero external calls. On movement the cursor resets
// (never combining snapshots across revisions) with a named bounded
// hold; no global transaction or native authority is invented — each
// capture owns its own read.
// bracketResult is one SAME-RR bracket outcome: either a confirmed
// seal, or the named hold with the exact progress to persist. A nil
// progress drops the saved cursor so the next drive recollects from
// scratch instead of combining snapshots across revisions.
type bracketResult struct {
	sealed   *SealedInventory
	progress *InventoryCollector
	hold     string
}

// chargeBracketCapturesAndObserves reserves EIGHT native-RPC units
// upfront through the existing readiness pass: TWO outer revision
// observations plus BOTH serving captures at three native reads each
// (one refs-only bracket per capture on the raw reader, never
// per-call charged). A nil pass (store-free unit fixtures) carries no
// charge; a pass with fewer than eight units free reports pending so
// the caller holds immediately with the saved cursor unchanged and
// zero external calls. No new counter or ledger is introduced: this
// is the existing reserveNativeRPC accounting only, and the collector
// keeps its own upfront two beside it.
func chargeBracketCapturesAndObserves(ctx context.Context) bool {
	budget := readinessBudgetFrom(ctx)
	if budget == nil {
		return true
	}
	return budget.reserveNativeRPC(8)
}

// bracketMaterialCollect runs the SAME-RR bracket without touching the
// store: observe the idle native revision before capture, collect
// under it, recapture the local after collection, and confirm the idle
// native revision after that recapture. It seals only if both local
// values are Equal and the revision stayed fixed; any movement defers
// with a named bounded hold and a reset cursor. Both bracket
// observations plus both serving captures are reserved upfront (eight
// units) through the existing readiness pass; a short budget holds
// pending before any call with the saved cursor kept, spending nothing
// and sealing nothing. Reads inside the collection keep their
// existing page-count bound; their native-call quota stays unqualified
// and out of scope here. An incomplete collection is never charged on
// the funded-decision path.
func bracketMaterialCollect(ctx context.Context, deps CompletionDriveDeps, repository, generation int64, saved *InventoryCollector) bracketResult {
	if !deps.available() {
		return bracketResult{hold: HoldCompletionWiring, progress: saved}
	}
	if deps.ReadIssues != nil {
		return bracketIssueObservations(ctx, deps, repository, generation)
	}
	// Both bracket observations plus both serving captures are
	// reserved upfront: with fewer than eight units free this holds
	// pending immediately with the saved cursor unchanged and zero
	// observes, captures, or page reads, so a late budget failure
	// can never discard newly collected work.
	if !chargeBracketCapturesAndObserves(ctx) {
		return bracketResult{hold: HoldInventoryInProgress, progress: saved}
	}
	collector := saved
	if collector == nil {
		collector = &InventoryCollector{}
	}
	revision, idle, err := deps.Enumerator.ObserveRevision(ctx)
	if err != nil || !idle {
		return bracketResult{hold: HoldInventoryRevision, progress: saved}
	}
	if collector.Revision != 0 && collector.Revision != revision {
		// The native revision moved across drives: reset the cursor
		// instead of resuming, so snapshots never combine.
		collector = &InventoryCollector{Revision: revision}
	}
	local, err := deps.CaptureLocal(ctx, repository)
	if err != nil {
		return bracketResult{hold: HoldInventoryUnavailable, progress: saved}
	}
	sealed, progress, err := collector.Collect(ctx, deps.Enumerator, repository, generation, local)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return bracketResult{hold: HoldInventoryInProgress, progress: progress}
		}
		return bracketResult{hold: err.Error(), progress: progress}
	}
	recaptured, err := deps.CaptureLocal(ctx, repository)
	if err != nil {
		return bracketResult{hold: HoldInventoryUnavailable}
	}
	// The confirming observation was reserved upfront with the opening
	// one, so no second charge happens here: confirmation cannot fail
	// on quota after collection.
	confirmed, idle, err := deps.Enumerator.ObserveRevision(ctx)
	if err != nil || !idle || confirmed != revision {
		return bracketResult{hold: HoldInventoryRevision}
	}
	// Canonical value comparison: Equal fails closed on unvalidated
	// material, so a moved or malformed recapture always defers.
	if !recaptured.Equal(local) {
		return bracketResult{hold: HoldInventoryMaterial}
	}
	return bracketResult{sealed: sealed}
}

func (c *Coordinator) driveCollecting(ctx context.Context, deps CompletionDriveDeps, record store.ReassessmentRecord, report *CompletionDriveReport) {
	cycleID := record.Cycle.CycleID
	hold := func(reason string, progress *InventoryCollector) {
		record.WaitReason = reason
		record.CollectorProgress = nil
		if progress != nil {
			if raw, err := json.Marshal(progress); err == nil {
				record.CollectorProgress = raw
			}
		}
		if err := c.Store.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, record); err != nil {
			if !errors.Is(err, store.ErrStaleRevision) && !errors.Is(err, store.ErrNotFound) {
				report.Held = append(report.Held, cycleID+":collect_hold_unrecorded")
				return
			}
		}
		report.Held = append(report.Held, cycleID+":"+reason)
	}
	var saved *InventoryCollector
	if len(record.CollectorProgress) > 0 {
		var loaded InventoryCollector
		if err := json.Unmarshal(record.CollectorProgress, &loaded); err == nil {
			saved = &loaded
		}
	}
	out := bracketMaterialCollect(ctx, deps, record.Cycle.Repository, record.Cycle.Generation, saved)
	if out.hold != "" {
		hold(out.hold, out.progress)
		return
	}
	sealed := out.sealed
	record.InventoryDigest = sealed.Digest
	record.EvidenceFingerprint = SealEvidenceFingerprint(sealed.Digest, record.Cycle.EventID, cycleID)
	record.Local = sealed.Local
	record.Evidence = sealed.Bodies
	record.EvidenceIssues = sealed.Issues
	record.EvidenceRevision = sealed.Revision
	record.CollectorProgress = nil
	record.WaitReason = ""
	if err := record.Cycle.AdvanceTo(factory.ReassessmentDeciding); err != nil {
		hold(HoldInventoryUnavailable, nil)
		return
	}
	next := record.Cycle
	next.State = factory.ReassessmentDeciding
	if err := next.Validate(); err != nil {
		hold(HoldInventoryUnavailable, nil)
		return
	}
	record.Cycle = next
	if err := c.Store.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, record); err != nil {
		if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrNotFound) {
			return
		}
		hold(HoldInventoryInProgress, nil)
		return
	}
	report.Sealed = append(report.Sealed, cycleID)
}

// driveDeciding takes one funded decision for an undecided cycle,
// dispatches the recorded reassess decision through the sole-run packet,
// or releases a fresh continue decision.
func (c *Coordinator) driveDeciding(ctx context.Context, deps CompletionDriveDeps, record store.ReassessmentRecord, report *CompletionDriveReport) {
	d := &decidingDrive{c: c, ctx: ctx, deps: deps, record: record, report: report, cycleID: record.Cycle.CycleID}
	d.drive()
}

// decidingDrive carries the shared state behind one deciding drive
// so each duty reads through one small check.
type decidingDrive struct {
	c       *Coordinator
	ctx     context.Context
	deps    CompletionDriveDeps
	record  store.ReassessmentRecord
	report  *CompletionDriveReport
	cycleID string
}

// attend holds one deciding cycle for explicit attention under its reason.
func (d *decidingDrive) attend(reason string) {
	if err := d.record.Cycle.AdvanceTo(factory.ReassessmentAttention); err != nil {
		d.report.Held = append(d.report.Held, d.cycleID+":attention_refused")
		return
	}
	d.record.WaitReason = reason
	next := d.record.Cycle
	next.State = factory.ReassessmentAttention
	d.record.Cycle = next
	if err := d.c.Store.CasReassessmentRecord(d.ctx, d.cycleID, factory.ReassessmentDeciding, d.record); err != nil {
		if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrNotFound) {
			return
		}
	}
	d.report.Held = append(d.report.Held, d.cycleID+":"+reason)
}

func (d *decidingDrive) drive() {
	if d.record.Cycle.Decision == factory.ReassessmentOptionReassess {
		d.dispatchCoder()
		return
	}
	if d.record.Cycle.Decision == "" {
		if d.decideFreshOption() {
			return
		}
	}
	if d.refreshDecidedRecord() {
		return
	}
	// A continue decision releases only after freshness, recheck and
	// snapshot invalidation. Any failure before the release CAS defers
	// (the recorded decision stands; the next drive retries without
	// charging again). Material drift holds with attention.
	if d.record.Cycle.Decision != factory.ReassessmentOptionContinue {
		d.attend("decision_not_actionable")
		return
	}
	if !d.deps.available() {
		d.attend("freshness_unavailable")
		return
	}
	d.releaseFreshInventory()
}

// decideFreshOption decides one undecided cycle, reporting true when
// the outcome already landed in the report.
func (d *decidingDrive) decideFreshOption() bool {
	if d.c.Planning == nil || d.c.Planning.Chooser == nil {
		d.record.WaitReason = HoldCompletionWiring
		if err := d.c.Store.CasReassessmentRecord(d.ctx, d.cycleID, factory.ReassessmentDeciding, d.record); err == nil {
			d.report.Held = append(d.report.Held, d.cycleID+":"+HoldCompletionWiring)
		}
		return true
	}
	state, err := DecisionStateFromRecord(d.record)
	if err != nil {
		if errors.Is(err, errReassessmentDecisionOversize) {
			d.attend("decision_evidence_oversize")
			return true
		}
		d.attend("decision_evidence_missing")
		return true
	}
	decided, err := d.c.DecideReassessment(d.ctx, d.record, state)
	if err != nil {
		return d.routeDecisionError(decided, err)
	}
	if decided.Cycle.Decision == factory.ReassessmentOptionReassess {
		if err := d.c.Store.CasReassessmentRecord(d.ctx, d.cycleID, factory.ReassessmentDeciding, decided); err != nil {
			if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrNotFound) {
				return true
			}
			d.report.Held = append(d.report.Held, d.cycleID+":decision_hold_unrecorded")
			return true
		}
		d.report.Decided = append(d.report.Decided, d.cycleID)
		d.record = decided
		d.dispatchCoder()
		return true
	}
	d.record = decided
	return false
}

// routeDecisionError holds one failed decision by its failure,
// reporting true always since the outcome lands in the report.
func (d *decidingDrive) routeDecisionError(decided store.ReassessmentRecord, err error) bool {
	if errors.Is(err, errReassessmentDeferred) {
		d.report.Held = append(d.report.Held, d.cycleID+":decision_in_flight")
		return true
	}
	if decided.WaitReason != "" {
		d.record = decided
		d.attend(decided.WaitReason)
		return true
	}
	d.report.Held = append(d.report.Held, d.cycleID+":decision_deferred")
	return true
}

// refreshDecidedRecord refreshes the deciding record so the release
// and defer CASs preserve the actual decided record+ledger and sealed
// bindings, never a stale pre-decision copy. Only the decided option
// rides forward; the fresh row (with the paid ledger call) wins
// outright. A failed reread or a row that left deciding holds the
// release: never persist a stale ledger over a failed refresh, and
// never override a conflicting already-recorded option.
func (d *decidingDrive) refreshDecidedRecord() bool {
	decidedOption := d.record.Cycle.Decision
	live, err := d.c.Store.ReassessmentRecord(d.ctx, d.cycleID)
	if err != nil {
		d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
		return true
	}
	if live.Cycle.CycleID != d.cycleID || live.Cycle.State != factory.ReassessmentDeciding {
		d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
		return true
	}
	if live.Cycle.Decision != "" && live.Cycle.Decision != decidedOption {
		// Never CAS the stale in-memory record over a live conflicting
		// option+ledger: hold without mutation; the next drive handles
		// the recorded decision.
		d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
		return true
	}
	d.record = live
	if d.record.Cycle.Decision == "" {
		d.record.Cycle.Decision = decidedOption
	}
	return false
}

// releaseFreshInventory re-observes the material inventory and
// releases one continued cycle when fresh.
func (d *decidingDrive) releaseFreshInventory() {
	// The release re-observes the material inventory through the
	// SAME-RR bracket at a stable native revision, resuming the
	// persisted fresh prefix across pass limits: native edits and
	// insertions after the seal change the fresh digest and refuse
	// the stale release, while an unchanged world releases. The
	// sealed digest never appears on the current side, and the
	// sealed Local/Evidence/InventoryDigest/fingerprint are never
	// replaced here — only the deciding prefix persists, without a
	// new spend or decision when continue is already recorded.
	var saved *InventoryCollector
	if len(d.record.CollectorProgress) > 0 {
		var loaded InventoryCollector
		if err := json.Unmarshal(d.record.CollectorProgress, &loaded); err == nil {
			saved = &loaded
		}
	}
	out := bracketMaterialCollect(d.ctx, d.deps, d.record.Cycle.Repository, d.record.Cycle.Generation, saved)
	if out.hold != "" {
		d.routeCollectHold(out)
		return
	}
	fresh := out.sealed
	sealed := freshnessFromBindings(d.record.Local, d.record.InventoryDigest)
	if err := CheckCompletionFreshness(sealed, freshnessFromBindings(fresh.Local, fresh.Digest)); err != nil {
		d.attend(err.Error())
		return
	}
	if err := d.c.Store.DeletePriorityScan(d.ctx); err != nil {
		d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
		return
	}
	if err := d.c.Store.EnqueueReadinessRootEvent(d.ctx, d.record.Cycle.Repository, d.record.Issue); err != nil {
		d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
		return
	}
	if err := d.record.Cycle.AdvanceTo(factory.ReassessmentReleased); err != nil {
		d.report.Held = append(d.report.Held, d.cycleID+":release_refused")
		return
	}
	next := d.record.Cycle
	next.State = factory.ReassessmentReleased
	d.record.Cycle = next
	d.record.WaitReason = ""
	d.record.CollectorProgress = nil
	if err := d.c.Store.CasReassessmentRecord(d.ctx, d.cycleID, factory.ReassessmentDeciding, d.record); err != nil {
		if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrNotFound) {
			return
		}
		d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
		return
	}
	d.report.Released = append(d.report.Released, d.cycleID)
}

// routeCollectHold defers or attends one held material collection.
// A busy or shifting native state, a moved local capture, or an
// exhausted quota defers the release with the fresh prefix (or its
// honest reset on a real revision move) persisted through the deciding
// CAS: the recorded continue decision — with its ledger — stands and
// the next drive resumes without charging again. Material or structural
// drift holds with attention for explicit Retry. A quota refusal defers
// with durable progress, never attention.
func (d *decidingDrive) routeCollectHold(out bracketResult) {
	switch out.hold {
	case HoldInventoryRevision, HoldInventoryInProgress, HoldInventoryMaterial,
		ErrReadinessPassPending.Error(), context.Canceled.Error(), context.DeadlineExceeded.Error():
		d.deferRelease(out)
	case HoldCompletionWiring, HoldInventoryUnavailable:
		d.attend("freshness_unavailable")
	default:
		d.attend(out.hold)
	}
}

// deferRelease persists the fresh prefix through the deciding CAS
// and defers one collected-but-unreleased cycle.
func (d *decidingDrive) deferRelease(out bracketResult) {
	d.record.CollectorProgress = nil
	if out.progress != nil {
		if raw, err := json.Marshal(out.progress); err == nil {
			d.record.CollectorProgress = raw
		}
	}
	if err := d.c.Store.CasReassessmentRecord(d.ctx, d.cycleID, factory.ReassessmentDeciding, d.record); err != nil {
		if !errors.Is(err, store.ErrStaleRevision) && !errors.Is(err, store.ErrNotFound) {
			d.report.Held = append(d.report.Held, d.cycleID+":release_hold_unrecorded")
			return
		}
	}
	d.report.Held = append(d.report.Held, d.cycleID+":release_deferred")
}

// StartCompletionRetry opens the one successor cycle for an attention
// cycle through its explicit authorized Retry. Decision-phase retries
// need only the attention state, the expected prior identity and
// reconciled decision calls: when the record carries an evidence
// fingerprint, its receipt must exist (completed or charged-failed);
// admitted coder runs must be settled and fully charged, and any staged
// adoption must be fully materialized first. Command replay reuses the recorded
// successor; no ordinary poll, input edit or policy edit creates one.
func (c *Coordinator) StartCompletionRetry(ctx context.Context, retry factory.ReassessmentRetry) (string, error) {
	if c.Store == nil {
		return "", store.ErrNotFound
	}
	ctx, unlock, err := c.progressPass(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := c.checkCompletionRetry(ctx, retry); err != nil {
		return "", err
	}
	return c.Store.StartReassessmentRetry(ctx, retry)
}

func (c *Coordinator) checkCompletionRetry(ctx context.Context, retry factory.ReassessmentRetry) error {
	if err := retry.Validate(); err != nil {
		return err
	}
	record, err := c.Store.ReassessmentRecord(ctx, retry.ExpectedPriorCycle)
	if err != nil {
		return err
	}
	if record.Cycle.EventID != retry.EventID {
		return store.ErrCompletionRetryIneligible
	}
	if record.Cycle.State == factory.ReassessmentSuperseded {
		if record.Cycle.Successor != retry.SuccessorCycleID {
			return store.ErrCompletionRetryIneligible
		}
		return nil
	}
	if record.Cycle.State != factory.ReassessmentAttention {
		return store.ErrCompletionRetryIneligible
	}
	for _, runID := range record.Cycle.Ledger.CoderRuns {
		run, err := c.Store.FactoryRun(ctx, runID)
		if err != nil || !run.Reconciled || run.Outcome == "" {
			return store.ErrCompletionRetryIneligible
		}
		assignment, err := c.Store.AssignmentByRun(ctx, runID)
		if err != nil || assignment.Stage != factory.AssignmentFinished || assignment.Subject.Completion == nil || assignment.Subject.Completion.CycleID != record.Cycle.CycleID {
			return store.ErrCompletionRetryIneligible
		}
		allowance, err := c.Store.ReassessmentAttemptAllowance(ctx, record.Cycle.Repository, *assignment.Subject.Completion, assignment.AttemptRoot)
		if err != nil || allowance.Active || !allowance.Closed {
			return store.ErrCompletionRetryIneligible
		}
	}
	if len(record.AdoptionProgress) != 0 {
		progress, err := loadReassessmentAdoption(c.Graph, record)
		if err != nil || progress.Journal.Releasable() != nil {
			return store.ErrCompletionRetryIneligible
		}
	}
	if len(record.Cycle.Ledger.DecisionCalls) > 0 {
		state, err := DecisionStateFromRecord(record)
		if err != nil {
			return err
		}
		if _, err := c.Store.PriorityJudgment(ctx, fingerprintOf(state)); err != nil {
			return store.ErrCompletionRetryIneligible
		}
	}
	if len(record.Cycle.Ledger.DecisionCalls) > 1 {
		if record.ProposalFingerprint == "" {
			return store.ErrCompletionRetryIneligible
		}
		if _, err := c.Store.PriorityJudgment(ctx, record.ProposalFingerprint); err != nil {
			return store.ErrCompletionRetryIneligible
		}
	}
	return nil
}
