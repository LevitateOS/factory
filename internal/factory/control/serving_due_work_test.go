package control

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

type dueReadinessSource struct {
	evidence AcceptanceEvidence
	calls    atomic.Int32
	passed   chan struct{}
}

func (s *dueReadinessSource) ReadAcceptanceEvidence(_ context.Context, _, issue string, _ []string) (AcceptanceEvidence, error) {
	if issue == "4" {
		select {
		case s.passed <- struct{}{}:
		default:
		}
		evidence := s.evidence
		evidence.Issue.Index = issue
		return evidence, nil
	}
	s.calls.Add(1)
	return s.evidence, nil
}

func TestServingOwnerResumesFutureReadinessBackoffWithoutRedelivery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := coordinatorFixture(t, nil, nil)
	source := &dueReadinessSource{evidence: readinessEvidence(12, readinessView("3"), nil, nil), passed: make(chan struct{}, 1)}
	c.AcceptanceReads, c.Readiness = source, &fakeObserver{revision: 12, idle: true}
	grantFullAuthority(t, c)
	hint := readinessHint("future-serving-readiness", 42, 3)
	id := readinessDeliverySourceID(hint.Delivery)
	if err := c.Store.EnqueueReadinessWork(ctx, id, hint.Delivery, factory.DependenceRef{Repository: 42, Issue: 3}); err != nil {
		t.Fatal(err)
	}
	// Keep the source deferred through the first serving tick. Only the
	// existing durable retry time makes it due on the following tick.
	if err := c.Store.DeferReadinessWork(ctx, id, time.Now().Add(17*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, filepath.Join(t.TempDir(), "coordinator.lock")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	// An independent ordinary source inserted after Start proves that the
	// timer owner actually visited due work while this source remained deferred.
	if err := c.Store.EnqueueReadinessWork(ctx, "observe-serving-pass", "", factory.DependenceRef{Repository: 42, Issue: 4}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.passed:
	case <-time.After(15 * time.Second):
		t.Fatal("serving timer did not assess the independent due source")
	}
	c.progressMu.Lock()
	if source.calls.Load() != 0 {
		c.progressMu.Unlock()
		t.Fatal("serving owner read native evidence before durable backoff elapsed")
	}
	c.progressMu.Unlock()
	deadline := time.Now().Add(15 * time.Second)
	for {
		seen, err := c.Store.IntakeDeliverySeen(ctx, hint.Delivery)
		if err != nil {
			t.Fatal(err)
		}
		if seen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("due readiness did not resume without redelivery or a manual pass")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	control, err := c.Store.IssueControl(ctx, 42, 3)
	if err != nil || control.NativeRev != 12 || source.calls.Load() != 1 {
		t.Fatal("due source was not assessed exactly once", control, source.calls.Load(), err)
	}
}

type capacityReleaseHost struct {
	*fakeDispatchHost
	mu      sync.Mutex
	states  map[string]project.FactoryState
	started chan project.FactoryLaunch
}

func (h *capacityReleaseHost) FactoryLaunch(_ context.Context, in project.FactoryLaunch) (project.FactoryState, error) {
	state := project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryRunning, Live: true, Delivered: true, LeaseID: "lease-" + in.Run.ID, Generation: 1, Container: strings.Repeat("c", 64), Invocation: in.Run.ID, UID: 1001, GID: 1002, Login: in.Run.Role, Unit: "fixture-" + in.Run.ID}
	h.mu.Lock()
	h.states[in.Run.ID] = state
	h.mu.Unlock()
	h.started <- in
	return state, nil
}

func (h *capacityReleaseHost) FactoryInspect(_ context.Context, in project.FactoryInspect) (project.FactoryState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.states[in.ID], nil
}

func (h *capacityReleaseHost) FactoryStop(ctx context.Context, in project.FactoryStop) (project.FactoryState, error) {
	return h.FactoryInspect(ctx, project.FactoryInspect{ID: in.ID})
}

func TestServingOwnerPicksUpQueuedIssueAfterNativeCapacityRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	capacity, err := db.Capacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	capacity.MaxConcurrentRuns = 1
	if err := db.SaveCapacity(ctx, capacity); err != nil {
		t.Fatal(err)
	}
	for _, issue := range []int64{3, 5} {
		decision := fx.accept(t, issue, "d"+factory.NewID()[:24])
		fx.queueAt(t, issue, decision.ID, time.Now().Add(time.Duration(issue-10)*time.Second))
	}
	host := &capacityReleaseHost{fakeDispatchHost: fx.host, states: make(map[string]project.FactoryState), started: make(chan project.FactoryLaunch, 2)}
	broker := &stubBroker{get: func(kind, id string) (identity.Execution, error) {
		state, _ := host.FactoryInspect(ctx, project.FactoryInspect{ID: id})
		phase := identity.ExecutionLive
		if !state.Live {
			phase = identity.ExecutionTerminal
		}
		return identity.Execution{Kind: kind, ExecutionID: id, State: phase, Digest: strings.Repeat("d", 64), LeaseID: state.LeaseID, Binding: &identity.Binding{Kind: kind, ID: id, Generation: 1, Project: state.Container, InvocationID: state.Invocation, UID: state.UID, GID: state.GID, Login: state.Login, CredentialRoot: "/home/coder/runs/" + id}}, nil
	}, close: func(string, string) error { return nil }}
	c := NewCoordinator(db, host, broker)
	c.DispatchReads = fx.reads
	if err := c.Start(ctx, filepath.Join(t.TempDir(), "coordinator.lock")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	await := func() project.FactoryLaunch {
		t.Helper()
		select {
		case launch := <-host.started:
			return launch
		case <-time.After(15 * time.Second):
			_ = c.Close()
			for _, issue := range []int64{3, 5} {
				owner, err := db.LatestIssueAssignment(ctx, fx.repo, issue)
				if err != nil {
					t.Logf("issue=%d assignment read=%v", issue, err)
					continue
				}
				run, _ := db.FactoryRun(ctx, owner.Run)
				reservation, _ := db.Reservation(ctx, owner.ID)
				allowance, _ := db.AttemptAllowance(ctx, fx.repo, issue)
				t.Logf("issue=%d assignment=%s/%s run=%s reconciled=%t reservation=%s root_active=%t root_closed=%t", issue, owner.Stage, owner.Reason, run.Outcome, run.Reconciled, reservation.State, allowance.Active, allowance.Closed)
			}
			t.Fatal("serving owner did not launch due work")
			return project.FactoryLaunch{}
		}
	}
	first := await()
	owner, err := db.AssignmentByRun(ctx, first.Run.ID)
	if err != nil || owner.Issue != 3 {
		t.Fatal("oldest issue was not admitted first", owner, err)
	}
	if _, err := db.LatestIssueAssignment(ctx, fx.repo, 5); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("second issue crossed the occupied capacity limit or could not be observed", err)
	}
	// Change only retained native terminal truth. The serving owner must
	// settle custody, close the failed root and admit the waiting issue.
	host.mu.Lock()
	state := host.states[first.Run.ID]
	state.Live, state.CredentialReturned = false, true
	state.Phase, state.Retirement = project.FactoryFailed, "confirmed"
	state.Reason = "native execution failed"
	host.states[first.Run.ID] = state
	host.mu.Unlock()
	second := await()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := db.AssignmentByRun(ctx, second.Run.ID)
	if err != nil || next.Issue != 5 || next.ID == owner.ID {
		t.Fatal("released capacity did not admit the queued issue", next, err)
	}
	settled, err := db.FactoryRun(ctx, first.Run.ID)
	if err != nil || !settled.Reconciled || settled.Outcome != factory.Failed {
		t.Fatal("native failure was not normally settled", settled, err)
	}
	finished, err := db.Assignment(ctx, owner.ID)
	if err != nil || finished.Outcome != factory.Failed || finished.Result == nil || finished.Result.Status != "failed" {
		t.Fatal("native failure was not accounted", finished, err)
	}
	allowance, err := db.AttemptAllowance(ctx, fx.repo, 3)
	if err != nil || !allowance.Closed || allowance.Active {
		t.Fatal("failed root retained capacity", allowance, err)
	}
}

func (f *capacityReleaseHost) InspectRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectRun(ctx, address)
}

func (f *capacityReleaseHost) RetireRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).RetireRun(ctx, address)
}

func (f *capacityReleaseHost) LaunchRun(ctx context.Context, launch execution.LaunchRequest) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).LaunchRun(ctx, launch)
}

func (f *capacityReleaseHost) ExportCandidate(ctx context.Context, request execution.CandidateRequest) (execution.CandidateBundle, error) {
	return host.NewFactoryObserver(f, nil).ExportCandidate(ctx, request)
}
