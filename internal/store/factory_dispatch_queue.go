package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// MaxQueuedDispatch bounds one global queued-issue listing for dispatch
// visits. The dispatch pass visits oldest first under its own tighter
// bound; the store only caps the read.
const MaxQueuedDispatch = 2048

// QueuedControls lists queued readiness records across every repository,
// oldest first seen first with repository and issue breaking ties. The
// order is deterministic: the same records always visit in the same order.
func (s *Store) QueuedControls(ctx context.Context, limit int) ([]factory.IssueControl, error) {
	if limit <= 0 || limit > MaxQueuedDispatch {
		return nil, errors.New("invalid queued control listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM issue_controls
		WHERE data->>'readiness'='queued'
		ORDER BY (data->>'first_seen_unix')::bigint ASC, repository ASC, issue ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var controls []factory.IssueControl
	for rows.Next() {
		var data []byte
		var control factory.IssueControl
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &control); err != nil {
			return nil, err
		}
		controls = append(controls, control)
	}
	return controls, rows.Err()
}

// ActiveRunCounts counts active recorded runs with no dispatch assignment:
// human and fixture runs that still occupy appliance and repository slots.
// Attributed runs count through their held reservations instead, never twice.
func (s *Store) ActiveRunCounts(ctx context.Context) (int, map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data->>'project_id' FROM factory_runs
		WHERE active AND id NOT IN (SELECT run FROM factory_assignments WHERE run!='') LIMIT 1001`)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	byProject := map[string]int{}
	total := 0
	for rows.Next() {
		var projectID string
		if err = rows.Scan(&projectID); err != nil {
			return 0, nil, err
		}
		total++
		byProject[projectID]++
	}
	return total, byProject, rows.Err()
}
