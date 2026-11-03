package factory

import (
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
)

var ErrHumanReviewObservation = errors.New("human review observation conflicts with captured command")

// ValidateHumanReviewObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanReviewObservation(c HumanReviewCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanReviewObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanReviewObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanReviewObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.SubmitReview, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanReviewObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanReviewObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanReviewObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanReviewObservation
	}
	return nil
}

// HumanReviewFinished excludes unresolved effects and native completion work.
func HumanReviewFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanReviewObservation retains terminal facts and exact provider receipts.
func AdvanceHumanReviewObservation(c HumanReviewCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanReviewObservation(c, old) != nil || ValidateHumanReviewObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewObservation
	}
	return result, nil
}
