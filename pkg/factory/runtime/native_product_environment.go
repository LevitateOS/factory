package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
)

// Native requests already originate from the admitted local profile owner.
// A collaboration account attributes the change; it does not make that owner
// an administrator of the remote repository or grant remote write permission.
func (b *nativeProductAccount) localOwnerAccess(ctx context.Context, actor collaboration.ActorID) error {
	if actor != b.binding.Actor.ID || b.binding.Profile.ID != b.port.operation.config.Profile {
		return factoryservice.ErrRuntimeGrantAccess
	}
	return b.current(ctx)
}

func (b *nativeProductAccount) localResourceAccess(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if err := b.localOwnerAccess(ctx, actor); err != nil {
		return err
	}
	return b.visible(ctx, actor, repository)
}

// Fountain controller authority is admitted through its host gateway. A local
// product connection cannot replace it with standalone placement or workspace
// grants, even when the desktop owner may read the repository.
func (b *nativeProductAccount) localExecutionSettingsAccess(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if b.binding.Host.Provider != collaboration.GitHub {
		return factoryservice.ErrPlacementUnavailable
	}
	return b.localResourceAccess(ctx, actor, repository)
}

func (b *nativeProductAccount) repositoryViewer(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (factoryservice.RepositoryViewer, error) {
	if err := b.localOwnerAccess(ctx, actor); err != nil {
		return factoryservice.RepositoryViewer{}, err
	}
	observed, err := b.repositoryObservation(ctx, actor, repository)
	if err != nil {
		return factoryservice.RepositoryViewer{}, err
	}
	return factoryservice.RepositoryViewer{
		LocalOwner: true, Operator: true, EnvironmentOwner: true, SessionCurrent: true,
		CodeWrite: observed.Push != nil && *observed.Push, OwnerOrAdmin: observed.Admin != nil && *observed.Admin,
	}, nil
}

func (b *nativeProductAccount) preparationStatus(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (*factoryservice.RepositoryPreparation, error) {
	if actor != b.binding.Actor.ID {
		return nil, factoryservice.ErrRepositoryHidden
	}
	setup, err := b.requestSetup(ctx, int64(repository))
	if err != nil || setup.Project == "" {
		return nil, err
	}
	return &factoryservice.RepositoryPreparation{Project: setup.Project, Ready: setup.State == factoryservice.RequestSetupReady}, nil
}

// This is recorded standing authority, not proof of a live admitted worker.
func (b *nativeProductAccount) recordedAuthority(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (factory.EffectiveAuthority, error) {
	if err := b.localResourceAccess(ctx, actor, repository); err != nil {
		return factory.EffectiveAuthority{}, err
	}
	in, err := b.store.FactoryAuthorityInput(ctx, int64(repository))
	if err != nil {
		return factory.EffectiveAuthority{}, err
	}
	return factory.EvaluateAuthority(in), nil
}

func (b *nativeProductAccount) nativeEnvironment(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, active bool) (*project.NativeEnvironment, error) {
	profile := b.port.operation.config.Profile
	runtime := collaboration.RuntimeID(profile)
	if !active {
		g, err := b.store.EnvironmentGrant(ctx, int64(repository))
		if err != nil {
			return nil, err
		}
		if g.Native == nil || g.Native.Profile != profile || g.Native.Runtime != runtime {
			return nil, factory.ErrPlacement
		}
		return g.Native, nil
	}
	p, err := b.store.Placement(ctx, repository)
	if err != nil {
		return nil, err
	}
	if p.RequireDispatch(b.binding.Host.ID, repository, runtime, p.Generation) != nil {
		return nil, factory.ErrPlacement
	}
	return &project.NativeEnvironment{Profile: profile, Runtime: runtime, Generation: p.Generation}, nil
}
