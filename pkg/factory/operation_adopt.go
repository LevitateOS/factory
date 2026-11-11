package factory

import "github.com/levitateos/factory/pkg/collaboration"

// State returns the outcome's native triple.
func (o OperationOutcome) State() collaboration.OperationState {
	return collaboration.OperationState{Effect: o.Effect, Cancellation: o.Cancellation, Completion: o.Completion}
}

// State returns the operation's recorded native triple.
func (o PublicationOperation) State() collaboration.OperationState {
	return collaboration.OperationState{Effect: o.Effect, Cancellation: o.Cancellation, Completion: o.Completion}
}

// State returns the merge operation's recorded native triple.
func (o MergeOperation) State() collaboration.OperationState {
	return collaboration.OperationState{Effect: o.Effect, Cancellation: o.Cancellation, Completion: o.Completion}
}

// Outcome returns the operation's recorded native answer.
func (o PublicationOperation) Outcome() OperationOutcome {
	return OperationOutcome{
		Receipt: []byte(o.Receipt), Effect: o.Effect, Cancellation: o.Cancellation, Completion: o.Completion, Reason: o.Reason,
		OperationID: o.OperationID, InstallationID: o.InstallationID, Kind: o.Kind, ActorID: o.ActorID, RepositoryID: o.RepositoryID,
	}
}

// Outcome returns the merge operation's recorded native answer.
func (o MergeOperation) Outcome() OperationOutcome {
	return OperationOutcome{
		Receipt: []byte(o.Receipt), Effect: o.Effect, Cancellation: o.Cancellation, Completion: o.Completion, Reason: o.Reason,
		OperationID: o.OperationID, InstallationID: o.InstallationID, Kind: o.Kind, ActorID: o.ActorID, RepositoryID: o.RepositoryID,
	}
}

// SameOutcome reports whether observed is an attributable repeat of the
// recorded answer, so adopting it would change nothing.
func (o PublicationOperation) SameOutcome(observed OperationOutcome) bool {
	return o.Work != nil && sameRecordedOutcome(o.Outcome(), o.Work.ActorID, o.Work.Repository, observed)
}

// SameOutcome reports whether observed is an attributable repeat of the
// recorded answer, so adopting it would change nothing.
func (o MergeOperation) SameOutcome(observed OperationOutcome) bool {
	return o.Work != nil && sameRecordedOutcome(o.Outcome(), o.Work.ActorID, o.Work.Repository, observed)
}

// Adopt records one provider observation under collaboration.FollowProvider,
// stamping UpdatedUnix only when the recorded answer changed. False means
// the observation is unattributable to this operation's intent or
// contradicts its recorded terminal effect or receipt; nothing is written.
func (o *PublicationOperation) Adopt(observed OperationOutcome, now int64) bool {
	if o.Work == nil {
		return false
	}
	next, ok := adoptFollowingProvider(o.Outcome(), o.Work.ActorID, o.Work.Repository, observed)
	if !ok {
		return false
	}
	changed := !sameAdoptedFields(o.Outcome(), next)
	o.Effect, o.Cancellation, o.Completion, o.Reason = next.Effect, next.Cancellation, next.Completion, next.Reason
	o.Receipt = string(next.Receipt)
	o.InstallationID, o.ActorID, o.RepositoryID = next.InstallationID, next.ActorID, next.RepositoryID
	if changed {
		o.UpdatedUnix = now
	}
	return true
}

// Adopt records one provider observation under collaboration.FollowProvider,
// exactly as PublicationOperation.Adopt does.
func (o *MergeOperation) Adopt(observed OperationOutcome, now int64) bool {
	if o.Work == nil {
		return false
	}
	next, ok := adoptFollowingProvider(o.Outcome(), o.Work.ActorID, o.Work.Repository, observed)
	if !ok {
		return false
	}
	changed := !sameAdoptedFields(o.Outcome(), next)
	o.Effect, o.Cancellation, o.Completion, o.Reason = next.Effect, next.Cancellation, next.Completion, next.Reason
	o.Receipt = string(next.Receipt)
	o.InstallationID, o.ActorID, o.RepositoryID = next.InstallationID, next.ActorID, next.RepositoryID
	if changed {
		o.UpdatedUnix = now
	}
	return true
}

// adoptFollowingProvider checks that observed belongs to the recorded
// identity and intent (a pre-submit cancellation tombstone carries no
// kind/actor/repository), then applies collaboration.FollowProvider. On
// adoption the observation replaces the recorded answer wholesale.
func adoptFollowingProvider(recorded OperationOutcome, workActor, workRepository int64, observed OperationOutcome) (OperationOutcome, bool) {
	if observed.NotObserved || observed.OperationID != recorded.OperationID {
		return recorded, false
	}
	if recorded.InstallationID != "" && recorded.InstallationID != observed.InstallationID {
		return recorded, false
	}
	tombstone := observed.Kind == "" && observed.Effect == OpEffectNotCommitted && observed.Cancellation == OpCancelCancelled
	if !tombstone && (observed.Kind != recorded.Kind || observed.ActorID != workActor || observed.RepositoryID != workRepository || observed.InstallationID == "") {
		return recorded, false
	}
	step := collaboration.OperationStep{
		Old: recorded.State(), Next: observed.State(),
		ReceiptChanged: len(recorded.Receipt) != 0 && string(recorded.Receipt) != string(observed.Receipt),
	}
	if collaboration.FollowProvider.Advance(step) != collaboration.AdvanceAdopt {
		return recorded, false
	}
	recorded.Effect, recorded.Cancellation, recorded.Completion, recorded.Reason = observed.Effect, observed.Cancellation, observed.Completion, observed.Reason
	recorded.Receipt = observed.Receipt
	recorded.InstallationID, recorded.ActorID, recorded.RepositoryID = observed.InstallationID, observed.ActorID, observed.RepositoryID
	return recorded, true
}

func sameAdoptedFields(a, b OperationOutcome) bool {
	return a.InstallationID == b.InstallationID && a.ActorID == b.ActorID && a.RepositoryID == b.RepositoryID &&
		string(a.Receipt) == string(b.Receipt) && a.State() == b.State() && a.Reason == b.Reason
}

func sameRecordedOutcome(recorded OperationOutcome, workActor, workRepository int64, observed OperationOutcome) bool {
	return observed.InstallationID != "" && workActor == observed.ActorID && workRepository == observed.RepositoryID &&
		!observed.NotObserved && recorded.OperationID == observed.OperationID && recorded.Kind == observed.Kind &&
		sameAdoptedFields(recorded, observed)
}
