package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// The authenticated connector supplies locators only. Accepted input, current
// policy and funding are admitted through the existing external command owner.
type AutomaticExternalPullPage struct {
	Selections []factory.ExternalPullSelection
	Next       string
	Complete   bool
}

type automaticExternalPullSource interface {
	ReadAutomaticExternalPulls(context.Context, int64, string) (AutomaticExternalPullPage, error)
	CheckAutomaticExternalPull(context.Context, factory.ExternalPullAdoption) error
}

type externalPullPoll struct {
	cursor            string
	policy, notBefore int64
}

// Pagination is scheduling metadata. On restart it starts at the beginning;
// retained commands, assignments and the dispatch gate own durable dedupe.
func (c *Coordinator) recoverExternalPulls(ctx context.Context, repository int64, policy factory.RepositoryPolicy) error {
	reader, ok := c.Reviews.(automaticExternalPullSource)
	if !ok || !policy.Enabled || policy.Paused || !policy.AutomaticExternalPulls {
		return nil
	}
	poll := c.externalPullPolls[repository]
	if poll.policy != policy.Revision {
		poll = externalPullPoll{policy: policy.Revision}
	}
	now := time.Now().Unix()
	if poll.notBefore > now && poll.notBefore <= now+idleCreationPollSeconds {
		return nil
	}
	if !readinessBudgetFrom(ctx).reserveNativeRPC(1) {
		return ErrReadinessPassPending
	}
	page, err := reader.ReadAutomaticExternalPulls(ctx, repository, poll.cursor)
	if err != nil {
		return err
	}
	if len(page.Selections) > creationRecoveryPageSize || page.Complete && page.Next != "" || !page.Complete && (page.Next == "" || page.Next == poll.cursor) {
		return store.ErrAdmissionChanged
	}
	for _, selection := range page.Selections {
		request, err := c.ExternalPullOffer(ctx, repository, selection)
		if err != nil {
			continue
		}
		request.Automatic = true
		if request.Policy != policy.Revision || request.ForkConsent || request.Validate() != nil {
			continue
		}
		if err = reader.CheckAutomaticExternalPull(ctx, request); err != nil {
			continue
		}
		if err = c.Store.ExternalPullOriginAvailable(ctx, request); err != nil {
			continue
		}
		digest := sha256.Sum256([]byte("automatic-external-pr\x00" + strconv.FormatInt(repository, 10) + "\x00" + strconv.FormatInt(request.PRID, 10) + "\x00" + request.Target.HeadOID))
		command := hex.EncodeToString(digest[:16])
		if _, err = c.Store.FactoryCommand(ctx, command); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		// The command admission rechecks policy after all authenticated I/O and
		// records the exact source and finite allowance in its normal transaction.
		if _, err = c.AdoptExternalPull(ctx, command, "automatic-external-pr", request); err == nil {
			_, _ = c.Store.EnqueueExternalPullEvent(ctx, repository, request.PRNumber, request.Target.HeadOID, command)
		}
	}
	current, err := c.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return err
	}
	if current.Revision != policy.Revision || !current.Enabled || current.Paused || !current.AutomaticExternalPulls {
		return store.ErrAdmissionChanged
	}
	poll.cursor, poll.notBefore = page.Next, 0
	if page.Complete {
		poll.notBefore = time.Now().Unix() + idleCreationPollSeconds
	}
	if c.externalPullPolls == nil {
		c.externalPullPolls = map[int64]externalPullPoll{}
	}
	c.externalPullPolls[repository] = poll
	return nil
}
