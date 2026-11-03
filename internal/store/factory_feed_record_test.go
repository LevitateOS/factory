package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestRecordAuthorAndStatusPure(t *testing.T) {
	if actor, author := RecordAuthor("repair", "soda-coder"); actor != "strategist" || author != "Strategist" {
		t.Fatalf("repair author = %q/%q", actor, author)
	}
	if actor, author := RecordAuthor("verify", "soda-reviewer"); actor != "test_pilot" || author != "Test Pilot" {
		t.Fatalf("verify author = %q/%q", actor, author)
	}
	if actor, author := RecordAuthor("", "coder"); actor != "coder" || author != "" {
		t.Fatalf("pre-phase author = %q/%q", actor, author)
	}
	if actor, author := RecordAuthor("mystery", ""); actor != "" || author != "" {
		t.Fatalf("unknown author = %q/%q", actor, author)
	}
	got := RecordStatus("implement", "finished", "succeeded", "", []string{"published", "merged", "completed"})
	if got != "implement:finished:succeeded>published>merged>completed" {
		t.Fatalf("status = %q", got)
	}
	// Over-bound compositions shed extra segments, then verdict, then
	// outcome, instead of exceeding the wire status bound.
	long := RecordStatus("investigate", "finished", "needs-human", "request-changes",
		[]string{"findings:123456", "a-very-long-recorded-stage-name", "another-long-stage"})
	if len(long) > feedRecordStatusLimit {
		t.Fatalf("status over bound: %q", long)
	}
	if long != "investigate:finished:needs-human:request-changes" {
		t.Fatalf("shed status = %q", long)
	}
	summary := TruncateFeedSummary(strings.Repeat("é", FeedSummaryLimit+10))
	if len([]rune(summary)) != FeedSummaryLimit {
		t.Fatalf("summary runes = %d", len([]rune(summary)))
	}
}

// insertFeedIntegration stores one publication/merge/completion chain
// bound to an assignment through the canonical identity links:
// publications.assignment, merges.publication = publication data id,
// completions.publication = publication data id.
func insertFeedIntegration(t *testing.T, db *sql.DB, assignment, publication string, pr int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data)
		VALUES($1,7,3,'run','published',1,$2)`, assignment, `{"id":"`+publication+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data)
		VALUES($1,7,3,$2,'merged',1,'{}')`, publication, pr); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data)
		VALUES(7,1,$1,$2,'occ-1','{}')`, strings.Repeat("e", 32), publication); err != nil {
		t.Fatal(err)
	}
}

func TestFeedRecordDetailsEnrichesPhasesAndIntegration(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)
	candidate := strings.Repeat("c", 40)

	impl := journeyAssignment(strings.Repeat("1", 32), strings.Repeat("1", 32), acceptance, 7, 3, true)
	impl.Phase = "implement"
	impl.Outcome = factory.Succeeded
	impl.FinishedUnix = 1700000600
	impl.Result = &factory.AssignmentResult{
		AssignmentID: impl.ID, RunID: strings.Repeat("a", 32), Phase: "implement",
		Status: "completed", Summary: "result summary", Candidate: candidate, RecordedUnix: 1700000600,
		PhaseResult: &factory.PhaseResult{Phase: "implement", Status: "completed", Summary: "built the candidate", Version: factory.PhaseResultVersion},
	}
	insertJourneyAssignment(t, raw, impl, factory.AssignmentFinished, 1)
	insertFeedIntegration(t, raw, impl.ID, strings.Repeat("9", 32), 42)

	review := journeyAssignment(strings.Repeat("2", 32), strings.Repeat("1", 32), acceptance, 7, 3, false)
	review.Phase = "review"
	review.Outcome = factory.Succeeded
	review.FinishedUnix = 1700001200
	review.Result = &factory.AssignmentResult{
		AssignmentID: review.ID, RunID: strings.Repeat("b", 32), Phase: "review",
		Status: "completed", Findings: []string{"finding one", "finding two"}, RecordedUnix: 1700001200,
		Review: &factory.ReviewReport{Verdict: "request-changes", Summary: "two defects", Body: "details"},
	}
	insertJourneyAssignment(t, raw, review, factory.AssignmentFinished, 1)

	plan := journeyAssignment(strings.Repeat("3", 32), strings.Repeat("3", 32), acceptance, 7, 4, true)
	plan.Phase = "investigate"
	insertJourneyAssignment(t, raw, plan, factory.AssignmentAssigned, 1)

	legacy := journeyAssignment(strings.Repeat("4", 32), strings.Repeat("4", 32), acceptance, 7, 5, true)
	insertJourneyAssignment(t, raw, legacy, factory.AssignmentAssigned, 1)

	verify := journeyAssignment(strings.Repeat("5", 32), strings.Repeat("5", 32), acceptance, 7, 3, true)
	verify.Phase = "verify"
	verify.Outcome = factory.Failed
	verify.FinishedUnix = 1700001800
	verify.Result = &factory.AssignmentResult{
		AssignmentID: verify.ID, RunID: strings.Repeat("d", 32), Phase: "verify",
		Status: "completed", Summary: "verification ran", RecordedUnix: 1700001800,
		PhaseResult: &factory.PhaseResult{
			Phase: "verify", Status: "completed", Summary: "one outcome failed", Version: factory.PhaseResultVersion,
			Output: &factory.PhaseOutput{Verification: &factory.AppVerificationReport{
				DeliveryID: strings.Repeat("f", 32), Outcome: "failed",
				CheckReceipts: []factory.VerificationCheckReceipt{{}, {}, {}},
				Failures:      []factory.CheckFailure{{}},
			}},
		},
	}
	insertJourneyAssignment(t, raw, verify, factory.AssignmentFinished, 1)

	details, err := s.FeedRecordDetails(ctx, []string{impl.ID, review.ID, plan.ID, legacy.ID, verify.ID, strings.Repeat("0", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 5 {
		t.Fatalf("details = %d rows: %+v", len(details), details)
	}
	got := details[impl.ID]
	if got.Actor != "builder" || got.Author != "Builder" || got.At != 1700000600 ||
		got.Status != "implement:finished:succeeded>published>merged>completed" ||
		got.Issue != 3 || got.PR != 42 || got.Ref != candidate || got.Summary != "built the candidate" {
		t.Fatalf("implement detail: %+v", got)
	}
	got = details[review.ID]
	if got.Actor != "inspector" || got.Author != "Inspector" || got.At != 1700001200 ||
		got.Status != "review:finished:succeeded:request-changes>findings:2" ||
		got.Summary != "two defects" || got.PR != 0 || got.Ref != "" {
		t.Fatalf("review detail: %+v", got)
	}
	got = details[plan.ID]
	if got.Actor != "architect" || got.Author != "Architect" || got.At != 1700000000 ||
		got.Status != "investigate:assigned" || got.Summary != "" || got.Issue != 4 {
		t.Fatalf("plan detail: %+v", got)
	}
	got = details[legacy.ID]
	if got.Actor != "coder" || got.Author != "" || got.Status != "assigned" {
		t.Fatalf("legacy detail: %+v", got)
	}
	got = details[verify.ID]
	if got.Actor != "test_pilot" || got.Author != "Test Pilot" ||
		got.Status != "verify:finished:failed:failed>checks:3>failures:1" ||
		got.Summary != "one outcome failed" {
		t.Fatalf("verify detail: %+v", got)
	}
	empty, err := s.FeedRecordDetails(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty details = %+v %v", empty, err)
	}
}

func feedPayloadOf(t *testing.T, result factory.AssignmentResult) *factory.FeedPayload {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return feedResultPayload(raw)
}

func TestFeedResultPayloadPerPhase(t *testing.T) {
	key := strings.Repeat("a", 32)
	candidate := strings.Repeat("c", 40)
	finding := factory.Finding{
		Key: key, Severity: "major", Defect: "cross-team read",
		Evidence: "repro steps", RequiredOutcome: "deny other teams", FixKind: "code", Actionable: true,
		Location: &factory.FindingLocation{Path: "api.go", Side: "new", Line: 12},
	}
	phase := func(output *factory.PhaseOutput) factory.AssignmentResult {
		return factory.AssignmentResult{PhaseResult: &factory.PhaseResult{Output: output}}
	}

	payload := feedPayloadOf(t, phase(&factory.PhaseOutput{Plan: &factory.InvestigationPlan{
		Approach: "guard in service", Findings: []string{"fact"}, Unresolved: []string{"gap"},
		Tasks: []factory.PlanTask{
			{ID: "a", Objective: "first", Lane: "l1"},
			{ID: "b", Objective: "second", Lane: "l1", DependsOn: []string{"a"}, CheckIDs: []string{"c1"}},
		},
		Lanes:  []factory.PlanLane{{ID: "l1", TaskIDs: []string{"a", "b"}}},
		Checks: []factory.CheckSpec{{ID: "c1", Argv: []string{"go", "test"}, Required: true}},
	}}))
	if payload == nil || payload.Plan == nil || payload.Plan.TasksTotal != 2 ||
		len(payload.Plan.Tasks) != 2 || payload.Plan.Tasks[1].DependsOn[0] != "a" ||
		payload.Plan.ChecksTotal != 1 || !payload.Plan.Checks[0].Required ||
		payload.Plan.Unresolved[0] != "gap" {
		t.Fatalf("plan payload: %+v", payload)
	}

	payload = feedPayloadOf(t, phase(&factory.PhaseOutput{Review: &factory.ReviewCycleReport{
		CycleID: strings.Repeat("b", 32), NewFindings: []factory.Finding{finding},
		Assessments: []factory.FindingAssessment{{Key: key, Status: "still_open", Evidence: "repro"}},
	}}))
	if payload == nil || payload.Review == nil || payload.Review.NewFindingsTotal != 1 ||
		payload.Review.NewFindings[0].Defect != "cross-team read" ||
		payload.Review.NewFindings[0].Location == nil || payload.Review.NewFindings[0].Location.Line != 12 ||
		payload.Review.Assessments[0].Status != "still_open" {
		t.Fatalf("review payload: %+v", payload)
	}

	payload = feedPayloadOf(t, phase(&factory.PhaseOutput{Correction: &factory.CorrectionReport{
		NewCandidate: candidate, Findings: []factory.Finding{finding},
		Dispositions: []factory.CorrectionDisposition{{Key: key, Disposition: "fixed"}},
		Evidence:     []factory.CorrectionFixEvidence{{Key: key, ChangedPaths: []string{"api.go"}, Commits: []string{candidate}}},
	}}))
	if payload == nil || payload.Correction == nil || payload.Correction.DispositionsTotal != 1 ||
		payload.Correction.Dispositions[0].Disposition != "fixed" ||
		payload.Correction.Dispositions[0].ChangedPaths[0] != "api.go" {
		t.Fatalf("correction payload: %+v", payload)
	}

	payload = feedPayloadOf(t, phase(&factory.PhaseOutput{Reassessment: &factory.ReassessmentReport{
		Summary: "one change", RetainedIssueIndexes: []int64{3},
		IntentChanges: []factory.ReassessmentIntentChange{{
			IntentID: "intent", Reason: "cycle",
			Tasks: []factory.IntentTask{{Key: "t1", Objective: "break cycle", DependsOn: []string{"t0"}}},
		}},
		Attention: []factory.ReassessmentAttentionEntry{{IssueIndex: 9, Reason: "waiting"}},
		Coverage:  []factory.ReassessmentCoverage{{IntentID: "intent", OutcomeID: "out", TaskKeys: []string{"t1"}}},
	}}))
	if payload == nil || payload.Reassessment == nil || payload.Reassessment.ChangesTotal != 1 ||
		payload.Reassessment.Changes[0].Tasks[0].DependsOn[0] != "t0" ||
		payload.Reassessment.RetainedIssues[0] != 3 || payload.Reassessment.Attention[0].Issue != 9 {
		t.Fatalf("reassessment payload: %+v", payload)
	}

	payload = feedPayloadOf(t, phase(&factory.PhaseOutput{Verification: &factory.AppVerificationReport{
		DeliveryID: strings.Repeat("d", 32), Outcome: "failed",
		CheckReceipts: []factory.VerificationCheckReceipt{
			{CheckID: "k1", Outcome: "passed"}, {CheckID: "k2", Outcome: "failed"},
		},
		Failures: []factory.CheckFailure{{CheckRef: "k2", Detail: "boom"}},
		Coverage: []factory.ReassessmentCoverage{{IntentID: "intent", OutcomeID: "out"}},
	}}))
	if payload == nil || payload.Verification == nil || payload.Verification.ChecksTotal != 2 ||
		payload.Verification.Checks[1].Outcome != "failed" ||
		payload.Verification.Failures[0].Check != "k2" || payload.Verification.CoverageTotal != 1 {
		t.Fatalf("verification payload: %+v", payload)
	}

	// Bounded previews keep the honest total; unrecorded content stays absent.
	many := make([]string, feedPayloadCap+6)
	payload = feedPayloadOf(t, factory.AssignmentResult{Findings: many})
	if payload == nil || payload.Review == nil || payload.Review.FindingsTotal != feedPayloadCap+6 ||
		len(payload.Review.Findings) != feedPayloadCap {
		t.Fatalf("capped payload: %+v", payload)
	}
	if got := feedPayloadOf(t, factory.AssignmentResult{}); got != nil {
		t.Fatalf("empty result payload: %+v", got)
	}
	if got := feedResultPayload([]byte("{broken")); got != nil {
		t.Fatalf("broken result payload: %+v", got)
	}
}

func TestFeedRecordDetailsTaskStates(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)
	impl := journeyAssignment(strings.Repeat("6", 32), strings.Repeat("6", 32), acceptance, 7, 3, true)
	impl.Phase = "implement"
	impl.Outcome = factory.Succeeded
	impl.Result = &factory.AssignmentResult{
		AssignmentID: impl.ID, RunID: strings.Repeat("a", 32), Phase: "implement",
		Status: "completed", RecordedUnix: 1700000600,
		PhaseResult: &factory.PhaseResult{
			Phase: "implement", Status: "completed", Version: factory.PhaseResultVersion,
			Output: &factory.PhaseOutput{Implementation: &factory.ImplementationReceipt{Candidate: strings.Repeat("c", 40)}},
		},
	}
	insertJourneyAssignment(t, raw, impl, factory.AssignmentFinished, 1)
	planID := strings.Repeat("8", 32)
	if _, err := raw.Exec(`INSERT INTO factory_plan_heads(attempt_root,plan_id,generation) VALUES($1,$2,1)`,
		impl.AttemptRoot, planID); err != nil {
		t.Fatal(err)
	}
	for _, task := range [][2]string{{"build", "verified"}, {"check", "running"}} {
		if _, err := raw.Exec(`INSERT INTO factory_plan_tasks(attempt_root,plan_id,task_id,revision,state,data)
			VALUES($1,$2,$3,1,$4,'{}')`, impl.AttemptRoot, planID, task[0], task[1]); err != nil {
			t.Fatal(err)
		}
	}
	details, err := s.FeedRecordDetails(ctx, []string{impl.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := details[impl.ID]
	if got.Payload == nil || got.Payload.Implementation == nil ||
		got.Payload.Implementation.Candidate != strings.Repeat("c", 40) ||
		got.Payload.Tasks == nil || got.Payload.Tasks.Total != 2 ||
		got.Payload.Tasks.Tasks[0].ID != "build" || got.Payload.Tasks.Tasks[0].State != "verified" {
		t.Fatalf("task state detail: %+v", got)
	}
}
