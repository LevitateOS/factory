// Frozen D13 post-completion reassessment contracts (FW30).
//
// After every verified completion each repository fences scheduling until
// its completion event receives its own Jev decision, even for an empty
// inventory. A cycle permits two Jev requests and one coder run; only an
// explicit authorized Retry starts a successor cycle. FW31 persists these
// records, fences dispatch and charges funding; FW32 admits the
// completion-subject coder, validates reports and applies graph revisions.
// This file freezes types, bounds, keys and pure transitions only: no
// storage, dispatch or native effects live here.
package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// ReassessmentInventoryDigest binds the ordered issue indexes and folded
// canonical body bytes to their repository, completion generation and native
// revision. Storage uses the same primitive to recognize a sealed empty set.
func ReassessmentInventoryDigest(repository, generation, revision int64, running [32]byte, indexes []int64) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d\x00%d\x00%d\x00", repository, generation, revision)))
	h.Write(running[:])
	for _, index := range indexes {
		h.Write([]byte(strconv.FormatInt(index, 10) + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// D13 Jev purposes and options. The decision asks whether the delivered
// change or current issue set warrants revising scope allocation,
// decomposition, acceptance details or dependencies; the proposal
// assessment judges one coherent revision set. Anything other than a
// valid selected option holds the event with attention: never an inferred
// continue and never an automatic extra agent call.
const (
	ReassessmentDecisionPurpose = "post-completion-reassessment-v1"
	ReassessmentProposalPurpose = "post-completion-proposal-v1"

	ReassessmentOptionReassess       = "reassess"
	ReassessmentOptionContinue       = "continue"
	ReassessmentOptionAccept         = "accept"
	ReassessmentOptionNeedsAttention = "needs_attention"
)

// Funded-decision thresholds (product policy). A decision option with
// confidence below the bound is not actionable; a proposal needs both
// option probability and confidence at or above its bounds.
const (
	MinReassessmentDecisionConfidence  = 0.50
	MinReassessmentProposalProbability = 0.80
	MinReassessmentProposalConfidence  = 0.80
)

// Two-call/one-run ledger bounds: the initial decision and, only on
// reassess, the proposal assessment; one coder run per cycle. Failed or
// indeterminate requests stay charged: reservations are never released.
const (
	MaxReassessmentDecisionCalls = 2
	MaxReassessmentCoderRuns     = 1
)

// Complete-inventory bounds. The sealed inventory holds every open
// non-PR native issue of the repository, ordered by issue index.
const (
	MaxReassessmentIssues        = 4096
	MaxReassessmentEvidenceBytes = 256 * 1024
)

// Reassessment report bounds. Text and reference limits reuse the D03
// plan bounds; the encoded report reuses the 64 KiB phase-result bound.
// The prompt exception (320 KiB with the complete inventory inline) is
// already pinned to the reassess phase and completion subject in
// assignment.go and project.MaxReassessPrompt.
const (
	MaxReassessmentIntentChanges = 64
	MaxReassessmentCoverage      = 64
	MaxReassessmentAttention     = 64
	MaxReassessmentSummaryBytes  = MaxPlanTextBytes
)

// Validation-report bound: the exact-field assessment digest FW32 asks
// Jev to judge alongside the proposal.
const MaxReassessmentValidationBytes = 16 * 1024

// Reassessment wait/retry reason bound for read models. Reasons name the
// resumable step, never evidence bytes.
const MaxReassessmentReasonChars = 256

// CompletionLocal is the local half of sealed D13 evidence: the source
// commit, preparation, accepted scope and authority the sealed native
// inventory was captured against, as digests only. FW31 seals it with
// the native inventory in one bracket; FW32 rechecks it before
// applying any graph effects. Ordinary observation timestamps never
// enter these digests; material content or source changes do. FW32
// carries the complete model-safe material alongside its digest:
// ScopeDigest is exactly the canonical material digest, never a second
// competing scope hash.
type CompletionLocal struct {
	Material             *CompletionMaterial `json:"material"`
	SourceCommit         string              `json:"source_commit"`
	PreparationDigest    string              `json:"preparation_digest"`
	ScopeDigest          string              `json:"scope_digest"`
	AuthorityFingerprint string              `json:"authority_fingerprint"`
}

// Equal compares canonical value content, not pointer identity: the four
// bindings plus the canonical material bytes. Unvalidated material never
// compares equal.
func (l CompletionLocal) Equal(other CompletionLocal) bool {
	if l.SourceCommit != other.SourceCommit || l.PreparationDigest != other.PreparationDigest ||
		l.ScopeDigest != other.ScopeDigest || l.AuthorityFingerprint != other.AuthorityFingerprint {
		return false
	}
	if (l.Material == nil) != (other.Material == nil) {
		return false
	}
	if l.Material == nil {
		return true
	}
	mine, err := l.Material.canonicalJSON()
	if err != nil {
		return false
	}
	theirs, err := other.Material.canonicalJSON()
	if err != nil {
		return false
	}
	return string(mine) == string(theirs)
}

// Digest hashes the complete validated local envelope for the report's
// LocalInputDigest: bindings plus the canonical material body.
func (l CompletionLocal) Digest() (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Sealed reports whether the local half carries its bindings. An
// unsealed local never seals an inventory and never releases an event.
func (l CompletionLocal) Sealed() bool {
	return l != (CompletionLocal{})
}

// MaxReassessmentDecisionBytes bounds the entire encoded D13 decision
// request (post-completion-reassessment-v1): the complete <=256 KiB
// sealed issue evidence plus the completion/local envelope must fit
// before any funded call is reserved. Requests beyond it refuse without
// spend; evidence is never truncated to fit.
const MaxReassessmentDecisionBytes = 320 * 1024

// CompletionIssueEvidence is one sealed issue's substance: the exact
// title, body, dependency edges and semantic digests the D13 decision
// judges. Dependencies carry native dependency issue IDs in numeric
// order, the same edge currency as the acceptance evidence. It is the
// canonical form of one collector Data payload: the producer encodes
// it, the seal persists it immutably, and the decision request carries
// it so Jev assesses substance, not hashes. Field order is fixed so
// the canonical encoding is digest-stable.
type CompletionIssueEvidence struct {
	Dependencies  []string `json:"dependencies,omitempty"`
	Index         int64    `json:"index"`
	Title         string   `json:"title"`
	Body          string   `json:"body"`
	TitleDigest   string   `json:"title_digest"`
	ContentDigest string   `json:"content_digest"`
}

// Validate checks one sealed issue body: a positive index, a nonempty
// title, valid semantic digests and numerically ascending unique
// native dependency IDs. The body itself may be empty; native titles
// may not.
func (e CompletionIssueEvidence) Validate() error {
	if e.Index < 1 {
		return errors.New("sealed issue lacks its index")
	}
	if e.Title == "" || strings.ContainsRune(e.Title, 0) || strings.ContainsRune(e.Body, 0) {
		return errors.New("sealed issue lacks its exact title and body")
	}
	if !ValidDigest(e.TitleDigest) || !ValidDigest(e.ContentDigest) {
		return errors.New("sealed issue lacks its semantic digests")
	}
	prior := ""
	for _, dep := range e.Dependencies {
		if !ValidDependencyRef(dep) || compareDependencyRef(dep, prior) <= 0 {
			return errors.New("sealed issue dependencies are not ascending and unique")
		}
		prior = dep
	}
	return nil
}

// ValidDependencyRef reports whether a native dependency ID is a
// canonical positive decimal without leading zeros.
func ValidDependencyRef(id string) bool {
	if id == "" || id[0] == '0' || len(id) > 20 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// compareDependencyRef orders canonical decimal IDs numerically:
// shorter digit strings name smaller issues.
func compareDependencyRef(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// CanonicalData encodes one sealed issue body in its digest-stable
// canonical form: the exact bytes the collector folds and the seal
// persists.
func (e CompletionIssueEvidence) CanonicalData() (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ParseCompletionIssueEvidence decodes one canonical collector payload
// back into its substance. Anything outside the canonical shape refuses
// so a malformed page can never seal.
func ParseCompletionIssueEvidence(data string) (CompletionIssueEvidence, error) {
	var evidence CompletionIssueEvidence
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return CompletionIssueEvidence{}, errors.New("sealed issue body outside its canonical shape")
	}
	if err := evidence.Validate(); err != nil {
		return CompletionIssueEvidence{}, err
	}
	return evidence, nil
}

// Validate requires every binding plus the complete material: a sealed
// value carries the validated material with ScopeDigest exactly its
// canonical digest, alongside the existing four-field shape checks. An
// all-zero local stays unsealed and never funds authority; there is no
// nil-material legacy path.
func (l CompletionLocal) Validate() error {
	if l.Material == nil {
		return errors.New("completion local lacks its material")
	}
	if err := l.Material.Validate(); err != nil {
		return err
	}
	digest, err := l.Material.Digest()
	if err != nil {
		return err
	}
	if digest != l.ScopeDigest {
		return errors.New("completion local scope differs from its material")
	}
	if !ValidCommit(l.SourceCommit) {
		return errors.New("completion local lacks its source commit")
	}
	if !ValidDigest(l.PreparationDigest) || !ValidDigest(l.ScopeDigest) || l.AuthorityFingerprint == "" ||
		len(l.AuthorityFingerprint) > MaxReassessmentReasonChars || strings.ContainsRune(l.AuthorityFingerprint, 0) {
		return errors.New("completion local lacks its bindings")
	}
	return nil
}

// Reassessment cycle states. Only a durable sealed inventory advances to
// deciding; a valid continue releases (after FW31 freshness checks); a
// valid reassess admits the sole run; a valid report advances to
// assessing; an accepted changed report enters applying while an accepted
// no-change report releases. Failures enter attention with the exact
// resumable step retained. Observation can finish recorded effects; only
// explicit authorized Retry supersedes an unresolved terminal cycle.
const (
	ReassessmentCollecting  = "collecting"
	ReassessmentDeciding    = "deciding"
	ReassessmentReassessing = "reassessing"
	ReassessmentAssessing   = "assessing"
	ReassessmentApplying    = "applying"
	ReassessmentReleased    = "released"
	ReassessmentAttention   = "attention"
	ReassessmentSuperseded  = "superseded"
)

// CompletionEvent is one verified completion: merged result, PR closure
// and original issue-closing occurrence confirmed under D09. Agent
// success, a closed-but-unmerged PR, manual closure and polling ticks
// are not completions. The unique key is publication plus original
// closing occurrence: observing the same effect through another recovery
// path cannot create another event.
type CompletionEvent struct {
	PublicationID     string `json:"publication_id"`
	MergedCommit      string `json:"merged_commit"`
	ClosingOccurrence string `json:"closing_occurrence"`
	AcceptanceID      string `json:"acceptance_id"`
	ID                string `json:"id"`
	Repository        int64  `json:"repository,string"`
	Issue             int64  `json:"issue,string"`
	Generation        int64  `json:"generation"`
}

func (e CompletionEvent) Validate() error {
	if !ValidID(e.ID) || !ValidID(e.PublicationID) {
		return errors.New("invalid completion event identity")
	}
	if e.Repository <= 0 || e.Issue <= 0 || e.Generation < 1 {
		return errors.New("invalid completion event scope or generation")
	}
	if !project.ValidDecisionID(e.AcceptanceID) {
		return errors.New("invalid completion acceptance binding")
	}
	if !ValidCommit(e.MergedCommit) {
		return errors.New("invalid completion merged commit")
	}
	if e.ClosingOccurrence == "" || len(e.ClosingOccurrence) > MaxReassessmentReasonChars ||
		strings.ContainsRune(e.ClosingOccurrence, 0) {
		return errors.New("invalid completion closing occurrence")
	}
	return nil
}

// UniqueKey returns the idempotence key for the event: publication plus
// original closing occurrence.
func (e CompletionEvent) UniqueKey() string {
	return e.PublicationID + "\x00" + e.ClosingOccurrence
}

// SubjectKey returns the canonical admission key for the completion.
func (e CompletionEvent) SubjectKey() string {
	return "completion:" + e.ID
}

// ReassessmentLedger is the two-call/one-run funding ledger for one
// cycle. Reservations are recorded before the outcome is known and stay
// charged whatever happens; no poll, input edit or policy edit refills
// a cycle. FW31 reserves each request against this ledger and the
// existing global allowance atomically.
type ReassessmentLedger struct {
	DecisionCalls []string `json:"decision_calls"`
	CoderRuns     []string `json:"coder_runs"`
}

func (l ReassessmentLedger) Validate() error {
	if len(l.DecisionCalls) > MaxReassessmentDecisionCalls {
		return errors.New("reassessment decision calls exceed their allowance")
	}
	if len(l.CoderRuns) > MaxReassessmentCoderRuns {
		return errors.New("reassessment coder runs exceed their allowance")
	}
	seen := make(map[string]bool, len(l.DecisionCalls)+len(l.CoderRuns))
	for _, id := range append(append([]string(nil), l.DecisionCalls...), l.CoderRuns...) {
		if !ValidID(id) || seen[id] {
			return errors.New("invalid reassessment ledger identity")
		}
		seen[id] = true
	}
	return nil
}

// ReserveDecisionCall records one Jev request under its purpose: the
// initial decision or, only after a reassess decision, the proposal
// assessment. It refuses a third call and any other purpose.
func (l ReassessmentLedger) ReserveDecisionCall(callID, purpose, decision string) (ReassessmentLedger, error) {
	if !ValidID(callID) {
		return ReassessmentLedger{}, errors.New("invalid reassessment call identity")
	}
	if purpose != ReassessmentDecisionPurpose && purpose != ReassessmentProposalPurpose {
		return ReassessmentLedger{}, errors.New("invalid reassessment call purpose")
	}
	if purpose == ReassessmentProposalPurpose && decision != ReassessmentOptionReassess {
		return ReassessmentLedger{}, errors.New("proposal assessment needs its reassess decision")
	}
	if len(l.DecisionCalls) >= MaxReassessmentDecisionCalls {
		return ReassessmentLedger{}, errors.New("reassessment decision calls exceed their allowance")
	}
	for _, id := range l.DecisionCalls {
		if id == callID {
			return ReassessmentLedger{}, errors.New("reassessment call identity reused")
		}
	}
	l.DecisionCalls = append(append([]string(nil), l.DecisionCalls...), callID)
	return l, nil
}

// AdmitCoderRun records the cycle's sole coder run. Only a reassess
// decision admits it; a continue decision launches no agent.
func (l ReassessmentLedger) AdmitCoderRun(runID, decision string) (ReassessmentLedger, error) {
	if !ValidID(runID) {
		return ReassessmentLedger{}, errors.New("invalid reassessment run identity")
	}
	if decision != ReassessmentOptionReassess {
		return ReassessmentLedger{}, errors.New("reassessment run needs its reassess decision")
	}
	if len(l.CoderRuns) >= MaxReassessmentCoderRuns {
		return ReassessmentLedger{}, errors.New("reassessment coder runs exceed their allowance")
	}
	l.CoderRuns = append(append([]string(nil), l.CoderRuns...), runID)
	return l, nil
}

// ReassessmentRetry is the explicit authorized Retry identity for one
// event: after prior calls, runs and operations are reconciled, it names
// the expected prior cycle and the one successor that continues its
// history. Command replay reuses the successor; no ordinary poll, input
// edit or policy edit creates one.
type ReassessmentRetry struct {
	SuccessorCycleID   string `json:"successor_cycle_id"`
	ExpectedPriorCycle string `json:"expected_prior_cycle"`
	CommandID          string `json:"command_id"`
	EventID            string `json:"event_id"`
}

func (r ReassessmentRetry) Validate() error {
	if !ValidID(r.EventID) || !ValidID(r.ExpectedPriorCycle) ||
		!ValidID(r.SuccessorCycleID) || !ValidID(r.CommandID) {
		return errors.New("invalid reassessment retry identity")
	}
	if r.SuccessorCycleID == r.ExpectedPriorCycle {
		return errors.New("reassessment successor repeats its prior cycle")
	}
	return nil
}

// ReassessmentCycle is one bounded completion cycle: its own Jev
// decision even when the remaining inventory is empty or identical to
// another completion's. It is never a child of the closed issue attempt:
// the completed root cannot reopen.
type ReassessmentCycle struct {
	Ledger     ReassessmentLedger `json:"ledger"`
	Successor  string             `json:"successor_cycle_id,omitempty"`
	RetryOf    string             `json:"retry_of,omitempty"`
	Proposal   string             `json:"proposal,omitempty"`
	Decision   string             `json:"decision,omitempty"`
	EventID    string             `json:"event_id"`
	CycleID    string             `json:"cycle_id"`
	State      string             `json:"state"`
	Repository int64              `json:"repository,string"`
	Generation int64              `json:"generation"`
}

func validReassessmentState(state string) bool {
	switch state {
	case ReassessmentCollecting, ReassessmentDeciding, ReassessmentReassessing,
		ReassessmentAssessing, ReassessmentApplying, ReassessmentReleased,
		ReassessmentAttention, ReassessmentSuperseded:
		return true
	default:
		return false
	}
}

func (c ReassessmentCycle) Validate() error {
	if !ValidID(c.EventID) || !ValidID(c.CycleID) {
		return errors.New("invalid reassessment cycle identity")
	}
	if c.Repository <= 0 || c.Generation < 1 {
		return errors.New("invalid reassessment cycle scope or generation")
	}
	if !validReassessmentState(c.State) {
		return errors.New("invalid reassessment cycle state")
	}
	if err := c.Ledger.Validate(); err != nil {
		return err
	}
	switch c.Decision {
	case "", ReassessmentOptionReassess, ReassessmentOptionContinue:
	default:
		return errors.New("invalid reassessment decision")
	}
	switch c.Proposal {
	case "", ReassessmentOptionAccept, ReassessmentOptionNeedsAttention:
	default:
		return errors.New("invalid reassessment proposal outcome")
	}
	if c.Proposal != "" && c.Decision != ReassessmentOptionReassess {
		return errors.New("reassessment proposal needs its reassess decision")
	}
	if len(c.Ledger.CoderRuns) != 0 && c.Decision != ReassessmentOptionReassess {
		return errors.New("reassessment run needs its reassess decision")
	}
	if c.RetryOf != "" && !ValidID(c.RetryOf) {
		return errors.New("invalid reassessment retry lineage")
	}
	if c.Successor != "" && (!ValidID(c.Successor) || c.State != ReassessmentSuperseded) {
		return errors.New("invalid reassessment successor")
	}
	return nil
}

// ValidReassessmentDecision reports whether a Jev decision response is
// actionable: the selected option with confidence at or above the bound.
// Anything else holds the event with attention.
func ValidReassessmentDecision(option string, confidence float64) bool {
	if confidence < MinReassessmentDecisionConfidence || confidence > 1 {
		return false
	}
	return option == ReassessmentOptionReassess || option == ReassessmentOptionContinue
}

// ValidReassessmentProposal reports whether a proposal assessment is
// actionable: selected accept with option probability and confidence at
// or above their bounds.
func ValidReassessmentProposal(option string, probability, confidence float64) bool {
	if option != ReassessmentOptionAccept {
		return false
	}
	return probability >= MinReassessmentProposalProbability && probability <= 1 &&
		confidence >= MinReassessmentProposalConfidence && confidence <= 1
}

// RecordDecision files one cycle decision. It requires the deciding
// state and an actionable response; anything else refuses so the caller
// holds the event with attention instead of inferring continue.
func (c ReassessmentCycle) RecordDecision(option string, confidence float64) (ReassessmentCycle, error) {
	if c.State != ReassessmentDeciding {
		return ReassessmentCycle{}, errors.New("reassessment decision outside its deciding state")
	}
	if !ValidReassessmentDecision(option, confidence) {
		return ReassessmentCycle{}, errors.New("reassessment decision is not actionable")
	}
	c.Decision = option
	return c, nil
}

// RecordProposal files the proposal assessment. It requires the
// assessing state, a reassess decision and an actionable accept;
// anything else refuses so the event waits for intervention.
func (c ReassessmentCycle) RecordProposal(option string, probability, confidence float64) (ReassessmentCycle, error) {
	if c.State != ReassessmentAssessing {
		return ReassessmentCycle{}, errors.New("reassessment proposal outside its assessing state")
	}
	if c.Decision != ReassessmentOptionReassess {
		return ReassessmentCycle{}, errors.New("reassessment proposal needs its reassess decision")
	}
	if !ValidReassessmentProposal(option, probability, confidence) {
		return ReassessmentCycle{}, errors.New("reassessment proposal is not acceptable")
	}
	c.Proposal = option
	return c, nil
}

// AdvanceTo moves the cycle along its pure state joint. Release on
// continue additionally needs FW31 freshness checks; release on accept
// additionally needs confirmed graph effects (or a confirmed no-change
// report) plus residual prior-adoption and pending-event ordering
// checks. FW31/FW32 enforce those before calling.
func (c ReassessmentCycle) AdvanceTo(next string) error {
	allowed, ok := map[string][]string{
		ReassessmentCollecting:  {ReassessmentDeciding, ReassessmentAttention},
		ReassessmentDeciding:    {ReassessmentReassessing, ReassessmentReleased, ReassessmentAttention},
		ReassessmentReassessing: {ReassessmentAssessing, ReassessmentAttention},
		ReassessmentAssessing:   {ReassessmentApplying, ReassessmentReleased, ReassessmentAttention},
		ReassessmentApplying:    {ReassessmentReleased, ReassessmentAttention},
	}[c.State]
	if !ok {
		return errors.New("terminal reassessment cycle cannot advance")
	}
	found := false
	for _, state := range allowed {
		if state == next {
			found = true
			break
		}
	}
	if !found {
		return errors.New("reassessment transition outside its cycle")
	}
	switch next {
	case ReassessmentReassessing:
		if c.Decision != ReassessmentOptionReassess {
			return errors.New("reassessment run needs its reassess decision")
		}
		if len(c.Ledger.CoderRuns) != 1 {
			return errors.New("reassessment run needs its admitted coder run")
		}
	case ReassessmentApplying:
		if c.Proposal != ReassessmentOptionAccept {
			return errors.New("reassessment apply needs its accepted proposal")
		}
	case ReassessmentReleased:
		if c.Decision == ReassessmentOptionReassess && c.Proposal != ReassessmentOptionAccept {
			return errors.New("reassessment release needs its accepted proposal")
		}
		if c.Decision == "" {
			return errors.New("reassessment release needs its decision")
		}
	}
	return nil
}

// Supersede retires an attention cycle through its explicit authorized
// Retry. Only the expected prior cycle matches; the successor preserves
// all earlier receipts and committed native effects as history.
func (c ReassessmentCycle) Supersede(retry ReassessmentRetry) (ReassessmentCycle, error) {
	if err := retry.Validate(); err != nil {
		return ReassessmentCycle{}, err
	}
	if c.State != ReassessmentAttention {
		return ReassessmentCycle{}, errors.New("only an attention cycle waits for retry")
	}
	if retry.EventID != c.EventID || retry.ExpectedPriorCycle != c.CycleID {
		return ReassessmentCycle{}, errors.New("reassessment retry names another cycle")
	}
	c.State = ReassessmentSuperseded
	c.Successor = retry.SuccessorCycleID
	return c, nil
}

// BindsAssignment is the T33 admission joint FW32 enforces: the
// assignment carries this cycle's completion and inventory binding on a
// fresh attempt root with no parent and no publication. The completed
// issue root cannot reopen: any parented or publishing assignment
// refuses through Assignment.Validate.
func (c ReassessmentCycle) BindsAssignment(a Assignment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.Subject.Kind != SubjectCompletion || a.Subject.Completion == nil {
		return errors.New("reassessment assignment lacks its completion subject")
	}
	if a.Subject.Completion.CompletionID != c.EventID || a.Subject.Completion.CycleID != c.CycleID {
		return errors.New("reassessment assignment does not match its cycle")
	}
	return nil
}

// AdmitReassessmentCoder admits the cycle's single completion-subject
// coder (T35) against the exact proposed recorded cycle BEFORE packet
// commit. A continue decision launches no agent; a parented or
// publishing assignment never reopens the completed root; the wrong
// subject, role or phase refuses. The recorded cycle itself is gated:
// it must validate, sit in the reassessing state, carry the reassess
// decision matching the caller's, name the assignment's repository, and
// record exactly the assignment's paid run. Admission is fresh assigned
// work only: first attempt, sole run in history, assigned stage.
func AdmitReassessmentCoder(cycle ReassessmentCycle, a Assignment, decision string) error {
	if decision != ReassessmentOptionReassess {
		return errors.New("continue launches no reassessment agent")
	}
	if err := cycle.Validate(); err != nil {
		return err
	}
	if cycle.State != ReassessmentReassessing {
		return errors.New("reassessment cycle is not in its reassessing state")
	}
	if cycle.Decision != decision || cycle.Decision != ReassessmentOptionReassess {
		return errors.New("reassessment cycle lacks its reassess decision")
	}
	if len(cycle.Ledger.CoderRuns) != 1 {
		return errors.New("reassessment run needs its admitted coder run")
	}
	if err := cycle.BindsAssignment(a); err != nil {
		return err
	}
	if cycle.Repository != a.Repository {
		return errors.New("reassessment assignment does not match its cycle repository")
	}
	if cycle.Ledger.CoderRuns[0] != a.Run {
		return errors.New("reassessment run does not match its admitted coder run")
	}
	if a.Attempts != 1 || len(a.RunHistory) != 1 || a.RunHistory[0] != a.Run {
		return errors.New("reassessment coder needs its fresh assigned run")
	}
	if a.Stage != AssignmentAssigned {
		return errors.New("reassessment coder needs its fresh assigned work")
	}
	if a.Phase != project.ExecutionReassess || !ValidPhaseRole(a.Phase, a.Role) {
		return errors.New("reassessment coder needs its reassess coder binding")
	}
	return nil
}

// ReassessmentIntentChange proposes the full replacement D02 graph for
// one intent: tasks are decision-keyed graph tasks, never native issue
// IDs. The delta stays inside idle factory-generated tasks within their
// existing accepted product scope; protected work never changes here.
type ReassessmentIntentChange struct {
	Tasks            []IntentTask `json:"tasks"`
	IntentID         string       `json:"intent_id"`
	ExpectedRevision string       `json:"expected_revision"`
	ScopeDigest      string       `json:"scope_digest"`
	Reason           string       `json:"reason"`
}

func validateReassessmentTask(task IntentTask, seen map[string]IntentTask) error {
	if !project.ValidDecisionID(task.Key) || !intentText(task.Objective, MaxPlanTextBytes) ||
		len(task.Outcomes) == 0 || len(task.Outcomes) > 20 || len(task.IdeaIDs) == 0 {
		return errors.New("invalid reassessment task")
	}
	if _, dup := seen[task.Key]; dup {
		return errors.New("duplicate reassessment task")
	}
	for _, outcome := range task.Outcomes {
		if !intentText(outcome, MaxPlanTextBytes) {
			return errors.New("invalid reassessment task outcome")
		}
	}
	for _, id := range task.IdeaIDs {
		if !project.ValidDecisionID(id) {
			return errors.New("reassessment task references an invalid product choice")
		}
	}
	return nil
}

type reassessmentGraph struct {
	tasks   map[string]IntentTask
	visited map[string]bool
	active  map[string]bool
}

func (g *reassessmentGraph) visit(key string) error {
	if g.active[key] {
		return errors.New("cyclic reassessment dependencies")
	}
	if g.visited[key] {
		return nil
	}
	task, ok := g.tasks[key]
	if !ok {
		return errors.New("unknown reassessment dependency")
	}
	g.active[key] = true
	edge := map[string]bool{}
	for _, dep := range task.DependsOn {
		if edge[dep] {
			return errors.New("duplicate reassessment dependency")
		}
		edge[dep] = true
		if err := g.visit(dep); err != nil {
			return err
		}
	}
	g.active[key] = false
	g.visited[key] = true
	return nil
}

func validateReassessmentTasks(tasks []IntentTask) error {
	if len(tasks) == 0 || len(tasks) > MaxPlanTasks {
		return errors.New("reassessment task count outside its bound")
	}
	seen := make(map[string]IntentTask, len(tasks))
	for _, task := range tasks {
		if err := validateReassessmentTask(task, seen); err != nil {
			return err
		}
		seen[task.Key] = task
	}
	g := &reassessmentGraph{tasks: seen, visited: map[string]bool{}, active: map[string]bool{}}
	for key := range seen {
		if err := g.visit(key); err != nil {
			return err
		}
	}
	return nil
}

func (ch ReassessmentIntentChange) validate() error {
	if !project.ValidDecisionID(ch.IntentID) || !project.ValidDecisionID(ch.ExpectedRevision) {
		return errors.New("invalid reassessment change identity")
	}
	if !ValidDigest(ch.ScopeDigest) {
		return errors.New("invalid reassessment change scope digest")
	}
	if !intentText(ch.Reason, MaxPlanTextBytes) {
		return errors.New("invalid reassessment change reason")
	}
	return validateReassessmentTasks(ch.Tasks)
}

// ReassessmentCoverage accounts one affected accepted product outcome:
// the outcome names an effective accepted product-idea ID in the
// intent's exact expected revision, never a task-outcome ordinal. FW32
// additionally checks the revision's effective ideas, the delivered
// acceptances' verified completion and their historic task bindings.
type ReassessmentCoverage struct {
	TaskKeys               []string `json:"task_keys"`
	DeliveredAcceptanceIDs []string `json:"delivered_acceptance_ids"`
	IntentID               string   `json:"intent_id"`
	OutcomeID              string   `json:"outcome_id"`
}

func (v ReassessmentCoverage) validate() error {
	return v.validateCoverage(false)
}

func (v ReassessmentCoverage) validateCoverage(repair bool) error {
	if !project.ValidDecisionID(v.IntentID) || !project.ValidDecisionID(v.OutcomeID) {
		return errors.New("invalid reassessment coverage identity")
	}
	if len(v.TaskKeys) == 0 || len(v.TaskKeys) > MaxPlanTasks {
		return errors.New("reassessment coverage task keys outside their bound")
	}
	seen := make(map[string]bool, len(v.TaskKeys))
	for _, key := range v.TaskKeys {
		if !project.ValidDecisionID(key) || seen[key] {
			return errors.New("invalid reassessment coverage task key")
		}
		seen[key] = true
	}
	if (!repair && len(v.DeliveredAcceptanceIDs) == 0) || len(v.DeliveredAcceptanceIDs) > MaxPlanTasks {
		return errors.New("reassessment coverage acceptances outside their bound")
	}
	for _, id := range v.DeliveredAcceptanceIDs {
		if !project.ValidDecisionID(id) {
			return errors.New("invalid reassessment delivered acceptance")
		}
	}
	return nil
}

// ReassessmentAttentionEntry keeps the event held and routes necessary changes
// outside current authority, or unresolved facts, to current
// product/native issue actions. It never silently drops work.
type ReassessmentAttentionEntry struct {
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Reason       string   `json:"reason"`
	IssueIndex   int64    `json:"issue_index"`
}

func (a ReassessmentAttentionEntry) validate() error {
	if a.IssueIndex < 1 {
		return errors.New("invalid reassessment attention issue")
	}
	if !intentText(a.Reason, MaxPlanTextBytes) {
		return errors.New("invalid reassessment attention reason")
	}
	if len(a.EvidenceRefs) > MaxInspectedRefs {
		return errors.New("reassessment attention evidence outside its bound")
	}
	for _, ref := range a.EvidenceRefs {
		if ref == "" || len(ref) > MaxPlanPathBytes || strings.ContainsRune(ref, 0) {
			return errors.New("invalid reassessment attention evidence")
		}
	}
	return nil
}

// ReassessmentReport is the strict version-2 reassessment payload: exact
// assignment bindings, D03-shaped inspected sources, a bounded summary,
// the retained issue set, at most 64 coherent intent changes, exact
// outcome coverage and bounded attention. An empty change set is valid,
// including after Jev requested investigation: inspection may establish
// that no changes are needed.
type ReassessmentReport struct {
	Inspected            []InspectedSource            `json:"inspected_refs"`
	IntentChanges        []ReassessmentIntentChange   `json:"intent_changes"`
	Coverage             []ReassessmentCoverage       `json:"coverage"`
	Attention            []ReassessmentAttentionEntry `json:"attention"`
	RetainedIssueIndexes []int64                      `json:"retained_issue_indexes"`
	Summary              string                       `json:"summary"`
	InventoryDigest      string                       `json:"inventory_digest"`
	SourceCommit         string                       `json:"source_commit"`
	PreparationDigest    string                       `json:"preparation_digest"`
	LocalInputDigest     string                       `json:"local_input_digest"`
	CompletionID         string                       `json:"completion_id"`
	CycleID              string                       `json:"cycle_id"`
}

func (r ReassessmentReport) validateScope() error {
	if !ValidID(r.CompletionID) || !ValidID(r.CycleID) {
		return errors.New("invalid reassessment report identity")
	}
	if !ValidDigest(r.InventoryDigest) || !ValidDigest(r.PreparationDigest) || !ValidDigest(r.LocalInputDigest) {
		return errors.New("invalid reassessment report digest")
	}
	if !ValidCommit(r.SourceCommit) {
		return errors.New("invalid reassessment report source")
	}
	return nil
}

func (r ReassessmentReport) validateInspected() error {
	if len(r.Inspected) > MaxInspectedRefs {
		return errors.New("reassessment report inspects too many sources")
	}
	for _, ref := range r.Inspected {
		if !validOwnershipEntry(ref.Path) || strings.HasSuffix(ref.Path, "/") {
			return errors.New("reassessment inspected source is not an exact file")
		}
		if !ValidCommit(ref.Commit) {
			return errors.New("reassessment inspected source lacks its commit")
		}
		if ref.Evidence == "" || len(ref.Evidence) > MaxPlanTextBytes {
			return errors.New("reassessment inspected evidence outside its bound")
		}
	}
	return nil
}

func (r ReassessmentReport) validateTasks() error {
	if r.Summary == "" || len(r.Summary) > MaxReassessmentSummaryBytes {
		return errors.New("reassessment summary outside its bound")
	}
	if !sortedUniqueIndexes(r.RetainedIssueIndexes) {
		return errors.New("reassessment retained issues are not sorted unique")
	}
	for _, index := range r.RetainedIssueIndexes {
		if index < 1 {
			return errors.New("invalid reassessment retained issue")
		}
	}
	if len(r.IntentChanges) > MaxReassessmentIntentChanges {
		return errors.New("reassessment intent changes exceed their bound")
	}
	prior := ""
	for _, change := range r.IntentChanges {
		if change.IntentID <= prior {
			return errors.New("reassessment intent changes are not sorted unique")
		}
		prior = change.IntentID
		if err := change.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r ReassessmentReport) validateCoverage() error {
	if len(r.Coverage) > MaxReassessmentCoverage {
		return errors.New("reassessment coverage exceeds its bound")
	}
	covered := make(map[string]bool, len(r.Coverage))
	for _, entry := range r.Coverage {
		if err := entry.validate(); err != nil {
			return err
		}
		key := entry.IntentID + "\x00" + entry.OutcomeID
		if covered[key] {
			return errors.New("reassessment outcome covered twice")
		}
		covered[key] = true
	}
	if err := coverageMatchesChanges(r.IntentChanges, r.Coverage); err != nil {
		return err
	}
	if len(r.Attention) > MaxReassessmentAttention {
		return errors.New("reassessment attention exceeds its bound")
	}
	for _, item := range r.Attention {
		if err := item.validate(); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxPhaseResultBytes {
		return errors.New("encoded reassessment report exceeds its bound")
	}
	return nil
}

func (r ReassessmentReport) Validate() error {
	if err := r.validateScope(); err != nil {
		return err
	}
	if err := r.validateInspected(); err != nil {
		return err
	}
	if err := r.validateTasks(); err != nil {
		return err
	}
	return r.validateCoverage()
}

// coverageMatchesChanges enforces exact outcome accounting: every
// affected effective idea (every idea named by any proposed task) is
// covered exactly once, proposed task keys carrying that idea include the
// outcome, and coverage names no intent without a proposed change.
func coverageMatchesChanges(changes []ReassessmentIntentChange, coverage []ReassessmentCoverage) error {
	byIntent := make(map[string]ReassessmentIntentChange, len(changes))
	affected := make(map[string]bool)
	for _, change := range changes {
		byIntent[change.IntentID] = change
		for _, task := range change.Tasks {
			for _, id := range task.IdeaIDs {
				affected[change.IntentID+"\x00"+id] = true
			}
		}
	}
	if len(affected) == 0 && len(coverage) != 0 {
		return errors.New("reassessment coverage without affected outcomes")
	}
	if len(coverage) != len(affected) {
		return errors.New("reassessment coverage misses affected outcomes")
	}
	for _, entry := range coverage {
		key := entry.IntentID + "\x00" + entry.OutcomeID
		if !affected[key] {
			return errors.New("reassessment coverage names an unaffected outcome")
		}
		change := byIntent[entry.IntentID]
		keys := make(map[string][]string, len(change.Tasks))
		for _, task := range change.Tasks {
			keys[task.Key] = task.IdeaIDs
		}
		includes := false
		for _, taskKey := range entry.TaskKeys {
			ideas, ok := keys[taskKey]
			if !ok {
				return errors.New("reassessment coverage names an unproposed task")
			}
			for _, id := range ideas {
				if id == entry.OutcomeID {
					includes = true
					break
				}
			}
		}
		if !includes {
			return errors.New("reassessment coverage task misses its outcome idea")
		}
	}
	return nil
}

func sortedUniqueIndexes(indexes []int64) bool {
	for i, index := range indexes {
		if i > 0 && indexes[i-1] >= index {
			return false
		}
	}
	return true
}

// AccountingHolds enforces the exactly-once inventory rule: with a
// sealed inventory of exact native issue indexes, every issue appears exactly once in the
// retained set or the deterministically derived changed/retired set from
// the proposed graphs, and every referenced issue belongs to that sealed
// inventory or its explicit delivered context. FW32 derives changed from
// the applied native packets; this joint checks the accounting.
func (r ReassessmentReport) AccountingHolds(inventory []int64, changed []int64) error {
	return repairAccountingHolds(r.RetainedIssueIndexes, r.Attention, inventory, changed)
}

func (r StallRepairReport) AccountingHolds(inventory []int64, changed []int64) error {
	return repairAccountingHolds(r.RetainedIssueIndexes, r.Attention, inventory, changed)
}

func (r DeliveryRepairReport) AccountingHolds(inventory []int64, changed []int64) error {
	return repairAccountingHolds(r.RetainedIssueIndexes, r.Attention, inventory, changed)
}

func repairAccountingHolds(retained []int64, attention []ReassessmentAttentionEntry, inventory []int64, changed []int64) error {
	if len(inventory) > MaxReassessmentIssues || !sortedUniqueIndexes(inventory) {
		return errors.New("invalid reassessment inventory size")
	}
	for _, index := range inventory {
		if index < 1 {
			return errors.New("invalid reassessment inventory issue")
		}
	}
	if !sortedUniqueIndexes(changed) {
		return errors.New("reassessment changed issues are not sorted unique")
	}
	if len(retained)+len(changed) != len(inventory) {
		return errors.New("reassessment accounting misses inventory issues")
	}
	merged := make([]int64, 0, len(inventory))
	merged = append(merged, retained...)
	merged = append(merged, changed...)
	sort.Slice(merged, func(i, j int) bool { return merged[i] < merged[j] })
	for i, index := range merged {
		if index != inventory[i] {
			return errors.New("reassessment accounting misses inventory issues")
		}
	}
	for _, item := range attention {
		i := sort.Search(len(inventory), func(i int) bool { return inventory[i] >= item.IssueIndex })
		if i == len(inventory) || inventory[i] != item.IssueIndex {
			return errors.New("reassessment attention names an unsealed issue")
		}
	}
	return nil
}

// IntentDigest pairs one intent with the digest of its proposed
// successor revision or native packet.
type IntentDigest struct {
	IntentID string `json:"intent_id"`
	Digest   string `json:"digest"`
}

// ReassessmentValidationReport is the bounded assessment digest the
// proposal judgment sees alongside the proposal: exact digests and
// sorted unique index lists, no freeform narrative.
type ReassessmentValidationReport struct {
	ScopeDigests          []IntentDigest `json:"scope_digests"`
	PacketDigests         []IntentDigest `json:"packet_digests"`
	MutableIssueIndexes   []int64        `json:"mutable_issue_indexes"`
	ProtectedIssueIndexes []int64        `json:"protected_issue_indexes"`
	InventoryDigest       string         `json:"inventory_digest"`
	ProposalDigest        string         `json:"proposal_digest"`
	CoverageDigest        string         `json:"coverage_digest"`
}

func (v ReassessmentValidationReport) Validate() error {
	if !ValidDigest(v.InventoryDigest) || !ValidDigest(v.ProposalDigest) || !ValidDigest(v.CoverageDigest) {
		return errors.New("invalid reassessment validation digest")
	}
	prior := ""
	for _, pair := range v.ScopeDigests {
		if !project.ValidDecisionID(pair.IntentID) || !ValidDigest(pair.Digest) {
			return errors.New("invalid reassessment scope digest")
		}
		if pair.IntentID <= prior {
			return errors.New("reassessment scope digests are not sorted unique")
		}
		prior = pair.IntentID
	}
	prior = ""
	for _, pair := range v.PacketDigests {
		if !project.ValidDecisionID(pair.IntentID) || !ValidDigest(pair.Digest) {
			return errors.New("invalid reassessment packet digest")
		}
		if pair.IntentID <= prior {
			return errors.New("reassessment packet digests are not sorted unique")
		}
		prior = pair.IntentID
	}
	if !sortedUniqueIndexes(v.MutableIssueIndexes) || !sortedUniqueIndexes(v.ProtectedIssueIndexes) {
		return errors.New("reassessment validation issues are not sorted unique")
	}
	if len(v.MutableIssueIndexes) > MaxReassessmentIssues || len(v.ProtectedIssueIndexes) > MaxReassessmentIssues {
		return errors.New("reassessment validation issues exceed their bound")
	}
	encoded, err := json.Marshal(v)
	if err != nil || len(encoded) > MaxReassessmentValidationBytes {
		return errors.New("encoded reassessment validation exceeds its bound")
	}
	return nil
}

// ReassessmentView is the frozen UI read-model contract: completion,
// gate decision, assessment progress, exact proposal effects/receipts
// and the wait/retry reason. Read endpoints spend nothing and carry
// digests, never evidence bytes. FW23 binds this to the plan/work API
// and Work/Spaces components.
type ReassessmentView struct {
	ProposalDigest  string   `json:"proposal_digest,omitempty"`
	InventoryDigest string   `json:"inventory_digest,omitempty"`
	WaitReason      string   `json:"wait_reason,omitempty"`
	ReceiptDigests  []string `json:"receipt_digests,omitempty"`
	CompletionID    string   `json:"completion_id"`
	CycleID         string   `json:"cycle_id"`
	State           string   `json:"state"`
	Decision        string   `json:"decision,omitempty"`
	Proposal        string   `json:"proposal,omitempty"`
	Repository      int64    `json:"repository,string"`
	Generation      int64    `json:"generation"`
	Retryable       bool     `json:"retryable"`
}

func (v ReassessmentView) Validate() error {
	if !ValidID(v.CompletionID) || !ValidID(v.CycleID) {
		return errors.New("invalid reassessment view identity")
	}
	if v.Repository <= 0 || v.Generation < 1 {
		return errors.New("invalid reassessment view scope or generation")
	}
	if !validReassessmentState(v.State) {
		return errors.New("invalid reassessment view state")
	}
	switch v.Decision {
	case "", ReassessmentOptionReassess, ReassessmentOptionContinue:
	default:
		return errors.New("invalid reassessment view decision")
	}
	switch v.Proposal {
	case "", ReassessmentOptionAccept, ReassessmentOptionNeedsAttention:
	default:
		return errors.New("invalid reassessment view proposal")
	}
	for _, digest := range append(append([]string(nil), v.ReceiptDigests...), v.ProposalDigest, v.InventoryDigest) {
		if digest == "" {
			continue
		}
		if !ValidDigest(digest) {
			return errors.New("invalid reassessment view digest")
		}
	}
	if v.WaitReason == "" && v.State == ReassessmentAttention {
		return errors.New("reassessment wait needs its reason")
	}
	if len(v.WaitReason) > MaxReassessmentReasonChars || strings.ContainsRune(v.WaitReason, 0) ||
		(v.WaitReason != "" && strings.TrimSpace(v.WaitReason) == "") {
		return errors.New("invalid reassessment wait reason")
	}
	if v.Retryable && v.State != ReassessmentAttention {
		return errors.New("only an attention cycle waits for retry")
	}
	return nil
}
