package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// ErrTakeoverConflict reports a reused takeover identity with changed
// content. The same run and destination with identical content replays.
var ErrTakeoverConflict = errors.New("takeover identity reused for different content")

// RecordTakeover saves the confirmed runtime result on its attributed Feed
// post. The run/destination key identifies the event and its canonical result.
func (s *Store) RecordTakeover(ctx context.Context, r factory.TakeoverRecord) (factory.TakeoverRecord, error) {
	if err := r.Validate(); err != nil {
		return factory.TakeoverRecord{}, err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return factory.TakeoverRecord{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.TakeoverRecord{}, err
	}
	defer tx.Rollback()
	var runJSON []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 FOR SHARE`, r.Run).Scan(&runJSON); err != nil {
		return factory.TakeoverRecord{}, err
	}
	var run factory.Run
	if err := json.Unmarshal(runJSON, &run); err != nil {
		return factory.TakeoverRecord{}, err
	}
	if !r.BindsRun(run) {
		return factory.TakeoverRecord{}, ErrTakeoverConflict
	}
	repository, issue, assignment, err := factoryRunFeedScopeTx(ctx, tx, r.Run, r.Project)
	if err != nil {
		return factory.TakeoverRecord{}, err
	}
	if repository <= 0 {
		return factory.TakeoverRecord{}, ErrNotFound
	}
	identity := "takeover:" + r.Run + ":" + r.TargetKey()
	summary := r.Member + " can continue from the retained worker changes."
	if r.Native != nil {
		summary = "The retained worker changes are available in your checkout."
	}
	at, _ := time.Parse(time.RFC3339, r.Copied)
	if err = recordFactoryFeedEventTx(ctx, tx, repository, issue, assignment, identity, "takeover:copied", factory.FeedCommunication{
		ComponentID: "F08", Title: "Work copied for human takeover", Summary: summary,
		Items: []factory.FeedCommunicationItem{{Label: "Working copy", State: "copied"}},
	}, at.Unix()); err != nil {
		return factory.TakeoverRecord{}, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{takeover}',COALESCE(data->'takeover',$1::jsonb)) WHERE repository=$2 AND author='factory' AND data->>'event_identity'=$3 AND data->>'status'='takeover:copied' RETURNING data->'takeover'`, string(data), repository, identity).Scan(&raw); err != nil {
		return factory.TakeoverRecord{}, err
	}
	var stored factory.TakeoverRecord
	if err = json.Unmarshal(raw, &stored); err != nil {
		return factory.TakeoverRecord{}, err
	}
	if !stored.Equal(r) {
		return factory.TakeoverRecord{}, ErrTakeoverConflict
	}
	return stored, tx.Commit()
}

// Takeover reads one destination's completed recovery from its Feed post.
func (s *Store) Takeover(ctx context.Context, run, targetKey string) (factory.TakeoverRecord, error) {
	var result factory.TakeoverRecord
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data->'takeover' FROM factory_feed_posts WHERE author='factory' AND data->>'event_identity'=$1 AND data->>'status'='takeover:copied' AND data ? 'takeover'`, "takeover:"+run+":"+targetKey).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	return result, err
}
