package factory

import (
	"errors"
	"fmt"
)

// Native operation effect states mirrored from the conditional-operation
// contract. Pending work may still resolve; committed and not_committed
// are terminal native verdicts; indeterminate keeps a retained native
// fence owned by offline recovery.
const (
	OpEffectPending       = "pending"
	OpEffectCommitted     = "committed"
	OpEffectNotCommitted  = "not_committed"
	OpEffectIndeterminate = "indeterminate"
)

// ValidOpEffect reports whether effect is a known native effect state.
func ValidOpEffect(effect string) bool {
	switch effect {
	case OpEffectPending, OpEffectCommitted, OpEffectNotCommitted, OpEffectIndeterminate:
		return true
	default:
		return false
	}
}

// Native cancellation states mirrored from the contract. None means no
// cancellation was ever requested; pending means the ordering against an
// in-flight write is still unknown.
const (
	OpCancelNone          = "none"
	OpCancelPending       = "pending"
	OpCancelCancelled     = "cancelled"
	OpCancelTooLate       = "too_late"
	OpCancelIndeterminate = "indeterminate"
)

// ValidOpCancellation reports whether status is a known cancellation state.
func ValidOpCancellation(status string) bool {
	switch status {
	case OpCancelNone, OpCancelPending, OpCancelCancelled, OpCancelTooLate, OpCancelIndeterminate:
		return true
	default:
		return false
	}
}

// PublicationOperation is one persisted conditional operation. Work is
// its immutable intent, recorded before submission. Effect, Cancellation and
// Completion mirror the latest native observations; Reason carries the
// bounded native reason code; Receipt carries the bounded raw native
// receipt. Decoded link fields are adopted only from this operation's own
// committed receipt and verified against the submitted intent.
type PublicationOperation struct {
	Work           *PublicationIntent `json:"work,omitempty"`
	RunID          string             `json:"run_id,omitempty"`
	OperationID    string             `json:"operation_id"`
	InstallationID string             `json:"installation_id,omitempty"`
	ActorID        int64              `json:"actor_id,omitempty"`
	RepositoryID   int64              `json:"repository_id,omitempty"`
	Kind           string             `json:"kind"`
	Effect         string             `json:"effect,omitempty"`
	Cancellation   string             `json:"cancellation,omitempty"`
	Completion     string             `json:"completion,omitempty"`
	Reason         string             `json:"reason,omitempty"`
	Receipt        string             `json:"receipt,omitempty"`
	HeadRef        string             `json:"head_ref,omitempty"`
	BaseRef        string             `json:"base_ref,omitempty"`
	HeadOID        string             `json:"head_oid,omitempty"`
	BaseOID        string             `json:"base_oid,omitempty"`
	Attempts       int                `json:"attempts"`
	PRNumber       int64              `json:"pr_number,omitempty"`
	PRID           int64              `json:"pr_id,omitempty"`
	IssueID        int64              `json:"issue_id,omitempty"`
	UpdatedUnix    int64              `json:"updated_unix"`
}

// Validate rejects malformed operation records. An unsubmitted operation
// carries its kind and no observations; a submitted one carries its exact
// identity, attempt count and record time, with links only for its own
// kind. The effect stays empty while its submit is in flight.
func (o PublicationOperation) Validate() error {
	if o.RunID != "" && !ValidID(o.RunID) {
		return errors.New("invalid publication operation run")
	}
	if o.Kind != OpRefPublish && o.Kind != OpPRCreate {
		return errors.New("invalid publication operation kind")
	}
	if o.Attempts < 0 || o.Attempts > MaxPublicationAttempts {
		return errors.New("invalid publication attempt count")
	}
	if o.Attempts == 0 {
		return o.validateUnsubmittedOperation()
	}
	if !ValidPublicationOperationID(o.OperationID) {
		return errors.New("invalid publication operation identity")
	}
	if o.Work == nil || o.Work.OperationID != o.OperationID {
		return errors.New("publication operation lacks its immutable intent")
	}
	if err := o.Work.Validate(); err != nil {
		return err
	}
	return o.validateSubmittedOperation()
}

// validateUnsubmittedOperation refuses unsubmitted operations carrying
// any observation, then checks their links.
func (o PublicationOperation) validateUnsubmittedOperation() error {
	if o.RunID != "" || o.Work != nil || o.OperationID != "" || o.InstallationID != "" || o.ActorID != 0 || o.RepositoryID != 0 || o.Effect != "" || o.Cancellation != "" ||
		o.Completion != "" || o.Reason != "" || o.Receipt != "" || o.UpdatedUnix != 0 {
		return errors.New("unsubmitted operation carries no observations")
	}
	return o.validateLinks()
}

// validateSubmittedOperation refuses submitted operations with unknown
// effects, missing record time, invalid states or oversized
// observations, then checks their links.
func (o PublicationOperation) validateSubmittedOperation() error {
	// An empty effect records a submit in flight: the identity is
	// persisted before its submit so a crash reconciles by lookup and a
	// withdrawal cancels by the same identity. The next pass reconciles
	// it before anything else.
	if o.Effect != "" && !ValidOpEffect(o.Effect) {
		return errors.New("submitted operation effect is unknown")
	}
	if o.UpdatedUnix <= 0 {
		return errors.New("submitted operation lacks its record time")
	}
	if o.Cancellation != "" && !ValidOpCancellation(o.Cancellation) {
		return errors.New("invalid publication cancellation state")
	}
	if o.Completion != "" && !ValidOpCompletion(o.Completion) {
		return errors.New("invalid publication completion state")
	}
	if len(o.Reason) > 64 || len(o.Receipt) > MaxPublicationReceipt {
		return errors.New("publication observation exceeds bounds")
	}
	return o.validateLinks()
}

func (o PublicationOperation) validateLinks() error {
	if o.PRNumber < 0 || o.PRID < 0 || o.IssueID < 0 {
		return errors.New("invalid publication native links")
	}
	if o.Kind == OpRefPublish {
		return validateBranchOperationLinks(o)
	}
	return validatePullRequestLinks(o)
}

// validateBranchOperationLinks refuses branch operations carrying any
// PR links or snapshot.
func validateBranchOperationLinks(o PublicationOperation) error {
	if o.PRNumber != 0 || o.PRID != 0 || o.IssueID != 0 {
		return errors.New("branch operation carries no PR links")
	}
	if o.HeadRef != "" || o.BaseRef != "" || o.HeadOID != "" || o.BaseOID != "" {
		return errors.New("branch operation carries no PR snapshot")
	}
	return nil
}

// validatePullRequestLinks refuses PR creations with partial links,
// unverified links or malformed snapshots. A committed creation may
// await adoption: links arrive only from its own verified receipt. An
// uncommitted creation carries no links, and partial links never
// stand.
func validatePullRequestLinks(o PublicationOperation) error {
	if (o.PRNumber == 0) != (o.PRID == 0) || (o.PRNumber == 0) != (o.IssueID == 0) {
		return errors.New("incomplete publication PR links")
	}
	if o.Effect != OpEffectCommitted && o.PRNumber != 0 {
		return errors.New("uncommitted PR creation carries no PR links")
	}
	if (o.HeadRef != "" && !ValidTargetBranch(o.HeadRef)) || (o.BaseRef != "" && !ValidTargetBranch(o.BaseRef)) {
		return errors.New("invalid publication PR snapshot refs")
	}
	if (o.HeadOID != "" && !ValidCommit(o.HeadOID)) || (o.BaseOID != "" && !ValidCommit(o.BaseOID)) {
		return errors.New("invalid publication PR snapshot tips")
	}
	if o.PRNumber != 0 && (o.HeadRef == "" || o.BaseRef == "" || o.HeadOID == "" || o.BaseOID == "") {
		return errors.New("publication PR links lack their snapshot")
	}
	return nil
}

// ValidPublicationOperationID reports whether id is admissible as a native
// conditional operation identity: the contract's alphabet, bounded length.
func ValidPublicationOperationID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == ':' {
			continue
		}
		return false
	}
	return true
}

// PublicationOperationID derives the deterministic identity for one
// publication's kind attempt. The identity is persisted before its submit
// so a lost reply reconciles by lookup instead of resubmitting blindly;
// terminal identities are never reused.
func PublicationOperationID(publicationID, kind string, attempt int) string {
	suffix := "publish"
	if kind == OpPRCreate {
		suffix = "prcreate"
	}
	return fmt.Sprintf("soda-%s-%s-%d", publicationID, suffix, attempt)
}

// PublicationBranch derives the deterministic attributable branch for one
// assignment's publication. Corrections republish to this same branch with
// the prior tip as the expected lease; nothing else may ride along.
func PublicationBranch(assignmentID string) string {
	return "soda/factory/" + assignmentID
}
