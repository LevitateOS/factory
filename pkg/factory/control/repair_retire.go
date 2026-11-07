package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) retireReadyStallRepairs(ctx context.Context, repositories map[int64]bool) {
	if len(repositories) == 0 {
		return
	}
	records, err := c.Store.PendingStallRepairRecords(ctx, 64)
	if err != nil {
		return
	}
	for _, record := range records {
		if !repositories[record.Repository] || len(record.Repair.Cycles) == 0 {
			continue
		}
		cycle := record.Repair.Cycles[len(record.Repair.Cycles)-1]
		// A durable graph prefix has its own exact readback owner; ready work cannot
		// abandon an activated head or suppress reconciliation of its admitted effect.
		if cycle.State == factory.RepairApplying {
			continue
		}
		cycle.State, cycle.Reason = factory.RepairStale, "ready_work_retired_obsolete_diagnosis"
		if c.saveRepairCycle(ctx, record, cycle) != nil {
			continue
		}
		if cycle.RunID != "" && c.Host != nil && c.Broker != nil {
			run, e := c.Store.FactoryRun(ctx, cycle.RunID)
			if e != nil || run.Reconciled {
				continue
			}
			c.settleRun(ctx, run)
		}
	}
}

func (c *Coordinator) nextStallRepairCycle(ctx context.Context, record store.StallRepairRecord, previous factory.StallRepairCycle) {
	if previous.Number >= factory.DefaultRepairCyclesPerEpisode {
		return
	}
	fresh := bracketMaterialCollect(ctx, c.completionDriveDeps(), record.Repository, record.ProgressGeneration, nil)
	if fresh.hold != "" {
		return
	}
	sealed := fresh.sealed
	next := factory.StallRepairCycle{ID: factory.RepairCycleID(record.EpisodeID, previous.Number+1), Number: previous.Number + 1, State: factory.RepairReady, InventoryDigest: sealed.Digest, NativeRevision: sealed.Revision, Local: sealed.Local, Issues: sealed.Bodies}
	before, err := previous.RepairFactsDigest()
	if err != nil {
		return
	}
	after, err := next.RepairFactsDigest()
	if err != nil || before == after {
		return
	}
	grant, err := c.Store.RepairGrantCommand(ctx, record.Repository)
	if err != nil {
		return
	}
	charge := factory.RepairCharge{ID: factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, next.Number), GrantID: grant.ID, Kind: factory.RepairChargeRun, EpisodeID: record.EpisodeID, Cycle: next.Number, EvidenceDigest: next.InventoryDigest, ActiveMinutes: stallEpisodeActiveMinutes, SpentUnix: time.Now().Unix()}
	charges, err := c.Store.RepairCharges(ctx, record.Repository)
	if err != nil {
		return
	}
	for _, prior := range charges {
		if prior.ID == charge.ID {
			if prior.EvidenceDigest != next.InventoryDigest {
				c.holdRepair(ctx, record, previous, "repair_retained_charge_needs_original_inventory", false)
				return
			}
			charge = prior
		}
	}
	if _, err = c.Store.AdmitStallInvestigation(ctx, record.Repository, charge, time.Now()); err != nil {
		if !errors.Is(err, store.ErrStaleRevision) {
			c.holdRepair(ctx, record, previous, "repair_new_evidence_needs_grant_room", false)
		}
		return
	}
	record.Repair.Cycles = append(record.Repair.Cycles, next)
	record.Repair.Revision++
	_ = c.Store.SaveStallRepairEpisode(ctx, record.StallEvent, record.Repair.Revision-1, record.Repair)
}
