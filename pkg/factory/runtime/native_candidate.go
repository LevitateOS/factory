package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

var _ execution.CandidateExporter = NativeExecution{}

func (p NativeExecution) ExportCandidate(ctx context.Context, request execution.CandidateRequest) (execution.CandidateBundle, error) {
	var empty execution.CandidateBundle
	if p.Store == nil || request.Address.Validate() != nil || request.Role != project.RoleCoder || !project.ValidPreparationID(request.Preparation) || !project.ValidCommit(request.Candidate) {
		return empty, execution.ErrCandidateInvalid
	}
	run, err := p.Store.FactoryRun(ctx, request.Address.Run)
	if err != nil {
		return empty, err
	}
	if run.Native == nil || run.ProjectID != request.Address.Workspace || run.Role != request.Role || run.Native.Provenance.Profile != p.Config.Profile || run.Native.Intent == "" || !run.Native.Settled() {
		return empty, execution.ErrCandidateInvalid
	}
	preparation, err := p.Store.Preparation(ctx, request.Preparation)
	if err != nil {
		return empty, err
	}
	native := run.Native.Provenance
	if preparation.Native == nil || preparation.Native.Profile != native.Profile || preparation.Native.InstanceID != native.InstanceID || preparation.Native.WorktreeID != native.WorktreeID || preparation.Preparation.Project != run.ProjectID || preparation.Preparation.Role != run.Role || preparation.Preparation.SourceCommit != run.InputSHA || preparation.State.SourceBundleSHA256 != run.Native.BundleSHA256 {
		return empty, execution.ErrCandidateInvalid
	}
	var result struct {
		IntentDigest string `json:"intent_digest"`
		Status       string `json:"status"`
		Candidate    string `json:"candidate,omitempty"`
		SHA256       string `json:"sha256,omitempty"`
		Bundle       []byte `json:"bundle,omitempty"`
	}
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation string `json:"operation"`
		Intent    string `json:"intent"`
		Candidate string `json:"candidate"`
	}{"export", run.Native.Intent, request.Candidate}, &result)
	if err != nil {
		return empty, err
	}
	intentDigest := sha256.Sum256([]byte(run.Native.Intent))
	if result.IntentDigest != hex.EncodeToString(intentDigest[:]) {
		return empty, execution.ErrCandidateInvalid
	}
	if result.Status == "candidate_mismatch" {
		return empty, execution.ErrCandidateInvalid
	}
	if len(result.Bundle) > project.MaxSourceBundle {
		return empty, execution.ErrCandidateBounds
	}
	bundleDigest := sha256.Sum256(result.Bundle)
	if result.Status != "exported" || result.Candidate != request.Candidate || len(result.Bundle) == 0 || result.SHA256 != hex.EncodeToString(bundleDigest[:]) {
		return empty, execution.ErrCandidateInvalid
	}
	return execution.CandidateBundle{Address: request.Address, Candidate: request.Candidate, Bundle: result.Bundle}, nil
}
