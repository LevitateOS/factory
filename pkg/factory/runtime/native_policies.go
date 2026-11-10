package runtime

import (
	"context"
	"slices"
	"time"

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
	if b.binding.Host.Provider == collaboration.Fountain {
		if _, err := b.controllerGrant(ctx, repository, ref.ControllerGrantID, ref.Kind); err != nil {
			return err
		}
	} else if b.binding.Host.Provider != collaboration.GitHub || ref.ControllerGrantID != "" || ref.Kind == "execution.launch" {
		return factoryservice.ErrPolicyBindingUnavailable
	}
	return b.current(ctx)
}

type nativeControllerGrant struct {
	Repository string                  `json:"repository"`
	Actor      string                  `json:"actor"`
	GrantID    string                  `json:"grant_id"`
	Runtime    collaboration.RuntimeID `json:"runtime"`
	Generation int64                   `json:"generation,string"`
	NotAfter   int64                   `json:"not_after"`
	Operations []string                `json:"operations"`
}

func (b *nativeProductAccount) controllerGrant(ctx context.Context, repository collaboration.RepositoryID, id, operation string) (nativeControllerGrant, error) {
	var grant nativeControllerGrant
	if b.binding.Host.Provider != collaboration.Fountain || b.repository == nil || repository != b.repository.ID || !factory.ValidID(id) {
		return grant, factoryservice.ErrPolicyBindingUnavailable
	}
	if err := b.read(ctx, nativeRead{Kind: "operation_grant", Repository: b.repository.ExternalID, GrantID: id}, &grant); err != nil {
		return grant, err
	}
	placement, err := b.store.Placement(ctx, repository)
	if err != nil {
		return grant, err
	}
	if grant.Repository != b.repository.ExternalID || grant.Actor != b.binding.Actor.ExternalID || grant.GrantID != id || grant.NotAfter <= time.Now().Unix() ||
		grant.Runtime != collaboration.RuntimeID(b.binding.Profile.ID) || !slices.Contains(grant.Operations, operation) ||
		placement.RequireDispatch(b.binding.Host.ID, repository, grant.Runtime, grant.Generation) != nil {
		return nativeControllerGrant{}, factoryservice.ErrPolicyBindingUnavailable
	}
	return grant, nil
}

// These are account references for configuration, not provider permissions.
func (b *nativeProductAccount) policyBindings(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) ([]factory.ActorBindingRef, error) {
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
		return nil, err
	}
	if b.binding.Host.Provider != collaboration.GitHub && b.binding.Host.Provider != collaboration.Fountain {
		return []factory.ActorBindingRef{}, nil
	}
	var refs []factory.ActorBindingRef
	kinds := []string{factory.OpRefPublish, factory.OpPRCreate, factory.OpReviewSubmit, factory.OpThreadResolve, factory.OpMerge}
	if b.binding.Host.Provider == collaboration.Fountain {
		kinds = append(kinds, "execution.launch")
	}
	for _, kind := range kinds {
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
	options := &factoryservice.RepositoryPolicyOptions{MergeMethods: []string{}, NativeSetup: b.binding.Host.Provider == collaboration.GitHub, ControllerGrantRequired: b.binding.Host.Provider == collaboration.Fountain}
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
