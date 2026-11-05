package store

// Keep the same immutable identity and terminal-outcome guard on fresh and
// upgraded databases. Only supported unfinished human effects can acquire a
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
          AND OLD.type IN ('discussion.comment.create','issue.create','discussion.state.apply','discussion.edit','discussion.lock.apply','pull_request.review.thread.resolve','pull_request.review.request','pull_request.create','pull_request.review.submit')))))
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

// Earlier state captures may have been dispatched without this local fence.
// Retain their exact records and conservatively fence unknown dispatch history.
// A stale concurrent opener must not fence captures created after the upgrade.
var HumanStateDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='discussion.state.apply' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=82)`,
	`UPDATE schema_version SET version=83 WHERE version=82`,
}

// Existing edit captures have no reliable history under this local protocol.
// Preserve all content/conditions/outcomes while fencing that history unknown.
var HumanEditDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='discussion.edit' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=83)`,
	`UPDATE schema_version SET version=84 WHERE version=83`,
}

// Lock captures predating this protocol may already have reached a provider.
// Retain exact intent/reason/outcomes while conservatively fencing that history.
var HumanLockDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='discussion.lock.apply' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=84)`,
	`UPDATE schema_version SET version=85 WHERE version=84`,
}

// Earlier thread captures may already have reached an idempotent remote host.
// Retain their exact scope, full/unknown anchor and outcomes; no local retry
// permission can be inferred from an old unclaimed default.
var HumanThreadDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='pull_request.review.thread.resolve' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=85)`,
	`UPDATE schema_version SET version=86 WHERE version=85`,
}

// Reviewer-request captures from earlier adapters have unknown local dispatch
// history. Preserve exact user/team intent and outcomes while fencing that
// history; a stale opener cannot fence captures made after this upgrade.
var HumanReviewRequestDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='pull_request.review.request' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=86)`,
	`UPDATE schema_version SET version=87 WHERE version=86`,
}

// Earlier PR creation captures may already have reached an adapter. Preserve
// exact head/base repositories, branches, writing and outcomes while fencing
// that unknown dispatch history. Stale openers cannot fence new captures.
var HumanPullCreateDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='pull_request.create' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=87)`,
	`UPDATE schema_version SET version=88 WHERE version=87`,
}

// Earlier draft/submitted review captures may already have reached a provider.
// Preserve exact event, writing and outcomes while fencing unknown history.
// The version predicate protects new captures from stale concurrent openers.
var HumanReviewDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='pull_request.review.submit' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=88)`,
	`UPDATE schema_version SET version=89 WHERE version=88`,
}
