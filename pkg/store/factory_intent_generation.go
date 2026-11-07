package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

func (s *Store) IntentGeneration(ctx context.Context, revision string) (factory.IntentGeneration, error) {
	var g factory.IntentGeneration
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_intent_generations WHERE revision=$1`, revision).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &g)
	}
	return g, err
}

func checkSubmittedGenerationGraph(g factory.IntentGeneration) error {
	if err := checkGenerationIdentity(g); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, task := range g.SubmittedTasks {
		if !project.ValidDecisionID(task.Key) || seen[task.Key] {
			return errors.New("invalid submitted task graph")
		}
		seen[task.Key] = true
	}
	if g.ProtectedGraphCycle() != "" {
		if !factory.ValidID(g.ProtectedGraphCycle()) || g.PlanAssignment != "" || len(g.Delivered) > 0 {
			return errors.New("invalid reassessment generation owner")
		}
		for _, bindings := range [][]factory.IntentTaskBinding{g.RetainedBindings, g.CopiedBindings} {
			bound := map[string]bool{}
			for _, binding := range bindings {
				if !seen[binding.TaskKey] || bound[binding.TaskKey] || binding.RevisionID != g.RevisionID || binding.IssueID <= 0 || binding.IssueIndex <= 0 || !project.ValidDecisionID(binding.AcceptanceID) {
					return errors.New("invalid reassessment retained binding")
				}
				bound[binding.TaskKey] = true
			}
		}
	}
	for key, ref := range g.Delivered {
		if !seen[key] || g.TaskIssueIDs[key] != 0 || ref.IssueID <= 0 || ref.IssueIndex <= 0 || ref.ContentVersion < 0 {
			return errors.New("invalid delivered reuse")
		}
	}
	return nil
}

func checkGenerationIdentity(g factory.IntentGeneration) error {
	if (g.ReassessmentCycle != "" || g.RepairCycle != "") && !factory.ValidID(g.ProtectedGraphCycle()) {
		return errors.New("ambiguous protected graph owner")
	}
	localOnly := g.LocalOnly && factory.ValidID(g.ProtectedGraphCycle()) && g.OperationID == "" && g.State == "completed" && len(g.CopiedBindings) == len(g.SubmittedTasks)
	if g.LocalOnly && !localOnly {
		return errors.New("invalid local reassessment generation")
	}
	if !project.ValidDecisionID(g.RevisionID) || !project.ValidDecisionID(g.IntentID) || (!localOnly && g.OperationID == "") || g.ActorID <= 0 || g.NativeRev < 1 || g.NotAfter <= 0 || !factory.ValidDigest(g.IntentDigest) || !json.Valid(g.Payload) || (!localOnly && g.State != "pending") || len(g.Receipt) > 0 || len(g.SubmittedTasks) == 0 {
		return errors.New("invalid intent generation")
	}
	return nil
}

func checkGenerationHeadTx(ctx context.Context, tx *sql.Tx, g factory.IntentGeneration) error {
	var head string
	var intentRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1 FOR UPDATE`, g.IntentID).Scan(&head); err != nil {
		return err
	}
	if head != g.RevisionID {
		return ErrStaleRevision
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1`, head).Scan(&intentRaw); err != nil {
		return err
	}
	var r factory.IntentRevision
	if err := json.Unmarshal(intentRaw, &r); err != nil {
		return err
	}
	if r.State != "accepted" || r.Digest() != g.IntentDigest {
		return ErrStaleRevision
	}
	if err := checkGenerationManifestTx(ctx, tx, r, g); err != nil {
		return err
	}
	return nil
}

// The head lock and preparation-head locks make proposal adoption atomic with
// the exact accepted graph. Native receipt updates cannot replace this binding.
func checkGenerationManifestTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision, g factory.IntentGeneration) error {
	prior, priorRevision, found, err := inheritedGenerationManifestTx(ctx, tx, r)
	if err != nil {
		return err
	}
	if g.ProtectedGraphCycle() != "" {
		if !found {
			if g.VerificationManifest != nil || g.VerificationManifestDigest != "" {
				return errors.New("protected graph invented a verification manifest")
			}
			return nil
		}
		if prior.VerificationManifest.ValidateFor(priorRevision) != nil || g.VerificationManifest == nil || g.VerificationManifest.Proposal.Digest() != prior.VerificationManifest.Proposal.Digest() || g.VerificationManifest.Catalog != prior.VerificationManifest.Catalog || g.VerificationManifest.ValidateFor(r) != nil || g.VerificationManifestDigest != g.VerificationManifest.Digest() {
			return errors.New("protected graph omitted or changed its adopted verification manifest")
		}
		return nil
	}
	if found && g.VerificationManifest == nil {
		return errors.New("generation omitted its prior adopted verification manifest")
	}
	if g.VerificationManifest == nil && g.VerificationManifestDigest == "" && g.PlanAssignment == "" {
		return nil
	}
	var a factory.Assignment
	if err := contextJSONRow(ctx, tx, &a, `SELECT data FROM factory_assignments WHERE id=$1 FOR SHARE`, g.PlanAssignment); err != nil {
		// The existing nil-manifest graph mechanics path has no proposal to
		// adopt. Actual planning provenance is separately required by the
		// coordinator before effects; this branch grants no manifest authority.
		if errors.Is(err, sql.ErrNoRows) && g.VerificationManifest == nil && g.VerificationManifestDigest == "" && !found {
			return nil
		}
		return err
	}
	var proposal *factory.VerificationManifestProposal
	if a.Result != nil && a.Result.Reported && a.Result.PhaseResult != nil && a.Result.PhaseResult.Status == "completed" && a.Result.PhaseResult.Output != nil && a.Result.PhaseResult.Output.Decomposition != nil {
		proposal = a.Result.PhaseResult.Output.Decomposition.VerificationManifestProposal
	}
	if (proposal == nil) != (g.VerificationManifest == nil) {
		return errors.New("generation omitted or invented its recorded verification manifest")
	}
	if g.VerificationManifest == nil {
		if g.VerificationManifestDigest != "" {
			return errors.New("generation has an unbound verification manifest digest")
		}
		return nil
	}
	m := g.VerificationManifest
	if err := m.ValidateFor(r); err != nil {
		return err
	}
	if a.Stage != factory.AssignmentFinished || a.Subject.Intent == nil || a.Subject.Intent.IntentID != r.Intent || a.Subject.Intent.RevisionID != r.Predecessor || a.Subject.Intent.OwnerID != r.Owner || a.Repository != r.Repository || a.ProjectID != m.Catalog.Project || a.Preparation != m.Catalog.Preparation || a.SourceCommit != m.Catalog.SourceCommit || proposal.Digest() != m.Proposal.Digest() || g.VerificationManifestDigest != m.Digest() {
		return errors.New("generation verification manifest differs from its recorded plan")
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT p.data FROM project_preparations p JOIN project_requirement_heads r ON r.project_id=p.project_id AND r.decision=p.requirements JOIN project_approval_heads h ON h.project_id=p.project_id AND h.decision=p.approval WHERE p.id=$1 FOR SHARE OF p,r,h`, a.Preparation).Scan(&raw); err != nil {
		return err
	}
	var stored execution.PreparationRecord
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	p := stored.Preparation
	if p.Validate() != nil || p.Project != m.Catalog.Project || p.SourceCommit != m.Catalog.SourceCommit || p.Requirements.ID != m.Catalog.Requirements || p.Approval.ID != m.Catalog.Approval || factory.DeliveryPreparationDigest(p) != m.Catalog.PreparationDigest {
		return errors.New("generation verification catalog approval changed")
	}
	return nil
}

// A protected delta can only rebind definitions already adopted in its actual
// revision ancestry. Drafts between accepted heads do not erase that authority.
func inheritedGenerationManifestTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision) (factory.IntentGeneration, factory.IntentRevision, bool, error) {
	var generation factory.IntentGeneration
	var revision factory.IntentRevision
	if r.Predecessor == "" {
		return generation, revision, false, nil
	}
	var generationRaw, revisionRaw []byte
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE lineage AS (
	 SELECT id,data,1 AS depth FROM factory_intent_revisions WHERE id=$1 AND intent=$2
	 UNION ALL SELECT p.id,p.data,l.depth+1 FROM factory_intent_revisions p JOIN lineage l ON p.id=l.data->>'predecessor' WHERE p.intent=$2 AND l.depth<64)
	 SELECT g.data,l.data FROM lineage l LEFT JOIN factory_intent_generations g ON g.revision=l.id
	 WHERE (g.data->'verification_manifest' IS NOT NULL AND g.data->'verification_manifest'<>'null'::jsonb)
	 OR (l.depth=64 AND COALESCE(l.data->>'predecessor','')<>'') ORDER BY l.depth LIMIT 1`, r.Predecessor, r.Intent).Scan(&generationRaw, &revisionRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return generation, revision, false, nil
	}
	if err != nil {
		return generation, revision, false, err
	}
	if len(generationRaw) == 0 {
		return generation, revision, false, errors.New("verification manifest ancestry exceeds its read bound")
	}
	if err = json.Unmarshal(generationRaw, &generation); err != nil {
		return generation, revision, false, err
	}
	if err = json.Unmarshal(revisionRaw, &revision); err != nil {
		return generation, revision, false, err
	}
	if generation.VerificationManifest == nil || generation.IntentID != r.Intent || revision.Intent != r.Intent || revision.Repository != r.Repository || revision.Owner != r.Owner {
		return generation, revision, false, errors.New("inherited verification manifest differs from product authority")
	}
	return generation, revision, true, nil
}

func checkDeliveredBindingTx(ctx context.Context, tx *sql.Tx, g factory.IntentGeneration, key string, ref factory.GeneratedIssueIdentity) error {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_intent_tasks WHERE task=$1 AND revision IN (SELECT id FROM factory_intent_revisions WHERE intent=$2 AND id<>$3)`, key, g.IntentID, g.RevisionID)
	if err != nil {
		return err
	}
	chained := false
	for rows.Next() {
		var rawBinding []byte
		var b factory.IntentTaskBinding
		if err = rows.Scan(&rawBinding); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(rawBinding, &b); err != nil {
			rows.Close()
			return err
		}
		if b.IssueID == ref.IssueID && b.IssueIndex == ref.IssueIndex && b.ContentVersion == ref.ContentVersion {
			chained = true
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	if !chained {
		return errors.New("invalid delivered reuse")
	}
	return nil
}

func insertGenerationRecordTx(ctx context.Context, tx *sql.Tx, g factory.IntentGeneration, raw []byte) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_intent_generations(revision,data) VALUES($1,$2) ON CONFLICT(revision) DO NOTHING`, g.RevisionID, string(raw)); err != nil {
		return err
	}
	var old []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_generations WHERE revision=$1`, g.RevisionID).Scan(&old); err != nil {
		return err
	}
	same, err := sameJSONDocument(ctx, tx, old, raw)
	if err != nil {
		return err
	}
	if !same {
		return ErrCommandConflict
	}
	return nil
}

// BeginIntentGeneration persists the captured operation before submission.
// Delivered-history checks consume decoded facts bound to the opaque packet;
// provider field names and decoding belong to the adapter.
func (s *Store) BeginIntentGeneration(ctx context.Context, g factory.IntentGeneration, packet factory.GraphPacketFacts) error {
	if g.ReassessmentCycle != "" || g.RepairCycle != "" || g.LocalOnly || len(g.CopiedBindings) != 0 || len(g.RetainedBindings) != 0 {
		return errors.New("reassessment generations require their atomic cycle activation")
	}
	if err := checkSubmittedGenerationGraph(g); err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if len(g.Delivered) > 0 {
		if !json.Valid(packet.Canonical) {
			return errors.New("generation packet facts are required")
		}
		same, err := sameJSONDocument(ctx, tx, g.Payload, packet.Canonical)
		if err != nil {
			return err
		}
		if !same {
			return ErrCommandConflict
		}
		for _, ref := range g.Delivered {
			if _, edited := packet.Edits[ref.IssueID]; edited {
				return errors.New("delivered history unexpectedly mutated")
			}
		}
	}
	if err = checkGenerationHeadTx(ctx, tx, g); err != nil {
		return err
	}
	for key, ref := range g.Delivered {
		if err = checkDeliveredBindingTx(ctx, tx, g, key, ref); err != nil {
			return err
		}
	}
	if err = insertGenerationRecordTx(ctx, tx, g, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func compareGenerationRecord(old, g factory.IntentGeneration) (a, b []byte, err error) {
	compare := g
	compare.State = old.State
	compare.Receipt = old.Receipt
	if a, err = json.Marshal(compare); err != nil {
		return nil, nil, err
	}
	if b, err = json.Marshal(old); err != nil {
		return nil, nil, err
	}
	return a, b, nil
}

func checkGenerationReceipt(g factory.IntentGeneration, receipt factory.GraphReceiptFacts) error {
	if g.State != "pending" && g.State != "committed" && g.State != "refused" && g.State != "completed" {
		return errors.New("invalid generation outcome")
	}
	if len(g.Receipt) > 0 && len(g.Delivered) > 0 {
		issues := append([]factory.GeneratedIssueIdentity(nil), receipt.Edits...)
		for _, identity := range receipt.Creates {
			issues = append(issues, identity)
		}
		for _, entry := range issues {
			for _, ref := range g.Delivered {
				if entry.IssueID == ref.IssueID {
					return errors.New("delivered history unexpectedly mutated")
				}
			}
		}
	}
	return nil
}

func checkGenerationRollback(old, g factory.IntentGeneration) error {
	if old.State == "completed" && g.State != "completed" || old.State == "refused" && g.State != "refused" || old.State == "committed" && g.State == "pending" {
		return ErrCommandConflict
	}
	return nil
}

func writeGenerationRecordTx(ctx context.Context, tx *sql.Tx, g factory.IntentGeneration, b []byte) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_intent_generations SET data=$2 WHERE revision=$1 AND data=$3::jsonb`, g.RevisionID, string(raw), string(b))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

// RecordIntentGeneration retains the original provider receipt and outcome.
// Decoded facts must match that receipt before any delivered-history check.
func (s *Store) RecordIntentGeneration(ctx context.Context, g factory.IntentGeneration, receipt factory.GraphReceiptFacts) error {
	old, err := s.IntentGeneration(ctx, g.RevisionID)
	if err != nil {
		return err
	}
	a, b, err := compareGenerationRecord(old, g)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	same, err := sameJSONDocument(ctx, tx, a, b)
	if err != nil {
		return err
	}
	if !same {
		return ErrCommandConflict
	}
	if len(g.Delivered) > 0 && len(g.Receipt) > 0 {
		if !json.Valid(receipt.Original) {
			return errors.New("generation receipt facts are required")
		}
		same, err := sameJSONDocument(ctx, tx, g.Receipt, receipt.Original)
		if err != nil {
			return err
		}
		if !same {
			return ErrCommandConflict
		}
	}
	if err = checkGenerationReceipt(g, receipt); err != nil {
		return err
	}
	if err = checkGenerationRollback(old, g); err != nil {
		return err
	}
	if err = writeGenerationRecordTx(ctx, tx, g, b); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BindIntentTaskAcceptance(ctx context.Context, b factory.IntentTaskBinding) error {
	if !project.ValidDecisionID(b.RevisionID) || !project.ValidDecisionID(b.TaskKey) || !project.ValidDecisionID(b.AcceptanceID) || b.IssueID <= 0 || b.IssueIndex <= 0 || b.ContentVersion < 0 || b.CreatedUnix < 0 {
		return errors.New("invalid generated task binding")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := bindIntentTaskAcceptanceTx(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

func bindIntentTaskAcceptanceTx(ctx context.Context, tx *sql.Tx, b factory.IntentTaskBinding) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var head string
	var intent string
	if err = tx.QueryRowContext(ctx, `SELECT intent FROM factory_intent_revisions WHERE id=$1`, b.RevisionID).Scan(&intent); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1 FOR UPDATE`, intent).Scan(&head); err != nil {
		return err
	}
	if head != b.RevisionID {
		return ErrStaleRevision
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_intent_tasks(revision,task,data) VALUES($1,$2,$3) ON CONFLICT(revision,task) DO NOTHING`, b.RevisionID, b.TaskKey, string(raw))
	if err != nil {
		return err
	}
	var old []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_tasks WHERE revision=$1 AND task=$2`, b.RevisionID, b.TaskKey).Scan(&old); err != nil {
		return err
	}
	same, err := sameJSONDocument(ctx, tx, old, raw)
	if err != nil {
		return err
	}
	if !same {
		return ErrCommandConflict
	}
	return nil
}

func (s *Store) IntentTaskBindings(ctx context.Context, revision string) ([]factory.IntentTaskBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_intent_tasks WHERE revision=$1 ORDER BY task`, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []factory.IntentTaskBinding{}
	for rows.Next() {
		var raw []byte
		var b factory.IntentTaskBinding
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// GeneratedIntentCurrent revalidates the accepted product revision behind a
// derived acceptance. Ordinary human issue acceptances have no generated link.
// FW32 (T37): this tests for existence of a current accepted-head binding,
// never an arbitrary LIMIT 1 row over historical bindings. A copied frozen
// acceptance appears in several revisions at once; only a binding whose
// revision is still the accepted head counts as current.
func (s *Store) GeneratedIntentCurrent(ctx context.Context, acceptance string) (bool, bool, error) {
	var generated, current bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_intent_tasks WHERE data->>'acceptance_id'=$1), EXISTS(SELECT 1 FROM factory_intent_tasks t JOIN factory_intent_revisions r ON r.id=t.revision JOIN factory_intent_heads h ON h.id=r.intent AND h.revision=r.id WHERE t.data->>'acceptance_id'=$1 AND r.data->>'state'='accepted' AND NOT EXISTS(SELECT 1 FROM factory_intent_generations g WHERE g.revision=r.id AND g.data->>'state'<>'completed'))`, acceptance).Scan(&generated, &current)
	if err != nil {
		return false, false, err
	}
	if !generated {
		return true, false, nil
	}
	return current, true, nil
}

// PendingIntentGenerations recovers accepted heads that lack confirmed graph
// adoption. Refused terminal operations await a newly reviewed revision.
func (s *Store) PendingIntentGenerations(ctx context.Context, after string, limit int) ([]factory.IntentRevision, error) {
	if limit < 1 || limit > 64 {
		return nil, errors.New("invalid pending intent limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision LEFT JOIN factory_intent_generations g ON g.revision=r.id WHERE r.id>$2 AND r.data->>'state'='accepted' AND (g.revision IS NULL OR g.data->>'state' IN ('pending','committed')) ORDER BY r.id LIMIT $1`, limit, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []factory.IntentRevision{}
	for rows.Next() {
		var raw []byte
		var r factory.IntentRevision
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// PreviousGeneratedIntent follows recorded predecessors rather than assuming
// the previous review draft already owns a native graph.
func (s *Store) PreviousGeneratedIntent(ctx context.Context, intent, revision string) (factory.IntentRevision, error) {
	cursor := revision
	seen := map[string]bool{}
	for cursor != "" {
		if seen[cursor] || len(seen) > 256 {
			return factory.IntentRevision{}, errors.New("invalid intent revision history")
		}
		seen[cursor] = true
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1 AND intent=$2`, cursor, intent).Scan(&raw); err != nil {
			return factory.IntentRevision{}, err
		}
		var r factory.IntentRevision
		if err := json.Unmarshal(raw, &r); err != nil {
			return factory.IntentRevision{}, err
		}
		if cursor != revision && r.State == "accepted" {
			g, err := s.IntentGeneration(ctx, cursor)
			if err == nil && (g.State == "committed" || g.State == "completed") {
				return r, nil
			}
			if err != nil && !errors.Is(err, ErrNotFound) {
				return factory.IntentRevision{}, err
			}
		}
		cursor = r.Predecessor
	}
	return factory.IntentRevision{}, ErrNotFound
}

// IntentGenerationOperation resolves only an original recorded workflow identity.
func (s *Store) IntentGenerationOperation(ctx context.Context, operation string) (factory.IntentGeneration, error) {
	var g factory.IntentGeneration
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_intent_generations WHERE data->>'operation_id'=$1`, operation).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &g)
	}
	return g, err
}

// IntentGenerationHead is an internal recovery lookup of the current accepted
// head behind an already registered generation. It supplies its recorded owner.
func (s *Store) IntentGenerationHead(ctx context.Context, revision string) (factory.IntentRevision, error) {
	var r factory.IntentRevision
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision WHERE r.id=$1`, revision).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	return r, err
}
