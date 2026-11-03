package control

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// ErrPreparationIntervention preserves a failed or retired identity. The
// administrator must inspect its effects and choose a new preparation identity.
var ErrPreparationIntervention = errors.New("preparation requires administrator intervention and a new identity")

// StartPreparation admits the exact human-selected standing setup before any
// native effect. authorize checks the current caller and both recorded approvers;
// it grants no provider lease or privileged installation capability.
func (c *Coordinator) StartPreparation(ctx context.Context, request execution.StandingPreparationRequest, authorize func(context.Context) error) (execution.PreparationRecord, error) {
	in := request.Input
	if err := request.Validate(); err != nil {
		return execution.PreparationRecord{}, err
	}
	setupFiles := make([]string, 0, len(in.Setup.Files))
	for name := range in.Setup.Files {
		setupFiles = append(setupFiles, name)
	}
	if project.PreparationInputsDigestOf(setupFiles, in.Preparation.Requirements.SourceInputs, in.Preparation.Tools) != in.Preparation.Requirements.Digest {
		return execution.PreparationRecord{}, store.ErrCandidatePreparationConflict
	}
	if authorize == nil || c.Store == nil || c.Host == nil {
		return execution.PreparationRecord{}, errors.New("standing preparation authority is unavailable")
	}
	deadline := time.Now().Add(30 * time.Second)
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := authorize(bounded); err != nil {
		return execution.PreparationRecord{}, err
	}
	stored, _, err := c.Store.AdmitStandingPreparation(bounded, execution.PreparationRecord{Preparation: in.Preparation, Native: request.Native})
	if err != nil {
		return execution.PreparationRecord{}, err
	}
	if preparationNeedsIntervention(stored.State) {
		return stored, ErrPreparationIntervention
	}
	inspectCtx, stopInspect := context.WithTimeout(bounded, 5*time.Second)
	state, inspectErr := c.Host.InspectWorkspacePreparation(inspectCtx, stored.Address())
	stopInspect()
	if inspectErr == nil {
		stored, err = c.adoptStandingPreparation(bounded, stored, state)
		if err != nil || state.Phase == project.PrepareRunning || state.Phase == project.PrepareReady {
			return stored, err
		}
	} else if !errors.Is(inspectErr, execution.ErrPreparationNotObserved) {
		return stored, inspectErr
	}
	// Missing can start; an approved or waiting identity can resume after its
	// prerequisite is repaired. Recheck authority before the identical request.
	if err := authorize(bounded); err != nil {
		return stored, err
	}
	if err := c.Store.AuthorizeStandingPreparation(bounded, execution.PreparationRecord{Preparation: in.Preparation, Native: request.Native}); err != nil {
		return stored, err
	}
	callCtx, stopCall := context.WithDeadline(bounded, deadline.Add(-5*time.Second))
	state, err = c.Host.PrepareWorkspace(callCtx, request)
	stopCall()
	if err == nil {
		return c.adoptStandingPreparation(bounded, stored, state)
	}
	// The native worker outlives an HTTP observation. A lost or canceled reply
	// neither removes admission nor authorizes another worker identity.
	recoveryCtx, stopRecovery := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer stopRecovery()
	recoveryCtx, stopRecoveryInspect := context.WithTimeout(recoveryCtx, 5*time.Second)
	defer stopRecoveryInspect()
	state, inspectErr = c.Host.InspectWorkspacePreparation(recoveryCtx, stored.Address())
	if inspectErr != nil {
		return stored, errors.Join(err, inspectErr)
	}
	return c.adoptStandingPreparation(recoveryCtx, stored, state)
}

func (c *Coordinator) adoptStandingPreparation(ctx context.Context, stored execution.PreparationRecord, state execution.PreparationState) (execution.PreparationRecord, error) {
	if !candidatePreparationIdentityMatches(state, stored.Preparation) || state.ValidateFor(stored.Address()) != nil ||
		state.Ready != (state.Phase == project.PrepareReady) || state.Ready && state.Stopped {
		return stored, errors.New("native standing preparation identity was not confirmed")
	}
	observed := stored
	observed.State = state
	// Keep the revision read before the native observation. Reloading it here
	// would let a late Ready reply overwrite a newer terminal observation.
	if err := c.Store.ObservePreparation(ctx, observed); err != nil {
		current, readErr := c.Store.Preparation(ctx, stored.Preparation.ID)
		if readErr != nil {
			return execution.PreparationRecord{}, errors.Join(err, readErr)
		}
		// The serving observer may have confirmed this exact reply first.
		// Adopt that durable result without replacing any newer observation.
		if current.Preparation.Revision > stored.Preparation.Revision && !preparationNeedsIntervention(current.State) &&
			sameStandingPreparationObservation(current, observed) {
			return current, nil
		}
		return current, err
	}
	current, err := c.Store.Preparation(ctx, stored.Preparation.ID)
	if err != nil {
		return stored, err
	}
	if preparationNeedsIntervention(current.State) {
		return current, ErrPreparationIntervention
	}
	return current, nil
}

func sameStandingPreparationObservation(left, right execution.PreparationRecord) bool {
	if !left.Preparation.Requirements.Equal(right.Preparation.Requirements) {
		return false
	}
	left.Preparation.Revision, right.Preparation.Revision = 0, 0
	// Preserve the requirement reference's nil/empty selection semantics.
	left.Preparation.Requirements.SourceInputs, right.Preparation.Requirements.SourceInputs = nil, nil
	return reflect.DeepEqual(left, right)
}

func preparationNeedsIntervention(state execution.PreparationState) bool {
	return state.Stopped || state.Phase == project.PrepareFailed || state.Phase == project.PrepareStopped || state.Phase == project.PrepareInterrupted
}

const maxStandingPreparationObservations = 4

// Observe only: the serving owner never replays an administrator's setup.
// Missing or unavailable native identities retain their admitted row, while
// the fair cursor keeps a waiting Project from hiding another Project's result.
func (c *Coordinator) observeStandingPreparations(ctx context.Context, cursor *string) {
	if c.Host == nil {
		return
	}
	if cursor == nil {
		local := ""
		cursor = &local
	}
	records, err := c.Store.PendingStandingPreparations(ctx, *cursor, maxStandingPreparationObservations)
	if err != nil {
		return
	}
	budget := readinessBudgetFrom(ctx)
	for _, record := range records {
		if ctx.Err() != nil || budget != nil && !budget.reserveNativeRPC(1) {
			return
		}
		inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		state, err := c.Host.InspectWorkspacePreparation(inspectCtx, record.Address())
		cancel()
		*cursor = record.Preparation.ID
		if err != nil {
			continue
		}
		_, _ = c.adoptStandingPreparation(ctx, record, state)
	}
}
