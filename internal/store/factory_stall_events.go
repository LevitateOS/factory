package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// Frozen D15 stall-event storage contracts (R36).
//
// factory_stall_events holds one row per bound stall observation,
// keyed by (repository, progress_generation, input_fingerprint): the
// D15 event uniqueness. data carries the factory.StallEvent. Rows are
// insert-only observations; the investigation state flow rides the
// repair charge journal and R37 owns later lifecycle transitions.

const (
	// TableFactoryStallEvents is the unique stall-event owner.
	TableFactoryStallEvents = "factory_stall_events"
)

// FactoryStallEventsDDL lives in factory_completions.go beside the
// other D15/D13 owner DDLs: the Rust drift test resolves the bare
// schemaStatements identifier only across its registered Go owners.

// StandingStallEvent is the latest bound observation for one repository:
// the generation its next detection round must reuse until verified
// delivery advances progress, with the event identity and sealed digest
// a fresh round compares for staleness.
type StandingStallEvent struct {
	Repository int64
	Generation int64
	EventID    string
	Digest     string
}

// SaveStallEvent records one bound stall observation. A redetected
// round carries the identical (repository, generation, fingerprint)
// key and replays to the stored row instead of duplicating it.
func (s *Store) SaveStallEvent(ctx context.Context, event factory.StallEvent) (bool, error) {
	if err := event.Validate(); err != nil {
		return false, err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return false, err
	}
	var created bool
	err = s.db.QueryRowContext(ctx, `INSERT INTO factory_stall_events(repository,generation,event,episode,fingerprint,data) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING RETURNING TRUE`,
		event.Repository, event.ProgressGeneration, event.ID, event.EpisodeID, event.InputFingerprint, string(payload)).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return created, nil
}

// StallRoundGeneration returns the standing detection round: the
// highest recorded progress generation, zero before the first bound
// event. Verified delivery alone advances progress under R37; until
// then every round reuses this generation and replays its episode.
func (s *Store) StallRoundGeneration(ctx context.Context) (int64, error) {
	var generation sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(generation) FROM factory_stall_events`).Scan(&generation); err != nil {
		return 0, err
	}
	if !generation.Valid {
		return 0, nil
	}
	return generation.Int64, nil
}

// StandingStallEvents returns the latest bound observation per
// repository. A fresh round feeds these back as prior events so ready
// work stales an obsolete investigation before any release. Latest
// is true observation order (the insertion sequence), never a hash
// of the observed bytes: R37 must advance the progress generation
// on verified delivery rather than read recency into this row.
func (s *Store) StandingStallEvents(ctx context.Context) (map[int64]StandingStallEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON (repository) repository, generation, event, data->>'inventory_digest' FROM factory_stall_events ORDER BY repository, generation DESC, seq DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	standing := map[int64]StandingStallEvent{}
	for rows.Next() {
		var entry StandingStallEvent
		var digest sql.NullString
		if err := rows.Scan(&entry.Repository, &entry.Generation, &entry.EventID, &digest); err != nil {
			return nil, err
		}
		entry.Digest = digest.String
		standing[entry.Repository] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return standing, nil
}
