package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// ErrPendingRuns reports a resume, retry or takeover refused while recorded
// runs are unsettled. The caller stops or reconciles first; pending native
// effects never appear cancelled, queued or taken over.
type ErrPendingRuns struct {
	Runs []string
}

func (e ErrPendingRuns) Error() string { return "factory runs are not settled" }

// PauseRepository latches one repository's dispatch closed and then stops
// its outstanding runs. Withdrawal precedes every stop, so a delayed
// dispatch cannot pass the closed gate and a delayed start meets
// the stop tombstone. The result reports each native stop outcome.
func (c *Coordinator) PauseRepository(ctx context.Context, principal string, repository int64) (factory.PauseResult, error) {
	bounded, stop := context.WithTimeout(ctx, 10*time.Minute)
	defer stop()
	if repository <= 0 {
		return factory.PauseResult{}, errors.New("invalid pause target")
	}
	withdrawal, outcomes, err := c.withdrawAndStopRuns(bounded, repository, factory.CauseControlPaused, principal)
	if err != nil {
		return factory.PauseResult{}, err
	}
	_, gateRevision, _, err := c.Store.DispatchState(bounded, repository)
	if err != nil {
		return factory.PauseResult{}, err
	}
	if err = c.Store.FreezeAttemptAllowances(bounded, repository, gateRevision, time.Now()); err != nil {
		return factory.PauseResult{}, err
	}
	return factory.PauseResult{Withdrawal: withdrawal, Runs: outcomes, Paused: true}, nil
}

// withdrawAndStopRuns closes dispatch first and then settles every
// outstanding run of the repository's project. It is the shared first
// half of pause and coordinated Project stop.
func (c *Coordinator) withdrawAndStopRuns(ctx context.Context, repository int64, cause, principal string) (factory.Withdrawal, []factory.RunStopOutcome, error) {
	withdrawal, err := c.Store.WithdrawDispatch(ctx, repository, cause, principal)
	if err != nil {
		return factory.Withdrawal{}, nil, err
	}
	withdrawal.Publications = c.cancelRepositoryPublications(ctx, repository)
	withdrawal.Merges = c.cancelRepositoryMerges(ctx, repository)
	outcomes := []factory.RunStopOutcome{}
	p, err := c.Store.NativeProjectForCollaborationRepository(ctx, collaboration.RepositoryID(repository))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return withdrawal, outcomes, nil
		}
		return factory.Withdrawal{}, nil, err
	}
	preparationErr := c.stopCandidatePreparations(ctx, repository, p.ID)
	runs, err := c.projectRuns(ctx, p.ID)
	if err != nil {
		return factory.Withdrawal{}, nil, err
	}
	for _, run := range runs {
		if run.Reconciled {
			continue
		}
		bounded, stop := context.WithTimeout(ctx, 2*time.Minute)
		settled := c.settleRun(bounded, run)
		stop()
		outcomes = append(outcomes, factory.RunStopOutcome{
			ID: settled.RunID, Outcome: settled.Outcome, Reason: settled.Reason,
			Confirmed: settled.Confirmed, Uncertain: settled.Uncertain,
		})
	}
	if preparationErr != nil {
		return withdrawal, outcomes, preparationErr
	}
	return withdrawal, outcomes, nil
}

func (c *Coordinator) stopCandidatePreparations(ctx context.Context, repository int64, projectID string) error {
	return c.stopCandidatePreparationsForRoot(ctx, repository, projectID, 0, "")
}

func (c *Coordinator) stopCandidatePreparationsForRoot(ctx context.Context, repository int64, projectID string, issue int64, root string) error {
	after := ""
	for {
		page, err := c.Store.CandidatePreparations(ctx, repository, projectID, issue, root, after, 64, 0)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		if c.Host == nil {
			return errors.New("candidate preparation host unavailable")
		}
		for _, record := range page {
			after = record.Preparation.Preparation.ID
			state, stopErr := c.Host.RetireWorkspacePreparation(ctx, record.Preparation.Address())
			if stopErr != nil {
				return stopErr
			}
			if err = c.Store.RecordCandidatePreparationStop(ctx, after, state); err != nil {
				return err
			}
			if state.Retirement != "confirmed" {
				return store.ErrCandidatePreparationPending
			}
		}
		if len(page) < 64 {
			return nil
		}
	}
}

// projectRuns lists one project's recorded runs: every unsettled run
// first, then recent history. Settled history never hides live work;
// the history tail stays bounded for display-sized decisions.
func (c *Coordinator) projectRuns(ctx context.Context, projectID string) ([]factory.Run, error) {
	outstanding, err := c.Store.ProjectUnsettledRuns(ctx, projectID, 1000)
	if err != nil {
		return nil, err
	}
	history, err := c.Store.ProjectFactoryRuns(ctx, projectID, 1000)
	if err != nil {
		return nil, err
	}
	out := make([]factory.Run, 0, len(outstanding)+len(history))
	seen := make(map[string]bool, len(outstanding)+len(history))
	for _, run := range outstanding {
		if seen[run.ID] {
			continue
		}
		seen[run.ID] = true
		out = append(out, run)
	}
	for _, run := range history {
		if seen[run.ID] {
			continue
		}
		seen[run.ID] = true
		out = append(out, run)
	}
	return out, nil
}

// StopProject withdraws dispatch and stops outstanding runs for one
// project without touching holds or host lifecycle. The lifecycle API
// runs the hold and host stop around this in the same order.
func (c *Coordinator) StopProject(ctx context.Context, principal, projectID string) (factory.Withdrawal, []factory.RunStopOutcome, error) {
	bounded, stop := context.WithTimeout(ctx, 10*time.Minute)
	defer stop()
	repository, err := c.Store.CollaborationRepositoryForNativeProject(bounded, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return factory.Withdrawal{}, nil, factory.ErrNotFound
		}
		return factory.Withdrawal{}, nil, err
	}
	return c.withdrawAndStopRuns(bounded, int64(repository.ID), factory.CauseProjectStop, principal)
}

// ClearProjectStopAfterStart releases only the Project lifecycle cause after
// host start verification and confirmation that no unsettled Project run
// remains. Independent repository and grant causes stay closed.
func (c *Coordinator) ClearProjectStopAfterStart(ctx context.Context, projectID string, verification factory.StartVerification) (bool, error) {
	if err := verification.Validate(); err != nil {
		return false, err
	}
	p, err := c.Store.Project(ctx, projectID)
	if err != nil {
		return false, err
	}
	repository, err := c.Store.CollaborationRepositoryForNativeProject(ctx, p.ID)
	if err != nil {
		return false, err
	}
	open, revision, withdrawal, err := c.Store.DispatchState(ctx, int64(repository.ID))
	if err != nil || open {
		return open, err
	}
	hasProjectStop := false
	for _, cause := range withdrawal.ActiveCauses {
		if cause == factory.CauseProjectStop {
			hasProjectStop = true
			break
		}
	}
	if !hasProjectStop || len(verification.Revived) != 0 || len(verification.Unverified) != 0 {
		return false, nil
	}
	unsettled, err := c.Store.ProjectUnsettledRuns(ctx, p.ID, 1)
	if err != nil {
		return false, err
	}
	if len(unsettled) != 0 {
		return false, nil
	}
	open, _, err = c.Store.ClearProjectStop(ctx, int64(repository.ID), revision)
	return open, err
}

// ResumeRepository reopens a paused gate after every recorded run settles
// and current authority permits it. Queued work stays queued.
func (c *Coordinator) ResumeRepository(ctx context.Context, repository int64) (factory.ResumeResult, error) {
	bounded, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	if repository <= 0 {
		return factory.ResumeResult{}, errors.New("invalid resume target")
	}
	if p, err := c.Store.NativeProjectForCollaborationRepository(bounded, collaboration.RepositoryID(repository)); err == nil {
		runs, err := c.projectRuns(bounded, p.ID)
		if err != nil {
			return factory.ResumeResult{}, err
		}
		pending := []string{}
		for _, run := range runs {
			if !run.Reconciled {
				pending = append(pending, run.ID)
			}
		}
		if len(pending) > 0 {
			return factory.ResumeResult{}, ErrPendingRuns{Runs: pending}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return factory.ResumeResult{}, err
	}
	open, revision, _, err := c.Store.DispatchState(bounded, repository)
	if err != nil {
		return factory.ResumeResult{}, err
	}
	if open {
		effective, err := c.EffectiveAuthority(bounded, repository)
		if err != nil {
			return factory.ResumeResult{}, err
		}
		return factory.ResumeResult{Effective: effective, Revision: revision}, nil
	}
	effective, err := c.EffectiveAuthority(bounded, repository)
	if err != nil {
		return factory.ResumeResult{}, err
	}
	for _, reason := range effective.Missing {
		if reason != factory.MissingDispatch {
			return factory.ResumeResult{}, ErrIneffectiveAuthority
		}
	}
	_, revision, err = c.Store.ReopenDispatch(bounded, repository, revision)
	if err != nil {
		return factory.ResumeResult{}, err
	}
	after, err := c.EffectiveAuthority(bounded, repository)
	if err != nil {
		return factory.ResumeResult{}, err
	}
	return factory.ResumeResult{Effective: after, Revision: revision, Reopened: after.DispatchOpen}, nil
}
