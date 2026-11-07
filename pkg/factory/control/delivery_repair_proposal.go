package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

const deliveryRepairRubric = "Does this complete Strategist proposal address the exact recorded failed application outcomes with idle in-scope follow-up work, preserve accepted scope, delivered/manual/active/PR-bound work and origin funding, and account for the entire sealed inventory? Accept only when deterministic protected-graph validation and factual evidence justify the repair; otherwise needs_attention. No-change never verifies a failed application."

type deliveryRepairProposalState struct {
	Input      factory.CurrentDeliveryRepairContext `json:"input"`
	Proposal   factory.DeliveryRepairReport         `json:"proposal"`
	Validation factory.ReassessmentValidationReport `json:"validation"`
}

func (c *Coordinator) driveDeliveryRepairResult(ctx context.Context, record store.DeliveryRepairRecord) {
	cycle := record.Cycle
	run, err := c.Store.FactoryRun(ctx, cycle.RunID)
	if err != nil || !run.Reconciled {
		return
	}
	a, err := c.Store.Assignment(ctx, cycle.AssignmentID)
	if err != nil || a.Stage != factory.AssignmentFinished {
		return
	}
	if a.Outcome != factory.Succeeded || run.Outcome != factory.Succeeded || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil || a.Result.PhaseResult.BindAssignment(a) != nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.DeliveryRepair == nil {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery repair investigator did not complete")
		return
	}
	proposal := a.Result.PhaseResult.Output.DeliveryRepair
	if proposal.Validate() != nil || proposal.Subject != record.Subject || proposal.Outcome != factory.RepairOutcomeRepair || len(proposal.IntentChanges) == 0 || len(proposal.Attention) > 0 {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "failed application needs in-scope changed work or owner attention")
		return
	}
	fresh := bracketMaterialCollect(ctx, c.completionDriveDeps(), record.Cause.Repository, 0, nil)
	if fresh.hold != "" {
		return
	}
	if CheckCompletionFreshness(freshnessFromBindings(cycle.Local, cycle.InventoryDigest), freshnessFromBindings(fresh.sealed.Local, fresh.sealed.Digest)) != nil {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery repair inputs changed before proposal")
		return
	}
	priors, authority, changed, err := c.graphRepairInputs(ctx, cycle.Local, cycle.Issues, cycle.RunID, "", proposal.IntentChanges)
	if err != nil {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery repair authority unavailable")
		return
	}
	packets, validation, journal, err := validateDeliveryRepairAdoption(c.Graph, *proposal, record, priors, authority, changed)
	if err != nil {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery repair proposal invalid")
		return
	}
	if !c.currentDeliveryRepairSponsor(ctx, record) {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery repair sponsor withdrawn")
		return
	}
	if cycle.State == factory.RepairInvestigating {
		c.saveDeliveryRepair(ctx, record, factory.RepairAssessing, "")
		return
	}
	if cycle.ProposalOption == "" {
		input := factory.CurrentDeliveryRepairContext{Subject: record.Subject, Cause: record.Cause, Local: cycle.Local, Issues: cycle.Issues, Complete: true, State: cycle.State}
		state := deliveryRepairProposalState{input, *proposal, validation}
		answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{Repository: record.Cause.Repository, Purpose: factory.JudgmentDeliveryRepairProposal, State: state, Question: deliveryRepairRubric, Options: reassessmentProposalOptions(), Outcome: "delivery_repair_proposal_assessed", Admit: func(ctx context.Context, fingerprint string) (bool, error) {
			return c.Store.AdmitDeliveryRepairJudgment(ctx, record.Cause.Repository, record.Cause.FailedVerificationRunID, cycle.ID, fingerprint, time.Now())
		}})
		if err != nil {
			return
		}
		latest, err := c.Store.DeliveryRepairRecord(ctx, record.Cause.Repository, record.Cause.FailedVerificationRunID)
		if err != nil {
			return
		}
		record = latest
		cycle = record.Cycle
		cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence = answer.Selected, answer.Probabilities[factory.ReassessmentOptionAccept], answer.Confidence
		record.Cycle = cycle
		stateName := factory.RepairAssessing
		reason := ""
		if !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
			stateName, reason = factory.RepairAttention, "delivery repair proposal needs owner attention"
		}
		c.saveDeliveryRepair(ctx, record, stateName, reason)
		return
	}
	if !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
		return
	}
	progress, err := buildGraphRepairAdoption(c.Graph, proposal.IntentChanges, priors, packets, validation, journal)
	if err != nil {
		c.saveDeliveryRepair(ctx, record, factory.RepairAttention, "delivery repair staging invalid")
		return
	}
	record.Cycle.AdoptionProgress, err = json.Marshal(progress)
	if err != nil {
		return
	}
	c.saveDeliveryRepair(ctx, record, factory.RepairApplying, "")
}

func (c *Coordinator) currentDeliveryRepairSponsor(ctx context.Context, record store.DeliveryRepairRecord) bool {
	grant, err := c.Store.RepairGrantCommand(ctx, record.Cause.Repository)
	if err != nil || !grant.Active {
		return false
	}
	a, err := c.Store.Assignment(ctx, record.Cycle.AssignmentID)
	return err == nil && a.ActorID == grant.Sponsor && a.Run == record.Cycle.RunID && a.Subject.DeliveryRepair != nil && *a.Subject.DeliveryRepair == record.Subject
}

func validateDeliveryRepairAdoption(codec factory.GraphCodec, proposal factory.DeliveryRepairReport, record store.DeliveryRepairRecord, priors map[string]ReassessmentPrior, authority ReassessmentAuthority, changed []int64) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error) {
	var validation factory.ReassessmentValidationReport
	journal := ReassessmentJournal{DeliveryID: record.Subject.DeliveryID, FailedVerificationRunID: record.Subject.FailedVerificationRunID, CycleID: record.Subject.CycleID, InventoryDigest: record.Subject.InventoryDigest}
	inventory := []int64{}
	for _, issue := range record.Cycle.Issues {
		inventory = append(inventory, issue.Index)
	}
	if proposal.Validate() != nil || proposal.Subject != record.Subject || proposal.AccountingHolds(inventory, changed) != nil || len(proposal.Attention) > 0 {
		return nil, validation, journal, errors.New("delivery repair does not cover exact inventory")
	}
	failed := map[string]bool{}
	for _, failure := range record.Cause.Report.Failures {
		failed[failure.CheckRef] = true
	}
	for _, cause := range proposal.Causes {
		if !failed[cause.CheckRef] {
			return nil, validation, journal, errors.New("repair cause is outside failed outcomes")
		}
	}
	if len(proposal.Causes) == 0 {
		return nil, validation, journal, errors.New("delivery repair needs a recorded failure cause")
	}
	for _, ref := range proposal.Inspected {
		if ref.Commit != record.Cycle.Local.SourceCommit {
			return nil, validation, journal, errors.New("repair inspected another source")
		}
	}
	packets := []ReassessmentIntentPacket{}
	for _, change := range proposal.IntentChanges {
		if change.IntentID != record.Cause.Delivery.IntentID {
			return nil, validation, journal, errors.New("delivery repair cannot change another intent")
		}
		packet, err := preflightReassessmentIntent(codec, change, priors, authority, proposal.Coverage)
		if err != nil {
			return nil, validation, journal, err
		}
		packets = append(packets, packet)
	}
	var err error
	validation, err = graphRepairValidation(proposal, proposal.Subject.InventoryDigest, proposal.Coverage, proposal.IntentChanges, proposal.RetainedIssueIndexes, packets)
	if err != nil {
		return nil, validation, journal, err
	}
	effective := false
	for _, packet := range packets {
		operation := ""
		if !packet.NoEffect {
			operation = "delivery-repair-" + record.Subject.CycleID + "-" + packet.IntentID
		}
		journal.Steps = append(journal.Steps, ReassessmentJournalStep{IntentID: packet.IntentID, SuccessorRevision: packet.SuccessorRevision, PacketDigest: packet.PacketDigest, OperationID: operation, State: reassessmentStepStaged, NoEffect: packet.NoEffect})
		effective = effective || !packet.NoEffect || len(packet.ReboundKeys) > 0 || len(packet.RetiredKeys) > 0
	}
	if !effective {
		return nil, validation, journal, errors.New("delivery repair graph has no effect")
	}
	return packets, validation, journal, nil
}
