package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

func admitNativeDispatchTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, run factory.Run, grants admissionGrants, native execution.NativeLaunch) (factory.Run, error) {
	refuse := func() (factory.Run, error) { return factory.Run{}, ErrAdmissionChanged }
	env := grants.environment
	account := grants.sponsorship.Native
	if !native.Active() || !collaboration.ValidID(a.Connection) || env.Native == nil || env.Project != a.ProjectID ||
		account == nil || account.Validate() != nil || account.Profile != env.Native.Profile || !native.MatchesAuthentication(account.Authentication, account.CredentialSHA256) ||
		native.Runtime != env.Native.Runtime || native.PlacementGeneration != env.Native.Generation || native.Workspace.Profile != env.Native.Profile ||
		run.Native == nil || !native.MatchesRecord(*run.Native) || run.Native.Retirement != execution.NotStarted || run.Native.Sealed ||
		run.Native.Provenance.Process != nil || run.Native.Provenance.Custody.State != execution.NotAcquired || run.Native.Intent != "" ||
		a.NativeTool == nil || *a.NativeTool != native.Tool || a.AdoptedHarness != (project.FactoryHarnessPin{}) || native.PromptSHA256 != a.PromptSHA ||
		run.Image != "" || run.Harness != a.Harness || run.Model != a.Model {
		return refuse()
	}
	if err := requireNativeDecisionTx(ctx, tx, a.ProjectID, env.Native); err != nil {
		return factory.Run{}, err
	}
	if _, err := lockAttemptAllowanceRootTx(ctx, tx, a.Repository, a.Issue, a.AttemptRoot); err != nil && (a.AttemptRoot != a.ID || !errors.Is(err, sql.ErrNoRows)) {
		return factory.Run{}, err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR SHARE`, a.Preparation).Scan(&raw); err != nil {
		return factory.Run{}, err
	}
	prep, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil {
		return factory.Run{}, err
	}
	p := prep.Preparation
	if prep.Native == nil || *prep.Native != native.Workspace || p.Project != a.ProjectID || p.Role != a.Role ||
		p.Requirements.ID != a.Authority.RequirementsID || p.Approval.ID != a.Authority.ApprovalID ||
		p.SourceCommit != a.SourceCommit || !prep.State.Ready || prep.State.Stopped || prep.State.SourceBundleSHA256 != native.BundleSHA256 ||
		meta != nil && (meta.Stopped || meta.Retirement == "confirmed") {
		return refuse()
	}
	if a.Phase == project.ExecutionCorrect && (meta == nil || meta.PublicationID == "" ||
		meta.OwnerAssignment != a.PublicationAssignment || meta.ChildAssignment != a.ID || meta.AttemptRoot != a.AttemptRoot ||
		meta.SourceRun == nil || meta.SourceRun.Candidate != a.SourceCommit) {
		return refuse()
	}
	// Use store-observed grants for the admission, then capture the exact intent
	// in the same transaction as the run, assignment and held reservation.
	n := *run.Native
	selection := grants.policy.Roles[a.Role]
	if selection.Profile.Validate(selection.Harness) != nil {
		return refuse()
	}
	version := 3
	if native.TrustMode == execution.NativeCLIProvider {
		version = 4
	}
	if project.ExecutionManifestRequired(a.Phase) {
		version = 5
	}
	intent := execution.NativeIntent{Kind: "factory_native_launch_intent", Version: version, Run: run.ID, Workspace: a.ProjectID,
		Role: a.Role, SourceCommit: a.SourceCommit, Connection: a.Connection, Deadline: run.Deadline, Native: native,
		Model: a.Model, Effort: selection.Profile.Effort, Contributor: selection.Profile.Contributor}
	if version == 5 {
		intent.ExecutionPhase = a.Phase
		intent.ManifestDigest = project.ExecutionManifestDigest(run.ExecutionManifest)
	}
	n.Intent, err = intent.Encode()
	if err != nil {
		return factory.Run{}, err
	}
	run.Native = &n
	run.Admission = &factory.RunAdmission{Authority: a.Authority, Policy: grants.policy, Native: &native}
	if run.Admission.Policy.Execution != nil {
		captured, err := factory.ControllerAdmissionDigest(a, run)
		if err != nil {
			return refuse()
		}
		run.Admission.ControllerManifestDigest = captured
	}
	if run.RuntimeKind() != collaboration.Native || run.Validate() != nil {
		return refuse()
	}
	return run, nil
}
