package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// ActivateStallRepairIntent uses the same serial successor and native generation
// owners as completion adoption. Its genuine stall cycle and journal CAS are
// committed in the same transaction as the head, operation and copied bindings.
func (s *Store) ActivateStallRepairIntent(ctx context.Context, repository int64, event, cycleID string, expected, next json.RawMessage, revision factory.IntentRevision, generation factory.IntentGeneration) error {
	if !factory.ValidID(cycleID) || !json.Valid(expected) || !json.Valid(next) || generation.RepairCycle != cycleID || generation.ReassessmentCycle != "" || revision.Repository != repository || revision.State != "accepted" || generation.RevisionID != revision.ID || generation.IntentID != revision.Intent || generation.IntentDigest != revision.Digest() {
		return errors.New("invalid stall repair activation")
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
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_stall_events WHERE repository=$1 AND event=$2 FOR UPDATE`, repository, event).Scan(&raw); err != nil {
		return err
	}
	var record StallRepairRecord
	if json.Unmarshal(raw, &record) != nil {
		return errors.New("invalid repair record")
	}
	index := -1
	for i, cycle := range record.Repair.Cycles {
		if cycle.ID == cycleID {
			index = i
		}
	}
	if index < 0 {
		return ErrNotFound
	}
	cycle := record.Repair.Cycles[index]
	if cycle.State != factory.RepairApplying || !factory.ValidReassessmentProposal(cycle.ProposalOption, cycle.ProposalProbability, cycle.ProposalConfidence) {
		return ErrStaleRevision
	}
	same, err := sameJSONDocument(ctx, tx, cycle.AdoptionProgress, expected)
	if err != nil {
		return err
	}
	if !same {
		return ErrStaleRevision
	}
	raw, err = json.Marshal(revision)
	if err != nil {
		return err
	}
	if err = saveIntentRevisionTx(ctx, tx, revision, raw); err != nil {
		return err
	}
	if err = checkGenerationHeadTx(ctx, tx, generation); err != nil {
		return err
	}
	raw, err = json.Marshal(generation)
	if err != nil {
		return err
	}
	if err = insertGenerationRecordTx(ctx, tx, generation, raw); err != nil {
		return err
	}
	for _, binding := range generation.CopiedBindings {
		if err = bindIntentTaskAcceptanceTx(ctx, tx, binding); err != nil {
			return err
		}
	}
	if _, err = completionMaterialTx(ctx, tx, repository); err != nil {
		return err
	}
	cycle.AdoptionProgress, cycle.Reason = next, ""
	record.Repair.Cycles[index] = cycle
	record.Repair.Revision++
	raw, err = json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_stall_events SET data=$1 WHERE repository=$2 AND event=$3`, string(raw), repository, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StallRepairCycle(ctx context.Context, cycleID string) (StallRepairRecord, factory.StallRepairCycle, error) {
	if !factory.ValidID(cycleID) {
		return StallRepairRecord{}, factory.StallRepairCycle{}, errors.New("invalid repair cycle identity")
	}
	var raw []byte
	var record StallRepairRecord
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_stall_events WHERE data->'repair'->'cycles' @> $1::jsonb`, `[{"id":"`+cycleID+`"}]`).Scan(&raw)
	if err != nil {
		return record, factory.StallRepairCycle{}, err
	}
	if json.Unmarshal(raw, &record) != nil {
		return record, factory.StallRepairCycle{}, errors.New("invalid repair cycle record")
	}
	for _, cycle := range record.Repair.Cycles {
		if cycle.ID == cycleID {
			return record, cycle, nil
		}
	}
	return record, factory.StallRepairCycle{}, ErrNotFound
}
