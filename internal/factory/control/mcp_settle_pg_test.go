package control

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// The authenticated correction terminal must retain its whole typed report
// through PostgreSQL settlement. Changed or omitted keys are refused before
// recording, so the worker can correct them without freezing its assignment.
func TestSettleCorrectionTerminalPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	for index, malformed := range []bool{false, true, false, false} {
		a := phaseTestAssignment(t, project.ExecutionCorrect, project.RoleCoder, false)
		a.Issue, a.Subject.Issue.Issue = int64(11+index), int64(11+index)
		finding := correctionTestFinding(1, true, factory.FindingFixCode)
		digest, err := factory.FindingSetDigest([]string{finding.Key})
		if err != nil {
			t.Fatal(err)
		}
		cycle := factory.ReviewCycle{PublicationID: factory.NewID(), AttemptRoot: a.AttemptRoot, Phase: project.ExecutionCorrect, Head: a.SourceCommit, BaseOID: strings.Repeat("b", 40), FindingDigest: digest, ReviewerAssignment: factory.NewID(), CorrectionAssignment: a.ID, Round: 1, TriggerDecision: "correction", TriggerReason: "answer the observed finding"}
		cycle.ID = factory.ReviewCycleID(cycle.PublicationID, cycle.AttemptRoot, cycle.Round, cycle.Phase)
		a.ReviewContext = &factory.CurrentReviewContext{Publication: factory.CurrentPublicationContext{ID: cycle.PublicationID, AssignmentID: a.PublicationAssignment}, AdmittedTuple: factory.CandidateTuple{Head: a.SourceCommit, Base: cycle.BaseOID, DiffBase: cycle.BaseOID, Revision: 1}, Cycle: cycle, CarriedFindings: []factory.Finding{finding}}
		if err := a.Validate(); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
			t.Fatal(err)
		}
		run := phaseTestRun(a, "")
		run.Image = "sha256:" + strings.Repeat("c", 64)
		run.Started, run.Deadline = time.Now().Add(-time.Second), time.Now().Add(time.Hour)
		if err := db.RecordFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		staged := testMCPCapability()
		staged.AssignmentID, staged.Profile, staged.State = a.ID, factory.MCPProfileFixer, factory.MCPCapabilityStaged
		if err := db.StageMCPCapability(ctx, staged); err != nil {
			t.Fatal(err)
		}
		if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID, staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
			t.Fatal(err)
		}
		capability, err := db.GetMCPCapability(ctx, staged.ID)
		if err != nil {
			t.Fatal(err)
		}
		key, candidate := finding.Key, strings.Repeat("8", 40)
		if malformed {
			key = factory.NewID()
		}
		evidence := []factory.CorrectionFixEvidence{{Key: finding.Key, ChangedPaths: []string{"native-proof.txt"}, Commits: []string{candidate}}}
		payload, err := json.Marshal(map[string]any{"finding_digest": digest, "candidate": candidate, "dispositions": []factory.CorrectionDisposition{{Key: key, Disposition: factory.DispositionFixed}}, "fix_evidence": evidence, "evidence_refs": []string{"native checkpoint"}})
		if err != nil {
			t.Fatal(err)
		}
		if index == 2 {
			payload, _ = json.Marshal(map[string]any{"candidate": candidate})
		}
		submission := testMCPSubmission(capability, factory.MCPToolFinishCorrection, factory.NewID(), payload)
		if err := CheckMCPTerminalFinish(a, submission, time.Now()); malformed || index == 2 {
			if err == nil {
				t.Fatal("invalid correction did not return a correctable refusal")
			}
			if _, frozen, err := db.TerminalMCPProposal(ctx, a.ID); err != nil || frozen {
				t.Fatal("invalid correction froze its assignment", err)
			}
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatal(err)
		}
		// Native completion may arrive before the periodic MCP drive. Its
		// empty stdout must not consume an already-recorded typed terminal.
		retired := run
		retired.Outcome, retired.Reconciled = factory.Succeeded, true
		foreign := retired
		foreign.ID = factory.NewID()
		if handled, err := settleRecordedMCPBeforeRun(ctx, db, a, foreign, time.Now()); err != nil || handled {
			t.Fatalf("foreign retired run claimed terminal: %v/%v", handled, err)
		}
		if index != 3 {
			if err := finishFromRun(ctx, db, a, retired, time.Now()); err != nil {
				t.Fatal(err)
			}
		} else if _, accounted := AccountSettledRun(ctx, db, retired, time.Now()); !accounted {
			t.Fatal("retired MCP run was not accounted through its terminal")
		}
		if settled, err := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); err != nil || settled {
			mapped, note, mapErr := mapMCPTerminalFinish(a, submission, time.Now())
			receipt, found, receiptErr := db.TerminalMCPProposal(ctx, a.ID)
			t.Fatalf("correction case=%d settlement=%v err=%v map=%v/%s outcome=%s result=%+v receipt=%+v/%v/%v", index, settled, err, mapErr, note, mapped.Outcome, mapped.Result, receipt, found, receiptErr)
		}
		finished, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finished.Outcome != factory.Succeeded || finished.Result == nil || finished.Result.PhaseResult == nil || finished.Result.PhaseResult.Output.Correction == nil || finished.Result.Candidate != candidate || len(finished.Result.PhaseResult.Output.Correction.Evidence) != 1 || finished.Result.PhaseResult.Output.Correction.Findings[0].Defect != finding.Defect {
			t.Fatalf("typed correction report lost: %+v", finished)
		}
		receipt, err := db.MCPWorkStatus(ctx, a.ID, submission.SubmissionID)
		if err != nil || receipt.State != factory.MCPWorkAccepted {
			t.Fatalf("correction receipt: %+v err=%v", receipt, err)
		}
		if err := finishFromRun(ctx, db, a, retired, time.Now()); err != nil {
			t.Fatalf("retired finish replay: %v", err)
		}
		replayed, err := db.Assignment(ctx, a.ID)
		if err != nil || factory.ResultDigest(*replayed.Result) != factory.ResultDigest(*finished.Result) {
			t.Fatal("retired finish replay changed the original terminal")
		}
	}
}

// TestSettleMCPTerminalsPGDrive proves the coordinator drive against
// real PostgreSQL (SODA_PG_*; skips without it): a recorded FW40
// review terminal behind a persisted assigned assignment finishes
// the assignment typed and advances its receipt recorded→accepted,
// and a second drive settles nothing more.
func TestSettleMCPTerminalsPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	a := phaseTestAssignment(t, project.ExecutionReview, project.RoleReviewer, true)
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	staged := testMCPCapability()
	staged.AssignmentID = a.ID
	staged.Profile = factory.MCPProfileInspector
	staged.State = factory.MCPCapabilityStaged
	if err := db.StageMCPCapability(ctx, staged); err != nil {
		t.Fatalf("stage capability: %v", err)
	}
	if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID,
		staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
		t.Fatalf("activate capability: %v", err)
	}
	capability, err := db.GetMCPCapability(ctx, staged.ID)
	if err != nil {
		t.Fatalf("capability readback: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"conclusion": "incomplete", "coverage": "partial", "evidence_refs": "r",
	})
	if err != nil {
		t.Fatal(err)
	}
	submission := testMCPSubmission(capability, factory.MCPToolFinishReview, factory.NewID(), payload)
	if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
		t.Fatalf("record terminal: %v", err)
	}

	coordinator := &Coordinator{Store: db}
	if settled := coordinator.settleMCPTerminals(ctx); settled != 1 {
		t.Fatalf("settled %d, want 1", settled)
	}
	finished, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.NeedsHuman ||
		finished.Result == nil || finished.Result.Status != "blocked" ||
		finished.Result.PhaseResult == nil || finished.Result.PhaseResult.Status != "blocked" {
		t.Fatalf("driven finish not typed blocked: %+v", finished)
	}
	advanced, err := db.MCPWorkStatus(ctx, a.ID, submission.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.State != factory.MCPWorkAccepted {
		t.Fatalf("receipt state %q, want accepted", advanced.State)
	}
	if settled := coordinator.settleMCPTerminals(ctx); settled != 0 {
		t.Fatalf("second drive settled %d, want 0", settled)
	}
}

// These fixtures bypass the public admission check. Settlement must propagate
// their mapping errors without manufacturing replacement worker results.
func TestSettleVerifyRepairWrongSubjectReturnsErrorPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	seed := func(t *testing.T, sqlDB *sql.DB, issue int64, tool factory.MCPTool, profile factory.MCPProfile, payload map[string]any) (factory.Assignment, factory.MCPSubmission) {
		t.Helper()
		a := phaseTestAssignment(t, project.ExecutionInvestigate, project.RoleCoder, true)
		// One unfinished assignment per repository issue: spread the
		// fenced cases across issues, keeping scope and subject bound.
		a.Issue, a.Subject.Issue.Issue = issue, issue
		if err := a.Validate(); err != nil {
			t.Fatalf("seed assignment invalid: %v", err)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
			t.Fatalf("seed assignment: %v", err)
		}
		staged := testMCPCapability()
		staged.AssignmentID = a.ID
		staged.Profile = profile
		staged.State = factory.MCPCapabilityStaged
		if err := db.StageMCPCapability(ctx, staged); err != nil {
			t.Fatalf("stage capability: %v", err)
		}
		if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID,
			staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
			t.Fatalf("activate capability: %v", err)
		}
		capability, err := db.GetMCPCapability(ctx, staged.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		submission := testMCPSubmission(capability, tool, factory.NewID(), encoded)
		if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatalf("record terminal: %v", err)
		}
		return a, submission
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	verifyPayload := map[string]any{
		"outcome": "verified", "delivery_id": factory.NewID(),
		"input_digest":   strings.Repeat("c", 64),
		"check_receipts": []any{map[string]any{}}, "coverage": []any{},
	}
	verifyAssignment, verifySubmission := seed(t, sqlDB, 11, factory.MCPToolFinishAppVerify, factory.MCPProfileTestPilot, verifyPayload)
	repairPayload := map[string]any{"subject": "stall:" + factory.NewID(), "report": map[string]any{}}
	repairAssignment, repairSubmission := seed(t, sqlDB, 12, factory.MCPToolFinishReassessment, factory.MCPProfileStrategist, repairPayload)

	for _, tc := range []struct {
		name       string
		assignment factory.Assignment
		submission factory.MCPSubmission
	}{
		{"verify", verifyAssignment, verifySubmission},
		{"repair", repairAssignment, repairSubmission},
	} {
		if settled, err := SettleMCPTerminalProposal(ctx, db, tc.assignment.ID, time.Now()); err == nil || settled {
			t.Fatalf("%s invalid finish did not return its error: settled=%v err=%v", tc.name, settled, err)
		}
		finished, err := db.Assignment(ctx, tc.assignment.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finished.Stage != factory.AssignmentAssigned || finished.Result != nil {
			t.Fatalf("%s invalid report manufactured a terminal result: %+v", tc.name, finished)
		}
		advanced, err := db.MCPWorkStatus(ctx, tc.assignment.ID, tc.submission.SubmissionID)
		if err != nil {
			t.Fatal(err)
		}
		if advanced.State != factory.MCPWorkRecorded {
			t.Fatalf("%s posted work changed after refusal: %q", tc.name, advanced.State)
		}
	}
	// A real stall report arriving before native completion must survive the
	// generic empty-stdout finish. A foreign inventory returns a mapping error.
	for _, foreign := range []bool{false, true} {
		a := phaseTestAssignment(t, project.ExecutionRepair, project.RoleCoder, false)
		a.Issue, a.Acceptance, a.PublicationAssignment = 0, "", ""
		a.Subject = factory.ExecutionSubject{Kind: factory.SubjectStall, Stall: &factory.StallSubject{StallID: factory.NewID(), CycleID: a.ID, InventoryDigest: strings.Repeat("9", 64)}}
		if err := a.Validate(); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(a)
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
			t.Fatal(err)
		}
		allowance := factory.AttemptAllowance{Limits: factory.AttemptLimits{ActiveMinutes: 30}, Stall: a.Subject.Stall, RootAssignment: a.ID, Repository: a.Repository, CheckpointUnix: time.Now().Unix(), Active: true}
		if err := allowance.Validate(); err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(allowance)
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`, a.Repository, a.Issue, a.ID, allowance.Revision, string(raw)); err != nil {
			t.Fatal(err)
		}
		staged := testMCPCapability()
		staged.AssignmentID, staged.RunID, staged.Profile, staged.State = a.ID, a.Run, factory.MCPProfileStrategist, factory.MCPCapabilityStaged
		if err := db.StageMCPCapability(ctx, staged); err != nil {
			t.Fatal(err)
		}
		if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID, staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
			t.Fatal(err)
		}
		capability, err := db.GetMCPCapability(ctx, staged.ID)
		if err != nil {
			t.Fatal(err)
		}
		report := factory.StallRepairReport{StallID: a.Subject.Stall.StallID, CycleID: a.ID, InventoryDigest: a.Subject.Stall.InventoryDigest, SourceCommit: a.SourceCommit, PreparationDigest: strings.Repeat("8", 64), Inspected: []factory.InspectedSource{{Path: "native-proof.txt", Commit: a.SourceCommit, Evidence: "Inspected retained requirement evidence"}}, Causes: []factory.CheckFailure{{CheckRef: "manual-result", Detail: "No authorized producer or accepted resolution"}}, RetainedIssueIndexes: []int64{1, 2}, Outcome: factory.RepairOutcomeIncomplete}
		if foreign {
			report.InventoryDigest = strings.Repeat("7", 64)
		}
		payload, _ := json.Marshal(map[string]any{"subject": "stall:" + report.StallID, "report": report})
		submission := testMCPSubmission(capability, factory.MCPToolFinishReassessment, factory.NewID(), payload)
		if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatal(err)
		}
		run := phaseTestRun(a, factory.Succeeded)
		run.Reconciled = true
		if err := finishFromRun(ctx, db, a, run, time.Now()); (err != nil) != foreign {
			t.Fatalf("foreign=%v settlement error=%v", foreign, err)
		}
		finished, err := db.Assignment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if foreign {
			if finished.Stage != factory.AssignmentAssigned || finished.Result != nil {
				t.Fatal("foreign stall inventory manufactured a result")
			}
		} else if finished.Outcome != factory.Succeeded || !finished.Result.Reported || finished.Result.PhaseResult.Output.Repair == nil || finished.Result.PhaseResult.Output.Repair.Outcome != factory.RepairOutcomeIncomplete {
			t.Fatalf("typed stall diagnosis lost behind native completion: %+v", finished.Result)
		}
		if settled, err := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); (err != nil) != foreign || settled {
			t.Fatalf("stall terminal replay changed finish: %v/%v", settled, err)
		}
	}
}

// TestSettleVerifyArtifactRefsResolvedPGDrive proves the R32.6 finish
// revalidation against real PostgreSQL: an app verification finish
// citing this assignment's complete artifact digest settles with the
// report recorded intact, while a finish citing an unknown digest
// fences blocked with its proposal preserved for inspection.
func TestSettleVerifyArtifactRefsResolvedPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	seed := func(t *testing.T, deliveryID string) (factory.Assignment, factory.MCPCapability) {
		t.Helper()
		a := phaseTestAssignment(t, project.ExecutionVerify, project.RoleReviewer, false)
		a.Issue = 0
		// App verification runs read-only with no publication slot
		// and no issue authority, mirroring dispatch launch.
		a.PublicationAssignment = ""
		a.Acceptance = ""
		a.Subject = factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &factory.DeliverySubject{
			DeliveryID: deliveryID, IntentID: "d" + strings.Repeat("1", 24), AcceptedRevision: "d" + strings.Repeat("2", 24),
			ScopeDigest: strings.Repeat("a", 64), PreparationDigest: strings.Repeat("b", 64),
			VerificationManifestDigest: strings.Repeat("c", 64), SourceCommit: a.SourceCommit,
		}}
		if err := a.Validate(); err != nil {
			t.Fatalf("verify assignment invalid: %v", err)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
			t.Fatalf("seed assignment: %v", err)
		}
		// A delivery finish closes its root admission, which the store
		// requires as a recorded attempt allowance like production
		// dispatch records. Seed the root allowance carrying this
		// assignment's exact delivery subject.
		allowance := factory.AttemptAllowance{
			Limits:         factory.AttemptLimits{ActiveMinutes: 10},
			RootAssignment: a.AttemptRoot, Repository: a.Repository, Issue: 0,
			Delivery:       a.Subject.Delivery,
			CheckpointUnix: time.Now().Unix(), Active: true,
		}
		if err := allowance.Validate(); err != nil {
			t.Fatalf("seed allowance invalid: %v", err)
		}
		allowanceRaw, err := json.Marshal(allowance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`,
			a.Repository, 0, a.AttemptRoot, allowance.Revision, string(allowanceRaw)); err != nil {
			t.Fatalf("seed allowance: %v", err)
		}
		staged := testMCPCapability()
		staged.AssignmentID = a.ID
		staged.Profile = factory.MCPProfileTestPilot
		staged.State = factory.MCPCapabilityStaged
		if err := db.StageMCPCapability(ctx, staged); err != nil {
			t.Fatalf("stage capability: %v", err)
		}
		if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID,
			staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
			t.Fatalf("activate capability: %v", err)
		}
		capability, err := db.GetMCPCapability(ctx, staged.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a, capability
	}
	upload := func(t *testing.T, capability factory.MCPCapability, uploadID, body string) string {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"upload_id": uploadID, "name": "evidence.txt", "media_type": "text/plain",
			"offset": 0, "content_base64": base64.StdEncoding.EncodeToString([]byte(body)), "final": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		artifact, _, err := db.UploadWorkerArtifact(ctx, capability, raw)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		if artifact.Digest == "" {
			t.Fatal("final upload returned no attested digest")
		}
		if _, err := db.ResolveArtifactDigest(ctx, capability.Repository, capability.AssignmentID, artifact.Digest); err != nil {
			t.Fatalf("resolve attested digest: %v", err)
		}
		return artifact.Digest
	}
	finish := func(t *testing.T, capability factory.MCPCapability, deliveryID string, refs []string) factory.MCPSubmission {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"outcome": "verified", "delivery_id": deliveryID, "input_digest": strings.Repeat("c", 64),
			"check_receipts": []any{map[string]any{"check_id": "app-check", "outcome": "passed", "log_excerpt": "checks green"}},
			"coverage":       []any{},
			"artifact_refs":  refs,
		})
		if err != nil {
			t.Fatal(err)
		}
		submission := testMCPSubmission(capability, factory.MCPToolFinishAppVerify, factory.NewID(), raw)
		if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatalf("record verify finish: %v", err)
		}
		return submission
	}
	goodDelivery := factory.NewID()
	goodAssignment, goodCap := seed(t, goodDelivery)
	digest := upload(t, goodCap, factory.NewID(), "hello verify")
	goodSubmission := finish(t, goodCap, goodDelivery, []string{digest})
	badDelivery := factory.NewID()
	badAssignment, badCap := seed(t, badDelivery)
	_ = upload(t, badCap, factory.NewID(), "hello verify")
	badSubmission := finish(t, badCap, badDelivery, []string{strings.Repeat("f", 64)})
	coordinator := &Coordinator{Store: db}
	if settled := coordinator.settleMCPTerminals(ctx); settled != 2 {
		t.Fatalf("settled %d, want 2", settled)
	}
	goodFinished, err := db.Assignment(ctx, goodAssignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if goodFinished.Stage != factory.AssignmentFinished || goodFinished.Outcome == factory.NeedsHuman ||
		goodFinished.Result == nil || goodFinished.Result.PhaseResult == nil ||
		goodFinished.Result.PhaseResult.Output == nil || goodFinished.Result.PhaseResult.Output.Verification == nil ||
		len(goodFinished.Result.PhaseResult.Output.Verification.ArtifactRefs) != 1 ||
		goodFinished.Result.PhaseResult.Output.Verification.ArtifactRefs[0] != digest {
		t.Fatalf("verified finish did not record its artifact ref: %+v", goodFinished)
	}
	goodReceipt, err := db.MCPWorkStatus(ctx, goodAssignment.ID, goodSubmission.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	if goodReceipt.State != factory.MCPWorkAccepted {
		t.Fatalf("verified receipt state %q, want accepted", goodReceipt.State)
	}
	badFinished, err := db.Assignment(ctx, badAssignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if badFinished.Stage != factory.AssignmentFinished || badFinished.Outcome != factory.NeedsHuman ||
		badFinished.Result == nil || badFinished.Result.Status != "blocked" {
		t.Fatalf("unknown digest finish not fenced blocked: %+v", badFinished)
	}
	badReceipt, err := db.MCPWorkStatus(ctx, badAssignment.ID, badSubmission.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	if badReceipt.State != factory.MCPWorkAccepted {
		t.Fatalf("fenced receipt state %q, want accepted", badReceipt.State)
	}
	if settled := coordinator.settleMCPTerminals(ctx); settled != 0 {
		t.Fatalf("second drive settled %d, want 0", settled)
	}
}

// finishArgsPlanHead builds the intent draft behind a worker-scale
// finish: a ~9KB objective with one accepted choice, mirroring the
// ~8.8KB objective+facts the fenced worker tried to inline. The
// staged input preserves the full objective, so the finish itself
// never needs to repeat it.
func finishArgsPlanHead() factory.IntentRevision {
	return factory.IntentRevision{
		ID: "d888888888888888888888888", Intent: "d777777777777777777777777", Owner: 7, Repository: 7,
		Objective: strings.Repeat("Record the draft plan with explicit legacy synchronization. ", 160),
		State:     "draft", MaxIssues: 10, CreatedUnix: 1700000000,
		Ideas: []factory.ProductIdea{
			{ID: "d333333333333333333333333", Description: "Capture leave requests", Consequence: "Requests can be recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
		},
	}
}

// finishArgsPlanWalkthroughs authors planning-agent walkthroughs bound
// to the head, mirroring the worker's own authored content: facts
// preserved from the admitted objective, two alternatives covering
// the resolved scope.
func finishArgsPlanWalkthroughs(t *testing.T, head factory.IntentRevision) factory.ProposalContent {
	t.Helper()
	basis, err := factory.ComposeProposalContent(head)
	if err != nil {
		t.Fatalf("walkthrough basis: %v", err)
	}
	content := factory.ProposalContent{
		Producer: factory.PlanningProducer, Supported: true,
		Intent: head.Intent, Revision: head.ID, Objective: head.Objective,
		Facts: basis.Facts, Excluded: basis.Excluded, Exclusions: basis.Exclusions,
		Unknowns: factory.DescribeAlternatives(head.Objective).Unknowns,
	}
	for _, key := range []string{"guided", "list"} {
		content.Alternatives = append(content.Alternatives, factory.ProductAlternative{
			Key: key, Name: key + " leave review", Audience: "Managers and their team",
			Summary:    "Track leave requests with a visible review step.",
			Steps:      []factory.WalkthroughStep{{Actor: "The person", Action: "records the leave request", Result: "The request is tracked"}},
			Visibility: "The person and the reviewer see the request.", Consequences: "Reviews take reviewer time.",
			Evidence: "The recorded draft and inspected source",
			Outcomes: []string{"Implement the recorded product objective", "Requests stay visible before acceptance"},
			IdeaIDs:  append([]string(nil), factory.ResolvedScope(head)...),
		})
	}
	if err := content.Validate(); err != nil {
		t.Fatalf("walkthroughs invalid: %v", err)
	}
	if err := factory.ValidatePlanningWalkthroughs(head, content); err != nil {
		t.Fatalf("walkthrough binding invalid: %v", err)
	}
	return content
}

// TestSettleProductPlanFinishArgsPGDrive proves the FJ-v3-526
// finish-args finding against real PostgreSQL: a worker-scale
// (~10KB, under the 64 KiB cap) but wellformed finish_product_plan
// payload carrying only candidates, walkthroughs, coverage and
// evidence_refs settles through SettleMCPTerminalProposal with
// intent, revision and operation bound from the staged planning
// input; the same shape with inlined top-level objective/facts
// still settles server-side because the settler binds only its
// known carriers and ignores unknown top-level fields. The actual
// gate the worker hit sits earlier: the peer/admission layer
// refuses unknown top-level fields before settlement, so raising
// the 64 KiB cap would not have helped.
func TestSettleProductPlanFinishArgsPGDrive(t *testing.T) {
	head := finishArgsPlanHead()
	content, err := factory.ComposeProposalContent(head)
	if err != nil {
		t.Fatalf("proposal content: %v", err)
	}
	in := factory.SemanticPlannerInput{
		Operation: factory.IntentPlanningPropose, Head: head, Content: content,
		Alternatives: factory.DescribeAlternatives(head.Objective),
		Inspected: []factory.InspectedSource{
			{Path: "internal/factory/intent.go", Commit: strings.Repeat("c", 40), Evidence: "Draft head carries attributed effective scope with no tasks yet"},
		},
	}
	prompt, err := StageIntentPlanningPrompt(head, in)
	if err != nil {
		t.Fatalf("staged prompt: %v", err)
	}
	if !strings.Contains(string(prompt), FinishProductPlanArgsGuidance) {
		t.Fatal("staged prompt omits the finish-args guidance the worker reads")
	}
	walkthroughs := finishArgsPlanWalkthroughs(t, head)
	candidates := []factory.SemanticCandidate{{
		Key: "keep", Rationale: "Keep the recorded draft as the single review baseline.",
		Tasks: []factory.IntentTask{{
			Key: "d999999999999999999999999", Objective: "Plan the recorded draft",
			Outcomes: []string{"Draft is planned"}, IdeaIDs: []string{"d333333333333333333333333"},
		}},
	}}
	// The FJ-v3-586 label-compat rule: the 4000 review-label bound
	// counts UTF-8 BYTES, not runes — the persisted-old-valid
	// standard. A 4000-byte multibyte objective (2000 runes) is
	// preserved verbatim through the real baseline; a 4002-byte one
	// (2001 runes, still under 4000 runes) falls back, so recorded
	// walkthroughs substituted under the byte rule keep verifying.
	multiIDs := []string{"d" + strings.Repeat("1", 24), "d" + strings.Repeat("2", 24), "d" + strings.Repeat("3", 24), "d" + strings.Repeat("4", 24)}
	verbatimObjective := strings.Repeat("é", 2000)
	if len(verbatimObjective) != 4000 {
		t.Fatalf("byte-window fixture out of its band: %d bytes", len(verbatimObjective))
	}
	kept := factory.ProposeIntent(factory.IntentRevision{Objective: verbatimObjective}, multiIDs)
	if len(kept.Ideas) == 0 || kept.Ideas[0].Description != verbatimObjective {
		t.Fatal("4000-byte multibyte objective was not preserved verbatim")
	}
	fallbackObjective := strings.Repeat("é", 2001)
	dropped := factory.ProposeIntent(factory.IntentRevision{Objective: fallbackObjective}, multiIDs)
	if len(dropped.Ideas) == 0 || dropped.Ideas[0].Description != "Implement the recorded product objective" {
		t.Fatalf("4002-byte multibyte objective did not fall back: %q", dropped.Ideas[0].Description)
	}
	// The FJ-v3-586 compat regression: a persisted pre-fix VALID
	// multibyte walkthrough — substitute Outcomes[0], verbatim
	// facts[0] — in the byte window (over 4000 bytes, under 4000
	// runes) passes acceptance AND review with exact identity and
	// digest unchanged, while a genuine unrelated mismatch still
	// refuses with the exact messages. No normalization of
	// mismatched facts, no digest/authority/history mutation, no
	// arbitrary-mismatch acceptance.
	compatSignal := "Approve leave requests with a visible reviewer decision. "
	compatObjective := compatSignal + strings.Repeat("é", 2000)
	if len(compatObjective) <= 4000 || utf8.RuneCountInString(compatObjective) > 4000 {
		t.Fatalf("compat objective outside the byte window: %d bytes, %d runes", len(compatObjective), utf8.RuneCountInString(compatObjective))
	}
	compatHead := factory.IntentRevision{
		ID: "d" + strings.Repeat("5", 24), Intent: "d" + strings.Repeat("6", 24), Owner: 7, Repository: 7,
		Objective: compatObjective, State: "draft", MaxIssues: 10, CreatedUnix: 1700000000,
		Ideas: []factory.ProductIdea{
			{ID: "d" + strings.Repeat("7", 24), Description: "Capture leave requests", Consequence: "Requests can be recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7},
		},
	}
	if err := compatHead.Validate(); err != nil {
		t.Fatalf("compat head invalid: %v", err)
	}
	compatDigest := compatHead.Digest()
	compatBasis, err := factory.ComposeProposalContent(compatHead)
	if err != nil {
		t.Fatalf("compat basis: %v", err)
	}
	if !compatBasis.Supported || len(compatBasis.Alternatives) != 2 || len(compatBasis.Alternatives[0].Outcomes) == 0 {
		t.Fatal("compat head left supported coverage")
	}
	// The recorded byte-rule label: substituted, not verbatim.
	for _, alternative := range compatBasis.Alternatives {
		if alternative.Outcomes[0] != "Implement the recorded product objective" {
			t.Fatalf("compat basis lost its byte-rule label: %q", alternative.Outcomes[0])
		}
	}
	compatContent := factory.ProposalContent{
		Producer: factory.PlanningProducer, Supported: true,
		Intent: compatHead.Intent, Revision: compatHead.ID, Objective: compatHead.Objective,
		Facts: compatBasis.Facts, Excluded: compatBasis.Excluded, Exclusions: compatBasis.Exclusions,
		Unknowns: compatBasis.Unknowns,
	}
	for _, alternative := range compatBasis.Alternatives {
		alternative.IdeaIDs = append([]string(nil), factory.ResolvedScope(compatHead)...)
		compatContent.Alternatives = append(compatContent.Alternatives, alternative)
	}
	if compatContent.Facts[0] != compatHead.Objective || len(compatContent.Facts[0]) != len(compatHead.Objective) {
		t.Fatal("compat walkthroughs lost their byte-verbatim facts")
	}
	if err := compatContent.Validate(); err != nil {
		t.Fatalf("compat content refused: %v", err)
	}
	// Acceptance: the recorded walkthrough revalidates clean.
	if err := factory.ValidatePlanningWalkthroughs(compatHead, compatContent); err != nil {
		t.Fatalf("persisted old-valid walkthrough refused acceptance: %v", err)
	}
	// Review: consolidation carries the recorded label and the
	// deterministic consistency check emits no outcome_gap.
	compatProposal, err := factory.ConsolidateContent(compatContent, compatContent.Alternatives[0].Key, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("compat consolidation refused: %v", err)
	}
	if compatProposal.Outcomes[0] != "Implement the recorded product objective" {
		t.Fatalf("compat proposal lost its recorded label: %q", compatProposal.Outcomes[0])
	}
	if mismatches := factory.CheckProposalEffects(compatContent, compatProposal); len(mismatches) != 0 {
		t.Fatalf("persisted old-valid walkthrough emitted mismatches: %+v", mismatches)
	}
	// Exact identity and digest unchanged through acceptance+review.
	if compatHead.Digest() != compatDigest || compatContent.Intent != compatHead.Intent ||
		compatContent.Revision != compatHead.ID || compatContent.Objective != compatHead.Objective {
		t.Fatal("acceptance or review mutated identity or digest")
	}
	// Genuine unrelated mismatch still refuses with the exact
	// messages: outcome_gap from consistency, and the planning
	// comparator's refusal for a verbatim long Outcomes[0].
	unrelated := compatContent.Alternatives[0]
	unrelated.Outcomes = append([]string{"An unrelated invented outcome"}, unrelated.Outcomes[1:]...)
	unrelatedProposal, err := factory.ConsolidateContent(factory.ProposalContent{
		Producer: compatContent.Producer, Supported: compatContent.Supported,
		Intent: compatContent.Intent, Revision: compatContent.Revision, Objective: compatContent.Objective,
		Facts: compatContent.Facts, Excluded: compatContent.Excluded, Exclusions: compatContent.Exclusions,
		Unknowns:     compatContent.Unknowns,
		Alternatives: append([]factory.ProductAlternative{unrelated}, compatContent.Alternatives[1:]...),
	}, unrelated.Key, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("unrelated consolidation refused: %v", err)
	}
	mismatches := factory.CheckProposalEffects(compatContent, unrelatedProposal)
	if len(mismatches) != 1 || mismatches[0].Class != factory.MismatchOutcomeGap ||
		mismatches[0].Detail != "The first outcome does not preserve the requested objective." {
		t.Fatalf("unrelated mismatch misreported: %+v", mismatches)
	}
	verbatim := compatContent
	verbatim.Alternatives = nil
	for _, alternative := range compatContent.Alternatives {
		alternative.Outcomes = append([]string{compatHead.Objective}, alternative.Outcomes[1:]...)
		verbatim.Alternatives = append(verbatim.Alternatives, alternative)
	}
	if err := factory.ValidatePlanningWalkthroughs(compatHead, verbatim); err == nil ||
		err.Error() != "planning walkthrough needs distinct alternatives preserving the objective" {
		t.Fatalf("verbatim long Outcomes[0] misreported: %v", err)
	}
	if compatContent.Alternatives[0].Outcomes[0] != "Implement the recorded product objective" {
		t.Fatal("refusal probe mutated the recorded walkthrough")
	}
	base := map[string]any{
		"candidates": candidates, "coverage": "Every effective outcome is covered by the single review baseline.",
		"evidence_refs": []string{"approved preparation"}, "walkthroughs": walkthroughs,
	}
	wellformed, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(wellformed) <= 10<<10 || len(wellformed) >= 64<<10 {
		t.Fatalf("worker-scale payload out of its band: %d bytes", len(wellformed))
	}
	withInline, err := json.Marshal(map[string]any{
		"candidates": candidates, "coverage": "Every effective outcome is covered by the single review baseline.",
		"evidence_refs": []string{"approved preparation"}, "walkthroughs": walkthroughs,
		"objective": head.Objective, "facts": []string{head.Objective},
	})
	if err != nil {
		t.Fatal(err)
	}

	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	seed := func(t *testing.T, payload []byte) (factory.Assignment, factory.MCPSubmission) {
		t.Helper()
		a := phaseTestAssignment(t, project.ExecutionDecompose, project.RoleCoder, false)
		// An intent decompose assignment is a fresh root: shared
		// repository slot, no issue authority, no publication.
		a.Issue, a.PublicationAssignment, a.Acceptance = 0, "", ""
		a.Subject = factory.ExecutionSubject{
			Kind:   factory.SubjectIntent,
			Intent: &factory.IntentSubject{IntentID: head.Intent, RevisionID: head.ID, ScopeDigest: head.Digest(), OwnerID: head.Owner},
		}
		a.SourceCommit = strings.Repeat("c", 40)
		a.Prompt = prompt
		sum := sha256.Sum256(prompt)
		a.PromptSHA = hex.EncodeToString(sum[:])
		if err := a.Validate(); err != nil {
			t.Fatalf("plan assignment invalid: %v", err)
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
			t.Fatalf("seed assignment: %v", err)
		}
		// A reported product plan finish closes its root admission,
		// which the store requires as a recorded intent attempt
		// allowance like production dispatch records.
		allowance := factory.AttemptAllowance{
			Limits:         factory.AttemptLimits{ActiveMinutes: 45},
			Intent:         &factory.IntentAttemptScope{IntentID: head.Intent, OwnerID: head.Owner},
			RootAssignment: a.ID, Repository: a.Repository, Issue: 0,
			CheckpointUnix: time.Now().Unix(), Active: true,
		}
		if err := allowance.Validate(); err != nil {
			t.Fatalf("seed allowance invalid: %v", err)
		}
		allowanceRaw, err := json.Marshal(allowance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`,
			a.Repository, 0, a.ID, allowance.Revision, string(allowanceRaw)); err != nil {
			t.Fatalf("seed allowance: %v", err)
		}
		staged := testMCPCapability()
		staged.AssignmentID, staged.Profile, staged.State = a.ID, factory.MCPProfileProductLead, factory.MCPCapabilityStaged
		if err := db.StageMCPCapability(ctx, staged); err != nil {
			t.Fatalf("stage capability: %v", err)
		}
		if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID,
			staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
			t.Fatalf("activate capability: %v", err)
		}
		capability, err := db.GetMCPCapability(ctx, staged.ID)
		if err != nil {
			t.Fatal(err)
		}
		submission := testMCPSubmission(capability, factory.MCPToolFinishProductPlan, factory.NewID(), payload)
		if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
			t.Fatalf("record terminal: %v", err)
		}
		return a, submission
	}
	cleanAssignment, cleanSubmission := seed(t, wellformed)
	inlineAssignment, inlineSubmission := seed(t, withInline)

	for _, id := range []string{cleanAssignment.ID, inlineAssignment.ID} {
		settled, err := SettleMCPTerminalProposal(ctx, db, id, time.Now())
		if err != nil || !settled {
			t.Fatalf("settle %s: settled=%v err=%v", id, settled, err)
		}
	}
	for _, tc := range []struct {
		name       string
		assignment factory.Assignment
		submission factory.MCPSubmission
	}{
		{"wellformed", cleanAssignment, cleanSubmission},
		{"inline-objective-facts", inlineAssignment, inlineSubmission},
	} {
		finished, err := db.Assignment(ctx, tc.assignment.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.Succeeded ||
			finished.Result == nil || !finished.Result.Reported ||
			finished.Result.PhaseResult == nil || finished.Result.PhaseResult.Output == nil ||
			finished.Result.PhaseResult.Output.Decomposition == nil {
			t.Fatalf("%s finish unsettled: stage=%q outcome=%q reason=%q resultReported=%v",
				tc.name, finished.Stage, finished.Outcome, finished.Reason, finished.Result != nil && finished.Result.Reported)
		}
		decomposition := finished.Result.PhaseResult.Output.Decomposition
		if decomposition.Intent != head.Intent || decomposition.Revision != head.ID ||
			decomposition.Operation != factory.IntentPlanningPropose || len(decomposition.Candidates) != 1 ||
			decomposition.Walkthroughs == nil {
			t.Fatalf("%s finish not bound to its staged input: %+v", tc.name, decomposition)
		}
		// The FJ-v3-538 reconciliation: the ~9KB nested objective and
		// facts settle byte-verbatim — the staged prompt no longer
		// caps preserved fields at 4000 characters.
		settledWalkthroughs := decomposition.Walkthroughs
		if utf8.RuneCountInString(head.Objective) <= 4000 {
			t.Fatalf("%s head objective %d characters does not prove the long-field path", tc.name, utf8.RuneCountInString(head.Objective))
		}
		if settledWalkthroughs.Objective != head.Objective || len(settledWalkthroughs.Facts) == 0 ||
			settledWalkthroughs.Facts[0] != head.Objective {
			t.Fatalf("%s finish did not preserve its long nested objective/facts verbatim", tc.name)
		}
		advanced, err := db.MCPWorkStatus(ctx, tc.assignment.ID, tc.submission.SubmissionID)
		if err != nil {
			t.Fatal(err)
		}
		if advanced.State != factory.MCPWorkAccepted {
			t.Fatalf("%s receipt state %q, want accepted", tc.name, advanced.State)
		}
	}
	for _, id := range []string{cleanAssignment.ID, inlineAssignment.ID} {
		settled, err := SettleMCPTerminalProposal(ctx, db, id, time.Now())
		if err != nil || settled {
			t.Fatalf("replay %s: settled=%v err=%v", id, settled, err)
		}
	}
}

// seedStallTerminalPGDrive records a wellformed stall repair finish
// behind a persisted stall root. Seed the attempt allowance only when
// withAllowance is set: without it the store finish refuses with
// factory.ErrNotFound while the assignment stays assigned — the FUX26
// missing-allowance class, never a concurrent finish.
func seedStallTerminalPGDrive(t *testing.T, ctx context.Context, db *store.Store, sqlDB *sql.DB, withAllowance bool) (factory.Assignment, factory.MCPSubmission) {
	t.Helper()
	a := phaseTestAssignment(t, project.ExecutionRepair, project.RoleCoder, false)
	a.Issue, a.Acceptance, a.PublicationAssignment = 0, "", ""
	a.Subject = factory.ExecutionSubject{Kind: factory.SubjectStall, Stall: &factory.StallSubject{StallID: factory.NewID(), CycleID: a.ID, InventoryDigest: strings.Repeat("9", 64)}}
	if err := a.Validate(); err != nil {
		t.Fatalf("seed assignment invalid: %v", err)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(raw)); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	if withAllowance {
		allowance := factory.AttemptAllowance{Limits: factory.AttemptLimits{ActiveMinutes: 30}, Stall: a.Subject.Stall, RootAssignment: a.ID, Repository: a.Repository, CheckpointUnix: time.Now().Unix(), Active: true}
		if err := allowance.Validate(); err != nil {
			t.Fatalf("seed allowance invalid: %v", err)
		}
		allowanceRaw, err := json.Marshal(allowance)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES($1,$2,$3,$4,$5)`,
			a.Repository, a.Issue, a.ID, allowance.Revision, string(allowanceRaw)); err != nil {
			t.Fatalf("seed allowance: %v", err)
		}
	}
	staged := testMCPCapability()
	staged.AssignmentID, staged.RunID, staged.Profile, staged.State = a.ID, a.Run, factory.MCPProfileStrategist, factory.MCPCapabilityStaged
	if err := db.StageMCPCapability(ctx, staged); err != nil {
		t.Fatalf("stage capability: %v", err)
	}
	if err := db.ActivateMCPCapability(ctx, staged.ID, staged.Generation, staged.RunID,
		staged.AssignmentID, staged.Authority, time.Now().Unix(), false); err != nil {
		t.Fatalf("activate capability: %v", err)
	}
	capability, err := db.GetMCPCapability(ctx, staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := factory.StallRepairReport{StallID: a.Subject.Stall.StallID, CycleID: a.ID, InventoryDigest: a.Subject.Stall.InventoryDigest, SourceCommit: a.SourceCommit, PreparationDigest: strings.Repeat("8", 64), Inspected: []factory.InspectedSource{{Path: "native-proof.txt", Commit: a.SourceCommit, Evidence: "Inspected retained requirement evidence"}}, Causes: []factory.CheckFailure{{CheckRef: "manual-result", Detail: "No authorized producer or accepted resolution"}}, RetainedIssueIndexes: []int64{1, 2}, Outcome: factory.RepairOutcomeIncomplete}
	payload, err := json.Marshal(map[string]any{"subject": "stall:" + report.StallID, "report": report})
	if err != nil {
		t.Fatal(err)
	}
	submission := testMCPSubmission(capability, factory.MCPToolFinishReassessment, factory.NewID(), payload)
	if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
		t.Fatalf("record terminal: %v", err)
	}
	return a, submission
}

// TestSettleMissingAllowanceKeepsReceiptRecordedPGDrive proves the
// FUX26 fix against real PostgreSQL: a missing attempt allowance makes
// FinishAssignment refuse with factory.ErrNotFound while the assignment stays
// assigned, and the settler must return that error with the receipt left
// recorded — never advance it recorded→accepted and strand
// still-assigned work. A second pass refuses the same way, so the
// recorded terminal stays retryable.
func TestSettleMissingAllowanceKeepsReceiptRecordedPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	a, submission := seedStallTerminalPGDrive(t, ctx, db, sqlDB, false)
	for pass := 0; pass < 2; pass++ {
		settled, err := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now())
		if err == nil || settled {
			t.Fatalf("pass %d: missing-allowance settlement=%v err=%v, want a refusal", pass, settled, err)
		}
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("pass %d: missing-allowance error=%v, want the store miss surfaced", pass, err)
		}
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Stage != factory.AssignmentAssigned {
		t.Fatalf("missing allowance finished its assignment: %+v", stored)
	}
	receipt, err := db.MCPWorkStatus(ctx, a.ID, submission.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != factory.MCPWorkRecorded {
		t.Fatalf("missing-allowance receipt state %q, want recorded", receipt.State)
	}
}

// TestSettleConcurrentFinishSettlesAcceptedPGDrive proves the FUX26
// fix preserves genuine concurrent settlement against real
// PostgreSQL: two settlers racing one recorded stall repair terminal finish
// exactly once — exactly one reports settled, the assignment ends
// finished, and the receipt still advances recorded→accepted.
func TestSettleConcurrentFinishSettlesAcceptedPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	a, submission := seedStallTerminalPGDrive(t, ctx, db, sqlDB, true)
	type outcome struct {
		settled bool
		err     error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			settled, err := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now())
			results <- outcome{settled: settled, err: err}
		}()
	}
	winners := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("concurrent settlement errored: %v", r.err)
		}
		if r.settled {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent settlement winners=%d, want exactly 1", winners)
	}
	finished, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Stage != factory.AssignmentFinished || finished.Outcome != factory.Succeeded {
		t.Fatalf("concurrent finish unsettled: %+v", finished)
	}
	advanced, err := db.MCPWorkStatus(ctx, a.ID, submission.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.State != factory.MCPWorkAccepted {
		t.Fatalf("concurrent receipt state %q, want accepted", advanced.State)
	}
}

// finishRacingSettleStore wraps a real MCPTerminalSettleStore and lands a
// genuine concurrent finish inside FinishAssignment itself: the settler's
// initial read still observes the assigned row (no fast path); the wrapper
// first completes the row through the real Store with the settler's own
// valid finished value (genuine Store-produced finish, not scaffolding),
// then delegates the settler's finish, which must miss on the
// already-finished row. That miss forces the genuine factory.ErrNotFound→reread
// branch deterministically without goroutine racing.
type finishRacingSettleStore struct {
	MCPTerminalSettleStore
	firstErr  error
	secondErr error
	finishes  int
}

func (s *finishRacingSettleStore) FinishAssignment(ctx context.Context, a factory.Assignment) error {
	s.finishes++
	s.firstErr = s.MCPTerminalSettleStore.FinishAssignment(ctx, a)
	if s.firstErr != nil {
		return s.firstErr
	}
	s.secondErr = s.MCPTerminalSettleStore.FinishAssignment(ctx, a)
	return s.secondErr
}

// TestSettleFinishedRowRereadAcceptsPGDrive deterministically traverses the
// FUX26 factory.ErrNotFound→reread branch against real PostgreSQL with a genuine
// Store-produced finish: the initial read observes the assigned row, the
// concurrent winner completes it through the real Store inside
// FinishAssignment, the delegated finish must miss with factory.ErrNotFound (asserted
// explicitly, proving the branch was taken and not the fast path), and the
// settler must re-read the terminal row and advance the receipt
// recorded→accepted.
func TestSettleFinishedRowRereadAcceptsPGDrive(t *testing.T) {
	db, dsn := postgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	a, submission := seedStallTerminalPGDrive(t, ctx, db, sqlDB, true)
	racing := &finishRacingSettleStore{MCPTerminalSettleStore: db}
	settled, err := SettleMCPTerminalProposal(ctx, racing, a.ID, time.Now())
	if err != nil {
		t.Fatalf("reread settlement errored: %v", err)
	}
	if !settled {
		t.Fatal("reread settlement parked, want the recorded receipt accepted")
	}
	if racing.finishes != 1 {
		t.Fatalf("FinishAssignment invoked %d times, want exactly 1 (no fast path)", racing.finishes)
	}
	if racing.firstErr != nil {
		t.Fatalf("concurrent winner finish error=%v, want a genuine first completion", racing.firstErr)
	}
	if !errors.Is(racing.secondErr, store.ErrNotFound) {
		t.Fatalf("delegated finish error=%v, want the factory.ErrNotFound miss that forces the reread branch", racing.secondErr)
	}
	stored, err := db.Assignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Stage != factory.AssignmentFinished {
		t.Fatalf("reread left assignment: %+v", stored)
	}
	advanced, err := db.MCPWorkStatus(ctx, a.ID, submission.SubmissionID)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.State != factory.MCPWorkAccepted {
		t.Fatalf("reread receipt state %q, want accepted", advanced.State)
	}
}
