package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// Packet admission already owns the accepted-head lock. A manifest cannot be
// attached after dispatch or omitted to recover the old preparation-only path.
func checkVerificationManifestPacketTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, run factory.Run) error {
	if a.Subject.Delivery == nil {
		if len(run.VerificationManifest) > 0 {
			return errors.New("non-delivery run carries verification checks")
		}
		return nil
	}
	var g factory.IntentGeneration
	err := contextJSONRow(ctx, tx, &g, `SELECT data FROM factory_intent_generations WHERE revision=$1 FOR SHARE`, a.Subject.Delivery.AcceptedRevision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if g.VerificationManifest == nil {
		if len(run.VerificationManifest) > 0 {
			return errors.New("verification run invented an adopted manifest")
		}
		return nil
	}
	m, err := project.ParseVerificationManifest(run.VerificationManifest)
	if err != nil {
		return errors.New("adopted verification manifest must be retained in its dispatch packet")
	}
	d := a.Subject.Delivery
	adopted := g.VerificationManifest
	if a.Phase != project.ExecutionVerify || a.Role != project.RoleReviewer || m.Run != run.ID || m.Project != a.ProjectID || m.Preparation != a.Preparation || m.SourceCommit != a.SourceCommit || !m.Deadline.Equal(run.Deadline) || m.Intent != d.IntentID || m.AcceptedRevision != d.AcceptedRevision || m.ScopeDigest != d.ScopeDigest || m.AdoptedManifestDigest != g.VerificationManifestDigest || m.AdoptedManifestDigest != adopted.Digest() || m.CheckDigest != adopted.Catalog.CheckDigest || !slices.Equal(m.Outcomes, adopted.Proposal.Outcomes) || m.Requirements != a.Authority.RequirementsID || m.Approval != a.Authority.ApprovalID || m.PreparationDigest != d.PreparationDigest {
		return errors.New("verification execution differs from admitted approved definitions")
	}
	check := factory.ApprovedAppCheck{IntentID: d.IntentID, AcceptedRevision: d.AcceptedRevision, Outcomes: m.Outcomes}
	if check.Digest() != d.VerificationManifestDigest {
		return errors.New("verification outcome map differs from its delivery")
	}
	return nil
}

func checkVerifiedManifestExecutionTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (string, error) {
	var generation factory.IntentGeneration
	err := contextJSONRow(ctx, tx, &generation, `SELECT data FROM factory_intent_generations WHERE revision=$1 FOR SHARE`, a.Subject.Delivery.AcceptedRevision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if generation.VerificationManifest == nil {
		return "", nil
	}
	var run factory.Run
	if err := contextJSONRow(ctx, tx, &run, `SELECT data FROM factory_runs WHERE id=$1 FOR SHARE`, a.Run); err != nil {
		return "", err
	}
	if len(run.VerificationManifest) == 0 {
		if err := checkVerificationManifestPacketTx(ctx, tx, a, run); err != nil {
			return "", err
		}
		return "", nil
	}
	if err := checkVerificationManifestPacketTx(ctx, tx, a, run); err != nil {
		return "", err
	}
	s := run.VerificationChecks
	if !run.Reconciled || run.Outcome != factory.Succeeded || !run.CredentialsReturned() || s == nil || s.Phase != "passed" || run.InvocationID() == "" || run.InvocationID() != s.Incarnation {
		return "", errors.New("application manifest has no retired native run receipt")
	}
	if run.Validate() != nil {
		return "", errors.New("application manifest run receipt is invalid")
	}
	if a.Result == nil || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Verification == nil {
		return "", errors.New("application has no typed verification report")
	}
	receipts := a.Result.PhaseResult.Output.Verification.CheckReceipts
	if len(receipts) != 1 || receipts[0].CheckID != factory.ApprovedAppCheckID || receipts[0].Outcome != factory.CheckReceiptPassed || receipts[0].ArtifactDigest != s.OutputDigest {
		return "", errors.New("verification report differs from native manifest receipt")
	}
	return s.OutputDigest, nil
}
