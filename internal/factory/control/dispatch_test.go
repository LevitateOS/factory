package control

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func TestDispatchPassLaunchesOldestWithinShortLimit(t *testing.T) {
	t.Run("canonical_project_boundary", func(t *testing.T) {
		ctx := t.Context()
		db, dsn := dispatchTestDB(t)
		if _, err := db.BindConfiguredFountainLedger(ctx, "https://fixture-fountain.example.test"); err != nil {
			t.Fatal(err)
		}
		github := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.example.test"}
		if _, err := db.PutCollaborationHost(ctx, github); err != nil {
			t.Fatal(err)
		}
		foreign, err := db.ObserveCollaborationRepository(ctx, github.ID, "7", "alice/factory")
		if err != nil {
			t.Fatal(err)
		}
		fx := dispatchSeed(t, db)
		if fx.repo == 7 || fx.repo == int64(foreign.ID) {
			t.Fatal("fixture did not separate native, local and foreign repository keys")
		}
		// Numeric equality with a native Project must not give GitHub that
		// Project's preparation, authority or lifecycle gate.
		if err := db.CreateProject(ctx, store.Project{ID: "foreign-number-collision", Name: "Same", RepositoryID: int64(foreign.ID), OwnerID: 7, Repository: "alice/factory"}); err != nil {
			t.Fatal(err)
		}
		input, err := db.FactoryAuthorityInput(ctx, fx.repo)
		if err != nil || !input.ProjectExists {
			t.Fatalf("canonical authority lost native Project readiness: %+v %v", input, err)
		}
		foreignInput, err := db.FactoryAuthorityInput(ctx, int64(foreign.ID))
		if err != nil || foreignInput.ProjectExists {
			t.Fatalf("foreign local ID borrowed native Project readiness: %+v %v", foreignInput, err)
		}
		if _, err := db.CompletionSourceContext(ctx, int64(foreign.ID)); err == nil || !strings.Contains(err.Error(), "lacks its project") {
			t.Fatalf("foreign completion borrowed a native Project: %v", err)
		}
		coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker}
		if _, outcomes, err := coord.StopProject(ctx, "native:7", fx.proj); err != nil || len(outcomes) != 0 {
			t.Fatalf("canonical Project stop: %+v %v", outcomes, err)
		}
		if open, _, _, err := db.DispatchState(ctx, fx.repo); err != nil || open {
			t.Fatalf("Project stop missed local gate: %t %v", open, err)
		}
		for _, unrelated := range []int64{7, int64(foreign.ID)} {
			if open, _, _, err := db.DispatchState(ctx, unrelated); err != nil || !open {
				t.Fatalf("Project stop changed native/foreign gate %d: %t %v", unrelated, open, err)
			}
		}
		verified, err := coord.VerifyProjectStart(ctx, fx.proj)
		if err != nil || verified.DispatchOpen {
			t.Fatalf("start verification read native rather than local gate: %+v %v", verified, err)
		}
		if open, err := coord.ClearProjectStopAfterStart(ctx, fx.proj, verified); err != nil || !open {
			t.Fatalf("start did not release exact local Project gate: %t %v", open, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := store.Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		fx.db = reopened
		head := fx.accept(t, 3, "d333333333333333333333333")
		fx.queue(t, 3, head.ID)
		report := DispatchPass(ctx, fx.deps())
		if len(report.Launched) != 1 || len(report.Errors) != 0 || report.Launched[0].Repository != fx.repo {
			t.Fatalf("canonical Project dispatch after reopen: %+v", report)
		}
		assignment, err := reopened.Assignment(ctx, report.Launched[0].AssignmentID)
		if err != nil || assignment.Repository != fx.repo || assignment.ProjectID != fx.proj {
			t.Fatalf("dispatch lost exact local/native association: %+v %v", assignment, err)
		}
		if native, err := reopened.Project(ctx, fx.proj); err != nil || native.RepositoryID != 7 || native.OwnerID != 7 {
			t.Fatalf("dispatch rewrote native Project ownership: %+v %v", native, err)
		}
	})
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	if err := db.SaveCapacity(ctx, factory.Capacity{Revision: 1, UpdatedBy: 7, MaxConcurrentRuns: 1}); err != nil {
		t.Fatal(err)
	}
	const acceptedBody = "Implement app.can and client.jsx; app.cannot and notes.mdbackup are prose, not source files."
	oldHead := fx.accept(t, 3, "d333333333333333333333333", acceptedBody)
	youngHead := fx.accept(t, 5, "d555555555555555555555555")
	now := time.Now()
	fx.queueAt(t, 3, oldHead.ID, now.Add(-2*time.Hour))
	fx.queueAt(t, 5, youngHead.ID, now.Add(-time.Hour))

	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 1 || report.Launched[0].Issue != 3 {
		t.Fatalf("launched = %+v", report.Launched)
	}
	if len(report.Waits) != 1 || report.Waits[0].Issue != 5 || report.Waits[0].Reason != WaitCapacity {
		t.Fatalf("waits = %+v", report.Waits)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("errors = %+v", report.Errors)
	}
	launched := report.Launched[0]
	a, err := db.Assignment(ctx, launched.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Stage != factory.AssignmentAssigned || a.Attempts != 1 || a.Run != launched.RunID ||
		a.SourceCommit != fx.tip || a.Acceptance != oldHead.ID || a.Connection != "conn" {
		t.Fatalf("assignment = %+v", a)
	}
	if a.Authority.Sponsorship != 1 || a.Authority.RequirementsID == "" || a.Authority.ApprovalID == "" {
		t.Fatalf("assignment authority = %+v", a.Authority)
	}
	if a.AcceptedContext == nil || a.AcceptedContext.Title != "objective 3; inspect README.md" || a.AcceptedContext.Body != acceptedBody || len(a.AcceptedContext.Sources) != 1 || a.AcceptedContext.Sources[0].Content != "answer 3" || a.AcceptedContext.RepositoryContext == nil {
		t.Fatal("atomic dispatch lost exact structured accepted context")
	}
	for _, want := range []string{
		"Prompt template: " + factory.DispatchPromptTemplate, "Recorded attempt active-time limit: 120 minutes",
		factory.LibraryReadGuidance,
		"synthetic approved setup instructions", "synthetic exact candidate diff",
		"Required evidence checks: ci", "Required evidence:", "report blocked with the concrete reason",
		`Provider connection: "conn"`,
		"Preparation requirements: " + a.Authority.RequirementsID + " approval: " + a.Authority.ApprovalID,
	} {
		if !strings.Contains(string(a.Prompt), want) {
			t.Errorf("recorded prompt lacks %q", want)
		}
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("host launches = %d", len(fx.host.launches))
	}
	sent := fx.host.launches[0]
	if string(sent.Prompt) != string(a.Prompt) || sent.Run.Assignment != a.PromptSHA ||
		sent.Run.SourceCommit != fx.tip || sent.Run.Actor != 7 || sent.Run.Connection != "conn" ||
		sent.HarnessSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("launch = %+v", sent.Run)
	}
	if sent.Run.Preparation != "f111111111111111111111111" {
		t.Fatalf("launch preparation = %+v", sent.Run)
	}
	if run, err := db.FactoryRun(ctx, launched.RunID); err != nil {
		t.Fatal(err)
	} else if span := run.Deadline.Sub(run.Started); span <= 119*time.Minute || span > 120*time.Minute {
		t.Fatalf("run deadline span = %v", span)
	} else if len(fx.host.contextRequests) != 1 || len(fx.host.contextRoles) != 1 {
		t.Fatalf("prepared repository context reads = %+v roles=%+v", fx.host.contextRequests, fx.host.contextRoles)
	} else {
		contextRead := fx.host.contextRequests[0]
		if fx.host.contextRoles[0] != project.RoleCoder || contextRead.ID != a.Preparation ||
			contextRead.SourceCommit != a.SourceCommit || contextRead.ApprovedBase != a.SourceCommit ||
			contextRead.DiffBase != a.SourceCommit || contextRead.Candidate != fx.tip ||
			!contextRead.NotAfter.Equal(run.Deadline) || strings.Join(contextRead.Paths, ",") != "README.md,app.can,client.jsx" {
			t.Fatalf("initial context changed approved source, role or run deadline: request=%+v role=%q run=%+v", contextRead, fx.host.contextRoles[0], run)
		}
	}
	run, err := db.FactoryRun(ctx, launched.RunID)
	if err != nil || run.InputSHA != fx.tip || run.Reconciled {
		t.Fatalf("run = %+v %v", run, err)
	}
	view, err := db.FactoryRunView(ctx, launched.RunID)
	if err != nil || view.Attempt != launched.AssignmentID || view.Issue != 3 {
		t.Fatalf("view = %+v %v", view, err)
	}
	reservation, err := db.Reservation(ctx, launched.AssignmentID)
	if err != nil || reservation.State != factory.ReservationHeld || reservation.PlannedMinutes != 120 {
		t.Fatalf("reservation = %+v %v", reservation, err)
	}
	// The context classifier runs once at the launch boundary with the
	// actual packet and current facts; its choice is then frozen.
	guarded := fx.deps()
	guarded.MCPKey = []byte(strings.Repeat("k", 32))
	beforeLaunch := len(fx.host.launches)
	// Simulate a restart after the pending marker commits but before its
	// deterministic capability production. No identity or attempt changes.
	run.ContextPending = true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	chooser := &bundleGapChooser{pick: factory.ContextBundleCurrent}
	guarded.Priority = &PriorityJudgments{Journal: db, Chooser: chooser}
	qualified := RecoverDispatch(ctx, guarded)
	if len(qualified.Launched) != 1 || !chooser.sawFacts || chooser.calls != 1 {
		t.Fatalf("actual current facts did not qualify launch: %+v facts=%t calls=%d", qualified, chooser.sawFacts, chooser.calls)
	}
	run, err = db.FactoryRun(ctx, run.ID)
	if err != nil || run.ContextPending {
		t.Fatalf("native launch did not clear durable context hold: %+v %v", run, err)
	}
	if chooser.sawAttempt || chooser.history != 0 {
		t.Fatal("first staged run fabricated execution history for context selection")
	}
	capability, err := db.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A pending launch uses its admitted bundle even after the host selection
	// changes. Missing admitted identity must not be rebuilt from that selection.
	currentHarness := fx.host.harness
	fx.host.harness = func(string) (project.FactoryHarnessPin, error) {
		t.Fatal("pending launch consulted the current tool selection")
		return project.FactoryHarnessPin{}, nil
	}
	pending, wait, terminal := pendingContextLaunch(ctx, guarded, a, run)
	if wait != nil || terminal || pending.HarnessSHA256 != run.Admission.Tool.SHA256 {
		t.Fatalf("pending launch changed its admitted bundle: %+v wait=%+v terminal=%t", pending.Run, wait, terminal)
	}
	missing := run
	admission := *run.Admission
	admission.Tool = nil
	missing.Admission = &admission
	if _, wait, terminal = pendingContextLaunch(ctx, guarded, a, missing); wait == nil || wait.Reason != WaitHarness || terminal {
		t.Fatalf("missing tool identity was reconstructed: wait=%+v terminal=%t", wait, terminal)
	}
	fx.host.harness = currentHarness
	// Save the owner choice on this real admitted capability. It remains the
	// only effective choice record and its author survives later activation.
	run.ContextPending = true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	current, err := db.FactoryCurrentContext(ctx, capability, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := factory.CurrentRequiredSemanticDigest(current.Role)
	if err != nil {
		t.Fatal(err)
	}
	request := factory.ContextCurrentRequest{Fingerprint: capability.ContextSelectionSnapshot, ScopeDigest: capability.ScopeDigest(), RequiredDigest: digest}
	resolution, err := db.SaveContextCurrentResolution(ctx, capability, request, "native:7", time.Now())
	if err != nil || resolution.Principal != "native:7" {
		t.Fatalf("canonical context choice: %+v %v", resolution, err)
	}
	capability, err = db.GetMCPCapability(ctx, run.ID)
	if err != nil || capability.ContextCurrentResolution == nil || *capability.ContextCurrentResolution != resolution {
		t.Fatal("context choice was not saved on its capability", err)
	}
	coord := &Coordinator{Store: db}
	if repeated, err := coord.UseCurrentContext(ctx, "native:7", run.ID, request); err != nil || repeated != resolution {
		t.Fatalf("current choice changed: %+v %v", repeated, err)
	}
	run.ContextPending = false
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateObservedMCPCapability(ctx, capability); err != nil {
		t.Fatal(err)
	}
	capability, err = db.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, attempt, _, err := loadContextBundleState(ctx, db, capability, a, nil)
	if err != nil || !attempt || len(state.HistoryPreview) != 1 || state.HistoryPreview[0].ID != run.ID {
		t.Fatalf("observed native execution lost its actual continuity: %+v attempt=%t err=%v", state.HistoryPreview, attempt, err)
	}
	if bundle, reason, err := PrepareContextBundleSelection(ctx, db, nil, a, run, nil); err != nil || bundle != factory.ContextBundleCurrent || reason != "" || chooser.calls != 1 {
		t.Fatalf("frozen actual context choice was consulted again: %s %s %v", bundle, reason, err)
	}

	// Settlement releases the executing session. The reported
	// investigation advances to its implementation child, which retains
	// the slot; completed planning never names a candidate.
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "done", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	plan := phaseTestPlan(t, a.AttemptRoot, a.SourceCommit, a.Preparation)
	output := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	})
	finished, ok := accountRunFinish(t, ctx, db, run, output, time.Now())
	if !ok || finished.Outcome != factory.Succeeded || finished.Result == nil || !finished.Result.Reported {
		t.Fatalf("accounted = %+v %v", finished, ok)
	}
	if finished.Result.Candidate != "" {
		t.Fatalf("candidate = %q", finished.Result.Candidate)
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total < 1 {
		t.Fatalf("usage total = %d", total)
	}
	// The reported investigation advances to its implementation child,
	// which retains the short-limit slot: the younger issue still waits.
	second := DispatchPass(ctx, fx.deps())
	if len(second.Launched) != 1 || second.Launched[0].Issue != 3 || len(second.Errors) != 0 || waitReason(second, 5) != WaitCapacity {
		t.Fatalf("reported investigation did not advance under its retained slot: %+v", second)
	}
	child, err := db.Assignment(ctx, second.Launched[0].AssignmentID)
	if err != nil || child.ParentAssignment != a.ID || child.Phase != project.ExecutionImplement || child.AttemptRoot != a.AttemptRoot {
		t.Fatalf("advanced child = %+v %v", child, err)
	}
	if child.AcceptedContext == nil || child.AcceptedContext.AcceptanceID != oldHead.ID || child.AcceptedContext.Title != a.AcceptedContext.Title || child.AcceptedContext.Body != acceptedBody || !reflect.DeepEqual(child.AcceptedContext.Sources, a.AcceptedContext.Sources) {
		t.Fatal("implementation child lost its verified accepted context")
	}
	childRun, err := db.FactoryRun(ctx, child.Run)
	if err != nil {
		t.Fatal(err)
	}
	uncertain := &bundleGapChooser{pick: factory.ContextBundleCurrent, confidence: 0.6}
	guarded.Priority = &PriorityJudgments{Journal: db, Chooser: uncertain}
	beforeLaunch = len(fx.host.launches)
	unsure := newDispatchReport()
	launchTail(ctx, guarded, child, childRun, fx.host.launches[beforeLaunch-1], &unsure)
	if len(unsure.Launched) != 1 || len(fx.host.launches) != beforeLaunch+1 || uncertain.calls != 1 {
		t.Fatalf("low confidence selection held work instead of serving current context: %+v calls=%d", unsure, uncertain.calls)
	}
	allowance, err := db.AttemptAllowance(ctx, fx.repo, 3)
	if err != nil || !allowance.Active || allowance.Closed || allowance.RootAssignment != a.AttemptRoot {
		t.Fatalf("settled candidate attempt = %+v, %v", allowance, err)
	}
}

func TestDispatchRefusesTargetDifferentFromPreparedCoderSource(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	inputs := fx.reads.inputs["7/3"]
	inputs.Tip = strings.Repeat("f", 40)
	fx.reads.inputs["7/3"] = inputs

	report := DispatchPass(ctx, fx.deps())
	// The moved tip has no ready preparation, so the preparation gate
	// refuses before any source-context read; nothing launches.
	if len(report.Launched) != 0 || len(report.Errors) != 0 || waitReason(report, 3) != WaitPreparation ||
		len(fx.host.launches) != 0 || len(fx.host.contextRequests) != 0 {
		t.Fatalf("mismatched prepared source launched or read context: %+v launches=%d reads=%+v", report, len(fx.host.launches), fx.host.contextRequests)
	}
}

func TestDispatchRefusesRepositoryContextPathOverflowBeforeNativeRead(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	var body strings.Builder
	for i := 0; i < project.MaxFactoryContextPaths; i++ {
		body.WriteString(fmt.Sprintf("see selected-%02d.md\n", i))
	}
	head := fx.accept(t, 3, "d333333333333333333333333", body.String())
	fx.queue(t, 3, head.ID)

	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 0 || len(report.Errors) != 0 || waitReason(report, 3) != WaitSource ||
		len(fx.host.launches) != 0 || len(fx.host.contextRequests) != 0 {
		t.Fatalf("path-reference overflow reached native context read or launch: %+v launches=%d reads=%+v", report, len(fx.host.launches), fx.host.contextRequests)
	}
}

func TestDispatchPassWithoutDepsReports(t *testing.T) {
	report := DispatchPass(context.Background(), DispatchDeps{})
	if len(report.Errors) != 1 || len(report.Launched) != 0 {
		t.Fatalf("report = %+v", report)
	}
}
