package control

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// A terminal proposal must not finish its assignment while trusted checks
// still need that assignment's active capability. Keep the proposal recorded
// until its exact native final receipt is available; publication separately
// re-observes the native binding before any export.
func checkpointTerminalReady(ctx context.Context, st MCPTerminalSettleStore, finished factory.Assignment) (bool, string, error) {
	if finished.Outcome != factory.Succeeded || finished.Result == nil ||
		(finished.Phase != project.ExecutionImplement && finished.Phase != project.ExecutionCorrect) {
		return true, "", nil
	}
	run, err := st.FactoryRun(ctx, finished.Run)
	if err != nil {
		return false, "", err
	}
	if len(run.ExecutionManifest) == 0 {
		return true, "", nil
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	var plan factory.InvestigationPlan
	if err != nil || manifest.Run != finished.Run || manifest.Phase != finished.Phase ||
		json.Unmarshal(manifest.Plan, &plan) != nil || plan.AttemptRoot != finished.AttemptRoot || plan.SourceCommit != finished.SourceCommit {
		return false, PlanRefuseEvidence, nil
	}
	if run.FinalCheckpoint == nil {
		return false, "", nil
	}
	request := run.FinalCheckpoint.Request
	request.ExpectedHead = finished.Result.Candidate
	reason := finalCheckpointReceiptGate(run, plan, request)
	if reason == PlanWaitFinalIntegration {
		return false, "", nil
	}
	return reason == "", reason, nil
}
