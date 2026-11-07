// Package store owns Factory's PostgreSQL schema and retained appliance row operations. It is not
// an HTTP API, host runtime or generic SQL escape hatch for other packages.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/levitateos/factory/pkg/project"
)

// ErrNotFound is returned when a looked-up row is absent. Prefer this sentinel
// over importing database/sql solely for sql.ErrNoRows.
var ErrNotFound = sql.ErrNoRows

// ErrCommandConflict reports a reused command identity with changed content.
var ErrCommandConflict = errors.New("command identity reused for different content")

// ErrLastKeyConfirmationRequired means this removal would delete the user's
// final saved development key and its current mutation did not confirm that.
var ErrLastKeyConfirmationRequired = errors.New("final saved key removal requires confirmation")

type Store struct {
	db        *sql.DB
	appliance bool
}
type User struct {
	ID          int64
	Login, Name string
}
type Key struct {
	ID                  int64
	Public, Fingerprint string
}
type Project struct {
	Profile               *project.Profile
	ID, Name              string
	RepositoryID, OwnerID int64
	Repository, IP        string
	Ready                 bool
}
type Session struct {
	User User
	CSRF string
	// ContextID binds a native request to its current Forgejo session.
	ContextID string
	Expires   int64
}

// sameJSONDocument reports whether two JSON documents carry the same
// content. JSONB storage normalizes key order, so byte comparison cannot
// tell an identical replay from a conflicting rewrite.
func sameJSONDocument(ctx context.Context, t *sql.Tx, stored, fresh []byte) (bool, error) {
	var same bool
	err := t.QueryRowContext(ctx, `SELECT $1::jsonb = $2::jsonb`, string(stored), string(fresh)).Scan(&same)
	return same, err
}

// Open connects to the PostgreSQL store, creating the current schema in an
// empty database, applying supported retained upgrades and refusing unknown
// schemas. dsn is a
// PostgreSQL connection URL; callers read it from a restricted secret
// file, never from configuration text.
func Open(dsn string) (*Store, error) {
	return OpenContext(context.Background(), dsn)
}

// OpenContext opens the appliance composition with the caller's startup lifetime.
func OpenContext(ctx context.Context, dsn string) (*Store, error) {
	return openContext(ctx, dsn, false)
}

// OpenStandaloneContext creates a native Factory database without appliance
// Projects, identity custody or application hosting. Retained shared databases keep their compatibility obligations.
// It never removes tables or converts a retained database to a smaller schema.
func OpenStandaloneContext(ctx context.Context, dsn string) (*Store, error) {
	return openContext(ctx, dsn, true)
}

func openContext(ctx context.Context, dsn string, standalone bool) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres connection string is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return configureStore(ctx, db, standalone)
}

func configureStore(ctx context.Context, db *sql.DB, standalone bool) (*Store, error) {
	// Concurrent dispatch passes admit through separate connections;
	// each backend retains a bounded connection pool.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	s := &Store{db: db}
	err := initializeSchema(ctx, db, standalone)
	if err == nil {
		var hosting bool
		err = db.QueryRowContext(ctx, `SELECT application_hosting,appliance_compatibility FROM schema_version`).Scan(&hosting, &s.appliance)
		if err == nil && !standalone && (!hosting || !s.appliance) {
			err = errors.New("standalone database does not include application hosting")
		}
	}
	if err == nil {
		err = initializeLibrarySchema(ctx, db)
	}
	if err == nil {
		err = initializeArtifactSchema(ctx, db)
	}
	if err == nil {
		err = initializeDeliverySchema(ctx, db)
	}
	if err == nil {
		err = initializeRepairSchema(ctx, db)
	}
	if err == nil {
		err = initializeFactoryIdentitySchema(ctx, db)
	}
	if err == nil {
		err = initializeDeliveryRepairSchema(ctx, db)
	}
	if err == nil {
		err = initializeCollaborationSchema(ctx, db)
	}
	if err == nil {
		err = initializeFactoryExecutionSchema(ctx, db, s.appliance)
	}
	if err == nil {
		err = initializeFactoryPlacementSchema(ctx, db)
	}
	if err == nil {
		err = initializeNativeWorkspaceSchema(ctx, db, s.appliance)
	}
	if err == nil {
		err = initializeNativeInputSchema(ctx, db)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the PostgreSQL connection is live without
// touching product rows. Readiness probes use it; a failed ping means
// the dashboard cannot serve admitted work.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) UpsertUser(ctx context.Context, u User) error {
	if u.ID <= 0 || strings.TrimSpace(u.Login) == "" {
		return errors.New("invalid provider user")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id,login,name) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET login=excluded.login`, u.ID, u.Login, u.Name)
	return err
}

func (s *Store) User(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `SELECT id,login,name FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Login, &u.Name)
	return u, err
}

func (s *Store) RenameProfile(ctx context.Context, id int64, name string) error {
	if len(name) > 200 {
		return errors.New("name too long")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET name=$1 WHERE id=$2`, name, id)
	return err
}

func (s *Store) AddKey(ctx context.Context, uid int64, public, fingerprint string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO keys(user_id,public,fingerprint) VALUES($1,$2,$3) ON CONFLICT(user_id,fingerprint) DO NOTHING`, uid, public, fingerprint)
	return err
}

func (s *Store) RemoveKey(ctx context.Context, uid, id int64, confirmLast bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var lockedID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&lockedID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var keyID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM keys WHERE user_id=$1 AND id=$2`, uid, id).Scan(&keyID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM keys WHERE user_id=$1`, uid).Scan(&count); err != nil {
		return false, err
	}
	if count == 1 && !confirmLast {
		return false, ErrLastKeyConfirmationRequired
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM keys WHERE user_id=$1 AND id=$2`, uid, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) Keys(ctx context.Context, uid int64) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,public,fingerprint FROM keys WHERE user_id=$1 ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err = rows.Scan(&k.ID, &k.Public, &k.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) CreateProject(ctx context.Context, p Project) error {
	if p.Profile == nil {
		_, err := s.db.ExecContext(ctx, `INSERT INTO projects(id,name,repository_id,owner_id,repository) VALUES($1,$2,$3,$4,$5)`, p.ID, p.Name, p.RepositoryID, p.OwnerID, p.Repository)
		return err
	}
	if err := p.Profile.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p.Profile)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects(id,name,repository_id,owner_id,repository,creation_profile) VALUES($1,$2,$3,$4,$5,$6)`, p.ID, p.Name, p.RepositoryID, p.OwnerID, p.Repository, string(raw))
	return err
}

func (s *Store) MarkReady(ctx context.Context, id, ip string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET ip=$1,ready=TRUE WHERE id=$2`, ip, id)
	return err
}

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var profile sql.NullString
	err := row.Scan(&p.ID, &p.Name, &p.RepositoryID, &p.OwnerID, &p.Repository, &p.IP, &p.Ready, &profile)
	if err == nil && profile.Valid {
		p.Profile, err = project.Decode(profile.String)
	}
	return p, err
}

const projectColumns = `id,name,repository_id,owner_id,repository,ip,ready,creation_profile`

func (s *Store) Project(ctx context.Context, id string) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id=$1`, id))
}

// SpaceProjects is a bounded scan of Soda associations, not an authorized catalog.
// NULL deliberately rejects oversized stored labels instead of silently truncating
// them or allocating arbitrary DB text. Callers must authorize every returned row.
func (s *Store) SpaceProjects(ctx context.Context) ([]Project, error) {
	if !s.appliance {
		return []Project{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN octet_length(id)<=128 THEN id END,
 CASE WHEN octet_length(name)<=1024 THEN name END,repository_id,owner_id,
 CASE WHEN octet_length(repository)<=2048 THEN repository END,
 CASE WHEN octet_length(ip)<=128 THEN ip END,ready,creation_profile FROM projects ORDER BY id LIMIT 129`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProjectByRepository takes the appliance's native repository ID. Factory local
// IDs must use NativeProjectForCollaborationRepository at the identity boundary.
func (s *Store) ProjectByRepository(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE repository_id=$1`, id))
}

func (s *Store) Join(ctx context.Context, pid string, uid int64, login string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT login FROM memberships WHERE project_id=$1 AND user_id=$2 FOR UPDATE`, pid, uid).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existing != login {
		return ErrStaleRevision
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT held FROM project_member_access WHERE project_id=$1 AND user_id=$2 FOR UPDATE`, pid, uid).Scan(&held)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if held {
		return ErrProjectAccessHeld
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memberships(project_id,user_id,login) VALUES($1,$2,$3) ON CONFLICT(project_id,user_id) DO UPDATE SET login=memberships.login WHERE memberships.login=EXCLUDED.login`, pid, uid, login)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_member_access SET rejoin=FALSE WHERE project_id=$1 AND user_id=$2 AND NOT held`, pid, uid)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MemberLogin(ctx context.Context, pid string, uid int64) (string, error) {
	var login string
	err := s.db.QueryRowContext(ctx, `SELECT login FROM memberships m WHERE project_id=$1 AND user_id=$2 AND NOT EXISTS(SELECT 1 FROM project_member_access a WHERE a.project_id=m.project_id AND a.user_id=m.user_id AND (a.held OR a.rejoin))`, pid, uid).Scan(&login)
	return login, err
}
