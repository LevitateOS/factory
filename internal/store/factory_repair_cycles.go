package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/levitateos/sodaos/internal/factory"
)

type StallRepairRecord struct {
	factory.StallEvent
	Repair factory.StallRepairEpisode `json:"repair"`
}

func (s *Store) StallRepairRecord(ctx context.Context, repository int64, event string) (StallRepairRecord, error) {
	var record StallRepairRecord
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_stall_events WHERE repository=$1 AND event=$2`, repository, event).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrNotFound
	}
	if err != nil {
		return record, err
	}
	err = json.Unmarshal(raw, &record)
	return record, err
}

func (s *Store) RepairEpisodeRecord(ctx context.Context, repository int64, episode string) (StallRepairRecord, error) {
	var record StallRepairRecord
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_stall_events WHERE repository=$1 AND episode=$2 ORDER BY (data ? 'repair') DESC,seq LIMIT 1`, repository, episode).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrNotFound
	}
	if err != nil {
		return record, err
	}
	err = json.Unmarshal(raw, &record)
	return record, err
}

func (s *Store) PendingStallRepairRecords(ctx context.Context, limit int) ([]StallRepairRecord, error) {
	if limit < 1 || limit > 64 {
		return nil, errors.New("invalid repair episode page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_stall_events WHERE data ? 'repair' AND data->'repair'->'cycles'->-1->>'state'<>'stale' ORDER BY repository,seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []StallRepairRecord
	for rows.Next() {
		var raw []byte
		var record StallRepairRecord
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) SaveStallRepairEpisode(ctx context.Context, event factory.StallEvent, expected int64, episode factory.StallRepairEpisode) error {
	if event.Validate() != nil || episode.Revision != expected+1 || len(episode.Cycles) > factory.DefaultRepairCyclesPerEpisode {
		return errors.New("invalid repair episode update")
	}
	for i, cycle := range episode.Cycles {
		if cycle.Number != i+1 {
			return errors.New("repair cycles must remain ordered and contiguous")
		}
		if err := cycle.ValidateFor(event); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "soda-repair-episode:"+event.EpisodeID); err != nil {
		return err
	}
	var canonical string
	if err = tx.QueryRowContext(ctx, `SELECT event FROM factory_stall_events WHERE repository=$1 AND episode=$2 ORDER BY (data ? 'repair') DESC,seq LIMIT 1`, event.Repository, event.EpisodeID).Scan(&canonical); err != nil {
		return err
	}
	if canonical != event.ID {
		return ErrStaleRevision
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_stall_events WHERE repository=$1 AND event=$2 FOR UPDATE`, event.Repository, event.ID).Scan(&raw); err != nil {
		return err
	}
	var prior StallRepairRecord
	if json.Unmarshal(raw, &prior) != nil || prior.StallEvent != event || prior.Repair.Revision != expected {
		return ErrStaleRevision
	}
	if len(episode.Cycles) < len(prior.Repair.Cycles) {
		return errors.New("repair cycles cannot be removed")
	}
	for i, old := range prior.Repair.Cycles {
		next := episode.Cycles[i]
		if !factory.RepairTransitionAllowed(old.State, next.State) {
			return errors.New("invalid repair cycle transition")
		}
		if old.ID != next.ID || old.Number != next.Number || old.AssignmentID != "" && (old.AssignmentID != next.AssignmentID || old.RunID != next.RunID) {
			return errors.New("repair cycle identity is immutable")
		}
		if old.InventoryDigest != "" && (old.InventoryDigest != next.InventoryDigest || old.NativeRevision != next.NativeRevision || !old.Local.Equal(next.Local) || !reflect.DeepEqual(old.Issues, next.Issues)) {
			return errors.New("sealed repair inputs are immutable")
		}
		if old.JudgmentFingerprint != "" && old.JudgmentFingerprint != next.JudgmentFingerprint {
			return errors.New("repair judgment reservation is immutable")
		}
		if old.ProposalOption != "" && (old.ProposalOption != next.ProposalOption || old.ProposalProbability != next.ProposalProbability || old.ProposalConfidence != next.ProposalConfidence) {
			return errors.New("repair judgment is immutable")
		}
	}
	if len(episode.Cycles) > len(prior.Repair.Cycles)+1 {
		return errors.New("only one new repair cycle may be retained")
	}
	if len(episode.Cycles) > len(prior.Repair.Cycles) && len(prior.Repair.Cycles) > 0 {
		last := prior.Repair.Cycles[len(prior.Repair.Cycles)-1]
		if last.State != factory.RepairAttention && last.State != factory.RepairAdopted {
			return errors.New("prior repair cycle must finish before new evidence")
		}
		before, beforeErr := last.RepairFactsDigest()
		after, afterErr := episode.Cycles[len(episode.Cycles)-1].RepairFactsDigest()
		if beforeErr != nil || afterErr != nil || before == after {
			return factory.ErrRepairNeedsEvidence
		}
	}
	prior.Repair = episode
	raw, err = json.Marshal(prior)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_stall_events SET data=$1 WHERE repository=$2 AND event=$3`, string(raw), event.Repository, event.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RepairEpisodeViews(ctx context.Context, repository int64) ([]factory.RepairEpisodeView, error) {
	if repository <= 0 {
		return nil, errors.New("invalid repair repository")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_stall_events WHERE repository=$1 AND data ? 'repair' ORDER BY seq LIMIT 17`, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	views := []factory.RepairEpisodeView{}
	for rows.Next() {
		var raw []byte
		var record StallRepairRecord
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		if len(views) >= 16 {
			return nil, errors.New("repair episode view exceeds finite grant history bound")
		}
		view := factory.RepairEpisodeView{EventID: record.ID, EpisodeID: record.EpisodeID, Reason: record.Reason, Cycles: []factory.RepairCycleView{}}
		for _, cycle := range record.Repair.Cycles {
			view.Cycles = append(view.Cycles, factory.RepairCycleView{ID: cycle.ID, Number: cycle.Number, State: cycle.State, Reason: cycle.Reason, RunID: cycle.RunID, AssignmentID: cycle.AssignmentID, InventoryDigest: cycle.InventoryDigest})
		}
		views = append(views, view)
	}
	return views, rows.Err()
}
