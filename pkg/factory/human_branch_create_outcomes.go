package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanBranchCreateObservation = errors.New("human branch creation observation conflicts with captured command")

// ValidateHumanBranchCreateObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanBranchCreateObservation(c HumanBranchCreateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanBranchCreateObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanBranchCreateObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanBranchCreateObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateBranch, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanBranchCreateObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanBranchCreateObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanBranchCreateObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanBranchCreateObservation
	}
	return nil
}

// HumanBranchCreateFinished excludes unresolved effects and native completion work.
func HumanBranchCreateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanBranchCreateObservation retains terminal facts and exact provider receipts.
func AdvanceHumanBranchCreateObservation(c HumanBranchCreateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanBranchCreateObservation(c, old) != nil || ValidateHumanBranchCreateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanBranchCreateObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanBranchCreateObservation
	}
	return result, nil
}
