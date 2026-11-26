package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/levitateos/factory/pkg/factory"
)

// ErrRunViewConflict reports a reused run view identity with changed content.
var ErrRunViewConflict = errors.New("run view identity reused for different content")

// RecordFactoryRunView stores the display binding for one recorded run. The
// same binding replays; changed content for the same run conflicts instead of
// rebinding the run to another issue or attempt.
func (s *Store) RecordFactoryRunView(ctx context.Context, v factory.RunView) (factory.RunView, bool, error) {
	if err := v.Validate(); err != nil {
		return factory.RunView{}, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4) ON CONFLICT(run) DO NOTHING`,
		v.RunID, v.Repository, v.Issue, v.Attempt)
	if err != nil {
		return factory.RunView{}, false, fmt.Errorf("factory run view record failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return factory.RunView{}, false, err
	}
	stored, err := s.FactoryRunView(ctx, v.RunID)
	if err != nil {
		return factory.RunView{}, false, err
	}
	if stored != v {
		return factory.RunView{}, false, ErrRunViewConflict
	}
	return stored, n == 1, nil
}

// FactoryRunView returns the display binding for one run.
func (s *Store) FactoryRunView(ctx context.Context, runID string) (factory.RunView, error) {
	var v factory.RunView
	v.RunID = runID
	err := s.db.QueryRowContext(ctx, `SELECT repository,issue,attempt FROM factory_run_views WHERE run=$1`, runID).
		Scan(&v.Repository, &v.Issue, &v.Attempt)
	if err != nil {
		return factory.RunView{}, err
	}
	return v, nil
}

// FactoryRunViews returns recorded display bindings, newest first, bounded
// for the Spaces collection read. Callers join runs to bindings in memory;
// absence of a binding leaves the run unbound, never hidden.
func (s *Store) FactoryRunViews(ctx context.Context, limit int) ([]factory.RunView, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid run view list bound")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run,repository,issue,attempt FROM factory_run_views ORDER BY seq DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var views []factory.RunView
	for rows.Next() {
		var v factory.RunView
		if err := rows.Scan(&v.RunID, &v.Repository, &v.Issue, &v.Attempt); err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

// RepositoryRun is one recorded run with its display binding.
type RepositoryRun struct {
	Run  factory.Run
	View factory.RunView
}

// FactoryRepositoryRuns returns the runs bound to one repository, newest
// first. Runs without a binding belong to no repository and are not listed.
func (s *Store) FactoryRepositoryRuns(ctx context.Context, repository int64, limit int) ([]RepositoryRun, error) {
	if repository < 1 || limit < 1 || limit > 200 {
		return nil, errors.New("invalid repository run list bound")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.run,v.issue,v.attempt,r.data FROM factory_run_views v JOIN factory_runs r ON r.id=v.run WHERE v.repository=$1 ORDER BY v.seq DESC LIMIT $2`, repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var runs []RepositoryRun
	for rows.Next() {
		item := RepositoryRun{View: factory.RunView{Repository: repository}}
		var data []byte
		if err := rows.Scan(&item.View.RunID, &item.View.Issue, &item.View.Attempt, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &item.Run); err != nil {
			return nil, err
		}
		runs = append(runs, item)
	}
	return runs, rows.Err()
}
