package store

import (
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// NATIVE112 targeted scope-guard cases only: sameAttemptAllowanceScope must
// keep the immutable nil/present + IntentID + OwnerID equality alongside the
// existing Completion equality, without any early return that bypasses
// Intent. Pure checks only; no store, SQL, or PG fixture.

// intentScopeAllowance builds an independently valid time-only intent
// allowance: zero issue, nonnil Intent, time-only limits.
func intentScopeAllowance(t *testing.T, intentID string, ownerID int64, root string) factory.AttemptAllowance {
	t.Helper()
	now := time.Unix(1700000000, 0)
	a := factory.AttemptAllowance{
		Limits:         factory.AttemptLimits{ActiveMinutes: 45},
		Intent:         &factory.IntentAttemptScope{IntentID: intentID, OwnerID: ownerID},
		Repository:     7,
		RootAssignment: root,
		CheckpointUnix: now.Unix(),
		Active:         true,
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("intent allowance fixture refused: %v", err)
	}
	return a
}

// cloneScopeAllowance copies the Intent/Completion scopes by value so no
// test ever aliases the stored pointer.
func cloneScopeAllowance(a factory.AttemptAllowance) factory.AttemptAllowance {
	out := a
	if a.Intent != nil {
		scope := *a.Intent
		out.Intent = &scope
	}
	if a.Completion != nil {
		subject := *a.Completion
		out.Completion = &subject
	}
	return out
}

func completionScopeAllowance(t *testing.T, root string) factory.AttemptAllowance {
	t.Helper()
	now := time.Unix(1700000000, 0)
	a := factory.AttemptAllowance{
		Limits:         factory.AttemptLimits{ActiveMinutes: 30},
		Completion:     &factory.CompletionSubject{CompletionID: factory.NewID(), CycleID: factory.NewID(), InventoryDigest: strings.Repeat("d", 64)},
		Repository:     7,
		RootAssignment: root,
		CheckpointUnix: now.Unix(),
		Active:         true,
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("completion allowance fixture refused: %v", err)
	}
	return a
}

func issueScopeAllowance(t *testing.T, root string) factory.AttemptAllowance {
	t.Helper()
	now := time.Unix(1700000000, 0)
	a := factory.AttemptAllowance{
		Limits:         factory.DefaultAttemptLimits(),
		Repository:     7,
		Issue:          3,
		RootAssignment: root,
		CheckpointUnix: now.Unix(),
		Active:         true,
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("issue allowance fixture refused: %v", err)
	}
	return a
}

// Two independently built valid intent allowances with the same stable
// scope compare equal, including through a value-copy of the Intent
// pointer.
func TestSameAttemptAllowanceScopeKeepsStableIntent(t *testing.T) {
	intentID := "d" + strings.Repeat("b", 24)
	root := factory.NewID()
	stored := intentScopeAllowance(t, intentID, 7, root)
	next := intentScopeAllowance(t, intentID, 7, root)
	if err := stored.Validate(); err != nil {
		t.Fatalf("stored intent allowance refused: %v", err)
	}
	if err := next.Validate(); err != nil {
		t.Fatalf("next intent allowance refused: %v", err)
	}
	if !sameAttemptAllowanceScope(stored, next) {
		t.Fatal("equal stable intent scopes refused")
	}
	if !sameAttemptAllowanceScope(stored, cloneScopeAllowance(stored)) {
		t.Fatal("stable intent value-copy refused")
	}
}

// The counterexample: changed IntentID or OwnerID must refuse. The
// pre-NATIVE112 helper returned TRUE for both.
func TestSameAttemptAllowanceScopeRefusesIntentMutation(t *testing.T) {
	intentID := "d" + strings.Repeat("c", 24)
	root := factory.NewID()
	stored := intentScopeAllowance(t, intentID, 7, root)
	forgedID := cloneScopeAllowance(stored)
	forgedID.Intent.IntentID = factory.NewID()
	if sameAttemptAllowanceScope(stored, forgedID) {
		t.Fatal("changed IntentID kept the allowance scope")
	}
	forgedOwner := cloneScopeAllowance(stored)
	forgedOwner.Intent.OwnerID = 8
	if sameAttemptAllowanceScope(stored, forgedOwner) {
		t.Fatal("changed OwnerID kept the allowance scope")
	}
	dropped := cloneScopeAllowance(stored)
	dropped.Intent = nil
	if sameAttemptAllowanceScope(stored, dropped) {
		t.Fatal("dropped Intent kept the allowance scope")
	}
	plain := issueScopeAllowance(t, root)
	gained := cloneScopeAllowance(plain)
	gained.Intent = &factory.IntentAttemptScope{IntentID: "d" + strings.Repeat("f", 24), OwnerID: 7}
	if sameAttemptAllowanceScope(plain, gained) {
		t.Fatal("gained Intent kept the allowance scope")
	}
}

// Legitimate advances on the same stable intent stay in scope, and the
// pre-existing issue/completion behavior is unchanged.
func TestSameAttemptAllowanceScopeKeepsLegitimateAdvances(t *testing.T) {
	intentID := "d" + strings.Repeat("e", 24)
	root := factory.NewID()
	stored := intentScopeAllowance(t, intentID, 7, root)
	advanced := cloneScopeAllowance(stored)
	advanced.Revision++
	ticked, err := advanced.Checkpoint(time.Unix(1700000600, 0), false)
	if err != nil {
		t.Fatalf("intent checkpoint refused: %v", err)
	}
	ticked.Revision++
	if err := ticked.Validate(); err != nil {
		t.Fatalf("advanced intent allowance refused: %v", err)
	}
	if !sameAttemptAllowanceScope(stored, ticked) {
		t.Fatal("revision and clock advance on stable intent refused")
	}
	issueRoot := factory.NewID()
	if !sameAttemptAllowanceScope(issueScopeAllowance(t, issueRoot), issueScopeAllowance(t, issueRoot)) {
		t.Fatal("equal issue scopes refused")
	}
	moved := issueScopeAllowance(t, issueRoot)
	moved.Issue = 4
	if sameAttemptAllowanceScope(issueScopeAllowance(t, issueRoot), moved) {
		t.Fatal("changed issue kept the allowance scope")
	}
	completionRoot := factory.NewID()
	kept := completionScopeAllowance(t, completionRoot)
	if !sameAttemptAllowanceScope(kept, cloneScopeAllowance(kept)) {
		t.Fatal("equal completion scopes refused")
	}
	forged := cloneScopeAllowance(kept)
	forged.Completion.CompletionID = factory.NewID()
	if sameAttemptAllowanceScope(kept, forged) {
		t.Fatal("completion identity mutation kept the allowance scope")
	}
}
