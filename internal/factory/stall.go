// D15 stall classification and finite repair accounting. The detector
// runs inside the existing progress pass after observation and
// reconciliation; there is no new timer, polling model or scheduler.
// Detection classifies dependency components and their causal blockers,
// never one repository-wide boolean, so a legitimate external wait in
// one component cannot hide a repairable cycle or missing producer in
// another. Ordinary waits spend no generative or Jev call; only an
// admitted investigation consumes the execution slot.
package factory

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// Stall reasons: the three deterministically established stall causes.
// Anything else is a named wait, stale evidence or attention.
const (
	StallReasonCycle              = "cycle"
	StallReasonMissingProducer    = "missing_producer"
	StallReasonUnexplainedNoReady = "unexplained_no_ready"
)

// ValidStallReason reports whether reason is an established stall cause.
func ValidStallReason(reason string) bool {
	switch reason {
	case StallReasonCycle, StallReasonMissingProducer, StallReasonUnexplainedNoReady:
		return true
	default:
		return false
	}
}

// Stall states: the investigation flow plus its three branches. An
// uncertain applying step reconciles pending effects before release.
const (
	StallObserved       = "observed"
	StallCollecting     = "collecting"
	StallInvestigating  = "investigating"
	StallValidating     = "validating"
	StallAssessing      = "assessing"
	StallApplying       = "applying"
	StallReleased       = "released"
	StallClassifiedWait = "classified_wait"
	StallStale          = "stale"
	StallAttention      = "attention"
)

// ValidStallState reports whether state is a known stall lifecycle state.
func ValidStallState(state string) bool {
	switch state {
	case StallObserved, StallCollecting, StallInvestigating, StallValidating,
		StallAssessing, StallApplying, StallReleased,
		StallClassifiedWait, StallStale, StallAttention:
		return true
	default:
		return false
	}
}

// Named D15 wait kinds. Active work, CI, capacity, quota, preparation,
// permissions, explicit pause, unanswered product choice and pending
// completion are distinct waits with a responsible owner and a next
// event; none alone calls an investigator.
const (
	StallWaitActiveWork        = "active_work"
	StallWaitCIChecks          = "ci_checks"
	StallWaitCapacity          = "capacity"
	StallWaitQuota             = "quota"
	StallWaitPreparation       = "preparation"
	StallWaitPermissions       = "permissions"
	StallWaitExplicitPause     = "explicit_pause"
	StallWaitProductChoice     = "product_choice"
	StallWaitPendingCompletion = "pending_completion"
)

// StallEvent is one bound stall episode observation. Uniqueness is
// (repository, progress_generation, input_fingerprint); the episode
// identity is (repository, progress_generation). Progress advances
// only on verified delivery of a required outcome, never on edits,
// graph adoption, retries or agent declarations.
type StallEvent struct {
	ID                 string `json:"id"`
	Repository         int64  `json:"repository,string"`
	ProgressGeneration int64  `json:"progress_generation"`
	InputFingerprint   string `json:"input_fingerprint"`
	InventoryDigest    string `json:"inventory_digest"`
	SourceCommit       string `json:"source_commit"`
	PreparationDigest  string `json:"preparation_digest"`
	Reason             string `json:"reason"`
	EpisodeID          string `json:"episode_id"`
	State              string `json:"state"`
}

// StallKey names the stall subject key space.
func StallKey(id string) string { return "stall:" + id }

func (e StallEvent) Validate() error {
	if !ValidID(e.ID) || !ValidID(e.EpisodeID) {
		return errors.New("invalid stall identity")
	}
	if e.Repository <= 0 || e.ProgressGeneration < 0 {
		return errors.New("invalid stall scope")
	}
	if !ValidDigest(e.InputFingerprint) || !ValidDigest(e.InventoryDigest) {
		return errors.New("stall must bind its sealed inventory inputs")
	}
	if e.SourceCommit != "" && !ValidCommit(e.SourceCommit) {
		return errors.New("invalid stall source commit")
	}
	if e.PreparationDigest != "" && !ValidDigest(e.PreparationDigest) {
		return errors.New("invalid stall preparation digest")
	}
	if !ValidStallReason(e.Reason) {
		return errors.New("invalid stall reason")
	}
	if !ValidStallState(e.State) {
		return errors.New("invalid stall state")
	}
	return nil
}

// StallSubject binds one repair investigation to its stall: the stall
// identity, the repair cycle and the sealed inventory it may change.
// Only idle accepted generated tasks may change; manual, active,
// PR-bound and completed records keep their IDs.
type StallSubject struct {
	StallID         string `json:"stall_id"`
	CycleID         string `json:"cycle_id"`
	InventoryDigest string `json:"inventory_digest"`
}

func (s StallSubject) Validate() error {
	if !ValidID(s.StallID) || !ValidID(s.CycleID) {
		return errors.New("invalid stall subject identity")
	}
	if !ValidDigest(s.InventoryDigest) {
		return errors.New("stall subject must bind its sealed inventory")
	}
	return nil
}

// Frozen D15 repair allowance defaults: 4 investigator runs, 4 proposal
// judgments and 120 active minutes total per repository grant. Each
// episode allows at most 2 automatic cycles, each one investigator of
// at most 30 active minutes and one proposal Jev call, with the
// tighter remaining limits prevailing.
const (
	DefaultRepairInvestigatorRuns   = 4
	DefaultRepairProposalJudgments  = 4
	DefaultRepairActiveMinutes      = 120
	DefaultRepairCyclesPerEpisode   = 2
	DefaultRepairCycleActiveMinutes = 30
)

// RepairGrant is the explicit repository repair authorization. The
// repository sponsor authorizes this scope; repair funding never
// silently debits a private intent owner's grant. An explicit
// authorized limit increase preserves consumption and prior history.
type RepairGrant struct {
	ID                string `json:"id"`
	Repository        int64  `json:"repository,string"`
	Sponsor           int64  `json:"sponsor,string"`
	Revision          int64  `json:"revision"`
	Predecessor       string `json:"predecessor,omitempty"`
	InvestigatorRuns  int    `json:"investigator_runs"`
	ProposalJudgments int    `json:"proposal_judgments"`
	ActiveMinutes     int    `json:"active_minutes"`
	Effect            string `json:"effect,omitempty"`
	Active            bool   `json:"active"`
	CreatedUnix       int64  `json:"created_unix"`
}

// DefaultRepairGrant returns the frozen D15 default limits.
func DefaultRepairGrant() RepairGrant {
	return RepairGrant{
		InvestigatorRuns:  DefaultRepairInvestigatorRuns,
		ProposalJudgments: DefaultRepairProposalJudgments,
		ActiveMinutes:     DefaultRepairActiveMinutes,
		Active:            true,
	}
}

func (g RepairGrant) Validate() error {
	if !ValidID(g.ID) {
		return errors.New("invalid repair grant identity")
	}
	if g.Repository <= 0 || g.Sponsor <= 0 || g.Revision < 0 || g.CreatedUnix <= 0 {
		return errors.New("invalid repair grant scope")
	}
	if g.Predecessor != "" && !ValidID(g.Predecessor) {
		return errors.New("invalid repair grant predecessor")
	}
	if g.InvestigatorRuns < 0 || g.InvestigatorRuns > DefaultRepairInvestigatorRuns ||
		g.ProposalJudgments < 0 || g.ProposalJudgments > DefaultRepairProposalJudgments ||
		g.ActiveMinutes < 0 || g.ActiveMinutes > DefaultRepairActiveMinutes {
		return errors.New("repair grant exceeds its finite bounds")
	}
	if g.Effect != "" && len(g.Effect) > 4000 {
		return errors.New("invalid repair grant effect")
	}
	return nil
}

// RepairTarget names the grant ledger target for one repository.
func RepairTarget(repository int64) string {
	return "repository/" + strconv.FormatInt(repository, 10) + "/repair-grant"
}

var (
	ErrRepairGrantInactive      = errors.New("repository repair grant is not active")
	ErrRepairRunsExhausted      = errors.New("repair investigator-run allowance exhausted")
	ErrRepairJudgmentsExhausted = errors.New("repair proposal-judgment allowance exhausted")
	ErrRepairTimeExhausted      = errors.New("repair active-time allowance exhausted")
	ErrRepairCyclesExhausted    = errors.New("repair episode cycle allowance exhausted")
	ErrRepairNeedsEvidence      = errors.New("repair second cycle needs materially new factual evidence")
)

// RepairCharge is one immutable charge by run or request identity,
// recorded before launch. Failure stays charged; there is no
// unreserve. Retry, replacement issues and new event hashes never
// refill the episode or repository allowance.
type RepairCharge struct {
	ID             string `json:"id"`
	GrantID        string `json:"grant_id"`
	Kind           string `json:"kind"`
	EpisodeID      string `json:"episode_id"`
	Cycle          int    `json:"cycle"`
	EvidenceDigest string `json:"evidence_digest"`
	ActiveMinutes  int    `json:"active_minutes"`
	SpentUnix      int64  `json:"spent_unix"`
	Failed         bool   `json:"failed"`
}

// Repair charge kinds: one investigator run or one proposal judgment.
const (
	RepairChargeRun      = "run"
	RepairChargeJudgment = "judgment"
)

func (c RepairCharge) Validate() error {
	if !ValidID(c.ID) || !ValidID(c.GrantID) || !ValidID(c.EpisodeID) {
		return errors.New("invalid repair charge identity")
	}
	if c.Kind != RepairChargeRun && c.Kind != RepairChargeJudgment {
		return errors.New("invalid repair charge kind")
	}
	if c.Cycle < 1 || c.Cycle > DefaultRepairCyclesPerEpisode {
		return errors.New("repair charge cycle outside its bound")
	}
	if !ValidDigest(c.EvidenceDigest) {
		return errors.New("repair charge must bind its factual evidence")
	}
	if c.ActiveMinutes < 0 || c.ActiveMinutes > DefaultRepairCycleActiveMinutes {
		return errors.New("repair charge exceeds its cycle time bound")
	}
	if c.SpentUnix <= 0 {
		return errors.New("invalid repair charge time")
	}
	return nil
}

// RepairAccount applies one grant's finite allowances to its immutable
// charge history. Duplicate ticks, restarts, hash changes, issue
// replacement, graph reshuffles and retries replay existing charges
// instead of consuming new room: unchanged facts spend once.
type RepairAccount struct {
	Grant   RepairGrant
	Charges []RepairCharge
}

// chargeIDs returns the spent charge identities for replay detection.
func (a RepairAccount) chargeIDs() map[string]RepairCharge {
	spent := make(map[string]RepairCharge, len(a.Charges))
	for _, charge := range a.Charges {
		spent[charge.ID] = charge
	}
	return spent
}

// episodeEvidence returns the distinct factual evidence digests spent
// per episode and cycle, so a second cycle must bring new facts.
func (a RepairAccount) episodeEvidence(episode string) (first string, cycles int) {
	seen := map[int]string{}
	for _, charge := range a.Charges {
		if charge.EpisodeID != episode {
			continue
		}
		if _, ok := seen[charge.Cycle]; !ok {
			seen[charge.Cycle] = charge.EvidenceDigest
		}
	}
	ordered := 0
	for cycle := 1; cycle <= DefaultRepairCyclesPerEpisode; cycle++ {
		digest, ok := seen[cycle]
		if !ok {
			break
		}
		ordered = cycle
		if cycle == 1 {
			first = digest
		}
	}
	return first, ordered
}

// spentTotals returns consumed runs, judgments and active minutes.
func (a RepairAccount) spentTotals() (runs, judgments, minutes int) {
	for _, charge := range a.Charges {
		switch charge.Kind {
		case RepairChargeRun:
			runs++
			minutes += charge.ActiveMinutes
		case RepairChargeJudgment:
			judgments++
		}
	}
	return runs, judgments, minutes
}

// AdmitCharge charges one investigator run or proposal judgment before
// its launch. Replaying the same charge identity returns the recorded
// charge without spending again, even after exhaustion. A second
// automatic cycle for an episode requires materially new factual
// evidence; a graph reshuffle alone does not qualify. Two
// unsuccessful cycles require owner attention.
func (a RepairAccount) admitChargeShape(charge RepairCharge) error {
	if err := a.Grant.Validate(); err != nil {
		return err
	}
	return charge.Validate()
}

func (a RepairAccount) admitChargeGrant(charge RepairCharge) error {
	if !a.Grant.Active {
		return ErrRepairGrantInactive
	}
	if charge.GrantID != a.Grant.ID {
		return errors.New("repair charge does not match its grant")
	}
	return nil
}

func (a RepairAccount) admitChargeCycle(charge RepairCharge) error {
	first, cycles := a.episodeEvidence(charge.EpisodeID)
	if charge.Cycle > cycles+1 {
		return ErrRepairCyclesExhausted
	}
	if charge.Cycle == cycles+1 && cycles >= 1 {
		if cycles+1 > DefaultRepairCyclesPerEpisode {
			return ErrRepairCyclesExhausted
		}
		if charge.EvidenceDigest == first {
			return ErrRepairNeedsEvidence
		}
	}
	if charge.Cycle > DefaultRepairCyclesPerEpisode {
		return ErrRepairCyclesExhausted
	}
	// Each cycle holds at most one investigator run and one proposal
	// judgment: a further charge for a spent cycle needs owner
	// attention even when the repository totals still show room.
	for _, prior := range a.Charges {
		if prior.EpisodeID == charge.EpisodeID && prior.Cycle == charge.Cycle && prior.Kind == charge.Kind {
			return ErrRepairCyclesExhausted
		}
	}
	return nil
}

func (a RepairAccount) AdmitCharge(charge RepairCharge) (RepairAccount, bool, error) {
	if err := a.admitChargeShape(charge); err != nil {
		return RepairAccount{}, false, err
	}
	if _, ok := a.chargeIDs()[charge.ID]; ok {
		return a, false, nil
	}
	if err := a.admitChargeGrant(charge); err != nil {
		return RepairAccount{}, false, err
	}
	if err := a.admitChargeCycle(charge); err != nil {
		return RepairAccount{}, false, err
	}
	runs, judgments, minutes := a.spentTotals()
	switch charge.Kind {
	case RepairChargeRun:
		if runs >= a.Grant.InvestigatorRuns {
			return RepairAccount{}, false, ErrRepairRunsExhausted
		}
		if minutes+charge.ActiveMinutes > a.Grant.ActiveMinutes {
			return RepairAccount{}, false, ErrRepairTimeExhausted
		}
	case RepairChargeJudgment:
		if judgments >= a.Grant.ProposalJudgments {
			return RepairAccount{}, false, ErrRepairJudgmentsExhausted
		}
	}
	next := a
	next.Charges = append(append([]RepairCharge(nil), a.Charges...), charge)
	return next, true, nil
}

// Frozen D15 intent execution aggregate bounds: 480 active minutes by
// default, allowed 1 to 10,080 minutes, explicitly recorded at
// authorization beside existing intent funding. All generated issues,
// attempts and completion reassessments retain origin intent;
// retries, revisions and splits cannot refill this aggregate.
const (
	DefaultIntentActiveMinutes = 480
	MinIntentActiveMinutes     = 1
	MaxIntentActiveMinutes     = 10080
)

// IntentExecutionAllowance is the immutable active-time aggregate for
// one origin intent. New attempts, decomposition revisions,
// split or replacement issues and verification cannot refill stable
// usage; parent intervals charge once and never multiply native
// workers; queued work consumes no active time.
type IntentExecutionAllowance struct {
	IntentID       string   `json:"intent_id"`
	OwnerID        int64    `json:"owner_id,string"`
	RepositoryID   int64    `json:"repository_id,string"`
	Revision       int64    `json:"revision"`
	Predecessor    string   `json:"predecessor,omitempty"`
	ActiveLimitMs  int64    `json:"active_limit_ms"`
	ConsumedMs     int64    `json:"consumed_ms"`
	AuthorityRefs  []string `json:"authority_refs,omitempty"`
	Active         bool     `json:"active"`
	CheckpointUnix int64    `json:"checkpoint_unix"`
}

// DefaultIntentExecutionAllowance returns the frozen D15 default.
func DefaultIntentExecutionAllowance() IntentExecutionAllowance {
	return IntentExecutionAllowance{
		ActiveLimitMs: int64(DefaultIntentActiveMinutes) * 60 * 1000,
		Active:        false,
	}
}

func (a IntentExecutionAllowance) Validate() error {
	if a.IntentID == "" || a.OwnerID <= 0 || a.RepositoryID <= 0 || a.Revision < 0 || a.CheckpointUnix <= 0 {
		return errors.New("invalid intent execution aggregate binding")
	}
	if a.Predecessor != "" && !ValidID(a.Predecessor) {
		return errors.New("invalid intent execution aggregate predecessor")
	}
	minMs := int64(MinIntentActiveMinutes) * 60 * 1000
	maxMs := int64(MaxIntentActiveMinutes) * 60 * 1000
	if a.ActiveLimitMs < minMs || a.ActiveLimitMs > maxMs {
		return errors.New("intent active-time limit outside its bound")
	}
	if a.ConsumedMs < 0 || a.ConsumedMs > a.ActiveLimitMs {
		return errors.New("intent active-time consumption outside its limit")
	}
	for _, ref := range a.AuthorityRefs {
		if !ValidID(ref) {
			return errors.New("invalid intent execution authority reference")
		}
	}
	return nil
}

// IntentExecutionTarget names the aggregate ledger target for one intent.
func IntentExecutionTarget(intent string) string {
	return "intent/" + intent + "/execution-limit"
}

var (
	ErrIntentTimeExhausted = errors.New("intent active-time aggregate exhausted")
	ErrIntentChargeOverlap = errors.New("intent interval already charged")
)

// intentInterval is one observed parent active interval, charged once
// by its run identity. Overlapping native-worker observations of the
// same parent interval never multiply the charge.
type intentInterval struct {
	run   string
	start int64
	end   int64
}

// ChargeInterval records one observed parent active interval. The same
// run identity replays without charging again; queued work (a zero or
// negative duration) consumes no active time. Consumption is
// cumulative and never decreases: retries, revisions, splits and
// replacement issues share the origin aggregate instead of refilling
// it. Only the union of disjoint observed time advances the total, so
// overlapping native-worker reports of one parent interval charge once.
func (a IntentExecutionAllowance) ChargeInterval(intervals []intentInterval, run string, startUnix, endUnix int64) (IntentExecutionAllowance, []intentInterval, bool, error) {
	if err := a.Validate(); err != nil {
		return IntentExecutionAllowance{}, nil, false, err
	}
	if run == "" {
		return IntentExecutionAllowance{}, nil, false, errors.New("invalid intent charge run identity")
	}
	for _, charged := range intervals {
		if charged.run == run {
			return a, intervals, false, nil
		}
	}
	if endUnix <= startUnix {
		return a, intervals, false, nil
	}
	if startUnix < 0 || endUnix < 0 || endUnix-startUnix > math.MaxInt64/1000 {
		return IntentExecutionAllowance{}, nil, false, errors.New("invalid intent charge interval")
	}
	addMs := (endUnix - startUnix) * 1000
	for _, charged := range intervals {
		overlapStart := startUnix
		if charged.start > overlapStart {
			overlapStart = charged.start
		}
		overlapEnd := endUnix
		if charged.end < overlapEnd {
			overlapEnd = charged.end
		}
		if overlapEnd > overlapStart {
			addMs -= (overlapEnd - overlapStart) * 1000
		}
	}
	next := a
	nextIntervals := append(append([]intentInterval(nil), intervals...), intentInterval{run: run, start: startUnix, end: endUnix})
	if addMs <= 0 {
		return next, nextIntervals, false, nil
	}
	if next.ConsumedMs+addMs > next.ActiveLimitMs {
		return IntentExecutionAllowance{}, nil, false, ErrIntentTimeExhausted
	}
	next.ConsumedMs += addMs
	return next, nextIntervals, true, nil
}

// Checkpoint closes the previous active interval. Pause stops charging
// only after confirmed retirement: deactivating while runs are still
// unretired keeps the clock running. Time is never subtracted.
func (a IntentExecutionAllowance) Checkpoint(now time.Time, active bool, runsRetired bool) (IntentExecutionAllowance, error) {
	if err := a.Validate(); err != nil {
		return IntentExecutionAllowance{}, err
	}
	if now.Unix() < a.CheckpointUnix {
		return IntentExecutionAllowance{}, ErrAllowanceClock
	}
	if a.Active && !active && !runsRetired {
		next := a
		next.CheckpointUnix = now.Unix()
		return next, nil
	}
	if a.Active {
		elapsed := now.Unix() - a.CheckpointUnix
		if elapsed > math.MaxInt64/1000-a.ConsumedMs/1000 {
			return IntentExecutionAllowance{}, errors.New("intent active time overflow")
		}
		a.ConsumedMs += elapsed * 1000
		if a.ConsumedMs > a.ActiveLimitMs {
			a.ConsumedMs = a.ActiveLimitMs
		}
	}
	a.CheckpointUnix, a.Active = now.Unix(), active
	return a, nil
}

// Repair outcomes: the stall is addressed by a repair proposal, the
// evidence establishes a legitimate wait, inspection establishes no
// change is needed, or the investigation is incomplete and held.
const (
	RepairOutcomeRepair       = "repair"
	RepairOutcomeVerifiedWait = "verified_wait"
	RepairOutcomeNoChange     = "no_change"
	RepairOutcomeIncomplete   = "incomplete"
)

// ValidRepairOutcome reports whether outcome is an admitted D15/D16
// repair outcome.
func ValidRepairOutcome(outcome string) bool {
	switch outcome {
	case RepairOutcomeRepair, RepairOutcomeVerifiedWait,
		RepairOutcomeNoChange, RepairOutcomeIncomplete:
		return true
	default:
		return false
	}
}

// validateRepairEvidence checks one repair report's shared evidence
// envelope under the D15 limits: bounded inspected sources, causes,
// retained issues, intent changes, coverage and attention. Coverage
// entries must each validate with no duplicate outcome, but the D13
// change-matching rule stays D13's own: repair coverage stands on its
// own bindings.
func validateRepairInspected(inspected []InspectedSource) error {
	if len(inspected) > MaxInspectedRefs {
		return errors.New("repair report inspects too many sources")
	}
	for _, ref := range inspected {
		if !validOwnershipEntry(ref.Path) || strings.HasSuffix(ref.Path, "/") {
			return errors.New("repair inspected source is not an exact file")
		}
		if !ValidCommit(ref.Commit) {
			return errors.New("repair inspected source lacks its commit")
		}
		if !intentText(ref.Evidence, MaxPlanTextBytes) {
			return errors.New("repair inspected evidence outside its bound")
		}
	}
	return nil
}

func validateRepairCauses(causes []CheckFailure, retained []int64) error {
	if len(causes) == 0 || len(causes) > MaxInspectedRefs {
		return errors.New("repair report lacks its bounded causes")
	}
	for _, cause := range causes {
		if err := cause.Validate(); err != nil {
			return err
		}
	}
	if !sortedUniqueIndexes(retained) {
		return errors.New("repair retained issues are not sorted unique")
	}
	for _, index := range retained {
		if index < 1 {
			return errors.New("invalid repair retained issue")
		}
	}
	return nil
}

func validateRepairChanges(changes []ReassessmentIntentChange) error {
	if len(changes) > MaxReassessmentIntentChanges {
		return errors.New("repair intent changes exceed their bound")
	}
	prior := ""
	for _, change := range changes {
		if change.IntentID <= prior {
			return errors.New("repair intent changes are not sorted unique")
		}
		prior = change.IntentID
		if err := change.validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateRepairCoverage(coverage []ReassessmentCoverage, attention []ReassessmentAttentionEntry) error {
	if len(coverage) > MaxReassessmentCoverage {
		return errors.New("repair coverage exceeds its bound")
	}
	covered := make(map[string]bool, len(coverage))
	for _, entry := range coverage {
		if err := entry.validateCoverage(true); err != nil {
			return err
		}
		key := entry.IntentID + "\x00" + entry.OutcomeID
		if covered[key] {
			return errors.New("repair outcome covered twice")
		}
		covered[key] = true
	}
	if len(attention) > MaxReassessmentAttention {
		return errors.New("repair attention exceeds its bound")
	}
	for _, entry := range attention {
		if err := entry.validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateRepairEvidence(
	inspected []InspectedSource,
	causes []CheckFailure,
	retained []int64,
	changes []ReassessmentIntentChange,
	coverage []ReassessmentCoverage,
	attention []ReassessmentAttentionEntry,
) error {
	if err := validateRepairInspected(inspected); err != nil {
		return err
	}
	if err := validateRepairCauses(causes, retained); err != nil {
		return err
	}
	if err := validateRepairChanges(changes); err != nil {
		return err
	}
	return validateRepairCoverage(coverage, attention)
}

// StallRepairReport is the typed output of a completed repair phase
// answering a stall: the full D15 field set over the sealed inventory
// with inspected sources, causes, retained issues, intent changes,
// coverage, attention and outcome. It carries no candidate. Cause and
// coverage adoption detail belongs to the R37 repair consumers; the
// envelope binds the result.
type StallRepairReport struct {
	StallID              string                       `json:"stall_id"`
	CycleID              string                       `json:"cycle_id"`
	InventoryDigest      string                       `json:"inventory_digest"`
	SourceCommit         string                       `json:"source_commit"`
	PreparationDigest    string                       `json:"preparation_digest"`
	Inspected            []InspectedSource            `json:"inspected_refs"`
	Causes               []CheckFailure               `json:"causes"`
	RetainedIssueIndexes []int64                      `json:"retained_issue_indexes"`
	IntentChanges        []ReassessmentIntentChange   `json:"intent_changes"`
	Coverage             []ReassessmentCoverage       `json:"coverage"`
	Attention            []ReassessmentAttentionEntry `json:"attention"`
	Outcome              string                       `json:"outcome"`
}

func (r StallRepairReport) Validate() error {
	if !ValidID(r.StallID) || !ValidID(r.CycleID) {
		return errors.New("invalid stall repair report identity")
	}
	if !ValidDigest(r.InventoryDigest) || !ValidDigest(r.PreparationDigest) {
		return errors.New("stall repair report must bind its sealed digests")
	}
	if !ValidCommit(r.SourceCommit) {
		return errors.New("invalid stall repair report source")
	}
	if !ValidRepairOutcome(r.Outcome) {
		return errors.New("invalid stall repair outcome")
	}
	return validateRepairEvidence(
		r.Inspected, r.Causes, r.RetainedIssueIndexes,
		r.IntentChanges, r.Coverage, r.Attention,
	)
}

// ApplyIncrease records an explicit owner-authorized limit increase. It
// retains consumed usage and prior history; it does not change accepted
// scope or refill anything.
func (a IntentExecutionAllowance) ApplyIncrease(newLimitMs int64) (IntentExecutionAllowance, error) {
	if err := a.Validate(); err != nil {
		return IntentExecutionAllowance{}, err
	}
	minMs := int64(MinIntentActiveMinutes) * 60 * 1000
	maxMs := int64(MaxIntentActiveMinutes) * 60 * 1000
	if newLimitMs < a.ActiveLimitMs || newLimitMs < minMs || newLimitMs > maxMs {
		return IntentExecutionAllowance{}, errors.New("intent limit increase must stay inside its bound")
	}
	next := a
	next.ActiveLimitMs = newLimitMs
	return next, nil
}
