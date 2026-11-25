package store

import (
	"context"
	"database/sql"

	"github.com/levitateos/factory/pkg/factory"
)

// feedRequestMetSQL is the one definition of an agent request the recorded
// settings now satisfy, for the post alias p, from the repository's current
// setting rows only (no provider read): capacity reaching the amount, an
// active sponsorship (its use is not capped),
// an active operator or environment grant, or an enabled, unpaused policy.
// An ask without a request is never met.
func feedRequestMetSQL(p string) string {
	r := p + `.data->'communication'->'request'`
	active := func(alias string) string { return `COALESCE((` + alias + `.data->>'active')::boolean,false)` }
	return `(CASE ` + r + `->>'setting'
 WHEN '` + factory.SettingCapacity + `' THEN EXISTS(SELECT 1 FROM factory_capacity rc WHERE (rc.data->>'max_concurrent_runs')::int >= (` + r + `->>'amount')::int)
 WHEN '` + factory.SettingSponsorships + `' THEN EXISTS(SELECT 1 FROM factory_sponsorships rs WHERE rs.repository=` + p + `.repository AND ` + active("rs") + `)
 WHEN '` + factory.SettingOperatorGrant + `' THEN EXISTS(SELECT 1 FROM factory_operator_grants ro WHERE ro.repository=` + p + `.repository AND ` + active("ro") + `)
 WHEN '` + factory.SettingEnvironmentGrant + `' THEN EXISTS(SELECT 1 FROM project_environment_grants re WHERE re.repository=` + p + `.repository AND ` + active("re") + `)
 WHEN '` + factory.SettingPolicy + `' THEN EXISTS(SELECT 1 FROM factory_policies rp WHERE rp.repository=` + p + `.repository
  AND COALESCE((rp.data->>'enabled')::boolean,false) AND NOT COALESCE((rp.data->>'paused')::boolean,false))
 ELSE false END)`
}

// feedRequestStates serves each request's state on its ask: met when the
// recorded setting satisfies it, otherwise open while the ask has no reply
// (feedQuestionStates already derived that) and replied after one.
func (s *Store) feedRequestStates(ctx context.Context, details map[string]FeedPostDetail) error {
	ids := []string{}
	for id, detail := range details {
		if detail.Payload != nil && detail.Payload.Communication != nil && detail.Payload.Communication.Request != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,`+feedRequestMetSQL("p")+` FROM factory_feed_posts p WHERE p.id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var met sql.NullBool
		if err := rows.Scan(&id, &met); err != nil {
			return err
		}
		c := details[id].Payload.Communication
		switch {
		case met.Bool:
			c.Request.State = "met"
		case c.QuestionState == "open":
			c.Request.State = "open"
		default:
			c.Request.State = "replied"
		}
	}
	return rows.Err()
}
