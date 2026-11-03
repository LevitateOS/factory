package access

import (
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
)

type (
	AutomationModel string
	Trigger         string
)

const (
	OriginalCreationEvidence AutomationModel = "original_creation_evidence"
	CurrentStateAdoption     AutomationModel = "current_state_adoption"
	IssueChanged             Trigger         = "issue_changed"
	PullChanged              Trigger         = "pull_changed"
	ReviewChanged            Trigger         = "review_changed"
	ChecksChanged            Trigger         = "checks_changed"
)

// PolicyProposal states the terms of a separate explicit automation enrollment.
// It has NO active field, activation method or admission conversion. Connecting
// an account, selecting a runtime or decoding a proposal enables no automation.
// D1 must qualify the current-state authority model before GitHub intake can
// consume it. Existing Fountain original-creation proof must not be relabeled
// as current-state adoption (nor reconstructed from polling/webhook signatures).
type PolicyProposal struct {
	Policy              GrantRef                   `json:"policy"`
	Host                collaboration.HostID       `json:"host"`
	Repository          collaboration.RepositoryID `json:"repository,string"`
	Runtime             collaboration.RuntimeID    `json:"runtime"`
	PlacementGeneration int64                      `json:"placement_generation,string"`
	ProposedBy          collaboration.ActorID      `json:"proposed_by,string"`
	Model               AutomationModel            `json:"model"`
	EligibleAuthors     []PolicyAuthor             `json:"eligible_authors"`
	Triggers            []Trigger                  `json:"triggers"`
	AllowForks          bool                       `json:"allow_forks"`
	MaxActiveRuns       int                        `json:"max_active_runs"`
	MaxMinutes          int                        `json:"max_minutes"`
	NotAfter            time.Time                  `json:"not_after"`
}

// Author IDs use a string wire form, as do all canonical numeric identities.
// A wrapper avoids Go's unsupported ,string option on a slice of integers.
type PolicyAuthor struct {
	Actor collaboration.ActorID `json:"actor,string"`
}

func (p PolicyProposal) Validate() error {
	if !p.Policy.Valid() || !collaboration.ValidID(string(p.Host)) || p.Repository <= 0 ||
		!collaboration.ValidID(string(p.Runtime)) || p.PlacementGeneration <= 0 || p.ProposedBy <= 0 ||
		(p.Model != OriginalCreationEvidence && p.Model != CurrentStateAdoption) ||
		len(p.EligibleAuthors) == 0 || len(p.EligibleAuthors) > 256 || len(p.Triggers) == 0 || len(p.Triggers) > 4 ||
		p.MaxActiveRuns < 1 || p.MaxActiveRuns > 64 || p.MaxMinutes < 1 || p.MaxMinutes > 10080 || p.NotAfter.IsZero() {
		return ErrDenied
	}
	authors := map[collaboration.ActorID]bool{}
	for _, a := range p.EligibleAuthors {
		if a.Actor <= 0 || authors[a.Actor] {
			return ErrDenied
		}
		authors[a.Actor] = true
	}
	triggers := map[Trigger]bool{}
	for _, t := range p.Triggers {
		if (t != IssueChanged && t != PullChanged && t != ReviewChanged && t != ChecksChanged) || triggers[t] {
			return ErrDenied
		}
		triggers[t] = true
	}
	return nil
}

// IntakeAdoption binds one accepted content observation to an explicitly
// activated policy's authority. This is retained evidence, not an intake API.
// SourceAuthor can be unknown; AdoptedBy is the current authorized adopter and
// never an invented assertion of the source author's historical permission.
// Exact head/base are mandatory for a pull; an issue has neither commit field.
type IntakeAdoption struct {
	Policy              GrantRef                    `json:"policy"`
	Model               AutomationModel             `json:"model"`
	Trigger             Trigger                     `json:"trigger"`
	Host                collaboration.HostID        `json:"host"`
	Runtime             collaboration.RuntimeID     `json:"runtime"`
	PlacementGeneration int64                       `json:"placement_generation,string"`
	Issue               *collaboration.IssueRef     `json:"issue"`
	Pull                *collaboration.PullRef      `json:"pull"`
	ContentRevision     collaboration.Revision      `json:"content_revision"`
	Head                *collaboration.CommitID     `json:"head"`
	Base                *collaboration.CommitID     `json:"base"`
	HeadRepository      *collaboration.RepositoryID `json:"head_repository,string"`
	SourceAuthor        *collaboration.ActorID      `json:"source_author,string"`
	AdoptedBy           collaboration.ActorID       `json:"adopted_by,string"`
	Authority           GrantRef                    `json:"authority"`
	InputDigest         string                      `json:"input_digest"`
	ObservedAt          time.Time                   `json:"observed_at"`
	OriginalCreation    *OriginalCreationRef        `json:"original_creation"`
}

// This reference must resolve to the current engine's full authoritative
// creation/provenance proof. A body marker or ordinary author read is not proof.
type OriginalCreationRef struct {
	Authority string `json:"authority"`
	Reference string `json:"reference"`
}

// ValidateAgainst checks retained scope/content/eligibility, not policy
// activation, provider truth, original proof authenticity or spending permission.
func (a IntakeAdoption) ValidateAgainst(p PolicyProposal) error {
	if p.Validate() != nil || a.Policy != p.Policy || a.Model != p.Model || a.Host != p.Host || a.Runtime != p.Runtime ||
		a.PlacementGeneration != p.PlacementGeneration || a.AdoptedBy <= 0 || !a.Authority.Valid() ||
		!collaboration.ValidText(string(a.ContentRevision), 256) || a.ObservedAt.IsZero() || !a.ObservedAt.Before(p.NotAfter) ||
		!validDigest(a.InputDigest) || a.SourceAuthor == nil || !slices.Contains(p.EligibleAuthors, PolicyAuthor{Actor: *a.SourceAuthor}) ||
		!slices.Contains(p.Triggers, a.Trigger) {
		return ErrDenied
	}
	if a.Model == OriginalCreationEvidence {
		if a.OriginalCreation == nil || !collaboration.ValidText(a.OriginalCreation.Authority, 256) ||
			!collaboration.ValidText(a.OriginalCreation.Reference, 256) {
			return ErrDenied
		}
	} else if a.OriginalCreation != nil {
		return ErrDenied
	}
	switch {
	case a.Issue != nil && a.Pull == nil:
		i := a.Issue
		if i.Kind != collaboration.SubjectIssue || i.Repository != p.Repository || i.Number <= 0 ||
			!collaboration.ValidText(i.ExternalID, 1024) || a.Head != nil || a.Base != nil || a.HeadRepository != nil || a.Trigger != IssueChanged {
			return ErrDenied
		}
	case a.Pull != nil && a.Issue == nil:
		i := a.Pull
		if i.Kind != collaboration.SubjectPull || i.Repository != p.Repository || i.Number <= 0 ||
			!collaboration.ValidText(i.ExternalID, 1024) || a.Head == nil || a.Base == nil ||
			!validCommit(*a.Head) || !validCommit(*a.Base) || a.Trigger == IssueChanged ||
			a.HeadRepository == nil || *a.HeadRepository <= 0 || (!p.AllowForks && *a.HeadRepository != p.Repository) {
			return ErrDenied
		}
	default:
		return ErrDenied
	}
	return nil
}

func validCommit(c collaboration.CommitID) bool {
	return (len(c) == 40 || len(c) == 64) && hexText(string(c))
}

func validDigest(s string) bool { return len(s) == 64 && hexText(s) }

func hexText(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
