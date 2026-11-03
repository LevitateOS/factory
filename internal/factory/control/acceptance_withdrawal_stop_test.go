package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func TestWithdrawAcceptanceStopsAdmittedRun(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	decision := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, decision.ID)
	otherDecision := fx.accept(t, 5, "d555555555555555555555555")
	fx.queue(t, 5, otherDecision.ID)

	// DispatchPass records the assignment, run and run view in the real store
	// before invoking this host launch. Keeping the process active exercises
	// withdrawal after packet admission, rather than its pre-packet gate.
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryRunning}, nil
	}
	dispatched := DispatchPass(ctx, fx.deps())
	if len(dispatched.Launched) != 1 || dispatched.Launched[0].Issue != 3 || len(dispatched.Errors) != 0 {
		t.Fatalf("dispatch = %+v", dispatched)
	}
	runID := dispatched.Launched[0].RunID
	run, err := db.FactoryRun(ctx, runID)
	if err != nil || run.Reconciled {
		t.Fatalf("precondition: run should remain active: %+v, %v", run, err)
	}
	assignment, err := db.Assignment(ctx, dispatched.Launched[0].AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	history := append([]string(nil), assignment.RunHistory...)

	var stopped []string
	host := &stubHost{}
	broker := &stubBroker{}
	settleStubs(host, broker)
	withdrawalCustodyFixture(host, broker)
	confirmedStop := host.stop
	stopErr := errors.New("host stop reply is uncertain")
	stopCalls := 0
	host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		stopCalls++
		stopped = append(stopped, in.ID)
		if stopCalls == 1 {
			return project.FactoryState{}, stopErr
		}
		return confirmedStop(in)
	}
	coord := &Coordinator{Store: db, Host: host, Broker: broker}
	pending, err := coord.WithdrawAcceptance(ctx, fx.repo, 3, decision.ID, 7, func(ctx context.Context) error { return ctx.Err() })
	if !errors.Is(err, ErrCommandRunning) || len(pending.PendingRuns) != 1 {
		t.Fatalf("uncertain host stop was reported as completed or lost its pending target: %+v, %v", pending, err)
	}
	unfinished, err := db.FactoryRun(ctx, runID)
	if err != nil || unfinished.Reconciled {
		t.Fatalf("uncertain stop settled the run: %+v, %v", unfinished, err)
	}
	reservation, err := db.Reservation(ctx, assignment.ID)
	if err != nil || reservation.State != factory.ReservationHeld {
		t.Fatalf("uncertain stop released the reservation: %+v, %v", reservation, err)
	}
	other, err := db.IssueControl(ctx, fx.repo, 5)
	if err != nil || other.Acceptance != otherDecision.ID || other.Readiness != factory.ReadinessQueued {
		t.Fatalf("withdrawal changed queued unrelated issue: %+v, %v", other, err)
	}
	result, err := coord.WithdrawAcceptance(ctx, fx.repo, 3, decision.ID, 7, func(ctx context.Context) error { return ctx.Err() })
	if err != nil || !result.Withdrawn {
		t.Fatalf("complete withdrawal from current run state: %+v, %v", result, err)
	}
	if len(stopped) != 2 || stopped[0] != runID || stopped[1] != runID {
		t.Fatalf("withdrawal stop calls = %v, want two attempts on only %q", stopped, runID)
	}
	settled, err := db.FactoryRun(ctx, runID)
	if err != nil || !settled.Reconciled || settled.Outcome != factory.Cancelled {
		t.Fatalf("withdrawn run was not accounted as cancelled: %+v, %v", settled, err)
	}
	assignment, err = db.Assignment(ctx, assignment.ID)
	if err != nil || !reflect.DeepEqual(assignment.RunHistory, history) {
		t.Fatalf("withdrawal changed assignment attempt history: %+v, %v", assignment.RunHistory, err)
	}
	before := stopCalls
	replayed, err := coord.WithdrawAcceptance(ctx, fx.repo, 3, decision.ID, 7, func(ctx context.Context) error { return ctx.Err() })
	if err != nil || stopCalls != before || replayed.Decision != result.Decision || !replayed.Withdrawn {
		t.Fatalf("repeating completed withdrawal re-stopped a run: result=%+v calls=%d err=%v", replayed, stopCalls, err)
	}
	other, err = db.IssueControl(ctx, fx.repo, 5)
	if err != nil || other.Acceptance != otherDecision.ID || other.Readiness != factory.ReadinessQueued {
		t.Fatalf("completed withdrawal changed queued unrelated issue: %+v, %v", other, err)
	}
}

func TestAcceptanceWithdrawalSurvivesRunReconciliation(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	decision := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, decision.ID)
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryRunning}, nil
	}
	dispatched := DispatchPass(ctx, fx.deps())
	if len(dispatched.Launched) != 1 {
		t.Fatalf("dispatch = %+v", dispatched)
	}
	runID := dispatched.Launched[0].RunID
	host, broker := &stubHost{}, &stubBroker{}
	settleStubs(host, broker)
	withdrawalCustodyFixture(host, broker)
	stopCalls := 0
	confirmedStop := host.stop
	host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		stopCalls++
		if stopCalls == 1 {
			return project.FactoryState{}, errors.New("stop reply lost")
		}
		return confirmedStop(in)
	}
	coord := &Coordinator{Store: db, Host: host, Broker: broker}
	pending, err := coord.WithdrawAcceptance(ctx, fx.repo, 3, decision.ID, 7, func(ctx context.Context) error { return ctx.Err() })
	if !errors.Is(err, ErrCommandRunning) || len(pending.PendingRuns) != 1 {
		t.Fatalf("initial withdrawal = %+v, %v", pending, err)
	}
	reservation, err := db.Reservation(ctx, dispatched.Launched[0].AssignmentID)
	if err != nil || reservation.State != factory.ReservationHeld {
		t.Fatalf("uncertain stop released the reservation: %+v, %v", reservation, err)
	}

	newHead := decision
	newHead.ID, newHead.Predecessor = "d444444444444444444444444", decision.ID
	newHead.NativeRev++
	if err := db.AdmitAcceptanceDecision(ctx, newHead); err != nil {
		t.Fatal(err)
	}
	if _, err := coord.reconcileRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if stopCalls != 2 {
		t.Fatalf("stop calls = %d, want initial withdrawal and actual run reconciliation", stopCalls)
	}
	oldWithdrawn, _, err := db.AcceptanceWithdrawn(ctx, fx.repo, 3, decision.ID)
	if err != nil || !oldWithdrawn {
		t.Fatalf("original decision latch lost during reconciliation: withdrawn=%v err=%v", oldWithdrawn, err)
	}
	head, err := db.AcceptanceHead(ctx, fx.repo, 3)
	if err != nil || head != newHead.ID {
		t.Fatalf("reconciliation changed newer acceptance head: %q, %v", head, err)
	}
	run, err := db.FactoryRun(ctx, runID)
	if err != nil || !run.Reconciled || run.Outcome != factory.Cancelled {
		t.Fatalf("reconciled run = %+v, %v", run, err)
	}
	if _, err := coord.WithdrawAcceptance(ctx, fx.repo, 3, decision.ID, 7, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, store.ErrStaleRevision) {
		t.Fatalf("superseded withdrawal did not check the current head: %v", err)
	}
	if stopCalls != 2 {
		t.Fatal("stale withdrawal stopped a run again", stopCalls)
	}
}

func TestWithdrawAcceptancePreservesSettledRunHistory(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	decision := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, decision.ID)
	launchCalls := 0
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		launchCalls++
		if launchCalls == 1 {
			return project.FactoryState{}, errors.New("first launch never reached host")
		}
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryRunning}, nil
	}
	first := DispatchPass(ctx, fx.deps())
	if waitReason(first, 3) != WaitLaunchRefused {
		t.Fatalf("first attempt = %+v", first)
	}
	second := DispatchPass(ctx, fx.deps())
	if len(second.Launched) != 1 {
		t.Fatalf("retry = %+v", second)
	}
	assignment, err := db.Assignment(ctx, second.Launched[0].AssignmentID)
	if err != nil || len(assignment.RunHistory) != 2 || assignment.RunHistory[1] != second.Launched[0].RunID {
		t.Fatalf("expected two recorded attempts: %+v, %v", assignment, err)
	}
	historical, err := db.FactoryRun(ctx, assignment.RunHistory[0])
	if err != nil || !historical.Reconciled {
		t.Fatalf("first attempt is not settled: %+v, %v", historical, err)
	}
	// The first attempt is durably failed and immutable while the retry is
	// active. Withdrawal must preserve that recorded outcome and settle only
	// the active run.
	historicalOutcome, historicalSummary := historical.Outcome, historical.Summary
	host, broker := &stubHost{}, &stubBroker{}
	settleStubs(host, broker)
	withdrawalCustodyFixture(host, broker)
	host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
		return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryCompleted}, nil
	}
	var stopIDs []string
	confirmedStop := host.stop
	host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		reservation, err := db.Reservation(ctx, assignment.ID)
		if err != nil || reservation.State != factory.ReservationHeld {
			t.Errorf("historical run accounting released the latest run reservation before stop: %+v, %v", reservation, err)
		}
		stopIDs = append(stopIDs, in.ID)
		return confirmedStop(in)
	}
	coord := &Coordinator{Store: db, Host: host, Broker: broker}
	if _, err := coord.WithdrawAcceptance(ctx, fx.repo, 3, decision.ID, 7, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	old, err := db.FactoryRun(ctx, assignment.RunHistory[0])
	if err != nil || !old.Reconciled || old.Outcome != historicalOutcome || old.Summary != historicalSummary {
		t.Fatalf("withdrawal overwrote immutable historical outcome: %+v, %v", old, err)
	}
	latest, err := db.FactoryRun(ctx, assignment.RunHistory[1])
	if err != nil || !latest.Reconciled || latest.Outcome != factory.Cancelled {
		t.Fatalf("latest run was not settled: %+v, %v", latest, err)
	}
	if len(stopIDs) != 1 || stopIDs[0] != latest.ID {
		t.Fatalf("withdrawal stopped history instead of only the active run: %v", stopIDs)
	}
	current, err := db.Assignment(ctx, assignment.ID)
	if err != nil || !reflect.DeepEqual(current.RunHistory, assignment.RunHistory) {
		t.Fatalf("withdrawal changed recorded attempts: %+v, %v", current.RunHistory, err)
	}
}

// Declared broker metadata follows the current custody contract: sealing closes
// this execution and leaves no delivered binding/lease. The real coordinator and
// PostgreSQL still own retirement, accounting and replay; no live broker runs.
func withdrawalCustodyFixture(host *stubHost, broker *stubBroker) {
	confirmed := host.stop
	host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		state, err := confirmed(in)
		// Launch peers above declare no credential delivery. Retirement cannot
		// invent a credential return for that never-delegated run.
		state.CredentialReturned = false
		return state, err
	}
	broker.get = func(kind, id string) (identity.Execution, error) {
		state := identity.ExecutionPending
		if broker.closed[id] {
			state = identity.ExecutionTerminal
		}
		return identity.Execution{Kind: kind, ExecutionID: id, State: state, Digest: strings.Repeat("a", 64)}, nil
	}
}
