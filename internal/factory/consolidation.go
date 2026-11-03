package factory

import (
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

// Milestone is one ordered delivery step from the recommended walkthrough:
// what happens and its visible result. Milestones carry no dates or estimates.
type Milestone struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

// ConsolidatedProposal is the one coherent recommendation composed from
// compared alternatives. Outcomes and constraints preserve the person's
// direction; milestones follow the recommended walkthrough; Advice is the
// durable judgment fingerprint behind the recommendation.
type ConsolidatedProposal struct {
	Producer    string             `json:"producer"`
	Intent      string             `json:"intent"`
	Revision    string             `json:"revision"`
	Objective   string             `json:"objective"`
	Recommended string             `json:"recommended"`
	Alternative ProductAlternative `json:"alternative"`
	Outcomes    []string           `json:"outcomes"`
	Constraints []string           `json:"constraints"`
	Excluded    []string           `json:"excluded,omitempty"`
	Milestones  []Milestone        `json:"milestones"`
	Advice      string             `json:"advice"`
}

// ConsolidateContent composes one proposal from compared content. Corrections
// flow into outcomes and constraints without any approval gate: an explicit
// correction is person input, not a pending decision.
func ConsolidateContent(content ProposalContent, recommended, advice string) (ConsolidatedProposal, error) {
	if err := content.Validate(); err != nil {
		return ConsolidatedProposal{}, err
	}
	if !content.Supported {
		return ConsolidatedProposal{}, errors.New("no alternatives to consolidate")
	}
	var alternative *ProductAlternative
	for i := range content.Alternatives {
		if content.Alternatives[i].Key == recommended {
			alternative = &content.Alternatives[i]
		}
	}
	if alternative == nil {
		return ConsolidatedProposal{}, errors.New("unknown recommended alternative")
	}
	owned := *alternative
	owned.Steps = append([]WalkthroughStep(nil), alternative.Steps...)
	for i := range owned.Steps {
		owned.Steps[i].Branches = append([]WalkthroughBranch(nil), alternative.Steps[i].Branches...)
	}
	proposal := ConsolidatedProposal{
		Producer:    content.Producer,
		Intent:      content.Intent,
		Revision:    content.Revision,
		Objective:   content.Objective,
		Recommended: recommended,
		Alternative: owned,
		Outcomes:    append([]string(nil), alternative.Outcomes...),
		Excluded:    append([]string(nil), content.Excluded...),
		Advice:      advice,
	}
	if len(content.Facts) > 1 {
		proposal.Constraints = append([]string(nil), content.Facts[1:]...)
	}
	for _, step := range alternative.Steps {
		proposal.Milestones = append(proposal.Milestones, Milestone{Name: step.Actor + ": " + step.Action, Result: step.Result})
		for _, branch := range step.Branches {
			proposal.Milestones = append(proposal.Milestones, Milestone{Name: "If " + branch.Outcome, Result: branch.Result})
		}
	}
	return proposal, proposal.Validate()
}

// Validate checks proposal binding, preserved direction and milestone bounds.
func (p ConsolidatedProposal) validateIdentity() error {
	if (p.Producer != AuthoredProducer && p.Producer != PlanningProducer) || !project.ValidDecisionID(p.Intent) || !project.ValidDecisionID(p.Revision) || !intentText(p.Objective, 16000) {
		return errors.New("invalid consolidated proposal identity or objective")
	}
	if err := p.Alternative.Validate(); err != nil {
		return err
	}
	if p.Producer == AuthoredProducer {
		if authored, exists := authoredAlternatives[p.Alternative.Key]; !exists || authored.Name != p.Alternative.Name {
			return errors.New("unknown authored product alternative")
		}
	}
	if p.Recommended != p.Alternative.Key || !ValidDigest(p.Advice) {
		return errors.New("invalid consolidated recommendation or advice")
	}
	if len(p.Outcomes) == 0 || len(p.Outcomes) > 20 || len(p.Constraints) > 50 || len(p.Excluded) > 50 || len(p.Milestones) == 0 || len(p.Milestones) > 40 {
		return errors.New("invalid consolidated proposal bounds")
	}
	return nil
}

func (p ConsolidatedProposal) validateCollections() error {
	for _, outcome := range p.Outcomes {
		if !intentText(outcome, 4000) {
			return errors.New("invalid consolidated outcome")
		}
	}
	for _, constraint := range p.Constraints {
		if !intentText(constraint, 4000) {
			return errors.New("invalid consolidated constraint")
		}
	}
	for _, id := range p.Excluded {
		if !project.ValidDecisionID(id) {
			return errors.New("invalid consolidated exclusion")
		}
	}
	for _, milestone := range p.Milestones {
		if !intentText(milestone.Name, 4000) || !intentText(milestone.Result, 4000) {
			return errors.New("invalid consolidated milestone")
		}
	}
	return nil
}

func (p ConsolidatedProposal) Validate() error {
	if err := p.validateIdentity(); err != nil {
		return err
	}
	return p.validateCollections()
}
