package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// PullEventHint is only a locator from an authenticated provider notification.
// It proves neither head state, current permission, source consent nor funding.
// A commit-only check notification uses Number=0 and an exact Head.
type PullEventHint struct {
	Delivery string
	Repository int64
	Number int64
	Head string
}

func (h PullEventHint) Validate() error {
	if checkIntakeDelivery(IntakeHint{Delivery:h.Delivery})!=nil || h.Repository<1 || h.Number<0 || h.Number==0 && h.Head=="" || h.Head!="" && !factory.ValidCommit(h.Head) { return errors.New("invalid external PR event locator") }
	return nil
}

// ObservePullEvent wakes only already-admitted external work. The original
// command owns its accepted issue, source, policy, placement and finite limits.
// The normal serving owner performs authenticated re-observation and dispatch;
// a private callback cannot launch desktop work or create an allowance.
func (c *Coordinator) ObservePullEvent(ctx context.Context,hint PullEventHint) (bool,error) {
	if c.Store==nil || hint.Validate()!=nil { return false,errors.New("external PR event is unavailable") }
	return c.Store.EnqueueExternalPullEvent(ctx,hint.Repository,hint.Number,hint.Head,hint.Delivery)
}
