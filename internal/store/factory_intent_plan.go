package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// IntentPlanAttempt returns the latest decompose assignment recorded for
// one intent revision, or ErrNotFound when no planning run exists yet.
// The draft planning status read uses it to show pending/reported
// results without exposing any other assignment.
func (s *Store) IntentPlanAttempt(ctx context.Context, intentID, revisionID string) (factory.Assignment, error) {
	var a factory.Assignment
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE data->'subject'->'intent'->>'intent_id'=$1 AND data->'subject'->'intent'->>'revision_id'=$2 ORDER BY seq DESC LIMIT 1`, intentID, revisionID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return factory.Assignment{}, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(data, &a)
	}
	return a, err
}
