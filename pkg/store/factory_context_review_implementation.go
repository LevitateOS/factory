package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// A publication grants its exact committed implementation evidence to its
// independent reviewer. The existing typed plan projection excludes prompts,
// coding conversations and event logs and stays in this same read snapshot.
func currentPublishedImplementationTx(ctx context.Context, tx *sql.Tx, a, owner factory.Assignment, p factory.Publication, current *factory.CurrentReviewContext) error {
	// Corrections can advance the current head. The original implementation
	// remains bound to the immutable PR creation candidate; current correction
	// facts and the current tuple separately describe the later head.
	if p.PRCreate.Work == nil {
		return errors.New("published implementation lacks its original candidate binding")
	}
	publishedHead := p.PRCreate.Work.Candidate
	var source factory.Assignment
	err := contextJSONRow(ctx, tx, &source, `SELECT data FROM factory_assignments WHERE repository=$1 AND issue=$2 AND data->>'publication_assignment'=$3 AND data->>'phase'='implement' AND data#>>'{result,candidate}'=$4 ORDER BY seq DESC LIMIT 1`, a.Repository, a.Issue, a.PublicationAssignment, publishedHead)
	if errors.Is(err, sql.ErrNoRows) && owner.Result != nil && owner.Result.Candidate == publishedHead {
		return nil // A legacy direct candidate has no execution manifest.
	}
	if err != nil {
		return err
	}
	var run factory.Run
	if err := contextJSONRow(ctx, tx, &run, `SELECT data FROM factory_runs WHERE id=$1`, source.Run); err != nil {
		return err
	}
	if len(run.ExecutionManifest) == 0 {
		return nil
	}
	if source.Phase != project.ExecutionImplement || source.Repository != a.Repository || source.Issue != a.Issue || source.ActorID != a.ActorID || source.ProjectID != a.ProjectID || source.AttemptRoot != a.AttemptRoot || source.PublicationAssignment != a.PublicationAssignment || source.Acceptance != a.Acceptance || source.Stage != factory.AssignmentFinished || source.Outcome != factory.Succeeded || source.Result == nil || !source.Result.Reported || source.Result.Candidate != publishedHead || !run.Reconciled || run.Outcome != factory.Succeeded || run.ProjectID != a.ProjectID {
		return errors.New("published implementation evidence is outside review grant")
	}
	var projected factory.CurrentContext
	if err := currentPlanTx(ctx, tx, source, run, &projected); err != nil {
		return err
	}
	plan := projected.Role.Plan
	if plan == nil || len(projected.Evidence.MissingReasons) != 0 || plan.FinalCheckpoint == nil || plan.FinalCheckpoint.Status != "committed" || plan.FinalCheckpoint.Candidate != publishedHead || len(plan.Receipts) != len(plan.Tasks) {
		return errors.New("published implementation lacks its trusted complete checkpoints")
	}
	for _, receipt := range plan.Receipts {
		if receipt.State != PlanTaskCompleted || receipt.Commit == "" {
			return errors.New("published task evidence is incomplete")
		}
	}
	current.Implementation = plan
	return nil
}
