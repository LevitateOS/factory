package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// CaptureHumanInline commits preparation before any remote effect. A concurrent
// identical request returns the winning preparation, even when the losing caller
// observed a different revision/deadline. Dispatch ONLY the returned capture.
// Callers must still check current human authority before submit/get/cancel.
func (s *Store) CaptureHumanInline(ctx context.Context, capture factory.HumanInlineCapture) (factory.HumanInlineCapture, bool, error) {
	cmd, err := capture.Command()
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	defer tx.Rollback()
	q := capture.Request
	var bound bool
	err = tx.QueryRowContext(ctx, `SELECT l.adopted AND EXISTS(SELECT 1 FROM collaboration_repositories r JOIN collaboration_actors a ON a.host=r.host WHERE r.host=$1 AND r.id=$2 AND a.id=$3) FROM collaboration_ledger l WHERE singleton FOR SHARE`, q.Host, q.Repository, q.Actor).Scan(&bound)
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	if !bound {
		return factory.HumanInlineCapture{}, false, collaboration.ErrUnboundLedger
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, cmd.ID, cmd.Type, cmd.Target, cmd.Principal, cmd.Digest, cmd.Payload, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	var stored factory.Command
	err = tx.QueryRowContext(ctx, `SELECT id,type,target,principal,digest,payload,outcome,created,finished FROM factory_commands WHERE id=$1 FOR UPDATE`, cmd.ID).Scan(&stored.ID, &stored.Type, &stored.Target, &stored.Principal, &stored.Digest, &stored.Payload, &stored.Outcome, &stored.Created, &stored.Finished)
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	if stored.Type != factory.CommandHumanInline {
		return factory.HumanInlineCapture{}, false, ErrCommandConflict
	}
	saved, err := factory.DecodeHumanInlineCapture(stored)
	if err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	if !saved.Request.SameRequest(q) {
		return factory.HumanInlineCapture{}, false, ErrCommandConflict
	}
	if err := tx.Commit(); err != nil {
		return factory.HumanInlineCapture{}, false, err
	}
	return saved, n == 0, nil
}

// HumanInlineCapture looks up by admitted exact request, before preparing any
// new provider snapshot. Absence is ErrNotFound, never proof of a remote outcome.
func (s *Store) HumanInlineCapture(ctx context.Context, q factory.HumanInlineCommand) (factory.HumanInlineCapture, error) {
	if err := q.Validate(); err != nil {
		return factory.HumanInlineCapture{}, err
	}
	cmd, err := s.FactoryCommand(ctx, q.ID)
	if err != nil {
		return factory.HumanInlineCapture{}, err
	}
	if cmd.Type != factory.CommandHumanInline {
		return factory.HumanInlineCapture{}, ErrCommandConflict
	}
	saved, err := factory.DecodeHumanInlineCapture(cmd)
	if err != nil {
		return factory.HumanInlineCapture{}, err
	}
	if !saved.Request.SameRequest(q) {
		return factory.HumanInlineCapture{}, ErrCommandConflict
	}
	return saved, nil
}

// ObserveHumanInline saves only validated adapter observations in the existing
// command row. The row lock serializes overlapping replies; immutable capture and
// terminal guards remain the database's final boundary. No network runs in SQL.
func (s *Store) ObserveHumanInline(ctx context.Context, c factory.HumanInlineCapture, o collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	cmd, err := c.Command()
	if err != nil {
		return empty, err
	}
	if err := factory.ValidateHumanInlineObservation(c, o); err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var stored factory.Command
	err = tx.QueryRowContext(ctx, `SELECT id,type,target,principal,digest,payload,outcome,created,finished FROM factory_commands WHERE id=$1 FOR UPDATE`, cmd.ID).Scan(&stored.ID, &stored.Type, &stored.Target, &stored.Principal, &stored.Digest, &stored.Payload, &stored.Outcome, &stored.Created, &stored.Finished)
	if err != nil {
		return empty, err
	}
	if stored.Type != cmd.Type || stored.Target != cmd.Target || stored.Principal != cmd.Principal || stored.Digest != cmd.Digest || stored.Payload != cmd.Payload {
		return empty, ErrCommandConflict
	}
	if _, err := factory.DecodeHumanInlineCapture(stored); err != nil {
		return empty, err
	}
	if stored.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(stored.Outcome), &prior) != nil {
			return empty, factory.ErrHumanInlineObservation
		}
		o, err = factory.AdvanceHumanInlineObservation(c, prior, o)
		if err != nil {
			return empty, err
		}
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return empty, err
	}
	if len(raw) > 64<<10 {
		return empty, factory.ErrHumanInlineObservation
	}
	if stored.Finished != "" {
		if !factory.HumanInlineFinished(o) || string(raw) != stored.Outcome {
			return empty, factory.ErrHumanInlineObservation
		}
	} else if string(raw) != stored.Outcome {
		finished := ""
		if factory.HumanInlineFinished(o) {
			finished = time.Now().UTC().Format(time.RFC3339Nano)
		}
		result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$2,finished=$3 WHERE id=$1 AND finished=''`, cmd.ID, string(raw), finished)
		if err := checkGrantRowsAffected(result, err, "human inline observation save failed"); err != nil {
			return empty, err
		}
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return o, nil
}
