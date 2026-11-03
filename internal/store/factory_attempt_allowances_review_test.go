package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// TestReviewDispatchChargeOncePerImmutableAssignment proves the T44
// store leg: ChargeReviewDispatch charges the attempt-root review
// allowance exactly once per immutable reviewer assignment identity —
// first inspections spend ReviewRounds but never the extra pass, one
// discretionary extra pass per head spends once across any later round,
// policy-required passes stay separately caused, replays never recharge
// even after exhaustion, and exhaustion withholds without an error.
func TestReviewDispatchChargeOncePerImmutableAssignment(t *testing.T) {
	ctx := context.Background()
	db := publicationStoreFixture(t)
	now := time.Now().UTC()
	root := recordFinishedAssignment(t, db, now, 3, true, "completed")
	head := strings.Repeat("e", 40)

	charge := func(reviewer, chargeHead string, policy bool) (bool, bool, error) {
		return db.ChargeReviewDispatch(ctx, root.Repository, root.Issue, root.AttemptRoot, reviewer, chargeHead, policy)
	}
	read := func() factory.AttemptAllowance {
		allowance, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
		if err != nil {
			t.Fatal(err)
		}
		return allowance
	}

	first := factory.NewID()
	consumed, exhausted, err := charge(first, head, false)
	if err != nil || !consumed || exhausted {
		t.Fatalf("first inspection charge = %v,%v,%v", consumed, exhausted, err)
	}
	if got := read(); len(got.Reviews) != 1 || len(got.UnchangedHeadExtra) != 0 || got.ReviewedHeads[head] != first {
		t.Fatalf("first inspection ledger = %+v", got)
	}
	// Replay of the same immutable assignment never recharges.
	consumed, exhausted, err = charge(first, head, false)
	if err != nil || consumed || exhausted {
		t.Fatalf("replay charge = %v,%v,%v", consumed, exhausted, err)
	}
	if got := read(); len(got.Reviews) != 1 {
		t.Fatalf("replay recharged: %+v", got)
	}

	extra := factory.NewID()
	consumed, exhausted, err = charge(extra, head, false)
	if err != nil || !consumed || exhausted {
		t.Fatalf("single extra charge = %v,%v,%v", consumed, exhausted, err)
	}
	if got := read(); got.UnchangedHeadExtra[head] != extra {
		t.Fatalf("extra pass ledger = %+v", got)
	}
	// A second discretionary inspection of the same head withholds
	// without an error, however the round behind it changed.
	consumed, exhausted, err = charge(factory.NewID(), head, false)
	if err != nil || consumed || !exhausted {
		t.Fatalf("second extra charge = %v,%v,%v", consumed, exhausted, err)
	}
	// The separately caused policy pass still inspects the spent head,
	// spending only its round.
	policy := factory.NewID()
	consumed, exhausted, err = charge(policy, head, true)
	if err != nil || !consumed || exhausted {
		t.Fatalf("policy charge = %v,%v,%v", consumed, exhausted, err)
	}
	if got := read(); len(got.UnchangedHeadExtra) != 1 || len(got.Reviews) != 3 {
		t.Fatalf("policy charge touched the extra ledger: %+v", got)
	}

	// New heads inspect until ReviewRounds (default 6) is spent: three
	// charges here reach exactly six rounds, so the next withholds.
	for i, newHead := range []string{strings.Repeat("f", 40), strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		consumed, exhausted, err = charge(factory.NewID(), newHead, false)
		if err != nil || !consumed || exhausted {
			t.Fatalf("new-head charge %d = %v,%v,%v", i, consumed, exhausted, err)
		}
	}
	if got := read(); len(got.Reviews) != factory.DefaultReviewRounds {
		t.Fatalf("review ledger = %+v", got)
	}
	consumed, exhausted, err = charge(factory.NewID(), strings.Repeat("c", 40), false)
	if err != nil || consumed || !exhausted {
		t.Fatalf("over-round charge = %v,%v,%v", consumed, exhausted, err)
	}
	consumed, exhausted, err = charge(first, head, false)
	if err != nil || consumed || exhausted {
		t.Fatalf("exhausted replay charge = %v,%v,%v", consumed, exhausted, err)
	}

	for name, tc := range map[string]struct {
		repo, issue    int64
		root, reviewer string
		head           string
	}{
		"bad repository": {0, root.Issue, root.AttemptRoot, first, head},
		"bad root":       {root.Repository, root.Issue, "bad", first, head},
		"bad assignment": {root.Repository, root.Issue, root.AttemptRoot, "bad", head},
		"bad head":       {root.Repository, root.Issue, root.AttemptRoot, first, "bad"},
	} {
		if _, _, err := db.ChargeReviewDispatch(ctx, tc.repo, tc.issue, tc.root, tc.reviewer, tc.head, false); err == nil {
			t.Fatalf("%s charged", name)
		}
	}
}
