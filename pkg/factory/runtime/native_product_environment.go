package runtime

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
)

func (b *nativeProductAccount) adminAccess(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	observed, err := b.repositoryObservation(ctx, actor, repository)
	if err != nil {
		return err
	}
	if observed.Admin == nil {
		return factoryservice.ErrPlacementUnavailable
	}
	if !*observed.Admin {
		return factoryservice.ErrPlacementAdminRequired
	}
	return ctx.Err()
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
