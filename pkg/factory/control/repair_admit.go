package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// repairEpisodeChargeID derives the deterministic cycle-1
// investigator-run charge identity for one bound stall episode.
// Redetecting the same episode replays the identical charge, so the
// store spends once and replays return the recorded receipt.
func repairEpisodeChargeID(episodeID string) string {
	return factory.RepairChargeID(episodeID, factory.RepairChargeRun, 1)
}

// AdmitStallEpisodeRun admits one cycle-1 investigator-run charge for a
// classified stall repository result under the repository's explicit
// repair grant. The caller passes the detection round's progress
// generation: the bound episode must match it exactly, so a stale
// episode from an older round redetects instead of spending and a
// generation mismatch is impossible by construction. Non-stall
// results admit nothing and report unspent; a stall result with an
// invalid bound event fails closed instead of spending against an
// unnamed episode. Later cycles need materially new evidence through
// the Strategist proposal path, never this joint.
func AdmitStallEpisodeRun(ctx context.Context, db *store.Store, result StallRepositoryResult, generation int64, activeMinutes int, now time.Time) (bool, error) {
	if result.Kind != StallRepositoryStall || !result.Investigate || result.Event == nil {
		return false, nil
	}
	event := result.Event
	if err := event.Validate(); err != nil {
		return false, errors.New("stall result carries an invalid bound episode")
	}
	if event.Repository != result.Repository {
		return false, errors.New("stall episode belongs to another repository")
	}
	if event.ProgressGeneration != generation {
		return false, errors.New("stall episode no longer matches its detection round: redetect before spending")
	}
	charges, err := db.RepairCharges(ctx, result.Repository)
	if err != nil {
		return false, err
	}
	for _, prior := range charges {
		if prior.ID == repairEpisodeChargeID(event.EpisodeID) {
			// Observation hashes and explicit grant revisions never rewrite the
			// original admitted cycle or replenish its charge.
			spent, err := db.AdmitStallInvestigation(ctx, result.Repository, prior, now)
			return spent, err
		}
	}
	grant, err := db.RepairGrantCommand(ctx, result.Repository)
	if err != nil {
		return false, err
	}
	charge := factory.RepairCharge{
		ID: repairEpisodeChargeID(event.EpisodeID), GrantID: grant.ID,
		Kind: factory.RepairChargeRun, EpisodeID: event.EpisodeID, Cycle: 1,
		EvidenceDigest: event.InventoryDigest, ActiveMinutes: activeMinutes,
		SpentUnix: now.Unix(),
	}
	spent, err := db.AdmitStallInvestigation(ctx, result.Repository, charge, now)
	if err != nil {
		return false, err
	}
	return spent, nil
}
