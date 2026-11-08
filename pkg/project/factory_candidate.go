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
		if p.SourceMerge != nil {
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
		if m.Validate() != nil || p.Preparation.Role != RoleCoder || m.Export.Project != p.Preparation.Project || m.Commit != p.Preparation.SourceCommit {
			return errors.New("merged source does not bind the coder preparation")
		}
	}
	if p.Preparation.Role == RoleReviewer && (len(p.Bundle) == 0 || len(p.Bundle) > MaxSourceBundle) {
		return errors.New("candidate source bundle exceeds preparation bounds")
	}
	if p.Deadline.IsZero() {
		return errors.New("candidate preparation requires an absolute deadline")
	}
	return nil
}

// FactoryMergedSource binds a provider-created result to its recorded merge and
// original reviewed run. The run export never stands in for the merged bytes.
type FactoryMergedSource struct {
	Export     FactoryExport `json:"export"`
	MergeID    string        `json:"merge_id"`
	Commit     string        `json:"commit"`
	Repository int64         `json:"repository,string"`
	Target     string        `json:"target"`
}

func (s FactoryMergedSource) Validate() error {
	if s.Export.Validate() != nil || s.Export.Role != RoleCoder || !ValidFactoryRunID(s.MergeID) || !ValidCommit(s.Commit) || s.Repository <= 0 || len(s.Target) <= len("refs/heads/") || len(s.Target) > 256 || s.Target[:len("refs/heads/")] != "refs/heads/" {
		return errors.New("invalid merged preparation source")
	}
	return nil
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
