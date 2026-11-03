package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/factory/execution"

	sdk "forgejo.org/extension-sdk"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// TestServeStallRoundAdmitsBoundedEpisode proves the R36 production
// joint on the real store path: a no-launch dispatch pass runs
// detection at the standing generation, persists the bound episode
// durably, and admits exactly one cycle-1 charge under the explicit
// grant. A redetected round replays without spending; a pass that
// launched work skips detection entirely.
func TestServeStallRoundAdmitsBoundedEpisode(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 44
	grantID := "d1000000000000000000000000000044"
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

	round, err := coord.ServeStallRound(ctx, DispatchReport{})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if len(round.Repositories) != 1 || round.Repositories[0].Kind != StallRepositoryStall {
		t.Fatalf("round=%+v, want one bound stall repository", round)
	}
	standing, err := db.StandingStallEvents(ctx)
	if err != nil {
		t.Fatalf("standing: %v", err)
	}
	entry, ok := standing[repo]
	if !ok || entry.Generation != 0 || entry.EventID == "" || entry.Digest == "" {
		t.Fatalf("standing=%+v, want the durable generation-0 observation", standing)
	}
	if charges, err := db.RepairCharges(ctx, repo); err != nil || len(charges) != 1 {
		t.Fatalf("charges=%d err=%v, want exactly one admitted cycle", len(charges), err)
	}

	again, err := coord.ServeStallRound(ctx, DispatchReport{})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if len(again.Repositories) != 1 {
		t.Fatalf("round=%+v, want the replayed episode", again)
	}
	if charges, err := db.RepairCharges(ctx, repo); err != nil || len(charges) != 1 {
		t.Fatalf("charges=%d err=%v, want redetection to replay without spending", len(charges), err)
	}

	skipped, err := coord.ServeStallRound(ctx, DispatchReport{
		Launched: []DispatchLaunch{{Repository: repo, Issue: 9}},
	})
	if err != nil {
		t.Fatalf("skip: %v", err)
	}
	if len(skipped.Repositories) != 0 {
		t.Fatalf("round=%+v, want no detection when the pass launched work", skipped)
	}
	t.Run("retained Strategist launch and required context", func(t *testing.T) {
		serving, dsn := dispatchTestDB(t)
		fx := dispatchSeed(t, serving)
		fx.accept(t, 3, "d333333333333333333333333")
		stallSeedControl(t, serving, fx.repo, 3, factory.ReadinessBlocked, factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0))
		grant := factory.DefaultRepairGrant()
		grant.ID, grant.Repository, grant.Sponsor, grant.Active, grant.CreatedUnix = factory.NewID(), fx.repo, 7, true, time.Now().Unix()
		encoded, _ := json.Marshal(grant)
		command := factory.Command{ID: grant.ID, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(fx.repo), Principal: "soda-tester", Payload: string(encoded)}
		command.Digest = factory.SettingsDigest(command.Type, command.Target, command.Payload)
		if _, created, err := serving.ApplyRepairGrantCommand(ctx, command, grant, time.Now()); err != nil || !created {
			t.Fatal("repair grant", err)
		}
		enumerator := &fixtureEnumerator{revision: 41, idle: true, pages: map[string]NativeInventoryPage{"": {Issues: []NativeInventoryIssue{inventoryIssue(t, 3)}, Complete: true}}}
		worker := &Coordinator{Store: serving, Host: fx.host, Broker: fx.broker, DispatchReads: fx.reads, CompletionEnumerator: enumerator, CompletionLocalCapture: serving.CaptureCompletionLocal, MCPKey: []byte(strings.Repeat("k", 32)), Priority: &PriorityJudgments{Journal: serving, Chooser: &bundleGapChooser{pick: factory.ContextBundleCurrent}}}
		first, err := worker.ServeStallRound(ctx, DispatchReport{})
		if err != nil || len(first.Repositories) != 1 || first.Repositories[0].Event == nil {
			t.Fatal("bound repair episode", err)
		}
		event := *first.Repositories[0].Event
		record, err := serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil || len(record.Repair.Cycles) != 1 || record.Repair.Cycles[0].State != factory.RepairReady {
			t.Fatalf("repair input not sealed: %+v %v", record.Repair, err)
		}
		if _, err = worker.ServeStallRound(ctx, DispatchReport{}); err != nil {
			t.Fatal(err)
		}
		record, err = serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil || record.Repair.Cycles[0].State != factory.RepairInvestigating {
			t.Fatalf("repair did not retain sole investigator: state=%s reason=%s %v", record.Repair.Cycles[0].State, record.Repair.Cycles[0].Reason, err)
		}
		cycle := record.Repair.Cycles[0]
		if len(fx.host.launches) != 1 || fx.host.launches[0].Run.ID != cycle.RunID || fx.host.launches[0].Run.ExecutionPhase != project.ExecutionRepair || fx.host.launches[0].MCP == nil {
			t.Fatalf("repair native launch missing or unbound: launches=%d cycle=%+v", len(fx.host.launches), cycle)
		}
		assignment, err := serving.Assignment(ctx, cycle.AssignmentID)
		if err != nil || assignment.Subject.Stall == nil || assignment.Subject.Stall.CycleID != cycle.ID || assignment.Run != cycle.RunID {
			t.Fatal("repair assignment lost cycle", err)
		}
		capability, err := serving.GetMCPCapability(ctx, cycle.RunID)
		if err != nil || capability.Profile != factory.MCPProfileStrategist {
			t.Fatal("repair MCP profile", err)
		}
		current, err := serving.FactoryCurrentContext(ctx, capability, time.Now())
		if err != nil || current.Evidence.Availability != "complete" || current.Role.Repair == nil || !current.Role.Repair.Complete || current.Role.Repair.InventoryDigest != cycle.InventoryDigest || current.Allowance == nil || current.Allowance.MaxChildren != 0 {
			t.Fatalf("repair current input incomplete: %+v %v", current.Evidence, err)
		}
		if current.Role.Repair.Budget == nil || current.Role.Repair.Budget.RunsRemaining != 3 || current.Role.Repair.Budget.ActiveMinutesRemaining != 90 || current.Role.Repair.Budget.CyclesRemaining != 1 || current.Role.Repair.State != factory.RepairInvestigating {
			t.Fatal("repair current context lost canonical consumed grant budget")
		}
		for i := 0; i < 2; i++ {
			if _, err = worker.ServeStallRound(ctx, DispatchReport{}); err != nil {
				t.Fatal(err)
			}
		}
		charges, err := serving.RepairCharges(ctx, fx.repo)
		if err != nil || len(charges) != 1 || len(fx.host.launches) != 1 {
			t.Fatalf("redetection refilled repair: charges=%d launches=%d %v", len(charges), len(fx.host.launches), err)
		}
		run, err := serving.FactoryRun(ctx, cycle.RunID)
		if err != nil {
			t.Fatal(err)
		}
		run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "repair no-change result", true
		if err = serving.SaveFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		proposal := factory.StallRepairReport{StallID: record.ID, CycleID: cycle.ID, InventoryDigest: cycle.InventoryDigest, SourceCommit: cycle.Local.SourceCommit, PreparationDigest: cycle.Local.PreparationDigest, Causes: []factory.CheckFailure{{CheckRef: "dependency_cycle", Detail: "The recorded dependency cycle still has no viable producer."}}, RetainedIssueIndexes: []int64{3}, Outcome: factory.RepairOutcomeNoChange}
		output := phaseFinish(t, factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionRepair, Status: "completed", Summary: "No graph change proposed; the real stall remains.", Output: &factory.PhaseOutput{Repair: &proposal}})
		if a, ok := accountRunFinish(t, ctx, serving, run, output, time.Now()); !ok || a.Outcome != factory.Succeeded {
			t.Fatalf("repair typed finish refused: %s %s %v", a.Outcome, a.Reason, ok)
		}
		for i := 0; i < 3; i++ {
			_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		}
		record, err = serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil || len(record.Repair.Cycles) != 1 || record.Repair.Cycles[0].State != factory.RepairAttention {
			t.Fatalf("no-change cleared or refilled real stall: cycles=%d state=%s %v", len(record.Repair.Cycles), record.Repair.Cycles[0].State, err)
		}
		// A native revision or replaced issue identity alone does not supply facts.
		enumerator.revision++
		_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		if charges, _ = serving.RepairCharges(ctx, fx.repo); len(charges) != 1 {
			t.Fatal("native revision refilled repair")
		}
		changed := factory.CompletionIssueEvidence{Index: 3, Title: "New manual factual evidence", Body: "The missing producer can now be traced to a distinct external contract.", TitleDigest: evidenceDigest("New manual factual evidence"), ContentDigest: evidenceDigest("The missing producer can now be traced to a distinct external contract.")}
		body, _ := changed.CanonicalData()
		enumerator.revision++
		enumerator.pages[""] = NativeInventoryPage{Issues: []NativeInventoryIssue{{Index: 3, Data: body}}, Complete: true}
		_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		record, err = serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil || len(record.Repair.Cycles) != 2 || record.Repair.Cycles[1].State != factory.RepairReady {
			t.Fatalf("material new evidence did not admit bounded next cycle: cycles=%d %v", len(record.Repair.Cycles), err)
		}
		if charges, _ = serving.RepairCharges(ctx, fx.repo); len(charges) != 2 {
			t.Fatal("new evidence did not retain cumulative charges")
		}
		_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		record, err = serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil {
			t.Fatal(err)
		}
		second := record.Repair.Cycles[1]
		if second.State != factory.RepairInvestigating || len(fx.host.launches) != 2 {
			t.Fatalf("second bounded cycle did not launch: %s %s", second.State, second.Reason)
		}
		run, err = serving.FactoryRun(ctx, second.RunID)
		if err != nil {
			t.Fatal(err)
		}
		// A structured terminal may be fenced before native retirement.
		// Its exact typed root closes admission but keeps its active clock
		// until SaveFactoryRun records the actual stopped worker.
		secondCapability, err := serving.GetMCPCapability(ctx, second.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if err = serving.ActivateObservedMCPCapability(ctx, secondCapability); err != nil {
			t.Fatal(err)
		}
		secondCapability, err = serving.GetMCPCapability(ctx, second.RunID)
		if err != nil {
			t.Fatal(err)
		}
		invalidReport, _ := json.Marshal(map[string]any{"subject": secondCapability.Subject, "report": map[string]any{}})
		submission := testMCPSubmission(secondCapability, factory.MCPToolFinishReassessment, factory.NewID(), invalidReport)
		if _, err = serving.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatal(err)
		}
		if finished, err := SettleMCPTerminalProposal(ctx, serving, second.AssignmentID, time.Now()); err != nil || !finished {
			t.Fatal("early fenced Strategist terminal", finished, err)
		}
		sqlDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		var beforeAllowance, afterAllowance factory.AttemptAllowance
		var allowanceRaw []byte
		if err = sqlDB.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, second.AssignmentID).Scan(&allowanceRaw); err != nil || json.Unmarshal(allowanceRaw, &beforeAllowance) != nil || !beforeAllowance.Active || !beforeAllowance.Closed {
			t.Fatal("fenced root must retain active clock before native retirement", err)
		}
		run.Outcome, run.Reconciled = factory.Succeeded, true
		if err = serving.SaveFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err = sqlDB.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, second.AssignmentID).Scan(&allowanceRaw); err != nil || json.Unmarshal(allowanceRaw, &afterAllowance) != nil || afterAllowance.Active || !afterAllowance.Closed || afterAllowance.Limits != beforeAllowance.Limits || afterAllowance.ActiveSeconds < beforeAllowance.ActiveSeconds {
			t.Fatal("retired typed root did not freeze the same cumulative allowance", err)
		}
		if _, created := AccountSettledRun(ctx, serving, run, time.Now()); created {
			t.Fatal("retirement replaced an already fenced terminal")
		}
		finishedSecond, err := serving.Assignment(ctx, second.AssignmentID)
		if err != nil || finishedSecond.Stage != factory.AssignmentFinished || finishedSecond.Outcome != factory.NeedsHuman {
			t.Fatal("original fenced terminal changed after native settlement", err)
		}
		for i := 0; i < 3; i++ {
			_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		}
		changed.Body += " A further external fact cannot buy a third automatic run."
		changed.ContentDigest = evidenceDigest(changed.Body)
		body, _ = changed.CanonicalData()
		enumerator.revision++
		enumerator.pages[""] = NativeInventoryPage{Issues: []NativeInventoryIssue{{Index: 3, Data: body}}, Complete: true}
		_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		record, err = serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil || len(record.Repair.Cycles) != 2 || record.Repair.Cycles[1].State != factory.RepairAttention {
			t.Fatal("two unsuccessful cycles did not retain owner attention", err)
		}
		if charges, _ = serving.RepairCharges(ctx, fx.repo); len(charges) != 2 || len(fx.host.launches) != 2 {
			t.Fatal("new facts refilled exhausted automatic cycles")
		}
		ready, err := serving.IssueControl(ctx, fx.repo, 3)
		if err != nil {
			t.Fatal(err)
		}
		ready.Readiness, ready.Reason, ready.Blockers, ready.Fingerprint = factory.ReadinessQueued, factory.ReasonEligible, nil, strings.Repeat("9", 64)
		if _, _, err = serving.RecordIssueAssessment(ctx, ready, time.Now()); err != nil {
			t.Fatal(err)
		}
		_, _ = worker.ServeStallRound(ctx, DispatchReport{})
		record, err = serving.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		if err != nil || record.Repair.Cycles[1].State != factory.RepairStale || len(fx.host.launches) != 2 {
			t.Fatal("ready work did not retire obsolete diagnosis", err)
		}
	})
	t.Run("protected repair adoption recovers native lost reply", stallRepairGraphIntegration)
}

func stallRepairGraphIntegration(t *testing.T) {
	for _, mode := range []string{"lost_reply", "lost_reply_manifest", "grant_hold_new_effect", "grant_withdrawn_after_admission", "append_producer", "delivery_repair", "delivery_repair_tight", "delivery_repair_incomplete", "delivery_repair_wrong_failure", "delivery_repair_bad_input", "delivery_repair_nochange", "delivery_repair_withdrawn", "delivery_repair_grant_hold_new_effect", "delivery_repair_grant_withdrawn_after_admission"} {
		t.Run(mode, func(t *testing.T) { stallRepairGraphMode(t, mode) })
	}
}

func stallRepairGraphMode(t *testing.T, mode string) {
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	deliveryMode := strings.HasPrefix(mode, "delivery_repair")
	fx := dispatchSeed(t, db)
	graph := &generatedGraphFixture{revision: 41, actor: 91, owner: true, records: map[string]sdk.OperationRecord{}, issues: map[int64]sdk.NativeSnapshot{}}
	coord := NewCoordinator(db, fx.host, fx.broker)
	coord.DispatchReads = fx.reads
	coord.Graph, coord.GraphActorID, coord.AcceptanceReads = graph, graph.actor, graph
	revision := broadJoinAcceptedRevision()
	revision.Repository, revision.Tasks, revision.Ideas = fx.repo, revision.Tasks[:2], revision.Ideas[:1]
	revision.EffectiveOutcomes = []string{revision.Ideas[0].ID}
	if mode == "delivery_repair" {
		// The canonical request owner is independent from native execution
		// custody and its native:7 command principal, including full BIGINT keys.
		revision.Owner = 9007199254740993
		for index := range revision.Ideas {
			revision.Ideas[index].Actor = revision.Owner
		}
	}
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if generation, err := generateChoiceMechanics(t, coord, ctx, revision.Intent, revision.Owner, chosenFor(revision)); err != nil || generation.State != "completed" {
		t.Fatalf("initial accepted graph: state=%s %v", generation.State, err)
	}
	initial, err := db.IntentTaskBindings(ctx, revision.ID)
	if err != nil || len(initial) != 2 {
		t.Fatal("initial bindings", err)
	}
	var adoptedManifest *factory.AdoptedVerificationManifest
	if mode == "lost_reply_manifest" {
		// This retained adopted snapshot isolates protected-delta inheritance;
		// the actual Product Lead/catalog producer has its separate integration.
		prep, e := db.Preparation(ctx, "f222222222222222222222222")
		if e != nil {
			t.Fatal(e)
		}
		p := prep.Preparation
		m := factory.AdoptedVerificationManifest{Intent: revision.Intent, AcceptedRevision: revision.ID, ScopeDigest: revision.Digest(), Catalog: factory.ApprovedProjectCheckCatalog{Project: p.Project, Preparation: p.ID, Requirements: p.Requirements.ID, Approval: p.Approval.ID, SourceCommit: p.SourceCommit, PreparationDigest: factory.DeliveryPreparationDigest(p), CheckDigest: strings.Repeat("7", 64)}, Proposal: factory.VerificationManifestProposal{Outcomes: revision.EffectiveOutcomes, Checks: []factory.ManifestCheckRef{{Outcome: revision.Ideas[0].ID, CatalogID: factory.ApprovedAppCheckID, Artifacts: []string{factory.ProjectCheckReceiptArtifact}, Fixtures: []string{factory.ProjectCheckPreparedFixture}}}}}
		if e = m.ValidateFor(revision); e != nil {
			t.Fatal(e)
		}
		g, e := db.IntentGeneration(ctx, revision.ID)
		if e != nil {
			t.Fatal(e)
		}
		g.VerificationManifest = &m
		g.VerificationManifestDigest = m.Digest()
		body, _ := json.Marshal(g)
		sqlDB, e := sql.Open("pgx", dsn)
		if e != nil {
			t.Fatal(e)
		}
		defer sqlDB.Close()
		if _, e = sqlDB.Exec(`UPDATE factory_intent_generations SET data=$1 WHERE revision=$2`, string(body), revision.ID); e != nil {
			t.Fatal(e)
		}
		adoptedManifest = &m
	}
	graph.issues[2].Issue.Content += "\nRetained human note."
	graph.issues[2].Issue.ContentDigest = evidenceDigest(graph.issues[2].Issue.Content)
	graph.issues[2].Issue.ContentVersion++
	graph.revision++
	protectedBody := graph.issues[2].Issue.Content
	protectedHeads := map[int64]string{}
	var protectedDependencies []byte
	if mode == "append_producer" {
		manual := &sdk.SnapshotIssue{ID: "103", Index: "3", Title: "Supply the requested fixture", Content: "A maintainer must accept the exact fixture result.", Visible: true, Complete: true, Provenance: sdk.SnapshotCreationProvenance{PosterID: "8"}}
		manual.TitleDigest, manual.ContentDigest = evidenceDigest(manual.Title), evidenceDigest(manual.Content)
		graph.issues[3] = sdk.NativeSnapshot{Issue: manual, Dependencies: &sdk.SnapshotDependencyPage{IssueID: "103", Complete: true, Items: []sdk.SnapshotDependency{}}}
		// These ordinary human edges and their readopted result requirements
		// protect both existing consumers. The missing manual producer has no
		// factory acceptance; adoption may add work but cannot claim its result.
		for _, binding := range initial {
			index := binding.IssueIndex
			endpoint, native := int64(2), "102"
			if index == 2 {
				endpoint, native = 3, "103"
			}
			snapshot := graph.issues[index]
			snapshot.Dependencies.Items = []sdk.SnapshotDependency{{OccurrenceID: "801", IssueID: snapshot.Issue.ID, DependencyID: native, Visible: true, Complete: true}}
			snapshot.Dependencies.Total = 1
			graph.issues[index] = snapshot
			decision, err := db.AcceptanceDecision(ctx, binding.AcceptanceID)
			if err != nil {
				t.Fatal(err)
			}
			decision.ID, decision.Predecessor = generatedDecisionID("human-result", revision.ID, binding.TaskKey), decision.ID
			decision.NativeRev, decision.ContentDigest, decision.ContentVersion = graph.revision, snapshot.Issue.ContentDigest, snapshot.Issue.ContentVersion
			decision.Prerequisites = []factory.AcceptedPrerequisite{{Occurrence: "801", DependsOn: native, EndpointRepo: fx.repo, EndpointIssue: endpoint, Outcome: factory.PrereqResult}}
			if _, err = coord.AdmitAcceptance(ctx, decision, func(ctx context.Context) error { return ctx.Err() }); err != nil {
				t.Fatal("human result requirement", err)
			}
			protectedHeads[index] = decision.ID
		}
		protectedDependencies, _ = json.Marshal([]*sdk.SnapshotDependencyPage{graph.issues[1].Dependencies, graph.issues[2].Dependencies, graph.issues[3].Dependencies})
	}
	baseline := graph.submissions
	coord.Graph = &reassessmentLostReplyGraph{generatedGraphFixture: graph, loseReply: true}
	enumerator := reassessmentGraphInventory{graph: graph}
	coord.CompletionEnumerator, coord.CompletionLocalCapture = enumerator, db.CaptureCompletionLocal
	chooser := &reassessmentLifecycleChooser{}
	coord.Planning = &PlanningJudgments{Chooser: chooser}
	coord.MCPKey = []byte(strings.Repeat("k", 32))
	coord.Priority = &PriorityJudgments{Journal: db, Chooser: &bundleGapChooser{pick: factory.ContextBundleCurrent}}
	grant := factory.DefaultRepairGrant()
	if mode == "delivery_repair_tight" {
		grant.ActiveMinutes = 2
	}
	grant.ID, grant.Repository, grant.Sponsor, grant.Active, grant.CreatedUnix = factory.NewID(), fx.repo, 7, true, time.Now().Unix()
	raw, _ := json.Marshal(grant)
	command := factory.Command{ID: grant.ID, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(fx.repo), Principal: "soda-tester", Payload: string(raw)}
	command.Digest = factory.SettingsDigest(command.Type, command.Target, command.Payload)
	if _, _, err = db.ApplyRepairGrantCommand(ctx, command, grant, time.Now()); err != nil {
		t.Fatal(err)
	}
	var deliveryRecord store.DeliveryRepairRecord
	var failedDeliveryRun string
	var ownerRequest factory.DeliveryRepairResult
	var failedVerifierJSON []byte
	var failedLedger string
	var deliverySQL *sql.DB
	var failedVerifierAssignment string
	if deliveryMode {
		// The generated graph above is the ordinary accepted native graph.
		// Its completed tasks provide this fixture's delivered prerequisites;
		// the new verifier/repair below both use real packet and MCP owners.
		sqlDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		deliverySQL = sqlDB
		for _, binding := range initial {
			event := factory.CompletionEvent{ID: factory.NewID(), Repository: fx.repo, Issue: binding.IssueIndex, Generation: binding.IssueIndex, PublicationID: factory.NewID(), AcceptanceID: binding.AcceptanceID, MergedCommit: fx.tip, ClosingOccurrence: "1"}
			body, _ := json.Marshal(event)
			if _, err = sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES($1,$2,$3,$4,'1',$5)`, fx.repo, event.Generation, event.ID, event.PublicationID, string(body)); err != nil {
				t.Fatal(err)
			}
		}
		sponsor, err := db.Sponsorship(ctx, fx.repo, "conn")
		if err != nil {
			t.Fatal(err)
		}
		sponsor.Roles = []string{project.RoleCoder, project.RoleReviewer}
		if err = db.SaveSponsorship(ctx, sponsor); err != nil {
			t.Fatal(err)
		}
		manifest := factory.ApprovedAppCheck{IntentID: revision.Intent, AcceptedRevision: revision.ID, Outcomes: []string{revision.Ideas[0].ID}}
		encoded, _ := json.Marshal(manifest)
		script := []byte("#!/bin/sh\n" + factory.ApprovedAppCheckPrefix + string(encoded) + "\nexit 0\n")
		zero := 0
		fx.host.inspectPreparation = func(in project.PrepareInspect) (execution.PreparationState, error) {
			p, e := db.Preparation(ctx, in.ID)
			if e != nil {
				return execution.PreparationState{}, e
			}
			return execution.PreparationState{Container: strings.Repeat("c", 64), ID: in.ID, Project: fx.proj, Role: p.Preparation.Role, SourceCommit: fx.tip, SetupDigest: p.Preparation.SetupDigest, Phase: project.PrepareReady, Ready: true, SetupExit: &zero, CheckExit: &zero, Output: "native application assertions passed"}, nil
		}
		for _, id := range []string{"f111111111111111111111111", "f222222222222222222222222"} {
			state, e := fx.host.inspectPreparation(project.PrepareInspect{Project: fx.proj, ID: id})
			if e != nil {
				t.Fatal(e)
			}
			stored, e := db.Preparation(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			stored.State = state
			if e = db.ObservePreparation(ctx, stored); e != nil {
				t.Fatal(e)
			}
		}
		fx.host.contextRead = func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
			m := fixturePreparationContext(in, role)
			m.Check = script
			return m, nil
		}
		coord.DispatchReads = &appVerificationReads{tip: fx.tip}
		subject, preparation, prompt, e := coord.deliveryAdmission(ctx, store.DeliveredIntentCandidate{Revision: revision, FirstIssue: initial[0].IssueIndex})
		if e != nil {
			t.Fatal("failed-verifier admission inputs", e)
		}
		deps := coord.dispatchDeps()
		occupancy, e := snapshotOccupancy(ctx, db)
		if e != nil {
			t.Fatal(e)
		}
		report := newDispatchReport()
		DispatchTimeOnlyRoot(ctx, deps, &occupancy, fx.repo, subject, fx.tip, prompt, preparation, &report)
		if len(report.Launched) != 1 {
			t.Fatalf("real verification packet: %+v", report)
		}
		a, e := db.Assignment(ctx, report.Launched[0].AssignmentID)
		if e != nil {
			t.Fatal(e)
		}
		failedDeliveryRun = a.Run
		failedVerifierAssignment = a.ID
		capability, e := db.GetMCPCapability(ctx, a.Run)
		if e != nil {
			t.Fatal(e)
		}
		if e = db.ActivateObservedMCPCapability(ctx, capability); e != nil {
			t.Fatal(e)
		}
		capability, e = db.GetMCPCapability(ctx, a.Run)
		if e != nil {
			t.Fatal(e)
		}
		verification := factory.AppVerificationReport{DeliveryID: subject.Delivery.DeliveryID, InputDigest: capability.ScopeDigest(), Outcome: factory.AppVerificationFailed, CheckReceipts: []factory.VerificationCheckReceipt{{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: factory.MCPInputDigest([]byte("native application assertions passed")), LogExcerpt: "Native assertions passed"}}, Failures: []factory.CheckFailure{{CheckRef: revision.Ideas[0].ID, Detail: "The accepted application outcome requires an idle in-scope follow-up."}}}
		verification.Coverage = []factory.ReassessmentCoverage{}
		if mode == "delivery_repair_incomplete" {
			verification.Outcome = factory.AppVerificationIncomplete
		}
		if mode == "delivery_repair_wrong_failure" {
			verification.Failures[0].CheckRef = factory.NewID()
		}
		if mode == "delivery_repair_bad_input" {
			verification.InputDigest = strings.Repeat("8", 64)
		}
		body, _ := json.Marshal(verification)
		submission := testMCPSubmission(capability, factory.MCPToolFinishAppVerify, factory.NewID(), body)
		if _, e = db.RecordMCPSubmission(ctx, submission); e != nil {
			t.Fatal(e)
		}
		if done, e := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); e != nil || !done {
			t.Fatal("authenticated failed verification", done, e)
		}
		if _, e = coord.RequestDeliveryRepair(ctx, "native:7", revision.Owner, a.Run); e == nil {
			t.Fatal("repair requested before actual retirement")
		}
		fx.host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
			r, e := db.FactoryRun(ctx, in.ID)
			if e != nil {
				return project.FactoryState{}, e
			}
			return project.FactoryState{ID: in.ID, Project: r.ProjectID, Role: r.Role, Phase: project.FactoryCompleted, Retirement: "confirmed", CredentialReturned: true, LeaseID: "lease-" + in.ID, Generation: 3}, nil
		}
		r, e := db.FactoryRun(ctx, a.Run)
		if e != nil {
			t.Fatal(e)
		}
		r.Outcome, r.Reconciled = factory.Succeeded, true
		observed, e := fx.host.inspect(project.FactoryInspect{Project: fx.proj, ID: r.ID})
		if e != nil {
			t.Fatal(e)
		}
		if e = adoptExecutionFacts(&r, nativeRunObservation(t, observed), nativeCustodyObservation(t, observed, identity.Execution{Kind: identity.Factory, ExecutionID: r.ID, State: identity.ExecutionTerminal, LeaseID: observed.LeaseID, Binding: &identity.Binding{Kind: identity.Factory, ID: r.ID, Generation: observed.Generation, Project: fx.proj, ChildID: a.Preparation}})); e != nil {
			t.Fatal(e)
		}
		if e = db.SaveFactoryRun(ctx, r); e != nil {
			t.Fatal(e)
		}
		reservation, e := db.Reservation(ctx, a.ID)
		if e != nil {
			t.Fatal(e)
		}
		if reservation.State == factory.ReservationHeld {
			if e = db.RecordRunUsage(ctx, factory.Usage{RunID: r.ID, Repository: fx.repo, Connection: a.Connection, Minutes: 1, StartedAt: r.Started, EndedAt: r.Started.Add(time.Millisecond)}); e != nil {
				t.Fatal(e)
			}
			if e = db.ConsumeReservation(ctx, a.ID); e != nil {
				t.Fatal(e)
			}
		}
		if mode == "delivery_repair_incomplete" || mode == "delivery_repair_wrong_failure" || mode == "delivery_repair_bad_input" {
			if _, e = coord.RequestDeliveryRepair(ctx, "native:7", revision.Owner, a.Run); e == nil {
				t.Fatal("unqualified report manufactured delivery repair cause")
			}
			if available, e := coord.DeliveryRepairAvailable(ctx, a.Run); e != nil || available {
				t.Fatal("unqualified report offered repair", e)
			}
			if _, e = db.DeliveryRepairRecord(ctx, fx.repo, a.Run); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("unqualified report retained a cause", e)
			}
			if charges, e := db.RepairCharges(ctx, fx.repo); e != nil || len(charges) != 0 {
				t.Fatal("unqualified report spent repair grant", e)
			}
			return
		}
		if available, e := coord.DeliveryRepairAvailable(ctx, a.Run); e != nil || !available {
			t.Fatal("fresh qualified owner repair unavailable", e)
		}
		if e = sqlDB.QueryRow(`SELECT md5(to_jsonb(l)::text||to_jsonb(r)::text||to_jsonb(u)::text) FROM factory_attempt_allowances l JOIN factory_reservations r ON r.assignment=l.root_assignment JOIN factory_usage u ON u.run=$2 WHERE l.root_assignment=$1`, a.ID, a.Run).Scan(&failedLedger); e != nil {
			t.Fatal(e)
		}
		if _, e = db.IntentExecutionLimit(ctx, revision.Intent); !errors.Is(e, store.ErrNotFound) {
			t.Fatal("fixture unexpectedly preconfigured origin limit", e)
		}
		if mode == "delivery_repair" {
			journal := func() string {
				t.Helper()
				var digest string
				if err := sqlDB.QueryRowContext(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(c) ORDER BY id),'[]'::jsonb)::text) FROM factory_commands c`).Scan(&digest); err != nil {
					t.Fatal(err)
				}
				return digest
			}
			before := journal()
			for _, actor := range []int64{0, 1, 7, revision.Owner + 1} {
				if _, err := coord.RequestDeliveryRepair(ctx, "native:7", actor, a.Run); err == nil {
					t.Fatal("unadmitted/foreign/native-number owner admitted repair", actor)
				}
			}
			for _, principal := range []string{"", strings.Repeat("p", 129)} {
				if _, err := coord.RequestDeliveryRepair(ctx, principal, revision.Owner, a.Run); err == nil {
					t.Fatal("invalid command principal admitted repair")
				}
			}
			if after := journal(); before != after {
				t.Fatal("refused owner/principal changed the retained command journal")
			}
			if _, err := db.IntentExecutionLimit(ctx, revision.Intent); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refused owner manufactured an origin limit", err)
			}
			if _, err := db.DeliveryRepairRecord(ctx, fx.repo, a.Run); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("refused owner manufactured a repair cause", err)
			}
		}
		receipt, e := coord.RequestDeliveryRepair(ctx, "native:7", revision.Owner, a.Run)
		if e != nil {
			t.Fatal("explicit delivery repair request", e)
		}
		if replay, e := coord.RequestDeliveryRepair(ctx, "native:7", revision.Owner, a.Run); e != nil || replay != receipt {
			t.Fatal("delivery repair request replay", e)
		}
		ownerRequest = receipt
		failedAssignment, e := db.Assignment(ctx, a.ID)
		if e != nil {
			t.Fatal(e)
		}
		failedVerifierJSON, _ = json.Marshal(failedAssignment)
		if limit, e := db.IntentExecutionLimit(ctx, revision.Intent); e != nil || !limit.Active || limit.OwnerID != revision.Owner || limit.RepositoryID != fx.repo || limit.ActiveLimitMs != factory.DefaultIntentExecutionAllowance().ActiveLimitMs {
			t.Fatal("explicit owner request did not record canonical default origin", e)
		}
		if mode == "delivery_repair" {
			var principal, payload string
			if err := sqlDB.QueryRowContext(ctx, `SELECT principal,payload FROM factory_commands WHERE type=$1 AND target=$2`, factory.CommandIntentExecutionLimit, factory.IntentExecutionTarget(revision.Intent)).Scan(&principal, &payload); err != nil {
				t.Fatal(err)
			}
			var limit factory.IntentExecutionAllowance
			if principal != "native:7" || json.Unmarshal([]byte(payload), &limit) != nil || limit.OwnerID != revision.Owner {
				t.Fatal("canonical ownership rewrote native command attribution or rounded the owner")
			}
			if native, err := db.Project(ctx, fx.proj); err != nil || native.OwnerID != 7 || native.RepositoryID != 7 {
				t.Fatal("canonical repair owner rewrote native custody", native, err)
			}
		}
		for pass := 0; pass < 3; pass++ {
			coord.driveDeliveryRepairs(ctx)
		}
		deliveryRecord, e = db.DeliveryRepairRecord(ctx, fx.repo, a.Run)
		if e != nil || deliveryRecord.Cycle.State != factory.RepairInvestigating {
			t.Fatal("delivery repair investigator", deliveryRecord.Cycle.State, deliveryRecord.Cycle.Reason, e)
		}
	}
	for _, binding := range initial {
		if deliveryMode {
			continue
		}
		if mode == "append_producer" {
			stallSeedControl(t, db, fx.repo, binding.IssueIndex, factory.ReadinessBlocked, factory.BlockerResultPending, stallBlocker(factory.BlockerResultPending, fx.repo, binding.IssueIndex+1))
		} else {
			stallSeedControl(t, db, fx.repo, binding.IssueIndex, factory.ReadinessBlocked, factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0))
		}
	}
	var event factory.StallEvent
	var record store.StallRepairRecord
	if !deliveryMode {
		first, e := coord.ServeStallRound(ctx, DispatchReport{})
		if e != nil || len(first.Repositories) != 1 || first.Repositories[0].Event == nil {
			t.Fatal("stall observation", e)
		}
		event = *first.Repositories[0].Event
		_, _ = coord.ServeStallRound(ctx, DispatchReport{})
		record, err = db.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
	} else {
		record.Repair.Cycles = []factory.StallRepairCycle{deliveryRecord.Cycle}
	}
	if err != nil || len(record.Repair.Cycles) != 1 {
		t.Fatal("repair stage", err)
	}
	cycle := record.Repair.Cycles[0]
	if cycle.State != factory.RepairInvestigating {
		t.Fatalf("repair launch state=%s reason=%s", cycle.State, cycle.Reason)
	}
	for _, launch := range fx.host.launches {
		if launch.Run.ID != cycle.RunID {
			continue
		}
		for _, guidance := range []string{"factory.reassessment_scope", "factory.record_diagnosis", "factory.draft_repair", "factory.preview_repair", "payload_hash", "no extra model consultation", "Finish once"} {
			if !strings.Contains(string(launch.Prompt), guidance) {
				t.Fatal("repair Strategist lost R40 tool guidance", guidance)
			}
		}
	}
	tasks := append([]factory.IntentTask(nil), revision.Tasks...)
	retained := []int64{2}
	if mode == "append_producer" || deliveryMode {
		tasks = append(tasks, factory.IntentTask{Key: choiceTaskKey(3), Objective: "Supply the missing in-scope validation fixture", Outcomes: []string{"The requested fixture is available for independent maintainer acceptance"}, IdeaIDs: []string{revision.Ideas[0].ID}})
		retained = []int64{1, 2, 3}
		if deliveryMode {
			retained = []int64{1, 2}
		}
	} else {
		tasks[0].Objective += " with the clarified validation boundary"
	}
	keys := submittedKeys(tasks)
	successor := generatedDecisionID("revision", cycle.ID, revision.ID)
	proposal := factory.StallRepairReport{StallID: record.ID, CycleID: cycle.ID, InventoryDigest: cycle.InventoryDigest, SourceCommit: cycle.Local.SourceCommit, PreparationDigest: cycle.Local.PreparationDigest, Causes: []factory.CheckFailure{{CheckRef: "dependency_cycle", Detail: "The idle task lacks its precise validation boundary."}}, RetainedIssueIndexes: retained, IntentChanges: []factory.ReassessmentIntentChange{{IntentID: revision.Intent, ExpectedRevision: successor, ScopeDigest: revision.Digest(), Reason: "Clarify the idle validation task while retaining protected human work.", Tasks: tasks}}, Coverage: []factory.ReassessmentCoverage{{IntentID: revision.Intent, OutcomeID: revision.Ideas[0].ID, TaskKeys: keys}}, Outcome: factory.RepairOutcomeRepair}
	if mode == "append_producer" {
		proposal.Causes = []factory.CheckFailure{{CheckRef: "missing_producer", Detail: "The manual fixture requirement has no accepted producer."}}
		proposal.IntentChanges[0].Reason = "Add the missing fixture producer within the accepted scope; preserve both human prerequisite routes."
	}
	if !deliveryMode {
		if err = proposal.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	run, err := db.FactoryRun(ctx, cycle.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var repairCapability factory.MCPCapability
	if deliveryMode || mode == "append_producer" {
		repairCapability, err = db.GetMCPCapability(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.ActivateObservedMCPCapability(ctx, repairCapability); err != nil {
			t.Fatal(err)
		}
		repairCapability, err = db.GetMCPCapability(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		current, e := db.FactoryCurrentContext(ctx, repairCapability, time.Now())
		if deliveryMode && (e != nil || current.Role.DeliveryRepair == nil || !current.Role.DeliveryRepair.Complete || current.Role.DeliveryRepair.Subject != deliveryRecord.Subject || current.Role.DeliveryRepair.Cause.FailureDigest != deliveryRecord.Cause.FailureDigest) {
			t.Fatal("native Strategist lacks exact mandatory repair context", e)
		}
	}
	if mode == "append_producer" || mode == "delivery_repair" {
		var report any = proposal
		if deliveryMode {
			report = factory.DeliveryRepairReport{Subject: deliveryRecord.Subject, InputDigest: repairCapability.ScopeDigest(), Causes: []factory.CheckFailure{{CheckRef: revision.Ideas[0].ID, Detail: "Add the bounded missing application behavior."}}, RetainedIssueIndexes: retained, IntentChanges: proposal.IntentChanges, Coverage: proposal.Coverage, Outcome: factory.RepairOutcomeRepair}
		}
		encoded, _ := json.Marshal(report)
		previewDB, e := sql.Open("pgx", dsn)
		if e != nil {
			t.Fatal(e)
		}
		defer previewDB.Close()
		snapshot := func() string {
			t.Helper()
			var all strings.Builder
			for _, table := range []string{"factory_commands", "factory_assignments", "factory_runs", "factory_attempt_allowances", "factory_reservations", "factory_usage", "factory_priority_judgments", "factory_stall_events", "factory_intent_heads", "factory_intent_generations", "factory_intent_tasks", "issue_acceptance_heads", "issue_acceptance_decisions"} {
				var body string
				if e := previewDB.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM ` + table + ` t`).Scan(&body); e != nil {
					t.Fatal("preview retained row snapshot", table, e)
				}
				all.WriteString(body)
			}
			return all.String()
		}
		before := snapshot()
		var first []byte
		for i := 0; i < 2; i++ {
			preview, e := coord.PreviewRepair(ctx, repairCapability, encoded)
			if e != nil || !preview.Valid || preview.Validation == nil || len(preview.Effects) != 1 || preview.Effects[0].Creates != 1 || len(preview.Effects[0].PreservedTasks) != 2 || preview.InputDigest != repairCapability.ScopeDigest() {
				t.Fatalf("bound protected graph preview: %+v %v", preview, e)
			}
			body, _ := json.Marshal(preview)
			if i > 0 && string(body) != string(first) {
				t.Fatal("same preview changed its deterministic result")
			}
			first = body
		}
		var altered map[string]any
		_ = json.Unmarshal(encoded, &altered)
		if deliveryMode {
			altered["input_digest"] = strings.Repeat("9", 64)
		} else {
			altered["inventory_digest"] = strings.Repeat("9", 64)
		}
		foreign, _ := json.Marshal(altered)
		if _, e = coord.PreviewRepair(ctx, repairCapability, foreign); e == nil {
			t.Fatal("preview admitted another cause scope")
		}
		_ = json.Unmarshal(encoded, &altered)
		change := altered["intent_changes"].([]any)[0].(map[string]any)
		change["tasks"].([]any)[0].(map[string]any)["objective"] = "Rewrite protected delivered or human-accepted work"
		invalid, _ := json.Marshal(altered)
		preview, e := coord.PreviewRepair(ctx, repairCapability, invalid)
		if e != nil || preview.Valid || preview.Reason == "" || len(preview.Effects) != 0 {
			t.Fatalf("protected rewrite lacked a structural refusal: %+v %v", preview, e)
		}
		_ = json.Unmarshal(encoded, &altered)
		altered["apply"] = true
		invalid, _ = json.Marshal(altered)
		if _, e = coord.PreviewRepair(ctx, repairCapability, invalid); e == nil {
			t.Fatal("preview admitted an unknown effect field")
		}
		revoked := repairCapability
		revoked.State = factory.MCPCapabilityRevoked
		if _, e = coord.PreviewRepair(ctx, revoked, encoded); e == nil {
			t.Fatal("preview admitted an inactive capability")
		}
		currentEnumerator := coord.CompletionEnumerator
		coord.CompletionEnumerator = nil
		if _, e = coord.PreviewRepair(ctx, repairCapability, encoded); e == nil {
			t.Fatal("preview assumed unavailable native inventory was empty")
		}
		coord.CompletionEnumerator = currentEnumerator
		capture := coord.CompletionLocalCapture
		coord.CompletionLocalCapture = func(ctx context.Context, repo int64) (factory.CompletionLocal, error) {
			current, err := capture(ctx, repo)
			current.SourceCommit = strings.Repeat("9", 40)
			return current, err
		}
		if _, e = coord.PreviewRepair(ctx, repairCapability, encoded); e == nil {
			t.Fatal("preview accepted moved current source")
		}
		coord.CompletionLocalCapture = capture
		if after := snapshot(); before != after || chooser.calls != 0 || graph.submissions != baseline {
			t.Fatal("read-only repair preview spent, recorded or changed a native graph")
		}
		// Intermediate diagnosis is an immutable report with its own attributed
		// feed outlet. It cannot turn suspected causation into graph authority.
		diagnosis := factory.RepairDiagnosis{Subject: repairCapability.Subject, InputDigest: repairCapability.ScopeDigest(), SourceCommit: cycle.Local.SourceCommit, InventoryDigest: cycle.InventoryDigest, Title: "Missing fixture producer", Summary: "The sealed inventory records no authorized producer for the required fixture.", State: "supported", Causes: proposal.Causes, EvidenceRefs: []string{"sealed-inventory:" + cycle.InventoryDigest}, Alternatives: []string{"A renamed graph alone would leave the prerequisite unsatisfied."}, Uncertainty: []string{"The maintainer must independently accept the fixture result."}}
		body, _ := json.Marshal(diagnosis)
		submission := testMCPSubmission(repairCapability, factory.MCPToolRecordDiagnosis, factory.NewID(), body)
		receipt, e := db.RecordWorkerPost(ctx, submission)
		if e != nil || receipt.Record.ID == "" {
			t.Fatal("cause-bound diagnosis was not recorded with feed receipt", e)
		}
		post, e := db.FeedPost(ctx, receipt.Record.ID)
		if e != nil || post.Actor != "strategist" || post.Assignment != cycle.AssignmentID || post.Payload == nil || post.Payload.Communication == nil || post.Payload.Communication.Kind != "diagnosis" || len(post.Payload.Communication.Items) != len(diagnosis.Causes) || len(post.Payload.Communication.Alternatives) != 1 || len(post.Payload.Communication.Remaining) != 1 || !strings.Contains(post.Payload.Communication.Summary, "unchanged") {
			t.Fatal("diagnosis lost typed evidence, attribution or uncertainty", e)
		}
		if replay, e := db.RecordWorkerPost(ctx, submission); e != nil || replay.Record.ID != receipt.Record.ID {
			t.Fatal("diagnosis lost-reply replay duplicated its observation", e)
		}
		conflict := submission
		changedDiagnosis := diagnosis
		changedDiagnosis.Summary = "Changed bytes cannot reuse the diagnosis identity."
		conflict.Payload, _ = json.Marshal(changedDiagnosis)
		if repeated, e := db.RecordWorkerPost(ctx, conflict); e != nil || repeated.Record.ID != receipt.Record.ID {
			t.Fatal("reused diagnosis key did not return its original post", e)
		}
		for _, field := range []string{"subject", "input", "source", "inventory", "artifact", "state"} {
			bad := diagnosis
			switch field {
			case "subject":
				bad.Subject += ":foreign"
			case "input":
				bad.InputDigest = strings.Repeat("9", 64)
			case "source":
				bad.SourceCommit = strings.Repeat("9", 40)
			case "inventory":
				bad.InventoryDigest = strings.Repeat("9", 64)
			case "artifact":
				bad.ArtifactIDs = []string{factory.NewID()}
			case "state":
				bad.State = "adopted"
			}
			payload, _ := json.Marshal(bad)
			sub := testMCPSubmission(repairCapability, factory.MCPToolRecordDiagnosis, factory.NewID(), payload)
			if _, e := db.RecordWorkerPost(ctx, sub); e == nil {
				t.Fatal("diagnosis admitted invalid " + field)
			}
		}
		for count := 1; count < factory.MaxRepairDiagnoses; count++ {
			sub := testMCPSubmission(repairCapability, factory.MCPToolRecordDiagnosis, factory.NewID(), body)
			if _, e := db.RecordWorkerPost(ctx, sub); e != nil {
				t.Fatal("bounded diagnosis record", count, e)
			}
		}
		overflow := testMCPSubmission(repairCapability, factory.MCPToolRecordDiagnosis, factory.NewID(), body)
		if _, e := db.RecordWorkerPost(ctx, overflow); e == nil {
			t.Fatal("diagnosis limit did not preserve the terminal slot")
		}
		seen := map[string]bool{}
		var after int64
		for page := 0; page < 4; page++ {
			view, e := db.ReassessmentScope(ctx, repairCapability, after)
			if e != nil || view.Total != factory.MaxRepairDiagnoses || len(view.Records) != factory.RepairObservationPageSize || view.InputDigest != repairCapability.ScopeDigest() || view.InventoryDigest != cycle.InventoryDigest || view.MaximumPerTool != factory.MaxRepairDiagnoses || view.Complete != (page == 3) {
				t.Fatalf("diagnosis scope page: %+v %v", view, e)
			}
			for _, record := range view.Records {
				if seen[record.ID] || record.Sequence <= after || record.Tool != factory.MCPToolRecordDiagnosis || !factory.ValidID(record.ID) {
					t.Fatal("diagnosis scope lost canonical own sequence or identity")
				}
				seen[record.ID] = true
			}
			after = view.NextAfter
		}
		if len(seen) != factory.MaxRepairDiagnoses || snapshot() != before || chooser.calls != 0 || graph.submissions != baseline {
			t.Fatal("diagnosis report/scope changed graphs, workflow ledgers or spending")
		}
		var expanded map[string]any
		_ = json.Unmarshal(encoded, &expanded)
		changeForDraft := expanded["intent_changes"].([]any)[0].(map[string]any)
		changeForDraft["reason"] = strings.Repeat("é漢🧪", 400)
		changeForDraft["tasks"].([]any)[0].(map[string]any)["objective"] = strings.Repeat("αβ", 200)
		unicodeReport, _ := json.Marshal(expanded)
		draft := factory.RepairDraft{Report: unicodeReport, DiagnosisRefs: []string{"diagnosis:" + receipt.Record.ID}}
		draftBody, _ := json.Marshal(draft)
		draftSubmission := testMCPSubmission(repairCapability, factory.MCPToolDraftRepair, factory.NewID(), draftBody)
		draftReceipt, e := db.RecordWorkerPost(ctx, draftSubmission)
		if e != nil || draftReceipt.Record.ID == "" {
			t.Fatal("cause-bound draft was not recorded", e)
		}
		if count, e := db.MCPSubmissionCount(ctx, repairCapability.AssignmentID); e != nil || count != 0 {
			t.Fatal("repair posts acquired a duplicate MCP journal", count, e)
		}
		draftPost, e := db.FeedPost(ctx, draftReceipt.Record.ID)
		if e != nil || draftPost.Payload == nil || draftPost.Payload.Communication == nil || draftPost.Payload.Communication.Kind != "repair_draft" || len(draftPost.Payload.Communication.Items) != len(tasks)+1 || !strings.Contains(draftPost.Payload.Communication.Summary, "Not submitted") || draftPost.Payload.Communication.Items[1].ID != tasks[0].Key {
			t.Fatal("draft lost its typed proposed graph or authority boundary", e)
		}
		if len(draftPost.Payload.Communication.Remaining) < 2 || len([]rune(draftPost.Payload.Communication.Items[0].Detail)) > store.FeedSummaryLimit {
			t.Fatal("large draft preview hid its abbreviation or exceeded the feed bound")
		}
		var recovered strings.Builder
		offset, fragments := 0, 0
		for {
			page, e := db.ReadRepairObservation(ctx, repairCapability, draftReceipt.Record.ID, offset)
			if e != nil || page.ID != draftReceipt.Record.ID || page.Tool != factory.MCPToolDraftRepair || page.Encoding != "utf8-json-fragment" || page.Offset != offset || len(page.Content) > factory.RepairObservationFragmentBytes || !utf8.ValidString(page.Content) {
				t.Fatalf("exact draft fragment: %+v %v", page, e)
			}
			recovered.WriteString(page.Content)
			fragments++
			if page.Complete {
				break
			}
			if page.NextOffset <= offset {
				t.Fatal("draft fragment did not advance")
			}
			offset = page.NextOffset
		}
		if fragments < 2 || recovered.String() != string(draftBody) {
			t.Fatal("immutable Unicode draft did not recover byte-for-byte")
		}
		midRune := 0
		for i := range draftBody {
			if !utf8.RuneStart(draftBody[i]) {
				midRune = i
				break
			}
		}
		for _, invalidOffset := range []int{-1, len(draftBody), midRune} {
			if _, e := db.ReadRepairObservation(ctx, repairCapability, draftReceipt.Record.ID, invalidOffset); e == nil {
				t.Fatal("draft read admitted an invalid byte offset", invalidOffset)
			}
		}
		if _, e := db.ReadRepairObservation(ctx, repairCapability, factory.NewID(), 0); e == nil {
			t.Fatal("draft read supplied missing or foreign content")
		}
		foreignCap := repairCapability
		foreignCap.Project = "p" + strings.Repeat("9", 24)
		if _, e := db.ReadRepairObservation(ctx, foreignCap, draftReceipt.Record.ID, 0); e == nil {
			t.Fatal("draft read admitted a foreign capability tuple")
		}
		if replay, e := db.RecordWorkerPost(ctx, draftSubmission); e != nil || replay.Record.ID != draftReceipt.Record.ID {
			t.Fatal("draft replay lost its original receipt", e)
		}
		for _, kind := range []string{"missing_diagnosis", "duplicate_diagnosis", "foreign_input", "unknown_report_field"} {
			bad := draft
			switch kind {
			case "missing_diagnosis":
				bad.DiagnosisRefs = []string{"diagnosis:" + factory.NewID()}
			case "duplicate_diagnosis":
				bad.DiagnosisRefs = []string{draft.DiagnosisRefs[0], draft.DiagnosisRefs[0]}
			default:
				var report map[string]any
				_ = json.Unmarshal(encoded, &report)
				if kind == "unknown_report_field" {
					report["apply"] = true
				} else if deliveryMode {
					report["input_digest"] = strings.Repeat("9", 64)
				} else {
					report["inventory_digest"] = strings.Repeat("9", 64)
				}
				bad.Report, _ = json.Marshal(report)
			}
			payload, _ := json.Marshal(bad)
			sub := testMCPSubmission(repairCapability, factory.MCPToolDraftRepair, factory.NewID(), payload)
			if _, e := db.RecordWorkerPost(ctx, sub); e == nil {
				t.Fatal("draft admitted " + kind)
			}
		}
		conflictingDraft := draftSubmission
		draftWithoutRef := draft
		draftWithoutRef.DiagnosisRefs = nil
		conflictingDraft.Payload, _ = json.Marshal(draftWithoutRef)
		if repeated, e := db.RecordWorkerPost(ctx, conflictingDraft); e != nil || repeated.Record.ID != draftReceipt.Record.ID {
			t.Fatal("reused draft key did not return its original post", e)
		}
		for count := 1; count < factory.MaxRepairObservations; count++ {
			sub := testMCPSubmission(repairCapability, factory.MCPToolDraftRepair, factory.NewID(), draftBody)
			if _, e := db.RecordWorkerPost(ctx, sub); e != nil {
				t.Fatal("bounded draft recording", count, e)
			}
		}
		overflow = testMCPSubmission(repairCapability, factory.MCPToolDraftRepair, factory.NewID(), draftBody)
		if _, e := db.RecordWorkerPost(ctx, overflow); e == nil {
			t.Fatal("draft limit did not preserve its terminal slot")
		}
		view, e := db.ReassessmentScope(ctx, repairCapability, 0)
		if e != nil || view.Total != 2*factory.MaxRepairObservations || snapshot() != before || chooser.calls != 0 || graph.submissions != baseline {
			t.Fatal("drafts granted adoption or changed funding/workflow rows", e)
		}
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "repair proposal ready", true
	if err = db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	phaseOutput := &factory.PhaseOutput{Repair: &proposal}
	if deliveryMode {
		cap, e := db.GetMCPCapability(ctx, run.ID)
		if e != nil {
			t.Fatal(e)
		}
		phaseOutput = &factory.PhaseOutput{DeliveryRepair: &factory.DeliveryRepairReport{Subject: deliveryRecord.Subject, InputDigest: cap.ScopeDigest(), Causes: []factory.CheckFailure{{CheckRef: revision.Ideas[0].ID, Detail: "Add the bounded missing application behavior."}}, RetainedIssueIndexes: retained, IntentChanges: proposal.IntentChanges, Coverage: proposal.Coverage, Outcome: factory.RepairOutcomeRepair}}
		if mode == "delivery_repair_nochange" {
			phaseOutput.DeliveryRepair.Outcome = factory.RepairOutcomeNoChange
			phaseOutput.DeliveryRepair.IntentChanges = nil
			phaseOutput.DeliveryRepair.Coverage = nil
		}
		if err = phaseOutput.DeliveryRepair.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	output := phaseFinish(t, factory.PhaseResult{Version: factory.PhaseResultVersion, Phase: project.ExecutionRepair, Status: "completed", Summary: "Repair the idle accepted graph and preserve protected work.", Output: phaseOutput})
	if deliveryMode {
		payload, _ := json.Marshal(map[string]any{"report": phaseOutput.DeliveryRepair})
		submission := testMCPSubmission(repairCapability, factory.MCPToolFinishReassessment, factory.NewID(), payload)
		if _, e := db.RecordMCPSubmission(ctx, submission); e != nil {
			t.Fatal(e)
		}
		if settled, e := SettleMCPTerminalProposal(ctx, db, cycle.AssignmentID, time.Now()); e != nil || !settled {
			t.Fatal("authenticated repair proposal did not settle", settled, e)
		}
		if a, e := db.Assignment(ctx, cycle.AssignmentID); e != nil || a.Outcome != factory.Succeeded {
			t.Fatal("authenticated repair finish", a.Outcome, a.Reason, e)
		}
	} else if a, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); !ok || a.Outcome != factory.Succeeded {
		t.Fatalf("repair finish outcome=%s reason=%s recorded=%v", a.Outcome, a.Reason, ok)
	}
	if mode == "append_producer" || mode == "delivery_repair" {
		if _, e := db.ReassessmentScope(ctx, repairCapability, 0); e == nil {
			t.Fatal("finished investigator retained active repair scope")
		}
		late := factory.RepairDiagnosis{Subject: repairCapability.Subject, InputDigest: repairCapability.ScopeDigest(), SourceCommit: cycle.Local.SourceCommit, InventoryDigest: cycle.InventoryDigest, Title: "Late diagnosis", Summary: "A finished worker has no authority to append another report.", State: "incomplete", Causes: proposal.Causes}
		payload, _ := json.Marshal(late)
		sub := testMCPSubmission(repairCapability, factory.MCPToolRecordDiagnosis, factory.NewID(), payload)
		if _, e := db.RecordWorkerPost(ctx, sub); e == nil {
			t.Fatal("finished investigator appended a late diagnosis", e)
		}
	}
	grantChanged := false
	changeGrant := func(active bool) {
		t.Helper()
		prior := grant
		grant.ID, grant.Predecessor, grant.Revision, grant.Active = factory.NewID(), prior.ID, prior.Revision+1, active
		raw, _ := json.Marshal(grant)
		command := factory.Command{ID: grant.ID, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(fx.repo), Principal: "soda-tester", Payload: string(raw)}
		command.Digest = factory.SettingsDigest(command.Type, command.Target, command.Payload)
		if _, _, err := db.ApplyRepairGrantCommand(ctx, command, grant, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "delivery_repair_withdrawn" {
		changeGrant(false)
	}
	for pass := 0; pass < 14; pass++ {
		if deliveryMode {
			coord.driveDeliveryRepairs(ctx)
			deliveryRecord, err = db.DeliveryRepairRecord(ctx, fx.repo, failedDeliveryRun)
			record.Repair.Cycles = []factory.StallRepairCycle{deliveryRecord.Cycle}
		} else {
			coord.driveStallRepairs(ctx, map[int64]bool{fx.repo: true})
			record, err = db.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
		}
		if err != nil {
			t.Fatal(err)
		}
		cycle = record.Repair.Cycles[0]
		if deliveryMode && cycle.State == factory.RepairAttention {
			break
		}
		withdrawNew, withdrawActive := strings.HasSuffix(mode, "grant_hold_new_effect"), strings.HasSuffix(mode, "grant_withdrawn_after_admission")
		if (withdrawNew || withdrawActive) && !grantChanged && cycle.State == factory.RepairApplying {
			progress, err := loadGraphRepairAdoption(coord.Graph, cycle.AdoptionProgress, record.Repository, "", record.ID, cycle.ID, cycle.InventoryDigest)
			if deliveryMode {
				progress, err = loadDeliveryRepairAdoption(coord.Graph, deliveryRecord)
			}
			if err != nil {
				t.Fatal(err)
			}
			step, _ := progress.Journal.CurrentStep()
			if withdrawNew && step.State == reassessmentStepStaged {
				changeGrant(false)
				var heldReason string
				if deliveryMode {
					coord.driveDeliveryRepairs(ctx)
					held, e := db.DeliveryRepairRecord(ctx, fx.repo, failedDeliveryRun)
					err = e
					heldReason = held.Cycle.Reason
				} else {
					coord.driveStallRepairs(ctx, map[int64]bool{fx.repo: true})
					held, e := db.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
					err = e
					heldReason = held.Repair.Cycles[0].Reason
				}
				if err != nil || heldReason != "repair_sponsor_grant_unavailable" || graph.submissions != baseline {
					t.Fatal("withdrawn grant admitted new effect", err)
				}
				if _, err = db.IntentGeneration(ctx, successor); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("held successor activated")
				}
				changeGrant(true)
				grantChanged = true
			} else if withdrawActive && step.State == reassessmentStepActive {
				changeGrant(false)
				grantChanged = true
			}
		}
		if cycle.State == factory.RepairAdopted {
			break
		}
		// Restart from retained rows after the immutable current operation exists.
		if pass == 5 {
			fresh := NewCoordinator(db, coord.Host, coord.Broker)
			fresh.Graph, fresh.GraphActorID, fresh.AcceptanceReads = coord.Graph, coord.GraphActorID, coord.AcceptanceReads
			fresh.CompletionEnumerator, fresh.CompletionLocalCapture, fresh.Planning = enumerator, db.CaptureCompletionLocal, coord.Planning
			coord = fresh
		}
	}
	if mode == "delivery_repair_withdrawn" || mode == "delivery_repair_nochange" {
		charges, e := db.RepairCharges(ctx, fx.repo)
		if cycle.State != factory.RepairAttention || e != nil || len(charges) != 1 || chooser.calls != 0 || graph.submissions != baseline {
			t.Fatal("delivery failure/withdrawal fabricated judgment or graph effects", cycle.State, cycle.Reason, e)
		}
		return
	}
	if cycle.State != factory.RepairAdopted {
		charges, _ := db.RepairCharges(ctx, fx.repo)
		t.Fatalf("repair adoption state=%s reason=%s charges=%d calls=%d reserved=%v", cycle.State, cycle.Reason, len(charges), chooser.calls, cycle.JudgmentFingerprint != "")
	}
	head, err := db.IntentRevision(ctx, revision.Intent, revision.Owner)
	if err != nil || head.ID != successor {
		t.Fatal("repair successor missing", err)
	}
	bindings, err := db.IntentTaskBindings(ctx, successor)
	if err != nil || len(bindings) != len(tasks) {
		t.Fatal("repair readback missing", err)
	}
	for _, binding := range bindings {
		if binding.TaskKey == tasks[1].Key && binding.AcceptanceID != initial[1].AcceptanceID {
			t.Fatal("protected acceptance changed")
		}
	}
	if mode == "append_producer" {
		for _, binding := range initial {
			head, err := db.AcceptanceHead(ctx, fx.repo, binding.IssueIndex)
			if err != nil || head != protectedHeads[binding.IssueIndex] {
				t.Fatal("repair replaced human result acceptance", err)
			}
		}
		if _, err := db.AcceptanceHead(ctx, fx.repo, 3); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("repair invented an acceptance for the manual result", err)
		}
		after, _ := json.Marshal([]*sdk.SnapshotDependencyPage{graph.issues[1].Dependencies, graph.issues[2].Dependencies, graph.issues[3].Dependencies})
		if string(after) != string(protectedDependencies) || len(graph.issues) != 4 {
			t.Fatal("repair changed protected prerequisite routes or omitted the new producer")
		}
		generation, err := db.IntentGeneration(ctx, successor)
		var payload sdk.IssueGraphPayload
		if err != nil || json.Unmarshal(generation.Payload, &payload) != nil || len(payload.Creates) != 1 || len(payload.Edits) != 0 || len(payload.Dependencies) != 0 {
			t.Fatal("append producer did not remain one independent create", err)
		}
	}
	charges, err := db.RepairCharges(ctx, fx.repo)
	if err != nil || len(charges) != 2 || chooser.calls != 1 || graph.submissions != baseline+1 || graph.issues[2].Issue.Content != protectedBody {
		t.Fatalf("repair duplicated spend/effect or changed protected work: charges=%d calls=%d submissions=%d %v", len(charges), chooser.calls, graph.submissions, err)
	}
	if mode == "delivery_repair_tight" {
		for _, charge := range charges {
			if charge.Kind == factory.RepairChargeRun && charge.ActiveMinutes != 2 {
				t.Fatal("repair ignored tighter remaining grant", charge.ActiveMinutes)
			}
		}
	}
	if generation, e := db.IntentGeneration(ctx, successor); e != nil || generation.RepairCycle != cycle.ID || generation.ReassessmentCycle != "" || generation.State != "completed" {
		t.Fatal("repair borrowed completion identity", e)
	}
	if adoptedManifest != nil {
		g, e := db.IntentGeneration(ctx, successor)
		if e != nil || g.VerificationManifest == nil || g.VerificationManifest.Proposal.Digest() != adoptedManifest.Proposal.Digest() || g.VerificationManifest.Catalog != adoptedManifest.Catalog || g.VerificationManifest.ValidateFor(head) != nil || g.VerificationManifestDigest != g.VerificationManifest.Digest() {
			t.Fatal("protected delta dropped or changed adopted verification", e)
		}
		omitted := g
		omitted.VerificationManifest = nil
		omitted.VerificationManifestDigest = ""
		if e = db.RecordIntentGeneration(ctx, omitted, factory.GraphReceiptFacts{}); e == nil {
			t.Fatal("recorded generation allowed manifest omission")
		}
		changed := head
		changed.ID = factory.NewID()
		changed.Predecessor = head.ID
		changed.EffectiveOutcomes = []string{factory.NewID()}
		if e = coord.inheritGraphVerification(ctx, changed, &factory.IntentGeneration{}); e == nil {
			t.Fatal("protected delta silently changed effective verification outcomes")
		}
	}
	if deliveryMode {
		coord.driveDeliveryRepairs(ctx)
		deliveryRecord, err = db.DeliveryRepairRecord(ctx, fx.repo, failedDeliveryRun)
		record.Repair.Cycles = []factory.StallRepairCycle{deliveryRecord.Cycle}
		if deliveryRecord.Subject.CycleID != ownerRequest.CycleID || deliveryRecord.Cause.FailedVerificationRunID != ownerRequest.RunID {
			t.Fatal("repair changed its sole requested cycle")
		}
		failed, e := db.AssignmentByRun(ctx, failedDeliveryRun)
		body, _ := json.Marshal(failed)
		if e != nil || string(body) != string(failedVerifierJSON) {
			t.Fatal("repair changed original failed verification", e)
		}
		if again, e := coord.RequestDeliveryRepair(ctx, "native:7", revision.Owner, failedDeliveryRun); e == nil && again.CycleID != cycle.ID {
			t.Fatal("repeated repair created another cycle")
		}
		var afterLedger string
		if e := deliverySQL.QueryRow(`SELECT md5(to_jsonb(l)::text||to_jsonb(r)::text||to_jsonb(u)::text) FROM factory_attempt_allowances l JOIN factory_reservations r ON r.assignment=l.root_assignment JOIN factory_usage u ON u.run=$2 WHERE l.root_assignment=$1`, failedVerifierAssignment, failedDeliveryRun).Scan(&afterLedger); e != nil || afterLedger != failedLedger {
			t.Fatal("repair rewrote closed verifier allowance or original spend", e)
		}
	} else {
		coord.driveStallRepairs(ctx, map[int64]bool{fx.repo: true})
		record, err = db.RepairEpisodeRecord(ctx, fx.repo, event.EpisodeID)
	}
	charges, _ = db.RepairCharges(ctx, fx.repo)
	if err != nil || len(record.Repair.Cycles) != 1 || len(charges) != 2 {
		t.Fatal("graph reshuffle alone refilled repair", err)
	}
}

// TestServeStallRoundGenerationReuseAndIsolation proves the R36 serving
// joints across rounds and repositories on the real store path: the
// detection generation reuses until verified delivery advances it, and a
// stalled repository's bound episode and single charge never leak into a
// sibling repository that still holds ready work.
func TestServeStallRoundGenerationReuseAndIsolation(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const stalled, ready = 61, 62
	grantID := "d1000000000000000000000000000061"
	grant := factory.RepairGrant{
		ID: grantID, Repository: stalled, Sponsor: 7, Revision: 0,
		InvestigatorRuns: 4, ProposalJudgments: 4, ActiveMinutes: 120,
		Active: true, CreatedUnix: 1000,
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	cmd := factory.Command{
		ID: grantID, Type: factory.CommandRepairGrant, Target: factory.RepairTarget(stalled),
		Principal: "soda-tester", Payload: string(raw),
	}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if _, created, err := db.ApplyRepairGrantCommand(ctx, cmd, grant, time.Now().UTC()); err != nil || !created {
		t.Fatalf("apply grant: created=%v err=%v", created, err)
	}
	stallSeedControl(t, db, stalled, 1, factory.ReadinessBlocked,
		factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0))
	stallSeedControl(t, db, ready, 1, factory.ReadinessQueued, factory.ReasonEligible)

	byRepo := func(round StallClassification) map[int64]StallRepositoryResult {
		out := map[int64]StallRepositoryResult{}
		for _, result := range round.Repositories {
			out[result.Repository] = result
		}
		return out
	}
	for round := 1; round <= 2; round++ {
		served, err := coord.ServeStallRound(ctx, DispatchReport{})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if served.Generation != 0 {
			t.Fatalf("round %d generation = %d, want reused 0", round, served.Generation)
		}
		repos := byRepo(served)
		stalledResult, ok := repos[stalled]
		if !ok || stalledResult.Kind != StallRepositoryStall || !stalledResult.Investigate || stalledResult.Event == nil {
			t.Fatalf("round %d stalled=%+v, want one bound episode", round, stalledResult)
		}
		readyResult, ok := repos[ready]
		if !ok || readyResult.Kind != StallRepositoryReady || readyResult.Investigate || readyResult.Event != nil {
			t.Fatalf("round %d ready=%+v, want resumed selection without an episode", round, readyResult)
		}
		if charges, err := db.RepairCharges(ctx, stalled); err != nil || len(charges) != 1 {
			t.Fatalf("round %d stall charges=%d err=%v, want exactly one", round, len(charges), err)
		}
		if charges, err := db.RepairCharges(ctx, ready); err != nil || len(charges) != 0 {
			t.Fatalf("round %d ready charges=%d err=%v, want none", round, len(charges), err)
		}
	}
	standing, err := db.StandingStallEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := standing[stalled]; !ok || entry.Generation != 0 {
		t.Fatalf("standing=%+v, want the stalled generation-0 observation", standing)
	}
	if _, ok := standing[ready]; ok {
		t.Fatalf("standing=%+v, want no observation for the ready repository", standing)
	}
}
