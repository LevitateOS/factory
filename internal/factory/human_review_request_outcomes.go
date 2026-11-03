package factory

import (
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
)

var ErrHumanReviewRequestObservation = errors.New("human reviewer observation conflicts with captured command")

// ValidateHumanReviewRequestObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanReviewRequestObservation(c HumanReviewRequestCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanReviewRequestObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanReviewRequestObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanReviewRequestObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.RequestReview, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanReviewRequestObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanReviewRequestObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanReviewRequestObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanReviewRequestObservation
	}
	return nil
}

// HumanReviewRequestFinished excludes unresolved effects and native completion work.
func HumanReviewRequestFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanReviewRequestObservation retains terminal facts and exact provider receipts.
func AdvanceHumanReviewRequestObservation(c HumanReviewRequestCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanReviewRequestObservation(c, old) != nil || ValidateHumanReviewRequestObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewRequestObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanReviewRequestObservation
	}
	return result, nil
}
