package store

import (
	"context"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

// T37 (FW32): GeneratedIntentCurrent tests for existence of a current
// accepted-head binding, never an arbitrary LIMIT 1 row over historical
// bindings. A copied frozen acceptance lives in several revisions at
// once; only a binding whose revision is still the accepted head counts.
func TestGeneratedIntentCurrentPrefersCurrentHeadOverHistory(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	task := factory.IntentTask{Key: "d444444444444444444444444", Objective: "Build request form", Outcomes: []string{"Submit a request"}, IdeaIDs: []string{"d333333333333333333333333"}}
	first := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Repository: 42, Objective: "Build a request form", State: "accepted", MaxIssues: 4, CreatedUnix: 1, Ideas: []factory.ProductIdea{{ID: "d333333333333333333333333", Description: "Request form", Consequence: "Requests are recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}, Tasks: []factory.IntentTask{task}}
	if err := s.SaveIntentRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	acceptance := "d777777777777777777777777"
	if err := s.BindIntentTaskAcceptance(ctx, factory.IntentTaskBinding{RevisionID: first.ID, TaskKey: task.Key, IssueID: 101, IssueIndex: 1, AcceptanceID: acceptance}); err != nil {
		t.Fatal(err)
	}
	if current, found, err := s.GeneratedIntentCurrent(ctx, acceptance); err != nil || !found || !current {
		t.Fatal("accepted head binding not current:", current, found, err)
	}
	next := first
	next.ID = "d555555555555555555555555"
	next.Predecessor = first.ID
	if err := s.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	// The unchanged task keeps its frozen acceptance in the successor:
	// the same acceptance now binds two revisions, only the head counts.
	if err := s.BindIntentTaskAcceptance(ctx, factory.IntentTaskBinding{RevisionID: next.ID, TaskKey: task.Key, IssueID: 101, IssueIndex: 1, AcceptanceID: acceptance}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if current, found, err := s.GeneratedIntentCurrent(ctx, acceptance); err != nil || !found || !current {
			t.Fatal("copied frozen acceptance lost its current head:", current, found, err)
		}
	}
	third := next
	third.ID = "d666666666666666666666666"
	third.Predecessor = next.ID
	if err := s.SaveIntentRevision(ctx, third); err != nil {
		t.Fatal(err)
	}
	if current, found, err := s.GeneratedIntentCurrent(ctx, acceptance); err != nil || !found || current {
		t.Fatal("historical acceptance stayed authoritative:", current, found, err)
	}
	if current, found, err := s.GeneratedIntentCurrent(ctx, "d999999999999999999999999"); err != nil || !current || found {
		t.Fatal("human acceptance did not stay generated-free:", current, found, err)
	}
}
