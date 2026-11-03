package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func openReadOnly(dsn string) (*sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres connection string is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	return db, nil
}

// ReadSchemaVersion connects to an existing database without migrating and
// returns the single schema_version row. Callers that need the current schema
// must use OpenObserve or Open instead.
func ReadSchemaVersion(ctx context.Context, dsn string) (int, error) {
	db, err := openReadOnly(dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	inner, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer func() { _ = inner.Rollback() }()
	return loadSchemaVersion(ctx, inner)
}

// OpenObserve connects to an existing database without migrating. It refuses
// unless the stored schema exactly matches SchemaVersion(). Observation
// performs reads only; it never initializes or repairs the schema.
func OpenObserve(ctx context.Context, dsn string) (*Store, error) {
	db, err := openReadOnly(dsn)
	if err != nil {
		return nil, err
	}
	inner, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		db.Close()
		return nil, err
	}
	version, err := loadSchemaVersion(ctx, inner)
	if err != nil {
		_ = inner.Rollback()
		db.Close()
		return nil, err
	}
	if err = inner.Rollback(); err != nil {
		db.Close()
		return nil, err
	}
	if version != SchemaVersion() {
		db.Close()
		return nil, errors.New("database schema differs from this application")
	}
	return &Store{db: db}, nil
}

// IntegrityCheck verifies the opened store's schema version, required
// columns and immutability guards in a read-only transaction.
func (s *Store) IntegrityCheck(ctx context.Context) error {
	inner, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return errors.New("soda database integrity failed")
	}
	defer func() { _ = inner.Rollback() }()
	t := inner
	version, err := loadSchemaVersion(ctx, t)
	if err != nil || version != SchemaVersion() {
		return errors.New("soda database integrity failed")
	}
	if err = verifyRequiredColumns(ctx, t); err != nil {
		return errors.New("soda database integrity failed")
	}
	if err = verifyImmutableCreationProfile(ctx, t); err != nil {
		return errors.New("soda database integrity failed")
	}
	if err = verifyImmutablePreparationRefs(ctx, t); err != nil {
		return errors.New("soda database integrity failed")
	}
	if err = verifyImmutableExecutionIdentity(ctx, t); err != nil {
		return errors.New("soda database integrity failed")
	}
	return nil
}
