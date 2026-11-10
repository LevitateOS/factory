package project

import (
	"errors"
	"time"
)

// FactoryCandidate prepares a fresh reviewer checkout of one exact candidate,
// reusing only a ready preparation's protected approved setup. The coordinator
// binds the current requirement/admin decisions before requesting preparation.
// Neither role Git configuration nor its private home crosses this boundary.
// A coder copies protected source or addresses one settled coder export for a
// refreshed source. Its caller supplies no bundle. Native corrections address
// the published run's export and retain the protected approved setup.
type FactoryCandidate struct {
	Preparation       Preparation          `json:"preparation"`
	SourcePreparation string               `json:"source_preparation"`
	SourceRun         *FactoryExport       `json:"source_run,omitempty"`
	SourceMerge       *FactoryMergedSource `json:"source_merge,omitempty"`
	SourcePull        *FactoryPullSource `json:"source_pull,omitempty"`
	Bundle            []byte               `json:"bundle"`
	Deadline          time.Time            `json:"deadline"`
}

func (p FactoryCandidate) Validate() error {
	if err := p.Preparation.Validate(); err != nil {
		return err
	}
	if !ValidPreparationID(p.SourcePreparation) || p.SourcePreparation == p.Preparation.ID {
		return errors.New("candidate preparation requires a fresh identity")
	}
	if p.Preparation.Role == RoleCoder && len(p.Bundle) != 0 {
		return errors.New("coder retry source must come from the protected preparation")
	}
	if p.SourceRun != nil {
		if p.SourceMerge != nil || p.SourcePull != nil {
			return errors.New("candidate preparation has conflicting sources")
		}
		if err := p.SourceRun.Validate(); err != nil {
			return err
		}
		if p.Preparation.Role != RoleCoder || p.SourceRun.Role != RoleCoder || p.SourceRun.Project != p.Preparation.Project || p.SourceRun.Candidate != p.Preparation.SourceCommit {
			return errors.New("candidate source run does not bind the coder preparation")
		}
	}
	if p.SourceMerge != nil {
		m := p.SourceMerge
		if p.SourcePull != nil || m.Validate() != nil || p.Preparation.Role != RoleCoder || m.Workspace() != p.Preparation.Project || m.Commit != p.Preparation.SourceCommit {
			return errors.New("merged source does not bind the coder preparation")
		}
	}
	if p.SourcePull != nil && (p.SourcePull.Validate() != nil || p.SourcePull.Head != p.Preparation.SourceCommit || len(p.Bundle) != 0) {
		return errors.New("external PR source does not bind the preparation")
	}
	if p.Preparation.Role == RoleReviewer && p.SourcePull == nil && (len(p.Bundle) == 0 || len(p.Bundle) > MaxSourceBundle) {
		return errors.New("candidate source bundle exceeds preparation bounds")
	}
	if p.Deadline.IsZero() {
		return errors.New("candidate preparation requires an absolute deadline")
	}
	return nil
}

// FactoryPullSource addresses authenticated provider bytes, never a caller's
// archive or an invented worker export. The Store supplies this tuple from an
// admitted external publication. Native authority is checked before and after
// fetching the exact head; a moved PR cannot substitute its current checkout.
type FactoryPullSource struct {
	Publication string `json:"publication"`
	Repository int64 `json:"repository,string"`
	Number int64 `json:"number,string"`
	PullID int64 `json:"pull_id,string"`
	IssueID int64 `json:"issue_id,string"`
	AuthorID int64 `json:"author_id,string"`
	HeadRepository int64 `json:"head_repository,string"`
	HeadRef string `json:"head_ref"`
	BaseRef string `json:"base_ref"`
	Head string `json:"head"`
	Base string `json:"base"`
}

func (s FactoryPullSource) Validate() error {
	if !ValidFactoryRunID(s.Publication) || s.Repository < 1 || s.HeadRepository < 1 || s.Number < 1 || s.PullID < 1 || s.IssueID < 1 || s.AuthorID < 1 || !ValidCommit(s.Head) || !ValidCommit(s.Base) || s.Head == s.Base || len(s.HeadRef) < 12 || len(s.HeadRef) > 256 || s.HeadRef[:11] != "refs/heads/" || len(s.BaseRef) < 12 || len(s.BaseRef) > 256 || s.BaseRef[:11] != "refs/heads/" {
		return errors.New("invalid external PR preparation source")
	}
	return nil
}

// FactoryMergedSource binds a provider-created result to its recorded merge and
// original reviewed run. The run export never stands in for the merged bytes.
type FactoryMergedSource struct {
	// Pull replaces Export only when the reviewed candidate was external and
	// had no Factory Builder. The recorded merge remains the authority for
	// fetching its resulting commit; these fields never fabricate a run.
	Pull       *FactoryPullSource `json:"pull,omitempty"`
	Project    string `json:"project,omitempty"`
	Export     FactoryExport `json:"export"`
	MergeID    string        `json:"merge_id"`
	Commit     string        `json:"commit"`
	Repository int64         `json:"repository,string"`
	Target     string        `json:"target"`
}

func (s FactoryMergedSource) Validate() error {
	if s.Pull==nil && (s.Project!="" || s.Export.Validate()!=nil || s.Export.Role!=RoleCoder) ||
		s.Pull!=nil && (s.Pull.Validate()!=nil || s.Export!=(FactoryExport{}) || !ValidID(s.Project) || s.Pull.Repository!=s.Repository || s.Pull.BaseRef!=s.Target) ||
		!ValidFactoryRunID(s.MergeID) || !ValidCommit(s.Commit) || s.Repository <= 0 || len(s.Target) <= len("refs/heads/") || len(s.Target) > 256 || s.Target[:len("refs/heads/")] != "refs/heads/" {
		return errors.New("invalid merged preparation source")
	}
	return nil
}

func (s FactoryMergedSource) Workspace() string {
	if s.Pull!=nil { return s.Project }
	return s.Export.Project
}

// FactoryCandidateInspect addresses the checkout through a settled recorded
// run. Role, preparation and container incarnation come from the host receipt.
type FactoryCandidateInspect struct {
	Project string `json:"project"`
	ID      string `json:"id"`
}

func (p FactoryCandidateInspect) Validate() error {
	if !ValidID(p.Project) || !ValidFactoryRunID(p.ID) {
		return errors.New("invalid candidate inspection address")
	}
	return nil
}

// FactoryCandidateState identifies the observed checkout HEAD and whether
// tracked, staged or untracked work differs. Untracked build output may follow
// worktree .gitignore rules; tracked ignore-file changes still count as edits.
// The private .soda-home is excluded; role Git configuration and info/exclude
// cannot hide edits.
type FactoryCandidateState struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Container string `json:"container"`
	Candidate string `json:"candidate"`
	Dirty     bool   `json:"dirty"`
}
