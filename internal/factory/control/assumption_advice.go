package control

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// JudgeAssumptions reviews supplied proposal ideas against a draft revision.
// Decided ideas keep their answers. Attributable autonomous and evidenced
// supported choices settle without a human stamp; unclassified legacy
// choices stay pending for the person. Every pending assumption is material
// (no producer writes another kind), so each becomes a focused challenge:
// stranding it pending (no question, no vote) would block the revision while
// design keeps clarifying. Challenges hold only their own idea's dependents;
// declining one guess changes no other decision.
func (c *Coordinator) JudgeAssumptions(_ context.Context, r factory.IntentRevision, content factory.ProposalContent, ideas []factory.ProductIdea) (factory.AssumptionReview, error) {
	if err := r.Validate(); err != nil {
		return factory.AssumptionReview{}, err
	}
	if err := content.Validate(); err != nil {
		return factory.AssumptionReview{}, err
	}
	if r.State != "draft" || content.Revision != r.ID || content.Intent != r.Intent {
		return factory.AssumptionReview{}, errors.New("assumption review needs the matching draft revision")
	}
	var review factory.AssumptionReview
	for _, idea := range ideas {
		if idea.Decision != "pending" {
			continue
		}
		if !idea.Assumption {
			if settlement, ok := settleChoice(idea); ok {
				review.Settled = append(review.Settled, settlement)
			}
			continue
		}
		challenge := factory.AssumptionChallenge{Idea: idea.ID, Assumption: idea.Description, Consequence: idea.Consequence, Recommendation: factory.RecommendAssumptionResolution(idea)}
		if err := challenge.Validate(); err != nil {
			return factory.AssumptionReview{}, err
		}
		review.Challenges = append(review.Challenges, challenge)
	}
	return review, review.Validate()
}

// settleChoice accepts attributable autonomous and evidenced supported
// choices without a judgment call. Material, legacy-unclassified and
// human-claimed choices stay pending for the person.
func settleChoice(idea factory.ProductIdea) (factory.AssumptionSettlement, bool) {
	if idea.Material || idea.Actor != 0 || idea.Provenance == "human" {
		return factory.AssumptionSettlement{}, false
	}
	if idea.Source == "autonomous" && (idea.Provenance == "autonomous" || idea.Provenance == "") {
		return factory.AssumptionSettlement{Idea: idea.ID, Source: "autonomous", Provenance: "autonomous"}, true
	}
	if idea.Source == "supported" && (idea.Provenance == "autonomous" || idea.Provenance == "") && idea.Evidence != "" {
		return factory.AssumptionSettlement{Idea: idea.ID, Source: "supported", Provenance: "autonomous"}, true
	}
	return factory.AssumptionSettlement{}, false
}
