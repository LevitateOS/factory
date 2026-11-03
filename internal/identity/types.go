// Package identity owns subscription connection, grant and execution contracts.
package identity

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrDenied    = errors.New("identity authority denied")
	ErrBusy      = errors.New("subscription is in use")
	ErrStale     = errors.New("identity generation changed")
	ErrUncertain = errors.New("subscription requires reconnection")
	ErrNotFound  = errors.New("identity execution missing")
)

const (
	Codex    = "codex"
	Muse     = "muse"
	Ready    = "ready"
	Reauth   = "reauth"
	Revoked  = "revoked"
	Factory  = "factory"
	Terminal = "terminal"
)

const (
	// ExecutionPending means the execution identity is admitted but no lease
	// has been reserved yet; a retry with the same digest may proceed.
	ExecutionPending = "pending"
	// ExecutionLive means a lease is reserved for this execution identity.
	ExecutionLive = "live"
	// ExecutionTerminal means the execution can never acquire again. The
	// nonsecret identity outlives lease return/deletion; a consumed or
	// closed execution cannot acquire a new lease.
	ExecutionTerminal = "terminal"
)

type Connection struct {
	ProviderID string `json:"provider_id"`
	ID         string `json:"id"`
	OwnerID    int64  `json:"owner_id,string"`
	Label      string `json:"label"`
	Email      string `json:"email"`
	Plan       string `json:"plan"`
	Generation int64  `json:"generation"`
	State      string `json:"state"`
}

type Grant struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connection_id"`
	UserID       int64  `json:"user_id,string"`
	ProjectID    string `json:"project_id"`
	Revision     int64  `json:"revision"`
	Revoked      bool   `json:"revoked"`
}

type GrantRequest struct {
	ConnectionID              string `json:"connection_id"`
	UserID                    int64  `json:"user_id,string"`
	ProjectID                 string `json:"project_id"`
	ConfirmSubscription       bool   `json:"confirm_subscription"`
	ConfirmCredentialExposure bool   `json:"confirm_credential_exposure"`
}

type AcquireRequest struct {
	// RepositoryID is supplied by trusted factory admission, never a guest.
	RepositoryID int64     `json:"repository_id,string,omitempty"`
	ProviderID   string    `json:"provider_id"`
	ExecutionID  string    `json:"execution_id"`
	ActorID      int64     `json:"actor_id,string"`
	ConnectionID string    `json:"connection_id"`
	ProjectID    string    `json:"project_id"`
	Kind         string    `json:"kind"`
	Deadline     time.Time `json:"deadline"`
	Role         string    `json:"role,omitempty"`
}

// Binding identifies a native process boundary, not a browser attachment.
type Binding struct {
	ChildID        string `json:"child_id,omitempty"`
	UID            int    `json:"uid,omitempty"`
	GID            int    `json:"gid,omitempty"`
	Scope          string `json:"scope,omitempty"`
	CredentialRoot string `json:"credential_root,omitempty"`
	InvocationID   string `json:"invocation_id,omitempty"`
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	Project        string `json:"project"`
	Login          string `json:"login"`
	Generation     int64  `json:"generation"`
}

type Lease struct {
	// RepositoryID records factory admission.
	RepositoryID  int64     `json:"repository_id,string,omitempty"`
	ProviderID    string    `json:"provider_id"`
	ID            string    `json:"id"`
	ConnectionID  string    `json:"connection_id"`
	Generation    int64     `json:"generation"`
	ActorID       int64     `json:"actor_id,string"`
	ProjectID     string    `json:"project_id"`
	ExecutionID   string    `json:"execution_id"`
	Kind          string    `json:"kind"`
	Role          string    `json:"role,omitempty"`
	Deadline      time.Time `json:"deadline"`
	GrantID       string    `json:"grant_id,omitempty"`
	GrantRevision int64     `json:"grant_revision,omitempty"`
	Binding       *Binding  `json:"binding,omitempty"`
}

type Enrollment struct {
	ProviderID      string      `json:"provider_id"`
	ID              string      `json:"id"`
	VerificationURL string      `json:"verification_url"`
	UserCode        string      `json:"user_code"`
	State           string      `json:"state"`
	Error           string      `json:"error,omitempty"`
	Connection      *Connection `json:"connection,omitempty"`
}

func (r GrantRequest) Validate() error {
	if r.ConnectionID == "" || r.UserID <= 0 || r.ProjectID == "" || !r.ConfirmSubscription || !r.ConfirmCredentialExposure {
		return ErrDenied
	}
	return nil
}

func (r AcquireRequest) Validate(now time.Time) error {
	if !ProviderValid(r.ProviderID) || r.ActorID <= 0 || r.ConnectionID == "" || r.ExecutionID == "" || (r.Kind != Factory && r.Kind != Terminal) || !r.Deadline.After(now) || r.Deadline.After(now.Add(24*time.Hour)) {
		return ErrDenied
	}
	return nil
}

func (b Binding) Validate() error {
	if b.ID == "" || b.Generation <= 0 || (b.Kind != Factory && b.Kind != Terminal) {
		return ErrDenied
	}
	if b.Kind == Terminal && (b.Project == "" || strings.TrimSpace(b.Login) == "") {
		return ErrDenied
	}
	return nil
}

func CredentialValid(data []byte) bool {
	return len(data) > 0 && len(data) <= 256<<10 && json.Valid(data)
}

// Execution is the unique (kind, execution_id) acquisition identity. The
// digest binds the immutable request; the same ID with a changed request
// refuses. Binding carries only nonsecret process metadata, never credentials.
type Execution struct {
	Binding     *Binding `json:"binding,omitempty"`
	Kind        string   `json:"kind"`
	ExecutionID string   `json:"execution_id"`
	Digest      string   `json:"digest"`
	State       string   `json:"state"`
	LeaseID     string   `json:"lease_id,omitempty"`
}

func (e Execution) Validate() error {
	if (e.Kind != Factory && e.Kind != Terminal) || e.ExecutionID == "" || len(e.ExecutionID) > 128 {
		return ErrDenied
	}
	switch e.State {
	case ExecutionPending, ExecutionLive, ExecutionTerminal:
	default:
		return ErrDenied
	}
	if e.State != ExecutionTerminal && e.Digest == "" {
		return ErrDenied
	}
	if e.Binding != nil {
		if err := e.Binding.Validate(); err != nil {
			return err
		}
		// Binding identity rules belong to the runtime adapter:
		// supervised factory runs bind run IDs. The record retains the
		// kind-matched binding the adapter attested.
		if e.Binding.Kind != e.Kind {
			return ErrDenied
		}
	}
	return nil
}

// ProviderValid identifies the supported native account adapters. Execution
// admission remains specific to the provider and its credential exposure model.
func ProviderValid(id string) bool { return id == Codex || id == Muse }
