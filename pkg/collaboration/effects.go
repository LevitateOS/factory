package collaboration

import (
	"bytes"
	"encoding/json"
	"errors"
)

// OperationKind names a semantic effect, not an HTTP endpoint. Support is
// discovered at admission; a known spelling does not imply provider support.
type OperationKind string

const (
	MutateGraph          OperationKind = "issue.graph.mutate"
	PublishRef           OperationKind = "git.ref.publish"
	OpenPull             OperationKind = "pull_request.create"
	SubmitReview         OperationKind = "pull_request.review.submit"
	CreateReviewComment  OperationKind = "pull_request.review.comment.create"
	ResolveReviewThread  OperationKind = "pull_request.review.thread.resolve"
	Merge                OperationKind = "pull_request.merge"
	ApplyIssueLifecycle  OperationKind = "issue.lifecycle.apply"
	RequestReview        OperationKind = "pull_request.review.request"
	PublishComment       OperationKind = "issue.comment.publish"
	CreateComment        OperationKind = "discussion.comment.create"
	EditDiscussion       OperationKind = "discussion.edit"
	OpenIssue            OperationKind = "issue.create"
	ApplyDiscussionLock  OperationKind = "discussion.lock.apply"
	ApplyDiscussionState OperationKind = "discussion.state.apply"
)

type Atomicity string

const (
	Atomic  Atomicity = "atomic"
	Ordered Atomicity = "ordered"
)

// MutationConditions are REQUIRED guarantees, not best-effort read checks.
// Adapters refuse unsupported guards before sending effects. In particular,
// reading a head and then calling an unguarded write is not a conditional write.
// AuthorizationRevision refers to the application's admitted authority; it is
// not a token, browser capability, execution grant or standing spending policy.
type MutationConditions struct {
	AuthorizationRevision string              `json:"authorization_revision"`
	RepositoryRevision    *Revision           `json:"repository_revision"`
	Resources             []ResourceCondition `json:"resources"`
	NotAfter              int64               `json:"not_after"` // Unix seconds; checked afresh at dispatch
}

type ResourceCondition struct {
	Kind       string    `json:"kind"`        // issue, pull, ref, thread
	ExternalID string    `json:"external_id"` // opaque resource ID or exact ref name
	Revision   *Revision `json:"revision"`
	Exists     *bool     `json:"exists"`
	Head       *CommitID `json:"head"`
	Base       *CommitID `json:"base"`
}

// OperationIntent is persisted by the existing owning journal BEFORE dispatch.
// IDs and payload never change on retry. Connection selection and credentials
// remain outside intent identity. Payload is the bounded, typed kind-specific
// semantic JSON, not an arbitrary provider request or browser-controlled URL.
// Atomicity is required: ordered effects must never satisfy an atomic intent.
// Authority pins the adapter's operation namespace (Fountain installation, or
// the local effect journal for a provider without remote operation receipts).
// It is neither repository identity nor evidence of remote atomicity.
type OperationIntent struct {
	OperationID string             `json:"operation_id"`
	Authority   string             `json:"authority"`
	Host        HostID             `json:"host"`
	Repository  RepositoryID       `json:"repository,string"`
	Actor       ActorID            `json:"actor,string"`
	Kind        OperationKind      `json:"kind"`
	Atomicity   Atomicity          `json:"atomicity"`
	Conditions  MutationConditions `json:"conditions"`
	Payload     json.RawMessage    `json:"payload"`
}

// OperationBinding is the immutable identity echoed by an admitted outcome.
// IntentDigest is the owning application's captured digest. A provider's digest
// over its translated envelope is separate and MUST NOT replace it.
type OperationBinding struct {
	Repository   RepositoryID  `json:"repository,string"`
	Actor        ActorID       `json:"actor,string"`
	Kind         OperationKind `json:"kind"`
	Atomicity    Atomicity     `json:"atomicity"`
	IntentDigest string        `json:"intent_digest"`
}

type (
	EffectState       string
	CancellationState string
	CompletionState   string
)

const (
	EffectPending               EffectState       = "pending"
	EffectNotCommitted          EffectState       = "not_committed"
	EffectCommitted             EffectState       = "committed"
	EffectIndeterminate         EffectState       = "indeterminate"
	EffectPartial               EffectState       = "partial"
	CancellationNone            CancellationState = "none"
	CancellationPending         CancellationState = "pending"
	CancellationCancelled       CancellationState = "cancelled"
	CancellationTooLate         CancellationState = "too_late"
	CancellationIndeterminate   CancellationState = "indeterminate"
	CompletionPending           CompletionState   = "pending"
	CompletionComplete          CompletionState   = "complete"
	CompletionNeedsIntervention CompletionState   = "needs_intervention"
)

// ProviderReceipt retains original provider evidence, not a synthesized success.
// Authority binds the provider installation/receipt namespace; Reference is its
// operation or request identity. Raw receipt bytes stay server-side and must be
// credential-free. Markers/search/readback are observations, not durable receipts
// of an attributable write. Translators retain them separately as observations.
type ProviderReceipt struct {
	Authority    string          `json:"authority"`
	Reference    string          `json:"reference"`
	IntentDigest string          `json:"intent_digest"`
	Payload      json.RawMessage `json:"payload"`
}

// EffectCheckpoint is a stable, preassigned step in an ordered plan (e.g. issue
// creation, branch publication, PR creation). Save each outcome before advancing.
// Partial is only an aggregate state; an individual step stays unresolved if its
// response was lost. Observed objects do not establish causal write ownership.
type EffectCheckpoint struct {
	ID      string           `json:"id"`
	Effect  EffectState      `json:"effect"`
	Reason  string           `json:"reason"`
	Receipt *ProviderReceipt `json:"receipt"`
}

// OperationRecord is data stored within existing Factory journals, not a second
// journal. Binding is nil only for a pre-submit cancellation tombstone; never
// fabricate actor/repository/kind/digest fields when the provider did not know
// them. Cancellation cannot erase already committed or uncertain effects.
type OperationRecord struct {
	Host         HostID             `json:"host"`
	Authority    string             `json:"authority"`
	OperationID  string             `json:"operation_id"`
	Binding      *OperationBinding  `json:"binding"`
	Effect       EffectState        `json:"effect"`
	Cancellation CancellationState  `json:"cancellation"`
	Completion   CompletionState    `json:"completion"`
	Reason       string             `json:"reason"`
	Receipt      *ProviderReceipt   `json:"receipt"`
	Checkpoints  []EffectCheckpoint `json:"checkpoints"`
}

type LookupState string

const (
	LookupFound       LookupState = "found"
	LookupNotObserved LookupState = "not_observed"
	LookupUnavailable LookupState = "unavailable"
	LookupUnknown     LookupState = "unknown"
)

// NotObserved is an observation, never proof that an earlier request cannot
// arrive or that resubmitting a non-idempotent write is safe. Unavailable differs
// from absent. No retry authorization can be derived from these fields alone.
type OperationLookup struct {
	Authority   string           `json:"authority"`
	Host        HostID           `json:"host"`
	OperationID string           `json:"operation_id"`
	State       LookupState      `json:"state"`
	Record      *OperationRecord `json:"record"`
	Reason      string           `json:"reason"`
}

var ErrInvalidEffect = errors.New("invalid collaboration effect contract")

// Validate checks shape and internal consistency, not permission, freshness,
// receipt authenticity, kind-specific payload semantics or retry eligibility.
func (i OperationIntent) Validate() error {
	if !validOperationScope(i.Host, i.Authority, i.OperationID) || i.Repository <= 0 || i.Actor <= 0 ||
		!ValidText(string(i.Kind), 128) || !validAtomicity(i.Atomicity) ||
		!ValidText(i.Conditions.AuthorizationRevision, 256) || i.Conditions.NotAfter <= 0 ||
		!validOptionalRevision(i.Conditions.RepositoryRevision) || len(i.Conditions.Resources) > 64 ||
		!validEffectJSON(i.Payload, 48<<10) {
		return ErrInvalidEffect
	}
	seen := map[string]bool{}
	for _, c := range i.Conditions.Resources {
		key := c.Kind + "\x00" + c.ExternalID
		if (c.Kind != "issue" && c.Kind != "pull" && c.Kind != "ref" && c.Kind != "thread") ||
			!ValidText(c.ExternalID, 1024) || seen[key] || !validOptionalRevision(c.Revision) ||
			(c.Revision == nil && c.Exists == nil && c.Head == nil && c.Base == nil) ||
			(c.Head != nil && !ValidCommitID(*c.Head)) || (c.Base != nil && !ValidCommitID(*c.Base)) {
			return ErrInvalidEffect
		}
		seen[key] = true
	}
	return nil
}

func validOperationScope(host HostID, authority, operation string) bool {
	return ValidID(string(host)) && ValidText(authority, 256) && ValidText(operation, 256)
}

func validAtomicity(a Atomicity) bool { return a == Atomic || a == Ordered }

func validOptionalRevision(r *Revision) bool { return r == nil || ValidText(string(*r), 256) }

func validEffectJSON(raw json.RawMessage, limit int) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(raw) <= limit && len(trimmed) > 1 && trimmed[0] == '{' && json.Valid(trimmed)
}

// ValidCommitID accepts a full lowercase SHA-1 or SHA-256 Git object identity.
func ValidCommitID(c CommitID) bool {
	return (len(c) == 40 || len(c) == 64) && lowercaseHex(string(c))
}

func lowercaseHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
