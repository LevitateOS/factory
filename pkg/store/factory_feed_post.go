package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/factory"
)

// Durable feed communication records (R32.2/R32.14): comment identity,
// revision and scope plus the intake/judgment/draft/effect request row.
// SourcePost identity is server-generated; the caller's idempotency key
// replays the same submission to the same comment instead of
// duplicating it. Reads do not run JEV; native effects apply through the
// issue creator.

// FeedPostDetail carries one stored comment with its serving
// fields. Source is the persisted resolved snapshot of the feed
// entry the comment was submitted against, when it named one.
// Consultation carries actual tool inputs/results, distinct from agent prose.
type FeedPostDetail struct {
	Consultation  *factory.FeedConsultation
	Contribution  *factory.ContributionReview
	Actor         string
	Assignment    string
	EventIdentity string
	Payload       *factory.FeedPayload
	Record        factory.PostRecord
	Body          string
	AuthorName    string
	At            int64
	Status        string
	Source        *factory.PostSource
}

var ErrInvalidPostDraft = errors.New("invalid issue draft")

// SavePostIssueContext retains bounded discussion context read through the
// author's live native admission. It contains no session or service credential.
func (s *Store) SavePostIssueContext(ctx context.Context, id string, revision int64, candidates []factory.PostWorkCandidate) error {
	data, err := json.Marshal(candidates)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts
	 SET data=data || jsonb_build_object('issue_context_revision',$2::bigint,'issue_context',$3::jsonb)
	 WHERE id=$1 AND revision=$2 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false)`, id, revision, string(data))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

func (s *Store) PostIssueContext(ctx context.Context, id string, revision int64) ([]factory.PostWorkCandidate, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data->'issue_context' FROM factory_feed_posts
	 WHERE id=$1 AND revision=$2 AND (data->>'issue_context_revision')::bigint=$2`, id, revision).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var candidates []factory.PostWorkCandidate
	if err := json.Unmarshal(raw, &candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

// FeedReplyIDs lists the actual conversation, including replies from other
// browsers. The caller authorizes the repository and source before this read.
// Posts retain direct-reply paging; work records include their discussion threads.
func (s *Store) FeedReplyIDs(ctx context.Context, repository int64, kind, source string, before int64) ([]string, int64, error) {
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE discussion AS (
 SELECT id,seq FROM factory_feed_posts
 WHERE repository=$1 AND data->>'parent_id'=$3
 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false)
 UNION ALL SELECT c.id,c.seq FROM factory_feed_posts c JOIN discussion p ON c.data->>'parent_id'=p.id
 WHERE $2::text='record' AND c.repository=$1 AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)
 ) SELECT id,seq FROM (
 SELECT id,seq FROM discussion
 UNION ALL SELECT id,seq FROM factory_assignments WHERE repository=$1
 AND COALESCE(NULLIF(data->>'parent_assignment',''),data->'planning_context'->'IssueMaker'->>'source_post',data->'planning_context'->'IssueMaker'->>'comment')=$3
 AND COALESCE(data->'planning_context'->'IssueMaker','null'::jsonb)='null'::jsonb
 UNION ALL SELECT c.event,c.feed_seq FROM factory_completions c JOIN factory_publications p ON p.data->>'id'=c.publication JOIN factory_assignments a ON a.id=p.assignment WHERE a.repository=$1 AND a.id=$3
 UNION ALL SELECT m.publication,m.feed_seq FROM factory_merges m JOIN factory_publications p ON p.data->>'id'=m.publication JOIN factory_assignments a ON a.id=p.assignment WHERE a.repository=$1 AND a.id=$3 AND m.stage='merged'
 UNION ALL SELECT j.snapshot,j.feed_seq FROM factory_priority_judgments j JOIN factory_publications p ON p.data->>'id'=j.data->'input'->>'publication_id' JOIN factory_assignments a ON a.id=p.assignment WHERE a.repository=$1 AND a.id=$3 AND j.data->>'kind'='delivery-v1'
 ) replies WHERE ($4::bigint=0 OR seq<$4) AND $2::text IN ('post','record')
 ORDER BY seq DESC LIMIT 21`, repository, kind, source, before)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	ids := []string{}
	var next, last int64
	for rows.Next() {
		var id string
		var seq int64
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, 0, err
		}
		if len(ids) == 20 {
			next = last
			break
		}
		ids = append(ids, id)
		last = seq
	}
	return ids, next, rows.Err()
}

// SaveFeedPost persists one comment submission and returns its
// durable identity. A repeated idempotency key from the same author and
// repository replays the original identity instead of inserting a
// duplicate; the body of the replayed call is ignored.
func (s *Store) SaveFeedPost(ctx context.Context, req factory.PostRequest, author, authorName string, at int64) (string, int64, error) {
	return s.SaveFeedPostWithSource(ctx, req, author, authorName, at, nil)
}

// SaveFeedPostWithSource persists one comment submission with the
// server-resolved source snapshot its submission named (nil for a
// top-level comment). The snapshot is stored verbatim beside the
// comment so later judgment and authoring consume the authorized
// evidence as submitted; a replayed idempotency key recovers the
// original comment and its original snapshot.
func (s *Store) SaveFeedPostWithSource(ctx context.Context, req factory.PostRequest, author, authorName string, at int64, source *factory.PostSource) (string, int64, error) {
	if source != nil && req.ParentID == "" {
		req.ParentID = source.ID
	}
	if req.ParentID != "" && source == nil {
		var err error
		source, err = s.ResolvePostParent(ctx, req.Repository, req.ParentID)
		if err != nil {
			return "", 0, err
		}
		req.SourceKind, req.SourceID = source.Kind, source.ID
	}
	if err := req.Validate(); err != nil {
		return "", 0, err
	}
	if source != nil {
		if err := source.Validate(); err != nil {
			return "", 0, err
		}
		if req.ParentID != source.ID || req.SourceKind != source.Kind || req.SourceID != source.ID {
			return "", 0, errors.New("comment source snapshot does not match its submission")
		}
	}
	if author == "" || at <= 0 {
		return "", 0, errors.New("comment needs its author and time")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", 0, err
	}
	fields := map[string]any{
		"issue": req.Issue, "parent_id": req.ParentID, "edited": false,
		"deleted": false, "revoked": false, "at": at, "author_name": authorName,
	}
	if source != nil {
		fields["source"] = source
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return "", 0, err
	}
	postID := hex.EncodeToString(id[:])
	result, err := s.db.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data)
		VALUES($1,$2,0,$3,$4,$5,$6) ON CONFLICT(repository,author,idempotency) DO NOTHING`,
		postID, req.Repository, author, req.Body, req.IdempotencyKey, string(data))
	if err != nil {
		return "", 0, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return "", 0, err
	} else if n == 1 {
		return postID, 0, nil
	}
	return s.FeedPostSubmission(ctx, req.Repository, author, req.IdempotencyKey)
}

// FeedPostSubmission recovers the existing command's current identity and
// revision from the same author/repository/idempotency index used by submission.
// It does not resolve a live parent or rewrite the captured body/source.
func (s *Store) FeedPostSubmission(ctx context.Context, repository int64, author, key string) (string, int64, error) {
	var id string
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT id,revision FROM factory_feed_posts WHERE repository=$1 AND author=$2 AND idempotency=$3`, repository, author, key).Scan(&id, &revision)
	return id, revision, err
}

// EditFeedPost records one new comment revision under CAS: the
// expected revision must match and the author must own the comment.
// Deleted or revoked sources refuse the edit.
func (s *Store) EditFeedPost(ctx context.Context, id, author, body string, expected int64, at int64) (int64, error) {
	if len(body) == 0 || utf8.RuneCountInString(body) > factory.PostBodyLimit || at <= 0 {
		return 0, errors.New("comment edit needs its bounded body and time")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var savedRevision int64
	var savedAuthor string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision,author,data FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, id).Scan(&savedRevision, &savedAuthor, &raw); err != nil {
		return 0, err
	}
	if savedAuthor != author || savedRevision != expected {
		return 0, ErrStaleRevision
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, err
	}
	if data["deleted"] == true || data["revoked"] == true {
		return 0, errors.New("comment source is closed to edits")
	}
	if data["product_change"] != nil {
		return 0, errors.New("the reviewed product change keeps its original comment; add a new reply")
	}
	data["edited"] = true
	data["at"] = at
	encoded, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts SET revision=revision+1,body=$2,data=$3 WHERE id=$1`,
		id, body, string(encoded)); err != nil {
		return 0, err
	}
	// A new source revision reopens an unsettled draft and effect
	// against the edited text; the recorded judgment and route survive.
	// A settled native outcome (applied or verified) keeps its draft,
	// effect and retained bytes as audit: the linked issue already
	// exists and the driver reconciles rather than duplicating it.
	if _, err := tx.ExecContext(ctx, `UPDATE factory_post_intakes
		SET revision=$2,
		draft=CASE WHEN effect IN ('applied','verified') THEN draft ELSE 'pending' END,
		effect=CASE WHEN effect IN ('applied','verified') THEN effect ELSE 'pending' END,
		data=CASE WHEN effect IN ('applied','verified') THEN data ELSE '{}' END
		WHERE post=$1`, id, expected+1); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return expected + 1, nil
}

// DeleteFeedPost tombstones one comment: the row and its bound
// pipeline still resolve, but the feed projection hides the body.
func (s *Store) DeleteFeedPost(ctx context.Context, id, author string, expected int64) (int64, error) {
	return s.markFeedPost(ctx, id, author, expected, "deleted")
}

// MarkFeedPostRevoked records access loss on one comment: bound
// effects block instead of applying against a source the caller can no
// longer see.
func (s *Store) MarkFeedPostRevoked(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	data["revoked"] = true
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts SET revision=revision+1,data=$2 WHERE id=$1`, id, string(encoded)); err != nil {
		return err
	}
	if err := blockPostEffectTx(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) markFeedPost(ctx context.Context, id, author string, expected int64, flag string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var savedRevision int64
	var savedAuthor string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision,author,data FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, id).Scan(&savedRevision, &savedAuthor, &raw); err != nil {
		return 0, err
	}
	if savedAuthor != author || savedRevision != expected {
		return 0, ErrStaleRevision
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, err
	}
	data[flag] = true
	encoded, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts SET revision=revision+1,data=$2 WHERE id=$1`, id, string(encoded)); err != nil {
		return 0, err
	}
	if err := blockPostEffectTx(ctx, tx, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return expected + 1, nil
}

// blockPostEffectTx settles an unsettled native effect as blocked
// when its source closes: a deleted or revoked comment never applies a
// native issue against text its readers can no longer see.
func blockPostEffectTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE factory_post_intakes SET effect='blocked'
		WHERE post=$1 AND effect IN ('pending','failed')`, id)
	return err
}

// FeedPost reads one durable comment with its serving fields.
func (s *Store) FeedPost(ctx context.Context, id string) (FeedPostDetail, error) {
	var out FeedPostDetail
	var repository, revision, issue int64
	var author, body, parentId, authorName string
	var edited, deleted, revoked bool
	var at int64
	var sourceRaw, dataRaw []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,repository,revision,author,body,
		COALESCE(data->>'parent_id',''),COALESCE((data->>'issue')::bigint,0),
		COALESCE((data->>'edited')::boolean,false),COALESCE((data->>'deleted')::boolean,false),
		COALESCE((data->>'revoked')::boolean,false),COALESCE(data->>'author_name',''),
		COALESCE((data->>'at')::bigint,0),COALESCE(data->'source','null'::jsonb),data FROM factory_feed_posts WHERE id=$1`, id).
		Scan(&out.Record.ID, &repository, &revision, &author, &body, &parentId, &issue, &edited, &deleted, &revoked, &authorName, &at, &sourceRaw, &dataRaw)
	if err != nil {
		return FeedPostDetail{}, err
	}
	out.Record = factory.PostRecord{ID: out.Record.ID, Revision: revision, Repository: repository, Author: author, Issue: issue, ParentID: parentId, Edited: edited, Deleted: deleted, Revoked: revoked}
	out.Body, out.AuthorName, out.At = body, authorName, at
	if err := applyFeedCommunicationDetail(&out, dataRaw); err != nil {
		return FeedPostDetail{}, err
	}
	if len(sourceRaw) != 0 && string(sourceRaw) != "null" {
		var source factory.PostSource
		if err := json.Unmarshal(sourceRaw, &source); err != nil {
			return FeedPostDetail{}, err
		}
		out.Source = &source
	}
	if out.Payload != nil && out.Payload.Communication != nil && out.Payload.Communication.Kind == "ask" {
		details := map[string]FeedPostDetail{out.Record.ID: out}
		if err := s.feedQuestionStates(ctx, details); err != nil {
			return FeedPostDetail{}, err
		}
		out = details[out.Record.ID]
	}
	if out.Payload != nil && out.Payload.Communication != nil {
		details := map[string]FeedPostDetail{out.Record.ID: out}
		if err := s.feedPostResolutions(ctx, details); err != nil {
			return FeedPostDetail{}, err
		}
		out = details[out.Record.ID]
	}
	return out, nil
}

// RecordPostIntake opens the durable request behind one comment
// revision: intake recorded, judgment/draft/effect pending. Replaying
// the same comment recovers the row instead of duplicating it.
func (s *Store) RecordPostIntake(ctx context.Context, postID string, revision int64) error {
	return s.recordPostIntake(ctx, postID, revision, false)
}

func (s *Store) RecordIssueMakerIntake(ctx context.Context, postID string, revision int64) error {
	return s.recordPostIntake(ctx, postID, revision, true)
}

func (s *Store) recordPostIntake(ctx context.Context, postID string, revision int64, issueMaker bool) error {
	data := "{}"
	if issueMaker {
		data = `{"issue_maker_version":1}`
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO factory_post_intakes(post,revision,intake,judgment,route,draft,effect,issue,attempts,data)
		VALUES($1,$2,'recorded','pending','', 'pending','pending',0,0,$3::jsonb) ON CONFLICT(post) DO NOTHING`, postID, revision, data)
	return err
}

// PostIntake reads the durable request behind one comment.
func (s *Store) PostIntake(ctx context.Context, postID string) (factory.PostIntake, error) {
	var pipe factory.PostIntake
	err := s.db.QueryRowContext(ctx, `SELECT post,revision,intake,judgment,route,draft,effect,issue,attempts
		FROM factory_post_intakes WHERE post=$1`, postID).
		Scan(&pipe.SourcePost, &pipe.Revision, &pipe.Intake, &pipe.Judgment, &pipe.Route, &pipe.Draft, &pipe.Effect, &pipe.Issue, &pipe.Attempts)
	if err != nil {
		return factory.PostIntake{}, err
	}
	return pipe, nil
}

// AdvancePostIntake moves one request row through CAS: every state
// column must still hold its expected value or the write refuses with
// ErrStaleRevision instead of dropping a concurrent advance.
func (s *Store) AdvancePostIntake(ctx context.Context, expected, next factory.PostIntake) error {
	if expected.SourcePost == "" || expected.SourcePost != next.SourcePost {
		return errors.New("comment pipeline advance needs its comment identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE factory_post_intakes
		SET revision=$2,intake=$3,judgment=$4,route=$5,draft=$6,effect=$7,issue=$8,attempts=$9,
		data=(CASE WHEN $12='pending' AND $4='applied' AND $5='existing_work' THEN data || jsonb_build_object('feedback_revision',$2::bigint) ELSE data END)
		WHERE post=$1 AND revision=$10 AND intake=$11 AND judgment=$12 AND route=$13 AND draft=$14 AND effect=$15 AND issue=$16 AND attempts=$17`,
		next.SourcePost, next.Revision, next.Intake, next.Judgment, next.Route, next.Draft, next.Effect, next.Issue, next.Attempts,
		expected.Revision, expected.Intake, expected.Judgment, expected.Route, expected.Draft, expected.Effect, expected.Issue, expected.Attempts)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	if next.Issue > 0 && next.Issue != expected.Issue && (next.Effect == factory.PostEffectApplied || next.Effect == factory.PostEffectVerified) {
		var repository int64
		if err = tx.QueryRowContext(ctx, `SELECT repository FROM factory_feed_posts WHERE id=$1`, next.SourcePost).Scan(&repository); err != nil {
			return err
		}
		if err = recordFactoryFeedEventTx(ctx, tx, repository, next.Issue, "", "issue:"+strconv.FormatInt(next.Issue, 10), "native_issue:created", factory.FeedCommunication{
			ComponentID: "F05", Title: fmt.Sprintf("Issue #%d created from a post", next.Issue), Summary: "The native repository confirmed the new issue.",
			ParentID: next.SourcePost, Items: []factory.FeedCommunicationItem{{Label: fmt.Sprintf("Issue #%d", next.Issue), State: "created"}},
		}, 0); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NotePostAttempt records one funded judgment, draft or native
// retry against the bounded attempt count. Exhaustion refuses instead
// of spending blindly.
func (s *Store) NotePostAttempt(ctx context.Context, postID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE factory_post_intakes
		SET attempts=attempts+1 WHERE post=$1 AND attempts<$2`, postID, factory.PostAttemptLimit)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("comment attempts are exhausted")
	}
	return nil
}

// RecordPostDraft retains one LLM-authored issue draft bound to its
// exact comment source revision. A malformed draft refuses before any
// native effect.
func (s *Store) RecordPostDraft(ctx context.Context, draft factory.IssueDraft) error {
	if err := draft.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPostDraft, err)
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE factory_post_intakes SET data=data || $2::jsonb
		WHERE post=$1 AND revision=$3`, draft.SourcePost, string(raw), draft.Revision)
	return err
}

// PostDraft reads the retained draft behind one comment, refusing
// when the bound revision moved on (edited source) or no draft exists.
func (s *Store) PostDraft(ctx context.Context, postID string, revision int64) (factory.IssueDraft, error) {
	var raw []byte
	var saved int64
	if err := s.db.QueryRowContext(ctx, `SELECT data,revision FROM factory_post_intakes WHERE post=$1`, postID).Scan(&raw, &saved); err != nil {
		return factory.IssueDraft{}, err
	}
	if saved != revision || len(raw) == 0 || string(raw) == "{}" {
		return factory.IssueDraft{}, sql.ErrNoRows
	}
	var draft factory.IssueDraft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return factory.IssueDraft{}, err
	}
	if draft.SourcePost != postID || draft.Revision != revision {
		return factory.IssueDraft{}, sql.ErrNoRows
	}
	return draft, nil
}

// FeedPostDetails reads the serving fields behind a set of post
// entries: author, time and the pipeline-derived status. Comments
// without a pipeline row report status open; deleted or revoked
// sources never reach the feed.
func (s *Store) FeedPostDetails(ctx context.Context, ids []string) (map[string]FeedPostDetail, error) {
	out := make(map[string]FeedPostDetail, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.repository,c.revision,c.author,c.body,
		COALESCE(c.data->>'parent_id',''),COALESCE((c.data->>'issue')::bigint,0),
		COALESCE((c.data->>'edited')::boolean,false),COALESCE((c.data->>'deleted')::boolean,false),
		COALESCE((c.data->>'revoked')::boolean,false),COALESCE(c.data->>'author_name',''),
		COALESCE((c.data->>'at')::bigint,0),
		COALESCE(r.judgment,''),COALESCE(r.route,''),COALESCE(r.draft,''),COALESCE(r.effect,''),COALESCE(r.issue,0),COALESCE(r.attempts,0),c.data
		FROM factory_feed_posts c LEFT JOIN factory_post_intakes r ON r.post=c.id
		WHERE c.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var detail FeedPostDetail
		var judgment, route, draft, effect string
		var issue int64
		var attempts int
		var dataRaw []byte
		if err := rows.Scan(&detail.Record.ID, &detail.Record.Repository, &detail.Record.Revision,
			&detail.Record.Author, &detail.Body, &detail.Record.ParentID, &detail.Record.Issue,
			&detail.Record.Edited, &detail.Record.Deleted, &detail.Record.Revoked,
			&detail.AuthorName, &detail.At, &judgment, &route, &draft, &effect, &issue, &attempts, &dataRaw); err != nil {
			return nil, err
		}
		if detail.Record.Deleted || detail.Record.Revoked {
			continue
		}
		if judgment == "" {
			detail.Status = PostStatus(nil)
		} else {
			detail.Status = PostStatus(&factory.PostIntake{
				SourcePost: detail.Record.ID, Intake: factory.PostIntakeRecorded,
				Judgment: judgment, Route: route, Draft: draft, Effect: effect, Issue: issue, Attempts: attempts,
			})
		}
		if err := applyFeedCommunicationDetail(&detail, dataRaw); err != nil {
			return nil, err
		}
		out[detail.Record.ID] = detail
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.feedQuestionStates(ctx, out); err != nil {
		return nil, err
	}
	if err := s.feedPostResolutions(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolvePostSource resolves one caller-named feed entry inside
// the given repository into its persisted source snapshot. Unknown,
// closed or cross-repository entries report ErrNotFound without
// disclosing which: the caller already proved repository visibility,
// and the source must live inside that same repository. Evidence
// comes from the canonical owners only — the assignment row and the
// feed record read for records, the stored comment and its pipeline
// status for posts.
// ResolvePostParent resolves the one parent relationship for both stored messages
// and posts projected from canonical work records. The caller supplies no type.
func (s *Store) ResolvePostParent(ctx context.Context, repository int64, id string) (*factory.PostSource, error) {
	parent, err := s.ResolvePostSource(ctx, repository, factory.PostSourcePost, id)
	if !errors.Is(err, ErrNotFound) {
		return parent, err
	}
	return s.ResolvePostSource(ctx, repository, factory.PostSourceRecord, id)
}

func (s *Store) ResolvePostSource(ctx context.Context, repository int64, kind, id string) (*factory.PostSource, error) {
	if repository <= 0 {
		return nil, errors.New("comment source needs its repository scope")
	}
	switch kind {
	case factory.PostSourceRecord:
		entry, err := s.FeedEntryByID(ctx, id)
		if err != nil || entry.Repository != repository || entry.Kind != factory.FeedKindRecord {
			if err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			return nil, ErrNotFound
		}
		source := &factory.PostSource{Kind: kind, ID: id}
		var details map[string]FeedRecordDetail
		if entry.Activity == "" {
			assignment, readErr := s.Assignment(ctx, id)
			if readErr != nil {
				return nil, readErr
			}
			source.Issue = assignment.Issue
			source.Product, err = s.assignmentPostProduct(ctx, assignment)
			if err != nil {
				return nil, err
			}
			details, err = s.FeedRecordDetails(ctx, []string{id})
		} else {
			details, err = s.FeedMilestoneDetails(ctx, []factory.FeedEntry{entry})
		}
		if err != nil {
			return nil, err
		}
		if detail, ok := details[id]; ok {
			source.PR, source.Ref, source.Status, source.Summary = detail.PR, detail.Ref, detail.Status, detail.Summary
			if source.Issue <= 0 {
				source.Issue = detail.Issue
			}
		}
		return source, nil
	case factory.PostSourcePost:
		detail, err := s.FeedPost(ctx, id)
		if err != nil || detail.Record.Repository != repository || detail.Record.Deleted || detail.Record.Revoked {
			if err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			return nil, ErrNotFound
		}
		source := &factory.PostSource{
			Kind: kind, ID: id, Issue: detail.Record.Issue,
			Summary: TruncateFeedSummary(detail.Body), Status: PostStatus(nil),
		}
		source.Product, err = s.postPostProduct(ctx, detail)
		if err != nil {
			return nil, err
		}
		if detail.Payload != nil && detail.Payload.Communication != nil {
			source.Status = detail.Status
			return source, nil
		}
		if pipe, err := s.PostIntake(ctx, id); err == nil {
			source.Status = PostStatus(&pipe)
			if source.Issue <= 0 {
				source.Issue = pipe.Issue
			}
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return source, nil
	default:
		return nil, errors.New("comment source needs its known kind")
	}
}

// PendingPostIntakes lists durable comment requests the
// service-side driver may still advance: judgment pending, or a
// new_issue route whose draft or native effect is unsettled. Closed
// (deleted/revoked) sources never list. Settled routes
// (discussion, existing work, clarify), failed judgments, invalid
// drafts and settled effects are final and stay off the list, so a
// restart resumes each stored request instead of re-deriving it.
func (s *Store) PendingPostIntakes(ctx context.Context, limit int) ([]factory.PostIntake, error) {
	if limit <= 0 || limit > 64 {
		return nil, errors.New("invalid pending comment listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.post,r.revision,r.intake,r.judgment,r.route,r.draft,r.effect,r.issue,r.attempts
		FROM factory_post_intakes r
		JOIN factory_feed_posts c ON c.id=r.post
		WHERE COALESCE((c.data->>'deleted')::boolean,false)=false
		AND COALESCE((c.data->>'revoked')::boolean,false)=false
		AND r.attempts<$2
		AND (r.judgment='pending'
			OR (r.judgment='applied' AND r.route='new_issue' AND c.data#>>'{source,product,intent}' IS NULL
				AND (r.draft='pending' OR (r.draft='applied' AND r.effect IN ('pending','failed'))
				 OR (r.data->>'issue_maker_version'='1' AND r.effect='verified' AND NOT EXISTS(
				 SELECT 1 FROM factory_feed_posts maker WHERE maker.id=r.data->>'issue_maker_post'
				 AND maker.data->'issue_maker'->>'state' IN ('completed','failed')))))
			OR (r.judgment='applied' AND r.route='existing_work' AND r.effect IN ('pending','failed') AND (r.data->>'feedback_revision')::bigint=r.revision))
		ORDER BY COALESCE(r.data->>'last_checked_at',''),c.seq LIMIT $1`, limit, factory.PostAttemptLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.PostIntake
	for rows.Next() {
		var pipe factory.PostIntake
		if err := rows.Scan(&pipe.SourcePost, &pipe.Revision, &pipe.Intake, &pipe.Judgment, &pipe.Route, &pipe.Draft, &pipe.Effect, &pipe.Issue, &pipe.Attempts); err != nil {
			return nil, err
		}
		out = append(out, pipe)
	}
	return out, rows.Err()
}

// NotePostProgress retains a safe user-facing reason and rotates waiting
// requests through the continuation queue without consuming model attempts.
func (s *Store) NotePostProgress(ctx context.Context, id, detail string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE factory_post_intakes
		SET data=data || jsonb_build_object('last_checked_at',clock_timestamp()::text,'detail',$2::text) WHERE post=$1`, id, TruncateFeedSummary(detail))
	return err
}

// NotePostContextWait cannot attach a late capture failure to newer writing or
// overwrite successfully captured context. The caller owns this exact revision.
func (s *Store) NotePostContextWait(ctx context.Context, id string, revision int64, reason, detail string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE factory_post_intakes i
	 SET data=i.data || jsonb_build_object('stalled',$3::text,'detail',$4::text,'last_checked_at',clock_timestamp()::text)
	 WHERE i.post=$1 AND i.revision=$2 AND EXISTS (
	 SELECT 1 FROM factory_feed_posts p WHERE p.id=i.post AND p.revision=$2
	 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
	 AND NOT COALESCE((p.data->>'issue_context_revision')::bigint=$2,false))`, id, revision, reason, TruncateFeedSummary(detail))
	return checkGrantRowsAffected(result, err, "post context wait failed")
}

func (s *Store) NotePostAuthoring(ctx context.Context, id string, revision int64, progress factory.PostAuthoring, now int64) error {
	if err := progress.Validate(); err != nil {
		return err
	}
	progress.Detail = TruncateFeedSummary(progress.Detail)
	progress.CheckedUnix, progress.SinceUnix = now, now
	body, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE factory_post_intakes
	 SET data=data || jsonb_build_object('authoring', $3::jsonb || jsonb_build_object('since_unix',
	 CASE WHEN data->'authoring'->>'state'=$4 AND COALESCE(data->'authoring'->>'reason','')=$5
	 THEN COALESCE((data->'authoring'->>'since_unix')::bigint,$6) ELSE $6 END))
	 WHERE post=$1 AND revision=$2`, id, revision, string(body), progress.State, progress.Reason, now)
	return err
}

func (s *Store) PostAuthoring(ctx context.Context, id string) (*factory.PostAuthoring, error) {
	var body []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data->'authoring' FROM factory_post_intakes WHERE post=$1`, id).Scan(&body); err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	var progress factory.PostAuthoring
	if err := json.Unmarshal(body, &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

// NotePostRouting keeps JEV's routing Choice beside the request.
func (s *Store) NotePostRouting(ctx context.Context, id string, routing factory.PostRouting) error {
	body, err := json.Marshal(routing)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE factory_post_intakes
		SET data=data || jsonb_build_object('routing',$2::jsonb) WHERE post=$1`, id, string(body))
	return err
}

// PostRouting reads the recorded routing Choice, nil before JEV answers.
func (s *Store) PostRouting(ctx context.Context, id string) (*factory.PostRouting, error) {
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT data->'routing' FROM factory_post_intakes WHERE post=$1`, id).Scan(&body)
	if err != nil || len(body) == 0 || string(body) == "null" {
		return nil, err
	}
	var routing factory.PostRouting
	if err := json.Unmarshal(body, &routing); err != nil {
		return nil, err
	}
	return &routing, nil
}

// NotePostStall records why a request cannot move on its own (empty clears).
func (s *Store) NotePostStall(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE factory_post_intakes
		SET data=CASE WHEN $2='' THEN data - 'stalled' ELSE data || jsonb_build_object('stalled',$2::text) END WHERE post=$1`, id, reason)
	return err
}

func (s *Store) PostStall(ctx context.Context, id string) (string, error) {
	var reason string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(data->>'stalled','') FROM factory_post_intakes WHERE post=$1`, id).Scan(&reason)
	return reason, err
}

func (s *Store) PostProgress(ctx context.Context, id string) (string, error) {
	var detail string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(data->>'detail','') FROM factory_post_intakes WHERE post=$1`, id).Scan(&detail)
	return detail, err
}

func (s *Store) PostFeedbackRequested(ctx context.Context, id string, revision int64) (bool, error) {
	var pending bool
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE((data->>'feedback_revision')::bigint=$2,false) FROM factory_post_intakes WHERE post=$1`, id, revision).Scan(&pending)
	return pending, err
}

// RetryFeedPost preserves the original post and prior processing history.
// Uncertain issue writes keep their draft/identity for reconciliation; a new
// judgment/planning attempt gets a new source revision instead of another post.
func (s *Store) RetryFeedPost(ctx context.Context, id, author string, expected int64, body string, at int64) error {
	return s.retryFeedPost(ctx, id, author, expected, body, at, false)
}

func (s *Store) retryFeedPost(ctx context.Context, id, author string, expected int64, body string, at int64, issueMaker bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := retryFeedPostTx(ctx, tx, id, author, expected, body, at, issueMaker, 0, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func retryFeedPostTx(ctx context.Context, tx *sql.Tx, id, author string, expected int64, body string, at int64, issueMaker bool, expectedRepository int64, commandID string) error {
	var savedAuthor, oldBody string
	var revision, repository int64
	var closed bool
	if err := tx.QueryRowContext(ctx, `SELECT author,body,revision,repository,COALESCE((data->>'deleted')::boolean,false) OR COALESCE((data->>'revoked')::boolean,false) OR data ? 'product_change'
		FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, id).Scan(&savedAuthor, &oldBody, &revision, &repository, &closed); err != nil {
		return err
	}
	if savedAuthor != author || revision != expected || closed || (expectedRepository > 0 && repository != expectedRepository) {
		return ErrStaleRevision
	}
	if issueMaker {
		var rolePost string
		err := tx.QueryRowContext(ctx, `SELECT id FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, factory.IssueMakerPostID(id, revision)).Scan(&rolePost)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	var draft, effect, route, judgment string
	var attempts int
	var previousMaker bool
	if err := tx.QueryRowContext(ctx, `SELECT draft,effect,route,judgment,attempts,COALESCE(data->>'issue_maker_version','')='1' FROM factory_post_intakes WHERE post=$1 FOR UPDATE`, id).Scan(&draft, &effect, &route, &judgment, &attempts, &previousMaker); err != nil {
		return err
	}
	var needsContext bool
	if err := tx.QueryRowContext(ctx, `SELECT NOT COALESCE((data->>'issue_context_revision')::bigint=$2,false) FROM factory_feed_posts WHERE id=$1`, id, revision).Scan(&needsContext); err != nil {
		return err
	}
	refreshContext := needsContext && judgment == factory.PostJudgmentPending && draft == factory.PostDraftPending && effect == factory.PostEffectPending
	var unstartedWait bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(data->'authoring'->>'state'='waiting' AND COALESCE(data->'authoring'->>'run','')='',false) FROM factory_post_intakes WHERE post=$1`, id).Scan(&unstartedWait); err != nil {
		return err
	}
	unstartedWait = unstartedWait && draft == factory.PostDraftPending && effect == factory.PostEffectPending
	continueRefinement := issueMaker && previousMaker && effect == factory.PostEffectVerified
	if !continueRefinement && !refreshContext && !unstartedWait && !(factory.PostIntake{Draft: draft, Effect: effect, Route: route, Judgment: judgment, Attempts: attempts}).RetryAllowed() {
		return errors.New("this request is still processing or has already completed")
	}
	if effect == factory.PostEffectApplied || effect == factory.PostEffectVerified {
		if !issueMaker || !previousMaker || !factory.ValidID(commandID) || effect != factory.PostEffectVerified || body != "" && body != oldBody {
			return errors.New("this request already created an issue")
		}
		var raw []byte
		post := factory.IssueMakerPostID(id, revision)
		if err := tx.QueryRowContext(ctx, `SELECT data->'issue_maker' FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, post).Scan(&raw); err != nil {
			return err
		}
		var maker factory.IssueMakerProgress
		if json.Unmarshal(raw, &maker) != nil || maker.Validate() != nil || maker.Scope.Issue <= 0 || (maker.State != "waiting" && maker.State != "failed" && maker.State != "unknown") {
			return errors.New("issue refinement is still processing or completed")
		}
		// The latest recorded attempt, rather than a display field that may be
		// empty while waiting, owns whether another inference can be admitted.
		attemptErr := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE data->'subject'->'intent'->>'intent_id'=$1 AND data->'subject'->'intent'->>'revision_id'=$2 ORDER BY seq DESC LIMIT 1`, factory.PostDecisionID("intent", id, revision), factory.PostDecisionID("revision", id, revision)).Scan(&raw)
		if attemptErr == nil {
			var assignment factory.Assignment
			if err := json.Unmarshal(raw, &assignment); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1`, assignment.Run).Scan(&raw); err != nil {
				return err
			}
			var run factory.Run
			if json.Unmarshal(raw, &run) != nil || !run.Reconciled {
				return errors.New("original refinement run must settle before fresh authorization")
			}
			if run.Outcome == factory.Failed || run.Outcome == factory.Cancelled {
				maker.Scope.Attempt = commandID
			}
		} else if errors.Is(attemptErr, sql.ErrNoRows) {
			maker.Scope.Attempt = commandID
		} else {
			return attemptErr
		}
		maker.State, maker.Detail = "queued", "Continuing the saved issue refinement under fresh request authorization; original operations and results are retained."
		raw, err := json.Marshal(maker)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{issue_maker}',$2::jsonb) WHERE id=$1`, post, string(raw)); err != nil {
			return err
		}
		// Only this explicit, journaled continuation archives the expired proof.
		// Existing native command preparations/deadlines remain immutable.
		_, err = tx.ExecContext(ctx, `UPDATE factory_post_intakes SET data=(data-'issue_intake_grant'-'issue_intake_scope') || CASE WHEN data ? 'issue_intake_grant' THEN jsonb_build_object('issue_intake_grant_history',COALESCE(data->'issue_intake_grant_history','[]'::jsonb)||jsonb_build_array(data->'issue_intake_grant')) ELSE '{}'::jsonb END WHERE post=$1`, id)
		return err
	}
	if body == "" {
		body = oldBody
	}
	if err := (factory.PostRequest{Repository: 1, Body: body, IdempotencyKey: "retry"}).Validate(); err != nil {
		return err
	}
	var err error
	if draft == factory.PostDraftApplied {
		if body != oldBody {
			return errors.New("check the previous issue creation before changing this request")
		}
		_, err = tx.ExecContext(ctx, `UPDATE factory_post_intakes SET attempts=0,data=data || '{"detail":""}'::jsonb WHERE post=$1`, id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE factory_feed_posts SET revision=revision+1,body=$2,
		 data=data || jsonb_build_object('edited',true,'at',$3::bigint,'history',(CASE WHEN jsonb_typeof(data->'history')='array' THEN data->'history' ELSE '[]'::jsonb END) || jsonb_build_array(jsonb_build_object('revision',revision,'body',body))) WHERE id=$1`, id, body, at)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE factory_post_intakes SET revision=$2,judgment='pending',route='',draft='pending',effect='pending',issue=0,attempts=0,
			 data=jsonb_build_object('history',(CASE WHEN jsonb_typeof(data->'history')='array' THEN data->'history' ELSE '[]'::jsonb END) || jsonb_build_array(jsonb_build_object('revision',revision,'judgment',judgment,'route',route,'issue',issue))) || CASE WHEN $3 THEN '{"issue_maker_version":1}'::jsonb ELSE '{}'::jsonb END WHERE post=$1`, id, expected+1, issueMaker || previousMaker)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

// RecentPostWorkCandidates lists the most recently worked native
// issues in one repository as existing-work candidates for comment
// routing: the latest assignment per issue supplies the recorded
// summary and status through the canonical feed record read. Issues
// with no recorded factory work never appear; nothing is inferred
// beyond the recorded rows.
func (s *Store) RecentPostWorkCandidates(ctx context.Context, repository int64, limit int) ([]factory.PostWorkCandidate, error) {
	if repository <= 0 || limit <= 0 || limit > 32 {
		return nil, errors.New("invalid comment work candidate listing")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,issue FROM (
			SELECT DISTINCT ON (issue) id,issue,seq FROM factory_assignments
			WHERE repository=$1 AND issue>0 ORDER BY issue,seq DESC
		) latest ORDER BY seq DESC LIMIT $2`, repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	issueByID := map[string]int64{}
	for rows.Next() {
		var id string
		var issue int64
		if err := rows.Scan(&id, &issue); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		issueByID[id] = issue
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	details, err := s.FeedRecordDetails(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]factory.PostWorkCandidate, 0, len(ids))
	for _, id := range ids {
		candidate := factory.PostWorkCandidate{Issue: issueByID[id], Origin: factory.PostCandidateRecent}
		if detail, ok := details[id]; ok {
			candidate.Summary, candidate.Status = detail.Summary, detail.Status
		}
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}

// PostStatus derives one post's feed status from its pipeline row: the
// separate states stay distinguishable instead of collapsing into one
// generic marker.
func PostStatus(pipe *factory.PostIntake) string {
	if pipe == nil {
		return "open"
	}
	if pipe.Judgment == factory.PostJudgmentPending {
		if pipe.Attempts >= factory.PostAttemptLimit {
			return "judgment:failed"
		}
		return "judgment:pending"
	}
	if pipe.Judgment == factory.PostJudgmentFailed {
		return "judgment:failed"
	}
	switch pipe.Route {
	case factory.PostRouteExistingWork:
		if pipe.Effect == factory.PostEffectApplied || pipe.Effect == factory.PostEffectVerified {
			return "feedback:delivered"
		}
		return "route:" + pipe.Route
	case factory.PostRouteDiscussion, factory.PostRouteClarify:
		return "route:" + pipe.Route
	}
	if pipe.Route != factory.PostRouteNewIssue {
		return "open"
	}
	if pipe.Draft == factory.PostDraftPending {
		if pipe.Attempts >= factory.PostAttemptLimit {
			return "draft:failed"
		}
		return "draft:pending"
	}
	if pipe.Draft == factory.PostDraftInvalid {
		return "draft:invalid"
	}
	switch pipe.Effect {
	case factory.PostEffectVerified:
		return fmt.Sprintf("issue:%d", pipe.Issue)
	case factory.PostEffectApplied:
		return "effect:applied"
	case factory.PostEffectBlocked:
		return "effect:blocked"
	case factory.PostEffectFailed:
		return "effect:failed"
	default:
		if pipe.Attempts >= factory.PostAttemptLimit {
			return "effect:unconfirmed"
		}
		return "effect:pending"
	}
}
