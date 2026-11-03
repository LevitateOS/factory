package control

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// RetryRun records an explicit retry request after the prior run is
// reconciled and current authority revalidates. The next dispatch pass
// rechecks current policy, accepted inputs and allowance before launching.
func (c *Coordinator) RetryRun(ctx context.Context, commandID, principal, runID string) (factory.RetryDecision, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	run, err := c.admitRetryRun(bounded, runID)
	if err != nil {
		return factory.RetryDecision{}, err
	}
	cmd, err := buildRetryCommand(commandID, principal, run)
	if err != nil {
		return factory.RetryDecision{}, err
	}
	view, err := c.Store.FactoryRunView(bounded, run.ID)
	if err != nil {
		return factory.RetryDecision{}, retryOwnershipError(err)
	}
	assignment, err := c.Store.Assignment(bounded, view.Attempt)
	if err != nil {
		return factory.RetryDecision{}, retryOwnershipError(err)
	}
	implementation := assignment.Phase == project.ExecutionImplement && run.ContextPending &&
		run.Outcome == factory.Cancelled && run.Summary == "stop-before-start" &&
		!run.HasExecutionIdentity()
	if implementation {
		cmd.Type = factory.CommandRetryImplementation
		cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	}
	if prior, done, err := c.replayRetryCommand(bounded, cmd); done || err != nil {
		return prior, err
	}
	decision, err := cmd.RetryDecision()
	if err != nil {
		return factory.RetryDecision{}, err
	}
	if assignment.Subject.Kind == factory.SubjectDelivery && assignment.Phase == project.ExecutionVerify && assignment.Issue == 0 {
		if err := c.checkDeliveryRetrySource(bounded, assignment); err != nil {
			return factory.RetryDecision{}, err
		}
		stored, created, err := c.Store.RecordDeliveryRetryCommand(bounded, cmd, decision, assignment.Repository, time.Now())
		if err != nil {
			return factory.RetryDecision{}, err
		}
		if !created {
			return replayRetry(stored)
		}
		return decision, nil
	}
	head, view, err := c.admitRetryOwnership(bounded, run)
	if err != nil {
		return factory.RetryDecision{}, err
	}
	// Close the settled root before fresh-attempt admission; all immutable
	// retry and accounting checks keep their rules.
	if !implementation {
		if _, err = c.Store.CloseAttempt(bounded, assignment.ID); err != nil {
			return factory.RetryDecision{}, err
		}
	}
	record := c.Store.RecordExplicitRetryCommand
	if implementation {
		record = c.Store.RecordImplementationRetryCommand
	}
	stored, created, err := record(bounded, cmd, decision, view.Repository, view.Issue, head, time.Now())
	if err != nil {
		return factory.RetryDecision{}, err
	}
	if !created {
		return replayRetry(stored)
	}
	return decision, nil
}

// DeliveryRetryAvailable also checks the live integrated source. A later owner
// commit cannot leave an old verifier advertised as retryable indefinitely.
func (c *Coordinator) DeliveryRetryAvailable(ctx context.Context, runID string) (bool, error) {
	available, err := c.Store.DeliveryRetryAvailable(ctx, runID)
	if err != nil || !available {
		return false, err
	}
	view, err := c.Store.FactoryRunView(ctx, runID)
	if err != nil {
		return false, err
	}
	a, err := c.Store.Assignment(ctx, view.Attempt)
	if err != nil {
		return false, err
	}
	if err = c.checkDeliveryRetrySource(ctx, a); err != nil {
		if errors.Is(err, store.ErrExplicitRetryIneligible) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *Coordinator) checkDeliveryRetrySource(ctx context.Context, a factory.Assignment) error {
	if c.DispatchReads == nil {
		return errors.New("current application source is unavailable")
	}
	_, issue, err := c.Store.ApplicationVerificationInputs(ctx, a)
	if err != nil {
		return err
	}
	policy, err := c.Store.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		return err
	}
	native, err := c.DispatchReads.ReadDispatchInputs(ctx, strconv.FormatInt(a.Repository, 10), strconv.FormatInt(issue, 10), nil, policy.TargetBranch)
	if err != nil {
		return err
	}
	if native.TipRef != policy.TargetBranch || native.Tip != a.SourceCommit {
		return store.ErrExplicitRetryIneligible
	}
	return nil
}

// admitRetryRun binds one retry request to its reconciled prior run
// with effective authority.
func (c *Coordinator) admitRetryRun(ctx context.Context, runID string) (factory.Run, error) {
	var empty factory.Run
	run, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		return empty, factory.ErrNotFound
	}
	if !run.Reconciled {
		return empty, ErrPendingRuns{Runs: []string{runID}}
	}
	repository, err := c.Store.CollaborationRepositoryForNativeProject(ctx, run.ProjectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return empty, factory.ErrNotFound
		}
		return empty, err
	}
	effective, err := c.EffectiveAuthority(ctx, int64(repository.ID))
	if err != nil {
		return empty, err
	}
	if !effective.Effective && !onlyDispatchClosed(effective) {
		return empty, ErrIneffectiveAuthority
	}
	return run, nil
}

// buildRetryCommand builds the validated retry command behind one
// prior run.
func buildRetryCommand(commandID, principal string, run factory.Run) (factory.Command, error) {
	var empty factory.Command
	payload, err := json.Marshal(map[string]string{"prior_run": run.ID, "prior_outcome": string(run.Outcome)})
	if err != nil {
		return empty, err
	}
	target := "run/" + run.ID
	cmd := factory.Command{
		ID: commandID, Type: factory.CommandRetry, Target: target, Principal: principal,
		Payload: string(payload), Digest: factory.SettingsDigest(factory.CommandRetry, target, string(payload)),
	}
	if err = cmd.Validate(); err != nil {
		return empty, err
	}
	return cmd, nil
}

// replayRetryCommand replays one already-recorded retry command,
// reporting done when the outcome stands.
func (c *Coordinator) replayRetryCommand(ctx context.Context, cmd factory.Command) (factory.RetryDecision, bool, error) {
	prior, err := c.Store.FactoryCommand(ctx, cmd.ID)
	if err == nil {
		if prior.Digest != cmd.Digest {
			return factory.RetryDecision{}, true, store.ErrCommandConflict
		}
		decision, err := replayRetry(prior)
		return decision, true, err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return factory.RetryDecision{}, true, err
	}
	return factory.RetryDecision{}, false, nil
}

// admitRetryOwnership binds one retry to the owning attempt's current
// acceptance head, returning the head with its run view.
func (c *Coordinator) admitRetryOwnership(ctx context.Context, run factory.Run) (string, factory.RunView, error) {
	var empty factory.RunView
	view, err := c.Store.FactoryRunView(ctx, run.ID)
	if err != nil {
		return "", empty, retryOwnershipError(err)
	}
	if view.RunID != run.ID || view.Repository <= 0 || view.Issue <= 0 {
		return "", empty, store.ErrExplicitRetryIneligible
	}
	assignment, err := c.Store.Assignment(ctx, view.Attempt)
	if err != nil {
		return "", empty, retryOwnershipError(err)
	}
	if assignment.Run != run.ID || assignment.AttemptRoot == "" {
		return "", empty, store.ErrExplicitRetryIneligible
	}
	owner, err := c.Store.LatestIssueAssignment(ctx, view.Repository, view.Issue)
	if err != nil {
		return "", empty, retryOwnershipError(err)
	}
	if owner.AttemptRoot != assignment.AttemptRoot || assignment.PublicationAssignment != owner.ID {
		return "", empty, store.ErrExplicitRetryIneligible
	}
	head, err := c.Store.AcceptanceHead(ctx, view.Repository, view.Issue)
	if err != nil {
		return "", empty, retryOwnershipError(err)
	}
	if owner.Acceptance != head || assignment.Acceptance != head {
		return "", empty, store.ErrExplicitRetryIneligible
	}
	return head, view, nil
}

// Missing ownership is a domain refusal. An unavailable database or malformed
// stored record remains an operational error and cannot imply ineligibility.
func retryOwnershipError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return store.ErrExplicitRetryIneligible
	}
	return err
}

func onlyDispatchClosed(effective factory.EffectiveAuthority) bool {
	return len(effective.Missing) == 1 && effective.Missing[0] == factory.MissingDispatch
}

func replayRetry(stored factory.Command) (factory.RetryDecision, error) {
	if stored.Finished == "" {
		return factory.RetryDecision{}, ErrCommandRunning
	}
	return stored.RetryDecision()
}
