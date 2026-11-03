package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

func currentDeliveryRepairContextTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, out *factory.CurrentContext) error {
	if a.Subject.DeliveryRepair == nil {
		return errors.New("delivery repair context lacks its subject")
	}
	record, err := deliveryRepairRecordTx(ctx, tx, a.Repository, a.Subject.DeliveryRepair.FailedVerificationRunID, false)
	if err != nil {
		return err
	}
	cycle := record.Cycle
	if record.Subject != *a.Subject.DeliveryRepair || cycle.AssignmentID != a.ID || cycle.RunID != a.Run || cycle.NativeRevision != a.NativeRev || cycle.Local.SourceCommit != a.SourceCommit || record.Cause.ProjectID != a.ProjectID || cycle.State == factory.RepairStale {
		return errors.New("delivery repair context differs from its sole admitted investigator")
	}
	grant, err := currentDeliveryRepairGrantTx(ctx, tx, a.Repository)
	if err != nil || grant.Sponsor != a.ActorID {
		return errors.New("delivery repair context sponsor grant is unavailable")
	}
	out.Role.DeliveryRepair = &factory.CurrentDeliveryRepairContext{Subject: record.Subject, Cause: record.Cause, Local: cycle.Local, Issues: cycle.Issues, Complete: true, State: cycle.State, Reason: cycle.Reason}
	return nil
}

func currentStallContextTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, out *factory.CurrentContext) error {
	if a.Subject.Stall == nil {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "sealed_repair_inputs_unavailable")
		return nil
	}
	var record StallRepairRecord
	if err := contextJSONRow(ctx, tx, &record, `SELECT data FROM factory_stall_events WHERE repository=$1 AND event=$2`, a.Repository, a.Subject.Stall.StallID); err != nil {
		return err
	}
	commands, err := finishedCommands(ctx, tx, factory.CommandRepairGrant, factory.RepairTarget(a.Repository))
	if err != nil {
		return err
	}
	head, err := headRepairGrant(commands)
	if err != nil {
		return err
	}
	var grant factory.RepairGrant
	if json.Unmarshal([]byte(head.Payload), &grant) != nil || !grant.Active || grant.Sponsor != a.ActorID {
		return errors.New("repair context sponsor grant is unavailable")
	}
	for _, cycle := range record.Repair.Cycles {
		if cycle.ID != a.Subject.Stall.CycleID {
			continue
		}
		if cycle.ValidateFor(record.StallEvent) != nil || cycle.AssignmentID != a.ID || cycle.RunID != a.Run || cycle.InventoryDigest != a.Subject.Stall.InventoryDigest || cycle.NativeRevision != a.NativeRev || cycle.Local.SourceCommit != a.SourceCommit || cycle.State == factory.RepairStale {
			return errors.New("repair context differs from its sole admitted investigator")
		}
		out.Role.Repair = &factory.CurrentRepairContext{StallID: record.ID, EpisodeID: record.EpisodeID, CycleID: cycle.ID, Cycle: cycle.Number, Cause: record.Reason, InventoryDigest: cycle.InventoryDigest, NativeRevision: cycle.NativeRevision, Local: cycle.Local, Issues: cycle.Issues, ChargeID: a.Run, Complete: true}
		out.Role.Repair.State, out.Role.Repair.Reason = cycle.State, cycle.Reason
		out.Role.Repair.PriorCycles = []factory.CurrentRepairCycle{}
		for _, prior := range record.Repair.Cycles {
			if prior.Number < cycle.Number {
				out.Role.Repair.PriorCycles = append(out.Role.Repair.PriorCycles, factory.CurrentRepairCycle{ID: prior.ID, Number: prior.Number, State: prior.State, Reason: prior.Reason, InventoryDigest: prior.InventoryDigest})
			}
		}
		charges, err := repairCharges(ctx, tx, a.Repository)
		if err != nil {
			return err
		}
		budget := factory.CurrentRepairBudget{CyclesRemaining: factory.DefaultRepairCyclesPerEpisode - len(record.Repair.Cycles), RunsRemaining: grant.InvestigatorRuns, JudgmentsRemaining: grant.ProposalJudgments, ActiveMinutesRemaining: grant.ActiveMinutes}
		for _, charge := range charges {
			if charge.Kind == factory.RepairChargeRun {
				budget.RunsRemaining--
				budget.ActiveMinutesRemaining -= charge.ActiveMinutes
			} else if charge.Kind == factory.RepairChargeJudgment {
				budget.JudgmentsRemaining--
			}
		}
		budget.CyclesRemaining = max(0, budget.CyclesRemaining)
		budget.RunsRemaining = max(0, budget.RunsRemaining)
		budget.JudgmentsRemaining = max(0, budget.JudgmentsRemaining)
		budget.ActiveMinutesRemaining = max(0, budget.ActiveMinutesRemaining)
		out.Role.Repair.Budget = &budget
		return nil
	}
	return errors.New("bound repair context is unavailable")
}
