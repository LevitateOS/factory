package store

// HumanLockJournalUpgradeDDL permits full human discussion lock captures in
// the existing immutable command journal. Other command limits are unchanged.
var HumanLockJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=74`,
}
