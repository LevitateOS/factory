package factory

import (
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
)

var ErrHumanPullCreateObservation = errors.New("human pull creation observation conflicts with captured command")

// ValidateHumanPullCreateObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanPullCreateObservation(c HumanPullCreateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanPullCreateObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanPullCreateObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanPullCreateObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.OpenPull, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanPullCreateObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanPullCreateObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanPullCreateObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanPullCreateObservation
	}
	return nil
}

// HumanPullCreateFinished excludes unresolved effects and native completion work.
func HumanPullCreateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanPullCreateObservation retains terminal facts and exact provider receipts.
func AdvanceHumanPullCreateObservation(c HumanPullCreateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanPullCreateObservation(c, old) != nil || ValidateHumanPullCreateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanPullCreateObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanPullCreateObservation
	}
	return result, nil
}
