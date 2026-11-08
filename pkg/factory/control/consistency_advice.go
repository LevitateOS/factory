package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

const consistencyRubric = "Verify the recommended walkthrough, its illustrative summary and the effective outcomes against each other and the supplied corrections. Choose consistent only when the walkthrough delivers every requested outcome, adds no unsourced effects and matches its summary. A walkthrough that reintroduces supplied declined behavior is an effect_added mismatch. Otherwise name the specific mismatch class. Judge only supplied facts; grant no authority, access or spending."

func consistencyOptions() map[string]any {
	return map[string]any{
		"consistent":    "The walkthrough, summary and effective outcomes agree and stay within sourced facts.",
		"outcome_gap":   "The walkthrough misses a requested outcome or correction that recomposition must add.",
		"effect_added":  "The walkthrough adds an effect with no sourced basis that recomposition must remove or source.",
		"summary_drift": "The summary or illustration diverges from the walkthrough and effective outcomes.",
	}
}

var consistencyDetails = map[string]string{
	factory.MismatchOutcomeGap:   "The walkthrough does not deliver a requested outcome or correction; recompose the affected steps.",
	factory.MismatchEffectAdded:  "The walkthrough adds an effect with no sourced basis; remove it or source it from the request.",
	factory.MismatchSummaryDrift: "The summary or illustration diverges from the walkthrough; recompose them together.",
	factory.MismatchUnverified:   "The consistency check could not confirm the walkthrough; recompose or re-verify.",
}

type consistencyEvidence struct {
	Objective   string                     `json:"objective"`
	Facts       []string                   `json:"facts"`
	Unknowns    []string                   `json:"unknowns"`
	Recommended factory.ProductAlternative `json:"recommended"`
	Outcomes    []string                   `json:"outcomes"`
	Milestones  []factory.Milestone        `json:"milestones"`
	Constraints []string                   `json:"constraints"`
	Exclusions  []factory.Exclusion        `json:"exclusions,omitempty"`
	Ideas       []factory.ProductIdea      `json:"ideas,omitempty"`
}

// JudgeConsistency verifies the consolidated proposal. Deterministic code
// checks run first and return specific mismatches without spending a call.
// Otherwise one consistency-purpose judgment confirms the walkthrough or
// names its mismatch class; a low-confidence clean verdict reports
// unverified rather than claiming proof. Findings bind the exact revision.
func (c *Coordinator) JudgeConsistency(ctx context.Context, r factory.IntentRevision, content factory.ProposalContent, proposal factory.ConsolidatedProposal) (factory.ConsistencyReport, error) {
	if err := r.Validate(); err != nil {
		return factory.ConsistencyReport{}, err
	}
	if err := content.Validate(); err != nil {
		return factory.ConsistencyReport{}, err
	}
	if err := proposal.Validate(); err != nil {
		return factory.ConsistencyReport{}, err
	}
	if r.State != "draft" || content.Revision != r.ID || content.Intent != r.Intent || proposal.Revision != r.ID || proposal.Intent != r.Intent {
		return factory.ConsistencyReport{}, errors.New("consistency review needs the matching draft revision")
	}
	report := factory.ConsistencyReport{Producer: factory.AuthoredProducer, Intent: r.Intent, Revision: r.ID}
	if mismatches := factory.CheckProposalEffects(content, proposal); len(mismatches) > 0 {
		advice, err := codeCheckAdvice(content, proposal)
		if err != nil {
			return factory.ConsistencyReport{}, err
		}
		report.Mismatches = mismatches
		report.Advice = advice
		return report, report.Validate()
	}
	state := consistencyEvidence{Objective: r.Objective, Facts: content.Facts, Unknowns: content.Unknowns, Recommended: proposal.Alternative, Outcomes: proposal.Outcomes, Milestones: proposal.Milestones, Constraints: proposal.Constraints, Exclusions: content.Exclusions}
	if content.Producer == factory.PlanningProducer {
		state.Ideas = r.Ideas
	}
	options := consistencyOptions()
	answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{
		Repository: r.Repository,
		Purpose:    factory.JudgmentConsistency,
		State:      state,
		Question:   consistencyRubric,
		Options:    options,
		Outcome:    "consistency",
		Admit: func(ctx context.Context, snapshot string) (bool, error) {
			return true, c.checkIntentAdvice(ctx, r)
		},
	})
	if err != nil {
		return factory.ConsistencyReport{}, err
	}
	fingerprint, err := factory.JudgmentFingerprint(factory.JudgmentConsistency, state, consistencyRubric, options)
	if err != nil {
		return factory.ConsistencyReport{}, err
	}
	report.Confidence, report.Advice = answer.Confidence, fingerprint
	if answer.Selected == "consistent" && answer.Confidence >= 0.5 {
		return report, report.Validate()
	}
	class := answer.Selected
	if class == "consistent" {
		class = factory.MismatchUnverified
	}
	report.Mismatches = []factory.Mismatch{{Class: class, Detail: consistencyDetails[class]}}
	return report, report.Validate()
}

// codeCheckAdvice fingerprints deterministic findings so even a judgment-free
// report carries provenance. It reuses the consistency purpose over the exact
// checked inputs without reserving allowance or calling the chooser.
func codeCheckAdvice(content factory.ProposalContent, proposal factory.ConsolidatedProposal) (string, error) {
	return factory.JudgmentFingerprint(factory.JudgmentConsistency, struct {
		Objective   string                     `json:"objective"`
		Facts       []string                   `json:"facts"`
		Recommended factory.ProductAlternative `json:"recommended"`
		Exclusions  []factory.Exclusion        `json:"exclusions,omitempty"`
	}{content.Objective, content.Facts, proposal.Alternative, content.Exclusions}, "deterministic-effect-scan", map[string]any{"mismatch": "code-found", "clean": "code-found-none"})
}
