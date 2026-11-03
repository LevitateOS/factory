// Package access owns Factory application admission contracts. Provider/HTTP
// adapters establish current authority; browser capabilities are presentation.
package access

import (
	"errors"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
)

type (
	AdmissionSource string
	Permission      string
)

const (
	FountainSession AdmissionSource = "fountain_session"
	NativeAccount   AdmissionSource = "native_account"
	FountainGateway AdmissionSource = "fountain_gateway"
	RuntimeGrant    AdmissionSource = "runtime_grant"
	Read            Permission      = "factory.read"
	Collaborate     Permission      = "collaboration.write"
	Configure       Permission      = "factory.configure"
)

var ErrDenied = errors.New("factory authority unavailable or denied")

// ActorEvidence is INTERNAL, credential-free adapter input. It must not be
// decoded from browser JSON as an admission claim. Native account/gateway facts
// bind their canonical account connection; Fountain session facts have no
// invented desktop connection. Runtime facts name their admitted runtime.
type ActorEvidence struct {
	Source      AdmissionSource             `json:"source"`
	Host        collaboration.HostID        `json:"host"`
	Repository  collaboration.RepositoryID  `json:"repository,string"`
	Actor       collaboration.ActorID       `json:"actor,string"`
	Connection  *collaboration.ConnectionID `json:"connection"`
	Runtime     *collaboration.RuntimeID    `json:"runtime"`
	Authority   string                      `json:"authority"`
	Revision    collaboration.Revision      `json:"revision"`
	Permissions []Permission                `json:"permissions"`
	ObservedAt  time.Time                   `json:"observed_at"`
	ExpiresAt   time.Time                   `json:"expires_at"`
}

// AdmittedActor cannot be constructed by JSON decoding. Its zero value denies.
// It carries read/human-collaboration/configuration authority only. There is no
// conversion from account admission to run, publication or spending authority.
type AdmittedActor struct{ evidence ActorEvidence }

// Admit is called ONLY by a trusted adapter after provider authentication,
// current permission and canonical host/repository/connection checks. It checks
// the evidence envelope, not provider truth. A saved envelope is not a login or
// permission cache: revocation still requires fresh authoritative admission.
func Admit(e ActorEvidence, now time.Time) (AdmittedActor, error) {
	if now.IsZero() || !collaboration.ValidID(string(e.Host)) || e.Repository <= 0 || e.Actor <= 0 ||
		!collaboration.ValidText(e.Authority, 256) || !collaboration.ValidText(string(e.Revision), 256) ||
		e.ObservedAt.IsZero() || e.ObservedAt.After(now) || !e.ExpiresAt.After(now) ||
		!e.ExpiresAt.After(e.ObservedAt) || len(e.Permissions) == 0 || len(e.Permissions) > 3 {
		return AdmittedActor{}, ErrDenied
	}
	switch e.Source {
	case FountainSession:
		if e.Connection != nil || e.Runtime != nil {
			return AdmittedActor{}, ErrDenied
		}
	case NativeAccount, FountainGateway:
		if e.Connection == nil || !collaboration.ValidID(string(*e.Connection)) || e.Runtime != nil {
			return AdmittedActor{}, ErrDenied
		}
	case RuntimeGrant:
		if e.Runtime == nil || !collaboration.ValidID(string(*e.Runtime)) || e.Connection != nil {
			return AdmittedActor{}, ErrDenied
		}
	default:
		return AdmittedActor{}, ErrDenied
	}
	seen := map[Permission]bool{}
	for _, p := range e.Permissions {
		if (p != Read && p != Collaborate && p != Configure) || seen[p] || (e.Source == RuntimeGrant && p == Configure) {
			return AdmittedActor{}, ErrDenied
		}
		seen[p] = true
	}
	return AdmittedActor{evidence: cloneEvidence(e)}, nil
}

// Require checks this already-admitted, exact resource scope and expiry. The
// application must also revalidate revocable authority at its mutation boundary.
func (a AdmittedActor) Require(host collaboration.HostID, repository collaboration.RepositoryID, permission Permission, now time.Time) error {
	e := a.evidence
	if now.IsZero() || host != e.Host || repository != e.Repository || !e.ExpiresAt.After(now) ||
		e.ObservedAt.After(now) || !slices.Contains(e.Permissions, permission) {
		return ErrDenied
	}
	return nil
}

func (a AdmittedActor) Evidence() ActorEvidence { return cloneEvidence(a.evidence) }

func cloneEvidence(e ActorEvidence) ActorEvidence {
	e.Permissions = slices.Clone(e.Permissions)
	if e.Connection != nil {
		v := *e.Connection
		e.Connection = &v
	}
	if e.Runtime != nil {
		v := *e.Runtime
		e.Runtime = &v
	}
	return e
}
