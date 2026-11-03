package control

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

type taskJournalSource interface {
	FactoryTaskEvents(context.Context, project.FactoryTaskEvents) (project.FactoryTaskEventsState, error)
}

// importRunTaskEvents imports one bounded native journal page. The original
// launch bytes, rather than the current mutable plan head, bind its exact plan
// and profile; the native observation supplies the process invocation. An empty
// page establishes that all currently observed events have been persisted.
func (c *Coordinator) importRunTaskEvents(ctx context.Context, run factory.Run, state execution.RunObservation) (drained, stop bool, err error) {
	if len(run.ExecutionManifest) == 0 {
		return true, false, nil
	}
	source, ok := c.Host.(taskJournalSource)
	if !ok {
		return false, false, errors.New("native task journal unavailable")
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	if err != nil {
		return false, false, err
	}
	if manifest.Run != run.ID || !manifest.Deadline.Equal(run.Deadline) ||
		state.Run != run.ID || state.Workspace != run.ProjectID || state.InvocationID() == "" {
		return false, false, ErrTaskEventBinding
	}
	var plan factory.InvestigationPlan
	if err := json.Unmarshal(manifest.Plan, &plan); err != nil {
		return false, false, err
	}
	stored, err := c.Store.InvestigationPlan(ctx, plan.AttemptRoot, plan.ID)
	if err != nil {
		return false, false, err
	}
	if stored.Digest != plan.Digest || plan.SourceCommit != run.InputSHA {
		return false, false, ErrTaskEventBinding
	}
	receipts, err := c.Store.PlanTaskReceipts(ctx, plan.AttemptRoot, plan.ID)
	if err != nil {
		return false, false, err
	}
	cursor := importCursor(receipts)
	page, err := source.FactoryTaskEvents(ctx, project.FactoryTaskEvents{
		Project: run.ProjectID, ID: run.ID, AfterSequence: cursor, ByteLimit: project.MaxTaskEventsBytes,
	})
	if err != nil {
		return false, false, err
	}
	if err := page.Validate(); err != nil {
		return false, false, err
	}
	if page.ID != run.ID || page.Project != run.ProjectID || page.NextSequence <= cursor {
		return false, false, ErrTaskEventBinding
	}
	result, err := ImportTaskEvents(plan, receipts, TaskEventImport{
		AttemptRoot: plan.AttemptRoot, PlanID: plan.ID, Invocation: state.InvocationID(),
		PlanDigest: project.ExecutionManifestDigest(run.ExecutionManifest), MaxChildren: manifest.MaxChildren, Events: page.Events,
		Phase: manifest.Phase,
	})
	if err != nil {
		return false, false, err
	}
	if page.NextSequence != result.NextSequence+1 {
		return false, false, ErrTaskEventGap
	}
	if err := c.Store.SavePlanTaskReceipts(ctx, result.Writes); err != nil {
		return false, false, err
	}
	return len(page.Events) == 0, result.Stop, nil
}
