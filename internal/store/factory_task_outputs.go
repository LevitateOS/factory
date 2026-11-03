package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

// MCPTaskOutputSubmissions reads the immutable authenticated parent proposals.
// Journal sequence, rather than submission identity, orders their checkpoints.
func (s *Store) MCPTaskOutputSubmissions(ctx context.Context, assignment string) ([]factory.MCPSubmission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,idempotency,data FROM factory_feed_posts WHERE data->>'assignment'=$1 AND data->'work'->>'tool'=$2 ORDER BY seq LIMIT 257`, assignment, string(factory.MCPToolSubmitTaskOutput))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []factory.MCPSubmission
	for rows.Next() {
		var id, key string
		var raw []byte
		if err := rows.Scan(&id, &key, &raw); err != nil {
			return nil, err
		}
		submission, _, err := decodeMCPWorkPost(assignment, key, id, raw)
		if err != nil {
			return nil, err
		}
		result = append(result, submission)
	}
	if len(result) > 256 {
		return nil, errors.New("task output proposals exceed the admitted bound")
	}
	return result, rows.Err()
}

// SaveFactoryFinalCheckpoint retains only a confirmed native response. The
// request is immutable once reserved; terminal native evidence never changes.
func (s *Store) SaveFactoryFinalCheckpoint(ctx context.Context, run factory.Run, result execution.CheckpointReceipt) error {
	if err := result.ValidateFor(result.Request, true); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, run.ID).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleRevision
		}
		return err
	}
	var current factory.Run
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.CheckpointBinding(result.Request) != nil || result.Request.ManifestDigest != project.ExecutionManifestDigest(run.ExecutionManifest) {
		return ErrStaleRevision
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(run.ExecutionManifest)
	if err != nil {
		return err
	}
	written, err := tx.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{final_checkpoint}',$2::jsonb) WHERE id=$1 AND data->'execution_manifest'=$3::jsonb
AND (coalesce(data->'final_checkpoint','null'::jsonb)='null'::jsonb OR data->'final_checkpoint'=$2::jsonb
OR (data->'final_checkpoint'->'request'=$2::jsonb->'request' AND data->'final_checkpoint'->>'status' NOT IN ('committed','failed')))`, run.ID, string(raw), string(manifest))
	if err != nil {
		return err
	}
	n, err := written.RowsAffected()
	if err == nil && n != 1 {
		return ErrStaleRevision
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
