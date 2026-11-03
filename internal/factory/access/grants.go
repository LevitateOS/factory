package access

import (
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
)

// ProviderConnectionID identifies AI-provider custody, not a collaboration
// account. Do not substitute a GitHub/Fountain login for a funding grant.
type ProviderConnectionID string

type GrantRef struct {
	ID       string                 `json:"id"`
	Revision collaboration.Revision `json:"revision"`
}

func (g GrantRef) Valid() bool {
	return collaboration.ValidText(g.ID, 256) && collaboration.ValidText(string(g.Revision), 256)
}

// SponsorshipRef points to existing standing sponsorship/broker grant records.
// It introduces no second grant ledger or credential custody. Sponsor is the
// funding decision-maker; neither the run principal nor publisher inherits it.
type SponsorshipRef struct {
	Grant                GrantRef              `json:"grant"`
	Sponsor              collaboration.ActorID `json:"sponsor,string"`
	Connection           ProviderConnectionID  `json:"connection"`
	CredentialGeneration int64                 `json:"credential_generation,string"`
}

// Principal is the application's scoped execution identity on one runtime.
// It is neither a provider account nor an assertion of OS-user isolation.
type Principal struct {
	Runtime collaboration.RuntimeID `json:"runtime"`
	Actor   collaboration.ActorID   `json:"actor,string"`
	ID      string                  `json:"id"`
}

// RunAuthority is an immutable dispatch snapshot assembled by trusted admission
// from current standing grants. Validate checks shape, NOT those grants' truth,
// balance, revocation, placement ownership or native-mode qualification.
// Repository/capacity/operator/sponsorship grants retain their existing owners.
type RunAuthority struct {
	Host                collaboration.HostID       `json:"host"`
	Repository          collaboration.RepositoryID `json:"repository,string"`
	PlacementGeneration int64                      `json:"placement_generation,string"`
	Principal           Principal                  `json:"principal"`
	InitiatingHuman     *collaboration.ActorID     `json:"initiating_human,string"`
	PublishingActor     *collaboration.ActorID     `json:"publishing_actor,string"`
	RepositoryPolicy    GrantRef                   `json:"repository_policy"`
	Capacity            GrantRef                   `json:"capacity"`
	Operator            GrantRef                   `json:"operator"`
	Sponsorship         SponsorshipRef             `json:"sponsorship"`
	Admission           GrantRef                   `json:"admission"`
	Role                string                     `json:"role"`
	IssuedAt            time.Time                  `json:"issued_at"`
	Deadline            time.Time                  `json:"deadline"`
}

func (a RunAuthority) Validate() error {
	if !collaboration.ValidID(string(a.Host)) || a.Repository <= 0 || a.PlacementGeneration <= 0 ||
		!collaboration.ValidID(string(a.Principal.Runtime)) || a.Principal.Actor <= 0 || !collaboration.ValidText(a.Principal.ID, 128) ||
		(a.InitiatingHuman != nil && *a.InitiatingHuman <= 0) || (a.PublishingActor != nil && *a.PublishingActor <= 0) ||
		!a.RepositoryPolicy.Valid() || !a.Capacity.Valid() || !a.Operator.Valid() || !a.Admission.Valid() ||
		!a.Sponsorship.Grant.Valid() || a.Sponsorship.Sponsor <= 0 || a.Sponsorship.CredentialGeneration <= 0 ||
		!collaboration.ValidText(string(a.Sponsorship.Connection), 128) ||
		(a.Role != "coder" && a.Role != "reviewer") || a.IssuedAt.IsZero() || !a.Deadline.After(a.IssuedAt) {
		return ErrDenied
	}
	return nil
}
