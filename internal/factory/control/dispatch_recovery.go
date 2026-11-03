package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func recoverFinishedMCPAccounting(ctx context.Context, deps DispatchDeps, report *DispatchReport) {
	runs, err := deps.Store.FinishedMCPAccountingRuns(ctx, MaxDispatchRecovery)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "finished MCP accounting inventory unavailable"})
		return
	}
	for _, run := range runs {
		if err := deps.Store.AccountFinishedMCPRun(ctx, run.ID); err != nil {
			if errors.Is(err, store.ErrMCPAccountingTimeUnavailable) {
				report.Waits = append(report.Waits, DispatchWait{Reason: "retirement_time_unavailable", Detail: "Retired worker accounting waits for its recorded end time."})
				continue
			}
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "finished MCP run accounting remains unconfirmed"})
		}
	}
}

// recoverAssigned resolves every interrupted assigned assignment: runs
// that settled without accounting consume their reservation and finish,
// launches that never reached the host release and retry bounded, and
// fenced runs stay held for reconcile to settle. Recovery launches only
// after re-checking authority, limits and inputs; anything uncertain
// defers to a later pass.
func recoverAssigned(ctx context.Context, deps DispatchDeps, report *DispatchReport) {
	assigned, err := deps.Store.AssignedAssignments(ctx, MaxDispatchRecovery)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "assignment recovery unavailable"})
		return
	}
	for _, a := range assigned {
		if err := ctx.Err(); err != nil {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "dispatch recovery cancelled"})
			return
		}
		recoverOne(ctx, deps, a, report)
	}
}

// recoverOne resolves one assigned assignment and records the outcome in
// the report. Resolved means consumed, released-and-retried, or finished;
// deferred means untouched for a later pass.
func recoverOne(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) {
	reservation, err := deps.Store.Reservation(ctx, a.ID)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment reservation unreadable"})
		return
	}
	runs, err := dispatchHistoryRuns(ctx, deps.Store, a)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment runs unreadable"})
		return
	}
	active := activeHistoryRun(runs)
	if active == nil {
		recoverSettled(ctx, deps, a, reservation, report)
		return
	}
	if active.ContextPending && recoverPendingContext(ctx, deps, a, reservation, *active, report) {
		return
	}
	unused, fenced := confirmUnused(ctx, deps, a, active)
	switch {
	case fenced:
		ensureHeld(ctx, deps, a, reservation, report)
	case unused:
		recoverUnused(ctx, deps, a, reservation, *active, report)
	default:
		ensureHeld(ctx, deps, a, reservation, report)
	}
}

// dispatchHistoryRuns loads every attempt run of one assignment. A
// missing record reports nil at its position: record-before-call means a
// missing run never reached the host.
func dispatchHistoryRuns(ctx context.Context, db *store.Store, a factory.Assignment) ([]*factory.Run, error) {
	runs := make([]*factory.Run, 0, len(a.RunHistory))
	for _, id := range a.RunHistory {
		run, err := db.FactoryRun(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				runs = append(runs, nil)
				continue
			}
			return nil, err
		}
		runs = append(runs, &run)
	}
	return runs, nil
}

func activeHistoryRun(runs []*factory.Run) *factory.Run {
	for _, run := range runs {
		if run != nil && !run.Reconciled {
			return run
		}
	}
	return nil
}

// confirmUnused first observes both authorities, then durably fences an
// apparently unused identity before releasing its reservation. A lost launch
// reply can leave a native job in flight: absence alone cannot prove that
// acquisition will never arrive. Any delivered or uncertain effect stays held.
func confirmUnused(ctx context.Context, deps DispatchDeps, a factory.Assignment, run *factory.Run) (unused, fenced bool) {
	current, err := deps.Store.FactoryRun(ctx, run.ID)
	if err != nil || current.Reconciled || current.ProjectID != a.ProjectID {
		return false, true
	}
	*run = current
	address := execution.RunAddress{Workspace: a.ProjectID, Run: run.ID}
	state, hostErr := deps.Host.InspectRun(ctx, address)
	if hostErr != nil && !isHostNotFound(hostErr) {
		return false, true
	}
	if hostErr == nil {
		if err := retainRunFacts(ctx, deps.Store, run, state, nil); err != nil {
			return false, true
		}
	}
	custody, custodyErr := deps.Broker.ReadCustody(ctx, address)
	if custodyErr != nil && !errors.Is(custodyErr, execution.ErrCustodyNotObserved) {
		return false, true
	}
	if custodyErr == nil {
		if custody.ValidateFor(address) != nil || custody.Kind != run.RuntimeKind() {
			return false, true
		}
		if err := retainCustodyFacts(ctx, deps.Store, run, custody); err != nil {
			return false, true
		}
		if hostErr == nil {
			if err := retainRunFacts(ctx, deps.Store, run, state, &custody); err != nil {
				return false, true
			}
		}
	}
	// Current absence cannot erase a process/acquisition witnessed earlier. It
	// requires ordinary reconciliation and accounting, never unused release.
	if run.HasExecutionIdentity() {
		return false, true
	}
	if hostErr == nil && state.Phase != project.FactoryApproved && !unusedStoppedState(state) {
		return false, false
	}
	if custodyErr == nil && !unusedClosedExecution(custody, *run) {
		return false, false
	}
	state, err = deps.Host.RetireRun(ctx, address)
	if err != nil {
		return false, true
	}
	if err := retainRunFacts(ctx, deps.Store, run, state, nil); err != nil {
		return false, true
	}
	if !unusedStoppedState(state) || run.HasExecutionIdentity() {
		return false, false
	}
	sealErr := deps.Broker.SealCustody(ctx, address)
	custody, err = deps.Broker.ReadCustody(ctx, address)
	if err != nil {
		return false, true
	}
	if err := retainCustodyFacts(ctx, deps.Store, run, custody); err != nil {
		return false, true
	}
	if err := retainRunFacts(ctx, deps.Store, run, state, &custody); err != nil {
		return false, true
	}
	if sealErr != nil {
		return false, true
	}
	return unusedClosedExecution(custody, *run) && !run.HasExecutionIdentity(), false
}

func unusedStoppedState(state execution.RunObservation) bool {
	if state.Phase != project.FactoryStopped || state.Retirement != execution.Retired || state.Live || state.InvocationID() != "" || state.CredentialDeliveryObserved() || state.CredentialReturnObserved() {
		return false
	}
	if state.Kind == collaboration.Native {
		return state.Native != nil && state.Native.Custody.State == execution.NotAcquired
	}
	return state.Kind == collaboration.Appliance && state.Appliance != nil && state.Appliance.LeaseID == ""
}

func unusedClosedExecution(custody execution.CustodyObservation, run factory.Run) bool {
	if custody.ValidateFor(execution.RunAddress{Workspace: run.ProjectID, Run: run.ID}) != nil || custody.Kind != run.RuntimeKind() || !custody.Sealed {
		return false
	}
	if custody.Kind == collaboration.Native {
		return custody.Native.State == execution.NotAcquired
	}
	return custody.Appliance.State == execution.Released && custody.Appliance.LeaseID == "" && custody.Appliance.Binding == nil
}

func isHostNotFound(err error) bool { return errors.Is(err, execution.ErrRunNotObserved) }

// ensureHeld re-holds a released reservation behind a still-active run so
// the in-flight attempt keeps counting against its limits.
func ensureHeld(ctx context.Context, deps DispatchDeps, a factory.Assignment, reservation factory.Reservation, report *DispatchReport) {
	if reservation.State == factory.ReservationHeld {
		return
	}
	if reservation.State != factory.ReservationReleased {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "reservation behind a live run is not held"})
		return
	}
	reservation.State = factory.ReservationHeld
	reservation.Revision++
	if err := deps.Store.ReholdReservation(ctx, reservation); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "reservation re-hold failed"})
	}
}

// recoverSettled resolves an assigned assignment with no live attempt.
// A held reservation served its run: usage is recorded, the reservation
// is consumed and the assignment finishes from the run. A released
// reservation served nothing: attempts remain retryable, so recovery
// retries bounded or finishes terminally. A consumed reservation without
// a finish replays the missing finish.
func recoverSettled(ctx context.Context, deps DispatchDeps, a factory.Assignment, reservation factory.Reservation, report *DispatchReport) {
	latest, err := deps.Store.FactoryRun(ctx, a.Run)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			recoverMissingRun(ctx, deps, a, reservation, report)
			return
		}
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment run unreadable"})
		return
	}
	if !latest.Reconciled {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "settled recovery met a live run"})
		return
	}
	switch reservation.State {
	case factory.ReservationHeld:
		if err := recordConfirmedUsage(ctx, deps.Store, a, latest, time.Now()); err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "confirmed usage unrecorded"})
			return
		}
		if err := deps.Store.ConsumeReservation(ctx, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "reservation consume failed"})
			return
		}
		if err := finishFromRun(ctx, deps.Store, a, latest, time.Now()); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment finish failed"})
			}
			return
		}
		report.Recovered = append(report.Recovered, a.ID)
	case factory.ReservationConsumed:
		if err := finishFromRun(ctx, deps.Store, a, latest, time.Now()); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment finish failed"})
			}
			return
		}
		report.Recovered = append(report.Recovered, a.ID)
	default:
		if latest.Outcome == factory.Succeeded {
			if err := finishFromRun(ctx, deps.Store, a, latest, time.Now()); err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment finish failed"})
				}
				return
			}
			report.Recovered = append(report.Recovered, a.ID)
			return
		}
		retryOrFinish(ctx, deps, a, report)
	}
}

// recoverMissingRun resolves an assigned assignment whose latest run was
// never recorded: record-before-call proves the host never ran it, so
// the attempt released its capacity without consuming anything.
func recoverMissingRun(ctx context.Context, deps DispatchDeps, a factory.Assignment, reservation factory.Reservation, report *DispatchReport) {
	if reservation.State == factory.ReservationHeld {
		if err := deps.Store.ReleaseReservation(ctx, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "reservation release failed"})
			return
		}
	}
	retryOrFinish(ctx, deps, a, report)
}

// recoverUnused settles an active run the host and broker never saw,
// releases its confirmed-unused capacity, then retries bounded or
// finishes the assignment.
func recoverUnused(ctx context.Context, deps DispatchDeps, a factory.Assignment, reservation factory.Reservation, run factory.Run, report *DispatchReport) {
	settled := run
	settled.Outcome, settled.Summary, settled.Reconciled = factory.Failed, "dispatch launch never reached the host", true
	if err := deps.Store.SaveFactoryRun(ctx, settled); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "unlaunched run unsettled"})
		return
	}
	if reservation.State == factory.ReservationHeld {
		if err := deps.Store.ReleaseReservation(ctx, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "reservation release failed"})
			return
		}
	}
	retryOrFinish(ctx, deps, a, report)
}

// retryOrFinish retries a released assignment whose attempts remain and
// whose inputs still hold, or finishes it terminally. Limits and
// preparation defer quietly: the next pass retries, and the fresh visit
// already reports the issue as assigned.
func retryOrFinish(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) {
	if a.Attempts >= factory.MaxDispatchAttempts {
		finishTerminal(ctx, deps, a, factory.Failed, factory.AssignReasonExhausted, "dispatch launch refused", report)
		return
	}
	head, err := deps.Store.AcceptanceHead(ctx, a.Repository, a.Issue)
	if err != nil || head != a.Acceptance {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "acceptance head unreadable"})
			return
		}
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "acceptance no longer authorizes this attempt", report)
		return
	}
	effective, err := deps.Authority(ctx, a.Repository)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "authority unreadable"})
		return
	}
	if !effective.Effective {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonWithdrawn, "grants no longer authorize dispatch", report)
		return
	}
	retryAttempt(ctx, deps, a, report)
}

// finishTerminal finishes one assigned assignment with a synthesized
// result. A concurrent finish replays silently.
func finishTerminal(ctx context.Context, deps DispatchDeps, a factory.Assignment, outcome factory.Outcome, reason, summary string, report *DispatchReport) {
	current, err := deps.Store.Assignment(ctx, a.ID)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment unreadable"})
		return
	}
	if current.Stage != factory.AssignmentAssigned {
		return
	}
	now := time.Now()
	status := "failed"
	if outcome == factory.Cancelled {
		status = "cancelled"
	}
	// Terminal finishes keep the assignment's bound typed result,
	// exactly as run-derived terminal states do.
	result := synthesizePhased(current, status, summary, now.Unix())
	current.Stage, current.Outcome, current.Reason = factory.AssignmentFinished, outcome, reason
	current.FinishedUnix = now.Unix()
	current.Result = &result
	if err := current.Validate(); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "terminal assignment invalid"})
		return
	}
	if err := deps.Store.FinishAssignment(ctx, current); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment finish failed"})
		}
		return
	}
	report.Recovered = append(report.Recovered, a.ID)
}
