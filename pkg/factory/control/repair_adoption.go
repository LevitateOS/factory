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

func (c *Coordinator) driveStallRepairAdoption(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) {
	c.driveRepairAdoption(ctx, repairAdoptionCause{stall: &record, cycle: cycle})
}

func (c *Coordinator) driveRepairAdoption(ctx context.Context, cause repairAdoptionCause) {
	cycle := cause.cycle
	progress, err := cause.load(c.Graph)
	if err != nil {
		cause.hold(ctx, c, cycle, "repair_adoption_journal_invalid")
		return
	}
	if c.Graph == nil || c.GraphActorID <= 0 {
		cause.hold(ctx, c, cycle, "repair_native_graph_unavailable")
		return
	}
	// A possibly committed current operation is read before freshness comparison:
	// its lost reply cannot cause either a duplicate effect or a discarded prefix.
	if step, e := progress.Journal.CurrentStep(); e == nil && step.State == reassessmentStepActive && !step.NoEffect {
		entry := progress.Intents[progress.Journal.Cursor]
		generation, e := c.Store.IntentGeneration(ctx, entry.Revision.ID)
		if e != nil {
			cause.hold(ctx, c, cycle, "repair_operation_unreadable")
			return
		}
		if generation.State == "pending" {
			lookup, e := c.Graph.LookupGraph(ctx, generation, cause.repository())
			if e != nil || lookup.Record == nil && lookup.State != collaboration.LookupNotObserved {
				cause.hold(ctx, c, cycle, "repair_operation_unresolved")
				return
			}
			if lookup.Record != nil {
				if _, e = c.recordAcceptedGenerationOutcome(ctx, generation, *lookup.Record); e != nil {
					cause.hold(ctx, c, cycle, "repair_operation_refused")
					return
				}
			}
		}
	}
	fresh := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, cause.repository(), cause.progressGeneration(), nil)
	if fresh.hold != "" {
		cause.hold(ctx, c, cycle, fresh.hold)
		return
	}
	if c.matchGraphRepairEffects(ctx, cycle.Local, cycle.Issues, cycle.NativeRevision, cycle.ID, true, progress, *fresh.sealed) != nil {
		cause.hold(ctx, c, cycle, "repair_inputs_changed_after_prefix")
		return
	}
	if progress.Journal.Releasable() == nil {
		if c.Store.DeletePriorityScan(ctx) != nil {
			return
		}
		cycle.State, cycle.Reason = factory.RepairAdopted, "repair_graph_adopted_awaiting_fresh_readiness"
		_ = cause.save(ctx, c, cycle)
		for _, issue := range fresh.sealed.Bodies {
			_ = c.Store.EnqueueReadinessRootEvent(ctx, cause.repository(), issue.Index)
		}
		return
	}
	step, err := progress.Journal.CurrentStep()
	if err != nil {
		return
	}
	entry := progress.Intents[progress.Journal.Cursor]
	if step.State == reassessmentStepStaged {
		if !cause.sponsored(ctx, c, cycle) {
			cause.hold(ctx, c, cycle, "repair_sponsor_grant_unavailable")
			return
		}
		nativeRev, e := c.Graph.CheckIntentOwner(ctx, cause.repository(), entry.Revision.Owner, c.GraphActorID)
		if e != nil {
			cause.hold(ctx, c, cycle, "repair_owner_unavailable")
			return
		}
		payload, e := canonicalGraphPacket(c.Graph, entry.Packet.Payload)
		if e != nil {
			return
		}
		generation := factory.IntentGeneration{RevisionID: entry.Revision.ID, IntentID: entry.Revision.Intent, OperationID: step.OperationID, ActorID: c.GraphActorID, NativeRev: nativeRev, AuthRevision: entry.Revision.ID + ":" + entry.Revision.Digest(), NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: payload, SubmittedTasks: entry.Revision.Tasks, TaskIssueIDs: entry.TaskIssueIDs, IntentDigest: entry.Revision.Digest(), State: "pending", RepairCycle: cycle.ID, CopiedBindings: entry.Packet.CopiedBindings, RetainedBindings: entry.RetainedBindings, LocalOnly: step.NoEffect}
		if e = c.inheritGraphVerification(ctx, entry.Revision, &generation); e != nil {
			cause.hold(ctx, c, cycle, "repair_verification_manifest_changed")
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
		if cause.activate(ctx, c, cycle, next, entry.Revision, generation) != nil {
			cause.hold(ctx, c, cycle, "repair_activation_deferred")
		}
		return
	}
	generation, err := c.GenerateAcceptedIntent(ctx, entry.Revision.Intent, entry.Revision.ID, entry.Revision.Owner)
	if err != nil || generation.State != "completed" {
		cause.hold(ctx, c, cycle, "repair_adoption_deferred")
		return
	}
	if wait, err := cause.validate(ctx, c, generation); err != nil || wait != nil {
		cause.hold(ctx, c, cycle, "repair_adoption_binding_changed")
		return
	}
	if progress.Journal.MarkStepCommitted(entry.Revision.Intent, entry.Packet.PacketDigest) != nil {
		return
	}
	cycle.AdoptionProgress, err = json.Marshal(progress)
	if err == nil {
		_ = cause.save(ctx, c, cycle)
	}
}

func (c *Coordinator) validateStallRepairGeneration(ctx context.Context, generation factory.IntentGeneration) (*DispatchWait, error) {
	if generation.ReassessmentCycle != "" || generation.PlanAssignment != "" || !factory.ValidID(generation.RepairCycle) {
		return nil, errors.New("ambiguous stall graph owner")
	}
	record, cycle, err := c.Store.StallRepairCycle(ctx, generation.RepairCycle)
	if err != nil {
		return nil, err
	}
	return c.validateRepairAdoptionGeneration(ctx, generation, repairAdoptionCause{stall: &record, cycle: cycle})
}

// Retained generations identify either cause with RepairCycle alone. Resolve
// that historical representation only at generic recovery, never in a typed
// stall/delivery adoption path.
func (c *Coordinator) validateRepairGeneration(ctx context.Context, generation factory.IntentGeneration) (*DispatchWait, error) {
	if _, _, err := c.Store.DeliveryRepairCycle(ctx, generation.RepairCycle); err == nil {
		return c.validateDeliveryRepairGeneration(ctx, generation)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return c.validateStallRepairGeneration(ctx, generation)
}

func (c *Coordinator) validateRepairAdoptionGeneration(ctx context.Context, generation factory.IntentGeneration, cause repairAdoptionCause) (*DispatchWait, error) {
	cycle := cause.cycle
	if cycle.State != factory.RepairApplying || !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
		return nil, errors.New("repair generation lacks its applying accepted cycle")
	}
	progress, err := cause.load(c.Graph)
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
	fresh := bracketMaterialCollect(ctx, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, cause.repository(), cause.progressGeneration(), nil)
	if fresh.hold != "" {
		return &DispatchWait{Reason: WaitInputsUnavailable, Detail: fresh.hold}, nil
	}
	return nil, c.matchGraphRepairEffects(ctx, cycle.Local, cycle.Issues, cycle.NativeRevision, cycle.ID, true, progress, *fresh.sealed)
}

// Stall episodes and delivery failures retain separate journals and atomic
// activation transactions while sharing the adoption procedure.
type repairAdoptionCause struct {
	stall    *store.StallRepairRecord
	delivery *store.DeliveryRepairRecord
	cycle    factory.StallRepairCycle
}

func (cause repairAdoptionCause) repository() int64 {
	if cause.stall != nil {
		return cause.stall.Repository
	}
	return cause.delivery.Cause.Repository
}

func (cause repairAdoptionCause) progressGeneration() int64 {
	if cause.stall != nil {
		return cause.stall.ProgressGeneration
	}
	return 0
}

func (cause repairAdoptionCause) load(codec factory.GraphCodec) (reassessmentAdoptionProgress, error) {
	if cause.stall != nil {
		cycle := cause.cycle
		return loadGraphRepairAdoption(codec, cycle.AdoptionProgress, cause.stall.Repository, "", cause.stall.ID, cycle.ID, cycle.InventoryDigest)
	}
	return loadDeliveryRepairAdoption(codec, *cause.delivery)
}

func (cause repairAdoptionCause) hold(ctx context.Context, c *Coordinator, cycle factory.StallRepairCycle, reason string) {
	if cause.stall != nil {
		c.holdRepair(ctx, *cause.stall, cycle, reason, false)
		return
	}
	c.holdDeliveryRepair(ctx, *cause.delivery, cycle, reason)
}

func (cause repairAdoptionCause) save(ctx context.Context, c *Coordinator, cycle factory.StallRepairCycle) error {
	if cause.stall != nil {
		return c.saveRepairCycle(ctx, *cause.stall, cycle)
	}
	return c.saveDeliveryRepairCycle(ctx, *cause.delivery, cycle)
}

func (cause repairAdoptionCause) sponsored(ctx context.Context, c *Coordinator, cycle factory.StallRepairCycle) bool {
	if cause.stall != nil {
		return c.currentRepairSponsor(ctx, *cause.stall, cycle)
	}
	return c.currentDeliveryRepairSponsor(ctx, *cause.delivery)
}

func (cause repairAdoptionCause) activate(ctx context.Context, c *Coordinator, cycle factory.StallRepairCycle, next []byte, revision factory.IntentRevision, generation factory.IntentGeneration) error {
	if cause.stall != nil {
		return c.Store.ActivateStallRepairIntent(ctx, cause.stall.Repository, cause.stall.ID, cycle.ID, cycle.AdoptionProgress, next, revision, generation)
	}
	return c.Store.ActivateDeliveryRepairIntent(ctx, cause.delivery.Cause.Repository, cause.delivery.Cause.FailedVerificationRunID, cycle.ID, cycle.AdoptionProgress, next, revision, generation)
}

func (cause repairAdoptionCause) validate(ctx context.Context, c *Coordinator, generation factory.IntentGeneration) (*DispatchWait, error) {
	if cause.stall != nil {
		return c.validateStallRepairGeneration(ctx, generation)
	}
	return c.validateDeliveryRepairGeneration(ctx, generation)
}
