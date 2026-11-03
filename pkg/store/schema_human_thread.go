package store

// Bounded human thread captures use 128 KiB; previous command limits are retained.
var HumanThreadJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create') AND octet_length(payload)<=2097152) OR (type='pull_request.review.thread.resolve' AND octet_length(payload)<=131072) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.review.thread.resolve') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=77`,
}
