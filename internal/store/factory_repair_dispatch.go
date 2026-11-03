package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

func repairRootInputsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (StallRepairRecord, int, factory.RepairCharge, error) {
	var record StallRepairRecord
	var charge factory.RepairCharge
	if a.Subject.Stall == nil || a.Phase != project.ExecutionRepair || a.Role != project.RoleCoder || a.Issue != 0 || a.Acceptance != "" || a.ID != a.AttemptRoot || a.ID != a.Subject.Stall.CycleID || a.ParentAssignment != "" || a.PublicationAssignment != "" {
		return record, 0, charge, errors.New("repair requires its exact independent stall root")
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_stall_events WHERE repository=$1 AND event=$2 FOR UPDATE`, a.Repository, a.Subject.Stall.StallID).Scan(&raw); err != nil {
		return record, 0, charge, err
	}
	if json.Unmarshal(raw, &record) != nil || record.StallEvent.Validate() != nil {
		return record, 0, charge, errors.New("repair observation is malformed")
	}
	index := -1
	for i, cycle := range record.Repair.Cycles {
		if cycle.ID == a.Subject.Stall.CycleID {
			index = i
		}
	}
	if index < 0 {
		return record, 0, charge, errors.New("repair cycle is not retained")
	}
	cycle := record.Repair.Cycles[index]
	if cycle.ValidateFor(record.StallEvent) != nil || cycle.State != factory.RepairReady || cycle.InventoryDigest != a.Subject.Stall.InventoryDigest || cycle.NativeRevision != a.NativeRev || cycle.Local.SourceCommit != a.SourceCommit || cycle.AssignmentID != "" || a.Run != factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, cycle.Number) {
		return record, 0, charge, errors.New("repair cycle changed or already owns its investigator")
	}
	var payload string
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM factory_commands WHERE id=$1 AND type=$2 AND target=$3 AND finished<>''`, a.Run, factory.CommandRepairCharge, repairChargeTarget(a.Repository)).Scan(&payload); err != nil {
		return record, 0, charge, err
	}
	if json.Unmarshal([]byte(payload), &charge) != nil || charge.Validate() != nil || charge.EpisodeID != record.EpisodeID || charge.Kind != factory.RepairChargeRun || charge.Cycle != cycle.Number || charge.ActiveMinutes < 1 {
		return record, 0, charge, errors.New("repair lacks its exact admitted run charge")
	}
	if cycle.Number > 1 && charge.EvidenceDigest != cycle.InventoryDigest {
		return record, 0, charge, errors.New("later repair charge differs from retained factual inventory")
	}
	commands, err := finishedCommands(ctx, tx, factory.CommandRepairGrant, factory.RepairTarget(a.Repository))
	if err != nil {
		return record, 0, charge, err
	}
	head, err := headRepairGrant(commands)
	if err != nil {
		return record, 0, charge, err
	}
	var grant factory.RepairGrant
	if json.Unmarshal([]byte(head.Payload), &grant) != nil || grant.Validate() != nil || !grant.Active || grant.Sponsor != a.ActorID {
		return record, 0, charge, errors.New("repair sponsor grant changed")
	}
	return record, index, charge, nil
}

func validateStallRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	_, _, _, err := repairRootInputsTx(ctx, tx, a)
	return err
}

func insertStallAttemptAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, deadline, now time.Time) error {
	record, index, charge, err := repairRootInputsTx(ctx, tx, a)
	if err != nil {
		return err
	}
	allowance := factory.AttemptAllowance{Limits: factory.AttemptLimits{ActiveMinutes: charge.ActiveMinutes}, Stall: a.Subject.Stall, RootAssignment: a.ID, Repository: a.Repository, CheckpointUnix: now.Unix(), Active: true}
	if err := allowance.Validate(); err != nil {
		return err
	}
	if !deadline.After(now) || deadline.After(time.Unix(now.Unix()+allowance.RemainingSeconds(now), 0)) {
		return factory.ErrAttemptTimeExhausted
	}
	raw, err := json.Marshal(allowance)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,0,$2,0,$3)`, a.Repository, a.ID, string(raw)); err != nil {
		return err
	}
	cycle := &record.Repair.Cycles[index]
	cycle.State, cycle.AssignmentID, cycle.RunID = factory.RepairInvestigating, a.ID, a.Run
	record.Repair.Revision++
	raw, err = json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_stall_events SET data=$1 WHERE repository=$2 AND event=$3`, string(raw), a.Repository, record.ID)
	return err
}
