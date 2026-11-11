package store

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// FW32 material cutover: the producer captures the complete snapshot in
// the SAME repeatable-read tx CaptureCompletionLocal holds. Proven on
// the disposable SODA_PG_* fixture; skips without it.
func materialScopeRevision(intent, revision string) factory.IntentRevision {
	idea := "d" + strings.Repeat("c", 24)
	return factory.IntentRevision{
		ID: revision, Intent: intent,
		Owner: 7, Repository: 7, Objective: "accepted objective",
		Ideas: []factory.ProductIdea{{
			ID: idea, Description: "effective choice", Consequence: "consequence",
			Decision: "accept", Source: "supported", Provenance: "autonomous", Evidence: "evidence",
		}},
		Tasks: []factory.IntentTask{{
			Key: "d" + strings.Repeat("e", 24), Objective: "authorized task",
			Outcomes: []string{"done"}, IdeaIDs: []string{idea},
		}},
		State:             "accepted",
		MaxIssues:         2,
		EffectiveOutcomes: []string{idea},
		CreatedUnix:       time.Now().Unix(),
	}
}

// An unaccepted generation task body must never enter the model-safe
// projection: CaptureCompletionLocal refuses the WHOLE capture when a
// submitted task references a pending idea, even though
// BeginIntentGeneration admits unique well-formed keys. The generation
// admission gate stays a separate baseline concern; capture fails
// closed here.
func TestCaptureCompletionMaterialRefusesUnacceptedSubmitted(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	pending := "d" + strings.Repeat("d", 24)
	accepted := "d" + strings.Repeat("c", 24)
	intent := "d" + strings.Repeat("b", 24)
	rev := "d" + strings.Repeat("a", 24)
	revision := factory.IntentRevision{
		ID: rev, Intent: intent,
		Owner: 7, Repository: 7, Objective: "accepted objective",
		Ideas: []factory.ProductIdea{{
			ID: accepted, Description: "effective choice", Consequence: "consequence",
			Decision: "accept", Source: "supported", Provenance: "autonomous", Evidence: "evidence",
		}, {
			ID: pending, Description: "pending choice",
			Consequence: "consequence", Decision: "pending",
		}},
		Tasks: []factory.IntentTask{{
			Key: "d" + strings.Repeat("e", 24), Objective: "authorized task",
			Outcomes: []string{"done"}, IdeaIDs: []string{accepted},
		}},
		State:             "accepted",
		MaxIssues:         2,
		EffectiveOutcomes: []string{accepted},
		CreatedUnix:       time.Now().Unix(),
	}
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatalf("revision: %v", err)
	}
	generation := factory.IntentGeneration{
		RevisionID: rev, IntentID: intent, OperationID: "op-1", ActorID: 7,
		NativeRev: 1, NotAfter: time.Now().Add(time.Hour).Unix(),
		Payload:      json.RawMessage(`{"edits":[]}`),
		IntentDigest: revision.Digest(),
		SubmittedTasks: []factory.IntentTask{{
			Key: "d" + strings.Repeat("f", 24), Objective: "sentinel unaccepted objective",
			Outcomes: []string{"done"}, IdeaIDs: []string{pending},
		}},
		State: "pending",
	}
	if err := db.BeginIntentGeneration(ctx, generation, factory.GraphPacketFacts{Canonical: generation.Payload}); err != nil {
		t.Fatalf("generation: %v", err)
	}
	_, err := db.CaptureCompletionLocal(ctx, 7)
	if err == nil {
		t.Fatal("capture exported an unaccepted submitted task body")
	}
	if !strings.Contains(err.Error(), "unresolved scope") {
		t.Fatalf("err=%v, want the submitted-task authorization refusal", err)
	}
}

// Admitted accepted history stays retained and canonically ordered
// regardless of lexical ID order: LatestAccepted follows the immutable
// predecessor chain, while the final projection sorts by
// (IntentID, RevisionID). Both ID directions are proven; a private
// draft successor adds head metadata without leaking its body.
func TestCaptureCompletionMaterialOrdersAcceptedHistory(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	chain := func(intent, oldID, newID string) {
		t.Helper()
		old := materialScopeRevision(intent, oldID)
		if err := db.SaveIntentRevision(ctx, old); err != nil {
			t.Fatalf("old revision: %v", err)
		}
		recent := materialScopeRevision(intent, newID)
		recent.Predecessor = oldID
		if err := db.SaveIntentRevision(ctx, recent); err != nil {
			t.Fatalf("new revision: %v", err)
		}
	}
	chain("d"+strings.Repeat("b", 24), "d"+strings.Repeat("a", 24), "d"+strings.Repeat("f", 24))
	chain("d"+strings.Repeat("c", 24), "d"+strings.Repeat("e", 24), "d"+strings.Repeat("1", 24))
	local, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("captured local invalid: %v", err)
	}
	byIntent := make(map[string][]factory.CompletionIntentScope)
	for _, scope := range local.Material.Intents {
		byIntent[scope.IntentID] = append(byIntent[scope.IntentID], scope)
	}
	for intent, newID := range map[string]string{
		"d" + strings.Repeat("b", 24): "d" + strings.Repeat("f", 24),
		"d" + strings.Repeat("c", 24): "d" + strings.Repeat("1", 24),
	} {
		scopes := byIntent[intent]
		if len(scopes) != 2 {
			t.Fatalf("intent %s scopes=%d, want both accepted bodies retained", intent, len(scopes))
		}
		if scopes[0].RevisionID >= scopes[1].RevisionID {
			t.Fatalf("intent %s order=[%s %s], want canonical ascending", intent, scopes[0].RevisionID, scopes[1].RevisionID)
		}
		for _, scope := range scopes {
			if len(scope.Ideas) != 1 || len(scope.Tasks) != 1 {
				t.Fatalf("scope %s lost its accepted body", scope.RevisionID)
			}
			if (scope.RevisionID == newID) != scope.LatestAccepted {
				t.Fatalf("scope %s latest=%v, want latest exactly on chain newest %s",
					scope.RevisionID, scope.LatestAccepted, newID)
			}
		}
	}
	draft := materialScopeRevision("d"+strings.Repeat("b", 24), "d"+strings.Repeat("d", 24))
	draft.Predecessor = "d" + strings.Repeat("f", 24)
	draft.State = "draft"
	if err := db.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatalf("draft successor: %v", err)
	}
	after, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture after draft: %v", err)
	}
	count := 0
	for _, scope := range after.Material.Intents {
		if scope.IntentID == "d"+strings.Repeat("b", 24) {
			count++
			if scope.RevisionID == draft.ID {
				t.Fatal("private draft body leaked into scopes")
			}
			if scope.CurrentState != "draft" {
				t.Fatalf("scope state=%s, want draft head metadata", scope.CurrentState)
			}
		}
	}
	if count != 2 {
		t.Fatalf("scopes=%d after draft, want both accepted retained", count)
	}
}

// Plan sections follow canonical root order, not assignment
// first-encounter: random root/child IDs must still emit ascending
// (root, plan, task) sections with root-join semantics retained.
func TestCompletionPlansTxSortsRoots(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	rootF := strings.Repeat("f", 32)
	rootB := strings.Repeat("b", 32)
	planF := factory.NewID()
	planB := factory.NewID()
	if err := db.SaveInvestigationPlan(ctx, planRevisionFixture(rootF, planF, 0, "Investigate, plan, then implement behind checks.")); err != nil {
		t.Fatalf("plan F: %v", err)
	}
	if err := db.SaveInvestigationPlan(ctx, planRevisionFixture(rootB, planB, 0, "Investigate, plan, then implement behind checks.")); err != nil {
		t.Fatalf("plan B: %v", err)
	}
	if err := db.AdvancePlanHead(ctx, rootF, "", planF); err != nil {
		t.Fatalf("head F: %v", err)
	}
	if err := db.AdvancePlanHead(ctx, rootB, "", planB); err != nil {
		t.Fatalf("head B: %v", err)
	}
	for root, plan := range map[string]string{rootF: planF, rootB: planB} {
		receipt := PlanTaskReceipt{
			AttemptRoot: root, PlanID: plan, TaskID: "design",
			Revision: 0, State: PlanTaskPending,
		}
		if err := db.SavePlanTaskReceipt(ctx, receipt); err != nil {
			t.Fatalf("receipt %s: %v", root[:4], err)
		}
	}
	// Encounter fff before bbb: child assignment first, its root second.
	assignments := []factory.CompletionAssignment{
		{ID: strings.Repeat("a", 32), AttemptRoot: rootF},
		{ID: rootB, AttemptRoot: rootB},
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	plans, heads, tasks, err := completionPlansTx(ctx, tx, assignments)
	if err != nil {
		t.Fatalf("plans: %v", err)
	}
	if len(plans) != 2 || plans[0].AttemptRoot != rootB || plans[1].AttemptRoot != rootF {
		t.Fatalf("plans=%+v, want root-canonical order independent of encounter", plans)
	}
	if plans[0].ID != planB || plans[1].ID != planF {
		t.Fatal("plan refs lost their root join")
	}
	if len(heads) != 2 || heads[0].AttemptRoot != rootB || heads[1].AttemptRoot != rootF {
		t.Fatalf("heads=%+v, want root-canonical order", heads)
	}
	if len(tasks) != 2 || tasks[0].AttemptRoot != rootB || tasks[1].AttemptRoot != rootF {
		t.Fatalf("tasks=%+v, want root-canonical order", tasks)
	}
}

func TestCaptureCompletionMaterialComplete(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()

	v1 := acceptanceFixture("d0123456789abcdef01234567", "")
	if err := db.AdmitAcceptanceDecision(ctx, v1); err != nil {
		t.Fatal(err)
	}
	intent := "d" + strings.Repeat("b", 24)
	rev := "d" + strings.Repeat("a", 24)
	revision := materialScopeRevision(intent, rev)
	if err := db.SaveIntentRevision(ctx, revision); err != nil {
		t.Fatalf("revision: %v", err)
	}
	generation := factory.IntentGeneration{
		RevisionID: rev, IntentID: intent, OperationID: "op-1", ActorID: 7,
		NativeRev: 1, NotAfter: time.Now().Add(time.Hour).Unix(),
		Payload:        json.RawMessage(`{"edits":[]}`),
		IntentDigest:   revision.Digest(),
		SubmittedTasks: append([]factory.IntentTask(nil), revision.Tasks...),
		State:          "pending",
	}
	if err := db.BeginIntentGeneration(ctx, generation, factory.GraphPacketFacts{Canonical: generation.Payload}); err != nil {
		t.Fatalf("generation: %v", err)
	}
	binding := factory.IntentTaskBinding{
		RevisionID: rev, TaskKey: revision.Tasks[0].Key,
		IssueID: 3, IssueIndex: 3, ContentVersion: 2, AcceptanceID: v1.ID,
	}
	if err := db.BindIntentTaskAcceptance(ctx, binding); err != nil {
		t.Fatalf("binding: %v", err)
	}
	v2 := acceptanceFixture("d123456789abcdef012345678", v1.ID)
	v2.NativeRev = 43
	if err := db.AdmitAcceptanceDecision(ctx, v2); err != nil {
		t.Fatalf("successor acceptance: %v", err)
	}
	draft := revision
	draft.ID = "d" + strings.Repeat("f", 24)
	draft.Predecessor = rev
	draft.State = "draft"
	if err := db.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatalf("draft successor: %v", err)
	}

	a, r, run, view := dispatchTestPacket(t, time.Now().UTC())
	a.Acceptance = v2.ID
	a.Subject.Issue.Acceptance = v2.ID
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatalf("packet: %v", err)
	}
	plan := planRevisionFixture(a.ID, factory.NewID(), 0, "Investigate, plan, then implement behind checks.")
	if err := db.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := db.AdvancePlanHead(ctx, a.ID, "", plan.ID); err != nil {
		t.Fatalf("plan head: %v", err)
	}
	receipt := PlanTaskReceipt{
		AttemptRoot: a.ID, PlanID: plan.ID, TaskID: "design",
		Revision: 0, State: PlanTaskPending,
	}
	if err := db.SavePlanTaskReceipt(ctx, receipt); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if _, err := db.RecordCheckAssessment(ctx, checkAssessmentFixture()); err != nil {
		t.Fatalf("check: %v", err)
	}
	p := publicationTestRecord()
	p.ID, p.AssignmentID, p.Run, p.Acceptance = factory.NewID(), a.ID, a.Run, v2.ID
	p.ProjectID = dispatchTestProjectID()
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("publication: %v", err)
	}

	first, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("captured local invalid: %v", err)
	}
	material := first.Material
	if material.Repository != 7 {
		t.Fatalf("repository=%d, want 7", material.Repository)
	}
	if len(material.Intents) != 1 {
		t.Fatalf("intents=%d, want the one admitted accepted revision", len(material.Intents))
	}
	scope := material.Intents[0]
	if scope.RevisionID != rev || !scope.LatestAccepted || scope.CurrentState != "draft" {
		t.Fatalf("scope=%+v, want the accepted revision latest under its draft head", scope)
	}
	if len(scope.Ideas) != 1 || len(scope.Tasks) != 1 || len(scope.Bindings) != 1 {
		t.Fatalf("scope=%+v, want effective scope with its historical binding", scope)
	}
	if scope.Bindings[0].AcceptanceID != v1.ID || scope.Bindings[0].CreatedUnix != 0 {
		t.Fatalf("binding=%+v, want the historical acceptance without observation time", scope.Bindings[0])
	}
	if len(material.Acceptances) != 2 {
		t.Fatalf("acceptances=%d, want head plus historically referenced body", len(material.Acceptances))
	}
	if material.Acceptances[0].ID != v1.ID || material.Acceptances[1].ID != v2.ID {
		t.Fatalf("acceptances=%v, want ordered head history", material.Acceptances)
	}
	if len(material.Assignments) != 1 || len(material.Assignments[0].RunHistory) != 1 {
		t.Fatalf("assignments=%+v, want the dispatched work with its run state", material.Assignments)
	}
	assignment := material.Assignments[0]
	if assignment.Reservation == nil || assignment.Reservation.State == "" {
		t.Fatalf("assignment=%+v, want its reservation state", assignment)
	}
	if len(assignment.RunHistory[0].ID) == 0 {
		t.Fatal("run history lost its run identity")
	}
	if len(material.Plans) != 1 || len(material.PlanHeads) != 1 || len(material.PlanTasks) != 1 {
		t.Fatalf("plans=%d heads=%d tasks=%d, want the retained revision with head and receipt",
			len(material.Plans), len(material.PlanHeads), len(material.PlanTasks))
	}
	if len(material.Publications) != 1 || material.Publications[0].AssignmentID != a.ID {
		t.Fatalf("publications=%+v, want the recorded publication", material.Publications)
	}
	if len(material.Checks) != 1 {
		t.Fatalf("checks=%d, want the recorded verdict", len(material.Checks))
	}
	if len(material.Controls) != 1 || material.Preparations == nil {
		t.Fatalf("controls=%d preparations=%+v, want sealed control and explicit preparations",
			len(material.Controls), material.Preparations)
	}
	if len(material.Authority.Roles) != 1 || material.Authority.Roles[0] != "soda-coder" {
		t.Fatalf("authority=%+v, want the sponsored role subset, not the configured set", material.Authority)
	}
	if material.Authority.ReassessmentMinutes != factory.DefaultReassessmentMinutes {
		t.Fatalf("authority=%+v, want the admitted minutes", material.Authority)
	}
	if material.Authority.Effective.Missing == nil {
		t.Fatal("authority verdict is not canonical empty")
	}
	digest, err := material.Digest()
	if err != nil || digest != first.ScopeDigest {
		t.Fatalf("digest=%q scope=%q err=%v, want ScopeDigest exactly the material digest", digest, first.ScopeDigest, err)
	}

	second, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil || !second.Equal(first) {
		t.Fatalf("second=%+v err=%v, want the stable capture", second, err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 777); err == nil {
		t.Fatal("unknown repository captured instead of refusing")
	}

	queued := factory.IssueControl{
		Repository: 7, Issue: 3, Acceptance: v2.ID,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	now := time.Now().UTC()
	stored, changed, err := db.RecordIssueAssessment(ctx, queued, now)
	if err != nil {
		t.Fatalf("control refresh: %v", err)
	}
	_ = stored
	_ = changed
	observed, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil || !observed.Equal(first) {
		t.Fatalf("observed=%+v err=%v, want observation-only refresh to leave the capture unchanged", observed, err)
	}
	blocked := queued
	blocked.Readiness = factory.ReadinessBlocked
	blocked.Reason = factory.BlockerAcceptanceMissing
	blocked.Blockers = []factory.Blocker{{
		Code:       factory.BlockerAcceptanceMissing,
		Resolution: factory.BlockerResolution(factory.BlockerAcceptanceMissing),
	}}
	blocked.Fingerprint = strings.Repeat("3", 64)
	if _, changed, err := db.RecordIssueAssessment(ctx, blocked, now.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("blocked assessment not recorded: changed=%v err=%v", changed, err)
	}
	moved, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil || moved.Equal(first) {
		t.Fatalf("moved=%+v err=%v, want material control change to move the capture", moved, err)
	}

	// Provider facts are declared fixture inputs. Every publication and owner
	// validates canonically; captures resolve their real PostgreSQL provenance.
	writeAssignment := func(owner factory.Assignment, execution *factory.Run) {
		t.Helper()
		if err := owner.Validate(); err != nil {
			t.Fatalf("external fixture owner: %v", err)
		}
		data, err := json.Marshal(owner)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, owner.ID, owner.Repository, owner.Issue, owner.Run, owner.Stage, owner.Revision, string(data)); err != nil {
			t.Fatal(err)
		}
		if execution != nil {
			if err := execution.Validate(); err != nil {
				t.Fatalf("external fixture run: %v", err)
			}
			data, err := json.Marshal(execution)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,FALSE,TRUE,$2)`, execution.ID, string(data)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4)`, execution.ID, owner.Repository, owner.Issue, owner.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	writePublication := func(publication factory.Publication, insert bool) {
		t.Helper()
		if err := publication.Validate(); err != nil {
			t.Fatalf("external fixture publication: %v", err)
		}
		data, err := json.Marshal(publication)
		if err != nil {
			t.Fatal(err)
		}
		if insert {
			_, err = db.db.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, publication.AssignmentID, publication.Repository, publication.Issue, publication.Run, publication.Stage, publication.Revision, string(data))
		} else {
			_, err = db.db.ExecContext(ctx, `UPDATE factory_publications SET run=$2,data=$3 WHERE assignment=$1`, publication.AssignmentID, publication.Run, string(data))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	planning, _, planningRun, _ := finishedPublishableAssignment(t, now, 3, true, "completed")
	planning.Acceptance, planning.Subject.Issue.Acceptance = v2.ID, v2.ID
	planning.NativeTool = &execution.NativeTool{Harness: project.FactoryHarnessMuse, Version: "fixture", SHA256: strings.Repeat("a", 64), Executable: "/declared/fixture/muse"}
	planning.Harness, planning.HarnessVers = project.FactoryHarnessMuse+"-fixture", "fixture"
	planningRun.Harness = planning.Harness
	planningRun.Outcome, planningRun.Reconciled = factory.Succeeded, true
	planningPlan := phaseTestPlan(t, planning.ID, planning.SourceCommit, planning.Preparation)
	planning.Result.Candidate, planning.Result.Phase = "", project.ExecutionInvestigate
	planning.Result.PhaseResult = &factory.PhaseResult{Version: 2, Phase: project.ExecutionInvestigate, Status: "completed", Summary: "declared completed plan", Output: &factory.PhaseOutput{Plan: &planningPlan, PlanReview: strings.Repeat("e", 64)}}
	writeAssignment(planning, &planningRun)
	if err := db.SaveInvestigationPlan(ctx, planningPlan); err != nil {
		t.Fatal(err)
	}
	if err := db.AdvancePlanHead(ctx, planning.ID, "", planningPlan.ID); err != nil {
		t.Fatal(err)
	}
	externalPublication := func(owner factory.Assignment, number int64) factory.Publication {
		t.Helper()
		publication := publicationTestRecord()
		publication.ID, publication.AssignmentID, publication.Run = owner.ID, owner.ID, ""
		publication.Authority, publication.Acceptance, publication.Preparation = owner.Authority, owner.Acceptance, owner.Preparation
		publication.BaseSHA, publication.Candidate = owner.SourceCommit, strings.Repeat("2", 40)
		publication.PRNumber, publication.PRID = number, number+1000
		publication.Stage, publication.Outcome, publication.Reason = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked
		publication.CreatedUnix, publication.FinishedUnix = now.Unix(), now.Unix()
		publication.External = &factory.ExternalPublicationOrigin{CommandID: owner.ID, Host: collaboration.HostID(strings.Repeat("a", 32)), Profile: collaboration.ProfileID(strings.Repeat("b", 32)), Connection: collaboration.ConnectionID(strings.Repeat("c", 32)), ActorID: 7, Policy: 1, ObservationDigest: strings.Repeat("d", 64), ObservationRevision: 1, ObservedUnix: now.Unix(),
			Target: factory.PublicationPullTarget{AuthorID: 8, IssueID: number + 2000, HeadRepository: 7, HeadRef: "refs/heads/external-material", BaseRef: publication.TargetBranch, HeadOID: publication.Candidate, BaseOID: publication.BaseSHA}}
		return publication
	}
	legacyExternal := externalPublication(planning, 901)
	writePublication(legacyExternal, true)
	retainedID := factory.NewID()
	retainedOwner := factory.Assignment{ID: retainedID, AttemptRoot: retainedID, PublicationAssignment: retainedID, Subject: planning.Subject, ProjectID: planning.ProjectID, Role: project.RoleCoder, Authority: planning.Authority, ActorID: planning.ActorID, Acceptance: planning.Acceptance, Preparation: planning.Preparation, SourceCommit: planning.SourceCommit, Stage: factory.AssignmentRetained, Repository: 7, Issue: 3, NativeRev: 1, CreatedUnix: now.Unix(),
		RetainedPublication: &factory.RetainedPublication{AdoptedFrom: legacyExternal.ID, Publication: legacyExternal.ID, Assignment: planning.ID, Run: planning.Run, Candidate: legacyExternal.Candidate, Connection: planning.Connection}}
	writeAssignment(retainedOwner, nil)
	retainedExternal := externalPublication(retainedOwner, 902)
	writePublication(retainedExternal, true)
	canonicalID := factory.NewID()
	canonicalOwner := retainedOwner
	canonicalOwner.ID, canonicalOwner.AttemptRoot, canonicalOwner.PublicationAssignment = canonicalID, canonicalID, canonicalID
	canonicalOwner.RetainedPublication = nil
	canonicalExternal := externalPublication(canonicalOwner, 903)
	canonicalOwner.ExternalPublication = &factory.ExternalPublicationOwner{PlanAssignment: planning.ID, Connection: planning.Connection, Origin: *canonicalExternal.External}
	writeAssignment(canonicalOwner, nil)
	writePublication(canonicalExternal, true)
	externalCapture, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("runless external publications blocked complete material: %v", err)
	}
	assertExternal := func(local factory.CompletionLocal, expected factory.Publication) {
		t.Helper()
		for _, publication := range local.Material.Publications {
			if publication.ID == expected.ID {
				if publication.Run != expected.Run || publication.Candidate != expected.Candidate || !reflect.DeepEqual(publication.External, expected.External) {
					t.Fatalf("external publication provenance changed: %+v", publication)
				}
				return
			}
		}
		t.Fatalf("external publication %s was omitted", expected.ID)
	}
	for _, publication := range []factory.Publication{legacyExternal, retainedExternal, canonicalExternal} {
		assertExternal(externalCapture, publication)
	}
	canonicalFound := false
	for _, owner := range externalCapture.Material.Assignments {
		if owner.ID == canonicalID {
			canonicalFound = reflect.DeepEqual(owner.External, canonicalOwner.ExternalPublication) && len(owner.RunHistory) == 0
		}
	}
	if !canonicalFound {
		t.Fatal("non-executing external owner lost its real planning provenance")
	}
	correction, _, correctionRun, _ := finishedPublishableAssignment(t, now, 3, true, "completed")
	correction.Acceptance, correction.Subject.Issue.Acceptance = v2.ID, v2.ID
	correction.ParentAssignment, correction.AttemptRoot, correction.PublicationAssignment = retainedID, retainedID, retainedID
	correction.Phase, correction.SourceCommit, correction.Result.Candidate = project.ExecutionCorrect, retainedExternal.Candidate, strings.Repeat("3", 40)
	correctionRun.InputSHA, correctionRun.Outcome, correctionRun.Reconciled = correction.SourceCommit, factory.Succeeded, true
	writeAssignment(correction, &correctionRun)
	correctedExternal := retainedExternal
	correctedExternal.Run, correctedExternal.Candidate = correction.Run, correction.Result.Candidate
	operationID := factory.PublicationOperationID(correctedExternal.ID, factory.OpRefPublish, 2)
	operation := publicationStoreIntent(operationID)
	operation.TargetBranch, operation.ComparisonRef, operation.ComparisonOID = correctedExternal.TargetBranch, correctedExternal.TargetBranch, correctedExternal.BaseSHA
	operation.Candidate, operation.ExpectedOld = correctedExternal.Candidate, retainedExternal.Candidate
	operation.CorrectionNumber, operation.CorrectionAuthor = correctedExternal.PRNumber, correctedExternal.External.Target.AuthorID
	correctedExternal.Corrections = []factory.PublicationOperation{{Work: operation, RunID: correction.Run, OperationID: operationID, Kind: factory.OpRefPublish, Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, Attempts: 1, Receipt: "declared-correction-receipt", UpdatedUnix: now.Unix()}}
	writePublication(correctedExternal, false)
	continuationID := factory.NewID()
	continuationOwner := retainedOwner
	continuationOwner.ID, continuationOwner.AttemptRoot, continuationOwner.PublicationAssignment = continuationID, continuationID, continuationID
	continuationOwner.RetainedPublication = &factory.RetainedPublication{AdoptedFrom: correctedExternal.ID, Publication: legacyExternal.ID, Assignment: planning.ID, Run: correction.Run, Candidate: correctedExternal.Candidate, Connection: planning.Connection}
	writeAssignment(continuationOwner, nil)
	continuationExternal := externalPublication(continuationOwner, 904)
	continuationExternal.Candidate, continuationExternal.External.Target.HeadOID = correctedExternal.Candidate, correctedExternal.Candidate
	writePublication(continuationExternal, true)
	correctedCapture, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("external correction lost its actual run: %v", err)
	}
	assertExternal(correctedCapture, correctedExternal)
	assertExternal(correctedCapture, continuationExternal)
	continuationFound := false
	for _, owner := range correctedCapture.Material.Assignments {
		if owner.ID == continuationID {
			continuationFound = reflect.DeepEqual(owner.Retained, &factory.CompletionRetainedCandidate{AdoptedFrom: correctedExternal.ID, AssignmentID: planning.ID, PublicationID: legacyExternal.ID, RunID: correction.Run, Candidate: correctedExternal.Candidate}) && len(owner.RunHistory) == 0
		}
	}
	if !continuationFound {
		t.Fatal("retained external continuation lost its original or immediate predecessor provenance")
	}
	if correctedCapture.Equal(externalCapture) {
		t.Fatal("corrected external candidate did not change the material digest")
	}
	if repeat, err := db.CaptureCompletionLocal(ctx, 7); err != nil || !repeat.Equal(correctedCapture) {
		t.Fatalf("external material recapture changed: %v", err)
	}
	fixtureDSN, ok := TestFixtureDSN()
	if !ok {
		t.Fatal("fixture connection disappeared")
	}
	reopenURI, err := url.Parse(fixtureDSN)
	if err != nil {
		t.Fatal("invalid fixture connection")
	}
	var database string
	if err := db.db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	reopenURI.Path = "/" + database
	reopened, err := Open(reopenURI.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if afterOpen, err := reopened.CaptureCompletionLocal(ctx, 7); err != nil || !afterOpen.Equal(correctedCapture) {
		t.Fatalf("external material changed after reopen: %v", err)
	}
	for _, predecessor := range []string{factory.NewID(), canonicalExternal.ID} {
		if _, err := db.db.ExecContext(ctx, `UPDATE factory_assignments SET data=jsonb_set(data,'{retained_publication,adopted_from}',to_jsonb($2::text)) WHERE id=$1`, continuationID, predecessor); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
			t.Fatal("dangling or foreign retained-review predecessor was accepted or omitted")
		}
		if _, err := db.db.ExecContext(ctx, `UPDATE factory_assignments SET data=jsonb_set(data,'{retained_publication,adopted_from}',to_jsonb($2::text)) WHERE id=$1`, continuationID, correctedExternal.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []struct {
		name   string
		change func(*factory.Publication)
	}{
		{"missing run", func(publication *factory.Publication) {
			publication.Run = factory.NewID()
			publication.Corrections[0].RunID = publication.Run
		}},
		{"foreign run owner", func(publication *factory.Publication) {
			publication.Run = a.Run
			publication.Corrections[0].RunID = a.Run
		}},
		{"wrong project owner", func(publication *factory.Publication) { publication.ProjectID = "p" + strings.Repeat("9", 24) }},
	} {
		altered := correctedExternal
		altered.Corrections = append([]factory.PublicationOperation(nil), correctedExternal.Corrections...)
		invalid.change(&altered)
		writePublication(altered, false)
		if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
			t.Fatalf("%s publication was accepted or omitted", invalid.name)
		}
		writePublication(correctedExternal, false)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_publications SET data=jsonb_set(data,'{external,observation_digest}','"malformed"'::jsonb) WHERE assignment=$1`, correctedExternal.AssignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
		t.Fatal("malformed external origin was accepted or omitted")
	}
	writePublication(correctedExternal, false)
	if restored, err := db.CaptureCompletionLocal(ctx, 7); err != nil || !restored.Equal(correctedCapture) {
		t.Fatalf("refused external rows changed the retained capture: %v", err)
	}

	if _, err := db.db.ExecContext(ctx, `INSERT INTO issue_acceptance_heads(repository, issue, decision) VALUES(7, 9, $1)`,
		"d"+strings.Repeat("9", 24)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 7); err == nil {
		t.Fatal("dangling acceptance head captured instead of refusing")
	}
	tampered := moved
	tampered.ScopeDigest = strings.Repeat("0", 64)
	if err := tampered.Validate(); err == nil {
		t.Fatal("tampered digest validated")
	}
	var naked factory.CompletionLocal
	if err := naked.Validate(); err == nil {
		t.Fatal("nil material validated")
	}
}

// An omitted policy minute allowance resolves to the product default at
// capture: the validator receives the resolved admitted value, and the
// stored policy row is never mutated by the read path.
func TestCaptureCompletionMaterialDefaultMinutes(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_policies SET data = jsonb_set(data, '{reassessment_minutes}', '0') WHERE repository=7`); err != nil {
		t.Fatal(err)
	}
	local, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("captured local invalid: %v", err)
	}
	if local.Material.Authority.ReassessmentMinutes != factory.DefaultReassessmentMinutes {
		t.Fatalf("minutes=%d, want the resolved default %d",
			local.Material.Authority.ReassessmentMinutes, factory.DefaultReassessmentMinutes)
	}
}

// A withdrawn sponsorship permits nothing: the role projection is
// explicit empty, never the configured set, and the capture stays
// valid with the honest negative verdict behind it.
func TestCaptureCompletionMaterialWithdrawnSponsorship(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	sponsorship, err := db.Sponsorship(ctx, 7, "conn")
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.Active = false
	if err := db.SaveSponsorship(ctx, sponsorship); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	local, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("captured local invalid: %v", err)
	}
	roles := local.Material.Authority.Roles
	if roles == nil || len(roles) != 0 {
		t.Fatalf("roles=%v, want explicit empty for withdrawn sponsorship", roles)
	}
	if local.Material.Authority.Effective.Effective {
		t.Fatal("withdrawn sponsorship reads effective")
	}
}
