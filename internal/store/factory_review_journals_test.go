package store

import (
	"context"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func journalStoreFinding(n int, located bool) factory.Finding {
	finding := factory.Finding{
		Key: strings.Repeat("0", 31) + string(rune('a'+n)), OriginRun: strings.Repeat("1", 32),
		Severity: factory.FindingSeverityMajor, Defect: "missing bound",
		Evidence: "line lacks a check", RequiredOutcome: "add the check",
		FixKind: factory.FindingFixCode, Actionable: n == 0,
	}
	if located {
		finding.Location = &factory.FindingLocation{Path: "x.go", Side: "new", Line: int64(10 + n)}
	}
	return finding
}

// journalStoreCycleJournal builds one recorded cycle with its answering
// correction journal over the same finding set.
func journalStoreCycleJournal(t *testing.T, p factory.Publication, round int) (factory.ReviewCycle, factory.CorrectionJournal) {
	t.Helper()
	findings := []factory.Finding{journalStoreFinding(0, true), journalStoreFinding(1, true)}
	keys := []string{findings[0].Key, findings[1].Key}
	digest, err := factory.FindingSetDigest(keys)
	if err != nil {
		t.Fatal(err)
	}
	attemptRoot := strings.Repeat("b", 32)
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: attemptRoot,
		ID:                 factory.ReviewCycleID(p.ID, attemptRoot, round, "review"),
		Phase:              "review",
		TriggerDecision:    factory.DeliveryReviewAgain,
		TriggerReason:      "correction set needs another inspection",
		Head:               p.Candidate,
		BaseOID:            p.PRCreate.BaseOID,
		FindingDigest:      digest,
		ReviewerAssignment: strings.Repeat("4", 32),
		Round:              round,
	}
	journal := factory.CorrectionJournal{CycleID: cycle.ID, RunID: strings.Repeat("c", 32), Report: factory.CorrectionReport{
		SetDigest: digest, NewCandidate: strings.Repeat("d", 40),
		Findings: findings,
		Dispositions: []factory.CorrectionDisposition{
			{Key: findings[0].Key, Disposition: factory.DispositionFixed},
			{Key: findings[1].Key, Disposition: factory.DispositionDeclinedOptional},
		},
	}}
	if err := journal.Validate(); err != nil {
		t.Fatalf("journal fixture refused: %v", err)
	}
	return cycle, journal
}

func TestReviewCycleJournalPersists(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	p := recordPublishedCorrectionFixture(t, db, ctx)
	cycle, journal := journalStoreCycleJournal(t, p, 1)
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record cycle: %v", err)
	}
	p.CorrectionJournals = []factory.CorrectionJournal{journal}
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record correction journal: %v", err)
	}
	got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.ReviewCycles) != 1 || got.ReviewCycles[0] != cycle {
		t.Fatalf("cycle not journaled: %+v", got.ReviewCycles)
	}
	if len(got.CorrectionJournals) != 1 || got.CorrectionJournals[0].CycleID != cycle.ID || got.CorrectionJournals[0].Report.SetDigest != cycle.FindingDigest {
		t.Fatalf("journal not recorded: %+v", got.CorrectionJournals)
	}
	// Re-recording the identical journals over a fresh read advances only
	// the revision: replay reuses its identity instead of duplicating.
	got.Revision++
	if err := db.UpdatePublication(ctx, got); err != nil {
		t.Fatalf("identical journal replay refused: %v", err)
	}
}

func TestReviewCycleJournalImmutable(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	p := recordPublishedCorrectionFixture(t, db, ctx)
	cycle, journal := journalStoreCycleJournal(t, p, 1)
	p.ReviewCycles = []factory.ReviewCycle{cycle}
	p.CorrectionJournals = []factory.CorrectionJournal{journal}
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record journals: %v", err)
	}
	t.Run("rewrite cycle", func(t *testing.T) {
		next, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		next.ReviewCycles[0].Round = 2
		next.Revision++
		if err := db.UpdatePublication(ctx, next); err == nil {
			t.Fatal("rewrote a recorded cycle")
		}
	})
	t.Run("shrink cycles", func(t *testing.T) {
		next, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		next.ReviewCycles = nil
		next.CorrectionJournals = nil
		next.Revision++
		if err := db.UpdatePublication(ctx, next); err == nil {
			t.Fatal("removed recorded journals")
		}
	})
	t.Run("edit journal disposition", func(t *testing.T) {
		next, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		next.CorrectionJournals[0].Report.Dispositions[0].Disposition = factory.DispositionDisputed
		next.Revision++
		if err := db.UpdatePublication(ctx, next); err == nil {
			t.Fatal("edited a recorded correction journal")
		}
	})
	t.Run("digest mismatch", func(t *testing.T) {
		next, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		_, stray := journalStoreCycleJournal(t, next, 1)
		stray.Report.SetDigest = strings.Repeat("0", 64)
		stray.RunID = strings.Repeat("d", 32)
		next.CorrectionJournals = append(next.CorrectionJournals, stray)
		next.Revision++
		if err := db.UpdatePublication(ctx, next); err == nil {
			t.Fatal("journaled a correction over a changed finding set")
		}
	})
}
