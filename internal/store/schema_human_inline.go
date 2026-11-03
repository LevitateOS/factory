package store

// Only bounded human inline captures gain the existing large-command allowance.
// Older immutable captures and every other command's limit remain unchanged.
var HumanInlineJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=76`,
}
