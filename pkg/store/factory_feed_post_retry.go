package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// RetryIssueMakerFeedPostCommand commits the retry and its immutable receipt in
// one transaction. replayed is true only for an already committed command; that
// path never changes the current post/intake. Callers must admit current actor
// visibility and authorship before calling, including when recovering a receipt.
func (s *Store) RetryIssueMakerFeedPostCommand(ctx context.Context, request factory.FeedPostRetryCommand, at int64) (receipt factory.FeedPostRetryReceipt, replayed bool, err error) {
	command, err := request.Command()
	if err != nil {
		return receipt, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return receipt, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, created, err := recordFactoryCommandTx(ctx, tx, command, time.Unix(at, 0))
	if err != nil {
		return receipt, false, err
	}
	if stored.Type != command.Type || stored.Target != command.Target || stored.Principal != command.Principal || stored.Payload != command.Payload {
		return receipt, false, ErrCommandConflict
	}
	// Check actual retained scope even for replay. The receipt is historical, so
	// head revision/processing state must not be required to match its result.
	var author string
	var repository int64
	if err = tx.QueryRowContext(ctx, `SELECT author,repository FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, request.Post).Scan(&author, &repository); err != nil {
		return receipt, false, err
	}
	if author != request.Author || repository != request.Repository {
		return receipt, false, ErrStaleRevision
	}
	if !created {
		if stored.Finished == "" || stored.Outcome == "" {
			return receipt, false, errors.New("post retry command has no committed receipt")
		}
		if err = json.Unmarshal([]byte(stored.Outcome), &receipt); err != nil {
			return receipt, false, err
		}
		if err = receipt.Validate(request); err != nil {
			return receipt, false, err
		}
		return receipt, true, nil
	}
	if err = retryFeedPostTx(ctx, tx, request.Post, request.Author, request.ExpectedRevision, request.Body, at, true, request.Repository); err != nil {
		return receipt, false, err
	}
	var revision int64
	var body string
	if err = tx.QueryRowContext(ctx, `SELECT revision,body FROM factory_feed_posts WHERE id=$1`, request.Post).Scan(&revision, &body); err != nil {
		return receipt, false, err
	}
	receipt = factory.FeedPostRetryReceipt{CommandID: request.ID, Post: request.Post, Repository: strconv.FormatInt(request.Repository, 10), Author: request.Author, ExpectedRevision: request.ExpectedRevision, Revision: revision, BodyDigest: factory.MCPInputDigest([]byte(body))}
	if err = receipt.Validate(request); err != nil {
		return receipt, false, err
	}
	outcome, err := json.Marshal(receipt)
	if err != nil {
		return receipt, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$2,finished=$3 WHERE id=$1 AND finished=''`, request.ID, string(outcome), time.Unix(at, 0).UTC().Format(time.RFC3339Nano))
	if err = checkGrantRowsAffected(result, err, "post retry receipt save failed"); err != nil {
		return receipt, false, err
	}
	if err = tx.Commit(); err != nil {
		return receipt, false, err
	}
	return receipt, false, nil
}
