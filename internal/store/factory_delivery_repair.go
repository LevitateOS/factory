package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// Delivery repair uses the existing internal command journal. The immutable
// payload binds its failed cause; only this owner advances the bounded outcome
// under the record revision. Human request receipts remain separate commands.
type DeliveryRepairRecord struct {
	Revision int64                         `json:"revision"`
	Cause    factory.DeliveryRepairCause   `json:"cause"`
	Subject  factory.DeliveryRepairSubject `json:"subject"`
	Cycle    factory.StallRepairCycle      `json:"cycle"`
}

func deliveryRepairTarget(repository int64, run string) string {
	return "repository/" + strconv.FormatInt(repository, 10) + "/delivery-repair/" + run
}

func (r DeliveryRepairRecord) Validate() error {
	c, s, cycle := r.Cause, r.Subject, r.Cycle
	if c.Validate() != nil || r.Revision < 0 || s.DeliveryID != c.Delivery.DeliveryID || s.FailedVerificationRunID != c.FailedVerificationRunID || s.FailureDigest != c.FailureDigest || s.CycleID != factory.DeliveryRepairCycleID(c.FailedVerificationRunID) || cycle.ID != s.CycleID || cycle.Number != 1 || len(cycle.Reason) > 256 {
		return errors.New("invalid delivery repair journal binding")
	}
	switch cycle.State {
	case factory.RepairCollecting, factory.RepairAttention, factory.RepairStale:
	case factory.RepairReady, factory.RepairInvestigating, factory.RepairAssessing, factory.RepairApplying, factory.RepairAdopted:
		if s.Validate() != nil || cycle.InventoryDigest != s.InventoryDigest || cycle.NativeRevision < 1 || cycle.Local.Validate() != nil || cycle.Local.Material.Repository != c.Repository || cycle.Issues == nil {
			return errors.New("delivery repair lacks its complete sealed input")
		}
		var prior int64
		for _, issue := range cycle.Issues {
			if issue.Validate() != nil || issue.Index <= prior {
				return errors.New("delivery repair inventory is invalid or unordered")
			}
			prior = issue.Index
		}
	default:
		return errors.New("unknown delivery repair state")
	}
	if cycle.AssignmentID != "" || cycle.RunID != "" {
		if cycle.AssignmentID != cycle.ID || cycle.RunID != factory.RepairChargeID(factory.DeliveryRepairCauseID(c.FailedVerificationRunID), factory.RepairChargeRun, 1) {
			return errors.New("delivery repair changed its sole worker identity")
		}
	}
	if cycle.JudgmentFingerprint != "" && !factory.ValidDigest(cycle.JudgmentFingerprint) {
		return errors.New("invalid delivery repair judgment reservation")
	}
	if cycle.State == factory.RepairApplying || cycle.State == factory.RepairAdopted {
		if !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) || !json.Valid(cycle.AdoptionProgress) {
			return errors.New("delivery repair adoption lacks its qualified proposal and journal")
		}
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("delivery repair exceeds the existing command journal bound")
	}
	return nil
}

func deliveryRepairRecordTx(ctx context.Context, q grantCommandSQL, repository int64, run string, lock bool) (DeliveryRepairRecord, error) {
	var record DeliveryRepairRecord
	if repository <= 0 || !factory.ValidID(run) {
		return record, errors.New("invalid delivery repair lookup")
	}
	query := `SELECT outcome FROM factory_commands WHERE id=$1 AND type=$2 AND target=$3 AND outcome<>''`
	if lock {
		query += ` FOR UPDATE`
	}
	var raw string
	err := q.QueryRowContext(ctx, query, factory.DeliveryRepairCauseID(run), factory.CommandDeliveryRepair, deliveryRepairTarget(repository, run)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrNotFound
	}
	if err != nil {
		return record, err
	}
	if json.Unmarshal([]byte(raw), &record) != nil || record.Validate() != nil || record.Cause.Repository != repository || record.Cause.FailedVerificationRunID != run {
		return record, errors.New("invalid retained delivery repair journal")
	}
	return record, nil
}

func (s *Store) DeliveryRepairRecord(ctx context.Context, repository int64, failedRun string) (DeliveryRepairRecord, error) {
	return deliveryRepairRecordTx(ctx, s.db, repository, failedRun, false)
}

func deliveryRepairReportScopeTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, digest string) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_mcp_capabilities WHERE assignment=$1 AND run=$2 FOR SHARE`, a.ID, a.Run).Scan(&raw); err != nil {
		return err
	}
	var cap factory.MCPCapability
	if json.Unmarshal(raw, &cap) != nil || cap.Validate() != nil || cap.AssignmentID != a.ID || cap.RunID != a.Run || cap.Repository != a.Repository || cap.Project != a.ProjectID || cap.SourceDigest != a.SourceCommit || cap.ScopeDigest() != digest {
		return errors.New("delivery repair report differs from admitted input scope")
	}
	return nil
}

// validateDeliveryRepairCauseTx repeats the canonical retained failed-report,
// native retirement and current accepted-scope joins. Controller native reads
// are additional evidence, never a substitute for these transaction facts.
func validateDeliveryRepairCauseTx(ctx context.Context, tx *sql.Tx, cause factory.DeliveryRepairCause) error {
	if err := cause.Validate(); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 AND repository=$2 AND stage='finished' FOR SHARE`, cause.FailedAssignmentID, cause.Repository).Scan(&raw); err != nil {
		return err
	}
	var a factory.Assignment
	if json.Unmarshal(raw, &a) != nil || a.Validate() != nil || a.Run != cause.FailedVerificationRunID || a.ProjectID != cause.ProjectID || a.Outcome != factory.Succeeded || a.Subject.Delivery == nil || *a.Subject.Delivery != cause.Delivery || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil || a.Result.PhaseResult.BindAssignment(a) != nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Verification == nil || !reflect.DeepEqual(*a.Result.PhaseResult.Output.Verification, cause.Report) {
		return errors.New("delivery repair differs from its canonical failed verification")
	}
	if err := deliveryRepairReportScopeTx(ctx, tx, a, cause.Report.InputDigest); err != nil {
		return err
	}
	if err := validateVerificationJourneyRefsTx(ctx, tx, a, cause.Report); err != nil {
		return err
	}
	var run factory.Run
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 AND active=FALSE AND settled=TRUE FOR SHARE`, a.Run).Scan(&raw); err != nil {
		return err
	}
	if json.Unmarshal(raw, &run) != nil || !run.Reconciled || !run.CredentialsReturned() || run.Outcome != factory.Succeeded || run.ProjectID != a.ProjectID || run.InputSHA != a.SourceCommit {
		return errors.New("delivery repair requires confirmed verification retirement")
	}
	if _, _, err := applicationVerificationPrerequisitesTx(ctx, tx, a); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT p.data FROM project_preparations p JOIN project_requirement_heads r ON r.project_id=p.project_id AND r.decision=p.requirements JOIN project_approval_heads h ON h.project_id=p.project_id AND h.decision=p.approval WHERE p.id=$1 FOR SHARE OF p,r,h`, a.Preparation).Scan(&raw); err != nil {
		return err
	}
	p, _, err := unmarshalCandidatePreparation(raw)
	if err != nil {
		return err
	}
	state := p.State
	outputDigest := factory.MCPInputDigest([]byte(state.Output))
	manifestOutput, err := checkVerifiedManifestExecutionTx(ctx, tx, a)
	if err != nil {
		return err
	}
	if manifestOutput != "" {
		outputDigest = manifestOutput
	}
	if factory.DeliveryPreparationDigest(p.Preparation) != cause.Delivery.PreparationDigest || !state.Ready || state.Stopped || state.Phase != project.PrepareReady || state.SourceCommit != a.SourceCommit || state.CheckExit == nil || *state.CheckExit != 0 || len(cause.Report.CheckReceipts) != 1 || cause.Report.CheckReceipts[0].CheckID != factory.ApprovedAppCheckID || cause.Report.CheckReceipts[0].Outcome != factory.CheckReceiptPassed || cause.Report.CheckReceipts[0].ArtifactDigest != outputDigest {
		return errors.New("delivery repair failure lacks its matching trusted check receipt")
	}
	return nil
}

func (s *Store) RecordDeliveryRepairCause(ctx context.Context, cause factory.DeliveryRepairCause, now time.Time) (DeliveryRepairRecord, bool, error) {
	var record DeliveryRepairRecord
	if err := cause.Validate(); err != nil {
		return record, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return record, false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, deliveryRepairTarget(cause.Repository, cause.FailedVerificationRunID)); err != nil {
		return record, false, err
	}
	prior, err := deliveryRepairRecordTx(ctx, tx, cause.Repository, cause.FailedVerificationRunID, true)
	if err == nil {
		if !reflect.DeepEqual(prior.Cause, cause) {
			return record, false, ErrCommandConflict
		}
		return prior, false, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return record, false, err
	}
	if err = validateDeliveryRepairCauseTx(ctx, tx, cause); err != nil {
		return record, false, err
	}
	cycle := factory.DeliveryRepairCycleID(cause.FailedVerificationRunID)
	record = DeliveryRepairRecord{Cause: cause, Subject: factory.DeliveryRepairSubject{DeliveryID: cause.Delivery.DeliveryID, FailedVerificationRunID: cause.FailedVerificationRunID, FailureDigest: cause.FailureDigest, CycleID: cycle}, Cycle: factory.StallRepairCycle{ID: cycle, Number: 1, State: factory.RepairCollecting}}
	if err = record.Validate(); err != nil {
		return record, false, err
	}
	payload, _ := json.Marshal(struct {
		FailureDigest string `json:"failure_digest"`
	}{cause.FailureDigest})
	raw, _ := json.Marshal(record)
	cmd := factory.Command{ID: factory.DeliveryRepairCauseID(cause.FailedVerificationRunID), Type: factory.CommandDeliveryRepair, Target: deliveryRepairTarget(cause.Repository, cause.FailedVerificationRunID), Principal: "factory-delivery-repair", Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if _, _, err = recordFactoryCommandTx(ctx, tx, cmd, now); err != nil {
		return record, false, err
	}
	// This record owns the running repair cycle. Completion marks it finished;
	// there is no separate owner-request receipt.
	if _, err = tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$1 WHERE id=$2 AND type=$3 AND finished=''`, string(raw), cmd.ID, factory.CommandDeliveryRepair); err != nil {
		return record, false, err
	}
	return record, true, tx.Commit()
}

func (s *Store) PendingDeliveryRepairRecords(ctx context.Context, after string, limit int) ([]DeliveryRepairRecord, string, error) {
	if limit < 1 || limit > 64 || after != "" && !factory.ValidID(after) {
		return nil, after, errors.New("invalid delivery repair page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,outcome FROM factory_commands WHERE type=$1 AND outcome<>'' AND id>$2 AND outcome::jsonb->'cycle'->>'state'<>'stale' ORDER BY id LIMIT $3`, factory.CommandDeliveryRepair, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var records []DeliveryRepairRecord
	next := ""
	for rows.Next() {
		var raw string
		var record DeliveryRepairRecord
		if err = rows.Scan(&next, &raw); err != nil {
			return nil, after, err
		}
		if json.Unmarshal([]byte(raw), &record) != nil || record.Validate() != nil || next != factory.DeliveryRepairCauseID(record.Cause.FailedVerificationRunID) {
			return nil, after, errors.New("invalid retained delivery repair page")
		}
		records = append(records, record)
	}
	return records, next, rows.Err()
}

func saveDeliveryRepairRecordTx(ctx context.Context, tx *sql.Tx, prior, next DeliveryRepairRecord) error {
	if next.Validate() != nil || next.Revision != prior.Revision+1 || !reflect.DeepEqual(prior.Cause, next.Cause) || prior.Subject.DeliveryID != next.Subject.DeliveryID || prior.Subject.FailedVerificationRunID != next.Subject.FailedVerificationRunID || prior.Subject.FailureDigest != next.Subject.FailureDigest || prior.Subject.CycleID != next.Subject.CycleID || !factory.RepairTransitionAllowed(prior.Cycle.State, next.Cycle.State) {
		return errors.New("invalid delivery repair transition")
	}
	a, b := prior.Cycle, next.Cycle
	if prior.Subject.InventoryDigest != "" && prior.Subject.InventoryDigest != next.Subject.InventoryDigest || a.AssignmentID != "" && (a.AssignmentID != b.AssignmentID || a.RunID != b.RunID) || a.InventoryDigest != "" && (a.InventoryDigest != b.InventoryDigest || a.NativeRevision != b.NativeRevision || !a.Local.Equal(b.Local) || !reflect.DeepEqual(a.Issues, b.Issues)) || a.JudgmentFingerprint != "" && a.JudgmentFingerprint != b.JudgmentFingerprint || a.ProposalOption != "" && (a.ProposalOption != b.ProposalOption || a.ProposalProbability != b.ProposalProbability || a.ProposalConfidence != b.ProposalConfidence) {
		return errors.New("delivery repair retained inputs and decisions are immutable")
	}
	if a.AssignmentID == "" && b.AssignmentID != "" {
		return errors.New("delivery repair worker binding belongs to atomic dispatch")
	}
	if a.JudgmentFingerprint == "" && b.JudgmentFingerprint != "" {
		return errors.New("delivery repair judgment belongs to atomic reservation")
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	finished := ""
	if b.State == factory.RepairAdopted || b.State == factory.RepairAttention || b.State == factory.RepairStale {
		finished = time.Now().UTC().Format(time.RFC3339Nano)
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$1,finished=$4 WHERE id=$2 AND type=$3 AND finished=''`, string(raw), factory.DeliveryRepairCauseID(prior.Cause.FailedVerificationRunID), factory.CommandDeliveryRepair, finished)
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
	return nil
}

func (s *Store) SaveDeliveryRepairRecord(ctx context.Context, record DeliveryRepairRecord, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prior, err := deliveryRepairRecordTx(ctx, tx, record.Cause.Repository, record.Cause.FailedVerificationRunID, true)
	if err != nil {
		return err
	}
	if prior.Revision != expected {
		return ErrStaleRevision
	}
	if err = saveDeliveryRepairRecordTx(ctx, tx, prior, record); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeliveryRepairCycle(ctx context.Context, cycleID string) (DeliveryRepairRecord, factory.StallRepairCycle, error) {
	var record DeliveryRepairRecord
	if !factory.ValidID(cycleID) {
		return record, record.Cycle, errors.New("invalid delivery repair cycle")
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT outcome FROM factory_commands WHERE type=$1 AND outcome<>'' AND outcome::jsonb->'cycle'->>'id'=$2`, factory.CommandDeliveryRepair, cycleID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, record.Cycle, ErrNotFound
	}
	if err != nil {
		return record, record.Cycle, err
	}
	if json.Unmarshal([]byte(raw), &record) != nil || record.Validate() != nil {
		return record, record.Cycle, errors.New("invalid retained delivery repair cycle")
	}
	return record, record.Cycle, nil
}
