package control

import (
	"github.com/levitateos/sodaos/internal/factory"
)

// dispatchCoder turns the recorded decision into its one funded root.
// The packet owns the cycle transition and sole-run ledger: a wait here
// leaves the decision intact, and replay of a committed packet never
// launches or charges another coder.
func (d *decidingDrive) dispatchCoder() {
	d.report.AwaitingCoder = append(d.report.AwaitingCoder, d.cycleID)
	if d.c.Host == nil || d.c.Broker == nil {
		d.report.Held = append(d.report.Held, d.cycleID+":reassessment_dispatch_unavailable")
		return
	}
	if _, err := DecisionStateFromRecord(d.record); err != nil {
		d.attend("reassessment_evidence_invalid")
		return
	}
	issues := make([]NativeInventoryIssue, 0, len(d.record.Evidence))
	for _, body := range d.record.Evidence {
		raw, err := body.CanonicalData()
		if err != nil {
			d.attend("reassessment_evidence_invalid")
			return
		}
		issues = append(issues, NativeInventoryIssue{Index: body.Index, Data: raw})
	}
	prompt, err := RenderReassessmentPrompt(ReassessmentPromptInventory{
		Issues: issues, CompletionID: d.record.Cycle.EventID, CycleID: d.cycleID,
		InventoryDigest: d.record.InventoryDigest, Local: d.record.Local,
		Repository: d.record.Cycle.Repository, Generation: d.record.Cycle.Generation,
		Complete: true,
	})
	if err != nil {
		d.attend("reassessment_prompt_invalid")
		return
	}
	occupancy, err := snapshotOccupancy(d.ctx, d.c.Store)
	if err != nil {
		d.report.Held = append(d.report.Held, d.cycleID+":reassessment_occupancy_unavailable")
		return
	}
	subject := factory.ExecutionSubject{
		Kind: factory.SubjectCompletion,
		Completion: &factory.CompletionSubject{
			CompletionID: d.record.Cycle.EventID, CycleID: d.cycleID,
			InventoryDigest: d.record.InventoryDigest,
		},
	}
	report := newDispatchReport()
	DispatchTimeOnlyRoot(d.ctx, d.c.dispatchDeps(), &occupancy, d.record.Cycle.Repository,
		subject, d.record.Local.SourceCommit, prompt, "", &report)
	for _, wait := range report.Waits {
		d.report.Held = append(d.report.Held, d.cycleID+":"+wait.Reason)
	}
	for _, failure := range report.Errors {
		d.report.Held = append(d.report.Held, d.cycleID+":"+failure.Reason)
	}
}
