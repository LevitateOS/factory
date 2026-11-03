package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// recordPostResolutionTx posts one resolve_post disposition.
// The target must be a live suggestion or answer in this repository
// and match its resolution; a second disposition for the same post
// refuses so recorded states survive exactly as written.
func recordPostResolutionTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, submission factory.MCPSubmission) error {
	resolution, err := factory.DecodePostResolution(submission.Tool, submission.Payload)
	if err != nil {
		return err
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(data->'communication'->>'kind','') FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false) FOR UPDATE`, resolution.Post, cap.Repository).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("resolution target is not available in this project")
		}
		return err
	}
	if err = factory.ResolutionForPost(kind, resolution.Resolution); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE repository=$1 AND data->'resolution'->>'post'=$2)`, cap.Repository, resolution.Post).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errors.New("post resolution already recorded")
	}
	_, err = recordWorkerPostTx(ctx, tx, cap, submission, factory.FeedCommunication{Kind: "resolution", ParentID: resolution.Post, Title: "Post " + resolution.Resolution, Summary: resolution.Reason})
	return err
}

// feedPostResolutions attaches recorded dispositions without mutating
// the original posts or claiming an unrecorded state: resolved posts
// carry their state and reason, everything else stays pending.
func (s *Store) feedPostResolutions(ctx context.Context, details map[string]FeedPostDetail) error {
	ids := make([]string, 0, len(details))
	for id := range details {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	// Post IDs are globally unique content digests and the details
	// only exist when their posts passed the repository-scoped read,
	// so matching IDs is exact without another repository filter.
	rows, err := s.db.QueryContext(ctx, `SELECT data->'resolution'->>'post',data->'resolution'->>'resolution',COALESCE(data->'resolution'->>'reason','') FROM factory_feed_posts WHERE data->'resolution'->>'post'=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, resolution, reason string
		if err := rows.Scan(&id, &resolution, &reason); err != nil {
			return err
		}
		if detail, ok := details[id]; ok && detail.Payload != nil && detail.Payload.Communication != nil {
			detail.Payload.Communication.Disposition = resolution
			detail.Payload.Communication.DispositionReason = reason
		}
	}
	return rows.Err()
}
