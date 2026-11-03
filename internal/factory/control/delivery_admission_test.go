package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/application"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func checkFactoryApplicationReadback(t *testing.T, ctx context.Context, db *store.Store, a factory.Assignment, bundle string) string {
	t.Helper()
	r := application.Record{Application: application.Spec{ID: "a" + strings.Repeat("1", 24), Revision: 1, Name: "FactoryFixture", ArtifactSHA256: strings.Repeat("2", 64), BundleSHA256: strings.Repeat("3", 64), ToolsSHA256: strings.Repeat("4", 64), RuntimeImage: "sha256:" + strings.Repeat("5", 64), Image: "sha256:" + strings.Repeat("6", 64), Data: "a" + strings.Repeat("7", 24), OwnerTeam: "12345678-1234-1234-1234-123456789abc", Origin: "https://factory-app.example.test", Port: 24001}, AdministratorID: 7}
	if err := db.CreateApplication(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := db.SetApplicationProject(ctx, r.Application.ID, 7, a.Repository); err != nil {
		t.Fatal(err)
	}
	if proof, err := db.VerifiedApplicationDelivery(ctx, r.Application.ID); err != nil || proof != nil {
		t.Fatal("labels or project link supplied byte proof", proof, err)
	}
	o := application.Observation{ID: r.Application.ID, Revision: 1, Image: r.Application.Image, Container: strings.Repeat("a", 64), Origin: r.Application.Origin, BundleFileSHA256: strings.Repeat("f", 64)}
	if err := db.ObserveApplication(ctx, r.Application.ID, 7, 1, nil, &o); err != nil {
		t.Fatal(err)
	}
	if proof, err := db.VerifiedApplicationDelivery(ctx, r.Application.ID); err != nil || proof != nil {
		t.Fatal("different native bundle matched delivery", proof, err)
	}
	matched := o
	matched.BundleFileSHA256 = bundle
	if err := db.ObserveApplication(ctx, r.Application.ID, 7, 1, &o, &matched); err != nil {
		t.Fatal(err)
	}
	proof, err := db.VerifiedApplicationDelivery(ctx, r.Application.ID)
	if err != nil || proof == nil || proof.Run != a.Run || proof.SourceCommit != a.SourceCommit || proof.Revision != a.Subject.Delivery.AcceptedRevision || proof.BundleFileSHA256 != bundle || proof.Environment != "company" {
		t.Fatal("native bytes did not join canonical delivery", proof, err)
	}
	if err := db.SetApplicationProject(ctx, r.Application.ID, 7, a.Repository+1); err != nil {
		t.Fatal(err)
	}
	if proof, err := db.VerifiedApplicationDelivery(ctx, r.Application.ID); err != nil || proof != nil {
		t.Fatal("foreign repository borrowed verified bytes", proof, err)
	}
	if err := db.SetApplicationProject(ctx, r.Application.ID, 7, a.Repository); err != nil {
		t.Fatal(err)
	}
	request, delivery, err := db.VerifiedFactoryBuildInput(ctx, a.Repository, a.Subject.Delivery.DeliveryID, "company")
	if err != nil || request.Run != a.Run || delivery.BundleFileSHA256 != bundle || delivery.SourceCommit != a.SourceCommit {
		t.Fatal("canonical delivery did not admit its exact native packaging input", request, delivery, err)
	}
	if _, _, err := db.VerifiedFactoryBuildInput(ctx, a.Repository+1, a.Subject.Delivery.DeliveryID, "company"); err == nil {
		t.Fatal("foreign repository admitted packaging")
	}
	artifact := application.BuildArtifact{Environment: "company", Project: request.Project, Run: request.Run, Incarnation: request.Incarnation, ManifestDigest: request.ManifestDigest, SourceCommit: delivery.SourceCommit, Name: "PackagedFixture", ArtifactSHA256: r.Application.ArtifactSHA256, BundleSHA256: r.Application.BundleSHA256, BundleFileSHA256: bundle, StorageSHA256: strings.Repeat("8", 64), ToolsSHA256: r.Application.ToolsSHA256, RuntimeImage: r.Application.RuntimeImage, Image: "sha256:" + strings.Repeat("9", 64), Origin: "https://packaged-app.example.test"}
	for _, mismatch := range []string{"hash", "source", "run"} {
		foreign := artifact
		switch mismatch {
		case "hash":
			foreign.BundleFileSHA256 = strings.Repeat("f", 64)
		case "source":
			foreign.SourceCommit = strings.Repeat("f", 40)
		case "run":
			foreign.Run = strings.Repeat("f", 32)
		}
		if _, err := db.SaveApplicationBuild(ctx, a.Repository, delivery.DeliveryID, foreign); err == nil {
			t.Fatal("foreign packaged artifact admitted", mismatch)
		}
	}
	build, err := db.SaveApplicationBuild(ctx, a.Repository, delivery.DeliveryID, artifact)
	if err != nil || build.Artifact != artifact || build.Delivery != delivery {
		t.Fatal("native build was not retained", build, err)
	}
	again, err := db.SaveApplicationBuild(ctx, a.Repository, delivery.DeliveryID, artifact)
	if err != nil || again.ID != build.ID {
		t.Fatal("build response recovery changed its identity", again, err)
	}
	builds, err := db.ApplicationBuilds(ctx, a.Repository, delivery.DeliveryID)
	if err != nil || len(builds) != 1 {
		t.Fatal("build response recovery duplicated inventory", builds, err)
	}
	setup, err := db.CreateApplicationBuildSetup(ctx, a.Repository, build.ID, 7)
	if err != nil || setup.Application.Image != artifact.Image || setup.Application.Origin != artifact.Origin || setup.Application.Name != artifact.Name {
		t.Fatal("build references did not produce the owner setup", setup, err)
	}
	setupAgain, err := db.CreateApplicationBuildSetup(ctx, a.Repository, build.ID, 7)
	if err != nil || setupAgain.ID != setup.ID || setupAgain.Application != setup.Application || setupAgain.ExpiresAt != setup.ExpiresAt {
		t.Fatal("setup response recovery changed identity or data", setupAgain, err)
	}
	settled := application.SetupResult{Operation: setup.ID, Observation: application.SetupObservation{}, Receipt: &application.SetupReceipt{Schema: "canlang.soda.setup.v1", Operation: setup.ID, App: artifact.Name, ArtifactDigest: artifact.ArtifactSHA256, BundleDigest: artifact.BundleSHA256, StorageFingerprint: artifact.StorageSHA256, OwnerTeam: r.Application.OwnerTeam, CompletedAt: time.Now().Unix()}, Application: &application.Observation{ID: setup.Application.ID, Revision: 1, Image: artifact.Image, Container: strings.Repeat("b", 64), Origin: artifact.Origin, BundleFileSHA256: bundle}}
	if err := db.SettleApplicationSetup(ctx, setup, settled, false); err != nil {
		t.Fatal("native owner receipt did not settle packaged app", err)
	}
	if repository, err := db.ApplicationProject(ctx, setup.Application.ID); err != nil || repository != a.Repository {
		t.Fatal("settled packaged app lost its project", repository, err)
	}
	if retained, err := db.Application(ctx, setup.Application.ID, 7); err != nil || retained.Application.Data != setup.Application.Data || retained.Application.OwnerTeam != r.Application.OwnerTeam {
		t.Fatal("settled packaged app changed its admitted data or owner", retained, err)
	}
	previewRequest, previewDelivery, err := db.VerifiedFactoryBuildInput(ctx, a.Repository, delivery.DeliveryID, "preview")
	if err != nil || previewRequest.Environment != "preview" || previewDelivery.BundleFileSHA256 == bundle {
		t.Fatal("preview did not select separate native output", previewRequest, previewDelivery, err)
	}
	previewArtifact := artifact
	previewArtifact.Environment, previewArtifact.Origin, previewArtifact.Image, previewArtifact.BundleFileSHA256 = "preview", "https://preview.packaged-app.example.test", "sha256:"+strings.Repeat("a", 64), previewDelivery.BundleFileSHA256
	wrongEnvironment := previewArtifact
	wrongEnvironment.BundleFileSHA256 = bundle
	if _, err := db.SaveApplicationBuild(ctx, a.Repository, delivery.DeliveryID, wrongEnvironment); err == nil {
		t.Fatal("company bytes relabeled as preview")
	}
	preview, err := db.SaveApplicationBuild(ctx, a.Repository, delivery.DeliveryID, previewArtifact)
	if err != nil || preview.ID == build.ID || preview.Delivery.Environment != "preview" {
		t.Fatal("preview inventory overlaps company", preview, err)
	}
	previewAgain, err := db.SaveApplicationBuild(ctx, a.Repository, delivery.DeliveryID, previewArtifact)
	if err != nil || previewAgain.ID != preview.ID {
		t.Fatal("preview response recovery changed identity", previewAgain, err)
	}
	builds, err = db.ApplicationBuilds(ctx, a.Repository, delivery.DeliveryID)
	if err != nil || len(builds) != 2 {
		t.Fatal("environment inventories were conflated or duplicated", builds, err)
	}
	previewSetup, err := db.CreateApplicationBuildSetup(ctx, a.Repository, preview.ID, 7)
	if err != nil || previewSetup.ID == setup.ID || previewSetup.Application.ID == setup.Application.ID || previewSetup.Application.Data == setup.Application.Data || previewSetup.Application.Origin == setup.Application.Origin || previewSetup.Application.Port == setup.Application.Port {
		t.Fatal("preview setup borrowed company identity or data", previewSetup, err)
	}
	previewSetupAgain, err := db.CreateApplicationBuildSetup(ctx, a.Repository, preview.ID, 7)
	if err != nil || previewSetupAgain.ID != previewSetup.ID || previewSetupAgain.Application != previewSetup.Application {
		t.Fatal("preview setup response recovery changed data", previewSetupAgain, err)
	}
	previewOwner := "22345678-1234-1234-1234-123456789abc"
	previewSettled := application.SetupResult{Operation: previewSetup.ID, Observation: application.SetupObservation{}, Receipt: &application.SetupReceipt{Schema: "canlang.soda.setup.v1", Operation: previewSetup.ID, App: previewArtifact.Name, ArtifactDigest: previewArtifact.ArtifactSHA256, BundleDigest: previewArtifact.BundleSHA256, StorageFingerprint: previewArtifact.StorageSHA256, OwnerTeam: previewOwner, CompletedAt: time.Now().Unix()}, Application: &application.Observation{ID: previewSetup.Application.ID, Revision: 1, Image: previewArtifact.Image, Container: strings.Repeat("c", 64), Origin: previewArtifact.Origin, BundleFileSHA256: previewArtifact.BundleFileSHA256}}
	if err := db.SettleApplicationSetup(ctx, previewSetup, previewSettled, false); err != nil {
		t.Fatal("preview native owner receipt did not settle", err)
	}
	if projection, err := db.VerifiedApplicationDelivery(ctx, previewSetup.Application.ID); err != nil || projection == nil || projection.Environment != "preview" || projection.BundleFileSHA256 != previewArtifact.BundleFileSHA256 {
		t.Fatal("preview did not retain exact native provenance", projection, err)
	}
	if retained, err := db.Application(ctx, previewSetup.Application.ID, 7); err != nil || retained.Application.Data != previewSetup.Application.Data || retained.Application.OwnerTeam != previewOwner {
		t.Fatal("preview did not retain its own data and owner", retained, err)
	}
	return r.Application.ID
}

func TestDeliveredIntentAdmitsScopedTestPilotOnce(t *testing.T) {
	for _, fixture := range []struct {
		early       bool
		state       string
		failed      bool
		spent       int64
		adopted     bool
		checkFailed bool
	}{{state: factory.ReassessmentCollecting}, {early: true, state: factory.ReassessmentCollecting}, {state: factory.ReassessmentAttention}, {early: true, state: factory.ReassessmentAttention}, {early: true, state: factory.ReassessmentCollecting, failed: true}, {early: true, state: factory.ReassessmentAttention, failed: true}, {early: true, state: factory.ReassessmentCollecting, spent: 3540}, {early: true, state: factory.ReassessmentCollecting, spent: 3600}, {state: factory.ReassessmentCollecting, adopted: true}, {state: factory.ReassessmentAttention, adopted: true}, {state: factory.ReassessmentAttention, adopted: true, failed: true}, {state: factory.ReassessmentAttention, adopted: true, checkFailed: true}} {
		early := fixture.early
		t.Run(fmt.Sprintf("%s/early-terminal-%t/reported-failed-%t/spent-%d/adopted-%t/check-failed-%t", fixture.state, early, fixture.failed, fixture.spent, fixture.adopted, fixture.checkFailed), func(t *testing.T) {
			ctx := context.Background()
			db, dsn := dispatchTestDB(t)
			fx := dispatchSeed(t, db)
			sponsor, err := db.Sponsorship(ctx, fx.repo, "conn")
			if err != nil {
				t.Fatal(err)
			}
			sponsor.Roles = []string{project.RoleCoder, project.RoleReviewer}
			if err = db.SaveSponsorship(ctx, sponsor); err != nil {
				t.Fatal(err)
			}
			idea := factory.ProductIdea{ID: phasedDecisionID(51), Description: "A starter saves a task", Consequence: "Tasks persist", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
			task := factory.IntentTask{Key: phasedDecisionID(52), Objective: "Implement task storage", Outcomes: []string{"Tasks persist"}, IdeaIDs: []string{idea.ID}}
			revision := factory.IntentRevision{ID: phasedDecisionID(41), Intent: phasedDecisionID(40), Owner: 7, Repository: 7, Objective: "Task app", State: "accepted", MaxIssues: 10, CreatedUnix: 1, Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{task}, EffectiveOutcomes: []string{idea.ID}}
			if err = db.SaveIntentRevision(ctx, revision); err != nil {
				t.Fatal(err)
			}
			binding := factory.IntentTaskBinding{RevisionID: revision.ID, TaskKey: task.Key, IssueID: 3, IssueIndex: 3, AcceptanceID: phasedDecisionID(53), CreatedUnix: 1}
			if err = db.BindIntentTaskAcceptance(ctx, binding); err != nil {
				t.Fatal(err)
			}
			candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16)
			if err != nil || len(candidates) != 0 {
				t.Fatalf("undelivered work admitted: %+v %v", candidates, err)
			}
			sqlDB, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			event := factory.CompletionEvent{ID: factory.NewID(), Repository: 7, Issue: 3, Generation: 1, PublicationID: factory.NewID(), AcceptanceID: binding.AcceptanceID, MergedCommit: fx.tip, ClosingOccurrence: "1"}
			raw, _ := json.Marshal(event)
			if _, err = sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,1,$1,$2,'1',$3)`, event.ID, event.PublicationID, string(raw)); err != nil {
				t.Fatal(err)
			}
			// A real merge also seeds its reassessment cycle. Verification of the
			// delivered application must not require changing that independent
			// cycle or repeating its already-recorded judgment to open the fence.
			cycle := factory.ReassessmentCycle{CycleID: factory.NewID(), EventID: event.ID, Repository: 7, Generation: 1, State: fixture.state}
			if fixture.state == factory.ReassessmentAttention {
				cycle.Decision = factory.ReassessmentOptionContinue
				cycle.Ledger.DecisionCalls = []string{factory.NewID()}
			}
			reassessment := store.ReassessmentRecord{Cycle: cycle, Completion: event, Issue: event.Issue}
			if fixture.state == factory.ReassessmentAttention {
				reassessment.WaitReason = "decision_not_actionable"
			}
			if err = reassessment.Validate(); err != nil {
				t.Fatal(err)
			}
			raw, _ = json.Marshal(reassessment)
			if _, err = sqlDB.Exec(`INSERT INTO factory_reassessments(cycle,repository,generation,event,state,data) VALUES($1,7,1,$2,$3,$4)`, cycle.CycleID, event.ID, cycle.State, string(raw)); err != nil {
				t.Fatal(err)
			}
			var originalCycle string
			if err = sqlDB.QueryRow(`SELECT data::text FROM factory_reassessments WHERE cycle=$1`, cycle.CycleID).Scan(&originalCycle); err != nil {
				t.Fatal(err)
			}
			if pending, err := db.CompletionPending(ctx, 7); err != nil || len(pending) != 1 || pending[0] != 1 {
				t.Fatal("real completion must keep its fence closed", pending, err)
			}
			candidates, _, err = db.DeliveredIntentCandidates(ctx, "", 16)
			if err != nil || len(candidates) != 1 {
				t.Fatalf("delivered work missing: %+v %v", candidates, err)
			}
			prep, err := db.Preparation(ctx, "f222222222222222222222222")
			if err != nil {
				t.Fatal(err)
			}
			manifest := factory.ApprovedAppCheck{IntentID: revision.Intent, AcceptedRevision: revision.ID, Outcomes: []string{idea.ID}}
			encoded, _ := json.Marshal(manifest)
			script := []byte("#!/bin/sh\n" + factory.ApprovedAppCheckPrefix + string(encoded) + "\nexit 0\n")
			if fixture.adopted {
				// Catalog/adoption itself is exercised by the production PL/Generate
				// integrations. This fixture supplies its recorded authority to the
				// actual delivery packet and native-receipt consumer.
				script = []byte("#!/bin/sh\n# approved deterministic task assertions\nexit 0\n")
				coder, err := db.Preparation(ctx, "f111111111111111111111111")
				if err != nil {
					t.Fatal(err)
				}
				catalog := factory.ApprovedProjectCheckCatalog{Project: fx.proj, Preparation: coder.Preparation.ID, Requirements: coder.Preparation.Requirements.ID, Approval: coder.Preparation.Approval.ID, SourceCommit: fx.tip, PreparationDigest: factory.DeliveryPreparationDigest(coder.Preparation), CheckDigest: factory.MCPInputDigest(script)}
				proposal := factory.VerificationManifestProposal{Outcomes: revision.EffectiveOutcomes, Checks: []factory.ManifestCheckRef{{Outcome: idea.ID, CatalogID: factory.ApprovedAppCheckID, Artifacts: []string{factory.ProjectCheckReceiptArtifact}, Fixtures: []string{factory.ProjectCheckPreparedFixture}}}}
				adopted := &factory.AdoptedVerificationManifest{Intent: revision.Intent, AcceptedRevision: revision.ID, ScopeDigest: revision.Digest(), Proposal: proposal, Catalog: catalog}
				if err := adopted.ValidateFor(revision); err != nil {
					t.Fatal(err)
				}
				generation := factory.IntentGeneration{RevisionID: revision.ID, IntentID: revision.Intent, IntentDigest: revision.Digest(), State: "completed", SubmittedTasks: revision.Tasks, VerificationManifest: adopted, VerificationManifestDigest: adopted.Digest()}
				raw, _ := json.Marshal(generation)
				if _, err := sqlDB.Exec(`INSERT INTO factory_intent_generations(revision,data) VALUES($1,$2)`, revision.ID, string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			zero := 0
			fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) {
				return execution.PreparationState{Container: strings.Repeat("c", 64), ID: prep.Preparation.ID, Project: fx.proj, Role: project.RoleReviewer, SourceCommit: fx.tip, SetupDigest: prep.Preparation.SetupDigest, Phase: project.PrepareReady, Ready: true, CheckExit: &zero, Output: "task storage passed"}, nil
			}
			if fixture.adopted {
				prep.State, err = fx.host.inspectPreparation(project.PrepareInspect{Project: fx.proj, ID: prep.Preparation.ID})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.ObservePreparation(ctx, prep); err != nil {
					t.Fatal("retain complete native preparation observation", err)
				}
			}
			fx.host.contextRead = func(request project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
				material := fixturePreparationContext(request, role)
				material.Check = script
				return material, nil
			}
			c := &Coordinator{Store: db, Host: fx.host, DispatchReads: &appVerificationReads{tip: fx.tip}}
			if fixture.adopted && !fixture.checkFailed {
				good := script
				script = append(append([]byte(nil), script...), []byte("# changed approved assertions\n")...)
				if _, _, _, err := c.deliveryAdmission(ctx, candidates[0]); err == nil {
					t.Fatal("changed protected definitions reached delivery admission")
				}
				script = good
			}
			subject, preparation, prompt, err := c.deliveryAdmission(ctx, candidates[0])
			if err != nil {
				t.Fatal(err)
			}
			if preparation != prep.Preparation.ID || subject.Delivery == nil || len(prompt) == 0 {
				t.Fatal("missing delivery scope")
			}
			deps := fx.deps()
			deps.MCPKey = []byte("test-delivery-key-32-bytes-long!!")
			deps.ContextVerification = c.ApplicationVerificationEvidence
			deps.VerificationManifest = c.stageDeliveryVerificationManifest
			deps.Priority = &PriorityJudgments{Journal: db, Chooser: &bundleGapChooser{pick: factory.ContextBundleCurrent}}
			occupancy, err := snapshotOccupancy(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []struct {
				name string
				edit func(*factory.DeliverySubject)
			}{
				{"foreign-intent", func(s *factory.DeliverySubject) { s.IntentID = phasedDecisionID(61) }},
				{"stale-accepted-head", func(s *factory.DeliverySubject) { s.AcceptedRevision = phasedDecisionID(62) }},
				{"changed-accepted-scope", func(s *factory.DeliverySubject) { s.ScopeDigest = strings.Repeat("e", 64) }},
				{"changed-ready-preparation", func(s *factory.DeliverySubject) { s.PreparationDigest = strings.Repeat("d", 64) }},
			} {
				wrong := subject
				payload := *subject.Delivery
				wrong.Delivery = &payload
				invalid.edit(wrong.Delivery)
				refused := newDispatchReport()
				DispatchTimeOnlyRoot(ctx, deps, &occupancy, 7, wrong, fx.tip, prompt, preparation, &refused)
				if len(refused.Launched) != 0 || len(refused.Errors)+len(refused.Waits) == 0 {
					t.Fatalf("%s admitted an unbound verifier: %+v", invalid.name, refused)
				}
				var recorded int
				if err = sqlDB.QueryRow(`SELECT count(*) FROM factory_assignments WHERE data->>'phase'='verify'`).Scan(&recorded); err != nil || recorded != 0 {
					t.Fatalf("%s wrote a verification packet: %d %v", invalid.name, recorded, err)
				}
			}
			if fixture.adopted && !fixture.checkFailed {
				for _, fault := range []string{"missing-manifest", "unknown-outcome", "omitted-outcome", "changed-check"} {
					badDeps := deps
					badDeps.VerificationManifest = func(ctx context.Context, a factory.Assignment, r factory.Run) ([]byte, error) {
						raw, err := c.stageDeliveryVerificationManifest(ctx, a, r)
						if err != nil {
							return nil, err
						}
						if fault == "missing-manifest" {
							return nil, nil
						}
						m, err := project.ParseVerificationManifest(raw)
						if err != nil {
							return nil, err
						}
						switch fault {
						case "unknown-outcome":
							m.Outcomes = []string{phasedDecisionID(99)}
							m.Checks[0].Outcomes = m.Outcomes
						case "omitted-outcome":
							m.Outcomes = nil
							m.Checks[0].Outcomes = nil
						case "changed-check":
							m.CheckDigest = strings.Repeat("f", 64)
						}
						return project.MarshalVerificationManifest(m)
					}
					refused := newDispatchReport()
					DispatchTimeOnlyRoot(ctx, badDeps, &occupancy, 7, subject, fx.tip, prompt, preparation, &refused)
					if len(refused.Launched) != 0 || len(refused.Errors)+len(refused.Waits) == 0 {
						t.Fatal("invalid manifest admitted before CLI", fault, refused)
					}
					var count int
					if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_assignments`).Scan(&count); err != nil || count != 0 {
						t.Fatal("manifest refusal left a dispatch packet", fault, count, err)
					}
				}
			}
			report := newDispatchReport()
			launches := 0
			var native project.FactoryState
			fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
				launches++
				for _, guidance := range []string{"factory.verification_scope", "factory.record_journey_result", "journey:<post ID>", "observations you actually made or inspected", "Do not change code"} {
					if !strings.Contains(string(in.Prompt), guidance) {
						t.Fatal("Test Pilot initial/retry lost R40 observation guidance", launches, guidance)
					}
				}
				a, err := db.AssignmentByRun(ctx, in.Run.ID)
				if err != nil || a.Role != project.RoleReviewer || a.Phase != project.ExecutionVerify || in.MCP == nil || in.Run.ExecutionPhase != project.ExecutionVerify {
					t.Fatalf("unbound Test Pilot: role=%s phase=%s mcp=%t error=%v", a.Role, a.Phase, in.MCP != nil, err)
				}
				var data []byte
				if err = sqlDB.QueryRow(`SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&data); err != nil {
					t.Fatal(err)
				}
				var allowance factory.AttemptAllowance
				if err = json.Unmarshal(data, &allowance); err != nil || allowance.Delivery == nil || allowance.Delivery.DeliveryID != subject.Delivery.DeliveryID {
					t.Fatalf("delivery unfunded: %+v %v", allowance, err)
				}
				if allowance.Limits.ActiveMinutes > factory.MaxAppVerificationRunMinutes || in.Run.Deadline.After(time.Unix(allowance.CheckpointUnix, 0).Add(factory.MaxAppVerificationRunMinutes*time.Minute)) {
					t.Fatal("Test Pilot exceeded its per-run 30-minute ceiling", allowance.Limits, in.Run.Deadline)
				}
				if fixture.spent == 3540 && launches > 1 && allowance.Limits.ActiveMinutes != 1 {
					t.Fatal("successor did not inherit the remaining stable-intent minute", allowance.Limits)
				}
				if launches == 1 {
					at := time.Unix(allowance.CheckpointUnix+120, 0)
					if remaining, err := db.RemainingAppVerificationSeconds(ctx, 7, revision.Intent, at); err != nil || remaining != 3480 {
						t.Fatal("live native interval was omitted from aggregate time", remaining, err)
					}
					var untouched []byte
					if err := sqlDB.QueryRow(`SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&untouched); err != nil || string(untouched) != string(data) {
						t.Fatal("spend-free allowance read rewrote its live root", err)
					}
				}
				native = project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}
				if fixture.adopted {
					m, err := project.ParseVerificationManifest(in.VerificationManifest)
					stored, storeErr := db.FactoryRun(ctx, in.Run.ID)
					if err != nil || m.ValidateForRun(in.Run) != nil || in.Run.VerificationManifestDigest != project.ExecutionManifestDigest(in.VerificationManifest) || storeErr != nil || string(stored.VerificationManifest) != string(in.VerificationManifest) {
						t.Fatal("launch did not retain its exact approved manifest", err, storeErr)
					}
					native.Invocation, native.LeaseID, native.Generation, native.Delivered, native.CredentialReturned, native.Retirement = strings.Repeat("b", 32), "native-manifest-lease", 1, true, true, "confirmed"
					native.Container = strings.Repeat("c", 64)
					fx.broker.get = func(kind, id string) (identity.Execution, error) {
						if id != native.ID {
							return identity.Execution{}, identity.ErrNotFound
						}
						return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionTerminal, Digest: strings.Repeat("d", 64), Binding: &identity.Binding{Kind: kind, ID: id, Generation: native.Generation, Project: native.Container, InvocationID: native.Invocation, Login: project.RoleReviewer, ChildID: preparation}}, nil
					}
					checks := &project.FactoryVerificationChecksState{Project: in.Run.Project, Run: in.Run.ID, Incarnation: native.Invocation, ManifestDigest: in.Run.VerificationManifestDigest, Phase: "passed", SourceCommit: m.SourceCommit, Preparation: m.Preparation, CheckDigest: m.CheckDigest, ExitCode: &zero, OutputDigest: factory.MCPInputDigest([]byte("per-run native manifest assertions passed")), OutputExcerpt: "full-name.expected=Hello, Ada Lovelace!\nfull-name.observed=Hello, Ada Lovelace!\n", Retirement: "confirmed"}
					checks.ApplicationBundleSHA256 = factory.MCPInputDigest([]byte("checked application bundle bytes"))
					checks.ApplicationPreviewBundleSHA256 = factory.MCPInputDigest([]byte("checked preview application bundle bytes"))
					if fixture.failed {
						// Older retained receipts carry a digest without captured text.
						checks.OutputExcerpt = ""
					} else if fixture.state == factory.ReassessmentCollecting {
						checks.OutputExcerpt = "\n\t"
					}
					if fixture.checkFailed {
						code := 1
						checks.Phase, checks.ExitCode, checks.Reason = "failed", &code, "required_check_failed"
						checks.OutputDigest = factory.MCPInputDigest([]byte("per-run assertions failed"))
						checks.ApplicationBundleSHA256 = ""
						checks.ApplicationPreviewBundleSHA256 = ""
						native.Phase, native.Delivered, native.Reason = project.FactoryFailed, false, "verification_checks_failed"
					}
					native.VerificationChecks = checks
				}
				return native, nil
			}
			DispatchTimeOnlyRoot(ctx, deps, &occupancy, 7, subject, fx.tip, prompt, preparation, &report)
			if len(report.Launched) != 1 || len(report.Errors) != 0 || len(report.Waits) != 0 {
				t.Fatalf("verification dispatch: %+v", report)
			}
			var retainedCycle string
			if err = sqlDB.QueryRow(`SELECT data::text FROM factory_reassessments WHERE cycle=$1`, cycle.CycleID).Scan(&retainedCycle); err != nil || retainedCycle != originalCycle {
				t.Fatal("verification changed the completion cycle or its judgment", err)
			}
			if pending, err := db.CompletionPending(ctx, 7); err != nil || len(pending) != 1 || pending[0] != 1 {
				t.Fatal("verification released the independent completion fence", pending, err)
			}
			seen, err := db.DeliveryAlreadyAssigned(ctx, 7, subject.Delivery.DeliveryID)
			if err != nil || !seen {
				t.Fatal("delivery replay is unbound", err)
			}
			// The normal current-format reopen must preserve the new packet and its allowance.
			if deadline, err := db.CapAttemptDeadline(ctx, report.Launched[0].AssignmentID, time.Now().Add(time.Minute).Unix()); err != nil || deadline <= time.Now().Unix() {
				t.Fatal("delivery lifetime is unavailable", err)
			}
			a, err := db.Assignment(ctx, report.Launched[0].AssignmentID)
			if err != nil {
				t.Fatal(err)
			}
			run, err := db.FactoryRun(ctx, a.Run)
			if err != nil {
				t.Fatal(err)
			}
			journeyRef, alternateJourneyRef := "", ""
			var verificationCapability factory.MCPCapability
			if fixture.adopted {
				fx.host.inspect = func(project.FactoryInspect) (project.FactoryState, error) { return native, nil }
				if err := retainRunFacts(ctx, db, &run, nativeRunObservation(t, native), nil); err != nil {
					t.Fatal("native manifest receipt observation", err)
				}
				// A stale supervisor save cannot erase the packet or a terminal
				// receipt, and a terminal receipt cannot acquire different bytes.
				stale := run
				stale.VerificationManifest, stale.VerificationChecks = nil, nil
				if err := db.SaveFactoryRun(ctx, stale); err != nil {
					t.Fatal("stale observer failed to retain native manifest receipt", err)
				}
				retained, err := db.FactoryRun(ctx, run.ID)
				if err != nil || string(retained.VerificationManifest) != string(run.VerificationManifest) || retained.VerificationChecks == nil || retained.VerificationChecks.OutputDigest != native.VerificationChecks.OutputDigest || retained.VerificationChecks.OutputExcerpt != native.VerificationChecks.OutputExcerpt {
					t.Fatal("stale observer erased immutable verification evidence", err)
				}
				if !fixture.checkFailed {
					evidence, err := c.ApplicationVerificationEvidence(ctx, a)
					want := native.VerificationChecks.OutputExcerpt
					if want == "" {
						want = "Native admitted verification manifest passed; no output captured."
					} else if strings.TrimSpace(want) == "" {
						want = "Native admitted verification manifest passed; captured output contains only whitespace (see verification_checks.output_excerpt)."
					}
					if err != nil || evidence.ExecutionReceipt == nil || evidence.ExecutionReceipt.LogExcerpt != want || evidence.ExecutionReceipt.ArtifactDigest != native.VerificationChecks.OutputDigest {
						t.Fatal("worker evidence lost actual approved check diagnostics", evidence.ExecutionReceipt, err)
					}
				}
				for _, corrupt := range []string{strings.Repeat("x", project.MaxVerificationOutputExcerpt+1), "native\x00output", string([]byte{0xff})} {
					invalid := *native.VerificationChecks
					invalid.OutputExcerpt = corrupt
					request := project.FactoryVerificationChecks{Project: invalid.Project, Run: invalid.Run, Incarnation: invalid.Incarnation, ManifestDigest: invalid.ManifestDigest}
					if invalid.ValidateFor(request) == nil {
						t.Fatal("unbounded or unsafe native check excerpt was accepted")
					}
				}
				for _, field := range []string{"digest", "excerpt", "truncation"} {
					changed := *native.VerificationChecks
					switch field {
					case "digest":
						changed.OutputDigest = strings.Repeat("f", 64)
					case "excerpt":
						changed.OutputExcerpt = "changed runtime observation"
					case "truncation":
						changed.OutputTruncated = !changed.OutputTruncated
					}
					retained.VerificationChecks = &changed
					if err := db.SaveFactoryRun(ctx, retained); err == nil {
						t.Fatal("terminal native receipt accepted changed output", field)
					}
				}
				binding := identity.Binding{Kind: identity.Factory, ID: run.ID, Project: strings.Repeat("c", 64), Login: project.RoleReviewer, Generation: 1, InvocationID: native.Invocation, ChildID: preparation}
				if err := adoptExecutionFacts(&run, nativeRunObservation(t, native), nativeCustodyObservation(t, native, identity.Execution{Kind: identity.Factory, ExecutionID: run.ID, State: identity.ExecutionTerminal, Binding: &binding})); err != nil {
					t.Fatal(err)
				}
				if fixture.checkFailed {
					run.Outcome, run.Reconciled, run.Summary = factory.Failed, true, "verification checks failed before worker startup"
					if err := db.SaveFactoryRun(ctx, run); err != nil {
						t.Fatal(err)
					}
					finished, settled := AccountSettledRun(ctx, db, run, time.Now())
					if !settled || finished.Outcome != factory.Failed || finished.Result == nil || finished.Result.Reported || finished.Result.PhaseResult != nil && finished.Result.PhaseResult.Output != nil && finished.Result.PhaseResult.Output.Verification != nil {
						t.Fatal("pre-CLI check failure invented a Test Pilot report", settled, finished.Outcome)
					}
					var posts int
					if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->'communication'->>'component_id'='F10'`).Scan(&posts); err != nil || posts != 0 {
						t.Fatal("failed manifest verified application", posts, err)
					}
					return
				}
				verificationCapability, err = db.GetMCPCapability(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.ActivateObservedMCPCapability(ctx, verificationCapability); err != nil {
					t.Fatal(err)
				}
				verificationCapability, err = db.GetMCPCapability(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, observationResult := range []string{"passed", "incomplete"} {
					o := factory.VerificationJourneyResult{DeliveryID: subject.Delivery.DeliveryID, InputDigest: verificationCapability.ScopeDigest(), SourceCommit: fx.tip, Preparation: a.Preparation, ManifestDigest: subject.Delivery.VerificationManifestDigest, OutcomeIDs: []string{idea.ID}, Name: "Retained task readback", Steps: []string{"Save a task", "Read its title"}, Expected: "The requested title persists.", Observed: "The native assertion reports a saved title.", Result: observationResult, Limitations: []string{"Outcome coverage still needs a typed final report."}}
					id := factory.NewID()
					raw, _ := json.Marshal(o)
					sub := testMCPSubmission(verificationCapability, factory.MCPToolRecordJourneyResult, id, raw)
					post, err := db.RecordWorkerPost(ctx, sub)
					if err != nil {
						t.Fatal("record verification journey", err)
					}
					if observationResult == "passed" {
						journeyRef = "journey:" + post.Record.ID
					} else {
						alternateJourneyRef = "journey:" + post.Record.ID
					}
				}
			}
			if early {
				capability, err := db.GetMCPCapability(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.ActivateObservedMCPCapability(ctx, capability); err != nil {
					t.Fatal(err)
				}
				capability, err = db.GetMCPCapability(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if fixture.failed {
					verification := factory.AppVerificationReport{DeliveryID: subject.Delivery.DeliveryID, InputDigest: strings.Repeat("9", 64), Outcome: factory.AppVerificationFailed, CheckReceipts: []factory.VerificationCheckReceipt{{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: factory.MCPInputDigest([]byte("task storage passed")), LogExcerpt: "Native assertions passed"}}, Failures: []factory.CheckFailure{{CheckRef: idea.ID, Detail: "The approved assertions omit required task behavior."}}}
					a.Stage, a.Outcome, a.Reason, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, time.Now().Unix()
					a.Result = &factory.AssignmentResult{AssignmentID: a.ID, RunID: a.Run, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application coverage failed", Reported: true, RecordedUnix: time.Now().Unix(), PhaseResult: &factory.PhaseResult{Version: 2, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application coverage failed", Output: &factory.PhaseOutput{Verification: &verification}}}
					if err := db.FinishAssignment(ctx, a); err != nil {
						t.Fatal("reported application failure", err)
					}
				} else {
					submission := testMCPSubmission(capability, factory.MCPToolFinishIncomplete, factory.NewID(), []byte(`{"outcome":"blocked","cause":"Application evidence is incomplete."}`))
					if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
						t.Fatal(err)
					}
					if settled, err := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); err != nil || !settled {
						t.Fatal("early Test Pilot terminal did not settle", settled, err)
					}
				}
				if _, err := c.RetryRun(ctx, factory.NewID(), "native:7", run.ID); err == nil {
					t.Fatal("verification Retry admitted before native retirement")
				}
				var before factory.AttemptAllowance
				var raw []byte
				if err := sqlDB.QueryRow(`SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &before) != nil || !before.Active || !before.Closed {
					t.Fatal("early Test Pilot terminal must keep its actual native clock", err)
				}
				run.Outcome, run.Reconciled = factory.Succeeded, true
				if err := db.SaveFactoryRun(ctx, run); err != nil {
					t.Fatal("retired Test Pilot native outcome failed to save", err)
				}
				var after factory.AttemptAllowance
				if err := sqlDB.QueryRow(`SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &after) != nil || after.Active || !after.Closed || after.Limits != before.Limits || after.ActiveSeconds < before.ActiveSeconds {
					t.Fatal("retired Test Pilot did not checkpoint the same closed allowance", err)
				}
				finished, err := db.Assignment(ctx, a.ID)
				wantOutcome, wantStatus := factory.NeedsHuman, "blocked"
				if fixture.failed {
					wantOutcome, wantStatus = factory.Succeeded, "completed"
				}
				if err != nil || finished.Outcome != wantOutcome || finished.Result == nil || finished.Result.Status != wantStatus {
					t.Fatal("native retirement changed the recorded verification result", err)
				}
				if err := db.SaveFactoryRun(ctx, run); err != nil {
					t.Fatal("native Test Pilot save replay failed", err)
				}
				var replayed factory.AttemptAllowance
				if err := sqlDB.QueryRow(`SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &replayed) != nil || replayed.Revision != after.Revision || replayed.ActiveSeconds != after.ActiveSeconds {
					t.Fatal("native Test Pilot replay charged or reopened its root", err)
				}
				reservation, err := db.Reservation(ctx, a.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.RunUsage(ctx, run.ID); errors.Is(err, store.ErrNotFound) && reservation.State != factory.ReservationReleased {
					if err = db.RecordRunUsage(ctx, factory.Usage{RunID: run.ID, Repository: 7, Connection: a.Connection, Minutes: 1, StartedAt: run.Started, EndedAt: run.Started.Add(time.Millisecond)}); err != nil {
						t.Fatal(err)
					}
				} else if err != nil && !errors.Is(err, store.ErrNotFound) {
					t.Fatal(err)
				}
				if reservation.State == factory.ReservationHeld {
					if err = db.ConsumeReservation(ctx, a.ID); err != nil {
						t.Fatal(err)
					}
				}
				if fixture.spent > 0 {
					// Retained roots from before the fixed cap remain readable and
					// charged. Seed their historical observed time before taking the
					// immutable snapshot; admission must never reset these rows.
					after.Limits.ActiveMinutes = factory.DefaultAppVerificationActiveMinutes
					after.ActiveSeconds = fixture.spent
					raw, _ := json.Marshal(after)
					if _, err := sqlDB.Exec(`UPDATE factory_attempt_allowances SET data=$2 WHERE root_assignment=$1`, a.ID, string(raw)); err != nil {
						t.Fatal(err)
					}
				}
				retained := func() string {
					var rows string
					if err := sqlDB.QueryRow(`SELECT jsonb_build_object(
						'assignment',(SELECT to_jsonb(x) FROM factory_assignments x WHERE id=$1),
						'run',(SELECT to_jsonb(x) FROM factory_runs x WHERE id=$2),
						'allowance',(SELECT to_jsonb(x) FROM factory_attempt_allowances x WHERE root_assignment=$1),
						'reservation',(SELECT to_jsonb(x) FROM factory_reservations x WHERE assignment=$1),
						'usage',(SELECT to_jsonb(x) FROM factory_usage x WHERE run=$2),
						'cycle',(SELECT to_jsonb(x) FROM factory_reassessments x WHERE cycle=$3))::text`, a.ID, run.ID, cycle.CycleID).Scan(&rows); err != nil {
						t.Fatal(err)
					}
					return rows
				}
				oldRows := retained()
				if fixture.spent == 3600 {
					if remaining, err := db.RemainingAppVerificationSeconds(ctx, 7, revision.Intent, time.Now()); err != nil || remaining != 0 {
						t.Fatal("historical active time did not exhaust the stable intent", remaining, err)
					}
					if available, err := c.DeliveryRetryAvailable(ctx, run.ID); err != nil || available {
						t.Fatal("exhausted verifier advertised Retry", available, err)
					}
					if _, err := c.RetryRun(ctx, factory.NewID(), "native:7", run.ID); !errors.Is(err, store.ErrExplicitRetryIneligible) {
						t.Fatal("exhausted verifier queued Retry", err)
					}
					if retained() != oldRows {
						t.Fatal("exhaustion check rewrote retained spending")
					}
					return
				}
				if available, err := db.DeliveryRetryAvailable(ctx, run.ID); err != nil || !available {
					t.Fatal("retired verifier Retry unavailable", available, err)
				}
				c.DispatchReads = &appVerificationReads{tip: strings.Repeat("e", 40)}
				if available, err := c.DeliveryRetryAvailable(ctx, run.ID); err != nil || available {
					t.Fatal("old-source verifier advertised Retry", available, err)
				}
				if _, err := c.RetryRun(ctx, factory.NewID(), "native:7", run.ID); !errors.Is(err, store.ErrExplicitRetryIneligible) {
					t.Fatal("old-source verifier Retry queued", err)
				}
				c.DispatchReads = &appVerificationReads{tip: fx.tip}
				if available, err := c.DeliveryRetryAvailable(ctx, run.ID); err != nil || !available {
					t.Fatal("current-source verifier Retry unavailable", available, err)
				}
				unowned := subject
				unownedPayload := *subject.Delivery
				unownedPayload.DeliveryID = factory.NewID()
				unowned.Delivery = &unownedPayload
				unownedOccupancy, err := snapshotOccupancy(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				unownedReport := newDispatchReport()
				DispatchTimeOnlyRoot(ctx, deps, &unownedOccupancy, 7, unowned, fx.tip, prompt, preparation, &unownedReport)
				if len(unownedReport.Launched) != 0 || len(unownedReport.Waits)+len(unownedReport.Errors) == 0 {
					t.Fatal("verification polled into an unrequested retry")
				}
				command := factory.NewID()
				decision, err := c.RetryRun(ctx, command, "native:7", run.ID)
				if err != nil || !decision.Queued {
					t.Fatal("explicit verifier Retry refused", decision, err)
				}
				admitted, err := db.FactoryCommand(ctx, command)
				if err != nil || admitted.Finished == "" || admitted.Outcome != "" {
					t.Fatalf("verifier Retry retained a response or was not admitted: %+v %v", admitted, err)
				}
				if replay, err := c.RetryRun(ctx, command, "native:7", run.ID); err != nil || replay != decision {
					t.Fatal("verifier Retry lost-reply recovery", replay, err)
				}
				if _, err := c.RetryRun(ctx, factory.NewID(), "native:7", run.ID); !errors.Is(err, store.ErrExplicitRetryIneligible) {
					t.Fatal("duplicate queued verifier Retry", err)
				}
				if retained() != oldRows {
					t.Fatal("queuing verification Retry changed old result, spending or completion decision")
				}
				if available, err := db.DeliveryRetryAvailable(ctx, run.ID); err != nil || available {
					t.Fatal("queued verifier Retry still advertised", available, err)
				}
				pending, err := db.PendingDeliveryRetry(ctx, 7, *subject.Delivery)
				if err != nil || pending != decision {
					t.Fatal("verification Retry was not selected", pending, err)
				}
				foreign := *subject.Delivery
				foreign.ScopeDigest = strings.Repeat("f", 64)
				if _, err := db.PendingDeliveryRetry(ctx, 7, foreign); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("foreign version selected verification Retry", err)
				}
				payload := *subject.Delivery
				payload.DeliveryID = factory.DeliveryRetryID(payload, command)
				subject = factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &payload}
				retryPrompt, err := c.deliveryRetryPrompt(ctx, candidates[0], subject, preparation)
				if err != nil || !strings.Contains(string(retryPrompt), payload.DeliveryID) {
					t.Fatal("verification Retry prompt is unbound", err)
				}
				occupancy, err = snapshotOccupancy(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				retryReport := newDispatchReport()
				dispatchTimeOnlyRetry(ctx, deps, &occupancy, 7, subject, fx.tip, retryPrompt, preparation, &decision, &retryReport)
				if len(retryReport.Launched) != 1 || len(retryReport.Errors) != 0 || len(retryReport.Waits) != 0 || retryReport.Launched[0].AssignmentID != command {
					t.Fatalf("explicit verifier successor: %+v", retryReport)
				}
				if retained() != oldRows {
					t.Fatal("new verifier packet rewrote the old incomplete result or budget")
				}
				if replay, err := c.RetryRun(ctx, command, "native:7", run.ID); err != nil || replay != decision {
					t.Fatal("consumed verifier Retry lost its immutable receipt", replay, err)
				}
				if _, err := db.PendingDeliveryRetry(ctx, 7, payload); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("consumed verifier Retry stayed pending", err)
				}
				repeated := newDispatchReport()
				dispatchTimeOnlyRetry(ctx, deps, &occupancy, 7, subject, fx.tip, retryPrompt, preparation, &decision, &repeated)
				if len(repeated.Launched) != 0 {
					t.Fatal("verifier Retry launched twice")
				}
				if _, err := c.RetryRun(ctx, factory.NewID(), "native:7", run.ID); !errors.Is(err, store.ErrExplicitRetryIneligible) {
					t.Fatal("old verifier remained retryable after successor", err)
				}
				successor, err := db.Assignment(ctx, command)
				if err != nil {
					t.Fatal(err)
				}
				successorRun, err := db.FactoryRun(ctx, successor.Run)
				if err != nil {
					t.Fatal(err)
				}
				capability, err = db.GetMCPCapability(ctx, successor.Run)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.ActivateObservedMCPCapability(ctx, capability); err != nil {
					t.Fatal(err)
				}
				capability, err = db.GetMCPCapability(ctx, successor.Run)
				if err != nil {
					t.Fatal(err)
				}
				submission := testMCPSubmission(capability, factory.MCPToolFinishIncomplete, factory.NewID(), []byte(`{"outcome":"blocked","cause":"Second verification remains incomplete."}`))
				if _, err := db.RecordMCPSubmission(ctx, submission); err != nil {
					t.Fatal(err)
				}
				if settled, err := SettleMCPTerminalProposal(ctx, db, successor.ID, time.Now()); err != nil || !settled {
					t.Fatal("second Test Pilot terminal did not settle", settled, err)
				}
				successorRun.Outcome, successorRun.Reconciled = factory.Succeeded, true
				if err := db.SaveFactoryRun(ctx, successorRun); err != nil {
					t.Fatal(err)
				}
				secondReservation, err := db.Reservation(ctx, successor.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.RunUsage(ctx, successor.Run); errors.Is(err, store.ErrNotFound) && secondReservation.State != factory.ReservationReleased {
					if err := db.RecordRunUsage(ctx, factory.Usage{RunID: successor.Run, Repository: 7, Connection: successor.Connection, Minutes: 1, StartedAt: successorRun.Started, EndedAt: successorRun.Started.Add(time.Millisecond)}); err != nil {
						t.Fatal(err)
					}
				} else if err != nil && !errors.Is(err, store.ErrNotFound) {
					t.Fatal(err)
				}
				if secondReservation.State == factory.ReservationHeld {
					if err := db.ConsumeReservation(ctx, successor.ID); err != nil {
						t.Fatal(err)
					}
				}
				if remaining, err := db.RemainingAppVerificationSeconds(ctx, 7, revision.Intent, time.Now()); err != nil || remaining != 0 {
					t.Fatal("two retained runs did not exhaust the stable intent", remaining, err)
				}
				if available, err := c.DeliveryRetryAvailable(ctx, successor.Run); err != nil || available {
					t.Fatal("third verification Retry advertised", available, err)
				}
				if _, err := c.RetryRun(ctx, factory.NewID(), "native:7", successor.Run); !errors.Is(err, store.ErrExplicitRetryIneligible) {
					t.Fatal("third verification Retry queued", err)
				}
				// A changed accepted version still shares the same two spent run
				// identities. Exercise planning and then the atomic packet owner
				// directly, as if a caller retained an earlier planning snapshot.
				nextRevision := revision
				nextRevision.ID, nextRevision.Predecessor = phasedDecisionID(42), revision.ID
				if err := db.SaveIntentRevision(ctx, nextRevision); err != nil {
					t.Fatal(err)
				}
				nextBinding := binding
				nextBinding.RevisionID = nextRevision.ID
				if err := db.BindIntentTaskAcceptance(ctx, nextBinding); err != nil {
					t.Fatal(err)
				}
				fresh := payload
				fresh.AcceptedRevision, fresh.ScopeDigest = nextRevision.ID, nextRevision.Digest()
				fresh.DeliveryID = factory.DeliveryID(fresh)
				subject = factory.ExecutionSubject{Kind: factory.SubjectDelivery, Delivery: &fresh}
				occupancy, err = snapshotOccupancy(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				third := newDispatchReport()
				DispatchTimeOnlyRoot(ctx, deps, &occupancy, 7, subject, fx.tip, retryPrompt, preparation, &third)
				if len(third.Launched) != 0 || len(third.Waits) != 1 || third.Waits[0].Reason != WaitAllowance {
					t.Fatal("revision change refilled verification allowance", third)
				}
				successor.ID, successor.Run = factory.NewID(), factory.NewID()
				successor.AttemptRoot, successor.RunHistory, successor.Subject = successor.ID, []string{successor.Run}, subject
				successorRun.ID, successorRun.Outcome, successorRun.Reconciled = successor.Run, "", false
				successorRun.Started = time.Now().UTC().Truncate(time.Second)
				successorRun.Deadline = successorRun.Started.Add(time.Minute)
				reservation = factory.Reservation{AssignmentID: successor.ID, Repository: 7, Connection: successor.Connection, State: factory.ReservationHeld, PlannedMinutes: 1}
				expected := factory.IssueControl{Repository: 7, Issue: 0, NativeRev: successor.NativeRev}
				err = db.RecordDispatchPacket(ctx, expected, successor, reservation, successorRun, factory.RunView{RunID: successor.Run, Repository: 7, Issue: 0, Attempt: successor.ID})
				if !errors.Is(err, factory.ErrAttemptTimeExhausted) {
					t.Fatal("atomic packet gate refilled stable-intent runs", err)
				}
				if _, err := db.Assignment(ctx, successor.ID); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("refused third packet left an assignment", err)
				}
				if retained() != oldRows {
					t.Fatal("exhaustion or revision change modified the prior charged root")
				}
				if fixture.state == factory.ReassessmentCollecting && !fixture.failed && fixture.spent == 0 {
					before, err := db.AppVerificationAllowance(ctx, revision.Intent, 7, time.Now())
					if err != nil || before.Revision != "" || before.Limits.Runs != 2 || before.Limits.ActiveMinutes != 60 || before.Consumption.Runs != 2 || before.Remaining.Runs != 0 {
						t.Fatal("default verification allowance read", before, err)
					}
					unchanged := func() string {
						t.Helper()
						var data string
						if err := sqlDB.QueryRow(`SELECT json_build_object('roots',(SELECT jsonb_agg(to_jsonb(t) ORDER BY root_assignment) FROM factory_attempt_allowances t),'usage',(SELECT jsonb_agg(to_jsonb(t) ORDER BY run) FROM factory_usage t),'reservations',(SELECT jsonb_agg(to_jsonb(t) ORDER BY assignment) FROM factory_reservations t),'policy',(SELECT jsonb_agg(to_jsonb(t) ORDER BY repository) FROM factory_policies t),'sponsor',(SELECT jsonb_agg(to_jsonb(t) ORDER BY repository,connection) FROM factory_sponsorships t),'budget',(SELECT jsonb_agg(to_jsonb(t) ORDER BY connection) FROM factory_connection_usage_budgets t))::text`).Scan(&data); err != nil {
							t.Fatal(err)
						}
						return data
					}
					originalFunding := unchanged()
					type increaseResult struct {
						view factory.AppVerificationAllowance
						err  error
					}
					results := make(chan increaseResult, 2)
					for range 2 {
						change := factory.AppVerificationAllowanceChange{CommandID: factory.NewID(), Limits: factory.AppVerificationLimits{Runs: 3, ActiveMinutes: 60}}
						go func() {
							view, err := db.IncreaseAppVerificationAllowance(ctx, revision.Intent, 7, revision.Repository, "native:7", change, time.Now())
							results <- increaseResult{view, err}
						}()
					}
					accepted, stale := 0, 0
					for range 2 {
						result := <-results
						if errors.Is(result.err, store.ErrStaleRevision) {
							stale++
							continue
						}
						if result.err != nil || result.view.Revision == "" || result.view.CommandID != result.view.Revision || result.view.Limits.Runs != 3 || result.view.Limits.ActiveMinutes != 60 || result.view.Consumption != before.Consumption || result.view.Remaining.Runs != 1 {
							t.Fatal("owner verification increase", result.view, result.err)
						}
						accepted++
					}
					if accepted != 1 || stale != 1 || unchanged() != originalFunding {
						t.Fatal("concurrent increase forked or reset existing funding", accepted, stale)
					}
					if _, err := db.AppVerificationAllowance(ctx, revision.Intent, 8, time.Now()); !errors.Is(err, factory.ErrVerificationAllowanceOwner) {
						t.Fatal("foreign owner read verification allowance", err)
					}
					occupancy, err = snapshotOccupancy(ctx, db)
					if err != nil {
						t.Fatal(err)
					}
					fundedThird := newDispatchReport()
					DispatchTimeOnlyRoot(ctx, deps, &occupancy, 7, subject, fx.tip, retryPrompt, preparation, &fundedThird)
					// The explicitly added admission launches; a missing context
					// choice serves current context rather than holding.
					if len(fundedThird.Launched) != 1 || len(fundedThird.Errors) != 0 {
						t.Fatal("explicit count increase did not launch its added admission", fundedThird)
					}
					var thirdLimit int
					var contextPending bool
					if err := sqlDB.QueryRow(`SELECT (b.data->'limits'->>'active_minutes')::int,COALESCE((r.data->>'context_pending')::boolean,false) FROM factory_assignments a JOIN factory_attempt_allowances b ON b.root_assignment=a.id JOIN factory_runs r ON r.id=a.run WHERE a.data->'subject'->'delivery'->>'accepted_revision'=$1`, nextRevision.ID).Scan(&thirdLimit, &contextPending); err != nil || thirdLimit < 1 || thirdLimit > 30 || contextPending {
						t.Fatal("third packet lacked its bounded retained root", thirdLimit, contextPending, err)
					}
					if retained() != oldRows {
						t.Fatal("new root rewrote the old charged result")
					}
					view, err := db.AppVerificationAllowance(ctx, revision.Intent, 7, time.Now())
					if err != nil || view.Consumption.Runs != 3 || view.Limits.ActiveMinutes != 60 || view.Remaining.Runs != 0 {
						t.Fatal("third admission did not retain the same total", view, err)
					}
				}
				return
			}
			run.Outcome, run.Reconciled = factory.Succeeded, true
			if err = db.SaveFactoryRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			verification := factory.AppVerificationReport{DeliveryID: subject.Delivery.DeliveryID, InputDigest: strings.Repeat("9", 64), Outcome: factory.AppVerificationVerified, CheckReceipts: []factory.VerificationCheckReceipt{{CheckID: factory.ApprovedAppCheckID, Outcome: factory.CheckReceiptPassed, ArtifactDigest: factory.MCPInputDigest([]byte("task storage passed")), LogExcerpt: "Native assertions passed"}}, Coverage: []factory.ReassessmentCoverage{{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{task.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID}}}}
			if fixture.adopted {
				verification.CheckReceipts[0].ArtifactDigest = native.VerificationChecks.OutputDigest
				verification.InputDigest = verificationCapability.ScopeDigest()
				verification.EvidenceRefs = []string{journeyRef}
				if fixture.failed {
					capability, err := db.GetMCPCapability(ctx, run.ID)
					if err != nil {
						t.Fatal(err)
					}
					verification.InputDigest, verification.Outcome, verification.Coverage = capability.ScopeDigest(), factory.AppVerificationFailed, []factory.ReassessmentCoverage{}
					verification.Failures = []factory.CheckFailure{{CheckRef: idea.ID, Detail: "Approved assertions leave a required outcome uncovered."}}
				}
			}
			a.Stage, a.Outcome, a.Reason, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, time.Now().Unix()
			a.Result = &factory.AssignmentResult{AssignmentID: a.ID, RunID: a.Run, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application assertions passed", Reported: true, RecordedUnix: time.Now().Unix(), PhaseResult: &factory.PhaseResult{Version: 2, Phase: project.ExecutionVerify, Status: "completed", Summary: "Application assertions passed", Output: &factory.PhaseOutput{Verification: &verification}}}
			if fixture.adopted {
				for _, ref := range []string{"journey:invalid", "journey:" + factory.NewID(), alternateJourneyRef} {
					bad := verification
					bad.Outcome, bad.Failures = factory.AppVerificationVerified, nil
					bad.Coverage = []factory.ReassessmentCoverage{{IntentID: revision.Intent, OutcomeID: idea.ID, TaskKeys: []string{task.Key}, DeliveredAcceptanceIDs: []string{binding.AcceptanceID}}}
					bad.EvidenceRefs = []string{ref}
					raw, _ := json.Marshal(bad)
					sub := testMCPSubmission(verificationCapability, factory.MCPToolFinishAppVerify, factory.NewID(), raw)
					if _, err := db.RecordMCPSubmission(ctx, sub); err == nil {
						t.Fatal("invalid journey evidence froze verification finish", ref)
					}
				}
				raw, _ := json.Marshal(verification)
				sub := testMCPSubmission(verificationCapability, factory.MCPToolFinishAppVerify, factory.NewID(), raw)
				if _, err := db.RecordMCPSubmission(ctx, sub); err != nil {
					t.Fatal("bound journey finish admission", err)
				}
				if settled, err := SettleMCPTerminalProposal(ctx, db, a.ID, time.Now()); err != nil || !settled {
					t.Fatal("bound journey finish settlement", settled, err)
				}
				a, err = db.Assignment(ctx, a.ID)
				if err != nil || a.Result == nil || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Verification == nil || len(a.Result.PhaseResult.Output.Verification.EvidenceRefs) != 1 || a.Result.PhaseResult.Output.Verification.EvidenceRefs[0] != journeyRef {
					t.Fatal("typed finish dropped canonical journey reference", err)
				}
			} else if err = db.FinishAssignment(ctx, a); err != nil {
				t.Fatal("delivery settlement", err)
			}
			if fixture.adopted && fixture.failed {
				cause, err := c.deliveryRepairCause(ctx, run.ID)
				if err != nil || len(cause.Report.CheckReceipts) != 1 || cause.Report.CheckReceipts[0].ArtifactDigest != native.VerificationChecks.OutputDigest {
					t.Fatal("repair cause lost exact per-run verification receipt", err)
				}
				record, admitted, err := db.RecordDeliveryRepairCause(ctx, cause, time.Now())
				if err != nil || !admitted || len(record.Cause.Report.EvidenceRefs) != 1 || record.Cause.Report.EvidenceRefs[0] != journeyRef {
					t.Fatal("canonical repair cause lost original journey evidence", err)
				}
				checks := *native.VerificationChecks
				native.VerificationChecks.OutputDigest = factory.MCPInputDigest([]byte("task storage passed"))
				if _, err := c.deliveryRepairCause(ctx, run.ID); err == nil {
					t.Fatal("repair cause substituted preparation output for run receipt")
				}
				native.VerificationChecks = &checks
				return
			}
			if available, err := db.DeliveryRetryAvailable(ctx, run.ID); err != nil || available {
				t.Fatal("verified delivery advertised Retry", available, err)
			}
			if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 16); err != nil || len(candidates) != 1 {
				t.Fatal("worker report alone suppressed an unverified scope", candidates, err)
			}
			// Pending reports still invalidate against an unrelated current
			// source change. Only the independently adopted F10 is stable.
			c.DispatchReads = &appVerificationReads{tip: strings.Repeat("e", 40)}
			if err := c.verifyApplicationReport(ctx, a); err == nil {
				t.Fatal("pending verification survived changed current source")
			}
			c.DispatchReads = &appVerificationReads{tip: fx.tip}
			if err := c.verifyApplicationReport(ctx, a); err != nil {
				t.Fatal("trusted F10 adoption", err)
			}
			var resultPost string
			if err := sqlDB.QueryRow(`SELECT id FROM factory_feed_posts WHERE data->>'event_identity'=$1 AND data->'communication'->>'component_id'='F10'`, a.Subject.Delivery.DeliveryID).Scan(&resultPost); err != nil {
				t.Fatal("verified result post missing", err)
			}
			resultSource, err := db.ResolvePostSource(ctx, a.Repository, factory.PostSourcePost, resultPost)
			if err != nil || resultSource.Product == nil || resultSource.Product.Intent != revision.Intent || resultSource.Product.AcceptedRevision != revision.ID {
				t.Fatal("verified app result cannot anchor an exact product change", resultSource, err)
			}
			appID := ""
			if fixture.adopted {
				appID = checkFactoryApplicationReadback(t, ctx, db, a, native.VerificationChecks.ApplicationBundleSHA256)
			}
			c.DispatchReads = &appVerificationReads{tip: strings.Repeat("e", 40)}
			fx.host.contextRead = func(project.FactoryPreparationContextRequest, string) (project.FactoryPreparationContext, error) {
				t.Fatal("verified current accepted revision requested another native preparation")
				return project.FactoryPreparationContext{}, nil
			}
			if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 1); err != nil || len(candidates) != 0 {
				t.Fatal("authoritative verified current revision was rediscovered", candidates, err)
			}
			var waitsBefore, waitsAfter int
			if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->'communication'->>'component_id'='F09'`).Scan(&waitsBefore); err != nil {
				t.Fatal(err)
			}
			c.driveDeliveryVerifications(ctx)
			if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_feed_posts WHERE data->'communication'->>'component_id'='F09'`).Scan(&waitsAfter); err != nil || waitsAfter != waitsBefore {
				t.Fatal("verified revision emitted another preparation wait", waitsBefore, waitsAfter, err)
			}
			other := revision
			other.ID, other.Intent = phasedDecisionID(43), phasedDecisionID(60)
			if err := db.SaveIntentRevision(ctx, other); err != nil {
				t.Fatal(err)
			}
			otherBinding := binding
			otherBinding.RevisionID = other.ID
			if err := db.BindIntentTaskAcceptance(ctx, otherBinding); err != nil {
				t.Fatal(err)
			}
			candidates, next, err := db.DeliveredIntentCandidates(ctx, "", 1)
			if err != nil || len(candidates) != 0 || next != revision.Intent {
				t.Fatal("verified page did not advance its bounded cursor", candidates, next, err)
			}
			if candidates, _, err := db.DeliveredIntentCandidates(ctx, next, 1); err != nil || len(candidates) != 1 || candidates[0].Revision.Intent != other.Intent {
				t.Fatal("verified page prefix starved a different delivered intent", candidates, err)
			}
			nextRevision := revision
			nextRevision.ID, nextRevision.Predecessor = phasedDecisionID(42), revision.ID
			if err := db.SaveIntentRevision(ctx, nextRevision); err != nil {
				t.Fatal(err)
			}
			if appID != "" {
				if proof, err := db.VerifiedApplicationDelivery(ctx, appID); err != nil || proof == nil || proof.Revision != revision.ID || proof.Revision == nextRevision.ID || proof.Run != a.Run || proof.SourceCommit != a.SourceCommit || proof.Environment != "company" {
					t.Fatal("running old bundle lost its exact historical verification", proof, err)
				}
				builds, err := db.ApplicationBuilds(ctx, a.Repository, a.Subject.Delivery.DeliveryID)
				if err != nil || len(builds) != 2 {
					t.Fatal("historical build inventory disappeared", builds, err)
				}
				for _, build := range builds {
					if _, _, err := db.VerifiedFactoryBuildInput(ctx, a.Repository, build.Delivery.DeliveryID, build.Artifact.Environment); err == nil {
						t.Fatal("changed accepted head admitted stale packaging")
					}
					before, err := db.ApplicationSetup(ctx, build.SetupID)
					if err != nil {
						t.Fatal(err)
					}
					recovered, err := db.CreateApplicationBuildSetup(ctx, a.Repository, build.ID, 7)
					if err != nil || recovered.ID != before.ID || recovered.Application != before.Application || recovered.State != "complete" {
						t.Fatal("changed head lost existing setup recovery", recovered, err)
					}
				}
			}
			binding.RevisionID = nextRevision.ID
			if err := db.BindIntentTaskAcceptance(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if candidates, _, err := db.DeliveredIntentCandidates(ctx, "", 1); err != nil || len(candidates) != 1 || candidates[0].Revision.ID != nextRevision.ID {
				t.Fatal("historical F10 suppressed a changed accepted revision", candidates, err)
			}
			var closed bool
			if err = sqlDB.QueryRow(`SELECT (data->>'closed')::boolean FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&closed); err != nil || !closed {
				t.Fatal("delivery allowance remained open", err)
			}
			reopened, err := store.Open(dsn)
			if err != nil {
				t.Fatal("current-format reopen", err)
			}
			defer reopened.Close()
			persisted, err := reopened.Assignment(ctx, a.ID)
			if err != nil || persisted.Stage != factory.AssignmentFinished {
				t.Fatal("startup extension lost retained delivery", err)
			}
		})
	}
	t.Run("waiting-prefix-does-not-starve-seventeenth-delivery", func(t *testing.T) {
		ctx := context.Background()
		db, dsn := dispatchTestDB(t)
		fx := dispatchSeed(t, db)
		sponsor, err := db.Sponsorship(ctx, fx.repo, "conn")
		if err != nil {
			t.Fatal(err)
		}
		sponsor.Roles = []string{project.RoleCoder, project.RoleReviewer}
		if err := db.SaveSponsorship(ctx, sponsor); err != nil {
			t.Fatal(err)
		}
		sqlDB, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer sqlDB.Close()
		idea := factory.ProductIdea{ID: phasedDecisionID(51), Description: "Save tasks", Consequence: "Tasks persist", Decision: "accept", Source: "requested", Provenance: "human", Actor: 7}
		task := factory.IntentTask{Key: phasedDecisionID(52), Objective: "Save tasks", Outcomes: []string{"Tasks persist"}, IdeaIDs: []string{idea.ID}}
		var ready factory.IntentRevision
		for i := 0; i < 17; i++ {
			revision := factory.IntentRevision{ID: phasedDecisionID(200 + i), Intent: phasedDecisionID(100 + i), Owner: 7, Repository: 7, Objective: "Task app", State: "accepted", MaxIssues: 10, CreatedUnix: int64(i + 1), Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{task}, EffectiveOutcomes: []string{idea.ID}}
			if err := db.SaveIntentRevision(ctx, revision); err != nil {
				t.Fatal(err)
			}
			binding := factory.IntentTaskBinding{RevisionID: revision.ID, TaskKey: task.Key, IssueID: 3, IssueIndex: 3, AcceptanceID: phasedDecisionID(53), CreatedUnix: 1}
			if err := db.BindIntentTaskAcceptance(ctx, binding); err != nil {
				t.Fatal(err)
			}
			ready = revision
		}
		event := factory.CompletionEvent{ID: factory.NewID(), Repository: 7, Issue: 3, Generation: 1, PublicationID: factory.NewID(), AcceptanceID: phasedDecisionID(53), MergedCommit: fx.tip, ClosingOccurrence: "1"}
		raw, _ := json.Marshal(event)
		if _, err := sqlDB.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES(7,1,$1,$2,'1',$3)`, event.ID, event.PublicationID, string(raw)); err != nil {
			t.Fatal(err)
		}
		prep, err := db.Preparation(ctx, "f222222222222222222222222")
		if err != nil {
			t.Fatal(err)
		}
		manifest := factory.ApprovedAppCheck{IntentID: ready.Intent, AcceptedRevision: ready.ID, Outcomes: ready.EffectiveOutcomes}
		encoded, _ := json.Marshal(manifest)
		fx.host.contextRead = func(request project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
			material := fixturePreparationContext(request, role)
			material.Check = append([]byte("#!/bin/sh\n"+factory.ApprovedAppCheckPrefix), append(encoded, []byte("\nexit 0\n")...)...)
			return material, nil
		}
		zero := 0
		fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) {
			return execution.PreparationState{Container: strings.Repeat("c", 64), ID: prep.Preparation.ID, Project: fx.proj, Role: project.RoleReviewer, SourceCommit: fx.tip, SetupDigest: prep.Preparation.SetupDigest, Phase: project.PrepareReady, Ready: true, CheckExit: &zero, Output: "task storage passed"}, nil
		}
		c := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: &appVerificationReads{tip: fx.tip}, MCPKey: []byte("test-delivery-key-32-bytes-long!!"), Priority: &PriorityJudgments{Journal: db, Chooser: &bundleGapChooser{pick: factory.ContextBundleCurrent}}}
		c.driveDeliveryVerifications(ctx)
		if len(fx.host.launches) != 0 {
			t.Fatal("delivery drive exceeded its bounded waiting page")
		}
		c.driveDeliveryVerifications(ctx)
		var attempts int
		if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_assignments WHERE data->'subject'->'delivery'->>'intent_id'=$1`, ready.Intent).Scan(&attempts); err != nil || attempts != 1 || len(fx.host.launches) != 1 {
			t.Fatal("waiting intents starved ready seventeenth delivery", attempts, len(fx.host.launches), err)
		}
		c.driveDeliveryVerifications(ctx) // wrap without admitting another root.
		var total int
		if err := sqlDB.QueryRow(`SELECT count(*) FROM factory_assignments`).Scan(&total); err != nil || total != 1 {
			t.Fatal("waiting prefix spent or queue wrap duplicated delivery", total, err)
		}
	})
}
