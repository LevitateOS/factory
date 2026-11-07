package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// NATIVE95 proves the source-qualified completion capture on the
// disposable PG fixture (SODA_PG_*; skips without it): the authority
// context round-trips stably, the ready-base tip seals, head/policy
// moves refuse without fallback, a missing base holds, a changed tip
// without inputs or without a reconciled successful coder
// publication/merge refuses, and the exact merged coder tip seals with
// the tip bound while the accepted requirement source stays distinct.

func sourceInputDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

const sourceTestInputPath = "config/dependencies.lock"

// admitSourceHeads records a new requirement decision (carrying the
// given exact source selection) plus its verified approval, advancing
// both project heads past the dispatch fixture seed.
func admitSourceHeads(t *testing.T, db *Store, reqID, reqCommit string, inputs map[string]string, apprID string) {
	t.Helper()
	ctx := context.Background()
	projectID := dispatchTestProjectID()
	head, err := db.RequirementHead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	requirement := project.RequirementDecision{
		ID: reqID, Predecessor: head, Project: projectID, Approver: 7,
		SourceCommit: reqCommit, SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64),
		SourceInputs: inputs,
	}
	if err := db.AdmitRequirementDecision(ctx, requirement); err != nil {
		t.Fatal(err)
	}
	approvalHead, err := db.ApprovalHead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	approval := project.ApprovalDecision{
		ID: apprID, Predecessor: approvalHead, Project: projectID, Requirement: reqID,
		Approver: 7, EffectsDigest: strings.Repeat("a", 64), ReadinessDigest: strings.Repeat("b", 64), Verified: true,
	}
	if err := db.AdmitApprovalDecision(ctx, approval); err != nil {
		t.Fatal(err)
	}
}

// admitReadySourcePreparation admits a standing coder preparation
// against the current heads and observes it ready through the honest
// observation path: no caller observation seeds readiness, the state
// advances under CAS revision from the admitted identity.
func admitReadySourcePreparation(t *testing.T, db *Store, prepID string) execution.PreparationRecord {
	t.Helper()
	ctx := context.Background()
	projectID := dispatchTestProjectID()
	reqHead, err := db.RequirementHead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := db.RequirementDecision(ctx, reqHead)
	if err != nil {
		t.Fatal(err)
	}
	reqDepth, err := db.RequirementDepth(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	apprHead, err := db.ApprovalHead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := db.ApprovalDecision(ctx, apprHead)
	if err != nil {
		t.Fatal(err)
	}
	apprDepth, err := db.ApprovalDepth(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	prep := project.Preparation{
		ID: prepID, Project: projectID, Role: project.RoleCoder, Revision: 0,
		Requirements: requirement.Ref(reqDepth), Approval: approval.Ref(apprDepth),
		SourceCommit: requirement.SourceCommit, SetupDigest: requirement.SetupDigest,
	}
	stored, _, err := db.AdmitStandingPreparation(ctx, execution.PreparationRecord{Preparation: prep})
	if err != nil {
		t.Fatalf("admit standing preparation: %v", err)
	}
	stored.State = execution.PreparationState{
		ID: prepID, Project: projectID, Role: project.RoleCoder,
		Phase: project.PrepareReady, Ready: true,
		Container:    strings.Repeat("c", 64),
		SourceCommit: requirement.SourceCommit, SetupDigest: requirement.SetupDigest,
	}
	if err := db.ObservePreparation(ctx, stored); err != nil {
		t.Fatalf("observe preparation ready: %v", err)
	}
	observed, err := db.Preparation(ctx, prepID)
	if err != nil || !observed.State.Ready {
		t.Fatalf("preparation not ready: %+v %v", observed, err)
	}
	return observed
}

// seedSourcePacket records one honest dispatch packet (assignment,
// reservation, run and view) plus its open coder publication proving
// the exact candidate tip over its base. The material projection joins
// publications to their assignments, so the packet is the honest
// anchor, not decoration.
func seedSourcePacket(t *testing.T, db *Store, issue int64, acceptance, prepID, tip, base string) factory.Publication {
	t.Helper()
	p := publicationTestRecord()
	p.AssignmentID = factory.NewID()
	p.Repository, p.Issue = 7, issue
	p.ProjectID = dispatchTestProjectID()
	p.Acceptance = acceptance
	p.Preparation = prepID
	p.Run = factory.NewID()
	p.Candidate, p.BaseSHA = tip, base
	// The packet admission checks authority against the CURRENT heads,
	// which the new requirement/approval seeds moved: bind current.
	p.Authority = dispatchCurrentAuthority(t, db, factory.Assignment{
		Repository: 7, Connection: "conn", ProjectID: dispatchTestProjectID(),
	})
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(context.Background(), p); err != nil {
		t.Fatalf("record publication: %v", err)
	}
	return p
}

// finishSourceRun advances the packet run to the given reconciled
// outcome through the honest supervisor observation path.
func finishSourceRun(t *testing.T, db *Store, runID string, reconciled bool, outcome factory.Outcome) {
	t.Helper()
	ctx := context.Background()
	run, err := db.FactoryRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	run.Reconciled, run.Outcome = reconciled, outcome
	if outcome != "" {
		run.Summary = "coder run finished"
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("finished seed run invalid: %v", err)
	}
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatalf("finish run: %v", err)
	}
}

// seedMergedSourceMerge inserts one merged merge row for the exact
// publication tip. The value validates fully before the insert, so the
// row carries the same confirmed completion shape the merge journey
// records; table triggers guard only later updates and deletes, never
// this insert.
func seedMergedSourceMerge(t *testing.T, db *Store, p factory.Publication, tip, base string) factory.Merge {
	t.Helper()
	return seedMergedSourceMergeFor(t, db, p, dispatchTestProjectID(), tip, base)
}

// CompletionSourceContext is stable over one disposable database: the
// projected authority validates, repeats canonically, and an unknown
// or invalid repository refuses instead of projecting emptiness.
func TestCompletionSourceContextStable(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	first, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("context invalid: %v", err)
	}
	second, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatalf("second context: %v", err)
	}
	want, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("authority context is not stable")
	}
	if _, err := db.CompletionSourceContext(ctx, 777); err == nil {
		t.Fatal("unknown repository projected a context instead of refusing")
	}
	if _, err := db.CompletionSourceContext(ctx, 0); err == nil {
		t.Fatal("invalid repository projected a context instead of refusing")
	}
}

// The exact ready-base tip seals: tip == standing preparation source
// is the approved base, and the seal equals the plain local slice.
func TestCaptureCompletionLocalAtSourceReadyBase(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	admitReadySourcePreparation(t, db, "f"+strings.Repeat("1", 24))
	tip := strings.Repeat("1", 40)
	expected, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	local, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, tip)
	if err != nil {
		t.Fatalf("ready-base capture: %v", err)
	}
	if err := local.Validate(); err != nil {
		t.Fatalf("ready-base local invalid: %v", err)
	}
	if local.SourceCommit != tip {
		t.Fatalf("source=%q, want the proven tip", local.SourceCommit)
	}
	if local.Material.Authority.RequirementSourceCommit != tip {
		t.Fatalf("requirement source=%q, want the accepted source", local.Material.Authority.RequirementSourceCommit)
	}
	plain, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !local.Equal(plain) {
		t.Fatal("ready-base seal differs from the plain local slice")
	}
}

// A moved requirement head or policy revision refuses the stale
// context with the named change error: no fallback, no sealed output,
// and no tip or JSON bytes in the refusal.
func TestCaptureCompletionLocalAtSourceRefusesStaleContext(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	admitReadySourcePreparation(t, db, "f"+strings.Repeat("2", 24))
	tip := strings.Repeat("1", 40)
	expected, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	admitSourceHeads(t, db, "d"+strings.Repeat("4", 24), strings.Repeat("3", 40), nil, "d"+strings.Repeat("5", 24))
	stale, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, tip)
	if !errors.Is(err, ErrCompletionAuthorityChanged) {
		t.Fatalf("stale=%+v err=%v, want the authority-changed refusal", stale, err)
	}
	if stale.Sealed() {
		t.Fatal("stale context sealed output instead of refusing")
	}
	if strings.Contains(err.Error(), tip) || strings.Contains(err.Error(), "{") {
		t.Fatalf("refusal leaks capture bytes: %v", err)
	}
	fresh, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := db.RepositoryPolicy(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, fresh, tip); !errors.Is(err, ErrCompletionAuthorityChanged) {
		t.Fatalf("policy move err=%v, want the authority-changed refusal", err)
	}
}

// Without any standing ready preparation the capture holds: a missing
// matching base never seals from a fabricated preparation.
func TestCaptureCompletionLocalAtSourceHoldsWithoutBase(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	expected, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, strings.Repeat("1", 40)); !errors.Is(err, ErrCompletionSourceHold) {
		t.Fatalf("baseless err=%v, want the source hold", err)
	}
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, "not-a-commit"); err == nil {
		t.Fatal("malformed tip captured instead of refusing")
	}
	bad := expected
	bad.TargetBranch = ""
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, bad, strings.Repeat("1", 40)); err == nil {
		t.Fatal("invalid context captured instead of refusing")
	}
}

// A changed tip with no approved input selection on either side
// refuses: absence permits the exact approved source only, never a
// refresh onto an unproven tip.
func TestCaptureCompletionLocalAtSourceRefusesChangedTipWithoutInputs(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	admitSourceHeads(t, db, "d"+strings.Repeat("4", 24), strings.Repeat("3", 40), nil, "d"+strings.Repeat("5", 24))
	prep := admitReadySourcePreparation(t, db, "f"+strings.Repeat("6", 24))
	if len(prep.Preparation.Requirements.SourceInputs) != 0 {
		t.Fatal("seed preparation carries inputs it should not")
	}
	expected, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, strings.Repeat("4", 40)); !errors.Is(err, ErrCompletionSourceHold) {
		t.Fatalf("inputless changed tip err=%v, want the source hold", err)
	}
}

// changedTipFixture seeds heads carrying the exact approved input
// selection plus its ready preparation, returning the context and a
// changed tip carrying no proof yet.
func changedTipFixture(t *testing.T) (*Store, factory.CompletionAuthority, string) {
	t.Helper()
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	inputs := map[string]string{sourceTestInputPath: sourceInputDigest("selected configuration")}
	admitSourceHeads(t, db, "d"+strings.Repeat("4", 24), strings.Repeat("3", 40), inputs, "d"+strings.Repeat("5", 24))
	prep := admitReadySourcePreparation(t, db, "f"+strings.Repeat("6", 24))
	if prep.Preparation.Requirements.SourceInputs[sourceTestInputPath] != inputs[sourceTestInputPath] {
		t.Fatal("seed preparation lost its approved input selection")
	}
	expected, err := db.CompletionSourceContext(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	return db, expected, strings.Repeat("4", 40)
}

// An unknown changed tip with a complete input selection still holds:
// selection alone never proves the tip.
func TestCaptureCompletionLocalAtSourceRefusesUnknownTip(t *testing.T) {
	db, expected, tip := changedTipFixture(t)
	if _, err := db.CaptureCompletionLocalAtSource(context.Background(), 7, expected, tip); !errors.Is(err, ErrCompletionSourceHold) {
		t.Fatalf("unknown tip err=%v, want the source hold", err)
	}
}

// A changed tip whose only publication ran to failure refuses: the
// merged preparation source validator admits reconciled successful
// coder runs only.
func TestCaptureCompletionLocalAtSourceRefusesFailedTip(t *testing.T) {
	db, expected, tip := changedTipFixture(t)
	ctx := context.Background()
	p := seedSourcePacket(t, db, 3, "d"+strings.Repeat("a", 24), "f"+strings.Repeat("6", 24), tip, strings.Repeat("3", 40))
	finishSourceRun(t, db, p.Run, false, factory.Failed)
	seedMergedSourceMerge(t, db, p, tip, strings.Repeat("3", 40))
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, tip); !errors.Is(err, ErrCompletionSourceHold) {
		t.Fatalf("failed tip err=%v, want the source hold", err)
	}
}

// A changed tip whose successful run never reconciled refuses:
// completion-grade proof needs reconciliation, not just success.
func TestCaptureCompletionLocalAtSourceRefusesUnreconciledTip(t *testing.T) {
	db, expected, tip := changedTipFixture(t)
	ctx := context.Background()
	p := seedSourcePacket(t, db, 3, "d"+strings.Repeat("a", 24), "f"+strings.Repeat("6", 24), tip, strings.Repeat("3", 40))
	finishSourceRun(t, db, p.Run, false, factory.Succeeded)
	seedMergedSourceMerge(t, db, p, tip, strings.Repeat("3", 40))
	if _, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, tip); !errors.Is(err, ErrCompletionSourceHold) {
		t.Fatalf("unreconciled tip err=%v, want the source hold", err)
	}
}

// The exact merged successful coder tip seals: the local source is the
// tip while the accepted requirement source and delivered provenance
// stay distinct, and a same-commit off-target merge never supplies
// the proof. (A same-repo second project is impossible: projects are
// unique per repository, so target exactness is the decoy that fits.)
func TestCaptureCompletionLocalAtSourceMergedCoderTip(t *testing.T) {
	for _, method := range []string{"", "squash"} {
		t.Run("method/"+method, func(t *testing.T) {
			db, expected, tip := changedTipFixture(t)
			ctx := context.Background()
			base := strings.Repeat("3", 40)
			p := seedSourcePacket(t, db, 3, "d"+strings.Repeat("a", 24), "f"+strings.Repeat("6", 24), tip, base)
			finishSourceRun(t, db, p.Run, true, factory.Succeeded)
			merged := seedMergedSourceMerge(t, db, p, tip, base)
			if method != "" {
				merged.Operation.Work.Method = method
				tip = strings.Repeat("9", 40)
				merged.MergedCommit, merged.Operation.MergedCommit = tip, tip
				raw, err := json.Marshal(merged)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.db.ExecContext(ctx, `UPDATE factory_merges SET data=$1 WHERE publication=$2`, raw, p.ID); err != nil {
					t.Fatal(err)
				}
			}
			// One factory session per repository is a product invariant, so the
			// finished main packet releases its reservation before the decoy
			// packet admits. The release touches no material or proof binding.
			if err := db.ReleaseReservation(ctx, p.AssignmentID); err != nil {
				t.Fatal(err)
			}
			seedOffTargetDecoy(t, db, p.Preparation, tip, base)
			local, err := db.CaptureCompletionLocalAtSource(ctx, 7, expected, tip)
			if err != nil {
				t.Fatalf("merged coder tip: %v", err)
			}
			if err := local.Validate(); err != nil {
				t.Fatalf("merged local invalid: %v", err)
			}
			if local.SourceCommit != tip {
				t.Fatalf("source=%q, want the proven tip", local.SourceCommit)
			}
			found := false
			for _, projected := range local.Material.Merges {
				if projected.ID == merged.ID {
					found = projected.Method == method && projected.MergedCommit == tip && projected.HeadOID == p.Candidate
				}
			}
			if !found {
				t.Fatalf("candidate/result/method provenance lost: %+v", local.Material.Merges)
			}
			exported, err := db.MergedPreparationSource(ctx, 7, p.ProjectID, merged.BaseRef, tip)
			if err != nil || exported.Export.Candidate != p.Candidate || exported.Commit != tip || exported.MergeID != merged.ID {
				t.Fatalf("merged result must not invent a coder export: %+v %v", exported, err)
			}
			tx, err := db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			registration := CandidatePreparationRegistration{Repository: 7, Project: p.ProjectID, SourceMerge: &exported}
			if err = checkMergedPreparationSourceTx(ctx, tx, registration, tip, merged.BaseRef); err != nil {
				t.Fatalf("recorded merged source refused: %v", err)
			}
			other := exported
			other.MergeID = factory.NewID()
			registration.SourceMerge = &other
			if err = checkMergedPreparationSourceTx(ctx, tx, registration, tip, merged.BaseRef); !errors.Is(err, ErrCandidatePreparationConflict) {
				t.Fatalf("another merge identity admitted: %v", err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if local.Material.Authority.RequirementSourceCommit != base {
				t.Fatalf("requirement source=%q, want the accepted source to stay distinct", local.Material.Authority.RequirementSourceCommit)
			}
			plain, err := db.CaptureCompletionLocal(ctx, 7)
			if err != nil {
				t.Fatal(err)
			}
			if local.Equal(plain) {
				t.Fatal("merged seal equals the plain slice instead of binding the tip")
			}
			if local.ScopeDigest != plain.ScopeDigest || local.PreparationDigest != plain.PreparationDigest ||
				local.AuthorityFingerprint != plain.AuthorityFingerprint {
				t.Fatal("merged seal moved bindings beyond the proven tip")
			}
		})
	}
}

// seedOffTargetDecoy records a same-commit merged publication against
// another target branch: the exact repo/project/target/tip predicate
// must never choose it, even though its commit matches.
func seedOffTargetDecoy(t *testing.T, db *Store, preparation, tip, base string) {
	t.Helper()
	p := seedSourcePacket(t, db, 5, "d"+strings.Repeat("b", 24), preparation, tip, base)
	now := time.Now().Unix()
	operationID := factory.MergeOperationID(p.ID, 1)
	offTarget := "refs/heads/other"
	intent := factory.MergeIntent{
		OperationID: operationID, AuthRevision: "accepted-revision",
		HeadRef: "refs/heads/soda/factory/candidate", BaseRef: offTarget,
		HeadOID: tip, BaseOID: base,
		Repository: 7, ActorID: 8, PRNumber: 9, PRID: 11, IssueID: 13,
		PRAuthorID: 5, ReviewerID: 6,
		NativeRev: 12, NotAfter: time.Now().Add(10 * time.Minute).Unix(), AssessmentRevision: 2, ReviewID: 21,
	}
	m := factory.Merge{
		Authority: dispatchTestAuthority(),
		Operation: factory.MergeOperation{
			Kind: factory.OpMerge, Attempts: 1, OperationID: operationID, UpdatedUnix: now, Work: &intent,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, MergedCommit: tip,
			PRNumber: 9, PRID: 11, IssueID: 13,
			HeadRef: "refs/heads/soda/factory/candidate", BaseRef: offTarget,
			HeadOID: tip, BaseOID: base,
		},
		ID: factory.NewID(), PublicationID: p.ID, AssignmentID: p.AssignmentID,
		ProjectID: dispatchTestProjectID(), Role: project.RoleCoder, Acceptance: p.Acceptance,
		HeadRef: "refs/heads/soda/factory/candidate", BaseRef: offTarget,
		HeadOID: tip, BaseOID: base, MergedCommit: tip,
		Stage: factory.MergeMerged, Outcome: factory.Succeeded, Reason: factory.MergeReasonMerged,
		Repository: 7, Issue: 5, PRNumber: 9, PRID: 11, IssueID: 13,
		PRAuthorID: 5, ReviewerID: 6, Revision: 1, CreatedUnix: now,
		FinishedUnix: now, MergedUnix: now, ClosedUnix: now,
		OriginalClosingOccurrence: "901", OriginalClosedUnix: now,
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("decoy merge invalid: %v", err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(context.Background(), `INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		m.PublicationID, m.Repository, m.Issue, m.PRNumber, m.Stage, m.Revision, string(raw)); err != nil {
		t.Fatal(err)
	}
}

// seedMergedSourceMergeFor seeds the merged merge row for an explicit
// project: the shared base-tip shape with the exact project binding.
func seedMergedSourceMergeFor(t *testing.T, db *Store, p factory.Publication, projectID, tip, base string) factory.Merge {
	t.Helper()
	now := time.Now().Unix()
	operationID := factory.MergeOperationID(p.ID, 1)
	intent := factory.MergeIntent{
		OperationID: operationID, AuthRevision: "accepted-revision",
		HeadRef: "refs/heads/soda/factory/candidate", BaseRef: "refs/heads/main",
		HeadOID: tip, BaseOID: base,
		Repository: 7, ActorID: 8, PRNumber: 9, PRID: 11, IssueID: 13,
		PRAuthorID: 5, ReviewerID: 6,
		NativeRev: 12, NotAfter: time.Now().Add(10 * time.Minute).Unix(), AssessmentRevision: 2, ReviewID: 21,
	}
	m := factory.Merge{
		Authority: dispatchTestAuthority(),
		Operation: factory.MergeOperation{
			Kind: factory.OpMerge, Attempts: 1, OperationID: operationID, UpdatedUnix: now, Work: &intent,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, MergedCommit: tip,
			PRNumber: 9, PRID: 11, IssueID: 13,
			HeadRef: "refs/heads/soda/factory/candidate", BaseRef: "refs/heads/main",
			HeadOID: tip, BaseOID: base,
		},
		ID: factory.NewID(), PublicationID: p.ID, AssignmentID: p.AssignmentID,
		ProjectID: projectID, Role: project.RoleCoder, Acceptance: p.Acceptance,
		HeadRef: "refs/heads/soda/factory/candidate", BaseRef: "refs/heads/main",
		HeadOID: tip, BaseOID: base, MergedCommit: tip,
		Stage: factory.MergeMerged, Outcome: factory.Succeeded, Reason: factory.MergeReasonMerged,
		Repository: 7, Issue: 3, PRNumber: 9, PRID: 11, IssueID: 13,
		PRAuthorID: 5, ReviewerID: 6, Revision: 1, CreatedUnix: now,
		FinishedUnix: now, MergedUnix: now, ClosedUnix: now,
		OriginalClosingOccurrence: "901", OriginalClosedUnix: now,
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("seed merge invalid: %v", err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(context.Background(), `INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		m.PublicationID, m.Repository, m.Issue, m.PRNumber, m.Stage, m.Revision, string(raw)); err != nil {
		t.Fatal(err)
	}
	return m
}
