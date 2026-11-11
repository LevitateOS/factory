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

// advanceHumanCommandObservation applies the KeepUncertainty policy to two
// validated observations of the same captured command.
func advanceHumanCommandObservation(old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	if old.Record == nil {
		return next, nil
	}
	if next.Record == nil {
		return old, nil
	}
	a, b := old.Record, next.Record
	step := collaboration.OperationStep{
		Old:            humanCommandState(a),
		Next:           humanCommandState(b),
		ReceiptChanged: a.Receipt != nil && (b.Receipt == nil || a.Receipt.Authority != b.Receipt.Authority || a.Receipt.Reference != b.Receipt.Reference || a.Receipt.IntentDigest != b.Receipt.IntentDigest || !bytes.Equal(a.Receipt.Payload, b.Receipt.Payload)),
		BindingDropped: a.Binding != nil && b.Binding == nil,
	}
	switch collaboration.KeepUncertainty.Advance(step) {
	case collaboration.AdvanceConflict:
		return collaboration.OperationLookup{}, errHumanCommandObservation
	case collaboration.AdvanceKeep:
		return old, nil
	default:
		return next, nil
	}
}

func humanCommandState(r *collaboration.OperationRecord) collaboration.OperationState {
	return collaboration.OperationState{Effect: r.Effect, Cancellation: r.Cancellation, Completion: r.Completion}
}

func retainHumanCancellationUncertainty(old, next collaboration.OperationLookup) collaboration.OperationLookup {
	// A local cancellation can race the causal provider reply. Preserve the
	// cancellation uncertainty without discarding a proved terminal primary
	// effect, including one whose separate completion is unfinished. A later
	// completion reply must retain that uncertainty too.
	if old.Record != nil && next.Record != nil {
		if kept := collaboration.RetainCancellationUncertainty(humanCommandState(old.Record), humanCommandState(next.Record)); kept != next.Record.Cancellation {
			record := *next.Record
			record.Cancellation = kept
			next.Record = &record
		}
	}
	return next
}
