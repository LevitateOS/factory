package control

import (
	"context"

	"github.com/levitateos/factory/pkg/factory"
)

// DeliveryModel names the configured request selector. The exact resolved
// provider model is separately retained on the answer beside that selector.
func (p *TypeSafePriority) DeliveryModel() string {
	return systemOneModel(p.Model)
}

func (p *TypeSafePriority) ValidateDeliveryState(state any) error {
	_, err := deliveryJudgmentInput(state, p.DeliveryModel())
	return err
}

func (p *TypeSafePriority) SelectDelivery(ctx context.Context, state any, question string, choices map[string]any) (factory.DeliveryAnswer, error) {
	return selectDeliveryAnswer(ctx, p, p.DeliveryModel(), state, question, choices)
}

// RepositoryDeliveryJudgment binds the selected model and spending source before
// material is fingerprinted. A bound chooser must refuse a changed policy.
type RepositoryDeliveryJudgment interface {
	BindDeliveryJudgment(context.Context, int64) (ProductJudgment, string, int64, error)
}

type boundDeliveryJudgment struct {
	ProductJudgment
	model string
}

func (p boundDeliveryJudgment) DeliveryModel() string { return p.model }

func (p boundDeliveryJudgment) SelectDelivery(ctx context.Context, state any, question string, choices map[string]any) (factory.DeliveryAnswer, error) {
	return selectDeliveryAnswer(ctx, p.ProductJudgment, p.model, state, question, choices)
}

func (c *Coordinator) deliveryChooser(ctx context.Context, repository int64) (deliveryProductionChooser, int64, error) {
	if c.Planning == nil {
		return nil, 0, ErrDeliveryUnavailable
	}
	if provider, ok := c.Planning.Chooser.(RepositoryDeliveryJudgment); ok {
		chooser, model, revision, err := provider.BindDeliveryJudgment(ctx, repository)
		if err != nil || chooser == nil || model == "" || revision < 1 {
			return nil, 0, ErrDeliveryUnavailable
		}
		return boundDeliveryJudgment{chooser, model}, revision, nil
	}
	chooser, ok := c.Planning.Chooser.(deliveryProductionChooser)
	if !ok {
		return nil, 0, ErrDeliveryUnavailable
	}
	return chooser, 0, nil
}

func selectDeliveryAnswer(ctx context.Context, chooser ProductJudgment, model string, state any, question string, choices map[string]any) (factory.DeliveryAnswer, error) {
	input, err := deliveryJudgmentInput(state, model)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	fingerprint, err := factory.DeliveryFingerprint(input)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	answer, err := chooser.Select(ctx, state, question, choices)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	result := factory.DeliveryAnswer{Fingerprint: fingerprint, Selected: answer.Selected, Model: input.Model, ProviderModel: answer.Model, Probabilities: answer.Probabilities, Probability: answer.Probabilities[answer.Selected], Confidence: answer.Confidence}
	return result, nil
}
