package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// settleRunFixture stages one dispatched coder attempt whose host run just
// settled with the given outcome, before any settlement accounting ran.
func settleRunFixture(t *testing.T, outcome factory.Outcome, summary string) (context.Context, *store.Store, factory.Assignment, factory.Run) {
	t.Helper()
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 1 {
		t.Fatalf("launched = %+v", report.Launched)
	}
	a, err := db.Assignment(ctx, report.Launched[0].AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := db.FactoryRun(ctx, report.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = outcome, summary, true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	return ctx, db, a, run
}

// TestSettlementConcurrentDoubleFinish proves the R11 once-charged joint
// under a real race: two concurrent settlements of the same run produce
// one finished assignment and one charge. The loser replays safely instead
// of double-charging or duplicating the allowance transition.
func TestSettlementConcurrentDoubleFinish(t *testing.T) {
	ctx, db, a, run := settleRunFixture(t, factory.Failed, "host says so")

	const racers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	acked := make([]bool, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, acked[i] = AccountSettledRun(ctx, db, run, time.Now())
		}(i)
	}
	close(start)
	wg.Wait()

	confirmed := 0
	for _, ok := range acked {
		if ok {
			confirmed++
		}
	}
	if confirmed < 1 {
		t.Fatal("no racer accounted the settled run")
	}
	finished, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.Failed {
		t.Fatalf("assignment = %+v, want one finished failed write", finished)
	}
	if _, err := db.RunUsage(ctx, run.ID); err != nil {
		t.Fatalf("usage row: %v", err)
	}
	total, err := db.UsageTotal(ctx, a.Repository, a.Connection)
	if err != nil {
		t.Fatal(err)
	}
	if reservation, err := db.Reservation(ctx, a.ID); err != nil || reservation.State != factory.ReservationConsumed {
		t.Fatalf("reservation = %+v %v, want consumed once", reservation, err)
	}
	// A sequential replay after the race settles nothing twice.
	if again, ok := AccountSettledRun(ctx, db, run, time.Now()); ok || again.Stage != "" {
		t.Fatalf("replay = %+v %v, want silent refusal", again, ok)
	}
	if replayed, err := db.UsageTotal(ctx, a.Repository, a.Connection); err != nil || replayed != total {
		t.Fatalf("usage total = %d, want stable %d", replayed, total)
	}
}

// TestSettlementConsumeThenCrashReplay proves the R11 partial-settlement
// joint: usage recorded and the reservation consumed, then a crash before
// the terminal write. The recovery replay completes the same settlement
// without a second charge.
func TestSettlementConsumeThenCrashReplay(t *testing.T) {
	ctx, db, a, run := settleRunFixture(t, factory.Failed, "host says so")
	now := time.Now()

	// Crash point: confirmed usage plus consumed reservation, no finish.
	if err := recordConfirmedUsage(ctx, db, a, run, now); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if current, err := db.Assignment(ctx, a.ID); err != nil || current.Stage != factory.AssignmentAssigned {
		t.Fatalf("assignment = %+v %v, want still assigned before replay", current, err)
	}

	finished, ok := AccountSettledRun(ctx, db, run, now)
	if !ok || finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.Failed {
		t.Fatalf("replay = %+v %v, want the completed failed settlement", finished, ok)
	}
	total, err := db.UsageTotal(ctx, a.Repository, a.Connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunUsage(ctx, run.ID); err != nil {
		t.Fatalf("usage row: %v", err)
	}
	if again, ok := AccountSettledRun(ctx, db, run, time.Now()); ok || again.Stage != "" {
		t.Fatalf("second replay = %+v %v, want silent refusal", again, ok)
	}
	if replayed, err := db.UsageTotal(ctx, a.Repository, a.Connection); err != nil || replayed != total {
		t.Fatalf("usage total = %d, want stable %d", replayed, total)
	}
}
