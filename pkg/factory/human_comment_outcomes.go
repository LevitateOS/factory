package factory

import (
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var ErrHumanCommentObservation = errors.New("human comment observation conflicts with captured command")

// ValidateHumanCommentObservation checks journal binding, not provider trust.
// The admitted adapter authenticates native intent/receipt evidence first.
func ValidateHumanCommentObservation(c HumanCommentCapture, o collaboration.OperationLookup) error {
	cmd, err := c.Command()
	if err != nil || o.Validate() != nil || o.Host != c.Request.Host || o.Authority != c.Authority || o.OperationID != c.OperationID {
		return ErrHumanCommentObservation
	}
	if o.Record == nil {
		return nil
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return ErrHumanCommentObservation
	}
	if r.Binding == nil {
		if r.Effect != collaboration.EffectNotCommitted || r.Cancellation != collaboration.CancellationCancelled || r.Completion != collaboration.CompletionComplete || r.Receipt != nil {
			return ErrHumanCommentObservation
		}
		return nil
	}
	if *r.Binding != (collaboration.OperationBinding{Repository: c.Request.Repository, Actor: c.Request.Actor, Kind: collaboration.CreateComment, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}) {
		return ErrHumanCommentObservation
	}
	if r.Effect == collaboration.EffectCommitted {
		if r.Receipt == nil || r.Receipt.Authority != c.Authority || r.Receipt.Reference != c.OperationID || len(r.Receipt.IntentDigest) != 64 {
			return ErrHumanCommentObservation
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return ErrHumanCommentObservation
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return ErrHumanCommentObservation
	}
	return nil
}

// HumanCommentFinished excludes pending/unknown effects and unresolved completion.
func HumanCommentFinished(o collaboration.OperationLookup) bool { return humanCommandFinished(o) }

// AdvanceHumanCommentObservation serializes overlapping validated observations.
func AdvanceHumanCommentObservation(c HumanCommentCapture, old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if ValidateHumanCommentObservation(c, old) != nil || ValidateHumanCommentObservation(c, next) != nil {
		return collaboration.OperationLookup{}, ErrHumanCommentObservation
	}
	// A local cancellation can race the causal provider reply. Preserve the
	// cancellation uncertainty without discarding a newly proved complete primary
	// effect just because that reply predates the cancellation request. Copy the
	// record so the caller's observation is never mutated.
	if old.Record != nil && next.Record != nil &&
		old.Record.Effect == collaboration.EffectIndeterminate &&
		old.Record.Cancellation == collaboration.CancellationIndeterminate &&
		(next.Record.Effect == collaboration.EffectCommitted || next.Record.Effect == collaboration.EffectNotCommitted) &&
		next.Record.Completion == collaboration.CompletionComplete &&
		next.Record.Cancellation == collaboration.CancellationNone {
		record := *next.Record
		record.Cancellation = old.Record.Cancellation
		next.Record = &record
	}
	result, err := advanceHumanCommandObservation(old, next)
	if err != nil {
		return collaboration.OperationLookup{}, ErrHumanCommentObservation
	}
	return result, nil
}
