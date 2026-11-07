package factory

import (
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
)

// GraphOperationOutcome is the result consumed by the existing generation
// journal. The adapter checks its provider namespace and known actor/repository
// binding before returning it. A pre-submit cancellation may have no binding.
// Receipt is original provider evidence; it is never re-encoded for storage.
// This narrow port does not invent the HostID required by the general
// collaboration.OperationRecord before the identity cutover.
type GraphOperationOutcome struct {
	OperationID  string
	Effect       collaboration.EffectState
	Cancellation collaboration.CancellationState
	Completion   collaboration.CompletionState
	Reason       string
	Receipt      json.RawMessage
}

// An absent record is retryable only when State is explicitly NotObserved
// and the existing accepted-head, owner, revision and deadline guards hold.
// Unknown or unavailable observations never authorize a new submission.
type GraphOperationLookup struct {
	State  collaboration.LookupState
	Record *GraphOperationOutcome
	// Resumable requires known committed prior steps and an unclaimed next step.
	// It never authorizes replay of an unknown provider send.
	Resumable bool
}
