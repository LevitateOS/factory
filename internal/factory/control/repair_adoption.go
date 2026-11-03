package control

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

func (c *Coordinator) driveStallRepairAdoption(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) {
	progress, err := loadGraphRepairAdoption(c.Graph, cycle.AdoptionProgress, record.Repository, "", record.ID, cycle.ID, cycle.InventoryDigest)
	if err != nil {
		c.holdRepair(ctx, record, cycle, "repair_adoption_journal_invalid", false)
		return
	}
	if c.Graph == nil || c.GraphActorID <= 0 {
		c.holdRepair(ctx, record, cycle, "repair_native_graph_unavailable", false)
		return
	}
	// A possibly committed current operation is read before freshness comparison:
	// its lost reply cannot cause either a duplicate effect or a discarded prefix.
	if step, e := progress.Journal.CurrentStep(); e == nil && step.State == reassessmentStepActive && !step.NoEffect {
		entry := progress.Intents[progress.Journal.Cursor]
		generation, e := c.Store.IntentGeneration(ctx, entry.Revision.ID)
		if e != nil {
			c.holdRepair(ctx, record, cycle, "repair_operation_unreadable", false)
			return
		}
		if generation.State == "pending" {
			lookup, e := c.Graph.LookupGraph(ctx, generation, record.Repository)
			if e != nil || lookup.Record == nil && lookup.State != collaboration.LookupNotObserved {
				c.holdRepair(ctx, record, cycle, "repair_operation_unresolved", false)
				return
			}
			if lookup.Record != nil {
				if _, e = c.recordAcceptedGenerationOutcome(ctx, generation, *lookup.Record); e != nil {
					c.holdRepair(ctx, record, cycle, "repair_operation_refused", false)
					return
				}
			}
		}
	}
	fresh := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, record.Repository, record.ProgressGeneration, nil)
	if fresh.hold != "" {
		c.holdRepair(ctx, record, cycle, fresh.hold, false)
		return
	}
	if c.matchGraphRepairEffects(ctx, cycle.Local, cycle.Issues, cycle.NativeRevision, cycle.ID, true, progress, *fresh.sealed) != nil {
		c.holdRepair(ctx, record, cycle, "repair_inputs_changed_after_prefix", false)
		return
	}
	if progress.Journal.Releasable() == nil {
		if c.Store.DeletePriorityScan(ctx) != nil {
			return
		}
		cycle.State, cycle.Reason = factory.RepairAdopted, "repair_graph_adopted_awaiting_fresh_readiness"
		_ = c.saveRepairCycle(ctx, record, cycle)
		for _, issue := range fresh.sealed.Bodies {
			_ = c.Store.EnqueueReadinessRootEvent(ctx, record.Repository, issue.Index)
		}
		return
	}
	step, err := progress.Journal.CurrentStep()
	if err != nil {
		return
	}
	entry := progress.Intents[progress.Journal.Cursor]
	if step.State == reassessmentStepStaged {
		if !c.currentRepairSponsor(ctx, record, cycle) {
			c.holdRepair(ctx, record, cycle, "repair_sponsor_grant_unavailable", false)
			return
		}
		nativeRev, e := c.Graph.CheckIntentOwner(ctx, record.Repository, entry.Revision.Owner, c.GraphActorID)
		if e != nil {
			c.holdRepair(ctx, record, cycle, "repair_owner_unavailable", false)
			return
		}
		payload, e := canonicalGraphPacket(c.Graph, entry.Packet.Payload)
		if e != nil {
			return
		}
		generation := factory.IntentGeneration{RevisionID: entry.Revision.ID, IntentID: entry.Revision.Intent, OperationID: step.OperationID, ActorID: c.GraphActorID, NativeRev: nativeRev, AuthRevision: entry.Revision.ID + ":" + entry.Revision.Digest(), NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: payload, SubmittedTasks: entry.Revision.Tasks, TaskIssueIDs: entry.TaskIssueIDs, IntentDigest: entry.Revision.Digest(), State: "pending", RepairCycle: cycle.ID, CopiedBindings: entry.Packet.CopiedBindings, RetainedBindings: entry.RetainedBindings, LocalOnly: step.NoEffect}
		if e = c.inheritGraphVerification(ctx, entry.Revision, &generation); e != nil {
			c.holdRepair(ctx, record, cycle, "repair_verification_manifest_changed", false)
			return
		}
		if generation.LocalOnly {
			generation.State = "completed"
		}
		if _, e = progress.Journal.MarkStepActive(entry.Revision.Intent); e != nil {
			return
		}
		next, e := json.Marshal(progress)
		if e != nil {
			return
		}
		if c.Store.ActivateStallRepairIntent(ctx, record.Repository, record.ID, cycle.ID, cycle.AdoptionProgress, next, entry.Revision, generation) != nil {
			c.holdRepair(ctx, record, cycle, "repair_activation_deferred", false)
		}
		return
	}
	generation, err := c.GenerateAcceptedIntent(ctx, entry.Revision.Intent, entry.Revision.ID, entry.Revision.Owner)
	if err != nil || generation.State != "completed" {
		c.holdRepair(ctx, record, cycle, "repair_adoption_deferred", false)
		return
	}
	if wait, err := c.validateStallRepairGeneration(ctx, generation); err != nil || wait != nil {
		c.holdRepair(ctx, record, cycle, "repair_adoption_binding_changed", false)
		return
	}
	if progress.Journal.MarkStepCommitted(entry.Revision.Intent, entry.Packet.PacketDigest) != nil {
		return
	}
	cycle.AdoptionProgress, err = json.Marshal(progress)
	if err == nil {
		_ = c.saveRepairCycle(ctx, record, cycle)
	}
}

func (c *Coordinator) validateStallRepairGeneration(ctx context.Context, generation factory.IntentGeneration) (*DispatchWait, error) {
	if _, _, err := c.Store.DeliveryRepairCycle(ctx, generation.RepairCycle); err == nil {
		return c.validateDeliveryRepairGeneration(ctx, generation)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if generation.ReassessmentCycle != "" || generation.PlanAssignment != "" || !factory.ValidID(generation.RepairCycle) {
		return nil, errors.New("ambiguous stall graph owner")
	}
	record, cycle, err := c.Store.StallRepairCycle(ctx, generation.RepairCycle)
	if err != nil {
		return nil, err
	}
	if cycle.State != factory.RepairApplying || !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
		return nil, errors.New("repair generation lacks its applying accepted cycle")
	}
	progress, err := loadGraphRepairAdoption(c.Graph, cycle.AdoptionProgress, record.Repository, "", record.ID, cycle.ID, cycle.InventoryDigest)
	if err != nil {
		return nil, err
	}
	if err = progress.Journal.GuardRecoveryStep(generation.IntentID); err != nil {
		return nil, err
	}
	entry := progress.Intents[progress.Journal.Cursor]
	step := progress.Journal.Steps[progress.Journal.Cursor]
	raw, err := canonicalGraphPacket(c.Graph, entry.Packet.Payload)
	if err != nil || step.State != reassessmentStepActive || generation.RevisionID != entry.Revision.ID || generation.IntentDigest != entry.Revision.Digest() || generation.OperationID != step.OperationID || generation.ActorID != c.GraphActorID || !reflect.DeepEqual(generation.SubmittedTasks, entry.Revision.Tasks) || !maps.Equal(generation.TaskIssueIDs, entry.TaskIssueIDs) || !reflect.DeepEqual(generation.CopiedBindings, entry.Packet.CopiedBindings) || !reflect.DeepEqual(generation.RetainedBindings, entry.RetainedBindings) || generation.LocalOnly != entry.Packet.NoEffect || !sameEncodedJSON(generation.Payload, raw) {
		return nil, errors.New("repair operation differs from its approved current step")
	}
	if err = c.currentAcceptedIntent(ctx, entry.Revision); err != nil {
		return nil, err
	}
	fresh := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, record.Repository, record.ProgressGeneration, nil)
	if fresh.hold != "" {
		return &DispatchWait{Reason: WaitInputsUnavailable, Detail: fresh.hold}, nil
	}
	return nil, c.matchGraphRepairEffects(ctx, cycle.Local, cycle.Issues, cycle.NativeRevision, cycle.ID, true, progress, *fresh.sealed)
}
