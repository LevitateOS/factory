package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

type countingAcceptanceSource struct {
	inner   AcceptanceSource
	byIssue map[string]int
}

func (s *countingAcceptanceSource) ReadAcceptanceEvidence(ctx context.Context, repository, issue string, commentIDs []string) (AcceptanceEvidence, error) {
	if s.byIssue == nil {
		s.byIssue = map[string]int{}
	}
	s.byIssue[repository+"/"+issue]++
	return s.inner.ReadAcceptanceEvidence(ctx, repository, issue, commentIDs)
}

func TestRecoveryConsumesSettledRunWithoutHook(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	first := DispatchPass(ctx, fx.deps())
	if len(first.Launched) != 1 {
		t.Fatalf("first pass = %+v", first.Launched)
	}
	// The run settles without the accounting hook, as if the process died
	// between the run save and the hook.
	run, err := db.FactoryRun(ctx, first.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Failed, "boom", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	second := DispatchPass(ctx, fx.deps())
	if len(second.Recovered) != 1 || second.Recovered[0] != first.Launched[0].AssignmentID {
		t.Fatalf("recovered = %+v errors = %+v", second.Recovered, second.Errors)
	}
	a, err := db.Assignment(ctx, first.Launched[0].AssignmentID)
	if err != nil || a.Stage != factory.AssignmentFinished || a.Outcome != factory.Failed {
		t.Fatalf("assignment = %+v %v", a, err)
	}
	if r, _ := db.Reservation(ctx, a.ID); r.State != factory.ReservationConsumed {
		t.Fatalf("reservation = %+v", r)
	}
	if total, _ := db.UsageTotal(ctx, 7, "conn"); total < 1 {
		t.Fatalf("usage = %d", total)
	}
}

func TestReleasedReviewerChildRetryKeepsRecordedPlan(t *testing.T) {
	ctx := context.Background()
	fx, parent, _, run := reviewRunSeed(t, false)
	child, err := fx.db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var launches []project.FactoryLaunch
	host := &fakeDispatchHost{
		launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
			launches = append(launches, in)
			return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
		},
		harness: func(family string) (project.FactoryHarnessPin, error) {
			return project.FactoryHarnessPin{Harness: family, Version: fx.seed.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
		},
	}
	deps := fx.seed.deps()
	deps.Host = host
	deps.Broker = &fakeDispatchBroker{}
	reservation, err := fx.db.Reservation(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unused, fenced := confirmUnused(ctx, deps, child, &run); !unused || fenced {
		t.Fatalf("child run was not confirmed unused: unused=%t fenced=%t", unused, fenced)
	}
	report := DispatchReport{}
	recoverUnused(ctx, deps, child, reservation, run, &report)
	if len(launches) != 1 || launches[0].Run.Role != project.RoleReviewer ||
		launches[0].Run.Actor != child.ActorID ||
		launches[0].Run.Preparation != child.Preparation || launches[0].Run.HarnessVers != child.HarnessVers ||
		launches[0].Run.Model != child.Model || launches[0].Run.SourceCommit != child.SourceCommit || launches[0].Run.Connection != child.Connection ||
		!bytes.Equal(launches[0].Prompt, child.Prompt) || launches[0].Run.Assignment != child.PromptSHA {
		t.Fatalf("review retry changed its recorded plan: launches=%+v errors=%+v", launches, report.Errors)
	}
	after, err := fx.db.Assignment(ctx, child.ID)
	if err != nil || after.Attempts != 2 || after.AttemptRoot != parent.AttemptRoot ||
		after.PublicationAssignment != parent.ID || after.Role != project.RoleReviewer ||
		!bytes.Equal(after.Prompt, child.Prompt) || after.PromptSHA != child.PromptSHA {
		t.Fatalf("review retry changed child identity: %+v err=%v", after, err)
	}
}

func TestReleasedCorrectionChildRetryKeepsRecordedPlan(t *testing.T) {
	ctx := context.Background()
	fx, publication := checkSeed(t, 16)
	parent, err := fx.db.Assignment(ctx, publication.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	_, run := correctionChildFixture(t, fx, parent, &publication, false)
	child, err := fx.db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var launches []project.FactoryLaunch
	host := &fakeDispatchHost{
		launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
			launches = append(launches, in)
			return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
		},
		harness: func(family string) (project.FactoryHarnessPin, error) {
			return project.FactoryHarnessPin{Harness: family, Version: fx.seed.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
		},
	}
	deps := fx.seed.deps()
	deps.Host = host
	deps.Broker = &fakeDispatchBroker{}
	reservation, err := fx.db.Reservation(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unused, fenced := confirmUnused(ctx, deps, child, &run); !unused || fenced {
		t.Fatalf("correction child run was not confirmed unused: unused=%t fenced=%t", unused, fenced)
	}
	report := DispatchReport{}
	recoverUnused(ctx, deps, child, reservation, run, &report)
	if len(launches) != 1 || launches[0].Run.Role != project.RoleCoder ||
		launches[0].Run.Actor != child.ActorID ||
		launches[0].Run.Preparation != child.Preparation || launches[0].Run.Model != child.Model ||
		launches[0].Run.SourceCommit != child.SourceCommit || launches[0].Run.Connection != child.Connection ||
		!bytes.Equal(launches[0].Prompt, child.Prompt) || launches[0].Run.Assignment != child.PromptSHA {
		t.Fatalf("correction retry changed its recorded plan: launches=%+v waits=%+v errors=%+v", launches, report.Waits, report.Errors)
	}
	after, err := fx.db.Assignment(ctx, child.ID)
	if err != nil || after.Attempts != 2 || after.AttemptRoot != parent.AttemptRoot ||
		after.PublicationAssignment != parent.ID || after.Role != project.RoleCoder || !bytes.Equal(after.Prompt, child.Prompt) {
		t.Fatalf("correction retry changed child identity: %+v err=%v", after, err)
	}
}

func TestReleasedReviewerRetryAcceptsCurrentAuthorityAndNativeRevision(t *testing.T) {
	ctx := context.Background()
	fx, _, _, run := reviewRunSeed(t, false)
	child, err := fx.db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := fx.db.Capacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	capacity.MaxConcurrentRuns++
	if err := fx.db.SaveCapacity(ctx, capacity); err != nil {
		t.Fatal(err)
	}
	control, err := fx.db.IssueControl(ctx, child.Repository, child.Issue)
	if err != nil {
		t.Fatal(err)
	}
	control.NativeRev++
	control.Fingerprint = strings.Repeat("4", 64)
	if _, _, err := fx.db.RecordIssueAssessment(ctx, control, time.Now()); err != nil {
		t.Fatal(err)
	}
	inputKey := fmt.Sprintf("%d/%d", child.Repository, child.Issue)
	inputs := fx.seed.reads.inputs[inputKey]
	inputs.Revision = control.NativeRev
	fx.seed.reads.inputs[inputKey] = inputs
	var launches []project.FactoryLaunch
	host := &fakeDispatchHost{
		launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
			launches = append(launches, in)
			return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
		},
		harness: func(family string) (project.FactoryHarnessPin, error) {
			return project.FactoryHarnessPin{Harness: family, Version: fx.seed.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
		},
	}
	deps := fx.seed.deps()
	deps.Host = host
	deps.Broker = &fakeDispatchBroker{}
	reservation, err := fx.db.Reservation(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unused, fenced := confirmUnused(ctx, deps, child, &run); !unused || fenced {
		t.Fatalf("reviewer run was not confirmed unused: unused=%t fenced=%t", unused, fenced)
	}
	report := DispatchReport{}
	recoverUnused(ctx, deps, child, reservation, run, &report)
	if len(launches) != 1 || launches[0].Run.Role != project.RoleReviewer || !bytes.Equal(launches[0].Prompt, child.Prompt) {
		t.Fatalf("retry did not retain the current-authorized child plan: launches=%+v errors=%+v", launches, report.Errors)
	}
	after, err := fx.db.Assignment(ctx, child.ID)
	if err != nil || after.Attempts != child.Attempts+1 || after.Authority != child.Authority ||
		after.AttemptRoot != child.AttemptRoot || after.PublicationAssignment != child.PublicationAssignment ||
		after.SourceCommit != child.SourceCommit || after.PromptSHA != child.PromptSHA || !bytes.Equal(after.Prompt, child.Prompt) ||
		len(after.RunHistory) != len(child.RunHistory)+1 || !slices.Equal(after.RunHistory[:len(child.RunHistory)], child.RunHistory) {
		t.Fatalf("retry changed the child assignment plan: before=%+v after=%+v err=%v", child, after, err)
	}
	retryRun, err := fx.db.FactoryRun(ctx, after.Run)
	if err != nil || retryRun.Admission == nil {
		t.Fatalf("retry run admission is missing: %+v err=%v", retryRun.Admission, err)
	}
	currentCapacity, err := fx.db.Capacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if retryRun.Admission.Authority.Capacity != currentCapacity.Revision {
		t.Fatalf("retry run did not capture the current capacity revision: admission=%+v capacity=%d", retryRun.Admission.Authority, currentCapacity.Revision)
	}
}

func TestReleasedReviewerRetryRefusesLostSponsorship(t *testing.T) {
	for name, change := range map[string]func(*factory.Sponsorship){
		"withdrawn":             func(s *factory.Sponsorship) { s.Active = false },
		"reviewer role removed": func(s *factory.Sponsorship) { s.Roles = []string{project.RoleCoder} },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fx, _, _, run := reviewRunSeed(t, false)
			child, err := fx.db.AssignmentByRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			sponsorship, err := fx.db.Sponsorship(ctx, child.Repository, child.Connection)
			if err != nil {
				t.Fatal(err)
			}
			change(&sponsorship)
			if err := fx.db.SaveSponsorship(ctx, sponsorship); err != nil {
				t.Fatal(err)
			}
			var launches []project.FactoryLaunch
			host := &fakeDispatchHost{
				launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
					launches = append(launches, in)
					return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
				},
				harness: func(family string) (project.FactoryHarnessPin, error) {
					return project.FactoryHarnessPin{Harness: family, Version: fx.seed.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
				},
			}
			deps := fx.seed.deps()
			deps.Host = host
			deps.Broker = &fakeDispatchBroker{}
			reservation, err := fx.db.Reservation(ctx, child.ID)
			if err != nil {
				t.Fatal(err)
			}
			if unused, fenced := confirmUnused(ctx, deps, child, &run); !unused || fenced {
				t.Fatalf("reviewer run was not confirmed unused: unused=%t fenced=%t", unused, fenced)
			}
			report := DispatchReport{}
			recoverUnused(ctx, deps, child, reservation, run, &report)
			if len(launches) != 0 {
				t.Fatalf("child launched after sponsorship change: %+v", launches)
			}
			after, err := fx.db.Assignment(ctx, child.ID)
			if err != nil || after.Stage != factory.AssignmentFinished || after.Reason != factory.AssignReasonWithdrawn {
				t.Fatalf("unauthorized child was not retired: %+v err=%v", after, err)
			}
		})
	}
}

func TestReleasedReviewerRetryRefusesChangedRequiredChecks(t *testing.T) {
	ctx := context.Background()
	fx, _, _, run := reviewRunSeed(t, false)
	child, err := fx.db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := fx.db.RepositoryPolicy(ctx, child.Repository)
	if err != nil {
		t.Fatal(err)
	}
	policy.Checks = []string{"new-required-check"}
	if err := fx.db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	var launches []project.FactoryLaunch
	host := &fakeDispatchHost{
		launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
			launches = append(launches, in)
			return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
		},
		harness: func(family string) (project.FactoryHarnessPin, error) {
			return project.FactoryHarnessPin{Harness: family, Version: fx.seed.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
		},
	}
	deps := fx.seed.deps()
	deps.Host = host
	deps.Broker = &fakeDispatchBroker{}
	reservation, err := fx.db.Reservation(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unused, fenced := confirmUnused(ctx, deps, child, &run); !unused || fenced {
		t.Fatalf("reviewer run was not confirmed unused: unused=%t fenced=%t", unused, fenced)
	}
	report := DispatchReport{}
	recoverUnused(ctx, deps, child, reservation, run, &report)
	if len(launches) != 0 {
		t.Fatalf("child launched with obsolete required checks: %+v", launches)
	}
	after, err := fx.db.Assignment(ctx, child.ID)
	if err != nil || after.Stage != factory.AssignmentFinished || after.Reason != factory.AssignReasonSuperseded {
		t.Fatalf("obsolete child was not retired: %+v waits=%+v err=%v", after, report.Waits, err)
	}
}

func TestReleasedChildRetryRefusesSupersededPublication(t *testing.T) {
	ctx := context.Background()
	fx, publication := checkSeed(t, 16)
	parent, err := fx.db.Assignment(ctx, publication.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	correction := correctionRun(t, fx, parent, strings.Repeat("d", 40))
	// Keep the publisher whose target-tip ledger was populated by checkSeed's
	// initial publication; a fresh happyPublisher would observe an empty ref.
	exec := fx.exec
	exec.push = func(factory.PublicationWork) (factory.OperationOutcome, error) {
		return factory.OperationOutcome{NotObserved: true}, nil
	}
	pending := fx.coord.PublishCorrection(ctx, correction.ID)
	if len(pending.Waits) != 1 || pending.Waits[0].Reason != "push_unconfirmed" {
		t.Fatalf("correction intent was not left pending: %+v", pending)
	}
	stored, err := fx.db.PublicationByAssignment(ctx, parent.ID)
	if err != nil || len(stored.Corrections) != 1 || stored.Candidate != publication.Candidate ||
		stored.Corrections[0].Effect != factory.OpEffectPending || stored.Corrections[0].Work == nil {
		t.Fatalf("pending correction intent is not recorded against the old head: %+v %v", stored, err)
	}
	sponsorship, err := fx.db.Sponsorship(ctx, parent.Repository, parent.Connection)
	if err != nil {
		t.Fatal(err)
	}
	reviewerPermitted := false
	for _, role := range sponsorship.Roles {
		reviewerPermitted = reviewerPermitted || role == project.RoleReviewer
	}
	if !reviewerPermitted {
		sponsorship.Roles = append(sponsorship.Roles, project.RoleReviewer)
		if err := fx.db.SaveSponsorship(ctx, sponsorship); err != nil {
			t.Fatal(err)
		}
	}
	_, reviewRun := fx.childRun(t, parent, &publication, project.RoleReviewer, publication.Candidate, "f222222222222222222222222", false)
	staleChild, err := fx.db.AssignmentByRun(ctx, reviewRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	work := exec.pushes[len(exec.pushes)-1]
	exec.ledger[work.OperationID] = publicationTestOutcome(work, factory.OpRefPublish, committedOutcome("correction-receipt"))
	advanced := fx.coord.PublishCorrection(ctx, correction.ID)
	if len(advanced.Corrected) != 1 || advanced.Corrected[0].HeadOID != strings.Repeat("d", 40) {
		t.Fatalf("recorded correction was not reconciled: %+v", advanced)
	}
	var launches []project.FactoryLaunch
	host := &fakeDispatchHost{
		launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
			launches = append(launches, in)
			return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
		},
		harness: func(family string) (project.FactoryHarnessPin, error) {
			return project.FactoryHarnessPin{Harness: family, Version: fx.seed.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
		},
	}
	deps := fx.seed.deps()
	deps.Host = host
	deps.Broker = &fakeDispatchBroker{}
	reservation, err := fx.db.Reservation(ctx, staleChild.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unused, fenced := confirmUnused(ctx, deps, staleChild, &reviewRun); !unused || fenced {
		t.Fatalf("stale child run was not confirmed unused: unused=%t fenced=%t", unused, fenced)
	}
	report := DispatchReport{}
	recoverUnused(ctx, deps, staleChild, reservation, reviewRun, &report)
	if len(launches) != 0 {
		t.Fatalf("stale reviewer child launched: %+v", launches)
	}
	after, err := fx.db.Assignment(ctx, staleChild.ID)
	if err != nil || after.Stage != factory.AssignmentFinished || after.Reason != factory.AssignReasonSuperseded {
		t.Fatalf("stale child was not retired: %+v err=%v", after, err)
	}
	if len(report.Errors) != 0 || len(report.Waits) != 0 {
		t.Fatalf("stale child retry report: %+v", report)
	}
}

func TestDispatchResumesPersistedReadinessFromIntakeEvent(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	endpointHead := fx.accept(t, 3, "d333333333333333333333333")
	dependent := factory.Acceptance{
		ID: "d555555555555555555555555", Repository: fx.repo, IssueIndex: "5", Approver: 5, NativeRev: 41,
		TitleDigest: dispatchDigest("dep title"), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: dispatchDigest("dep body"), ContentVersion: 1,
		Prerequisites: []factory.AcceptedPrerequisite{{
			Occurrence: "100", DependsOn: "200", PrereqAcceptance: endpointHead.ID,
			EndpointRepo: fx.repo, EndpointIssue: 3, Outcome: factory.PrereqCode,
		}},
	}
	if err := db.AdmitAcceptanceDecision(ctx, dependent); err != nil {
		t.Fatal(err)
	}
	source := &fakeAcceptanceSource{evidence: map[string]AcceptanceEvidence{
		"7/5": {
			Issue:        AcceptanceIssueView{Index: "5", TitleDigest: dependent.TitleDigest, TitleOccurrence: dependent.TitleOccurrence, ContentDigest: dependent.ContentDigest, ContentVer: 1, Visible: true},
			Dependencies: []AcceptanceEdge{{Occurrence: "100", DependsOn: "200", Visible: true}},
			Revision:     41,
		},
		"7/3": {
			Issue: AcceptanceIssueView{
				Index: "3", TitleDigest: endpointHead.TitleDigest, TitleOccurrence: endpointHead.TitleOccurrence, ContentDigest: endpointHead.ContentDigest,
				ContentVer: endpointHead.ContentVersion, Visible: true,
			},
			Comments: []AcceptanceComment{{ID: "11", Digest: endpointHead.Sources[0].Digest, ContentVer: 0, Visible: true}},
			Revision: 41,
		},
	}}
	countedSource := &countingAcceptanceSource{inner: source}
	fx.reads.err = &AcceptanceRefusal{Reason: RefusalNativeBusy}
	coord := &Coordinator{
		Store: db, Host: fx.host, Broker: fx.broker,
		DispatchReads: fx.reads, AcceptanceReads: countedSource,
	}
	// Acceptance admission creates head work for both fixtures. Drain those
	// sources first so they cannot account for the later dependent read.
	if report := coord.Dispatch(ctx); len(report.Errors) != 0 {
		t.Fatalf("initial head-work drain errors = %+v", report.Errors)
	}
	for _, headWorkID := range []string{"root:7/3", "root:7/5"} {
		if _, err := db.ReadinessWork(ctx, headWorkID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("initial head source %s remains pending: %v", headWorkID, err)
		}
	}
	dependentReads := countedSource.byIssue["7/5"]

	// Admit through the authenticated intake producer and force only its page
	// work to yield. Dispatch must resume this persisted source on a fresh pass.
	delivery := "endpoint-head-intake"
	passCtx, _, cancelPass := coord.readinessPass(ctx)
	readinessBudgetFrom(passCtx).pages.Store(readinessMaxPages)
	_, _, err := coord.ObserveIssueEvent(passCtx, readinessHint(delivery, fx.repo, 3))
	cancelPass()
	if !errors.Is(err, ErrReadinessPassPending) {
		t.Fatalf("intake should yield with durable work pending, got %v", err)
	}
	workID := readinessDeliverySourceID(delivery)
	if seen, seenErr := db.IntakeDeliverySeen(ctx, delivery); seenErr != nil || seen {
		t.Fatalf("yield acknowledged incomplete delivery: seen=%t err=%v", seen, seenErr)
	}
	work, err := db.ReadinessWork(ctx, workID)
	if err != nil || work.Delivery != delivery {
		t.Fatalf("yield did not preserve the source: %+v err=%v", work, err)
	}

	if report := coord.Dispatch(ctx); len(report.Errors) != 0 {
		t.Fatalf("dispatch/drain errors = %+v", report.Errors)
	}
	if countedSource.byIssue["7/5"] <= dependentReads {
		t.Fatal("dispatch resume did not reassess the dependent after initial head work was drained")
	}
	if seen, seenErr := db.IntakeDeliverySeen(ctx, delivery); seenErr != nil || !seen {
		t.Fatalf("completed intake delivery was not acknowledged: seen=%t err=%v", seen, seenErr)
	}
	if _, workErr := db.ReadinessWork(ctx, workID); !errors.Is(workErr, store.ErrNotFound) {
		t.Fatalf("completed source remains pending: err=%v", workErr)
	}
	after, err := db.IssueControl(ctx, fx.repo, 5)
	if err != nil || after.Readiness != factory.ReadinessBlocked || after.Reason != factory.BlockerCodePending {
		t.Fatalf("dependent control after durable dispatch = %+v err=%v", after, err)
	}
}

func TestReportedAssignmentReadinessRootDrainsOnDispatch(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	seed := dispatchSeed(t, db)
	root := seed.accept(t, 3, "d333333333333333333333333")
	dependent := factory.Acceptance{
		ID: "d" + strings.Repeat("5", 24), Repository: seed.repo, IssueIndex: "5", Approver: 5, NativeRev: 41,
		TitleDigest: dispatchDigest("objective 5"), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: dispatchDigest("body 5"), ContentVersion: 2,
		Sources:     []factory.SelectedSource{{ID: "11", ContentVersion: 0, Digest: dispatchDigest("answer 5")}},
		Resolutions: []factory.SelectedSource{{ID: "31", ContentVersion: 0, Digest: dispatchDigest("resolution 5")}},
		Prerequisites: []factory.AcceptedPrerequisite{{
			Occurrence: "21", DependsOn: "9", EndpointRepo: seed.repo, EndpointIssue: 3,
			Outcome: factory.PrereqResult,
		}},
	}
	if err := db.AdmitAcceptanceDecision(ctx, dependent); err != nil {
		t.Fatal(err)
	}
	rootEvidence := AcceptanceEvidence{
		Revision: 41,
		Issue:    AcceptanceIssueView{Index: "3", TitleDigest: root.TitleDigest, TitleOccurrence: root.TitleOccurrence, ContentDigest: root.ContentDigest, ContentVer: root.ContentVersion, Visible: true},
		Comments: []AcceptanceComment{{ID: root.Sources[0].ID, Digest: root.Sources[0].Digest, ContentVer: root.Sources[0].ContentVersion, Visible: true}},
	}
	dependentEvidence := AcceptanceEvidence{
		Revision: 41,
		Issue:    AcceptanceIssueView{Index: "5", TitleDigest: dependent.TitleDigest, TitleOccurrence: dependent.TitleOccurrence, ContentDigest: dependent.ContentDigest, ContentVer: dependent.ContentVersion, Visible: true},
		Comments: []AcceptanceComment{
			{ID: dependent.Sources[0].ID, Digest: dependent.Sources[0].Digest, ContentVer: dependent.Sources[0].ContentVersion, Visible: true},
			{ID: dependent.Resolutions[0].ID, Digest: dependent.Resolutions[0].Digest, ContentVer: dependent.Resolutions[0].ContentVersion, Visible: true},
		},
		Dependencies: []AcceptanceEdge{{Occurrence: "21", DependsOn: "9", Visible: true}},
	}
	evidence := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{
		"7/3": rootEvidence, "7/5": dependentEvidence,
	}}
	countedEvidence := &countingAcceptanceSource{inner: evidence}
	coord := NewCoordinator(db, seed.host, seed.broker)
	coord.AcceptanceReads, coord.DispatchReads = countedEvidence, seed.reads

	// This first real intake drains the initial acceptance roots and, through its
	// existing dispatch hook, launches the assigned run. It establishes a baseline
	// read count for the dependant before the successful report is recorded.
	if _, _, err := coord.ObserveIssueEvent(ctx, readinessHint("assignment-report-root", seed.repo, 3)); err != nil {
		t.Fatal("initial root intake:", err)
	}
	beforeDependentReads := countedEvidence.byIssue["7/5"]
	workID := "root:7/3"
	if _, err := db.ReadinessWork(ctx, workID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("initial root work was not completed before settlement: %v", err)
	}
	initialDependent, err := db.IssueControl(ctx, seed.repo, 5)
	if err != nil || initialDependent.Readiness != factory.ReadinessBlocked || initialDependent.Reason != factory.BlockerResultPending {
		t.Fatalf("open result prerequisite was not initially blocked: %+v, %v", initialDependent, err)
	}
	assigned, err := db.AssignedAssignments(ctx, 10)
	if err != nil || len(assigned) != 1 || assigned[0].Issue != 3 {
		t.Fatalf("intake dispatch did not record the root assignment: %+v, %v", assigned, err)
	}

	// The test substitutes only the host/native stop reply and broker boundary.
	// Reconcile still runs the production settlement of the recorded MCP
	// finish, Store write and same-transaction readiness enqueue; this is
	// not native qualification.
	reported := assigned[0]
	plan := phaseTestPlan(t, reported.AttemptRoot, reported.SourceCommit, reported.Preparation)
	recordPhaseFinishMCP(t, ctx, db, reported, phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	}))
	seed.host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{
			ID: in.ID, Project: in.Project, Phase: project.FactoryCompleted, Retirement: "confirmed",
		}, nil
	}
	coord.Host = seed.host
	coord.Broker = &stubBroker{
		get:   func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error { return nil },
	}
	// This nested pass models settlement inside a caller-owned coordinator pass,
	// not an installed native report; the report below remains a substituted
	// host boundary response.
	passCtx, _, cancelPass := coord.readinessPass(ctx)
	reconcile, err := coord.Reconcile(passCtx)
	cancelPass()
	if err != nil || len(reconcile.Settled) != 1 || len(reconcile.Fenced) != 0 {
		t.Fatalf("successful report settlement: %+v, %v", reconcile, err)
	}
	finished, err := db.Assignment(ctx, assigned[0].ID)
	if err != nil || finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.Succeeded ||
		finished.Result == nil || !finished.Result.Reported {
		t.Fatalf("settlement did not record a reported successful assignment: %+v, %v", finished, err)
	}
	if countedEvidence.byIssue["7/5"] != beforeDependentReads {
		t.Fatal("nested settlement unexpectedly drained readiness before the next Dispatch trigger")
	}
	work, err := db.ReadinessWork(ctx, workID)
	if err != nil || work.Root != (factory.DependenceRef{Repository: seed.repo, Issue: 3}) {
		t.Fatalf("reported finish did not emit its durable root: %+v, %v", work, err)
	}
	// The substituted native snapshot now reports the selected resolution and
	// endpoint closure needed by PrereqResult; assignment success alone does not
	// satisfy that prerequisite.
	rootEvidence.Issue.Closed, rootEvidence.Issue.ClosedUnix = true, time.Now().Unix()
	rootEvidence.Issue.ClosingOccurrence, rootEvidence.Issue.LifecycleOccurrence = "93", "93"
	rootEvidence.Revision = 42
	evidence.evidence["7/3"] = rootEvidence

	if report := coord.Dispatch(ctx); len(report.Errors) != 0 {
		t.Fatalf("Dispatch readiness drain reported errors: %+v", report.Errors)
	}
	if countedEvidence.byIssue["7/5"] <= beforeDependentReads {
		t.Fatal("Dispatch did not reassess the dependent from the reported assignment root")
	}
	readyDependent, err := db.IssueControl(ctx, seed.repo, 5)
	if err != nil || readyDependent.Readiness != factory.ReadinessQueued || readyDependent.Reason != factory.ReasonEligible {
		t.Fatalf("closed, resolved result did not queue the reassessed dependent: %+v, %v", readyDependent, err)
	}
	if _, err = db.ReadinessWork(ctx, workID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Dispatch did not complete the emitted source: %v", err)
	}
}

func TestTypedAbsenceControlsLaunchReservationPG(t *testing.T) {
	for _, mode := range []string{"typed", "text"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db, _ := dispatchTestDB(t)
			fx := dispatchSeed(t, db)
			decision := fx.accept(t, 3, "d333333333333333333333333")
			fx.queue(t, 3, decision.ID)
			fx.host.launch = func(project.FactoryLaunch) (project.FactoryState, error) {
				return project.FactoryState{}, errors.New("lost launch reply")
			}
			fx.host.inspect = func(project.FactoryInspect) (project.FactoryState, error) {
				if mode == "typed" {
					return project.FactoryState{}, fmt.Errorf("inspect: %w", host.ErrRunNotFound)
				}
				return project.FactoryState{}, errors.New("inspect: factory run not found")
			}
			report := DispatchPass(ctx, fx.deps())
			if len(fx.host.launches) != 1 {
				t.Fatalf("launch was not attempted: %+v", report)
			}
			run, err := db.FactoryRun(ctx, fx.host.launches[0].Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			assignment, err := db.AssignmentByRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			reservation, err := db.Reservation(ctx, assignment.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "text" {
				if run.Reconciled || reservation.State != factory.ReservationHeld {
					t.Fatal("matching error text released uncertain work", run, reservation)
				}
			} else if !run.Reconciled || run.Outcome != factory.Failed || reservation.State != factory.ReservationReleased {
				t.Fatal("confirmed unused identity did not release its reservation", run, reservation)
			}
		})
	}
}

// Real dispatch packets and PostgreSQL reopen: scripted successful/lost native
// replies retain evidence before custody fails and cannot become unused later.
func TestDispatchRetainsObservedExecutionPG(t *testing.T) {
	for _, mode := range []string{"success", "lost_reply", "custody_unreadable", "custody_conflict", "custody_only", "sealed_custody_only", "partial_launch"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			db, dsn := dispatchTestDB(t)
			fx := dispatchSeed(t, db)
			decision := fx.accept(t, 3, "d333333333333333333333333")
			fx.queue(t, 3, decision.ID)
			var state project.FactoryState
			fullObservation := false
			fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
				state = project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryRunning, Live: true, Container: strings.Repeat("c", 64), Invocation: factory.NewID(), Unit: "fixture-run.service", UID: 1001, GID: 1002, Login: "factory", LeaseID: "owned-lease", Generation: 3, Delivered: true}
				if mode == "lost_reply" || strings.HasSuffix(mode, "custody_only") {
					return project.FactoryState{}, errors.New("lost launch reply")
				}
				if mode == "partial_launch" {
					return project.FactoryState{ID: state.ID, Project: state.Project, Role: state.Role, Phase: state.Phase, Invocation: state.Invocation, Live: true}, nil
				}
				return state, nil
			}
			fx.host.inspect = func(project.FactoryInspect) (project.FactoryState, error) {
				if strings.HasSuffix(mode, "custody_only") {
					return project.FactoryState{}, host.ErrRunNotFound
				}
				return state, nil
			}
			fx.broker.get = func(kind, id string) (identity.Execution, error) {
				if mode == "partial_launch" && !fullObservation {
					return identity.Execution{}, identity.ErrNotFound
				}
				if mode == "custody_unreadable" {
					return identity.Execution{}, errors.New("custody unreachable")
				}
				boundary := state.Container
				if mode == "custody_conflict" {
					boundary = strings.Repeat("d", 64)
				}
				phase, lease := identity.ExecutionLive, state.LeaseID
				if mode == "sealed_custody_only" {
					phase, lease = identity.ExecutionTerminal, ""
				}
				return identity.Execution{Kind: kind, ExecutionID: id, State: phase, Digest: strings.Repeat("e", 64), LeaseID: lease, Binding: &identity.Binding{Kind: kind, ID: id, Project: boundary, Generation: state.Generation, InvocationID: state.Invocation, UID: state.UID, GID: state.GID, Login: state.Login}}, nil
			}
			report := DispatchPass(ctx, fx.deps())
			expectedLaunched := 0
			if mode == "success" {
				expectedLaunched = 1
			}
			if len(fx.host.launches) != 1 || len(report.Launched) != expectedLaunched {
				t.Fatal("unexpected launch result", report)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fx.db = db
			saved, err := db.FactoryRun(ctx, state.ID)
			expectedLease := state.LeaseID
			expectedGeneration := int64(3)
			if mode == "sealed_custody_only" {
				expectedLease = ""
			}
			if mode == "partial_launch" {
				expectedLease, expectedGeneration = "", 0
			}
			if err != nil || saved.InvocationID() != state.Invocation || saved.IdentityLeaseID != expectedLease || saved.IdentityGeneration != expectedGeneration || saved.Reconciled || saved.Outcome != "" {
				t.Fatal("launch observation lost at reopen", saved, err)
			}
			if (saved.IdentityBinding != nil) != (mode != "custody_unreadable" && mode != "custody_conflict" && mode != "partial_launch") {
				t.Fatal("foreign/unreadable custody adopted", saved)
			}
			if strings.HasSuffix(mode, "custody_only") && (saved.ApplianceInvocation != nil || saved.CredentialReturned) {
				t.Fatal("broker-only read invented host facts or credential return", saved)
			}
			if mode == "partial_launch" {
				if saved.ApplianceInvocation == nil || saved.ApplianceInvocation.Container != "" {
					t.Fatal("partial launch invented its missing boundary", saved)
				}
				fullObservation = true
				RecoverDispatch(ctx, fx.deps())
				saved, err = db.FactoryRun(ctx, state.ID)
				if err != nil || saved.InvocationID() != state.Invocation || saved.ApplianceInvocation == nil || saved.ApplianceInvocation.Container != state.Container || saved.IdentityLeaseID != state.LeaseID || saved.IdentityBinding == nil {
					t.Fatal("later observation did not fill unknown boundary facts", saved, err)
				}
			}
			fx.host.inspect = func(project.FactoryInspect) (project.FactoryState, error) {
				return project.FactoryState{}, host.ErrRunNotFound
			}
			fx.broker.get = func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound }
			RecoverDispatch(ctx, fx.deps())
			a, err := db.AssignmentByRun(ctx, state.ID)
			if err != nil {
				t.Fatal(err)
			}
			reservation, err := db.Reservation(ctx, a.ID)
			if err != nil || reservation.State != factory.ReservationHeld || len(fx.host.launches) != 1 {
				t.Fatal("absence released/relaunched observed work", reservation, err)
			}
			saved, err = db.FactoryRun(ctx, state.ID)
			if err != nil || saved.InvocationID() != state.Invocation || saved.Reconciled || saved.Outcome != "" {
				t.Fatal("recovery erased observed execution", saved, err)
			}
		})
	}
}

func TestIntakeTriggersAutomaticDispatch(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	decision := fx.accept(t, 3, "d333333333333333333333333")
	source := &fakeAcceptanceSource{evidence: map[string]AcceptanceEvidence{
		"7/3": {
			Issue: AcceptanceIssueView{
				Index: "3", TitleDigest: decision.TitleDigest, TitleOccurrence: decision.TitleOccurrence, ContentDigest: decision.ContentDigest,
				ContentVer: decision.ContentVersion, Visible: true,
			},
			Comments: []AcceptanceComment{{
				ID: "11", Digest: decision.Sources[0].Digest,
				ContentVer: decision.Sources[0].ContentVersion, Visible: true,
			}},
			Revision: 41,
		},
	}}
	coord := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, AcceptanceReads: source, DispatchReads: fx.reads}
	control, _, err := coord.ObserveIssueEvent(ctx, IntakeHint{Delivery: "st08-intake-1", Repository: fx.repo, Issue: 3})
	if err != nil || control.Readiness != factory.ReadinessQueued {
		t.Fatalf("intake = %+v %v", control, err)
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("automatic launches = %d", len(fx.host.launches))
	}
	assignment, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil || assignment.Run != fx.host.launches[0].Run.ID || assignment.Stage != factory.AssignmentAssigned {
		t.Fatalf("latest assignment = %+v %v", assignment, err)
	}
	// A duplicate delivery replays without dispatching again.
	again, changed, err := coord.ObserveIssueEvent(ctx, IntakeHint{Delivery: "st08-intake-1", Repository: fx.repo, Issue: 3})
	if err != nil || changed || again.Revision != control.Revision {
		t.Fatalf("replay = %+v %v %v", again, changed, err)
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("launches after replay = %d", len(fx.host.launches))
	}
}

func TestAssessedResultPrerequisiteAppearsInRecordedDispatchPrompt(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	endpoint := fx.accept(t, 3, "d333333333333333333333333")
	dependent := fx.accept(t, 5, "d555555555555555555555555")
	dependent.Predecessor = dependent.ID
	dependent.ID = "d666666666666666666666666"
	dependent.Prerequisites = []factory.AcceptedPrerequisite{{
		Occurrence: "21", DependsOn: "9", EndpointRepo: fx.repo, EndpointIssue: 3,
		Outcome: factory.PrereqResult,
	}}
	resolution := "accepted resolution"
	dependent.Resolutions = []factory.SelectedSource{{ID: "31", ContentVersion: 0, Digest: dispatchDigest(resolution)}}
	if err := db.AdmitAcceptanceDecision(ctx, dependent); err != nil {
		t.Fatal(err)
	}
	fx.decision[5] = dependent
	dependentInput := fx.reads.inputs["7/5"]
	dependentInput.Comments = append(dependentInput.Comments, DispatchComment{ID: "31", Content: resolution, Digest: dispatchDigest(resolution), Visible: true})
	fx.reads.inputs["7/5"] = dependentInput
	endpointIssue := AcceptanceIssueView{
		Index: "3", TitleDigest: endpoint.TitleDigest, TitleOccurrence: endpoint.TitleOccurrence, ContentDigest: endpoint.ContentDigest,
		ContentVer: endpoint.ContentVersion, ClosingOccurrence: "94", LifecycleOccurrence: "94", ClosedUnix: 1_800_000_000,
		Closed: true, Visible: true,
	}
	// The same idle native revision must report the endpoint closed in both
	// acceptance and dispatch reads; only the dependent remains runnable.
	endpointInput := fx.reads.inputs["7/3"]
	endpointInput.Issue.Closed, endpointInput.Issue.LifecycleOccurrence, endpointInput.Issue.ClosingOccurrence, endpointInput.Issue.ClosedUnix = true, "94", "94", endpointIssue.ClosedUnix
	fx.reads.inputs["7/3"] = endpointInput
	source := &fakeAcceptanceSource{evidence: map[string]AcceptanceEvidence{
		"7/3": {
			Issue:    endpointIssue,
			Comments: []AcceptanceComment{{ID: "11", Digest: endpoint.Sources[0].Digest, Visible: true}},
			Revision: 41,
		},
		"7/5": {
			Issue: AcceptanceIssueView{
				Index: "5", TitleDigest: dependent.TitleDigest, TitleOccurrence: dependent.TitleOccurrence, ContentDigest: dependent.ContentDigest,
				ContentVer: dependent.ContentVersion, Visible: true,
			},
			Comments: []AcceptanceComment{
				{ID: "11", Digest: dependent.Sources[0].Digest, Visible: true},
				{ID: "31", Digest: dispatchDigest(resolution), Visible: true},
			},
			Dependencies: []AcceptanceEdge{{Occurrence: "21", DependsOn: "9", Visible: true}},
			Revision:     41,
		},
	}}
	coord := &Coordinator{
		Store: db, Host: fx.host, Broker: fx.broker,
		AcceptanceReads: source, DispatchReads: fx.reads,
	}
	control, _, err := coord.ObserveIssueEvent(ctx, IntakeHint{Delivery: "result-dependency-dispatch", Repository: fx.repo, Issue: 5})
	if err != nil || control.Readiness != factory.ReadinessQueued {
		t.Fatalf("dependent readiness = %+v err=%v", control, err)
	}
	if len(fx.host.launches) != 1 {
		t.Fatalf("dependency dispatch launches = %d", len(fx.host.launches))
	}
	assignment, err := db.LatestIssueAssignment(ctx, fx.repo, 5)
	if err != nil || assignment.Stage != factory.AssignmentAssigned {
		t.Fatalf("dependent assignment = %+v err=%v", assignment, err)
	}
	for _, want := range []string{
		"## Accepted prerequisites", "Occurrence 21: depends on issue 9 at repository 7, issue 3; outcome result",
		"satisfied as of queued readiness revision " + fmt.Sprint(control.Revision),
		"native closing occurrence 94 under acceptance " + dependent.ID,
	} {
		if !strings.Contains(string(assignment.Prompt), want) {
			t.Errorf("recorded dependent prompt lacks %q", want)
		}
	}
}
