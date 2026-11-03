package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// ActivateReassessmentIntent commits one preflighted current journal
// step, its accepted successor head, immutable generation and copied
// bindings together. A crash cannot expose an activated head without
// the exact operation that recovery must resume. The controller owns
// typed journal preflight; this boundary compares its exact persisted
// progress under the cycle lock before any head changes.
func (s *Store) ActivateReassessmentIntent(ctx context.Context, cycleID string, expected, next json.RawMessage, revision factory.IntentRevision, generation factory.IntentGeneration) error {
	if !factory.ValidID(cycleID) || !json.Valid(expected) || !json.Valid(next) || generation.ReassessmentCycle != cycleID || revision.State != "accepted" || generation.RevisionID != revision.ID || generation.IntentID != revision.Intent || generation.IntentDigest != revision.Digest() {
		return errors.New("invalid reassessment activation")
	}
	if err := revision.Validate(); err != nil {
		return err
	}
	if err := checkSubmittedGenerationGraph(generation); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lockedReassessmentRowTx(ctx, tx, cycleID, factory.ReassessmentApplying)
	if err != nil {
		return err
	}
	if record.Cycle.Proposal != factory.ReassessmentOptionAccept || record.Cycle.Repository != revision.Repository {
		return ErrStaleRevision
	}
	same, err := sameJSONDocument(ctx, tx, record.AdoptionProgress, expected)
	if err != nil {
		return err
	}
	if !same {
		return ErrStaleRevision
	}
	raw, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	if err := saveIntentRevisionTx(ctx, tx, revision, raw); err != nil {
		return err
	}
	if err := checkGenerationHeadTx(ctx, tx, generation); err != nil {
		return err
	}
	raw, err = json.Marshal(generation)
	if err != nil {
		return err
	}
	if err := insertGenerationRecordTx(ctx, tx, generation, raw); err != nil {
		return err
	}
	for _, binding := range generation.CopiedBindings {
		if err := bindIntentTaskAcceptanceTx(ctx, tx, binding); err != nil {
			return err
		}
	}
	// The activated head must remain representable by the canonical
	// bounded completion capture used by every subsequent recovery pass.
	if _, err := completionMaterialTx(ctx, tx, revision.Repository); err != nil {
		return err
	}
	record.AdoptionProgress, record.WaitReason = next, ""
	raw, err = json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_reassessments SET data=$1 WHERE cycle=$2 AND state=$3`, string(raw), cycleID, factory.ReassessmentApplying); err != nil {
		return err
	}
	return tx.Commit()
}
