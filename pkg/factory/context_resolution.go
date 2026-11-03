package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const ActionContextCurrent = "use_current_context"

// ContextCurrentResolution is an explicit owner's choice of the complete
// required-current projection. It is separate from the original model receipt.
type ContextCurrentResolution struct {
	Principal      string `json:"principal"`
	RunID          string `json:"run_id"`
	AssignmentID   string `json:"assignment_id"`
	ScopeDigest    string `json:"scope_digest"`
	Fingerprint    string `json:"fingerprint"`
	RequiredDigest string `json:"required_digest"`
	Bundle         string `json:"bundle"`
	RecordedUnix   int64  `json:"recorded_unix"`
}

type ContextCurrentRequest struct {
	Fingerprint    string `json:"fingerprint"`
	RequiredDigest string `json:"required_digest"`
	ScopeDigest    string `json:"scope_digest"`
}

func (r ContextCurrentResolution) ValidateFor(cap MCPCapability) error {
	if r.RunID != cap.RunID || r.AssignmentID != cap.AssignmentID || r.ScopeDigest != cap.ScopeDigest() || r.Fingerprint != cap.ContextSelectionSnapshot || !ValidDigest(r.Fingerprint) || !ValidDigest(r.RequiredDigest) || r.Bundle != ContextBundleCurrent || r.RecordedUnix <= 0 {
		return errors.New("owner context resolution differs from its capability")
	}
	return nil
}

// CurrentRequiredSemanticDigest binds admitted requirements while allowing
// receipts and verified progress to refresh under the saved current template.
func CurrentRequiredSemanticDigest(role CurrentRoleContext) (string, error) {
	role.Tools, role.ResultSchema = nil, nil
	role.ResultTool, role.RecordSource = "", ""
	if role.Plan != nil {
		plan := *role.Plan
		plan.Receipts, plan.VerifiedIntegrationCommit, plan.FinalCheckpoint = nil, nil, nil
		role.Plan = &plan
	}
	if role.Review != nil {
		review := *role.Review
		// CI can complete after reviewer admission. Its observed progress
		// refreshes without changing the admitted candidate or requirements.
		review.CheckAssessment = nil
		role.Review = &review
	}
	if role.Verification != nil {
		verification := *role.Verification
		verification.Execution, verification.ExecutionReceipt = nil, nil
		role.Verification = &verification
	}
	raw, err := json.Marshal(role)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
