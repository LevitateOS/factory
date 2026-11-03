package store

// HumanEditJournalUpgradeDDL retains full human edit captures in the existing
// immutable command journal. Other commands keep their existing payload bounds.
var HumanEditJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=72`,
}
