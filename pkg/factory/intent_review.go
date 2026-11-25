package factory

// Saved-review freshness against the current intent head. current means the
// head still is the result revision the review was saved under; stale means
// the head moved on while the stored presentation is preserved; missing
// means no presentation was ever stored for the requested revision.
const (
	ReviewCurrent = "current"
	ReviewStale   = "stale"
	ReviewMissing = "missing"
)

// SavedReview is the immutable owner-private presentation of one Explore
// round. BasisRevision is the draft head Explore composed from; ResultRevision
// is the newly saved revision carrying the settled proposal. Content,
// Recommendation and Consistency describe the basis input and bind its
// revision. Challenges and Settled instead target result ideas, including
// newly generated ones absent from the basis, evaluated using basis
// evidence; consumers resolve those IDs against the result revision. A
// saved review grants no acceptance, repository authority or spending
// permission, and it changes no authorization digest.
type SavedReview struct {
	Intent         string                `json:"intent"`
	BasisRevision  string                `json:"basis_revision"`
	ResultRevision string                `json:"result_revision"`
	Content        ProposalContent       `json:"content"`
	Challenges     []AssumptionChallenge `json:"challenges"`
	Settled        []string              `json:"settled"`
	Recommendation *ConsolidatedProposal `json:"recommendation,omitempty"`
	Consistency    *ConsistencyReport    `json:"consistency,omitempty"`
	SavedUnix      int64                 `json:"saved_unix"`
}

// SavedReviewRead is one owner-private exact-revision review read. HeadRevision
// is the current head at read time; Status classifies the stored review
// against it. A missing read carries no review and performs no judgments,
// funding mutation, generation or provisioning.
type SavedReviewRead struct {
	Intent       string       `json:"intent"`
	HeadRevision string       `json:"head_revision"`
	Status       string       `json:"status"`
	Review       *SavedReview `json:"review,omitempty"`
}
