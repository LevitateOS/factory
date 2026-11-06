package store

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

// BeginPostIssueCreation retains the source revision before a remote create.
// Unlike the current intake draft, this journal survives edits and revocation.
// A missing result means unknown, never proof that the operation had no effect.
func (s *Store) BeginPostIssueCreation(ctx context.Context, post string, revision int64) error {
	if !factory.ValidID(post) || revision < 0 {
		return errors.New("invalid automatic issue source")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{issue_creations}',
	 COALESCE(data->'issue_creations','{}'::jsonb) || jsonb_build_object($3::text,COALESCE(data->'issue_creations'->$3::text,'0'::jsonb)))
	 WHERE id=$1 AND revision=$2 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false)`, post, revision, strconv.FormatInt(revision, 10))
	return checkGrantRowsAffected(result, err, "automatic issue source changed")
}

// RecordPostIssueCreation settles that same source revision even if its author
// edited or revoked it during provider I/O. -1 records a provider-proved refusal;
// zero remains unknown. A conflicting issue cannot replace a recorded result.
func (s *Store) RecordPostIssueCreation(ctx context.Context, post string, revision, issue int64) error {
	if (issue <= 0 && issue != -1) || revision < 0 {
		return errors.New("invalid automatic issue result")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,ARRAY['issue_creations',$2::text],to_jsonb($3::bigint))
	 WHERE id=$1 AND data->'issue_creations'->$2::text IN ('0'::jsonb,to_jsonb($3::bigint))`, post, strconv.FormatInt(revision, 10), issue)
	return checkGrantRowsAffected(result, err, "automatic issue result conflicts with its source")
}

// AutomaticIssueOrigin uses causal journals, never the creator's account. The
// two answers share one database snapshot: an unbound operation must not vanish
// between its pending and recorded-result reads. Human issue commands are absent
// from these journals. Unresolved creates hold new intake until reconciliation;
// without a returned issue identity they cannot safely be called unrelated.
func (s *Store) AutomaticIssueOrigin(ctx context.Context, repository, issue int64) (generated, pending bool, err error) {
	err = s.db.QueryRowContext(ctx, `WITH posts AS MATERIALIZED (
	 SELECT id,data FROM factory_feed_posts WHERE repository=$1
	), roots AS (
	 SELECT value::bigint AS issue FROM posts CROSS JOIN LATERAL jsonb_each_text(COALESCE(data->'issue_creations','{}'::jsonb))
	), maker_issues AS (
	 SELECT id,item FROM posts CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(data->'issue_maker'->'issues','null'::jsonb),'[]'::jsonb)) item
	), generations AS MATERIALIZED (
	 SELECT g.revision,g.data FROM factory_intent_generations g JOIN factory_intent_revisions r ON r.id=g.revision
	 WHERE r.data->>'repository'=$1::text
	), created_tasks AS (
	 SELECT g.revision,g.data->>'state' AS state,task->>'key' AS key FROM generations g
	 CROSS JOIN LATERAL jsonb_array_elements(g.data->'submitted_tasks') task
	 WHERE COALESCE((g.data->'task_issue_ids'->>(task->>'key'))::bigint,0)=0
	 AND NOT COALESCE(g.data->'delivered','{}'::jsonb) ? (task->>'key')
	), bound_tasks AS (
	 SELECT t.data FROM factory_intent_tasks t JOIN created_tasks c ON c.revision=t.revision AND c.key=t.task
	)
	SELECT EXISTS(SELECT 1 FROM roots WHERE issue=$2)
	 OR EXISTS(SELECT 1 FROM factory_post_intakes i JOIN posts p ON p.id=i.post WHERE i.issue=$2 AND i.route='new_issue')
	 OR EXISTS(SELECT 1 FROM maker_issues WHERE item->>'index'=$2::text)
	 OR EXISTS(SELECT 1 FROM bound_tasks WHERE data->>'issue_index'=$2::text),
	 EXISTS(SELECT 1 FROM roots WHERE issue=0)
	 OR EXISTS(SELECT 1 FROM factory_post_intakes i JOIN posts p ON p.id=i.post
	 WHERE i.route='new_issue' AND i.draft='applied' AND i.issue=0
	 AND NOT COALESCE(p.data->'issue_creations','{}'::jsonb) ? i.revision::text)
	 OR EXISTS(SELECT 1 FROM posts p CROSS JOIN LATERAL jsonb_each(COALESCE(p.data->'issue_operations','{}'::jsonb)) op
	 WHERE op.value->'mutation'->>'action'='create' AND NOT EXISTS(
	 SELECT 1 FROM maker_issues m WHERE m.id=p.id AND m.item->>'slot'=op.value->'mutation'->>'slot'))
	 OR EXISTS(SELECT 1 FROM created_tasks c WHERE state IN ('pending','committed') AND NOT EXISTS(
	 SELECT 1 FROM factory_intent_tasks t WHERE t.revision=c.revision AND t.task=c.key))`, repository, issue).Scan(&generated, &pending)
	return
}
