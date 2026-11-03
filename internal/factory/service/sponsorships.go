package service

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

type SponsorshipChange struct {
	ExpectedRevision int64    `json:"expected_revision"`
	Roles            []string `json:"roles"`
	AllowanceMinutes int      `json:"allowance_minutes"`
	Active           bool     `json:"active"`
}

// SponsorshipCustody contains nonsecret broker facts for one currently owned
// connection and this repository's runtime. Grant.UserID is the broker's execution
// actor, not the canonical sponsor. Project retains the existing opaque runtime
// reference; the host alone resolves its native binding and connection ownership.
type SponsorshipCustody struct {
	Repository collaboration.RepositoryID
	Connection string
	Project    string
	Generation int64
	Grants     []identity.Grant
}

type SponsorshipWriter interface {
	ApplySponsorship(context.Context, string, factory.Sponsorship) (factory.GrantResult, error)
}

type Sponsorships struct {
	Store      *store.Store
	Writer     SponsorshipWriter
	Visibility WorkVisibility
	Custody    func(context.Context, collaboration.ActorID, collaboration.RepositoryID, string) (SponsorshipCustody, error)
}

var (
	ErrSponsorshipInvalid           = errors.New("sponsorship is invalid")
	ErrSponsorshipUnavailable       = errors.New("sponsorship admission is unavailable")
	ErrSponsorshipAccess            = errors.New("current sponsorship visibility is required")
	ErrSponsorshipBrokerUnavailable = errors.New("sponsorship custody is unavailable")
	ErrSponsorshipConnectionMissing = errors.New("sponsorship connection was not found")
	ErrSponsorshipOwnerRequired     = errors.New("current connection ownership is required")
	ErrSponsorshipGrantUnavailable  = errors.New("sponsorship requires a live runtime grant")
)

func (s Sponsorships) Apply(ctx context.Context, actor collaboration.ActorID, principal string, repository collaboration.RepositoryID, connection string, in SponsorshipChange) (factory.GrantResult, error) {
	var empty factory.GrantResult
	if actor < 1 || repository < 1 || principal == "" || connection == "" || len(connection) > 128 || in.ExpectedRevision < 0 {
		return empty, ErrSponsorshipInvalid
	}
	if s.Store == nil || s.Writer == nil || s.Visibility == nil || s.Custody == nil {
		return empty, ErrSponsorshipUnavailable
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Visibility(ctx, actor, repository); err != nil {
			return errors.Join(ErrSponsorshipAccess, err)
		}
		return ctx.Err()
	}
	if err := authorize(); err != nil {
		return empty, err
	}
	previous := ""
	stored, err := s.Store.Sponsorship(ctx, int64(repository), connection)
	if err == nil {
		if stored.Repository != int64(repository) || stored.Connection != connection {
			return empty, ErrSponsorshipUnavailable
		}
		previous = stored.GrantID
	} else if !errors.Is(err, store.ErrNotFound) {
		// Unknown continuity cannot be treated as absence and select a new grant.
		return empty, err
	}
	custody, err := s.Custody(ctx, actor, repository, connection)
	if err != nil {
		return empty, err
	}
	if custody.Repository != repository || custody.Connection != connection || custody.Generation < 1 || !project.ValidID(custody.Project) {
		return empty, ErrSponsorshipBrokerUnavailable
	}
	var selected identity.Grant
	for _, grant := range custody.Grants {
		if grant.ConnectionID != connection || grant.ProjectID != custody.Project || grant.Revoked || grant.UserID < 1 || grant.Revision < 0 || grant.ID == "" || len(grant.ID) > 128 {
			continue
		}
		if grant.ID == previous {
			selected = grant
			break
		}
		if selected.ID == "" || grant.Revision > selected.Revision {
			selected = grant
		}
	}
	if selected.ID == "" {
		return empty, ErrSponsorshipGrantUnavailable
	}
	sponsorship := factory.Sponsorship{
		Repository: int64(repository), Revision: in.ExpectedRevision, GrantedBy: int64(actor),
		Connection: connection, Roles: in.Roles, AllowanceMinutes: in.AllowanceMinutes, Active: in.Active,
		GrantID: selected.ID, Generation: custody.Generation, ActorID: selected.UserID, ProjectID: selected.ProjectID,
	}
	if err := sponsorship.Validate(); err != nil {
		return empty, errors.Join(ErrSponsorshipInvalid, err)
	}
	// Broker reads can outlive visibility or caller cancellation. Neither a
	// recorded sponsorship nor its previous grant bypasses current admission.
	if err := authorize(); err != nil {
		return empty, err
	}
	return s.Writer.ApplySponsorship(ctx, principal, sponsorship)
}
