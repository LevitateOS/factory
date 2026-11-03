package control

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// rendezvousReads meets the first two accepted-input reads at a barrier
// and lets later reads through. Both passes check limits on their empty
// snapshot before either records, so the test deterministically exercises
// the concurrent-admission interleaving instead of racing for it.
type rendezvousReads struct {
	mu       sync.Mutex
	inputs   map[string]DispatchInputs
	arrivals int
	gate     chan struct{}
}

func (r *rendezvousReads) ReadDispatchInputs(_ context.Context, repository, issue string, _ []string, _ string) (DispatchInputs, error) {
	r.mu.Lock()
	r.arrivals++
	if r.arrivals == 2 {
		close(r.gate)
	}
	gate, gated := r.gate, r.arrivals <= 2
	r.mu.Unlock()
	if gated {
		<-gate
	}
	in, ok := r.inputs[repository+"/"+issue]
	if !ok {
		return DispatchInputs{}, &AcceptanceRefusal{Reason: RefusalIncompleteEvidence}
	}
	return in, nil
}

type mutexDispatchHost struct {
	mu       sync.Mutex
	launches int
	pin      project.FactoryHarnessPin
}

func (h *mutexDispatchHost) FactoryLaunch(_ context.Context, in project.FactoryLaunch) (project.FactoryState, error) {
	h.mu.Lock()
	h.launches++
	h.mu.Unlock()
	return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
}

func (h *mutexDispatchHost) FactoryInspect(context.Context, project.FactoryInspect) (project.FactoryState, error) {
	return project.FactoryState{}, host.ErrRunNotFound
}

func (*mutexDispatchHost) FactoryStop(_ context.Context, in project.FactoryStop) (project.FactoryState, error) {
	return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryStopped, Retirement: "confirmed"}, nil
}

func (h *mutexDispatchHost) FactoryHarness(context.Context, string) (project.FactoryHarnessPin, error) {
	return h.pin, nil
}

func (*mutexDispatchHost) ReadWorkspaceContext(_ context.Context, scoped execution.PreparationContextRequest) (execution.PreparationContext, error) {
	in, role := scoped.Input, scoped.Role
	return execution.PreparationContext{Material: fixturePreparationContext(in, role)}, nil
}

func TestConcurrentDispatchPassesPreserveCapacityAndBudget(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	if err := db.SaveCapacity(ctx, factory.Capacity{Revision: 1, UpdatedBy: 7, MaxConcurrentRuns: 1}); err != nil {
		t.Fatal(err)
	}
	oldHead := fx.accept(t, 3, "d333333333333333333333333")
	youngHead := fx.accept(t, 5, "d555555555555555555555555")
	now := time.Now()
	fx.queueAt(t, 3, oldHead.ID, now.Add(-2*time.Hour))
	fx.queueAt(t, 5, youngHead.ID, now.Add(-time.Hour))
	inputs := make(map[string]DispatchInputs, len(fx.reads.inputs))
	for key, in := range fx.reads.inputs {
		inputs[key] = in
	}
	coord := &Coordinator{Store: db}
	deps := DispatchDeps{
		Store:  db,
		Broker: &fakeDispatchBroker{},
		Reads:  &rendezvousReads{inputs: inputs, gate: make(chan struct{})},
		Host: &mutexDispatchHost{pin: project.FactoryHarnessPin{
			Harness: project.FactoryHarnessMuse, Version: fx.harness,
			SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64),
		}},
		Authority: coord.EffectiveAuthority,
	}
	reports := make([]DispatchReport, 2)
	var wg sync.WaitGroup
	for i := range reports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reports[i] = DispatchPass(ctx, deps)
		}()
	}
	wg.Wait()
	launched := len(reports[0].Launched) + len(reports[1].Launched)
	if launched != 1 {
		t.Fatalf("concurrent passes launched %d runs against capacity 1: %+v %+v", launched, reports[0], reports[1])
	}
	for i, report := range reports {
		if len(report.Errors) != 0 {
			t.Fatalf("pass %d errors: %+v", i, report.Errors)
		}
	}
	held, err := db.HeldReservations(ctx, store.MaxHeldReservations)
	if err != nil {
		t.Fatal(err)
	}
	planned := 0
	for _, r := range held {
		planned += r.PlannedMinutes
	}
	if len(held) != 1 || planned != 120 {
		t.Fatalf("held reservations = %d (%d planned minutes), want 1 (120)", len(held), planned)
	}
}

func (f *mutexDispatchHost) InspectRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectRun(ctx, address)
}

func (f *mutexDispatchHost) InspectTool(ctx context.Context, family string) (execution.ToolObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectTool(ctx, family)
}

func (f *mutexDispatchHost) RetireRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).RetireRun(ctx, address)
}

func (f *mutexDispatchHost) LaunchRun(ctx context.Context, launch execution.LaunchRequest) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).LaunchRun(ctx, launch)
}
