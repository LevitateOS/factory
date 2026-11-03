package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// ErrCompletionFenced reports a dispatch packet for a repository whose
// completion fence is closed: at least one completion event has no
// released cycle yet. New issue investigation/implementation waits with
// reassessment_pending; already-running work, review/correction,
// cancellation and reconciliation are unaffected. Other repositories
// continue normally: a fence never turns the global tournament into a
// wait.
var ErrCompletionFenced = errors.New("repository completion fence is closed")

// ReassessmentRecord is one persisted bounded cycle: the FW30 domain
// cycle plus the sealed completion event, the complete sealed issue
// bodies with their inventory digest and native revision bracket, the
// funded-decision evidence fingerprint, the wait/retry reason for read
// models and the opaque resumable collector progress. Sealed evidence
// never changes: once an inventory digest is recorded, later writes
// must carry it with its bodies unchanged.
type ReassessmentRecord struct {
	Cycle               factory.ReassessmentCycle         `json:"cycle"`
	Completion          factory.CompletionEvent           `json:"completion,omitempty"`
	Local               factory.CompletionLocal           `json:"local,omitempty"`
	Evidence            []factory.CompletionIssueEvidence `json:"evidence"`
	InventoryDigest     string                            `json:"inventory_digest,omitempty"`
	EvidenceFingerprint string                            `json:"evidence_fingerprint,omitempty"`
	ProposalFingerprint string                            `json:"proposal_fingerprint,omitempty"`
	WaitReason          string                            `json:"wait_reason,omitempty"`
	CollectorProgress   json.RawMessage                   `json:"collector_progress,omitempty"`
	AdoptionProgress    json.RawMessage                   `json:"adoption_progress,omitempty"`
	Issue               int64                             `json:"issue,omitempty"`
	EvidenceIssues      int                               `json:"evidence_issues,omitempty"`
	EvidenceRevision    int64                             `json:"evidence_revision,omitempty"`
}

// evidenceBytes totals the canonical sealed body bytes for the bound
// check: the same bytes the collector bounded before the seal.
func (r ReassessmentRecord) evidenceBytes() int {
	total := 0
	for _, body := range r.Evidence {
		raw, err := body.CanonicalData()
		if err != nil {
			return factory.MaxReassessmentEvidenceBytes + 1
		}
		total += len(raw)
	}
	return total
}

// Validate checks the record shape without enforcing liveness: the
// cycle validates itself, digests validate when present, sealed bodies
// validate within their count and byte bounds with indexes matching
// the sealed count, and the wait reason stays a short resumable-step
// name, never evidence bytes.
// validateReassessmentCompletion refuses completions that do not
// match their cycle.
func (r ReassessmentRecord) validateReassessmentCompletion() error {
	if r.Completion != (factory.CompletionEvent{}) {
		if err := r.Completion.Validate(); err != nil {
			return err
		}
		if r.Completion.Repository != r.Cycle.Repository || r.Completion.ID != r.Cycle.EventID ||
			r.Completion.Generation != r.Cycle.Generation {
			return errors.New("reassessment completion does not match its cycle")
		}
	}
	return nil
}

// validateReassessmentMaterial refuses sealed or digest-bearing
// records whose local material diverges. Material binds sealed and
// digest-bearing records: an all-zero Local is allowed only on an
// unsealed record with no InventoryDigest. A nonzero Local must
// validate full material/repo; a sealed record MUST validate Local
// even when the digest alone looks well-formed. Validation precedes
// any Material pointer access.
func (r ReassessmentRecord) validateReassessmentMaterial() error {
	if r.Local.Sealed() || r.InventoryDigest != "" {
		if err := r.Local.Validate(); err != nil {
			return err
		}
		if r.Local.Material.Repository != r.Cycle.Repository {
			return errors.New("reassessment local material names another repository")
		}
	}
	return nil
}

func (r ReassessmentRecord) Validate() error {
	if err := r.Cycle.Validate(); err != nil {
		return err
	}
	if err := r.validateReassessmentCompletion(); err != nil {
		return err
	}
	if err := r.validateReassessmentMaterial(); err != nil {
		return err
	}
	if r.InventoryDigest != "" && !factory.ValidDigest(r.InventoryDigest) {
		return errors.New("invalid sealed inventory digest")
	}
	if r.EvidenceFingerprint != "" && !factory.ValidDigest(r.EvidenceFingerprint) {
		return errors.New("invalid decision evidence fingerprint")
	}
	if r.ProposalFingerprint != "" && !factory.ValidDigest(r.ProposalFingerprint) {
		return errors.New("invalid proposal evidence fingerprint")
	}
	if r.InventoryDigest != "" {
		// A complete empty inventory was previously omitted by JSON's
		// omitempty. Its recorded count, bracket and exact empty-set digest
		// distinguish that representation from missing positive inventory.
		if r.Evidence == nil && (r.EvidenceIssues != 0 || r.EvidenceRevision < 1 ||
			r.InventoryDigest != factory.ReassessmentInventoryDigest(r.Cycle.Repository, r.Cycle.Generation, r.EvidenceRevision, [32]byte{}, nil)) {
			return errors.New("sealed reassessment lacks its issue bodies")
		}
		if len(r.Evidence) != r.EvidenceIssues || len(r.Evidence) > factory.MaxReassessmentIssues {
			return errors.New("sealed reassessment bodies differ from their sealed count")
		}
		if r.EvidenceRevision < 1 {
			return errors.New("sealed reassessment lacks its revision bracket")
		}
		if r.evidenceBytes() > factory.MaxReassessmentEvidenceBytes {
			return errors.New("sealed reassessment bodies exceed their bound")
		}
		for _, body := range r.Evidence {
			if err := body.Validate(); err != nil {
				return err
			}
		}
	}
	if len(r.WaitReason) > factory.MaxReassessmentReasonChars {
		return errors.New("invalid reassessment wait reason")
	}
	if len(r.AdoptionProgress) != 0 && !json.Valid(r.AdoptionProgress) {
		return errors.New("invalid reassessment adoption progress")
	}
	return nil
}

// recordCompletionTx inserts one verified completion and seeds its first
// bounded cycle inside the caller's transaction. The caller is the
// first UpdateMerge recording this merge as merged: the merge already
// carries its confirmed merged commit, PR linkage, acceptance and
// original issue-closing occurrence (Merge.Validate enforces them for
// merged merges), and the same transaction already closed the issue
// allowance and retained the readiness enqueue, so rollback rolls back
// all four together with this insert.
//
// Exact-once: the (publication, occurrence) unique key keeps recovery
// re-observation from recording the event twice; a replay returns
// inserted=false and leaves the first cycle standing. Ordering: the
// repository dispatch-gate row is locked here, the same row packet
// admission locks, so whichever commits first defines the order and the
// fence check inside packet transactions always observes this insert.
func recordCompletionTx(ctx context.Context, tx *sql.Tx, m factory.Merge) (bool, error) {
	if m.Stage != factory.MergeMerged {
		return false, errors.New("completion needs its merged merge")
	}
	if !factory.ValidNativeOccurrence(m.OriginalClosingOccurrence) {
		return false, errors.New("completion needs its original issue closure")
	}
	if !project.ValidDecisionID(m.Acceptance) {
		return false, errors.New("completion needs its acceptance binding")
	}
	if !factory.ValidCommit(m.MergedCommit) {
		return false, errors.New("completion needs its merged commit")
	}
	if _, _, _, err := ensureDispatchGateTx(ctx, tx, m.Repository); err != nil {
		return false, err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0)+1 FROM factory_completions WHERE repository=$1`, m.Repository).Scan(&generation); err != nil {
		return false, err
	}
	event := factory.CompletionEvent{
		PublicationID: m.PublicationID, MergedCommit: m.MergedCommit,
		ClosingOccurrence: m.OriginalClosingOccurrence, AcceptanceID: m.Acceptance,
		ID: factory.NewID(), Repository: m.Repository, Issue: m.Issue, Generation: generation,
	}
	if err := event.Validate(); err != nil {
		return false, err
	}
	eventData, err := json.Marshal(event)
	if err != nil {
		return false, err
	}
	var insertedID string
	err = tx.QueryRowContext(ctx, `INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(publication,occurrence) DO NOTHING RETURNING event`,
		event.Repository, event.Generation, event.ID, event.PublicationID, event.ClosingOccurrence, string(eventData)).Scan(&insertedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	cycle := factory.ReassessmentCycle{
		EventID: event.ID, CycleID: factory.NewID(),
		State: factory.ReassessmentCollecting, Repository: event.Repository, Generation: event.Generation,
	}
	if err := cycle.Validate(); err != nil {
		return false, err
	}
	record := ReassessmentRecord{Cycle: cycle, Completion: event, Issue: m.Issue}
	recordData, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_reassessments(cycle,repository,generation,event,state,data)
		VALUES($1,$2,$3,$4,$5,$6)`, cycle.CycleID, cycle.Repository, cycle.Generation, cycle.EventID, cycle.State, string(recordData)); err != nil {
		return false, err
	}
	return true, nil
}

// checkCompletionFenceTx refuses packet admission while the repository
// fence is closed. The caller holds the repository dispatch-gate row
// lock (the same row completion insertion locks), so this read observes
// every committed completion event. Only released or superseded cycles
// rest the fence: collecting, deciding, reassessing, assessing,
// applying and attention all keep it closed.
func checkCompletionFenceTx(ctx context.Context, tx *sql.Tx, repository int64) error {
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_reassessments
		WHERE repository=$1 AND state NOT IN ('released','superseded'))`, repository).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrCompletionFenced
	}
	return nil
}

// CompletionPending lists the pending completion generations for one
// repository in processing order: every generation whose cycle has not
// reached a terminal state. An empty list means the fence is open.
func (s *Store) CompletionPending(ctx context.Context, repository int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT generation FROM factory_reassessments
		WHERE repository=$1 AND state NOT IN ('released','superseded') ORDER BY generation`, repository)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var pending []int64
	for rows.Next() {
		var generation int64
		if err = rows.Scan(&generation); err != nil {
			_ = rows.Close()
			return nil, err
		}
		pending = append(pending, generation)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	return pending, nil
}

// PendingReassessmentCycles lists every unresolved cycle for one
// repository in generation order so the completion driver processes
// the oldest event first. Released and superseded cycles are history,
// not work.
func (s *Store) PendingReassessmentCycles(ctx context.Context, repository int64) ([]ReassessmentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_reassessments
		WHERE repository=$1 AND state NOT IN ('released','superseded') ORDER BY generation`, repository)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cycles []ReassessmentRecord
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var record ReassessmentRecord
		if err = json.Unmarshal(raw, &record); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = record.Validate(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		cycles = append(cycles, record)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	return cycles, nil
}

// CaptureCompletionLocal reads the local half of D13 sealed evidence
// for one repository in a single repeatable-read transaction: the
// requirement source commit (reused from the projected material's
// current-head authority, no redundant head read), the
// approved-preparation digest, the canonical material digest as the
// accepted-scope digest, and the authority fingerprint, carrying the
// complete validated material alongside. It is the production CompletionLocalCapture
// behind the completion drive: stable while nothing material changes,
// sensitive to source, preparation, scope and authority changes, and
// honest about gaps — a repository without a project or requirement
// source refuses instead of sealing from partial bindings. Private
// per-issue candidate custody and runtime preparation state stay out:
// they are volatile execution detail, not the approved local bindings
// the seal interprets the inventory against.
func (s *Store) CaptureCompletionLocal(ctx context.Context, repository int64) (factory.CompletionLocal, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	material, preparationDigest, err := completionLocalProjectionTx(ctx, tx, repository)
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	// Local.SourceCommit REMAINS the accepted requirement source for
	// this plain slice; the source-qualified entry binds the proven
	// native tip instead. Both share the ONE private capture body.
	local, err := sealCompletionLocal(material, preparationDigest, material.Authority.RequirementSourceCommit)
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.CompletionLocal{}, err
	}
	return local, nil
}

// PendingCompletionRepositories lists every repository with at least
// one unresolved cycle, so the completion driver fences and releases
// per repository without scanning unrelated work.
func (s *Store) PendingCompletionRepositories(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT repository FROM factory_reassessments
		WHERE state NOT IN ('released','superseded') ORDER BY repository`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var repositories []int64
	for rows.Next() {
		var repository int64
		if err = rows.Scan(&repository); err != nil {
			_ = rows.Close()
			return nil, err
		}
		repositories = append(repositories, repository)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	return repositories, nil
}

// EnqueueReadinessRootEvent coalesces one best-effort readiness root
// outside a domain transaction: after a continue release it forces a
// fresh assessment pass instead of reusing a stale snapshot.
func (s *Store) EnqueueReadinessRootEvent(ctx context.Context, repository, issue int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = enqueueReadinessRootEventTx(ctx, tx, repository, issue); err != nil {
		return err
	}
	return tx.Commit()
}

// ReassessmentRecord reads one cycle by its ID for the completion
// driver and explicit Retry validation.
func (s *Store) ReassessmentRecord(ctx context.Context, cycleID string) (ReassessmentRecord, error) {
	var record ReassessmentRecord
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_reassessments WHERE cycle=$1`, cycleID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReassessmentRecord{}, ErrNotFound
		}
		return ReassessmentRecord{}, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return ReassessmentRecord{}, err
	}
	if err := record.Validate(); err != nil {
		return ReassessmentRecord{}, err
	}
	return record, nil
}

// CasReassessmentRecord advances one cycle under an expected-state
// guard: the write lands only when the row still carries the state the
// caller sealed, decided or reconciled against, so concurrent drivers
// serialize instead of overwriting. Sealed evidence is immutable: once
// an inventory digest or evidence fingerprint is recorded, later writes
// must carry it unchanged. Only the state column and data move; rows
// are never deleted.
//
// Paid ledger prefix rule: the locked row is the only admission
// authority for paid decision calls (AdmitReassessmentCall). Incoming
// DecisionCalls must be an ordered prefix of the locked current list: a
// shorter or empty prefix is legitimate stale input and is replaced
// here with a copy of the exact locked list, while an extra,
// unreserved, forked or reordered ID refuses with no write. Only
// The same prefix rule preserves the sole coder run admitted by the
// dispatch packet. Generic cycle writes cannot create or replace it.
// admitCasRecord refuses CAS writes outside the admitted cycle
// identity and completed issue.
func admitCasRecord(cycleID string, record ReassessmentRecord) error {
	if !factory.ValidID(cycleID) {
		return errors.New("invalid reassessment cycle identity")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.Cycle.CycleID != cycleID {
		return errors.New("reassessment record does not match its cycle")
	}
	if record.Issue <= 0 {
		return errors.New("reassessment record lacks its completed issue")
	}
	return nil
}

// checkDecisionCallPrefix refuses CAS writes that introduce unreserved
// calls or fork the locked paid ledger.
func checkDecisionCallPrefix(current, record ReassessmentRecord) error {
	incoming := record.Cycle.Ledger.DecisionCalls
	locked := current.Cycle.Ledger.DecisionCalls
	if len(incoming) > len(locked) {
		return errors.New("reassessment CAS introduces unreserved decision calls")
	}
	for i, id := range incoming {
		if id != locked[i] {
			return errors.New("reassessment CAS forks the locked paid ledger")
		}
	}
	incoming = record.Cycle.Ledger.CoderRuns
	locked = current.Cycle.Ledger.CoderRuns
	if len(incoming) > len(locked) {
		return errors.New("reassessment CAS introduces an unadmitted coder run")
	}
	for i, id := range incoming {
		if id != locked[i] {
			return errors.New("reassessment CAS forks the admitted coder ledger")
		}
	}
	return nil
}

// checkSealedReassessmentImmutable refuses CAS writes that mutate
// sealed evidence, bindings, bodies, completion or the completed
// issue.
func checkSealedReassessmentImmutable(current, record ReassessmentRecord) error {
	if current.EvidenceFingerprint != "" && record.EvidenceFingerprint != current.EvidenceFingerprint {
		return errors.New("sealed reassessment evidence is immutable")
	}
	if current.Local.Sealed() && !record.Local.Equal(current.Local) {
		return errors.New("sealed reassessment local bindings are immutable")
	}
	if current.InventoryDigest != "" {
		if err := checkSealedReassessmentBodies(current, record); err != nil {
			return err
		}
		if record.Completion != current.Completion {
			return errors.New("sealed reassessment completion is immutable")
		}
	}
	if record.Issue != current.Issue {
		return errors.New("reassessment completed issue is immutable")
	}
	return nil
}

// checkSealedReassessmentBodies refuses CAS writes that mutate sealed
// issue bodies.
func checkSealedReassessmentBodies(current, record ReassessmentRecord) error {
	if record.EvidenceRevision != current.EvidenceRevision || record.EvidenceIssues != current.EvidenceIssues ||
		len(record.Evidence) != len(current.Evidence) {
		return errors.New("sealed reassessment bodies are immutable")
	}
	for i := range record.Evidence {
		sealed, err := record.Evidence[i].CanonicalData()
		held, herr := current.Evidence[i].CanonicalData()
		if err != nil || herr != nil || sealed != held {
			return errors.New("sealed reassessment bodies are immutable")
		}
	}
	return nil
}

func (s *Store) CasReassessmentRecord(ctx context.Context, cycleID, expectedState string, record ReassessmentRecord) error {
	if err := admitCasRecord(cycleID, record); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state,data FROM factory_reassessments WHERE cycle=$1 FOR UPDATE`, cycleID).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if state != expectedState {
		return ErrStaleRevision
	}
	var current ReassessmentRecord
	if err = json.Unmarshal(raw, &current); err != nil {
		return err
	}
	// The locked ledger validates as-is: a malformed current row never
	// becomes an empty fallback.
	if err := current.Cycle.Ledger.Validate(); err != nil {
		return err
	}
	if err := checkDecisionCallPrefix(current, record); err != nil {
		return err
	}
	record.Cycle.Ledger.DecisionCalls = append([]string(nil), current.Cycle.Ledger.DecisionCalls...)
	record.Cycle.Ledger.CoderRuns = append([]string(nil), current.Cycle.Ledger.CoderRuns...)
	if record.ProposalFingerprint != "" && record.ProposalFingerprint != current.ProposalFingerprint {
		return errors.New("reassessment CAS changes its reserved proposal fingerprint")
	}
	record.ProposalFingerprint = current.ProposalFingerprint
	if err := record.Validate(); err != nil {
		return err
	}
	if current.InventoryDigest != "" && record.InventoryDigest != current.InventoryDigest {
		return errors.New("sealed reassessment inventory is immutable")
	}
	if err := checkSealedReassessmentImmutable(current, record); err != nil {
		return err
	}
	recordData, err := json.Marshal(record)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_reassessments SET state=$1,data=$2 WHERE cycle=$3 AND state=$4`,
		record.Cycle.State, string(recordData), cycleID, expectedState)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return tx.Commit()
}

// lockedReassessmentRowTx reads one reassessment row under the caller's
// open transaction with a write lock, refusing missing rows and stale
// states.
func lockedReassessmentRowTx(ctx context.Context, tx *sql.Tx, cycleID, expectedState string) (ReassessmentRecord, error) {
	var raw []byte
	var state string
	var record ReassessmentRecord
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_reassessments WHERE cycle=$1 FOR UPDATE`, cycleID).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return record, ErrNotFound
		}
		return record, err
	}
	if state != expectedState {
		return record, ErrStaleRevision
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, err
	}
	return record, nil
}

// AdmitReassessmentCall charges a cycle call once through its existing ledger.
// Callers derive the call ID from the judgment key, so another attempt at the
// same question uses that charge. A full cycle refuses new calls.
func (s *Store) AdmitReassessmentCall(ctx context.Context, cycleID, expectedState, callID, purpose, decision, fingerprint string) (bool, error) {
	if !factory.ValidID(cycleID) || !factory.ValidID(callID) || !factory.ValidDigest(fingerprint) {
		return false, errors.New("invalid reassessment reservation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lockedReassessmentRowTx(ctx, tx, cycleID, expectedState)
	if err != nil {
		return false, err
	}
	if purpose != factory.ReassessmentDecisionPurpose && purpose != factory.ReassessmentProposalPurpose {
		return false, errors.New("invalid reassessment call purpose")
	}
	for _, id := range record.Cycle.Ledger.DecisionCalls {
		if id == callID {
			return true, tx.Commit()
		}
	}
	if len(record.Cycle.Ledger.DecisionCalls) >= factory.MaxReassessmentDecisionCalls {
		return false, nil
	}
	ledger, err := record.Cycle.Ledger.ReserveDecisionCall(callID, purpose, decision)
	if err != nil {
		return false, err
	}
	if purpose == factory.ReassessmentProposalPurpose {
		if record.ProposalFingerprint != "" && record.ProposalFingerprint != fingerprint {
			return false, errors.New("reassessment proposal reservation changed its evidence")
		}
		record.ProposalFingerprint = fingerprint
	}
	record.Cycle.Ledger = ledger
	recordData, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_reassessments SET data=$1 WHERE cycle=$2 AND state=$3`, string(recordData), cycleID, expectedState); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// StartReassessmentRetry creates the one successor cycle for an
// attention cycle through its explicit authorized Retry. The prior
// cycle must still carry the expected attention state with no
// successor yet; command replay reuses the recorded successor instead
// of creating another. The successor preserves all earlier receipts
// and committed native effects as history via its retry lineage. Only
// explicit authorized Retry supersedes: no poll, input edit or policy
// edit creates a successor.
func (s *Store) StartReassessmentRetry(ctx context.Context, retry factory.ReassessmentRetry) (string, error) {
	if err := retry.Validate(); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	successor, err := startReassessmentRetryTx(ctx, tx, retry, 0)
	if err != nil {
		return "", err
	}
	return successor, tx.Commit()
}

func startReassessmentRetryTx(ctx context.Context, tx *sql.Tx, retry factory.ReassessmentRetry, repository int64) (string, error) {
	var raw []byte
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_reassessments WHERE cycle=$1 FOR UPDATE`, retry.ExpectedPriorCycle).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	var prior ReassessmentRecord
	if err := json.Unmarshal(raw, &prior); err != nil {
		return "", err
	}
	if repository > 0 && prior.Cycle.Repository != repository {
		return "", ErrCompletionRetryIneligible
	}
	if prior.Cycle.EventID != retry.EventID {
		return "", ErrCompletionRetryIneligible
	}
	if state == factory.ReassessmentSuperseded {
		if prior.Cycle.Successor == "" || prior.Cycle.Successor != retry.SuccessorCycleID {
			return "", errors.New("reassessment retry names another successor")
		}
		return prior.Cycle.Successor, nil
	}
	superseded, err := prior.Cycle.Supersede(retry)
	if err != nil {
		return "", err
	}
	prior.Cycle = superseded
	prior.WaitReason = ""
	priorData, err := json.Marshal(prior)
	if err != nil {
		return "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_reassessments SET state=$1,data=$2 WHERE cycle=$3 AND state=$4`,
		factory.ReassessmentSuperseded, string(priorData), retry.ExpectedPriorCycle, factory.ReassessmentAttention)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrStaleRevision
	}
	successor := factory.ReassessmentCycle{
		EventID: retry.EventID, CycleID: retry.SuccessorCycleID, RetryOf: retry.ExpectedPriorCycle,
		State: factory.ReassessmentCollecting, Repository: prior.Cycle.Repository, Generation: prior.Cycle.Generation,
	}
	successorRecord := ReassessmentRecord{Cycle: successor, Issue: prior.Issue, Completion: prior.Completion}
	successorData, err := json.Marshal(successorRecord)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_reassessments(cycle,repository,generation,event,state,data)
		VALUES($1,$2,$3,$4,$5,$6)`, successor.CycleID, successor.Repository, successor.Generation,
		successor.EventID, successor.State, string(successorData)); err != nil {
		return "", err
	}
	return successor.CycleID, nil
}
