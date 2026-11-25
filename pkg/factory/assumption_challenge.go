package factory

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
