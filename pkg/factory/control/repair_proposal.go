package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

const repairProposalRubric = "Does this complete Strategist repair proposal explain the factual stall causes, preserve accepted scope and all protected work, account for every sealed issue, and make justified graph changes that address those causes? Select accept only when the proposal and deterministic validation support the repair; otherwise select needs_attention. A no-change report cannot clear a real stall."

type repairProposalState struct {
	Input      factory.CurrentRepairContext         `json:"input"`
	Proposal   factory.StallRepairReport            `json:"proposal"`
	Validation factory.ReassessmentValidationReport `json:"validation"`
}

func repairCurrentInput(record store.StallRepairRecord, cycle factory.StallRepairCycle) factory.CurrentRepairContext {
	input := factory.CurrentRepairContext{StallID: record.ID, EpisodeID: record.EpisodeID, CycleID: cycle.ID, Cycle: cycle.Number, Cause: record.Reason, InventoryDigest: cycle.InventoryDigest, NativeRevision: cycle.NativeRevision, Local: cycle.Local, Issues: cycle.Issues, ChargeID: factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, cycle.Number), Complete: true, State: cycle.State, PriorCycles: []factory.CurrentRepairCycle{}}
	for _, prior := range record.Repair.Cycles {
		if prior.Number < cycle.Number {
			input.PriorCycles = append(input.PriorCycles, factory.CurrentRepairCycle{ID: prior.ID, Number: prior.Number, State: prior.State, Reason: prior.Reason, InventoryDigest: prior.InventoryDigest})
		}
	}
	return input
}

func (c *Coordinator) stallCoderReport(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) (*factory.StallRepairReport, error) {
	if cycle.ValidateFor(record.StallEvent) != nil || cycle.AssignmentID != cycle.ID || cycle.RunID != factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, cycle.Number) {
		return nil, errors.New("repair sole worker binding invalid")
	}
	run, err := c.Store.FactoryRun(ctx, cycle.RunID)
	if err != nil {
		return nil, err
	}
	a, err := c.Store.AssignmentByRun(ctx, cycle.RunID)
	if err != nil {
		return nil, err
	}
	if !run.Reconciled || a.Stage == factory.AssignmentAssigned {
		return nil, nil
	}
	if run.Outcome != factory.Succeeded || a.Stage != factory.AssignmentFinished || a.Outcome != factory.Succeeded || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil {
		return nil, errors.New("repair investigator did not complete")
	}
	result := a.Result.PhaseResult
	if a.ID != cycle.AssignmentID || a.Repository != record.Repository || a.Phase != project.ExecutionRepair || a.Role != project.RoleCoder || a.SourceCommit != cycle.Local.SourceCommit || a.Subject.Stall == nil || a.Subject.Stall.StallID != record.ID || a.Subject.Stall.CycleID != cycle.ID || a.Subject.Stall.InventoryDigest != cycle.InventoryDigest || result.BindAssignment(a) != nil || result.Status != "completed" || result.Output == nil || result.Output.Repair == nil {
		return nil, errors.New("repair result differs from admitted cycle")
	}
	proposal := result.Output.Repair
	if proposal.Validate() != nil || proposal.PreparationDigest != cycle.Local.PreparationDigest {
		return nil, errors.New("repair report binding invalid")
	}
	for _, source := range proposal.Inspected {
		if source.Commit != cycle.Local.SourceCommit {
			return nil, errors.New("repair inspected source differs from sealed source")
		}
	}
	return proposal, nil
}

func (c *Coordinator) saveRepairCycle(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) error {
	for i := range record.Repair.Cycles {
		if record.Repair.Cycles[i].ID == cycle.ID {
			record.Repair.Cycles[i] = cycle
			record.Repair.Revision++
			return c.Store.SaveStallRepairEpisode(ctx, record.StallEvent, record.Repair.Revision-1, record.Repair)
		}
	}
	return errors.New("repair cycle missing")
}

func (c *Coordinator) holdRepair(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle, reason string, attention bool) {
	cycle.Reason = reason
	if attention {
		cycle.State = factory.RepairAttention
	}
	_ = c.saveRepairCycle(ctx, record, cycle)
}

func (c *Coordinator) driveStallRepairResult(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) {
	proposal, err := c.stallCoderReport(ctx, record, cycle)
	if err != nil {
		c.holdRepair(ctx, record, cycle, "repair_investigator_unusable", true)
		return
	}
	if proposal == nil {
		return
	}
	if cycle.State == factory.RepairInvestigating {
		cycle.State = factory.RepairAssessing
		cycle.Reason = ""
		_ = c.saveRepairCycle(ctx, record, cycle)
		return
	}
	if proposal.Outcome != factory.RepairOutcomeRepair || len(proposal.IntentChanges) == 0 || len(proposal.Attention) > 0 {
		c.holdRepair(ctx, record, cycle, "real_stall_needs_changed_graph_or_owner_attention", true)
		return
	}
	fresh := bracketMaterialCollect(ctx, c.completionDriveDeps(), record.Repository, record.ProgressGeneration, nil)
	if fresh.hold != "" {
		c.holdRepair(ctx, record, cycle, fresh.hold, false)
		return
	}
	if CheckCompletionFreshness(freshnessFromBindings(cycle.Local, cycle.InventoryDigest), freshnessFromBindings(fresh.sealed.Local, fresh.sealed.Digest)) != nil {
		c.holdRepair(ctx, record, cycle, "repair_inputs_changed_before_proposal", true)
		return
	}
	priors, authority, changed, err := c.graphRepairInputs(ctx, cycle.Local, cycle.Issues, cycle.RunID, "", proposal.IntentChanges)
	if err != nil {
		c.holdRepair(ctx, record, cycle, "repair_authority_unusable", true)
		return
	}
	packets, validation, journal, err := validateStallRepairAdoption(c.Graph, *proposal, record, cycle, priors, authority, changed)
	if err != nil {
		c.holdRepair(ctx, record, cycle, "repair_proposal_invalid", true)
		return
	}
	if cycle.ProposalOption != factory.ReassessmentOptionAccept {
		if !c.currentRepairSponsor(ctx, record, cycle) {
			c.holdRepair(ctx, record, cycle, "repair_sponsor_grant_unavailable", true)
			return
		}
		if cycle.JudgmentFingerprint == "" && (c.Planning == nil || c.Planning.Chooser == nil) {
			c.holdRepair(ctx, record, cycle, "repair_assessment_unavailable", false)
			return
		}
		state := repairProposalState{Input: repairCurrentInput(record, cycle), Proposal: *proposal, Validation: validation}
		raw, err := json.Marshal(state)
		if err != nil || len(raw) > factory.MaxReassessmentDecisionBytes+factory.MaxPhaseResultBytes+factory.MaxReassessmentValidationBytes {
			c.holdRepair(ctx, record, cycle, "repair_assessment_oversize", true)
			return
		}
		answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{Repository: record.Repository, Purpose: factory.JudgmentStallRepairProposal, State: state, Question: repairProposalRubric, Options: reassessmentProposalOptions(), Outcome: "stall_repair_proposal_assessed", Admit: func(ctx context.Context, fingerprint string) (bool, error) {
			return c.Store.AdmitStallRepairJudgment(ctx, record.Repository, record.ID, cycle.ID, fingerprint, time.Now())
		}})
		if err != nil {
			// A failed call retries on the next pass; anything else needs attention.
			attention := !errors.Is(err, ErrConsultationFailed)
			latest, e := c.Store.StallRepairRecord(ctx, record.Repository, record.ID)
			if e == nil {
				for _, next := range latest.Repair.Cycles {
					if next.ID == cycle.ID {
						c.holdRepair(ctx, latest, next, "repair_assessment_unavailable", attention)
					}
				}
			}
			return
		}
		// Reservation changes the private retained ledger; reload its current CAS.
		latest, err := c.Store.StallRepairRecord(ctx, record.Repository, record.ID)
		if err != nil {
			return
		}
		for _, next := range latest.Repair.Cycles {
			if next.ID == cycle.ID {
				cycle = next
			}
		}
		cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence = answer.Selected, answer.Probabilities[factory.ReassessmentOptionAccept], answer.Confidence
		if !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
			c.holdRepair(ctx, latest, cycle, "repair_proposal_needs_attention", true)
			return
		}
		_ = c.saveRepairCycle(ctx, latest, cycle)
		return
	}
	progress, err := buildGraphRepairAdoption(c.Graph, proposal.IntentChanges, priors, packets, validation, journal)
	if err != nil {
		c.holdRepair(ctx, record, cycle, "repair_staging_invalid", true)
		return
	}
	cycle.AdoptionProgress, err = json.Marshal(progress)
	if err != nil {
		return
	}
	cycle.State, cycle.Reason = factory.RepairApplying, ""
	_ = c.saveRepairCycle(ctx, record, cycle)
}

// Grant withdrawal holds genuinely new paid calls and graph activations.
// An already admitted immutable operation keeps its receipt/recovery owner.
func (c *Coordinator) currentRepairSponsor(ctx context.Context, record store.StallRepairRecord, cycle factory.StallRepairCycle) bool {
	grant, err := c.Store.RepairGrantCommand(ctx, record.Repository)
	if err != nil || !grant.Active {
		return false
	}
	a, err := c.Store.Assignment(ctx, cycle.AssignmentID)
	return err == nil && a.ActorID == grant.Sponsor && a.Run == cycle.RunID && a.Subject.Stall != nil && a.Subject.Stall.StallID == record.ID && a.Subject.Stall.CycleID == cycle.ID
}

func validateStallRepairAdoption(codec factory.GraphCodec, proposal factory.StallRepairReport, record store.StallRepairRecord, cycle factory.StallRepairCycle, priors map[string]ReassessmentPrior, authority ReassessmentAuthority, changed []int64) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error) {
	var validation factory.ReassessmentValidationReport
	journal := ReassessmentJournal{StallID: record.ID, CycleID: cycle.ID, InventoryDigest: cycle.InventoryDigest}
	indexes := make([]int64, 0, len(cycle.Issues))
	for _, issue := range cycle.Issues {
		indexes = append(indexes, issue.Index)
	}
	if proposal.Validate() != nil || proposal.StallID != record.ID || proposal.CycleID != cycle.ID || proposal.InventoryDigest != cycle.InventoryDigest || proposal.SourceCommit != cycle.Local.SourceCommit || proposal.PreparationDigest != cycle.Local.PreparationDigest || len(proposal.Attention) > 0 || proposal.AccountingHolds(indexes, changed) != nil {
		return nil, validation, journal, errors.New("repair proposal does not cover its exact sealed inventory")
	}
	packets := make([]ReassessmentIntentPacket, 0, len(proposal.IntentChanges))
	for _, change := range proposal.IntentChanges {
		packet, err := preflightReassessmentIntent(codec, change, priors, authority, proposal.Coverage)
		if err != nil {
			return nil, validation, journal, err
		}
		packets = append(packets, packet)
	}
	validation, err := graphRepairValidation(proposal, proposal.InventoryDigest, proposal.Coverage, proposal.IntentChanges, proposal.RetainedIssueIndexes, packets)
	if err != nil {
		return nil, validation, journal, err
	}
	for _, packet := range packets {
		operation := ""
		if !packet.NoEffect {
			operation = "repair-" + cycle.ID + "-" + packet.IntentID
		}
		journal.Steps = append(journal.Steps, ReassessmentJournalStep{IntentID: packet.IntentID, SuccessorRevision: packet.SuccessorRevision, PacketDigest: packet.PacketDigest, OperationID: operation, State: reassessmentStepStaged, NoEffect: packet.NoEffect})
	}
	// An unchanged graph is not a repair even when its revision identifier changed.
	effective := false
	for _, packet := range packets {
		if !packet.NoEffect || len(packet.ReboundKeys) > 0 || len(packet.RetiredKeys) > 0 {
			effective = true
		}
	}
	if !effective {
		return nil, validation, journal, errors.New("repair graph has no effect")
	}
	return packets, validation, journal, nil
}
