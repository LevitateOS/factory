package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

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

// BeginPostFeedback atomically allocates the only possible POST. A lost caller
// receives the retained record, never another send permit. Older rows begin in
// lookup-only mode because their old marker lookup may already have sent.
func (s *Store) BeginPostFeedback(ctx context.Context, intent factory.PostFeedbackIntent) (factory.PostFeedbackDelivery, bool, error) {
	var empty factory.PostFeedbackDelivery
	if err := intent.Validate(); err != nil {
		return empty, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	var raw []byte
	var version string
	err = tx.QueryRowContext(ctx, `SELECT p.data,COALESCE(i.data->>'feedback_version','') FROM factory_feed_posts p JOIN factory_post_intakes i ON i.post=p.id
 WHERE p.id=$1 AND p.repository=$2 AND p.revision=$3 AND i.revision=$3 AND i.issue=$4 AND i.route='existing_work' AND i.judgment='applied'
 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false) FOR UPDATE OF p`, intent.SourcePost, intent.Repository, intent.Revision, intent.Issue).Scan(&raw, &version)
	if err != nil {
		return empty, false, err
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal(raw, &data); err != nil {
		return empty, false, err
	}
	entries := map[string]factory.PostFeedbackDelivery{}
	if saved := data["feedback_deliveries"]; len(saved) > 0 {
		if err = json.Unmarshal(saved, &entries); err != nil {
			return empty, false, err
		}
	}
	key := strconv.FormatInt(intent.Revision, 10)
	if saved, ok := entries[key]; ok {
		if saved.Validate() != nil || saved.Intent != intent {
			return empty, false, ErrStaleRevision
		}
		return saved, false, tx.Commit()
	}
	delivery := factory.PostFeedbackDelivery{Intent: intent, Legacy: version != "1", Cursor: 1}
	if !delivery.Legacy {
		delivery.NotBefore = time.Now().Add(5 * time.Minute).Unix()
	}
	entries[key] = delivery
	encoded, err := json.Marshal(entries)
	if err != nil {
		return empty, false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{feedback_deliveries}',$2::jsonb) WHERE id=$1`, intent.SourcePost, string(encoded))
	if err != nil {
		return empty, false, err
	}
	if err = tx.Commit(); err != nil {
		return empty, false, err
	}
	return delivery, !delivery.Legacy, nil
}

// SavePostFeedback can settle the original record after a source edit/revocation.
// CAS preserves competing recovery, all other revisions and human text.
func (s *Store) SavePostFeedback(ctx context.Context, expected, next factory.PostFeedbackDelivery) error {
	if expected.Validate() != nil || next.Validate() != nil || expected.Intent != next.Intent || expected.Legacy != next.Legacy || next.Cursor < expected.Cursor || expected.Complete && next != expected && !(expected.Receipt == "" && expected.Attention == "not_found" && next.Receipt != "") && !(expected.Receipt != "" && next.Receipt == expected.Receipt && next.Attention == "content_changed") {
		return ErrStaleRevision
	}
	before, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	after, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,ARRAY['feedback_deliveries',$2::text],$4::jsonb)
 WHERE id=$1 AND data->'feedback_deliveries'->$2::text=$3::jsonb`, expected.Intent.SourcePost, strconv.FormatInt(expected.Intent.Revision, 10), string(before), string(after))
	return checkGrantRowsAffected(result, err, "feedback recovery changed")
}

func (s *Store) PostFeedback(ctx context.Context, post string, revision int64) (factory.PostFeedbackDelivery, error) {
	var delivery factory.PostFeedbackDelivery
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data->'feedback_deliveries'->$2::text FROM factory_feed_posts WHERE id=$1 AND data->'feedback_deliveries' ? $2::text`, post, strconv.FormatInt(revision, 10)).Scan(&raw)
	if err != nil {
		return delivery, err
	}
	if err = json.Unmarshal(raw, &delivery); err != nil {
		return delivery, err
	}
	return delivery, delivery.Validate()
}
