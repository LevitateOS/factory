package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/identity"
	"github.com/levitateos/factory/pkg/project"
)

// StopResult reports the observed outcome of stopping a run. Uncertain stays
// fenced: the run keeps its unsettled state for reconcile to settle.
type StopResult struct {
	RunID     string `json:"run_id"`
	Outcome   string `json:"outcome,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Confirmed bool   `json:"confirmed"`
	Uncertain bool   `json:"uncertain"`
}

// FencedRun names a run reconcile could not settle and why.
type FencedRun struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// ReconcileResult reports the runs settled or left fenced by reconciliation.
type ReconcileResult struct {
	Settled []string    `json:"settled"`
	Fenced  []FencedRun `json:"fenced,omitempty"`
}

// Stop retires one recorded run and settles it when retirement and broker
// closure confirm. An already settled run returns its recorded outcome.
func (c *Coordinator) Stop(ctx context.Context, runID string) (StopResult, error) {
	ctx, ownsPass, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	bounded, stop := context.WithTimeout(ctx, 3*time.Minute)
	defer stop()
	run, err := c.Store.FactoryRun(bounded, runID)
	if err != nil {
		return StopResult{}, factory.ErrNotFound
	}
	result := c.settleRun(bounded, run)
	if ownsPass {
		_, _ = c.drainReadinessWork(ctx, "")
	}
	return result, nil
}

// Reconcile retires outstanding native runs and settles each one whose
// retirement, credential return and broker closure confirm. Unobserved
// pre-launch context holds retain their admitted packet; unresolved native
// effects stay fenced for a later command.
func (c *Coordinator) Reconcile(ctx context.Context) (ReconcileResult, error) {
	ctx, ownsPass, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	bounded, stop := context.WithTimeout(ctx, 10*time.Minute)
	defer stop()
	result, err := c.reconcileRuns(bounded)
	if err != nil {
		return ReconcileResult{}, err
	}
	if ownsPass {
		_, _ = c.drainReadinessWork(ctx, "")
	}
	return result, nil
}

func (c *Coordinator) reconcileRuns(ctx context.Context) (ReconcileResult, error) {
	receipt := ReconcileResult{Settled: []string{}}
	// Outstanding work first, in progressing batches: settled history
	// never displaces an unresolved run no matter how many settled rows
	// exist. Fenced runs stay unsettled, so visited identities bound the
	// walk instead of batch fullness alone.
	visited := map[string]bool{}
	for {
		batch, err := c.Store.FactoryUnsettledRuns(ctx, 1000)
		if err != nil {
			return receipt, err
		}
		progress := false
		for _, run := range batch {
			if visited[run.ID] {
				continue
			}
			visited[run.ID] = true
			progress = true
			if c.preservePendingContext(ctx, run) {
				continue
			}
			bounded, stop := context.WithTimeout(ctx, 2*time.Minute)
			settled := c.settleRun(bounded, run)
			stop()
			if settled.Uncertain {
				receipt.Fenced = append(receipt.Fenced, FencedRun{ID: run.ID, Reason: settled.Reason})
				continue
			}
			receipt.Settled = append(receipt.Settled, run.ID)
		}
		if !progress || len(batch) < 1000 {
			return receipt, nil
		}
	}
}

// settleRun drives one recorded run to its settled state: host stop, broker
// closure, then the durable record. A settled run never reopens; anything
// unconfirmed stays fenced with its observed facts saved.
func (c *Coordinator) settleRun(ctx context.Context, run factory.Run) StopResult {
	receipt := StopResult{RunID: run.ID}
	if run.Reconciled {
		receipt.Confirmed, receipt.Outcome, receipt.Reason = true, string(run.Outcome), run.Summary
		return receipt
	}
	state, err := c.Host.RetireRun(ctx, execution.RunAddress{Workspace: run.ProjectID, Run: run.ID})
	if err != nil {
		receipt.Uncertain, receipt.Reason = true, "host stop unconfirmed"
		return receipt
	}
	address := execution.RunAddress{Workspace: run.ProjectID, Run: run.ID}
	// Preserve the actual process/lease before another boundary can fail. This
	// neither settles the run nor releases its reserved capacity.
	if err = retainRunFacts(ctx, c.Store, &run, state, nil); err != nil {
		receipt.Uncertain, receipt.Reason = true, "run observation could not be retained"
		return receipt
	}
	sealErr := c.Broker.SealCustody(ctx, address)
	custody, err := c.Broker.ReadCustody(ctx, address)
	if err != nil || custody.ValidateFor(address) != nil {
		receipt.Uncertain, receipt.Reason = true, "broker execution unreadable"
		return receipt
	}
	if err = retainCustodyFacts(ctx, c.Store, &run, custody); err != nil {
		receipt.Uncertain, receipt.Reason = true, "recorded execution conflicts with observed facts"
		return receipt
	}
	if err = retainRunFacts(ctx, c.Store, &run, state, &custody); err != nil {
		receipt.Uncertain, receipt.Reason = true, "run observation could not be retained"
		return receipt
	}
	if sealErr != nil {
		receipt.Uncertain, receipt.Reason = true, "broker closure unconfirmed"
		return receipt
	}
	if !custody.Sealed {
		receipt.Uncertain, receipt.Reason = true, "broker execution unreadable"
		return receipt
	}
	outcome, ok := settleOutcome(state)
	if !ok {
		receipt.Uncertain, receipt.Reason = true, settleReason(state)
		return receipt
	}
	if run.CredentialsDelegated() && !run.CredentialsReturned() {
		receipt.Uncertain, receipt.Reason = true, "credential return unconfirmed"
		return receipt
	}
	run.Outcome, run.Summary, run.Reconciled = outcome, settleReason(state), true
	if err = c.Store.SaveFactoryRun(ctx, run); err != nil {
		receipt.Uncertain, receipt.Reason = true, "run record unsettled"
		return receipt
	}
	// Settlement records accounting and the result. Reassessing dependants is
	// local bookkeeping only; automatic publication/review/correction happens
	// at the existing Start and Dispatch advancement boundaries.
	_, _ = AccountSettledRun(ctx, c.Store, run, time.Now())
	// A recorded MCP terminal behind this run advances promptly now
	// that run-output accounting finished its assignment; the progress
	// drive remains the recovery path for replays and crashes.
	if settled, err := c.Store.AssignmentByRun(ctx, run.ID); err == nil {
		_, _ = SettleMCPTerminalProposal(ctx, c.Store, settled.ID, time.Now())
	}
	receipt.Confirmed, receipt.Outcome, receipt.Reason = true, string(outcome), run.Summary
	return receipt
}

// adoptRunObservation retains process identity independently of custody reads.
// Cleanup claims require the separate custody observation below. Mutations are
// atomic so a conflicting observation cannot partially replace known facts.
func adoptRunObservation(run *factory.Run, state execution.RunObservation) error {
	address := execution.RunAddress{Workspace: run.ProjectID, Run: run.ID}
	if state.ValidateFor(address) != nil || state.Kind != run.RuntimeKind() || state.Role != "" && state.Role != run.Role {
		return errors.New("run observation does not bind the retained run")
	}
	next := *run
	if err := adoptVerificationChecks(&next, state); err != nil {
		return err
	}
	if run.Native != nil {
		n := *run.Native
		n.Provenance = *state.Native
		n.Retirement = state.Retirement
		known, observed := run.Native.Provenance.Custody, state.Native.Custody
		if known.AcquisitionID != "" && (known.AcquisitionID != observed.AcquisitionID || known.Connection != observed.Connection || known.Generation != observed.Generation) {
			return errors.New("native custody identity differs from the recorded run")
		}
		n.Provenance.Custody = known
		if known.AcquisitionID == "" && observed.AcquisitionID != "" {
			n.Provenance.Custody = observed
			n.Provenance.Custody.State = execution.CustodyUncertain
		}
		if !n.Advances(*run.Native) {
			return errors.New("native execution identity changed")
		}
		next.Native = &n
	} else {
		facts := state.Appliance
		if next.IdentityLeaseID != "" && (next.IdentityLeaseID != facts.LeaseID || next.IdentityGeneration != facts.Invocation.Generation) {
			return errors.New("lease differs from recorded execution")
		}
		if facts.LeaseID != "" {
			next.IdentityLeaseID, next.IdentityGeneration = facts.LeaseID, facts.Invocation.Generation
		}
		if p := facts.Invocation; p.Invocation != "" {
			if next.ApplianceInvocation != nil {
				var err error
				p, err = p.Retain(*next.ApplianceInvocation)
				if err != nil {
					return err
				}
			}
			next.ApplianceInvocation = &p
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*run = next
	return nil
}

// settleOutcome maps confirmed host retirement to its run outcome. Uncertain
// retirement never settles: a pending native effect cannot appear cancelled.
func settleOutcome(state execution.RunObservation) (factory.Outcome, bool) {
	if state.Retirement != execution.Retired {
		return "", false
	}
	switch state.Phase {
	case project.FactoryCompleted:
		return factory.Succeeded, true
	case project.FactoryFailed:
		return factory.Failed, true
	case project.FactoryStopped:
		return factory.Cancelled, true
	default:
		return "", false
	}
}

func settleReason(state execution.RunObservation) string {
	if state.Phase == project.FactoryCompleted && state.Output != "" {
		if len(state.Output) > 4096 {
			return state.Output[:4096]
		}
		return state.Output
	}
	if state.Reason != "" {
		return state.Reason
	}
	return "run " + state.Phase
}

// adoptExecutionFacts fills recorded lease/binding/credential facts from host
// and broker observations. First-recorded facts never change; a conflict
// fences the run instead of rewriting its identity.
func adoptExecutionFacts(run *factory.Run, state execution.RunObservation, custody execution.CustodyObservation) error {
	address := execution.RunAddress{Workspace: run.ProjectID, Run: run.ID}
	if custody.ValidateFor(address) != nil || custody.Kind != run.RuntimeKind() {
		return errors.New("custody observation does not bind the retained run")
	}
	next := *run
	if err := adoptRunObservation(&next, state); err != nil {
		return err
	}
	if custody.Kind == collaboration.Native {
		n := *next.Native
		observed, retained := *custody.Native, n.Provenance.Custody
		if retained.AcquisitionID != "" && (observed.AcquisitionID != retained.AcquisitionID || observed.Connection != retained.Connection || observed.Generation != retained.Generation) {
			return errors.New("native custody differs from observed execution")
		}
		n.Provenance.Custody, n.Sealed = observed, custody.Sealed
		if !n.Advances(*run.Native) {
			return errors.New("native custody regressed")
		}
		next.Native = &n
		if err := next.Validate(); err != nil {
			return err
		}
		*run = next
		return nil
	}
	facts := state.Appliance
	if custody.Appliance.LeaseID != "" && custody.Appliance.LeaseID != facts.LeaseID {
		return errors.New("broker lease differs from observed execution")
	}
	if b := custody.Appliance.Binding; b != nil {
		if facts.Invocation.Container != "" && facts.Invocation.Container != b.Boundary {
			return errors.New("broker container differs from observed execution")
		}
		if facts.Invocation.Invocation != "" && b.Invocation != "" && facts.Invocation.Invocation != b.Invocation {
			return errors.New("broker invocation differs from observed execution")
		}
		binding := identity.Binding{Kind: b.Kind, ID: b.ID, Project: b.Boundary, Generation: b.Generation, InvocationID: b.Invocation, UID: b.UID, GID: b.GID, Login: b.Login, Scope: b.Scope, ChildID: b.ChildID, CredentialRoot: b.CredentialRoot}
		if next.IdentityBinding == nil {
			next.IdentityBinding, next.CredentialDelegated = &binding, true
		} else if *next.IdentityBinding != binding {
			return errors.New("binding differs from recorded execution")
		}
	}
	if facts.CredentialReturned {
		next.CredentialReturned = true
	}
	if facts.CredentialDelivered && next.IdentityBinding == nil {
		return errors.New("delivered credentials have no observed binding")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*run = next
	return nil
}
