package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// recordGenerationIntent stores one generation's undriven intent behind
// its derived operation identity. It re-reads the row so the intent
// lands on the current revision behind the bound authorization.
func recordGenerationIntent(t *testing.T, ctx context.Context, db *store.Store, m factory.Merge) factory.Merge {
	t.Helper()
	current, err := db.MergeByPublication(ctx, m.PublicationID)
	if err != nil {
		t.Fatal(err)
	}
	m = current
	n := 1
	if m.Authorization.Bound() {
		n = m.Authorization.Generation.Generation
	}
	operationID := factory.MergeOperationIDForGeneration(m.PublicationID, n)
	intent := &factory.MergeIntent{
		OperationID: operationID, AuthRevision: "accepted-revision",
		HeadRef: m.HeadRef, BaseRef: m.BaseRef,
		HeadOID: m.HeadOID, BaseOID: m.BaseOID,
		Repository: m.Repository, ActorID: 8, PRNumber: m.PRNumber, PRID: m.PRID, IssueID: m.IssueID,
		PRAuthorID: m.PRAuthorID, ReviewerID: m.ReviewerID,
		NativeRev: 12, NotAfter: time.Now().Add(10 * time.Minute).Unix(), AssessmentRevision: 2, ReviewID: 21,
	}
	m.Operation = factory.MergeOperation{Kind: factory.OpMerge, Attempts: 1, OperationID: operationID, UpdatedUnix: 1200, Work: intent}
	m.Revision++
	if err := db.UpdateMerge(ctx, m); err != nil {
		t.Fatal(err)
	}
	stored, err := db.MergeByPublication(ctx, m.PublicationID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

// TestUpdateMergeRechecksUnionBindingInTx proves the registration
// transaction re-verifies the journaled D07 acceptance independently of
// the seam: a union binding for unjournaled acceptance refuses on
// commit, while the journaled binding commits through the same path.
func TestUpdateMergeRechecksUnionBindingInTx(t *testing.T) {
	ctx := context.Background()
	fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 66)
	db := fx.publish.db
	stray := unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
	stray.Generation.AcceptanceID = strings.Repeat("7", 32)
	m.Revision++
	bound := m
	bound.Authorization = stray
	if err := db.UpdateMerge(ctx, bound); err == nil {
		t.Fatal("unjournaled union binding committed")
	}
	if got, _ := db.MergeByPublication(ctx, p.ID); got.Authorization.Bound() {
		t.Fatal("refused binding persisted")
	}
	bound.Authorization = unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
	if err := db.UpdateMerge(ctx, bound); err != nil {
		t.Fatalf("journaled union binding refused in its transaction: %v", err)
	}
}

// TestCommittedMergeRecoversDespiteWithdrawal proves a recorded
// committed merge still confirms after a later invalidation — here a
// dispatch withdrawal closing every new submit. Recovery reuses the
// exact recorded operation by lookup: one lookup, no resubmit, no new
// judgment, and no replacement generation.
func TestCommittedMergeRecoversDespiteWithdrawal(t *testing.T) {
	ctx := context.Background()
	fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 67)
	db := fx.publish.db
	exec := happyMerger()
	fx.wire(exec)
	if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, unionAuthForBind(t, m, acceptance, decision, fingerprint, true)); err != nil {
		t.Fatal(err)
	}
	driven := recordGenerationIntent(t, ctx, db, m)
	operationID := driven.Operation.OperationID
	exec.ledger[operationID] = factory.OperationOutcome{
		OperationID: operationID, InstallationID: "test-installation", Kind: factory.OpMerge,
		ActorID: 8, RepositoryID: driven.Repository, Effect: factory.OpEffectCommitted,
		Cancellation: factory.OpCancelNone, Completion: factory.OpCompletionComplete, Reason: "merged",
		Receipt: []byte(`{"pr_number":9}`),
	}
	if _, err := db.WithdrawDispatch(ctx, driven.Repository, "test withdrawal", "soda-tester"); err != nil {
		t.Fatal(err)
	}
	report := &MergeReport{Merged: []MergeLink{}}
	fx.publish.coord.reconcileMerge(ctx, driven, report)
	if len(report.Errors) != 0 {
		t.Fatalf("recovery errored: %+v", report)
	}
	recovered, err := db.MergeByPublication(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Stage != factory.MergeMerged || recovered.MergedCommit != recovered.HeadOID {
		t.Fatalf("committed merge not recovered: %+v", recovered)
	}
	if recovered.Operation.OperationID != operationID || len(recovered.History) != 0 {
		t.Fatalf("recovery replaced its exact operation: %+v", recovered.Operation)
	}
	if len(exec.submits) != 0 {
		t.Fatalf("recovery resubmitted: %d", len(exec.submits))
	}
	// Recovery reuses the exact recorded operation by identity: the
	// lookup path, the cancellation check, or both — never another
	// identity and never a resubmission.
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, exec.lookups...), exec.cancels...) {
		if id != operationID {
			t.Fatalf("recovery touched another operation: %+v", append(exec.lookups, exec.cancels...))
		}
		seen[id] = true
	}
	if !seen[operationID] {
		t.Fatalf("recovery bypassed its exact operation: lookups=%+v cancels=%+v", exec.lookups, exec.cancels)
	}
	if len(report.Merged) != 1 || report.Merged[0].MergeID != recovered.ID {
		t.Fatalf("recovery unlinked: %+v", report)
	}
}
