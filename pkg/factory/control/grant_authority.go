package control

import (
	"context"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// EffectiveAuthority derives the visible verdict from the current grants,
// preparation readiness and dispatch state. Store owns the shared input
// collection so command transactions use the same source and projection.
func (c *Coordinator) EffectiveAuthority(ctx context.Context, repository int64) (factory.EffectiveAuthority, error) {
	return c.EffectiveRoleAuthority(ctx, repository, project.RoleCoder)
}

func (c *Coordinator) EffectiveRoleAuthority(ctx context.Context, repository int64, role string) (factory.EffectiveAuthority, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	in, err := c.Store.FactoryRoleAuthorityInput(bounded, repository, role)
	if err != nil {
		return factory.EffectiveAuthority{}, err
	}
	return factory.EvaluateAuthority(in), nil
}
