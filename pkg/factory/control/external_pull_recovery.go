package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// The authenticated connector supplies locators only. Accepted input, current
// policy and funding are admitted through the existing external command owner.
type AutomaticExternalPullPage struct {
	Selections []AutomaticExternalPullSelection
	Next       string
	Complete   bool
}

// Discovery already observes the exact provider PR and head. These facts allow
// a consumed command to be skipped before repeating full acceptance reads;
// they grant no authority to adopt or launch an unseen candidate.
type AutomaticExternalPullSelection struct {
	factory.ExternalPullSelection
	PRID int64
	Head string
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
func (c *Coordinator) recoverExternalPulls(ctx context.Context, repository int64, policy factory.RepositoryPolicy) (result error) {
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
	defer func() {
		if result != nil && !errors.Is(result, ErrReadinessPassPending) {
			poll.notBefore = time.Now().Unix() + idleCreationPollSeconds
			if c.externalPullPolls == nil {
				c.externalPullPolls = map[int64]externalPullPoll{}
			}
			c.externalPullPolls[repository] = poll
		}
	}()
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
		if selection.PRID < 1 || !factory.ValidCommit(selection.Head) {
			return store.ErrAdmissionChanged
		}
		digest := sha256.Sum256([]byte("automatic-external-pr\x00" + strconv.FormatInt(repository, 10) + "\x00" + strconv.FormatInt(selection.PRID, 10) + "\x00" + selection.Head))
		command := hex.EncodeToString(digest[:16])
		if prior, err := c.Store.FactoryCommand(ctx, command); err == nil {
			var captured factory.ExternalPullAdoption
			if prior.Validate() != nil || prior.Type != factory.CommandAdoptExternalPull || prior.Target != factory.ExternalPullTarget(repository, selection.Number) || prior.Principal != "automatic-external-pr" ||
				json.Unmarshal([]byte(prior.Payload), &captured) != nil || captured.Validate() != nil || !captured.Automatic || captured.Repository != repository || captured.Issue != selection.Issue || captured.PRNumber != selection.Number || captured.PRID != selection.PRID || captured.Target.HeadOID != selection.Head {
				return store.ErrCommandConflict
			}
			// The original command's assignment/publication recovery owns all
			// remaining work. Discovery must not renew its source or allowance.
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		request, err := c.ExternalPullOffer(ctx, repository, selection.ExternalPullSelection)
		if err != nil {
			continue
		}
		request.Automatic = true
		if request.Policy != policy.Revision || request.ForkConsent || request.Validate() != nil || request.PRID != selection.PRID || request.Target.HeadOID != selection.Head {
			continue
		}
		if err = reader.CheckAutomaticExternalPull(ctx, request); err != nil {
			continue
		}
		if err = c.Store.ExternalPullOriginAvailable(ctx, request); err != nil {
			continue
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
