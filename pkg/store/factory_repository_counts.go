package store

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// RepositoryDispatchCounts reports exact recorded queue/assignment states.
// Active means assigned dispatch work, not a claim about a live host process.
func (s *Store) RepositoryDispatchCounts(ctx context.Context, repository int64) (queued, active int, err error) {
	if repository < 1 {
		return 0, 0, errors.New("invalid repository dispatch count scope")
	}
	err = s.db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM issue_controls WHERE repository=$1 AND data->>'readiness'=$2),
 (SELECT count(*) FROM factory_assignments WHERE repository=$1 AND stage=$3)`, repository, factory.ReadinessQueued, factory.AssignmentAssigned).Scan(&queued, &active)
	return queued, active, err
}
