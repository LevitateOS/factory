package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
)

// ErrPostRouteStarted refuses a re-route once the routed work has begun: an
// issue is being written, feedback was delivered or Issue Maker took the post.
var ErrPostRouteStarted = errors.New("this post's work has already started")

// postReroutable is the one rule for "Wrong call? Choose where it goes": JEV
// has decided, and nothing has acted on that decision yet.
func postReroutable(judgment, draft, effect string, issueMaker bool, routing *factory.PostRouting) bool {
	return judgment == factory.PostJudgmentApplied && draft == factory.PostDraftPending && effect == factory.PostEffectPending && !issueMaker && routing != nil
}

// PostReroutable reports whether the author may still choose where a post goes.
func (s *Store) PostReroutable(ctx context.Context, id string) (bool, error) {
	var judgment, draft, effect string
	var revision int64
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT judgment,draft,effect,revision,COALESCE(data->'routing','null'::jsonb) FROM factory_post_intakes WHERE post=$1`, id).Scan(&judgment, &draft, &effect, &revision, &raw)
	if err != nil {
		return false, err
	}
	var routing *factory.PostRouting
	if json.Unmarshal(raw, &routing) != nil {
		return false, nil
	}
	var maker bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE id=$1)`, factory.IssueMakerPostID(id, revision)).Scan(&maker); err != nil {
		return false, err
	}
	return postReroutable(judgment, draft, effect, maker, routing), nil
}

// ReroutePost replaces JEV's routing decision with the author's choice, one of
// the options JEV weighed other than asking a question back. The choice is kept
// on the routing record, and the post moves on from there like any route.
func (s *Store) ReroutePost(ctx context.Context, id, author string, expected int64, choice string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var savedAuthor string
	var revision int64
	var closed bool
	if err := tx.QueryRowContext(ctx, `SELECT author,revision,COALESCE((data->>'deleted')::boolean,false) OR COALESCE((data->>'revoked')::boolean,false) OR data ? 'product_change'
		FROM factory_feed_posts WHERE id=$1 FOR UPDATE`, id).Scan(&savedAuthor, &revision, &closed); err != nil {
		return err
	}
	if savedAuthor != author || revision != expected || closed {
		return ErrStaleRevision
	}
	var judgment, draft, effect string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT judgment,draft,effect,COALESCE(data->'routing','null'::jsonb) FROM factory_post_intakes WHERE post=$1 AND revision=$2 FOR UPDATE`, id, revision).Scan(&judgment, &draft, &effect, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleRevision
		}
		return err
	}
	var routing *factory.PostRouting
	if err := json.Unmarshal(raw, &routing); err != nil {
		return err
	}
	var maker bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE id=$1)`, factory.IssueMakerPostID(id, revision)).Scan(&maker); err != nil {
		return err
	}
	if !postReroutable(judgment, draft, effect, maker, routing) {
		return ErrPostRouteStarted
	}
	if strings.HasPrefix(choice, "clarify_") || !slices.ContainsFunc(routing.Options, func(o factory.PostRoutingOption) bool { return o.Key == choice }) {
		return errors.New("choose one of the options JEV weighed")
	}
	route, issue := choice, int64(0)
	if number, ok := strings.CutPrefix(choice, "existing_"); ok {
		route = factory.PostRouteExistingWork
		if issue, err = strconv.ParseInt(number, 10, 64); err != nil || issue <= 0 {
			return errors.New("choose one of the options JEV weighed")
		}
	} else if choice != factory.PostRouteNewIssue && choice != factory.PostRouteDiscussion {
		return errors.New("choose one of the options JEV weighed")
	}
	routing.Chosen = choice
	routing.Unsure = false
	body, err := json.Marshal(routing)
	if err != nil {
		return err
	}
	// The question JEV asked back (when it was unsure) no longer applies.
	if _, err := tx.ExecContext(ctx, `UPDATE factory_post_intakes SET route=$2, issue=$3,
		data=(data - 'detail' - 'stalled') || jsonb_build_object('routing',$4::jsonb) WHERE post=$1`, id, route, issue, string(body)); err != nil {
		return err
	}
	return tx.Commit()
}
