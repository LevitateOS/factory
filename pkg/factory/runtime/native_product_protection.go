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
type nativeRequiredChecks struct {
	Protection                       *collaboration.BranchProtection
	EvaluationCommit                 collaboration.CommitID
	StatusGate                       string
	MissingRequired, InvalidRequired []string
}

func (s nativeCollaborationReads) readRequiredChecks(ctx context.Context, repo collaboration.RepositoryID, pull nativeSubjectRef, head, base collaboration.CommitID, branch string) (nativeRequiredChecks, error) {
	var empty nativeRequiredChecks
	var envelope struct {
		Protection struct {
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
		} `json:"protection"`
		EvaluationCommit collaboration.CommitID `json:"evaluation_commit"`
		StatusGate       string                 `json:"status_gate"`
		MissingRequired  []string               `json:"missing_required"`
		InvalidRequired  []string               `json:"invalid_required"`
	}
	pin, basePin := string(head), string(base)
	if err := s.read(ctx, nativeRead{Kind: "pull_required_checks", Repository: s.identity.SelectedRepository, Number: uint32(pull.Number), Head: &pin, Base: &basePin}, &envelope); err != nil {
		var failure *NativePortError
		if errors.As(err, &failure) {
			if failure.Status == 409 {
				return empty, factoryservice.ErrChecksChanged
			}
			if failure.Status == 413 {
				return empty, factoryservice.ErrChecksTooLarge
			}
		}
		return empty, nativeMissing(err, factoryservice.ErrChecksMissing)
	}
	raw := envelope.Protection
	if raw.Base != base || raw.Branch != branch {
		return empty, factoryservice.ErrChecksChanged
	}
	b := raw.Summary
	if raw.Repository != s.identity.SelectedRepository || raw.Pull != pull || raw.Head != head || !collaboration.ValidCommitID(raw.Base) || !collaboration.ValidCommitID(raw.BranchCommit) || !collaboration.ValidText(raw.Branch, 4096) || b.Repository != 0 || b.Branch != "" || b.Observation != (collaboration.Observation{}) || b.RequirementsComplete == nil || b.ClassicVisible == nil || b.RulesVisible == nil || (raw.Protected == nil) != (b.Protected == nil) || raw.Protected != nil && *raw.Protected != *b.Protected {
		return empty, errNativeRead
	}
	if _, err := s.mapRead(ctx, raw.Repository, raw.Name, nil, nil); err != nil {
		return empty, err
	}
	b.Repository, b.Branch, b.Observation = repo, raw.Branch, s.observation(raw)
	if !collaboration.ValidCommitID(envelope.EvaluationCommit) {
		return empty, errNativeRead
	}
	if envelope.MissingRequired == nil || envelope.InvalidRequired == nil {
		return empty, errNativeRead
	}
	return nativeRequiredChecks{EvaluationCommit: envelope.EvaluationCommit, Protection: &b, StatusGate: envelope.StatusGate, MissingRequired: envelope.MissingRequired, InvalidRequired: envelope.InvalidRequired}, nil
}
