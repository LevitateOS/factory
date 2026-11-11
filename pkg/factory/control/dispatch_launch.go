package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// executeFreshAttempt records one fresh assignment under the open gate and
// launches its first run. Registration, packet and the admission-gate
// limit recheck land in one transaction before any host call, so a
// concurrent pass either wins the issue or consumes the room first; the
// loser waits on a refreshed snapshot instead of admitting stale work.
func executeFreshAttempt(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, plan *attemptPlan, repository, issue int64, report *DispatchReport, retry *factory.RetryDecision) {
	now := time.Now()
	assignmentID, runID := factory.NewID(), factory.NewID()
	if plan.preparationOwner != "" {
		assignmentID = plan.preparationOwner
	}
	if retry != nil {
		assignmentID = retry.CommandID
	}
	attemptRoot := plan.attemptRoot
	if attemptRoot == "" {
		attemptRoot = assignmentID
	}
	authority := plan.effective.Authority
	role := project.RoleCoder
	publication := assignmentID
	authority.RequirementsID, authority.ApprovalID = plan.requirement, plan.approval
	assignment := factory.Assignment{
		ExternalPlanning: plan.externalPlanning,
		AcceptedContext: plan.acceptedContext,
		Authority:       authority,
		ID:              assignmentID, AttemptRoot: attemptRoot, PublicationAssignment: publication,
		Subject: plan.subject, Phase: plan.phase, ParentAssignment: plan.parent,
		ProjectID: plan.projectID, Role: role,
		Repository: repository, Issue: issue, NativeRev: plan.inputs.Revision,
		Acceptance: plan.acceptance.ID, Preparation: plan.prep.Preparation.ID,
		Harness: plan.tool.Harness + "-" + plan.tool.Version, HarnessVers: plan.tool.Version,
		AdoptedHarness: plan.tool.appliancePin(),
		Model:          plan.policy.Roles[role].Model, Connection: plan.sponsorship.Connection,
		SourceCommit: plan.inputs.Tip, Prompt: plan.prompt, PromptSHA: plan.promptSHA,
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(), ActorID: plan.sponsorship.ActorID,
	}
	reservation := factory.Reservation{
		AssignmentID: assignmentID, Repository: repository,
		Connection: plan.sponsorship.Connection, State: factory.ReservationHeld, PlannedMinutes: plan.planned,
	}
	run := factory.Run{
		ID: runID, ProjectID: plan.projectID, Role: role, InputSHA: plan.inputs.Tip,
		Started: now, Deadline: plan.deadline,
		Image: plan.tool.Image, Harness: assignment.Harness, Model: assignment.Model,
	}
	view := factory.RunView{RunID: runID, Repository: repository, Issue: issue, Attempt: assignmentID}
	var packetErr error
	if retry == nil {
		packetErr = plan.recordPacket(ctx, deps.Store, assignment, reservation, run, view)
	} else if native := plan.captureRuntime(&assignment, &run); native != nil {
		packetErr = deps.Store.RecordNativeExplicitRetryPacket(ctx, *retry, plan.control, assignment, reservation, run, view, *native)
	} else {
		packetErr = deps.Store.RecordExplicitRetryPacket(ctx, *retry, plan.control, assignment, reservation, run, view)
	}
	if err := packetErr; err != nil {
		if plan.preparationOwner != "" && deps.RetireRetry != nil && retryPreparationInvalidated(err) {
			// A lost commit reply may still have admitted the packet. Retire
			// only after the Store confirms that no assignment owns it.
			if _, readErr := deps.Store.Assignment(ctx, assignmentID); errors.Is(readErr, store.ErrNotFound) {
				if retireErr := deps.RetireRetry(ctx, plan.prep); retireErr != nil {
					report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrHost, Detail: "rejected retry preparation retirement is unconfirmed"})
				}
			}
		}
		reportPacketRefusal(ctx, deps, occupancy, repository, issue, err, report)
		return
	}
	occupancy.hold(reservation)
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: runID, Project: plan.projectID, Role: role,
			Preparation: plan.prep.Preparation.ID,
			Harness:     plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model:      assignment.Model,
			Assignment: plan.promptSHA, SourceCommit: plan.inputs.Tip,
			Connection: plan.sponsorship.Connection, Actor: assignment.ActorID,
			Deadline: plan.deadline, ExecutionPhase: plan.phase,
		},
		Prompt: plan.prompt, HarnessSHA256: plan.tool.SHA256,
	}
	if err := launch.Validate(); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrHost, Detail: "dispatch launch invalid"})
		return
	}
	launchTail(ctx, deps, assignment, run, launch, report)
}

// implementationManifest stages the recorded investigation plan as the
// immutable execution manifest of its implementation run: the plan's
// canonical bytes, digest-bound to the launch. The executor verifies the
// supplied bytes; FW13 owns the child-control envelope around them.
func implementationManifest(plan *factory.InvestigationPlan) ([]byte, error) {
	if plan == nil {
		return nil, errors.New("implementation lacks its recorded plan")
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if err := plan.Executable(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	if len(data) > project.MaxExecutionManifest {
		return nil, errors.New("recorded plan exceeds its manifest bound")
	}
	return data, nil
}

// stageExecutionManifest binds the recorded investigation plan as the full
// immutable execution envelope of one phased run (D05/FW14, C15 wire):
// exact plan bytes and digest, invocation, phase/run/deadline, the
// admitted profile snapshot and the child limit. An omitted profile
// resolves to the product defaults at staging, matching policy creation;
// running snapshots stay explicit. The returned bytes are parsed back
// before use so a construction drift refuses here instead of at the
// native boundary. New native implementations stage sequential tasks with no
// children; appliance callers retain their captured child limit. Corrections
// stage their singleton envelope with no children.
//
// Invocation reuses the fresh run identity. Dispatch observes no native
// invocation: the executor learns systemd's at launch and enforces it for
// task events (record_task_event binds the live binding, never this
// claim). The run identity is the only dispatch-known unique execution
// identity, so it doubles as the invocation claim; executor event
// authorization stays authoritative. FW14 joint for D05 review:
// either accept this doubling with the live binding authoritative, or
// revise D05 so the executor adopts the staged invocation (deep change).
func stageExecutionManifest(plan *factory.InvestigationPlan, runID string, deadline time.Time, profile factory.ExecutionProfile, phase string, maxChildren int) ([]byte, error) {
	planBytes, err := implementationManifest(plan)
	if err != nil {
		return nil, err
	}
	effective := factory.EffectiveExecutionProfile(&profile)
	manifest := project.ExecutionManifest{
		Plan: planBytes, PlanDigest: project.PlanDigest(planBytes),
		Invocation: runID, Phase: phase, Run: runID,
		Deadline: deadline,
		Effort:   effective.Effort, Contributor: effective.Contributor,
		MaxChildren: maxChildren,
	}
	data, err := project.MarshalExecutionManifest(manifest)
	if err != nil {
		return nil, err
	}
	if _, err := project.ParseExecutionManifest(data); err != nil {
		return nil, err
	}
	return data, nil
}

// verifyStagedManifest checks the staged envelope binds the launch it
// rides: digest, phase, run, deadline and contributor rule against the
// recorded run. A construction drift refuses before any host call.
func verifyStagedManifest(launch project.FactoryLaunch) error {
	manifest, err := project.ParseExecutionManifest(launch.ExecutionManifest)
	if err != nil {
		return err
	}
	return manifest.ValidateManifestForRun(launch.Run)
}

// admitImplementChild records one advanced implementation child under the
// open gate and launches its first run. Registration, packet and the
// admission-gate limit recheck land in one transaction before any host
// call, exactly like a fresh attempt. The child keeps its parent's attempt
// root, publication slot, preparation and source: the allowance is retained
// rather than reset, and planning stages no publication capability.
func admitImplementChild(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, plan *attemptPlan, childID string, parent factory.Assignment, report *DispatchReport, retry *factory.RetryDecision) {
	repository, issue := parent.Repository, parent.Issue
	now := time.Now()
	runID := factory.NewID()
	selection := plan.policy.Roles[project.RoleCoder]
	manifest, err := stageExecutionManifest(plan.advancePlan, runID, plan.deadline, selection.Profile, project.ExecutionImplement, plan.implementationChildren())
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrStore, Detail: "recorded plan is unstaged"})
		return
	}
	manifestDigest := project.ExecutionManifestDigest(manifest)
	authority := plan.effective.Authority
	authority.RequirementsID, authority.ApprovalID = plan.requirement, plan.approval
	assignment := factory.Assignment{
		AcceptedContext: plan.acceptedContext,
		Authority:       authority,
		ID:              childID, AttemptRoot: parent.AttemptRoot, ParentAssignment: parent.ID,
		PublicationAssignment: parent.PublicationAssignment,
		Subject:               plan.subject, Phase: project.ExecutionImplement,
		ProjectID: plan.projectID, Role: project.RoleCoder,
		Repository: repository, Issue: issue, NativeRev: plan.inputs.Revision,
		Acceptance: parent.Acceptance, Preparation: plan.prep.Preparation.ID,
		Harness: plan.tool.Harness + "-" + plan.tool.Version, HarnessVers: plan.tool.Version,
		AdoptedHarness: plan.tool.appliancePin(),
		Model:          plan.policy.Roles[project.RoleCoder].Model, Connection: plan.sponsorship.Connection,
		SourceCommit: parent.SourceCommit, Prompt: plan.prompt, PromptSHA: plan.promptSHA,
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(), ActorID: plan.sponsorship.ActorID,
	}
	if assignment.Subject.Kind != parent.Subject.Kind ||
		assignment.Subject.SubjectKey(repository) != parent.Subject.SubjectKey(repository) ||
		assignment.Subject.Issue == nil || parent.Subject.Issue == nil ||
		assignment.Subject.Issue.Acceptance != parent.Subject.Issue.Acceptance ||
		assignment.Acceptance != parent.Subject.Issue.Acceptance {
		report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrStore, Detail: "dispatch assignment does not match its subject"})
		return
	}
	reservation := factory.Reservation{
		AssignmentID: childID, Repository: repository,
		Connection: plan.sponsorship.Connection, State: factory.ReservationHeld, PlannedMinutes: plan.planned,
	}
	run := factory.Run{
		ID: runID, ProjectID: plan.projectID, Role: project.RoleCoder, InputSHA: parent.SourceCommit,
		ContextPending: deps.mcpConfigured(), ExecutionManifest: manifest,
		Started: now, Deadline: plan.deadline,
		Image: plan.tool.Image, Harness: assignment.Harness, Model: assignment.Model,
	}
	view := factory.RunView{RunID: runID, Repository: repository, Issue: issue, Attempt: childID}
	var packetErr error
	if retry == nil {
		packetErr = plan.recordPacket(ctx, deps.Store, assignment, reservation, run, view)
	} else if native := plan.captureRuntime(&assignment, &run); native != nil {
		packetErr = deps.Store.RecordNativeImplementationRetryPacket(ctx, *retry, plan.control, assignment, reservation, run, view, *native)
	} else {
		packetErr = deps.Store.RecordImplementationRetryPacket(ctx, *retry, plan.control, assignment, reservation, run, view)
	}
	if packetErr != nil {
		reportPacketRefusal(ctx, deps, occupancy, repository, issue, packetErr, report)
		return
	}
	occupancy.hold(reservation)
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: runID, Project: plan.projectID, Role: project.RoleCoder,
			Preparation: plan.prep.Preparation.ID,
			Harness:     plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model:      assignment.Model,
			Assignment: plan.promptSHA, SourceCommit: parent.SourceCommit,
			Connection: plan.sponsorship.Connection, Actor: assignment.ActorID,
			Deadline: plan.deadline, ExecutionPhase: project.ExecutionImplement,
			ManifestDigest: manifestDigest,
		},
		Prompt: plan.prompt, HarnessSHA256: plan.tool.SHA256,
		ExecutionManifest: manifest,
	}
	if err := launch.Validate(); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrHost, Detail: "dispatch launch invalid"})
		return
	}
	if err := verifyStagedManifest(launch); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrHost, Detail: "dispatch launch invalid"})
		return
	}
	launchTail(ctx, deps, assignment, run, launch, report)
}

// reportPacketRefusal maps one atomic packet refusal to its wait. A refusal
// proves a concurrent admission consumed the room first; the issue waits
// for a later pass instead of launching.
func reportPacketRefusal(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, repository, issue int64, packetErr error, report *DispatchReport) {
	if errors.Is(packetErr, store.ErrDispatchControlStale) {
		report.Waits = append(report.Waits, DispatchWait{Repository: repository, Issue: issue, Reason: WaitInputsChanged, Detail: "queued readiness changed during dispatch"})
		return
	}
	if errors.Is(packetErr, store.ErrDispatchClosed) {
		report.Waits = append(report.Waits, DispatchWait{Repository: repository, Issue: issue, Reason: WaitDispatchClosed, Detail: "dispatch gate closed during dispatch"})
		return
	}
	if errors.Is(packetErr, store.ErrCompletionFenced) {
		reportCompletionFence(repository, issue, report)
		return
	}
	if errors.Is(packetErr, store.ErrAssignmentActive) {
		refreshOccupancy(ctx, deps, occupancy, report)
		report.Waits = append(report.Waits, DispatchWait{Repository: repository, Issue: issue, Reason: WaitAssigned, Detail: "a concurrent dispatch won this issue"})
		return
	}
	if wait := admissionWait(packetErr); wait != nil {
		refreshOccupancy(ctx, deps, occupancy, report)
		report.Waits = append(report.Waits, DispatchWait{Repository: repository, Issue: issue, Reason: wait.Reason, Detail: wait.Detail})
		return
	}
	report.Errors = append(report.Errors, DispatchError{Repository: repository, Issue: issue, Reason: DispatchErrStore, Detail: "dispatch packet unrecorded: " + packetErr.Error()})
}

// retryAttempt relaunches one released assignment after re-checking
// limits, role preparation, harness and inputs. Root prompts are rebuilt
// and compared; child prompts remain their recorded immutable bytes.
// Limit and preparation waits defer quietly: the fresh visit already
// reports the issue as assigned.
func retryAttempt(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) {
	occupancy, control, ok := admitRetryAttempt(ctx, deps, a, report)
	if !ok {
		return
	}
	child := a.PublicationAssignment != a.ID
	publicationTarget := ""
	if child {
		var parentOK bool
		if publicationTarget, parentOK = checkPublishedParent(ctx, deps, a, report); !parentOK {
			return
		}
	}
	plan, ok := planRetryAttempt(ctx, deps, &occupancy, control, a, child, report)
	if !ok {
		return
	}
	if child {
		if !checkRetryChildPlan(ctx, deps, a, plan, publicationTarget, report) {
			return
		}
		if !checkRetryChildRun(ctx, deps, a, plan, report) {
			return
		}
	} else if !bytes.Equal(plan.prompt, a.Prompt) {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "accepted inputs no longer hold", report)
		return
	}
	runID := factory.NewID()
	next, run, ok := recordRetryRun(ctx, deps, a, plan, runID, report)
	if !ok {
		return
	}
	// A retry starts a new provider process, so it adopts the current pin;
	// the same already-captured execution recovers its recorded bytes exactly.
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: runID, Project: a.ProjectID, Role: a.Role,
			Preparation: a.Preparation,
			Harness:     plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model:      a.Model,
			Assignment: a.PromptSHA, SourceCommit: a.SourceCommit,
			Connection: a.Connection, Actor: a.ActorID,
			Deadline: plan.deadline, ExecutionPhase: plan.phase,
		},
		Prompt: a.Prompt, HarnessSHA256: plan.tool.SHA256,
	}
	validateRetryLaunch(ctx, deps, a, next, run, launch, false, report)
}

// routeAttemptWait finishes one planned attempt on terminal waits,
// reporting false when the outcome already landed in the report.
func routeAttemptWait(ctx context.Context, deps DispatchDeps, a factory.Assignment, wait *planWait, report *DispatchReport) bool {
	if wait == nil {
		return true
	}
	switch wait.Reason {
	case WaitAuthority, WaitDispatchClosed, WaitSponsorshipRole:
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonWithdrawn, "grants no longer authorize dispatch", report)
	case WaitInputsChanged, WaitInputsHidden, WaitIssueClosed, WaitIssueLocked:
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "accepted inputs no longer hold", report)
	}
	return false
}

// recordRetryRun records one retry packet behind a planned attempt
// with a fresh run identity. A retry is a new provider process, so the run
// and the calling assignment snapshot adopt the current pin; recovery of
// the same already-captured execution keeps its recorded bytes exactly.
func recordRetryRun(ctx context.Context, deps DispatchDeps, a factory.Assignment, plan *attemptPlan, runID string, report *DispatchReport) (factory.Assignment, factory.Run, bool) {
	var none factory.Assignment
	now := time.Now()
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: a.Role, InputSHA: a.SourceCommit,
		Started: now, Deadline: plan.deadline,
		Image: plan.tool.Image, Harness: plan.tool.Harness + "-" + plan.tool.Version, Model: a.Model,
	}
	// Advance the calling snapshot to the current pin so the packet records
	// the new execution's identity; the store keeps every other binding exact.
	a.Harness = plan.tool.Harness + "-" + plan.tool.Version
	a.HarnessVers = plan.tool.Version
	a.AdoptedHarness = plan.tool.appliancePin()
	view := factory.RunView{RunID: runID, Repository: a.Repository, Issue: a.Issue, Attempt: a.ID}
	next, err := deps.Store.RecordRetryPacket(ctx, a, plan.control, plan.effective.Authority, run, view, plan.planned)
	if err != nil {
		if errors.Is(err, store.ErrDispatchControlStale) {
			report.Waits = append(report.Waits, DispatchWait{Repository: a.Repository, Issue: a.Issue, Reason: WaitInputsChanged, Detail: "queued readiness changed during retry"})
			return none, factory.Run{}, false
		}
		// A limit refusal defers quietly: the fresh visit already
		// reports the issue as assigned, and a later pass retries.
		if admissionWait(err) != nil {
			return none, factory.Run{}, false
		}
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "dispatch attempt unrecorded"})
		return none, factory.Run{}, false
	}
	return next, run, true
}

// validateRetryLaunch validates one retry launch, verifying the
// staged manifest for manifest-bound phases before tailing it.
func validateRetryLaunch(ctx context.Context, deps DispatchDeps, a, next factory.Assignment, run factory.Run, launch project.FactoryLaunch, verifyManifest bool, report *DispatchReport) {
	if err := launch.Validate(); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrHost, Detail: "dispatch launch invalid"})
		return
	}
	if verifyManifest {
		if err := verifyStagedManifest(launch); err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrHost, Detail: "dispatch launch invalid"})
			return
		}
	}
	launchTail(ctx, deps, next, run, launch, report)
}

// admitRetryAttempt snapshots capacity and readiness behind one retry,
// routing phased children to their lineage-bound relaunches.
func admitRetryAttempt(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) (passOccupancy, factory.IssueControl, bool) {
	var none passOccupancy
	var noControl factory.IssueControl
	occupancy, err := snapshotOccupancy(ctx, deps.Store)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "capacity accounting unavailable"})
		return none, noControl, false
	}
	control, err := deps.Store.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		report.Waits = append(report.Waits, DispatchWait{Repository: a.Repository, Issue: a.Issue, Reason: WaitInputsChanged, Detail: "queued readiness is unavailable"})
		return none, noControl, false
	}
	// An advanced implementation child rebinds against its finished
	// investigation parent: there is no publication yet, so the published
	// parent checks below do not apply.
	if a.Phase == project.ExecutionImplement && a.ParentAssignment != "" && a.PublicationAssignment != a.ID {
		retryPhasedChild(ctx, deps, &occupancy, control, a, report)
		return none, noControl, false
	}
	// A phased publication child (review or correction) rebinds against
	// its publication owner and, for corrections, its current reviewer:
	// the recorded prompt bytes stay immutable and the correction
	// manifest restages from the derived singleton plan. A relaunch never
	// recharges: the review round and correction cycle were consumed by
	// the immutable assignment identity at first launch.
	if (a.Phase == project.ExecutionReview || a.Phase == project.ExecutionCorrect) &&
		a.ParentAssignment != "" && a.PublicationAssignment != a.ID &&
		a.Subject.Kind == factory.SubjectIssue {
		retryPhasedPublicationChild(ctx, deps, &occupancy, control, a, report)
		return none, noControl, false
	}
	return occupancy, control, true
}

// checkPublishedParent requires the published parent behind one
// publication child retry with its recorded branch.
func checkPublishedParent(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) (string, bool) {
	publication, err := deps.Store.PublicationByAssignment(ctx, a.PublicationAssignment)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "published parent no longer holds", report)
			return "", false
		}
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "published parent unreadable"})
		return "", false
	}
	if publication.Stage != factory.PublicationPublished || publication.WithdrawRequested || publication.Candidate != a.SourceCommit ||
		publication.Acceptance != a.Acceptance || publication.Repository != a.Repository || publication.Issue != a.Issue ||
		publication.AssignmentID != a.PublicationAssignment {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "published parent no longer holds", report)
		return "", false
	}
	if a.Role != project.RoleCoder && a.Role != project.RoleReviewer {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "child role no longer holds", report)
		return "", false
	}
	return publication.TargetBranch, true
}

// planRetryAttempt plans one retry under its recorded role, routing
// terminal waits to the report.
func planRetryAttempt(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, control factory.IssueControl, a factory.Assignment, child bool, report *DispatchReport) (*attemptPlan, bool) {
	role, exactPreparation := project.RoleCoder, ""
	if child {
		role, exactPreparation = a.Role, a.Preparation
	}
	lineage := attemptLineage{phase: a.Phase, parent: a.ParentAssignment}
	plan, wait, failed := planAttemptForRole(ctx, deps, occupancy, a.Repository, a.Issue, a.Acceptance, control, false, role, exactPreparation, child, "", lineage)
	if failed != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: failed.Reason, Detail: failed.Detail})
		return nil, false
	}
	if !routeAttemptWait(ctx, deps, a, wait, report) {
		return nil, false
	}
	return plan, true
}

// checkRetryChildPlan requires the recorded child profile and
// authority behind one publication child retry.
func checkRetryChildPlan(ctx context.Context, deps DispatchDeps, a factory.Assignment, plan *attemptPlan, publicationTarget string, report *DispatchReport) bool {
	selection := plan.policy.Roles[a.Role]
	// A version move never cancels the retry: the relaunch adopts the
	// current staged pin, so only a family move (a different tool) ends
	// the recorded child. The saved model, connection, preparation and
	// branch still bind exactly.
	harnessMoved := !strings.HasPrefix(a.Harness, plan.tool.Harness+"-")
	if plan.attemptRoot != a.AttemptRoot ||
		plan.sponsorship.Connection != a.Connection || selection.Model != a.Model ||
		harnessMoved ||
		plan.prep.Preparation.ID != a.Preparation || plan.policy.TargetBranch != publicationTarget {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "recorded child profile or authority changed", report)
		return false
	}
	return true
}

// checkRetryChildRun requires the prior child run and environment
// profile behind one publication child retry.
func checkRetryChildRun(ctx context.Context, deps DispatchDeps, a factory.Assignment, plan *attemptPlan, report *DispatchReport) bool {
	previousRun, err := deps.Store.FactoryRun(ctx, a.Run)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "prior child run profile is unavailable", report)
			return false
		}
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "prior child run unreadable"})
		return false
	}
	if previousRun.Admission == nil {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "prior child run profile is unavailable", report)
		return false
	}
	environment, err := deps.Store.EnvironmentGrant(ctx, a.Repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonWithdrawn, "environment authorization no longer holds", report)
			return false
		}
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "environment profile unreadable"})
		return false
	}
	previous := previousRun.Admission
	oldSelection, hadOldSelection := previous.Policy.Roles[a.Role]
	selection := plan.policy.Roles[a.Role]
	if !environment.Active || environment.Profile == nil {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonWithdrawn, "environment authorization no longer holds", report)
		return false
	}
	if !hadOldSelection ||
		previous.Policy.TargetBranch != plan.policy.TargetBranch ||
		!slices.Equal(previous.Policy.Checks, plan.policy.Checks) ||
		oldSelection != selection || previous.Profile == nil || *previous.Profile != *environment.Profile {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "recorded child launch profile changed", report)
		return false
	}
	return true
}

// retryPhasedChild relaunches one interrupted advanced implementation
// child: the recorded prompt bytes are immutable, so planning only rechecks
// live limits, preparation, harness and inputs before recording a fresh run
// for the same assignment. Lineage rebinds against the finished
// investigation parent — same subject, root, publication slot, preparation
// and source — and the staged manifest is remarshaled from the recorded
// plan, so its digest still binds the launch.
func retryPhasedChild(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, control factory.IssueControl, a factory.Assignment, report *DispatchReport) {
	parent, err := deps.Store.Assignment(ctx, a.ParentAssignment)
	if err != nil || !validImplementLineage(a, parent) {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "implementation parent no longer holds", report)
		return
	}
	plan, wait, failed := planAttemptForRole(ctx, deps, occupancy, a.Repository, a.Issue, a.Acceptance, control, false, a.Role, a.Preparation, true, "", attemptLineage{phase: a.Phase, parent: a.ParentAssignment})
	if failed != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: failed.Reason, Detail: failed.Detail})
		return
	}
	if !routeAttemptWait(ctx, deps, a, wait, report) {
		return
	}
	runID := factory.NewID()
	// The recorded prompt bytes are immutable, so the staged envelope is
	// remarshaled from the recorded plan with this retry's fresh run
	// identity, deadline and current limits: its digest still binds the
	// launch below.
	selection := plan.policy.Roles[a.Role]
	manifest, err := stageExecutionManifest(parent.Result.PhaseResult.Output.Plan, runID, plan.deadline, selection.Profile, project.ExecutionImplement, plan.implementationChildren())
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "recorded plan is unstaged"})
		return
	}
	next, run, ok := recordRetryRun(ctx, deps, a, plan, runID, report)
	if !ok {
		return
	}
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: runID, Project: a.ProjectID, Role: a.Role,
			Preparation: a.Preparation,
			Harness:     plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model:      a.Model,
			Assignment: a.PromptSHA, SourceCommit: a.SourceCommit,
			Connection: a.Connection, Actor: a.ActorID,
			Deadline: plan.deadline, ExecutionPhase: project.ExecutionImplement,
			ManifestDigest: project.ExecutionManifestDigest(manifest),
		},
		Prompt: a.Prompt, HarnessSHA256: plan.tool.SHA256,
		ExecutionManifest: manifest,
	}
	validateRetryLaunch(ctx, deps, a, next, run, launch, true, report)
}

// validImplementLineage verifies one implementation child still binds its
// recorded investigation parent: a finished reported investigation with an
// executable plan, sharing subject, root, publication slot, preparation and
// source. Anything else supersedes the child instead of executing it.
func validImplementLineage(a, parent factory.Assignment) bool {
	if !validImplementParent(a, parent) {
		return false
	}
	if !validInvestigatePlan(parent) {
		return false
	}
	return implementScopeMatches(a, parent)
}

// validImplementParent verifies one implementation child's parent is
// a finished reported investigation owning the child's parent slot.
func validImplementParent(a, parent factory.Assignment) bool {
	return parent.Validate() == nil && parent.ID == a.ParentAssignment &&
		parent.Phase == project.ExecutionInvestigate && parent.SucceededReported() &&
		parent.Result != nil && parent.Result.PhaseResult != nil
}

// validInvestigatePlan verifies one investigation parent carries a
// completed executable plan.
func validInvestigatePlan(parent factory.Assignment) bool {
	phased := parent.Result.PhaseResult
	return phased.Phase == project.ExecutionInvestigate && phased.Status == "completed" &&
		phased.Output != nil && phased.Output.Plan != nil &&
		phased.Output.Plan.Validate() == nil && phased.Output.Plan.Executable() == nil
}

// implementScopeMatches verifies one implementation child shares its
// subject, root, publication slot, preparation and source with its
// investigation parent.
func implementScopeMatches(a, parent factory.Assignment) bool {
	return a.Role == project.RoleCoder && a.Repository == parent.Repository && a.Issue == parent.Issue &&
		a.Acceptance == parent.Acceptance && a.AttemptRoot == parent.AttemptRoot &&
		a.PublicationAssignment == parent.PublicationAssignment && a.Preparation == parent.Preparation &&
		a.SourceCommit == parent.SourceCommit &&
		a.Subject.Kind == parent.Subject.Kind &&
		a.Subject.SubjectKey(a.Repository) == parent.Subject.SubjectKey(parent.Repository)
}

const launchObservationTimeout = 5 * time.Second

// launchTail briefly observes one recorded attempt's launch, then records
// exactly one of launched, confirmed-unused
// settle-and-release, or fenced-and-held. A returned error never guesses
// the run's effects; the host and broker decide what is unused. The native
// run retains its admitted deadline after this observation ends.
func launchTail(ctx context.Context, deps DispatchDeps, a factory.Assignment, run factory.Run, launch project.FactoryLaunch, report *DispatchReport) {
	if deps.Store == nil {
		return
	}
	captured, err := deps.Store.FactoryRun(ctx, run.ID)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "prelaunch run unavailable"})
		return
	}
	run = captured
	if deps.mcpConfigured() {
		recorded := run
		recorded.ContextPending = true
		if err := deps.Store.SaveFactoryRun(ctx, recorded); err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "prelaunch context hold unrecorded"})
			return
		}
		run = recorded
	}
	// Deliver the choice already retained with this run, including time-only
	// investigation/review phases which intentionally have no plan manifest.
	if launch.Run.Harness == project.FactoryHarnessMuse && run.Admission != nil {
		selection, ok := run.Admission.Policy.Roles[a.Role]
		profile := selection.Profile
		if !ok || selection.Harness != launch.Run.Harness || selection.Model != launch.Run.Model || profile.Validate(selection.Harness) != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "recorded Muse execution profile unavailable"})
			return
		}
		launch.Run.Effort, launch.Run.Contributor = profile.Effort, profile.Contributor
		if err := launch.Validate(); err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "recorded Muse execution profile differs from launch"})
			return
		}
	}
	if deps.Store != nil {
		if len(launch.ExecutionManifest) > 0 {
			if err := deps.Store.BindFactoryExecutionManifest(ctx, launch); err != nil {
				report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "execution manifest could not be retained"})
				return
			}
		}
		if err := ProduceMCPCapabilityForLaunch(ctx, deps.Store, a, run); err != nil {
			if deps.mcpConfigured() {
				recordLaunchFailure(ctx, deps, a, run, errors.New("worker MCP could not be prepared"), report)
				return
			}
		} else if deps.mcpConfigured() {
			capability, err := deps.Store.GetMCPCapability(ctx, run.ID)
			if err != nil {
				report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "worker MCP binding unavailable"})
				return
			}
			secret, err := deps.deriveMCPSecret(ctx, capability)
			if err != nil {
				report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "worker MCP configuration unavailable"})
				return
			}
			launch.MCP = &project.FactoryMCPBinding{Capability: capability.ID, Secret: secret}
		}
	}
	// The context classifier picks how much history the worker sees. Its
	// answer is advice: a failed or unsure choice serves current context.
	// Required current facts must still be readable before paid execution.
	_, _, _ = PrepareContextBundleSelection(ctx, deps.Store, deps.ContextChooser, a, run, deps.ContextVerification)
	if deps.mcpConfigured() {
		capability, err := deps.Store.GetMCPCapability(ctx, run.ID)
		if err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "worker context capability unavailable"})
			return
		}
		current, _, _, err := loadContextBundleState(ctx, deps.Store, capability, a, deps.ContextVerification)
		if err != nil || current.Current.Availability != "complete" {
			report.Waits = append(report.Waits, DispatchWait{Repository: a.Repository, Issue: a.Issue, Reason: "context_selection_hold", Detail: "required current worker context is unavailable"})
			return
		}
	}
	if run.ContextPending {
		run.ContextPending = false
		if err := deps.Store.SaveFactoryRun(ctx, run); err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "prelaunch context admission unrecorded"})
			return
		}
	}
	launchCtx, cancel := context.WithTimeout(ctx, launchObservationTimeout)
	defer cancel()
	run, err = deps.Store.FactoryRun(launchCtx, run.ID)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "captured launch run unavailable"})
		return
	}
	current, err := deps.Store.Assignment(launchCtx, a.ID)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "captured launch assignment unavailable"})
		return
	}
	request, err := run.LaunchRequest(current, launch)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "launch differs from captured runtime admission"})
		return
	}
	state, err := deps.Host.LaunchRun(launchCtx, request)
	if err == nil {
		if err := observeRun(launchCtx, deps.Store, deps.Broker, &run, state); err != nil {
			report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrFence, Detail: "launch evidence unconfirmed; reservation retained"})
			ensureHeldLaunch(ctx, deps, a, report)
			return
		}
		report.Launched = append(report.Launched, DispatchLaunch{
			Repository: a.Repository, Issue: a.Issue,
			AssignmentID: a.ID, RunID: run.ID, Phase: state.Phase,
		})
		return
	}
	recordLaunchFailure(ctx, deps, a, run, err, report)
}

func recordLaunchFailure(ctx context.Context, deps DispatchDeps, a factory.Assignment, run factory.Run, launchErr error, report *DispatchReport) {
	unused, fenced := confirmUnused(ctx, deps, a, &run)
	switch {
	case fenced:
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrHost, Detail: "launch outcome unconfirmed"})
		ensureHeldLaunch(ctx, deps, a, report)
	case !unused:
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrFence, Detail: "launch left effects for reconcile"})
		ensureHeldLaunch(ctx, deps, a, report)
	default:
		settleUnusedLaunch(ctx, deps, a, run, launchErr, report)
	}
}

// ensureHeldLaunch re-holds the reservation behind a fenced launch so
// the uncertain attempt keeps counting against its limits.
func ensureHeldLaunch(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) {
	reservation, err := deps.Store.Reservation(ctx, a.ID)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "assignment reservation unreadable"})
		return
	}
	ensureHeld(ctx, deps, a, reservation, report)
}

// settleUnusedLaunch settles a launch the host and broker never saw and
// releases its confirmed-unused capacity. Attempts remain retryable
// until exhausted; an exhausted assignment finishes failed.
func settleUnusedLaunch(ctx context.Context, deps DispatchDeps, a factory.Assignment, run factory.Run, launchErr error, report *DispatchReport) {
	settled := run
	settled.Outcome, settled.Summary, settled.Reconciled = factory.Failed, boundSummary("dispatch launch refused: "+launchErr.Error(), "run failed"), true
	if err := deps.Store.SaveFactoryRun(ctx, settled); err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "unlaunched run unsettled"})
		return
	}
	if err := deps.Store.ReleaseReservation(ctx, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "reservation release failed"})
		return
	}
	if a.Attempts >= factory.MaxDispatchAttempts {
		finishTerminal(ctx, deps, a, factory.Failed, factory.AssignReasonExhausted, "dispatch launch refused", report)
		report.Waits = append(report.Waits, DispatchWait{Repository: a.Repository, Issue: a.Issue, Reason: WaitLaunchExhausted, Detail: "dispatch launch refused"})
		return
	}
	report.Waits = append(report.Waits, DispatchWait{Repository: a.Repository, Issue: a.Issue, Reason: WaitLaunchRefused, Detail: "dispatch launch refused; retries remain"})
}

// validPublicationChildOwner verifies one publication child still binds its
// recorded publication owner: a finished reported coder owning its own
// publication slot on the same scope, root and acceptance.
func validPublicationChildOwner(a, owner factory.Assignment) bool {
	if owner.Validate() != nil || owner.ID != a.PublicationAssignment ||
		owner.PublicationAssignment != owner.ID || owner.Role != project.RoleCoder ||
		!owner.PublicationOwnerReady() {
		return false
	}
	return owner.Repository == a.Repository && owner.Issue == a.Issue &&
		owner.ProjectID == a.ProjectID && owner.Acceptance == a.Acceptance &&
		owner.AttemptRoot == a.AttemptRoot
}

// validReviewLineage verifies one reviewer child still binds its publication
// owner: the same scope, root and acceptance, on a distinct reviewer
// preparation under the owner's publication slot.
func validReviewLineage(a, owner factory.Assignment) bool {
	if !validPublicationChildOwner(a, owner) {
		return false
	}
	return a.Role == project.RoleReviewer && a.Phase == project.ExecutionReview &&
		a.ParentAssignment == owner.ID &&
		a.Subject.Kind == factory.SubjectIssue && a.Subject.Issue != nil &&
		a.Subject.Issue.Issue == a.Issue && a.Subject.Issue.Acceptance == a.Acceptance &&
		a.Preparation != owner.Preparation
}

// validCorrectLineage verifies one corrector child still binds its
// publication owner and the reviewer that authored its verdict: the same
// scope, root, acceptance and candidate, a single typed review verdict on
// this head, and the owner's coder setup in a session distinct from the reviewer.
func validCorrectLineage(a, owner, reviewer factory.Assignment) bool {
	if !validPublicationChildOwner(a, owner) {
		return false
	}
	if !validCorrectReviewer(a, reviewer) {
		return false
	}
	if !validCorrectVerdict(a, reviewer) {
		return false
	}
	return correctScopeMatches(a, owner, reviewer)
}

// validCorrectReviewer verifies the reviewer behind one corrector
// child: a finished reported review owning the child's parent slot.
func validCorrectReviewer(a, reviewer factory.Assignment) bool {
	return reviewer.Validate() == nil && reviewer.ID == a.ParentAssignment &&
		reviewer.Role == project.RoleReviewer && reviewer.Phase == project.ExecutionReview &&
		reviewer.Succeeded() &&
		reviewer.Result != nil && reviewer.Result.Reported && reviewer.Result.Status == "completed"
}

// validCorrectVerdict verifies one corrector's review verdict carries
// exactly one decision on this head.
func validCorrectVerdict(a, reviewer factory.Assignment) bool {
	verdict := reviewer.Result.PhaseResult
	if verdict == nil || verdict.Phase != project.ExecutionReview || verdict.Output == nil ||
		(verdict.Output.Review == nil) == (verdict.Output.Clear == nil) {
		return false
	}
	return verdict.Output.Clear == nil || verdict.Output.Clear.Head == a.SourceCommit
}

// correctScopeMatches verifies one corrector child shares its scope,
// root, acceptance, candidate and subject with its owner and reviewer
// while retaining its admitted coder setup.
func correctScopeMatches(a, owner, reviewer factory.Assignment) bool {
	return a.Role == project.RoleCoder && a.Phase == project.ExecutionCorrect &&
		reviewer.Repository == a.Repository && reviewer.Issue == a.Issue &&
		reviewer.ProjectID == a.ProjectID && reviewer.Acceptance == a.Acceptance &&
		reviewer.AttemptRoot == a.AttemptRoot &&
		reviewer.PublicationAssignment == a.PublicationAssignment &&
		reviewer.SourceCommit == a.SourceCommit &&
		correctSubjectMatches(a) &&
		a.PublicationCoderPreparation(owner) && a.Preparation != reviewer.Preparation
}

// correctSubjectMatches verifies one corrector child targets its own
// accepted issue subject.
func correctSubjectMatches(a factory.Assignment) bool {
	return a.Subject.Kind == factory.SubjectIssue && a.Subject.Issue != nil &&
		a.Subject.Issue.Issue == a.Issue && a.Subject.Issue.Acceptance == a.Acceptance
}

// newestCommittedCorrectionRunID returns the run behind the newest committed
// review submission on one exact head and base.
func newestCommittedCorrectionRunID(p factory.Publication, head, base string) (string, bool) {
	for i := len(p.ReviewOperations) - 1; i >= 0; i-- {
		op := p.ReviewOperations[i]
		if op.Work.HeadOID != head || op.Work.BaseOID != base {
			continue
		}
		if op.Outcome.Effect != factory.OpEffectCommitted || op.Outcome.Completion != factory.OpCompletionComplete {
			return "", false
		}
		return op.RunID, true
	}
	return "", false
}

// retryPhasedPublicationChild relaunches one interrupted phased reviewer or
// corrector: the recorded prompt bytes are immutable, so planning only
// rechecks live limits, preparation, harness and inputs before recording a
// fresh run for the same assignment. Reviewers relaunch bare; correctors
// restage the derived singleton plan as their correct-phase manifest with
// the retry's fresh run identity. Anything the lineage no longer binds
// supersedes the child instead of executing it.
func retryPhasedPublicationChild(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, control factory.IssueControl, a factory.Assignment, report *DispatchReport) {
	owner, p, ok := loadPhasedPublicationRetry(ctx, deps, a, report)
	if !ok {
		return
	}
	lineage, ok := resolvePhasedRetryLineage(ctx, deps, a, owner, p, report)
	if !ok {
		return
	}
	plan, wait, failed := planAttemptForRole(ctx, deps, occupancy, a.Repository, a.Issue, a.Acceptance, control, false, a.Role, a.Preparation, true, "", lineage)
	if failed != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: failed.Reason, Detail: failed.Detail})
		return
	}
	if !routeAttemptWait(ctx, deps, a, wait, report) {
		return
	}
	// A relaunch reuses the recorded prompt bytes: refuse when the
	// recorded launch profile moved under the child, exactly like the
	// generic publication child retry. A changed launch profile
	// supersedes instead of executing a stale plan.
	if !checkRetryChildPlan(ctx, deps, a, plan, p.TargetBranch, report) {
		return
	}
	if !checkRetryChildRun(ctx, deps, a, plan, report) {
		return
	}
	runID := factory.NewID()
	var manifest []byte
	manifestDigest := ""
	if a.Phase == project.ExecutionCorrect {
		var manifestOK bool
		if manifest, manifestDigest, manifestOK = stageCorrectionRetryManifest(ctx, deps, plan, a, owner, p, runID, report); !manifestOK {
			return
		}
	}
	next, run, ok := recordRetryRun(ctx, deps, a, plan, runID, report)
	if !ok {
		return
	}
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: runID, Project: a.ProjectID, Role: a.Role,
			Preparation: a.Preparation,
			Harness:     plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model:      a.Model,
			Assignment: a.PromptSHA, SourceCommit: a.SourceCommit,
			Connection: a.Connection, Actor: a.ActorID,
			Deadline: plan.deadline, ExecutionPhase: a.Phase,
			ManifestDigest: manifestDigest,
		},
		Prompt: a.Prompt, HarnessSHA256: plan.tool.SHA256,
		ExecutionManifest: manifest,
	}
	validateRetryLaunch(ctx, deps, a, next, run, launch, a.Phase == project.ExecutionCorrect, report)
}

// loadPhasedPublicationRetry loads the publication owner and its
// published candidate behind one phased publication child retry.
func loadPhasedPublicationRetry(ctx context.Context, deps DispatchDeps, a factory.Assignment, report *DispatchReport) (factory.Assignment, factory.Publication, bool) {
	var none factory.Assignment
	var noPublication factory.Publication
	owner, err := deps.Store.Assignment(ctx, a.PublicationAssignment)
	if err != nil || !validPublicationChildOwner(a, owner) {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "publication owner no longer holds", report)
		return none, noPublication, false
	}
	p, err := deps.Store.PublicationByAssignment(ctx, owner.ID)
	if err != nil || p.Stage != factory.PublicationPublished || p.WithdrawRequested ||
		p.Repository != a.Repository || p.Issue != a.Issue || p.Acceptance != a.Acceptance ||
		p.Candidate != a.SourceCommit {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "published candidate no longer holds", report)
		return none, noPublication, false
	}
	return owner, p, true
}

// resolvePhasedRetryLineage binds one phased publication child retry
// to its reviewer or corrector lineage.
func resolvePhasedRetryLineage(ctx context.Context, deps DispatchDeps, a, owner factory.Assignment, p factory.Publication, report *DispatchReport) (attemptLineage, bool) {
	var none attemptLineage
	if a.Phase == project.ExecutionReview {
		if !validReviewLineage(a, owner) {
			finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "reviewer lineage no longer holds", report)
			return none, false
		}
		return attemptLineage{phase: a.Phase, parent: owner.ID}, true
	}
	reviewer, err := deps.Store.Assignment(ctx, a.ParentAssignment)
	if err != nil || !validCorrectLineage(a, owner, reviewer) {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "corrector lineage no longer holds", report)
		return none, false
	}
	current, ok := newestCommittedCorrectionRunID(p, a.SourceCommit, p.PullTarget().BaseOID)
	if !ok || current != reviewer.Run {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "correction answers a superseded review", report)
		return none, false
	}
	return attemptLineage{phase: a.Phase, parent: reviewer.ID}, true
}

// stageCorrectionRetryManifest restages the derived singleton plan
// behind one corrector retry with its fresh run identity.
func stageCorrectionRetryManifest(ctx context.Context, deps DispatchDeps, plan *attemptPlan, a, owner factory.Assignment, p factory.Publication, runID string, report *DispatchReport) ([]byte, string, bool) {
	var cycle factory.ReviewCycle
	found := false
	for _, recorded := range p.ReviewCycles {
		if recorded.CorrectionAssignment == a.ID {
			cycle, found = recorded, true
			break
		}
	}
	if !found {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "correction answers an unrecorded cycle", report)
		return nil, "", false
	}
	origin, err := deps.Store.PublicationExecutionOwner(ctx, p, owner)
	if err != nil || origin.Phase != project.ExecutionInvestigate || origin.Result == nil || origin.Result.PhaseResult == nil ||
		origin.Result.PhaseResult.Output == nil || origin.Result.PhaseResult.Output.Plan == nil {
		finishTerminal(ctx, deps, a, factory.Cancelled, factory.AssignReasonSuperseded, "recorded investigation plan is unavailable", report)
		return nil, "", false
	}
	reviewer, err := deps.Store.Assignment(ctx, a.ParentAssignment)
	if err != nil || reviewer.Result == nil || reviewer.Result.PhaseResult == nil || reviewer.Result.PhaseResult.Output == nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "recorded correction review is unavailable"})
		return nil, "", false
	}
	verdict := reviewer.Result.PhaseResult.Output
	keys := correctionFindingKeys(ResolvedReviewVerdict{Review: verdict.Review, Clear: verdict.Clear})
	derived, err := deriveCorrectionPlan(plan.acceptance, *origin.Result.PhaseResult.Output.Plan,
		owner.AttemptRoot, origin.AttemptRoot, a.SourceCommit, p.PullTarget().BaseOID, a.Preparation,
		cycle.ID, cycle.FindingDigest, keys, plan.policy.Checks)
	if err != nil {
		if reason, blocked := correctionPlanWaitReason(err); blocked {
			report.Waits = append(report.Waits, DispatchWait{Repository: a.Repository, Issue: a.Issue, Reason: reason, Detail: "recorded investigation plan is unavailable"})
			return nil, "", false
		}
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "recorded plan is unstaged"})
		return nil, "", false
	}
	selection := plan.policy.Roles[a.Role]
	staged, err := stageExecutionManifest(derived, runID, plan.deadline, selection.Profile, project.ExecutionCorrect, 0)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Repository: a.Repository, Issue: a.Issue, Reason: DispatchErrStore, Detail: "recorded plan is unstaged"})
		return nil, "", false
	}
	return staged, project.ExecutionManifestDigest(staged), true
}
