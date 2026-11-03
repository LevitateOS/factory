package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

type appVerificationReads struct{ tip string }

func (r *appVerificationReads) ReadDispatchInputs(_ context.Context, _, _ string, _ []string, target string) (DispatchInputs, error) {
	return DispatchInputs{Tip: r.tip, TipRef: target}, nil
}

func TestApplicationVerificationRequiresApprovedNativeCheck(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx := context.Background()
	fx := dispatchSeed(t, db)
	idea := factory.ProductIdea{ID: phasedDecisionID(31), Description: "A starter can save a task", Consequence: "Tasks persist", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
	task := factory.IntentTask{Key: phasedDecisionID(32), Objective: "Implement task storage", Outcomes: []string{"Tasks persist"}, IdeaIDs: []string{idea.ID}}
	revision := factory.IntentRevision{ID: phasedDecisionID(21), Intent: phasedDecisionID(20), Owner: 7, Repository: 7, Objective: "Task app", State: "accepted", MaxIssues: 10, CreatedUnix: 1, Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{task}, EffectiveOutcomes: []string{idea.ID}}
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	nativeTask := task
	nativeTask.Key = phasedDecisionID(34)
	binding := factory.IntentTaskBinding{RevisionID: revision.ID, TaskKey: nativeTask.Key, IssueID: 103, IssueIndex: 3, AcceptanceID: phasedDecisionID(33), CreatedUnix: 1}
	if err := db.BindIntentTaskAcceptance(ctx, binding); err != nil {
		t.Fatal(err)
	}
	secondTask := nativeTask
	secondTask.Key = phasedDecisionID(35)
	secondBinding := factory.IntentTaskBinding{RevisionID: revision.ID, TaskKey: secondTask.Key, IssueID: 104, IssueIndex: 4, AcceptanceID: phasedDecisionID(36), CreatedUnix: 1}
	if err := db.BindIntentTaskAcceptance(ctx, secondBinding); err != nil {
		t.Fatal(err)
	}
	manifest := factory.ApprovedAppCheck{IntentID: revision.Intent, AcceptedRevision: revision.ID, Outcomes: []string{idea.ID}}
	encoded, _ := json.Marshal(manifest)
	script := []byte("#!/bin/sh\n" + factory.ApprovedAppCheckPrefix + string(encoded) + "\nexit 0\n")
	a := phaseTestRepairAssignment(t, project.ExecutionVerify, project.RoleReviewer, phasedDeliverySubject())
	a.ProjectID = fx.proj
	a.SourceCommit = fx.tip
	seedPreparation, err := db.Preparation(ctx, "f222222222222222222222222")
	if err != nil {
		t.Fatal(err)
	}
	p := seedPreparation.Preparation
	p.ID = a.Preparation
	p.Requirements.SourceInputs = map[string]string{"app.can": factory.MCPInputDigest([]byte("original approved source"))}
	if _, _, err := db.AdmitPreparation(ctx, execution.PreparationRecord{Preparation: p}); err != nil {
		t.Fatal(err)
	}
	a.Subject.Delivery = &factory.DeliverySubject{DeliveryID: strings.Repeat("7", 32), IntentID: revision.Intent, AcceptedRevision: revision.ID, ScopeDigest: revision.Digest(), SourceCommit: fx.tip, PreparationDigest: factory.DeliveryPreparationDigest(p), VerificationManifestDigest: manifest.Digest()}
	zero := 0
	state := execution.PreparationState{Container: strings.Repeat("c", 64), ID: p.ID, Project: p.Project, Role: p.Role, SourceCommit: p.SourceCommit, SetupDigest: p.SetupDigest, Phase: project.PrepareReady, Ready: true, CheckExit: &zero, Output: "task storage passed"}
	report := factory.AppVerificationReport{
		DeliveryID: a.Subject.Delivery.DeliveryID, InputDigest: strings.Repeat("9", 64), Outcome: factory.AppVerificationVerified,
		CheckReceipts: []factory.VerificationCheckReceipt{{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: factory.MCPInputDigest([]byte(state.Output)), LogExcerpt: "task storage passed"}}, Coverage: []factory.ReassessmentCoverage{{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{nativeTask.Key, secondTask.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID, secondBinding.AcceptanceID}}},
	}
	a.Stage, a.Outcome, a.Reason, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, time.Now().Unix()
	a.Result = &factory.AssignmentResult{AssignmentID: a.ID, RunID: a.Run, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application checks passed", Reported: true, RecordedUnix: time.Now().Unix(), PhaseResult: &factory.PhaseResult{Version: 2, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application checks passed", Output: &factory.PhaseOutput{Verification: &report}}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	generation := factory.IntentGeneration{RevisionID: revision.ID, IntentID: revision.Intent, IntentDigest: revision.Digest(), State: "completed", SubmittedTasks: []factory.IntentTask{nativeTask, secondTask}}
	raw, _ := json.Marshal(generation)
	if _, err := sqlDB.Exec(`INSERT INTO factory_intent_generations(revision,data) VALUES($1,$2)`, revision.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(a)
	if _, err := sqlDB.Exec(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,0,$2,'finished',0,$3)`, a.ID, a.Run, string(raw)); err != nil {
		t.Fatal(err)
	}
	// A normal later owner change advances the checked target, without
	// rewriting the Factory's original native merge/closure prerequisite.
	event := factory.CompletionEvent{ID: factory.NewID(), Repository: 7, Issue: 3, Generation: 1, PublicationID: factory.NewID(), AcceptanceID: binding.AcceptanceID, MergedCommit: strings.Repeat("e", 40), ClosingOccurrence: "1"}
	raw, _ = json.Marshal(event)
	if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,1,$1,$2,'1',$3)`, event.ID, event.PublicationID, string(raw)); err != nil {
		t.Fatal(err)
	}
	secondEvent := event
	secondEvent.ID, secondEvent.PublicationID, secondEvent.Generation = factory.NewID(), factory.NewID(), 2
	secondEvent.Issue, secondEvent.AcceptanceID = secondBinding.IssueIndex, secondBinding.AcceptanceID
	raw, _ = json.Marshal(secondEvent)
	if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,2,$1,$2,'1',$3)`, secondEvent.ID, secondEvent.PublicationID, string(raw)); err != nil {
		t.Fatal(err)
	}
	contextFault := ""
	host := &fakeDispatchHost{inspectPreparation: func(project.PrepareInspect) (execution.PreparationState, error) { return state, nil }, contextRead: func(request project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		if len(request.Paths) != 1 || request.Paths[0] != "app.can" || request.ApprovedBase != a.SourceCommit {
			t.Fatal("verification did not select the approved paths at integrated source", request)
		}
		material := fixturePreparationContext(request, role)
		material.Check = script
		material.Files = []project.FactoryContextFile{{Path: "app.can", Content: []byte("example saved_task_persists { save task; read task; expect title == saved title }")}}
		switch contextFault {
		case "foreign-preparation":
			material.ID = "f" + strings.Repeat("e", 24)
		case "stale-source":
			material.Candidate = strings.Repeat("e", 40)
		case "missing-source":
			material.Files = nil
		case "oversize-source":
			material.Files[0].Content = []byte(strings.Repeat("x", project.MaxFactoryContextFileBytes+1))
		}
		return material, nil
	}}
	reads := &appVerificationReads{tip: fx.tip}
	c := &Coordinator{Store: db, Host: host, DispatchReads: reads}
	evidenceSnapshot := func() string {
		t.Helper()
		var digest string
		if err := sqlDB.QueryRow(`SELECT md5(jsonb_build_array((SELECT jsonb_agg(data ORDER BY event) FROM factory_completions),(SELECT jsonb_agg(data ORDER BY id) FROM factory_assignments),(SELECT jsonb_agg(data ORDER BY id) FROM factory_runs),(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM factory_commands c),(SELECT jsonb_agg(data ORDER BY root_assignment) FROM factory_attempt_allowances),(SELECT jsonb_agg(data ORDER BY assignment) FROM factory_reservations),(SELECT jsonb_agg(data ORDER BY run) FROM factory_usage))::text)`).Scan(&digest); err != nil {
			t.Fatal(err)
		}
		return digest
	}
	beforeEvidence := evidenceSnapshot()
	// A native pass receipt alone does not let Test Pilot assess assertions.
	// Preserve the exact authorized script and source, as readable UTF-8, in
	// the same evidence returned by the authenticated worker interface.
	evidence, err := c.ApplicationVerificationEvidence(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	encodedEvidence, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var projected struct {
		Delivery struct {
			IntentID         string `json:"intent_id"`
			AcceptedRevision string `json:"accepted_revision"`
			ScopeDigest      string `json:"scope_digest"`
			SourceCommit     string `json:"source_commit"`
			Tasks            []struct {
				TaskKey      string                  `json:"task_key"`
				OutcomeIDs   []string                `json:"outcome_ids"`
				IssueID      int64                   `json:"issue_id,string"`
				IssueIndex   int64                   `json:"issue_index,string"`
				AcceptanceID string                  `json:"acceptance_id"`
				Completion   factory.CompletionEvent `json:"completion"`
			} `json:"tasks"`
		} `json:"delivery_prerequisites"`
		Context struct {
			Project, ID, Role string
			SourceCommit      string `json:"source_commit"`
			Check             string
			Files             []struct{ Path, Content string }
		} `json:"approved_context"`
	}
	if json.Unmarshal(encodedEvidence, &projected) != nil || projected.Context.Project != p.Project || projected.Context.ID != p.ID || projected.Context.Role != p.Role || projected.Context.SourceCommit != p.SourceCommit || projected.Context.Check != string(script) || len(projected.Context.Files) != 1 || projected.Context.Files[0].Path != "app.can" || !strings.Contains(projected.Context.Files[0].Content, "saved_task_persists") {
		t.Fatal("native evidence omitted readable approved assertions", string(encodedEvidence))
	}
	if projected.Delivery.IntentID != revision.Intent || projected.Delivery.AcceptedRevision != revision.ID || projected.Delivery.ScopeDigest != revision.Digest() || projected.Delivery.SourceCommit != a.SourceCommit || len(projected.Delivery.Tasks) != 2 {
		t.Fatal("native evidence omitted the current delivery prerequisite scope")
	}
	delivered := projected.Delivery.Tasks[0]
	if delivered.TaskKey != nativeTask.Key || delivered.TaskKey == task.Key || len(delivered.OutcomeIDs) != 1 || delivered.OutcomeIDs[0] != idea.ID || delivered.IssueID != binding.IssueID || delivered.IssueIndex != binding.IssueIndex || delivered.AcceptanceID != binding.AcceptanceID || delivered.Completion != event || delivered.Completion.MergedCommit == projected.Delivery.SourceCommit {
		t.Fatal("native evidence lost the canonical task/merge/closure bindings")
	}
	if second := projected.Delivery.Tasks[1]; second.TaskKey != secondTask.Key || second.AcceptanceID != secondBinding.AcceptanceID || second.Completion != secondEvent {
		t.Fatal("native evidence omitted another contributor to the same outcome")
	}
	if strings.Contains(string(encodedEvidence), state.Output) {
		t.Fatal("native evidence exposed unfiltered check output")
	}
	for _, fault := range []string{"foreign-preparation", "stale-source", "missing-source", "oversize-source"} {
		contextFault = fault
		if _, err := c.ApplicationVerificationEvidence(ctx, a); err == nil {
			t.Fatal("verification exposed invalid assertion context", fault)
		}
	}
	contextFault = ""
	if evidenceSnapshot() != beforeEvidence {
		t.Fatal("verification evidence reads changed retained history or spending")
	}
	// Worker prose and a normal successful setup check are insufficient.
	goodScript := script
	script = []byte("#!/bin/sh\nexit 0\n")
	if err := c.verifyApplicationReport(ctx, a); err == nil {
		t.Fatal("ordinary preparation was promoted to application completion")
	}
	script = goodScript
	bad := 1
	state.CheckExit = &bad
	if err := c.verifyApplicationReport(ctx, a); err == nil {
		t.Fatal("failing native check was accepted")
	}
	state.CheckExit = &zero
	reads.tip = strings.Repeat("e", 40)
	if err := c.verifyApplicationReport(ctx, a); err == nil {
		t.Fatal("stale integrated source was accepted")
	}
	reads.tip = fx.tip
	claimed := report.CheckReceipts[0].ArtifactDigest
	report.CheckReceipts[0].ArtifactDigest = strings.Repeat("f", 64)
	if err := c.verifyApplicationReport(ctx, a); err == nil {
		t.Fatal("forged receipt was accepted")
	}
	report.CheckReceipts[0].ArtifactDigest = claimed
	acceptedCoverage := report.Coverage[0]
	report.Coverage = append(report.Coverage, factory.ReassessmentCoverage{IntentID: revision.Intent, OutcomeID: phasedDecisionID(99), TaskKeys: []string{task.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID}})
	if err := c.verifyApplicationReport(ctx, a); err == nil {
		t.Fatal("unknown outcome was accepted as delivered scope")
	}
	report.Coverage = []factory.ReassessmentCoverage{acceptedCoverage}
	// These are syntactically valid worker claims, persisted just like a real
	// finish. Only the current canonical task/acceptance join can reject them.
	for _, foreign := range []factory.ReassessmentCoverage{
		{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{nativeTask.Key, phasedDecisionID(98)}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID, secondBinding.AcceptanceID}},
		{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{nativeTask.Key, secondTask.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID, phasedDecisionID(99)}},
		{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{nativeTask.Key, secondTask.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID, binding.AcceptanceID}},
		{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{nativeTask.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID}},
	} {
		report.Coverage = []factory.ReassessmentCoverage{foreign}
		if err := a.Validate(); err != nil {
			t.Fatal("foreign reference fixture was not a valid worker report", err)
		}
		raw, _ = json.Marshal(a)
		if _, err := sqlDB.Exec(`UPDATE factory_assignments SET data=$2 WHERE id=$1`, a.ID, string(raw)); err != nil {
			t.Fatal(err)
		}
		if err := c.verifyApplicationReport(ctx, a); err == nil {
			t.Fatal("foreign or repeated delivery reference authorized F10", foreign)
		}
		var completed int
		if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->'communication'->>'component_id'='F10'`).Scan(&completed); err != nil || completed != 0 {
			t.Fatal("refused coverage wrote application completion", completed, err)
		}
	}
	report.Coverage = []factory.ReassessmentCoverage{acceptedCoverage}
	raw, _ = json.Marshal(a)
	if _, err := sqlDB.Exec(`UPDATE factory_assignments SET data=$2 WHERE id=$1`, a.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := c.verifyApplicationReport(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := c.verifyApplicationReport(ctx, a); err != nil {
		t.Fatal("completion replay", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->'communication'->>'component_id'='F10'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("trusted completion count %d: %v", count, err)
	}
	entries, _, _, err := db.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	post, err := db.FeedPost(ctx, entries[0].ID)
	if err != nil || post.Actor != "factory" || post.Status != "application:verified" || post.Payload.Communication.ComponentID != "F10" {
		t.Fatalf("completion projection: %+v %v", post, err)
	}
	pending, _, err := db.PendingAppVerifications(ctx, 0, 16)
	if err != nil || len(pending) != 0 {
		t.Fatalf("completed delivery still pending: %+v %v", pending, err)
	}
	// A full stale prefix must not prevent a later, valid report's F10 join.
	// Keep every stale report as history; only queue rotation may skip it.
	ready := ""
	for i := 0; i < 17; i++ {
		var queued factory.Assignment
		if err := json.Unmarshal(raw, &queued); err != nil {
			t.Fatal(err)
		}
		queued.ID, queued.Run = factory.NewID(), factory.NewID()
		queued.RunHistory = []string{queued.Run}
		queued.AttemptRoot = queued.ID
		queued.Subject.Delivery.DeliveryID = factory.NewID()
		queued.Result.AssignmentID, queued.Result.RunID = queued.ID, queued.Run
		queued.Result.PhaseResult.Output.Verification.DeliveryID = queued.Subject.Delivery.DeliveryID
		if i < 16 {
			queued.SourceCommit = strings.Repeat("f", 40)
			queued.Subject.Delivery.SourceCommit = queued.SourceCommit
		} else {
			ready = queued.Subject.Delivery.DeliveryID
		}
		if err := queued.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(queued)
		if _, err := sqlDB.Exec(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,0,$2,'finished',0,$3)`, queued.ID, queued.Run, string(encoded)); err != nil {
			t.Fatal(err)
		}
	}
	c.driveAppVerifications(ctx)
	var readyPosts int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->>'event_identity'=$1 AND data->'communication'->>'component_id'='F10'`, ready).Scan(&readyPosts); err != nil || readyPosts != 0 {
		t.Fatal("report drive exceeded its bounded page", readyPosts, err)
	}
	c.driveAppVerifications(ctx)
	if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->>'event_identity'=$1 AND data->'communication'->>'component_id'='F10'`, ready).Scan(&readyPosts); err != nil || readyPosts != 1 {
		t.Fatal("stale report prefix starved ready seventeenth report", readyPosts, err)
	}
	c.driveAppVerifications(ctx) // wrap and reconcile the retained stale prefix.
	var retained int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_assignments WHERE data->'subject'->'delivery'->>'source_commit'=$1`, strings.Repeat("f", 40)).Scan(&retained); err != nil || retained != 16 {
		t.Fatal("queue rotation changed stale report history", retained, err)
	}
	// A malformed completion cannot lend an unrelated repository's history to
	// the accepted task, even if its indexed issue and acceptance happen to match.
	foreign := event
	foreign.ID, foreign.PublicationID, foreign.Generation, foreign.Repository = factory.NewID(), factory.NewID(), 3, 8
	raw, _ = json.Marshal(foreign)
	if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,3,$1,$2,'1',$3)`, foreign.ID, foreign.PublicationID, string(raw)); err != nil {
		t.Fatal(err)
	}
	beforeEvidence = evidenceSnapshot()
	if _, err := c.ApplicationVerificationEvidence(ctx, a); err == nil || evidenceSnapshot() != beforeEvidence {
		t.Fatal("foreign canonical completion was exposed or changed retained history")
	}
	// A legacy accepted revision carries no explicit effective subset.
	// The decided accept/edit ideas in revision order are the canonical
	// F10 scope: pending/declined excluded, raw bytes and digest
	// unchanged, exact coverage with replay but no duplicates.
	legacyAccept := factory.ProductIdea{ID: phasedDecisionID(40), Description: "A starter can save a task", Consequence: "Tasks persist", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
	legacyEdit := factory.ProductIdea{ID: phasedDecisionID(41), Description: "A starter can rename a task", Consequence: "Titles stay current", Correction: "Rename only with confirmation", Decision: "edit", Source: "requested", Provenance: "human", Actor: 7}
	legacyPending := factory.ProductIdea{ID: phasedDecisionID(42), Description: "Sync to the legacy tracker", Consequence: "Legacy stays current", Decision: "pending", Source: "autonomous", Provenance: "autonomous"}
	legacyDeclined := factory.ProductIdea{ID: phasedDecisionID(43), Description: "Delete tasks automatically", Consequence: "Old tasks vanish", Decision: "decline", Source: "requested", Provenance: "human", Actor: 7}
	legacyTask := factory.IntentTask{Key: phasedDecisionID(44), Objective: "Implement task storage", Outcomes: []string{"Tasks persist"}, IdeaIDs: []string{legacyAccept.ID}}
	legacyCheck := factory.IntentTask{Key: phasedDecisionID(45), Objective: "Confirm task renaming", Outcomes: []string{"Titles stay current"}, IdeaIDs: []string{legacyEdit.ID}}
	legacy := factory.IntentRevision{
		ID: phasedDecisionID(46), Intent: phasedDecisionID(47), Owner: 7, Repository: 7, Objective: "Task app", State: "accepted", MaxIssues: 10, CreatedUnix: 1,
		Ideas: []factory.ProductIdea{legacyAccept, legacyEdit, legacyPending, legacyDeclined}, Tasks: []factory.IntentTask{legacyTask, legacyCheck},
	}
	if err := db.SaveIntentRevision(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	// The independent expectation comes from the fixture's decided
	// accept/edit ideas, never from the raw empty subset under test.
	legacyScope := []string{legacyAccept.ID, legacyEdit.ID}
	if resolved := factory.ResolvedScope(legacy); !slices.Equal(resolved, legacyScope) {
		t.Fatalf("legacy scope resolved=%v want decided accept+edit in revision order", resolved)
	}
	legacyManifest := factory.ApprovedAppCheck{IntentID: legacy.Intent, AcceptedRevision: legacy.ID, Outcomes: append([]string(nil), legacyScope...)}
	if !legacyManifest.BindsIntent(legacy) {
		t.Fatal("legacy accepted scope did not bind its canonical manifest")
	}
	if (factory.ApprovedAppCheck{IntentID: legacy.Intent, AcceptedRevision: legacy.ID, Outcomes: []string{legacyAccept.ID, legacyPending.ID}}).BindsIntent(legacy) {
		t.Fatal("pending substitution bound the canonical manifest")
	}
	legacyEncoded, _ := json.Marshal(legacyManifest)
	script = []byte("#!/bin/sh\n" + factory.ApprovedAppCheckPrefix + string(legacyEncoded) + "\nexit 0\n")
	legacyBinding := factory.IntentTaskBinding{RevisionID: legacy.ID, TaskKey: legacyTask.Key, IssueID: 105, IssueIndex: 5, AcceptanceID: phasedDecisionID(48), CreatedUnix: 1}
	if err := db.BindIntentTaskAcceptance(ctx, legacyBinding); err != nil {
		t.Fatal(err)
	}
	legacySecond := factory.IntentTaskBinding{RevisionID: legacy.ID, TaskKey: legacyCheck.Key, IssueID: 106, IssueIndex: 6, AcceptanceID: phasedDecisionID(49), CreatedUnix: 1}
	if err := db.BindIntentTaskAcceptance(ctx, legacySecond); err != nil {
		t.Fatal(err)
	}
	legacyEvent := factory.CompletionEvent{ID: factory.NewID(), Repository: 7, Issue: 5, Generation: 4, PublicationID: factory.NewID(), AcceptanceID: legacyBinding.AcceptanceID, MergedCommit: strings.Repeat("e", 40), ClosingOccurrence: "1"}
	raw, _ = json.Marshal(legacyEvent)
	if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,4,$1,$2,'1',$3)`, legacyEvent.ID, legacyEvent.PublicationID, string(raw)); err != nil {
		t.Fatal(err)
	}
	legacySecondEvent := legacyEvent
	legacySecondEvent.ID, legacySecondEvent.PublicationID, legacySecondEvent.Generation = factory.NewID(), factory.NewID(), 5
	legacySecondEvent.Issue, legacySecondEvent.AcceptanceID = legacySecond.IssueIndex, legacySecond.AcceptanceID
	raw, _ = json.Marshal(legacySecondEvent)
	if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,5,$1,$2,'1',$3)`, legacySecondEvent.ID, legacySecondEvent.PublicationID, string(raw)); err != nil {
		t.Fatal(err)
	}
	legacySubject := factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &factory.DeliverySubject{DeliveryID: factory.NewID(), IntentID: legacy.Intent, AcceptedRevision: legacy.ID, ScopeDigest: legacy.Digest(), SourceCommit: fx.tip, PreparationDigest: factory.DeliveryPreparationDigest(p), VerificationManifestDigest: legacyManifest.Digest()}}
	b := phaseTestRepairAssignment(t, project.ExecutionVerify, project.RoleReviewer, legacySubject)
	b.ProjectID, b.SourceCommit, b.Preparation = fx.proj, fx.tip, a.Preparation
	// The saved legacy revision bytes are frozen before any journey or
	// adoption work: the pure projection must never rewrite history.
	var legacyRevisionBefore []byte
	if err := sqlDB.QueryRow(`SELECT data FROM factory_intent_revisions WHERE id=$1`, legacy.ID).Scan(&legacyRevisionBefore); err != nil {
		t.Fatal(err)
	}
	// The journey observation is recorded against the live assigned
	// delivery through genuine store admission, before the worker's final
	// report exists: the Test Pilot capability stages and activates under
	// the production dispatch/preparation gates.
	var legacyDispatchOpen bool
	if err := sqlDB.QueryRow(`SELECT open FROM factory_dispatch WHERE repository=7`).Scan(&legacyDispatchOpen); err != nil {
		if _, err := sqlDB.Exec(`INSERT INTO factory_dispatch(repository,revision,open,data) VALUES(7,1,true,'{}')`); err != nil {
			t.Fatal(err)
		}
	}
	legacyPrep, err := db.Preparation(ctx, b.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	legacyCap := factory.MCPCapability{
		ID: factory.NewID(), AssignmentID: b.ID, RunID: b.Run, Generation: 1,
		Profile: factory.MCPProfileTestPilot, Subject: b.Subject.SubjectKey(b.Repository),
		Repository: b.Repository, Project: b.ProjectID,
		PreparationDigest: legacyPrep.Preparation.SetupDigest,
		SourceDigest:      b.SourceCommit, AcceptedRevision: strconv.FormatInt(b.NativeRev, 10),
		Authority:    factory.MCPInputDigest([]byte("legacy journey authority")),
		DeadlineUnix: time.Now().Add(time.Hour).Unix(), State: factory.MCPCapabilityStaged,
	}
	if err := legacyCap.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(b)
	if _, err := sqlDB.Exec(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,0,$2,'assigned',0,$3)`, b.ID, b.Run, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := db.StageMCPCapability(ctx, legacyCap); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateObservedMCPCapability(ctx, legacyCap); err != nil {
		t.Fatal(err)
	}
	legacyCap, err = db.GetMCPCapability(ctx, legacyCap.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacyJourney := factory.VerificationJourneyResult{
		DeliveryID: legacySubject.Delivery.DeliveryID, InputDigest: legacyCap.ScopeDigest(),
		SourceCommit: b.SourceCommit, Preparation: b.Preparation,
		ManifestDigest: legacySubject.Delivery.VerificationManifestDigest,
		OutcomeIDs:     append([]string(nil), legacyScope...),
		Name:           "Retained legacy readback", Steps: []string{"Save a task", "Read back its title"},
		Expected: "The requested title persists.", Observed: "The native assertion reports a saved title.",
		Result:      "passed",
		Limitations: []string{"Legacy scope resolves purely; no new authority is granted."},
	}
	legacyJourneyID := factory.NewID()
	rawJourney, _ := json.Marshal(legacyJourney)
	legacyJourneySub := testMCPSubmission(legacyCap, factory.MCPToolRecordJourneyResult, legacyJourneyID, rawJourney)
	journeyPost, err := db.RecordWorkerPost(ctx, legacyJourneySub)
	if err != nil {
		t.Fatal("legacy journey observation refused", err)
	}
	legacyJourneyID = journeyPost.Record.ID
	// Terminal finish-reference validation refuses bad references while the
	// delivery is still assigned, before any adoption: a malformed ref and
	// an unknown but well-formed ref admit nothing.
	legacyFinishReport := factory.AppVerificationReport{
		DeliveryID: legacySubject.Delivery.DeliveryID, InputDigest: legacyCap.ScopeDigest(), Outcome: factory.AppVerificationVerified,
		CheckReceipts: []factory.VerificationCheckReceipt{{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: factory.MCPInputDigest([]byte(state.Output)), LogExcerpt: "task storage passed"}},
		Coverage: []factory.ReassessmentCoverage{
			{IntentID: legacy.Intent, OutcomeID: legacyAccept.ID, TaskKeys: []string{legacyTask.Key}, DeliveredAcceptanceIDs: []string{legacyBinding.AcceptanceID}},
			{IntentID: legacy.Intent, OutcomeID: legacyEdit.ID, TaskKeys: []string{legacyCheck.Key}, DeliveredAcceptanceIDs: []string{legacySecond.AcceptanceID}},
		},
	}
	for name, ref := range map[string]string{"malformed journey reference": "journey:invalid", "unknown journey observation": "journey:" + factory.NewID()} {
		bad := legacyFinishReport
		bad.EvidenceRefs = []string{ref}
		rawBad, _ := json.Marshal(bad)
		badSub := testMCPSubmission(legacyCap, factory.MCPToolFinishAppVerify, factory.NewID(), rawBad)
		if _, err := db.RecordMCPSubmission(ctx, badSub); err == nil {
			t.Fatalf("terminal finish admitted %s", name)
		}
	}
	// Final adoption cites the recorded observation: the worker report
	// carries journey:<submission> into verifyApplicationReport, which
	// rechecks the immutable observation at RecordVerifiedApplication
	// before posting F10.
	legacyReport := legacyFinishReport
	legacyReport.EvidenceRefs = []string{"journey:" + legacyJourneyID}
	b.Stage, b.Outcome, b.Reason, b.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, time.Now().Unix()
	b.Result = &factory.AssignmentResult{AssignmentID: b.ID, RunID: b.Run, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application checks passed", Reported: true, RecordedUnix: time.Now().Unix(), PhaseResult: &factory.PhaseResult{Version: 2, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application checks passed", Output: &factory.PhaseOutput{Verification: &legacyReport}}}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(b)
	if _, err := sqlDB.Exec(`UPDATE factory_assignments SET stage='finished', data=$2 WHERE id=$1`, b.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	storeLegacy := func() {
		t.Helper()
		raw, _ = json.Marshal(b)
		if _, err := sqlDB.Exec(`UPDATE factory_assignments SET data=$2 WHERE id=$1`, b.ID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	exactCoverage := legacyReport.Coverage
	// Same-cardinality pending/foreign substitution is not exact coverage.
	for name, outcome := range map[string]string{"pending revival": legacyPending.ID, "declined revival": legacyDeclined.ID, "foreign outcome": phasedDecisionID(99)} {
		legacyReport.Coverage = []factory.ReassessmentCoverage{exactCoverage[0], {IntentID: legacy.Intent, OutcomeID: outcome, TaskKeys: []string{legacyCheck.Key}, DeliveredAcceptanceIDs: []string{legacySecond.AcceptanceID}}}
		b.Result.PhaseResult.Output.Verification = &legacyReport
		storeLegacy()
		if err := c.verifyApplicationReport(ctx, b); err == nil {
			t.Fatalf("same-cardinality %s authorized legacy F10", name)
		}
	}
	legacyReport.Coverage = exactCoverage
	b.Result.PhaseResult.Output.Verification = &legacyReport
	storeLegacy()
	if err := c.verifyApplicationReport(ctx, b); err != nil {
		t.Fatal("legacy accepted-empty scope refused its exact coverage", err)
	}
	if err := c.verifyApplicationReport(ctx, b); err != nil {
		t.Fatal("legacy completion replay", err)
	}
	var legacyF10 int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->'communication'->>'component_id'='F10' AND data->>'event_identity'=$1`, legacySubject.Delivery.DeliveryID).Scan(&legacyF10); err != nil || legacyF10 != 1 {
		t.Fatalf("legacy F10 posts=%d err=%v want exactly one", legacyF10, err)
	}
	if count, err := db.VerifiedApplicationOutcomeCount(ctx, legacy); err != nil || count != len(legacyScope) {
		t.Fatalf("legacy evidenced count=%d err=%v want %d", count, err, len(legacyScope))
	}
	var legacyRaw []byte
	if err := sqlDB.QueryRow(`SELECT data FROM factory_intent_revisions WHERE id=$1`, legacy.ID).Scan(&legacyRaw); err != nil || strings.Contains(string(legacyRaw), "effective_outcomes") {
		t.Fatal("legacy revision raw bytes gained an effective subset", err)
	}
	var reread factory.IntentRevision
	if err := json.Unmarshal(legacyRaw, &reread); err != nil || reread.Digest() != legacy.Digest() {
		t.Fatal("legacy revision digest moved under the pure projection", err)
	}
	if string(legacyRaw) != string(legacyRevisionBefore) {
		t.Fatal("journey record and final adoption rewrote saved legacy revision bytes")
	}
	var adopted factory.Assignment
	if err := sqlDB.QueryRow(`SELECT data FROM factory_assignments WHERE id=$1`, b.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &adopted) != nil ||
		adopted.Result == nil || adopted.Result.PhaseResult == nil || adopted.Result.PhaseResult.Output == nil ||
		adopted.Result.PhaseResult.Output.Verification == nil || len(adopted.Result.PhaseResult.Output.Verification.EvidenceRefs) != 1 ||
		adopted.Result.PhaseResult.Output.Verification.EvidenceRefs[0] != "journey:"+legacyJourneyID {
		t.Fatal("final adoption lost its canonical legacy journey reference", err)
	}
	script = goodScript
}
