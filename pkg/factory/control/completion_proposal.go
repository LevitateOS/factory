package control

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

const reassessmentProposalRubric = "Does this complete reassessment proposal coherently preserve the accepted product scope and protected work, account for the entire sealed inventory, and make only justified changes? Select accept only when the proposal and deterministic validation support it; otherwise select needs_attention."

type ReassessmentProposalState struct {
	Inventory  ReassessmentDecisionState            `json:"inventory"`
	Proposal   factory.ReassessmentReport           `json:"proposal"`
	Validation factory.ReassessmentValidationReport `json:"validation"`
}

func (c *Coordinator) holdCompletion(ctx context.Context, record store.ReassessmentRecord, reason string, report *CompletionDriveReport, attention bool) {
	expected := record.Cycle.State
	if attention {
		if err := record.Cycle.AdvanceTo(factory.ReassessmentAttention); err != nil {
			report.Held = append(report.Held, record.Cycle.CycleID+":attention_refused")
			return
		}
		record.Cycle.State = factory.ReassessmentAttention
	}
	record.WaitReason = reason
	if err := c.Store.CasReassessmentRecord(ctx, record.Cycle.CycleID, expected, record); err != nil {
		if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrNotFound) {
			return
		}
		reason = "completion_hold_unrecorded"
	}
	report.Held = append(report.Held, record.Cycle.CycleID+":"+reason)
}

// completionCoderReport accepts only the sole admitted, settled coder's
// typed result. A live assignment waits; a failed or unbound terminal
// result requires explicit attention rather than another automatic run.
func (c *Coordinator) completionCoderReport(ctx context.Context, record store.ReassessmentRecord) (*factory.ReassessmentReport, error) {
	if len(record.Cycle.Ledger.CoderRuns) != 1 {
		return nil, errors.New("reassessment_coder_not_admitted")
	}
	runID := record.Cycle.Ledger.CoderRuns[0]
	run, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	a, err := c.Store.AssignmentByRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !run.Reconciled || a.Stage == factory.AssignmentAssigned {
		return nil, nil
	}
	if run.Outcome != factory.Succeeded || a.Stage != factory.AssignmentFinished || a.Outcome != factory.Succeeded || a.Result == nil || !a.Result.Reported || a.Result.PhaseResult == nil {
		return nil, errors.New("reassessment_coder_failed")
	}
	if a.Phase != project.ExecutionReassess || a.Role != project.RoleCoder || a.Repository != record.Cycle.Repository || a.SourceCommit != record.Local.SourceCommit || a.Subject.Completion == nil || a.Subject.Completion.CycleID != record.Cycle.CycleID || a.Subject.Completion.CompletionID != record.Cycle.EventID || a.Subject.Completion.InventoryDigest != record.InventoryDigest {
		return nil, errors.New("reassessment_coder_binding_invalid")
	}
	result := a.Result.PhaseResult
	if result.Phase != project.ExecutionReassess || result.Status != "completed" || result.Output == nil || result.Output.Reassessment == nil {
		return nil, errors.New("reassessment_report_missing")
	}
	proposal := result.Output.Reassessment
	if err := proposal.Validate(); err != nil {
		return nil, errors.New("reassessment_report_invalid")
	}
	localDigest, err := record.Local.Digest()
	if err != nil || proposal.CompletionID != record.Cycle.EventID || proposal.CycleID != record.Cycle.CycleID || proposal.InventoryDigest != record.InventoryDigest || proposal.SourceCommit != record.Local.SourceCommit || proposal.PreparationDigest != record.Local.PreparationDigest || proposal.LocalInputDigest != localDigest {
		return nil, errors.New("reassessment_report_binding_invalid")
	}
	for _, source := range proposal.Inspected {
		if source.Commit != record.Local.SourceCommit {
			return nil, errors.New("reassessment_report_source_invalid")
		}
	}
	return proposal, nil
}

func (c *Coordinator) driveReassessmentCoder(ctx context.Context, record store.ReassessmentRecord, report *CompletionDriveReport) {
	proposal, err := c.completionCoderReport(ctx, record)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_coder_unusable", report, true)
		return
	}
	if proposal == nil {
		report.AwaitingCoder = append(report.AwaitingCoder, record.Cycle.CycleID)
		return
	}
	if err := record.Cycle.AdvanceTo(factory.ReassessmentAssessing); err != nil {
		c.holdCompletion(ctx, record, "reassessment_assessment_refused", report, true)
		return
	}
	record.Cycle.State, record.WaitReason = factory.ReassessmentAssessing, ""
	if err := c.Store.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentReassessing, record); err != nil {
		report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_assessment_deferred")
	}
}

func reassessmentProposalOptions() map[string]any {
	return map[string]any{
		factory.ReassessmentOptionAccept:         map[string]any{"effect": "adopt the validated coherent revision set, or release its confirmed no-change result"},
		factory.ReassessmentOptionNeedsAttention: map[string]any{"effect": "hold the completion for explicit attention without applying the proposal"},
	}
}

func (c *Coordinator) driveReassessmentProposal(ctx context.Context, deps CompletionDriveDeps, record store.ReassessmentRecord, report *CompletionDriveReport) {
	proposal, err := c.completionCoderReport(ctx, record)
	if err != nil || proposal == nil {
		c.holdCompletion(ctx, record, "reassessment_report_unusable", report, true)
		return
	}
	// Reuse the same complete, revision-bracketed inventory capture used
	// by continue. A proposal never applies against its own old hashes.
	var saved *InventoryCollector
	if len(record.CollectorProgress) != 0 {
		var progress InventoryCollector
		if json.Unmarshal(record.CollectorProgress, &progress) == nil {
			saved = &progress
		}
	}
	fresh := bracketMaterialCollect(ctx, deps, record.Cycle.Repository, record.Cycle.Generation, saved)
	if fresh.hold != "" {
		record.CollectorProgress = nil
		if fresh.progress != nil {
			record.CollectorProgress, _ = json.Marshal(fresh.progress)
		}
		c.holdCompletion(ctx, record, fresh.hold, report, false)
		return
	}
	if err := CheckCompletionFreshness(freshnessFromBindings(record.Local, record.InventoryDigest), freshnessFromBindings(fresh.sealed.Local, fresh.sealed.Digest)); err != nil {
		c.holdCompletion(ctx, record, "reassessment_inputs_changed", report, true)
		return
	}
	priors, authority, changed, err := c.reassessmentInputs(ctx, record, *proposal)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_authority_unusable", report, true)
		return
	}
	indexes := make([]int64, 0, len(record.Evidence))
	for _, issue := range record.Evidence {
		indexes = append(indexes, issue.Index)
	}
	bindings := ReassessmentFreshness{SourceCommit: record.Local.SourceCommit, PreparationDigest: record.Local.PreparationDigest, ScopeDigestValid: true, AuthorityValid: true}
	// Structural preflight happens before the proposal call is funded.
	packets, validation, journal, err := preflightReassessmentAdoption(c.Graph, *proposal, priors, authority,
		bindings, record.InventoryDigest, indexes, changed, record.Cycle.EventID, record.Cycle.CycleID)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_proposal_invalid", report, true)
		return
	}
	if record.Cycle.Proposal != factory.ReassessmentOptionAccept {
		c.assessReassessmentProposal(ctx, record, *proposal, validation, report)
		return
	}
	if len(packets) != 0 {
		c.stageReassessmentAdoption(ctx, record, *proposal, priors, packets, validation, journal, report)
		return
	}
	if err := journal.Releasable(); err != nil {
		c.holdCompletion(ctx, record, "reassessment_adoption_incomplete", report, false)
		return
	}
	c.releaseReassessment(ctx, record, report)
}

func (c *Coordinator) assessReassessmentProposal(ctx context.Context, record store.ReassessmentRecord, proposal factory.ReassessmentReport, validation factory.ReassessmentValidationReport, report *CompletionDriveReport) {
	if c.Planning == nil || c.Planning.Chooser == nil {
		c.holdCompletion(ctx, record, "reassessment_assessment_unavailable", report, false)
		return
	}
	inventory, err := DecisionStateFromRecord(record)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_evidence_invalid", report, true)
		return
	}
	state := ReassessmentProposalState{Inventory: inventory, Proposal: proposal, Validation: validation}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > factory.MaxReassessmentDecisionBytes+factory.MaxPhaseResultBytes+factory.MaxReassessmentValidationBytes {
		c.holdCompletion(ctx, record, "reassessment_proposal_oversize", report, true)
		return
	}
	answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{
		Repository: record.Cycle.Repository,
		Purpose:    factory.JudgmentPurpose(factory.ReassessmentProposalPurpose), State: state,
		Question: reassessmentProposalRubric, Options: reassessmentProposalOptions(), Outcome: "reassessment_proposal_assessed",
		Admit: func(ctx context.Context, fingerprint string) (bool, error) {
			return c.Store.AdmitReassessmentCall(ctx, record.Cycle.CycleID, factory.ReassessmentAssessing, fingerprint[:32], factory.ReassessmentProposalPurpose, record.Cycle.Decision, fingerprint)
		},
	})
	if err != nil {
		// A failed call retries on the next drive; anything else needs attention.
		c.holdCompletion(ctx, record, "reassessment_proposal_unavailable", report, !errors.Is(err, ErrConsultationFailed))
		return
	}
	cycle, err := record.Cycle.RecordProposal(answer.Selected, answer.Probabilities[factory.ReassessmentOptionAccept], answer.Confidence)
	if err != nil {
		c.holdCompletion(ctx, record, "reassessment_proposal_needs_attention", report, true)
		return
	}
	record.Cycle, record.WaitReason, record.CollectorProgress = cycle, "", nil
	if err := c.Store.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentAssessing, record); err != nil {
		report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_proposal_deferred")
	}
	// Effects and no-change release happen on the next fresh inventory
	// observation, after this external judgment has finished.
}

func (c *Coordinator) releaseReassessment(ctx context.Context, record store.ReassessmentRecord, report *CompletionDriveReport) {
	if err := record.Cycle.AdvanceTo(factory.ReassessmentReleased); err != nil {
		c.holdCompletion(ctx, record, "reassessment_release_refused", report, false)
		return
	}
	if err := c.Store.DeletePriorityScan(ctx); err != nil {
		c.holdCompletion(ctx, record, "reassessment_release_deferred", report, false)
		return
	}
	if err := c.Store.EnqueueReadinessRootEvent(ctx, record.Cycle.Repository, record.Issue); err != nil {
		c.holdCompletion(ctx, record, "reassessment_release_deferred", report, false)
		return
	}
	expected := record.Cycle.State
	record.Cycle.State, record.WaitReason, record.CollectorProgress = factory.ReassessmentReleased, "", nil
	if err := c.Store.CasReassessmentRecord(ctx, record.Cycle.CycleID, expected, record); err != nil {
		report.Held = append(report.Held, record.Cycle.CycleID+":reassessment_release_deferred")
		return
	}
	report.Released = append(report.Released, record.Cycle.CycleID)
}
