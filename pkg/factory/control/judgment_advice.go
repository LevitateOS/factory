package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// PurposeAdvice is one typed judgment call. Admit checks the actual work scope
// and allowance before a new call. Completed choices are reused by input key;
// no placeholder judgment is written before the provider answers.
type PurposeAdvice struct {
	Repository int64
	Purpose    factory.JudgmentPurpose
	State      any
	Question   string
	Options    map[string]any
	Outcome    string
	Candidates []factory.PriorityCandidate
	Admit      func(context.Context, string) (bool, error)
}

// ErrConsultationFailed reports a provider call that failed or returned an invalid
// answer. Nothing is recorded, so the next pass asks again; callers hold for
// now instead of requiring attention.
var ErrConsultationFailed = fmt.Errorf("%w: consultation failed, the next pass retries", ErrPriorityUnavailable)

func buildPurposeRequest(advice PurposeAdvice) (factory.PriorityRequest, string, error) {
	fingerprint, err := factory.JudgmentFingerprint(advice.Purpose, advice.State, advice.Question, advice.Options)
	if err != nil {
		return factory.PriorityRequest{}, "", err
	}
	stateBytes, err := json.Marshal(advice.State)
	if err != nil {
		return factory.PriorityRequest{}, "", err
	}
	request := factory.PriorityRequest{Snapshot: fingerprint, Rubric: advice.Question, State: stateBytes, Criteria: advice.Options}
	for id := range advice.Options {
		request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: id})
	}
	return request, fingerprint, nil
}

func (c *Coordinator) consultPurposeChoice(ctx context.Context, advice PurposeAdvice, fingerprint string, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	answer, callErr := SelectProductJudgment(bounded, c.Planning.Chooser, advice.Repository, "purpose:"+fingerprint, advice.State, advice.Question, advice.Options)
	cancel()
	if callErr != nil {
		return factory.PriorityAnswer{}, ErrConsultationFailed
	}
	if admitted, err := advice.Admit(ctx, fingerprint); err != nil || !admitted {
		return factory.PriorityAnswer{}, errors.Join(ErrPriorityUnavailable, err)
	}
	call := factory.PriorityCall{Request: request, Answer: answer}
	candidates := request.Candidates
	if len(advice.Candidates) > 0 {
		candidates = advice.Candidates
	}
	receipt := factory.PriorityReceipt{Snapshot: fingerprint, Purpose: advice.Purpose, Candidates: candidates, Calls: []factory.PriorityCall{call}, Selected: answer.Selected, Outcome: advice.Outcome, CreatedUnix: time.Now().Unix()}
	if err := c.Store.RecordPriorityJudgment(ctx, receipt); err != nil {
		return factory.PriorityAnswer{}, err
	}
	return answer, nil
}

func (c *Coordinator) AdvisePurpose(ctx context.Context, advice PurposeAdvice) (factory.PriorityAnswer, bool, error) {
	if c.Store == nil || advice.Admit == nil || advice.Outcome == "" {
		return factory.PriorityAnswer{}, false, ErrPriorityUnavailable
	}
	request, fingerprint, err := buildPurposeRequest(advice)
	if err != nil {
		return factory.PriorityAnswer{}, false, err
	}
	admitted, err := advice.Admit(ctx, fingerprint)
	if err != nil || !admitted {
		return factory.PriorityAnswer{}, false, errors.Join(ErrPriorityUnavailable, err)
	}
	prior, err := c.Store.PriorityJudgment(ctx, fingerprint)
	if err == nil {
		if c.Planning != nil {
			if retained, ok := c.Planning.Chooser.(interface {
				CheckJudgment(context.Context, int64, string) error
			}); ok {
				if err := retained.CheckJudgment(ctx, advice.Repository, "purpose:"+fingerprint); err != nil {
					return factory.PriorityAnswer{}, false, err
				}
			}
		}
		return prior.Calls[0].Answer, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return factory.PriorityAnswer{}, false, err
	}
	if c.Planning == nil || c.Planning.Chooser == nil {
		return factory.PriorityAnswer{}, false, ErrPriorityUnavailable
	}
	answer, err := c.consultPurposeChoice(ctx, advice, fingerprint, request)
	if err != nil {
		return factory.PriorityAnswer{}, false, err
	}
	return answer, false, nil
}
