package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

type planningChooser struct{ calls int }

func (f *planningChooser) Select(_ context.Context, _ any, _ string, choices map[string]any) (factory.PriorityAnswer, error) {
	f.calls++
	selected := "incremental_acceptance"
	a := factory.PriorityAnswer{Selected: selected, Model: "synthetic-model", Confidence: 1, Probabilities: map[string]float64{}}
	for id := range choices {
		a.Probabilities[id] = 0
	}
	a.Probabilities[selected] = 1
	return a, nil
}

type progressTaskJournalHost struct {
	*fakeDispatchHost
	read           func(project.FactoryTaskEvents) (project.FactoryTaskEventsState, error)
	checkpoint     func(project.FactoryTaskCheckpoint, bool) (project.FactoryTaskCheckpointState, error)
	readCheckpoint func(project.FactoryTaskCheckpoint) (project.FactoryTaskCheckpointState, error)
}

func (h *progressTaskJournalHost) FactoryTaskEvents(_ context.Context, req project.FactoryTaskEvents) (project.FactoryTaskEventsState, error) {
	return h.read(req)
}

func (h *progressTaskJournalHost) FactoryTaskCheckpoint(_ context.Context, req project.FactoryTaskCheckpoint) (project.FactoryTaskCheckpointState, error) {
	if h.checkpoint == nil {
		return project.FactoryTaskCheckpointState{}, errors.New("fixture has no checkpoint producer")
	}
	return h.checkpoint(req, false)
}

func (h *progressTaskJournalHost) FactoryFinalCandidate(_ context.Context, req project.FactoryTaskCheckpoint) (project.FactoryTaskCheckpointState, error) {
	if h.checkpoint == nil {
		return project.FactoryTaskCheckpointState{}, errors.New("fixture has no checkpoint producer")
	}
	return h.checkpoint(req, true)
}

func (h *progressTaskJournalHost) FactoryFinalCandidateReceipt(_ context.Context, req project.FactoryTaskCheckpoint) (project.FactoryTaskCheckpointState, error) {
	if h.readCheckpoint != nil {
		return h.readCheckpoint(req)
	}
	return h.FactoryFinalCandidate(context.Background(), req)
}

func (h *progressTaskJournalHost) CheckpointTask(ctx context.Context, r execution.CheckpointRequest) (execution.CheckpointReceipt, error) {
	return host.NewFactoryObserver(h, nil).CheckpointTask(ctx, r)
}

func (h *progressTaskJournalHost) CheckpointFinal(ctx context.Context, r execution.CheckpointRequest) (execution.CheckpointReceipt, error) {
	return host.NewFactoryObserver(h, nil).CheckpointFinal(ctx, r)
}

func (h *progressTaskJournalHost) ReadFinalCheckpoint(ctx context.Context, r execution.CheckpointRequest) (execution.CheckpointReceipt, error) {
	return host.NewFactoryObserver(h, nil).ReadFinalCheckpoint(ctx, r)
}

// Real dispatch, authenticated proposals, receipt CAS and publication gates
// share PostgreSQL. Only native process observations are scripted here; the
// separate retained-VM fixture executes actual Git and systemd operations.
func TestProgressCheckpointsAuthenticatedOutputPG(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_retired_%v", retired), func(t *testing.T) { progressCheckpointsAuthenticatedOutputPG(t, retired) })
	}
}

func progressCheckpointsAuthenticatedOutputPG(t *testing.T, retired bool) {
	c, _, fx := admissionCoordinator(t, 3)
	ctx := context.Background()
	if report := c.Dispatch(ctx); len(report.Launched) != 1 {
		t.Fatalf("investigation: %+v", report)
	}
	owner, err := c.Store.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	plan := finishReportedInvestigation(t, ctx, c.Store, owner, func(plan *factory.InvestigationPlan) {
		check := plan.Checks[0]
		check.ID, check.Scope = "integration", factory.CheckScopeIntegration
		plan.Checks = append(plan.Checks, check)
	})
	dispatch := c.Dispatch(ctx)
	if len(dispatch.Launched) != 1 {
		t.Fatalf("implement: %+v", dispatch)
	}
	run, err := c.Store.FactoryRun(ctx, dispatch.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := c.Store.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := c.Store.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store.ActivateObservedMCPCapability(ctx, capability); err != nil {
		t.Fatal(err)
	}
	capability, err = c.Store.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := project.FactoryState{ID: run.ID, Project: run.ProjectID, Container: strings.Repeat("c", 64), Invocation: factory.NewID(), Phase: project.FactoryRunning, Live: true, LeaseID: "progress-lease", Generation: 1, Delivered: true}
	fx.broker.get = func(kind, id string) (identity.Execution, error) {
		if id != run.ID {
			return identity.Execution{}, identity.ErrNotFound
		}
		return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionLive, Digest: strings.Repeat("d", 64), LeaseID: state.LeaseID, Binding: &identity.Binding{Kind: kind, ID: id, Generation: state.Generation, Project: state.Container, InvocationID: state.Invocation}}, nil
	}
	previousInspect := fx.host.inspect
	fx.host.inspect = func(req project.FactoryInspect) (project.FactoryState, error) {
		if req.ID == run.ID {
			return state, nil
		}
		return previousInspect(req)
	}
	var events []project.TaskEvent
	appendEvent := func(kind, detail string) {
		event := project.TaskEvent{Sequence: int64(len(events) + 1), Type: kind, TaskKey: plan.Tasks[0].ID, ChildID: "019abcde-0123-4567-89ab-0123456789ab", Invocation: state.Invocation, PlanDigest: project.ExecutionManifestDigest(run.ExecutionManifest), Detail: detail}
		if len(events) > 0 {
			event.PrevDigest = project.TaskEventDigest(events[len(events)-1])
		}
		events = append(events, event)
	}
	output, candidate, tree := phaseTestSHA("released"), strings.Repeat("8", 40), strings.Repeat("9", 40)
	appendEvent(project.TaskEventChildStartIntent, "")
	// The start intent carries no native identity before the observation.
	events[0].ChildID = ""
	appendEvent(project.TaskEventChildStarted, "")
	appendEvent(project.TaskEventChildReported, output)
	var exact project.FactoryTaskCheckpoint
	calls, finalCalls := 0, 0
	host := &progressTaskJournalHost{fakeDispatchHost: fx.host, read: func(req project.FactoryTaskEvents) (project.FactoryTaskEventsState, error) {
		return project.FactoryTaskEventsState{ID: req.ID, Project: req.Project, Events: append([]project.TaskEvent(nil), events[req.AfterSequence:]...), NextSequence: int64(len(events) + 1)}, nil
	}, checkpoint: func(req project.FactoryTaskCheckpoint, final bool) (project.FactoryTaskCheckpointState, error) {
		if !final {
			calls++
			if calls == 1 {
				exact = req
				if req.ExpectedHead != plan.SourceCommit || req.OutputDigest != output {
					t.Fatalf("wrong task binding: %+v", req)
				}
				appendEvent(project.TaskEventCheckpointReady, "ci")
				appendEvent(project.TaskEventTaskCommitted, "tree="+tree+" commit="+candidate)
				return project.FactoryTaskCheckpointState{}, errors.New("lost native reply after commit")
			}
			if req != exact {
				t.Fatalf("lost reply changed exact parent: first=%+v next=%+v", exact, req)
			}
		} else {
			finalCalls++
			if req.ExpectedHead != candidate {
				t.Fatal("final did not bind committed task head")
			}
		}
		result := project.FactoryTaskCheckpointState{Request: req, FinalCandidate: final, Status: "committed", Candidate: candidate, Tree: tree, ReceiptDigest: phaseTestSHA("native receipt")}
		if final {
			result.Checks = []project.CheckpointCheckResult{{ID: "integration", Passed: true, LogDigest: phaseTestSHA("check log")}}
		}
		return result, nil
	}}
	c.Host = host
	readbacks := 0
	host.readCheckpoint = func(req project.FactoryTaskCheckpoint) (project.FactoryTaskCheckpointState, error) {
		if !retired || state.Live {
			t.Fatal("receipt recovery bypassed live progress")
		}
		readbacks++
		return host.checkpoint(req, true)
	}
	c.advanceProgress(ctx) // Import observed output; observer alone authorizes no checkpoint.
	c.advanceProgress(ctx)
	if calls != 0 {
		t.Fatal("unproposed observed output started a checkpoint")
	}
	assignment.Result = &factory.AssignmentResult{Candidate: candidate}
	publication := PublishReport{}
	if c.gatePhasedPublication(ctx, assignment, &publication) || len(publication.Waits) != 1 || publication.Waits[0].Reason != PlanWaitTasksIncomplete {
		t.Fatalf("unsettled task output bypassed publication gate: %+v", publication)
	}
	payload, err := json.Marshal(taskOutputProposal{TaskID: plan.Tasks[0].ID, ManifestDigest: project.ExecutionManifestDigest(run.ExecutionManifest), OutputDigest: output, NativeChildRef: events[2].ChildID, EvidenceRefs: []string{"observed native output"}})
	if err != nil {
		t.Fatal(err)
	}
	submission := testMCPSubmission(capability, factory.MCPToolSubmitTaskOutput, factory.NewID(), payload)

	if _, err := c.Store.RecordMCPSubmission(ctx, submission); err != nil {
		t.Fatal(err)
	}
	c.advanceProgress(ctx) // Reserve request before lost native reply.
	receipt, err := c.Store.MCPWorkStatus(ctx, assignment.ID, submission.SubmissionID)
	var frozen execution.CheckpointRequest
	wire, marshalErr := json.Marshal(exact)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := json.Unmarshal(wire, &frozen); err != nil {
		t.Fatal(err)
	}
	if err != nil || calls != 1 || receipt.State != factory.MCPWorkVerifying || receipt.Checkpoint == nil || !receipt.Checkpoint.Request.Equal(frozen) {
		t.Fatalf("lost reply reservation: %+v calls=%d err=%v", receipt, calls, err)
	}
	c.advanceProgress(ctx) // Import committed journal before receipt readback.
	c.advanceProgress(ctx) // Read back the retained exact request, never nominate the newer head.
	receipt, err = c.Store.MCPWorkStatus(ctx, assignment.ID, submission.SubmissionID)
	if err != nil || calls != 2 || receipt.State != factory.MCPWorkAccepted {
		t.Fatalf("checkpoint readback: %+v calls=%d err=%v", receipt, calls, err)
	}
	publication = PublishReport{}
	if c.gatePhasedPublication(ctx, assignment, &publication) || len(publication.Waits) != 1 || publication.Waits[0].Reason != PlanWaitFinalIntegration {
		t.Fatalf("tasks bypassed final checks: %+v", publication)
	}
	terminal := testMCPSubmission(capability, factory.MCPToolFinishImplement, factory.NewID(), []byte(`{"candidate":"`+candidate+`"}`))
	if _, err := c.Store.RecordMCPSubmission(ctx, terminal); err != nil {
		t.Fatal(err)
	}
	if settled, err := SettleMCPTerminalProposal(ctx, c.Store, assignment.ID, time.Now()); err != nil || settled {
		t.Fatalf("terminal closed active checkpoint authority: settled=%v err=%v", settled, err)
	}
	if retired {
		state.Live = false
		if err := c.recoverFinalCheckpoint(ctx, run, nativeRunObservation(t, state)); err != nil {
			t.Fatal(err)
		}
		if settled, err := SettleMCPTerminalProposal(ctx, c.Store, assignment.ID, time.Now()); err != nil || !settled {
			t.Fatalf("retired final readback did not settle: %v %v", settled, err)
		}
	} else {
		c.advanceProgress(ctx)
	}
	stored, err := c.Store.FactoryRun(ctx, run.ID)
	if err != nil || finalCalls != 1 || stored.FinalCheckpoint == nil {
		t.Fatalf("final receipt missing: %+v calls=%d err=%v", stored.FinalCheckpoint, finalCalls, err)
	}
	if retired && readbacks != 1 || !retired && readbacks != 0 {
		t.Fatalf("unexpected read-only recovery count %d", readbacks)
	}
	finished, err := c.Store.Assignment(ctx, assignment.ID)
	if err != nil || finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.Succeeded {
		t.Fatalf("exact native final did not settle terminal: %+v err=%v", finished, err)
	}
	publication = PublishReport{}
	if !c.gatePhasedPublication(ctx, assignment, &publication) {
		t.Fatalf("native final candidate refused: %+v", publication)
	}
	capability, err = c.Store.GetMCPCapability(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := c.Store.FactoryCurrentContext(ctx, capability, time.Now())
	if err != nil || current.Role.Plan == nil || current.Role.Plan.FinalCheckpoint == nil || !current.Role.Plan.FinalCheckpoint.Request.Equal(stored.FinalCheckpoint.Request) || current.Role.Plan.VerifiedIntegrationCommit == nil || *current.Role.Plan.VerifiedIntegrationCommit != candidate {
		t.Fatal("current context lost the bound integration receipt", current.Role.Plan, err)
	}
	stored.FinalCheckpoint = nil
	if err := c.Store.SaveFactoryRun(ctx, stored); err != nil {
		t.Fatal(err)
	}
	c.advanceProgress(ctx)
	if finalCalls != 1 {
		t.Fatal("completed integration checks reran")
	}
	changed := *receipt.Checkpoint
	changed.Request.Invocation = factory.NewID()
	if err := c.Store.SaveFactoryFinalCheckpoint(ctx, run, changed); err == nil {
		t.Fatal("task receipt replaced final integration evidence")
	}
}

// Exercise real dispatch, progress polling and receipt transactions against
// PostgreSQL. The native adapter supplies observed journal pages; no model
// completion text or direct receipt injection completes the task.
func TestProgressImportsTaskJournalPG(t *testing.T) {
	c, _, fx := admissionCoordinator(t, 3)
	ctx := context.Background()
	if report := c.Dispatch(ctx); len(report.Launched) != 1 {
		t.Fatalf("investigation did not launch: %+v", report)
	}
	owner, err := c.Store.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	plan := finishReportedInvestigation(t, ctx, c.Store, owner, nil)
	report := c.Dispatch(ctx)
	if len(report.Launched) != 1 {
		t.Fatalf("implementation did not launch: %+v", report)
	}
	run, err := c.Store.FactoryRun(ctx, report.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	launch := fx.host.launches[len(fx.host.launches)-1]
	if !bytes.Equal(run.ExecutionManifest, launch.ExecutionManifest) {
		t.Fatal("dispatch did not retain exact launch manifest")
	}
	invocation, child := factory.NewID(), factory.NewID()
	commit, tree := strings.Repeat("8", 40), strings.Repeat("9", 40)
	events := []project.TaskEvent{
		{Type: project.TaskEventChildStartIntent},
		{Type: project.TaskEventChildStarted, ChildID: child},
		{Type: project.TaskEventChildReported, ChildID: child, Detail: phaseTestSHA("observed output")},
		{Type: project.TaskEventCheckpointReady, ChildID: child, Detail: "ci"},
		{Type: project.TaskEventTaskCommitted, ChildID: child, Detail: "tree=" + tree + " commit=" + commit},
		{Type: project.TaskEventRunStopped, Detail: "native parent retired"},
	}
	for i := range events {
		events[i].Sequence, events[i].TaskKey = int64(i+1), plan.Tasks[0].ID
		events[i].Invocation, events[i].PlanDigest = invocation, project.ExecutionManifestDigest(run.ExecutionManifest)
		if i > 0 {
			events[i].PrevDigest = project.TaskEventDigest(events[i-1])
		}
	}
	previousInspect := fx.host.inspect
	fx.host.inspect = func(req project.FactoryInspect) (project.FactoryState, error) {
		if req.ID == run.ID {
			return project.FactoryState{ID: run.ID, Project: run.ProjectID, Invocation: invocation, Container: strings.Repeat("c", 64), LeaseID: "journal-lease", Generation: 1, Delivered: true, Phase: project.FactoryRunning, Live: true}, nil
		}
		return previousInspect(req)
	}
	fx.broker.get = func(kind, id string) (identity.Execution, error) {
		if id != run.ID {
			return identity.Execution{}, identity.ErrNotFound
		}
		return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionLive, Digest: strings.Repeat("d", 64), LeaseID: "journal-lease", Binding: &identity.Binding{Kind: kind, ID: id, Generation: 1, Project: strings.Repeat("c", 64), InvocationID: invocation}}, nil
	}
	// Initially serve only task completion, then the terminal journal record.
	available := 5
	replayChanged := false
	var cursors []int64
	c.Host = &progressTaskJournalHost{fakeDispatchHost: fx.host, read: func(req project.FactoryTaskEvents) (project.FactoryTaskEventsState, error) {
		cursors = append(cursors, req.AfterSequence)
		if replayChanged {
			changed := events[4]
			changed.Detail = "tree=" + tree + " commit=" + strings.Repeat("7", 40)
			return project.FactoryTaskEventsState{ID: req.ID, Project: req.Project, Events: []project.TaskEvent{changed}, NextSequence: 6}, nil
		}
		page := append([]project.TaskEvent(nil), events[req.AfterSequence:available]...)
		return project.FactoryTaskEventsState{ID: req.ID, Project: req.Project, Events: page, NextSequence: int64(available + 1), Terminal: available == len(events)}, nil
	}}
	c.advanceProgress(ctx)
	receipt, err := c.Store.PlanTaskReceipt(ctx, plan.AttemptRoot, plan.ID, plan.Tasks[0].ID)
	if err != nil || receipt.State != store.PlanTaskCompleted || receipt.Commit != commit || receipt.Tree != tree || receipt.Revision != 5 || len(receipt.Checks) != 1 {
		t.Fatalf("progress did not import complete native evidence: %+v (%v)", receipt, err)
	}
	c.advanceProgress(ctx)
	again, err := c.Store.PlanTaskReceipt(ctx, plan.AttemptRoot, plan.ID, plan.Tasks[0].ID)
	if err != nil || again.Revision != receipt.Revision || len(cursors) != 2 || cursors[0] != 0 || cursors[1] != 5 {
		t.Fatalf("progress replay changed receipts or cursor: %+v cursors=%v (%v)", again, cursors, err)
	}
	available = len(events)
	state, err := c.Host.InspectRun(ctx, execution.RunAddress{Workspace: run.ProjectID, Run: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	replayChanged = true
	if _, _, err := c.importRunTaskEvents(ctx, run, state); !errors.Is(err, ErrTaskEventGap) {
		t.Fatalf("changed replay bytes advanced the stored cursor: %v", err)
	}
	replayChanged = false
	previous := events[5].PrevDigest
	events[5].PrevDigest = strings.Repeat("a", 64)
	if _, _, err := c.importRunTaskEvents(ctx, run, state); !errors.Is(err, ErrTaskEventBinding) {
		t.Fatalf("cross-page chain rewrite advanced stored evidence: %v", err)
	}
	unchanged, err := c.Store.PlanTaskReceipt(ctx, plan.AttemptRoot, plan.ID, plan.Tasks[0].ID)
	if err != nil || unchanged.Revision != again.Revision || importCursor([]store.PlanTaskReceipt{unchanged}) != 5 {
		t.Fatalf("rejected page changed stored receipt: revision=%d err=%v", unchanged.Revision, err)
	}
	events[5].PrevDigest = previous
	if _, stop, err := c.importRunTaskEvents(ctx, run, state); err != nil || !stop {
		t.Fatalf("terminal import did not preserve stop: stop=%v err=%v", stop, err)
	}
	if drained, stop, err := c.importRunTaskEvents(ctx, run, state); err != nil || !drained || !stop {
		t.Fatalf("completed-task terminal cursor was lost: drained=%v stop=%v err=%v", drained, stop, err)
	}
	// A supervisor observation without the manifest must preserve it.
	run.ExecutionManifest = nil
	if err := c.Store.SaveFactoryRun(ctx, run); err == nil {
		t.Fatal("stale observation erased the credential identity captured during progress")
	}
	run, err = c.Store.FactoryRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.ExecutionManifest = nil
	if err := c.Store.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	preserved, err := c.Store.FactoryRun(ctx, run.ID)
	if err != nil || !bytes.Equal(preserved.ExecutionManifest, launch.ExecutionManifest) {
		t.Fatal("supervisor observation overwrote launch manifest")
	}
	changed := launch
	manifest, _ := project.ParseExecutionManifest(launch.ExecutionManifest)
	manifest.MaxChildren = 1
	changed.ExecutionManifest, _ = project.MarshalExecutionManifest(manifest)
	changed.Run.ManifestDigest = project.ExecutionManifestDigest(changed.ExecutionManifest)
	if err := c.Store.BindFactoryExecutionManifest(ctx, changed); !errors.Is(err, store.ErrStaleRevision) {
		t.Fatalf("different launch bytes replaced recorded manifest: %v", err)
	}
	// A later plan head cannot substitute evidence for this exact launch.
	replacement := plan
	replacement.ID, replacement.Generation = factory.NewID(), plan.Generation+1
	replacement.Approach = "replacement investigation"
	replacement.Digest, err = factory.PlanDigest(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store.ReplacePlanHead(ctx, replacement, plan.ID); err != nil {
		t.Fatal(err)
	}
	implementation, err := c.Store.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	implementation.Result = &factory.AssignmentResult{Candidate: commit}
	publication := PublishReport{}
	if c.gatePhasedPublication(ctx, implementation, &publication) || len(publication.Waits) != 1 || publication.Waits[0].Reason != PlanRefuseEvidence {
		t.Fatalf("replacement head substituted for admitted launch: %+v", publication)
	}
}

// admissionCoordinator seeds one repository with the given ready issues and
// wires a planning coordinator over them, mirroring planningCoordinator.
func admissionCoordinator(t *testing.T, issues ...int64) (*Coordinator, *planningChooser, dispatchFixture) {
	t.Helper()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	evidence := map[string]AcceptanceEvidence{}
	for _, issue := range issues {
		decision := fx.accept(t, issue, fmt.Sprintf("d%024d", issue))
		fx.queue(t, issue, decision.ID)
		key := fmt.Sprintf("%d/%d", fx.repo, issue)
		input := fx.reads.inputs[key]
		evidence[key] = AcceptanceEvidence{Revision: input.Revision, Issue: AcceptanceIssueView{
			Index: fmt.Sprintf("%d", issue), TitleOccurrence: input.Issue.TitleOccurrence,
			TitleDigest: input.Issue.TitleDigest, ContentDigest: input.Issue.ContentDigest,
			ContentVer: input.Issue.ContentVer, Visible: true,
		}, Comments: []AcceptanceComment{{ID: "11", Digest: input.Comments[0].Digest, Visible: true}}}
	}
	// Launched runs stay live under inspection so recovery holds them
	// instead of proving them unused; settlement arrives through the
	// reconciled run observation below.
	launched := map[string]bool{}
	fx.host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
		if launched[in.ID] {
			return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryRunning}, nil
		}
		return project.FactoryState{}, host.ErrRunNotFound
	}
	inner := fx.host.launch
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		launched[in.Run.ID] = true
		return inner(in)
	}
	source := &fakeEvidenceSource{evidence: evidence}
	chooser := &planningChooser{}
	c := NewCoordinator(db, fx.host, fx.broker)
	c.AcceptanceReads, c.DispatchReads = source, fx.reads
	c.Planning = &PlanningJudgments{Chooser: chooser}
	return c, chooser, fx
}

// finishReportedInvestigation completes the stored owner with a reported
// executable plan bound to the owner's attempt root, source and
// preparation, then persists the finished assignment.
func finishReportedInvestigation(t *testing.T, ctx context.Context, db *store.Store, owner factory.Assignment, mutate func(*factory.InvestigationPlan)) factory.InvestigationPlan {
	t.Helper()
	plan := phaseTestPlan(t, owner.AttemptRoot, owner.SourceCommit, owner.Preparation)
	if mutate != nil {
		mutate(&plan)
		digest, err := factory.PlanDigest(plan)
		if err != nil {
			t.Fatal(err)
		}
		plan.Digest = digest
		if err := plan.Validate(); err != nil {
			t.Fatalf("mutated plan invalid: %v", err)
		}
	}
	run, err := db.FactoryRun(ctx, owner.Run)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "host says so", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatalf("investigation run unsettled: %v", err)
	}
	output := phaseFinish(t, factory.PhaseResult{
		Version: 2, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	})
	if _, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); !ok {
		t.Fatal("completed investigation unaccounted")
	}
	return plan
}

// T10 (FW12 scope): two ready issues produce one persisted investigation.
// The unselected issue gets no assignment and no prompt run; a replayed
// pass spends no duplicate investigation.
func TestSelectedInvestigationAdmitsOnce(t *testing.T) {
	c, _, fx := admissionCoordinator(t, 3, 4)
	ctx := context.Background()
	report := c.Dispatch(ctx)
	if len(report.Launched) != 1 || len(fx.host.launches) != 1 {
		t.Fatalf("two ready issues launched %+v", report)
	}
	selected := report.Launched[0].Issue
	unselected := int64(3)
	if selected == 3 {
		unselected = 4
	}
	if _, err := c.Store.LatestIssueAssignment(ctx, fx.repo, selected); err != nil {
		t.Fatalf("selected investigation unpersisted: %v", err)
	}
	if _, err := c.Store.LatestIssueAssignment(ctx, fx.repo, unselected); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unselected issue incurred investigation: %v", err)
	}
	replay := c.Dispatch(ctx)
	if len(replay.Launched) != 0 || len(fx.host.launches) != 1 {
		t.Fatalf("replay duplicated investigation %+v", replay)
	}
}

// FW12: a completed investigation advances exactly once to its
// implementation child while persisting and admitting the reported plan
// head. Restart reuses the admitted phase instead of relaunching.
func TestCompletedInvestigationAdvancesOnce(t *testing.T) {
	c, _, fx := admissionCoordinator(t, 3)
	ctx := context.Background()
	if report := c.Dispatch(ctx); len(report.Launched) != 1 {
		t.Fatalf("investigation did not launch %+v", report)
	}
	owner, err := c.Store.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	plan := finishReportedInvestigation(t, ctx, c.Store, owner, nil)
	advance := c.Dispatch(ctx)
	if len(advance.Launched) != 1 {
		t.Fatalf("completed investigation did not advance %+v", advance)
	}
	child, err := c.Store.Assignment(ctx, advance.Launched[0].AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Phase != project.ExecutionImplement || child.ParentAssignment != owner.ID {
		t.Fatalf("child is not the implementation advance: %+v", child)
	}
	head, _, err := c.Store.PlanHead(ctx, owner.AttemptRoot)
	if err != nil || head != plan.ID {
		t.Fatalf("plan head = %q, want %q (%v)", head, plan.ID, err)
	}
	restart := c.Dispatch(ctx)
	if len(restart.Launched) != 0 {
		t.Fatalf("restart relaunched the admitted phase %+v", restart)
	}
	if again, _, err := c.Store.PlanHead(ctx, owner.AttemptRoot); err != nil || again != plan.ID {
		t.Fatalf("restart moved the admitted head to %q (%v)", again, err)
	}
}

// FW12: a plan carrying unresolved mandatory facts is not executable and
// can never launch implementation. The issue waits for explicit Retry,
// and no plan head is persisted for it.
func TestUnresolvedPlanCannotLaunch(t *testing.T) {
	c, _, fx := admissionCoordinator(t, 3)
	ctx := context.Background()
	if report := c.Dispatch(ctx); len(report.Launched) != 1 {
		t.Fatalf("investigation did not launch %+v", report)
	}
	owner, err := c.Store.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	finishReportedInvestigation(t, ctx, c.Store, owner, func(plan *factory.InvestigationPlan) {
		plan.Unresolved = []string{"which endpoint owns the export"}
	})
	advance := c.Dispatch(ctx)
	if len(advance.Launched) != 0 {
		t.Fatalf("unresolved plan launched %+v", advance)
	}
	if reason := waitReason(advance, 3); reason != WaitAttemptRecorded {
		t.Fatalf("unresolved plan waits %q, want explicit retry", reason)
	}
	if _, _, err := c.Store.PlanHead(ctx, owner.AttemptRoot); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unresolved plan persisted a head: %v", err)
	}
}

// FW12: changed source inputs trigger reassessment instead of launching
// from the stale investigation. The moved tip first stages a source
// preparation rebuild; the stale head persists nothing either way.
func TestChangedSourceWithholdsAdvance(t *testing.T) {
	c, _, fx := admissionCoordinator(t, 3)
	ctx := context.Background()
	if report := c.Dispatch(ctx); len(report.Launched) != 1 {
		t.Fatalf("investigation did not launch %+v", report)
	}
	owner, err := c.Store.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	finishReportedInvestigation(t, ctx, c.Store, owner, nil)
	moved := fx.reads.inputs[fmt.Sprintf("%d/3", fx.repo)]
	moved.Tip = strings.Repeat("d", 40)
	fx.reads.inputs[fmt.Sprintf("%d/3", fx.repo)] = moved
	for pass := 0; pass < 2; pass++ {
		advance := c.Dispatch(ctx)
		if len(advance.Launched) != 0 {
			t.Fatalf("stale investigation launched %+v", advance)
		}
		if reason := waitReason(advance, 3); reason != WaitPreparation {
			t.Fatalf("moved source waits %q, want source preparation rebuild", reason)
		}
		if _, _, err := c.Store.PlanHead(ctx, owner.AttemptRoot); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("stale investigation persisted a head: %v", err)
		}
	}
}

// FW12: admission persists the reported revision once, admits the current
// head against live bindings, replays without duplicate writes, and
// refuses stale bindings, forged identities and unexecutable plans.
func TestAdmitInvestigationPlanBindings(t *testing.T) {
	db, _ := dispatchTestDB(t)
	ctx := context.Background()
	owner := phaseTestAssignment(t, project.ExecutionInvestigate, project.RoleCoder, true)
	plan := phaseTestPlan(t, owner.AttemptRoot, owner.SourceCommit, owner.Preparation)
	output := phaseFinish(t, factory.PhaseResult{
		Version: 2, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	})
	finished, err := buildRunFinish(t, owner, phaseTestRun(owner, factory.Succeeded), output, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	live, err := livePlanBindings(factory.Acceptance{}, factory.IssueControl{}, owner.SourceCommit, owner.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := admitInvestigationPlan(ctx, db, finished, live)
	if err != nil || admitted.Digest != plan.Digest {
		t.Fatalf("fresh plan unadmitted: %+v %v", admitted, err)
	}
	replayed, err := admitInvestigationPlan(ctx, db, finished, live)
	if err != nil || replayed.Digest != plan.Digest {
		t.Fatalf("replay did not reuse the admitted phase: %+v %v", replayed, err)
	}
	stale := live
	stale.SourceCommit = strings.Repeat("d", 40)
	if _, err := admitInvestigationPlan(ctx, db, finished, stale); !errors.Is(err, store.ErrStalePlanBindings) {
		t.Fatalf("stale source admitted: %v", err)
	}
	forged := plan
	forged.Approach = "a different approach under the same identity"
	forged.Digest = ""
	redigest, err := factory.PlanDigest(forged)
	if err != nil {
		t.Fatal(err)
	}
	forged.Digest = redigest
	if _, err := admitInvestigationPlan(ctx, db, withPlan(finished, forged), live); !errors.Is(err, store.ErrPlanConflict) {
		t.Fatalf("forged plan identity admitted: %v", err)
	}
}

func withPlan(owner factory.Assignment, plan factory.InvestigationPlan) factory.Assignment {
	phased := *owner.Result.PhaseResult
	out := *owner.Result.PhaseResult.Output
	out.Plan = &plan
	phased.Output = &out
	result := *owner.Result
	result.PhaseResult = &phased
	owner.Result = &result
	return owner
}

// FW12: prerequisite bindings are canonical over outcome references:
// order-free, sensitive to reference changes, and refused when a
// reference is missing.
func TestLivePlanBindingsCanonical(t *testing.T) {
	code := factory.AcceptedPrerequisite{
		Occurrence: "21", DependsOn: "9", EndpointRepo: 7, EndpointIssue: 9,
		Outcome: factory.PrereqCode, PrereqAcceptance: "d" + strings.Repeat("f", 24),
	}
	decision := factory.Acceptance{Prerequisites: []factory.AcceptedPrerequisite{code}}
	control := factory.IssueControl{EndpointHeads: map[string]string{"21": "d" + strings.Repeat("f", 24)}}
	tip, prep := strings.Repeat("c", 40), "f"+strings.Repeat("b", 24)
	first, err := livePlanBindings(decision, control, tip, prep)
	if err != nil || len(first.Prerequisites) != 1 {
		t.Fatalf("bindings unbuilt: %+v %v", first, err)
	}
	moved := factory.IssueControl{EndpointHeads: map[string]string{"21": "d" + strings.Repeat("0", 24)}}
	second, err := livePlanBindings(decision, moved, tip, prep)
	if err != nil {
		t.Fatal(err)
	}
	if first.Prerequisites[0] == second.Prerequisites[0] {
		t.Fatal("changed prerequisite reference kept its binding")
	}
	empty := factory.IssueControl{}
	if _, err := livePlanBindings(decision, empty, tip, prep); !errors.Is(err, errPlanInputsIncomplete) {
		t.Fatalf("missing reference built bindings: %v", err)
	}
}
