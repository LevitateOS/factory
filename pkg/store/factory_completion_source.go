package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// ErrCompletionAuthorityChanged reports a source-qualified capture whose
// projected authority no longer equals the context the caller opened:
// a head, policy or verdict moved between the context read and the
// capture. The capture refuses with no fallback and no sealed output.
var ErrCompletionAuthorityChanged = errors.New("completion source context no longer matches its authority")

// ErrCompletionSourceHold reports a source-qualified capture without its
// approved source proof: no standing ready preparation matches the
// current heads, the changed tip carries no exact approved input
// selection, or the changed tip has no reconciled successful coder
// publication and merge. The capture holds; it never seals from an
// unproven source.
var ErrCompletionSourceHold = errors.New("completion source lacks its approved proof")

// preparationBinding is the digest-stable approved-preparation identity
// behind one capture: the standing preparation references without
// private candidate custody or runtime execution state.
type preparationBinding struct {
	ID                string `json:"id"`
	Revision          int64  `json:"revision"`
	SourceCommit      string `json:"source_commit"`
	SetupDigest       string `json:"setup_digest"`
	RequirementDigest string `json:"requirement_digest"`
	ApprovalDigest    string `json:"approval_digest"`
}

// completionLocalProjectionTx projects the entire final material and
// the approved-preparation digest inside the caller's repeatable-read
// capture transaction. It performs no commit: the caller seals and
// commits. It is the ONE shared private capture body behind both the
// plain local wrapper and the source-qualified entry.
func completionLocalProjectionTx(ctx context.Context, tx *sql.Tx, repository int64) (*factory.CompletionMaterial, string, error) {
	material, err := completionMaterialTx(ctx, tx, repository)
	if err != nil {
		return nil, "", err
	}
	// The projected material already carries the SAME-RR repo project
	// and the current-head requirement source; reuse them instead of
	// re-reading the heads.
	projectID := material.Authority.ProjectID
	preparationRows, err := tx.QueryContext(ctx, `SELECT data FROM project_preparations
		WHERE project_id=$1 AND NOT (data ? 'factory_admission') ORDER BY id`, projectID)
	if err != nil {
		return nil, "", err
	}
	var preparations []preparationBinding
	for preparationRows.Next() {
		var raw []byte
		var stored execution.PreparationRecord
		if err = preparationRows.Scan(&raw); err != nil {
			_ = preparationRows.Close()
			return nil, "", err
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			_ = preparationRows.Close()
			return nil, "", err
		}
		preparations = append(preparations, preparationBinding{
			ID: stored.Preparation.ID, Revision: stored.Preparation.Revision,
			SourceCommit: stored.Preparation.SourceCommit, SetupDigest: stored.Preparation.SetupDigest,
			RequirementDigest: stored.Preparation.Requirements.Digest,
			ApprovalDigest:    stored.Preparation.Approval.EffectsDigest,
		})
	}
	if err = preparationRows.Err(); err != nil {
		_ = preparationRows.Close()
		return nil, "", err
	}
	_ = preparationRows.Close()
	preparationDoc, err := json.Marshal(struct {
		Preparations []preparationBinding `json:"preparations"`
	}{preparations})
	if err != nil {
		return nil, "", err
	}
	preparationSum := sha256.Sum256(preparationDoc)
	return material, hex.EncodeToString(preparationSum[:]), nil
}

// sealCompletionLocal assembles one validated local half over an
// already-projected same-transaction material: the scope digest stays
// exactly the canonical material digest, and the source commit is the
// caller's proven tip, never derived here.
func sealCompletionLocal(material *factory.CompletionMaterial, preparationDigest, sourceCommit string) (factory.CompletionLocal, error) {
	local := factory.CompletionLocal{
		Material:             material,
		SourceCommit:         sourceCommit,
		PreparationDigest:    preparationDigest,
		ScopeDigest:          "",
		AuthorityFingerprint: factory.AuthorityFingerprint(material.Authority.Effective),
	}
	scopeDigest, err := material.Digest()
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	local.ScopeDigest = scopeDigest
	if err = local.Validate(); err != nil {
		return factory.CompletionLocal{}, err
	}
	return local, nil
}

// CompletionSourceContext projects the standing completion authority
// for one repository in a single repeatable-read transaction: the repo
// project plus the existing authority/head/policy projection,
// validated before commit. Only the safe authority value is returned.
func (s *Store) CompletionSourceContext(ctx context.Context, repository int64) (factory.CompletionAuthority, error) {
	if repository <= 0 {
		return factory.CompletionAuthority{}, errors.New("completion source context lacks its repository")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return factory.CompletionAuthority{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var projectID string
	if err = tx.QueryRowContext(ctx, `SELECT p.id`+nativeProjectCollaborationJoin+` WHERE r.id=$1`, repository).Scan(&projectID); err != nil {
		return factory.CompletionAuthority{}, errors.New("completion source context lacks its project")
	}
	authority, err := completionAuthorityTx(ctx, tx, repository, projectID)
	if err != nil {
		return factory.CompletionAuthority{}, err
	}
	if err = authority.Validate(); err != nil {
		return factory.CompletionAuthority{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.CompletionAuthority{}, err
	}
	return authority, nil
}

// selectSourcePreparationTx returns the first standing ready coder
// preparation in ORDER BY id matching the current requirement and
// approval head IDs, the repo project and the current setup,
// requirements and approval-effects digests. Private candidate
// envelopes never enter, every candidate validates as a stored
// preparation, and a missing matching ready base holds instead of
// sealing from a fabricated preparation.
func selectSourcePreparationTx(ctx context.Context, tx *sql.Tx, projectID string, head completionHeadContext) (execution.PreparationRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM project_preparations
		WHERE project_id=$1 AND NOT (data ? 'factory_admission') ORDER BY id`, projectID)
	if err != nil {
		return execution.PreparationRecord{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		var stored execution.PreparationRecord
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return execution.PreparationRecord{}, err
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			_ = rows.Close()
			return execution.PreparationRecord{}, err
		}
		if err = stored.Validate(); err != nil {
			_ = rows.Close()
			return execution.PreparationRecord{}, err
		}
		preparation := stored.Preparation
		if preparation.Project != projectID || preparation.Role != project.RoleCoder || !stored.State.Ready {
			continue
		}
		if preparation.Requirements.ID != head.requirement.ID || preparation.Approval.ID != head.approval.ID {
			continue
		}
		if preparation.SetupDigest != head.requirement.SetupDigest ||
			preparation.Requirements.Digest != head.requirement.InputsDigest ||
			preparation.Approval.EffectsDigest != head.approval.EffectsDigest {
			continue
		}
		_ = rows.Close()
		return stored, nil
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return execution.PreparationRecord{}, err
	}
	_ = rows.Close()
	return execution.PreparationRecord{}, ErrCompletionSourceHold
}

// CaptureCompletionLocalAtSource captures the validated local half
// qualified by the caller's authority context and the actual native
// tip in ONE repeatable-read transaction: the entire final material
// and preparation digest are reconstructed here, the projected
// authority must equal the expected context by canonical validated
// JSON comparison, and the tip must prove its source. A tip equal to
// the selected standing preparation source is the exact approved
// base; a changed tip additionally requires the current immutable
// requirement SourceInputs and the preparation SourceInputs both
// nonempty and exactly matching, plus the SAME-RR
// publication/merged/run predicate through the existing merged
// preparation source validator. The final Local.SourceCommit is the
// actual tip while RequirementSourceCommit and the delivered merge
// provenance stay distinct. Any mismatch or unproven tip holds with a
// named error, never an accepted-source fallback, and no JSON or
// private values enter errors.
func admitCompletionCapture(repository int64, expected factory.CompletionAuthority, tip string) error {
	if repository <= 0 {
		return errors.New("completion source capture lacks its repository")
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	if !factory.ValidCommit(tip) {
		return errors.New("completion source capture lacks its tip")
	}
	return nil
}

func matchCompletionAuthority(expected, actual factory.CompletionAuthority) error {
	if err := actual.Validate(); err != nil {
		return err
	}
	want, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	got, err := json.Marshal(actual)
	if err != nil {
		return err
	}
	if string(got) != string(want) {
		return ErrCompletionAuthorityChanged
	}
	return nil
}

func reconcileCompletionTip(ctx context.Context, tx *sql.Tx, repository int64, projectID string, head completionHeadContext, preparation execution.PreparationRecord, actual factory.CompletionAuthority, tip string) error {
	if tip == preparation.Preparation.SourceCommit {
		return nil
	}
	required := head.requirement.SourceInputs
	selected := preparation.Preparation.Requirements.SourceInputs
	if len(required) == 0 || len(selected) == 0 || !maps.Equal(required, selected) {
		return ErrCompletionSourceHold
	}
	if _, err := mergedPreparationSourceTx(ctx, tx, repository, projectID, actual.TargetBranch, tip); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrCandidatePreparationConflict) {
			return ErrCompletionSourceHold
		}
		return err
	}
	return nil
}

func (s *Store) CaptureCompletionLocalAtSource(ctx context.Context, repository int64, expected factory.CompletionAuthority, tip string) (factory.CompletionLocal, error) {
	if err := admitCompletionCapture(repository, expected, tip); err != nil {
		return factory.CompletionLocal{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var projectID string
	if err = tx.QueryRowContext(ctx, `SELECT p.id`+nativeProjectCollaborationJoin+` WHERE r.id=$1`, repository).Scan(&projectID); err != nil {
		return factory.CompletionLocal{}, errors.New("completion source capture lacks its project")
	}
	material, preparationDigest, err := completionLocalProjectionTx(ctx, tx, repository)
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	if err = matchCompletionAuthority(expected, material.Authority); err != nil {
		return factory.CompletionLocal{}, err
	}
	head, err := completionHeadContextTx(ctx, tx, projectID)
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	preparation, err := selectSourcePreparationTx(ctx, tx, projectID, head)
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	if err = reconcileCompletionTip(ctx, tx, repository, projectID, head, preparation, material.Authority, tip); err != nil {
		return factory.CompletionLocal{}, err
	}
	local, err := sealCompletionLocal(material, preparationDigest, tip)
	if err != nil {
		return factory.CompletionLocal{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.CompletionLocal{}, err
	}
	return local, nil
}
