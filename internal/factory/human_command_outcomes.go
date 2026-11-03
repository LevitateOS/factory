package factory

import (
	"bytes"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
)

// Shared ordering for the existing atomic human command journals.
// Typed entry points validate each capture before entering this state reducer.
var errHumanCommandObservation = errors.New("human command observation conflicts")

// humanCommandFinished excludes pending/unknown effects and unresolved
// completion. Needs-intervention remains open for an explicit later resolution.
func humanCommandFinished(o collaboration.OperationLookup) bool {
	return o.State == collaboration.LookupFound && o.Record != nil &&
		(o.Record.Effect == collaboration.EffectCommitted || o.Record.Effect == collaboration.EffectNotCommitted) && o.Record.Completion == collaboration.CompletionComplete
}

func advanceHumanCommandObservation(old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if old.Record == nil {
		return next, nil
	}
	if next.Record == nil {
		return old, nil
	}
	a, b := old.Record, next.Record
	terminalEffect := a.Effect == collaboration.EffectCommitted || a.Effect == collaboration.EffectNotCommitted
	if terminalEffect && a.Effect != b.Effect {
		if b.Effect == collaboration.EffectCommitted || b.Effect == collaboration.EffectNotCommitted {
			return collaboration.OperationLookup{}, errHumanCommandObservation
		}
		return old, nil
	}
	if a.Receipt != nil {
		if b.Receipt == nil || a.Receipt.Authority != b.Receipt.Authority || a.Receipt.Reference != b.Receipt.Reference || a.Receipt.IntentDigest != b.Receipt.IntentDigest || !bytes.Equal(a.Receipt.Payload, b.Receipt.Payload) {
			return collaboration.OperationLookup{}, errHumanCommandObservation
		}
	}
	if humanCommandFinished(old) {
		return old, nil
	}
	if a.Binding != nil && b.Binding == nil {
		return old, nil
	}
	if a.Effect == collaboration.EffectIndeterminate && b.Effect == collaboration.EffectPending {
		return old, nil
	}
	if a.Completion == collaboration.CompletionNeedsIntervention && b.Completion == collaboration.CompletionPending {
		return old, nil
	}
	if a.Cancellation != b.Cancellation {
		switch a.Cancellation {
		case collaboration.CancellationCancelled, collaboration.CancellationTooLate:
			return old, nil
		case collaboration.CancellationPending:
			if b.Cancellation == collaboration.CancellationNone {
				return old, nil
			}
		case collaboration.CancellationIndeterminate:
			if b.Cancellation == collaboration.CancellationNone || b.Cancellation == collaboration.CancellationPending {
				return old, nil
			}
		}
	}
	return next, nil
}
