package store

import (
	"context"
	"errors"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

// TestIntentCreationSliceDraftAcceptReadBack chains the smallest real creation
// slice at source level: draft revision, accepted revision carrying one decided
// idea and its task graph, then read-back through the revision, list and
// history reads. It uses only the existing Store fixture and domain shapes;
// installed browser/API journeys stay separate Q08 evidence.
func TestIntentCreationSliceDraftAcceptReadBack(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	d := factory.Department{ID: "d777777777777777777777777", Owner: 1, Name: "Operations", Description: "Coordinate requests"}
	if err := s.CreateDepartment(ctx, d); err != nil {
		t.Fatal(err)
	}
	draft := factory.IntentRevision{ID: "d888888888888888888888888", Intent: "d999999999999999999999999", Owner: 1, Department: d.ID, Objective: "Track team requests", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	accepted := draft
	accepted.ID = "d000000000000000000000001"
	accepted.Predecessor = draft.ID
	accepted.Repository = 7
	accepted.State = "accepted"
	accepted.Ideas = []factory.ProductIdea{{ID: "d000000000000000000000002", Description: "Track", Consequence: "Visibility", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}
	accepted.Tasks = []factory.IntentTask{{Key: "d000000000000000000000003", Objective: "Entry", Outcomes: []string{"Capture"}, IdeaIDs: []string{"d000000000000000000000002"}}}
	if err := s.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	readBack, err := s.IntentRevision(ctx, accepted.Intent, 1)
	if err != nil {
		t.Fatal(err)
	}
	if readBack.ID != accepted.ID || readBack.State != "accepted" || readBack.Objective != draft.Objective {
		t.Fatalf("read-back diverged: %+v", readBack)
	}
	listed, _, err := s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{State: "accepted", Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].Intent != accepted.Intent {
		t.Fatalf("accepted list %v %v", listed, err)
	}
	history, err := s.IntentHistory(ctx, accepted.Intent, 1, 10)
	if err != nil || len(history) != 2 || history[0].ID != accepted.ID || history[1].ID != draft.ID {
		t.Fatalf("history %v %v", history, err)
	}
	if _, err := s.IntentRevision(ctx, accepted.Intent, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("created intent visible to another owner")
	}
}
