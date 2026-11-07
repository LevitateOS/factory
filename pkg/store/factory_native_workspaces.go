package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func initializeNativeWorkspaceSchema(ctx context.Context, db *sql.DB, appliance bool) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_native_workspaces(
 id TEXT PRIMARY KEY, repository BIGINT NOT NULL REFERENCES collaboration_repositories(id),
 runtime TEXT NOT NULL REFERENCES collaboration_runtimes(id), profile TEXT NOT NULL REFERENCES collaboration_profiles(id),
 UNIQUE(repository,runtime), CHECK(id ~ '^p[a-f0-9]{24}$'));
 CREATE OR REPLACE FUNCTION soda_guard_native_workspace() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'native workspace identity is immutable'; END $$;
 CREATE OR REPLACE TRIGGER factory_native_workspace_immutable BEFORE UPDATE OR DELETE ON factory_native_workspaces FOR EACH ROW EXECUTE FUNCTION soda_guard_native_workspace()`); err != nil {
		return err
	}
	for _, table := range []string{"project_requirement_decisions", "project_approval_decisions", "project_requirement_heads", "project_approval_heads", "project_preparations"} {
		if appliance && table == "project_preparations" {
			continue
		}
		native := "native"
		if table == "project_requirement_decisions" || table == "project_approval_decisions" || table == "project_preparations" {
			native = "(NULLIF(data->'native','null'::jsonb) IS NOT NULL)"
		} else {
			if _, err = tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN IF NOT EXISTS native BOOLEAN NOT NULL DEFAULT false`); err != nil {
				return err
			}
		}
		if !appliance {
			if _, err = tx.ExecContext(ctx, `DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='`+table+`'::regclass AND conname='`+table+`_native_fkey') THEN
 ALTER TABLE `+table+` ADD CONSTRAINT `+table+`_native_fkey FOREIGN KEY(project_id) REFERENCES factory_native_workspaces(id);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='`+table+`'::regclass AND conname='`+table+`_native_only') THEN
 ALTER TABLE `+table+` ADD CONSTRAINT `+table+`_native_only CHECK (`+native+`);
 END IF; END $$`); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN IF NOT EXISTS appliance_project_id TEXT GENERATED ALWAYS AS (CASE WHEN NOT `+native+` THEN project_id ELSE NULL END) STORED,
 ADD COLUMN IF NOT EXISTS native_workspace_id TEXT GENERATED ALWAYS AS (CASE WHEN `+native+` THEN project_id ELSE NULL END) STORED;
 DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='`+table+`'::regclass AND conname='`+table+`_appliance_fkey') THEN
 ALTER TABLE `+table+` ADD CONSTRAINT `+table+`_appliance_fkey FOREIGN KEY(appliance_project_id) REFERENCES projects(id);
 ALTER TABLE `+table+` ADD CONSTRAINT `+table+`_native_fkey FOREIGN KEY(native_workspace_id) REFERENCES factory_native_workspaces(id);
 END IF; END $$;
 ALTER TABLE `+table+` DROP CONSTRAINT IF EXISTS `+table+`_project_id_fkey`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExecutionWorkspace resolves the configured runtime without interpreting a
// native workspace ID as an appliance Project. Dispatch rechecks it atomically.
func (s *Store) ExecutionWorkspace(ctx context.Context, repository collaboration.RepositoryID) (string, *project.NativeEnvironment, error) {
	g, err := s.EnvironmentGrant(ctx, int64(repository))
	if err != nil {
		return "", nil, err
	}
	if g.Native == nil {
		p, err := s.NativeProjectForCollaborationRepository(ctx, repository)
		return p.ID, nil, err
	}
	if g.Validate() != nil || !g.Active {
		return "", nil, ErrAdmissionChanged
	}
	var matches bool
	err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_native_workspaces w JOIN factory_placements p ON p.repository=w.repository
 WHERE w.id=$1 AND w.repository=$2 AND w.runtime=$3 AND w.profile=$4 AND p.runtime=w.runtime AND p.state='active' AND p.generation=$5)`, g.Project, repository, g.Native.Runtime, g.Native.Profile, g.Native.Generation).Scan(&matches)
	if err != nil {
		return "", nil, err
	}
	if !matches {
		return "", nil, factory.ErrPlacement
	}
	return g.Project, g.Native, nil
}

// recordedExecutionWorkspaceTx resolves the selected logical owner for read
// projections, including withdrawn environments. Reading historical material
// does not admit execution; ExecutionWorkspace separately checks live enrollment.
func recordedExecutionWorkspaceTx(ctx context.Context, tx *sql.Tx, repository int64) (string, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM project_environment_grants WHERE repository=$1`, repository).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil {
		var grant project.EnvironmentGrant
		if json.Unmarshal(raw, &grant) != nil || grant.Validate() != nil || grant.Repository != repository {
			return "", ErrAdmissionChanged
		}
		if grant.Native != nil {
			var workspace string
			err = tx.QueryRowContext(ctx, `SELECT id FROM factory_native_workspaces
 WHERE id=$1 AND repository=$2 AND runtime=$3 AND profile=$4`,
				grant.Project, repository, grant.Native.Runtime, grant.Native.Profile).Scan(&workspace)
			return workspace, err
		}
	}
	var workspace string
	var appliance bool
	if err = tx.QueryRowContext(ctx, `SELECT appliance_compatibility FROM schema_version`).Scan(&appliance); err != nil {
		return "", err
	}
	if !appliance {
		return "", ErrNotFound
	}
	err = tx.QueryRowContext(ctx, `SELECT p.id`+nativeProjectCollaborationJoin+` WHERE r.id=$1`, repository).Scan(&workspace)
	return workspace, err
}

// Feed observations use the recorded workspace's canonical repository even
// after its current execution grant is withdrawn.
func recordedWorkspaceRepositoryTx(ctx context.Context, tx *sql.Tx, workspace string, native bool) (int64, error) {
	var repository int64
	if native {
		err := tx.QueryRowContext(ctx, `SELECT repository FROM factory_native_workspaces WHERE id=$1`, workspace).Scan(&repository)
		return repository, err
	}
	var appliance bool
	if err := tx.QueryRowContext(ctx, `SELECT appliance_compatibility FROM schema_version`).Scan(&appliance); err != nil {
		return 0, err
	}
	if !appliance {
		return 0, ErrNotFound
	}
	err := tx.QueryRowContext(ctx, `SELECT r.id`+nativeProjectCollaborationJoin+` WHERE p.id=$1`, workspace).Scan(&repository)
	return repository, err
}

// RecordedExecutionWorkspace supplies the repository owner for observation and
// shutdown even after grants or placement are withdrawn. It admits no new work.
func (s *Store) RecordedExecutionWorkspace(ctx context.Context, repository collaboration.RepositoryID) (string, error) {
	if repository <= 0 {
		return "", collaboration.ErrIdentityConflict
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	workspace, err := recordedExecutionWorkspaceTx(ctx, tx, int64(repository))
	if err != nil {
		return "", err
	}
	return workspace, tx.Commit()
}

// A native workspace is a stable logical owner for isolated checkouts. It never
// identifies a user-owned directory, container Project or current live process.
func bindNativeEnvironmentTx(ctx context.Context, tx *sql.Tx, g project.EnvironmentGrant) (project.EnvironmentGrant, error) {
	if g.Native == nil {
		return g, nil
	}
	n := g.Native
	if n.Validate() != nil || g.Profile != nil {
		return g, factory.ErrPlacement
	}
	var host collaboration.HostID
	if err := tx.QueryRowContext(ctx, `SELECT host FROM collaboration_repositories WHERE id=$1`, g.Repository).Scan(&host); err != nil {
		return g, err
	}
	if g.Active {
		if err := requirePlacementTx(ctx, tx, host, collaboration.RepositoryID(g.Repository), n.Runtime, n.Generation, n.Profile); err != nil {
			return g, err
		}
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM factory_native_workspaces WHERE repository=$1 AND runtime=$2 AND profile=$3`, g.Repository, n.Runtime, n.Profile).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) && g.Active && g.Project == "" {
		id = "p" + collaboration.NewID()[:24]
		_, err = tx.ExecContext(ctx, `INSERT INTO factory_native_workspaces(id,repository,runtime,profile) VALUES($1,$2,$3,$4)`, id, g.Repository, n.Runtime, n.Profile)
	}
	if err != nil {
		return g, err
	}
	if g.Project != "" && g.Project != id {
		return g, factory.ErrPlacement
	}
	g.Project = id
	return g, g.Validate()
}

func nativeDecisionOwner(decision any) *project.NativeEnvironment {
	switch d := decision.(type) {
	case project.RequirementDecision:
		return d.Native
	case project.ApprovalDecision:
		return d.Native
	default:
		return nil
	}
}

func requireNativeDecisionTx(ctx context.Context, tx *sql.Tx, workspace string, native *project.NativeEnvironment) error {
	if native == nil {
		return nil
	}
	var repository collaboration.RepositoryID
	var host collaboration.HostID
	if err := tx.QueryRowContext(ctx, `SELECT w.repository,r.host FROM factory_native_workspaces w JOIN collaboration_repositories r ON r.id=w.repository WHERE w.id=$1 AND w.profile=$2 AND w.runtime=$3`, workspace, native.Profile, native.Runtime).Scan(&repository, &host); err != nil {
		return err
	}
	if err := requirePlacementTx(ctx, tx, host, repository, native.Runtime, native.Generation, native.Profile); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM project_environment_grants WHERE repository=$1 FOR SHARE`, repository).Scan(&raw); err != nil {
		return err
	}
	var g project.EnvironmentGrant
	if json.Unmarshal(raw, &g) != nil || g.Validate() != nil || !g.Active || g.Project != workspace || g.Native == nil || *g.Native != *native {
		return ErrAdmissionChanged
	}
	return nil
}
