package control

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) stageStallRepair(ctx context.Context, event factory.StallEvent) error {
	record, err := c.Store.RepairEpisodeRecord(ctx, event.Repository, event.EpisodeID)
	if err != nil {
		return err
	}
	if len(record.Repair.Cycles) > 0 {
		return nil
	}
	episode := factory.StallRepairEpisode{Revision: record.Repair.Revision + 1, Cycles: []factory.StallRepairCycle{{ID: factory.RepairCycleID(record.EpisodeID, 1), Number: 1, State: factory.RepairCollecting}}}
	return c.Store.SaveStallRepairEpisode(ctx, record.StallEvent, record.Repair.Revision, episode)
}

func (c *Coordinator) driveStallRepairs(ctx context.Context, stalled map[int64]bool) {
	if c.Host == nil || c.Broker == nil || c.CompletionEnumerator == nil || c.CompletionLocalCapture == nil {
		return
	}
	records, err := c.Store.PendingStallRepairRecords(ctx, 16)
	if err != nil {
		return
	}
	for _, record := range records {
		if len(record.Repair.Cycles) == 0 {
			continue
		}
		index := len(record.Repair.Cycles) - 1
		cycle := record.Repair.Cycles[index]
		if (cycle.State == factory.RepairCollecting || cycle.State == factory.RepairReady || cycle.State == factory.RepairAttention || cycle.State == factory.RepairAdopted) && !stalled[record.Repository] {
			continue
		}
		switch cycle.State {
		case factory.RepairCollecting:
			var saved *InventoryCollector
			if len(cycle.CollectorProgress) > 0 {
				var progress InventoryCollector
				if json.Unmarshal(cycle.CollectorProgress, &progress) == nil {
					saved = &progress
				}
			}
			bracket := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, record.Repository, record.ProgressGeneration, saved)
			cycle.Reason = bracket.hold
			cycle.CollectorProgress = nil
			if bracket.progress != nil {
				cycle.CollectorProgress, _ = json.Marshal(bracket.progress)
			}
			if bracket.sealed != nil {
				sealed := bracket.sealed
				cycle.State, cycle.Reason, cycle.Local, cycle.Issues, cycle.InventoryDigest, cycle.NativeRevision = factory.RepairReady, "", sealed.Local, sealed.Bodies, sealed.Digest, sealed.Revision
			}
			record.Repair.Cycles[index] = cycle
			record.Repair.Revision++
			_ = c.Store.SaveStallRepairEpisode(ctx, record.StallEvent, record.Repair.Revision-1, record.Repair)
		case factory.RepairReady:
			if err := c.dispatchStallRepair(ctx, record, cycle); err != nil {
				latest, readErr := c.Store.StallRepairRecord(ctx, record.Repository, record.ID)
				if readErr == nil {
					for i := range latest.Repair.Cycles {
						if latest.Repair.Cycles[i].ID == cycle.ID {
							latest.Repair.Cycles[i].Reason = err.Error()
							if len(latest.Repair.Cycles[i].Reason) > 256 {
								latest.Repair.Cycles[i].Reason = "repair launch unavailable"
							}
						}
					}
					latest.Repair.Revision++
					_ = c.Store.SaveStallRepairEpisode(ctx, latest.StallEvent, latest.Repair.Revision-1, latest.Repair)
				}
			}
		case factory.RepairInvestigating, factory.RepairAssessing:
			c.driveStallRepairResult(ctx, record, cycle)
		case factory.RepairApplying:
			c.driveStallRepairAdoption(ctx, record, cycle)
		case factory.RepairAttention, factory.RepairAdopted:
			c.nextStallRepairCycle(ctx, record, cycle)
		}
	}
}

func (c *Coordinator) dispatchStallRepair(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) error {
	if err := cycle.ValidateFor(record.StallEvent); err != nil {
		return err
	}
	projectRecord, err := c.Store.NativeProjectForCollaborationRepository(ctx, collaboration.RepositoryID(record.Repository))
	if err != nil {
		return err
	}
	preparations, err := c.Store.ProjectPreparations(ctx, projectRecord.ID)
	if err != nil {
		return err
	}
	preparation := ""
	for _, candidate := range preparations {
		if candidate.Preparation.Role == project.RoleCoder && candidate.State.Ready && candidate.Preparation.SourceCommit == cycle.Local.SourceCommit {
			preparation = candidate.Preparation.ID
			break
		}
	}
	if preparation == "" {
		return errors.New("repair needs its ready approved source preparation")
	}
	input := factory.CurrentRepairContext{StallID: record.ID, EpisodeID: record.EpisodeID, CycleID: cycle.ID, Cycle: cycle.Number, Cause: record.Reason, InventoryDigest: cycle.InventoryDigest, NativeRevision: cycle.NativeRevision, Local: cycle.Local, Issues: cycle.Issues, ChargeID: factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, cycle.Number), Complete: true}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	prompt := append([]byte("You are Strategist for this bound stall episode. Inspect the complete sealed inputs and factory.context. Diagnose factual causes and propose only authorized changes to idle accepted generated work. Preserve manual, active, PR-bound and completed issue identities, protected routes/bindings, accepted human decisions and origin funding. A graph reshuffle is not new factual evidence; no-change cannot clear a real stall. Do not implement code, publish refs or claim graph effects. Finish with factory.finish_reassessment carrying the exact stall subject and full typed StallRepairReport. Its outcome must be repair, verified_wait, no_change or incomplete. Use no_change for completed diagnosis without graph changes; use incomplete when a required fact or authorization is missing, recording the concrete owner action in attention. These outcomes preserve the real stall. You may instead finish incomplete with a concrete missing fact.\n\n"+reassessmentWorkGuidance+"\n\n"), raw...)
	if len(prompt) > project.MaxReassessPrompt {
		return errors.New("complete repair input exceeds prompt bound")
	}
	occupancy, err := snapshotOccupancy(ctx, c.Store)
	if err != nil {
		return err
	}
	subject := factory.ExecutionSubject{Kind: factory.SubjectStall, Stall: &factory.StallSubject{StallID: record.ID, CycleID: cycle.ID, InventoryDigest: cycle.InventoryDigest}}
	report := newDispatchReport()
	DispatchTimeOnlyRoot(ctx, c.dispatchDeps(), &occupancy, record.Repository, subject, cycle.Local.SourceCommit, prompt, preparation, &report)
	if len(report.Errors) > 0 {
		return errors.New(report.Errors[0].Detail)
	}
	if len(report.Waits) > 0 {
		return errors.New(report.Waits[0].Reason)
	}
	return nil
}
