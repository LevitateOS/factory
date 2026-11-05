package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanIssueCreateObservation = errors.New("human issue creation observation conflicts with captured command")

// ValidateHumanIssueCreateObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanIssueCreateObservation(c HumanIssueCreateCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanIssueCreateObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanIssueCreateObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanIssueCreateObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.OpenIssue, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanIssueCreateObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanIssueCreateObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanIssueCreateObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanIssueCreateObservation
	}
	return nil
}

// HumanIssueCreateFinished excludes unresolved effects and native completion work.
func HumanIssueCreateFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanIssueCreateObservation retains terminal facts and exact provider receipts.
func AdvanceHumanIssueCreateObservation(c HumanIssueCreateCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanIssueCreateObservation(c, old) != nil || ValidateHumanIssueCreateObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanIssueCreateObservation
	}
	next = retainHumanCancellationUncertainty(old, next)
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanIssueCreateObservation
	}
	return result, nil
}
