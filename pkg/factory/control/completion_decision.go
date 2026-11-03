package control

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// ReassessmentDecisionOutcome labels funded completion-decision
// receipts. Success carries this fixed outcome; failure carries
// unavailable with the charged call retained, exactly like the funded
// delivery path.
const ReassessmentDecisionOutcome = "reassessment_decided"

// reassessmentDecisionRubric asks whether the delivered change or the
// current issue set warrants revising scope allocation, decomposition,
// acceptance details or dependencies before selecting another issue.
// Jev receives the evidence; it does not investigate source or write a
// plan.
const reassessmentDecisionRubric = "Does the delivered change or the current issue set warrant revising scope allocation, decomposition, acceptance details or dependencies before selecting another issue? Select reassess to investigate the full inventory with one coder run, or continue to release this completion and resume fresh priority selection."

// ReassessmentDecisionState is the exact Choice input for one
// completion decision. It embeds the cycle, the distinct completion
// ID, the sealed completion and local bindings, and the complete
// sealed issue bodies, so Jev assesses substance — the delivered
// change and the current issue set with their titles, bodies and
// dependencies — not hashes. The shared judgment cache reuses a
// receipt only while its purpose, rubric, model and evidence still
// match. Each completion gets its own decision even when inventories
// are identical or empty.
type ReassessmentDecisionState struct {
	Completion          factory.CompletionEvent           `json:"completion"`
	Local               factory.CompletionLocal           `json:"local"`
	Evidence            []factory.CompletionIssueEvidence `json:"evidence"`
	Model               string                            `json:"model"`
	CompletionID        string                            `json:"completion_id"`
	CycleID             string                            `json:"cycle_id"`
	InventoryDigest     string                            `json:"inventory_digest"`
	EvidenceFingerprint string                            `json:"evidence_fingerprint"`
	Repository          int64                             `json:"repository,string"`
	Generation          int64                             `json:"generation"`
	Issues              int                               `json:"issues"`
}

// errReassessmentDecisionOversize reports a decision request that
// exceeds the pre-spend bound. The seal holds instead: evidence is
// never truncated to fit, and nothing is charged.
var errReassessmentDecisionOversize = errors.New("reassessment decision exceeds its encoded bound")

// DecisionStateFromRecord builds the exact D13 decision input from one
// sealed cycle: the persisted completion, local bindings and issue
// bodies. It refuses unsealed, bodiless, tampered or materially
// incomplete records instead of asking Jev to judge hashes, and refuses
// oversize requests before any funding is reserved. Every refusal spends
// nothing: no reservation is taken and no provider is consulted.
func DecisionStateFromRecord(record store.ReassessmentRecord) (ReassessmentDecisionState, error) {
	if !factory.ValidDigest(record.InventoryDigest) || !factory.ValidDigest(record.EvidenceFingerprint) {
		return ReassessmentDecisionState{}, errors.New("reassessment decision lacks its sealed evidence")
	}
	if record.Evidence == nil && record.EvidenceIssues != 0 {
		return ReassessmentDecisionState{}, errors.New("reassessment decision lacks its sealed bodies")
	}
	if len(record.Evidence) != record.EvidenceIssues {
		return ReassessmentDecisionState{}, errors.New("reassessment decision bodies differ from their sealed count")
	}
	for _, body := range record.Evidence {
		if err := body.Validate(); err != nil {
			return ReassessmentDecisionState{}, errors.New("reassessment decision bodies fail their sealed validation")
		}
	}
	if err := record.Local.Validate(); err != nil {
		return ReassessmentDecisionState{}, errors.New("reassessment decision lacks its sealed material")
	}
	if err := record.Completion.Validate(); err != nil {
		return ReassessmentDecisionState{}, errors.New("reassessment decision lacks its sealed completion")
	}
	if err := record.Cycle.Validate(); err != nil {
		return ReassessmentDecisionState{}, err
	}
	if record.Completion.Repository != record.Cycle.Repository || record.Completion.Generation != record.Cycle.Generation {
		return ReassessmentDecisionState{}, errors.New("reassessment decision completion does not match its cycle")
	}
	// The sealed digest must be exactly the bodies it seals: refold
	// the persisted bodies through the same canonical fold and refuse
	// any mutation under an unchanged digest.
	recomputed, err := resealInventoryDigest(record)
	if err != nil || recomputed != record.InventoryDigest {
		return ReassessmentDecisionState{}, errors.New("reassessment decision bodies differ from their sealed digest")
	}
	state := ReassessmentDecisionState{
		Completion: record.Completion, Local: record.Local,
		Evidence:     append([]factory.CompletionIssueEvidence{}, record.Evidence...),
		CompletionID: record.Cycle.EventID, CycleID: record.Cycle.CycleID,
		InventoryDigest: record.InventoryDigest, EvidenceFingerprint: record.EvidenceFingerprint,
		Repository: record.Cycle.Repository, Generation: record.Cycle.Generation,
		Issues: len(record.Evidence),
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return ReassessmentDecisionState{}, err
	}
	if len(raw) > factory.MaxReassessmentDecisionBytes {
		return ReassessmentDecisionState{}, errReassessmentDecisionOversize
	}
	return state, nil
}

// resealInventoryDigest refolds the persisted sealed bodies through
// the same canonical fold the collector sealed: canonical body bytes in
// ascending index order under the sealed repository, generation and
// revision. Any persisted mutation — edited substance, reordered
// indexes or a substituted digest — folds differently and refuses.
func resealInventoryDigest(record store.ReassessmentRecord) (string, error) {
	// The persisted sequence is authoritative: require strictly
	// increasing positive indexes while reading it. A reordered or
	// duplicated persisted sequence refuses before any fold, provider
	// call, or reservation — never sort a malformed sequence into
	// admission.
	issues := make([]NativeInventoryIssue, 0, len(record.Evidence))
	indexes := make([]int64, 0, len(record.Evidence))
	var prior int64
	for i, body := range record.Evidence {
		if body.Index <= 0 || (i > 0 && body.Index <= prior) {
			return "", errors.New("resealed inventory bodies are not ordered")
		}
		prior = body.Index
		raw, err := body.CanonicalData()
		if err != nil {
			return "", err
		}
		issues = append(issues, NativeInventoryIssue{Index: body.Index, Data: raw})
		indexes = append(indexes, body.Index)
	}
	var running [32]byte
	running = foldInventoryPage(running, record.Cycle.Repository, record.Cycle.Generation, issues)
	return sealInventoryDigest(record.Cycle.Repository, record.Cycle.Generation, record.EvidenceRevision, running, indexes), nil
}

// reassessmentDecisionOptions offers exactly reassess or continue. No
// other option is actionable.
func reassessmentDecisionOptions() map[string]any {
	return map[string]any{
		factory.ReassessmentOptionReassess: map[string]any{"effect": "admit the single bounded coder run over the sealed inventory"},
		factory.ReassessmentOptionContinue: map[string]any{"effect": "release this event after freshness checks with no agent and no edits"},
	}
}

// applyReassessmentAnswer files one decision answer. Only a valid
// selected option at or above the confidence bound is actionable; low
// confidence, another option or a malformed answer holds the event
// with attention. A continue is never inferred.
func applyReassessmentAnswer(cycle factory.ReassessmentCycle, answer factory.PriorityAnswer) (factory.ReassessmentCycle, error) {
	return cycle.RecordDecision(answer.Selected, answer.Confidence)
}

// DecideReassessment consults Jev for one deciding cycle and files the
// outcome. Cached completed receipts bypass reservation through the
// shared judgment read path. Outcomes:
//
//   - a valid reassess or continue decision is recorded on the cycle;
//   - low confidence or a malformed recorded answer holds with
//     attention instead of inferring continue;
//   - a failed call records nothing and defers without state change so
//     the next drive asks again.
//
// No automatic extra agent call ever follows: only explicit authorized
// Retry supersedes an attention cycle.

// errReassessmentDeferred reports a decision attempt that changed
// nothing: the call failed or is in flight. The next drive asks again.
var errReassessmentDeferred = errors.New("reassessment decision in flight")

func checkReassessmentDecisionInput(c *Coordinator, record store.ReassessmentRecord, state ReassessmentDecisionState) error {
	if c.Planning == nil || c.Planning.Chooser == nil || c.Store == nil {
		return ErrPriorityUnavailable
	}
	if record.Cycle.State != factory.ReassessmentDeciding {
		return errors.New("reassessment decision outside its deciding state")
	}
	if state.CompletionID != record.Cycle.EventID || state.CycleID != record.Cycle.CycleID {
		return errors.New("reassessment decision does not match its cycle")
	}
	// Material mismatch refuses before any reservation or provider
	// spend: the decision input carries the sealed local and digests of
	// exactly this record. The canonical Equal value comparison fails
	// closed on unvalidated material: pointer-identical locals with
	// invalid bodies never compare equal.
	if err := state.Local.Validate(); err != nil {
		return errors.New("reassessment decision lacks its sealed material")
	}
	if !state.Local.Equal(record.Local) {
		return errors.New("reassessment decision material does not match its cycle")
	}
	if state.InventoryDigest != record.InventoryDigest || state.EvidenceFingerprint != record.EvidenceFingerprint {
		return errors.New("reassessment decision evidence does not match its cycle")
	}
	return nil
}

func (c *Coordinator) recoverReassessmentDecision(ctx context.Context, record store.ReassessmentRecord, state ReassessmentDecisionState, adviceErr error) (store.ReassessmentRecord, error) {
	// A completed receipt never hides behind this error: apply it, or
	// hold with attention when it is not actionable. A failed call
	// defers so the next drive asks again. Otherwise a full ledger
	// attends so explicit Retry can reconcile. Nothing here infers
	// continue.
	if prior, readErr := c.Store.PriorityJudgment(ctx, fingerprintOf(state)); readErr == nil && len(prior.Calls) == 1 {
		updated, applyErr := applyReassessmentAnswer(record.Cycle, prior.Calls[0].Answer)
		if applyErr != nil {
			record.WaitReason = "decision_not_actionable"
			return record, applyErr
		}
		record.Cycle = updated
		return record, nil
	}
	if errors.Is(adviceErr, ErrConsultationFailed) {
		return record, errReassessmentDeferred
	}
	if len(record.Cycle.Ledger.DecisionCalls) >= factory.MaxReassessmentDecisionCalls {
		record.WaitReason = "decision_unavailable"
		return record, adviceErr
	}
	return record, errReassessmentDeferred
}

func (c *Coordinator) DecideReassessment(ctx context.Context, record store.ReassessmentRecord, state ReassessmentDecisionState) (store.ReassessmentRecord, error) {
	if err := checkReassessmentDecisionInput(c, record, state); err != nil {
		return store.ReassessmentRecord{}, err
	}
	cycleID, decision := record.Cycle.CycleID, record.Cycle.Decision
	admit := func(ctx context.Context, fingerprint string) (bool, error) {
		return c.Store.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding, fingerprint[:32], factory.ReassessmentDecisionPurpose, decision, fingerprint)
	}
	answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{
		Purpose: factory.JudgmentPurpose(factory.ReassessmentDecisionPurpose),
		State:   state, Question: reassessmentDecisionRubric, Options: reassessmentDecisionOptions(),
		Outcome: ReassessmentDecisionOutcome, Admit: admit,
	})
	if err != nil {
		if errors.Is(err, ErrPriorityUnavailable) {
			return c.recoverReassessmentDecision(ctx, record, state, err)
		}
		return store.ReassessmentRecord{}, err
	}
	updated, err := applyReassessmentAnswer(record.Cycle, answer)
	if err != nil {
		record.WaitReason = "decision_not_actionable"
		return record, err
	}
	record.Cycle = updated
	record.WaitReason = ""
	return record, nil
}

// fingerprintOf binds the decision state to its exact cache identity:
// the same inputs AdvisePurpose fingerprints, so the driver re-reads
// the receipt the funded call recorded.
func fingerprintOf(state ReassessmentDecisionState) string {
	fingerprint, err := factory.JudgmentFingerprint(factory.JudgmentPurpose(factory.ReassessmentDecisionPurpose), state, reassessmentDecisionRubric, reassessmentDecisionOptions())
	if err != nil {
		return ""
	}
	return fingerprint
}

// DecisionFingerprintFromRecord identifies the funded decision receipt for a
// sealed cycle. Readback uses the same complete state, purpose, rubric and
// options as DecideReassessment; the evidence seal alone is not its cache key.
// Reconstructing this identity never reserves funding or consults a provider.
func DecisionFingerprintFromRecord(record store.ReassessmentRecord) (string, error) {
	state, err := DecisionStateFromRecord(record)
	if err != nil {
		return "", err
	}
	fingerprint := fingerprintOf(state)
	if fingerprint == "" {
		return "", errors.New("reassessment decision fingerprint unavailable")
	}
	return fingerprint, nil
}
