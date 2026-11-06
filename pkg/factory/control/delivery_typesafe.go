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
	input, err := deliveryJudgmentInput(state, p.DeliveryModel())
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	fingerprint, err := factory.DeliveryFingerprint(input)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	answer, err := p.Select(ctx, state, question, choices)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	result := factory.DeliveryAnswer{Fingerprint: fingerprint, Selected: answer.Selected, Model: input.Model, ProviderModel: answer.Model, Probabilities: answer.Probabilities, Probability: answer.Probabilities[answer.Selected], Confidence: answer.Confidence}
	return result, nil
}
