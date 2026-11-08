package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// Retire expired or invalidated setup even when the issue is no longer queued
// or planning now waits before reaching the preparation step. This runs under
// the same progress owner and budget as dispatch, without creating new work.
func (c *Coordinator) reconcileRetryPreparations(ctx context.Context) error {
	records, err := c.Store.PendingRetryPreparations(ctx, MaxDispatchRecovery)
	if err != nil {
		return err
	}
	for _, record := range records {
		p := record.Preparation
		if record.NotAfter > time.Now().Unix() {
			p.State = execution.PreparationState{}
			if _, _, err := c.Store.AdmitCandidatePreparation(ctx, p, record.Admission); err == nil {
				continue
			} else if !retryPreparationInvalidated(err) {
				return err
			}
		}
		// A committed packet transfers ownership to its recorded run; a lost
		// reply or concurrent request must not retire that run's checkout.
		if _, err := c.Store.Assignment(ctx, record.Admission.OwnerAssignment); !errors.Is(err, store.ErrNotFound) {
			if err != nil {
				return err
			}
			continue
		}
		if err := c.retireCandidatePreparation(ctx, record.Preparation); err != nil {
			return err
		}
	}
	return nil
}

func retryPreparationInvalidated(err error) bool {
	return errors.Is(err, store.ErrAdmissionChanged) || errors.Is(err, store.ErrDispatchClosed) ||
		errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrDispatchControlStale) ||
		errors.Is(err, store.ErrExplicitRetryIneligible) || errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, factory.ErrAttemptClosed) || errors.Is(err, factory.ErrAttemptTimeExhausted)
}

// Explicit Retry starts a new private checkout. The preceding run's checkout
// and protected source snapshot remain inspectable; neither is reset in place.
func (c *Coordinator) prepareRetryCoder(ctx context.Context, plan *attemptPlan, command string) (bool, error) {
	return c.prepareCoder(ctx, plan, command, nil)
}

// A confirmed native merge can advance a dependent's source while its exact
// accepted setup remains current. The export and the private setup retain
// separate identities; no caller provides source bundle bytes.
func (c *Coordinator) prepareSourceCoder(ctx context.Context, plan *attemptPlan) (bool, error) {
	open, revision, _, err := c.Store.DispatchState(ctx, plan.control.Repository)
	if err != nil || !open {
		return false, store.ErrDispatchClosed
	}
	identity, err := json.Marshal(struct {
		Repository, Issue  int64
		Acceptance, Source string
		Authority          factory.AuthorityRef
		Gate               int64
	}{plan.control.Repository, plan.control.Issue, plan.control.Acceptance, plan.inputs.Tip, plan.effective.Authority, revision})
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(append([]byte("source-coder\x00"), identity...))
	return c.prepareCoder(ctx, plan, hex.EncodeToString(sum[:16]), &revision)
}

func (c *Coordinator) prepareCoder(ctx context.Context, plan *attemptPlan, command string, expectedGate *int64) (bool, error) {
	source, sourceRun, sourceMerge, err := c.resolveCoderSource(ctx, plan)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256([]byte("fresh-coder\x00" + command + "\x00" + plan.inputs.Tip))
	prep := source
	prep.ID, prep.Revision = "f"+hex.EncodeToString(sum[:12]), 0
	prep.SourceCommit = plan.inputs.Tip
	requested := time.Now().Add(10 * time.Minute).Unix()
	if plan.deadline.Unix() < requested {
		requested = plan.deadline.Unix()
	}
	registration := store.CandidatePreparationRegistration{
		Repository: plan.control.Repository, Issue: plan.control.Issue, ActorID: plan.sponsorship.ActorID,
		Project: plan.projectID, OwnerAssignment: command, ChildAssignment: command, AttemptRoot: command,
		Connection: plan.sponsorship.Connection, Authority: plan.effective.Authority,
		GateRevision: plan.gateRev, Control: plan.control, RequestedNotAfter: requested,
		SourceRun: sourceRun, SourceMerge: sourceMerge,
	}
	record, created, err := c.admitCoderPreparation(ctx, plan, prep, registration, command, expectedGate)
	if err != nil {
		return false, err
	}
	stored := record.Preparation
	plan.preparationOwner = command
	if record.Stopped || record.Retirement == "confirmed" {
		return false, errors.New("retry preparation was retired")
	}
	if stored.State.Ready {
		if !candidatePreparationStateMatches(stored.State, prep) {
			return false, errors.New("retry preparation ready identity differs")
		}
		plan.prep = stored
		return true, nil
	}
	if !created {
		done, ready, err := c.inspectCoderPreparation(ctx, plan, prep, stored)
		if done || err != nil {
			return ready, err
		}
	}
	return c.authorizeCoderPreparation(ctx, plan, prep, source, stored, sourceRun, sourceMerge)
}

// resolveCoderSource rebinds one retry preparation to the merged
// source at the planned tip.
func (c *Coordinator) resolveCoderSource(ctx context.Context, plan *attemptPlan) (project.Preparation, *project.FactoryExport, *project.FactoryMergedSource, error) {
	source := plan.prep.Preparation
	if source.SourceCommit == plan.inputs.Tip {
		return source, nil, nil, nil
	}
	if len(source.Requirements.SourceInputs) == 0 {
		return source, nil, nil, errors.New("accepted effective source input selection is unavailable")
	}
	bound, err := c.Store.MergedPreparationSource(ctx, plan.control.Repository, plan.projectID, plan.policy.TargetBranch, plan.inputs.Tip)
	if err != nil {
		return source, nil, nil, err
	}
	if bound.Commit == bound.Export.Candidate {
		return source, &bound.Export, nil, nil
	}
	if plan.prep.Native == nil {
		return source, nil, nil, errors.New("host cannot fetch a provider-created merge result")
	}
	return source, nil, &bound, nil
}

// admitCoderPreparation reads the dispatch gate and admits one retry
// preparation, retiring an invalidated prior owner preparation.
func (c *Coordinator) admitCoderPreparation(ctx context.Context, plan *attemptPlan, prep project.Preparation, registration store.CandidatePreparationRegistration, command string, expectedGate *int64) (store.CandidatePreparationRecord, bool, error) {
	var empty store.CandidatePreparationRecord
	// The gate is read before preparation admission, rather than after prompt
	// construction, so setup is fenced by the same revision as dispatch.
	open, revision, _, err := c.Store.DispatchState(ctx, plan.control.Repository)
	if err != nil || !open {
		return empty, false, errors.New("retry dispatch gate is unavailable or closed")
	}
	if expectedGate != nil && revision != *expectedGate {
		return empty, false, store.ErrStaleRevision
	}
	registration.GateRevision = revision
	requested := execution.PreparationRecord{Preparation: prep}
	if plan.prep.Native != nil {
		owner := *plan.prep.Native
		owner.WorktreeID = prep.ID
		requested.Native = &owner
	}
	record, created, err := c.Store.AdmitCandidatePreparation(ctx, requested, registration)
	if err != nil {
		if prior, readErr := c.Store.CandidatePreparation(ctx, prep.ID); retryPreparationInvalidated(err) && readErr == nil &&
			prior.Admission.OwnerAssignment == command && prior.Admission.PublicationID == "" && !prior.Stopped {
			plan.prep = prior.Preparation
			if stopErr := c.retireCandidatePreparation(ctx, plan.prep); stopErr != nil {
				return empty, false, stopErr
			}
		}
		return empty, false, err
	}
	return record, created, nil
}

// inspectCoderPreparation inspects one already-recorded preparation,
// reporting done when the outcome already settles the caller.
func (c *Coordinator) inspectCoderPreparation(ctx context.Context, plan *attemptPlan, prep project.Preparation, stored execution.PreparationRecord) (bool, bool, error) {
	if budget := readinessBudgetFrom(ctx); budget != nil && !budget.reserveNativeRPC(1) {
		return true, false, ErrReadinessPassPending
	}
	state, inspectErr := c.Host.InspectWorkspacePreparation(ctx, stored.Address())
	if inspectErr == nil {
		return c.observeInspectedCoderPreparation(ctx, plan, prep, stored, state)
	}
	if !errors.Is(inspectErr, execution.ErrPreparationNotObserved) {
		return true, false, inspectErr
	}
	return false, false, nil
}

// observeInspectedCoderPreparation observes one inspected preparation
// state, settling ready, running and failed phases.
func (c *Coordinator) observeInspectedCoderPreparation(ctx context.Context, plan *attemptPlan, prep project.Preparation, stored execution.PreparationRecord, state execution.PreparationState) (bool, bool, error) {
	if !candidatePreparationIdentityMatches(state, prep) {
		return true, false, errors.New("retry preparation native identity differs")
	}
	if err := c.observeCandidatePreparation(ctx, stored, state); err != nil {
		return true, false, err
	}
	if state.Ready {
		planPrep, err := c.Store.Preparation(ctx, prep.ID)
		plan.prep = planPrep
		return true, err == nil, err
	}
	if state.Phase == project.PrepareRunning {
		return true, false, nil
	}
	if state.Phase == project.PrepareFailed || state.Phase == project.PrepareStopped || state.Phase == project.PrepareInterrupted {
		return true, false, errors.New("retry preparation requires intervention")
	}
	return false, false, nil
}

// authorizeCoderPreparation authorizes and prepares one admitted
// retry preparation, loading it when ready.
func (c *Coordinator) authorizeCoderPreparation(ctx context.Context, plan *attemptPlan, prep, source project.Preparation, stored execution.PreparationRecord, sourceRun *project.FactoryExport, sourceMerge *project.FactoryMergedSource) (bool, error) {
	notAfter, err := c.Store.AuthorizeCandidatePreparation(ctx, prep.ID)
	if err != nil {
		if retryPreparationInvalidated(err) {
			plan.prep = stored
			if stopErr := c.retireCandidatePreparation(ctx, plan.prep); stopErr != nil {
				return false, stopErr
			}
		}
		return false, err
	}
	if budget := readinessBudgetFrom(ctx); budget != nil && !budget.reserveNativeRPC(1) {
		return false, ErrReadinessPassPending
	}
	state, err := c.Host.PrepareWorkspaceCandidate(ctx, execution.CandidatePreparationRequest{Native: stored.Native, Input: project.FactoryCandidate{
		Preparation: prep, SourcePreparation: source.ID, Deadline: time.Unix(notAfter, 0),
		SourceRun: sourceRun, SourceMerge: sourceMerge,
	}})
	if err != nil {
		return false, err
	}
	if !candidatePreparationIdentityMatches(state, prep) {
		return false, errors.New("retry preparation native receipt differs")
	}
	if err := c.observeCandidatePreparation(ctx, stored, state); err != nil {
		return false, err
	}
	if !state.Ready {
		return false, nil
	}
	planPrep, err := c.Store.Preparation(ctx, prep.ID)
	plan.prep = planPrep
	return err == nil, err
}

func (c *Coordinator) retireCandidatePreparation(ctx context.Context, record execution.PreparationRecord) error {
	if budget := readinessBudgetFrom(ctx); budget != nil && !budget.reserveNativeRPC(1) {
		return ErrReadinessPassPending
	}
	state, err := c.Host.RetireWorkspacePreparation(ctx, record.Address())
	if err != nil {
		return err
	}
	if err := c.Store.RecordCandidatePreparationStop(ctx, record.Preparation.ID, state); err != nil {
		return err
	}
	if state.Retirement != "confirmed" {
		return store.ErrCandidatePreparationPending
	}
	return nil
}
