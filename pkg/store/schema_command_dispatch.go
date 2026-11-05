package store

// Keep the same immutable identity and terminal-outcome guard on fresh and
// upgraded databases. Only an unfinished comment can acquire the one-shot fence;
// neither observations nor restarts may release it.
const commandGuardDDL = `CREATE OR REPLACE FUNCTION soda_guard_command() RETURNS trigger AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.type IS DISTINCT FROM OLD.type
    OR NEW.target IS DISTINCT FROM OLD.target OR NEW.principal IS DISTINCT FROM OLD.principal
    OR NEW.digest IS DISTINCT FROM OLD.digest OR NEW.payload IS DISTINCT FROM OLD.payload
    OR NEW.created IS DISTINCT FROM OLD.created
    OR (OLD.finished!='' AND (NEW.finished IS DISTINCT FROM OLD.finished OR NEW.outcome IS DISTINCT FROM OLD.outcome))
    OR (NEW.dispatch_state IS DISTINCT FROM OLD.dispatch_state AND
      (OLD.dispatch_state!='unclaimed' OR NEW.dispatch_state!='reserved'
        OR OLD.finished!='' OR NEW.finished!='' OR OLD.type!='discussion.comment.create'))
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
