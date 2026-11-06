package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// DispatchTimeOnlyRoot admits and launches one Issue-zero time-only
// fresh root — an intent decompose root or a completion reassess
// root — through the shared serving route. It is the owning boundary
// for both subjects: the issue-enumerating pass (dispatchOne,
// planAttemptForRole) admits issue subjects only, and intent planning
// prompts belong to its R12 consumer, so the prompt arrives
// caller-staged exactly like review and correction children keep
// theirs. Checks run in the same fixed order as the issue path —
// authority, project, policy, sponsorship, capacity, preparation,
// harness, dispatch gate — and the packet transaction stays
// authoritative for subject admission and funding: persistence and
// funding commit before the host call, which always runs outside the
// transaction. Refusals report through the shared packet-refusal
// mapping, so replay and concurrent admission surface as waits, never
// as a second run or charge.
//
// No factory_issue_controls row exists for issue zero, so the packet's
// NativeRev self-check travels on a zero control value carrying only
// the repository, the zero issue and the root's native revision.
// DispatchIntentPlan connects one reviewed repository-bound intent head
// to its decompose run through the shared admission and launch: it loads
// the owner's current head, admits the requested planning operation for
// the exact input, stages the bounded prompt and launches through
// DispatchTimeOnlyRoot, whose packet authoritatively owns funding,
// cancellation and replay. Unassociated drafts launch nothing, and a
// wrong owner, phase, grant or staged input refuses before any effect.
// Provider calls and native activation stay outside: the enrolled host
// account answers the staged prompt, and settlement persists the typed
// result through the existing phased path.
func (c *Coordinator) DispatchIntentPlan(ctx context.Context, intentID string, owner int64, operation string, in factory.SemanticPlannerInput, source, preparationID string) DispatchReport {
	var report DispatchReport
	head, err := c.Store.IntentRevision(ctx, intentID, owner)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "intent head unreadable"})
		return report
	}
	subject := factory.ExecutionSubject{
		Kind:   factory.SubjectIntent,
		Intent: &factory.IntentSubject{IntentID: intentID, RevisionID: head.ID, ScopeDigest: head.Digest(), OwnerID: owner},
	}
	if err := subject.Validate(); err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "intent subject invalid"})
		return report
	}
	// Every inspected source must answer the admitted source commit:
	// evidence for another commit cannot fund or launch this run.
	for _, ref := range in.Inspected {
		if ref.Commit != source {
			report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "inspected source does not match its admitted source"})
			return report
		}
	}
	// The packet authoritatively funds or refuses; the pre-check only
	// binds the operation to the exact head and input.
	if err := AdmitIntentBoundLaunch(subject, project.ExecutionDecompose, project.RoleCoder, operation, head, true, in); err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: err.Error()})
		return report
	}
	prompt, err := StageIntentPlanningPrompt(head, in)
	if err != nil {
		reason := DispatchErrStore
		if errors.Is(err, ErrIntentPlanningPromptBound) {
			reason = DispatchErrPromptBound
		}
		report.Errors = append(report.Errors, DispatchError{Reason: reason, Detail: err.Error()})
		return report
	}
	occupancy, err := snapshotOccupancy(ctx, c.Store)
	if err != nil {
		report.Errors = append(report.Errors, DispatchError{Reason: DispatchErrStore, Detail: "occupancy unreadable"})
		return report
	}
	dispatchTimeOnlyRoot(ctx, c.dispatchDeps(), &occupancy, head.Repository, subject, source, prompt, preparationID, &in, &report)
	return report
}

// Native revisions name the sealed native bracket for reassess roots
// and the lowest valid value for intent roots, which have no native
// observation: the sealed record and the head revision are their
// authoritative input bindings. preparationID pins the dispatch to the
// preparation the caller already read: when set, the run funds only if
// that exact preparation is still the ready match, so a changed
// preparation waits instead of funding stale input. Empty keeps the
// standing match for callers without one.
func DispatchTimeOnlyRoot(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, repository int64, subject factory.ExecutionSubject, source string, prompt []byte, preparationID string, report *DispatchReport) {
	dispatchTimeOnlyRoot(ctx, deps, occupancy, repository, subject, source, prompt, preparationID, nil, report)
}

func dispatchTimeOnlyRoot(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, repository int64, subject factory.ExecutionSubject, source string, prompt []byte, preparationID string, planning *factory.SemanticPlannerInput, report *DispatchReport) {
	dispatchTimeOnlyRootAttempt(ctx, deps, occupancy, repository, subject, source, prompt, preparationID, planning, nil, report)
}

func dispatchTimeOnlyRetry(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, repository int64, subject factory.ExecutionSubject, source string, prompt []byte, preparationID string, retry *factory.RetryDecision, report *DispatchReport) {
	dispatchTimeOnlyRootAttempt(ctx, deps, occupancy, repository, subject, source, prompt, preparationID, nil, retry, report)
}

func dispatchTimeOnlyRootAttempt(ctx context.Context, deps DispatchDeps, occupancy *passOccupancy, repository int64, subject factory.ExecutionSubject, source string, prompt []byte, preparationID string, planning *factory.SemanticPlannerInput, retry *factory.RetryDecision, report *DispatchReport) {
	deps = admitDispatchDependencies(deps)
	// Truncated to the second like every packet caller: the funding
	// check compares the deadline against a Unix-second horizon, so a
	// sub-second skew on exact-horizon funding would refuse.
	t := &timeOnlyLaunch{
		ctx: ctx, deps: deps, occupancy: occupancy, repository: repository,
		subject: subject, source: source, prompt: prompt, preparationID: preparationID, planning: planning, retry: retry,
		report: report, now: time.Now().UTC().Truncate(time.Second),
	}
	t.drive()
}

// timeOnlyLaunch carries the shared state behind one time-only root
// dispatch so each duty reads through one small check.
type timeOnlyLaunch struct {
	ctx           context.Context
	deps          DispatchDeps
	occupancy     *passOccupancy
	repository    int64
	subject       factory.ExecutionSubject
	source        string
	prompt        []byte
	preparationID string
	planning      *factory.SemanticPlannerInput
	retry         *factory.RetryDecision
	report        *DispatchReport
	now           time.Time
	phase         string
	role          string
	plan          *attemptPlan
	projectID     string
	selection     factory.RoleSelection
}

func (t *timeOnlyLaunch) drive() {
	phase, ok := t.admitSubject()
	if !ok {
		return
	}
	t.phase = phase
	t.plan = &attemptPlan{subject: t.subject, phase: phase, freshAttempt: true}
	if !t.loadGrants() {
		return
	}
	if !t.selectSponsorship() {
		return
	}
	if !t.checkLimits() {
		return
	}
	if !t.selectPreparation() {
		return
	}
	if !t.checkHarness() {
		return
	}
	t.submitPacket()
}

// admitSubject binds one time-only subject to its execution phase
// with its prompt and source commit.
func (t *timeOnlyLaunch) admitSubject() (string, bool) {
	phase := ""
	t.role = project.RoleCoder
	switch t.subject.Kind {
	case factory.SubjectIntent:
		phase = project.ExecutionDecompose
		if t.subject.Intent == nil {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "intent root lacks its intent subject"})
			return "", false
		}
	case factory.SubjectCompletion:
		phase = project.ExecutionReassess
		if t.subject.Completion == nil {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "reassessment root lacks its completion subject"})
			return "", false
		}
	case factory.SubjectDelivery:
		phase, t.role = project.ExecutionVerify, project.RoleReviewer
		if t.subject.Delivery == nil || t.subject.Delivery.Validate() != nil || t.subject.Delivery.SourceCommit != t.source {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "verification requires its exact delivery source"})
			return "", false
		}
	case factory.SubjectStall:
		phase = project.ExecutionRepair
		if t.subject.Stall == nil || t.subject.Stall.Validate() != nil {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "repair root lacks its exact stall cycle"})
			return "", false
		}
	case factory.SubjectDeliveryRepair:
		phase = project.ExecutionRepair
		if t.subject.DeliveryRepair == nil || t.subject.DeliveryRepair.Validate() != nil {
			return "", false
		}
	default:
		t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "time-only dispatch needs its intent or completion subject"})
		return "", false
	}
	if !factory.ValidPhaseRole(phase, t.role) || !factory.SubjectKindAllowed(t.subject.Kind, phase) {
		t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "time-only root does not match its execution phase"})
		return "", false
	}
	if len(t.prompt) == 0 || len(t.prompt) > project.MaxPromptForPhase(phase) {
		t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitPrompt, Detail: "time-only prompt is empty or over its bound"})
		return "", false
	}
	if !factory.ValidCommit(t.source) {
		t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "time-only root lacks its source commit"})
		return "", false
	}
	return phase, true
}

// loadGrants loads authority, project, policy and the coder role
// selection behind one time-only root.
func (t *timeOnlyLaunch) loadGrants() bool {
	effective, err := t.deps.Authority(t.ctx, t.repository)
	if err != nil || !effective.Effective {
		t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitAuthority, Detail: "authority ineffective for time-only dispatch"})
		return false
	}
	if !effective.DispatchOpen {
		t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitDispatchClosed, Detail: "dispatch closed for time-only dispatch"})
		return false
	}
	t.plan.effective = effective
	workspace, native, err := t.deps.Store.ExecutionWorkspace(t.ctx, collaboration.RepositoryID(t.repository))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitAuthority, Detail: "project changed during time-only dispatch"})
		} else {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "project unreadable"})
		}
		return false
	}
	t.projectID = workspace
	t.plan.projectID, t.plan.nativeEnvironment = workspace, native
	policy, err := t.deps.Store.RepositoryPolicy(t.ctx, t.repository)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitAuthority, Detail: "policy changed during time-only dispatch"})
		} else {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "policy unreadable"})
		}
		return false
	}
	t.plan.policy = policy
	selection, ok := policy.Roles[t.role]
	if !ok {
		t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitSponsorshipRole, Detail: "policy has no coder role selection"})
		return false
	}
	t.selection = selection
	return true
}

// selectSponsorship selects the sponsorship behind one time-only root.
func (t *timeOnlyLaunch) selectSponsorship() bool {
	sponsorship, wait, failure := selectSponsorship(t.ctx, t.deps, t.repository, t.plan.effective, t.role)
	if failure != nil {
		t.report.Errors = append(t.report.Errors, timeOnlyFailure(t.repository, failure))
		return false
	}
	if wait != nil {
		t.report.Waits = append(t.report.Waits, timeOnlyWait(t.repository, wait))
		return false
	}
	if sponsorship.ProjectID != t.projectID {
		t.report.Waits = append(t.report.Waits, timeOnlyWait(t.repository, waitFor(WaitAuthority, "sponsorship changed during time-only dispatch")))
		return false
	}
	if t.subject.Kind == factory.SubjectStall || t.subject.Kind == factory.SubjectDeliveryRepair {
		grant, err := t.deps.Store.RepairGrantCommand(t.ctx, t.repository)
		if err != nil || !grant.Active || grant.Sponsor != sponsorship.ActorID {
			t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitSponsorship, Detail: "repair needs its explicit sponsor's admitted account"})
			return false
		}
	}
	t.plan.sponsorship = sponsorship
	return true
}

// checkLimits checks attempt limits behind one time-only root.
func (t *timeOnlyLaunch) checkLimits() bool {
	if first, second := checkLimits(t.ctx, t.deps, t.occupancy, t.plan); second != nil {
		t.report.Errors = append(t.report.Errors, timeOnlyFailure(t.repository, second))
		return false
	} else if first != nil {
		t.report.Waits = append(t.report.Waits, timeOnlyWait(t.repository, first))
		return false
	}
	return true
}

// selectPreparation selects the preparation behind one time-only root.
func (t *timeOnlyLaunch) selectPreparation() bool {
	if first, second := selectPreparationFor(t.ctx, t.deps, t.plan, t.role, t.preparationID); second != nil {
		t.report.Errors = append(t.report.Errors, timeOnlyFailure(t.repository, second))
		return false
	} else if first != nil {
		t.report.Waits = append(t.report.Waits, timeOnlyWait(t.repository, first))
		return false
	}
	return true
}

// checkHarness checks the harness behind one time-only root.
func (t *timeOnlyLaunch) checkHarness() bool {
	if wait := checkHarnessFor(t.ctx, t.deps, t.plan, t.role); wait != nil {
		t.report.Waits = append(t.report.Waits, timeOnlyWait(t.repository, wait))
		return false
	}
	return true
}

// submitPacket gates, scopes, records and launches one time-only root.
func (t *timeOnlyLaunch) submitPacket() {
	open, gateRev, _, err := t.deps.Store.DispatchState(t.ctx, t.repository)
	if err != nil {
		t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "dispatch state unreadable"})
		return
	}
	if !open {
		t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitDispatchClosed, Detail: "dispatch closed for time-only dispatch"})
		return
	}
	t.plan.gateRev = gateRev
	nativeRev, minutes, wait, failure := timeOnlyScope(t.ctx, t.deps, t.repository, t.subject)
	if failure != nil {
		t.report.Errors = append(t.report.Errors, timeOnlyFailure(t.repository, failure))
		return
	}
	if wait != nil {
		t.report.Waits = append(t.report.Waits, timeOnlyWait(t.repository, wait))
		return
	}
	deadline := t.plan.deadline
	t.plan.planned = min(t.plan.planned, minutes)
	if horizon := t.now.Add(time.Duration(minutes) * time.Minute); horizon.Before(deadline) {
		deadline = horizon
	}
	assignment, run, ok := t.buildAssignment(nativeRev, deadline)
	if !ok {
		return
	}
	reservation := factory.Reservation{AssignmentID: assignment.ID, Repository: t.repository, Connection: t.plan.sponsorship.Connection, State: factory.ReservationHeld, PlannedMinutes: t.plan.planned}
	view := factory.RunView{RunID: run.ID, Repository: t.repository, Issue: 0, Attempt: assignment.ID}
	t.plan.control = factory.IssueControl{Repository: t.repository, Issue: 0, NativeRev: nativeRev}
	t.plan.prompt, t.plan.promptSHA = t.prompt, assignment.PromptSHA
	t.plan.preparationOwner = t.plan.prep.Preparation.ID
	if err := t.plan.recordPacket(t.ctx, t.deps.Store, assignment, reservation, run, view); err != nil {
		reportPacketRefusal(t.ctx, t.deps, t.occupancy, t.repository, 0, err, t.report)
		return
	}
	t.occupancy.hold(reservation)
	launch, failure := stageTimeOnlyLaunch(t.plan, t.selection.Model, assignment, run, deadline)
	if failure != nil {
		t.report.Errors = append(t.report.Errors, timeOnlyFailure(t.repository, failure))
		return
	}
	launchTail(t.ctx, t.deps, assignment, run, launch, t.report)
}

// buildAssignment builds the assignment and run behind one
// time-only root.
func (t *timeOnlyLaunch) buildAssignment(nativeRev int64, deadline time.Time) (factory.Assignment, factory.Run, bool) {
	var none factory.Assignment
	var noRun factory.Run
	promptSum := sha256.Sum256(t.prompt)
	assignmentID, runID := factory.NewID(), factory.NewID()
	if t.retry != nil {
		if t.subject.Kind != factory.SubjectDelivery || t.retry.Validate() != nil || t.subject.Delivery == nil || t.subject.Delivery.DeliveryID != factory.DeliveryRetryID(*t.subject.Delivery, t.retry.CommandID) {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "verification Retry does not bind its command"})
			return none, noRun, false
		}
		assignmentID = t.retry.CommandID
	}
	if t.subject.Stall != nil {
		record, err := t.deps.Store.StallRepairRecord(t.ctx, t.repository, t.subject.Stall.StallID)
		if err != nil {
			t.report.Errors = append(t.report.Errors, DispatchError{Repository: t.repository, Reason: DispatchErrStore, Detail: "repair cycle unavailable"})
			return none, noRun, false
		}
		for _, cycle := range record.Repair.Cycles {
			if cycle.ID == t.subject.Stall.CycleID {
				assignmentID, runID = cycle.ID, factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, cycle.Number)
			}
		}
	}
	if t.subject.DeliveryRepair != nil {
		assignmentID = t.subject.DeliveryRepair.CycleID
		runID = factory.RepairChargeID(factory.DeliveryRepairCauseID(t.subject.DeliveryRepair.FailedVerificationRunID), factory.RepairChargeRun, 1)
	}
	assignment := factory.Assignment{
		PlanningContext: t.planning,
		Authority:       t.plan.effective.Authority,
		ID:              assignmentID, AttemptRoot: assignmentID, PublicationAssignment: "", ParentAssignment: "",
		Subject: t.subject, Phase: t.phase,
		ProjectID: t.projectID, ActorID: t.plan.sponsorship.ActorID, Role: t.role,
		Repository: t.repository, Issue: 0, NativeRev: nativeRev, Acceptance: "", Preparation: t.plan.prep.Preparation.ID,
		Harness: t.plan.tool.Harness + "-" + t.plan.tool.Version, HarnessVers: t.plan.tool.Version, AdoptedHarness: t.plan.tool.appliancePin(), Model: t.selection.Model,
		Connection: t.plan.sponsorship.Connection, SourceCommit: t.source,
		Prompt: t.prompt, PromptSHA: hex.EncodeToString(promptSum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: t.now.Unix(),
	}
	assignment.Authority.RequirementsID, assignment.Authority.ApprovalID = t.plan.requirement, t.plan.approval
	// The run records the current staged host identity exactly like the
	// issue path: the pin's image and family-version, never the saved
	// policy version or an image derived from the executable digest.
	// The model stays the admitted policy selection.
	run := factory.Run{
		ID: runID, ProjectID: t.projectID, Role: t.role, InputSHA: t.source,
		Started: t.now, Deadline: deadline,
		Image: t.plan.tool.Image, Harness: t.plan.tool.Harness + "-" + t.plan.tool.Version, Model: t.selection.Model,
	}
	t.plan.captureRuntime(&assignment, &run)
	if t.deps.VerificationManifest != nil {
		manifest, err := t.deps.VerificationManifest(t.ctx, assignment, run)
		if err != nil {
			t.report.Waits = append(t.report.Waits, DispatchWait{Repository: t.repository, Reason: WaitPreparation, Detail: err.Error()})
			return none, noRun, false
		}
		run.VerificationManifest = manifest
	}
	return assignment, run, true
}

// timeOnlyWait and timeOnlyFailure carry shared planning checks into
// the serving report for issue-zero roots, which report under the zero
// issue they dispatch.
func timeOnlyWait(repository int64, wait *planWait) DispatchWait {
	return DispatchWait{Repository: repository, Issue: 0, Reason: wait.Reason, Detail: wait.Detail}
}

func timeOnlyFailure(repository int64, failure *planWait) DispatchError {
	return DispatchError{Repository: repository, Issue: 0, Reason: failure.Reason, Detail: failure.Detail}
}

// timeOnlyScope resolves the root's native revision and funded minutes
// from recorded state: the sealed evidence bracket and the
// policy-admitted reassessment horizon for completion roots, the lowest
// valid revision and the policy-admitted intent horizon for intent
// roots. An unfunded intent posture waits for explicit funding; it
// never launches on omission.
func timeOnlyScope(ctx context.Context, deps DispatchDeps, repository int64, subject factory.ExecutionSubject) (int64, int, *planWait, *planWait) {
	fail := func(detail string) (int64, int, *planWait, *planWait) {
		return 0, 0, nil, waitFor(DispatchErrStore, detail)
	}
	if subject.Kind == factory.SubjectIntent {
		policy, err := deps.Store.RepositoryPolicy(ctx, repository)
		if err != nil {
			return fail("policy unreadable")
		}
		if policy.IntentMinutes < 1 {
			return 0, 0, waitFor(WaitAllowance, "intent root is unfunded; explicit funding required"), nil
		}
		return 1, policy.IntentMinutes, nil, nil
	}
	if subject.Kind == factory.SubjectDelivery {
		policy, err := deps.Store.RepositoryPolicy(ctx, repository)
		if err != nil {
			return fail("policy unreadable")
		}
		remaining, err := deps.Store.RemainingAppVerificationSeconds(ctx, repository, subject.Delivery.IntentID, time.Now())
		if err != nil {
			return fail("application verification allowance unreadable")
		}
		minutes := min(policy.AttemptLimits.ActiveMinutes, factory.MaxAppVerificationRunMinutes, int(remaining/60))
		if minutes < 1 {
			return 0, 0, waitFor(WaitAllowance, "application verification intent allowance is exhausted (2 runs / 60 active minutes)"), nil
		}
		return 1, minutes, nil, nil
	}
	if subject.Kind == factory.SubjectStall && subject.Stall != nil {
		record, err := deps.Store.StallRepairRecord(ctx, repository, subject.Stall.StallID)
		if err != nil {
			return fail("repair cycle unreadable")
		}
		charges, err := deps.Store.RepairCharges(ctx, repository)
		if err != nil {
			return fail("repair charges unreadable")
		}
		for _, cycle := range record.Repair.Cycles {
			if cycle.ID != subject.Stall.CycleID {
				continue
			}
			if cycle.State != factory.RepairReady || cycle.ValidateFor(record.StallEvent) != nil || cycle.InventoryDigest != subject.Stall.InventoryDigest {
				return fail("repair cycle input changed")
			}
			for _, charge := range charges {
				if charge.ID == factory.RepairChargeID(record.EpisodeID, factory.RepairChargeRun, cycle.Number) && charge.ActiveMinutes > 0 {
					return cycle.NativeRevision, charge.ActiveMinutes, nil, nil
				}
			}
		}
		return fail("repair cycle has no admitted investigator charge")
	}
	if subject.Kind == factory.SubjectDeliveryRepair && subject.DeliveryRepair != nil {
		record, err := deps.Store.DeliveryRepairRecord(ctx, repository, subject.DeliveryRepair.FailedVerificationRunID)
		if err != nil || record.Subject != *subject.DeliveryRepair || record.Cycle.State != factory.RepairReady {
			return fail("delivery repair cycle changed or unavailable")
		}
		charges, err := deps.Store.RepairCharges(ctx, repository)
		if err != nil {
			return fail("delivery repair funding unavailable")
		}
		for _, charge := range charges {
			if charge.ID == factory.RepairChargeID(factory.DeliveryRepairCauseID(record.Cause.FailedVerificationRunID), factory.RepairChargeRun, 1) && charge.ActiveMinutes > 0 {
				return record.Cycle.NativeRevision, charge.ActiveMinutes, nil, nil
			}
		}
		return fail("delivery repair has no admitted investigator charge")
	}
	record, err := deps.Store.ReassessmentRecord(ctx, subject.Completion.CycleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fail("reassessment answers an unstaged cycle")
		}
		return fail("reassessment cycle unreadable")
	}
	if err := record.Validate(); err != nil {
		return fail("reassessment cycle invalid: " + err.Error())
	}
	if record.Cycle.State != factory.ReassessmentDeciding || record.Cycle.Decision != factory.ReassessmentOptionReassess || len(record.Cycle.Ledger.CoderRuns) != 0 {
		return fail("reassessment answers a cycle without its reassess decision")
	}
	if !record.Local.Sealed() || record.InventoryDigest != subject.Completion.InventoryDigest {
		return fail("reassessment answers an unsealed cycle")
	}
	policy, err := deps.Store.RepositoryPolicy(ctx, repository)
	if err != nil {
		return fail("policy unreadable")
	}
	return record.EvidenceRevision, factory.EffectiveReassessmentMinutes(policy.ReassessmentMinutes), nil, nil
}

// stageTimeOnlyLaunch stages the host launch for one recorded root:
// the intent-bound joint for decompose roots, the phase-tagged generic
// launch for reassess roots. Both validate before the host call.
func stageTimeOnlyLaunch(plan *attemptPlan, model string, assignment factory.Assignment, run factory.Run, deadline time.Time) (project.FactoryLaunch, *planWait) {
	if assignment.Subject.Kind == factory.SubjectIntent {
		staged, err := StageIntentBoundLaunch(assignment.Subject, IntentBoundLaunchInput{
			Prompt: promptOf(plan, assignment), Deadline: deadline, RunID: run.ID,
			ProjectID: assignment.ProjectID, PreparationID: assignment.Preparation,
			Harness: plan.tool.Harness, HarnessVers: plan.tool.Version, Model: model,
			PromptSHA: assignment.PromptSHA, SourceCommit: assignment.SourceCommit,
			Connection: assignment.Connection, HarnessSHA256: plan.tool.SHA256, ActorID: assignment.ActorID,
		})
		if err != nil {
			return project.FactoryLaunch{}, waitFor(DispatchErrStore, "intent launch unstaged: "+err.Error())
		}
		return staged, nil
	}
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: run.ID, Project: assignment.ProjectID, Role: assignment.Role,
			Preparation: assignment.Preparation,
			Harness:     plan.tool.Harness, HarnessVers: plan.tool.Version,
			Model:      model,
			Assignment: assignment.PromptSHA, SourceCommit: assignment.SourceCommit,
			Connection: assignment.Connection, Actor: assignment.ActorID,
			Deadline: deadline, ExecutionPhase: assignment.Phase,
		},
		Prompt: promptOf(plan, assignment), HarnessSHA256: plan.tool.SHA256,
		VerificationManifest: run.VerificationManifest,
	}
	if len(run.VerificationManifest) > 0 {
		launch.Run.VerificationManifestDigest = project.ExecutionManifestDigest(run.VerificationManifest)
	}
	if err := launch.Validate(); err != nil {
		return project.FactoryLaunch{}, waitFor(DispatchErrStore, "reassessment launch invalid: "+err.Error())
	}
	return launch, nil
}

func promptOf(plan *attemptPlan, assignment factory.Assignment) []byte {
	if len(plan.prompt) != 0 {
		return plan.prompt
	}
	return assignment.Prompt
}
