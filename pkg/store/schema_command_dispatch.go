package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Historical expectations come from their frozen upgrade DDL, never the fresh
// table text. The current known bound may already exist at an earlier marker;
// accepting that shape never changes a command's dispatch history.
func commandPayloadExpression(version int) (string, error) {
	expression := "char_length(payload)<=8192" // Schemas 68–70.
	var history []string
	switch {
	case version >= 68 && version <= 70:
	case version == 71:
		history = HumanCommentJournalUpgradeDDL
	case version == 72:
		history = HumanEditJournalUpgradeDDL
	case version == 73:
		history = HumanIssueCreateJournalUpgradeDDL
	case version == 74:
		history = HumanLockJournalUpgradeDDL
	case version == 75:
		history = HumanReviewJournalUpgradeDDL
	case version == 76:
		history = HumanInlineJournalUpgradeDDL
	case version == 77:
		history = HumanThreadJournalUpgradeDDL
	case version == 78:
		history = HumanReviewRequestJournalUpgradeDDL
	case version == 79:
		history = HumanMergeJournalUpgradeDDL
	case version >= 80 && version <= 89:
		history = HumanPullCreateJournalUpgradeDDL
	case version >= 90 && version <= 103:
		history = HumanBranchCreateDispatchUpgradeDDL
	case version == 104 || version == 105:
		history = reviewRetryCommandUpgradeDDL
	default:
		return "", fmt.Errorf("command payload constraint has no schema %d contract", version)
	}
	if history != nil {
		const prefix = "ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK("
		expression = ""
		for _, statement := range history {
			if strings.HasPrefix(statement, prefix) && strings.HasSuffix(statement, ")") {
				expression = strings.TrimSuffix(strings.TrimPrefix(statement, prefix), ")")
				break
			}
		}
		if expression == "" {
			return "", fmt.Errorf("command payload schema %d contract is unavailable", version)
		}
	}
	return expression, nil
}

// Refuse unknown bounds before any migration can replace them. PostgreSQL's
// parser normalizes only the two known expressions inside this transaction.
func verifyCommandPayloadCheck(ctx context.Context, tx *sql.Tx, version int) error {
	previous, err := commandPayloadExpression(version)
	if err != nil {
		return err
	}
	current, err := commandPayloadExpression(SchemaVersion())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE soda_command_payload_shape(type TEXT,payload TEXT,CHECK(`+previous+`),CHECK(`+current+`)) ON COMMIT DROP`); err != nil {
		return err
	}
	var known bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (
	 SELECT 1 FROM pg_constraint expected
	 WHERE expected.conrelid='pg_temp.soda_command_payload_shape'::regclass AND expected.contype='c'
	 AND pg_get_constraintdef(expected.oid)=pg_get_constraintdef(actual.oid))
	 FROM pg_constraint actual WHERE actual.conrelid='factory_commands'::regclass
	 AND actual.conname='factory_commands_payload_check' AND actual.contype='c' AND actual.convalidated`).Scan(&known)
	if err != nil {
		return fmt.Errorf("command payload constraint unavailable: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE pg_temp.soda_command_payload_shape`); err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("command payload refuses an unknown retained schema %d constraint", version)
	}
	return nil
}

// Keep the same immutable identity and terminal-outcome guard on fresh and
// upgraded databases. Only supported unfinished provider effects can acquire a
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
          AND OLD.type IN ('discussion.comment.create','issue.create','discussion.state.apply','discussion.edit','discussion.lock.apply','pull_request.review.thread.resolve','pull_request.review.request','pull_request.create','pull_request.review.submit','git.branch.create','git.ref.publish','pull_request.review.comment.create','pull_request.merge','issue.graph.mutate')))))
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

// Preserve any earlier branch-shaped command history without inferring local
// dispatch ownership. A stale opener cannot fence new schema-90 captures.
var HumanBranchCreateDispatchUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.merge','pull_request.create') AND octet_length(payload)<=2097152) OR (type IN ('pull_request.review.thread.resolve','pull_request.review.request') AND octet_length(payload)<=131072) OR (type='git.branch.create' AND octet_length(payload)<=32768) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.merge','pull_request.create','pull_request.review.thread.resolve','pull_request.review.request','git.branch.create') AND char_length(payload)<=8192))`,
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='git.branch.create' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=89)`,
	`UPDATE schema_version SET version=90 WHERE version=89`,
}

// Earlier inline captures may already have reached an adapter. Preserve exact
// writing/selection and outcomes while fencing unknown local dispatch history.
var HumanInlineDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='pull_request.review.comment.create' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=90)`,
	`UPDATE schema_version SET version=91 WHERE version=90`,
}

// Earlier merge captures have no reliable local one-shot dispatch history.
var HumanMergeDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='pull_request.merge' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=91)`,
	`UPDATE schema_version SET version=92 WHERE version=91`,
}

// A single dependency effect now shares the one-shot command reservation.
// Older graph-shaped rows cannot acquire dispatch ownership retroactively.
var IssueDependencyDispatchUpgradeDDL = []string{
	commandGuardDDL,
	`UPDATE factory_commands SET dispatch_state='legacy_unknown' WHERE type='issue.graph.mutate' AND dispatch_state='unclaimed' AND EXISTS(SELECT 1 FROM schema_version WHERE version=95)`,
	`UPDATE schema_version SET version=96 WHERE version=95`,
}

// A fresh review submission retry captures the completed review verbatim, but
// dispatch still belongs to its separate native review command. This widens no
// other command and does not release or reclassify any historical reservation.
const reviewRetryCommandPayloadCheck = `(type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.merge','pull_request.create','retry-unsent-review') AND octet_length(payload)<=2097152) OR (type IN ('pull_request.review.thread.resolve','pull_request.review.request') AND octet_length(payload)<=131072) OR (type='git.branch.create' AND octet_length(payload)<=32768) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.merge','pull_request.create','retry-unsent-review','pull_request.review.thread.resolve','pull_request.review.request','git.branch.create') AND char_length(payload)<=8192)`

var reviewRetryCommandUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK(` + reviewRetryCommandPayloadCheck + `)`,
	`UPDATE schema_version SET version=104 WHERE version=103`,
}
