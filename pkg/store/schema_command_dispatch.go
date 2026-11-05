package store

// Keep the same immutable identity and terminal-outcome guard on fresh and
// upgraded databases. Only supported unfinished human creations can acquire a
// reservation. Unclaimed history may be conservatively fenced legacy_unknown;
// no transition can release a reservation or an existing legacy fence.
const commandGuardDDL = `CREATE OR REPLACE FUNCTION soda_guard_command() RETURNS trigger AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.type IS DISTINCT FROM OLD.type
    OR NEW.target IS DISTINCT FROM OLD.target OR NEW.principal IS DISTINCT FROM OLD.principal
    OR NEW.digest IS DISTINCT FROM OLD.digest OR NEW.payload IS DISTINCT FROM OLD.payload
    OR NEW.created IS DISTINCT FROM OLD.created
    OR (OLD.finished!='' AND (NEW.finished IS DISTINCT FROM OLD.finished OR NEW.outcome IS DISTINCT FROM OLD.outcome))
    OR (NEW.dispatch_state IS DISTINCT FROM OLD.dispatch_state AND NOT (
      OLD.dispatch_state='unclaimed' AND (
        NEW.dispatch_state='legacy_unknown' OR (
          NEW.dispatch_state='reserved' AND OLD.finished='' AND NEW.finished=''
          AND OLD.type IN ('discussion.comment.create','issue.create')))))
  THEN
    RAISE EXCEPTION 'factory command is immutable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`

// Existing captures have no reliable dispatch history. Conservatively fence all
// of them without changing their payloads or claiming an actual dispatch time.
var CommandDispatchUpgradeDDL = []string{
	`ALTER TABLE factory_commands ADD COLUMN dispatch_state TEXT NOT NULL DEFAULT 'legacy_unknown' CHECK(dispatch_state IN ('unclaimed','reserved','legacy_unknown'))`,
	`ALTER TABLE factory_commands ALTER COLUMN dispatch_state SET DEFAULT 'unclaimed'`,
	commandGuardDDL,
	`UPDATE schema_version SET version=81`,
}

// Schema 81 issue captures predate this one-shot protocol and may already have
// been sent by another adapter. Preserve all payload/outcome bytes while fencing
// that history. The version predicate prevents a concurrent stale opener from
// fencing new schema-82 rows after another opener has committed the upgrade.
var IssueCreateDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='issue.create' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=81)`,
	`UPDATE schema_version SET version=82 WHERE version=81`,
}
