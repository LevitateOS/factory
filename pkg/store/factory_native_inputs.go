package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

func initializeNativeInputSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE SEQUENCE IF NOT EXISTS factory_native_input_revision;
CREATE TABLE IF NOT EXISTS factory_native_input_heads(
 repository BIGINT NOT NULL REFERENCES collaboration_repositories(id), issue BIGINT NOT NULL CHECK(issue>0),
 digest TEXT NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 revision BIGINT NOT NULL DEFAULT nextval('factory_native_input_revision') CHECK(revision>0), PRIMARY KEY(repository,issue));
CREATE TABLE IF NOT EXISTS factory_native_occurrences(
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, repository BIGINT NOT NULL REFERENCES collaboration_repositories(id),
 external_id TEXT NOT NULL CHECK(octet_length(external_id) BETWEEN 1 AND 1024), UNIQUE(repository,external_id));
CREATE OR REPLACE FUNCTION soda_guard_native_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'native occurrence identity is immutable'; END $$;
CREATE OR REPLACE TRIGGER factory_native_occurrence_immutable BEFORE UPDATE OR DELETE ON factory_native_occurrences
 FOR EACH ROW EXECUTE FUNCTION soda_guard_native_occurrence()`); err != nil {
		return err
	}
	return tx.Commit()
}

// ObserveNativeIssueInputs maps provider history IDs and versions the observed
// issue/edge snapshot. Selected comments and branch tips are checked separately;
// reading another selected subset must not change an issue's canonical revision.
// This records observations only, without admitting acceptance or execution.
func (s *Store) ObserveNativeIssueInputs(ctx context.Context, repository, issue int64, digest string, occurrences []string) (int64, map[string]string, error) {
	if repository <= 0 || issue <= 0 || !factory.ValidDigest(digest) || len(occurrences) > 53 {
		return 0, nil, ErrAdmissionChanged
	}
	for _, id := range occurrences {
		if len(id) == 0 || len(id) > 1024 {
			return 0, nil, ErrAdmissionChanged
		}
		for _, char := range []byte(id) {
			if char < 33 || char > 126 {
				return 0, nil, ErrAdmissionChanged
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	var revision int64
	err = tx.QueryRowContext(ctx, `INSERT INTO factory_native_input_heads(repository,issue,digest) VALUES($1,$2,$3)
 ON CONFLICT(repository,issue) DO UPDATE SET digest=EXCLUDED.digest,revision=nextval('factory_native_input_revision')
 WHERE factory_native_input_heads.digest<>EXCLUDED.digest RETURNING revision`, repository, issue, digest).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT revision FROM factory_native_input_heads WHERE repository=$1 AND issue=$2`, repository, issue).Scan(&revision)
	}
	if err != nil {
		return 0, nil, err
	}
	mapped := make(map[string]string, len(occurrences))
	for _, id := range occurrences {
		if _, exists := mapped[id]; exists {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_native_occurrences(repository,external_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, repository, id); err != nil {
			return 0, nil, err
		}
		var canonical int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_native_occurrences WHERE repository=$1 AND external_id=$2`, repository, id).Scan(&canonical); err != nil {
			return 0, nil, err
		}
		mapped[id] = strconv.FormatInt(canonical, 10)
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	return revision, mapped, nil
}
