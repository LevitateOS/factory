package store

// FeedPostsUpgradeDDL replaces the old communication store in place. Stable
// identities, revisions, budgets and native-effect receipts survive the rename.
// Only current feed data is converted; captured run artifacts remain unchanged.
var FeedPostsUpgradeDDL = []string{
	`ALTER TABLE factory_feed_comments RENAME TO factory_feed_posts`,
	`ALTER INDEX factory_feed_comments_repository RENAME TO factory_feed_posts_repository`,
	`ALTER TABLE factory_feed_comment_requests RENAME TO factory_post_intakes`,
	`ALTER TABLE factory_post_intakes RENAME COLUMN comment TO post`,
	`CREATE FUNCTION pg_temp.soda_post_data(value JSONB) RETURNS JSONB LANGUAGE plpgsql AS $$
DECLARE result JSONB;
BEGIN
 IF jsonb_typeof(value)='object' THEN
  SELECT COALESCE(jsonb_object_agg(CASE key WHEN 'comment' THEN 'source_post' WHEN 'comment_id' THEN 'post_id' WHEN 'reply_to' THEN 'parent_id' ELSE key END, pg_temp.soda_post_data(item)), '{}'::jsonb)
   INTO result FROM jsonb_each(value) fields(key,item);
  RETURN result;
 ELSIF jsonb_typeof(value)='array' THEN
  SELECT COALESCE(jsonb_agg(pg_temp.soda_post_data(item) ORDER BY position), '[]'::jsonb)
   INTO result FROM jsonb_array_elements(value) WITH ORDINALITY fields(item,position);
  RETURN result;
 END IF;
 RETURN value;
END $$`,
	`UPDATE factory_feed_posts SET data=pg_temp.soda_post_data(data)`,
	`UPDATE factory_feed_posts SET data=jsonb_set(data,'{parent_id}',to_jsonb(COALESCE(NULLIF(data->>'parent_id',''), data->'communication'->>'parent_id', data->'source'->>'id','')))`,
	`UPDATE factory_post_intakes SET data=pg_temp.soda_post_data(data)`,
	`DROP FUNCTION pg_temp.soda_post_data(JSONB)`,
	`CREATE INDEX factory_feed_posts_parent ON factory_feed_posts(repository,(data->>'parent_id'),seq)`,
	`UPDATE schema_version SET version=70`,
}
