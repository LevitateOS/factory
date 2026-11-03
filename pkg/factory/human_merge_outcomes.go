package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanMergeObservation = errors.New("human merge observation conflicts with captured command")

// ValidateHumanMergeObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanMergeObservation(c HumanMergeCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanMergeObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanMergeObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanMergeObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.Merge, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanMergeObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanMergeObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanMergeObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanMergeObservation
	}
	return nil
}

// HumanMergeFinished excludes unresolved effects and native completion work.
func HumanMergeFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanMergeObservation retains terminal facts and exact provider receipts.
func AdvanceHumanMergeObservation(c HumanMergeCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanMergeObservation(c, old) != nil || ValidateHumanMergeObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanMergeObservation
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanMergeObservation
	}
	return result, nil
}
