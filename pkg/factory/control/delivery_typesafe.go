package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// DeliveryModel names the configured request selector. The exact resolved
// provider model is separately retained on the answer beside that selector.
func (p *TypeSafePriority) DeliveryModel() string {
	if p.Model == "" {
		return "jev-latest"
	}
	return p.Model
}

func (p *TypeSafePriority) ValidateDeliveryState(state any) error {
	material, ok := state.(*deliveryJudgmentMaterial)
	if !ok || material == nil || material.Input.Model != p.DeliveryModel() {
		return errors.New("delivery judgment requires complete bound semantic material")
	}
	return material.validate()
}

func (p *TypeSafePriority) SelectDelivery(ctx context.Context, state any, question string, choices map[string]any) (factory.DeliveryAnswer, error) {
	if err := p.ValidateDeliveryState(state); err != nil {
		return factory.DeliveryAnswer{}, err
	}
	material := state.(*deliveryJudgmentMaterial)
	fingerprint, err := factory.DeliveryFingerprint(material.Input)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	answer, err := p.Select(ctx, material, question, choices)
	if err != nil {
		return factory.DeliveryAnswer{}, err
	}
	result := factory.DeliveryAnswer{Fingerprint: fingerprint, Selected: answer.Selected, Model: material.Input.Model, ProviderModel: answer.Model, Probabilities: answer.Probabilities, Probability: answer.Probabilities[answer.Selected], Confidence: answer.Confidence}
	return result, nil
}
