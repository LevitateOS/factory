package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/project"
)

// LANE87 head-context foundation: the capture projects the admitted
// current requirement+approval heads through actual public admission
// paths (AdmitRequirementDecision/AdmitApprovalDecision). Proven on
// the disposable SODA_PG_* fixture; skips without it.
func headTestRequirementID() string { return "d" + strings.Repeat("1", 24) }

func headTestApprovalID() string { return "d" + strings.Repeat("2", 24) }

func headTestAdmitSuccessor(t *testing.T, ctx context.Context, db *Store, verified bool) (string, string) {
	t.Helper()
	projectID := dispatchTestProjectID()
	nextRequirement := "d" + strings.Repeat("3", 24)
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{
		ID: nextRequirement, Predecessor: dispatchTestRequirementID(), Project: projectID, Approver: 7,
		SourceCommit: strings.Repeat("2", 40), SetupDigest: strings.Repeat("c", 64), InputsDigest: strings.Repeat("d", 64),
	}); err != nil {
		t.Fatalf("successor requirement: %v", err)
	}
	nextApproval := "d" + strings.Repeat("4", 24)
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{
		ID: nextApproval, Predecessor: dispatchTestApprovalID(), Project: projectID, Requirement: nextRequirement,
		Approver: 7, EffectsDigest: strings.Repeat("e", 64), ReadinessDigest: strings.Repeat("f", 64), Verified: verified,
	}); err != nil {
		t.Fatalf("successor approval: %v", err)
	}
	return nextRequirement, nextApproval
}

// Admitted heads project verbatim: IDs into the existing authority
// refs, digests/verified/target into the new fields, and the local
// source commit reuses the projected requirement source.
func TestCaptureCompletionCurrentHeadProjectsHeads(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	local, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("captured local invalid: %v", err)
	}
	authority := local.Material.Authority
	if authority.Effective.Authority.RequirementsID != dispatchTestRequirementID() ||
		authority.Effective.Authority.ApprovalID != dispatchTestApprovalID() {
		t.Fatalf("heads=%+v, want the admitted requirement+approval heads",
			authority.Effective.Authority)
	}
	if authority.ProjectID != dispatchTestProjectID() {
		t.Fatalf("project=%q, want the repo project", authority.ProjectID)
	}
	if authority.RequirementSourceCommit != strings.Repeat("1", 40) ||
		local.SourceCommit != authority.RequirementSourceCommit {
		t.Fatalf("source=%q local=%q, want the reused requirement source commit",
			authority.RequirementSourceCommit, local.SourceCommit)
	}
	if authority.RequirementSetupDigest != strings.Repeat("a", 64) ||
		authority.RequirementInputsDigest != strings.Repeat("b", 64) ||
		authority.ApprovalEffectsDigest != strings.Repeat("a", 64) ||
		authority.ApprovalReadinessDigest != strings.Repeat("b", 64) {
		t.Fatal("head digests differ from the admitted decisions")
	}
	if !authority.ApprovalVerified {
		t.Fatal("admitted verified approval reads unverified")
	}
	if authority.TargetBranch != "refs/heads/main" {
		t.Fatalf("target=%q, want the SAME-RR policy target", authority.TargetBranch)
	}
}

// A valid head successor moves the material digest and Equal: same
// shape, new heads, visibly different capture.
func TestCaptureCompletionHeadSuccessorMovesDigest(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	first, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	nextRequirement, nextApproval := headTestAdmitSuccessor(t, ctx, db, false)
	second, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if err := second.Validate(); err != nil {
		t.Fatalf("successor local invalid: %v", err)
	}
	authority := second.Material.Authority
	if authority.Effective.Authority.RequirementsID != nextRequirement ||
		authority.Effective.Authority.ApprovalID != nextApproval {
		t.Fatalf("heads=%+v, want the successor pair", authority.Effective.Authority)
	}
	if authority.ApprovalVerified {
		t.Fatal("false verified relabeled after the successor")
	}
	if second.ScopeDigest == first.ScopeDigest || second.Equal(first) {
		t.Fatal("head successor left the capture unchanged")
	}
	if second.SourceCommit != strings.Repeat("2", 40) {
		t.Fatalf("source=%q, want the successor requirement source", second.SourceCommit)
	}
}

// An old approval bound to a superseded requirement holds the
// capture; admitting the matching approval successor releases it.
func TestCaptureCompletionHeadMismatchedApprovalHolds(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	projectID := dispatchTestProjectID()
	nextRequirement := headTestRequirementID()
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{
		ID: nextRequirement, Predecessor: dispatchTestRequirementID(), Project: projectID, Approver: 7,
		SourceCommit: strings.Repeat("2", 40), SetupDigest: strings.Repeat("c", 64), InputsDigest: strings.Repeat("d", 64),
	}); err != nil {
		t.Fatalf("successor requirement: %v", err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
		t.Fatal("mismatched old approval captured instead of holding")
	} else if !strings.Contains(err.Error(), "another requirement") {
		t.Fatalf("err=%v, want the approval-requirement binding refusal", err)
	}
	nextApproval := headTestApprovalID()
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{
		ID: nextApproval, Predecessor: dispatchTestApprovalID(), Project: projectID, Requirement: nextRequirement,
		Approver: 7, EffectsDigest: strings.Repeat("e", 64), ReadinessDigest: strings.Repeat("f", 64),
	}); err != nil {
		t.Fatalf("matching approval: %v", err)
	}
	local, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("released local invalid: %v", err)
	}
	authority := local.Material.Authority
	if authority.Effective.Authority.RequirementsID != nextRequirement ||
		authority.Effective.Authority.ApprovalID != nextApproval {
		t.Fatalf("heads=%+v, want the matched successor pair", authority.Effective.Authority)
	}
}

// A changed policy target binds into the capture from the SAME-RR
// policy row.
func TestCaptureCompletionHeadPolicyTargetBinds(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	first, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	policy, err := db.RepositoryPolicy(ctx, 7)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	policy.TargetBranch = "refs/heads/release"
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatalf("policy target: %v", err)
	}
	second, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if err := second.Validate(); err != nil {
		t.Fatalf("retargeted local invalid: %v", err)
	}
	if second.Material.Authority.TargetBranch != "refs/heads/release" {
		t.Fatalf("target=%q, want the changed policy target",
			second.Material.Authority.TargetBranch)
	}
	if second.ScopeDigest == first.ScopeDigest || second.Equal(first) {
		t.Fatal("policy target change left the capture unchanged")
	}
	if second.Material.Authority.ProjectID != first.Material.Authority.ProjectID ||
		second.Material.Authority.RequirementSourceCommit != first.Material.Authority.RequirementSourceCommit {
		t.Fatal("policy target change moved the head digests")
	}
}

// Missing head rows hold the capture with a named error, never an
// empty authority fallback.
func TestCaptureCompletionHeadMissingHolds(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	projectID := dispatchTestProjectID()
	if _, err := db.db.ExecContext(ctx, `DELETE FROM project_approval_heads WHERE project_id=$1`, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
		t.Fatal("missing approval head captured instead of holding")
	} else if !strings.Contains(err.Error(), "approval head") {
		t.Fatalf("err=%v, want the approval-head hold", err)
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM project_requirement_heads WHERE project_id=$1`, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
		t.Fatal("missing requirement head captured instead of holding")
	} else if !strings.Contains(err.Error(), "requirement head") {
		t.Fatalf("err=%v, want the requirement-head hold", err)
	}
}

// No SourceInputs map and no raw setup/config/secret bodies enter the
// projected encoding.
func TestCaptureCompletionHeadSealsNoRawInputs(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	local, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	raw, err := json.Marshal(local.Material)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "source_inputs") {
		t.Fatal("projected material leaks a SourceInputs map")
	}
}
