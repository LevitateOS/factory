package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func contributionOwnerFixture(t *testing.T, s *Store) *ContributionOwner {
	t.Helper()
	owner, err := s.OwnContributions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner
}

func TestMCPInvitedContributionUsesCurrentSourceOnce(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	owner := contributionOwnerFixture(t, s)
	cap := testMCPCapabilityRow()
	assignment := journeyAssignment(cap.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), cap.Repository, 42, true)
	assignment.Run = cap.RunID
	assignment.AcceptedContext = &factory.AcceptedContext{Title: "Equipment approval policy", Body: "Implement protected manager and Finance approvals without self-approval."}
	db := journeySQL(t, dsn)
	insertJourneyAssignment(t, db, assignment, "assigned", 1)
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,'{}')`, cap.RunID); err != nil {
		t.Fatal(err)
	}
	cap = activateMCPCapability(t, ctx, s, cap)
	post, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: cap.Repository, Body: "The Finance check can be bypassed through the direct delivery operation. Please inspect the authorization boundary.", IdempotencyKey: "role-source"}, "7", "Maya", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	// A native completion reassessment has no accepted issue/planning scope.
	// Newer posts in that project must not fill discovery's bounded window and
	// starve an older source with a genuinely eligible contribution role.
	unscoped := testMCPCapabilityRow()
	unscoped.Repository, unscoped.RunID = 8, "run-reassessment"
	unscoped.Profile = factory.MCPProfileStrategist
	reassessment := journeyAssignment(unscoped.AssignmentID, unscoped.AssignmentID, "", 8, 0, true)
	reassessment.Run, reassessment.Phase, reassessment.Role = unscoped.RunID, "reassess", "soda-coder"
	reassessment.PublicationAssignment = ""
	reassessment.Subject = factory.ExecutionSubject{Kind: factory.SubjectCompletion, Completion: &factory.CompletionSubject{CompletionID: factory.NewID(), CycleID: factory.NewID(), InventoryDigest: strings.Repeat("b", 64)}}
	unscoped.Subject = reassessment.Subject.SubjectKey(8)
	insertJourneyAssignment(t, db, reassessment, "assigned", 1)
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,'{}')`, unscoped.RunID); err != nil {
		t.Fatal(err)
	}
	activateMCPCapability(t, ctx, s, unscoped)
	for range 8 {
		if _, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 8, Body: "This project currently has no role with a bounded discussion scope.", IdempotencyKey: "unscoped-" + factory.NewID()}, "7", "Maya", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if sources, err := s.ContributionSources(ctx); err != nil || len(sources) != 1 || sources[0] != post {
		t.Fatalf("discovery lost eligible source behind unscoped roles: %v %v", sources, err)
	}
	review, err := s.ContributionContext(ctx, post)
	if err != nil || len(review.Roles) != 1 || !strings.Contains(review.Roles[0].Scope, "protected manager") {
		t.Fatalf("eligible scope missing: %+v %v", review, err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || claimed {
		t.Fatalf("duplicate claim: %v %v", claimed, err)
	}
	if sources, err := s.ContributionSources(ctx); err != nil || len(sources) != 0 {
		t.Fatalf("claimed source or assessment rediscovered: %v %v", sources, err)
	}
	for _, question := range factory.ContributionQuestions {
		review.Calls = append(review.Calls, factory.ContributionCall{Assignment: cap.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "fixture-jev", Selected: "contribute", Probabilities: map[string]float64{"contribute": .95, "no_contribution": .05}, Confidence: .9}})
	}
	factory.SelectContribution(&review)
	if err := owner.SettleContributionReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	invitationID := factory.ContributionReviewID(post, 0)
	inbox, err := s.MCPInbox(ctx, cap, "")
	if err != nil {
		t.Fatal(err)
	}
	foundInvite := false
	for _, item := range inbox.Items {
		if item.ID == invitationID || item.ID == post {
			foundInvite = true
		}
	}
	if !foundInvite {
		t.Fatalf("post invitation missing from inbox: %+v", inbox.Items)
	}
	page, err := s.MCPDiscussion(ctx, cap, post, "")
	if err != nil || len(page.Items) < 1 || page.Items[len(page.Items)-1].ID != post {
		t.Fatalf("invited source absent: %+v %v", page, err)
	}
	var postInvite *factory.MCPFeedMessage
	for i := range page.Items {
		if page.Items[i].Contribution != nil && page.Items[i].Contribution.Source == post {
			postInvite = &page.Items[i]
		}
	}
	if postInvite == nil || !strings.Contains(postInvite.Contribution.Body, "Finance check") {
		t.Fatalf("post discussion hides its authorized source body: %+v", page.Items)
	}
	invitePage, err := s.MCPDiscussion(ctx, cap, invitationID, "")
	if err != nil || len(invitePage.Items) == 0 {
		t.Fatalf("post invitation unreadable: %v %+v", err, invitePage)
	}
	inviteFound := false
	for _, item := range invitePage.Items {
		if item.ID == invitationID && item.Contribution != nil && strings.Contains(item.Contribution.Body, "Finance check") {
			inviteFound = true
		}
	}
	if !inviteFound {
		t.Fatalf("post invitation hides its source snapshot: %+v", invitePage.Items)
	}
	// The reply below carries the exact bounded source text the discussion
	// exposed through the inbox-to-discussion chain, not a hand-authored body.
	replySummary := "Checking the exposed post: " + postInvite.Contribution.Body
	if len([]rune(replySummary)) > 400 {
		replySummary = string([]rune(replySummary)[:400])
	}
	payload, _ := json.Marshal(map[string]string{"title": "Approval boundary", "summary": replySummary, "parent_id": post})
	submission := testMCPSubmissionRow(cap, factory.MCPToolReply, payload)
	receipt, err := s.RecordWorkerPost(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := s.RecordWorkerPost(ctx, submission); err != nil || replay.Record.ID != receipt.Record.ID {
		t.Fatalf("reply replay: %+v %v", replay, err)
	}
	again := testMCPSubmissionRow(cap, factory.MCPToolReply, payload)
	if _, err := s.RecordWorkerPost(ctx, again); err == nil {
		t.Fatal("invitation admitted a second reply")
	}
	if sources, err := s.ContributionSources(ctx); err != nil || len(sources) != 0 {
		t.Fatalf("actual invited reply restarted discovery: %v %v", sources, err)
	}
	child, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: cap.Repository, Body: "Thanks. Please keep this confined to the delivery guard.", ParentID: post, IdempotencyKey: "role-followup"}, "7", "Maya", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	descendant := testMCPSubmissionRow(cap, factory.MCPToolReply, []byte(`{"title":"Another reply","summary":"An invitation cannot recursively admit replies to another descendant.","parent_id":"`+child+`"}`))
	if _, err := s.RecordWorkerPost(ctx, descendant); err == nil {
		t.Fatal("invitation widened through descendants")
	}
	if _, err := s.EditFeedPost(ctx, post, "7", "Revised request: inspect Finance decline as well as delivery.", 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MCPDiscussion(ctx, cap, post, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale invitation still reads edited source: %v", err)
	}
	revised, err := s.ContributionContext(ctx, post)
	if err != nil || revised.Revision != 1 || len(revised.Roles) != 1 {
		t.Fatalf("revised source cannot be reassessed: %+v %v", revised, err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, revised); err != nil || !claimed {
		t.Fatalf("revised claim: %v %v", claimed, err)
	}
	revised.Calls = review.Calls
	factory.SelectContribution(&revised)
	source := &factory.PostSource{Kind: factory.PostSourcePost, ID: post, Summary: "The actual source snapshot"}
	if _, _, err := s.SaveFeedPostWithSource(ctx, factory.PostRequest{Repository: cap.Repository, SourceKind: source.Kind, SourceID: post, Body: "The direct guard has already been fixed; use the current evidence.", IdempotencyKey: "browser-reply"}, "7", "Maya", time.Now().Unix(), source); err != nil {
		t.Fatal(err)
	}
	if err := owner.SettleContributionReview(ctx, revised); err != nil {
		t.Fatal(err)
	}
	changed, err := s.FeedPost(ctx, factory.ContributionReviewID(post, 1))
	if err != nil || changed.Contribution == nil || changed.Contribution.State != "stale" {
		t.Fatalf("new browser reply did not invalidate assessment: %+v %v", changed, err)
	}
	// Recovery uses real PostgreSQL session ownership, independent of receipt
	// age and of another process/connection opening the same retained store.
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	lostOwner := contributionOwnerFixture(t, s)
	otherOwner := contributionOwnerFixture(t, reopened)
	pending := func(producer *ContributionOwner, key string) factory.ContributionReview {
		t.Helper()
		id, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: cap.Repository, Body: "Check the current approval boundary: " + key, IdempotencyKey: key}, "7", "Maya", time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		review, err := s.ContributionContext(ctx, id)
		if err != nil || len(review.Roles) == 0 {
			t.Fatal("recovery source has no eligible recorded role", err)
		}
		if claimed, err := producer.ClaimContributionReview(ctx, review); err != nil || !claimed {
			t.Fatal("recovery claim", claimed, err)
		}
		return review
	}
	var live []factory.ContributionReview
	for range contributionRecoveryLimit {
		live = append(live, pending(owner, "live-owner-"+factory.NewID()))
	}
	lost := pending(lostOwner, "lost-owner")
	unknown := pending(otherOwner, "unowned-history")
	if _, err := db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=data-'contribution_owner' WHERE id=$1`, factory.ContributionReviewInvitationID(unknown)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{at}',to_jsonb($1::bigint)) WHERE data->>'contribution_owner'=$2`, time.Now().Add(-72*time.Hour).Unix(), owner.id); err != nil {
		t.Fatal(err)
	}
	late := lost
	for _, question := range factory.ContributionQuestions {
		late.Calls = append(late.Calls, factory.ContributionCall{Assignment: cap.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "declared-late-answer", Selected: "contribute", Probabilities: map[string]float64{"contribute": .95, "no_contribution": .05}, Confidence: .9}})
	}
	factory.SelectContribution(&late)
	if err := otherOwner.SettleContributionReview(ctx, late); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("another live producer settled this claim", err)
	}
	if claimed, err := otherOwner.ClaimContributionReview(ctx, lost); err != nil || claimed {
		t.Fatal("another producer replaced pending ownership", claimed, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reopened.RecoverContributionReviews(cancelled, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled contribution recovery did not stop", err)
	}
	recoveryPage, err := reopened.RecoverContributionReviews(ctx, 0)
	if err != nil || recoveryPage.Recovered != 0 || recoveryPage.Complete || recoveryPage.Next == 0 {
		t.Fatal("recovery changed old live owners or lost bounded continuation", recoveryPage, err)
	}
	end, err := reopened.RecoverContributionReviews(ctx, recoveryPage.Next)
	if err != nil || end.Recovered != 0 || !end.Complete || end.Next <= recoveryPage.Next {
		t.Fatal("live ownership was guessed dead", end, err)
	}
	// Terminate the actual owner backend. The Go producer still exists, but its
	// dedicated sql.Conn cannot reconnect or settle a late model response.
	var pid int
	if err := lostOwner.conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := db.QueryRowContext(ctx, `SELECT pg_terminate_backend($1,1000)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatal("could not terminate owned PostgreSQL producer session", terminated, err)
	}
	type recoveryResult struct {
		recoveryPage ContributionRecovery
		err          error
	}
	results := make(chan recoveryResult, 2)
	for _, peer := range []*Store{s, reopened} {
		go func() {
			result, err := peer.RecoverContributionReviews(ctx, recoveryPage.Next)
			results <- recoveryResult{result, err}
		}()
	}
	recovered := 0
	for range 2 {
		result := <-results
		if result.err != nil || !result.recoveryPage.Complete {
			t.Fatal("concurrent released-owner recovery", result)
		}
		recovered += result.recoveryPage.Recovered
	}
	if recovered != 1 {
		t.Fatal("released ownership was not recovered exactly once", recovered)
	}
	if err := lostOwner.SettleContributionReview(ctx, late); err == nil {
		t.Fatal("lost session settled a late invitation")
	}
	if claimed, err := otherOwner.ClaimContributionReview(ctx, lost); err != nil || claimed {
		t.Fatal("interrupted assessment was replayed", claimed, err)
	}
	assessment, err := reopened.FeedPost(ctx, factory.ContributionReviewInvitationID(lost))
	if err != nil || assessment.Contribution == nil || assessment.Contribution.State != "unavailable" || assessment.Contribution.Assignment != "" || assessment.Contribution.Profile != "" || len(assessment.Contribution.Calls) != 0 {
		t.Fatal("recovery invented confirmed calls or an invitation", assessment.Contribution, err)
	}
	for _, review := range append(live, unknown) {
		assessment, err := reopened.FeedPost(ctx, factory.ContributionReviewInvitationID(review))
		if err != nil || assessment.Contribution == nil || assessment.Contribution.State != "pending" {
			t.Fatal("live or unknown historical claim was recovered by age", assessment.Contribution, err)
		}
	}
	for _, id := range mustContributionSources(t, ctx, reopened) {
		if id == lost.Source {
			t.Fatal("recovered source became eligible for new model calls")
		}
	}
}

func mustContributionSources(t *testing.T, ctx context.Context, s *Store) []string {
	t.Helper()
	ids, err := s.ContributionSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestMCPDiscussionProjectsOversizedConsultationWithoutLosingStoredInput(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := t.Context()
	cap := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	// Actual assignment owns both the recorded call and its visible thread.
	raw, _ := json.Marshal(map[string]any{"id": cap.AssignmentID, "repository": "7", "issue": "42", "run": cap.RunID})
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,7,42,$2,'assigned',1,$3)`, cap.AssignmentID, cap.RunID, string(raw)); err != nil {
		t.Fatal(err)
	}
	state := strings.Repeat("Recorded project facts. ", 2500)
	c := SystemOneConsultation{Repository: 7, Role: cap.Profile, FeedConsultation: factory.FeedConsultation{Snapshot: strings.Repeat("f", 64), Assignment: cap.AssignmentID, Tool: string(factory.MCPToolSystemOneChoice), Kind: "choice", State: state, Instructions: "Compare the supplied alternatives."}}
	if err := s.RecordConsultationRequest(ctx, c); err != nil {
		t.Fatal(err)
	}
	post := factory.FeedConsultationPostID(c.Snapshot)
	page, err := s.MCPDiscussion(ctx, cap, post, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Consultation == nil || len(page.Items[0].Omitted) == 0 {
		t.Fatalf("large consultation cannot make paging progress: %+v %v", page, err)
	}
	stored, err := s.FeedPost(ctx, post)
	if err != nil || stored.Consultation == nil || stored.Consultation.State != state {
		t.Fatal("bounded MCP projection altered exact stored input", err)
	}
}

func TestMCPCommunicationPersistsFeedAndReplay(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	cap := testMCPCapabilityRow()
	assignment := journeyAssignment(cap.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), cap.Repository, 42, true)
	insertJourneyAssignment(t, journeySQL(t, dsn), assignment, "assigned", 1)
	cap = activateMCPCapability(t, ctx, s, cap)
	payload := []byte(`{"title":"Who can view a task?","summary":"The owner policy needs clarification.","question":"May team members view other people's tasks?","options":["Only owners","All team members"]}`)
	submission := testMCPSubmissionRow(cap, factory.MCPToolAsk, payload)
	receipt, err := s.RecordWorkerPost(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	if !factory.ValidID(receipt.Record.ID) {
		t.Fatalf("missing feed identity: %+v", receipt)
	}
	if count, err := s.MCPSubmissionCount(ctx, cap.AssignmentID); err != nil || count != 0 {
		t.Fatal("worker post acquired a duplicate submission record", count, err)
	}
	replay, err := s.RecordWorkerPost(ctx, submission)
	if err != nil || replay.Record.ID != receipt.Record.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	page, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != receipt.Record.ID {
		t.Fatalf("feed order/replay: %+v", page)
	}
	details, err := s.FeedPostDetails(ctx, []string{receipt.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	detail := details[receipt.Record.ID]
	if detail.Actor != "builder" || detail.AuthorName != "Builder" || detail.Assignment != cap.AssignmentID || detail.Record.Issue != 42 || detail.Status != "ask" || detail.Payload.Communication.Question == "" {
		t.Fatalf("lost bound metadata/content: %+v", detail)
	}

	if detail.Payload.Communication.QuestionState != "open" {
		t.Fatal("new question is not open")
	}
	if _, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 7, Body: "The intended policy is owner-only.", ParentID: receipt.Record.ID, IdempotencyKey: "question-reply"}, "7", "Owner", 1); err != nil {
		t.Fatal(err)
	}
	question, err := s.FeedPost(ctx, receipt.Record.ID)
	if err != nil || question.Payload.Communication.QuestionState != "replied" {
		t.Fatalf("human reply not reflected: %+v %v", question, err)
	}
	// Answer uses the same authentic identity and is a durable thread reply.
	answer := testMCPSubmissionRow(cap, factory.MCPToolAnswer, []byte(`{"title":"Policy clarified","summary":"Only task owners may view their tasks.","parent_id":"`+receipt.Record.ID+`"}`))
	answered, err := s.RecordWorkerPost(ctx, answer)
	if err != nil {
		t.Fatal(err)
	}
	replies, _, err := s.FeedReplyIDs(ctx, 7, "post", receipt.Record.ID, 0)
	if err != nil || len(replies) != 2 || replies[0] != answered.Record.ID {
		t.Fatalf("replies=%v err=%v", replies, err)
	}
	question, err = s.FeedPost(ctx, receipt.Record.ID)
	if err != nil || question.Payload.Communication.QuestionState != "answered" {
		t.Fatalf("explicit answer not reflected: %+v %v", question, err)
	}
	// No model/judgment request is invented for an authored worker report.
	if _, err = s.PostIntake(ctx, receipt.Record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("worker communication opened pipeline: %v", err)
	}
	// Unknown/spoofed identity fields refuse atomically without a receipt or post.
	bad := testMCPSubmissionRow(cap, factory.MCPToolAsk, []byte(`{"title":"Spoof","summary":"Try another identity","question":"Who?","actor":"inspector"}`))
	if _, err = s.RecordWorkerPost(ctx, bad); err == nil {
		t.Fatal("spoofed actor admitted")
	}
	if _, err = s.MCPWorkStatus(ctx, cap.AssignmentID, bad.SubmissionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused report saved receipt: %v", err)
	}
	// A caller-selected Factory event ID is ignored, so it cannot become a trusted card.
	stray := testMCPSubmissionRow(cap, factory.MCPToolPartial, []byte(`{"title":"Merged","summary":"An untrusted report","completed":["claimed merge"],"component_id":"F04"}`))
	strayReceipt, err := s.RecordWorkerPost(ctx, stray)
	if err != nil {
		t.Fatal(err)
	}
	if post, err := s.FeedPost(ctx, strayReceipt.Record.ID); err != nil || post.Payload == nil || post.Payload.Communication == nil || post.Payload.Communication.ComponentID != "" {
		t.Fatalf("stray component kept: %+v %v", post, err)
	}
}

func TestMCPCommunicationPostsDoNotConsumeFinishCapacity(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	cap := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	payload := []byte(`{"title":"Progress","summary":"Working on the assigned task.","remaining":["Finish the task"]}`)
	for i := 0; i < 130; i++ {
		submission := testMCPSubmissionRow(cap, factory.MCPToolPartial, payload)
		if _, err := s.RecordWorkerPost(ctx, submission); err != nil {
			t.Fatal(err)
		}
	}
	terminal := testMCPSubmissionRow(cap, factory.MCPToolFinishIncomplete, []byte(`{"outcome":"blocked","cause":"question","evidence_refs":[],"affected_task_ids":[],"requested_resolution":"Need owner input"}`))
	if _, err := s.RecordMCPSubmission(ctx, terminal); err != nil {
		t.Fatalf("communications consumed terminal capacity: %v", err)
	}
}

func TestMCPCommunicationEveryOutlet(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	cap := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	question := ""
	for _, tool := range factory.MCPCommunicationTools {
		payload := map[string]any{"title": "Worker update", "summary": "A concrete observation about the assigned work."}
		switch tool {
		case factory.MCPToolAsk:
			payload["question"] = "Which policy applies?"
		case factory.MCPToolAnswer, factory.MCPToolReply:
			payload["parent_id"] = question
		case factory.MCPToolFlag:
			payload["severity"] = "warning"
		case factory.MCPToolLearned:
			payload["scope"] = "Task access policy"
			payload["evidence_refs"] = []string{"src/access.go:12"}
		case factory.MCPToolSuggest:
			payload["rationale"] = "It reduces duplicated checks."
		case factory.MCPToolPartial:
			payload["completed"] = []string{"Reviewed the access rules"}
		case factory.MCPToolBlocked:
			payload["resolution"] = "Confirm the intended task access policy."
		case factory.MCPToolStuck:
			payload["attempts"] = []string{"Checked existing tests; they do not cover this case"}
		case factory.MCPToolRecordDecision:
			payload["decision"] = "Use the existing access helper"
			payload["rationale"] = "Keep the same policy across routes."
		}
		raw, _ := json.Marshal(payload)
		submission := testMCPSubmissionRow(cap, tool, raw)
		receipt, err := s.RecordWorkerPost(ctx, submission)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if tool == factory.MCPToolAsk {
			question = receipt.Record.ID
		}
		detail, err := s.FeedPost(ctx, receipt.Record.ID)
		if err != nil || detail.Payload == nil || detail.Payload.Communication.Kind != strings.TrimPrefix(string(tool), "factory.") {
			t.Fatalf("%s absent from stored feed: %+v %v", tool, detail, err)
		}
	}
}

func TestMCPFinishUsesCanonicalRecord(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	cap := testMCPCapabilityRow()
	a := journeyAssignment(cap.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), 7, 42, true)
	a.Phase = "implement"
	insertJourneyAssignment(t, journeySQL(t, dsn), a, "assigned", 1)
	cap = activateMCPCapability(t, ctx, s, cap)
	submission := testMCPSubmissionRow(cap, factory.MCPToolFinishIncomplete, []byte(`{"component_id":"S12","outcome":"blocked","cause":"question","evidence_refs":[],"affected_task_ids":[],"requested_resolution":"Need owner input"}`))
	work, err := s.RecordMCPSubmission(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	if !factory.ValidID(work.FeedPostID) || work.State != factory.MCPWorkRecorded {
		t.Fatalf("finish has no recorded work post: %+v", work)
	}
	if replay, err := s.RecordMCPSubmission(ctx, submission); err != nil || replay.FeedPostID != work.FeedPostID {
		t.Fatalf("finish repeat created another post: %+v %v", replay, err)
	}
	post, err := s.FeedPost(ctx, work.FeedPostID)
	if err != nil || post.Record.ParentID != a.ID || post.Assignment != a.ID {
		t.Fatalf("work post lost its assignment: %+v %v", post, err)
	}
	stored, found, err := s.TerminalMCPSubmission(ctx, a.ID)
	if err != nil || !found || stored.SubmissionID != submission.SubmissionID || string(stored.Payload) != string(submission.Payload) {
		t.Fatalf("work post lost the submitted result: %+v %v %v", stored, found, err)
	}
	details, err := s.FeedRecordDetails(ctx, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	detail := details[a.ID]
	if strings.Contains(detail.Status, "completed") || strings.Contains(detail.Status, "verified") {
		t.Fatalf("submission invented completion: %s", detail.Status)
	}
	page, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10})
	if err != nil || len(page) != 2 || page[0].Kind != "post" || page[0].ID != work.FeedPostID || page[1].Kind != "record" || page[1].ID != a.ID {
		t.Fatalf("finish must expose its single post beside the assignment: %+v %v", page, err)
	}
}

// TestPostResolutionDispositionPersistsAndExposes proves the R32.2 and
// R32.3 disposition route against real PostgreSQL: a recorded answer
// incorporates, moving its question past answered; a suggestion
// applies with its reason and refuses twice; refused resolutions must
// explain themselves; questions, status signals and missing posts
// refuse; dispositions surface on discussion reads beside the
// immutable posts; and recording partial, suggest or answer finishes
// nothing and grants no readiness.
func TestPostResolutionDispositionPersistsAndExposes(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	cap := testMCPCapabilityRow()
	assignment := journeyAssignment(cap.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), cap.Repository, 42, true)
	insertJourneyAssignment(t, journeySQL(t, dsn), assignment, "assigned", 1)
	cap = activateMCPCapability(t, ctx, s, cap)
	post := func(tool factory.MCPTool, payload string) FeedPostDetail {
		t.Helper()
		submission := testMCPSubmissionRow(cap, tool, []byte(payload))
		receipt, err := s.RecordWorkerPost(ctx, submission)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if !factory.ValidID(receipt.Record.ID) {
			t.Fatalf("%s recorded no post: %+v", tool, receipt)
		}
		return receipt
	}
	ask := post(factory.MCPToolAsk, `{"title":"Who can view a task?","summary":"The owner policy needs clarification.","question":"May team members view other people's tasks?"}`)
	answer := post(factory.MCPToolAnswer, `{"title":"Policy clarified","summary":"Only task owners may view their tasks.","parent_id":"`+ask.Record.ID+`"}`)
	question, err := s.FeedPost(ctx, ask.Record.ID)
	if err != nil || question.Payload.Communication.QuestionState != "answered" {
		t.Fatalf("answer not reflected: %+v %v", question, err)
	}
	attention := func(want bool) {
		t.Helper()
		entries, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{cap.Repository}, View: "attention", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range entries {
			found = found || entry.ID == ask.Record.ID
		}
		if found != want {
			t.Fatalf("question attention = %v, want %v", found, want)
		}
	}
	attention(true)
	resolve := func(payload string) error {
		t.Helper()
		submission := testMCPSubmissionRow(cap, factory.MCPToolResolvePost, []byte(payload))
		_, err := s.RecordWorkerPost(ctx, submission)
		return err
	}
	if err := resolve(`{"post":"` + answer.Record.ID + `","resolution":"incorporated"} {}`); err == nil {
		t.Fatal("trailing resolution object accepted")
	}
	if err := resolve(`{"post":"` + answer.Record.ID + `","resolution":"incorporated"}`); err != nil {
		t.Fatalf("incorporate: %v", err)
	}
	adopted, err := s.FeedPost(ctx, answer.Record.ID)
	if err != nil || adopted.Payload.Communication.Disposition != "incorporated" {
		t.Fatalf("incorporation not exposed: %+v %v", adopted, err)
	}
	question, err = s.FeedPost(ctx, ask.Record.ID)
	if err != nil || question.Payload.Communication.QuestionState != "incorporated" {
		t.Fatalf("question hid its adoption: %+v %v", question, err)
	}
	attention(false)
	suggest := post(factory.MCPToolSuggest, `{"title":"Cache the policy","summary":"Reading the policy file on every task is slow.","rationale":"Cache ownership at assignment start."}`)
	if err := resolve(`{"post":"` + suggest.Record.ID + `","resolution":"applied","reason":"Cached at dispatch"}`); err != nil {
		t.Fatalf("apply: %v", err)
	}
	applied, err := s.FeedPost(ctx, suggest.Record.ID)
	if err != nil || applied.Payload.Communication.Disposition != "applied" || applied.Payload.Communication.DispositionReason != "Cached at dispatch" {
		t.Fatalf("applied disposition lost: %+v %v", applied, err)
	}
	if err := resolve(`{"post":"` + suggest.Record.ID + `","resolution":"refused","reason":"Second thoughts"}`); err == nil {
		t.Fatal("second disposition accepted")
	}
	other := post(factory.MCPToolSuggest, `{"title":"Skip the cache","summary":"Caching adds staleness risk.","rationale":"Read the file every time."}`)
	if err := resolve(`{"post":"` + other.Record.ID + `","resolution":"refused"}`); err == nil {
		t.Fatal("unexplained refusal accepted")
	}
	if err := resolve(`{"post":"` + other.Record.ID + `","resolution":"incorporated"}`); err == nil {
		t.Fatal("suggestion incorporated")
	}
	if err := resolve(`{"post":"` + answer.Record.ID + `","resolution":"applied","reason":"Wrong kind"}`); err == nil {
		t.Fatal("answer applied")
	}
	if err := resolve(`{"post":"` + ask.Record.ID + `","resolution":"applied","reason":"Answering is not resolving"}`); err == nil {
		t.Fatal("question resolved")
	}
	partial := post(factory.MCPToolPartial, `{"title":"Halfway","summary":"First half done.","completed":["Read the policy"],"remaining":["Apply it"]}`)
	if err := resolve(`{"post":"` + partial.Record.ID + `","resolution":"applied","reason":"Status is not a proposal"}`); err == nil {
		t.Fatal("status signal resolved")
	}
	if err := resolve(`{"post":"` + factory.NewID() + `","resolution":"applied","reason":"Ghost post"}`); err == nil {
		t.Fatal("missing post resolved")
	}
	// Recording questions, answers, suggestions, partials and their
	// dispositions finishes nothing: the assignment carries no result
	// and no readiness path runs outside assignment finish.
	still, err := s.Assignment(ctx, assignment.ID)
	if err != nil || still.Result != nil {
		t.Fatalf("discussion traffic finished the role: %+v %v", still, err)
	}
	again, err := s.FeedPost(ctx, other.Record.ID)
	if err != nil || again.Payload.Communication.Disposition != "" {
		t.Fatalf("refused dispositions leaked state: %+v %v", again, err)
	}
	if count, err := s.MCPSubmissionCount(ctx, cap.AssignmentID); err != nil || count != 0 {
		t.Fatalf("synchronous discussion actions created queued work: %d %v", count, err)
	}
}

// TestMCPInvitedContributionOnAssignmentRecord proves the FUX14 record leg
// against real PostgreSQL: an ordinary finished AI result (a factory
// assignment record, never a separate authored post) is discoverable,
// carries eligible-role scope to JEV, claims once per revision, invites one
// running role, admits exactly one worker reply into the original record
// thread, and never loops on receipts, consultations or replies. A second
// record proves the evaluated no-invitation gate without manufacturing work.
func TestMCPInvitedContributionOnAssignmentRecord(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	owner := contributionOwnerFixture(t, s)
	now := time.Now().Unix()
	db := journeySQL(t, dsn)

	eligible := testMCPCapabilityRow()
	eligible.Profile = factory.MCPProfileInspector
	eligible.AssignmentID = factory.NewID()
	eligibleAssignment := journeyAssignment(eligible.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), eligible.Repository, 42, true)
	eligibleAssignment.Run = eligible.RunID
	eligibleAssignment.AcceptedContext = &factory.AcceptedContext{Title: "Equipment approval policy", Body: "Implement protected manager and Finance approvals without self-approval."}
	insertJourneyAssignment(t, db, eligibleAssignment, "assigned", 1)
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,'{}')`, eligible.RunID); err != nil {
		t.Fatal(err)
	}
	eligible = activateMCPCapability(t, ctx, s, eligible)

	sourceID := factory.NewID()
	source := journeyAssignment(sourceID, sourceID, "d"+strings.Repeat("a", 24), eligible.Repository, 42, true)
	source.Phase, source.Role = "implement", "coder"
	source.CreatedUnix, source.FinishedUnix = now, now
	result := factory.ResultSynthesized(sourceID, source.Run, "succeeded", "Implemented the delivery guard without self-approval; candidate abc123.", now)
	source.Result = &result
	insertJourneyAssignment(t, db, source, "finished", 1)

	sources, err := s.ContributionSources(ctx)
	if err != nil || len(sources) != 1 || sources[0] != sourceID {
		t.Fatalf("record source undiscovered: %v %v", sources, err)
	}
	review, err := s.ContributionContext(ctx, sourceID)
	if err != nil || review.SourceKind != factory.PostSourceRecord || review.Revision != 1 || len(review.Roles) != 1 {
		t.Fatalf("record context missing: %+v %v", review, err)
	}
	if !strings.Contains(review.Body, "delivery guard") || !strings.Contains(review.Roles[0].Scope, "protected manager") {
		t.Fatalf("record body/scope missing: %+v", review)
	}
	if review.SourceActor == string(eligible.Profile) {
		t.Fatalf("source actor not excluded: %+v", review)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || !claimed {
		t.Fatalf("record claim: %v %v", claimed, err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || claimed {
		t.Fatalf("record duplicate claim: %v %v", claimed, err)
	}
	if sources, err := s.ContributionSources(ctx); err != nil || len(sources) != 0 {
		t.Fatalf("claimed record rediscovered: %v %v", sources, err)
	}
	for _, question := range factory.ContributionQuestions {
		review.Calls = append(review.Calls, factory.ContributionCall{Assignment: eligible.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "fixture-jev", Selected: "contribute", Probabilities: map[string]float64{"contribute": .95, "no_contribution": .05}, Confidence: .9}})
	}
	factory.SelectContribution(&review)
	if review.State != "invited" || review.Assignment != eligible.AssignmentID {
		t.Fatalf("record not invited: %+v", review)
	}
	if err := owner.SettleContributionReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	invitation := factory.ContributionReviewInvitationID(review)
	// Inbox-to-discussion-to-reply chain with real caller data: the
	// invited worker discovers the invitation in its inbox, reads the
	// exact bounded authorized source snapshot/body/context from the
	// discussion projection (never the direct context helper), and
	// replies to the invitation target.
	inbox, err := s.MCPInbox(ctx, eligible, "")
	if err != nil {
		t.Fatal(err)
	}
	foundInbox := false
	for _, item := range inbox.Items {
		if item.ID == invitation {
			foundInbox = true
		}
	}
	if !foundInbox {
		t.Fatalf("record invitation missing from inbox: %+v", inbox.Items)
	}
	page, err := s.MCPDiscussion(ctx, eligible, invitation, "")
	if err != nil || len(page.Items) < 1 || page.Items[len(page.Items)-1].ID != invitation {
		t.Fatalf("record invitation unreadable: %+v %v", page, err)
	}
	var inviteMsg *factory.MCPFeedMessage
	for i := range page.Items {
		if page.Items[i].ID == invitation {
			inviteMsg = &page.Items[i]
		}
	}
	if inviteMsg == nil || inviteMsg.Contribution == nil || inviteMsg.Source == nil {
		t.Fatalf("record invitation hides its source snapshot: %+v", page.Items)
	}
	if inviteMsg.Contribution.Source != sourceID || inviteMsg.Source.ID != sourceID || inviteMsg.Source.Kind != factory.PostSourceRecord {
		t.Fatalf("record invitation names the wrong source: %+v %+v", inviteMsg.Contribution, inviteMsg.Source)
	}
	if !strings.Contains(inviteMsg.Contribution.Body, "delivery guard") || !strings.Contains(inviteMsg.Source.Summary, "delivery guard") {
		t.Fatalf("record invitation hides its original result/context: %+v %+v", inviteMsg.Contribution, inviteMsg.Source)
	}
	if inviteMsg.ParentID != sourceID {
		t.Fatalf("record invitation lost its source thread: %+v", inviteMsg)
	}
	recordPage, err := s.MCPDiscussion(ctx, eligible, sourceID, "")
	if err != nil || len(recordPage.Items) < 1 {
		t.Fatalf("record discussion refuses its original result: %v %+v", err, recordPage)
	}
	recordInvite := false
	for _, item := range recordPage.Items {
		if item.ID == invitation && item.Contribution != nil && strings.Contains(item.Contribution.Body, "delivery guard") {
			recordInvite = true
		}
	}
	if !recordInvite {
		t.Fatalf("record discussion hides its invitation source: %+v", recordPage.Items)
	}
	// The reply below reuses the exact bounded source text the discussion
	// exposed, not a hand-authored fixture: the worker read the original
	// result through the inbox-to-discussion chain.
	replySummary := "Checking the exposed result: " + inviteMsg.Contribution.Body
	if len([]rune(replySummary)) > 400 {
		replySummary = string([]rune(replySummary)[:400])
	}
	payload, _ := json.Marshal(map[string]string{"title": "Approval boundary", "summary": replySummary, "parent_id": invitation})
	submission := testMCPSubmissionRow(eligible, factory.MCPToolReply, payload)
	receipt, err := s.RecordWorkerPost(ctx, submission)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := s.RecordWorkerPost(ctx, submission); err != nil || replay.Record.ID != receipt.Record.ID {
		t.Fatalf("record reply replay: %+v %v", replay, err)
	}
	again := testMCPSubmissionRow(eligible, factory.MCPToolReply, payload)
	if _, err := s.RecordWorkerPost(ctx, again); err == nil {
		t.Fatal("record invitation admitted a second reply")
	}
	if sources, err := s.ContributionSources(ctx); err != nil || len(sources) != 0 {
		t.Fatalf("record reply restarted discovery: %v %v", sources, err)
	}
	thread, _, err := s.FeedReplyIDs(ctx, eligible.Repository, "record", sourceID, 0)
	if err != nil || len(thread) != 2 || thread[0] != receipt.Record.ID || thread[1] != invitation {
		t.Fatalf("reply missed original record thread: %v %v", thread, err)
	}
	resolved, err := s.ResolvePostSource(ctx, eligible.Repository, factory.PostSourceRecord, sourceID)
	if err != nil || resolved.ID != sourceID || resolved.Kind != factory.PostSourceRecord {
		t.Fatalf("record source unresolved: %+v %v", resolved, err)
	}
	child, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: eligible.Repository, Body: "Thanks, keeping this to the delivery guard.", ParentID: invitation, IdempotencyKey: "record-followup"}, "7", "Maya", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	descendant := testMCPSubmissionRow(eligible, factory.MCPToolReply, []byte(`{"title":"Another reply","summary":"An invitation cannot recursively admit replies to another descendant.","parent_id":"`+child+`"}`))
	if _, err := s.RecordWorkerPost(ctx, descendant); err == nil {
		t.Fatal("record invitation widened through descendants")
	}
	consultSnapshot := strings.Repeat("c", 64)
	consult := SystemOneConsultation{
		Repository: eligible.Repository, Role: eligible.Profile,
		FeedConsultation: factory.FeedConsultation{Snapshot: consultSnapshot, Assignment: eligible.AssignmentID, Tool: string(factory.MCPToolSystemOneChoice), Kind: "choice", State: "Which guard applies?", Instructions: "Pick the guard."},
	}
	if err := s.RecordConsultationRequest(ctx, consult); err != nil {
		t.Fatal(err)
	}
	// Loop exclusion: the worker reply and the consultation comment never
	// become new sources. A human follow-up is ordinary discussion and may
	// legitimately surface as its own post source.
	loopSources, err := s.ContributionSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{receipt.Record.ID, factory.FeedConsultationPostID(consultSnapshot), sourceID} {
		for _, found := range loopSources {
			if found == excluded {
				t.Fatalf("loop source rediscovered: %v in %v", excluded, loopSources)
			}
		}
	}

	secondID := factory.NewID()
	second := journeyAssignment(secondID, secondID, "d"+strings.Repeat("a", 24), eligible.Repository, 42, true)
	second.Phase, second.Role = "implement", "coder"
	second.CreatedUnix, second.FinishedUnix = now, now
	secondResult := factory.ResultSynthesized(secondID, second.Run, "succeeded", "Refactored the approval copy without changing the guard.", now)
	second.Result = &secondResult
	insertJourneyAssignment(t, db, second, "finished", 1)
	secondReview, err := s.ContributionContext(ctx, secondID)
	if err != nil || len(secondReview.Roles) == 0 {
		t.Fatalf("second record context: %+v %v", secondReview, err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, secondReview); err != nil || !claimed {
		t.Fatalf("second claim: %v %v", claimed, err)
	}
	for _, question := range factory.ContributionQuestions {
		secondReview.Calls = append(secondReview.Calls, factory.ContributionCall{Assignment: eligible.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "fixture-jev", Selected: "no_contribution", Probabilities: map[string]float64{"contribute": .1, "no_contribution": .9}, Confidence: .9}})
	}
	factory.SelectContribution(&secondReview)
	if secondReview.State != "not_invited" {
		t.Fatalf("nuisance record invited: %+v", secondReview)
	}
	if err := owner.SettleContributionReview(ctx, secondReview); err != nil {
		t.Fatal(err)
	}
	secondInvitation := factory.ContributionReviewInvitationID(secondReview)
	stored, err := s.FeedPost(ctx, secondInvitation)
	if err != nil || stored.Contribution == nil || stored.Contribution.State != "not_invited" {
		t.Fatalf("no-invitation not retained: %+v %v", stored, err)
	}
	nuisance := testMCPSubmissionRow(eligible, factory.MCPToolReply, []byte(`{"title":"Courtesy","summary":"Acknowledged.","parent_id":"`+secondInvitation+`"}`))
	if _, err := s.RecordWorkerPost(ctx, nuisance); err == nil {
		t.Fatal("no-invitation assessment admitted a reply")
	}
	settledSources, err := s.ContributionSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{sourceID, secondID, secondInvitation} {
		for _, found := range settledSources {
			if found == excluded {
				t.Fatalf("settled record rediscovered: %v in %v", excluded, settledSources)
			}
		}
	}
}

// TestMCPPostInvitationDirectRefusesStaleSource proves the FUX14 post-leg
// regression against real PostgreSQL: a direct reply to the invitation
// itself binds the exact live original post kind/revision/repository plus
// nondeleted/nonrevoked state. While live it admits once and consumes;
// after the original is edited or deleted it refuses with no new comment
// and no consumption.
func TestMCPPostInvitationDirectRefusesStaleSource(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	owner := contributionOwnerFixture(t, s)
	cap := testMCPCapabilityRow()
	assignment := journeyAssignment(cap.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), cap.Repository, 42, true)
	assignment.Run = cap.RunID
	assignment.AcceptedContext = &factory.AcceptedContext{Title: "Equipment approval policy", Body: "Implement protected manager and Finance approvals without self-approval."}
	db := journeySQL(t, dsn)
	insertJourneyAssignment(t, db, assignment, "assigned", 1)
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,'{}')`, cap.RunID); err != nil {
		t.Fatal(err)
	}
	cap = activateMCPCapability(t, ctx, s, cap)

	inviteOne := func(body string) (string, string) {
		t.Helper()
		post, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: cap.Repository, Body: body, IdempotencyKey: "stale-" + factory.NewID()}, "7", "Maya", time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		review, err := s.ContributionContext(ctx, post)
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || !claimed {
			t.Fatalf("claim %s: %v %v", post, claimed, err)
		}
		for _, question := range factory.ContributionQuestions {
			review.Calls = append(review.Calls, factory.ContributionCall{Assignment: cap.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "fixture-jev", Selected: "contribute", Probabilities: map[string]float64{"contribute": .95, "no_contribution": .05}, Confidence: .9}})
		}
		factory.SelectContribution(&review)
		if review.State != "invited" {
			t.Fatalf("not invited: %+v", review)
		}
		if err := owner.SettleContributionReview(ctx, review); err != nil {
			t.Fatal(err)
		}
		return post, factory.ContributionReviewID(post, 0)
	}
	countComments := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE repository=$1`, cap.Repository).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	invitationState := func(invitation string) string {
		t.Helper()
		detail, err := s.FeedPost(ctx, invitation)
		if err != nil || detail.Contribution == nil {
			t.Fatalf("invitation missing: %v %+v", err, detail)
		}
		return detail.Contribution.State
	}

	// Live invitation-direct admits once and consumes.
	livePost, liveInvite := inviteOne("The Finance check can be bypassed through the direct delivery operation. Please inspect the authorization boundary.")
	before := countComments()
	livePayload := []byte(`{"title":"Approval boundary","summary":"Live invitation-direct reply against the current revision.","parent_id":"` + liveInvite + `"}`)
	liveSubmission := testMCPSubmissionRow(cap, factory.MCPToolReply, livePayload)
	if _, err := s.RecordWorkerPost(ctx, liveSubmission); err != nil {
		t.Fatalf("live invitation-direct refused: %v", err)
	}
	if got := countComments(); got != before+1 {
		t.Fatalf("live invitation-direct created %d comments, want %d", got-before, 1)
	}
	if state := invitationState(liveInvite); state != "responded" {
		t.Fatalf("live invitation-direct did not consume: %s", state)
	}
	// Second live invitation-direct must refuse with no new comment.
	before = countComments()
	again := testMCPSubmissionRow(cap, factory.MCPToolReply, []byte(`{"title":"Again","summary":"Second live invitation-direct.","parent_id":"`+liveInvite+`"}`))
	if _, err := s.RecordWorkerPost(ctx, again); err == nil {
		t.Fatal("live invitation admitted a second reply")
	}
	if got := countComments(); got != before {
		t.Fatalf("second live reply created a comment: %d vs %d", got, before)
	}
	_ = livePost

	// Edited source: invitation-target reply refuses with no comment and no consumption.
	editedPost, editedInvite := inviteOne("The Finance check can be bypassed through the direct delivery operation. Please inspect the authorization boundary.")
	if _, err := s.EditFeedPost(ctx, editedPost, "7", "Revised request: inspect Finance decline as well as delivery.", 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	before = countComments()
	stale := testMCPSubmissionRow(cap, factory.MCPToolReply, []byte(`{"title":"Stale","summary":"Edited source invitation-direct.","parent_id":"`+editedInvite+`"}`))
	if _, err := s.RecordWorkerPost(ctx, stale); err == nil {
		t.Fatal("edited source admitted an invitation-target reply")
	}
	if got := countComments(); got != before {
		t.Fatalf("edited stale reply created a comment: %d vs %d", got, before)
	}
	if state := invitationState(editedInvite); state != "invited" {
		t.Fatalf("edited stale reply consumed the invitation: %s", state)
	}
	// Source-direct against the edited revision also refuses.
	before = countComments()
	staleSource := testMCPSubmissionRow(cap, factory.MCPToolReply, []byte(`{"title":"Stale","summary":"Edited source-direct.","parent_id":"`+editedPost+`"}`))
	if _, err := s.RecordWorkerPost(ctx, staleSource); err == nil {
		t.Fatal("edited source admitted a source-direct reply")
	}
	if got := countComments(); got != before {
		t.Fatalf("edited source-direct created a comment: %d vs %d", got, before)
	}

	// Deleted source: invitation-target reply refuses with no comment and no consumption.
	deletedPost, deletedInvite := inviteOne("The Finance check can be bypassed through the direct delivery operation. Please inspect the authorization boundary.")
	if _, err := s.DeleteFeedPost(ctx, deletedPost, "7", 0); err != nil {
		t.Fatal(err)
	}
	before = countComments()
	deleted := testMCPSubmissionRow(cap, factory.MCPToolReply, []byte(`{"title":"Stale","summary":"Deleted source invitation-direct.","parent_id":"`+deletedInvite+`"}`))
	if _, err := s.RecordWorkerPost(ctx, deleted); err == nil {
		t.Fatal("deleted source admitted an invitation-target reply")
	}
	if got := countComments(); got != before {
		t.Fatalf("deleted stale reply created a comment: %d vs %d", got, before)
	}
	if state := invitationState(deletedInvite); state != "invited" {
		t.Fatalf("deleted stale reply consumed the invitation: %s", state)
	}
}

// TestMCPRecordInvitationDirectRefusesStaleRevision proves the FUX14 record
// leg against real PostgreSQL: a direct reply to the record invitation
// binds the exact live original assignment kind/revision/repository plus
// finished stage. After the original is edited (revision bump) it refuses
// with no new comment and no consumption.
func TestMCPRecordInvitationDirectRefusesStaleRevision(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	owner := contributionOwnerFixture(t, s)
	now := time.Now().Unix()
	db := journeySQL(t, dsn)
	eligible := testMCPCapabilityRow()
	eligible.Profile = factory.MCPProfileInspector
	eligible.AssignmentID = factory.NewID()
	eligibleAssignment := journeyAssignment(eligible.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), eligible.Repository, 42, true)
	eligibleAssignment.Run = eligible.RunID
	eligibleAssignment.AcceptedContext = &factory.AcceptedContext{Title: "Equipment approval policy", Body: "Implement protected manager and Finance approvals without self-approval."}
	insertJourneyAssignment(t, db, eligibleAssignment, "assigned", 1)
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,'{}')`, eligible.RunID); err != nil {
		t.Fatal(err)
	}
	eligible = activateMCPCapability(t, ctx, s, eligible)

	sourceID := factory.NewID()
	source := journeyAssignment(sourceID, sourceID, "d"+strings.Repeat("a", 24), eligible.Repository, 42, true)
	source.Phase, source.Role = "implement", "coder"
	source.CreatedUnix, source.FinishedUnix = now, now
	result := factory.ResultSynthesized(sourceID, source.Run, "succeeded", "Implemented the delivery guard without self-approval; candidate abc123.", now)
	source.Result = &result
	insertJourneyAssignment(t, db, source, "finished", 1)

	review, err := s.ContributionContext(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	for _, question := range factory.ContributionQuestions {
		review.Calls = append(review.Calls, factory.ContributionCall{Assignment: eligible.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "fixture-jev", Selected: "contribute", Probabilities: map[string]float64{"contribute": .95, "no_contribution": .05}, Confidence: .9}})
	}
	factory.SelectContribution(&review)
	if review.State != "invited" {
		t.Fatalf("not invited: %+v", review)
	}
	if err := owner.SettleContributionReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	invitation := factory.ContributionReviewInvitationID(review)
	var before int
	if err := db.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE repository=$1`, eligible.Repository).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Simulate an edit of the original assignment: the live revision moves
	// past the invitation's bound revision.
	if _, err := db.Exec(`UPDATE factory_assignments SET revision=revision+1 WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	stale := testMCPSubmissionRow(eligible, factory.MCPToolReply, []byte(`{"title":"Stale","summary":"Edited record invitation-direct.","parent_id":"`+invitation+`"}`))
	if _, err := s.RecordWorkerPost(ctx, stale); err == nil {
		t.Fatal("edited record admitted an invitation-target reply")
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE repository=$1`, eligible.Repository).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("edited record stale reply created a comment: %d vs %d", after, before)
	}
	stored, err := s.FeedPost(ctx, invitation)
	if err != nil || stored.Contribution == nil || stored.Contribution.State != "invited" {
		t.Fatalf("edited record stale reply consumed the invitation: %+v %v", stored, err)
	}
	if _, err := s.MCPDiscussion(ctx, eligible, sourceID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale record discussion still reads the edited source: %v", err)
	}
}

// TestMCPInvitationAtomicityHoldsSourceThroughReply proves the FUX14
// invitation atomicity repair against real PostgreSQL: the invited reply
// holds the exact original source row through admission-to-consumption in
// the submission transaction, and a 0-row freshness consumption refuses
// instead of committing beside its comment and receipt. A source edit
// landing between admission and consumption cannot commit a stale reply:
// refusal leaves no new comment, no receipt and no consumed invitation.
// The worker's own discussion (no invitation) still succeeds.
func TestMCPInvitationAtomicityHoldsSourceThroughReply(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	owner := contributionOwnerFixture(t, s)
	cap := testMCPCapabilityRow()
	assignment := journeyAssignment(cap.AssignmentID, strings.Repeat("1", 32), "d"+strings.Repeat("a", 24), cap.Repository, 42, true)
	assignment.Run = cap.RunID
	assignment.AcceptedContext = &factory.AcceptedContext{Title: "Equipment approval policy", Body: "Implement protected manager and Finance approvals without self-approval."}
	db := journeySQL(t, dsn)
	insertJourneyAssignment(t, db, assignment, "assigned", 1)
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,'{}')`, cap.RunID); err != nil {
		t.Fatal(err)
	}
	cap = activateMCPCapability(t, ctx, s, cap)
	post, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: cap.Repository, Body: "The Finance check can be bypassed through the direct delivery operation. Please inspect the authorization boundary.", IdempotencyKey: "atomic-" + factory.NewID()}, "7", "Maya", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	review, err := s.ContributionContext(ctx, post)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := owner.ClaimContributionReview(ctx, review); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	for _, question := range factory.ContributionQuestions {
		review.Calls = append(review.Calls, factory.ContributionCall{Assignment: cap.AssignmentID, Question: question, Answer: factory.PriorityAnswer{Model: "fixture-jev", Selected: "contribute", Probabilities: map[string]float64{"contribute": .95, "no_contribution": .05}, Confidence: .9}})
	}
	factory.SelectContribution(&review)
	if review.State != "invited" {
		t.Fatalf("not invited: %+v", review)
	}
	if err := owner.SettleContributionReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	invitation := factory.ContributionReviewID(post, 0)

	// Mirror the transaction owner: lock the reply target FOR SHARE, then
	// admit. Admission of this invitation-direct reply must additionally
	// hold the exact original source row, so a concurrent source edit
	// queues instead of landing between admission and consumption.
	holder, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parentKind string
	if err := holder.QueryRowContext(ctx, `SELECT COALESCE(data->'communication'->>'kind','') FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false) FOR SHARE`, invitation, cap.Repository).Scan(&parentKind); err != nil {
		_ = holder.Rollback()
		t.Fatal(err)
	}
	if err := admitWorkerDiscussionReplyTx(ctx, holder, cap, invitation); err != nil {
		_ = holder.Rollback()
		t.Fatalf("live invitation-direct not admitted: %v", err)
	}
	// A concurrent source edit must not slip past the held admission:
	// an immediate exclusive probe from a second connection fails while
	// the holder is open and succeeds once it rolls back.
	var probeRev int64
	if err := db.QueryRow(`SELECT revision FROM factory_feed_posts WHERE id=$1 FOR UPDATE NOWAIT`, post).Scan(&probeRev); err == nil {
		_ = holder.Rollback()
		t.Fatal("source edit slipped past held invitation admission")
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT revision FROM factory_feed_posts WHERE id=$1 FOR UPDATE NOWAIT`, post).Scan(&probeRev); err != nil {
		t.Fatalf("source lock not released: %v", err)
	}

	// The freshness consumption itself must refuse a stale source
	// instead of silently matching zero rows: after the original is
	// edited, the helper reports an error and consumes nothing.
	if _, err := s.EditFeedPost(ctx, post, "7", "Revised request: inspect Finance decline as well as delivery.", 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	consuming, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := contributionReplyTx(ctx, consuming, cap, invitation, "stale-probe"); err == nil {
		_ = consuming.Rollback()
		t.Fatal("stale consumption matched nothing but reported success")
	}
	_ = consuming.Rollback()
	if detail, err := s.FeedPost(ctx, invitation); err != nil || detail.Contribution == nil || detail.Contribution.State != "invited" {
		t.Fatalf("stale consumption probe consumed the invitation: %+v %v", detail, err)
	}

	// Full-path refusal trio: the stale invited reply commits no new
	// comment, no receipt and no consumed invitation.
	var before int
	if err := db.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE repository=$1`, cap.Repository).Scan(&before); err != nil {
		t.Fatal(err)
	}
	stale := testMCPSubmissionRow(cap, factory.MCPToolReply, []byte(`{"title":"Stale","summary":"Edited source invitation-direct between admission and consumption.","parent_id":"`+invitation+`"}`))
	if _, err := s.RecordWorkerPost(ctx, stale); err == nil {
		t.Fatal("edited source admitted an invitation-target reply")
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE repository=$1`, cap.Repository).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("stale reply created a comment: %d vs %d", after, before)
	}
	if _, err := s.MCPWorkStatus(ctx, cap.AssignmentID, stale.SubmissionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale reply saved a receipt: %v", err)
	}
	if detail, err := s.FeedPost(ctx, invitation); err != nil || detail.Contribution == nil || detail.Contribution.State != "invited" {
		t.Fatalf("stale reply consumed the invitation: %+v %v", detail, err)
	}

	// Own-discussion replies need no invitation and still succeed: the
	// worker asks in its own thread and answers it without any claim.
	askPayload := []byte(`{"title":"Who can view a task?","summary":"The owner policy needs clarification.","question":"May team members view other people's tasks?"}`)
	askSubmission := testMCPSubmissionRow(cap, factory.MCPToolAsk, askPayload)
	askReceipt, err := s.RecordWorkerPost(ctx, askSubmission)
	if err != nil {
		t.Fatalf("own ask refused: %v", err)
	}
	answerSubmission := testMCPSubmissionRow(cap, factory.MCPToolAnswer, []byte(`{"title":"Policy clarified","summary":"Only task owners may view their tasks.","parent_id":"`+askReceipt.Record.ID+`"}`))
	if _, err := s.RecordWorkerPost(ctx, answerSubmission); err != nil {
		t.Fatalf("own-discussion reply refused: %v", err)
	}
}
