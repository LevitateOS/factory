package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// ContributionOwner holds a dedicated PostgreSQL session while a producer
// claims and settles assessments. Losing this session fences its later writes;
// another connection can prove ownership ended without trusting a timestamp.
// The private owner identity is not a worker capability or provider credential.
type ContributionOwner struct {
	store *Store
	conn  *sql.Conn
	id    string
}

const contributionOwnerPrefix = "soda-contribution-owner:"

func (s *Store) OwnContributions(ctx context.Context) (*ContributionOwner, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	owner := &ContributionOwner{store: s, conn: conn, id: factory.NewID()}
	var locked bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, contributionOwnerPrefix+owner.id).Scan(&locked)
	if err != nil || !locked {
		_ = owner.Close()
		if err == nil {
			err = errors.New("contribution ownership is unavailable")
		}
		return nil, err
	}
	return owner, nil
}

// Close discards the session rather than returning a session-level advisory lock
// to the pool. PostgreSQL releases ownership when that connection actually ends.
// A still-running model response cannot settle through a closed owner.
func (o *ContributionOwner) Close() error {
	err := o.conn.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return nil
	}
	return err
}

// ContributionRecovery is one bounded traversal of owned pending assessments.
// Next is a feed sequence cursor; live owners do not starve later lost owners.
// Unowned historical pending rows remain unknown and are never guessed dead.
type ContributionRecovery struct {
	Next      int64
	Recovered int
	Complete  bool
}

const contributionRecoveryLimit = 32

func (s *Store) RecoverContributionReviews(ctx context.Context, after int64) (ContributionRecovery, error) {
	result := ContributionRecovery{Next: after}
	if after < 0 {
		return result, errors.New("invalid contribution recovery cursor")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,id,data->>'contribution_owner' FROM factory_feed_posts
 WHERE seq>$1 AND data->'contribution'->>'state'='pending'
 AND data->>'contribution_owner' ~ '^[a-f0-9]{32}$' ORDER BY seq LIMIT $2`, after, contributionRecoveryLimit+1)
	if err != nil {
		return result, err
	}
	type candidate struct {
		seq       int64
		id, owner string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.seq, &c.id, &c.owner); err != nil {
			rows.Close()
			return result, err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	result.Complete = len(candidates) <= contributionRecoveryLimit
	if !result.Complete {
		candidates = candidates[:contributionRecoveryLimit]
	}
	for _, c := range candidates {
		recovered, err := s.recoverContributionReview(ctx, c.id, c.owner)
		if err != nil {
			return result, err
		}
		if recovered {
			result.Recovered++
		}
		result.Next = c.seq
	}
	return result, nil
}

func (s *Store) recoverContributionReview(ctx context.Context, id, owner string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var raw []byte
	var repository int64
	err = tx.QueryRowContext(ctx, `SELECT repository,data->'contribution' FROM factory_feed_posts
 WHERE id=$1 AND data->>'contribution_owner'=$2 AND data->'contribution'->>'state'='pending' FOR UPDATE`, id, owner).Scan(&repository, &raw)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var released bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, contributionOwnerPrefix+owner).Scan(&released); err != nil || !released {
		return false, err
	}
	var review factory.ContributionReview
	if json.Unmarshal(raw, &review) != nil || !factory.ValidID(review.Source) || review.Revision < 0 || review.Repository != repository || factory.ContributionReviewInvitationID(review) != id || review.State != "pending" {
		return false, errors.New("invalid retained contribution assessment")
	}
	// Calls may have happened, but their outcome was not durably confirmed.
	// Keep the same receipt and never invite, reopen it or re-run the judgments.
	review.State, review.Assignment, review.Profile = "unavailable", "", ""
	return true, settleContributionReviewTx(ctx, tx, review)
}
