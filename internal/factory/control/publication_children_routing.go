package control

import (
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/factory"
)

// deliveryRoute is the D08 route for one publication head before spending:
// dispatch an independent review, dispatch a bounded correction, or
// withhold the run pending the funded D08 Jev decision. The child path
// makes no funded call itself, so missing coverage, stale inputs, pending
// checks and native external blockers already wait without a paid
// decision; routeJev additionally names the decision that must precede
// another run.
type deliveryRoute int

const (
	// routeReview dispatches one independent discretionary review.
	routeReview deliveryRoute = iota
	// routePolicyReview dispatches the one bounded policy-required review
	// for a missing approval the configured reviewer can supply.
	routePolicyReview
	// routeCorrection dispatches one bounded correction cycle for a
	// concrete unfixed defect.
	routeCorrection
	// routeJev withholds the run: optional-only, disputed or repetitive
	// feedback invokes the D08 Jev decision before another coding or
	// review run, and optional advice never forces a correction.
	routeJev
)

// deliveryFeedback is the complete observable evidence behind one D08
// routing choice: the current review verdict, the current check verdict
// (empty when no current assessment exists), the dispositions of the
// newest correction journal on the current head (nil when no correction
// has answered this head yet), and whether a policy-required review is
// owed for a missing approval.
type deliveryFeedback struct {
	HasReview            bool
	ReviewVerdict        string
	ChecksVerdict        string
	NewestDispositions   []string
	PolicyOwed           bool
	OptionalOnly         bool
	NewUnfixedActionable bool
}

// routeDeliveryFeedback applies the D08 route-before-spending table. An
// explicit valid clear is not observable here (review verdicts carry no
// clear report); it admits D07 clean acceptance through AdmitClearReview
// once reviewer output parsing supplies the clear block. A concrete
// unfixed defect takes the bounded correction; complete proposed fixes
// without a later clear review, optional-only, disputed and repetitive
// feedback take the Jev decision before more coding or review.
func routeDeliveryFeedback(in deliveryFeedback) deliveryRoute {
	if !in.HasReview {
		if in.PolicyOwed {
			return routePolicyReview
		}
		return routeReview
	}
	if in.ReviewVerdict != "request-changes" {
		// An approving verdict with failing checks is a concrete unfixed
		// defect regardless of disposition history; the caller already
		// returns when an approval covers passing checks.
		return routeCorrection
	}
	if in.NewUnfixedActionable {
		return routeCorrection
	}
	if in.OptionalOnly {
		return routeJev
	}
	if in.NewestDispositions == nil {
		// No correction has answered this head yet: the feedback is new.
		return routeCorrection
	}
	disputed, unresolved := false, false
	for _, disposition := range in.NewestDispositions {
		switch disposition {
		case factory.DispositionDisputed:
			disputed = true
		case factory.DispositionUnresolved:
			unresolved = true
		}
	}
	// A dispute is evidence for the bounded D08 decision, never a fixed
	// finding or an automatic second coder run.
	if disputed {
		return routeJev
	}
	// A concrete unfixed defect still takes the bounded correction.
	if unresolved {
		return routeCorrection
	}
	// Optional-only feedback goes straight to Jev and never forces a
	// coder run; all-fixed feedback that the reviewer still rejects is
	// repetitive, so the decision precedes another run either way.
	return routeJev
}

func publicationDeliveryFeedback(p factory.Publication, review ResolvedReviewVerdict, checks *factory.CheckAssessment) deliveryFeedback {
	out := deliveryFeedback{HasReview: true, ReviewVerdict: review.Verdict, NewestDispositions: newestCurrentHeadDispositions(p)}
	if checks != nil {
		out.ChecksVerdict = checks.Verdict
	}
	if review.Review == nil {
		return out
	}
	answered := map[string]bool{}
	for _, journal := range p.CorrectionJournals {
		if journal.Report.NewCandidate != p.Candidate {
			continue
		}
		for _, disposition := range journal.Report.Dispositions {
			answered[disposition.Key] = true
		}
	}
	out.OptionalOnly = len(review.Review.NewFindings) > 0
	for _, finding := range review.Review.NewFindings {
		if finding.Actionable {
			out.OptionalOnly = false
			out.NewUnfixedActionable = out.NewUnfixedActionable || !answered[finding.Key]
		}
	}
	for _, assessment := range review.Review.Assessments {
		if assessment.Status == factory.FindingStillOpen {
			out.OptionalOnly = false
		}
	}
	return out
}

// policyReviewDecision is the separately recorded cause of a
// policy-required review (D08): it consumes ReviewRounds exactly like any
// other review but never the unchanged-head discretionary extra pass.
const policyReviewDecision = "policy-required-review"

// policyReviewBlockerApprovals is the one policy blocker implemented
// here: native approvals the configured reviewer can supply. Other
// blockers wait instead of spending a review that cannot satisfy policy.
const policyReviewBlockerApprovals = "approvals"

// policyReviewReason binds one policy-required review to its exact head,
// actor and policy blocker for deduplication.
func policyReviewReason(actor int64, head string) string {
	short := head
	if len(short) > 8 {
		short = short[:8]
	}
	return policyReviewBlockerApprovals + " need reviewer actor " + strconv.FormatInt(actor, 10) + " on head " + short
}

// missingPolicyApproval reports whether the configured reviewer has not
// yet supplied a committed approval for the publication's exact current
// head and base. A committed APPROVED operation by that actor on the head
// satisfies it; anything else (another actor's approval, a blocking
// request, no operation) leaves the approval missing. An unconfigured
// reviewer never owes one.
func missingPolicyApproval(p factory.Publication, reviewerActor int64) bool {
	if reviewerActor <= 0 {
		return false
	}
	for _, op := range p.ReviewOperations {
		if op.Work.ActorID != reviewerActor || op.Work.Event != "APPROVED" {
			continue
		}
		if op.Work.HeadOID != p.Candidate || op.Work.BaseOID != p.PRCreate.BaseOID {
			continue
		}
		if op.Outcome.Effect != factory.OpEffectCommitted || op.Outcome.Completion != factory.OpCompletionComplete {
			continue
		}
		return false
	}
	return true
}

// policyReviewRecorded reports whether a policy-required review for this
// exact head, actor and blocker is already recorded, so the bounded pass
// dispatches at most once: deduplicated by exact head, actor and policy
// blocker, however many passes reconsider the publication.
func policyReviewRecorded(p factory.Publication, head string, reviewerActor int64) bool {
	if reviewerActor <= 0 || !factory.ValidCommit(head) {
		return false
	}
	want := policyReviewReason(reviewerActor, head)
	for _, cycle := range p.ReviewCycles {
		if cycle.Head != head || cycle.TriggerDecision != policyReviewDecision || cycle.TriggerReason != want {
			continue
		}
		return true
	}
	return false
}

// newestCurrentHeadDispositions returns the dispositions of the newest
// correction journal whose cycle inspected the publication's current
// head, or nil when no correction has answered this head yet. Journals
// are append-only, so the last match wins; journals on superseded heads
// never route the current one.
func newestCurrentHeadDispositions(p factory.Publication) []string {
	byID := make(map[string]factory.ReviewCycle, len(p.ReviewCycles))
	for _, cycle := range p.ReviewCycles {
		byID[cycle.ID] = cycle
	}
	var newest []string
	answered := false
	for _, journal := range p.CorrectionJournals {
		cycle, ok := byID[journal.CycleID]
		if !ok || cycle.Head != p.Candidate {
			continue
		}
		newest = newest[:0]
		for _, disposition := range journal.Report.Dispositions {
			newest = append(newest, disposition.Disposition)
		}
		answered = true
	}
	if !answered {
		return nil
	}
	return newest
}

// policyCauseOwed combines the three policy-required preconditions with
// the available evidence: current passing checks on the head, a missing
// approval the configured reviewer can supply, and no recorded bounded
// pass for this exact head, actor and blocker.
func policyCauseOwed(p factory.Publication, checksVerdict string, reviewerActor int64) bool {
	if checksVerdict != factory.CheckPass {
		return false
	}
	if strings.TrimSpace(p.Candidate) == "" {
		return false
	}
	return missingPolicyApproval(p, reviewerActor) && !policyReviewRecorded(p, p.Candidate, reviewerActor)
}
