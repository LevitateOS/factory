package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// Schema 106 retains separate automatic external-PR authority. Older writers
// must refuse rather than ignore its current association and author checks.
const schemaFormatVersion = 106

// schemaStatements creates the shared PostgreSQL schema in dependency
// order: tables, indexes, guard functions, then triggers. JSON payloads
// live in JSONB columns, which reject malformed JSON on write; small
// flags are BOOLEAN; insertion-ordered listings use explicit seq columns
// instead of the SQLite rowid.
var schemaStatements = []string{
	`CREATE TABLE factory_activity_settings(repository BIGINT PRIMARY KEY CHECK(repository>0), revision BIGINT NOT NULL CHECK(revision>0), actor_id BIGINT NOT NULL CHECK(actor_id>0), data JSONB NOT NULL)`,
	`CREATE TABLE factory_feed_subscriptions(subscriber TEXT PRIMARY KEY, revision BIGINT NOT NULL CHECK(revision>0), data JSONB NOT NULL)`,
	`CREATE TABLE schema_version(version INTEGER PRIMARY KEY, application_hosting BOOLEAN NOT NULL DEFAULT true, appliance_compatibility BOOLEAN NOT NULL DEFAULT true)`,
	fmt.Sprintf(`INSERT INTO schema_version(version) VALUES(%d)`, schemaFormatVersion),
	`CREATE TABLE factory_departments(id TEXT PRIMARY KEY, owner_id BIGINT NOT NULL CHECK(owner_id>0), data JSONB NOT NULL)`,
	`CREATE TABLE factory_intent_heads(id TEXT PRIMARY KEY, owner_id BIGINT NOT NULL CHECK(owner_id>0), revision TEXT NOT NULL)`,
	`CREATE TABLE factory_intent_revisions(id TEXT PRIMARY KEY, intent TEXT NOT NULL REFERENCES factory_intent_heads(id), data JSONB NOT NULL)`,
	`CREATE TABLE factory_priority_judgments(feed_seq BIGINT UNIQUE, snapshot TEXT PRIMARY KEY, max_calls INTEGER NOT NULL CHECK(max_calls BETWEEN 0 AND 18), data JSONB)`,
	`CREATE TABLE factory_planning(repository BIGINT NOT NULL, issue BIGINT NOT NULL, snapshot TEXT NOT NULL, data JSONB NOT NULL, PRIMARY KEY(repository,issue))`,
	`CREATE TABLE factory_plan_revisions(attempt_root TEXT NOT NULL CHECK(attempt_root ~ '^[a-f0-9]{32}$'), plan_id TEXT NOT NULL CHECK(plan_id ~ '^[a-f0-9]{32}$'), generation BIGINT NOT NULL CHECK(generation>=0), digest TEXT NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'), data JSONB NOT NULL, PRIMARY KEY(attempt_root,plan_id))`,
	`CREATE TABLE factory_plan_heads(attempt_root TEXT PRIMARY KEY CHECK(attempt_root ~ '^[a-f0-9]{32}$'), plan_id TEXT NOT NULL CHECK(plan_id ~ '^[a-f0-9]{32}$'), generation BIGINT NOT NULL CHECK(generation>=0))`,
	`CREATE TABLE factory_plan_tasks(attempt_root TEXT NOT NULL CHECK(attempt_root ~ '^[a-f0-9]{32}$'), plan_id TEXT NOT NULL CHECK(plan_id ~ '^[a-f0-9]{32}$'), task_id TEXT NOT NULL CHECK(task_id ~ '^[a-z][a-z0-9_-]{0,31}$'), revision BIGINT NOT NULL CHECK(revision>=0), state TEXT NOT NULL, data JSONB NOT NULL, PRIMARY KEY(attempt_root,plan_id,task_id))`,
	`CREATE TABLE factory_intent_generations(revision TEXT PRIMARY KEY REFERENCES factory_intent_revisions(id), data JSONB NOT NULL)`,
	`CREATE TABLE factory_intent_tasks(revision TEXT NOT NULL REFERENCES factory_intent_revisions(id), task TEXT NOT NULL, data JSONB NOT NULL, PRIMARY KEY(revision,task))`,
	`CREATE TABLE factory_intent_allowance_heads(intent TEXT PRIMARY KEY REFERENCES factory_intent_heads(id), owner_id BIGINT NOT NULL CHECK(owner_id>0), revision TEXT NOT NULL)`,
	`CREATE TABLE factory_intent_allowances(id TEXT PRIMARY KEY, intent TEXT NOT NULL REFERENCES factory_intent_allowance_heads(intent), data JSONB NOT NULL)`,
	`CREATE TABLE factory_intent_reviews(result_revision TEXT PRIMARY KEY REFERENCES factory_intent_revisions(id), data JSONB NOT NULL)`,
	`CREATE TABLE factory_runs(
seq BIGINT GENERATED ALWAYS AS IDENTITY,
id TEXT PRIMARY KEY,
active BOOLEAN NOT NULL, settled BOOLEAN NOT NULL,
data JSONB NOT NULL)`,
	`CREATE INDEX factory_unsettled_runs ON factory_runs(id) WHERE active OR NOT settled OR COALESCE(data->>'reconciled','false') <> 'true'`,
	`CREATE TABLE factory_commands(
id TEXT PRIMARY KEY, type TEXT NOT NULL, target TEXT NOT NULL DEFAULT '',
principal TEXT NOT NULL, digest TEXT NOT NULL, payload TEXT NOT NULL DEFAULT '' CONSTRAINT factory_commands_payload_check CHECK(` + reviewRetryCommandPayloadCheck + `),
outcome TEXT NOT NULL DEFAULT '' CHECK(char_length(outcome)<=65536),
created TEXT NOT NULL, finished TEXT NOT NULL DEFAULT '',
dispatch_state TEXT NOT NULL DEFAULT 'unclaimed' CHECK(dispatch_state IN ('unclaimed','reserved','legacy_unknown')))`,
	`CREATE INDEX factory_finished_commands_target ON factory_commands(target,type,created,id) WHERE finished<>''`,
	`CREATE TABLE factory_policies(repository BIGINT PRIMARY KEY CHECK(repository>0), revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_capacity(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_operator_grants(repository BIGINT PRIMARY KEY CHECK(repository>0), revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_sponsorships(repository BIGINT NOT NULL CHECK(repository>0), connection TEXT NOT NULL, revision INTEGER NOT NULL, data JSONB NOT NULL, PRIMARY KEY(repository,connection))`,
	`CREATE TABLE factory_connection_usage_budgets(connection TEXT PRIMARY KEY, revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_dispatch(repository BIGINT PRIMARY KEY CHECK(repository>0), revision INTEGER NOT NULL, open BOOLEAN NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_lifecycle_actions(id TEXT PRIMARY KEY, event TEXT NOT NULL, action TEXT NOT NULL, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0), revoked BOOLEAN NOT NULL DEFAULT FALSE, data JSONB NOT NULL)`,
	`CREATE INDEX factory_lifecycle_actions_target ON factory_lifecycle_actions(repository,issue,event)`,
	`CREATE TABLE factory_lifecycle_capabilities(event TEXT PRIMARY KEY, revision TEXT NOT NULL, installation TEXT NOT NULL)`,
	`CREATE TABLE factory_lifecycle_configs(event TEXT PRIMARY KEY, revision BIGINT NOT NULL CHECK(revision>=0), enabled BOOLEAN NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_run_views(seq BIGINT GENERATED ALWAYS AS IDENTITY, run TEXT PRIMARY KEY REFERENCES factory_runs(id), repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL DEFAULT 0 CHECK(issue>=0), attempt TEXT NOT NULL DEFAULT '' CHECK(char_length(attempt)<=64))`,
	`CREATE TABLE factory_assignments(seq BIGINT GENERATED ALWAYS AS IDENTITY, id TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK (
` + deliveryRepairAssignmentCheck + `
), run TEXT NOT NULL, stage TEXT NOT NULL, revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_attempt_allowances(repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK (
` + deliveryRepairAllowanceCheck + `
), root_assignment TEXT PRIMARY KEY REFERENCES factory_assignments(id), revision BIGINT NOT NULL CHECK(revision>=0), data JSONB NOT NULL)`,
	`CREATE INDEX factory_attempt_allowance_issues ON factory_attempt_allowances(repository,issue)`,
	`CREATE INDEX factory_active_attempts ON factory_attempt_allowances(repository) WHERE data->>'active'='true'`,
	`CREATE UNIQUE INDEX factory_unfinished_assignment ON factory_assignments(repository,issue) WHERE stage='assigned' AND issue>0`,
	`CREATE UNIQUE INDEX factory_unfinished_completion_assignment ON factory_assignments(repository,(data->'subject'->'completion'->>'completion_id'),(data->'subject'->'completion'->>'cycle_id')) WHERE stage='assigned' AND issue=0`,
	`CREATE INDEX factory_assignments_issue_history ON factory_assignments(repository,issue,seq DESC)`,
	`CREATE INDEX factory_finished_children ON factory_assignments(seq) WHERE stage='finished' AND data->>'publication_assignment'<>id`,
	`CREATE TABLE factory_feed_posts(seq BIGINT NOT NULL DEFAULT nextval(pg_get_serial_sequence('factory_assignments','seq')), id TEXT PRIMARY KEY CHECK(id ~ '^[a-f0-9]{32}$'), repository BIGINT NOT NULL CHECK(repository>0), revision BIGINT NOT NULL CHECK(revision>=0), author TEXT NOT NULL CHECK(octet_length(author) BETWEEN 1 AND 160), body TEXT NOT NULL CHECK(char_length(body) BETWEEN 1 AND 4096), idempotency TEXT NOT NULL CHECK(octet_length(idempotency) BETWEEN 1 AND 64), data JSONB NOT NULL, UNIQUE(repository,author,idempotency))`,
	`CREATE INDEX factory_feed_posts_repository ON factory_feed_posts(repository,seq DESC)`,
	`CREATE INDEX factory_feed_posts_parent ON factory_feed_posts(repository,(data->>'parent_id'),seq)`,
	`CREATE TABLE factory_post_intakes(post TEXT PRIMARY KEY REFERENCES factory_feed_posts(id), revision BIGINT NOT NULL CHECK(revision>=0), intake TEXT NOT NULL, judgment TEXT NOT NULL, route TEXT NOT NULL DEFAULT '', draft TEXT NOT NULL, effect TEXT NOT NULL, issue BIGINT NOT NULL DEFAULT 0 CHECK(issue>=0), attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 6), data JSONB NOT NULL)`,
	`CREATE TABLE factory_reservations(seq BIGINT GENERATED ALWAYS AS IDENTITY, assignment TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), connection TEXT NOT NULL, state TEXT NOT NULL, data JSONB NOT NULL)`,
	`CREATE INDEX factory_reservations_connection_state ON factory_reservations(connection,state)`,
	`CREATE TABLE factory_usage(run TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), connection TEXT NOT NULL, minutes INTEGER NOT NULL CHECK(minutes>=0), started_at TIMESTAMPTZ NOT NULL, ended_at TIMESTAMPTZ NOT NULL CHECK(ended_at>=started_at), data JSONB NOT NULL)`,
	`CREATE INDEX factory_usage_connection_ended ON factory_usage(connection,ended_at)`,
	`CREATE TABLE issue_acceptance_decisions(id TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), predecessor TEXT NOT NULL DEFAULT '', data JSONB NOT NULL)`,
	`CREATE TABLE issue_acceptance_heads(repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), decision TEXT NOT NULL, PRIMARY KEY(repository,issue))`,
	`CREATE TABLE issue_acceptance_withdrawals(repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), decision TEXT NOT NULL, withdrawer BIGINT NOT NULL CHECK(withdrawer>0), PRIMARY KEY(repository,issue,decision))`,
	`CREATE TABLE issue_controls(repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), revision INTEGER NOT NULL CHECK(revision>0), data JSONB NOT NULL, PRIMARY KEY(repository,issue))`,
	`CREATE TABLE intake_deliveries(delivery TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), kind TEXT NOT NULL, received_at INTEGER NOT NULL)`,
	`CREATE TABLE factory_publications(seq BIGINT GENERATED ALWAYS AS IDENTITY, assignment TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), run TEXT NOT NULL, stage TEXT NOT NULL, revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_check_assessments(repository BIGINT NOT NULL CHECK(repository>0), pr BIGINT NOT NULL CHECK(pr>0), revision INTEGER NOT NULL, data JSONB NOT NULL, PRIMARY KEY(repository,pr))`,
	`CREATE TABLE factory_merges(feed_seq BIGINT UNIQUE, seq BIGINT GENERATED ALWAYS AS IDENTITY, publication TEXT PRIMARY KEY, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0), pr BIGINT NOT NULL CHECK(pr>0), stage TEXT NOT NULL, revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE factory_readiness_budget(id INTEGER PRIMARY KEY CHECK(id=1), generation BIGINT NOT NULL CHECK(generation>=1), next_turn BIGINT NOT NULL CHECK(next_turn>=1))`,
	`INSERT INTO factory_readiness_budget(id,generation,next_turn) VALUES(1,1,1)`,
	`CREATE TABLE factory_readiness_sources(
id TEXT PRIMARY KEY CHECK(octet_length(id) BETWEEN 1 AND 160),
delivery TEXT NOT NULL DEFAULT '' CHECK(octet_length(delivery)<=128),
repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0),
creation_verified BOOLEAN NOT NULL DEFAULT FALSE,
creator_id BIGINT NOT NULL DEFAULT 0 CHECK(creator_id>=0), creator_write BOOLEAN NOT NULL DEFAULT FALSE,
native_issue_id BIGINT NOT NULL DEFAULT 0 CHECK(native_issue_id>=0), created_unix BIGINT NOT NULL DEFAULT 0 CHECK(created_unix>=0),
title_digest TEXT NOT NULL DEFAULT '' CHECK(octet_length(title_digest) IN (0,64)),
content_digest TEXT NOT NULL DEFAULT '' CHECK(octet_length(content_digest) IN (0,64)),
generation BIGINT NOT NULL DEFAULT 0 CHECK(generation>=0), turn BIGINT NOT NULL CHECK(turn>0),
retry_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch', attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 6),
CHECK(NOT creation_verified OR (creator_id>0 AND native_issue_id>0 AND created_unix>0 AND octet_length(title_digest)=64 AND octet_length(content_digest)=64)))`,
	`CREATE TABLE factory_creation_cursors(repository BIGINT PRIMARY KEY CHECK(repository>0), task_id BIGINT NOT NULL CHECK(task_id>=0))`,
	`CREATE TABLE factory_readiness_nodes(
source TEXT NOT NULL REFERENCES factory_readiness_sources(id) ON DELETE CASCADE,
repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0),
state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','scanning','done')),
cursor_repository BIGINT NOT NULL DEFAULT 0 CHECK(cursor_repository>=0), cursor_issue BIGINT NOT NULL DEFAULT 0 CHECK(cursor_issue>=0),
PRIMARY KEY(source,repository,issue), CHECK((cursor_repository=0)=(cursor_issue=0)))`,
	`CREATE TABLE factory_priority_scans(
scan TEXT PRIMARY KEY, native_revision BIGINT NOT NULL CHECK(native_revision>=0),
local_digest TEXT NOT NULL, subjects JSONB NOT NULL, cursor INTEGER NOT NULL DEFAULT 0 CHECK(cursor>=0))`,
	`CREATE TABLE factory_priority_candidates(
scan TEXT NOT NULL REFERENCES factory_priority_scans(scan) ON DELETE CASCADE,
subject TEXT NOT NULL, repository BIGINT NOT NULL CHECK(repository>0), issue BIGINT NOT NULL CHECK(issue>0),
candidate JSONB, exclusion TEXT, fingerprint TEXT NOT NULL DEFAULT '',
PRIMARY KEY(scan,subject))`,
	FactoryCompletionsDDL,
	`CREATE FUNCTION soda_feed_merge_order() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.stage='merged' AND NEW.feed_seq IS NULL THEN
			NEW.feed_seq := nextval(pg_get_serial_sequence('factory_assignments','seq'));
		END IF;
		RETURN NEW;
	END $$`,
	`CREATE TRIGGER factory_merge_feed_order BEFORE INSERT OR UPDATE ON factory_merges
	FOR EACH ROW EXECUTE FUNCTION soda_feed_merge_order()`,
	`CREATE FUNCTION soda_feed_judgment_order() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.data IS NOT NULL AND NEW.feed_seq IS NULL THEN
			NEW.feed_seq := nextval(pg_get_serial_sequence('factory_assignments','seq'));
		END IF;
		RETURN NEW;
	END $$`,
	`CREATE TRIGGER factory_judgment_feed_order BEFORE INSERT OR UPDATE ON factory_priority_judgments
	FOR EACH ROW EXECUTE FUNCTION soda_feed_judgment_order()`,
	FactoryReassessmentsDDL,
	FactoryStallEventsDDL,
	FactoryMCPCapabilitiesDDL,
	`CREATE TABLE project_environment_grants(repository BIGINT PRIMARY KEY CHECK(repository>0), revision INTEGER NOT NULL, data JSONB NOT NULL)`,
	`CREATE TABLE project_requirement_decisions(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, predecessor TEXT NOT NULL DEFAULT '', data JSONB NOT NULL)`,
	`CREATE TABLE project_requirement_heads(project_id TEXT PRIMARY KEY, decision TEXT NOT NULL)`,
	`CREATE TABLE project_approval_decisions(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, predecessor TEXT NOT NULL DEFAULT '', data JSONB NOT NULL)`,
	`CREATE TABLE project_approval_heads(project_id TEXT PRIMARY KEY, decision TEXT NOT NULL)`,
	`CREATE TABLE project_preparations(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, role TEXT NOT NULL, revision INTEGER NOT NULL, requirements TEXT NOT NULL, approval TEXT NOT NULL, data JSONB NOT NULL)`,
	`CREATE INDEX project_preparations_project ON project_preparations(project_id)`,
	`CREATE OR REPLACE FUNCTION soda_reject_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'soda: % is immutable', TG_TABLE_NAME;
END $$ LANGUAGE plpgsql`,
	`CREATE OR REPLACE FUNCTION soda_guard_run_binding() RETURNS trigger AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id
    OR (NEW.data->>'project_id') IS DISTINCT FROM (OLD.data->>'project_id')
    OR (NEW.data->>'role') IS DISTINCT FROM (OLD.data->>'role')
    OR (NEW.data->>'input_sha') IS DISTINCT FROM (OLD.data->>'input_sha')
    OR (NEW.data->>'started') IS DISTINCT FROM (OLD.data->>'started')
    OR (NEW.data->>'deadline') IS DISTINCT FROM (OLD.data->>'deadline')
    OR (NEW.data->>'image') IS DISTINCT FROM (OLD.data->>'image')
    OR (NEW.data->>'harness') IS DISTINCT FROM (OLD.data->>'harness')
    OR (NEW.data->>'model') IS DISTINCT FROM (OLD.data->>'model')
    OR (coalesce(OLD.data->>'outcome','')!='' AND coalesce(NEW.data->>'outcome','') IS DISTINCT FROM (OLD.data->>'outcome'))
    OR ((OLD.data->>'reconciled')::boolean IS TRUE AND (NEW.data->>'reconciled')::boolean IS NOT TRUE)
  THEN
    RAISE EXCEPTION 'factory run binding is immutable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`,
	commandGuardDDL,
	`CREATE OR REPLACE FUNCTION soda_guard_preparation_refs() RETURNS trigger AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.project_id IS DISTINCT FROM OLD.project_id
    OR NEW.role IS DISTINCT FROM OLD.role OR NEW.requirements IS DISTINCT FROM OLD.requirements
    OR NEW.approval IS DISTINCT FROM OLD.approval
  THEN
    RAISE EXCEPTION 'preparation references are immutable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`,
	`CREATE TRIGGER factory_run_binding_immutable BEFORE UPDATE ON factory_runs FOR EACH ROW EXECUTE FUNCTION soda_guard_run_binding()`,
	`CREATE TRIGGER factory_command_immutable BEFORE UPDATE ON factory_commands FOR EACH ROW EXECUTE FUNCTION soda_guard_command()`,
	`CREATE TRIGGER factory_plan_revision_immutable_update BEFORE UPDATE ON factory_plan_revisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER factory_plan_revision_immutable_delete BEFORE DELETE ON factory_plan_revisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER factory_run_view_immutable_update BEFORE UPDATE ON factory_run_views FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER factory_run_view_immutable_delete BEFORE DELETE ON factory_run_views FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	FactoryCompletionTriggers,
	FactoryCompletionDeleteTrigger,
	FactoryReassessmentDeleteTrigger,
	FactoryMCPCapabilityDeleteTrigger,
	`CREATE TRIGGER issue_acceptance_decision_immutable_update BEFORE UPDATE ON issue_acceptance_decisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER issue_acceptance_decision_immutable_delete BEFORE DELETE ON issue_acceptance_decisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER issue_acceptance_withdrawal_immutable_update BEFORE UPDATE ON issue_acceptance_withdrawals FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER issue_acceptance_withdrawal_immutable_delete BEFORE DELETE ON issue_acceptance_withdrawals FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER project_requirement_decision_immutable_update BEFORE UPDATE ON project_requirement_decisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER project_requirement_decision_immutable_delete BEFORE DELETE ON project_requirement_decisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER project_approval_decision_immutable_update BEFORE UPDATE ON project_approval_decisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER project_approval_decision_immutable_delete BEFORE DELETE ON project_approval_decisions FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`,
	`CREATE TRIGGER preparation_refs_immutable BEFORE UPDATE ON project_preparations FOR EACH ROW EXECUTE FUNCTION soda_guard_preparation_refs()`,
}

// schemaUpgrade68To69 migrates a retained 68 database — which carries the
// application builds table but no setup-renewals journal — to 69. It runs
// inside the open transaction, so any failure rolls the database back to 68
// without mutation.
var schemaUpgrade68To69 = []string{
	ApplicationSetupRenewalsDDL,
	`UPDATE schema_version SET version=69`,
}

// migrateSchema68To69 upgrades a genuine 68 database in place. A 68 marker
// beside an already-present renewals journal is not a 68 database — it is
// shape corruption — and is refused without mutation like any other foreign
// version.
func migrateSchema68To69(ctx context.Context, t *sql.Tx) error {
	var journal int
	if err := t.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='application_setup_renewals' AND table_type='BASE TABLE'`).Scan(&journal); err != nil {
		return err
	}
	if journal != 0 {
		return errors.New("database schema differs from this application")
	}
	for _, statement := range schemaUpgrade68To69 {
		if _, err := t.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate database schema 68 to 69: %w", err)
		}
	}
	return nil
}

// SchemaVersion is a format identifier. Adopted upgrades from 68 through the
// current format run inside the open transaction; foreign formats are refused.
func SchemaVersion() int { return schemaFormatVersion }

func schemaPresent(ctx context.Context, t *sql.Tx) (bool, error) {
	var hasVersion int
	if err := t.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='schema_version'`).Scan(&hasVersion); err != nil {
		return false, err
	}
	return hasVersion != 0, nil
}

func refuseUnversionedDatabase(ctx context.Context, t *sql.Tx) error {
	var objects int
	if err := t.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'`).Scan(&objects); err != nil {
		return err
	}
	if objects != 0 {
		return errors.New("refusing an unversioned nonempty database")
	}
	return nil
}

func storedSchemaVersion(ctx context.Context, t *sql.Tx) (int, error) {
	var count int
	var minimum, maximum sql.NullInt64
	if err := t.QueryRowContext(ctx, `SELECT count(*),min(version),max(version) FROM schema_version`).Scan(&count, &minimum, &maximum); err != nil {
		return 0, err
	}
	if count != 1 || !minimum.Valid || minimum.Int64 < 1 || minimum.Int64 != maximum.Int64 {
		return 0, errors.New("invalid database schema version record")
	}
	return int(minimum.Int64), nil
}

func verifyRequiredColumns(ctx context.Context, t *sql.Tx) error {
	for _, query := range []string{
		`SELECT repository,revision,actor_id,data FROM factory_activity_settings LIMIT 0`,
		`SELECT subscriber,revision,data FROM factory_feed_subscriptions LIMIT 0`,
		`SELECT feed_seq,snapshot,max_calls,data FROM factory_priority_judgments LIMIT 0`,
		`SELECT seq,id,repository,revision,author,body,idempotency,data FROM factory_feed_posts LIMIT 0`,
		`SELECT post,revision,intake,judgment,route,draft,effect,issue,attempts,data FROM factory_post_intakes LIMIT 0`,
		`SELECT id,project_id,role,revision,requirements,approval,data FROM project_preparations LIMIT 0`,
		`SELECT seq,id,active,settled,data FROM factory_runs LIMIT 0`,
		`SELECT id,type,target,principal,digest,payload,outcome,created,finished,dispatch_state FROM factory_commands LIMIT 0`,
		`SELECT repository,revision,data FROM factory_policies LIMIT 0`,
		`SELECT id,revision,data FROM factory_capacity LIMIT 0`,
		`SELECT repository,revision,data FROM factory_operator_grants LIMIT 0`,
		`SELECT repository,connection,revision,data FROM factory_sponsorships LIMIT 0`,
		`SELECT connection,revision,data FROM factory_connection_usage_budgets LIMIT 0`,
		`SELECT repository,revision,open,data FROM factory_dispatch LIMIT 0`,
		`SELECT seq,id,repository,issue,run,stage,revision,data FROM factory_assignments LIMIT 0`,
		`SELECT seq,assignment,repository,connection,state,data FROM factory_reservations LIMIT 0`,
		`SELECT run,repository,connection,minutes,started_at,ended_at,data FROM factory_usage LIMIT 0`,
		`SELECT id,event,action,repository,issue,attempts,revoked,data FROM factory_lifecycle_actions LIMIT 0`,
		`SELECT event,revision,installation FROM factory_lifecycle_capabilities LIMIT 0`,
		`SELECT event,revision,enabled,data FROM factory_lifecycle_configs LIMIT 0`,
		`SELECT intent,owner_id,revision FROM factory_intent_allowance_heads LIMIT 0`,
		`SELECT id,intent,data FROM factory_intent_allowances LIMIT 0`,
		`SELECT result_revision,data FROM factory_intent_reviews LIMIT 0`,
		`SELECT id,repository,issue,predecessor,data FROM issue_acceptance_decisions LIMIT 0`,
		`SELECT repository,issue,decision FROM issue_acceptance_heads LIMIT 0`,
		`SELECT repository,issue,decision,withdrawer FROM issue_acceptance_withdrawals LIMIT 0`,
		`SELECT repository,revision,data FROM project_environment_grants LIMIT 0`,
		`SELECT id,project_id,predecessor,data FROM project_requirement_decisions LIMIT 0`,
		`SELECT project_id,decision FROM project_requirement_heads LIMIT 0`,
		`SELECT id,project_id,predecessor,data FROM project_approval_decisions LIMIT 0`,
		`SELECT project_id,decision FROM project_approval_heads LIMIT 0`,
		`SELECT seq,run,repository,issue,attempt FROM factory_run_views LIMIT 0`,
		`SELECT seq,assignment,repository,issue,run,stage,revision,data FROM factory_publications LIMIT 0`,
		`SELECT feed_seq,seq,publication,repository,issue,pr,stage,revision,data FROM factory_merges LIMIT 0`,
		`SELECT feed_seq,repository,generation,event,publication,occurrence,data FROM factory_completions LIMIT 0`,
		`SELECT cycle,repository,generation,event,state,data FROM factory_reassessments LIMIT 0`,
		`SELECT id,generation,next_turn FROM factory_readiness_budget LIMIT 0`,
		`SELECT id,delivery,repository,issue,creation_verified,creator_id,creator_write,native_issue_id,created_unix,title_digest,content_digest,generation,turn,retry_at,attempts FROM factory_readiness_sources LIMIT 0`,
		`SELECT repository,task_id FROM factory_creation_cursors LIMIT 0`,
		`SELECT source,repository,issue,state,cursor_repository,cursor_issue FROM factory_readiness_nodes LIMIT 0`,
	} {
		rows, err := t.QueryContext(ctx, query)
		if err != nil {
			return errors.New("database schema is incomplete")
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func verifyTrigger(ctx context.Context, t *sql.Tx, name, table string) error {
	var found int
	if err := t.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.triggers WHERE trigger_name=$1 AND event_object_table=$2`, name, table).Scan(&found); err != nil {
		return err
	}
	if found != 1 {
		return errors.New("database schema is incomplete")
	}
	return nil
}

func verifyImmutablePreparationRefs(ctx context.Context, t *sql.Tx) error {
	return verifyTrigger(ctx, t, "preparation_refs_immutable", "project_preparations")
}

func loadSchemaVersion(ctx context.Context, t *sql.Tx) (int, error) {
	present, err := schemaPresent(ctx, t)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, refuseUnversionedDatabase(ctx, t)
	}
	return storedSchemaVersion(ctx, t)
}

// schemaOpenMu serializes schema opens inside this process. Concurrent opens
// must reread the committed current marker before attempting upgrade DDL.
// Cross-process losers are covered by the version reread below, which runs
// after the losing transaction rolls back.
var schemaOpenMu sync.Mutex

func initializeSchema(ctx context.Context, db *sql.DB, standalone bool) error {
	schemaOpenMu.Lock()
	defer schemaOpenMu.Unlock()
	inner, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	t := inner
	rolledBack := false
	rollback := func() {
		if !rolledBack {
			rolledBack = true
			_ = t.Rollback()
		}
	}
	defer rollback()
	version, err := loadSchemaVersion(ctx, t)
	if err != nil {
		return err
	}
	if version >= 68 && version <= SchemaVersion() {
		if err := verifyCommandPayloadCheck(ctx, t, version); err != nil {
			return err
		}
	}
	if version == 0 {
		// DDL carries no parameters.
		for _, statement := range schemaStatements {
			if _, err = inner.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create current database schema: %w", err)
			}
		}
		if standalone {
			if _, err = t.ExecContext(ctx, `UPDATE schema_version SET application_hosting=false, appliance_compatibility=false`); err != nil {
				return err
			}
		} else {
			for _, statement := range append(applianceCompatibilityStatements, applicationHostingStatements...) {
				if _, err = t.ExecContext(ctx, statement); err != nil {
					return fmt.Errorf("create application hosting schema: %w", err)
				}
			}
		}
	} else if version == 68 {
		if err = migrateSchema68To69(ctx, t); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, err)
		}
		version = 69
	} else if version != SchemaVersion() {
		// Each version in this contiguous range has an explicit upgrade below.
		// Final verification still refuses a missing or incomplete migration.
		if version < 69 || version > SchemaVersion() {
			return errors.New("database schema differs from this application")
		}
	}
	if version == 69 {
		for _, statement := range FeedPostsUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate feed posts: %w", err))
			}
		}
		version = 70
	}
	if version == 70 {
		for _, statement := range HumanCommentJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human comment journal: %w", err))
			}
		}
		version = 71
	}
	if version == 71 {
		for _, statement := range HumanEditJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human edit journal: %w", err))
			}
		}
		version = 72
	}
	if version == 72 {
		for _, statement := range HumanIssueCreateJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human issue creation journal: %w", err))
			}
		}
		version = 73
	}
	if version == 73 {
		for _, statement := range HumanLockJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human lock journal: %w", err))
			}
		}
		version = 74
	}
	if version == 74 {
		for _, statement := range HumanReviewJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human review journal: %w", err))
			}
		}
		version = 75
	}
	if version == 75 {
		for _, statement := range HumanInlineJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human inline journal: %w", err))
			}
		}
		version = 76
	}
	if version == 76 {
		for _, statement := range HumanThreadJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human thread journal: %w", err))
			}
		}
		version = 77
	}
	if version == 77 {
		for _, statement := range HumanReviewRequestJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human reviewer request journal: %w", err))
			}
		}
		version = 78
	}
	if version == 78 {
		for _, statement := range HumanMergeJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human merge journal: %w", err))
			}
		}
		version = 79
	}
	if version == 79 {
		for _, statement := range HumanPullCreateJournalUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human pull creation journal: %w", err))
			}
		}
		version = 80
	}
	if version == 80 {
		for _, statement := range CommandDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate command dispatch fence: %w", err))
			}
		}
		version = 81
	}
	if version == 81 {
		for _, statement := range IssueCreateDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human issue creation dispatch: %w", err))
			}
		}
		version = 82
	}
	if version == 82 {
		for _, statement := range HumanStateDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human state dispatch: %w", err))
			}
		}
		version = 83
	}
	if version == 83 {
		for _, statement := range HumanEditDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human edit dispatch: %w", err))
			}
		}
		version = 84
	}
	if version == 84 {
		for _, statement := range HumanLockDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human lock dispatch: %w", err))
			}
		}
		version = 85
	}
	if version == 85 {
		for _, statement := range HumanThreadDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human thread dispatch: %w", err))
			}
		}
		version = 86
	}
	if version == 86 {
		for _, statement := range HumanReviewRequestDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human reviewer-request dispatch: %w", err))
			}
		}
		version = 87
	}
	if version == 87 {
		for _, statement := range HumanPullCreateDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human pull creation dispatch: %w", err))
			}
		}
		version = 88
	}
	if version == 88 {
		for _, statement := range HumanReviewDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human review dispatch: %w", err))
			}
		}
		version = 89
	}
	if version == 89 {
		for _, statement := range HumanBranchCreateDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human branch creation dispatch: %w", err))
			}
		}
		version = 90
	}
	if version == 90 {
		for _, statement := range HumanInlineDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human inline dispatch: %w", err))
			}
		}
		version = 91
	}
	if version == 91 {
		for _, statement := range HumanMergeDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate human merge dispatch: %w", err))
			}
		}
		version = 92
	}
	if version == 92 {
		// Every retained format included application hosting. Record that
		// obligation without changing its tables, rows or execution authority.
		if _, err = t.ExecContext(ctx, `ALTER TABLE schema_version ADD COLUMN IF NOT EXISTS application_hosting BOOLEAN NOT NULL DEFAULT true; UPDATE schema_version SET version=93`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("record retained schema composition: %w", err))
		}
		version = 93
	}
	if version == 93 {
		if _, err = t.ExecContext(ctx, `ALTER TABLE schema_version ADD COLUMN IF NOT EXISTS appliance_compatibility BOOLEAN NOT NULL DEFAULT true; UPDATE schema_version SET version=94`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("record retained appliance compatibility: %w", err))
		}
		version = 94
	}
	if version == 94 {
		if _, err = t.ExecContext(ctx, commandGuardDDL); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate conditional ref dispatch: %w", err))
		}
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=95`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, err)
		}
		version = 95
	}
	if version == 95 {
		for _, statement := range IssueDependencyDispatchUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate issue dependency dispatch: %w", err))
			}
		}
		version = 96
	}
	if version == 96 {
		// This is a domain-format boundary, not new authority. Existing command
		// payloads, receipts, deadlines and dispatch states remain unchanged.
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=97 WHERE version=96`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate retained publication owner format: %w", err))
		}
		version = 97
	}
	if version == 97 {
		// Older writers cannot preserve captured controller grant references.
		// Changing the marker grants no old command new authority.
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=98 WHERE version=97`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate captured controller grant format: %w", err))
		}
		version = 98
	}
	if version == 98 {
		// A review-only retained owner has no publication send deadline. Older
		// writers must refuse instead of dropping that distinction.
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=99 WHERE version=98`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate retained review owner format: %w", err))
		}
		version = 99
	}
	if version == 99 {
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=100 WHERE version=99`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate finite merge-only retry format: %w", err))
		}
		version = 100
	}
	if version == 100 {
		// Older native constructors ignore the operation grant captured in a
		// workspace/preparation. Fence those writers without rewriting any
		// retained decision, command, deadline or uncertain effect.
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=101 WHERE version=100`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate host-claimed preparation format: %w", err))
		}
		version = 101
	}
	if version == 101 {
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=102 WHERE version=101`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate retained review setup capture format: %w", err))
		}
		version = 102
	}
	if version == 102 {
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=103 WHERE version=102`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate unallocated merge retirement format: %w", err))
		}
		version = 103
	}
	if version == 103 {
		for _, statement := range reviewRetryCommandUpgradeDDL {
			if _, err = t.ExecContext(ctx, statement); err != nil {
				rollback()
				return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate captured review retry format: %w", err))
			}
		}
		version = 104
	}
	if version == 104 {
		// The JSON contracts changed; their command payload bound did not.
		// Preserve every command/receipt and refuse any unknown prior shape.
		if err = verifyCommandPayloadCheck(ctx, t, 104); err != nil {
			return err
		}
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=105 WHERE version=104`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate external publication contract: %w", err))
		}
		version = 105
	}
	if version == 105 {
		if err = verifyCommandPayloadCheck(ctx, t, 105); err != nil {
			return err
		}
		if _, err = t.ExecContext(ctx, `UPDATE schema_version SET version=106 WHERE version=105`); err != nil {
			rollback()
			return reopenAfterLostUpgradeRace(ctx, db, fmt.Errorf("migrate automatic external publication authority: %w", err))
		}
	}
	if err = verifyCommandPayloadCheck(ctx, t, SchemaVersion()); err != nil {
		return err
	}
	if err = verifyCurrentSchema(ctx, t); err != nil {
		return err
	}
	rolledBack = true
	return t.Commit()
}

// reopenAfterLostUpgradeRace rereads the schema marker after a failed upgrade.
// When a concurrent opener committed the upgrade meanwhile, the
// database is now current: it still faces the full verification before the
// open succeeds. Any other outcome returns the original upgrade failure, so
// genuine corruption keeps its refusal signal and the unmigrated database
// is left untouched.
func reopenAfterLostUpgradeRace(ctx context.Context, db *sql.DB, upgradeErr error) error {
	inner, err := db.BeginTx(ctx, nil)
	if err != nil {
		return upgradeErr
	}
	t := inner
	defer func() { _ = t.Rollback() }()
	version, err := loadSchemaVersion(ctx, t)
	if err != nil {
		return upgradeErr
	}
	if version != SchemaVersion() {
		return upgradeErr
	}
	if err = verifyCommandPayloadCheck(ctx, t, version); err != nil {
		return upgradeErr
	}
	if err = verifyCurrentSchema(ctx, t); err != nil {
		return upgradeErr
	}
	return nil
}

// verifyCurrentSchema runs the current shape check: every required column
// query plus the immutability trigger guards.
func verifyCurrentSchema(ctx context.Context, t *sql.Tx) error {
	if err := verifyRequiredColumns(ctx, t); err != nil {
		return err
	}
	if err := verifyTrigger(ctx, t, "factory_command_immutable", "factory_commands"); err != nil {
		return err
	}
	if err := verifyImmutablePreparationRefs(ctx, t); err != nil {
		return err
	}
	var hosting, appliance bool
	if err := t.QueryRowContext(ctx, `SELECT application_hosting,appliance_compatibility FROM schema_version`).Scan(&hosting, &appliance); err != nil {
		return err
	}
	if hosting && !appliance {
		return errors.New("application hosting requires appliance compatibility")
	}
	if appliance {
		if err := verifyApplianceCompatibility(ctx, t); err != nil {
			return err
		}
	}
	if hosting {
		return verifyApplicationHosting(ctx, t)
	}
	return nil
}
