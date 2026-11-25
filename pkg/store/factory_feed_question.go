package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/levitateos/factory/pkg/factory"
)

// ErrQuestionAnswered refuses a second typed answer to one ask. Its first live
// typed answer stays the recorded one; free-text replies remain open.
var ErrQuestionAnswered = errors.New("the question already has a recorded answer")

// saveQuestionAnswer records one typed answer as an ordinary reply to its ask.
// The same author/idempotency key replays the original reply. Otherwise the
// ask is locked, the answer is checked against its declared form, and the
// reply stores the canonical answer as data with its plain-text rendering as
// the body.
func (s *Store) saveQuestionAnswer(ctx context.Context, req factory.PostRequest, author string, fields map[string]any) (string, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT id,revision FROM factory_feed_posts WHERE repository=$1 AND author=$2 AND idempotency=$3`, req.Repository, author, req.IdempotencyKey).Scan(&id, &revision)
	if err == nil {
		return id, revision, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(data->'communication','null'::jsonb) FROM factory_feed_posts
	 WHERE id=$1 AND repository=$2 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false)
	 FOR UPDATE`, req.ParentID, req.Repository).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, ErrNotFound
		}
		return "", 0, err
	}
	var ask *factory.FeedCommunication
	if err := json.Unmarshal(raw, &ask); err != nil {
		return "", 0, err
	}
	if ask == nil || ask.Kind != "ask" || ask.AnswerForm == nil {
		return "", 0, fmt.Errorf("%w: the parent is not a typed question", factory.ErrQuestionAnswerInvalid)
	}
	// Read after the lock above, so a withdrawal committed meanwhile is seen.
	var withdrawn bool
	if err := tx.QueryRowContext(ctx, `SELECT `+feedWithdrawnSQL("p")+` FROM factory_feed_posts p WHERE p.id=$1`, req.ParentID).Scan(&withdrawn); err != nil {
		return "", 0, err
	}
	if withdrawn {
		return "", 0, factory.ErrPostWithdrawn
	}
	answer, text, err := ask.AnswerForm.Admit(*req.Answer)
	if err != nil {
		return "", 0, err
	}
	var answered bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts c, (SELECT $1::text AS id, $2::bigint AS repository) p
	 WHERE `+feedReplySQL+` AND c.data ? 'answer')`, req.ParentID, req.Repository).Scan(&answered); err != nil {
		return "", 0, err
	}
	if answered {
		return "", 0, ErrQuestionAnswered
	}
	fields["answer"] = answer
	data, err := json.Marshal(fields)
	if err != nil {
		return "", 0, err
	}
	if id, err = newFeedPostID(); err != nil {
		return "", 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data) VALUES($1,$2,0,$3,$4,$5,$6)`,
		id, req.Repository, author, text, req.IdempotencyKey, string(data)); err != nil {
		return "", 0, err
	}
	return id, 0, tx.Commit()
}
