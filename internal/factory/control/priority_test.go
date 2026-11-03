package control

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

type priorityFixture struct {
	calls    []factory.PriorityRequest
	receipts []factory.PriorityReceipt
	choose   func(factory.PriorityRequest) factory.PriorityAnswer
}

func (f *priorityFixture) RecordPriorityJudgment(_ context.Context, r factory.PriorityReceipt) error {
	f.receipts = append(f.receipts, r)
	return nil
}

func (f *priorityFixture) PriorityJudgment(_ context.Context, snapshot string) (factory.PriorityReceipt, error) {
	for _, r := range f.receipts {
		if r.Snapshot == snapshot {
			return r, nil
		}
	}
	return factory.PriorityReceipt{}, store.ErrNotFound
}

func (f *priorityFixture) Choose(_ context.Context, r factory.PriorityRequest) (factory.PriorityAnswer, error) {
	f.calls = append(f.calls, r)
	return f.choose(r), nil
}

func priorityLast(r factory.PriorityRequest) factory.PriorityAnswer {
	answer := factory.PriorityAnswer{Selected: r.Candidates[len(r.Candidates)-1].ID, Model: "synthetic-model", Confidence: 1, Probabilities: map[string]float64{}}
	for _, c := range r.Candidates {
		answer.Probabilities[c.ID] = 0
	}
	answer.Probabilities[answer.Selected] = 1
	return answer
}

func priorityCandidates(count int, now time.Time) []factory.PriorityCandidate {
	var result []factory.PriorityCandidate
	for i := 0; i < count; i++ {
		result = append(result, factory.PriorityCandidate{ID: fmt.Sprint(i), Control: factory.IssueControl{FirstSeenUnix: now.Unix() + int64(i)}})
	}
	return result
}

func TestPriorityCompleteTournamentAndFiniteCalls(t *testing.T) {
	now := time.Now()
	f := &priorityFixture{choose: priorityLast}
	deps := &PriorityJudgments{Journal: f, Chooser: f, MaxCalls: 2}
	selected, err := selectPriority(context.Background(), deps, priorityCandidates(256, now), now)
	if err != nil || selected != "255" || len(f.calls) != 2 || len(f.receipts) != 1 {
		t.Fatalf("selection=%s error=%v calls=%d", selected, err, len(f.calls))
	}
	seen := map[string]bool{}
	for _, call := range f.calls {
		if len(call.Candidates) > 255 {
			t.Fatal("Choice overflow")
		}
		for _, c := range call.Candidates {
			seen[c.ID] = true
		}
	}
	if len(seen) != 256 {
		t.Fatalf("covered %d candidates", len(seen))
	}
	f = &priorityFixture{choose: priorityLast}
	deps.Journal, deps.Chooser, deps.MaxCalls = f, f, 1
	if _, err := selectPriority(context.Background(), deps, priorityCandidates(256, now), now); err == nil || len(f.calls) != 0 {
		t.Fatal("overbudget selection spent calls")
	}
}

func TestPriorityUncertaintyAgeAndSingleCandidate(t *testing.T) {
	now := time.Now()
	f := &priorityFixture{choose: func(r factory.PriorityRequest) factory.PriorityAnswer {
		a := priorityLast(r)
		a.Confidence = 0.1
		return a
	}}
	deps := &PriorityJudgments{Journal: f, Chooser: f}
	candidates := priorityCandidates(2, now)
	if chosen, err := selectPriority(context.Background(), deps, candidates, now); err != nil || chosen != "0" {
		t.Fatalf("uncertainty=%s %v", chosen, err)
	}
	candidates[0].Control.FirstSeenUnix = now.Add(-25 * time.Hour).Unix()
	// FA09: the aged replay still selects the due oldest item but
	// spends no new comparative call, so the total stays at the
	// first fresh run's single call.
	if chosen, err := selectPriority(context.Background(), deps, candidates, now); err != nil || chosen != "0" || len(f.calls) != 1 {
		t.Fatalf("age=%s %v calls=%d", chosen, err, len(f.calls))
	}
	if chosen, err := selectPriority(context.Background(), nil, candidates[:1], now); err != nil || chosen != "0" {
		t.Fatalf("single=%s %v", chosen, err)
	}
}

func TestPriorityDispatchSelectsAndRefusesStaleSet(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			ctx := context.Background()
			db, _ := dispatchTestDB(t)
			fx := dispatchSeed(t, db)
			old := fx.accept(t, 3, "d333333333333333333333333")
			young := fx.accept(t, 5, "d555555555555555555555555")
			fx.queueAt(t, 3, old.ID, time.Now().Add(-time.Hour))
			fx.queueAt(t, 5, young.ID, time.Now())
			f := &priorityFixture{choose: func(r factory.PriorityRequest) factory.PriorityAnswer {
				if stale {
					if err := db.SaveCapacity(ctx, factory.Capacity{Revision: 1, UpdatedBy: 7, MaxConcurrentRuns: 1}); err != nil {
						t.Fatal(err)
					}
				}
				return priorityLast(r)
			}}
			deps := fx.deps()
			deps.Priority = &PriorityJudgments{Journal: f, Chooser: f}
			// The D02 cursor admits only through its observe/refresh joints,
			// which production always wires; the stable revision mirrors the
			// seeded evidence bracket.
			deps.ObserveRevision = func(context.Context) (int64, bool, error) { return 41, true, nil }
			deps.RefreshCandidate = func(_ context.Context, control factory.IssueControl, _ int64) (factory.IssueControl, bool, error) {
				return control, true, nil
			}
			report := DispatchPass(ctx, deps)
			if stale {
				if len(report.Launched) != 0 || len(fx.host.launches) != 0 {
					t.Fatalf("stale launched: %+v", report)
				}
			} else if len(report.Launched) != 1 || report.Launched[0].Issue != 5 {
				t.Fatalf("priority launch: %+v", report)
			}
		})
	}
}

// TestPriorityCallCountBoundaries pins the FA04 derivation: 3,826 is
// the last inventory a 16-call budget funds, 3,827 needs 17, and the
// full 4,096-issue inventory needs exactly PriorityDefaultMaxCalls.
// A future inventory or group-size change must re-derive the budget.
func TestPriorityCallCountBoundaries(t *testing.T) {
	if got := priorityCallCount(3826); got != 16 {
		t.Fatalf("3826 calls=%d, want 16", got)
	}
	if got := priorityCallCount(3827); got != 17 {
		t.Fatalf("3827 calls=%d, want 17", got)
	}
	if got := priorityCallCount(4096); got != PriorityDefaultMaxCalls {
		t.Fatalf("4096 calls=%d, want default %d", got, PriorityDefaultMaxCalls)
	}
}

// TestPriorityDueOldestSkipsComparisons proves FA09: a starving oldest
// item resolves with zero comparative calls and zero reservation —
// not even a configured chooser is consulted — while fresh items
// still run the full tournament.
func TestPriorityDueOldestSkipsComparisons(t *testing.T) {
	now := time.Now()
	old := priorityCandidates(3, now.Add(-25*time.Hour))
	f := &priorityFixture{choose: priorityLast}
	selected, err := selectPriority(context.Background(), &PriorityJudgments{Journal: f, Chooser: f}, old, now)
	if err != nil || selected != "0" || len(f.calls) != 0 {
		t.Fatalf("selection=%s error=%v calls=%d", selected, err, len(f.calls))
	}
	if len(f.receipts) != 1 || f.receipts[0].Outcome != "fairness" || f.receipts[0].Selected != "0" || len(f.receipts[0].Calls) != 0 {
		t.Fatalf("fairness receipt: %+v", f.receipts)
	}
	chooseless := &priorityFixture{}
	selected, err = selectPriority(context.Background(), &PriorityJudgments{Journal: chooseless}, old, now)
	if err != nil || selected != "0" {
		t.Fatalf("chooseless fairness selection=%s error=%v", selected, err)
	}
	fresh := &priorityFixture{choose: priorityLast}
	if _, err := selectPriority(context.Background(), &PriorityJudgments{Journal: fresh, Chooser: fresh}, priorityCandidates(256, now), now); err != nil || len(fresh.calls) != 2 {
		t.Fatalf("fresh tournament error=%v calls=%d", err, len(fresh.calls))
	}
}

// TestPriorityFullInventoryTournament proves the default budget funds
// one tournament over the complete supported inventory, while an
// explicit 16-call bound still refuses it instead of truncating.
func TestPriorityFullInventoryTournament(t *testing.T) {
	now := time.Now()
	f := &priorityFixture{choose: priorityLast}
	selected, err := selectPriority(context.Background(), &PriorityJudgments{Journal: f, Chooser: f}, priorityCandidates(4096, now), now)
	if err != nil || selected != "4095" || len(f.calls) != PriorityDefaultMaxCalls {
		t.Fatalf("selection=%s error=%v calls=%d", selected, err, len(f.calls))
	}
	capped := &priorityFixture{choose: priorityLast}
	if _, err := selectPriority(context.Background(), &PriorityJudgments{Journal: capped, Chooser: capped, MaxCalls: 16}, priorityCandidates(4096, now), now); err != ErrPriorityUnavailable {
		t.Fatalf("explicit 16-call bound did not refuse the full inventory: %v", err)
	}
}
