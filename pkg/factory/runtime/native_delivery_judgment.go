package runtime

import (
	"context"
	"reflect"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

var _ control.RepositoryDeliveryJudgment = NativeJudgments{}

// BindDeliveryJudgment captures the selected repository policy before the Go
// delivery owner composes and fingerprints its complete semantic evidence.
func (s NativeJudgments) BindDeliveryJudgment(ctx context.Context, repository int64) (control.ProductJudgment, string, int64, error) {
	policy, err := s.Store.RepositoryPolicy(ctx, repository)
	if err != nil || policy.Judgments == nil || policy.Judgments.Validate() != nil || policy.Judgments.Account == "" {
		return nil, "", 0, control.ErrDeliveryUnavailable
	}
	chooser := nativeBoundDeliveryJudgment{NativeExecution: s.NativeExecution, policy: policy}
	return chooser, policy.Judgments.Model, policy.Revision, nil
}

type nativeBoundDeliveryJudgment struct {
	NativeExecution
	policy factory.RepositoryPolicy
}

func (s nativeBoundDeliveryJudgment) Select(ctx context.Context, state any, question string, choices map[string]any) (factory.PriorityAnswer, error) {
	prompt, err := factory.NativeJudgmentPrompt(state, question, choices)
	if err != nil {
		return factory.PriorityAnswer{}, err
	}
	chooser := nativeRepositoryJudgment{NativeExecution: s.NativeExecution, repository: s.policy.Repository, subject: "delivery:" + factory.MCPInputDigest([]byte(prompt))}
	chooser.admit = func(ctx context.Context) error {
		current, err := s.Store.RepositoryPolicy(ctx, s.policy.Repository)
		if err != nil || current.Revision != s.policy.Revision || !reflect.DeepEqual(current.Judgments, s.policy.Judgments) {
			return control.ErrDeliveryUnavailable
		}
		account, err := (NativeInputSource{s.NativeExecution}).account(ctx, strconv.FormatInt(s.policy.Repository, 10), factory.OpMerge)
		if err != nil {
			return err
		}
		return account.visible(ctx, account.binding.Actor.ID, account.repository.ID)
	}
	request := factory.PriorityRequest{}
	keys := make([]string, 0, len(choices))
	for key := range choices {
		keys = append(keys, key)
		request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: key})
	}
	return chooser.selectPrompt(ctx, prompt, keys, request)
}
