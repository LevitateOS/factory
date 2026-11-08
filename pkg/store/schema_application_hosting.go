package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Application hosting belongs to retained appliance databases. New standalone
// profiles do not create it; its defining DDL and verification stay together.
var applicationHostingStatements = []string{
	`CREATE TABLE applications(id TEXT PRIMARY KEY, administrator_id BIGINT NOT NULL CHECK(administrator_id>0), name TEXT NOT NULL, data_id TEXT NOT NULL UNIQUE, owner_team TEXT NOT NULL, origin TEXT NOT NULL UNIQUE, hostname TEXT NOT NULL UNIQUE, port INTEGER NOT NULL UNIQUE CHECK(port>=20000 AND port<30000), revision BIGINT NOT NULL CHECK(revision>0), observation JSONB CHECK(observation IS NULL OR octet_length(observation::text)<=2048))`,
	`CREATE TABLE application_revisions(application_id TEXT NOT NULL REFERENCES applications(id), revision BIGINT NOT NULL CHECK(revision>0), spec JSONB NOT NULL CHECK(octet_length(spec::text)<=4096), PRIMARY KEY(application_id,revision))`,
	`CREATE TABLE application_projects(application_id TEXT PRIMARY KEY REFERENCES applications(id), repository BIGINT NOT NULL CHECK(repository>0))`,
	ApplicationRestoresDDL,
	ApplicationSetupsDDL,
	ApplicationSetupRenewalsDDL,
	ApplicationBuildsDDL,
	`CREATE UNIQUE INDEX application_restore_pending ON application_restores(application_id) WHERE state='pending'`,
	`ALTER TABLE applications ADD FOREIGN KEY(id,revision) REFERENCES application_revisions(application_id,revision) DEFERRABLE INITIALLY DEFERRED`,
	`CREATE OR REPLACE FUNCTION soda_guard_application() RETURNS trigger AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.administrator_id IS DISTINCT FROM OLD.administrator_id
    OR NEW.name IS DISTINCT FROM OLD.name
    OR NEW.owner_team IS DISTINCT FROM OLD.owner_team OR NEW.origin IS DISTINCT FROM OLD.origin
    OR NEW.hostname IS DISTINCT FROM OLD.hostname OR NEW.port IS DISTINCT FROM OLD.port
    OR (NEW.revision <> OLD.revision AND NEW.revision-1 <> OLD.revision)
  THEN RAISE EXCEPTION 'application identity is immutable'; END IF;
  IF NEW.data_id IS DISTINCT FROM OLD.data_id AND NOT EXISTS(
    SELECT 1 FROM application_restores r WHERE r.application_id=OLD.id AND r.base_revision=OLD.revision
      AND r.target_data=NEW.data_id AND r.state='pending' AND NEW.revision=OLD.revision+1
  ) THEN RAISE EXCEPTION 'application data requires an admitted restore'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`,
	`CREATE TRIGGER application_identity_immutable BEFORE UPDATE ON applications FOR EACH ROW EXECUTE FUNCTION soda_guard_application()`,
	`CREATE TRIGGER application_revision_immutable_update BEFORE UPDATE ON application_revisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER application_revision_immutable_delete BEFORE DELETE ON application_revisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
}

func verifyApplicationHosting(ctx context.Context, tx *sql.Tx) error {
	for _, query := range []string{
		`SELECT id,administrator_id,name,data_id,owner_team,origin,hostname,port,revision,observation FROM applications LIMIT 0`,
		`SELECT id,application_id,base_revision,target_data,state,data FROM application_restores LIMIT 0`,
		`SELECT id,application_id,administrator_id,data_id,origin,hostname,port,state,data FROM application_setups LIMIT 0`,
		`SELECT setup_id,renewed_at,expires_at,administrator_id FROM application_setup_renewals LIMIT 0`,
		`SELECT id,repository,delivery_id,environment,data,setup_id FROM application_builds LIMIT 0`,
		`SELECT application_id,repository FROM application_projects LIMIT 0`,
		`SELECT application_id,revision,spec FROM application_revisions LIMIT 0`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("database schema is incomplete: %w", err)
		}
	}
	return verifyImmutableApplications(ctx, tx)
}

func verifyImmutableApplications(ctx context.Context, t *sql.Tx) error {
	for _, guard := range [][2]string{
		{"application_identity_immutable", "applications"},
		{"application_revision_immutable_update", "application_revisions"},
		{"application_revision_immutable_delete", "application_revisions"},
	} {
		if err := verifyTrigger(ctx, t, guard[0], guard[1]); err != nil {
			return err
		}
	}
	return nil
}
