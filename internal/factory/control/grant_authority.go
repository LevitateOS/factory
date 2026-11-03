package control

import (
	"context"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// EffectiveAuthority derives the visible verdict from the current grants,
// preparation readiness and dispatch state. Store owns the shared input
// collection so command transactions use the same source and projection.
func (c *Coordinator) EffectiveAuthority(ctx context.Context, repository int64) (factory.EffectiveAuthority, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	in, err := c.Store.FactoryAuthorityInput(bounded, repository)
	if err != nil {
		return factory.EffectiveAuthority{}, err
	}
	return factory.EvaluateAuthority(in), nil
}
