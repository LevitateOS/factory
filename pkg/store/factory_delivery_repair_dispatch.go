package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func currentDeliveryRepairGrantTx(ctx context.Context, tx *sql.Tx, repository int64) (factory.RepairGrant, error) {
	var grant factory.RepairGrant
	heads, err := finishedCommands(ctx, tx, factory.CommandRepairGrant, factory.RepairTarget(repository))
	if err != nil {
		return grant, err
	}
	head, err := headRepairGrant(heads)
	if err != nil {
		return grant, err
	}
	if json.Unmarshal([]byte(head.Payload), &grant) != nil || grant.Validate() != nil || !grant.Active {
		return grant, factory.ErrRepairGrantInactive
	}
	return grant, nil
}

func deliveryRepairChargeMatches(record DeliveryRepairRecord, charge factory.RepairCharge) bool {
	episode := factory.DeliveryRepairCauseID(record.Cause.FailedVerificationRunID)
	return charge.Validate() == nil && charge.EpisodeID == episode && charge.Cycle == 1 && charge.ID == factory.RepairChargeID(episode, charge.Kind, 1) && charge.EvidenceDigest == record.Subject.InventoryDigest && (charge.Kind != factory.RepairChargeRun || charge.ActiveMinutes > 0) && (charge.Kind != factory.RepairChargeJudgment || charge.ActiveMinutes == 0)
}

func (s *Store) AdmitDeliveryRepairInvestigation(ctx context.Context, repository int64, failedRun string, charge factory.RepairCharge, now time.Time) (bool, error) {
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
	if !deliveryRepairChargeMatches(record, charge) {
		return false, errors.New("delivery repair charge differs from its sealed cause")
	}
	// Exact replay stays readable after withdrawal; it never admits a new worker.
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_commands WHERE id=$1 AND type=$2 AND target=$3 AND finished<>'')`, charge.ID, factory.CommandRepairCharge, repairChargeTarget(repository)).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		if record.Cycle.State != factory.RepairReady || charge.Kind != factory.RepairChargeRun {
			return false, ErrStaleRevision
		}
		if err = validateDeliveryRepairCauseTx(ctx, tx, record.Cause); err != nil {
			return false, err
		}
		if err = checkDeliveryRepairRunsTx(ctx, tx, record, charge.ID); err != nil {
			return false, err
		}
		if err = checkDeliveryRepairIntentTimeTx(ctx, tx, record, charge.ID, int64(charge.ActiveMinutes)*60, now); err != nil {
			return false, err
		}
	}
	created, err := admitRepairChargeTx(ctx, tx, repository, charge, now)
	if err != nil {
		return false, err
	}
	return created, tx.Commit()
}

func deliveryRepairRootInputsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (DeliveryRepairRecord, factory.RepairCharge, error) {
	var charge factory.RepairCharge
	if a.Subject.DeliveryRepair == nil || a.Phase != project.ExecutionRepair || a.Role != project.RoleCoder || a.Issue != 0 || a.Acceptance != "" || a.ID != a.AttemptRoot || a.ID != a.Subject.DeliveryRepair.CycleID || a.ParentAssignment != "" || a.PublicationAssignment != "" {
		return DeliveryRepairRecord{}, charge, errors.New("delivery repair requires its exact independent root")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, factory.RepairTarget(a.Repository)); err != nil {
		return DeliveryRepairRecord{}, charge, err
	}
	record, err := deliveryRepairRecordTx(ctx, tx, a.Repository, a.Subject.DeliveryRepair.FailedVerificationRunID, true)
	if err != nil {
		return record, charge, err
	}
	cycle := record.Cycle
	if record.Subject != *a.Subject.DeliveryRepair || cycle.State != factory.RepairReady || cycle.AssignmentID != "" || cycle.Local.SourceCommit != a.SourceCommit || cycle.NativeRevision != a.NativeRev || record.Cause.ProjectID != a.ProjectID {
		return record, charge, errors.New("delivery repair sealed input changed or already owns its worker")
	}
	if err = validateDeliveryRepairCauseTx(ctx, tx, record.Cause); err != nil {
		return record, charge, err
	}
	var payload string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM factory_commands WHERE id=$1 AND type=$2 AND target=$3 AND finished<>''`, a.Run, factory.CommandRepairCharge, repairChargeTarget(a.Repository)).Scan(&payload); err != nil {
		return record, charge, err
	}
	if json.Unmarshal([]byte(payload), &charge) != nil || !deliveryRepairChargeMatches(record, charge) || charge.Kind != factory.RepairChargeRun {
		return record, charge, errors.New("delivery repair lacks its exact admitted investigator charge")
	}
	grant, err := currentDeliveryRepairGrantTx(ctx, tx, a.Repository)
	if err != nil {
		return record, charge, err
	}
	if grant.Sponsor != a.ActorID {
		return record, charge, errors.New("delivery repair sponsor changed")
	}
	if err = checkDeliveryRepairRunsTx(ctx, tx, record, charge.ID); err != nil {
		return record, charge, err
	}
	return record, charge, nil
}

func validateDeliveryRepairRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	_, _, err := deliveryRepairRootInputsTx(ctx, tx, a)
	return err
}

func insertDeliveryRepairAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, deadline, now time.Time) error {
	record, charge, err := deliveryRepairRootInputsTx(ctx, tx, a)
	if err != nil {
		return err
	}
	allowance := factory.AttemptAllowance{Limits: factory.AttemptLimits{ActiveMinutes: charge.ActiveMinutes}, DeliveryRepair: a.Subject.DeliveryRepair, RootAssignment: a.ID, Repository: a.Repository, CheckpointUnix: now.Unix(), Active: true}
	if err = allowance.Validate(); err != nil {
		return err
	}
	if !deadline.After(now) || deadline.After(time.Unix(now.Unix()+allowance.RemainingSeconds(now), 0)) {
		return factory.ErrAttemptTimeExhausted
	}
	if err = checkDeliveryRepairIntentTimeTx(ctx, tx, record, a.Run, deadline.Unix()-now.Unix(), now); err != nil {
		return err
	}
	raw, _ := json.Marshal(allowance)
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,0,$2,0,$3)`, a.Repository, a.ID, string(raw)); err != nil {
		return err
	}
	record.Cycle.State, record.Cycle.AssignmentID, record.Cycle.RunID = factory.RepairInvestigating, a.ID, a.Run
	record.Revision++
	raw, _ = json.Marshal(record)
	_, err = tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$1 WHERE id=$2 AND type=$3`, string(raw), factory.DeliveryRepairCauseID(record.Cause.FailedVerificationRunID), factory.CommandDeliveryRepair)
	return err
}
