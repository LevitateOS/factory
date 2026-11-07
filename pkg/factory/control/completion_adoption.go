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

type reassessmentAdoptionIntent struct {
	Revision         factory.IntentRevision      `json:"revision"`
	Packet           ReassessmentIntentPacket    `json:"packet"`
	PriorRevision    string                      `json:"prior_revision"`
	RetainedBindings []factory.IntentTaskBinding `json:"retained_bindings"`
	TaskIssueIDs     map[string]int64            `json:"task_issue_ids"`
}

type reassessmentAdoptionProgress struct {
	Journal    ReassessmentJournal                  `json:"journal"`
	Validation factory.ReassessmentValidationReport `json:"validation"`
	Intents    []reassessmentAdoptionIntent         `json:"intents"`
}

// Protected graph changes retain already adopted check definitions. Only the
// accepted revision/scope binding changes; a different effective outcome needs
// a new normal Product Lead proposal rather than a silent fixed-check fallback.
func (c *Coordinator) inheritGraphVerification(ctx context.Context, revision factory.IntentRevision, generation *factory.IntentGeneration) error {
	history, err := c.Store.IntentHistory(ctx, revision.Intent, revision.Owner, 50)
	if err != nil {
		return err
	}
	if len(history) == 0 || history[0].ID != revision.Predecessor {
		return store.ErrStaleRevision
	}
	for i, prior := range history {
		g, e := c.Store.IntentGeneration(ctx, prior.ID)
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if e == nil && g.VerificationManifest != nil {
			m := *g.VerificationManifest
			if m.ValidateFor(prior) != nil || !reflect.DeepEqual(prior.EffectiveOutcomes, revision.EffectiveOutcomes) || !reflect.DeepEqual(prior.Ideas, revision.Ideas) {
				return errors.New("protected graph changed adopted verification outcomes")
			}
			m.AcceptedRevision, m.ScopeDigest = revision.ID, revision.Digest()
			if err = m.ValidateFor(revision); err != nil {
				return err
			}
			generation.VerificationManifest = &m
			generation.VerificationManifestDigest = m.Digest()
			return nil
		}
		if i == len(history)-1 && prior.Predecessor != "" {
			return errors.New("verification manifest ancestry exceeds its read bound")
		}
	}
	return nil
}

func (c *Coordinator) stageReassessmentAdoption(ctx context.Context, record store.ReassessmentRecord, proposal factory.ReassessmentReport, priors map[string]ReassessmentPrior, packets []ReassessmentIntentPacket, validation factory.ReassessmentValidationReport, journal ReassessmentJournal, report *CompletionDriveReport) {
	progress, err := buildGraphRepairAdoption(c.Graph, proposal.IntentChanges, priors, packets, validation, journal)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_successor_invalid", report, true)
		return
	}
	raw, err := json.Marshal(progress)
	if err != nil || record.Cycle.AdvanceTo(factory.ReassessmentApplying) != nil {
		c.holdCompletion(ctx, record, "reassessment_staging_refused", report, true)
		return
	}
	record.AdoptionProgress, record.Cycle.State, record.WaitReason, record.CollectorProgress = raw, factory.ReassessmentApplying, "", nil
	if err := c.Store.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentAssessing, record); err != nil {
		report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_staging_deferred")
	}
}

func loadReassessmentAdoption(codec factory.GraphCodec, record store.ReassessmentRecord) (reassessmentAdoptionProgress, error) {
	return loadGraphRepairAdoption(codec, record.AdoptionProgress, record.Cycle.Repository, record.Cycle.EventID, "", record.Cycle.CycleID, record.InventoryDigest)
}

func loadGraphRepairAdoption(codec factory.GraphCodec, raw json.RawMessage, repository int64, completionID, stallID, cycleID, inventoryDigest string) (reassessmentAdoptionProgress, error) {
	var progress reassessmentAdoptionProgress
	if len(raw) == 0 || json.Unmarshal(raw, &progress) != nil || progress.Journal.CompletionID != completionID || progress.Journal.StallID != stallID || progress.Journal.CycleID != cycleID || progress.Journal.InventoryDigest != inventoryDigest || len(progress.Intents) != len(progress.Journal.Steps) || (completionID != "" || stallID != "") && (progress.Journal.DeliveryID != "" || progress.Journal.FailedVerificationRunID != "") {
		return progress, errors.New("reassessment adoption journal invalid")
	}
	for i, entry := range progress.Intents {
		step := progress.Journal.Steps[i]
		if entry.Revision.Validate() != nil || entry.Revision.ID != step.SuccessorRevision || entry.Revision.Intent != step.IntentID || entry.Revision.Predecessor != entry.PriorRevision || entry.Packet.PacketDigest != step.PacketDigest || entry.Packet.IntentID != step.IntentID || entry.Packet.NoEffect != step.NoEffect || entry.Revision.Repository != repository {
			return progress, errors.New("reassessment adoption step invalid")
		}
		raw, err := canonicalGraphPacket(codec, entry.Packet.Payload)
		if err != nil || packetDigest(raw) != step.PacketDigest {
			return progress, errors.New("reassessment adoption packet changed")
		}
	}
	return progress, progress.Validation.Validate()
}

func (c *Coordinator) driveReassessmentAdoption(ctx context.Context, deps CompletionDriveDeps, record store.ReassessmentRecord, report *CompletionDriveReport) {
	progress, err := loadReassessmentAdoption(c.Graph, record)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_journal_invalid", report, false)
		return
	}
	if !c.graphAvailable() {
		c.holdCompletion(ctx, record, "reassessment_native_graph_unavailable", report, false)
		return
	}
	// Observe a possibly committed operation before comparing current
	// native evidence. A lost submit reply must recover its exact receipt
	// rather than treating the approved effect as unrelated drift.
	if step, err := progress.Journal.CurrentStep(); err == nil && step.State == reassessmentStepActive && !step.NoEffect {
		entry := progress.Intents[progress.Journal.Cursor]
		generation, err := c.Store.IntentGeneration(ctx, entry.Revision.ID)
		if err != nil {
			c.holdCompletion(ctx, record, "reassessment_operation_unreadable", report, false)
			return
		}
		if generation.State == "pending" {
			lookup, err := c.Graph.LookupGraph(ctx, generation, entry.Revision.Repository)
			if err != nil || lookup.Record == nil && lookup.State != collaboration.LookupNotObserved {
				c.holdCompletion(ctx, record, "reassessment_operation_unresolved", report, false)
				return
			}
			if lookup.Record != nil && !lookup.Resumable {
				if _, err := c.recordAcceptedGenerationOutcome(ctx, generation, *lookup.Record); err != nil {
					c.holdCompletion(ctx, record, "reassessment_operation_refused", report, false)
					return
				}
			}
		}
	}
	if !c.freshReassessmentAdoption(ctx, deps, record, progress, report) {
		return
	}
	if progress.Journal.Releasable() == nil {
		c.releaseReassessment(ctx, record, report)
		return
	}
	step, err := progress.Journal.CurrentStep()
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_step_unavailable", report, false)
		return
	}
	entry := progress.Intents[progress.Journal.Cursor]
	if step.State == reassessmentStepStaged {
		actor, err := c.graphActor(ctx, entry.Revision.Repository, entry.Revision.Owner)
		if err != nil {
			c.holdCompletion(ctx, record, "reassessment_owner_unavailable", report, false)
			return
		}
		nativeRev, err := c.Graph.CheckIntentOwner(ctx, entry.Revision.Repository, entry.Revision.Owner, actor)
		if err != nil {
			c.holdCompletion(ctx, record, "reassessment_owner_unavailable", report, false)
			return
		}
		payload, err := canonicalGraphPacket(c.Graph, entry.Packet.Payload)
		if err != nil {
			c.holdCompletion(ctx, record, "reassessment_packet_unavailable", report, false)
			return
		}
		generation := factory.IntentGeneration{
			RevisionID: entry.Revision.ID, IntentID: entry.Revision.Intent, OperationID: step.OperationID,
			ActorID: actor, NativeRev: nativeRev, AuthRevision: entry.Revision.ID + ":" + entry.Revision.Digest(),
			NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: payload, SubmittedTasks: entry.Revision.Tasks,
			TaskIssueIDs: entry.TaskIssueIDs, IntentDigest: entry.Revision.Digest(), State: "pending",
			ReassessmentCycle: record.Cycle.CycleID, CopiedBindings: entry.Packet.CopiedBindings,
			RetainedBindings: entry.RetainedBindings, LocalOnly: step.NoEffect,
		}
		if err = c.inheritGraphVerification(ctx, entry.Revision, &generation); err != nil {
			c.holdCompletion(ctx, record, "reassessment_verification_manifest_changed", report, false)
			return
		}
		if generation.LocalOnly {
			generation.State = "completed"
		}
		if _, err := progress.Journal.MarkStepActive(entry.Revision.Intent); err != nil {
			c.holdCompletion(ctx, record, "reassessment_activation_refused", report, false)
			return
		}
		next, err := json.Marshal(progress)
		if err != nil || c.Store.ActivateReassessmentIntent(ctx, record.Cycle.CycleID, record.AdoptionProgress, next, entry.Revision, generation) != nil {
			report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_activation_deferred")
		}
		return
	}
	// Existing generation recovery owns lookup, conditional submission,
	// exact readback and per-task acceptance. Its cycle validator below
	// prevents generic recovery from running any later staged successor.
	generation, err := c.GenerateAcceptedIntent(ctx, entry.Revision.Intent, entry.Revision.ID, entry.Revision.Owner)
	if errors.Is(err, ErrGraphStepsPending) {
		c.holdCompletion(ctx, record, "reassessment_graph_steps_pending", report, false)
		return
	}
	if err != nil || generation.State != "completed" {
		c.holdCompletion(ctx, record, "reassessment_adoption_deferred", report, false)
		return
	}
	if _, err := c.validateReassessmentGeneration(ctx, generation); err != nil {
		c.holdCompletion(ctx, record, "reassessment_adoption_binding_changed", report, false)
		return
	}
	if err := progress.Journal.MarkStepCommitted(entry.Revision.Intent, entry.Packet.PacketDigest); err != nil {
		c.holdCompletion(ctx, record, "reassessment_readback_refused", report, false)
		return
	}
	record.AdoptionProgress, err = json.Marshal(progress)
	if err != nil || c.Store.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentApplying, record) != nil {
		report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_readback_deferred")
	}
}

func (c *Coordinator) validateReassessmentGeneration(ctx context.Context, generation factory.IntentGeneration) (*DispatchWait, error) {
	record, err := c.Store.ReassessmentRecord(ctx, generation.ReassessmentCycle)
	if err != nil {
		return nil, err
	}
	if record.Cycle.State != factory.ReassessmentApplying || record.Cycle.Proposal != factory.ReassessmentOptionAccept || generation.PlanAssignment != "" {
		return nil, errors.New("reassessment generation is not its applying accepted cycle")
	}
	progress, err := loadReassessmentAdoption(c.Graph, record)
	if err != nil {
		return nil, err
	}
	if err := progress.Journal.GuardRecoveryStep(generation.IntentID); err != nil {
		return nil, err
	}
	entry := progress.Intents[progress.Journal.Cursor]
	step := progress.Journal.Steps[progress.Journal.Cursor]
	actor, err := c.graphActor(ctx, entry.Revision.Repository, entry.Revision.Owner)
	if err != nil {
		return nil, err
	}
	raw, err := canonicalGraphPacket(c.Graph, entry.Packet.Payload)
	if err != nil || step.State != reassessmentStepActive || generation.RevisionID != entry.Revision.ID || generation.IntentDigest != entry.Revision.Digest() || generation.OperationID != step.OperationID || generation.ActorID != actor || !reflect.DeepEqual(generation.SubmittedTasks, entry.Revision.Tasks) || !maps.Equal(generation.TaskIssueIDs, entry.TaskIssueIDs) || !reflect.DeepEqual(generation.CopiedBindings, entry.Packet.CopiedBindings) || !reflect.DeepEqual(generation.RetainedBindings, entry.RetainedBindings) || generation.LocalOnly != entry.Packet.NoEffect || !sameEncodedJSON(generation.Payload, raw) {
		return nil, errors.New("reassessment generation differs from its approved current step")
	}
	if err := c.currentAcceptedIntent(ctx, entry.Revision); err != nil {
		return nil, err
	}
	// Generic graph recovery uses this same validator, so it also proves
	// complete inventory freshness before submission/adoption and cannot
	// bypass the completion driver's prefix accounting.
	deps := c.completionDriveDeps()
	fresh := bracketMaterialCollect(ctx, deps, record.Cycle.Repository, record.Cycle.Generation, nil)
	if fresh.hold != "" {
		return &DispatchWait{Reason: WaitInputsUnavailable, Detail: fresh.hold}, nil
	}
	if err := c.matchReassessmentEffects(ctx, record, progress, *fresh.sealed); err != nil {
		return nil, err
	}
	return nil, nil
}

func sameEncodedJSON(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func buildGraphRepairAdoption(codec factory.GraphCodec, changes []factory.ReassessmentIntentChange, priors map[string]ReassessmentPrior, packets []ReassessmentIntentPacket, validation factory.ReassessmentValidationReport, journal ReassessmentJournal) (reassessmentAdoptionProgress, error) {
	progress := reassessmentAdoptionProgress{Journal: journal, Validation: validation}
	for i, packet := range packets {
		change, prior := changes[i], priors[packet.IntentID]
		revision, err := SuccessorIntentRevision(prior.Revision, change, prior.SuccessorCreatedUnix)
		if err != nil {
			return progress, err
		}
		entry := reassessmentAdoptionIntent{Revision: revision, Packet: packet, PriorRevision: prior.Revision.ID, TaskIssueIDs: map[string]int64{}}
		facts, err := inspectGraphPacket(codec, packet.Payload)
		if err != nil {
			return progress, err
		}
		edited := facts.Edits
		for _, task := range revision.Tasks {
			if binding, ok := prior.Bindings[task.Key]; ok {
				if _, changed := edited[binding.IssueID]; changed {
					entry.TaskIssueIDs[task.Key] = binding.IssueID
				} else {
					binding.RevisionID = revision.ID
					entry.RetainedBindings = append(entry.RetainedBindings, binding)
				}
			}
		}
		for i := range entry.Packet.CopiedBindings {
			entry.Packet.CopiedBindings[i].RevisionID = revision.ID
		}
		progress.Intents = append(progress.Intents, entry)
	}
	return progress, nil
}
