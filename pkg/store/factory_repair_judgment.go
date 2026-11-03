package store

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// AdmitStallRepairJudgment consumes one deterministic cycle charge before the
// first external call. A crashed or failed call retries under the same
// fingerprint without a new charge; a saved receipt is replayed by the
// existing purpose journal before this method is reached.
func (s *Store) AdmitStallRepairJudgment(ctx context.Context, repository int64, event, cycleID, fingerprint string, now time.Time) (bool, error) {
	if !factory.ValidDigest(fingerprint) {
		return false, errors.New("invalid repair judgment fingerprint")
	}
	record, err := s.StallRepairRecord(ctx, repository, event)
	if err != nil {
		return false, err
	}
	index := -1
	for i, cycle := range record.Repair.Cycles {
		if cycle.ID == cycleID {
			index = i
		}
	}
	if index < 0 {
		return false, ErrNotFound
	}
	cycle := record.Repair.Cycles[index]
	if cycle.State != factory.RepairAssessing {
		return false, ErrStaleRevision
	}
	if cycle.JudgmentFingerprint != "" {
		if cycle.JudgmentFingerprint != fingerprint {
			return false, ErrStaleRevision
		}
		// The cycle is already charged: retry the call if it left no receipt.
		return true, nil
	}
	grant, err := s.RepairGrantCommand(ctx, repository)
	if err != nil {
		return false, err
	}
	evidence := record.InventoryDigest
	if cycle.Number > 1 {
		evidence = cycle.InventoryDigest
	}
	charge := factory.RepairCharge{ID: factory.RepairChargeID(record.EpisodeID, factory.RepairChargeJudgment, cycle.Number), GrantID: grant.ID, Kind: factory.RepairChargeJudgment, EpisodeID: record.EpisodeID, EvidenceDigest: evidence, Cycle: cycle.Number, SpentUnix: now.Unix()}
	created, err := s.AdmitStallInvestigation(ctx, repository, charge, now)
	if err != nil || !created {
		return false, err
	}
	cycle.JudgmentFingerprint = fingerprint
	record.Repair.Cycles[index] = cycle
	record.Repair.Revision++
	if err = s.SaveStallRepairEpisode(ctx, record.StallEvent, record.Repair.Revision-1, record.Repair); err != nil {
		return false, err
	}
	return true, nil
}
