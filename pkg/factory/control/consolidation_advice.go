package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

const consolidationRubric = "Compare the supplied product alternatives against the requested objective, facts and unknowns. Choose the one coherent alternative whose walkthrough best delivers the requested outcomes within stated constraints. Preserve the full objective; declining one alternative approves nothing else. Honor the supplied exclusions: never select a walkthrough that reintroduces declined behavior. Judge only supplied facts; grant no authority, access or spending."

type consolidationEvidence struct {
	Objective    string                       `json:"objective"`
	Facts        []string                     `json:"facts"`
	Unknowns     []string                     `json:"unknowns"`
	Alternatives []factory.ProductAlternative `json:"alternatives"`
	Exclusions   []factory.Exclusion          `json:"exclusions,omitempty"`
	Ideas        []factory.ProductIdea        `json:"ideas,omitempty"`
}

// JudgeConsolidation compares the content's actual alternatives and returns
// one coherent proposal. The selected advice persists as a durable receipt
// and failures retain their finite charge; the proposal carries the advice
// fingerprint as provenance. Person review of the single recommendation is
// the check, so no confidence threshold blocks the verdict. Corrections in
// the revision recompose the proposal without any renewed approval.
func (c *Coordinator) JudgeConsolidation(ctx context.Context, r factory.IntentRevision, content factory.ProposalContent) (factory.ConsolidatedProposal, error) {
	if err := r.Validate(); err != nil {
		return factory.ConsolidatedProposal{}, err
	}
	if err := content.Validate(); err != nil {
		return factory.ConsolidatedProposal{}, err
	}
	if r.State != "draft" || content.Revision != r.ID || content.Intent != r.Intent {
		return factory.ConsolidatedProposal{}, errors.New("consolidation needs the matching draft revision")
	}
	if !content.Supported {
		return factory.ConsolidatedProposal{}, errors.New("no alternatives to consolidate")
	}
	state := consolidationEvidence{Objective: r.Objective, Facts: content.Facts, Unknowns: content.Unknowns, Alternatives: content.Alternatives, Exclusions: content.Exclusions}
	if content.Producer == factory.PlanningProducer {
		state.Ideas = r.Ideas
	}
	options := make(map[string]any, len(content.Alternatives))
	for _, alternative := range content.Alternatives {
		options[alternative.Key] = alternative.Name + ": " + alternative.Summary
	}
	answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{
		Repository: r.Repository,
		Purpose:    factory.JudgmentConsolidation,
		State:      state,
		Question:   consolidationRubric,
		Options:    options,
		Outcome:    "consolidation",
		Admit: func(ctx context.Context, snapshot string) (bool, error) {
			return true, c.checkIntentAdvice(ctx, r)
		},
	})
	if err != nil {
		return factory.ConsolidatedProposal{}, err
	}
	fingerprint, err := factory.JudgmentFingerprint(factory.JudgmentConsolidation, state, consolidationRubric, options)
	if err != nil {
		return factory.ConsolidatedProposal{}, err
	}
	return factory.ConsolidateContent(content, answer.Selected, fingerprint)
}
