package factory

import (
	"bytes"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

var errHumanCommandObservation = errors.New("human command observation conflicts")

// Provider authenticity belongs to the admitted adapter. This binding checks
// only that an atomic observation belongs to the captured product command.
type humanCommandObservation struct {
	host      collaboration.HostID
	authority string
	operation string
	binding   collaboration.OperationBinding
}

func (c humanCommandObservation) valid(o collaboration.OperationLookup) bool {
	if o.Validate() != nil || o.Host != c.host || o.Authority != c.authority || o.OperationID != c.operation {
		return false
	}
	if o.Record == nil {
		return true
	}
	r := o.Record
	if len(r.Checkpoints) != 0 || r.Effect == collaboration.EffectPartial {
		return false
	}
	if r.Binding == nil {
		return r.Effect == collaboration.EffectNotCommitted && r.Cancellation == collaboration.CancellationCancelled && r.Completion == collaboration.CompletionComplete && r.Receipt == nil
	}
	if *r.Binding != c.binding {
		return false
	}
	if r.Effect == collaboration.EffectCommitted {
		// The receipt pins provider intent; the binding pins the product command.
		if r.Receipt == nil || r.Receipt.Authority != c.authority || r.Receipt.Reference != c.operation || len(r.Receipt.IntentDigest) != 64 {
			return false
		}
		for _, b := range r.Receipt.IntentDigest {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return false
			}
		}
	} else if r.Receipt != nil || r.Effect == collaboration.EffectNotCommitted && r.Completion != collaboration.CompletionComplete {
		return false
	}
	return true
}

// Needs-intervention remains open for an explicit later resolution.
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

func retainHumanCancellationUncertainty(old, next collaboration.OperationLookup) collaboration.OperationLookup {
	// A local cancellation can race the causal provider reply. Preserve the
	// cancellation uncertainty without discarding a proved terminal primary
	// effect, including one whose separate completion is unfinished. A later
	// completion reply must retain that uncertainty too.
	if old.Record != nil && next.Record != nil &&
		old.Record.Cancellation == collaboration.CancellationIndeterminate &&
		(next.Record.Effect == collaboration.EffectCommitted || next.Record.Effect == collaboration.EffectNotCommitted) &&
		next.Record.Cancellation == collaboration.CancellationNone {
		record := *next.Record
		record.Cancellation = old.Record.Cancellation
		next.Record = &record
	}
	return next
}
