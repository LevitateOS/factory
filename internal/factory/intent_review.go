package factory

import (
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

// Saved-review freshness against the current intent head. current means the
// head still is the result revision the review was saved under; stale means
// the head moved on while the stored presentation is preserved; missing
// means no presentation was ever stored for the requested revision.
const (
	ReviewCurrent = "current"
	ReviewStale   = "stale"
	ReviewMissing = "missing"
)

// ClassifyReview names the freshness of a stored review against the current
// head. Allowance and other non-head changes never affect the outcome.
func ClassifyReview(headRevision, resultRevision string, found bool) string {
	if !found {
		return ReviewMissing
	}
	if headRevision == resultRevision {
		return ReviewCurrent
	}
	return ReviewStale
}

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

// Validate checks basis/result identity, basis binding of the content
// payloads, verdict bounds and advice provenance. Verdict idea IDs target
// the result revision and are checked for shape, not resolved here.
func (r SavedReview) Validate() error {
	if !project.ValidDecisionID(r.Intent) || !project.ValidDecisionID(r.BasisRevision) || !project.ValidDecisionID(r.ResultRevision) {
		return errors.New("invalid saved review identity")
	}
	if r.BasisRevision == r.ResultRevision {
		return errors.New("saved review basis and result must differ")
	}
	if err := r.Content.Validate(); err != nil {
		return err
	}
	if r.Content.Intent != r.Intent || r.Content.Revision != r.BasisRevision {
		return errors.New("review content must bind the basis revision")
	}
	if len(r.Challenges) > 50 || len(r.Settled) > 50 {
		return errors.New("invalid saved review bounds")
	}
	seen := map[string]bool{}
	if err := checkSavedReviewVerdicts(r, seen); err != nil {
		return err
	}
	if err := checkSavedReviewAdvice(r); err != nil {
		return err
	}
	if r.SavedUnix <= 0 {
		return errors.New("invalid saved review timestamp")
	}
	return nil
}

func checkSavedReviewVerdicts(r SavedReview, seen map[string]bool) error {
	for _, challenge := range r.Challenges {
		if err := challenge.Validate(); err != nil {
			return err
		}
		if seen[challenge.Idea] {
			return errors.New("duplicate saved review verdict")
		}
		seen[challenge.Idea] = true
	}
	for _, id := range r.Settled {
		if !project.ValidDecisionID(id) || seen[id] {
			return errors.New("invalid saved review settlement")
		}
		seen[id] = true
	}
	return nil
}

func checkSavedReviewAdvice(r SavedReview) error {
	if r.Recommendation != nil {
		if err := r.Recommendation.Validate(); err != nil {
			return err
		}
		if r.Recommendation.Intent != r.Intent || r.Recommendation.Revision != r.BasisRevision {
			return errors.New("review recommendation must bind the basis revision")
		}
	}
	if r.Consistency != nil {
		if err := r.Consistency.Validate(); err != nil {
			return err
		}
		if r.Consistency.Intent != r.Intent || r.Consistency.Revision != r.BasisRevision {
			return errors.New("review consistency must bind the basis revision")
		}
	}
	return nil
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

// Validate checks read binding and status honesty: current only at the result
// head, stale only past it, missing only without a review.
func (r SavedReviewRead) Validate() error {
	if !project.ValidDecisionID(r.Intent) || !project.ValidDecisionID(r.HeadRevision) {
		return errors.New("invalid review read identity")
	}
	switch r.Status {
	case ReviewMissing:
		if r.Review != nil {
			return errors.New("missing review must not carry a review")
		}
		return nil
	case ReviewCurrent, ReviewStale:
	default:
		return errors.New("unknown review status")
	}
	if r.Review == nil {
		return errors.New("review read needs a stored review")
	}
	if err := r.Review.Validate(); err != nil {
		return err
	}
	if r.Review.Intent != r.Intent {
		return errors.New("review read intent mismatch")
	}
	if want := ClassifyReview(r.HeadRevision, r.Review.ResultRevision, true); r.Status != want {
		return errors.New("review status does not match the head")
	}
	return nil
}
