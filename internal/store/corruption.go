package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Test-only corruption and failure-injection helpers. Production code must
// never call these: they create invalid rows, remove schema, or inject a
// transaction-boundary condition that the public store API cannot stage.
// They live here because raw SQL is allowed only in internal/store.

// InjectCorruptFactoryRun inserts one factory_runs row with caller-supplied
// bytes, bypassing every validation the store API applies.
func InjectCorruptFactoryRun(ctx context.Context, dsn, id string, active, settled bool, data string) error {
	if strings.TrimSpace(dsn) == "" {
		return errors.New("postgres connection string is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,$2,$3,$4)`,
		id, active, settled, data)
	return err
}

// DropFactoryRunViews removes the run-view table so view-read failure paths
// can be exercised against a live database.
func DropFactoryRunViews(ctx context.Context, dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return errors.New("postgres connection string is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `DROP TABLE factory_run_views`)
	return err
}
