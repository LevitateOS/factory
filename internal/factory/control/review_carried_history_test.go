package control

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// F8: a candidate change invalidates acceptances but NEVER erases
// unresolved canonical finding identity/content/evidence/disposition.
// The carried set rebuilds in monotonic cycle order from recorded
// rounds only; journals for unrecorded cycles, rewritten content and
// missing history all hold instead of succeeding. Keys retire only on
// bound attributable verification.

func carriedHead(prefix byte) string { return strings.Repeat(string([]byte{prefix}), 40) }

func carriedFinding(key, evidence string) factory.Finding {
	return factory.Finding{
		Key: key, OriginRun: strings.Repeat("1", 32),
		Severity: factory.FindingSeverityMajor, Defect: "missing bound",
		Evidence: evidence, RequiredOutcome: "add the check",
		FixKind: factory.FindingFixCode, Actionable: true,
	}
}

func carriedJournal(t *testing.T, cycleID string, findings []factory.Finding, dispositions map[string]string) factory.CorrectionJournal {
	t.Helper()
	keys := make([]string, 0, len(findings))
	for _, finding := range findings {
		keys = append(keys, finding.Key)
	}
	digest, err := factory.FindingSetDigest(keys)
	if err != nil {
		t.Fatal(err)
	}
	ordered := make([]factory.CorrectionDisposition, 0, len(findings))
	for _, finding := range findings {
		disposition, ok := dispositions[finding.Key]
		if !ok {
			disposition = factory.DispositionUnresolved
		}
		ordered = append(ordered, factory.CorrectionDisposition{Key: finding.Key, Disposition: disposition})
	}
	return factory.CorrectionJournal{
		CycleID: cycleID, RunID: strings.Repeat("2", 32),
		Report: factory.CorrectionReport{SetDigest: digest, NewCandidate: strings.Repeat("d", 40), Findings: findings, Dispositions: ordered},
	}
}

func carriedCycle(pubID, root, head, base, reviewer string, round int) factory.ReviewCycle {
	return factory.ReviewCycle{
		ID:            factory.ReviewCycleID(pubID, root, round, "review"),
		PublicationID: pubID, AttemptRoot: root,
		Round: round, Phase: "review",
		TriggerDecision: "re-review", TriggerReason: "candidate changed",
		Head: head, BaseOID: base,
		FindingDigest:      reviewCycleInputDigest(head, base, nil, nil),
		ReviewerAssignment: reviewer,
	}
}

func carriedPublication(pubID, head, base string) factory.Publication {
	return factory.Publication{
		ID: pubID, AssignmentID: strings.Repeat("9", 32),
		Candidate: head, PRCreate: factory.PublicationOperation{BaseOID: base},
	}
}

func carriedKeys(findings []factory.Finding) map[string]bool {
	keys := map[string]bool{}
	for _, finding := range findings {
		keys[finding.Key] = true
	}
	return keys
}

// The current cycle's correction journal passes the same canonical
// pipeline as every other set: its findings enter the carried set
// because nothing later retires them, not through a shortcut.
func TestCarriedHistorySameCycleJournalAnswersCurrent(t *testing.T) {
	head, base := carriedHead('a'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("7", 32)
	finding := carriedFinding(key, "line 7 lacks a check")
	p := carriedPublication(pubID, head, base)
	cycle := carriedCycle(pubID, root, head, base, strings.Repeat("8", 32), 1)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, cycle.ID, []factory.Finding{finding}, map[string]string{key: factory.DispositionFixed}),
	}
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	carried, err := (&Coordinator{}).reviewCarriedFindings(context.Background(), p, cycle)
	if err != nil {
		t.Fatalf("same-cycle journal refused: %v", err)
	}
	if len(carried) != 1 || carried[0].Key != key {
		t.Fatalf("same-cycle journal findings not carried: %+v", carried)
	}
}

// Same-key changed content holds conflict instead of silently replacing
// original provenance: neither the old nor the rewritten content wins.
func TestCarriedHistoryChangedContentHoldsConflict(t *testing.T) {
	head, base := carriedHead('a'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("7", 32)
	p := carriedPublication(pubID, head, base)
	cycle := carriedCycle(pubID, root, head, base, strings.Repeat("8", 32), 1)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, cycle.ID, []factory.Finding{carriedFinding(key, "original evidence")}, nil),
		carriedJournal(t, cycle.ID, []factory.Finding{carriedFinding(key, "rewritten evidence")}, nil),
	}
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	carried, err := (&Coordinator{}).reviewCarriedFindings(context.Background(), p, cycle)
	if err == nil {
		t.Fatalf("changed content succeeded with %d findings", len(carried))
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("changed content refused for the wrong reason: %v", err)
	}
}

// A fixed disposition without bound verification evidence is an agent
// claim, not disappearance: the key stays carried.
func TestCarriedHistoryFixedWithoutVerificationStays(t *testing.T) {
	head, base := carriedHead('a'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("7", 32)
	p := carriedPublication(pubID, head, base)
	cycle := carriedCycle(pubID, root, head, base, strings.Repeat("8", 32), 1)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, cycle.ID,
			[]factory.Finding{carriedFinding(key, "line 7 lacks a check")},
			map[string]string{key: factory.DispositionFixed}),
	}
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	carried, err := (&Coordinator{}).reviewCarriedFindings(context.Background(), p, cycle)
	if err != nil {
		t.Fatalf("fixed-without-verification refused: %v", err)
	}
	if !carriedKeys(carried)[key] {
		t.Fatal("unverified fixed claim erased its finding")
	}
}

// A journal for a cycle the publication never recorded holds
// incomplete: unrecorded history cannot seed the canonical set.
func TestCarriedHistoryUnrecordedJournalHoldsIncomplete(t *testing.T) {
	head, base := carriedHead('a'), carriedHead('f')
	pubID, root := strings.Repeat("6", 32), strings.Repeat("7", 32)
	p := carriedPublication(pubID, head, base)
	cycle := carriedCycle(pubID, root, head, base, strings.Repeat("8", 32), 1)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, strings.Repeat("0", 32),
			[]factory.Finding{carriedFinding(strings.Repeat("3", 32), "line 7 lacks a check")}, nil),
	}
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	carried, err := (&Coordinator{}).reviewCarriedFindings(context.Background(), p, cycle)
	if err == nil {
		t.Fatalf("unrecorded journal succeeded with %d findings", len(carried))
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("unrecorded journal refused for the wrong reason: %v", err)
	}
}

// A complete current assessment succeeds: every carried actionable
// finding has its verified assessment with evidence.
func TestCarriedHistoryCompleteAssessmentSucceeds(t *testing.T) {
	head, base := carriedHead('b'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("7", 32)
	finding := carriedFinding(key, "line 7 lacks a check")
	p := carriedPublication(pubID, head, base)
	cycle := carriedCycle(pubID, root, head, base, strings.Repeat("8", 32), 1)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, cycle.ID, []factory.Finding{finding}, nil),
	}
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	carried, err := (&Coordinator{}).reviewCarriedFindings(context.Background(), p, cycle)
	if err != nil {
		t.Fatalf("carried history refused: %v", err)
	}
	clear := &factory.ClearReviewReport{
		CycleID: cycle.ID, Conclusion: factory.ClearReviewConclusion,
		Head: head, BaseOID: base, CoverageDigest: strings.Repeat("c", 64),
		Assessments: []factory.FindingAssessment{
			{Key: key, Status: factory.FindingVerified, Evidence: "check added at line 7, suite green"},
		},
	}
	if err := clear.Validate(carried); err != nil {
		t.Fatalf("complete assessment refused: %v", err)
	}
}

// Persisted-path proofs below need real PostgreSQL (SODA_PG_*; they skip
// without it): cross-head reviewer-assignment reads, bound verification
// retirement and incomplete-history refusal.

func carriedStoreAssignment(t *testing.T, s *store.Store, dsn string, a factory.Assignment) {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Routing columns mirror the JSON: finished rows carry the
	// assignment's own run, matching production FinishAssignment.
	if _, err := db.Exec(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, 7, 9, a.Run, "finished", 1, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func carriedReviewer(t *testing.T, id, root, head, cycleID string, report *factory.ReviewCycleReport) factory.Assignment {
	t.Helper()
	// One distinct valid run per reviewer: production records a run per
	// dispatch, and the canonical run-view join resolves one attempt per
	// run. Sharing a run across reviewers would neither bind nor retire.
	runID := strings.Repeat("d", 31) + id[31:]
	return factory.Assignment{
		ID: id, Role: project.RoleReviewer, SourceCommit: head,
		Phase: project.ExecutionReview, AttemptRoot: root,
		Run: runID, RunHistory: []string{runID}, Attempts: 1,
		Stage: factory.AssignmentFinished, Outcome: factory.Succeeded,
		Reason: factory.AssignReasonReported, FinishedUnix: 1700000001,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 9, Acceptance: "d" + strings.Repeat("a", 24)},
		},
		PublicationAssignment: strings.Repeat("9", 32), ParentAssignment: strings.Repeat("9", 32),
		ProjectID: "p" + strings.Repeat("1", 24), CreatedUnix: 1700000000,
		Acceptance: "d" + strings.Repeat("a", 24), Preparation: "f" + strings.Repeat("b", 24),
		Harness: "codex-1.2.3", HarnessVers: "1.2.3", Model: "muse-spark-1.3-contributor",
		Connection: "native", Prompt: []byte("review the candidate"),
		PromptSHA:  "5044a8c61c76dca4d24e852e42c590e2431855a110cc0ae16e1b2827955b9e02",
		NativeRev:  1,
		ActorID:    1,
		Repository: 7, Issue: 9,
		Result: &factory.AssignmentResult{
			Status: "completed", Reported: true, Summary: "review recorded",
			Phase:        project.ExecutionReview,
			AssignmentID: id, RunID: runID, RecordedUnix: 1700000000,
			PhaseResult: &factory.PhaseResult{
				Version: 2, Phase: project.ExecutionReview,
				Status: "completed", Summary: "review recorded", Output: &factory.PhaseOutput{Review: report},
			},
		},
	}
}

// carriedReviewRunID mirrors carriedReviewer's derived run for one reviewer.
func carriedReviewRunID(reviewerID string) string {
	return strings.Repeat("d", 31) + reviewerID[31:]
}

// carriedStoreReviewRun records the production-path completion rows for
// one reviewer fixture: a supervisor run that passes the canonical
// factory Run validator (settled succeeded reviewer run bound to the
// reviewer's project/source), plus the run-view row pointing at the
// reviewer assignment. The helper refuses to store an invalid run, so
// fixtures cannot fabricate minimal JSON the production path rejects.
func carriedStoreReviewRun(t *testing.T, s *store.Store, dsn, runID, head, reviewerID string, outcome factory.Outcome, mutate func(*factory.Run)) {
	t.Helper()
	started := time.Unix(1700000000, 0)
	run := factory.Run{
		ID: runID, ProjectID: "p" + strings.Repeat("1", 24), Role: project.RoleReviewer,
		InputSHA: head, Started: started, Deadline: started.Add(time.Hour),
		Image:   "sha256:" + strings.Repeat("a", 64),
		Harness: "codex-1.2.3", Model: "muse-spark-1.3-contributor",
		Outcome: outcome, Reconciled: outcome == factory.Succeeded,
	}
	if mutate != nil {
		mutate(&run)
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("fixture run is not production-valid: %v", err)
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO factory_runs(id,active,settled,data) VALUES($1,FALSE,TRUE,$2)`,
		runID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4)`,
		runID, 7, 9, reviewerID); err != nil {
		t.Fatal(err)
	}
	_ = s
}

// carriedStoreOwner records the self-owned coder publication assignment
// every reviewer fixture publishes under: the canonical owner join
// resolves run views through it, so positive paths need exactly one per
// database. The helper refuses an invalid owner.
func carriedStoreOwner(t *testing.T, s *store.Store, dsn string) {
	t.Helper()
	acceptance := "d" + strings.Repeat("a", 24)
	prompt := []byte("implement the candidate")
	sum := sha256.Sum256(prompt)
	owner := factory.Assignment{
		ID: strings.Repeat("9", 32), Role: project.RoleCoder, SourceCommit: carriedHead('f'),
		Phase: project.ExecutionImplement, AttemptRoot: strings.Repeat("9", 32),
		PublicationAssignment: strings.Repeat("9", 32),
		Run:                   strings.Repeat("e", 32), RunHistory: []string{strings.Repeat("e", 32)}, Attempts: 1,
		Stage: factory.AssignmentAssigned,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: 9, Acceptance: acceptance},
		},
		ProjectID: "p" + strings.Repeat("1", 24), CreatedUnix: 1700000000,
		Acceptance: acceptance, Preparation: "f" + strings.Repeat("b", 24),
		Harness: "codex-1.2.3", HarnessVers: "1.2.3", Model: "muse-spark-1.3-contributor",
		Connection: "native", Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		NativeRev: 1, ActorID: 1, Repository: 7, Issue: 9,
	}
	if err := owner.Validate(); err != nil {
		t.Fatalf("fixture owner is not production-valid: %v", err)
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		owner.ID, 7, 9, owner.Run, "assigned", 1, string(raw)); err != nil {
		t.Fatal(err)
	}
	_ = s
}

func carriedEphemeral(t *testing.T) (*store.Store, string) {
	t.Helper()
	admin, ok := store.TestFixtureDSN()
	if !ok {
		t.Skip("SODA_PG_* fixture unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, dsn, cleanup, err := store.OpenEphemeral(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return s, dsn
}

// A correction round carries no reviewer report by construction: its
// synthetic reviewer assignment never exists as a row. The canonical
// carried pipeline must skip report processing for correction rounds
// while still merging their journals, so a re-review after a correction
// answers the finding instead of holding on reviewer step17.
func TestCarriedHistorySkipsCorrectionRoundReviewer(t *testing.T) {
	s, dsn := carriedEphemeral(t)
	ctx := context.Background()
	head, base := carriedHead('b'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	reviewer1, reviewer3 := strings.Repeat("7", 32), strings.Repeat("5", 32)
	finding := carriedFinding(key, "line 7 lacks a check")
	prior := carriedCycle(pubID, root, head, base, reviewer1, 1)
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, reviewer1, root, head, prior.ID,
		&factory.ReviewCycleReport{CycleID: prior.ID, NewFindings: []factory.Finding{finding}}))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(reviewer1), head, reviewer1, factory.Succeeded, nil)
	carriedStoreOwner(t, s, dsn)
	correction := carriedCycle(pubID, root, head, base, strings.Repeat("4", 32), 2)
	correction.CorrectionAssignment = strings.Repeat("2", 32)
	current := carriedCycle(pubID, root, head, base, reviewer3, 3)
	p := carriedPublication(pubID, head, base)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, correction.ID, []factory.Finding{finding}, map[string]string{key: factory.DispositionFixed}),
	}
	p.ReviewCycles = []factory.ReviewCycle{prior, correction, current}
	coord := &Coordinator{Store: s}
	carried, err := coord.reviewCarriedFindings(ctx, p, current)
	if err != nil {
		t.Fatalf("carried history held on its correction round: %v", err)
	}
	if !carriedKeys(carried)[key] {
		t.Fatalf("round-one finding erased by its correction round: %v", carriedKeys(carried))
	}
}

// The F8 repro at bind level on the persisted path: a valid unresolved
// correction journal on the old head must still be answered after the
// head changes. An empty clear must refuse — never APPROVED with no
// assessment — with zero native review effect.
func TestCarriedHistoryKeepsUnresolvedAcrossHeadChange(t *testing.T) {
	s, dsn := carriedEphemeral(t)
	ctx := context.Background()
	oldHead, newHead, base := carriedHead('a'), carriedHead('b'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	oldReviewer, newReviewer := strings.Repeat("7", 32), strings.Repeat("8", 32)
	finding := carriedFinding(key, "line 7 lacks a check")
	prior := carriedCycle(pubID, root, oldHead, base, oldReviewer, 1)
	current := carriedCycle(pubID, root, newHead, base, newReviewer, 2)
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, oldReviewer, root, oldHead, prior.ID,
		&factory.ReviewCycleReport{
			CycleID: prior.ID, NewFindings: []factory.Finding{finding},
			Assessments: []factory.FindingAssessment{
				{Key: key, Status: factory.FindingStillOpen, Evidence: "fix not yet attempted"},
			},
		}))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(oldReviewer), oldHead, oldReviewer, factory.Succeeded, nil)
	carriedStoreOwner(t, s, dsn)
	p := carriedPublication(pubID, newHead, base)
	p.CorrectionJournals = []factory.CorrectionJournal{
		carriedJournal(t, prior.ID, []factory.Finding{finding}, nil),
	}
	p.ReviewCycles = []factory.ReviewCycle{prior, current}
	coord := &Coordinator{Store: s}
	carried, err := coord.reviewCarriedFindings(ctx, p, current)
	if err != nil {
		t.Fatalf("carried history refused: %v", err)
	}
	if !carriedKeys(carried)[key] {
		t.Fatalf("old-head unresolved finding erased after head change: %v", carriedKeys(carried))
	}
	clear := &factory.ClearReviewReport{
		CycleID: current.ID, Conclusion: factory.ClearReviewConclusion,
		Head: newHead, BaseOID: base, CoverageDigest: strings.Repeat("c", 64),
	}
	if err := clear.Validate(carried); err == nil {
		t.Fatal("empty clear approved an unassessed carried finding")
	}
	assignment := factory.Assignment{
		ID: current.ReviewerAssignment, Role: project.RoleReviewer, SourceCommit: newHead,
		Phase: project.ExecutionReview,
		Result: &factory.AssignmentResult{
			Status: "completed", Reported: true, Summary: "clear recorded",
			PhaseResult: &factory.PhaseResult{
				Version: 2, Phase: project.ExecutionReview,
				Status: "completed", Output: &factory.PhaseOutput{Clear: clear},
			},
		},
	}
	resolved, err := resolveAssignmentVerdict(assignment)
	if err != nil {
		t.Fatalf("clear verdict not resolved: %v", err)
	}
	if event, _, err := coord.bindPhasedReviewVerdict(ctx, p, assignment, newHead, resolved); err == nil || event == "APPROVED" {
		t.Fatalf("bind approved without assessment: event=%q err=%v", event, err)
	}
}

// A cross-head prior round's phased findings enter the carried set;
// bound recorded verification retires the key; an earlier verification
// cannot erase a later rediscovery of the identical finding.
func TestCarriedHistoryCrossHeadReadsAndRetires(t *testing.T) {
	s, dsn := carriedEphemeral(t)
	ctx := context.Background()
	oldHead, newHead, base := carriedHead('a'), carriedHead('b'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	oldReviewer, newReviewer := strings.Repeat("7", 32), strings.Repeat("8", 32)
	prior := carriedCycle(pubID, root, oldHead, base, oldReviewer, 1)
	current := carriedCycle(pubID, root, newHead, base, newReviewer, 2)
	report := func(assessments []factory.FindingAssessment) *factory.ReviewCycleReport {
		return &factory.ReviewCycleReport{
			CycleID:     prior.ID,
			NewFindings: []factory.Finding{carriedFinding(key, "line 7 lacks a check")},
			Assessments: assessments,
		}
	}
	coord := &Coordinator{Store: s}
	p := carriedPublication(pubID, newHead, base)
	p.ReviewCycles = []factory.ReviewCycle{prior, current}
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, oldReviewer, root, oldHead, prior.ID,
		report([]factory.FindingAssessment{{Key: key, Status: factory.FindingStillOpen, Evidence: "not yet fixed"}})))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(oldReviewer), oldHead, oldReviewer, factory.Succeeded, nil)
	carriedStoreOwner(t, s, dsn)
	carried, err := coord.reviewCarriedFindings(ctx, p, current)
	if err != nil {
		t.Fatalf("cross-head read refused: %v", err)
	}
	if !carriedKeys(carried)[key] {
		t.Fatal("still-open cross-head finding erased")
	}
	oldReviewer2 := strings.Repeat("a", 32)
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, oldReviewer2, root, oldHead, prior.ID,
		report([]factory.FindingAssessment{{Key: key, Status: factory.FindingVerified, Evidence: "check added, suite green"}})))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(oldReviewer2), oldHead, oldReviewer2, factory.Succeeded, nil)
	p2 := carriedPublication(pubID, newHead, base)
	prior2 := carriedCycle(pubID, root, oldHead, base, oldReviewer2, 1)
	p2.ReviewCycles = []factory.ReviewCycle{prior2, current}
	carried2, err := coord.reviewCarriedFindings(ctx, p2, current)
	if err != nil {
		t.Fatalf("verified read refused: %v", err)
	}
	if carriedKeys(carried2)[key] {
		t.Fatal("verified finding stayed carried")
	}
	rediscoverReviewer := strings.Repeat("b", 32)
	mid := carriedCycle(pubID, root, newHead, base, rediscoverReviewer, 2)
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, rediscoverReviewer, root, newHead, mid.ID,
		&factory.ReviewCycleReport{
			CycleID:     mid.ID,
			NewFindings: []factory.Finding{carriedFinding(key, "line 7 lacks a check")},
			Assessments: []factory.FindingAssessment{
				{Key: key, Status: factory.FindingStillOpen, Evidence: "regressed on the new head"},
			},
		}))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(rediscoverReviewer), newHead, rediscoverReviewer, factory.Succeeded, nil)
	p3 := carriedPublication(pubID, newHead, base)
	current3 := carriedCycle(pubID, root, newHead, base, newReviewer, 3)
	p3.ReviewCycles = []factory.ReviewCycle{prior2, mid, current3}
	carried3, err := coord.reviewCarriedFindings(ctx, p3, current3)
	if err != nil {
		t.Fatalf("rediscovery read refused: %v", err)
	}
	if !carriedKeys(carried3)[key] {
		t.Fatal("older verification erased a newer rediscovery")
	}
}

// A prior report that verifies one carried key while omitting another
// required key refuses at collection time: round 3 can never clear on
// the partial retirement, and no downstream approval follows.
func TestCarriedHistoryOmittedCoverageHoldsIncomplete(t *testing.T) {
	s, dsn := carriedEphemeral(t)
	ctx := context.Background()
	oldHead, newHead, base := carriedHead('a'), carriedHead('b'), carriedHead('f')
	keyA, keyB, keyC := strings.Repeat("3", 32), strings.Repeat("4", 32), strings.Repeat("5", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	oldReviewer, newReviewer := strings.Repeat("7", 32), strings.Repeat("8", 32)
	prior := carriedCycle(pubID, root, oldHead, base, oldReviewer, 1)
	round2Reviewer := strings.Repeat("a", 32)
	round2 := carriedCycle(pubID, root, oldHead, base, round2Reviewer, 2)
	current := carriedCycle(pubID, root, newHead, base, newReviewer, 3)
	both := []factory.Finding{carriedFinding(keyA, "first defect"), carriedFinding(keyB, "second defect")}
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, oldReviewer, root, oldHead, prior.ID,
		&factory.ReviewCycleReport{
			CycleID: prior.ID, NewFindings: both,
			Assessments: []factory.FindingAssessment{
				{Key: keyA, Status: factory.FindingStillOpen, Evidence: "still open"},
				{Key: keyB, Status: factory.FindingStillOpen, Evidence: "still open"},
			},
		}))
	// Round 2 verifies A but omits required B while introducing C: the
	// canonical report validator must refuse before any merge or
	// retirement, so no round-3 clear can build on a partial set.
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, round2Reviewer, root, oldHead, round2.ID,
		&factory.ReviewCycleReport{
			CycleID:     round2.ID,
			NewFindings: []factory.Finding{carriedFinding(keyC, "third defect")},
			Assessments: []factory.FindingAssessment{
				{Key: keyA, Status: factory.FindingVerified, Evidence: "fixed and checked"},
				{Key: keyC, Status: factory.FindingStillOpen, Evidence: "just found"},
			},
		}))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(oldReviewer), oldHead, oldReviewer, factory.Succeeded, nil)
	carriedStoreOwner(t, s, dsn)
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(round2Reviewer), oldHead, round2Reviewer, factory.Succeeded, nil)
	p := carriedPublication(pubID, newHead, base)
	p.ReviewCycles = []factory.ReviewCycle{prior, round2, current}
	coord := &Coordinator{Store: s}
	carried, err := coord.reviewCarriedFindings(ctx, p, current)
	if err == nil {
		t.Fatalf("omitted coverage succeeded with %d findings", len(carried))
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("omitted coverage refused for the wrong reason: %v", err)
	}
}

// Matched-run but unfinished, failed or wrong-subject prior results
// hold incomplete: only a finished succeeded review run on the exact
// historical subject can retire keys.
func TestCarriedHistoryUnfinishedFailedWrongSubjectHoldIncomplete(t *testing.T) {
	s, dsn := carriedEphemeral(t)
	ctx := context.Background()
	oldHead, newHead, base := carriedHead('a'), carriedHead('b'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	stage := func(id string, mutate func(*factory.Assignment)) (factory.Publication, factory.ReviewCycle) {
		p := carriedPublication(pubID, newHead, base)
		prior := carriedCycle(pubID, root, oldHead, base, id, 1)
		current := carriedCycle(pubID, root, newHead, base, strings.Repeat("8", 32), 2)
		a := carriedReviewer(t, id, root, oldHead, prior.ID,
			&factory.ReviewCycleReport{
				CycleID:     prior.ID,
				NewFindings: []factory.Finding{carriedFinding(key, "line 7 lacks a check")},
				Assessments: []factory.FindingAssessment{
					{Key: key, Status: factory.FindingVerified, Evidence: "checked"},
				},
			})
		mutate(&a)
		carriedStoreAssignment(t, s, dsn, a)
		p.ReviewCycles = []factory.ReviewCycle{prior, current}
		return p, current
	}
	coord := &Coordinator{Store: s}
	cases := map[string]struct {
		id     string
		mutate func(*factory.Assignment)
	}{
		"unfinished": {strings.Repeat("c", 32), func(a *factory.Assignment) {
			a.Stage, a.Outcome, a.Reason, a.Result, a.FinishedUnix = factory.AssignmentAssigned, factory.Outcome(""), "", nil, 0
		}},
		"failed": {strings.Repeat("d", 32), func(a *factory.Assignment) {
			a.Outcome, a.Reason = factory.Failed, factory.AssignReasonRunFailed
		}},
		"wrong_subject": {strings.Repeat("e", 32), func(a *factory.Assignment) {
			a.Subject = factory.ExecutionSubject{Kind: factory.SubjectIntent}
		}},
	}
	for name, tc := range cases {
		p, current := stage(tc.id, tc.mutate)
		if carried, err := coord.reviewCarriedFindings(ctx, p, current); err == nil {
			t.Fatalf("%s: invalid prior run succeeded with %d findings", name, len(carried))
		} else if !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("%s: refused for the wrong reason: %v", name, err)
		}
	}
}

// A cross-head prior round whose reviewer assignment is missing holds
// the cycle incomplete instead of succeeding with an empty set. A
// recorded verified assessment for a key the canonical set never
// recorded is likewise malformed evidence, not retirement.
func TestCarriedHistoryMissingCrossHeadHoldsIncomplete(t *testing.T) {
	s, dsn := carriedEphemeral(t)
	ctx := context.Background()
	oldHead, newHead, base := carriedHead('a'), carriedHead('b'), carriedHead('f')
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	p := carriedPublication(pubID, newHead, base)
	prior := carriedCycle(pubID, root, oldHead, base, strings.Repeat("0", 32), 1)
	current := carriedCycle(pubID, root, newHead, base, strings.Repeat("8", 32), 2)
	p.ReviewCycles = []factory.ReviewCycle{prior, current}
	coord := &Coordinator{Store: s}
	carried, err := coord.reviewCarriedFindings(ctx, p, current)
	if err == nil {
		t.Fatalf("missing cross-head history succeeded with %d findings", len(carried))
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("missing history refused for the wrong reason: %v", err)
	}
	stranger := strings.Repeat("e", 32)
	carriedStoreAssignment(t, s, dsn, carriedReviewer(t, stranger, root, oldHead, prior.ID,
		&factory.ReviewCycleReport{
			CycleID: prior.ID,
			Assessments: []factory.FindingAssessment{
				{Key: strings.Repeat("f", 32), Status: factory.FindingVerified, Evidence: "checked nowhere"},
			},
		}))
	carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(stranger), oldHead, stranger, factory.Succeeded, nil)
	carriedStoreOwner(t, s, dsn)
	p2 := carriedPublication(pubID, newHead, base)
	prior2 := carriedCycle(pubID, root, oldHead, base, stranger, 1)
	p2.ReviewCycles = []factory.ReviewCycle{prior2, current}
	if carried, err := coord.reviewCarriedFindings(ctx, p2, current); err == nil {
		t.Fatalf("unrecorded-key verification succeeded with %d findings", len(carried))
	} else if !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("unrecorded-key verification refused for the wrong reason: %v", err)
	}
}

// A valid reviewer assignment whose run was never recorded — or whose
// recorded run never succeeded — holds incomplete: matching run IDs in
// the DTO alone never retire findings.
func TestCarriedHistoryMissingOrUnsuccessfulRunHoldsIncomplete(t *testing.T) {
	oldHead, newHead, base := carriedHead('a'), carriedHead('b'), carriedHead('f')
	key := strings.Repeat("3", 32)
	pubID, root := strings.Repeat("6", 32), strings.Repeat("9", 32)
	stage := func(t *testing.T) (*store.Store, string, factory.Publication, factory.ReviewCycle) {
		s, dsn := carriedEphemeral(t)
		prior := carriedCycle(pubID, root, oldHead, base, strings.Repeat("7", 32), 1)
		current := carriedCycle(pubID, root, newHead, base, strings.Repeat("8", 32), 2)
		carriedStoreAssignment(t, s, dsn, carriedReviewer(t, strings.Repeat("7", 32), root, oldHead, prior.ID,
			&factory.ReviewCycleReport{
				CycleID:     prior.ID,
				NewFindings: []factory.Finding{carriedFinding(key, "line 7 lacks a check")},
				Assessments: []factory.FindingAssessment{
					{Key: key, Status: factory.FindingStillOpen, Evidence: "not yet fixed"},
				},
			}))
		p := carriedPublication(pubID, newHead, base)
		p.ReviewCycles = []factory.ReviewCycle{prior, current}
		return s, dsn, p, current
	}
	ctx := context.Background()
	t.Run("missing run record", func(t *testing.T) {
		s, _, p, current := stage(t)
		if carried, err := (&Coordinator{Store: s}).reviewCarriedFindings(ctx, p, current); err == nil {
			t.Fatalf("unrecorded run succeeded with %d findings", len(carried))
		} else if !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("unrecorded run refused for the wrong reason: %v", err)
		}
	})

	t.Run("run recorded for another project", func(t *testing.T) {
		s, dsn, p, current := stage(t)
		rev := strings.Repeat("7", 32)
		carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(rev), carriedHead('a'), rev,
			factory.Succeeded, func(r *factory.Run) { r.ProjectID = "p" + strings.Repeat("2", 24) })
		carriedStoreOwner(t, s, dsn)
		if carried, err := (&Coordinator{Store: s}).reviewCarriedFindings(ctx, p, current); err == nil {
			t.Fatalf("foreign-project run succeeded with %d findings", len(carried))
		} else if !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("foreign-project run refused for the wrong reason: %v", err)
		}
	})
	t.Run("owner from another publication", func(t *testing.T) {
		s, dsn, p, current := stage(t)
		rev := strings.Repeat("7", 32)
		carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(rev), carriedHead('a'), rev,
			factory.Succeeded, nil)
		carriedStoreOwner(t, s, dsn)
		p.AssignmentID = strings.Repeat("0", 32)
		if carried, err := (&Coordinator{Store: s}).reviewCarriedFindings(ctx, p, current); err == nil {
			t.Fatalf("foreign-owner history succeeded with %d findings", len(carried))
		} else if !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("foreign-owner history refused for the wrong reason: %v", err)
		}
	})
	t.Run("unreconciled succeeded run", func(t *testing.T) {
		s, dsn, p, current := stage(t)
		rev := strings.Repeat("7", 32)
		carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(rev), carriedHead('a'), rev,
			factory.Succeeded, func(r *factory.Run) { r.Reconciled = false })
		carriedStoreOwner(t, s, dsn)
		if carried, err := (&Coordinator{Store: s}).reviewCarriedFindings(ctx, p, current); err == nil {
			t.Fatalf("unreconciled run succeeded with %d findings", len(carried))
		} else if !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("unreconciled run refused for the wrong reason: %v", err)
		}
	})
	t.Run("unsuccessful recorded run", func(t *testing.T) {
		s, dsn, p, current := stage(t)
		rev := strings.Repeat("7", 32)
		carriedStoreReviewRun(t, s, dsn, carriedReviewRunID(rev), carriedHead('a'), rev, factory.Failed, nil)
		if carried, err := (&Coordinator{Store: s}).reviewCarriedFindings(ctx, p, current); err == nil {
			t.Fatalf("failed run succeeded with %d findings", len(carried))
		} else if !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("failed run refused for the wrong reason: %v", err)
		}
	})
}
