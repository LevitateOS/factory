package runtime

import (
	"context"
	"strconv"
	"strings"
	"unicode"

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

// Repository administration is the provider's admin observation, the same
// standing the access view reports as owner_or_admin. Missing evidence is
// unavailable, never a grant.
func nativeAdministered(observed NativeRepository, err error) error {
	if err != nil {
		return err
	}
	if observed.Admin == nil {
		return factoryservice.ErrRequestWriteUnavailable
	}
	if !*observed.Admin {
		return factoryservice.ErrPlacementAdminRequired
	}
	return nil
}

// administration observes the provider afresh for every check.
func (b *nativeProductAccount) administration(ctx context.Context, actor collaboration.ActorID, repository int64) error {
	return nativeAdministered(b.repositoryObservation(ctx, actor, collaboration.RepositoryID(repository)))
}

// readAdministration shares the request's selection for its first check and
// observes the provider afresh for later ones, including a writer's final check.
func (b *nativeProductAccount) readAdministration(view *nativeProductAccount) factoryservice.RequestWriteAccess {
	observe := b.readObservation(view)
	return func(ctx context.Context, actor collaboration.ActorID, repository int64) error {
		return nativeAdministered(observe(ctx, actor, collaboration.RepositoryID(repository)))
	}
}

// activityNavigation projects host-relative links in the provider's own path
// shape from the observed repository name. The desktop resolves them against
// its connection origin; malformed names produce no link.
func (b *nativeProductAccount) activityNavigation(view *nativeProductAccount) factoryservice.ActivityVisibility {
	observe := b.readObservation(view)
	pulls := "/pull/"
	if b.binding.Host.Provider != collaboration.GitHub {
		pulls = "/pulls/"
	}
	return func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (factoryservice.ActivityNavigation, error) {
		observed, err := observe(ctx, actor, repository)
		if err != nil {
			return factoryservice.ActivityNavigation{}, err
		}
		owner, name, _ := strings.Cut(observed.Name, "/")
		if !nativeLinkPart(owner) || !nativeLinkPart(name) {
			return factoryservice.ActivityNavigation{}, factoryservice.ErrActivityNavigation
		}
		path := "/" + owner + "/" + name
		return factoryservice.ActivityNavigation{RepositoryPath: path, ItemURL: func(item factory.ActivityItem) string {
			if !nativeLinkNumber(item.Issue) {
				return ""
			}
			switch item.Kind {
			case "issue_created", "verified_merge":
				return path + "/issues/" + item.Issue
			case "pr_created":
				if nativeLinkNumber(item.PR) {
					return path + pulls + item.PR
				}
			}
			return ""
		}}, nil
	}
}
func nativeLinkPart(part string) bool {
	return part != "" && part != "." && part != ".." && !strings.ContainsAny(part, "/\\%?#") &&
		!strings.ContainsFunc(part, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) })
}
func nativeLinkNumber(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
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
	native := &project.NativeEnvironment{Profile: profile, Runtime: runtime, Generation: p.Generation}
	if b.binding.Host.Provider == collaboration.Fountain {
		policy, err := b.store.RepositoryPolicy(ctx, int64(repository))
		if err != nil || policy.Execution == nil || b.policyBinding(ctx, actor, repository, *policy.Execution) != nil {
			return nil, factory.ErrPlacement
		}
		grant, err := b.controllerGrant(ctx, repository, policy.Execution.ControllerGrantID, "execution.launch")
		if err != nil || grant.Generation != p.Generation {
			return nil, factory.ErrPlacement
		}
		native.ControllerConnection, native.ControllerGrantID = policy.Execution.NativeConnection, grant.GrantID
	}
	return native, nil
}
