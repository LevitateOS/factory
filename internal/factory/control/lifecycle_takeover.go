package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/store"
)

// ErrTakeoverFailed reports a takeover the host did not confirm. The
// caller refreshes instead of assuming a copy that may not exist.
var ErrTakeoverFailed = errors.New("factory takeover unconfirmed")

// TakeoverRun copies a reconciled run's retained work into the admitted
// member's own checkout. Unsettled runs refuse: takeover cannot finish
// while retirement or credential accounting is unresolved. Provider homes
// and role Git configuration are excluded; no credential is transferred.
func (c *Coordinator) TakeoverRun(ctx context.Context, runID, member string) (factory.TakeoverRecord, error) {
	return c.RecoverRunWork(ctx, runID, execution.RecoveryTarget{Member: member})
}

// RecoverRunWork copies only settled work to an admitted destination reference.
// The application admits the receiver; the runtime resolves its owned checkout.
func (c *Coordinator) RecoverRunWork(ctx context.Context, runID string, target execution.RecoveryTarget) (factory.TakeoverRecord, error) {
	bounded, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	run, err := c.Store.FactoryRun(bounded, runID)
	if err != nil {
		return factory.TakeoverRecord{}, factory.ErrNotFound
	}
	if !run.Reconciled {
		return factory.TakeoverRecord{}, ErrPendingRuns{Runs: []string{runID}}
	}
	request := execution.WorkRecoveryRequest{Address: execution.RunAddress{Run: run.ID, Workspace: run.ProjectID}, Member: target.Member}
	if run.Native != nil {
		if target.Native == nil || target.Member != "" || !run.Native.Settled() {
			return factory.TakeoverRecord{}, execution.ErrRecovery
		}
		p := run.Native.Provenance
		request.Native = &execution.NativeRecovery{Source: execution.NativeWorkspaceRef{Profile: p.Profile, InstanceID: p.InstanceID, WorktreeID: p.WorktreeID}, Target: *target.Native}
	} else if target.Native != nil {
		return factory.TakeoverRecord{}, execution.ErrRecovery
	}
	if err = request.Validate(); err != nil {
		return factory.TakeoverRecord{}, err
	}
	key := request.Target().Key()
	if existing, err := c.Store.Takeover(bounded, runID, key); err == nil {
		if existing.Validate() != nil || !existing.BindsRun(run) || existing.TargetKey() != key {
			return factory.TakeoverRecord{}, ErrTakeoverFailed
		}
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return factory.TakeoverRecord{}, err
	}
	expected := request
	if request.Native != nil {
		captured := *request.Native
		expected.Native = &captured
	}
	result, err := c.Host.RecoverWork(bounded, request)
	if err != nil {
		return factory.TakeoverRecord{}, ErrTakeoverFailed
	}
	if err = result.ValidateFor(expected); err != nil {
		return factory.TakeoverRecord{}, ErrTakeoverFailed
	}
	record := factory.TakeoverRecord{
		Run: runID, Member: expected.Member, Project: run.ProjectID, Native: expected.Native,
		Dest: result.Destination, Copied: time.Now().UTC().Format(time.RFC3339),
	}
	return c.Store.RecordTakeover(bounded, record)
}

// VerifyProjectStart proves a coordinated start revived no old run: no
// recorded run reports a live broker lease or a live native boundary.
// Runs the host or broker never recorded stay unverified, never clean:
// a future launch or acquisition under that identity could still
// proceed. Fenced runs stay fenced for reconcile; leases stay closed;
// grants, readiness and the maintenance hold are untouched by the start.
func (c *Coordinator) VerifyProjectStart(ctx context.Context, projectID string) (factory.StartVerification, error) {
	bounded, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	p, err := c.Store.Project(bounded, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return factory.StartVerification{}, factory.ErrNotFound
		}
		return factory.StartVerification{}, err
	}
	repository, err := c.Store.CollaborationRepositoryForNativeProject(bounded, p.ID)
	if err != nil {
		return factory.StartVerification{}, err
	}
	runs, err := c.projectRuns(bounded, p.ID)
	if err != nil {
		return factory.StartVerification{}, err
	}
	verification := factory.StartVerification{Revived: []string{}, Unverified: []string{}, Started: true}
	for _, run := range runs {
		revived, unverified := c.checkStartRun(bounded, run)
		if revived {
			verification.Revived = append(verification.Revived, run.ID)
		} else if unverified {
			verification.Unverified = append(verification.Unverified, run.ID)
		}
	}
	held, err := c.Store.MaintenanceHold(bounded, p.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return factory.StartVerification{}, err
	}
	verification.Hold = err == nil && held.Hold
	verification.DispatchOpen, _, _, err = c.Store.DispatchState(bounded, int64(repository.ID))
	if err != nil {
		return factory.StartVerification{}, err
	}
	return verification, nil
}

// checkStartRun classifies one recorded run after a coordinated start. A
// live lease or live native boundary is revived; an unreadable or
// never-recorded boundary is unverified; anything else is clean.
func (c *Coordinator) checkStartRun(ctx context.Context, run factory.Run) (revived, unverified bool) {
	address := execution.RunAddress{Workspace: run.ProjectID, Run: run.ID}
	custody, err := c.Broker.ReadCustody(ctx, address)
	if err != nil || custody.ValidateFor(address) != nil || custody.Kind != run.RuntimeKind() {
		return false, true
	}
	if !run.Reconciled {
		if err := retainCustodyFacts(ctx, c.Store, &run, custody); err != nil {
			return false, true
		}
	}
	held := custody.Kind == collaboration.Native && custody.Native.State == execution.Held || custody.Kind == collaboration.Appliance && custody.Appliance.State == execution.Held
	if held {
		return true, false
	}
	if !custody.Sealed {
		return false, true
	}
	state, err := c.Host.InspectRun(ctx, address)
	if err != nil || state.ValidateFor(address) != nil || state.Kind != run.RuntimeKind() {
		return false, true
	}
	if !run.Reconciled {
		if err := retainRunFacts(ctx, c.Store, &run, state, nil); err != nil {
			return false, true
		}
	}
	if state.Live {
		return true, false
	}
	if state.Retirement != execution.Retired || adoptExecutionFacts(&run, state, custody) != nil || run.CredentialsDelegated() && !run.CredentialsReturned() {
		return false, true
	}
	return false, false
}
