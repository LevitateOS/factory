package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func reviewFixtureRevision(id, intent, predecessor string) factory.IntentRevision {
	return factory.IntentRevision{ID: id, Intent: intent, Predecessor: predecessor, Owner: 1, Objective: "Track leave requests", State: "draft", MaxIssues: 10, CreatedUnix: 1}
}

func reviewFixtureReview(intent, basis, result string) factory.SavedReview {
	return factory.SavedReview{
		Intent: intent, BasisRevision: basis, ResultRevision: result,
		Content: factory.ProposalContent{
			Producer: factory.AuthoredProducer, Supported: false,
			Intent: intent, Revision: basis,
			Objective: "Track leave requests",
			Facts:     []string{"Track leave requests"},
			Unknowns:  []string{"This objective is outside the authored representative coverage, so only the conservative baseline applies."},
		},
		Challenges: []factory.AssumptionChallenge{},
		Settled:    []string{},
		SavedUnix:  1,
	}
}

func reviewJSON(t *testing.T, review factory.SavedReview) string {
	t.Helper()
	raw, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mustHead(t *testing.T, s *Store, ctx context.Context, intent string, owner int64) string {
	t.Helper()
	head, err := s.IntentRevision(ctx, intent, owner)
	if err != nil {
		t.Fatal(err)
	}
	return head.ID
}

func TestSaveIntentReviewAtomicallyAndRead(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, result = "d000000000000000000000000", "d111111111111111111111111", "d222222222222222222222222"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	review := reviewFixtureReview(intent, basis, result)
	if err := s.SaveIntentRevisionWithReview(ctx, reviewFixtureRevision(result, intent, basis), review); err != nil {
		t.Fatal(err)
	}
	if head := mustHead(t, s, ctx, intent, 1); head != result {
		t.Fatalf("head=%s want %s", head, result)
	}
	stored, err := s.IntentReview(ctx, result, 1)
	if err != nil {
		t.Fatal(err)
	}
	if reviewJSON(t, stored) != reviewJSON(t, review) {
		t.Fatalf("stored review=%s want %s", reviewJSON(t, stored), reviewJSON(t, review))
	}
}

func TestSaveIntentReviewExactReplay(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, result = "d010000000000000000000000", "d111111111111111111111112", "d222222222222222222222223"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	revision := reviewFixtureRevision(result, intent, basis)
	review := reviewFixtureReview(intent, basis, result)
	if err := s.SaveIntentRevisionWithReview(ctx, revision, review); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevisionWithReview(ctx, revision, review); err != nil {
		t.Fatalf("exact replay refused: %v", err)
	}
	stored, err := s.IntentReview(ctx, result, 1)
	if err != nil || reviewJSON(t, stored) != reviewJSON(t, review) {
		t.Fatalf("replay changed the review: %+v %v", stored, err)
	}
}

func TestSaveIntentReviewConflictRefuses(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, result = "d020000000000000000000000", "d111111111111111111111113", "d222222222222222222222224"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	revision := reviewFixtureRevision(result, intent, basis)
	review := reviewFixtureReview(intent, basis, result)
	if err := s.SaveIntentRevisionWithReview(ctx, revision, review); err != nil {
		t.Fatal(err)
	}
	changed := review
	changed.SavedUnix = 2
	if err := s.SaveIntentRevisionWithReview(ctx, revision, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("changed review accepted: %v", err)
	}
	moved := revision
	moved.Objective = "Track vacation days"
	if err := s.SaveIntentRevisionWithReview(ctx, moved, review); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("changed revision accepted: %v", err)
	}
	stored, err := s.IntentReview(ctx, result, 1)
	if err != nil || reviewJSON(t, stored) != reviewJSON(t, review) {
		t.Fatalf("conflict changed the review: %+v %v", stored, err)
	}
}

func TestSaveIntentReviewStaleRefusesWithoutPartial(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, rival, result = "d030000000000000000000000", "d111111111111111111111114", "d444444444444444444444444", "d222222222222222222222225"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(rival, intent, basis)); err != nil {
		t.Fatal(err)
	}
	revision := reviewFixtureRevision(result, intent, basis)
	review := reviewFixtureReview(intent, basis, result)
	if err := s.SaveIntentRevisionWithReview(ctx, revision, review); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale save accepted: %v", err)
	}
	if head := mustHead(t, s, ctx, intent, 1); head != rival {
		t.Fatalf("stale save moved the head to %s", head)
	}
	if _, err := s.IntentReview(ctx, result, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale save left a review: %v", err)
	}
	if _, err := s.IntentHistory(ctx, intent, 1, 10); err != nil {
		t.Fatal(err)
	}
}

func TestSaveIntentReviewRejectsMisbound(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, result = "d040000000000000000000000", "d111111111111111111111115", "d222222222222222222222226"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	review := reviewFixtureReview(intent, basis, "d999999999999999999999999")
	if err := s.SaveIntentRevisionWithReview(ctx, reviewFixtureRevision(result, intent, basis), review); err == nil {
		t.Fatal("misbound review accepted")
	}
	if head := mustHead(t, s, ctx, intent, 1); head != basis {
		t.Fatalf("misbound save moved the head to %s", head)
	}
}

// TestSaveIntentReviewInsertFailureRollsBack forces the review write to
// fail after the revision write and proves the whole save rolls back:
// the head stays and no revision row survives.
func TestSaveIntentReviewInsertFailureRollsBack(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, result = "d060000000000000000000000", "d111111111111111111111117", "d222222222222222222222228"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE factory_intent_reviews`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevisionWithReview(ctx, reviewFixtureRevision(result, intent, basis), reviewFixtureReview(intent, basis, result)); err == nil {
		t.Fatal("review write failure accepted")
	}
	if head := mustHead(t, s, ctx, intent, 1); head != basis {
		t.Fatalf("failed save moved the head to %s", head)
	}
	var revisions int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_intent_revisions WHERE id=$1`, result).Scan(&revisions); err != nil || revisions != 0 {
		t.Fatalf("failed save left %d revision rows: %v", revisions, err)
	}
}

func TestIntentReviewPrivacy(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	const intent, basis, result = "d050000000000000000000000", "d111111111111111111111116", "d222222222222222222222227"
	if err := s.SaveIntentRevision(ctx, reviewFixtureRevision(basis, intent, "")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevisionWithReview(ctx, reviewFixtureRevision(result, intent, basis), reviewFixtureReview(intent, basis, result)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IntentReview(ctx, result, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner read the review: %v", err)
	}
	if _, err := s.IntentReview(ctx, "d999999999999999999999999", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing review reported: %v", err)
	}
}
