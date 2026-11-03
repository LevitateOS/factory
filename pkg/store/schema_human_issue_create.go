package store

// HumanIssueCreateJournalUpgradeDDL permits full plain human issue captures in
// the existing immutable command journal. Other command limits are unchanged.
var HumanIssueCreateJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=73`,
}
