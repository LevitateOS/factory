package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func acceptanceFixture(id, predecessor string) factory.Acceptance {
	return factory.Acceptance{
		ID: id, Predecessor: predecessor, Repository: 7, IssueIndex: "3", Approver: 5, NativeRev: 41,
		TitleDigest: strings.Repeat("a", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("b", 64), ContentVersion: 2,
		Sources: []factory.SelectedSource{{ID: "11", ContentVersion: 0, Digest: strings.Repeat("c", 64)}},
	}
}

func TestAdmitAcceptanceChainsPredecessors(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	first := acceptanceFixture("d0123456789abcdef01234567", "")
	if err := s.AdmitAcceptanceDecision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.AdmitAcceptanceDecision(ctx, first); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	changed := first
	changed.ContentVersion = 3
	if err := s.AdmitAcceptanceDecision(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("changed content: %v", err)
	}
	second := acceptanceFixture("d123456789abcdef012345678", "d0123456789abcdef01234567")
	second.NativeRev = 43
	if err := s.AdmitAcceptanceDecision(ctx, second); err != nil {
		t.Fatal(err)
	}
	head, err := s.AcceptanceHead(ctx, 7, 3)
	if err != nil || head != second.ID {
		t.Fatalf("head: %q %v", head, err)
	}
	depth, err := s.AcceptanceDepth(ctx, 7, 3)
	if err != nil || depth != 2 {
		t.Fatalf("depth: %d %v", depth, err)
	}
	got, err := s.AcceptanceDecision(ctx, second.ID)
	if err != nil || got.ID != second.ID || len(got.Sources) != 1 {
		t.Fatalf("readback: %+v %v", got, err)
	}
	stale := acceptanceFixture("d23456789abcdef0123456789", "")
	if err := s.AdmitAcceptanceDecision(ctx, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale predecessor: %v", err)
	}
	foreign := acceptanceFixture("d0123456789abcdef01234567", second.ID)
	foreign.NativeRev = 44
	if err := s.AdmitAcceptanceDecision(ctx, foreign); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("recorded ID: %v", err)
	}
	if _, err := s.AcceptanceHead(ctx, 7, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing head: %v", err)
	}
}

func TestWithdrawAcceptanceLatchesHead(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	first := acceptanceFixture("d0123456789abcdef01234567", "")
	if err := s.AdmitAcceptanceDecision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.WithdrawAcceptanceDecision(ctx, 7, 3, first.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.WithdrawAcceptanceDecision(ctx, 7, 3, first.ID, 5); err != nil {
		t.Fatalf("replay: %v", err)
	}
	withdrawn, by, err := s.AcceptanceWithdrawn(ctx, 7, 3, first.ID)
	if err != nil || !withdrawn || by != 5 {
		t.Fatalf("latch: %v %d %v", withdrawn, by, err)
	}
	withdrawn, _, err = s.AcceptanceWithdrawn(ctx, 7, 3, "d999999999999999999999999")
	if err != nil || withdrawn {
		t.Fatalf("unknown: %v %v", withdrawn, err)
	}
	second := acceptanceFixture("d123456789abcdef012345678", first.ID)
	second.NativeRev = 43
	if err := s.AdmitAcceptanceDecision(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.WithdrawAcceptanceDecision(ctx, 7, 3, first.ID, 5); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("superseded withdraw: %v", err)
	}
	withdrawn, _, err = s.AcceptanceWithdrawn(ctx, 7, 3, second.ID)
	if err != nil || withdrawn {
		t.Fatalf("fresh head latched: %v %v", withdrawn, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM issue_acceptance_decisions WHERE id=$1`, first.ID); err == nil {
		t.Fatal("decision delete accepted")
	}
}
