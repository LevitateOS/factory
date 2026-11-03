package store

// HumanCommentJournalUpgradeDDL retains prepared comment JSON in the existing
// command journal, including base64 provider bytes and escaped 64 KiB Markdown.
// Other command limits and existing immutability guards remain unchanged.
var HumanCommentJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type='discussion.comment.create' AND octet_length(payload)<=2097152) OR (type<>'discussion.comment.create' AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=71`,
}
