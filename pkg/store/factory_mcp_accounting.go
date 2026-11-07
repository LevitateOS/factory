package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var ErrMCPAccountingTimeUnavailable = errors.New("confirmed retired run accounting time unavailable")

// FinishedMCPAccountingRuns visits only held accounting behind a retired run
// whose immutable MCP terminal already finished its exact assignment.
func (s *Store) FinishedMCPAccountingRuns(ctx context.Context, limit int) ([]factory.Run, error) {
	if limit < 1 || limit > MaxHeldReservations {
		return nil, errors.New("invalid finished accounting bound")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.data FROM factory_runs r
JOIN factory_run_views v ON v.run=r.id JOIN factory_assignments a ON a.id=v.attempt
JOIN factory_reservations p ON p.assignment=a.id
WHERE r.settled AND NOT r.active AND r.data->>'reconciled'='true'
AND a.stage='finished' AND a.run=r.id AND p.state='held'
AND EXISTS(SELECT 1 FROM factory_feed_posts m WHERE m.data->>'assignment'=a.id
AND starts_with(m.data->'work'->>'tool','factory.finish_') AND m.data->'work'->>'state' IN ('recorded','accepted'))
ORDER BY r.seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []factory.Run
	for rows.Next() {
		var raw []byte
		var run factory.Run
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// AccountFinishedMCPRun records usage and consumes its reservation in one
// transaction. It never writes the finished assignment or its MCP result.
func (s *Store) AccountFinishedMCPRun(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var ended sql.NullString
	var settled, active bool
	if err := tx.QueryRowContext(ctx, `SELECT data,settled,active,data->>'settled_at' FROM factory_runs WHERE id=$1 FOR UPDATE`, id).Scan(&raw, &settled, &active, &ended); err != nil {
		return err
	}
	var run factory.Run
	if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.ID != id || !settled || active || !run.Reconciled {
		return errors.New("run accounting requires its confirmed retired record")
	}
	if err := tx.QueryRowContext(ctx, `SELECT a.data FROM factory_assignments a JOIN factory_run_views v ON v.attempt=a.id WHERE v.run=$1`, id).Scan(&raw); err != nil {
		return err
	}
	var a factory.Assignment
	if json.Unmarshal(raw, &a) != nil || a.Validate() != nil || a.Stage != factory.AssignmentFinished || a.Run != id || a.ProjectID != run.ProjectID || a.Role != run.Role || a.SourceCommit != run.InputSHA {
		return errors.New("run accounting differs from its finished assignment")
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_reservations WHERE assignment=$1 FOR UPDATE`, a.ID).Scan(&raw); err != nil {
		return err
	}
	var reservation factory.Reservation
	if json.Unmarshal(raw, &reservation) != nil || reservation.Validate() != nil || reservation.AssignmentID != a.ID || reservation.Repository != a.Repository || reservation.Connection != a.Connection || (reservation.State != factory.ReservationHeld && reservation.State != factory.ReservationConsumed) {
		return errors.New("run accounting reservation differs from its assignment")
	}
	var usage factory.Usage
	err = tx.QueryRowContext(ctx, `SELECT data FROM factory_usage WHERE run=$1`, id).Scan(&raw)
	if err == nil {
		if json.Unmarshal(raw, &usage) != nil || usage.Validate() != nil || usage.RunID != id || usage.Repository != a.Repository || usage.Connection != a.Connection || !usage.StartedAt.Equal(run.Started) {
			return errors.New("retained usage differs from its run")
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		end, err := time.Parse(time.RFC3339Nano, ended.String)
		if err != nil || end.Before(run.Started) {
			return ErrMCPAccountingTimeUnavailable
		}
		end = run.UsageEnd(end)
		elapsed := end.Sub(run.Started)
		usage = factory.Usage{RunID: id, Repository: a.Repository, Connection: a.Connection, Minutes: int((elapsed + time.Minute - time.Nanosecond) / time.Minute), StartedAt: run.Started.UTC(), EndedAt: end.UTC()}
		if err := usage.Validate(); err != nil {
			return err
		}
		body, _ := json.Marshal(usage)
		if _, err := tx.ExecContext(ctx, `INSERT INTO factory_usage(run,repository,connection,minutes,data,started_at,ended_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run) DO NOTHING`, id, a.Repository, a.Connection, usage.Minutes, string(body), floorUsageMicro(usage.StartedAt), ceilUsageMicro(usage.EndedAt)); err != nil {
			return err
		}
	} else {
		return err
	}
	if reservation.State == factory.ReservationHeld {
		if _, err := tx.ExecContext(ctx, `UPDATE factory_reservations SET state='consumed',data=jsonb_set(jsonb_set(data,'{state}','"consumed"'::jsonb),'{revision}',to_jsonb((data->>'revision')::bigint+1)) WHERE assignment=$1 AND state='held'`, a.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
