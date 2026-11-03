package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanThreadObservation = errors.New("human thread observation conflicts with captured command")

// ValidateHumanThreadObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanThreadObservation(c HumanThreadCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanThreadObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanThreadObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanThreadObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ResolveReviewThread, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanThreadObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanThreadObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanThreadObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanThreadObservation
	}
	return nil
}

// HumanThreadFinished excludes unresolved effects and native completion work.
func HumanThreadFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanThreadObservation retains terminal facts and exact provider receipts.
func AdvanceHumanThreadObservation(c HumanThreadCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanThreadObservation(c, old) != nil || ValidateHumanThreadObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanThreadObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanThreadObservation
	}
	return result, nil
}
