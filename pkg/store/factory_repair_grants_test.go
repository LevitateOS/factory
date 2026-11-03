package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// repairTestDigest returns a deterministic 64-hex evidence digest.
func repairTestDigest(seed byte) string {
	return strings.Repeat(string([]byte{seed}), 64)
}

// repairGrantFixture builds one grant command pair for tests.
func repairGrantFixture(id, predecessor string, revision int64, repo, sponsor int64, runs, judgments, minutes int, effect string) (factory.Command, factory.RepairGrant) {
	grant := factory.RepairGrant{
		ID: id, Repository: repo, Sponsor: sponsor, Revision: revision, Predecessor: predecessor,
		InvestigatorRuns: runs, ProposalJudgments: judgments, ActiveMinutes: minutes,
		Effect: effect, Active: true, CreatedUnix: 1000,
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		panic(err)
	}
	cmd := factory.Command{
		ID: id, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(repo),
		Principal: "soda-tester", Payload: string(raw),
	}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	return cmd, grant
}

func repairChargeFixture(id, grant, episode string, cycle int, kind, evidence string, minutes int, failed bool) factory.RepairCharge {
	return factory.RepairCharge{
		ID: id, GrantID: grant, Kind: kind, EpisodeID: episode, Cycle: cycle,
		EvidenceDigest: repairTestDigest(evidence[0]), ActiveMinutes: minutes,
		SpentUnix: 2000, Failed: failed,
	}
}

// T57 on the real store path: duplicate ticks, restarts, hash changes,
// issue replacement, graph reshuffles and retries never replenish
// room; unchanged facts spend once; at most two automatic cycles with
// materially new evidence; repository bounds hold with a separate
// sponsor scope.
func TestRepairGrantAdmissionAndNoRefillPG(t *testing.T) {
	t.Run("concurrent episodes cannot overspend one grant", func(t *testing.T) {
		ctx := context.Background()
		db := dispatchStoreFixture(t)
		now := time.Now().UTC()
		grantID := factory.NewID()
		cmd, grant := repairGrantFixture(grantID, "", 0, 991, 7, 4, 4, 120, "")
		if _, _, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, now); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 8)
		spent := make(chan bool, 8)
		var jobs sync.WaitGroup
		for i := 0; i < 8; i++ {
			jobs.Add(1)
			go func() {
				defer jobs.Done()
				<-start
				charge := repairChargeFixture(factory.NewID(), grantID, factory.NewID(), 1, factory.RepairChargeRun, "a", 1, false)
				created, err := db.AdmitStallInvestigation(ctx, 991, charge, now)
				results <- err
				spent <- created
			}()
		}
		close(start)
		jobs.Wait()
		close(results)
		close(spent)
		for err := range results {
			if err != nil && !errors.Is(err, factory.ErrRepairRunsExhausted) {
				t.Fatal(err)
			}
		}
		count := 0
		for created := range spent {
			if created {
				count++
			}
		}
		charges, err := db.RepairCharges(ctx, 991)
		if err != nil || count != 4 || len(charges) != 4 {
			t.Fatalf("concurrent grant overspent or lost charges: spent=%d retained=%d %v", count, len(charges), err)
		}
	})
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	const repo = 7
	grantID := "d0000000000000000000000000000001"
	cmd, grant := repairGrantFixture(grantID, "", 0, repo, 7, 4, 4, 120, "")
	admitted, created, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, now)
	if err != nil || !created {
		t.Fatalf("apply grant: created=%v err=%v", created, err)
	}
	if admitted.Finished == "" || admitted.Outcome != "" {
		t.Fatalf("grant retained a response or did not commit admission: %+v", admitted)
	}
	// Repeating the admitted grant reads it without recording another outcome.
	if repeated, created, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, now.Add(time.Minute)); err != nil || created || repeated.Finished != admitted.Finished || repeated.Outcome != "" {
		t.Fatalf("grant repeat: %+v created=%v err=%v", repeated, created, err)
	}
	// A revision that skips the predecessor chain is stale.
	badCmd, badGrant := repairGrantFixture("d0000000000000000000000000000002", "d0000000000000000000000000000099", 1, repo, 7, 4, 4, 120, "raise")
	if _, _, err := db.ApplyRepairGrantCommand(ctx, badCmd, badGrant, now); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale grant chain: err=%v", err)
	}
	episode := "e0000000000000000000000000000001"
	first := repairChargeFixture("c0000000000000000000000000000001", grantID, episode, 1, factory.RepairChargeRun, "a", 30, false)
	spent, err := db.AdmitStallInvestigation(ctx, repo, first, now)
	if err != nil || !spent {
		t.Fatalf("first charge: spent=%v err=%v", spent, err)
	}
	chargeRecord, err := db.FactoryCommand(ctx, first.ID)
	if err != nil || chargeRecord.Finished == "" || chargeRecord.Outcome != "" {
		t.Fatalf("charge retained a response or did not commit: %+v %v", chargeRecord, err)
	}
	// A restart replaying the same run identity spends nothing.
	if spent, err := db.AdmitStallInvestigation(ctx, repo, first, now); err != nil || spent {
		t.Fatalf("restart replay: spent=%v err=%v", spent, err)
	}
	resumed := first
	resumed.SpentUnix++
	if spent, err := db.AdmitStallInvestigation(ctx, repo, resumed, now); err != nil || spent {
		t.Fatalf("repeated charge with a later observation spent again: spent=%v err=%v", spent, err)
	}
	changedCharge := first
	changedCharge.ActiveMinutes--
	if _, err := db.AdmitStallInvestigation(ctx, repo, changedCharge, now); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("changed charge reused its identity: %v", err)
	}
	// A reshuffled second cycle over identical facts is refused.
	reshuffle := repairChargeFixture("c0000000000000000000000000000003", grantID, episode, 2, factory.RepairChargeRun, "a", 30, false)
	if _, err := db.AdmitStallInvestigation(ctx, repo, reshuffle, now); !errors.Is(err, factory.ErrRepairNeedsEvidence) {
		t.Fatalf("reshuffle without new facts: err=%v", err)
	}
	// Materially new factual evidence opens exactly one second cycle;
	// failure stays charged and counts against the bounds.
	second := repairChargeFixture("c0000000000000000000000000000004", grantID, episode, 2, factory.RepairChargeRun, "b", 30, true)
	if spent, err := db.AdmitStallInvestigation(ctx, repo, second, now); err != nil || !spent {
		t.Fatalf("evidenced second cycle: spent=%v err=%v", spent, err)
	}
	judgment := repairChargeFixture("c0000000000000000000000000000041", grantID, episode, 2, factory.RepairChargeJudgment, "b", 0, false)
	if spent, err := db.AdmitStallInvestigation(ctx, repo, judgment, now); err != nil || !spent {
		t.Fatalf("second-cycle judgment: spent=%v err=%v", spent, err)
	}
	// Two unsuccessful cycles need owner attention: no further
	// automatic charge fits the episode, whatever its evidence.
	third := repairChargeFixture("c0000000000000000000000000000005", grantID, episode, 2, factory.RepairChargeRun, "c", 30, false)
	if _, err := db.AdmitStallInvestigation(ctx, repo, third, now); !errors.Is(err, factory.ErrRepairCyclesExhausted) {
		t.Fatalf("third charge: err=%v", err)
	}
	charges, err := db.RepairCharges(ctx, repo)
	if err != nil || len(charges) != 3 {
		t.Fatalf("charges=%d err=%v, want the three spent charges", len(charges), err)
	}
	// The remaining runs fill the grant totals across episodes; the
	// fifth exceeds the bound while the exhausted replay stays free.
	episode2 := "e0000000000000000000000000000002"
	for _, charge := range []factory.RepairCharge{
		repairChargeFixture("c0000000000000000000000000000006", grantID, episode2, 1, factory.RepairChargeRun, "a", 30, false),
		repairChargeFixture("c0000000000000000000000000000007", grantID, episode2, 2, factory.RepairChargeRun, "b", 30, false),
	} {
		if spent, err := db.AdmitStallInvestigation(ctx, repo, charge, now); err != nil || !spent {
			t.Fatalf("charge %s: spent=%v err=%v", charge.ID, spent, err)
		}
	}
	fifth := repairChargeFixture("c0000000000000000000000000000042", grantID, "e0000000000000000000000000000003", 1, factory.RepairChargeRun, "a", 1, false)
	if _, err := db.AdmitStallInvestigation(ctx, repo, fifth, now); !errors.Is(err, factory.ErrRepairRunsExhausted) {
		t.Fatalf("fifth run: err=%v", err)
	}
	if spent, err := db.AdmitStallInvestigation(ctx, repo, first, now); err != nil || spent {
		t.Fatalf("exhausted replay: spent=%v err=%v", spent, err)
	}
	// Judgment calls hold their own finite bound: one per cycle across
	// episodes, four total for the grant.
	episode4 := "e0000000000000000000000000000004"
	for _, charge := range []factory.RepairCharge{
		repairChargeFixture("c0000000000000000000000000000031", grantID, episode4, 1, factory.RepairChargeJudgment, "a", 0, false),
		repairChargeFixture("c0000000000000000000000000000032", grantID, episode4, 2, factory.RepairChargeJudgment, "b", 0, false),
		repairChargeFixture("c0000000000000000000000000000033", grantID, episode2, 1, factory.RepairChargeJudgment, "a", 0, false),
	} {
		if spent, err := db.AdmitStallInvestigation(ctx, repo, charge, now); err != nil || !spent {
			t.Fatalf("judgment %s: spent=%v err=%v", charge.ID, spent, err)
		}
	}
	extra := repairChargeFixture("c0000000000000000000000000000019", grantID, episode2, 2, factory.RepairChargeJudgment, "b", 0, false)
	if _, err := db.AdmitStallInvestigation(ctx, repo, extra, now); !errors.Is(err, factory.ErrRepairJudgmentsExhausted) {
		t.Fatalf("fifth judgment: err=%v", err)
	}
}

// T57 time bound in isolation: active minutes exhaust independently of
// the run count.
func TestRepairActiveTimeBoundPG(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	const repo = 77
	grantID := "d0000000000000000000000000000011"
	cmd, grant := repairGrantFixture(grantID, "", 0, repo, 7, 4, 4, 80, "")
	if _, _, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, now); err != nil {
		t.Fatal(err)
	}
	episode := "e0000000000000000000000000000011"
	first := repairChargeFixture("c0000000000000000000000000000011", grantID, episode, 1, factory.RepairChargeRun, "a", 30, false)
	if spent, err := db.AdmitStallInvestigation(ctx, repo, first, now); err != nil || !spent {
		t.Fatalf("first run: spent=%v err=%v", spent, err)
	}
	second := repairChargeFixture("c0000000000000000000000000000014", grantID, episode, 2, factory.RepairChargeRun, "b", 30, false)
	if spent, err := db.AdmitStallInvestigation(ctx, repo, second, now); err != nil || !spent {
		t.Fatalf("second run: spent=%v err=%v", spent, err)
	}
	episode2 := "e0000000000000000000000000000015"
	over := repairChargeFixture("c0000000000000000000000000000012", grantID, episode2, 1, factory.RepairChargeRun, "a", 30, false)
	if _, err := db.AdmitStallInvestigation(ctx, repo, over, now); !errors.Is(err, factory.ErrRepairTimeExhausted) {
		t.Fatalf("over-time run: err=%v", err)
	}
	fitting := repairChargeFixture("c0000000000000000000000000000013", grantID, episode2, 1, factory.RepairChargeRun, "a", 20, false)
	if spent, err := db.AdmitStallInvestigation(ctx, repo, fitting, now); err != nil || !spent {
		t.Fatalf("fitting run: spent=%v err=%v", spent, err)
	}
}

// T57 grant increase: an explicit authorized raise opens new room while
// preserving every spent charge and the prior history.
func TestRepairGrantIncreasePreservesConsumptionPG(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	const repo = 78
	grantID := "d0000000000000000000000000000021"
	cmd, grant := repairGrantFixture(grantID, "", 0, repo, 7, 2, 1, 60, "")
	if _, _, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, now); err != nil {
		t.Fatal(err)
	}
	episode := "e0000000000000000000000000000021"
	for _, charge := range []factory.RepairCharge{
		repairChargeFixture("c0000000000000000000000000000021", grantID, episode, 1, factory.RepairChargeRun, "a", 10, false),
		repairChargeFixture("c0000000000000000000000000000022", grantID, episode, 2, factory.RepairChargeRun, "b", 10, false),
	} {
		if spent, err := db.AdmitStallInvestigation(ctx, repo, charge, now); err != nil || !spent {
			t.Fatalf("charge %s: spent=%v err=%v", charge.ID, spent, err)
		}
	}
	capped := repairChargeFixture("c0000000000000000000000000000023", grantID, "e0000000000000000000000000000025", 1, factory.RepairChargeRun, "a", 10, false)
	if _, err := db.AdmitStallInvestigation(ctx, repo, capped, now); !errors.Is(err, factory.ErrRepairRunsExhausted) {
		t.Fatalf("capped run: err=%v", err)
	}
	// An unexplained raise is refused; a stated raise preserves history.
	silentCmd, silent := repairGrantFixture("d0000000000000000000000000000022", grantID, 1, repo, 7, 4, 1, 60, "")
	if _, _, err := db.ApplyRepairGrantCommand(ctx, silentCmd, silent, now); err == nil {
		t.Fatal("silent limit raise admitted")
	}
	raisedCmd, raised := repairGrantFixture("d0000000000000000000000000000022", grantID, 1, repo, 7, 4, 1, 60, "owner authorizes two further investigator runs")
	if _, created, err := db.ApplyRepairGrantCommand(ctx, raisedCmd, raised, now); err != nil || !created {
		t.Fatalf("stated raise: created=%v err=%v", created, err)
	}
	// New charges bind the current head; the two pre-raise charges
	// stay valid history against the same scope. The refused capped
	// charge never persisted, so its identity is reusable.
	rebound := repairChargeFixture("c0000000000000000000000000000023", raisedCmd.ID, "e0000000000000000000000000000025", 1, factory.RepairChargeRun, "a", 10, false)
	if spent, err := db.AdmitStallInvestigation(ctx, repo, rebound, now); err != nil || !spent {
		t.Fatalf("post-raise charge: spent=%v err=%v", spent, err)
	}
	charges, err := db.RepairCharges(ctx, repo)
	if err != nil || len(charges) != 3 {
		t.Fatalf("charges=%d err=%v, want all three preserved", len(charges), err)
	}
}

// intentRevisionFixture builds one valid accepted revision for aggregate tests.
func intentRevisionFixture(id, predecessor, taskKey string, repo int64) factory.IntentRevision {
	task := factory.IntentTask{Key: taskKey, Objective: "Build request form", Outcomes: []string{"Submit a request"}, IdeaIDs: []string{"d333333333333333333333333"}}
	return factory.IntentRevision{
		ID: id, Intent: "d222222222222222222222222", Predecessor: predecessor,
		Owner: 1, Repository: repo, Objective: "Build a request form", State: "accepted",
		MaxIssues: 4, CreatedUnix: 1,
		Ideas: []factory.ProductIdea{{ID: "d333333333333333333333333", Description: "Request form", Consequence: "Requests are recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}},
		Tasks: []factory.IntentTask{task},
	}
}

// bumpAttemptSeconds records consumed active seconds through the
// production allowance saver, mirroring settled execution.
func bumpAttemptSeconds(t *testing.T, ctx context.Context, db *Store, repo, issue int64, seconds int64) {
	t.Helper()
	allowance, err := db.AttemptAllowance(ctx, repo, issue)
	if err != nil {
		t.Fatal(err)
	}
	allowance.ActiveSeconds = seconds
	allowance.CheckpointUnix = time.Now().Unix()
	allowance.Revision++
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// T60 on the real store path: new attempts, decomposition revisions,
// split or replacement issues and verification cannot refill stable
// intent usage; an explicit increase retains spent time.
func TestIntentExecutionAggregatePG(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	const repo = 7
	rev1 := intentRevisionFixture("d111111111111111111111111", "", "d444444444444444444444444", repo)
	if err := db.SaveIntentRevision(ctx, rev1); err != nil {
		t.Fatalf("save rev1: %v", err)
	}
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatalf("packet issue 3: %v", err)
	}
	bumpAttemptSeconds(t, ctx, db, repo, 3, 300)
	if err := db.BindIntentTaskAcceptance(ctx, factory.IntentTaskBinding{
		RevisionID: rev1.ID, TaskKey: rev1.Tasks[0].Key, IssueID: 303, IssueIndex: 3,
		AcceptanceID: "d555555555555555555555555",
	}); err != nil {
		t.Fatalf("bind issue 3: %v", err)
	}
	aggregate, err := db.IntentExecutionAggregateForIntent(ctx, rev1.Intent, 1)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if aggregate.ConsumedMs != 300*1000 || len(aggregate.Roots) != 1 || len(aggregate.Issues) != 1 {
		t.Fatalf("aggregate=%+v, want the 300s origin root charged once", aggregate)
	}
	// A decomposition revision keeps the origin aggregate: the revision
	// binds a split issue whose fresh row adds nothing and resets nothing.
	rev2 := intentRevisionFixture("d666666666666666666666666", rev1.ID, "d444444444444444444444444", repo)
	if err := db.SaveIntentRevision(ctx, rev2); err != nil {
		t.Fatalf("save rev2: %v", err)
	}
	if err := db.BindIntentTaskAcceptance(ctx, factory.IntentTaskBinding{
		RevisionID: rev2.ID, TaskKey: rev2.Tasks[0].Key, IssueID: 404, IssueIndex: 4,
		AcceptanceID: "d777777777777777777777777",
	}); err != nil {
		t.Fatalf("bind split issue 4: %v", err)
	}
	aggregate, err = db.IntentExecutionAggregateForIntent(ctx, rev1.Intent, 1)
	if err != nil {
		t.Fatalf("aggregate after split: %v", err)
	}
	if aggregate.ConsumedMs != 300*1000 {
		t.Fatalf("split refilled or dropped usage: %+v", aggregate)
	}
	if len(aggregate.Issues) != 2 {
		t.Fatalf("aggregate issues=%v, want both bound issues", aggregate.Issues)
	}
	// Verification and its distinct delivery-repair root carry issue zero, but
	// their spent time still belongs to the same stable origin intent.
	delivery := factory.DeliverySubject{DeliveryID: factory.NewID(), IntentID: rev1.Intent, AcceptedRevision: rev2.ID, ScopeDigest: rev2.Digest(), SourceCommit: a.SourceCommit, PreparationDigest: strings.Repeat("a", 64), VerificationManifestDigest: strings.Repeat("b", 64)}
	verificationRun := factory.NewID()
	for i, seconds := range []int64{30, 60} {
		root := a
		root.ID, root.Run, root.Issue, root.Acceptance = factory.NewID(), verificationRun, 0, ""
		root.AttemptRoot, root.ParentAssignment, root.PublicationAssignment = root.ID, "", ""
		root.RunHistory = []string{root.Run}
		root.Phase, root.Role = "verify", "soda-reviewer"
		root.Subject = factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &delivery}
		allowance := factory.AttemptAllowance{Limits: factory.AttemptLimits{ActiveMinutes: 30}, RootAssignment: root.ID, Repository: repo, Delivery: &delivery, ActiveSeconds: seconds, CheckpointUnix: now.Unix(), Closed: true}
		if i == 1 {
			root.ID = factory.DeliveryRepairCycleID(verificationRun)
			root.AttemptRoot, root.Run, root.Phase, root.Role = root.ID, factory.NewID(), "repair", "soda-coder"
			root.RunHistory = []string{root.Run}
			subject := factory.DeliveryRepairSubject{DeliveryID: delivery.DeliveryID, FailedVerificationRunID: verificationRun, FailureDigest: strings.Repeat("c", 64), InventoryDigest: strings.Repeat("d", 64), CycleID: root.ID}
			root.Subject = factory.ExecutionSubject{Kind: factory.SubjectDeliveryRepair, DeliveryRepair: &subject}
			allowance.RootAssignment, allowance.Delivery, allowance.DeliveryRepair = root.ID, nil, &subject
		}
		if err := root.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := allowance.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(root)
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,0,$2,'assigned',0,$3)`, root.ID, root.Run, string(encoded)); err != nil {
			t.Fatal(err)
		}
		encoded, _ = json.Marshal(allowance)
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES(7,0,$1,0,$2)`, root.ID, string(encoded)); err != nil {
			t.Fatal(err)
		}
	}
	aggregate, err = db.IntentExecutionAggregateForIntent(ctx, rev1.Intent, 1)
	if err != nil || aggregate.ConsumedMs != 390*1000 || len(aggregate.Roots) != 3 {
		t.Fatalf("delivery/repair reset origin usage: %+v %v", aggregate, err)
	}
	// The aggregate bound records beside funding; an explicit increase
	// retains spent time instead of refilling.
	limit := factory.DefaultIntentExecutionAllowance()
	limit.IntentID, limit.OwnerID, limit.RepositoryID = rev1.Intent, 1, repo
	limit.CheckpointUnix = now.Unix()
	raw, err := json.Marshal(limit)
	if err != nil {
		t.Fatal(err)
	}
	limitCmd := factory.Command{
		ID: factory.NewID(), Type: factory.CommandIntentExecutionLimit,
		Target:    factory.IntentExecutionTarget(rev1.Intent),
		Principal: "soda-tester", Payload: string(raw),
	}
	limitCmd.Digest = factory.SettingsDigest(limitCmd.Type, limitCmd.Target, limitCmd.Payload)
	admitted, created, err := db.ApplyIntentExecutionLimitCommand(ctx, limitCmd, limit, now)
	if err != nil || !created {
		t.Fatalf("apply limit: created=%v err=%v", created, err)
	}
	if admitted.Finished == "" || admitted.Outcome != "" {
		t.Fatalf("execution limit retained a response or did not commit admission: %+v", admitted)
	}
	if repeated, created, err := db.ApplyIntentExecutionLimitCommand(ctx, limitCmd, limit, now.Add(time.Minute)); err != nil || created || repeated.Finished != admitted.Finished || repeated.Outcome != "" {
		t.Fatalf("execution limit repeat: %+v created=%v err=%v", repeated, created, err)
	}
	raised := limit
	raised.Revision = 1
	raised.Predecessor = limitCmd.ID
	next, err := raised.ApplyIncrease(raised.ActiveLimitMs + 120*60*1000)
	if err != nil {
		t.Fatalf("domain increase: %v", err)
	}
	raised = next
	raw, err = json.Marshal(raised)
	if err != nil {
		t.Fatal(err)
	}
	raisedCmd := factory.Command{
		ID: factory.NewID(), Type: factory.CommandIntentExecutionLimit,
		Target:    factory.IntentExecutionTarget(rev1.Intent),
		Principal: "soda-tester", Payload: string(raw),
	}
	raisedCmd.Digest = factory.SettingsDigest(raisedCmd.Type, raisedCmd.Target, raisedCmd.Payload)
	if _, created, err := db.ApplyIntentExecutionLimitCommand(ctx, raisedCmd, raised, now); err != nil || !created {
		t.Fatalf("raise limit: created=%v err=%v", created, err)
	}
	aggregate, err = db.IntentExecutionAggregateForIntent(ctx, rev1.Intent, 1)
	if err != nil {
		t.Fatalf("aggregate after raise: %v", err)
	}
	if aggregate.ConsumedMs != 390*1000 || aggregate.LimitMs != raised.ActiveLimitMs {
		t.Fatalf("raise reset usage: %+v", aggregate)
	}
	// A second failed verification at another accepted revision has the same
	// origin repair ceiling. Retained roots count even before an allowance is
	// installed; replay excludes only its own exact run, never a new cause.
	secondDelivery := delivery
	secondDelivery.DeliveryID, secondDelivery.AcceptedRevision, secondDelivery.ScopeDigest = factory.NewID(), rev1.ID, rev1.Digest()
	secondFailedRun, secondRepairRun := factory.NewID(), factory.NewID()
	for i := 0; i < 2; i++ {
		root := a
		root.ID, root.Run, root.Issue, root.Acceptance = factory.NewID(), secondFailedRun, 0, ""
		root.AttemptRoot, root.ParentAssignment, root.PublicationAssignment = root.ID, "", ""
		root.Phase, root.Role = "verify", "soda-reviewer"
		root.Subject = factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &secondDelivery}
		if i == 1 {
			root.ID, root.Run, root.Phase, root.Role = factory.DeliveryRepairCycleID(secondFailedRun), secondRepairRun, "repair", "soda-coder"
			root.AttemptRoot = root.ID
			subject := factory.DeliveryRepairSubject{DeliveryID: secondDelivery.DeliveryID, FailedVerificationRunID: secondFailedRun, FailureDigest: strings.Repeat("e", 64), InventoryDigest: strings.Repeat("f", 64), CycleID: root.ID}
			root.Subject = factory.ExecutionSubject{Kind: factory.SubjectDeliveryRepair, DeliveryRepair: &subject}
		}
		root.RunHistory = []string{root.Run}
		if err := root.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(root)
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,0,$2,'assigned',0,$3)`, root.ID, root.Run, string(encoded)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	capRecord := DeliveryRepairRecord{Cause: factory.DeliveryRepairCause{Repository: repo, Delivery: secondDelivery}}
	if err := checkDeliveryRepairRunsTx(ctx, tx, capRecord, factory.NewID()); !errors.Is(err, factory.ErrRepairRunsExhausted) {
		t.Fatalf("third repair across failed runs/revisions: %v", err)
	}
	if err := checkDeliveryRepairRunsTx(ctx, tx, capRecord, secondRepairRun); err != nil {
		t.Fatalf("exact retained repair replay was charged again: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// A shrinking bound is refused.
	shrunk := raised
	shrunk.Revision = 2
	shrunk.Predecessor = raisedCmd.ID
	shrunk.ActiveLimitMs = limit.ActiveLimitMs
	raw, err = json.Marshal(shrunk)
	if err != nil {
		t.Fatal(err)
	}
	shrunkCmd := factory.Command{
		ID: factory.NewID(), Type: factory.CommandIntentExecutionLimit,
		Target:    factory.IntentExecutionTarget(rev1.Intent),
		Principal: "soda-tester", Payload: string(raw),
	}
	shrunkCmd.Digest = factory.SettingsDigest(shrunkCmd.Type, shrunkCmd.Target, shrunkCmd.Payload)
	if _, _, err := db.ApplyIntentExecutionLimitCommand(ctx, shrunkCmd, shrunk, now); err == nil {
		t.Fatal("shrinking aggregate bound admitted")
	}
}

// TestAllIssueControlsOverflowFailsClosed proves the inventory bound:
// a set past the limit returns no controls and reports incomplete, so
// no caller can prove deadlock from a truncated set. Bounded cost is
// preserved by the limit+1 probe row.
func TestAllIssueControlsOverflowFailsClosed(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	for issue := int64(1); issue <= 3; issue++ {
		control := factory.IssueControl{
			Repository: 31, Issue: issue,
			Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
			Fingerprint: repairTestDigest(byte('0' + issue)), Authority: repairTestDigest(byte('a' + issue)),
		}
		if _, _, err := db.RecordIssueAssessment(ctx, control, now); err != nil {
			t.Fatalf("seed control %d: %v", issue, err)
		}
	}
	if _, _, err := db.AllIssueControls(ctx, 0); err == nil {
		t.Fatal("zero inventory bound admitted")
	}
	controls, complete, err := db.AllIssueControls(ctx, 2)
	if err != nil || complete || controls != nil {
		t.Fatalf("overflow: complete=%v controls=%d err=%v, want incomplete with no set", complete, len(controls), err)
	}
	controls, complete, err = db.AllIssueControls(ctx, 3)
	if err != nil || !complete || len(controls) != 3 {
		t.Fatalf("exact bound: complete=%v controls=%d err=%v", complete, len(controls), err)
	}
}
