package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanStateObservation = errors.New("human state observation conflicts with captured command")

// ValidateHumanStateObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanStateObservation(c HumanStateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanStateObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanStateObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanStateObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.ApplyDiscussionState, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanStateObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanStateObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanStateObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanStateObservation
	}
	return nil
}

// HumanStateFinished excludes unresolved effects and native completion work.
func HumanStateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanStateObservation retains terminal facts and exact provider receipts.
func AdvanceHumanStateObservation(c HumanStateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanStateObservation(c, old) != nil || ValidateHumanStateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanStateObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanStateObservation
	}
	return result, nil
}
