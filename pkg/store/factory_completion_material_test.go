package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
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
