package control

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

func (c *Coordinator) driveDeliveryRepairAdoption(ctx context.Context, record store.DeliveryRepairRecord) {
	cycle := record.Cycle
	progress, err := loadDeliveryRepairAdoption(c.Graph, record)
	if err != nil {
		c.holdDeliveryRepair(ctx, record, cycle, "repair_adoption_journal_invalid", false)
		return
	}
	if c.Graph == nil || c.GraphActorID <= 0 {
		c.holdDeliveryRepair(ctx, record, cycle, "repair_native_graph_unavailable", false)
		return
	}
	// A possibly committed current operation is read before freshness comparison:
	// its lost reply cannot cause either a duplicate effect or a discarded prefix.
	if step, e := progress.Journal.CurrentStep(); e == nil && step.State == reassessmentStepActive && !step.NoEffect {
		entry := progress.Intents[progress.Journal.Cursor]
		generation, e := c.Store.IntentGeneration(ctx, entry.Revision.ID)
		if e != nil {
			c.holdDeliveryRepair(ctx, record, cycle, "repair_operation_unreadable", false)
			return
		}
		if generation.State == "pending" {
			lookup, e := c.Graph.LookupGraph(ctx, generation, record.Cause.Repository)
			if e != nil || lookup.Record == nil && lookup.State != collaboration.LookupNotObserved {
				c.holdDeliveryRepair(ctx, record, cycle, "repair_operation_unresolved", false)
				return
			}
			if lookup.Record != nil {
				if _, e = c.recordAcceptedGenerationOutcome(ctx, generation, *lookup.Record); e != nil {
					c.holdDeliveryRepair(ctx, record, cycle, "repair_operation_refused", false)
					return
				}
			}
		}
	}
	fresh := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, record.Cause.Repository, 0, nil)
	if fresh.hold != "" {
		c.holdDeliveryRepair(ctx, record, cycle, fresh.hold, false)
		return
	}
	if c.matchGraphRepairEffects(ctx, cycle.Local, cycle.Issues, cycle.NativeRevision, cycle.ID, true, progress, *fresh.sealed) != nil {
		c.holdDeliveryRepair(ctx, record, cycle, "repair_inputs_changed_after_prefix", false)
		return
	}
	if progress.Journal.Releasable() == nil {
		if c.Store.DeletePriorityScan(ctx) != nil {
			return
		}
		cycle.State, cycle.Reason = factory.RepairAdopted, "repair_graph_adopted_awaiting_fresh_readiness"
		_ = c.saveDeliveryRepairCycle(ctx, record, cycle)
		for _, issue := range fresh.sealed.Bodies {
			_ = c.Store.EnqueueReadinessRootEvent(ctx, record.Cause.Repository, issue.Index)
		}
		return
	}
	step, err := progress.Journal.CurrentStep()
	if err != nil {
		return
	}
	entry := progress.Intents[progress.Journal.Cursor]
	if step.State == reassessmentStepStaged {
		if !c.currentDeliveryRepairSponsor(ctx, record) {
			c.holdDeliveryRepair(ctx, record, cycle, "repair_sponsor_grant_unavailable", false)
			return
		}
		nativeRev, e := c.Graph.CheckIntentOwner(ctx, record.Cause.Repository, entry.Revision.Owner, c.GraphActorID)
		if e != nil {
			c.holdDeliveryRepair(ctx, record, cycle, "repair_owner_unavailable", false)
			return
		}
		payload, e := canonicalGraphPacket(c.Graph, entry.Packet.Payload)
		if e != nil {
			return
		}
		generation := factory.IntentGeneration{RevisionID: entry.Revision.ID, IntentID: entry.Revision.Intent, OperationID: step.OperationID, ActorID: c.GraphActorID, NativeRev: nativeRev, AuthRevision: entry.Revision.ID + ":" + entry.Revision.Digest(), NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: payload, SubmittedTasks: entry.Revision.Tasks, TaskIssueIDs: entry.TaskIssueIDs, IntentDigest: entry.Revision.Digest(), State: "pending", RepairCycle: cycle.ID, CopiedBindings: entry.Packet.CopiedBindings, RetainedBindings: entry.RetainedBindings, LocalOnly: step.NoEffect}
		if e = c.inheritGraphVerification(ctx, entry.Revision, &generation); e != nil {
			c.holdDeliveryRepair(ctx, record, cycle, "repair_verification_manifest_changed", false)
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
		if c.Store.ActivateDeliveryRepairIntent(ctx, record.Cause.Repository, record.Cause.FailedVerificationRunID, cycle.ID, cycle.AdoptionProgress, next, entry.Revision, generation) != nil {
			c.holdDeliveryRepair(ctx, record, cycle, "repair_activation_deferred", false)
		}
		return
	}
	generation, err := c.GenerateAcceptedIntent(ctx, entry.Revision.Intent, entry.Revision.ID, entry.Revision.Owner)
	if err != nil || generation.State != "completed" {
		c.holdDeliveryRepair(ctx, record, cycle, "repair_adoption_deferred", false)
		return
	}
	if wait, err := c.validateDeliveryRepairGeneration(ctx, generation); err != nil || wait != nil {
		c.holdDeliveryRepair(ctx, record, cycle, "repair_adoption_binding_changed", false)
		return
	}
	if progress.Journal.MarkStepCommitted(entry.Revision.Intent, entry.Packet.PacketDigest) != nil {
		return
	}
	cycle.AdoptionProgress, err = json.Marshal(progress)
	if err == nil {
		_ = c.saveDeliveryRepairCycle(ctx, record, cycle)
	}
}

func (c *Coordinator) validateDeliveryRepairGeneration(ctx context.Context, generation factory.IntentGeneration) (*DispatchWait, error) {
	if generation.ReassessmentCycle != "" || generation.PlanAssignment != "" || !factory.ValidID(generation.RepairCycle) {
		return nil, errors.New("ambiguous delivery repair graph owner")
	}
	record, cycle, err := c.Store.DeliveryRepairCycle(ctx, generation.RepairCycle)
	if err != nil {
		return nil, err
	}
	if cycle.State != factory.RepairApplying || !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
		return nil, errors.New("repair generation lacks its applying accepted cycle")
	}
	progress, err := loadDeliveryRepairAdoption(c.Graph, record)
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
	fresh := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, record.Cause.Repository, 0, nil)
	if fresh.hold != "" {
		return &DispatchWait{Reason: WaitInputsUnavailable, Detail: fresh.hold}, nil
	}
	return nil, c.matchGraphRepairEffects(ctx, cycle.Local, cycle.Issues, cycle.NativeRevision, cycle.ID, true, progress, *fresh.sealed)
}

func loadDeliveryRepairAdoption(codec factory.GraphCodec, record store.DeliveryRepairRecord) (reassessmentAdoptionProgress, error) {
	p, err := loadGraphRepairAdoption(codec, record.Cycle.AdoptionProgress, record.Cause.Repository, "", "", record.Cycle.ID, record.Cycle.InventoryDigest)
	if err != nil {
		return p, err
	}
	if p.Journal.DeliveryID != record.Subject.DeliveryID || p.Journal.FailedVerificationRunID != record.Subject.FailedVerificationRunID {
		return p, errors.New("delivery repair journal answers another failure")
	}
	return p, nil
}

func (c *Coordinator) holdDeliveryRepair(ctx context.Context, record store.DeliveryRepairRecord, cycle factory.StallRepairCycle, reason string, _ bool) {
	record.Cycle = cycle
	c.saveDeliveryRepair(ctx, record, cycle.State, reason)
}

func (c *Coordinator) saveDeliveryRepairCycle(ctx context.Context, record store.DeliveryRepairRecord, cycle factory.StallRepairCycle) error {
	record.Cycle = cycle
	record.Revision++
	return c.Store.SaveDeliveryRepairRecord(ctx, record, record.Revision-1)
}
