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
// refreshed source. Its caller supplies no bundle. Corrections retain their
// original checkout; the native producer verifies unchanged approved setup.
type FactoryCandidate struct {
	Preparation       Preparation    `json:"preparation"`
	SourcePreparation string         `json:"source_preparation"`
	SourceRun         *FactoryExport `json:"source_run,omitempty"`
	Bundle            []byte         `json:"bundle"`
	Deadline          time.Time      `json:"deadline"`
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
		if err := p.SourceRun.Validate(); err != nil {
			return err
		}
		if p.Preparation.Role != RoleCoder || p.SourceRun.Role != RoleCoder || p.SourceRun.Project != p.Preparation.Project || p.SourceRun.Candidate != p.Preparation.SourceCommit {
			return errors.New("candidate source run does not bind the coder preparation")
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
