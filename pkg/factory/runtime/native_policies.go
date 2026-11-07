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

// These are account references for configuration, not provider permissions.
func (b *nativeProductAccount) policyBindings(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) ([]factory.ActorBindingRef, error) {
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
		return nil, err
	}
	var refs []factory.ActorBindingRef
	for _, kind := range []string{factory.OpRefPublish, factory.OpPRCreate, factory.OpReviewSubmit, factory.OpThreadResolve, factory.OpMerge} {
		refs = append(refs, factory.ActorBindingRef{Kind: kind, ActorID: int64(actor),
			NativeProfile: b.binding.Profile.ID, NativeConnection: b.binding.Connection.ID})
	}
	return refs, nil
}

func (b *nativeProductAccount) policyOptions(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (*factoryservice.RepositoryPolicyOptions, error) {
	observed, err := b.repositoryObservation(ctx, actor, repository)
	if err != nil {
		return nil, err
	}
	options := &factoryservice.RepositoryPolicyOptions{MergeMethods: []string{}, NativeSetup: true}
	if branch := "refs/heads/" + observed.DefaultBranch; factory.ValidTargetBranch(branch) {
		options.TargetBranch = branch
	}
	for _, method := range []string{"merge", "squash", "rebase"} {
		if enabled := observed.MergeMethods[method]; enabled != nil && *enabled {
			options.MergeMethods = append(options.MergeMethods, method)
		}
	}
	return options, nil
}
