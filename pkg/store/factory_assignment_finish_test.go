package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// NATIVE107 bounded serving guard: FinishAssignment admits only the five
// terminal changes (Stage, Outcome, Reason, FinishedUnix, Result). Every
// other recorded input is immutable through finish.

// finishGuardBaseAssignment builds one assigned issue investigation root
// for pure helper tests. It needs no database and never validates: the
// helper under test is structural, not admission.
func finishGuardBaseAssignment() factory.Assignment {
	assignmentID := factory.NewID()
	runID := factory.NewID()
	return factory.Assignment{
		Authority: factory.AuthorityRef{
			Policy: 1, Operator: 1, Capacity: 1, Sponsorship: 1, Environment: 1,
			SponsorshipConnection: "conn",
			RequirementsID:        "d" + strings.Repeat("c", 24), ApprovalID: "d" + strings.Repeat("e", 24),
		},
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 3, Acceptance: "d" + strings.Repeat("a", 24)},
		},
		Prompt:     []byte("finish guard probe prompt"),
		RunHistory: []string{runID},
		ID:         assignmentID, AttemptRoot: assignmentID, PublicationAssignment: assignmentID,
		ProjectID: dispatchTestProjectID(), Role: project.RoleCoder, Phase: project.ExecutionInvestigate,
		Acceptance: "d" + strings.Repeat("a", 24), Preparation: "f" + strings.Repeat("b", 24),
		Harness: "codex-0.157.1", HarnessVers: "0.157.1", Model: "test",
		Connection: "conn", SourceCommit: strings.Repeat("c", 40),
		PromptSHA: strings.Repeat("e", 64),
		Run:       runID, Stage: factory.AssignmentAssigned, Attempts: 1,
		Repository: 7, Issue: 3, Revision: 0, NativeRev: 5,
		CreatedUnix: 1234567890, ActorID: 7,
	}
}

// finishGuardFinishedVariant marks base finished the way the serving caller
// does: exactly the five terminal fields change.
func finishGuardFinishedVariant(base factory.Assignment, now int64) factory.Assignment {
	finished := base
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
	finished.FinishedUnix = now
	result := factory.ResultSynthesized(finished.ID, finished.Run, "failed", "run failed", now)
	finished.Result = &result
	return finished
}

func TestSameAssignmentFinishInputAllowsFiveTerminalChanges(t *testing.T) {
	now := time.Now().UTC().Unix()
	t.Run("all five together", func(t *testing.T) {
		base := finishGuardBaseAssignment()
		if !sameAssignmentFinishInput(base, finishGuardFinishedVariant(base, now)) {
			t.Fatal("five legal terminal changes refused")
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*factory.Assignment)
	}{
		{"stage", func(a *factory.Assignment) { a.Stage = factory.AssignmentFinished }},
		{"outcome", func(a *factory.Assignment) { a.Outcome = factory.Failed }},
		{"reason", func(a *factory.Assignment) { a.Reason = factory.AssignReasonRunFailed }},
		{"finished unix", func(a *factory.Assignment) { a.FinishedUnix = now }},
		{"result", func(a *factory.Assignment) {
			result := factory.ResultSynthesized(a.ID, a.Run, "failed", "run failed", now)
			a.Result = &result
		}},
		{"differing result contents", func(a *factory.Assignment) {
			result := factory.ResultFromHarness(a.ID, a.Run, factory.Result{
				Status: "completed", Summary: "completed",
				Candidate: strings.Repeat("c", 40), Findings: []string{},
			}, now)
			a.Result = &result
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := finishGuardBaseAssignment()
			next := base
			tc.change(&next)
			if !sameAssignmentFinishInput(base, next) {
				t.Fatalf("legal terminal change %q refused", tc.name)
			}
		})
	}
}

func TestSameAssignmentFinishInputNestedEqualCopies(t *testing.T) {
	now := time.Now().UTC().Unix()
	base := finishGuardBaseAssignment()
	// Independently allocated nested values must compare equal: a JSON
	// round trip reallocates every pointer, slice and map.
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var current factory.Assignment
	if err = json.Unmarshal(raw, &current); err != nil {
		t.Fatal(err)
	}
	var next factory.Assignment
	if err = json.Unmarshal(raw, &next); err != nil {
		t.Fatal(err)
	}
	next = finishGuardFinishedVariant(next, now)
	if current.Subject.Issue == base.Subject.Issue || &current.Prompt[0] == &base.Prompt[0] {
		t.Fatal("round trip did not reallocate nested values")
	}
	if !sameAssignmentFinishInput(current, next) {
		t.Fatal("nested equal-valued copies refused")
	}
	// Manually reallocated copies compare equal as well.
	manual := base
	manual.Prompt = append([]byte(nil), base.Prompt...)
	manual.RunHistory = append([]string(nil), base.RunHistory...)
	issue := *base.Subject.Issue
	manual.Subject.Issue = &issue
	if !sameAssignmentFinishInput(base, finishGuardFinishedVariant(manual, now)) {
		t.Fatal("manually reallocated equal copies refused")
	}
}

func TestSameAssignmentFinishInputRefusesImmutableMutations(t *testing.T) {
	now := time.Now().UTC().Unix()
	altAcceptance := "d" + strings.Repeat("b", 24)
	for _, tc := range []struct {
		name   string
		change func(*factory.Assignment)
	}{
		{"id", func(a *factory.Assignment) { a.ID = factory.NewID() }},
		{"attempt root", func(a *factory.Assignment) { a.AttemptRoot = factory.NewID() }},
		{"publication assignment", func(a *factory.Assignment) { a.PublicationAssignment = factory.NewID() }},
		{"parent assignment", func(a *factory.Assignment) { a.ParentAssignment = factory.NewID() }},
		{"project", func(a *factory.Assignment) { a.ProjectID = "p" + strings.Repeat("e", 24) }},
		{"role", func(a *factory.Assignment) { a.Role = project.RoleReviewer }},
		{"phase", func(a *factory.Assignment) { a.Phase = project.ExecutionImplement }},
		{"acceptance", func(a *factory.Assignment) { a.Acceptance = altAcceptance }},
		{"subject acceptance only", func(a *factory.Assignment) {
			issue := *a.Subject.Issue
			issue.Acceptance = altAcceptance
			a.Subject.Issue = &issue
		}},
		{"acceptance and subject acceptance together", func(a *factory.Assignment) {
			issue := *a.Subject.Issue
			issue.Acceptance = altAcceptance
			a.Subject.Issue = &issue
			a.Acceptance = altAcceptance
		}},
		{"subject issue", func(a *factory.Assignment) {
			issue := *a.Subject.Issue
			issue.Issue = 4
			a.Subject.Issue = &issue
		}},
		{"subject kind", func(a *factory.Assignment) { a.Subject.Kind = factory.SubjectIntent }},
		{"preparation", func(a *factory.Assignment) { a.Preparation = "f" + strings.Repeat("c", 24) }},
		{"harness", func(a *factory.Assignment) { a.Harness = "other-0.0.1" }},
		{"harness version", func(a *factory.Assignment) { a.HarnessVers = "0.0.1" }},
		{"model", func(a *factory.Assignment) { a.Model = "other" }},
		{"connection", func(a *factory.Assignment) { a.Connection = "other" }},
		{"source commit", func(a *factory.Assignment) { a.SourceCommit = strings.Repeat("d", 40) }},
		{"prompt sha", func(a *factory.Assignment) { a.PromptSHA = strings.Repeat("f", 64) }},
		{"prompt bytes", func(a *factory.Assignment) { a.Prompt = append(a.Prompt, 'x') }},
		{"run", func(a *factory.Assignment) { a.Run = factory.NewID() }},
		{"run history", func(a *factory.Assignment) { a.RunHistory = []string{a.RunHistory[0], factory.NewID()} }},
		{"repository", func(a *factory.Assignment) { a.Repository = 8 }},
		{"issue", func(a *factory.Assignment) { a.Issue = 4 }},
		{"revision", func(a *factory.Assignment) { a.Revision++ }},
		{"native revision", func(a *factory.Assignment) { a.NativeRev++ }},
		{"attempts", func(a *factory.Assignment) { a.Attempts++ }},
		{"created unix", func(a *factory.Assignment) { a.CreatedUnix++ }},
		{"actor", func(a *factory.Assignment) { a.ActorID++ }},
		{"authority", func(a *factory.Assignment) { a.Authority.Policy++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := finishGuardBaseAssignment()
			next := finishGuardFinishedVariant(base, now)
			tc.change(&next)
			if sameAssignmentFinishInput(base, next) {
				t.Fatalf("immutable mutation %q allowed", tc.name)
			}
		})
	}
	t.Run("same subject key with different acceptance", func(t *testing.T) {
		base := finishGuardBaseAssignment()
		next := finishGuardFinishedVariant(base, now)
		issue := *next.Subject.Issue
		issue.Acceptance = altAcceptance
		next.Subject.Issue = &issue
		next.Acceptance = altAcceptance
		if keyBefore, keyAfter := base.Subject.SubjectKey(base.Repository), next.Subject.SubjectKey(next.Repository); keyBefore != keyAfter {
			t.Fatalf("subject key moved %q -> %q, want the same-key case", keyBefore, keyAfter)
		}
		if sameAssignmentFinishInput(base, next) {
			t.Fatal("same-key changed acceptance allowed")
		}
	})
}

func TestSameAssignmentFinishInputKeepsInputsUnmutated(t *testing.T) {
	base := finishGuardBaseAssignment()
	next := finishGuardFinishedVariant(base, time.Now().UTC().Unix())
	beforeCurrent, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	beforeNext, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	resultPtr := next.Result
	if !sameAssignmentFinishInput(base, next) {
		t.Fatal("five legal terminal changes refused")
	}
	afterCurrent, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	afterNext, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeCurrent) != string(afterCurrent) || string(beforeNext) != string(afterNext) {
		t.Fatal("helper mutated its inputs")
	}
	if next.Result != resultPtr {
		t.Fatal("helper replaced the shared result pointer")
	}
}

// TestFinishAssignmentRefusesChangedInputs replays VALID forged finishes
// against one shared canonical issue packet: a changed source commit and
// a same-key changed acceptance must both report ErrNotFound and leave
// the exact original row, readiness generation and allowance unchanged.
// The original inputs then finish, and the replay refuses.
func TestFinishAssignmentRefusesChangedInputs(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatalf("packet refused: %v", err)
	}
	original, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	generationBefore := readinessGeneration(t, db)
	allowanceBefore, allowanceErrBefore := db.AttemptAllowance(ctx, original.Repository, original.Issue)
	assertFinishRefusalLeavesAllUnchanged := func(name string) {
		t.Helper()
		stored, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatalf("%s: assignment unreadable: %v", name, err)
		}
		if !reflect.DeepEqual(stored, original) {
			t.Fatalf("%s: stored row changed: %+v", name, stored)
		}
		if generation := readinessGeneration(t, db); generation != generationBefore {
			t.Fatalf("%s: readiness generation %d, want %d", name, generation, generationBefore)
		}
		allowance, allowanceErr := db.AttemptAllowance(ctx, original.Repository, original.Issue)
		if (allowanceErr == nil) != (allowanceErrBefore == nil) || !reflect.DeepEqual(allowance, allowanceBefore) {
			t.Fatalf("%s: allowance changed: %+v %v", name, allowance, allowanceErr)
		}
	}
	finishWithTerminal := func(base factory.Assignment) factory.Assignment {
		t.Helper()
		finished := base
		finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
		finished.FinishedUnix = now.Unix()
		result := factory.ResultSynthesized(finished.ID, finished.Run, "failed", "run failed", now.Unix())
		finished.Result = &result
		return finished
	}
	t.Run("changed source", func(t *testing.T) {
		forged := finishWithTerminal(original)
		forged.SourceCommit = strings.Repeat("d", 40)
		if err := forged.Validate(); err != nil {
			t.Fatalf("forged changed-source request invalid, masking the store comparison: %v", err)
		}
		if err := db.FinishAssignment(ctx, forged); !errors.Is(err, ErrNotFound) {
			t.Fatalf("changed-source finish error = %v, want %v", err, ErrNotFound)
		}
		assertFinishRefusalLeavesAllUnchanged("changed source")
	})
	t.Run("same-key changed acceptance", func(t *testing.T) {
		forged := finishWithTerminal(original)
		forged.Acceptance = "d" + strings.Repeat("b", 24)
		// Fresh subject: the shared-issue fixture must not mutate the
		// original's subject through the copied pointer.
		forged.Subject.Issue = &factory.IssueSubject{Issue: forged.Subject.Issue.Issue, Acceptance: forged.Acceptance}
		if err := forged.Validate(); err != nil {
			t.Fatalf("forged changed-acceptance request invalid, masking the store comparison: %v", err)
		}
		if err := db.FinishAssignment(ctx, forged); !errors.Is(err, ErrNotFound) {
			t.Fatalf("changed-acceptance finish error = %v, want %v", err, ErrNotFound)
		}
		assertFinishRefusalLeavesAllUnchanged("changed acceptance")
	})
	finished := finishWithTerminal(original)
	if err := finished.Validate(); err != nil {
		t.Fatalf("original finish invalid: %v", err)
	}
	if err := db.FinishAssignment(ctx, finished); err != nil {
		t.Fatalf("original finish refused: %v", err)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil || stored.Stage != factory.AssignmentFinished {
		t.Fatalf("finished assignment unreadable: %+v %v", stored, err)
	}
	if err := db.FinishAssignment(ctx, finished); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terminal replay error = %v, want %v", err, ErrNotFound)
	}
}

// TestFinishAssignmentInvestigateSubjectAcceptanceRefused replays a VALID
// forged finish with a same-key changed acceptance (top-level and
// Subject.Issue.Acceptance together) against a subject-carrying
// investigate root. The refusal must leave the assigned row unchanged,
// and the legitimate investigate finish still commits.
func TestFinishAssignmentInvestigateSubjectAcceptanceRefused(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	root, reservation, run, view := phaseTestInvestigateRoot(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, root, reservation, run, view); err != nil {
		t.Fatalf("investigate root refused: %v", err)
	}
	original, err := db.Assignment(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	forged := original
	forged.Stage, forged.Outcome, forged.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
	forged.FinishedUnix = now.Unix()
	result := factory.ResultSynthesized(forged.ID, forged.Run, "failed", "run failed", now.Unix())
	forged.Result = &result
	alt := "d" + strings.Repeat("b", 24)
	// Reallocate the nested subject before mutating: forged must not
	// alias original's pointers, or the readback comparison is void.
	issue := *forged.Subject.Issue
	issue.Acceptance = alt
	forged.Subject.Issue = &issue
	forged.Acceptance = alt
	if keyBefore, keyAfter := original.Subject.SubjectKey(original.Repository), forged.Subject.SubjectKey(forged.Repository); keyBefore != keyAfter {
		t.Fatalf("subject key moved %q -> %q, want the same-key case", keyBefore, keyAfter)
	}
	if err := forged.Validate(); err != nil {
		t.Fatalf("forged subject-acceptance request invalid, masking the store comparison: %v", err)
	}
	if err := db.FinishAssignment(ctx, forged); !errors.Is(err, ErrNotFound) {
		t.Fatalf("subject-acceptance finish error = %v, want %v", err, ErrNotFound)
	}
	stored, err := db.Assignment(ctx, root.ID)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("refused subject-acceptance finish changed the row: %+v %v", stored, err)
	}
	finishInvestigateRoot(t, ctx, db, root, run, now)
}
