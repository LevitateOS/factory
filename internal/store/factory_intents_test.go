package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestIntentRepositoryBindingSurvivesNonAcceptedHeads(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	idea := factory.ProductIdea{ID: "d888888888888888888888888", Description: "Track", Consequence: "Visibility", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}
	task := factory.IntentTask{Key: "d999999999999999999999999", Objective: "Entry", Outcomes: []string{"Capture"}, IdeaIDs: []string{idea.ID}}
	committed := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d000000000000000000000000", Owner: 1, Repository: 42, Objective: "Tracker", State: "accepted", MaxIssues: 10, CreatedUnix: 1, Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{task}}
	if err := s.SaveIntentRevision(ctx, committed); err != nil {
		t.Fatal(err)
	}
	refused := func(r factory.IntentRevision) {
		t.Helper()
		if err := s.SaveIntentRevision(ctx, r); err == nil || !strings.Contains(err.Error(), "repository change needs a separate build request") {
			t.Fatalf("repository retarget accepted: %v", err)
		}
	}
	draft := committed
	draft.ID = "d222222222222222222222222"
	draft.Predecessor = committed.ID
	draft.State = "draft"
	if err := s.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	retarget := draft
	retarget.ID = "d333333333333333333333333"
	retarget.Predecessor = draft.ID
	retarget.Repository = 43
	refused(retarget)
	head, err := s.IntentRevision(ctx, committed.Intent, 1)
	if err != nil || head.ID != draft.ID {
		t.Fatalf("refused save moved the head: %+v %v", head, err)
	}
	next := draft
	next.ID = "d444444444444444444444444"
	next.Predecessor = draft.ID
	if err := s.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	rejected := next
	rejected.ID = "d555555555555555555555555"
	rejected.Predecessor = next.ID
	rejected.State = "rejected"
	if err := s.SaveIntentRevision(ctx, rejected); err != nil {
		t.Fatal(err)
	}
	afterRejected := rejected
	afterRejected.ID = "d666666666666666666666666"
	afterRejected.Predecessor = rejected.ID
	afterRejected.State = "draft"
	afterRejected.Repository = 43
	refused(afterRejected)
	if err := s.SaveIntentRevision(ctx, rejected); err != nil {
		t.Fatal("exact replay of pinned head refused")
	}
	same := rejected
	same.ID = "d777777777777777777777777"
	same.Predecessor = rejected.ID
	same.State = "accepted"
	if err := s.SaveIntentRevision(ctx, same); err != nil {
		t.Fatal(err)
	}
	free := factory.IntentRevision{ID: "d000000000000000000000001", Intent: "dffffffffffffffffffffffff", Owner: 1, Repository: 7, Objective: "Tracker", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, free); err != nil {
		t.Fatal(err)
	}
	choice := free
	choice.ID = "d000000000000000000000002"
	choice.Predecessor = free.ID
	choice.Repository = 8
	if err := s.SaveIntentRevision(ctx, choice); err != nil {
		t.Fatal("uncommitted repository choice refused")
	}
	first := choice
	first.ID = "d000000000000000000000003"
	first.Predecessor = choice.ID
	first.State = "accepted"
	first.Ideas = []factory.ProductIdea{idea}
	first.Tasks = []factory.IntentTask{task}
	if err := s.SaveIntentRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	late := first
	late.ID = "d000000000000000000000004"
	late.Predecessor = first.ID
	late.State = "draft"
	late.Repository = 7
	refused(late)
}

func TestIntentRevisionCASAndPrivacy(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	d := factory.Department{ID: "d111111111111111111111111", Owner: 1, Name: "Operations", Description: "Coordinate requests"}
	if err := s.CreateDepartment(ctx, d); err != nil {
		t.Fatal(err)
	}
	r := factory.IntentRevision{ID: "d222222222222222222222222", Intent: "d333333333333333333333333", Owner: 1, Department: d.ID, Objective: "Build request tracker", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Objective = "Changed objective"
	if !errors.Is(s.SaveIntentRevision(ctx, r), ErrCommandConflict) {
		t.Fatal("changed replay accepted")
	}
	r.ID = "d444444444444444444444444"
	if !errors.Is(s.SaveIntentRevision(ctx, r), ErrStaleRevision) {
		t.Fatal("stale predecessor accepted")
	}
	r.Predecessor = "d222222222222222222222222"
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IntentRevision(ctx, r.Intent, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("private intent visible")
	}
	r.Owner = 2
	r.Intent = "d555555555555555555555555"
	r.Predecessor = ""
	if s.SaveIntentRevision(ctx, r) == nil {
		t.Fatal("other owner department accepted")
	}
}

func TestListIntentRevisionsIsOwnerPrivateAndBounded(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	a := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Objective: "First", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	b := factory.IntentRevision{ID: "d333333333333333333333333", Intent: "d444444444444444444444444", Owner: 1, Objective: "Second", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	other := factory.IntentRevision{ID: "d555555555555555555555555", Intent: "d666666666666666666666666", Owner: 2, Objective: "Other", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	for _, r := range []factory.IntentRevision{a, b, other} {
		if err := s.SaveIntentRevision(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, next, err := s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{Limit: 1})
	if err != nil || len(got) != 1 || next == "" {
		t.Fatalf("bounded list %v %v %v", got, next, err)
	}
	got, next, err = s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{Cursor: next, Limit: 10})
	if err != nil || len(got) != 1 || next != "" || got[0].Intent == other.Intent {
		t.Fatal("cursor leaked or missed")
	}
	got, _, err = s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{State: "accepted", Limit: 10})
	if err != nil || len(got) != 0 {
		t.Fatal("empty filter not empty")
	}
}

func TestIntentHistoryAndRepositoryIdentity(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	a := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Objective: "Tracker", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.ID = "d333333333333333333333333"
	b.Predecessor = a.ID
	b.Repository = 42
	b.State = "accepted"
	b.Ideas = []factory.ProductIdea{{ID: "d444444444444444444444444", Description: "Track", Consequence: "Visibility", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}
	b.Tasks = []factory.IntentTask{{Key: "d555555555555555555555555", Objective: "Entry", Outcomes: []string{"Capture"}, IdeaIDs: []string{"d444444444444444444444444"}}}
	if err := s.SaveIntentRevision(ctx, b); err != nil {
		t.Fatal(err)
	}
	history, err := s.IntentHistory(ctx, a.Intent, 1, 10)
	if err != nil || len(history) != 2 || history[0].ID != b.ID {
		t.Fatalf("history %v %v", history, err)
	}
	c := b
	c.ID = "d666666666666666666666666"
	c.Predecessor = b.ID
	c.Repository = 43
	if s.SaveIntentRevision(ctx, c) == nil {
		t.Fatal("repository change kept same request identity")
	}
}

func TestPriorityJudgmentsStoreCompletedChoices(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	for _, snapshot := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		if _, err := s.PriorityJudgment(ctx, snapshot); err != ErrNotFound {
			t.Fatal("unexpected completed judgment", err)
		}
		judgment := factory.PriorityReceipt{Snapshot: snapshot, CreatedUnix: 1, Selected: "oldest", Outcome: "fairness"}
		if err := s.RecordPriorityJudgment(ctx, judgment); err != nil {
			t.Fatal(err)
		}
		got, err := s.PriorityJudgment(ctx, snapshot)
		if err != nil || got.Selected != judgment.Selected || got.Outcome != judgment.Outcome {
			t.Fatal("completed choice lost", got, err)
		}
		if err := s.RecordPriorityJudgment(ctx, judgment); err != ErrCommandConflict {
			t.Fatal("completed choice overwritten", err)
		}
	}
}
