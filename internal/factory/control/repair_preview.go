package control

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// PreviewRepair evaluates the existing deterministic adoption preflight against
// the active worker's sealed cause. It records nothing, consults no model and
// grants no graph authority. Actual adoption still requires its funded judgment
// and another fresh preflight under the durable adoption owner.
func (c *Coordinator) PreviewRepair(ctx context.Context, cap factory.MCPCapability, raw json.RawMessage) (factory.RepairPreview, error) {
	var out factory.RepairPreview
	if c == nil || c.Store == nil || cap.Profile != factory.MCPProfileStrategist || cap.State != factory.MCPCapabilityActive || cap.Validate() != nil || len(raw) == 0 || len(raw) > factory.MCPMaxSubmissionPayload {
		return out, errors.New("repair preview requires an active bound Strategist")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	a, err := c.Store.Assignment(bounded, cap.AssignmentID)
	if err != nil {
		return out, err
	}
	checkCurrent := func() error {
		current, err := c.Store.FactoryCurrentContext(bounded, cap, time.Now())
		if err != nil {
			return err
		}
		if current.Binding.Stage != factory.AssignmentAssigned || current.Run == nil || !current.Run.Active || current.Run.Settled || current.Run.Reconciled || current.Allowance == nil || current.Allowance.Closed || current.Allowance.DeadlineRemainingSeconds <= 0 {
			return errors.New("repair preview worker is no longer active")
		}
		switch {
		case a.Subject.Completion != nil:
			if current.Role.Strategy != nil && current.Role.Strategy.Complete {
				return nil
			}
		case a.Subject.Stall != nil:
			if current.Role.Repair != nil && current.Role.Repair.Complete {
				return nil
			}
		case a.Subject.DeliveryRepair != nil:
			if current.Role.DeliveryRepair != nil && current.Role.DeliveryRepair.Complete {
				return nil
			}
		}
		return errors.New("repair preview lacks complete admitted cause inputs")
	}
	if err := checkCurrent(); err != nil {
		return out, err
	}
	var local factory.CompletionLocal
	var inventory string
	var generation int64
	var changes []factory.ReassessmentIntentChange
	var delivered string
	var validate func(map[string]ReassessmentPrior, ReassessmentAuthority, []int64) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error)
	switch {
	case a.Subject.Completion != nil:
		record, err := c.Store.ReassessmentRecord(bounded, a.Subject.Completion.CycleID)
		if err != nil {
			return out, err
		}
		var proposal factory.ReassessmentReport
		if err := strictDecodeField(raw, &proposal); err != nil {
			return out, err
		}
		digest, err := record.Local.Digest()
		if err != nil || proposal.CompletionID != record.Cycle.EventID || proposal.CycleID != record.Cycle.CycleID || proposal.InventoryDigest != record.InventoryDigest || proposal.SourceCommit != record.Local.SourceCommit || proposal.PreparationDigest != record.Local.PreparationDigest || proposal.LocalInputDigest != digest {
			return out, errors.New("repair preview differs from admitted completion")
		}
		for _, ref := range proposal.Inspected {
			if ref.Commit != record.Local.SourceCommit {
				return out, errors.New("repair preview inspected another source")
			}
		}
		local, inventory, generation, changes, delivered = record.Local, record.InventoryDigest, record.Cycle.Generation, proposal.IntentChanges, record.Completion.AcceptanceID
		indexes := make([]int64, 0, len(record.Evidence))
		for _, issue := range record.Evidence {
			indexes = append(indexes, issue.Index)
		}
		validate = func(priors map[string]ReassessmentPrior, authority ReassessmentAuthority, changed []int64) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error) {
			return preflightReassessmentAdoption(c.Graph, proposal, priors, authority, ReassessmentFreshness{SourceCommit: local.SourceCommit, PreparationDigest: local.PreparationDigest, ScopeDigestValid: true, AuthorityValid: true}, inventory, indexes, changed, record.Cycle.EventID, record.Cycle.CycleID)
		}
	case a.Subject.Stall != nil:
		record, cycle, err := c.Store.StallRepairCycle(bounded, a.Subject.Stall.CycleID)
		if err != nil {
			return out, err
		}
		var proposal factory.StallRepairReport
		if err := strictDecodeField(raw, &proposal); err != nil {
			return out, err
		}
		if proposal.StallID != record.ID || proposal.CycleID != cycle.ID || proposal.InventoryDigest != cycle.InventoryDigest || proposal.SourceCommit != cycle.Local.SourceCommit || proposal.PreparationDigest != cycle.Local.PreparationDigest {
			return out, errors.New("repair preview differs from admitted stall")
		}
		for _, ref := range proposal.Inspected {
			if ref.Commit != cycle.Local.SourceCommit {
				return out, errors.New("repair preview inspected another source")
			}
		}
		local, inventory, generation, changes = cycle.Local, cycle.InventoryDigest, record.ProgressGeneration, proposal.IntentChanges
		validate = func(priors map[string]ReassessmentPrior, authority ReassessmentAuthority, changed []int64) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error) {
			return validateStallRepairAdoption(c.Graph, proposal, record, cycle, priors, authority, changed)
		}
	case a.Subject.DeliveryRepair != nil:
		record, _, err := c.Store.DeliveryRepairCycle(bounded, a.Subject.DeliveryRepair.CycleID)
		if err != nil {
			return out, err
		}
		var proposal factory.DeliveryRepairReport
		if err := strictDecodeField(raw, &proposal); err != nil {
			return out, err
		}
		if proposal.Subject != record.Subject || proposal.InputDigest != cap.ScopeDigest() {
			return out, errors.New("repair preview differs from admitted failed delivery")
		}
		local, inventory, changes = record.Cycle.Local, record.Cycle.InventoryDigest, proposal.IntentChanges
		validate = func(priors map[string]ReassessmentPrior, authority ReassessmentAuthority, changed []int64) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error) {
			return validateDeliveryRepairAdoption(c.Graph, proposal, record, priors, authority, changed)
		}
	default:
		return out, errors.New("repair preview subject is unavailable")
	}
	out = factory.RepairPreview{Subject: cap.Subject, InputDigest: cap.ScopeDigest(), SourceCommit: local.SourceCommit, InventoryDigest: inventory, Effects: []factory.RepairPreviewEffect{}, NextStep: "Structural feedback only; submit the typed report for the existing judgment and adoption process. No readiness or graph change is confirmed."}
	observe := func() ([]factory.CompletionIssueEvidence, error) {
		fresh := bracketMaterialCollect(bounded, CompletionDriveDeps{Enumerator: c.CompletionEnumerator, CaptureLocal: c.CompletionLocalCapture}, cap.Repository, generation, nil)
		if fresh.hold != "" || fresh.sealed == nil {
			return nil, errors.New("repair preview inventory is unavailable or unstable")
		}
		if err := CheckCompletionFreshness(freshnessFromBindings(local, inventory), freshnessFromBindings(fresh.sealed.Local, fresh.sealed.Digest)); err != nil {
			return nil, err
		}
		return fresh.sealed.Bodies, nil
	}
	bodies, err := observe()
	if err != nil {
		return out, err
	}
	priors, authority, changed, err := c.graphRepairInputs(bounded, local, bodies, cap.RunID, delivered, changes)
	if err != nil {
		return out, err
	}
	packets, validation, _, validationErr := validate(priors, authority, changed)
	if _, err := observe(); err != nil {
		return out, err
	}
	if err := checkCurrent(); err != nil {
		return out, err
	}
	if validationErr != nil {
		out.Reason = validationErr.Error()
		return out, nil
	}
	out.Valid, out.Validation = true, &validation
	for _, packet := range packets {
		facts, err := inspectGraphPacket(c.Graph, packet.Payload)
		if err != nil {
			return out, err
		}
		effect := factory.RepairPreviewEffect{IntentID: packet.IntentID, SuccessorRevision: packet.SuccessorRevision, PacketDigest: packet.PacketDigest, NoEffect: packet.NoEffect, Creates: len(facts.Creates), Edits: len(facts.Edits), DependencyChanges: facts.DependencyChanges, PreservedTasks: []string{}, ReboundTasks: append([]string{}, packet.ReboundKeys...), RetiredTasks: append([]string{}, packet.RetiredKeys...)}
		for _, binding := range packet.CopiedBindings {
			effect.PreservedTasks = append(effect.PreservedTasks, binding.TaskKey)
		}
		sort.Strings(effect.PreservedTasks)
		sort.Strings(effect.ReboundTasks)
		sort.Strings(effect.RetiredTasks)
		out.Effects = append(out.Effects, effect)
	}
	sort.Slice(out.Effects, func(i, j int) bool { return out.Effects[i].IntentID < out.Effects[j].IntentID })
	encoded, err := json.Marshal(out)
	if err != nil || len(encoded) > factory.MCPMaxResultPayloadBytes {
		return factory.RepairPreview{}, errors.New("repair preview exceeds its response bound")
	}
	return out, nil
}
