package runtime

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	factoryhttp "github.com/levitateos/factory/pkg/factory/httpapi"
	service "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// All work services share the request's current account and the application's
// coordinator. Reads remain available when execution or recovery is unavailable.
func (b *nativeProductAccount) bindWork(application service.Application) (factoryhttp.Work, factoryhttp.Runs) {
	c := application.Coordinator
	access := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
		if err := b.localResourceAccess(ctx, actor, repository); err != nil {
			return err
		}
		return b.writeAccess(ctx, actor, int64(repository))
	}
	authority := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (service.WorkAuthority, error) {
		if err := access(ctx, actor, repository); err != nil {
			reason := service.WorkAuthorityUnavailable
			if errors.Is(err, errNativeWriteDenied) {
				reason = service.WorkWriteRequired
			}
			return service.WorkAuthority{Reason: reason, SessionCurrent: b.current(ctx) == nil}, nil
		}
		return service.WorkAuthority{SessionCurrent: true}, nil
	}
	destination := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (execution.RecoveryTarget, error) {
		var empty execution.RecoveryTarget
		if err := access(ctx, actor, repository); err != nil {
			return empty, err
		}
		native, ok := c.Host.(NativeExecution)
		if !ok || native.Config.Profile != b.binding.Profile.ID {
			return empty, service.ErrInterventionUnavailable
		}
		var selected struct {
			Profile  collaboration.ProfileID `json:"profile"`
			Checkout string                  `json:"checkout_id"`
		}
		err := nativeHostCall(ctx, native.Config, native.Instance, "/api/native/execution", struct {
			Operation string `json:"operation"`
		}{"recovery_target"}, &selected)
		if err != nil || selected.Profile != b.binding.Profile.ID || !collaboration.ValidID(selected.Checkout) {
			return empty, service.ErrInterventionRecoveryTarget
		}
		return execution.RecoveryTarget{Native: &execution.NativeCheckoutRef{Profile: selected.Profile, CheckoutID: selected.Checkout}}, nil
	}
	membership := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, workspace string) (bool, error) {
		if err := b.localResourceAccess(ctx, actor, repository); err != nil {
			return false, err
		}
		enrolled, native, err := b.store.ExecutionWorkspace(ctx, repository)
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, factory.ErrPlacement) || errors.Is(err, store.ErrAdmissionChanged) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if native == nil || native.Profile != b.binding.Profile.ID || native.Runtime != collaboration.RuntimeID(b.binding.Profile.ID) || enrolled != workspace {
			return false, nil
		}
		return true, nil
	}
	repository := func(ctx context.Context, actor collaboration.ActorID, run factory.Run) (collaboration.RepositoryID, error) {
		if c.Host == nil {
			return 0, errors.Join(service.ErrInterventionUnavailable, service.ErrRunStatusUnavailable)
		}
		if b.repository == nil || run.Native == nil || run.Native.Provenance.Profile != b.binding.Profile.ID {
			return 0, service.ErrRunStatusScope
		}
		recorded, err := b.store.RecordedWorkspaceRepository(ctx, run.ProjectID, true)
		if err != nil {
			return 0, err
		}
		if recorded != b.repository.ID {
			return 0, service.ErrRunStatusScope
		}
		if err := b.visible(ctx, actor, recorded); err != nil {
			return 0, err
		}
		return recorded, nil
	}
	target := func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, run factory.Run) (execution.RecoveryTarget, error) {
		workspace, native, err := b.store.ExecutionWorkspace(ctx, repository)
		if err != nil || native == nil || native.Profile != b.binding.Profile.ID || native.Runtime != collaboration.RuntimeID(b.binding.Profile.ID) || workspace != run.ProjectID {
			return execution.RecoveryTarget{}, service.ErrInterventionRecoveryTarget
		}
		return destination(ctx, actor, repository)
	}
	inspect := func(ctx context.Context, actor collaboration.ActorID, run factory.Run) (execution.RunObservation, error) {
		if c.Host == nil {
			return execution.RunObservation{}, service.ErrRunStatusUnavailable
		}
		if _, err := repository(ctx, actor, run); err != nil {
			return execution.RunObservation{}, err
		}
		return c.Host.InspectRun(ctx, execution.RunAddress{Run: run.ID, Workspace: run.ProjectID})
	}
	issues := service.Issues{Store: b.store, Visibility: b.visible, ExecutionUnavailable: c.Host == nil, Authority: authority, Membership: membership,
		Recovery: func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, _ string) (bool, error) {
			_, err := destination(ctx, actor, repository)
			return err == nil, ctx.Err()
		},
		Status: func(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64) (control.AcceptanceValidity, error) {
			if err := b.visible(ctx, actor, repository); err != nil {
				return control.AcceptanceValidity{}, err
			}
			return c.AcceptanceStatus(ctx, int64(repository), strconv.FormatInt(issue, 10))
		}}
	commands := service.Interventions{Store: b.store, Writer: c, WriteAccess: access, RunRepository: repository, RecoveryTarget: target}
	return factoryhttp.Work{Reads: service.WorkReads{Store: b.store, Visibility: b.visible}, Issues: issues, VisibilityError: nativeProductResourceError},
		factoryhttp.Runs{Store: b.store, Visibility: b.visible, Status: service.RunStatus{Store: b.store, WriteAccess: access, RunRepository: repository, Inspect: inspect, Availability: c}, Commands: commands, VisibilityError: nativeProductResourceError, AuthorityError: nativeProductResourceError}
}
