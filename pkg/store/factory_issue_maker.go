package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// PostUsesIssueMaker reads the captured intake path at its exact revision.
// Missing, unreadable or unsupported metadata cannot select legacy authoring.
func (s *Store) PostUsesIssueMaker(ctx context.Context, id string, revision int64) (bool, error) {
	var version string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(data->>'issue_maker_version','') FROM factory_post_intakes WHERE post=$1 AND revision=$2`, id, revision).Scan(&version); err != nil {
		return false, err
	}
	switch version {
	case "":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, errors.New("stored post intake path is unsupported")
	}
}

func (s *Store) StartPostIssueMaker(ctx context.Context, comment FeedPostDetail) (factory.IssueMakerProgress, error) {
	id, revision := comment.Record.ID, comment.Record.Revision
	post := factory.IssueMakerPostID(id, revision)
	progress := factory.IssueMakerProgress{Scope: factory.IssueMakerScope{SourcePost: id, Revision: revision, Post: post}, State: "queued", Detail: "JEV identified new work. Capturing the original request as an issue before AI refinement.", Checked: time.Now().Unix()}
	var expiry sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT (data->'issue_intake_scope'->>'expires')::bigint FROM factory_post_intakes WHERE post=$1 AND revision=$2`, id, revision).Scan(&expiry); err != nil {
		return progress, err
	}
	progress.Scope.NotAfter = expiry.Int64
	title := "Making issues: " + strings.Join(strings.Fields(comment.Body), " ")
	if len([]rune(title)) > 160 {
		title = string([]rune(title)[:160])
	}
	communication := factory.FeedCommunication{Kind: "system", Title: title, Summary: progress.Detail, ParentID: id}
	data, err := json.Marshal(map[string]any{"actor": "issue_maker", "author_name": "Issue Maker", "at": time.Now().Unix(), "status": "issue_maker:queued", "parent_id": id, "communication": communication, "issue_maker": progress, "issue_slots": map[string]int{"request": 0}, "intake_intent": factory.PostDecisionID("intent", id, revision)})
	if err != nil {
		return progress, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data)
	 SELECT $1,c.repository,0,$2,c.body,$1,$3::jsonb FROM factory_feed_posts c JOIN factory_post_intakes r ON r.post=c.id
	 WHERE c.id=$4 AND c.revision=$5 AND r.revision=$5 AND r.judgment='applied' AND r.route='new_issue' AND r.data->>'issue_maker_version'='1'
	 AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)
	 ON CONFLICT DO NOTHING`, post, "worker:issue-maker:"+id, string(data), id, revision)
	if err != nil {
		return progress, err
	}
	return s.PostIssueMaker(ctx, id, revision)
}

func (s *Store) PostIssueMaker(ctx context.Context, id string, revision int64) (factory.IssueMakerProgress, error) {
	var progress factory.IssueMakerProgress
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data->'issue_maker' FROM factory_feed_posts WHERE id=$1`, factory.IssueMakerPostID(id, revision)).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &progress)
	}
	return progress, err
}

func (s *Store) SaveIssueMakerProgress(ctx context.Context, progress factory.IssueMakerProgress) error {
	if err := progress.Validate(); err != nil {
		return err
	}
	progress.Detail = TruncateFeedSummary(progress.Detail)
	progress.Checked = time.Now().Unix()
	data, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data->'issue_maker' FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, progress.Scope.Post).Scan(&currentRaw); err != nil {
		return err
	}
	var current factory.IssueMakerProgress
	if err = json.Unmarshal(currentRaw, &current); err != nil {
		return err
	}
	if current.Scope.Attempt != progress.Scope.Attempt || current.Scope.NotAfter != progress.Scope.NotAfter {
		return ErrStaleRevision
	}
	progress.Issues = current.Issues
	progress.Scope.Issue = current.Scope.Issue
	data, err = json.Marshal(progress)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts p SET data=p.data || jsonb_build_object('issue_maker',$2::jsonb,'status',$3::text,'communication',p.data->'communication' || jsonb_build_object('summary',$4::text))
	 FROM factory_feed_posts c WHERE p.id=$1 AND c.id=$5 AND c.revision=$6
	 AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)`, progress.Scope.Post, string(data), "issue_maker:"+progress.State, progress.Detail, progress.Scope.SourcePost, progress.Scope.Revision)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrStaleRevision
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_post_intakes SET data=data || jsonb_build_object('issue_maker_state',$3::text,'issue_maker_post',$4::text,'last_checked_at',clock_timestamp()::text) WHERE post=$1 AND revision=$2`, progress.Scope.SourcePost, progress.Scope.Revision, progress.State, progress.Scope.Post)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// RecordIssueMakerIssue publishes exactly one reply per native issue key. A
// refinement updates that reply instead of presenting another created issue.
func (s *Store) RecordIssueMakerIssue(ctx context.Context, scope factory.IssueMakerScope, key string, slot int, index int64, title, body string) error {
	if index <= 0 || !factory.ValidPlanKey(key) {
		return errors.New("invalid issue maker issue receipt")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var repository int64
	if err = tx.QueryRowContext(ctx, `SELECT repository,data->'issue_maker' FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, scope.Post).Scan(&repository, &raw); err != nil {
		return err
	}
	var progress factory.IssueMakerProgress
	if err = json.Unmarshal(raw, &progress); err != nil {
		return err
	}
	found := false
	for i, item := range progress.Issues {
		if item.Key == key {
			if item.Index != index {
				return ErrStaleRevision
			}
			progress.Issues[i].Title = title
			progress.Issues[i].BodyDigest = factory.MCPInputDigest([]byte(body))
			found = true
		}
	}
	if !found {
		if len(progress.Issues) >= 16 {
			return errors.New("issue maker issue limit reached")
		}
		progress.Issues = append(progress.Issues, factory.IssueMakerIssue{Key: key, Slot: slot, Index: index, Title: title, BodyDigest: factory.MCPInputDigest([]byte(body))})
	}
	if key == "request" {
		progress.Scope.Issue = index
	}
	raw, err = json.Marshal(progress)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{issue_maker}',$2::jsonb) WHERE id=$1`, scope.Post, string(raw)); err != nil {
		return err
	}
	post := factory.IssueMakerReplyID(scope.Post, key)
	communication := factory.FeedCommunication{Kind: "system", Title: TruncateFeedSummary(title), Summary: fmt.Sprintf("Created issue #%d. Open the issue to follow its work.", index), ParentID: scope.Post}
	if len([]rune(communication.Title)) > 160 {
		communication.Title = string([]rune(communication.Title)[:160])
	}
	data, err := json.Marshal(map[string]any{"actor": "issue_maker", "author_name": "Issue Maker", "at": time.Now().Unix(), "status": "issue_maker:created", "issue": index, "parent_id": scope.Post, "source": factory.PostSource{Kind: factory.PostSourcePost, ID: scope.Post, Issue: index}, "communication": communication})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data) VALUES($1,$2,0,$3,$4,$1,$5::jsonb)
	 ON CONFLICT(id) DO UPDATE SET body=EXCLUDED.body,data=factory_feed_posts.data || jsonb_build_object('communication',EXCLUDED.data->'communication')`, post, repository, "worker:issue-maker:"+scope.SourcePost, "Issue #"+strconv.FormatInt(index, 10)+": "+title, string(data))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SaveIssueIntakeGrant binds the trusted adapter's scope to the current source
// post and stores its opaque provider proof. The adapter must derive scope from
// that same admitted proof; the store does not authenticate or interpret it.
// Keep this server-side bearer out of semantic requests, prompts and client DTOs.
func (s *Store) SaveIssueIntakeGrant(ctx context.Context, id string, revision int64, scope factory.IssueIntakeScope, proof json.RawMessage) error {
	if scope.Validate() != nil || scope.Source != id || scope.Revision != revision || len(proof) == 0 || len(proof) > 8192 || !json.Valid(proof) {
		return errors.New("invalid issue intake grant")
	}
	scopeRaw, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Use the same source/role/intake order as admission and continuation.
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_feed_posts WHERE id=$1 FOR SHARE`, id).Scan(&locked); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, factory.IssueMakerPostID(id, revision)).Scan(&locked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_post_intakes r SET data=r.data || jsonb_build_object('issue_intake_grant',$3::jsonb,'issue_intake_scope',$7::jsonb)
	 FROM factory_feed_posts c WHERE r.post=$1 AND r.revision=$2 AND c.id=r.post AND c.revision=$2 AND c.author=$4 AND c.repository=$5::bigint
	 AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)
	 AND encode(sha256(convert_to(c.body,'UTF8')),'hex')=$6`, id, revision, string(proof), strconv.FormatInt(int64(scope.Actor), 10), int64(scope.Repository), scope.Digest, string(scopeRaw))
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrStaleRevision
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{issue_maker,scope,not_after}',to_jsonb($2::bigint)) WHERE id=$1 AND data ? 'issue_maker'`, factory.IssueMakerPostID(id, revision), scope.Expires); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IssueIntakeGrant(ctx context.Context, id string, revision int64) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.db.QueryRowContext(ctx, `SELECT data->'issue_intake_grant' FROM factory_post_intakes WHERE post=$1 AND revision=$2 AND data ? 'issue_intake_grant'`, id, revision).Scan(&raw)
	return raw, err
}

// PrepareIssueMakerMutation allocates bounded native slots/steps under the
// role-post lock and retains exact request bytes before any native effect.
func (s *Store) PrepareIssueMakerMutation(ctx context.Context, post, operation, hash, key string, request factory.IssueMakerMutation) (factory.IssueMakerMutation, error) {
	if request.Validate() != nil || operation == "" || len(operation) > 128 || !factory.ValidDigest(hash) || !factory.ValidPlanKey(key) {
		return request, errors.New("invalid issue maker mutation identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return request, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, post).Scan(&raw); err != nil {
		return request, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return request, err
	}
	type retained struct {
		Hash     string                     `json:"hash"`
		Mutation factory.IssueMakerMutation `json:"mutation"`
	}
	operations := map[string]retained{}
	slots := map[string]int{"request": 0}
	if fields["issue_operations"] != nil {
		if err = json.Unmarshal(fields["issue_operations"], &operations); err != nil {
			return request, err
		}
	}
	if fields["issue_slots"] != nil {
		if err = json.Unmarshal(fields["issue_slots"], &slots); err != nil {
			return request, err
		}
	}
	if existing, ok := operations[operation]; ok {
		if existing.Hash != hash {
			return request, ErrStaleRevision
		}
		return existing.Mutation, tx.Commit()
	}
	if len(operations) >= 32 {
		return request, errors.New("issue maker operation limit reached")
	}
	slot, exists := slots[key]
	if !exists {
		if request.Action != "create" || len(slots) >= 16 {
			return request, errors.New("issue key is unavailable")
		}
		slot = len(slots)
		slots[key] = slot
	}
	request.Slot, request.Step = slot, len(operations)
	operations[operation] = retained{Hash: hash, Mutation: request}
	opRaw, _ := json.Marshal(operations)
	slotRaw, _ := json.Marshal(slots)
	_, err = tx.ExecContext(ctx, `UPDATE factory_feed_posts SET data=data || jsonb_build_object('issue_operations',$2::jsonb,'issue_slots',$3::jsonb) WHERE id=$1`, post, string(opRaw), string(slotRaw))
	if err != nil {
		return request, err
	}
	return request, tx.Commit()
}
