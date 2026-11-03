package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/levitateos/sodaos/internal/factory"
)

// RecordCheckAssessment stores the latest check assessment for one native
// PR, replacing any earlier head's verdict. The caller leaves Revision
// zero; the store counts records. ST12 reads the latest record and must
// re-verify its head/base/policy bindings before consuming it.
func (s *Store) RecordCheckAssessment(ctx context.Context, a factory.CheckAssessment) (factory.CheckAssessment, error) {
	var empty factory.CheckAssessment
	if err := a.Validate(); err != nil {
		return empty, err
	}
	if a.Revision != 0 {
		return empty, fmt.Errorf("check assessment carries a fresh record: %w", ErrStaleRevision)
	}
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM factory_check_assessments WHERE repository=$1 AND pr=$2`, a.Repository, a.PRNumber).Scan(&revision)
	if err != nil && err != ErrNotFound {
		return empty, err
	}
	stored := a
	stored.Revision = revision + 1
	data, err := json.Marshal(stored)
	if err != nil {
		return empty, err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO factory_check_assessments(repository,pr,revision,data) VALUES($1,$2,$3,$4)
		ON CONFLICT(repository,pr) DO UPDATE SET revision=excluded.revision,data=excluded.data`,
		stored.Repository, stored.PRNumber, stored.Revision, string(data)); err != nil {
		return empty, fmt.Errorf("check assessment record failed: %w", err)
	}
	return stored, nil
}

// CheckAssessment returns the latest check assessment for one native PR.
// Absence reports ErrNotFound: no verdict exists yet for this PR.
func (s *Store) CheckAssessment(ctx context.Context, repository, prNumber int64) (factory.CheckAssessment, error) {
	var a factory.CheckAssessment
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_check_assessments WHERE repository=$1 AND pr=$2`, repository, prNumber).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &a)
	}
	return a, err
}
