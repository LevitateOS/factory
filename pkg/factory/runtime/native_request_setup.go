package runtime

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// requestSetup reports recorded native setup without provisioning or dispatch.
// Readiness needs current approved preparations for both pipeline roles; it is
// not execution, spending or provider admission.
func (b *nativeProductAccount) requestSetup(ctx context.Context, repository int64) (factoryservice.RequestSetup, error) {
	if err := b.visible(ctx, b.binding.Actor.ID, collaboration.RepositoryID(repository)); err != nil {
		return factoryservice.RequestSetup{}, err
	}
	return (NativeExecution{Config: b.port.operation.config, Instance: b.port.operation.instance, Store: b.store}).RequestSetup(ctx, repository)
}

// RequestSetup reads the native runtime's recorded setup. Product admission and
// request ownership remain with the caller; this observation grants no work.
func (p NativeExecution) RequestSetup(ctx context.Context, repository int64) (factoryservice.RequestSetup, error) {
	var empty factoryservice.RequestSetup
	if p.Store == nil || repository <= 0 || !collaboration.ValidID(string(p.Config.Profile)) || !collaboration.ValidID(p.Instance) {
		return empty, factoryservice.ErrRequestSetupUnavailable
	}
	in, err := p.Store.FactoryAuthorityInput(ctx, repository)
	if err != nil {
		return empty, err
	}
	result := factoryservice.RequestSetup{State: factoryservice.RequestSetupMissing}
	if in.Environment == nil || !in.ProjectExists {
		return result, nil
	}
	native := in.Environment.Native
	if native == nil || native.Profile != p.Config.Profile || native.Runtime != collaboration.RuntimeID(native.Profile) {
		result.State = factoryservice.RequestSetupUnavailable
		return result, nil
	}
	result.Project, result.State = in.Environment.Project, factoryservice.RequestSetupPreparing
	if !in.Environment.Active || !in.PreparationReady {
		return result, nil
	}
	preparations, err := p.Store.StandingProjectPreparations(ctx, result.Project)
	if err != nil {
		return empty, err
	}
	ready := make(map[string]bool)
	for _, preparation := range preparations {
		if preparation.Native == nil || preparation.Native.Profile != native.Profile ||
			preparation.Native.InstanceID != p.Instance || !preparation.State.Ready || preparation.State.Stopped {
			continue
		}
		if err := p.Store.AuthorizeStandingPreparation(ctx, preparation); err != nil {
			if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrAdmissionChanged) || errors.Is(err, store.ErrCandidatePreparationConflict) {
				continue
			}
			return empty, err
		}
		ready[preparation.Preparation.Role] = true
	}
	if ready[project.RoleCoder] && ready[project.RoleReviewer] {
		result.State = factoryservice.RequestSetupReady
	}
	return result, nil
}
