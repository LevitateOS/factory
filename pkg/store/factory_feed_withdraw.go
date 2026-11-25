package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
)

// feedWithdrawalSQL is the one link from a post p to the live record w that
// withdraws it (factory.FeedWithdrawal): a post in the same repository whose
// withdrawal names p. Serving, needs-you, attention, answers and the
// once-only rule all read it.
func feedWithdrawalSQL(w, p string) string {
	return w + `.repository=` + p + `.repository AND ` + w + `.data->'withdrawal'->>'post'=` + p + `.id
 AND NOT COALESCE((` + w + `.data->>'deleted')::boolean,false) AND NOT COALESCE((` + w + `.data->>'revoked')::boolean,false)`
}

// feedWithdrawnSQL holds when its author has withdrawn the post p.
func feedWithdrawnSQL(p string) string {
	return `EXISTS(SELECT 1 FROM factory_feed_posts w WHERE ` + feedWithdrawalSQL("w", p) + `)`
}

// feedAskWaitsSQL is the one definition of an ask that still waits for the
// person, for the post alias p (feedReplySQL's question alias): a live ask
// with no feedReplySQL reply (the "open" question state), whose agent request
// the recorded setting does not yet meet (feedRequestMetSQL) and which its
// author has not withdrawn. Both feedNeedsYouSQL and the feed's attention
// view use it.
func feedAskWaitsSQL() string {
	return `p.data->'communication'->>'kind'='ask'
 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)
 AND NOT EXISTS(SELECT 1 FROM factory_feed_posts c WHERE ` + feedReplySQL + `)
 AND NOT ` + feedRequestMetSQL("p") + `
 AND NOT ` + feedWithdrawnSQL("p")
}

// feedAuthor is who may withdraw: a worker role (any of its assignments in
// the repository) or the person by their author key.
type feedAuthor struct {
	role   factory.MCPProfile
	person string
}

func (a feedAuthor) owns(author, actor string) bool {
	if a.role != "" {
		return strings.HasPrefix(author, "worker:") && actor == string(a.role)
	}
	return a.person != "" && author == a.person
}

// checkWithdrawalTx admits one withdrawal against the recorded posts: the
// post is live in the repository, written by the same author, not itself a
// withdrawal and not yet withdrawn; a correction is a later live post by the
// same author. Both rows are locked before a later statement reads them, so
// a concurrent second withdrawal waits and then sees the first.
func checkWithdrawalTx(ctx context.Context, tx *sql.Tx, repository int64, by feedAuthor, w factory.FeedWithdrawal) error {
	if err := w.Validate(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM factory_feed_posts WHERE id IN ($1,$2) AND repository=$3 ORDER BY id FOR UPDATE`, w.Post, w.Correction, repository); err != nil {
		return err
	}
	type row struct {
		author, actor         string
		seq                   int64
		withdrawal, withdrawn bool
	}
	read := func(id string) (row, error) {
		var r row
		err := tx.QueryRowContext(ctx, `SELECT p.author,COALESCE(p.data->>'actor',''),p.seq,p.data ? 'withdrawal',`+feedWithdrawnSQL("p")+`
		 FROM factory_feed_posts p WHERE p.id=$1 AND p.repository=$2
		 AND NOT COALESCE((p.data->>'deleted')::boolean,false) AND NOT COALESCE((p.data->>'revoked')::boolean,false)`, id, repository).
			Scan(&r.author, &r.actor, &r.seq, &r.withdrawal, &r.withdrawn)
		return r, err
	}
	target, err := read(w.Post)
	if err != nil {
		return err
	}
	switch {
	case !by.owns(target.author, target.actor):
		return factory.ErrWithdrawNotAuthor
	case target.withdrawal:
		return errors.Join(factory.ErrWithdrawalInvalid, errors.New("a withdrawal cannot itself be withdrawn"))
	case target.withdrawn:
		return factory.ErrPostWithdrawn
	}
	if w.Correction == "" {
		return nil
	}
	correction, err := read(w.Correction)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!by.owns(correction.author, correction.actor) || correction.seq <= target.seq || correction.withdrawal || correction.withdrawn) {
		return errors.Join(factory.ErrWithdrawalInvalid, errors.New("the correction must be a later live post by the same author"))
	}
	return err
}

// recordWorkerWithdrawalTx records factory.withdraw as the worker's own post:
// an FYI communication whose summary is the reason, with the withdrawal
// beside it.
func recordWorkerWithdrawalTx(ctx context.Context, tx *sql.Tx, capability factory.MCPCapability, submission factory.MCPSubmission) error {
	w, err := factory.DecodeWithdrawal(submission.Tool, submission.Payload)
	if err != nil {
		return err
	}
	if err := checkWithdrawalTx(ctx, tx, capability.Repository, feedAuthor{role: capability.Profile}, w); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("the post to withdraw is not available in this project")
		}
		return err
	}
	title := "Withdrew an earlier post"
	if w.Correction != "" {
		title = "Corrected an earlier post"
	}
	_, err = recordWorkerPostTx(ctx, tx, capability, submission, factory.FeedCommunication{Kind: "withdraw", Title: title, Summary: w.Reason})
	return err
}

// saveFeedWithdrawal records the person's withdrawal of their own post as a
// new post whose body is the reason. The same author/idempotency key replays
// the original record. It opens no intake: a withdrawal is not routed.
func (s *Store) saveFeedWithdrawal(ctx context.Context, req factory.PostRequest, author string, fields map[string]any) (string, int64, error) {
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
	if err := checkWithdrawalTx(ctx, tx, req.Repository, feedAuthor{person: author}, *req.Withdraw); err != nil {
		return "", 0, err
	}
	fields["withdrawal"] = req.Withdraw
	data, err := json.Marshal(fields)
	if err != nil {
		return "", 0, err
	}
	if id, err = newFeedPostID(); err != nil {
		return "", 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data) VALUES($1,$2,0,$3,$4,$5,$6)`,
		id, req.Repository, author, req.Withdraw.Reason, req.IdempotencyKey, string(data)); err != nil {
		return "", 0, err
	}
	return id, 0, tx.Commit()
}

// feedWithdrawals serves Withdrawn on each withdrawn post from its record.
func (s *Store) feedWithdrawals(ctx context.Context, details map[string]FeedPostDetail) error {
	ids := make([]string, 0, len(details))
	for id := range details {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,COALESCE(w.data->>'author_name',''),w.data->'withdrawal'->>'reason',
	 COALESCE(w.data->'withdrawal'->>'correction',''),COALESCE((w.data->>'at')::bigint,0)
	 FROM factory_feed_posts p JOIN factory_feed_posts w ON `+feedWithdrawalSQL("w", "p")+` WHERE p.id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var withdrawn factory.FeedWithdrawn
		if err := rows.Scan(&id, &withdrawn.By, &withdrawn.Reason, &withdrawn.Correction, &withdrawn.At); err != nil {
			return err
		}
		detail := details[id]
		detail.Withdrawn = &withdrawn
		details[id] = detail
	}
	return rows.Err()
}
