package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func deliveryStoreInput(round int) factory.DeliveryFingerprintInput {
	return factory.DeliveryFingerprintInput{
		Reviews:        []string{strings.Repeat("1", 32)},
		Conversations:  []string{strings.Repeat("2", 64)},
		Constraints:    []string{"protected-files:docs/public"},
		AttemptRoot:    strings.Repeat("3", 32),
		PublicationID:  strings.Repeat("4", 32),
		ScopeDigest:    strings.Repeat("5", 64),
		FixDigest:      strings.Repeat("6", 64),
		DiffDigest:     strings.Repeat("7", 64),
		CheckDigest:    strings.Repeat("8", 64),
		TreeDigest:     strings.Repeat("9", 64),
		Head:           strings.Repeat("a", 40),
		Base:           strings.Repeat("b", 40),
		Model:          "jev-test",
		Rubric:         "ready_to_merge only with full evidence cover",
		PolicyRevision: 3,
		Round:          round,
	}
}

func deliveryStoreAnswer(fingerprint string) factory.DeliveryAnswer {
	return factory.DeliveryAnswer{
		Probabilities: map[string]float64{factory.DeliveryReviewAgain: 0.1, factory.DeliveryReadyToMerge: 0.9},
		Fingerprint:   fingerprint,
		Selected:      factory.DeliveryReadyToMerge,
		Model:         "jev-test",
		Probability:   0.9,
		Confidence:    0.85,
	}
}

func deliveryStoreFixture(t *testing.T) (*Store, factory.Assignment) {
	t.Helper()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, context.Background(), db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	return db, a
}

func TestDeliveryJudgmentAdmitRecordRead(t *testing.T) {
	db, a := deliveryStoreFixture(t)
	ctx := context.Background()
	in := deliveryStoreInput(1)
	fingerprint, err := factory.DeliveryFingerprint(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeliveryJudgment(ctx, fingerprint); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreserved read error=%v", err)
	}
	ok, err := db.AdmitDeliveryJudgment(ctx, a.Repository, a.Issue, a.AttemptRoot)
	if err != nil || !ok {
		t.Fatalf("reserve=%v err=%v", ok, err)
	}
	// Admission checks the actual allowance and creates no placeholder.
	ok, err = db.AdmitDeliveryJudgment(ctx, a.Repository, a.Issue, a.AttemptRoot)
	if err != nil || !ok {
		t.Fatalf("retry reserve=%v err=%v", ok, err)
	}
	var rows int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_priority_judgments`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("work admission created a judgment before its answer", rows, err)
	}
	receipt := factory.NewDeliveryReceipt(in, fingerprint, deliveryStoreAnswer(fingerprint), true, 41)
	if err := db.RecordDeliveryJudgment(ctx, receipt); err != nil {
		t.Fatalf("record: %v", err)
	}
	// A completed decision does not change the underlying work allowance.
	if ok, err = db.AdmitDeliveryJudgment(ctx, a.Repository, a.Issue, a.AttemptRoot); err != nil || !ok {
		t.Fatalf("decided admission=%v err=%v", ok, err)
	}
	if err := db.RecordDeliveryJudgment(ctx, receipt); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("double record error=%v", err)
	}
	got, err := db.DeliveryJudgment(ctx, fingerprint)
	if err != nil || !got.CurrentFor(fingerprint) || got.Decision.Effective != factory.DeliveryEffectiveReady {
		t.Fatalf("read=%+v err=%v", got, err)
	}
	// Completed decisions need no placeholder reservation.
	other := deliveryStoreInput(2)
	otherPrint, err := factory.DeliveryFingerprint(other)
	if err != nil {
		t.Fatal(err)
	}
	unreserved := factory.NewDeliveryReceipt(other, otherPrint, deliveryStoreAnswer(otherPrint), true, 41)
	if err := db.RecordDeliveryJudgment(ctx, unreserved); err != nil {
		t.Fatalf("unreserved record error=%v", err)
	}
}

func TestDeliveryJudgmentAllowsIndependentInputs(t *testing.T) {
	db, a := deliveryStoreFixture(t)
	ctx := context.Background()
	for _, round := range []int{1, 2} {
		in := deliveryStoreInput(round)
		fingerprint, err := factory.DeliveryFingerprint(in)
		if err != nil {
			t.Fatal(err)
		}
		if allowed, err := db.AdmitDeliveryJudgment(ctx, a.Repository, a.Issue, a.AttemptRoot); err != nil || !allowed {
			t.Fatal("work allowance refused", allowed, err)
		}
		if err := db.RecordDeliveryJudgment(ctx, factory.NewDeliveryReceipt(in, fingerprint, deliveryStoreAnswer(fingerprint), true, 41)); err != nil {
			t.Fatal(err)
		}
		if got, err := db.DeliveryJudgment(ctx, fingerprint); err != nil || got.Input.Round != round {
			t.Fatal("independent input lost", got, err)
		}
	}
}
