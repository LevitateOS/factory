package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/factory/pkg/factory"
)

func TestIntentGenerationDeliveredMustChainPriorBindings(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	task := factory.IntentTask{Key: "d444444444444444444444444", Objective: "Build request form", Outcomes: []string{"Submit a request"}, IdeaIDs: []string{"d333333333333333333333333"}}
	first := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Repository: 42, Objective: "Build a request form", State: "accepted", MaxIssues: 4, CreatedUnix: 1, Ideas: []factory.ProductIdea{{ID: "d333333333333333333333333", Description: "Request form", Consequence: "Requests are recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}, Tasks: []factory.IntentTask{task}}
	if err := s.SaveIntentRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	g1 := factory.IntentGeneration{RevisionID: first.ID, IntentID: first.Intent, OperationID: "graph-op-1", ActorID: 9, NativeRev: 1, AuthRevision: first.ID, NotAfter: 10, Payload: json.RawMessage(`{"creates":[]}`), SubmittedTasks: []factory.IntentTask{task}, IntentDigest: first.Digest(), State: "pending"}
	if err := s.BeginIntentGeneration(ctx, g1, factory.GraphPacketFacts{Canonical: g1.Payload}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindIntentTaskAcceptance(ctx, factory.IntentTaskBinding{RevisionID: first.ID, TaskKey: task.Key, IssueID: 101, IssueIndex: 1, AcceptanceID: "d555555555555555555555555"}); err != nil {
		t.Fatal(err)
	}
	next := first
	next.ID = "d666666666666666666666666"
	next.Predecessor = first.ID
	if err := s.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	base := factory.IntentGeneration{RevisionID: next.ID, IntentID: next.Intent, OperationID: "graph-op-2", ActorID: 9, NativeRev: 2, AuthRevision: next.ID, NotAfter: 10, SubmittedTasks: []factory.IntentTask{task}, IntentDigest: next.Digest(), State: "pending"}
	forged := base
	forged.Payload = json.RawMessage(`{"creates":[]}`)
	forged.Delivered = map[string]factory.GeneratedIssueIdentity{task.Key: {IssueID: 999, IssueIndex: 9}}
	if err := s.BeginIntentGeneration(ctx, forged, factory.GraphPacketFacts{Canonical: forged.Payload}); err == nil || !strings.Contains(err.Error(), "invalid delivered reuse") {
		t.Fatalf("fabricated delivered reuse accepted: %v", err)
	}
	edited := base
	edited.Payload = json.RawMessage(`{"edits":[{"issue_id":101,"expected_title":"t","expected_body":"b","expected_content_version":0,"title":"t","body":"b"}]}`)
	edited.Delivered = map[string]factory.GeneratedIssueIdentity{task.Key: {IssueID: 101, IssueIndex: 1}}
	if err := s.BeginIntentGeneration(ctx, edited, factory.GraphPacketFacts{Canonical: edited.Payload, Edits: map[int64]factory.GraphIssueText{101: {Title: "t", Body: "b"}}}); err == nil || !strings.Contains(err.Error(), "delivered history unexpectedly mutated") {
		t.Fatalf("delivered edit accepted: %v", err)
	}
	chained := base
	chained.Payload = json.RawMessage(`{"creates":[]}`)
	chained.Delivered = map[string]factory.GeneratedIssueIdentity{task.Key: {IssueID: 101, IssueIndex: 1}}
	if err := s.BeginIntentGeneration(ctx, chained, factory.GraphPacketFacts{}); err == nil {
		t.Fatal("delivered generation accepted without decoded packet facts")
	}
	if err := s.BeginIntentGeneration(ctx, chained, factory.GraphPacketFacts{Canonical: json.RawMessage(`{"creates":[{"key":"different"}]}`)}); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("packet facts were attached to different provider bytes", err)
	}
	if err := s.BeginIntentGeneration(ctx, chained, factory.GraphPacketFacts{Canonical: chained.Payload}); err != nil {
		t.Fatal(err)
	}
	chained.State = "committed"
	chained.Receipt = json.RawMessage(`{"issues":[{"issue_id":101}]}`)
	if err := s.RecordIntentGeneration(ctx, chained, factory.GraphReceiptFacts{}); err == nil {
		t.Fatal("receipt facts were omitted for delivered history")
	}
	if err := s.RecordIntentGeneration(ctx, chained, factory.GraphReceiptFacts{Original: json.RawMessage(`{"issues":[]}`)}); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("receipt facts were attached to different provider evidence", err)
	}
	facts := factory.GraphReceiptFacts{Original: chained.Receipt, Edits: []factory.GeneratedIssueIdentity{{IssueID: 101, IssueIndex: 1}}}
	if err := s.RecordIntentGeneration(ctx, chained, facts); err == nil || !strings.Contains(err.Error(), "delivered history unexpectedly mutated") {
		t.Fatal("receipt mutated delivered history", err)
	}
	if pending, err := s.IntentGeneration(ctx, chained.RevisionID); err != nil || pending.State != "pending" || len(pending.Receipt) != 0 {
		t.Fatal("refused receipt changed the stored generation", pending, err)
	}
}

func TestIntentGenerationPinsParentBeforeEffects(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	r := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Repository: 42, Objective: "Build a request form", State: "accepted", MaxIssues: 4, PlanningCalls: 2, CreatedUnix: 1, Ideas: []factory.ProductIdea{{ID: "d333333333333333333333333", Description: "Request form", Consequence: "Requests are recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}, Tasks: []factory.IntentTask{{Key: "d444444444444444444444444", Objective: "Build request form", Outcomes: []string{"Submit a request"}, IdeaIDs: []string{"d333333333333333333333333"}}}}
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	g := factory.IntentGeneration{RevisionID: r.ID, IntentID: r.Intent, OperationID: "graph-op-1", ActorID: 9, NativeRev: 1, AuthRevision: r.ID, NotAfter: 10, Payload: json.RawMessage(`{"creates":[]}`), IntentDigest: r.Digest(), State: "pending"}
	if s.BeginIntentGeneration(ctx, g, factory.GraphPacketFacts{Canonical: g.Payload}) == nil {
		t.Fatal("generation without submitted tasks accepted")
	}
	g.SubmittedTasks = append([]factory.IntentTask(nil), r.Tasks...)
	if err := s.BeginIntentGeneration(ctx, g, factory.GraphPacketFacts{Canonical: g.Payload}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginIntentGeneration(ctx, g, factory.GraphPacketFacts{Canonical: g.Payload}); err != nil {
		t.Fatal(err)
	}
	g.ActorID = 10
	if !errors.Is(s.BeginIntentGeneration(ctx, g, factory.GraphPacketFacts{Canonical: g.Payload}), ErrCommandConflict) {
		t.Fatal("changed operation accepted")
	}
	g.ActorID = 9
	if generated, pending, err := s.AutomaticIssueOrigin(ctx, 42, 1); err != nil || generated || !pending {
		t.Fatal("unbound graph creation must hold initial intake", generated, pending, err)
	}
	g.State = "committed"
	g.Receipt = json.RawMessage(`{"issues":[1]}`)
	if err := s.RecordIntentGeneration(ctx, g, factory.GraphReceiptFacts{}); err != nil {
		t.Fatal(err)
	}
	b := factory.IntentTaskBinding{RevisionID: r.ID, TaskKey: r.Tasks[0].Key, IssueID: 1, IssueIndex: 1, AcceptanceID: "d555555555555555555555555"}
	if err := s.BindIntentTaskAcceptance(ctx, b); err != nil {
		t.Fatal(err)
	}
	if generated, pending, err := s.AutomaticIssueOrigin(ctx, 42, 1); err != nil || !generated || pending {
		t.Fatal("graph-created issue origin was lost after binding", generated, pending, err)
	}
	if generated, pending, err := s.AutomaticIssueOrigin(ctx, 43, 1); err != nil || generated || pending {
		t.Fatal("graph origin escaped its repository", generated, pending, err)
	}
	current, found, err := s.GeneratedIntentCurrent(ctx, b.AcceptanceID)
	if err != nil || !found || current {
		t.Fatal("committed graph became authoritative before completion", current, found, err)
	}
	g.State = "completed"
	if err := s.RecordIntentGeneration(ctx, g, factory.GraphReceiptFacts{}); err != nil {
		t.Fatal(err)
	}
	current, found, err = s.GeneratedIntentCurrent(ctx, b.AcceptanceID)
	if err != nil || !found || !current {
		t.Fatal(current, found, err)
	}
	r.Predecessor = r.ID
	r.ID = "d666666666666666666666666"
	r.State = "draft"
	if err = s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	current, found, err = s.GeneratedIntentCurrent(ctx, b.AcceptanceID)
	if err != nil || !found || current {
		t.Fatal("superseded scope stayed authoritative")
	}
	g.State = "pending"
	g.Receipt = nil
	if !errors.Is(s.BeginIntentGeneration(ctx, g, factory.GraphPacketFacts{Canonical: g.Payload}), ErrStaleRevision) {
		t.Fatal("stale generation accepted")
	}
}

func TestSupersededAcceptanceInvalidatesPlanningWithoutRetarget(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	r := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Repository: 42, Objective: "Build a request form", State: "accepted", MaxIssues: 4, CreatedUnix: 1, Ideas: []factory.ProductIdea{{ID: "d333333333333333333333333", Description: "Request form", Consequence: "Requests are recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}, Tasks: []factory.IntentTask{{Key: "d444444444444444444444444", Objective: "Build request form", Outcomes: []string{"Submit a request"}, IdeaIDs: []string{"d333333333333333333333333"}}}}
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	binding := factory.IntentTaskBinding{RevisionID: r.ID, TaskKey: "d444444444444444444444444", IssueID: 101, IssueIndex: 1, ContentVersion: 1, AcceptanceID: "d777777777777777777777777", CreatedUnix: 1}
	if err := s.BindIntentTaskAcceptance(ctx, binding); err != nil {
		t.Fatal(err)
	}
	next := r
	next.ID = "d555555555555555555555555"
	next.Predecessor = r.ID
	next.Tasks[0].Objective = "Build request form with validation"
	if err := s.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	if current, generated, err := s.GeneratedIntentCurrent(ctx, binding.AcceptanceID); err != nil || !generated || current {
		t.Fatalf("superseded acceptance stayed current: %v %v %v", current, generated, err)
	}
	kept, err := s.IntentTaskBindings(ctx, r.ID)
	if err != nil || len(kept) != 1 || kept[0] != binding {
		t.Fatalf("prior binding retargeted: %+v %v", kept, err)
	}
}

func TestIntentPlanningAllowsIndependentIntents(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	for i, intent := range []string{"d222222222222222222222222", "d444444444444444444444444"} {
		r := factory.IntentRevision{ID: []string{"d111111111111111111111111", "d333333333333333333333333"}[i], Intent: intent, Owner: 1, Objective: "Track requests", State: "draft", MaxIssues: 4, CreatedUnix: 1}
		if err := s.SaveIntentRevision(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	first := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1}
	second := factory.IntentRevision{ID: "d333333333333333333333333", Intent: "d444444444444444444444444", Owner: 1}
	if err := s.CheckIntentJudgment(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckIntentJudgment(ctx, second); err != nil {
		t.Fatal("independent intent admission", err)
	}
}
