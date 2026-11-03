package control

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// publicationTestDB exercises the production schema without fixture DDL.
func publicationTestDB(t *testing.T) (*store.Store, string) {
	t.Helper()
	db, dsn := postgresFixture(t)
	return db, dsn
}

type fakePublishHost struct {
	export    func(project.FactoryExport) (project.FactoryExportState, error)
	exports   []project.FactoryExport
	bundle    []byte
	exportErr error
}

func (f *fakePublishHost) FactoryLaunch(ctx context.Context, in project.FactoryLaunch) (project.FactoryState, error) {
	return project.FactoryState{}, errors.New("unexpected host launch")
}

func (f *fakePublishHost) FactoryStop(ctx context.Context, in project.FactoryStop) (project.FactoryState, error) {
	return project.FactoryState{}, errors.New("unexpected host stop")
}

func (f *fakePublishHost) FactoryInspect(ctx context.Context, in project.FactoryInspect) (project.FactoryState, error) {
	return project.FactoryState{}, host.ErrRunNotFound
}

func (f *fakePublishHost) FactoryTakeover(ctx context.Context, in project.FactoryTakeover) (project.TakeoverResult, error) {
	return project.TakeoverResult{}, errors.New("unexpected host takeover")
}

func (f *fakePublishHost) RecoverWork(ctx context.Context, r execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error) {
	return host.NewFactoryObserver(f, nil).RecoverWork(ctx, r)
}

func (f *fakePublishHost) FactoryHarness(ctx context.Context, _ string) (project.FactoryHarnessPin, error) {
	return project.FactoryHarnessPin{}, errors.New("unexpected host harness")
}

func (*fakePublishHost) PrepareWorkspace(context.Context, execution.StandingPreparationRequest) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("standing preparation unavailable in fixture")
}

func (*fakePublishHost) PrepareWorkspaceCandidate(context.Context, execution.CandidatePreparationRequest) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("candidate preparation unavailable in publication fixture")
}

func (*fakePublishHost) InspectWorkspacePreparation(context.Context, execution.PreparationAddress) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("preparation inspection unavailable in publication fixture")
}

func (*fakePublishHost) RetireWorkspacePreparation(context.Context, execution.PreparationAddress) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("preparation stop unavailable in publication fixture")
}

func (*fakePublishHost) ReadWorkspaceContext(_ context.Context, scoped execution.PreparationContextRequest) (execution.PreparationContext, error) {
	return execution.PreparationContext{}, errors.New("prepared repository context unavailable in publication fixture")
}

func (f *fakePublishHost) FactoryExport(ctx context.Context, in project.FactoryExport) (project.FactoryExportState, error) {
	f.exports = append(f.exports, in)
	if f.export != nil {
		return f.export(in)
	}
	if f.exportErr != nil {
		return project.FactoryExportState{}, f.exportErr
	}
	return project.FactoryExportState{
		ID: in.ID, Project: in.Project, Phase: project.FactoryCompleted,
		Container: strings.Repeat("c", 64), Candidate: in.Candidate,
		Bundle: base64.StdEncoding.EncodeToString(f.bundle),
	}, nil
}

type fakePublisher struct {
	ledger   map[string]factory.OperationOutcome
	observe  func(factory.PublicationWork) (factory.PublicationObservation, error)
	submit   func(factory.PublicationWork) (factory.OperationOutcome, error)
	push     func(factory.PublicationWork) (factory.OperationOutcome, error)
	submitPR func(factory.PublicationWork) (factory.OperationOutcome, error)
	lookup   func(string) (factory.OperationOutcome, error)
	cancel   func(string) (factory.OperationOutcome, error)
	adopt    func(factory.PublicationWork, factory.OperationOutcome) (factory.BranchOutcome, error)
	adoptPR  func(factory.PublicationWork, factory.OperationOutcome) (factory.PRCreationOutcome, error)

	observes []factory.PublicationWork
	submits  []factory.PublicationWork
	pushes   []factory.PublicationWork
	creates  []factory.PublicationWork
	lookups  []string
	cancels  []string
}

func (f *fakePublisher) ObservePublication(ctx context.Context, work factory.PublicationWork) (factory.PublicationObservation, error) {
	f.observes = append(f.observes, work)
	return f.observe(work)
}

func (f *fakePublisher) SubmitPublish(ctx context.Context, work factory.PublicationWork) (factory.OperationOutcome, error) {
	f.submits = append(f.submits, work)
	return f.record(work, factory.OpRefPublish, f.submit)
}

func (f *fakePublisher) PushBranch(ctx context.Context, work factory.PublicationWork) (factory.OperationOutcome, error) {
	f.pushes = append(f.pushes, work)
	return f.record(work, factory.OpRefPublish, f.push)
}

func (f *fakePublisher) SubmitPRCreate(ctx context.Context, work factory.PublicationWork) (factory.OperationOutcome, error) {
	f.creates = append(f.creates, work)
	return f.record(work, factory.OpPRCreate, f.submitPR)
}

func (f *fakePublisher) LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error) {
	f.lookups = append(f.lookups, operationID)
	if f.lookup != nil {
		return f.lookup(operationID)
	}
	outcome, ok := f.ledger[operationID]
	if !ok {
		return factory.OperationOutcome{NotObserved: true}, nil
	}
	return outcome, nil
}

func (f *fakePublisher) CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error) {
	f.cancels = append(f.cancels, operationID)
	if f.cancel != nil {
		return f.cancel(operationID)
	}
	outcome := f.ledger[operationID]
	if outcome.Effect == factory.OpEffectCommitted {
		outcome.Cancellation = factory.OpCancelTooLate
	} else {
		outcome.Effect, outcome.Cancellation, outcome.Reason = factory.OpEffectNotCommitted, factory.OpCancelCancelled, "cancelled_before_admission"
	}
	outcome.OperationID, outcome.InstallationID = operationID, "test-installation"
	f.ledger[operationID] = outcome
	return outcome, nil
}

func (f *fakePublisher) AdoptBranch(_ context.Context, work factory.PublicationWork, outcome factory.OperationOutcome) (factory.BranchOutcome, error) {
	return f.adopt(work, outcome)
}

func (f *fakePublisher) AdoptPRCreation(_ context.Context, work factory.PublicationWork, outcome factory.OperationOutcome) (factory.PRCreationOutcome, error) {
	return f.adoptPR(work, outcome)
}

func (f *fakePublisher) record(w factory.PublicationWork, kind string, call func(factory.PublicationWork) (factory.OperationOutcome, error)) (factory.OperationOutcome, error) {
	outcome, err := call(w)
	if err == nil && !outcome.NotObserved {
		outcome = publicationTestOutcome(w, kind, outcome)
		f.ledger[w.OperationID] = outcome
	}
	return outcome, err
}

func publicationTestOutcome(w factory.PublicationWork, kind string, outcome factory.OperationOutcome) factory.OperationOutcome {
	outcome.OperationID, outcome.InstallationID, outcome.Kind = w.OperationID, "test-installation", kind
	outcome.ActorID, outcome.RepositoryID = w.ActorID, w.Repository
	return outcome
}

type publishFixture struct {
	reads *fakeEvidenceSource
	db    *store.Store
	dsn   string
	seed  dispatchFixture
	host  *fakePublishHost
	exec  *fakePublisher
	coord *Coordinator
}

func publishSeed(t *testing.T, sourceInputs ...map[string]string) *publishFixture {
	t.Helper()
	db, dsn := publicationTestDB(t)
	fx := dispatchSeed(t, db, sourceInputs...)
	fh := &fakePublishHost{bundle: []byte("bundle-bytes")}
	coord := NewCoordinator(db, fh, &fakeDispatchBroker{})
	reads := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
	coord.AcceptanceReads = reads
	return &publishFixture{db: db, dsn: dsn, seed: fx, host: fh, coord: coord, reads: reads}
}

func (fx *publishFixture) wire(exec *fakePublisher) {
	fx.exec = exec
	fx.coord.Publication = exec
}

func (fx *publishFixture) finishReported(t *testing.T, issue int64) factory.Assignment {
	t.Helper()
	ctx := context.Background()
	decision := fx.seed.accept(t, issue, "d"+strings.Repeat("a", 23)+string(rune('0'+issue%10)))
	now := time.Now().Truncate(time.Second)
	prompt := []byte("prompt")
	assignmentID := factory.NewID()
	a := factory.Assignment{
		ID: assignmentID, AttemptRoot: assignmentID, PublicationAssignment: assignmentID,
		ProjectID: fx.seed.proj, ActorID: 7, Role: project.RoleCoder,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: issue, Acceptance: decision.ID},
		},
		Phase:      project.ExecutionInvestigate,
		Repository: fx.seed.repo, Issue: issue, Revision: 0, NativeRev: 41,
		Acceptance: decision.ID, Preparation: "f111111111111111111111111",
		Connection: "conn", SourceCommit: strings.Repeat("c", 40),
		Prompt: prompt, PromptSHA: dispatchDigest("prompt"),
		Run: factory.NewID(), RunHistory: []string{}, Stage: factory.AssignmentAssigned, Attempts: 1,
		CreatedUnix: now.Unix(),
	}
	authority, err := fx.coord.EffectiveAuthority(ctx, a.Repository)
	if err != nil || !authority.Effective {
		t.Fatalf("fixture authority: %+v %v", authority, err)
	}
	policy, err := fx.db.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	selection := policy.Roles[a.Role]
	a.Harness, a.HarnessVers, a.Model = selection.Harness+"-"+selection.HarnessVers, selection.HarnessVers, selection.Model
	a.Authority = authority.Authority
	a.Authority.RequirementsID, err = fx.db.RequirementHead(ctx, a.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	a.Authority.ApprovalID, err = fx.db.ApprovalHead(ctx, a.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	fx.reads.evidence[strconv.FormatInt(a.Repository, 10)+"/"+strconv.FormatInt(issue, 10)] = AcceptanceEvidence{
		Revision: 9,
		Issue:    AcceptanceIssueView{Index: decision.IssueIndex, TitleDigest: decision.TitleDigest, TitleOccurrence: decision.TitleOccurrence, LifecycleOccurrence: factory.OriginalOccurrence, ContentDigest: decision.ContentDigest, ContentVer: decision.ContentVersion, Visible: true},
		Comments: []AcceptanceComment{{ID: decision.Sources[0].ID, Digest: decision.Sources[0].Digest, ContentVer: decision.Sources[0].ContentVersion, Visible: true}},
	}
	a.RunHistory = []string{a.Run}
	r := factory.Reservation{AssignmentID: a.ID, Repository: fx.seed.repo, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: a.Run, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: strings.Repeat("c", 40),
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: a.Run, Repository: fx.seed.repo, Issue: issue, Attempt: a.ID}
	if err := fx.db.RecordDispatchPacket(ctx, dispatchControlForAssignment(t, fx.db, a), a, r, run, view); err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Reconciled = factory.Succeeded, true
	if err := fx.db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	// The reported investigation carries its executable plan: phased
	// follow-ups (implementation advance, correction retry manifests)
	// restage it, and completed planning never names a candidate.
	plan := phaseTestPlan(t, a.AttemptRoot, a.SourceCommit, a.Preparation)
	result := factory.AssignmentResult{
		AssignmentID: a.ID, RunID: a.Run, Phase: project.ExecutionInvestigate,
		Status: "completed", Summary: "done", Candidate: strings.Repeat("2", 40),
		Findings: []string{}, Reported: true, RecordedUnix: now.Unix(),
		PhaseResult: &factory.PhaseResult{
			Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate,
			Status: "completed", Summary: "done",
			Output: &factory.PhaseOutput{Plan: &plan},
		},
	}
	a.Stage, a.Outcome, a.Reason, a.Result, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, &result, now.Unix()
	if err := fx.db.FinishAssignment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := fx.db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	return a
}

// childRun admits a phased publication child through the same atomic dispatch
// packet used by production: reviewers bind the publication owner, correctors
// bind the current reviewer. Native execution remains the caller's explicit
// stub. The publication gains the child's review cycle before admission and
// is refreshed in place.
func (fx *publishFixture) childRun(t *testing.T, parent factory.Assignment, publication *factory.Publication, role, source, preparation string, settled bool) (factory.Assignment, factory.Run) {
	t.Helper()
	ctx := context.Background()
	if publication.AssignmentID != parent.PublicationAssignment || publication.Stage != factory.PublicationPublished || publication.Candidate != source {
		t.Fatalf("child fixture must use the current published candidate: publication=%+v source=%q", publication, source)
	}
	authority, err := fx.coord.EffectiveAuthority(ctx, parent.Repository)
	if err != nil || !authority.Effective {
		t.Fatalf("child fixture authority: %+v %v", authority, err)
	}
	policy, err := fx.db.RepositoryPolicy(ctx, parent.Repository)
	if err != nil {
		t.Fatal(err)
	}
	selection, ok := policy.Roles[role]
	if !ok {
		t.Fatalf("child fixture role %q has no policy selection", role)
	}
	phase := project.ExecutionCorrect
	if role == project.RoleReviewer {
		phase = project.ExecutionReview
	}
	runID := factory.NewID()
	a := factory.Assignment{
		Authority: authority.Authority,
		ID:        factory.NewID(), AttemptRoot: parent.AttemptRoot, PublicationAssignment: parent.PublicationAssignment,
		ParentAssignment: parent.ID,
		Subject:          parent.Subject,
		Phase:            phase,
		ProjectID:        parent.ProjectID, ActorID: parent.ActorID, Role: role, Repository: parent.Repository, Issue: parent.Issue,
		NativeRev: parent.NativeRev, Acceptance: parent.Acceptance, Preparation: preparation,
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: parent.Connection, SourceCommit: source,
		Prompt: []byte("child prompt"), PromptSHA: dispatchDigest("child prompt"),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: time.Now().Unix(),
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("child fixture invalid: %v", err)
	}
	stored, err := fx.db.PublicationByAssignment(ctx, parent.PublicationAssignment)
	if err != nil {
		t.Fatal(err)
	}
	round := len(stored.ReviewCycles) + 1
	cycle := factory.ReviewCycle{
		PublicationID: stored.ID, AttemptRoot: parent.AttemptRoot,
		ID:    factory.ReviewCycleID(stored.ID, parent.AttemptRoot, round, "review"),
		Phase: "review", TriggerDecision: "initial-review",
		TriggerReason: "fixture child admission",
		Head:          source, BaseOID: stored.PRCreate.BaseOID,
		FindingDigest:      strings.Repeat("e", 64),
		ReviewerAssignment: a.ID, CorrectionAssignment: a.ID, Round: round,
	}
	if role == project.RoleReviewer {
		cycle.CorrectionAssignment = ""
	} else {
		cycle.ReviewerAssignment = parent.ID
	}
	if err := cycle.Validate(); err != nil {
		t.Fatalf("child cycle invalid: %v", err)
	}
	a.ReviewContext = &factory.CurrentReviewContext{Publication: factory.CurrentPublicationContext{ID: stored.ID, AssignmentID: stored.AssignmentID, Revision: stored.Revision, PRID: stored.PRID, PRNumber: stored.PRNumber, Stage: stored.Stage}, AdmittedTuple: factory.CandidateTuple{Head: source, Base: stored.PRCreate.BaseOID, DiffBase: stored.CurrentDiffBase(), Revision: stored.NativeRev}, Cycle: cycle, CarriedFindings: []factory.Finding{}}
	stored.ReviewCycles = append(stored.ReviewCycles, cycle)
	stored.Revision++
	if err := fx.db.UpdatePublication(ctx, stored); err != nil {
		t.Fatalf("childRun publication update: %v", err)
	}
	*publication, err = fx.db.PublicationByAssignment(ctx, parent.PublicationAssignment)
	if err != nil {
		t.Fatal(err)
	}
	a.Authority.RequirementsID, err = fx.db.RequirementHead(ctx, a.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	a.Authority.ApprovalID, err = fx.db.ApprovalHead(ctx, a.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	r := factory.Run{
		ID: a.Run, ProjectID: a.ProjectID, Role: role, InputSHA: source,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	reservation := factory.Reservation{
		AssignmentID: a.ID, Repository: a.Repository, Connection: a.Connection,
		State: factory.ReservationHeld, PlannedMinutes: 30,
	}
	view := factory.RunView{RunID: r.ID, Repository: a.Repository, Issue: a.Issue, Attempt: a.ID}
	if err := fx.db.RecordDispatchPacket(ctx, dispatchControlForAssignment(t, fx.db, a), a, reservation, r, view); err != nil {
		t.Fatalf("childRun packet record: %v", err)
	}
	if settled {
		r.Outcome, r.Summary, r.Reconciled = factory.Succeeded, "child finished", true
		if err := fx.db.SaveFactoryRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	return a, r
}

func accountChildResult(t *testing.T, fx *publishFixture, run factory.Run, output string) factory.Assignment {
	t.Helper()
	ctx := context.Background()
	a, accounted := accountRunFinish(t, ctx, fx.db, run, output, time.Now())
	if !accounted || a.Result == nil || !a.Result.Reported {
		t.Fatalf("settled child result was not accounted: assignment=%+v accounted=%t", a, accounted)
	}
	usage, err := fx.db.RunUsage(ctx, run.ID)
	if err != nil || usage.RunID != a.Run || usage.Repository != a.Repository || usage.Connection != a.Connection || a.PublicationAssignment == a.ID {
		t.Fatalf("child usage was not recorded against its own run: assignment=%+v usage=%+v err=%v", a, usage, err)
	}
	return a
}

func pendingOutcome() factory.OperationOutcome {
	return factory.OperationOutcome{Effect: factory.OpEffectPending, Cancellation: factory.OpCancelNone, Completion: factory.OpCompletionPending}
}

func committedOutcome(receipt string) factory.OperationOutcome {
	return factory.OperationOutcome{
		Effect: factory.OpEffectCommitted, Cancellation: factory.OpCancelNone,
		Completion: factory.OpCompletionComplete, Receipt: []byte(receipt),
	}
}

func happyPublisher() *fakePublisher {
	comparison := strings.Repeat("1", 40)
	published := map[string]string{}
	exec := &fakePublisher{
		ledger: map[string]factory.OperationOutcome{},
		observe: func(w factory.PublicationWork) (factory.PublicationObservation, error) {
			return factory.PublicationObservation{
				TargetRef:  factory.PublishBranchName(w.AssignmentID),
				TargetTip:  published[w.AssignmentID],
				Comparison: comparison, NativeRev: 9, ObservedUnix: time.Now().Unix(),
			}, nil
		},
		submit: func(w factory.PublicationWork) (factory.OperationOutcome, error) { return pendingOutcome(), nil },
		push: func(w factory.PublicationWork) (factory.OperationOutcome, error) {
			published[w.AssignmentID] = w.Candidate
			return committedOutcome(`{"ref":"` + factory.PublishBranchName(w.AssignmentID) + `"}`), nil
		},
		submitPR: func(w factory.PublicationWork) (factory.OperationOutcome, error) {
			return committedOutcome(`{"pr_number":9}`), nil
		},
		adopt: func(w factory.PublicationWork, o factory.OperationOutcome) (factory.BranchOutcome, error) {
			return factory.BranchOutcome{Operation: o, Ref: factory.PublishBranchName(w.AssignmentID), NewOID: w.Candidate}, nil
		},
		adoptPR: func(w factory.PublicationWork, o factory.OperationOutcome) (factory.PRCreationOutcome, error) {
			return factory.PRCreationOutcome{
				Operation: o, HeadRef: factory.PublishBranchName(w.AssignmentID), BaseRef: w.TargetBranch,
				HeadOID: w.Candidate, BaseOID: w.ComparisonOID,
				PRNumber: 9, PRID: 8, IssueID: 10, AuthorID: 7,
			}, nil
		},
	}
	return exec
}

func (fx *publishFixture) publication(t *testing.T, a factory.Assignment) factory.Publication {
	t.Helper()
	p, err := fx.db.PublicationByAssignment(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (fx *publishFixture) pass(t *testing.T) PublishReport {
	t.Helper()
	report := fx.coord.PublishPass(context.Background())
	if len(report.Errors) != 0 {
		t.Fatalf("publication pass: %+v", report)
	}
	return report
}

func (f *fakePublishHost) InspectRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectRun(ctx, address)
}

func (f *fakePublishHost) InspectTool(ctx context.Context, family string) (execution.ToolObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectTool(ctx, family)
}

func (f *fakePublishHost) RetireRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).RetireRun(ctx, address)
}

func (f *fakePublishHost) LaunchRun(ctx context.Context, launch execution.LaunchRequest) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).LaunchRun(ctx, launch)
}

func (f *fakePublishHost) ExportCandidate(ctx context.Context, request execution.CandidateRequest) (execution.CandidateBundle, error) {
	return host.NewFactoryObserver(f, nil).ExportCandidate(ctx, request)
}
