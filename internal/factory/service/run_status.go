package service

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// RunAvailability retains the coordinator's current retry/repair admission
// reader. It cannot launch or retire work and supplies no replacement journal.
type RunAvailability interface {
	DeliveryRetryAvailable(context.Context, string) (bool, error)
	DeliveryRepairAvailable(context.Context, string) (bool, error)
	EffectiveAuthority(context.Context, int64) (factory.EffectiveAuthority, error)
}

type RunStatus struct {
	Store         *store.Store
	WriteAccess   WorkVisibility
	RunRepository func(context.Context, collaboration.ActorID, factory.Run) (collaboration.RepositoryID, error)
	Inspect       func(context.Context, collaboration.ActorID, factory.Run) (execution.RunObservation, error)
	Availability  RunAvailability
}

var (
	ErrRunStatusUnavailable = errors.New("run status runtime is unavailable")
	ErrRunStatusAccess      = errors.New("current run execution authority is required")
	ErrRunStatusScope       = errors.New("run display binding differs from its runtime repository")
	ErrRunStatusContext     = errors.New("saved run context is unavailable")
	ErrRunStatusObservation = errors.New("run observation is unavailable")
	ErrRunStatusAuthority   = errors.New("current run authority is unavailable")
)

func (s RunStatus) Read(ctx context.Context, actor collaboration.ActorID, runID string) (*RunStatusView, error) {
	if actor < 1 || !factory.ValidID(runID) {
		return nil, factory.ErrNotFound
	}
	if s.Store == nil || s.WriteAccess == nil || s.RunRepository == nil || s.Inspect == nil {
		return nil, ErrRunStatusUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := s.Store.FactoryRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, factory.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if run.ID != runID {
		return nil, ErrRunStatusScope
	}
	repository, err := s.RunRepository(ctx, actor, run)
	if err != nil {
		return nil, err
	}
	if repository < 1 {
		return nil, ErrRunStatusUnavailable
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.WriteAccess(ctx, actor, repository); err != nil {
			return errors.Join(ErrRunStatusAccess, err)
		}
		return ctx.Err()
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	status := &RunStatusView{Run: factoryRunRecordDTO(run)}
	if run.ContextPending && !run.Reconciled && run.Outcome == "" {
		status.ContextSelection, err = control.ContextSelectionStatus(ctx, s.Store, run)
		if err != nil {
			return nil, errors.Join(ErrRunStatusContext, err)
		}
	}
	if binding, err := s.Store.FactoryRunView(ctx, runID); err == nil {
		if binding.RunID != runID || binding.Repository != int64(repository) {
			return nil, ErrRunStatusScope
		}
		status.View = factoryRunBindingDTO(binding)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	observation, err := s.Inspect(ctx, actor, run)
	if err == nil {
		if observation.ValidateFor(execution.RunAddress{Run: runID, Workspace: run.ProjectID}) != nil || observation.Kind != run.RuntimeKind() {
			return nil, ErrRunStatusObservation
		}
		if run.Native != nil {
			next := *run.Native
			next.Provenance, next.Retirement = *observation.Native, observation.Retirement
			if !next.Advances(*run.Native) {
				return nil, errors.Join(ErrRunStatusObservation, execution.ErrRunChanged)
			}
		}
		status.State = factoryRunLiveDTO(observation)
	} else if !errors.Is(err, execution.ErrRunNotObserved) {
		return nil, errors.Join(ErrRunStatusObservation, err)
	}
	if status.View != nil && status.View.Issue == "" {
		if err := s.delivery(ctx, actor, repository, run, status); err != nil {
			return nil, err
		}
	}
	if err := authorize(); err != nil {
		return nil, err
	}
	return status, nil
}

func (s RunStatus) delivery(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, run factory.Run, status *RunStatusView) error {
	assignment, err := s.Store.AssignmentByRun(ctx, run.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if assignment.ID != status.View.Attempt || assignment.Run != run.ID || assignment.ProjectID != run.ProjectID || assignment.Repository != int64(repository) || assignment.Issue != 0 || assignment.Phase != project.ExecutionVerify || assignment.Role != project.RoleReviewer || assignment.Subject.Kind != factory.SubjectDelivery || assignment.Subject.Delivery == nil {
		return nil
	}
	if s.Availability == nil {
		return ErrRunStatusUnavailable
	}
	available, err := s.Availability.DeliveryRetryAvailable(ctx, run.ID)
	if err != nil {
		return err
	}
	if available {
		effective, err := s.Availability.EffectiveAuthority(ctx, int64(repository))
		if err != nil {
			return errors.Join(ErrRunStatusAuthority, err)
		}
		available = effective.Effective || len(effective.Missing) == 1 && effective.Missing[0] == factory.MissingDispatch
	}
	settled := run.Reconciled && (status.State == nil || !status.State.Live && status.State.Terminal)
	status.VerificationRetry = &WorkCapability{Available: available && settled}
	status.DeliveryRepair = &factoryDeliveryRepairView{}
	if _, err := s.Store.IntentRevision(ctx, assignment.Subject.Delivery.IntentID, int64(actor)); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	record, err := s.Store.DeliveryRepairRecord(ctx, int64(repository), run.ID)
	if err == nil {
		status.DeliveryRepair.Progress, err = deliveryRepairProgressDTO(record, assignment)
	} else if errors.Is(err, store.ErrNotFound) {
		status.DeliveryRepair.Available, err = s.Availability.DeliveryRepairAvailable(ctx, run.ID)
		status.DeliveryRepair.Available = status.DeliveryRepair.Available && settled
	}
	return err
}
