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

func initializeNativeWorkspaceSchema(ctx context.Context, db *sql.DB) error {
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
	for _, table := range []string{"project_requirement_decisions", "project_approval_decisions", "project_requirement_heads", "project_approval_heads"} {
		native := "native"
		if table == "project_requirement_decisions" || table == "project_approval_decisions" {
			native = "(NULLIF(data->'native','null'::jsonb) IS NOT NULL)"
		} else {
			if _, err = tx.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN IF NOT EXISTS native BOOLEAN NOT NULL DEFAULT false`); err != nil {
				return err
			}
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
