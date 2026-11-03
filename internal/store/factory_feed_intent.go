package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
)

// Publish the observed native revision in the same transaction. This is a
// projection of the owner's recorded choice, not another decision authority.
// Private requests without a repository remain in their native owner scope.
func recordIntentRevisionFeedTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision) error {
	if r.Repository <= 0 {
		return nil
	}
	var intake bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE repository=$1 AND data->>'intake_intent'=$2)`, r.Repository, r.Intent).Scan(&intake); err != nil {
		return err
	}
	if intake {
		return nil
	}
	label := map[string]string{"draft": "Design proposal recorded", "accepted": "Design accepted", "declined": "Design declined", "rejected": "Design rejected", "abandoned": "Design abandoned"}[r.State]
	if label == "" {
		return nil
	}
	decision := label
	if r.Approach != "" {
		decision += " · " + strings.ReplaceAll(r.Approach, "_", " ")
	}
	content := factory.FeedCommunication{
		ComponentID: "F11", Title: label, Summary: TruncateFeedSummary(r.Objective),
		Decision: decision, DecisionOwner: strconv.FormatInt(r.Owner, 10),
		Scope: "Build request", Rationale: "Recorded by the build request's native revision operation. This post preserves the choice and source; implementation and verification are separate records.",
		EvidenceRefs: []string{"intent:" + r.Intent, "revision:" + r.ID},
		IntentSource: &factory.FeedIntentSource{Intent: r.Intent, Revision: r.ID, Predecessor: r.Predecessor, State: r.State, Owner: r.Owner},
	}
	if r.Predecessor != "" {
		content.EvidenceRefs = append(content.EvidenceRefs, "previous_revision:"+r.Predecessor)
	}
	for _, idea := range r.Ideas {
		if len(content.Items) == 24 {
			break
		}
		label := []rune(idea.Description)
		if len(label) > 160 {
			label = label[:160]
		}
		content.Items = append(content.Items, factory.FeedCommunicationItem{ID: idea.ID, Label: string(label), State: idea.Decision})
	}
	return recordFactoryFeedEventTx(ctx, tx, r.Repository, 0, "", "intent-revision:"+r.ID, "intent:"+r.State, content, r.CreatedUnix)
}
