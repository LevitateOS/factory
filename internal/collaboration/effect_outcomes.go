package collaboration

// ValidateAgainst checks the exact stored operation binding, before the owning
// journal decides whether this new observation may advance its durable state.
// Tombstones intentionally cannot pass this binding check; cancellation handling
// reconciles them separately without manufacturing a submitted intent receipt.
func (r OperationRecord) ValidateAgainst(i OperationIntent, digest string) error {
	if i.Validate() != nil || r.Validate() != nil || r.Host != i.Host || r.Authority != i.Authority || r.OperationID != i.OperationID ||
		r.Binding == nil || *r.Binding != (OperationBinding{
		Repository: i.Repository, Actor: i.Actor, Kind: i.Kind, Atomicity: i.Atomicity, IntentDigest: digest,
	}) {
		return ErrInvalidEffect
	}
	return nil
}

// Validate rejects contradictory outcomes without treating a receipt as trusted
// or authorizing journal transitions. The owning journal must compare immutable
// bindings and use its existing transaction/revision guards when saving it.
func (r OperationRecord) Validate() error {
	if !validOperationScope(r.Host, r.Authority, r.OperationID) || !validEffectState(r.Effect, true) ||
		!validCancellation(r.Cancellation) || !validCompletion(r.Completion) ||
		!validReason(r.Reason) || !validProviderReceipt(r.Receipt) || len(r.Checkpoints) > 64 {
		return ErrInvalidEffect
	}
	if r.Binding == nil {
		if r.Cancellation == CancellationNone || r.Receipt != nil || len(r.Checkpoints) != 0 ||
			r.Effect == EffectCommitted || r.Effect == EffectPartial {
			return ErrInvalidEffect
		}
	} else {
		b := r.Binding
		if b.Repository <= 0 || b.Actor <= 0 || !ValidText(string(b.Kind), 128) ||
			!validAtomicity(b.Atomicity) || len(b.IntentDigest) != 64 || !lowercaseHex(b.IntentDigest) ||
			(b.Atomicity == Atomic && (r.Effect == EffectPartial || len(r.Checkpoints) > 1)) {
			return ErrInvalidEffect
		}
	}
	if r.Completion == CompletionComplete && (r.Effect == EffectPending || r.Effect == EffectIndeterminate || r.Effect == EffectPartial) {
		return ErrInvalidEffect
	}
	if len(r.Checkpoints) == 0 {
		if r.Effect == EffectPartial {
			return ErrInvalidEffect
		}
		return nil
	}
	seen := map[string]bool{}
	committed, pending, uncertain := 0, false, false
	for _, c := range r.Checkpoints {
		if !ValidText(c.ID, 128) || seen[c.ID] || !validEffectState(c.Effect, false) ||
			!validReason(c.Reason) || !validProviderReceipt(c.Receipt) {
			return ErrInvalidEffect
		}
		seen[c.ID] = true
		switch c.Effect {
		case EffectCommitted:
			committed++
		case EffectPending:
			pending = true
		case EffectIndeterminate:
			uncertain = true
		}
	}
	want := EffectNotCommitted
	switch {
	case committed == len(r.Checkpoints):
		want = EffectCommitted
	case committed > 0:
		want = EffectPartial
	case uncertain:
		want = EffectIndeterminate
	case pending:
		want = EffectPending
	}
	if r.Effect != want {
		return ErrInvalidEffect
	}
	return nil
}

func (l OperationLookup) Validate() error {
	if !validOperationScope(l.Host, l.Authority, l.OperationID) || !validReason(l.Reason) {
		return ErrInvalidEffect
	}
	switch l.State {
	case LookupFound:
		if l.Record == nil || l.Record.Host != l.Host || l.Record.Authority != l.Authority || l.Record.OperationID != l.OperationID {
			return ErrInvalidEffect
		}
		return l.Record.Validate()
	case LookupNotObserved, LookupUnavailable, LookupUnknown:
		if l.Record != nil {
			return ErrInvalidEffect
		}
		return nil
	default:
		return ErrInvalidEffect
	}
}

func validReason(s string) bool { return s == "" || ValidText(s, 1024) }

func validProviderReceipt(r *ProviderReceipt) bool {
	return r == nil || (ValidText(r.Authority, 256) && ValidText(r.Reference, 256) &&
		(r.IntentDigest == "" || ValidText(r.IntentDigest, 256)) && validEffectJSON(r.Payload, 64<<10))
}

func validEffectState(s EffectState, aggregate bool) bool {
	switch s {
	case EffectPending, EffectNotCommitted, EffectCommitted, EffectIndeterminate:
		return true
	case EffectPartial:
		return aggregate
	default:
		return false
	}
}

func validCancellation(s CancellationState) bool {
	switch s {
	case CancellationNone, CancellationPending, CancellationCancelled, CancellationTooLate, CancellationIndeterminate:
		return true
	default:
		return false
	}
}

func validCompletion(s CompletionState) bool {
	return s == CompletionPending || s == CompletionComplete || s == CompletionNeedsIntervention
}
