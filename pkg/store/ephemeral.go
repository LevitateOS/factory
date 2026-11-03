package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
)

// TestFixtureDSN builds a superuser connection URL from the A10 disposable
// fixture environment: SODA_PG_HOST, SODA_PG_PORT and
// SODA_PG_SUPER_PASSWORD_FILE, as printed by soda-pg-fixture start, or the
// SODA_NATIVE_PG_DSN_FILE supplied by the native fixture driver.
// It reports ok=false when the fixture is absent; tests skip instead of
// guessing a database. The password travels from its restricted file into
// the returned URL only; it is never logged.
func TestFixtureDSN() (dsn string, ok bool) {
	// The native fixture owns a private Unix socket and passes only this secret
	// file path. Reuse the same ephemeral-database isolation as container tests.
	if file := os.Getenv("SODA_NATIVE_PG_DSN_FILE"); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", false
		}
		dsn = strings.TrimSpace(string(raw))
		uri, err := url.Parse(dsn)
		if err != nil || (uri.Scheme != "postgresql" && uri.Scheme != "postgres") || uri.Query().Get("host") == "" {
			return "", false
		}
		return dsn, true
	}
	host, port, passwordFile := os.Getenv("SODA_PG_HOST"), os.Getenv("SODA_PG_PORT"), os.Getenv("SODA_PG_SUPER_PASSWORD_FILE")
	if host == "" || port == "" || passwordFile == "" {
		return "", false
	}
	raw, err := os.ReadFile(passwordFile)
	if err != nil {
		return "", false
	}
	password := strings.TrimSpace(string(raw))
	if password == "" || strings.ContainsAny(password, "\r\n\x00") {
		return "", false
	}
	u := url.URL{Scheme: "postgres", Host: host + ":" + port, Path: "/postgres", User: url.UserPassword("postgres", password)}
	query := url.Values{}
	query.Set("sslmode", "disable")
	u.RawQuery = query.Encode()
	return u.String(), true
}

// OpenEphemeral creates a uniquely-named database on the given server and
// opens a Store on it for one test. The returned cleanup closes the store
// and drops the database. Tests resolve the server URL through
// TestFixtureDSN and skip when it is absent.
//
// The database is cloned from a per-process migrated template instead of
// running the full DDL per test; any template failure falls back to the
// plain create path.
func OpenEphemeral(ctx context.Context, superDSN string) (s *Store, dsn string, cleanup func(), err error) {
	dsn, drop, err := createClonedDatabase(ctx, superDSN)
	if err != nil {
		dsn, drop, err = createEphemeralDatabase(ctx, superDSN)
	}
	if err != nil {
		return nil, "", nil, err
	}
	s, err = Open(dsn)
	if err != nil {
		drop()
		return nil, "", nil, err
	}
	cleanup = func() {
		_ = s.Close()
		drop()
	}
	return s, dsn, cleanup, nil
}

// createEphemeralDatabase creates a uniquely-named empty database and
// returns its connection URL plus a drop cleanup. Schema tests use it to
// stage legacy version markers without opening a Store.
func createEphemeralDatabase(ctx context.Context, superDSN string) (string, func(), error) {
	name, err := ephemeralName()
	if err != nil {
		return "", nil, err
	}
	admin, err := sql.Open("pgx", superDSN)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = admin.Close() }()
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		return "", nil, err
	}
	u, err := url.Parse(superDSN)
	if err != nil {
		return "", nil, err
	}
	u.Path = "/" + name
	return u.String(), dropDatabaseFunc(superDSN, name), nil
}

func ephemeralName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	// Lowercase hex only, so the quoted identifier needs no escaping.
	return "soda_ephem_" + hex.EncodeToString(random[:]), nil
}

// ephemeralTemplates caches one migrated schema-only database per server
// for the life of the test process, with its drop cleanup for TestMain.
var ephemeralTemplates sync.Map // key string -> templateDrop

type templateDrop struct {
	name string
	drop func()
}

var ephemeralTemplateMu sync.Mutex

// dropEphemeralTemplates removes every template database this process
// created. TestMain calls it after the suite; best effort, never fatal.
func dropEphemeralTemplates() {
	ephemeralTemplates.Range(func(_, value any) bool {
		if template, ok := value.(templateDrop); ok {
			template.drop()
		}
		return true
	})
}

func ephemeralTemplateKey(superDSN string) string {
	return fmt.Sprintf("%s|schema-%d", superDSN, SchemaVersion())
}

// ensureEphemeralTemplate returns a migrated, connection-free template
// database, creating it once per process. The template holds schema only.
func ensureEphemeralTemplate(ctx context.Context, superDSN string) (string, error) {
	key := ephemeralTemplateKey(superDSN)
	if value, ok := ephemeralTemplates.Load(key); ok {
		return value.(templateDrop).name, nil
	}
	ephemeralTemplateMu.Lock()
	defer ephemeralTemplateMu.Unlock()
	if value, ok := ephemeralTemplates.Load(key); ok {
		return value.(templateDrop).name, nil
	}
	name, err := ephemeralName()
	if err != nil {
		return "", err
	}
	template := "soda_ephem_template_" + name[len("soda_ephem_"):]
	admin, err := sql.Open("pgx", superDSN)
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close() }()
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+template+`"`); err != nil {
		return "", err
	}
	drop := dropDatabaseFunc(superDSN, template)
	u, err := url.Parse(superDSN)
	if err != nil {
		drop()
		return "", err
	}
	u.Path = "/" + template
	migrated, err := Open(u.String())
	if err != nil {
		drop()
		return "", err
	}
	if err = migrated.Close(); err != nil {
		drop()
		return "", err
	}
	ephemeralTemplates.Store(key, templateDrop{name: template, drop: drop})
	return template, nil
}

// createClonedDatabase copies the migrated template instead of replaying
// DDL. Callers fall back to createEphemeralDatabase on any error.
func createClonedDatabase(ctx context.Context, superDSN string) (string, func(), error) {
	template, err := ensureEphemeralTemplate(ctx, superDSN)
	if err != nil {
		return "", nil, err
	}
	name, err := ephemeralName()
	if err != nil {
		return "", nil, err
	}
	admin, err := sql.Open("pgx", superDSN)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = admin.Close() }()
	if _, err = admin.ExecContext(ctx, `CREATE DATABASE "`+name+`" TEMPLATE "`+template+`"`); err != nil {
		return "", nil, err
	}
	u, err := url.Parse(superDSN)
	if err != nil {
		dropDatabaseFunc(superDSN, name)()
		return "", nil, err
	}
	u.Path = "/" + name
	return u.String(), dropDatabaseFunc(superDSN, name), nil
}

func dropDatabaseFunc(superDSN, name string) func() {
	return func() {
		drop, err := sql.Open("pgx", superDSN)
		if err != nil {
			return
		}
		defer func() { _ = drop.Close() }()
		_, _ = drop.ExecContext(context.Background(), `DROP DATABASE IF EXISTS "`+name+`"`)
	}
}
