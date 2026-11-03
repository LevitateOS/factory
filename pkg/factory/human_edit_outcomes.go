package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanEditObservation = errors.New("human edit observation conflicts with captured command")

// ValidateHumanEditObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanEditObservation(c HumanEditCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanEditObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial || !c.ExpectedCurrent() && r.Effect == collaboration.EffectCommitted {
		return ErrHumanEditObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanEditObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.EditDiscussion, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanEditObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanEditObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanEditObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanEditObservation
	}
	return nil
}

// HumanEditFinished excludes unresolved effects and native completion work.
func HumanEditFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanEditObservation retains terminal facts and exact provider receipts.
func AdvanceHumanEditObservation(c HumanEditCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanEditObservation(c, old) != nil || ValidateHumanEditObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanEditObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanEditObservation
	}
	return result, nil
}
