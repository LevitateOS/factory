package store

// Human PR captures retain full writing, explicit source and opaque preparation.
var HumanPullCreateJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.merge','pull_request.create') AND octet_length(payload)<=2097152) OR (type IN ('pull_request.review.thread.resolve','pull_request.review.request') AND octet_length(payload)<=131072) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit','pull_request.review.comment.create','pull_request.merge','pull_request.create','pull_request.review.thread.resolve','pull_request.review.request') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=80`,
}
