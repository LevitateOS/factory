package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

const progressInterval = 10 * time.Second

type progressPassKey struct{}

// Nested dispatch/publication/check/merge passes share one owner. Native
// withdrawal remains independent so a running CLI never delays revocation.
func (c *Coordinator) progressPass(ctx context.Context) (context.Context, func()) {
	if ctx.Value(progressPassKey{}) == c {
		return ctx, func() {}
	}
	c.progressMu.Lock()
	return context.WithValue(ctx, progressPassKey{}, c), c.progressMu.Unlock
}

// Publication and Actions activity advances the native revision without an
// issue delivery. Refresh the existing readiness record from authoritative
// evidence; an unchanged acceptance keeps its semantic revision and allowance.
// Store authority can reopen without advancing the native revision.
func (c *Coordinator) refreshReadiness(ctx context.Context, control factory.IssueControl) (factory.IssueControl, error) {
	if c.Readiness == nil {
		if c.AcceptanceReads != nil {
			reserved, err := c.reserveIssueAssessment(ctx, factory.DependenceRef{Repository: control.Repository, Issue: control.Issue})
			if err != nil {
				return factory.IssueControl{}, err
			}
			if !reserved {
				return factory.IssueControl{}, ErrReadinessPassPending
			}
			observed, err := c.assessOne(ctx, control.Repository, control.Issue)
			return observed.control, err
		}
		return control, nil
	}
	authorityChanged := false
	if control.Readiness == factory.ReadinessNotAuthorized && control.Reason == factory.BlockerAuthorityMissing {
		authority, err := c.EffectiveAuthority(ctx, control.Repository)
		if err != nil {
			return factory.IssueControl{}, err
		}
		authorityChanged = factory.AuthorityFingerprint(authority) != control.Authority
		if !authorityChanged {
			return control, nil
		}
	}
	budget := readinessBudgetFrom(ctx)
	if budget != nil && !budget.reserveNativeRPC(1) {
		return factory.IssueControl{}, ErrReadinessPassPending
	}
	revision, idle, err := c.Readiness.ObserveNativeRevision(ctx)
	if err != nil {
		return factory.IssueControl{}, err
	}
	if !idle {
		return factory.IssueControl{}, ErrReadinessPassPending
	}
	if revision == control.NativeRev && !authorityChanged {
		return control, nil
	}
	reserved, err := c.reserveIssueAssessment(ctx, factory.DependenceRef{Repository: control.Repository, Issue: control.Issue})
	if err != nil {
		return factory.IssueControl{}, err
	}
	if !reserved {
		return factory.IssueControl{}, ErrReadinessPassPending
	}
	observed, err := c.assessOne(ctx, control.Repository, control.Issue)
	return observed.control, err
}

// The database owner revisits durable work when no new issue delivery arrives:
// explicit Retry, delayed checks and settled publication children all use the
// same bounded, idempotent passes as intake. This never creates new authority.
func (c *Coordinator) startProgress(ctx context.Context) {
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	if c.closing.Load() || c.lock == nil {
		return
	}
	ctx, c.progressCancel = context.WithCancel(ctx)
	c.progressDone = make(chan struct{})
	go c.runProgress(ctx, c.progressDone)
}

func (c *Coordinator) runProgress(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	standingPreparationCursor := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.requestGate.RLock()
			if !c.closing.Load() && ctx.Err() == nil {
				c.advanceProgressPage(ctx, &standingPreparationCursor)
			}
			c.requestGate.RUnlock()
		}
	}
}

func (c *Coordinator) advanceProgress(ctx context.Context) {
	c.advanceProgressPage(ctx, nil)
}

func (c *Coordinator) advanceProgressPage(ctx context.Context, standingPreparationCursor *string) {
	ctx, unlock := c.progressPass(ctx)
	defer unlock()
	ctx, ownsPass, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	defer c.observeProgressStage("idle")
	defer func() {
		if acknowledgements, ok := c.Host.(execution.RunAcknowledgements); ok && ctx.Err() == nil {
			_ = acknowledgements.AcknowledgeSettledRuns(ctx)
		}
	}()
	c.observeProgressStage("settlement")
	c.stopRevokedRuns(ctx, 0)
	budget := readinessBudgetFrom(ctx)
	runs, err := c.Store.FactoryUnsettledRuns(ctx, MaxDispatchRecovery)
	if err != nil {
		return
	}
	for _, run := range runs {
		if ctx.Err() != nil {
			break
		}
		// Admission can remain approved while the broker is busy, without
		// ever creating a native unit. The recorded deadline still bounds
		// its custody; Stop preserves an already-retained terminal outcome.
		if !time.Now().Before(run.Deadline) {
			if !budget.reserveNativeRPC(5) {
				break
			}
			c.settleRun(ctx, run)
			continue
		}
		if !budget.reserveNativeRPC(2) {
			break
		}
		state, err := c.Host.InspectRun(ctx, execution.RunAddress{Workspace: run.ProjectID, Run: run.ID})
		if err != nil || state.Run != run.ID || state.Workspace != run.ProjectID {
			continue
		}
		if observeRun(ctx, c.Store, c.Broker, &run, state) != nil {
			continue
		}
		if len(run.ExecutionManifest) > 0 {
			if !budget.reserveNativeRPC(1) {
				break
			}
			drained, stop, importErr := c.importRunTaskEvents(ctx, run, state)
			if importErr == nil && stop {
				if !budget.reserveNativeRPC(1) {
					break
				}
				_ = c.recoverFinalCheckpoint(ctx, run, state)
				if !budget.reserveNativeRPC(5) {
					break
				}
				c.settleRun(ctx, run)
				continue
			}
			if importErr == nil && drained {
				if !budget.reserveNativeRPC(1) {
					break
				}
				if errors.Is(c.driveRunCheckpoints(ctx, run, state), errCheckpointFailed) {
					if !budget.reserveNativeRPC(5) {
						break
					}
					c.settleRun(ctx, run)
					continue
				}
			}
		}
		if state.Live {
			continue
		}
		if _, terminal := settleOutcome(state); terminal {
			// Stop, at most three broker close calls, and its metadata read.
			if !budget.reserveNativeRPC(5) {
				break
			}
			c.settleRun(ctx, run)
		}
	}
	// Recorded MCP terminals settle after run settlement and before
	// dispatch, so receipts behind run-finished assignments advance in
	// this pass and fresh typed finishes can advance downstream.
	_ = c.settleMCPTerminals(ctx)
	// Durable comment requests advance with the service, one bounded
	// page per pass, independently of any browser request.
	c.observeProgressStage("comments")
	if c.CommentDrive != nil {
		c.CommentDrive(ctx)
	}
	c.observeProgressStage("standing_preparation")
	c.observeStandingPreparations(ctx, standingPreparationCursor)
	c.observeProgressStage("intent_graphs")
	_ = c.RecoverIntentGraphs(ctx)
	c.observeProgressStage("creations")
	_ = c.recoverCreations(ctx)
	// D13 completion fence (FW31): best-effort single drive of every
	// unresolved completion cycle — seal, decide, release — in
	// repository and generation order. Attention cycles are never
	// touched and no automatic re-run starts; reassessing and later
	// states rest for the FW32 coder admission. The production
	// bindings are the serving-wired enumerator plus the serving
	// source capture; a missing binding holds collecting cycles
	// honestly instead of sealing from partial evidence. There is no
	// automatic store-local fallback: a nil production callback holds
	// with completion wiring, never with an unqualified capture.
	deps := c.completionDriveDeps()
	c.observeProgressStage("completions")
	_ = c.DriveCompletions(ctx, deps)
	c.observeProgressStage("delivery_verification")
	c.driveDeliveryVerifications(ctx)
	c.observeProgressStage("delivery_repair")
	c.driveDeliveryRepairs(ctx)
	// D15 stall detection (R36) runs inside this same progress pass,
	// after observation, reconciliation and dispatch: a pass that
	// selected no work classifies the sealed inventory and admits at
	// most one bounded episode per stalled repository. Best effort
	// like the completion drive; a failed round keeps attention
	// instead of spending.
	report := c.Dispatch(ctx)
	c.observeProgressStage("stall")
	_, _ = c.ServeStallRound(ctx, report)
	if ownsPass {
		c.observeProgressStage("readiness")
		_, _ = c.drainReadinessWork(ctx, "")
	}
}
