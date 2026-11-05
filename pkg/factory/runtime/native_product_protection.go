package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// Rust defines the provider policy decoding. Go binds that observation to the
// canonical repository and pull; it does not reinterpret provider parameters.
func (s nativeCollaborationReads) readProtection(ctx context.Context, repo collaboration.RepositoryID, pull nativeSubjectRef, head collaboration.CommitID) (*collaboration.BranchProtection, error) {
	var raw struct {
		Repository   string                         `json:"repository"`
		Name         string                         `json:"name"`
		Pull         nativeSubjectRef               `json:"pull"`
		Head         collaboration.CommitID         `json:"head"`
		Base         collaboration.CommitID         `json:"base"`
		Branch       string                         `json:"branch"`
		BranchCommit collaboration.CommitID         `json:"branch_commit"`
		Protected    *bool                          `json:"protected"`
		Classic      json.RawMessage                `json:"classic"`
		Rules        json.RawMessage                `json:"rules"`
		Summary      collaboration.BranchProtection `json:"summary"`
	}
	pin := string(head)
	if err := s.read(ctx, nativeRead{Kind: "pull_protection", Repository: s.identity.SelectedRepository, Number: uint32(pull.Number), Head: &pin}, &raw); err != nil {
		var failure *NativePortError
		if errors.As(err, &failure) {
			if failure.Status == 409 {
				return nil, factoryservice.ErrChecksChanged
			}
			if failure.Status == 413 {
				return nil, factoryservice.ErrChecksTooLarge
			}
		}
		return nil, nativeMissing(err, factoryservice.ErrChecksMissing)
	}
	b := raw.Summary
	if raw.Repository != s.identity.SelectedRepository || raw.Pull != pull || raw.Head != head || !collaboration.ValidCommitID(raw.Base) || !collaboration.ValidCommitID(raw.BranchCommit) || !collaboration.ValidText(raw.Branch, 4096) || b.Repository != 0 || b.Branch != "" || b.Observation != (collaboration.Observation{}) || b.RequirementsComplete == nil || b.ClassicVisible == nil || b.RulesVisible == nil || (raw.Protected == nil) != (b.Protected == nil) || raw.Protected != nil && *raw.Protected != *b.Protected {
		return nil, errNativeRead
	}
	if _, err := s.mapRead(ctx, raw.Repository, raw.Name, nil, nil); err != nil {
		return nil, err
	}
	b.Repository, b.Branch, b.Observation = repo, raw.Branch, s.observation(raw)
	return &b, nil
}
