package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

// planRevisionFixture returns a valid executable investigation plan under
// fixed identities so store tests assert exact bytes and pointers.
func planRevisionFixture(root, id string, generation int, approach string) factory.InvestigationPlan {
	tasks := []factory.PlanTask{
		{
			ID: "design", Objective: "Settle the approach", Outputs: []string{"approach note"},
			AcceptanceRefs: []string{"accept-1"}, Lane: "main",
			OwnedPaths: []string{"docs/plan.md"}, CheckIDs: []string{"unit"},
		},
		{
			ID: "build", Objective: "Implement the change", Outputs: []string{"candidate diff"},
			AcceptanceRefs: []string{"accept-1"}, DependsOn: []string{"design"}, Lane: "main",
			OwnedPaths: []string{"internal/factory/planning.go"}, InterfaceWrite: []string{"plan-bytes"},
			CheckIDs: []string{"unit"},
		},
		{
			ID: "docs", Objective: "Record the contract", Outputs: []string{"guide delta"},
			AcceptanceRefs: []string{"accept-2"}, Lane: "side",
			OwnedPaths: []string{"docs/reference/factory.md"},
		},
	}
	lanes := []factory.PlanLane{{ID: "main", TaskIDs: []string{"design", "build"}}, {ID: "side", TaskIDs: []string{"docs"}}}
	checks := []factory.CheckSpec{{ID: "unit", Argv: []string{"go", "test", "./internal/factory/"}, Cwd: ".", TimeoutSeconds: 300, Scope: factory.CheckScopeTask, Required: true}}
	commit := strings.Repeat("c", 40)
	plan := factory.InvestigationPlan{
		ID: id, AttemptRoot: root,
		ScopeDigest: strings.Repeat("a", 64), InputDigest: strings.Repeat("b", 64),
		SourceCommit: commit, IntegrationCommit: commit,
		PrerequisiteDigest: []string{strings.Repeat("e", 64)},
		Preparation:        "f" + strings.Repeat("d", 24),
		Approach:           approach,
		Inspected: []factory.InspectedSource{
			{Path: "internal/factory/planning.go", Commit: commit, Evidence: "placeholder steps lack evidence"},
		},
		Findings:           []string{"planning path carries no task graph"},
		AcceptanceCoverage: []string{"accept-1", "accept-2"},
		Rejected:           []factory.RejectedAlternative{{Approach: "hand-written steps", Reason: "no validation or receipts"}},
		Tasks:              tasks, Lanes: lanes, Checks: checks,
		Generation: generation,
	}
	digest, err := factory.PlanDigest(plan)
	if err != nil {
		panic(err)
	}
	plan.Digest = digest
	return plan
}

func planBindingsFixture(plan factory.InvestigationPlan) PlanBindings {
	return PlanBindings{
		SourceCommit:  plan.SourceCommit,
		Prerequisites: append([]string(nil), plan.PrerequisiteDigest...),
		Preparation:   plan.Preparation,
	}
}

// T11 (FW07 scope): unique keys, DAG, coverage, ownership and limits refuse
// before any row is written; the semantic digest round-trips exactly.
func TestSaveInvestigationPlanRoundTripAndRefusals(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	plan := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Investigate, plan, then implement behind checks.")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,42,'plan-run','assigned',1,'{"phase":"investigate"}')`, root); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	// Identical replay is idempotent.
	if err := s.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatalf("identical plan replay refused: %v", err)
	}
	got, err := s.InvestigationPlan(ctx, root, plan.ID)
	if err != nil || got.Digest != plan.Digest {
		t.Fatalf("plan round trip lost content: %v", err)
	}
	posts, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, View: "decisions", Limit: 10})
	if err != nil || len(posts) != 1 {
		t.Fatalf("plan revision did not publish exactly one decision: %+v %v", posts, err)
	}
	post, err := s.FeedPost(ctx, posts[0].ID)
	if err != nil || post.Payload == nil || post.Payload.Communication == nil || post.Payload.Communication.PlanSource == nil || post.Payload.Communication.PlanSource.Plan != plan.ID || post.Payload.Communication.PlanSource.Digest != plan.Digest {
		t.Fatalf("plan post lost its native revision: %+v %v", post, err)
	}
	next := planRevisionFixture(root, strings.Repeat("c", 32), 1, "Revised architecture retains the original plan history.")
	if err := s.SaveInvestigationPlan(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePlanHead(ctx, root, "", plan.ID); err != nil {
		t.Fatal(err)
	}
	current, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, View: "decisions", Current: true, Limit: 10})
	if err != nil || len(current) != 1 || current[0].ID != posts[0].ID {
		t.Fatalf("current view does not follow the native plan head: %+v %v", current, err)
	}
	history, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, View: "decisions", Limit: 10})
	if err != nil || len(history) != 2 {
		t.Fatalf("plan revision history lost: %+v %v", history, err)
	}
	if _, err = s.InvestigationPlan(ctx, root, strings.Repeat("1", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing plan revision: %v", err)
	}
	broken := plan
	broken.Digest = strings.Repeat("0", 64)
	if err := s.SaveInvestigationPlan(ctx, broken); err == nil {
		t.Fatal("plan with a forged digest admitted")
	}
	duplicates := plan
	duplicates.ID = strings.Repeat("2", 32)
	duplicates.Tasks[1].ID = duplicates.Tasks[0].ID
	duplicates.Digest, _ = factory.PlanDigest(duplicates)
	if err := s.SaveInvestigationPlan(ctx, duplicates); err == nil {
		t.Fatal("plan with duplicate task keys admitted")
	}
}

// T11 (FW07 scope): the original consumed plan survives a conflicting reuse
// of its identity and a later replacement.
func TestPlanRevisionImmutableSurvivesConflict(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	original := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Investigate, plan, then implement behind checks.")
	if err := s.SaveInvestigationPlan(ctx, original); err != nil {
		t.Fatal(err)
	}
	forged := original
	forged.Approach = "Skip investigation and write code directly."
	forged.Digest, _ = factory.PlanDigest(forged)
	if err := s.SaveInvestigationPlan(ctx, forged); !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("conflicting plan identity: %v", err)
	}
	got, err := s.InvestigationPlan(ctx, root, original.ID)
	if err != nil || got.Digest != original.Digest || got.Approach != original.Approach {
		t.Fatalf("consumed plan did not survive conflict: %v", err)
	}
}

// T11 (FW07 scope): head pointers move only under CAS; a stale move fails
// while a replay that already reached the target succeeds.
func TestAdvancePlanHeadCAS(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	first := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Investigate, plan, then implement behind checks.")
	second := planRevisionFixture(root, strings.Repeat("1", 32), 1, "Re-investigate after the source advanced.")
	if _, _, err := s.PlanHead(ctx, root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing plan head: %v", err)
	}
	if err := s.SaveInvestigationPlan(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInvestigationPlan(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePlanHead(ctx, root, "", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePlanHead(ctx, root, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	head, generation, err := s.PlanHead(ctx, root)
	if err != nil || head != second.ID || generation != 1 {
		t.Fatalf("head did not advance: %q gen %d err %v", head, generation, err)
	}
	if err := s.AdvancePlanHead(ctx, root, first.ID, strings.Repeat("b", 32)); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale head move: %v", err)
	}
	if err := s.AdvancePlanHead(ctx, root, first.ID, second.ID); err != nil {
		t.Fatalf("head replay refused: %v", err)
	}
	if err := s.AdvancePlanHead(ctx, root, second.ID, strings.Repeat("9", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("head moved to a missing revision: %v", err)
	}
	// A restart observes the moved head and the surviving original.
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	head, _, err = reopened.PlanHead(ctx, root)
	if err != nil || head != second.ID {
		t.Fatalf("restart lost the head: %q err %v", head, err)
	}
	got, err := reopened.InvestigationPlan(ctx, root, first.ID)
	if err != nil || got.Digest != first.Digest {
		t.Fatalf("restart lost the original plan: %v", err)
	}
}

// T11 (FW07 scope): replacement saves the new revision and moves the head
// atomically; a conflicting replacement moves nothing.
func TestReplacePlanHeadAtomic(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	first := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Investigate, plan, then implement behind checks.")
	second := planRevisionFixture(root, strings.Repeat("1", 32), 1, "Re-investigate after the source advanced.")
	if err := s.ReplacePlanHead(ctx, first, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePlanHead(ctx, second, first.ID); err != nil {
		t.Fatal(err)
	}
	head, generation, err := s.PlanHead(ctx, root)
	if err != nil || head != second.ID || generation != 1 {
		t.Fatalf("replacement did not move the head: %q gen %d err %v", head, generation, err)
	}
	got, err := s.InvestigationPlan(ctx, root, first.ID)
	if err != nil || got.Digest != first.Digest {
		t.Fatalf("replacement rewrote the original: %v", err)
	}
	forged := second
	forged.Approach = "Skip investigation and write code directly."
	forged.Digest, _ = factory.PlanDigest(forged)
	if err := s.ReplacePlanHead(ctx, forged, second.ID); !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("conflicting replacement: %v", err)
	}
	head, _, err = s.PlanHead(ctx, root)
	if err != nil || head != second.ID {
		t.Fatalf("conflicting replacement moved the head: %q err %v", head, err)
	}
	third := planRevisionFixture(root, strings.Repeat("2", 32), 2, "A third investigation.")
	if err := s.ReplacePlanHead(ctx, third, first.ID); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale replacement: %v", err)
	}
	head, _, err = s.PlanHead(ctx, root)
	if err != nil || head != second.ID {
		t.Fatalf("stale replacement moved the head: %q err %v", head, err)
	}
}

// T11 (FW07 scope): task receipts persist under CAS revision with the
// admitted lifecycle; restarts observe receipts instead of redoing work.
func TestPlanTaskReceiptLifecycle(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	plan := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Investigate, plan, then implement behind checks.")
	if err := s.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePlanHead(ctx, root, "", plan.ID); err != nil {
		t.Fatal(err)
	}
	ghost := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "ghost", State: PlanTaskPending}
	if err := s.SavePlanTaskReceipt(ctx, ghost); !errors.Is(err, ErrUnknownPlanTask) {
		t.Fatalf("receipt for an unplanned task: %v", err)
	}
	unknownPlan := PlanTaskReceipt{AttemptRoot: root, PlanID: strings.Repeat("9", 32), TaskID: "design", State: PlanTaskPending}
	if err := s.SavePlanTaskReceipt(ctx, unknownPlan); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt under a missing revision: %v", err)
	}
	early := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "design", State: PlanTaskRunning, Child: "child-1"}
	if err := s.SavePlanTaskReceipt(ctx, early); !errors.Is(err, ErrPlanTaskTransition) {
		t.Fatalf("task skipped pending: %v", err)
	}
	advanced := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "design", Revision: 1, State: PlanTaskPending}
	if err := s.SavePlanTaskReceipt(ctx, advanced); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("receipt skipped its base revision: %v", err)
	}
	pending := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "design", State: PlanTaskPending, InputDigest: strings.Repeat("0", 64)}
	if err := s.SavePlanTaskReceipt(ctx, pending); err != nil {
		t.Fatal(err)
	}
	running := pending
	running.Revision = 0
	running.State = PlanTaskRunning
	running.Child = "child-1"
	running.Events = []string{"child_started"}
	if err := s.SavePlanTaskReceipt(ctx, running); err != nil {
		t.Fatal(err)
	}
	stale := running
	stale.Events = []string{"child_started", "child_reported"}
	if err := s.SavePlanTaskReceipt(ctx, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("superseded receipt write: %v", err)
	}
	ready := running
	ready.Revision = 1
	ready.State = PlanTaskOutputReady
	ready.OutputDigest = strings.Repeat("1", 64)
	ready.Events = []string{"child_started", "child_reported"}
	ready.Checks = []string{"unit"}
	ready.Tree = strings.Repeat("2", 40)
	ready.Commit = strings.Repeat("3", 40)
	if err := s.SavePlanTaskReceipt(ctx, ready); err != nil {
		t.Fatal(err)
	}
	done := ready
	done.Revision = 2
	done.State = PlanTaskCompleted
	done.Reason = "checks and exact tree verified"
	if err := s.SavePlanTaskReceipt(ctx, done); err != nil {
		t.Fatal(err)
	}
	// Identical replay after the terminal write commits nothing new.
	if err := s.SavePlanTaskReceipt(ctx, done); err != nil {
		t.Fatalf("terminal receipt replay refused: %v", err)
	}
	got, err := s.PlanTaskReceipt(ctx, root, plan.ID, "design")
	if err != nil || got.Revision != 3 || got.State != PlanTaskCompleted {
		t.Fatalf("terminal receipt did not persist: %+v err %v", got, err)
	}
	failed := done
	failed.State = PlanTaskFailed
	failed.Reason = "late failure"
	if err := s.SavePlanTaskReceipt(ctx, failed); !errors.Is(err, ErrPlanTaskTransition) {
		t.Fatalf("terminal task changed state: %v", err)
	}
	skip := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "docs", State: PlanTaskCompleted, Child: "child-9", OutputDigest: strings.Repeat("1", 64), Reason: "done"}
	if err := s.SavePlanTaskReceipt(ctx, skip); !errors.Is(err, ErrPlanTaskTransition) {
		t.Fatalf("task skipped its lifecycle: %v", err)
	}
	docsPending := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "docs", State: PlanTaskPending}
	if err := s.SavePlanTaskReceipt(ctx, docsPending); err != nil {
		t.Fatal(err)
	}
	cancelled := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "docs", State: PlanTaskCancelled, Reason: "parent stopped"}
	if err := s.SavePlanTaskReceipt(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	// A restart observes both receipts instead of redoing the tasks.
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	receipts, err := reopened.PlanTaskReceipts(ctx, root, plan.ID)
	if err != nil || len(receipts) != 2 || receipts[0].TaskID != "design" || receipts[1].TaskID != "docs" {
		t.Fatalf("restart lost task receipts: %+v err %v", receipts, err)
	}
}

// A journal cursor is carried by its affected task receipts. A later invalid
// transition must roll back every earlier transition in that same native page.
func TestPlanTaskReceiptPageAtomicity(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	plan := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Implement behind checks.")
	if err := s.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	first := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "design", State: PlanTaskPending}
	second := PlanTaskReceipt{AttemptRoot: root, PlanID: plan.ID, TaskID: "docs", State: PlanTaskPending}
	if err := s.SavePlanTaskReceipts(ctx, []PlanTaskReceipt{first, second}); err != nil {
		t.Fatal(err)
	}
	first.State, first.Reason, first.Events = PlanTaskCancelled, "native run stopped", []string{"run_stopped@7"}
	second.State, second.Reason, second.Events = PlanTaskCancelled, "native run stopped", []string{"run_stopped@7"}
	second.Revision = 1 // An intervening writer makes the final CAS stale.
	if err := s.SavePlanTaskReceipts(ctx, []PlanTaskReceipt{first, second}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale page did not refuse: %v", err)
	}
	for _, key := range []string{"design", "docs"} {
		receipt, err := s.PlanTaskReceipt(ctx, root, plan.ID, key)
		if err != nil || receipt.State != PlanTaskPending || receipt.Revision != 0 || len(receipt.Events) != 0 {
			t.Fatalf("partial page committed its cursor: %+v (%v)", receipt, err)
		}
	}
	second.Revision = 0
	if err := s.SavePlanTaskReceipts(ctx, []PlanTaskReceipt{first, second}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"design", "docs"} {
		receipt, err := s.PlanTaskReceipt(ctx, root, plan.ID, key)
		if err != nil || receipt.State != PlanTaskCancelled || receipt.Revision != 1 || len(receipt.Events) != 1 {
			t.Fatalf("complete stop page did not commit: %+v (%v)", receipt, err)
		}
	}
}

// T11 (FW07 scope): only a fresh, complete head admits; stale source,
// prerequisite or preparation bindings refuse, as do unresolved plans.
func TestAdmitPlanHeadBindings(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	root := strings.Repeat("a", 32)
	plan := planRevisionFixture(root, strings.Repeat("b", 32), 0, "Investigate, plan, then implement behind checks.")
	if err := s.SaveInvestigationPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePlanHead(ctx, root, "", plan.ID); err != nil {
		t.Fatal(err)
	}
	admitted, err := s.AdmitPlanHead(ctx, root, planBindingsFixture(plan))
	if err != nil || admitted.Digest != plan.Digest {
		t.Fatalf("fresh head refused: %v", err)
	}
	stale := planBindingsFixture(plan)
	stale.SourceCommit = strings.Repeat("f", 40)
	if _, err = s.AdmitPlanHead(ctx, root, stale); !errors.Is(err, ErrStalePlanBindings) {
		t.Fatalf("stale source admitted: %v", err)
	}
	stale = planBindingsFixture(plan)
	stale.Prerequisites = []string{strings.Repeat("0", 64)}
	if _, err = s.AdmitPlanHead(ctx, root, stale); !errors.Is(err, ErrStalePlanBindings) {
		t.Fatalf("stale prerequisites admitted: %v", err)
	}
	stale = planBindingsFixture(plan)
	stale.Preparation = "f" + strings.Repeat("0", 24)
	if _, err = s.AdmitPlanHead(ctx, root, stale); !errors.Is(err, ErrStalePlanBindings) {
		t.Fatalf("stale preparation admitted: %v", err)
	}
	// An unresolved plan persists as a record but never admits.
	blocked := planRevisionFixture(root, strings.Repeat("1", 32), 1, "Re-investigate after the source advanced.")
	blocked.Unresolved = []string{"auth boundary undecided"}
	blocked.Digest, _ = factory.PlanDigest(blocked)
	if err := s.SaveInvestigationPlan(ctx, blocked); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePlanHead(ctx, root, plan.ID, blocked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitPlanHead(ctx, root, planBindingsFixture(blocked)); err == nil || errors.Is(err, ErrStalePlanBindings) {
		t.Fatalf("unresolved plan admitted: %v", err)
	}
	if _, err = s.AdmitPlanHead(ctx, strings.Repeat("9", 32), planBindingsFixture(plan)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admit without a head: %v", err)
	}
}

func TestSameDigestMultiset(t *testing.T) {
	a := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	b := []string{strings.Repeat("b", 64), strings.Repeat("a", 64)}
	if !sameDigestMultiset(a, b) || !sameDigestMultiset(nil, nil) {
		t.Fatal("equal multisets refused")
	}
	if sameDigestMultiset(a, a[:1]) || sameDigestMultiset(a, []string{strings.Repeat("a", 64), strings.Repeat("a", 64)}) {
		t.Fatal("different multisets admitted")
	}
}

func TestPlanTaskReceiptRefusals(t *testing.T) {
	base := PlanTaskReceipt{
		AttemptRoot: strings.Repeat("a", 32), PlanID: strings.Repeat("b", 32),
		TaskID: "design", State: PlanTaskRunning, Child: "child-1",
	}
	cases := map[string]func(*PlanTaskReceipt){
		"identity": func(r *PlanTaskReceipt) { r.AttemptRoot = "short" },
		"key":      func(r *PlanTaskReceipt) { r.TaskID = "Bad" },
		"revision": func(r *PlanTaskReceipt) { r.Revision = -1 },
		"state":    func(r *PlanTaskReceipt) { r.State = "coding" },
		"pending child": func(r *PlanTaskReceipt) {
			r.State = PlanTaskPending
			r.Child = "child-1"
		},
		"running child": func(r *PlanTaskReceipt) { r.Child = "" },
		"input":         func(r *PlanTaskReceipt) { r.InputDigest = "short" },
		"output ready": func(r *PlanTaskReceipt) {
			r.State = PlanTaskOutputReady
		},
		"check": func(r *PlanTaskReceipt) { r.Checks = []string{"Bad"} },
		"tree":  func(r *PlanTaskReceipt) { r.Tree = "short" },
		"terminal reason": func(r *PlanTaskReceipt) {
			r.State = PlanTaskCompleted
			r.OutputDigest = strings.Repeat("1", 64)
		},
		"reason bound": func(r *PlanTaskReceipt) {
			r.State = PlanTaskFailed
			r.Reason = strings.Repeat("x", factory.MaxPlanTextBytes+1)
		},
	}
	for name, mutate := range cases {
		receipt := base
		mutate(&receipt)
		if err := receipt.Validate(); err == nil {
			t.Fatalf("receipt %s admitted", name)
		}
	}
	pending := PlanTaskReceipt{AttemptRoot: base.AttemptRoot, PlanID: base.PlanID, TaskID: "design", State: PlanTaskPending}
	if err := pending.Validate(); err != nil {
		t.Fatalf("pending receipt refused: %v", err)
	}
	terminal := base
	terminal.State = PlanTaskFailed
	terminal.Reason = "check failed on the exact tree"
	if err := terminal.Validate(); err != nil {
		t.Fatalf("failed receipt refused: %v", err)
	}
	var bindings PlanBindings
	if err := bindings.Validate(); err == nil {
		t.Fatal("empty bindings admitted")
	}
}
