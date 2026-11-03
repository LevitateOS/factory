package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// PriorityJournal retains completed choices for unchanged candidate sets.
type PriorityJournal interface {
	RecordPriorityJudgment(context.Context, factory.PriorityReceipt) error
	PriorityJudgment(context.Context, string) (factory.PriorityReceipt, error)
}

type PriorityChooser interface {
	Choose(context.Context, factory.PriorityRequest) (factory.PriorityAnswer, error)
}

type PriorityJudgments struct {
	Journal       PriorityJournal
	Chooser       PriorityChooser
	MaxCalls      int
	StarvationAge time.Duration
}

var ErrPriorityUnavailable = errors.New("priority consultation unavailable or exhausted")

// PriorityDefaultMaxCalls bounds one tournament over the largest
// supported inventory. priorityCallCount(4096) is 18: seventeen
// 255-candidate groups plus the final for the 4,096-issue control
// reads. A future inventory or group-size change must re-derive
// this budget; TestPriorityCallCountBoundaries pins the derivation.
const PriorityDefaultMaxCalls = 18

// selectPriority covers every candidate through a deterministic tournament.
// Groups contain at most 255 alternatives; each winner advances until a final
// choice. The required call count is checked up front; an oversized set refuses,
// never truncated. Uncertain preference falls back to oldest within its group.
func selectPriority(ctx context.Context, deps *PriorityJudgments, candidates []factory.PriorityCandidate, now time.Time) (string, error) {
	if len(candidates) == 0 {
		return "", nil
	}
	if len(candidates) == 1 {
		return candidates[0].ID, nil
	}
	if deps == nil || deps.Journal == nil {
		return "", ErrPriorityUnavailable
	}
	t := &priorityTournament{ctx: ctx, deps: deps, candidates: candidates, now: now}
	selected, replayed, err := t.admitTournament()
	if err != nil || replayed {
		return selected, err
	}
	if t.fairness {
		// A due oldest item skips comparative spending: the age rule
		// already determines the selection, so no judgment call can
		// affect it. The call-free fairness receipt records the
		// deterministic reason; final admission still happens at
		// dispatch through the serving layer.
		t.receipt.Selected, t.receipt.Outcome = t.oldest.ID, "fairness"
		if err := t.deps.Journal.RecordPriorityJudgment(t.ctx, t.receipt); err != nil {
			return "", err
		}
		return t.oldest.ID, nil
	}
	if err := t.checkTournamentSize(); err != nil {
		return "", err
	}
	return t.runTournament()
}

// priorityTournament carries the shared state behind one priority
// tournament so each duty reads through one small check.
type priorityTournament struct {
	ctx        context.Context
	deps       *PriorityJudgments
	candidates []factory.PriorityCandidate
	now        time.Time
	snapshot   string
	oldest     factory.PriorityCandidate
	receipt    factory.PriorityReceipt
	fairness   bool
}

// admitTournament snapshots the candidates and replays an unchanged
// recorded selection, reporting replayed when the outcome stands.
func (t *priorityTournament) admitTournament() (string, bool, error) {
	snapshot, err := factory.PrioritySnapshot(t.candidates)
	if err != nil {
		return "", false, err
	}
	oldest := t.candidates[0]
	for _, c := range t.candidates {
		if c.Control.FirstSeenUnix < oldest.Control.FirstSeenUnix {
			oldest = c
		}
	}
	age := t.deps.StarvationAge
	if age <= 0 {
		age = 24 * time.Hour
	}
	t.snapshot = snapshot
	t.oldest = oldest
	t.receipt = factory.PriorityReceipt{Snapshot: snapshot, Purpose: factory.JudgmentNextWork, Candidates: t.candidates, CreatedUnix: t.now.Unix(), Calls: []factory.PriorityCall{}}
	t.fairness = oldest.Control.FirstSeenUnix > 0 && t.now.Sub(time.Unix(oldest.Control.FirstSeenUnix, 0)) >= age
	previous, readErr := t.deps.Journal.PriorityJudgment(t.ctx, snapshot)
	if readErr != nil && !errors.Is(readErr, store.ErrNotFound) {
		return "", false, readErr
	}
	if readErr == nil && (previous.Outcome == "selected" || previous.Outcome == "fairness") {
		for _, candidate := range t.candidates {
			if candidate.ID == previous.Selected {
				if t.fairness {
					return oldest.ID, true, nil
				}
				return previous.Selected, true, nil
			}
		}
	}
	return "", false, nil
}

// checkTournamentSize bounds the actual calls required by the candidate set.
func (t *priorityTournament) checkTournamentSize() error {
	maxCalls := t.deps.MaxCalls
	if maxCalls <= 0 {
		maxCalls = PriorityDefaultMaxCalls
	}
	required := priorityCallCount(len(t.candidates))
	if required > maxCalls || t.deps.Chooser == nil {
		return ErrPriorityUnavailable
	}
	return nil
}

// runTournament judges groups of at most 255 alternatives until one
// final choice remains, recording the receipt behind it.
func (t *priorityTournament) runTournament() (string, error) {
	remaining := t.candidates
	for len(remaining) > 1 {
		var winners []factory.PriorityCandidate
		for offset := 0; offset < len(remaining); offset += 255 {
			end := offset + 255
			if end > len(remaining) {
				end = len(remaining)
			}
			group := remaining[offset:end]
			if len(group) == 1 {
				winners = append(winners, group[0])
				continue
			}
			winner, err := t.judgePriorityGroup(group)
			if err != nil {
				return "", err
			}
			winners = append(winners, winner)
		}
		remaining = winners
	}
	t.receipt.Selected, t.receipt.Outcome = remaining[0].ID, "selected"
	if t.fairness {
		t.receipt.Selected, t.receipt.Outcome = t.oldest.ID, "fairness"
	}
	if err := t.deps.Journal.RecordPriorityJudgment(t.ctx, t.receipt); err != nil {
		return "", err
	}
	return t.receipt.Selected, nil
}

// judgePriorityGroup judges one group, falling back to oldest on
// uncertain preference.
func (t *priorityTournament) judgePriorityGroup(group []factory.PriorityCandidate) (factory.PriorityCandidate, error) {
	var none factory.PriorityCandidate
	request := factory.PriorityRequest{Snapshot: t.snapshot, Candidates: group, Rubric: factory.PriorityRubric}
	bounded, cancel := context.WithTimeout(t.ctx, 30*time.Second)
	answer, callErr := t.deps.Chooser.Choose(bounded, request)
	cancel()
	if callErr == nil {
		callErr = answer.Validate(request)
	}
	call := factory.PriorityCall{Request: request, Answer: answer}
	if callErr != nil {
		return none, ErrConsultationFailed
	}
	t.receipt.Calls = append(t.receipt.Calls, call)
	chosen := group[0]
	for _, c := range group {
		if answer.Confidence < 0.5 {
			if c.Control.FirstSeenUnix < chosen.Control.FirstSeenUnix {
				chosen = c
			}
		} else if c.ID == answer.Selected {
			chosen = c
		}
	}
	return chosen, nil
}

func priorityCallCount(count int) int {
	calls := 0
	for count > 1 {
		calls += count / 255
		if count%255 > 1 {
			calls++
		}
		count = (count + 254) / 255
	}
	return calls
}
