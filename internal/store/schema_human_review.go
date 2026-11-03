package store

// HumanReviewJournalUpgradeDDL permits bounded human draft/final review
// captures in the existing immutable journal. Other command limits stay intact.
var HumanReviewJournalUpgradeDDL = []string{
	`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
	`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply','pull_request.review.submit') AND char_length(payload)<=8192))`,
	`UPDATE schema_version SET version=75`,
}
