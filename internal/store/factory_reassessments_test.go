package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// FW31 persists D13 completion events and bounded cycles, fences packet
// admission per repository and charges funded Jev calls. Pure checks run
// everywhere; the transaction proof needs the disposable SODA_PG_*
// fixture and skips without it.
func TestReassessmentRecordValidation(t *testing.T) {
	cycle := factory.ReassessmentCycle{
		EventID: factory.NewID(), CycleID: factory.NewID(),
		State: factory.ReassessmentCollecting, Repository: 7, Generation: 1,
	}
	record := ReassessmentRecord{Cycle: cycle, Issue: 31}
	if err := record.Validate(); err != nil {
		t.Fatalf("valid record refused: %v", err)
	}
	bad := record
	bad.InventoryDigest = "not-a-digest"
	if err := bad.Validate(); err == nil {
		t.Fatal("malformed inventory digest accepted")
	}
	bad = record
	bad.WaitReason = strings.Repeat("r", factory.MaxReassessmentReasonChars+1)
	if err := bad.Validate(); err == nil {
		t.Fatal("oversize wait reason accepted")
	}
}

// Material binds sealed and digest-bearing records; an all-zero Local
// is allowed only on an unsealed record with no InventoryDigest.
func TestReassessmentRecordLocalBinding(t *testing.T) {
	base := func() ReassessmentRecord {
		cycle := factory.ReassessmentCycle{
			EventID: factory.NewID(), CycleID: factory.NewID(),
			State: factory.ReassessmentAttention, Repository: 7, Generation: 1,
		}
		return ReassessmentRecord{Cycle: cycle, Issue: 31}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("zero-local unsealed record refused: %v", err)
	}
	bad := base()
	bad.Completion = factory.CompletionEvent{ID: strings.Repeat("0", 32)}
	if err := bad.Validate(); err == nil {
		t.Fatal("malformed completion event accepted")
	}
	bad = base()
	bad.InventoryDigest = strings.Repeat("a", 64)
	if err := bad.Validate(); err == nil {
		t.Fatal("digest-bearing record with zero local accepted")
	}
	bad = base()
	bad.InventoryDigest = strings.Repeat("a", 64)
	bad.Local = factory.CompletionLocal{SourceCommit: strings.Repeat("c", 40)}
	if err := bad.Validate(); err == nil {
		t.Fatal("digest-only local accepted on a sealed record")
	}
	bad = base()
	local := sealedTestLocal(t)
	local.Material.Repository = 9
	bad.Local = local
	if err := bad.Validate(); err == nil {
		t.Fatal("wrong-repository material accepted")
	}
	good := base()
	good.Local = sealedTestLocal(t)
	good.InventoryDigest = strings.Repeat("a", 64)
	good.Evidence = []factory.CompletionIssueEvidence{sealedTestBody(t, 1, "title", "body")}
	good.EvidenceIssues = 1
	good.EvidenceRevision = 41
	if err := good.Validate(); err != nil {
		t.Fatalf("sealed material record refused: %v", err)
	}
}

func sealedTestBody(t *testing.T, index int64, title, body string) factory.CompletionIssueEvidence {
	t.Helper()
	sum := func(text string) string {
		digest := sha256.Sum256([]byte(text))
		return hex.EncodeToString(digest[:])
	}
	parsed, err := factory.ParseCompletionIssueEvidence(mustCanonical(t, factory.CompletionIssueEvidence{
		Index: index, Title: title, Body: body,
		TitleDigest: sum(title), ContentDigest: sum(body),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustCanonical(t *testing.T, body factory.CompletionIssueEvidence) string {
	t.Helper()
	data, err := body.CanonicalData()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sealedTestMaterial() *factory.CompletionMaterial {
	material := factory.CompletionMaterial{
		Intents:      []factory.CompletionIntentScope{},
		Acceptances:  []factory.Acceptance{},
		Withdrawals:  []factory.CompletionWithdrawal{},
		Controls:     []factory.IssueControl{},
		Preparations: []factory.CompletionPreparation{},
		Plans:        []factory.InvestigationPlan{},
		PlanHeads:    []factory.CompletionPlanHead{},
		PlanTasks:    []factory.CompletionPlanTask{},
		Assignments:  []factory.CompletionAssignment{},
		Publications: []factory.CompletionPublication{},
		Merges:       []factory.CompletionMerge{},
		Checks:       []factory.CompletionCheck{},
		Authority: factory.CompletionAuthority{
			Effective: factory.EffectiveAuthority{
				Missing: []string{},
				Authority: factory.AuthorityRef{
					RequirementsID: "d" + strings.Repeat("c", 24),
					ApprovalID:     "d" + strings.Repeat("e", 24),
				},
			},
			ProjectID:               "p" + strings.Repeat("d", 24),
			RequirementSourceCommit: strings.Repeat("c", 40),
			RequirementSetupDigest:  strings.Repeat("e", 64),
			RequirementInputsDigest: strings.Repeat("f", 64),
			ApprovalEffectsDigest:   strings.Repeat("0", 64),
			ApprovalReadinessDigest: strings.Repeat("1", 64),
			ApprovalVerified:        true,
			TargetBranch:            "refs/heads/main",
			Roles:                   []string{"soda-coder", "soda-reviewer"},
			ReassessmentMinutes:     factory.DefaultReassessmentMinutes,
		},
		Repository: 7,
	}
	return &material
}

func sealedTestLocal(t *testing.T) factory.CompletionLocal {
	t.Helper()
	material := sealedTestMaterial()
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return factory.CompletionLocal{
		Material:             material,
		SourceCommit:         strings.Repeat("c", 40),
		PreparationDigest:    strings.Repeat("d", 64),
		ScopeDigest:          digest,
		AuthorityFingerprint: "authority",
	}
}

func sealedTestRecord(t *testing.T) ReassessmentRecord {
	cycle := factory.ReassessmentCycle{
		EventID: factory.NewID(), CycleID: factory.NewID(),
		State: factory.ReassessmentDeciding, Repository: 7, Generation: 1,
	}
	return ReassessmentRecord{
		Cycle:           cycle,
		Issue:           31,
		InventoryDigest: strings.Repeat("a", 64),
		Local:           sealedTestLocal(t),
		EvidenceIssues:  1, EvidenceRevision: 41,
	}
}

// Sealed body rules are pure: they run everywhere, including without
// the disposable PG fixture.
func TestSealedEvidenceValidation(t *testing.T) {
	valid := sealedTestRecord(t)
	valid.Evidence = []factory.CompletionIssueEvidence{sealedTestBody(t, 9, "title", "body")}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid sealed record refused: %v", err)
	}
	empty := sealedTestRecord(t)
	empty.Evidence = []factory.CompletionIssueEvidence{}
	empty.EvidenceIssues = 0
	if err := empty.Validate(); err != nil {
		t.Fatalf("valid empty seal refused: %v", err)
	}
	bodiless := sealedTestRecord(t)
	if err := bodiless.Validate(); err == nil {
		t.Fatal("bodiless seal accepted")
	}
	miscounted := sealedTestRecord(t)
	miscounted.Evidence = []factory.CompletionIssueEvidence{sealedTestBody(t, 9, "title", "body")}
	miscounted.EvidenceIssues = 2
	if err := miscounted.Validate(); err == nil {
		t.Fatal("miscounted bodies accepted")
	}
	unbracketed := sealedTestRecord(t)
	unbracketed.Evidence = []factory.CompletionIssueEvidence{sealedTestBody(t, 9, "title", "body")}
	unbracketed.EvidenceRevision = 0
	if err := unbracketed.Validate(); err == nil {
		t.Fatal("unbracketed seal accepted")
	}
	oversize := sealedTestRecord(t)
	oversize.Evidence = []factory.CompletionIssueEvidence{sealedTestBody(t, 9, "title", strings.Repeat("x", factory.MaxReassessmentEvidenceBytes))}
	oversize.EvidenceIssues = 1
	if err := oversize.Validate(); err == nil {
		t.Fatal("oversize bodies accepted")
	}
}

// CaptureCompletionLocal is proven on the disposable PG fixture
// (SODA_PG_*; skips without it): the seeded requirement, grants and
// empty scope capture stably, an unknown repository refuses, and a
// new acceptance head moves only the scope digest.
func TestCaptureCompletionLocalStabilityAndScope(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	first, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("captured local invalid: %v", err)
	}
	if first.SourceCommit != strings.Repeat("1", 40) {
		t.Fatalf("source=%q, want the seeded requirement source commit", first.SourceCommit)
	}
	if second, err := db.CaptureCompletionLocal(ctx, 7); err != nil || !second.Equal(first) {
		t.Fatalf("second=%+v err=%v, want the stable capture", second, err)
	}
	if _, err := db.CaptureCompletionLocal(ctx, 777); err == nil {
		t.Fatal("unknown repository captured instead of refusing")
	}
	decision := acceptanceFixture("d0123456789abcdef01234567", "")
	if err := db.AdmitAcceptanceDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	third, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if third.ScopeDigest == first.ScopeDigest {
		t.Fatal("new acceptance head left the scope digest unchanged")
	}
	if third.SourceCommit != first.SourceCommit || third.PreparationDigest != first.PreparationDigest ||
		third.AuthorityFingerprint != first.AuthorityFingerprint {
		t.Fatalf("third=%+v, want only the scope binding to move", third)
	}
}

// CaptureCompletionLocal seals material control state into the scope
// digest (SUPERVISOR-073 hash-only slice): a queued->blocked assessment
// moves only the scope digest, while an equal-material replay, a
// NativeRev/assessed-time-only refresh and another repository's controls
// leave it unchanged.
func TestCaptureCompletionLocalControlFreshness(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	queued := factory.IssueControl{
		Repository: 7, Issue: 3,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	if _, changed, err := db.RecordIssueAssessment(ctx, queued, now); err != nil || !changed {
		t.Fatalf("queued assessment not recorded: changed=%v err=%v", changed, err)
	}
	base, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("base capture: %v", err)
	}
	blocked := queued
	blocked.Readiness = factory.ReadinessBlocked
	blocked.Reason = factory.BlockerAcceptanceMissing
	blocked.Blockers = []factory.Blocker{{
		Code:       factory.BlockerAcceptanceMissing,
		Resolution: factory.BlockerResolution(factory.BlockerAcceptanceMissing),
	}}
	blocked.Fingerprint = strings.Repeat("3", 64)
	stored, changed, err := db.RecordIssueAssessment(ctx, blocked, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("blocked assessment not recorded: changed=%v err=%v", changed, err)
	}
	if stored.Revision != 2 {
		t.Fatalf("blocked revision=%d, want 2", stored.Revision)
	}
	moved, err := db.CaptureCompletionLocal(ctx, 7)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if moved.ScopeDigest == base.ScopeDigest {
		t.Fatal("queued->blocked assessment left the scope digest unchanged")
	}
	if moved.SourceCommit != base.SourceCommit || moved.PreparationDigest != base.PreparationDigest ||
		moved.AuthorityFingerprint != base.AuthorityFingerprint {
		t.Fatalf("moved=%+v, want only the scope binding to move", moved)
	}
	if replayed, changed, err := db.RecordIssueAssessment(ctx, blocked, now.Add(2*time.Minute)); err != nil || changed {
		t.Fatalf("equal-material replay recorded: changed=%v err=%v", changed, err)
	} else if replayed.Revision != stored.Revision {
		t.Fatalf("replay revision=%d, want %d", replayed.Revision, stored.Revision)
	}
	if replay, err := db.CaptureCompletionLocal(ctx, 7); err != nil || !replay.Equal(moved) {
		t.Fatalf("replay=%+v err=%v, want the unchanged capture", replay, err)
	}
	refresh := blocked
	refresh.NativeRev = 41
	if refreshed, changed, err := db.RecordIssueAssessment(ctx, refresh, now.Add(3*time.Minute)); err != nil || changed {
		t.Fatalf("observation refresh recorded: changed=%v err=%v", changed, err)
	} else if refreshed.Revision != stored.Revision {
		t.Fatalf("refresh revision=%d, want %d", refreshed.Revision, stored.Revision)
	}
	if observed, err := db.CaptureCompletionLocal(ctx, 7); err != nil || !observed.Equal(moved) {
		t.Fatalf("observed=%+v err=%v, want the unchanged capture", observed, err)
	}
	foreign := factory.IssueControl{
		Repository: 8, Issue: 5,
		Readiness: factory.ReadinessBlocked, Reason: factory.BlockerAcceptanceMissing,
		Blockers: []factory.Blocker{{
			Code:       factory.BlockerAcceptanceMissing,
			Resolution: factory.BlockerResolution(factory.BlockerAcceptanceMissing),
		}},
		Fingerprint: strings.Repeat("4", 64), Authority: strings.Repeat("2", 64),
	}
	if _, changed, err := db.RecordIssueAssessment(ctx, foreign, now.Add(4*time.Minute)); err != nil || !changed {
		t.Fatalf("foreign assessment not recorded: changed=%v err=%v", changed, err)
	}
	if isolated, err := db.CaptureCompletionLocal(ctx, 7); err != nil || !isolated.Equal(moved) {
		t.Fatalf("isolated=%+v err=%v, want another repository's controls to leave the capture unchanged", isolated, err)
	}
}

// mergedCompletionRecord builds a valid merged merge value: the exact
// shape the first UpdateMerge recording a completion carries, without
// needing the merge journey itself.
func mergedCompletionRecord(publication, occurrence string) factory.Merge {
	m := mergeTestRecord()
	now := time.Now().Unix()
	m.PublicationID = publication
	m.Stage, m.Outcome, m.Reason = factory.MergeMerged, factory.Succeeded, factory.MergeReasonMerged
	m.FinishedUnix, m.MergedUnix, m.ClosedUnix = now, now, now
	m.OriginalClosingOccurrence, m.OriginalClosedUnix = occurrence, now
	m.MergedCommit = m.HeadOID
	return m
}

func recordTestCompletion(t *testing.T, db *Store, m factory.Merge) bool {
	t.Helper()
	tx, err := db.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted, err := recordCompletionTx(context.Background(), tx, m)
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return inserted
}

func TestCompletionPersistFenceAndFund(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	const repository = int64(7)

	if pending, err := db.CompletionPending(ctx, repository); err != nil || len(pending) != 0 {
		t.Fatalf("pending=%v err=%v, want an open fence", pending, err)
	}

	publication := factory.NewID()
	first := mergedCompletionRecord(publication, "501")
	if !recordTestCompletion(t, db, first) {
		t.Fatal("first completion not recorded")
	}
	// Recovery re-observation replays the same effect: exact-once.
	if recordTestCompletion(t, db, first) {
		t.Fatal("replayed completion recorded twice")
	}
	second := mergedCompletionRecord(factory.NewID(), "502")
	if !recordTestCompletion(t, db, second) {
		t.Fatal("second completion not recorded")
	}

	pending, err := db.CompletionPending(ctx, repository)
	if err != nil || len(pending) != 2 || pending[0] != 1 || pending[1] != 2 {
		t.Fatalf("pending=%v err=%v, want ordered generations 1,2", pending, err)
	}
	cycles, err := db.PendingReassessmentCycles(ctx, repository)
	if err != nil || len(cycles) != 2 {
		t.Fatalf("cycles=%v err=%v, want two collecting cycles", cycles, err)
	}
	for _, record := range cycles {
		if record.Cycle.State != factory.ReassessmentCollecting || record.Issue != 3 {
			t.Fatalf("cycle=%+v, want collecting cycle for issue 3", record.Cycle)
		}
	}
	if cycles[0].Cycle.Generation != 1 || cycles[1].Cycle.Generation != 2 {
		t.Fatal("cycles out of generation order")
	}

	// A wrong-state compare-and-swap refuses instead of overwriting.
	stale := cycles[0]
	stale.Cycle.State = factory.ReassessmentDeciding
	if err := db.CasReassessmentRecord(ctx, stale.Cycle.CycleID, factory.ReassessmentDeciding, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("wrong-state CAS: %v", err)
	}
	// Sealed evidence is immutable once recorded: the digest, the
	// bodies with their count and revision bracket, and the
	// completion event all refuse rewrites.
	sealed := cycles[0]
	sealedBody, err := factory.CompletionIssueEvidence{
		Index: 9, Title: "sealed title", Body: "sealed body",
		TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64),
	}.CanonicalData()
	if err != nil {
		t.Fatal(err)
	}
	parsedBody, err := factory.ParseCompletionIssueEvidence(sealedBody)
	if err != nil {
		t.Fatal(err)
	}
	sealed.InventoryDigest = strings.Repeat("a", 64)
	sealed.EvidenceFingerprint = strings.Repeat("b", 64)
	sealed.Local = sealedTestLocal(t)
	sealed.Evidence = []factory.CompletionIssueEvidence{parsedBody}
	sealed.EvidenceIssues, sealed.EvidenceRevision = 1, 41
	sealed.Cycle.State = factory.ReassessmentDeciding
	if err := db.CasReassessmentRecord(ctx, sealed.Cycle.CycleID, factory.ReassessmentCollecting, sealed); err != nil {
		t.Fatalf("seal CAS: %v", err)
	}
	rewritten := sealed
	rewritten.InventoryDigest = strings.Repeat("f", 64)
	if err := db.CasReassessmentRecord(ctx, rewritten.Cycle.CycleID, factory.ReassessmentDeciding, rewritten); err == nil {
		t.Fatal("sealed inventory rewritten")
	}
	editedBodies := sealed
	editedBodies.Evidence = []factory.CompletionIssueEvidence{parsedBody}
	editedBodies.Evidence[0].Body = "edited body after the seal"
	if err := db.CasReassessmentRecord(ctx, editedBodies.Cycle.CycleID, factory.ReassessmentDeciding, editedBodies); err == nil {
		t.Fatal("sealed bodies rewritten")
	}
	droppedBodies := sealed
	droppedBodies.Evidence = nil
	droppedBodies.EvidenceIssues = 0
	if err := db.CasReassessmentRecord(ctx, droppedBodies.Cycle.CycleID, factory.ReassessmentDeciding, droppedBodies); err == nil {
		t.Fatal("sealed bodies dropped")
	}

	// Calls reserve against the cycle ledger. A retry of an unanswered
	// call reuses its ledger entry; a recorded receipt is reused instead.
	call := factory.NewID()
	fingerprint := strings.Repeat("0", 64)
	reserved, err := db.AdmitReassessmentCall(ctx, sealed.Cycle.CycleID, factory.ReassessmentDeciding, call, factory.ReassessmentDecisionPurpose, "", fingerprint)
	if err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want a reservation", reserved, err)
	}
	if reserved, err := db.AdmitReassessmentCall(ctx, sealed.Cycle.CycleID, factory.ReassessmentDeciding, call, factory.ReassessmentDecisionPurpose, "", fingerprint); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want a retry of the unanswered call", reserved, err)
	}
	if retried, err := db.ReassessmentRecord(ctx, sealed.Cycle.CycleID); err != nil || len(retried.Cycle.Ledger.DecisionCalls) != 1 {
		t.Fatalf("retry took another ledger entry: %+v %v", retried.Cycle.Ledger, err)
	}
	if err := db.RecordPriorityJudgment(ctx, factory.PriorityReceipt{Snapshot: fingerprint, CreatedUnix: 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := db.PriorityJudgment(ctx, fingerprint); err != nil || got.Snapshot != fingerprint {
		t.Fatal("completed decision unavailable", got, err)
	}

	if reserved, err := db.AdmitReassessmentCall(ctx, sealed.Cycle.CycleID, factory.ReassessmentDeciding, factory.NewID(), factory.ReassessmentDecisionPurpose, "", strings.Repeat("1", 64)); err != nil || !reserved {
		t.Fatalf("second call: %v %v", reserved, err)
	}

	// The fenced repository refuses packet admission while the open
	// fence stands: no early admission through any packet path.
	a, r, run, view := dispatchTestPacket(t, time.Now().UTC())
	a.Issue, view.Issue = 4, 4
	a.Subject.Issue.Issue = 4
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); !errors.Is(err, ErrCompletionFenced) {
		t.Fatalf("packet past the fence: %v", err)
	}
}

func TestCompletionRetrySuccession(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	m := mergedCompletionRecord(factory.NewID(), "601")
	if !recordTestCompletion(t, db, m) {
		t.Fatal("completion not recorded")
	}
	cycles, err := db.PendingReassessmentCycles(ctx, 7)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("cycles=%v err=%v", cycles, err)
	}
	prior := cycles[0].Cycle.CycleID
	attention := cycles[0]
	attention.Cycle.State = factory.ReassessmentAttention
	attention.WaitReason = "decision_unavailable"
	if err := db.CasReassessmentRecord(ctx, prior, factory.ReassessmentCollecting, attention); err != nil {
		t.Fatalf("attention CAS: %v", err)
	}
	retry := factory.ReassessmentRetry{
		SuccessorCycleID: factory.NewID(), ExpectedPriorCycle: prior,
		CommandID: factory.NewID(), EventID: cycles[0].Cycle.EventID,
	}
	successor, err := db.StartReassessmentRetry(ctx, retry)
	if err != nil || successor != retry.SuccessorCycleID {
		t.Fatalf("successor=%q err=%v", successor, err)
	}
	// Command replay reuses the recorded successor.
	again, err := db.StartReassessmentRetry(ctx, retry)
	if err != nil || again != successor {
		t.Fatalf("replay=%q err=%v, want the recorded successor", again, err)
	}
	foreign := retry
	foreign.SuccessorCycleID = factory.NewID()
	if _, err := db.StartReassessmentRetry(ctx, foreign); err == nil {
		t.Fatal("second successor admitted for one prior cycle")
	}
}

// Sealed local bindings compare by canonical value content: an
// equal-content rematerialization advances, while valid-but-different
// material refuses without touching the sealed row.
func TestReassessmentLocalEqualCAS(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	seal := func(t *testing.T, occurrence string) ReassessmentRecord {
		t.Helper()
		m := mergedCompletionRecord(factory.NewID(), occurrence)
		if !recordTestCompletion(t, db, m) {
			t.Fatal("completion not recorded")
		}
		cycles, err := db.PendingReassessmentCycles(ctx, 7)
		if err != nil || len(cycles) == 0 {
			t.Fatalf("cycles=%v err=%v", cycles, err)
		}
		record := cycles[len(cycles)-1]
		body, err := factory.CompletionIssueEvidence{
			Index: 9, Title: "sealed title", Body: "sealed body",
			TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64),
		}.CanonicalData()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := factory.ParseCompletionIssueEvidence(body)
		if err != nil {
			t.Fatal(err)
		}
		record.InventoryDigest = strings.Repeat("a", 64)
		record.Evidence = []factory.CompletionIssueEvidence{parsed}
		record.EvidenceIssues, record.EvidenceRevision = 1, 41
		record.Local = sealedTestLocal(t)
		record.Cycle.State = factory.ReassessmentDeciding
		if err := record.Validate(); err != nil {
			t.Fatalf("seal candidate invalid: %v", err)
		}
		if err := db.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentCollecting, record); err != nil {
			t.Fatalf("seal CAS: %v", err)
		}
		return record
	}
	sealed := seal(t, "801")
	rematerialized := sealed
	other := *sealed.Local.Material
	rematerialized.Local.Material = &other
	if !rematerialized.Local.Equal(sealed.Local) {
		t.Fatal("equal content compares unequal")
	}
	rematerialized.Cycle.State = factory.ReassessmentReleased
	rematerialized.Cycle.Decision = factory.ReassessmentOptionContinue
	if err := db.CasReassessmentRecord(ctx, rematerialized.Cycle.CycleID, factory.ReassessmentDeciding, rematerialized); err != nil {
		t.Fatalf("equal-content advance refused: %v", err)
	}
	second := seal(t, "802")
	tampered := second
	changed := *second.Local.Material
	changed.Authority.ReassessmentMinutes = factory.DefaultReassessmentMinutes + 1
	tampered.Local.Material = &changed
	digest, err := tampered.Local.Material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	tampered.Local.ScopeDigest = digest
	if err := tampered.Validate(); err != nil {
		t.Fatalf("tampered candidate invalid: %v", err)
	}
	if tampered.Local.Equal(second.Local) {
		t.Fatal("different material compares equal")
	}
	if err := db.CasReassessmentRecord(ctx, tampered.Cycle.CycleID, factory.ReassessmentDeciding, tampered); err == nil {
		t.Fatal("rewritten material advanced past the seal")
	}
	stored, err := db.ReassessmentRecord(ctx, second.Cycle.CycleID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Local.Equal(second.Local) {
		t.Fatal("refused CAS mutated the sealed row")
	}
}

// reassessmentReceipts counts judgment rows so prefix tests prove no extra
// call was admitted.
func reassessmentReceipts(t *testing.T, db *Store) (receipts int) {
	t.Helper()
	if err := db.db.QueryRowContext(context.Background(), `SELECT count(*) FROM factory_priority_judgments`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	return receipts
}

// decidingTestCycle records one completion and advances its cycle to
// deciding, returning the stored deciding record.
func decidingTestCycle(t *testing.T, db *Store, occurrence string) ReassessmentRecord {
	t.Helper()
	ctx := context.Background()
	if !recordTestCompletion(t, db, mergedCompletionRecord(factory.NewID(), occurrence)) {
		t.Fatal("completion not recorded")
	}
	cycles, err := db.PendingReassessmentCycles(ctx, 7)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("cycles=%v err=%v, want one collecting cycle", cycles, err)
	}
	record := cycles[0]
	record.Cycle.State = factory.ReassessmentDeciding
	if err := db.CasReassessmentRecord(ctx, record.Cycle.CycleID, factory.ReassessmentCollecting, record); err != nil {
		t.Fatalf("deciding CAS: %v", err)
	}
	return record
}

// A stale-prefix CAS keeps the exact locked paid call: the caller read
// before the reservation, advances honest state, and the locked call,
// its receipt and the global spend all survive.
func TestCasStalePrefixKeepsPaidCall(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	decided := decidingTestCycle(t, db, "911")
	cycleID := decided.Cycle.CycleID
	stale, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	call := factory.NewID()
	reserved, err := db.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding,
		call, factory.ReassessmentDecisionPurpose, "", strings.Repeat("2", 64))
	if err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want a charged reservation", reserved, err)
	}
	receipts := reassessmentReceipts(t, db)
	stale.Cycle.State = factory.ReassessmentAttention
	stale.WaitReason = "decision_unavailable"
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, stale); err != nil {
		t.Fatalf("stale-prefix CAS: %v", err)
	}
	stored, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Cycle.State != factory.ReassessmentAttention {
		t.Fatalf("state=%q, want the honest stale advance", stored.Cycle.State)
	}
	if len(stored.Cycle.Ledger.DecisionCalls) != 1 || stored.Cycle.Ledger.DecisionCalls[0] != call {
		t.Fatalf("calls=%v, want the exact locked paid call", stored.Cycle.Ledger.DecisionCalls)
	}
	if receiptsAfter := reassessmentReceipts(t, db); receiptsAfter != receipts {
		t.Fatalf("receipts=%d->%d, want no recharge", receipts, receiptsAfter)
	}
}

// The second proposal reservation is admitted through its canonical
// purpose after a reassess decision, and a stale-prefix CAS holding
// only the first call retains both calls in order.
func TestCasStalePrefixKeepsBothCalls(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	decided := decidingTestCycle(t, db, "912")
	cycleID := decided.Cycle.CycleID
	call1 := factory.NewID()
	if reserved, err := db.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding,
		call1, factory.ReassessmentDecisionPurpose, "", strings.Repeat("3", 64)); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want the first paid call", reserved, err)
	}
	fresh, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Cycle.Decision = factory.ReassessmentOptionReassess
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, fresh); err != nil {
		t.Fatalf("reassess decision CAS: %v", err)
	}
	stale, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	call2 := factory.NewID()
	if reserved, err := db.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding,
		call2, factory.ReassessmentProposalPurpose, factory.ReassessmentOptionReassess, strings.Repeat("4", 64)); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want the admitted proposal call", reserved, err)
	}
	receipts := reassessmentReceipts(t, db)
	stale.Cycle.State = factory.ReassessmentAttention
	stale.WaitReason = "decision_unavailable"
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, stale); err != nil {
		t.Fatalf("stale-prefix CAS: %v", err)
	}
	stored, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Cycle.Ledger.DecisionCalls) != 2 ||
		stored.Cycle.Ledger.DecisionCalls[0] != call1 || stored.Cycle.Ledger.DecisionCalls[1] != call2 {
		t.Fatalf("calls=%v, want both paid calls in order", stored.Cycle.Ledger.DecisionCalls)
	}
	if stored.Cycle.Decision != factory.ReassessmentOptionReassess {
		t.Fatalf("decision=%q, want the recorded reassess decision", stored.Cycle.Decision)
	}
	if receiptsAfter := reassessmentReceipts(t, db); receiptsAfter != receipts {
		t.Fatalf("receipts=%d->%d, want no recharge", receipts, receiptsAfter)
	}
}

// Extra, reordered and forked paid IDs refuse: a prefix cannot grant
// authority, and every refusal leaves the row, the global budget and
// the receipts unchanged.
func TestCasRefusesUnreservedLedgerWrites(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	decided := decidingTestCycle(t, db, "913")
	cycleID := decided.Cycle.CycleID
	call1, call2 := factory.NewID(), factory.NewID()
	for i, reservation := range []struct {
		call, fingerprint string
	}{
		{call1, strings.Repeat("5", 64)},
		{call2, strings.Repeat("6", 64)},
	} {
		if reserved, err := db.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding,
			reservation.call, factory.ReassessmentDecisionPurpose, "", reservation.fingerprint); err != nil || !reserved {
			t.Fatalf("reservation %d: reserved=%v err=%v", i, reserved, err)
		}
	}
	receipts := reassessmentReceipts(t, db)
	current, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"extra":     {call1, call2, factory.NewID()},
		"reordered": {call2, call1},
		"forked":    {call1, factory.NewID()},
		"replaced":  {factory.NewID()},
	}
	for name, calls := range cases {
		bad := current
		bad.Cycle.Ledger.DecisionCalls = calls
		if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, bad); err == nil {
			t.Fatalf("%s ledger accepted", name)
		} else if errors.Is(err, ErrStaleRevision) {
			t.Fatalf("%s ledger refused as stale instead of unreserved: %v", name, err)
		}
	}
	stored, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Cycle.Ledger.DecisionCalls) != 2 ||
		stored.Cycle.Ledger.DecisionCalls[0] != call1 || stored.Cycle.Ledger.DecisionCalls[1] != call2 {
		t.Fatalf("calls=%v, want the locked pair untouched", stored.Cycle.Ledger.DecisionCalls)
	}
	if stored.Cycle.State != factory.ReassessmentDeciding {
		t.Fatalf("state=%q, want the refused row untouched", stored.Cycle.State)
	}
	if receiptsAfter := reassessmentReceipts(t, db); receiptsAfter != receipts {
		t.Fatalf("receipts=%d->%d, want no change on refusal", receipts, receiptsAfter)
	}
}

// An extra call also refuses within the domain limit: with one locked
// paid call, a two-call proposal stays inside the two-call allowance,
// so only the prefix guard can refuse it. Row, budget and receipts
// stay unchanged.
func TestCasRefusesWithinLimitExtraCall(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	decided := decidingTestCycle(t, db, "915")
	cycleID := decided.Cycle.CycleID
	call1 := factory.NewID()
	if reserved, err := db.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding,
		call1, factory.ReassessmentDecisionPurpose, "", strings.Repeat("8", 64)); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want a charged reservation", reserved, err)
	}
	receipts := reassessmentReceipts(t, db)
	current, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	bad := current
	bad.Cycle.Ledger.DecisionCalls = []string{call1, factory.NewID()}
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, bad); err == nil {
		t.Fatal("within-limit extra call accepted")
	} else if errors.Is(err, ErrStaleRevision) {
		t.Fatalf("within-limit extra call refused as stale instead of unreserved: %v", err)
	}
	stored, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Cycle.Ledger.DecisionCalls) != 1 || stored.Cycle.Ledger.DecisionCalls[0] != call1 {
		t.Fatalf("calls=%v, want the single locked call untouched", stored.Cycle.Ledger.DecisionCalls)
	}
	if receiptsAfter := reassessmentReceipts(t, db); receiptsAfter != receipts {
		t.Fatalf("receipts=%d->%d, want no change on refusal", receipts, receiptsAfter)
	}
}

// The prefix rule composes with the seal: the sealing write itself
// keeps the already-reserved paid call, while a stale expected state
// and sealed digest/body/local rewrites still refuse with the row,
// budget and receipts unchanged.
func TestCasPrefixKeepsSealAndStaleRefusals(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	decided := decidingTestCycle(t, db, "914")
	cycleID := decided.Cycle.CycleID
	call := factory.NewID()
	if reserved, err := db.AdmitReassessmentCall(ctx, cycleID, factory.ReassessmentDeciding,
		call, factory.ReassessmentDecisionPurpose, "", strings.Repeat("7", 64)); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v, want a charged reservation", reserved, err)
	}
	sealed, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := factory.CompletionIssueEvidence{
		Index: 9, Title: "sealed title", Body: "sealed body",
		TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64),
	}.CanonicalData()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := factory.ParseCompletionIssueEvidence(body)
	if err != nil {
		t.Fatal(err)
	}
	sealed.InventoryDigest = strings.Repeat("a", 64)
	sealed.EvidenceFingerprint = strings.Repeat("b", 64)
	sealed.Local = sealedTestLocal(t)
	sealed.Evidence = []factory.CompletionIssueEvidence{parsed}
	sealed.EvidenceIssues, sealed.EvidenceRevision = 1, 41
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, sealed); err != nil {
		t.Fatalf("seal CAS: %v", err)
	}
	stored, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InventoryDigest != strings.Repeat("a", 64) || !stored.Local.Equal(sealed.Local) ||
		len(stored.Evidence) != 1 || len(stored.Cycle.Ledger.DecisionCalls) != 1 ||
		stored.Cycle.Ledger.DecisionCalls[0] != call {
		t.Fatalf("sealing lost its seal or its paid call: %+v", stored.Cycle.Ledger.DecisionCalls)
	}
	receipts := reassessmentReceipts(t, db)
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentCollecting, stored); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale expected state: %v", err)
	}
	rewritten := stored
	rewritten.InventoryDigest = strings.Repeat("f", 64)
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, rewritten); err == nil {
		t.Fatal("sealed inventory rewritten")
	} else if errors.Is(err, ErrStaleRevision) {
		t.Fatalf("sealed inventory refused as stale instead of immutable: %v", err)
	}
	// The edited body keeps its shape (nonempty title, well-formed
	// digests), so entry validation passes and the refusal lands at
	// the sealed-bodies immutability comparison, not the state guard.
	editedBodies := stored
	editedBodies.Evidence = []factory.CompletionIssueEvidence{parsed}
	editedBodies.Evidence[0].Body = "edited body after the seal"
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, editedBodies); err == nil {
		t.Fatal("sealed bodies rewritten")
	} else if errors.Is(err, ErrStaleRevision) {
		t.Fatalf("sealed bodies refused as stale instead of immutable: %v", err)
	}
	tampered := stored
	changed := *stored.Local.Material
	changed.Authority.ReassessmentMinutes = factory.DefaultReassessmentMinutes + 1
	tampered.Local.Material = &changed
	materialDigest, err := tampered.Local.Material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	tampered.Local.ScopeDigest = materialDigest
	if err := db.CasReassessmentRecord(ctx, cycleID, factory.ReassessmentDeciding, tampered); err == nil {
		t.Fatal("sealed local bindings rewritten")
	} else if errors.Is(err, ErrStaleRevision) {
		t.Fatalf("sealed local bindings refused as stale instead of immutable: %v", err)
	}
	kept, err := db.ReassessmentRecord(ctx, cycleID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.InventoryDigest != strings.Repeat("a", 64) || !kept.Local.Equal(sealed.Local) ||
		len(kept.Cycle.Ledger.DecisionCalls) != 1 || kept.Cycle.Ledger.DecisionCalls[0] != call {
		t.Fatal("refused seal rewrites mutated the row")
	}
	if receiptsAfter := reassessmentReceipts(t, db); receiptsAfter != receipts {
		t.Fatalf("receipts=%d->%d, want no change on refusal", receipts, receiptsAfter)
	}
}
