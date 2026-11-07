package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/store"
)

// RecoverIntentGraphs resumes at most one accepted native graph per serving
// pass. The durable packet/receipt and existing task bindings survive restart;
// a fair cursor prevents one unavailable native repository starving another.
// Recovery never mints fresh operations: a head without a generation row has
// nothing to resume (creation belongs to the recorded-plan serving
// connector). Recovery validates the original binding and never attaches
// a new plan to an existing operation.
func (c *Coordinator) RecoverIntentGraphs(ctx context.Context) error {
	if !c.graphAvailable() {
		return nil
	}
	revisions, err := c.Store.PendingIntentGenerations(ctx, c.intentGenerationCursor, 1)
	if err != nil {
		return err
	}
	if len(revisions) == 0 {
		c.intentGenerationCursor = ""
		revisions, err = c.Store.PendingIntentGenerations(ctx, "", 1)
		if err != nil || len(revisions) == 0 {
			return err
		}
	}
	revision := revisions[0]
	c.intentGenerationCursor = revision.ID
	_, err = c.Store.IntentGeneration(ctx, revision.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = c.GenerateAcceptedIntent(ctx, revision.Intent, revision.ID, revision.Owner)
	if errors.Is(err, ErrGraphStepsPending) {
		return nil
	}
	return err
}
