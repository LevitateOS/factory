package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/levitateos/sodaos/internal/factory"
)

// FeedSubscription reads one subscriber's combined-feed preferences.
// A missing row reports revision 0 with an empty scope: the serving
// layer treats no preference as no subscribed repositories, never as
// the widest scope.
func (s *Store) FeedSubscription(ctx context.Context, subscriber string) (factory.FeedSubscription, int64, error) {
	sub := factory.FeedSubscription{Subscriber: subscriber}
	var revision int64
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision,data FROM factory_feed_subscriptions WHERE subscriber=$1`, subscriber).Scan(&revision, &raw)
	if errors.Is(err, ErrNotFound) {
		return sub, 0, nil
	}
	if err != nil {
		return sub, 0, err
	}
	if err = json.Unmarshal(raw, &sub); err != nil {
		return sub, 0, err
	}
	return sub, revision, nil
}

// SetFeedSubscription stores one subscriber's feed preferences under
// revision CAS: expected 0 inserts, otherwise the revision must match
// and bumps by one. A stale write refuses instead of dropping another
// update.
func (s *Store) SetFeedSubscription(ctx context.Context, subscriber string, expected int64, sub factory.FeedSubscription) error {
	if subscriber == "" || sub.Subscriber != subscriber || expected < 0 || expected == math.MaxInt64 {
		return errors.New("invalid feed subscription revision")
	}
	sub.Revision = expected + 1
	if err := sub.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	query := `UPDATE factory_feed_subscriptions SET revision=revision+1,data=$3 WHERE subscriber=$1 AND revision=$2`
	if expected == 0 {
		query = `INSERT INTO factory_feed_subscriptions(subscriber,revision,data) SELECT $1,1,$3::jsonb WHERE $2::bigint=0 ON CONFLICT DO NOTHING`
	}
	result, err := s.db.ExecContext(ctx, query, subscriber, expected, string(raw))
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
	return nil
}
