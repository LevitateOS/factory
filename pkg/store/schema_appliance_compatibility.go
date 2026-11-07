package store

import (
	"context"
	"database/sql"
)

// Preserve appliance data and references in retained shared databases; new
// standalone profiles bind their setup journals only to native workspaces.
var applianceCompatibilityStatements = []string{
	`CREATE TABLE users(id INTEGER PRIMARY KEY CHECK(id>0), login TEXT NOT NULL, name TEXT NOT NULL DEFAULT '')`,
	`CREATE TABLE keys(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), public TEXT NOT NULL, fingerprint TEXT NOT NULL, UNIQUE(user_id,fingerprint))`,
	`CREATE TABLE projects(id TEXT PRIMARY KEY, name TEXT NOT NULL, repository_id INTEGER NOT NULL UNIQUE, owner_id INTEGER NOT NULL REFERENCES users(id), repository TEXT NOT NULL, ip TEXT NOT NULL DEFAULT '', ready BOOLEAN NOT NULL DEFAULT FALSE, creation_profile JSONB CHECK(creation_profile IS NULL OR octet_length(creation_profile::text)<=1024))`,
	`CREATE TABLE memberships(project_id TEXT NOT NULL REFERENCES projects(id), user_id INTEGER NOT NULL REFERENCES users(id), login TEXT NOT NULL, PRIMARY KEY(project_id,user_id), UNIQUE(project_id,login))`,
	`CREATE TABLE project_member_access(project_id TEXT NOT NULL, user_id INTEGER NOT NULL, revision BIGINT NOT NULL CHECK(revision>0), held BOOLEAN NOT NULL, rejoin BOOLEAN NOT NULL, data JSONB NOT NULL, PRIMARY KEY(project_id,user_id), FOREIGN KEY(project_id,user_id) REFERENCES memberships(project_id,user_id))`,
	`CREATE TABLE grant_key_check(id INTEGER PRIMARY KEY CHECK(id=1), ciphertext BYTEA NOT NULL)`,
	`CREATE TABLE identity_connections(id TEXT PRIMARY KEY, owner_id INTEGER NOT NULL, generation INTEGER NOT NULL, state TEXT NOT NULL, data JSONB NOT NULL, credential BYTEA NOT NULL)`,
	`CREATE TABLE identity_grants(id TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES identity_connections(id), user_id INTEGER NOT NULL, project_id TEXT NOT NULL, revision INTEGER NOT NULL, revoked BOOLEAN NOT NULL, data JSONB NOT NULL)`,
	`CREATE UNIQUE INDEX identity_grant_recipient ON identity_grants(connection_id,user_id,project_id) WHERE revoked=FALSE`,
	`CREATE TABLE identity_leases(id TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES identity_connections(id), data JSONB NOT NULL)`,
	`CREATE TABLE identity_events(id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, owner_id INTEGER NOT NULL, connection_id TEXT NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE project_lifecycle_grants(project_id TEXT PRIMARY KEY REFERENCES projects(id), revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE project_maintenance(project_id TEXT PRIMARY KEY REFERENCES projects(id), revision INTEGER NOT NULL, hold BOOLEAN NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE identity_executions(kind TEXT NOT NULL, execution_id TEXT NOT NULL, state TEXT NOT NULL, lease_id TEXT NOT NULL DEFAULT '', data JSONB NOT NULL, PRIMARY KEY(kind,execution_id))`,
	`CREATE OR REPLACE FUNCTION soda_guard_creation_profile() RETURNS trigger AS $$
BEGIN
  IF NEW.creation_profile IS DISTINCT FROM OLD.creation_profile THEN
    RAISE EXCEPTION 'creation profile is immutable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`,
	`CREATE OR REPLACE FUNCTION soda_guard_execution_identity() RETURNS trigger AS $$
BEGIN
  IF NEW.kind IS DISTINCT FROM OLD.kind OR NEW.execution_id IS DISTINCT FROM OLD.execution_id
    OR (NEW.data->>'digest') IS DISTINCT FROM (OLD.data->>'digest')
    OR (OLD.state='terminal' AND NEW.state IS DISTINCT FROM 'terminal')
  THEN
    RAISE EXCEPTION 'execution identity is immutable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`,
	`CREATE TRIGGER immutable_creation_profile BEFORE UPDATE OF creation_profile ON projects FOR EACH ROW EXECUTE FUNCTION soda_guard_creation_profile()`,
	`CREATE TRIGGER identity_execution_immutable BEFORE UPDATE ON identity_executions FOR EACH ROW EXECUTE FUNCTION soda_guard_execution_identity()`,
	`CREATE TRIGGER identity_events_immutable_update BEFORE UPDATE ON identity_events FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER identity_events_immutable_delete BEFORE DELETE ON identity_events FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
}

func verifyApplianceCompatibility(ctx context.Context, tx *sql.Tx) error {
	for _, query := range []string{
		`SELECT id,login,name FROM users LIMIT 0`,
		`SELECT id,user_id,public,fingerprint FROM keys LIMIT 0`,
		`SELECT id,name,repository_id,owner_id,repository,ip,ready,creation_profile FROM projects LIMIT 0`,
		`SELECT project_id,user_id,login FROM memberships LIMIT 0`,
		`SELECT project_id,user_id,revision,held,rejoin,data FROM project_member_access LIMIT 0`,
		`SELECT id,ciphertext FROM grant_key_check LIMIT 0`,
		`SELECT project_id,revision,data FROM project_lifecycle_grants LIMIT 0`,
		`SELECT project_id,revision,hold,data FROM project_maintenance LIMIT 0`,
		`SELECT kind,execution_id,state,lease_id,data FROM identity_executions LIMIT 0`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	if err := verifyImmutableCreationProfile(ctx, tx); err != nil {
		return err
	}
	return verifyImmutableExecutionIdentity(ctx, tx)
}

func verifyImmutableCreationProfile(ctx context.Context, t *sql.Tx) error {
	return verifyTrigger(ctx, t, "immutable_creation_profile", "projects")
}

func verifyImmutableExecutionIdentity(ctx context.Context, t *sql.Tx) error {
	return verifyTrigger(ctx, t, "identity_execution_immutable", "identity_executions")
}
