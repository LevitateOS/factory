package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// gateTestMergeRow seeds one open merge row directly through the store,
// bypassing the publication flow: the FW22 authorization tests need a
// recorded row, not a full publish journey.
func gateTestMergeRow(t *testing.T, db *store.Store) factory.Merge {
	t.Helper()
	m := factory.Merge{
		Authority: factory.AuthorityRef{Policy: 2, Operator: 1, Capacity: 1, Sponsorship: 1, Environment: 1},
		Operation: factory.MergeOperation{Kind: factory.OpMerge},
		ID:        factory.NewID(), PublicationID: factory.NewID(), AssignmentID: factory.NewID(),
		ProjectID: "p" + strings.Repeat("d", 24),
		Role:      project.RoleCoder, Acceptance: "d" + strings.Repeat("a", 24),
		HeadRef: "refs/heads/soda/factory/candidate", BaseRef: "refs/heads/main",
		HeadOID: strings.Repeat("2", 40), BaseOID: strings.Repeat("1", 40),
		Stage:      factory.MergeOpen,
		Repository: 7, Issue: 3, PRNumber: 9, PRID: 11, IssueID: 13,
		PRAuthorID: 5, ReviewerID: 6, CreatedUnix: 1100,
	}
	if err := db.RecordMerge(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

// gateTestBinding builds a ready first-generation test authorization for
// the row's exact current head and base. It stands in for the future Jev
// producer through the same decision gate production enforces.
func gateTestBinding(t *testing.T, m factory.Merge) factory.MergeAuthorization {
	t.Helper()
	fingerprint := strings.Repeat("e", 64)
	answer, err := readyDeliveryAnswer(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return factory.MergeAuthorization{
		Generation: factory.MergeGeneration{
			Authority: m.Authority, PublicationID: m.PublicationID,
			DecisionID:        strings.Repeat("d", 32),
			NativeOperationID: factory.MergeGenerationOperationID(m.PublicationID, 1),
			Fingerprint:       fingerprint, Head: m.HeadOID, Base: m.BaseOID, DiffBase: m.BaseOID,
			ChecksDigest:       strings.Repeat("c", 64),
			Generation:         1,
			ReviewRound:        1,
			AssessmentRevision: 2, ObservedNativeRev: 12, NotAfter: 1300,
		},
		Decision:             factory.DeliveryDecision{Answer: answer, Effective: factory.DeliveryEffectiveReady, ReviewsFit: true},
		ResolutionsConfirmed: true,
	}
}

// TestMergeAuthorizationGateClosesUnboundSubmits (T27): missing Jev
// acceptance closes every new submit path. An unbound row waits for its
// authorization instead of submitting; the wait names the missing
// judgment honestly.
func TestMergeAuthorizationGateClosesUnboundSubmits(t *testing.T) {
	db, _ := postgresFixture(t)
	m := gateTestMergeRow(t, db)
	if err := mergeAuthorizationGate(m, factory.Publication{}); err == nil {
		t.Fatal("unbound row passed its authorization gate")
	} else {
		var wait *factory.PublicationWait
		if !errors.As(err, &wait) || wait.Reason != "merge_judgment_unavailable" {
			t.Fatalf("unbound row gave %v, want merge_judgment_unavailable wait", err)
		}
	}
}

// TestMergeAuthorizationGateRefusesStaleTuple (T27): a binding never
// follows its candidate. A moved head or base waits for
// re-authorization behind fresh review and judgment instead of merging
// another candidate.
func TestMergeAuthorizationGateRefusesStaleTuple(t *testing.T) {
	db, _ := postgresFixture(t)
	m := gateTestMergeRow(t, db)
	m.Authorization = gateTestBinding(t, m)
	if err := mergeAuthorizationGate(m, factory.Publication{}); err != nil {
		t.Fatalf("fresh binding refused: %v", err)
	}
	moved := m
	moved.HeadOID = strings.Repeat("4", 40)
	if err := mergeAuthorizationGate(moved, factory.Publication{}); err == nil {
		t.Fatal("moved head passed its stale binding")
	} else {
		var wait *factory.PublicationWait
		if !errors.As(err, &wait) || wait.Reason != "superseded_head" {
			t.Fatalf("moved head gave %v, want superseded_head wait", err)
		}
	}
	rebased := m
	rebased.BaseOID = strings.Repeat("5", 40)
	if err := mergeAuthorizationGate(rebased, factory.Publication{}); err == nil {
		t.Fatal("moved base passed its stale binding")
	} else {
		var wait *factory.PublicationWait
		if !errors.As(err, &wait) || wait.Reason != "superseded_base" {
			t.Fatalf("moved base gave %v, want superseded_base wait", err)
		}
	}
}

// TestMergeAuthorizationGateEnforcesDecisionFreshness (T27): a currently
// invalid acceptance never admits a submit. A review_again decision or
// unconfirmed resolutions hold the row instead of merging.
func TestMergeAuthorizationGateEnforcesDecisionFreshness(t *testing.T) {
	db, _ := postgresFixture(t)
	m := gateTestMergeRow(t, db)
	m.Authorization = gateTestBinding(t, m)
	answer, err := reviewDeliveryAnswer(m.Authorization.Generation.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	review := m
	review.Authorization.Decision = factory.DeliveryDecision{Answer: answer, Effective: factory.DeliveryEffectiveReview, ReviewsFit: true}
	if err := mergeAuthorizationGate(review, factory.Publication{}); err == nil {
		t.Fatal("review_again decision passed the merge gate")
	} else {
		var wait *factory.PublicationWait
		if !errors.As(err, &wait) || wait.Reason != "merge_authorization_stale" {
			t.Fatalf("review_again decision gave %v, want merge_authorization_stale wait", err)
		}
	}
	pending := m
	pending.Authorization.ResolutionsConfirmed = false
	if err := mergeAuthorizationGate(pending, factory.Publication{}); err == nil {
		t.Fatal("unconfirmed resolutions passed the merge gate")
	}
}

// TestAuthorizeMergeBindsFirstGeneration (T27/T28 under the FW37 union):
// the registration seam admits exactly one ready union-bound
// first-generation authorization per row. A second bind, a second
// generation, another candidate, a changed authority, an unready
// decision, unconfirmed resolutions and a binding without its union all
// refuse; nothing replaces a bound predecessor on its row.
func TestAuthorizeMergeBindsFirstGeneration(t *testing.T) {
	ctx := context.Background()
	t.Run("binds ready first generation", func(t *testing.T) {
		fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 51)
		auth := unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err != nil {
			t.Fatalf("ready first generation refused: %v", err)
		}
		stored, err := fx.publish.db.MergeByPublication(ctx, p.ID)
		if err != nil || !stored.Authorization.Bound() || stored.Authorization.Generation.Generation != 1 {
			t.Fatalf("binding not persisted: %+v %v", stored, err)
		}
		fresh, err := fx.publish.db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		if err := mergeAuthorizationGate(stored, fresh); err != nil {
			t.Fatalf("bound row refused its submit gate: %v", err)
		}
	})
	t.Run("never rebinds", func(t *testing.T) {
		fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 52)
		auth := unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err != nil {
			t.Fatal(err)
		}
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("second binding replaced its predecessor")
		}
	})
	t.Run("refuses second generation without ledger history", func(t *testing.T) {
		fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 53)
		auth := unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
		auth.Generation.Generation = 2
		auth.Generation.NativeOperationID = factory.MergeGenerationOperationID(p.ID, 2)
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("second generation bound without its predecessor history")
		}
	})
	t.Run("refuses another candidate", func(t *testing.T) {
		fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 54)
		auth := unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
		auth.Generation.Head = strings.Repeat("4", 40)
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("binding for another candidate accepted")
		}
	})
	t.Run("refuses changed authority", func(t *testing.T) {
		fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 55)
		auth := unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
		auth.Generation.Authority.Policy = 9
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("binding under changed authority accepted")
		}
	})
	t.Run("refuses unready decision and unconfirmed resolutions", func(t *testing.T) {
		fx, p, m, acceptance, decision, fingerprint := unionSeedForBind(t, ctx, 56)
		_ = decision
		auth := unionAuthForBind(t, m, acceptance, factory.DeliveryDecision{}, fingerprint, true)
		answer, err := reviewDeliveryAnswer(fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		auth.Decision = factory.DeliveryDecision{Answer: answer, Effective: factory.DeliveryEffectiveReview, ReviewsFit: true}
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("review_again decision bound")
		}
		auth = unionAuthForBind(t, m, acceptance, factory.DeliveryDecision{}, fingerprint, true)
		answer, err = readyDeliveryAnswer(fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		auth.Decision = factory.DeliveryDecision{Answer: answer, Effective: factory.DeliveryEffectiveReady, ReviewsFit: true}
		auth.ResolutionsConfirmed = false
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("unconfirmed resolutions bound")
		}
		auth = unionAuthForBind(t, m, acceptance, decision, fingerprint, true)
		auth.Decision.Answer.Confidence = factory.DeliveryReadyMinConfidence - 0.01
		auth.Decision.Effective = auth.Decision.Answer.EffectiveAction(true)
		if auth.Decision.Effective != factory.DeliveryEffectiveAttention {
			t.Fatal("low-confidence answer bypassed the delivery cutoff")
		}
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, auth); err == nil {
			t.Fatal("low-confidence decision bound a merge")
		}
	})
	t.Run("refuses a binding without its union", func(t *testing.T) {
		fx, p, m, _, _, _ := unionSeedForBind(t, ctx, 57)
		if err := fx.publish.coord.AuthorizeMerge(ctx, p.ID, gateTestBinding(t, m)); err == nil {
			t.Fatal("unionless binding registered")
		}
	})
}

// TestWithdrawUnboundRowCancelsWithoutGate (T27/T28): cancellation is
// the escape hatch, never an admission. Withdrawing an unbound,
// undriven row finishes it without consulting any judgment; the
// confirmed cancel is what a future replacement must prove.
func TestWithdrawUnboundRowCancelsWithoutGate(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	m := gateTestMergeRow(t, db)
	coord := &Coordinator{Store: db}
	report := &MergeReport{Merged: []MergeLink{}}
	coord.withdrawMerge(ctx, &m, report)
	if len(report.Errors) != 0 {
		t.Fatalf("withdrawal errored: %+v", report)
	}
	if len(report.Waits) != 0 {
		t.Fatalf("withdrawal waited on judgment: %+v", report)
	}
	stored, err := db.MergeByPublication(ctx, m.PublicationID)
	if err != nil || stored.Stage != factory.MergeWithdrawn || stored.Outcome != factory.Cancelled {
		t.Fatalf("unbound row not withdrawn: %+v %v", stored, err)
	}
}

// TestAdoptCommittedMergeAfterLostReplyWithoutRewrite (T29): a recorded
// committed merge is adopted on its own identity after a lost reply. No
// observation rewrites a committed effect, flips it, or attributes it
// to another installation, actor or operation.
func TestAdoptCommittedMergeAfterLostReplyWithoutRewrite(t *testing.T) {
	work := &factory.MergeIntent{
		OperationID: factory.MergeOperationID(strings.Repeat("9", 32), 1), AuthRevision: "accepted-revision",
		HeadRef: "refs/heads/soda/factory/candidate", BaseRef: "refs/heads/main",
		HeadOID: strings.Repeat("2", 40), BaseOID: strings.Repeat("1", 40),
		Repository: 7, ActorID: 8, PRNumber: 9, PRID: 11, IssueID: 13,
		PRAuthorID: 5, ReviewerID: 6,
		NativeRev: 12, NotAfter: 1300, AssessmentRevision: 2, ReviewID: 21,
	}
	op := &factory.MergeOperation{Kind: factory.OpMerge, OperationID: work.OperationID, Work: work}
	outcome := factory.OperationOutcome{
		OperationID: work.OperationID, InstallationID: "test-installation", Kind: factory.OpMerge,
		ActorID: 8, RepositoryID: 7, Effect: factory.OpEffectCommitted,
		Cancellation: factory.OpCancelNone, Completion: factory.OpCompletionComplete, Reason: "merged",
		Receipt: []byte(`{"pr_number":9}`),
	}
	coord := &Coordinator{}
	if !coord.adoptMergeObserved(op, outcome, time.Now()) {
		t.Fatal("committed merge not adopted after its lost reply")
	}
	if op.Effect != factory.OpEffectCommitted || op.InstallationID != "test-installation" {
		t.Fatalf("adopted effect rewritten: %+v", op)
	}
	// A later observation from another installation never rewrites the
	// recorded one; the first observation owns the attribution.
	foreign := outcome
	foreign.InstallationID = "other-installation"
	if coord.adoptMergeObserved(op, foreign, time.Now()) {
		t.Fatal("foreign installation rewrote its merge")
	}
	flipped := outcome
	flipped.Effect = factory.OpEffectNotCommitted
	if coord.adoptMergeObserved(op, flipped, time.Now()) {
		t.Fatal("committed effect flipped to not_committed")
	}
	unseen := outcome
	unseen.NotObserved = true
	if coord.adoptMergeObserved(op, unseen, time.Now()) {
		t.Fatal("unobserved lookup adopted")
	}
	if op.Effect != factory.OpEffectCommitted {
		t.Fatalf("refused observations rewrote the effect: %q", op.Effect)
	}
}

// TestCompleteMergeWaitsForClosureVerification (T29): a committed merge
// still verifies its closure before finishing. While native completion
// stays pending the merge waits with its bookkeeping intact; recovery
// never enters the authorization gate.
func TestCompleteMergeWaitsForClosureVerification(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	m := gateTestMergeRow(t, db)
	operationID := factory.MergeOperationID(m.PublicationID, 1)
	m.Operation = factory.MergeOperation{
		Kind: factory.OpMerge, OperationID: operationID, Effect: factory.OpEffectCommitted,
		Cancellation: factory.OpCancelNone, Completion: factory.OpCompletionPending,
	}
	coord := &Coordinator{Store: db, Merges: happyMerger()}
	report := &MergeReport{Merged: []MergeLink{}}
	coord.completeMerge(ctx, &m, report)
	if len(report.Errors) != 0 {
		t.Fatalf("completion check errored: %+v", report)
	}
	found := false
	for _, wait := range report.Waits {
		found = found || (wait.ID == m.ID && wait.Reason == "native_completion_pending")
	}
	if !found {
		t.Fatalf("pending completion misadvanced: %+v", report)
	}
	if len(report.Merged) != 0 {
		t.Fatalf("unverified merge linked: %+v", report)
	}
}
