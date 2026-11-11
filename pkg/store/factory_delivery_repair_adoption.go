package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

func deliveryRepairInvestigatorRetiredTx(ctx context.Context, tx *sql.Tx, record DeliveryRepairRecord) error {
	var raw []byte
	if record.Cycle.AssignmentID == "" || record.Cycle.RunID == "" {
		return errors.New("delivery repair has no admitted investigator")
	}
	if err := tx.QueryRowContext(ctx, `SELECT a.data FROM factory_assignments a JOIN factory_runs r ON r.id=a.run WHERE a.id=$1 AND a.stage='finished' AND r.active=FALSE AND r.settled=TRUE AND r.data->>'reconciled'='true' FOR SHARE OF a,r`, record.Cycle.AssignmentID).Scan(&raw); err != nil {
		return err
	}
	var a factory.Assignment
	if json.Unmarshal(raw, &a) != nil || a.Run != record.Cycle.RunID || a.Repository != record.Cause.Repository || a.ProjectID != record.Cause.ProjectID || a.Subject.DeliveryRepair == nil || *a.Subject.DeliveryRepair != record.Subject || a.Outcome != factory.Succeeded || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil || a.Result.PhaseResult.BindAssignment(a) != nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.DeliveryRepair == nil {
		return errors.New("delivery repair lacks its retired bound proposal")
	}
	return deliveryRepairReportScopeTx(ctx, tx, a, a.Result.PhaseResult.Output.DeliveryRepair.InputDigest)
}

// AdmitDeliveryRepairJudgment charges the repair grant and binds the
// cycle's judgment fingerprint before the first call. A call that failed
// without a receipt retries under the same fingerprint; a recorded receipt
// is reused (false).
func (s *Store) AdmitDeliveryRepairJudgment(ctx context.Context, repository int64, failedRun, cycleID, fingerprint string, now time.Time) (bool, error) {
	if !factory.ValidDigest(fingerprint) {
		return false, errors.New("invalid delivery repair judgment fingerprint")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, factory.RepairTarget(repository)); err != nil {
		return false, err
	}
	record, err := deliveryRepairRecordTx(ctx, tx, repository, failedRun, true)
	if err != nil {
		return false, err
	}
	if record.Cycle.ID != cycleID || record.Cycle.State != factory.RepairAssessing {
		return false, ErrStaleRevision
	}
	if record.Cycle.JudgmentFingerprint != "" {
		if record.Cycle.JudgmentFingerprint != fingerprint {
			return false, ErrStaleRevision
		}
		return true, tx.Commit()
	}
	if err = deliveryRepairInvestigatorRetiredTx(ctx, tx, record); err != nil {
		return false, err
	}
	grant, err := currentDeliveryRepairGrantTx(ctx, tx, repository)
	if err != nil {
		return false, err
	}
	episode := factory.DeliveryRepairCauseID(failedRun)
	charge := factory.RepairCharge{ID: factory.RepairChargeID(episode, factory.RepairChargeJudgment, 1), GrantID: grant.ID, Kind: factory.RepairChargeJudgment, EpisodeID: episode, Cycle: 1, EvidenceDigest: record.Subject.InventoryDigest, SpentUnix: now.Unix()}
	if created, err := admitRepairChargeTx(ctx, tx, repository, charge, now); err != nil || !created {
		if err == nil {
			err = ErrCommandConflict
		}
		return false, err
	}
	record.Cycle.JudgmentFingerprint = fingerprint
	record.Revision++
	raw, _ := json.Marshal(record)
	if record.Validate() != nil {
		return false, errors.New("delivery repair judgment exceeds its retained journal")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$1 WHERE id=$2 AND type=$3`, string(raw), episode, factory.CommandDeliveryRepair); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ActivateDeliveryRepairIntent retains the same graph-operation journal as
// stall/completion adoption. Withdrawal fences new activation while already
// active operations retain their exact reconciliation owner.
func (s *Store) ActivateDeliveryRepairIntent(ctx context.Context, repository int64, failedRun, cycleID string, expected, next json.RawMessage, revision factory.IntentRevision, generation factory.IntentGeneration) error {
	if !factory.ValidID(cycleID) || !json.Valid(expected) || !json.Valid(next) || generation.RepairCycle != cycleID || generation.ReassessmentCycle != "" || revision.Repository != repository || revision.State != factory.IntentAccepted || generation.RevisionID != revision.ID || generation.IntentID != revision.Intent || generation.IntentDigest != revision.Digest() || revision.Validate() != nil {
		return errors.New("invalid delivery repair activation")
	}
	if err := checkSubmittedGenerationGraph(generation); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, factory.RepairTarget(repository)); err != nil {
		return err
	}
	record, err := deliveryRepairRecordTx(ctx, tx, repository, failedRun, true)
	if err != nil {
		return err
	}
	if record.Subject.CycleID != cycleID || record.Cycle.State != factory.RepairApplying || !factory.ValidReassessmentProposal(record.Cycle.ProposalOption, record.Cycle.ProposalProbability, record.Cycle.ProposalConfidence) || revision.Intent != record.Cause.Delivery.IntentID {
		return ErrStaleRevision
	}
	if _, err = currentDeliveryRepairGrantTx(ctx, tx, repository); err != nil {
		return err
	}
	if err = deliveryRepairInvestigatorRetiredTx(ctx, tx, record); err != nil {
		return err
	}
	same, err := sameJSONDocument(ctx, tx, record.Cycle.AdoptionProgress, expected)
	if err != nil {
		return err
	}
	if !same {
		return ErrStaleRevision
	}
	raw, _ := json.Marshal(revision)
	if err = saveIntentRevisionTx(ctx, tx, revision, raw); err != nil {
		return err
	}
	if err = checkGenerationHeadTx(ctx, tx, generation); err != nil {
		return err
	}
	raw, _ = json.Marshal(generation)
	if err = insertGenerationRecordTx(ctx, tx, generation, raw); err != nil {
		return err
	}
	for _, binding := range generation.CopiedBindings {
		if err = bindIntentTaskAcceptanceTx(ctx, tx, binding); err != nil {
			return err
		}
	}
	if _, err = completionMaterialTx(ctx, tx, repository); err != nil {
		return err
	}
	prior := record
	record.Revision++
	record.Cycle.AdoptionProgress, record.Cycle.Reason = next, ""
	if err = saveDeliveryRepairRecordTx(ctx, tx, prior, record); err != nil {
		return err
	}
	return tx.Commit()
}
