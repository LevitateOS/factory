package factory

// D14 capability/submission DTOs (FW38).
//
// A capability binds one run to one assignment, profile, subject and
// input snapshot. A submission is one immutable tool call under that
// capability. Store persists both; control admits both; transport
// binds every request to both. Secrets never appear here: the
// per-capability secret derives from the immutable identity plus a
// service-private key held outside these DTOs, and rows retain only
// the ID and scope.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"
)

// Capability states. Only active capabilities admit calls; expiry,
// stop intent, withdrawal and terminal retirement revoke access.
const (
	MCPCapabilityStaged  = "staged"
	MCPCapabilityActive  = "active"
	MCPCapabilityRevoked = "revoked"
	MCPCapabilityRetired = "retired"
)

// MCPCapability is the recorded run authority. Generation orders
// re-deliveries of the same run: exact restart delivery reproduces
// the same capability, never a new deadline or allowance.
type MCPCapability struct {
	ID string `json:"id"`
	// ContextSelectionSnapshot is controller-owned reservation metadata.
	// It never changes the immutable credential scope or grants authority.
	ContextSelectionSnapshot string                    `json:"context_selection_snapshot,omitempty"`
	ContextCurrentResolution *ContextCurrentResolution `json:"context_current_resolution,omitempty"`
	AssignmentID             string                    `json:"assignment_id"`
	RunID                    string                    `json:"run_id"`
	Generation               int64                     `json:"generation"`
	Profile                  MCPProfile                `json:"profile"`
	Subject                  string                    `json:"subject"`
	Repository               int64                     `json:"repository"`
	Project                  string                    `json:"project"`
	PreparationDigest        string                    `json:"preparation_digest"`
	SourceDigest             string                    `json:"source_digest"`
	AcceptedRevision         string                    `json:"accepted_revision"`
	Authority                string                    `json:"authority"`
	DeadlineUnix             int64                     `json:"deadline_unix"`
	State                    string                    `json:"state"`
}

// Validate checks the immutable identity and scope of a capability.
// Deadline freshness and stop state are admission checks, not shape.
func (c MCPCapability) Validate() error {
	if !ValidID(c.ID) {
		return errors.New("invalid capability identity")
	}
	if !ValidID(c.AssignmentID) {
		return errors.New("invalid capability assignment")
	}
	if c.RunID == "" || len(c.RunID) > MCPMaxRefChars {
		return errors.New("invalid capability run")
	}
	if c.Generation < 1 {
		return errors.New("invalid capability generation")
	}
	if MCPPhaseOf(c.Profile) == "" {
		return errors.New("unknown capability profile")
	}
	if c.Subject == "" || len(c.Subject) > MCPMaxRefChars {
		return errors.New("invalid capability subject")
	}
	if c.Repository < 1 {
		return errors.New("invalid capability repository")
	}
	if !ValidProjectID(c.Project) {
		return errors.New("invalid capability project")
	}
	for name, digest := range map[string]string{
		"preparation": c.PreparationDigest,
		"authority":   c.Authority,
	} {
		if !ValidDigest(digest) {
			return errors.New("invalid capability " + name + " digest")
		}
	}
	if c.SourceDigest == "" || c.AcceptedRevision == "" {
		return errors.New("capability source snapshot incomplete")
	}
	switch c.State {
	case MCPCapabilityStaged, MCPCapabilityActive, MCPCapabilityRevoked, MCPCapabilityRetired:
	default:
		return errors.New("unknown capability state")
	}
	return nil
}

// Expired reports whether the capability is past its deadline at now.
func (c MCPCapability) Expired(now time.Time) bool {
	return !now.Before(time.Unix(c.DeadlineUnix, 0))
}

// ScopeDigest binds the immutable identity: any change to assignment,
// run, generation, profile, subject, repository, project or input
// snapshot changes the scope, so a replayed capability with edited
// bytes cannot borrow the original authority.
func (c MCPCapability) ScopeDigest() string {
	sum := sha256.Sum256([]byte(
		c.ID + "\n" + c.AssignmentID + "\n" + c.RunID + "\n" +
			strconv.FormatInt(c.Generation, 10) + "\n" + string(c.Profile) + "\n" + c.Subject + "\n" +
			strconv.FormatInt(c.Repository, 10) + "\n" + c.Project + "\n" + c.PreparationDigest + "\n" +
			c.SourceDigest + "\n" + c.AcceptedRevision + "\n" + c.Authority + "\n" +
			strconv.FormatInt(c.DeadlineUnix, 10) + "\n"))
	return hex.EncodeToString(sum[:])
}

// MCPSecretDeriver is the bounded native custody port. The product supplies
// only a recorded capability; its persistent root key stays with the native
// owner. A failed derivation must never fall back to another key.
type MCPSecretDeriver func(context.Context, MCPCapability) (string, error)

// ValidMCPSecret checks the fixed wire encoding of a derived 256-bit secret.
func ValidMCPSecret(secret string) bool {
	if len(secret) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(secret)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == secret
}

// DeriveMCPCapabilitySecret derives the per-capability secret with
// HMAC-SHA256 over the immutable identity and scope digest under the
// service-private persistent key. Host-owned delivery stages the
// secret separately from provider credentials; callers prove it
// without the server ever storing secret bytes beside the row.
func DeriveMCPCapabilitySecret(key []byte, capability MCPCapability) (string, error) {
	if len(key) < 32 {
		return "", errors.New("capability key below 256 bits")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(capability.ID + "\n" + capability.ScopeDigest()))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Controller processing states on submitted work posts.
const (
	MCPWorkRecorded  = "recorded"
	MCPWorkVerifying = "verifying"
	MCPWorkAccepted  = "accepted"
	MCPWorkRefused   = "refused"
)

// MCPSubmission carries one worker's work to its post writer. The assignment
// and submission ID form its post idempotency key. Persistence holds the
// authorizing capability's row lock through the write.
type MCPSubmission struct {
	AssignmentID string  `json:"assignment_id"`
	SubmissionID string  `json:"submission_id"`
	CapabilityID string  `json:"capability_id"`
	Tool         MCPTool `json:"tool"`
	Payload      []byte  `json:"payload,omitempty"`
}

// MCPInputDigest identifies content used by native inputs and posts.
func MCPInputDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// MCPWorkStatus projects the controller's processing state from a work post.
type MCPWorkStatus struct {
	Checkpoint   *execution.CheckpointReceipt `json:"checkpoint,omitempty"`
	FeedPostID   string                       `json:"feed_post_id,omitempty"`
	SubmissionID string                       `json:"submission_id"`
	Tool         MCPTool                      `json:"tool"`
	State        string                       `json:"state"`
	Reason       string                       `json:"reason,omitempty"`
}
