package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/levitateos/factory/pkg/factory"
)

// recordAssumptionChallengesTx records one F13 feed event per assumption
// challenge saved with an exploration review, in the review's transaction.
// Identity is the request and idea, so a replayed save or a later round that
// challenges the same idea again keeps the one card. Unbound requests have no
// repository feed and record nothing.
func recordAssumptionChallengesTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision, challenges []factory.AssumptionChallenge) error {
	if r.Repository <= 0 {
		return nil
	}
	for _, challenge := range challenges {
		content := factory.FeedCommunication{
			ComponentID: "F13",
			Title:       assumptionTitles["open"],
			Summary:     TruncateFeedSummary(challenge.Assumption),
			Scope:       "Build request",
			Assumption: &factory.FeedAssumption{
				Intent: r.Intent, Idea: challenge.Idea, Owner: r.Owner,
				Assumption: challenge.Assumption, Consequence: challenge.Consequence, Recommendation: challenge.Recommendation,
			},
		}
		if err := recordFactoryFeedEventTx(ctx, tx, r.Repository, 0, "", "assumption:"+r.Intent+":"+challenge.Idea, "assumption:open", content, r.CreatedUnix); err != nil {
			return err
		}
	}
	return nil
}

// feedAssumptionSQL is the one definition of an assumption challenge's served
// state for the post alias p, read from the owner's build request head: open
// while the head is a draft and the idea is pending, answered once the head
// records a decision, closed when the head no longer carries the question.
func feedAssumptionSQL(p string) string {
	ref := p + `.data->'communication'->'assumption'`
	return `COALESCE((SELECT jsonb_build_object(
 'state', CASE WHEN COALESCE(ax.idea->>'decision','pending')<>'pending' THEN 'answered'
  WHEN ax.idea IS NOT NULL AND ar.data->>'state'='draft' THEN 'open' ELSE 'closed' END,
 'decision', COALESCE(ax.idea->>'decision',''), 'correction', COALESCE(ax.idea->>'correction',''))
 FROM factory_intent_heads ah JOIN factory_intent_revisions ar ON ar.id=ah.revision AND ar.intent=ah.id
 LEFT JOIN LATERAL (SELECT ae AS idea FROM jsonb_array_elements(CASE WHEN jsonb_typeof(ar.data->'ideas')='array' THEN ar.data->'ideas' ELSE '[]'::jsonb END) ae
  WHERE ae->>'id'=` + ref + `->>'idea' LIMIT 1) ax ON true
 WHERE ah.id=` + ref + `->>'intent' AND ah.owner_id::text=` + ref + `->>'owner'),'{"state":"closed"}'::jsonb)`
}

// feedAssumptionOpenSQL selects open assumption challenges among Factory
// posts with alias p.
func feedAssumptionOpenSQL(p string) string {
	return `(` + p + `.author='factory' AND ` + p + `.data->'communication' ? 'assumption' AND (` + feedAssumptionSQL(p) + `)->>'state'='open')`
}

// assumptionTitles name a challenge card by its served state, so the headline
// and the Needs me rail follow the answer.
var assumptionTitles = map[string]string{
	"open":     "One consequential assumption still needs your decision",
	"answered": "Assumption answered",
	"closed":   "Assumption no longer open",
}

// feedAssumptionStates serves each challenge's state, decision, correction and
// title from the request head in one bounded read, and sets the event status
// the feed state derives from.
func (s *Store) feedAssumptionStates(ctx context.Context, details map[string]FeedPostDetail) error {
	ids := []string{}
	for id, detail := range details {
		if detail.Record.Author == "factory" && detail.Payload != nil && detail.Payload.Communication != nil && detail.Payload.Communication.Assumption != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,`+feedAssumptionSQL("p")+` FROM factory_feed_posts p WHERE p.id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var served struct {
			State      string `json:"state"`
			Decision   string `json:"decision"`
			Correction string `json:"correction"`
		}
		if err := json.Unmarshal(raw, &served); err != nil {
			return err
		}
		detail := details[id]
		assumption := detail.Payload.Communication.Assumption
		assumption.State = served.State
		if served.State == "answered" {
			assumption.Decision, assumption.Correction = served.Decision, served.Correction
		}
		detail.Payload.Communication.Title = assumptionTitles[served.State]
		detail.Status = "assumption:" + served.State
		details[id] = detail
	}
	return rows.Err()
}
