package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// ErrIneffectiveAuthority reports a dispatch request the current grants do
// not authorize. The caller restores the missing grant instead of retrying.
var ErrIneffectiveAuthority = errors.New("factory grants do not authorize dispatch")

// ApplyPolicy records the owner's repository policy. Disabling or pausing
// closes dispatch. Each record
// carries its expected revision; the store's revision-guarded save refuses
// a stale one.
func (c *Coordinator) ApplyPolicy(ctx context.Context, principal string, policy factory.RepositoryPolicy) (factory.GrantResult, error) {
	if policy.Repository <= 0 {
		return factory.GrantResult{}, store.ErrStaleRevision
	}
	return c.applyGrant(ctx, principal, policy, policy.Repository)
}

// ApplyCapacity records the appliance capacity. Capacity bounds future
// reservations only; it never closes already registered dispatch.
func (c *Coordinator) ApplyCapacity(ctx context.Context, principal string, capacity factory.Capacity) (factory.GrantResult, error) {
	return c.applyGrant(ctx, principal, capacity, 0)
}

// ApplyOperatorGrant records the operator's repository permission.
// Withdrawing it closes dispatch for that repository.
func (c *Coordinator) ApplyOperatorGrant(ctx context.Context, principal string, grant factory.OperatorGrant) (factory.GrantResult, error) {
	if grant.Repository <= 0 {
		return factory.GrantResult{}, store.ErrStaleRevision
	}
	return c.applyGrant(ctx, principal, grant, grant.Repository)
}

// ApplySponsorship records one connection sponsorship. Withdrawing the last
// active sponsorship closes dispatch; other active sponsorships keep it open.
func (c *Coordinator) ApplySponsorship(ctx context.Context, principal string, sponsorship factory.Sponsorship) (factory.GrantResult, error) {
	if sponsorship.Repository <= 0 {
		return factory.GrantResult{}, store.ErrStaleRevision
	}
	return c.applyGrant(ctx, principal, sponsorship, sponsorship.Repository)
}

// ApplyEnvironmentGrant records the owner's standing environment permission.
// Withdrawing it closes dispatch for that repository.
func (c *Coordinator) ApplyEnvironmentGrant(ctx context.Context, principal string, grant project.EnvironmentGrant) (factory.GrantResult, error) {
	if grant.Repository <= 0 {
		return factory.GrantResult{}, store.ErrStaleRevision
	}
	return c.applyGrant(ctx, principal, grant, grant.Repository)
}

// applyGrant saves the grant and local withdrawal atomically, then asks the
// native publication and merge owners to carry out requested cancellation.
func (c *Coordinator) applyGrant(ctx context.Context, principal string, record any, repository int64) (factory.GrantResult, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	result, err := c.Store.ApplyFactoryGrant(bounded, principal, record)
	if err != nil {
		return factory.GrantResult{}, err
	}
	if repository > 0 && result.Withdrawn {
		c.cancelRepositoryPublications(bounded, repository)
		c.cancelRepositoryMerges(bounded, repository)
	}
	return result, nil
}
