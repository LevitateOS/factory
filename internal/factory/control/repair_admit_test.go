package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// TestAdmitStallEpisodeRunAdmitsOneBoundCharge proves the R36→R37
// joint on the real store path: a classified stall with a valid bound
// episode spends exactly one cycle-1 investigator charge under the
// explicit grant; redetecting the same episode replays without
// spending; any other classification admits nothing.
func TestAdmitStallEpisodeRunAdmitsOneBoundCharge(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 21
	grantID := "d1000000000000000000000000000001"
	grant := factory.RepairGrant{
		ID: grantID, Repository: repo, Sponsor: 7, Revision: 0,
		InvestigatorRuns: 4, ProposalJudgments: 4, ActiveMinutes: 120,
		Active: true, CreatedUnix: 1000,
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	cmd := factory.Command{
		ID: grantID, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(repo),
		Principal: "soda-tester", Payload: string(raw),
	}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if _, created, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, time.Now().UTC()); err != nil || !created {
		t.Fatalf("apply grant: created=%v err=%v", created, err)
	}

	stallSeedControl(t, db, repo, 1, factory.ReadinessBlocked,
		factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0))
	// Fresh ephemeral store: zero verified deliveries, so the round
	// binds generation zero and the matching episode admits.
	classified, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 0})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(classified.Repositories) != 1 {
		t.Fatalf("repositories=%+v, want the seeded repository", classified.Repositories)
	}
	result := classified.Repositories[0]
	if result.Kind != StallRepositoryStall || !result.Investigate || result.Event == nil {
		t.Fatalf("repo=%+v, want a bound stall episode", result)
	}

	now := time.Now().UTC()
	// An otherwise valid episode from another repository must not spend
	// this repository's grant, even when its generation matches.
	foreign := result
	foreignEvent := *result.Event
	foreignEvent.Repository = repo + 1
	foreign.Event = &foreignEvent
	if err := foreignEvent.Validate(); err != nil {
		t.Fatalf("foreign episode fixture must remain valid: %v", err)
	}
	if spent, err := AdmitStallEpisodeRun(ctx, db, foreign, 0, 30, now); err == nil || spent {
		t.Fatalf("foreign repository episode: spent=%v err=%v, want refusal", spent, err)
	}
	if charges, err := db.RepairCharges(ctx, repo); err != nil || len(charges) != 0 {
		t.Fatalf("foreign episode charged target grant: charges=%d err=%v", len(charges), err)
	}
	spent, err := AdmitStallEpisodeRun(ctx, db, result, 0, 30, now)
	if err != nil || !spent {
		t.Fatalf("first admission: spent=%v err=%v", spent, err)
	}
	again, err := AdmitStallEpisodeRun(ctx, db, result, 0, 30, now)
	if err != nil || again {
		t.Fatalf("episode replay: spent=%v err=%v", again, err)
	}
	// The same episode against a newer round generation is stale and
	// must redetect, never spend: generation matching is exact.
	if spent, err := AdmitStallEpisodeRun(ctx, db, result, 1, 30, now); err == nil || spent {
		t.Fatalf("stale episode: spent=%v err=%v, want a redetect refusal", spent, err)
	}
	charges, err := db.RepairCharges(ctx, repo)
	if err != nil || len(charges) != 1 {
		t.Fatalf("charges=%d err=%v, want the single spent charge", len(charges), err)
	}
	if charges[0].EpisodeID != result.Event.EpisodeID || charges[0].Cycle != 1 || charges[0].Kind != factory.RepairChargeRun {
		t.Fatalf("charge does not bind its episode: %+v", charges[0])
	}
}

// TestAdmitStallEpisodeRunRefusesWithoutStall pins the negative half:
// ready, waiting and attention classifications admit nothing, and a
// stall result with a broken episode fails closed.
func TestAdmitStallEpisodeRunRefusesWithoutStall(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	now := time.Now().UTC()
	for _, result := range []StallRepositoryResult{
		{Repository: 21, Kind: StallRepositoryReady},
		{Repository: 21, Kind: StallRepositoryZeroWait},
		{Repository: 21, Kind: StallRepositoryAttention},
		{Repository: 21, Kind: StallRepositoryVerify},
		{Repository: 21, Kind: StallRepositoryStall, Investigate: false, Event: &factory.StallEvent{}},
	} {
		if spent, err := AdmitStallEpisodeRun(ctx, db, result, 0, 30, now); err != nil || spent {
			t.Fatalf("kind %q: spent=%v err=%v", result.Kind, spent, err)
		}
	}
	broken := StallRepositoryResult{
		Repository: 21, Kind: StallRepositoryStall, Investigate: true,
		Event: &factory.StallEvent{ID: "bad", EpisodeID: "worse"},
	}
	if _, err := AdmitStallEpisodeRun(ctx, db, broken, 0, 30, now); err == nil {
		t.Fatal("invalid bound episode admitted without an error")
	}
}

// TestAdmitStallEpisodeRunRefusesFutureGeneration pins generation
// matching: an episode bound ahead of the verified-delivery watermark
// cannot spend, even with a fully valid event shape.
func TestAdmitStallEpisodeRunRefusesFutureGeneration(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	ahead := StallRepositoryResult{
		Repository: 21, Kind: StallRepositoryStall, Investigate: true,
		Event: &factory.StallEvent{
			ID: factory.NewID(), EpisodeID: factory.NewID(),
			Repository: 21, ProgressGeneration: 5,
			InputFingerprint: strings.Repeat("3", 64), InventoryDigest: strings.Repeat("4", 64),
			Reason: factory.StallReasonCycle, State: factory.StallObserved,
		},
	}
	if spent, err := AdmitStallEpisodeRun(ctx, db, ahead, 0, 30, time.Now().UTC()); err == nil || spent {
		t.Fatalf("future episode: spent=%v err=%v, want a redetect refusal", spent, err)
	}
}
