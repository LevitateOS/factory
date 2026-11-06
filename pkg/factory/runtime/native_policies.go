package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// A policy reference can name only the account actually verified for this
// native operation. Saved registry IDs and browser fields confer no authority.
// Background publication must separately re-observe its selected account.
func (b *nativeProductAccount) policyBinding(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, ref factory.ActorBindingRef) error {
	if actor != b.binding.Actor.ID || b.repository == nil || repository != b.repository.ID || ref.Validate() != nil ||
		ref.TokenID != 0 || ref.NativeProfile != b.binding.Profile.ID || ref.NativeConnection != b.binding.Connection.ID || ref.ActorID != int64(b.binding.Actor.ID) {
		return factoryservice.ErrPolicyBindingUnavailable
	}
	return b.current(ctx)
}
