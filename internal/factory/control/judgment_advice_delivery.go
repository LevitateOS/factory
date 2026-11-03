package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// ErrDeliveryUnavailable reports a delivery judgment that cannot consult
// now: a closed attempt, or a malformed, missing or provider-failed answer.
// It never authorizes merge.
var ErrDeliveryUnavailable = errors.New("delivery judgment unavailable")

// DeliveryChooser is one delivery consultation. Code supplies the
// complete semantic evidence, the fixed delivery rubric and exactly the
// two delivery options; the chooser supplies only the choice.
type DeliveryChooser interface {
	SelectDelivery(context.Context, any, string, map[string]any) (factory.DeliveryAnswer, error)
}

// DeliveryFunding is the fingerprint-keyed delivery decision journal,
// satisfied by *store.Store.
type DeliveryFunding interface {
	DeliveryJudgment(context.Context, string) (factory.DeliveryReceipt, error)
	AdmitDeliveryJudgment(context.Context, int64, int64, string) (bool, error)
	RecordDeliveryJudgment(context.Context, factory.DeliveryReceipt) error
}

var _ DeliveryFunding = (*store.Store)(nil)

// DeliveryAdvice is one delivery-review-v1 request. Input is the complete
// semantic evidence; ReviewsFit reports whether another review fits the
// remaining allowance; ObservedNativeRevision is the raw native revision
// kept beside the receipt for mutation guards. Callers check deterministic
// evidence/authority/check prerequisites first, and a clean first review
// still consults through this same path with its empty finding set.
type DeliveryAdvice struct {
	Input                  factory.DeliveryFingerprintInput
	State                  any
	Repository             int64
	Issue                  int64
	AttemptRoot            string
	ReviewsFit             bool
	ObservedNativeRevision int64
	Chooser                DeliveryChooser
	Funding                DeliveryFunding
}

// AdviseDelivery returns the delivery choice for the exact semantic
// evidence. A matching completed receipt reuses without a new call.
// Otherwise it asks the chooser once and records the answer with its
// effective action; a failed call records nothing and the next pass asks
// again. Any evidence change yields a different fingerprint and asks anew.
func (c *Coordinator) AdviseDelivery(ctx context.Context, advice DeliveryAdvice) (factory.DeliveryDecision, bool, error) {
	if advice.Chooser == nil || advice.Funding == nil {
		return factory.DeliveryDecision{}, false, ErrDeliveryUnavailable
	}
	state := advice.State
	if state == nil {
		state = advice.Input
	}
	fingerprint, err := factory.DeliveryFingerprint(advice.Input)
	if err != nil {
		return factory.DeliveryDecision{}, false, err
	}
	if advice.ObservedNativeRevision < 0 {
		return factory.DeliveryDecision{}, false, errors.New("invalid delivery observation")
	}
	prior, err := advice.Funding.DeliveryJudgment(ctx, fingerprint)
	if err == nil {
		if prior.CurrentFor(fingerprint) {
			return prior.Decision, true, nil
		}
		return factory.DeliveryDecision{}, false, ErrDeliveryUnavailable
	}
	if !errors.Is(err, store.ErrNotFound) {
		return factory.DeliveryDecision{}, false, err
	}
	admitted, err := advice.Funding.AdmitDeliveryJudgment(ctx, advice.Repository, advice.Issue, advice.AttemptRoot)
	if err != nil {
		return factory.DeliveryDecision{}, false, err
	}
	if !admitted {
		return factory.DeliveryDecision{}, false, ErrDeliveryUnavailable
	}
	options := factory.DeliveryOptions()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	answer, callErr := advice.Chooser.SelectDelivery(bounded, state, factory.DeliveryRubric, options)
	cancel()
	if callErr == nil && (answer.Fingerprint != fingerprint || answer.Model != advice.Input.Model) {
		callErr = errors.New("delivery answer judged different evidence")
	}
	if callErr != nil {
		// Nothing is recorded, so the next pass asks again.
		return factory.DeliveryDecision{}, false, ErrDeliveryUnavailable
	}
	receipt := factory.NewDeliveryReceipt(advice.Input, fingerprint, answer, advice.ReviewsFit, advice.ObservedNativeRevision)
	if err := advice.Funding.RecordDeliveryJudgment(ctx, receipt); err != nil {
		return factory.DeliveryDecision{}, false, err
	}
	return receipt.Decision, false, nil
}
