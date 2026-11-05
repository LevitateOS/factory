package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanInlineObservation = errors.New("human inline observation conflicts with captured command")

// ValidateHumanInlineObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanInlineObservation(c HumanInlineCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanInlineObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanInlineObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanInlineObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateReviewComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanInlineObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanInlineObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanInlineObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanInlineObservation
	}
	return nil
}

// HumanInlineFinished excludes unresolved effects and native completion work.
func HumanInlineFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanInlineObservation retains terminal facts and exact provider receipts.
func AdvanceHumanInlineObservation(c HumanInlineCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanInlineObservation(c, old) != nil || ValidateHumanInlineObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanInlineObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanInlineObservation
	}
	return result, nil
}
