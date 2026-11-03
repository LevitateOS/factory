package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// BindFactoryExecutionManifest retains the exact admitted launch bytes before
// native execution. Supervisor observations preserve this immutable snapshot;
// retries may recover it but cannot replace it with a different plan or profile.
func (s *Store) BindFactoryExecutionManifest(ctx context.Context, launch project.FactoryLaunch) error {
	if err := launch.Validate(); err != nil {
		return err
	}
	manifest, err := project.ParseExecutionManifest(launch.ExecutionManifest)
	if err != nil {
		return err
	}
	if err := manifest.ValidateManifestForRun(launch.Run); err != nil {
		return err
	}
	assignment, err := s.AssignmentByRun(ctx, launch.Run.ID)
	if err != nil {
		return err
	}
	if assignment.Phase != manifest.Phase || assignment.ProjectID != launch.Run.Project ||
		assignment.Role != launch.Run.Role || assignment.PromptSHA != launch.Run.Assignment ||
		assignment.SourceCommit != launch.Run.SourceCommit {
		return errors.New("execution manifest does not bind its assignment")
	}
	var plan factory.InvestigationPlan
	if json.Unmarshal(manifest.Plan, &plan) != nil || plan.Validate() != nil || plan.AttemptRoot != assignment.AttemptRoot || plan.SourceCommit != assignment.SourceCommit {
		return errors.New("execution manifest lacks its exact admitted plan")
	}
	// The controller-generated correction revision is immutable task input;
	// keep the original investigation head while retaining this singleton.
	if manifest.Phase == project.ExecutionCorrect {
		if err := s.SaveInvestigationPlan(ctx, plan); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(launch.ExecutionManifest)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{execution_manifest}',$1::jsonb)
WHERE id=$2 AND data->>'project_id'=$3 AND data->>'role'=$4 AND data->>'input_sha'=$5
AND data->>'deadline'=$6
AND (coalesce(data->'execution_manifest','null'::jsonb)='null'::jsonb OR data->'execution_manifest'=$1::jsonb)`,
		string(encoded), launch.Run.ID, launch.Run.Project, launch.Run.Role, launch.Run.SourceCommit, launch.Run.Deadline.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrStaleRevision
	}
	return err
}
