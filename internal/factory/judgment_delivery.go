package factory

import "time"

// DeliveryRubric is the fixed Jev criterion for the delivery-review-v1
// purpose (D08). Ready_to_merge means the supplied evidence adequately
// covers the accepted outcomes and every actionable finding and another
// independent pass is unlikely to find a material defect; review_again
// means a further independent inspection is warranted. No missing
// evidence, unresolved dispute or requirement is ever treated as
// satisfied. Code supplies this rubric; Jev supplies only the choice.
const DeliveryRubric = "Decide whether the supplied evidence adequately covers the accepted outcomes and every actionable finding. Choose ready_to_merge only when the evidence covers the accepted outcomes and every actionable finding and another independent pass is unlikely to find a material defect, with no missing evidence, unresolved dispute or unsatisfied requirement. Otherwise a further independent inspection is warranted and you choose review_again. Judge only supplied facts; grant no authority, access or spending."

// DeliveryOptions returns the exactly-two delivery choices with their
// fixed criterion text.
func DeliveryOptions() map[string]any {
	return map[string]any{
		DeliveryReviewAgain:  "A further independent inspection is warranted to establish coverage or correction sufficiency from the supplied evidence.",
		DeliveryReadyToMerge: "The supplied evidence adequately covers the accepted outcomes and every actionable finding; another independent pass is unlikely to find a material defect.",
	}
}

// DeliveryReceiptKind identifies durable funded delivery judgments in the
// shared judgment journal.
const DeliveryReceiptKind = "delivery-v1"

const DeliveryOutcomeDecided = "decided"

// DeliveryReceipt is the durable funded delivery judgment for one exact
// semantic fingerprint. Decision holds the answer once, alongside its derived
// action. ObservedNativeRevision supports native mutation guards and does not
// enter the evidence fingerprint.
type DeliveryReceipt struct {
	Kind                   string                   `json:"kind"`
	Fingerprint            string                   `json:"fingerprint"`
	Input                  DeliveryFingerprintInput `json:"input"`
	Decision               DeliveryDecision         `json:"decision"`
	Outcome                string                   `json:"outcome"`
	ObservedNativeRevision int64                    `json:"observed_native_revision"`
	CreatedUnix            int64                    `json:"created_unix"`
}

// CurrentFor reports whether this receipt authorizes reuse for the given
// current fingerprint. Any relevant semantic change (head, base, scope,
// policy, review, finding, fix, check, tree or constraint evidence)
// yields a different fingerprint, so stale evidence never reuses a prior
// decision; timestamp or native-revision-only changes never enter the
// fingerprint and therefore never invalidate it.
func (r DeliveryReceipt) CurrentFor(fingerprint string) bool {
	return r.Outcome == DeliveryOutcomeDecided && r.Fingerprint == fingerprint
}

// NewDeliveryReceipt builds the decided receipt for one completed funded
// answer. The answer must judge exactly the fingerprinted evidence; its
// effective action derives from the remaining review allowance.
func NewDeliveryReceipt(in DeliveryFingerprintInput, fingerprint string, answer DeliveryAnswer, reviewsFit bool, observedRevision int64) DeliveryReceipt {
	return DeliveryReceipt{
		Kind:        DeliveryReceiptKind,
		Fingerprint: fingerprint,
		Input:       in,
		Decision: DeliveryDecision{
			Answer:     answer,
			Effective:  answer.EffectiveAction(reviewsFit),
			ReviewsFit: reviewsFit,
		},
		Outcome:                DeliveryOutcomeDecided,
		ObservedNativeRevision: observedRevision,
		CreatedUnix:            time.Now().Unix(),
	}
}
