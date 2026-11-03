package factory

import (
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

// AssumptionChallenge is one batched consequential question for the person.
// It names the assumed behavior, its consequence and a recommended resolution.
// Dependents of Idea stay held until the person answers; independent work
// continues. A challenge never records approval and never flips another idea.
type AssumptionChallenge struct {
	Idea           string `json:"idea"`
	Assumption     string `json:"assumption"`
	Consequence    string `json:"consequence"`
	Recommendation string `json:"recommendation"`
}

// RecommendAssumptionResolution authors the bounded default resolution offered
// beside a correction input. It restates the assumed behavior as sourced text,
// not as model prose or human approval.
func RecommendAssumptionResolution(idea ProductIdea) string {
	return "Confirm whether '" + idea.Description + "' holds, or explain the correction."
}

// Validate checks idea binding and bounded challenge prose.
func (c AssumptionChallenge) Validate() error {
	if !project.ValidDecisionID(c.Idea) || !intentText(c.Assumption, 4000) || !intentText(c.Consequence, 4000) || !intentText(c.Recommendation, 4000) {
		return errors.New("invalid assumption challenge")
	}
	return nil
}

// AssumptionSettlement records one supported/reversible choice settled without
// a human stamp. Source and Provenance carry the attributable autonomous
// basis; the decision is always accept, never a forged human approval.
type AssumptionSettlement struct {
	Idea       string `json:"idea"`
	Source     string `json:"source"`
	Provenance string `json:"provenance"`
}

// AssumptionReview is the complete per-idea assumption verdict: challenges
// interrupt, settlements proceed, and decided ideas keep their answers.
type AssumptionReview struct {
	Challenges []AssumptionChallenge  `json:"challenges"`
	Settled    []AssumptionSettlement `json:"settled"`
}

// Validate checks settlement bindings and attributable autonomous basis.
func (r AssumptionReview) Validate() error {
	if len(r.Challenges) > 50 || len(r.Settled) > 50 {
		return errors.New("invalid assumption review bounds")
	}
	seen := map[string]bool{}
	for _, challenge := range r.Challenges {
		if err := challenge.Validate(); err != nil {
			return err
		}
		if seen[challenge.Idea] {
			return errors.New("duplicate assumption verdict")
		}
		seen[challenge.Idea] = true
	}
	for _, settlement := range r.Settled {
		if !project.ValidDecisionID(settlement.Idea) || seen[settlement.Idea] {
			return errors.New("invalid settlement binding")
		}
		seen[settlement.Idea] = true
		if settlement.Source != "autonomous" && settlement.Source != "supported" && settlement.Source != "assumption" && settlement.Source != "" {
			return errors.New("invalid settlement source")
		}
		if settlement.Provenance != "autonomous" {
			return errors.New("settlement must be autonomous")
		}
	}
	return nil
}

// SettleAssumptions applies review settlements to a proposal copy. Only
// pending ideas with a matching settlement are accepted with the recorded
// autonomous basis; decided ideas, corrections and challenges are never
// touched. The input revision is not mutated.
func SettleAssumptions(proposal IntentRevision, review AssumptionReview) IntentRevision {
	settled := map[string]AssumptionSettlement{}
	for _, entry := range review.Settled {
		settled[entry.Idea] = entry
	}
	ideas := make([]ProductIdea, len(proposal.Ideas))
	copy(ideas, proposal.Ideas)
	for i := range ideas {
		entry, ok := settled[ideas[i].ID]
		if !ok || ideas[i].Decision != "pending" {
			continue
		}
		ideas[i].Decision = "accept"
		if entry.Source != "" {
			ideas[i].Source = entry.Source
		}
		ideas[i].Provenance = entry.Provenance
	}
	proposal.Ideas = ideas
	return proposal
}
