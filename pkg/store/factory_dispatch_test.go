package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

func dispatchStoreFixture(t *testing.T) *Store {
	t.Helper()
	db := grantStoreFixture(t)
	seedDispatchLimits(t, db)
	return db
}

func dispatchTestControlFor(t *testing.T, db *Store, a factory.Assignment) factory.IssueControl {
	t.Helper()
	if _, err := db.AcceptanceHead(context.Background(), a.Repository, a.Issue); errors.Is(err, ErrNotFound) {
		decision := acceptanceFixture(a.Acceptance, "")
		decision.Repository, decision.IssueIndex, decision.NativeRev = a.Repository, strconv.FormatInt(a.Issue, 10), a.NativeRev
		if err := db.AdmitAcceptanceDecision(context.Background(), decision); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	control := factory.IssueControl{
		Repository: a.Repository, Issue: a.Issue, Acceptance: a.Acceptance, NativeRev: a.NativeRev,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	stored, _, err := db.RecordIssueAssessment(context.Background(), control, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func recordDispatchTestPacket(t *testing.T, ctx context.Context, db *Store, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	t.Helper()
	return db.RecordDispatchPacket(ctx, dispatchTestControlFor(t, db, a), a, r, run, view)
}

// seedDispatchLimits records current dispatch authority and room for two
// concurrent runs on repository 7 and connection "conn".
func seedDispatchLimits(t *testing.T, db *Store) {
	t.Helper()
	ctx := context.Background()
	policy := grantTestPolicy()
	policy.Repository = 7
	for _, err := range []error{
		db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: 7, MaxConcurrentRuns: 2}),
		db.SaveRepositoryPolicy(ctx, policy),
		db.SaveOperatorGrant(ctx, factory.OperatorGrant{Repository: 7, GrantedBy: 7, Active: true}),
		db.SaveSponsorship(ctx, factory.Sponsorship{Repository: 7, GrantedBy: 7, ActorID: 7, ProjectID: dispatchTestProjectID(), Generation: 1, Connection: "conn", GrantID: "grant", Roles: []string{project.RoleCoder}, AllowanceMinutes: 120, Active: true}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	projectID := dispatchTestProjectID()
	if err := db.UpsertUser(ctx, User{ID: 7, Login: "soda-tester"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProject(ctx, Project{ID: projectID, Name: "factory", RepositoryID: 7, OwnerID: 7, Repository: "soda/factory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEnvironmentGrant(ctx, project.EnvironmentGrant{
		Project: projectID, Repository: 7, Owner: 7, Active: true,
		Profile: &project.Profile{
			ID: project.RockyHeadless, Distribution: "rocky", Version: "9.6",
			Interface: "headless", Architecture: "amd64",
			Image: "sha256:" + strings.Repeat("b", 64), Revision: strings.Repeat("c", 40),
		},
	}); err != nil {
		t.Fatal(err)
	}
	requirementID := dispatchTestRequirementID()
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{
		ID: requirementID, Project: projectID, Approver: 7,
		SourceCommit: strings.Repeat("1", 40), SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{
		ID: dispatchTestApprovalID(), Project: projectID, Requirement: requirementID,
		Approver: 7, EffectsDigest: strings.Repeat("a", 64), ReadinessDigest: strings.Repeat("b", 64), Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	seedReadyPacketPreparation(t, db, factory.Assignment{Preparation: "f" + strings.Repeat("b", 24), ProjectID: projectID, Role: project.RoleCoder, SourceCommit: strings.Repeat("c", 40), Authority: dispatchTestAuthority()})
}

func seedReadyPacketPreparation(t *testing.T, db *Store, a factory.Assignment) {
	t.Helper()
	if _, err := db.Preparation(t.Context(), a.Preparation); err == nil {
		return
	} else if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	requirement, err := db.RequirementDecision(t.Context(), a.Authority.RequirementsID)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := db.ApprovalDecision(t.Context(), a.Authority.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	p := project.Preparation{ID: a.Preparation, Project: a.ProjectID, Role: a.Role, SourceCommit: a.SourceCommit, SetupDigest: requirement.SetupDigest, Requirements: requirement.Ref(1), Approval: approval.Ref(1)}
	state := execution.PreparationState{ID: p.ID, Project: p.Project, Role: p.Role, SourceCommit: p.SourceCommit, SetupDigest: p.SetupDigest, Container: strings.Repeat("d", 64), Phase: project.PrepareReady, Ready: true}
	if _, _, err := db.AdmitPreparation(t.Context(), execution.PreparationRecord{Preparation: p, State: state}); err != nil {
		t.Fatal(err)
	}
}

func dispatchTestProjectID() string     { return "p" + strings.Repeat("d", 24) }
func dispatchTestRequirementID() string { return "d" + strings.Repeat("c", 24) }
func dispatchTestApprovalID() string    { return "d" + strings.Repeat("e", 24) }

func dispatchTestAuthority() factory.AuthorityRef {
	return factory.AuthorityRef{
		Policy: 1, Operator: 1, Capacity: 1, Sponsorship: 1, Environment: 1,
		SponsorshipConnection: "conn",
		RequirementsID:        dispatchTestRequirementID(), ApprovalID: dispatchTestApprovalID(),
	}
}

func assertDispatchPacketAbsent(t *testing.T, db *Store, a factory.Assignment) {
	t.Helper()
	for _, check := range []struct{ table, key, value string }{
		{"factory_assignments", "id", a.ID},
		{"factory_reservations", "assignment", a.ID},
		{"factory_runs", "id", a.Run},
		{"factory_run_views", "run", a.Run},
	} {
		var count int
		query := "SELECT count(*) FROM " + check.table + " WHERE " + check.key + "=$1"
		if err := db.db.QueryRowContext(context.Background(), query, check.value).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("dispatch packet left %d rows in %s", count, check.table)
		}
	}
}

func dispatchCurrentAuthority(t *testing.T, db *Store, a factory.Assignment) factory.AuthorityRef {
	t.Helper()
	policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := db.OperatorGrant(context.Background(), a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := db.Capacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sponsorship, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := db.EnvironmentGrant(context.Background(), a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := db.RequirementHead(context.Background(), a.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := db.ApprovalHead(context.Background(), a.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	return factory.AuthorityRef{
		Policy: policy.Revision, Operator: operator.Revision, Capacity: capacity.Revision,
		Sponsorship: sponsorship.Revision, SponsorshipConnection: sponsorship.Connection, Environment: environment.Revision,
		RequirementsID: requirement, ApprovalID: approval,
	}
}

func dispatchTestPrompt(t *testing.T) []byte {
	t.Helper()
	selection := grantTestPolicy().Roles[project.RoleCoder]
	prompt, err := factory.BuildDispatchPrompt(factory.PromptInputs{
		Repository: 7, Issue: 3, NativeRev: 5, Phase: project.ExecutionInvestigate,
		AcceptanceID: "d" + strings.Repeat("a", 24),
		TargetBranch: "refs/heads/main", SourceCommit: strings.Repeat("c", 40),
		Preparation: "f" + strings.Repeat("b", 24),
		Harness:     selection.Harness, Model: selection.Model, Role: project.RoleCoder,
		ProviderConnection: "conn", RequiredChecks: []string{"ci"},
		AttemptLimits:  factory.DefaultAttemptLimits(),
		RequirementsID: "d" + strings.Repeat("e", 24), ApprovalID: "d" + strings.Repeat("f", 24),
		Title: "objective",
	})
	if err != nil {
		t.Fatal(err)
	}
	return prompt
}

func dispatchTestPacket(t *testing.T, now time.Time) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	selection := grantTestPolicy().Roles[project.RoleCoder]
	prompt := dispatchTestPrompt(t)
	sum := sha256.Sum256(prompt)
	assignmentID, runID := factory.NewID(), factory.NewID()
	a := factory.Assignment{
		ID: assignmentID, AttemptRoot: assignmentID, PublicationAssignment: assignmentID,
		ProjectID: dispatchTestProjectID(), Role: project.RoleCoder,
		ActorID: 7,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 3, Acceptance: "d" + strings.Repeat("a", 24)},
		},
		Phase:      project.ExecutionInvestigate,
		Repository: 7, Issue: 3, Revision: 0, NativeRev: 5,
		Acceptance: "d" + strings.Repeat("a", 24), Preparation: "f" + strings.Repeat("b", 24),
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: "conn", SourceCommit: strings.Repeat("c", 40),
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned, Attempts: 1,
		CreatedUnix: now.Unix(), Authority: dispatchTestAuthority(),
	}
	r := factory.Reservation{AssignmentID: a.ID, Repository: 7, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: strings.Repeat("c", 40),
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: selection.Harness + "-" + selection.HarnessVers, Model: selection.Model,
	}
	view := factory.RunView{RunID: runID, Repository: 7, Issue: 3, Attempt: a.ID}
	return a, r, run, view
}

func TestRecordDispatchPacket(t *testing.T) {
	for _, mode := range []string{"native_preparation", "missing_preparation", "stopped_preparation", "unselected_paying_account"} {
		t.Run(mode, func(t *testing.T) {
			db := dispatchStoreFixture(t)
			a, reservation, run, view := dispatchTestPacket(t, time.Now())
			record, err := db.Preparation(t.Context(), a.Preparation)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unselected_paying_account":
				policy, err := db.RepositoryPolicy(t.Context(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				selection := policy.Roles[a.Role]
				selection.Account = "another-account"
				policy.Roles[a.Role] = selection
				if err := db.SaveRepositoryPolicy(t.Context(), policy); err != nil {
					t.Fatal(err)
				}
				a.Authority = dispatchCurrentAuthority(t, db, a)
			case "native_preparation":
				record.Preparation.ID = "f" + strings.Repeat("a", 24)
				record.Native = &execution.NativeWorkspaceRef{Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "owned-native-checkout"}
				record.State.ID, record.State.Container, record.State.Native = record.Preparation.ID, "", record.Native
				if _, _, err := db.AdmitPreparation(t.Context(), record); err != nil {
					t.Fatal(err)
				}
				a.Preparation = record.Preparation.ID
			case "missing_preparation":
				if _, err := db.db.ExecContext(t.Context(), `DELETE FROM project_preparations WHERE id=$1`, a.Preparation); err != nil {
					t.Fatal(err)
				}
			case "stopped_preparation":
				record.State.Ready, record.State.Phase, record.State.Stopped, record.State.Retirement = false, project.PrepareStopped, true, "confirmed"
				if err := db.ObservePreparation(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			}
			if err := recordDispatchTestPacket(t, t.Context(), db, a, reservation, run, view); !errors.Is(err, ErrAdmissionChanged) {
				t.Fatal("appliance packet admitted an unavailable preparation", err)
			}
			assertDispatchPacketAbsent(t, db, a)
			var allowances int
			if err := db.db.QueryRowContext(t.Context(), `SELECT count(*) FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&allowances); err != nil || allowances != 0 {
				t.Fatal("refused preparation consumed an attempt allowance", allowances, err)
			}
		})
	}
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, now)
	a.AdoptedHarness = project.FactoryHarnessPin{Harness: project.FactoryHarnessMuse, Version: a.HarnessVers, SHA256: strings.Repeat("a", 64), Image: run.Image}
	policy, err := db.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	policy.Execution = &factory.ActorBindingRef{TokenID: 7, ActorID: 7, Kind: "execution.launch", ControllerGrantID: strings.Repeat("9", 32)}
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	a.Authority = dispatchCurrentAuthority(t, db, a)
	unselected, err := db.Sponsorship(ctx, a.Repository, a.Connection)
	if err != nil {
		t.Fatal(err)
	}
	unselected.Connection, unselected.Revision = "aaa-unselected", 0
	if err := db.SaveSponsorship(ctx, unselected); err != nil {
		t.Fatal(err)
	}
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatalf("packet refused: %v", err)
	}
	got, err := db.Assignment(ctx, a.ID)
	if err != nil || got.Run != run.ID || got.Stage != factory.AssignmentAssigned {
		t.Fatalf("assignment unreadable: %+v %v", got, err)
	}
	byRun, err := db.AssignmentByRun(ctx, run.ID)
	if err != nil || byRun.ID != a.ID {
		t.Fatalf("assignment by run unreadable: %+v %v", byRun, err)
	}
	forged := got
	forged.ActorID++
	forged.Stage, forged.Outcome, forged.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
	forged.FinishedUnix = now.Unix()
	result := factory.ResultSynthesized(forged.ID, forged.Run, "failed", "run failed", forged.FinishedUnix)
	forged.Result = &result
	if err := db.FinishAssignment(ctx, forged); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finish changed the recorded execution actor: %v", err)
	}
	stored, err := db.FactoryRun(ctx, run.ID)
	if err != nil || stored.ProjectID != a.ProjectID {
		t.Fatalf("packet run unreadable: %+v %v", stored, err)
	}
	captured, err := factory.ControllerAdmissionDigest(got, stored)
	if err != nil || captured != stored.Admission.ControllerManifestDigest || stored.Admission.Policy.Execution.ControllerGrantID != policy.Execution.ControllerGrantID {
		t.Fatal("dispatch did not retain exact controller packet", captured, err)
	}
	changed := stored
	changed.Model = "another-model"
	altered, err := factory.ControllerAdmissionDigest(got, changed)
	if err != nil || altered == captured {
		t.Fatal("controller digest did not bind the selected model", err)
	}
	held, err := db.HeldReservations(ctx, 10)
	if err != nil || len(held) != 1 || held[0].AssignmentID != a.ID {
		t.Fatalf("reservation not held: %+v %v", held, err)
	}
	again, r2, run2, view2 := dispatchTestPacket(t, now)
	again.Authority = a.Authority
	again.ID, again.Run, again.RunHistory = factory.NewID(), run2.ID, []string{run2.ID}
	again.AttemptRoot, again.PublicationAssignment = again.ID, again.ID
	r2.AssignmentID = again.ID
	view2.Attempt = again.ID
	if err := recordDispatchTestPacket(t, ctx, db, again, r2, run2, view2); err == nil {
		t.Fatal("second unfinished assignment for the issue accepted")
	} else if !isAssignmentActive(err) {
		t.Fatalf("wrong conflict error: %v", err)
	}
	prepared, err := db.Preparation(ctx, got.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	input := project.FactoryLaunch{Run: project.FactoryRun{ID: stored.ID, Project: stored.ProjectID, Role: stored.Role, Preparation: got.Preparation,
		Harness: project.FactoryHarnessMuse, HarnessVers: got.HarnessVers, Model: got.Model, Effort: stored.Admission.Policy.Roles[got.Role].Profile.Effort,
		Contributor: stored.Admission.Policy.Roles[got.Role].Profile.Contributor, Assignment: got.PromptSHA, SourceCommit: got.SourceCommit,
		Connection: got.Connection, Actor: got.ActorID, Deadline: stored.Deadline, ExecutionPhase: got.Phase}, Prompt: got.Prompt,
		HarnessSHA256: stored.Admission.Tool.SHA256, MCP: &project.FactoryMCPBinding{Capability: stored.ID, Secret: strings.Repeat("a", 64)}}
	request, err := stored.LaunchRequest(got, input)
	if err != nil {
		t.Fatal("captured appliance request", err, input.Validate(), stored.Validate(), got.Validate())
	}
	capability := factory.MCPCapability{ID: stored.ID, AssignmentID: got.ID, RunID: stored.ID, Generation: 1,
		Profile: factory.MCPProfileOfAssignment(got), Subject: got.Subject.SubjectKey(got.Repository), Repository: got.Repository,
		Project: got.ProjectID, PreparationDigest: prepared.Preparation.SetupDigest, SourceDigest: got.SourceCommit, AcceptedRevision: strconv.FormatInt(got.NativeRev, 10),
		Authority: strings.Repeat("a", 64), DeadlineUnix: stored.Deadline.Unix(), State: factory.MCPCapabilityStaged}
	if err := db.StageMCPCapability(ctx, capability); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AuthorizeFactoryLaunch(ctx, request); err != nil {
		t.Fatal("current appliance packet refused", err)
	}
	policy, err = db.RepositoryPolicy(ctx, got.Repository)
	if err != nil {
		t.Fatal(err)
	}
	policy.Paused = true
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AuthorizeFactoryLaunch(ctx, request); err == nil {
		t.Fatal("captured appliance packet ignored current policy withdrawal")
	}
}

func TestRecordDispatchPacketEnforcesOneRepositorySession(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name             string
		releasePriorSlot bool
	}{
		{name: "held reservation"},
		{name: "active attributed run after reservation release", releasePriorSlot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchStoreFixture(t)
			now := time.Now().UTC()
			first, reservation, run, view := dispatchTestPacket(t, now)
			if err := recordDispatchTestPacket(t, ctx, db, first, reservation, run, view); err != nil {
				t.Fatal(err)
			}
			if tc.releasePriorSlot {
				if err := db.ReleaseReservation(ctx, first.ID); err != nil {
					t.Fatal(err)
				}
			}
			second, nextReservation, nextRun, nextView := dispatchTestPacket(t, now)
			second.ID, second.Issue, second.Run, second.RunHistory = factory.NewID(), 4, nextRun.ID, []string{nextRun.ID}
			second.Acceptance = "d" + strings.Repeat("b", 24)
			second.Subject.Issue.Issue, second.Subject.Issue.Acceptance = 4, second.Acceptance
			second.AttemptRoot, second.PublicationAssignment = second.ID, second.ID
			nextReservation.AssignmentID = second.ID
			nextView.Issue, nextView.Attempt = second.Issue, second.ID
			if err := recordDispatchTestPacket(t, ctx, db, second, nextReservation, nextRun, nextView); !errors.Is(err, ErrRepositoryFull) {
				t.Fatalf("second same-repository session error = %v, want %v", err, ErrRepositoryFull)
			}
			assertDispatchPacketAbsent(t, db, second)
		})
	}
}

func TestRecordDispatchPacketRejectsChangedQueuedControlAtomically(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, time.Now())
	expected := dispatchTestControlFor(t, db, a)
	changed := expected
	changed.NativeRev++
	changed.Fingerprint = strings.Repeat("3", 64)
	if _, _, err := db.RecordIssueAssessment(ctx, changed, time.Now()); err != nil {
		t.Fatal(err)
	}
	err := db.RecordDispatchPacket(ctx, expected, a, r, run, view)
	if !errors.Is(err, ErrDispatchControlStale) {
		t.Fatalf("stale queued control error = %v", err)
	}
	assertDispatchPacketAbsent(t, db, a)
}

func TestRecordDispatchPacketRejectsWithdrawnAcceptanceAtomically(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, time.Now())
	expected := dispatchTestControlFor(t, db, a)
	if err := db.WithdrawAcceptanceDecision(ctx, a.Repository, a.Issue, a.Acceptance, 7); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDispatchPacket(ctx, expected, a, r, run, view); !errors.Is(err, ErrDispatchControlStale) {
		t.Fatalf("withdrawn acceptance packet error = %v", err)
	}
	assertDispatchPacketAbsent(t, db, a)
}

func TestRecordDispatchPacketBindsExecutionActorAndProject(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(*factory.Sponsorship){
		"actor":   func(s *factory.Sponsorship) { s.ActorID++ },
		"project": func(s *factory.Sponsorship) { s.ProjectID = "p" + strings.Repeat("e", 24) },
	} {
		t.Run(name, func(t *testing.T) {
			db := dispatchStoreFixture(t)
			sponsorship, err := db.Sponsorship(ctx, 7, "conn")
			if err != nil {
				t.Fatal(err)
			}
			change(&sponsorship)
			if err := db.SaveSponsorship(ctx, sponsorship); err != nil {
				t.Fatal(err)
			}
			a, reservation, run, view := dispatchTestPacket(t, time.Now().UTC())
			a.Authority.Sponsorship = sponsorship.Revision + 1
			if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); !errors.Is(err, ErrAdmissionChanged) {
				t.Fatalf("packet admitted mismatched execution binding: %v", err)
			}
			if _, err := db.Assignment(ctx, a.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rejected packet persisted assignment: %v", err)
			}
		})
	}
}

func isAssignmentActive(err error) bool {
	return err != nil && (err == ErrAssignmentActive || strings.Contains(err.Error(), "unfinished assignment"))
}

func TestRecordDispatchPacketEnforcesLimits(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	t.Run("capacity", func(t *testing.T) {
		db := grantStoreFixture(t)
		seedDispatchLimits(t, db)
		if err := db.SaveCapacity(ctx, factory.Capacity{Revision: 1, UpdatedBy: 7, MaxConcurrentRuns: 1}); err != nil {
			t.Fatal(err)
		}
		a, r, run, view := dispatchTestPacket(t, now)
		a.Authority.Capacity = 2
		if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
			t.Fatal(err)
		}
		b, r2, run2, view2 := dispatchTestPacket(t, now)
		b.ID, b.Issue, b.Run, b.RunHistory = factory.NewID(), 4, run2.ID, []string{run2.ID}
		b.Acceptance = "d" + strings.Repeat("b", 24)
		b.Subject.Issue.Issue, b.Subject.Issue.Acceptance = 4, b.Acceptance
		b.AttemptRoot, b.PublicationAssignment = b.ID, b.ID
		b.Authority.Capacity = 2
		r2.AssignmentID = b.ID
		view2.Issue, view2.Attempt = 4, b.ID
		if err := recordDispatchTestPacket(t, ctx, db, b, r2, run2, view2); !errors.Is(err, ErrCapacityFull) {
			t.Fatalf("over-capacity packet accepted: %v", err)
		}
		if _, err := db.Assignment(ctx, b.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("refused packet left an assignment: %v", err)
		}
		settled := run
		settled.Outcome, settled.Reconciled, settled.Summary = factory.Succeeded, true, "completed"
		if err := db.SaveFactoryRun(ctx, settled); err != nil {
			t.Fatal(err)
		}
		finished, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		result := factory.Result{
			Status: "completed", Summary: "completed", Candidate: a.SourceCommit, Findings: []string{},
		}
		finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
		bound := factory.ResultFromHarness(a.ID, run.ID, result, now.Unix())
		finished.Result = &bound
		finished.FinishedUnix = now.Unix()
		if err := db.FinishAssignment(ctx, finished); err != nil {
			t.Fatal(err)
		}
		if err := db.ConsumeReservation(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		allowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
		if err != nil || !allowance.Active || allowance.Closed {
			t.Fatalf("settled reported attempt allowance = %+v, %v", allowance, err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, b, r2, run2, view2); !errors.Is(err, ErrCapacityFull) {
			t.Fatalf("packet bypassed appliance limit after session settled: %v", err)
		}
	})
	t.Run("allowance", func(t *testing.T) {
		db := grantStoreFixture(t)
		seedDispatchLimits(t, db)
		sponsorship, err := db.Sponsorship(ctx, 7, "conn")
		if err != nil {
			t.Fatal(err)
		}
		sponsorship.AllowanceMinutes = 30
		if err := db.SaveSponsorship(ctx, sponsorship); err != nil {
			t.Fatal(err)
		}
		a, r, run, view := dispatchTestPacket(t, now)
		a.Authority.Sponsorship = 2
		if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
			t.Fatal(err)
		}
		settled, err := db.FactoryRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		settled.Outcome, settled.Reconciled, settled.Summary = factory.Failed, true, "completed"
		if err := db.SaveFactoryRun(ctx, settled); err != nil {
			t.Fatal(err)
		}
		if err := db.ConsumeReservation(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if err := db.RecordRunUsage(ctx, factory.Usage{
			RunID: run.ID, Repository: a.Repository, Connection: a.Connection,
			Minutes: 30, StartedAt: now.Add(-30 * time.Minute), EndedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		finished, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
		bound := factory.ResultSynthesized(a.ID, run.ID, "failed", "completed", now.Unix())
		finished.Result = &bound
		finished.FinishedUnix = now.Unix()
		if err := db.FinishAssignment(ctx, finished); err != nil {
			t.Fatal(err)
		}
		b, r2, run2, view2 := dispatchTestPacket(t, now)
		b.ID, b.Issue, b.Run, b.RunHistory = factory.NewID(), 4, run2.ID, []string{run2.ID}
		b.Acceptance = "d" + strings.Repeat("b", 24)
		b.Subject.Issue.Issue, b.Subject.Issue.Acceptance = 4, b.Acceptance
		b.AttemptRoot, b.PublicationAssignment = b.ID, b.ID
		b.Authority.Sponsorship = 2
		r2.AssignmentID = b.ID
		view2.Issue, view2.Attempt = 4, b.ID
		if err := recordDispatchTestPacket(t, ctx, db, b, r2, run2, view2); !errors.Is(err, ErrAllowanceExhausted) {
			t.Fatalf("over-budget packet accepted: %v", err)
		}
	})
	t.Run("held session with missing run start fails closed", func(t *testing.T) {
		now := time.Now().UTC()
		db := dispatchStoreFixture(t)
		a, reservation, run, view := dispatchTestPacket(t, now)
		if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
			t.Fatal(err)
		}
		missingRun := factory.NewID()
		if _, err := db.db.ExecContext(ctx, `UPDATE factory_assignments SET run=$2,
			data=jsonb_set(data,'{run}',to_jsonb($2::text)) WHERE id=$1`, a.ID, missingRun); err != nil {
			t.Fatal(err)
		}
		second, r2, run2, view2 := dispatchTestPacket(t, now)
		second.ID, second.Issue, second.Run, second.RunHistory = factory.NewID(), 4, run2.ID, []string{run2.ID}
		second.Acceptance = "d" + strings.Repeat("b", 24)
		second.AttemptRoot, second.PublicationAssignment = second.ID, second.ID
		r2.AssignmentID = second.ID
		view2.Issue, view2.Attempt = 4, second.ID
		if err := recordDispatchTestPacket(t, ctx, db, second, r2, run2, view2); err == nil {
			t.Fatal("held session without a run start was ignored")
		}
		assertDispatchPacketAbsent(t, db, second)
	})
	t.Run("missing grants", func(t *testing.T) {
		db := grantStoreFixture(t)
		a, r, run, view := dispatchTestPacket(t, now)
		if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); !errors.Is(err, ErrAdmissionChanged) {
			t.Fatalf("unlimited packet accepted: %v", err)
		}
		assertDispatchPacketAbsent(t, db, a)
	})
}

func TestRecordRunUsageStoresOutwardMicrosecondProjections(t *testing.T) {
	ctx := context.Background()
	db := grantStoreFixture(t)
	started := time.Now().UTC().Truncate(time.Second).Add(123456789 * time.Nanosecond)
	ended := started.Add(121*time.Second + time.Nanosecond)
	usage := factory.Usage{
		RunID: factory.NewID(), Repository: 7, Connection: "conn", Minutes: 3,
		StartedAt: started, EndedAt: ended,
	}
	if err := db.RecordRunUsage(ctx, usage); err != nil {
		t.Fatal(err)
	}
	var storedStart, storedEnd time.Time
	if err := db.db.QueryRowContext(ctx, `SELECT started_at,ended_at FROM factory_usage WHERE run=$1`, usage.RunID).Scan(&storedStart, &storedEnd); err != nil {
		t.Fatal(err)
	}
	if !storedStart.Equal(floorUsageMicro(started)) || !storedEnd.Equal(ceilUsageMicro(ended)) {
		t.Fatalf("SQL interval projections = %s..%s, want %s..%s", storedStart, storedEnd, floorUsageMicro(started), ceilUsageMicro(ended))
	}
	canonical, err := db.RunUsage(ctx, usage.RunID)
	if err != nil || !canonical.StartedAt.Equal(started) || !canonical.EndedAt.Equal(ended) {
		t.Fatalf("canonical interval lost precision: %+v %v", canonical, err)
	}
}

func TestReportedAssignmentEnqueuesReadinessRootAtomically(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	t.Run("success", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		a, reservation, run, view := dispatchTestPacket(t, now)
		if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
			t.Fatal(err)
		}
		before := readinessGeneration(t, db)
		finished, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
		finished.FinishedUnix = now.Unix()
		result := factory.Result{Status: "completed", Summary: "completed", Candidate: a.SourceCommit, Findings: []string{}}
		bound := factory.ResultFromHarness(a.ID, run.ID, result, now.Unix())
		finished.Result = &bound
		if err = db.FinishAssignment(ctx, finished); err != nil {
			t.Fatal(err)
		}
		work, err := db.ReadinessWork(ctx, "root:7/3")
		if err != nil || work.Root != (factory.DependenceRef{Repository: 7, Issue: 3}) || readinessGeneration(t, db) != before+1 {
			t.Fatal("reported finish did not atomically enqueue root:", work, readinessGeneration(t, db), err)
		}
		if err = db.FinishAssignment(ctx, finished); !errors.Is(err, ErrNotFound) || readinessGeneration(t, db) != before+1 {
			t.Fatal("stale terminal replay advanced generation:", err, readinessGeneration(t, db))
		}
	})
	t.Run("source-capacity-rolls-back-finish", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		a, reservation, run, view := dispatchTestPacket(t, now)
		if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
			t.Fatal(err)
		}
		// Consume the admission-created notification before filling the
		// table: this case requires finish to allocate a new source header.
		work, err := db.BeginReadinessWork(ctx, "root:7/3", now)
		if err != nil {
			t.Fatal(err)
		}
		node, err := db.NextReadinessWorkNode(ctx, work.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.CheckpointReadinessAssessment(ctx, work, node, true); err != nil {
			t.Fatal(err)
		}
		if complete, err := db.CompleteReadinessWork(ctx, work, now); err != nil || !complete {
			t.Fatal("consume fixture admission work:", complete, err)
		}
		fillReadinessSourceHeaders(t, db, "capacity:finish", maxReadinessSources)
		finished, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
		finished.FinishedUnix = now.Unix()
		result := factory.Result{Status: "completed", Summary: "completed", Candidate: a.SourceCommit, Findings: []string{}}
		bound := factory.ResultFromHarness(a.ID, run.ID, result, now.Unix())
		finished.Result = &bound
		before := readinessGeneration(t, db)
		if err = db.FinishAssignment(ctx, finished); !errors.Is(err, ErrReadinessCapacity) {
			t.Fatal("full source table must refuse finish:", err)
		}
		stored, err := db.Assignment(ctx, a.ID)
		if err != nil || stored.Stage != factory.AssignmentAssigned || readinessGeneration(t, db) != before {
			t.Fatal("refused finish partially committed:", stored.Stage, readinessGeneration(t, db), err)
		}
	})
}
