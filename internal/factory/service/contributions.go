package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
	"github.com/levitateos/sodaos/internal/store"
)

// Contributions is the bounded continuation for already admitted running roles.
// A nil chooser waits without claiming. Construction and reads start no work.
type Contributions struct {
	Store   *store.Store
	Chooser control.ProductJudgment
}

var ErrContributionsUnavailable = errors.New("contribution store is unavailable")

type contributionAnswer struct {
	call factory.ContributionCall
	err  error
}

// Drive claims at most one source. Claimed=true reports ownership of that
// assessment, not an invitation; the persisted assessment is authoritative.
func (s Contributions) Drive(ctx context.Context) (claimed bool, err error) {
	if s.Chooser == nil {
		return false, nil
	}
	if s.Store == nil {
		return false, ErrContributionsUnavailable
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ids, err := s.Store.ContributionSources(ctx)
	if err != nil {
		return false, err
	}
	var lastError error
	for _, id := range ids {
		review, err := s.Store.ContributionContext(ctx, id)
		if err != nil {
			lastError = err
			continue
		}
		if len(review.Roles) == 0 {
			continue
		}
		owner, err := s.Store.OwnContributions(ctx)
		if err != nil {
			return false, err
		}
		claimed, err := owner.ClaimContributionReview(ctx, review)
		if err != nil {
			_ = owner.Close()
			lastError = err
			continue
		}
		if !claimed {
			_ = owner.Close()
			continue
		}
		defer owner.Close()
		return true, s.assess(ctx, owner, &review)
	}
	return false, lastError
}

func (s Contributions) assess(ctx context.Context, owner *store.ContributionOwner, review *factory.ContributionReview) error {
	callctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	calls, err := judgeContributions(callctx, s.Chooser, *review)
	review.Calls = calls
	// Replies can finish concurrently with a shutdown/deadline. Completed
	// answers do not authorize a late invitation after the review was stopped.
	if stopped := callctx.Err(); stopped != nil {
		err = errors.Join(err, stopped)
	}
	if err == nil {
		factory.SelectContribution(review)
	} else {
		review.State = "unavailable"
	}
	settlectx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer settleCancel()
	return errors.Join(err, owner.SettleContributionReview(settlectx, *review))
}

func judgeContributions(ctx context.Context, judge control.ProductJudgment, review factory.ContributionReview) ([]factory.ContributionCall, error) {
	// All prompts and state are fixed before launching the independent calls.
	// Each role gets the same binary judgment shape used in evaluation; role
	// selection afterward cannot dilute its probability across other candidates.
	type request struct {
		role     factory.ContributionRole
		question string
		state    any
	}
	requests := []request{}
	for _, role := range review.Roles {
		state := map[string]any{"source": map[string]any{"body": review.Body, "actor": review.SourceActor, "context": review.Context, "revision": review.Revision}, "existing_replies": review.Replies, "eligible_running_role": role}
		for _, question := range factory.ContributionQuestions {
			requests = append(requests, request{role, question, state})
		}
	}
	results := make([]contributionAnswer, len(requests))
	var wg sync.WaitGroup
	for index, input := range requests {
		wg.Add(1)
		go func(index int, input request) {
			defer wg.Done()
			answer, err := judge.Select(ctx, input.state, input.question, map[string]any{
				"contribute":      "This role can add a specific useful missing correction, risk or evidence within its admitted work.",
				"no_contribution": "A reply would be redundant, courtesy, ambiguous, already answered, irrelevant or outside its admitted work.",
			})
			results[index] = contributionAnswer{factory.ContributionCall{Assignment: input.role.Assignment, Question: input.question, Answer: answer}, err}
		}(index, input)
	}
	wg.Wait()
	calls := []factory.ContributionCall{}
	var failure error
	for _, result := range results {
		if result.err != nil {
			failure = result.err
		} else {
			calls = append(calls, result.call)
		}
	}
	return calls, failure
}
