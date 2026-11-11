package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func publicationStoreFixture(t *testing.T) *Store {
	t.Helper()
	db := grantStoreFixture(t)
	ctx := context.Background()
	projectID := "p" + strings.Repeat("d", 24)
	requirementID, approvalID := "d"+strings.Repeat("c", 24), "d"+strings.Repeat("e", 24)
	policy := grantTestPolicy()
	policy.Repository = 7
	for _, err := range []error{
		db.UpsertUser(ctx, User{ID: 7, Login: "soda-tester"}),
		db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: 7, MaxConcurrentRuns: 2}),
		db.SaveRepositoryPolicy(ctx, policy),
		db.SaveOperatorGrant(ctx, factory.OperatorGrant{Repository: 7, GrantedBy: 7, Active: true}),
		db.SaveSponsorship(ctx, factory.Sponsorship{Repository: 7, GrantedBy: 7, ActorID: 7, ProjectID: projectID, Connection: "conn", GrantID: "grant", Generation: 1, Roles: []string{project.RoleCoder}, AllowanceMinutes: 60, Active: true}),
		db.SaveEnvironmentGrant(ctx, project.EnvironmentGrant{Repository: 7, Owner: 7, Active: true, Profile: &project.Profile{ID: project.RockyHeadless, Distribution: "rocky", Version: "9.6", Interface: "headless", Architecture: "amd64", Image: "sha256:" + strings.Repeat("b", 64), Revision: strings.Repeat("c", 40)}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateProject(ctx, Project{ID: projectID, Name: "factory", RepositoryID: 7, OwnerID: 7, Repository: "soda-tester/factory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{ID: requirementID, Project: projectID, Approver: 7, SourceCommit: strings.Repeat("1", 40), SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{ID: approvalID, Project: projectID, Requirement: requirementID, Approver: 7, EffectsDigest: strings.Repeat("a", 64), ReadinessDigest: strings.Repeat("b", 64), Verified: true}); err != nil {
		t.Fatal(err)
	}
	seedReadyPacketPreparation(t, db, factory.Assignment{Preparation: "f" + strings.Repeat("b", 24), ProjectID: projectID, Role: project.RoleCoder, SourceCommit: strings.Repeat("1", 40), Authority: dispatchTestAuthority()})
	if err := db.AdmitAcceptanceDecision(ctx, factory.Acceptance{ID: "d" + strings.Repeat("a", 24), Repository: 7, IssueIndex: "3", Approver: 7, NativeRev: 9, TitleDigest: strings.Repeat("a", 64), TitleOccurrence: "original", ContentDigest: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	return db
}

func publicationTestRecord() factory.Publication {
	return factory.Publication{
		Authority:    dispatchTestAuthority(),
		Publish:      factory.PublicationOperation{Kind: factory.OpRefPublish},
		PRCreate:     factory.PublicationOperation{Kind: factory.OpPRCreate},
		ID:           factory.NewID(),
		AssignmentID: factory.NewID(),
		ProjectID:    "p" + strings.Repeat("d", 24),
		Role:         project.RoleCoder,
		Acceptance:   "d" + strings.Repeat("a", 24),
		Preparation:  "f" + strings.Repeat("b", 24),
		Run:          factory.NewID(),
		Candidate:    strings.Repeat("2", 40),
		BaseSHA:      strings.Repeat("1", 40),
		TargetBranch: "refs/heads/main",
		Stage:        factory.PublicationOpen,
		Repository:   7,
		Issue:        3,
		CreatedUnix:  1100,
	}
}

func TestRecordPublicationRoundTrip(t *testing.T) {
	t.Run("fresh retained publication preserves uncertain original and paid work", func(t *testing.T) {
		// An ordinary publication has a different ID from its execution owner.
		// Review recovery must preserve that original identity and both receipts.
		{
			ctx, db := context.Background(), dispatchStoreFixture(t)
			sponsor, err := db.Sponsorship(ctx, 7, "conn")
			if err != nil {
				t.Fatal(err)
			}
			sponsor.Roles = append(sponsor.Roles, project.RoleReviewer)
			if err = db.SaveSponsorship(ctx, sponsor); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			origin := recordFinishedAssignment(t, db, now, 3, true, "completed")
			p := publicationTestRecord()
			p.Authority, p.AssignmentID, p.Run, p.ProjectID = origin.Authority, origin.ID, origin.Run, origin.ProjectID
			p.BaseSHA, p.Candidate, p.Preparation = origin.SourceCommit, origin.Result.Candidate, origin.Preparation
			if err = db.RecordPublication(ctx, p); err != nil {
				t.Fatal(err)
			}
			request := factory.ExternalPullAdoption{Automatic: true, Policy: 1, Repository: 7, Issue: 3, Acceptance: p.Acceptance, PRNumber: 999, PRID: 9999, Connection: "conn", Limits: factory.DefaultAttemptLimits(),
				Target: factory.PublicationPullTarget{AuthorID: 42, IssueID: 9999, HeadRepository: 7, HeadRef: "refs/heads/human", BaseRef: p.TargetBranch, HeadOID: p.Candidate, BaseOID: p.BaseSHA}}
			if err = db.ExternalPullOriginAvailable(ctx, request); err != nil {
				t.Fatal("human PR sharing the Factory account was excluded", err)
			}
			unknownID := factory.NewID()
			if _, err = db.db.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,7,99,'','fenced',0,'{"pr_create":{"attempts":1,"effect":"unknown"}}')`, unknownID); err != nil {
				t.Fatal(err)
			}
			if err = db.ExternalPullOriginAvailable(ctx, request); !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("unknown Factory PR creation was not held", err)
			}
			if _, err = db.db.ExecContext(ctx, `UPDATE factory_publications SET data=jsonb_build_object('external',jsonb_build_object('target',jsonb_build_object('head_oid',$2::text)),'pr_number',998) WHERE assignment=$1`, unknownID, request.Target.HeadOID); err != nil {
				t.Fatal(err)
			}
			if err = db.ExternalPullOriginAvailable(ctx, request); !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("the same external head was admitted twice", err)
			}
			// Corrections change the publication candidate while its origin stays
			// immutable. Fencing cannot turn an already handled head into new work.
			if _, err = db.db.ExecContext(ctx, `UPDATE factory_publications SET data=jsonb_build_object('external',jsonb_build_object('target',jsonb_build_object('head_oid',$2::text)),'candidate',$3::text,'pr_number',999) WHERE assignment=$1`, unknownID, strings.Repeat("a", 40), request.Target.HeadOID); err != nil {
				t.Fatal(err)
			}
			if err = db.ExternalPullOriginAvailable(ctx, request); !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("fenced corrected candidate was adopted again", err)
			}
			if _, err = db.db.ExecContext(ctx, `UPDATE factory_publications SET data=jsonb_set(jsonb_set(data,'{candidate}',to_jsonb($2::text)),'{corrections}',jsonb_build_array(jsonb_build_object('effect','committed','work',jsonb_build_object('candidate',$3::text)))) WHERE assignment=$1`, unknownID, strings.Repeat("f", 40), request.Target.HeadOID); err != nil {
				t.Fatal(err)
			}
			if err = db.ExternalPullOriginAvailable(ctx, request); !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("prior committed correction head was adopted again", err)
			}
			if _, err = db.db.ExecContext(ctx, `DELETE FROM factory_publications WHERE assignment=$1`, unknownID); err != nil {
				t.Fatal(err)
			}
			p.Comparison, p.NativeRev, p.ObservedUnix = p.BaseSHA, 9, now.Unix()
			p.Publish = publishedStoreOperation(p, factory.OpRefPublish, 1, "ordinary-branch-receipt")
			p.PRCreate = publishedStoreOperation(p, factory.OpPRCreate, 1, "ordinary-pr-receipt")
			p.PRNumber, p.PRID = 19, 190
			p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 19, 190, p.Issue
			p.PRCreate.HeadRef, p.PRCreate.BaseRef = factory.PublishBranchName(origin.ID), p.TargetBranch
			p.PRCreate.HeadOID, p.PRCreate.BaseOID = p.Candidate, p.BaseSHA
			p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, now.Unix()
			p.Revision++
			if err = p.Validate(); err != nil {
				t.Fatal("ordinary published fixture", err)
			}
			if err = db.UpdatePublication(ctx, p); err != nil {
				t.Fatal(err)
			}
			request.PRNumber = p.PRNumber
			if err = db.ExternalPullOriginAvailable(ctx, request); !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("causal Factory publication reentered external intake", err)
			}
			canonical, err := db.Assignment(ctx, origin.ID)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := db.RepositoryPolicy(ctx, p.Repository)
			if err != nil {
				t.Fatal(err)
			}
			policy.IntentMinutes++
			if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			policy, err = db.RepositoryPolicy(ctx, p.Repository)
			if err != nil {
				t.Fatal(err)
			}
			scopeTx, err := db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			scope, captured, _, err := retainedFundingScopeTx(ctx, scopeTx, p.ID)
			_ = scopeTx.Rollback()
			if err != nil || !reflect.DeepEqual(captured, canonical) || captured.Authority.Policy == policy.Revision || scope.Authority.Policy != policy.Revision {
				t.Fatal("current funding policy rewrote original assignment authority", err)
			}
			var raw []byte
			before, err := db.AttemptAllowance(ctx, 7, 3)
			if err != nil {
				t.Fatal(err)
			}
			offer, err := db.RetainedCandidateFundingOffer(ctx, p.ID)
			if err != nil || offer.PreviousRoot != origin.ID || offer.CarryUnused || offer.Policy != policy.Revision {
				t.Fatal("ordinary candidate funding offer", err)
			}
			changed := offer
			changed.PreviousRoot = p.ID
			if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", changed, now); err == nil {
				t.Fatal("wrong previous owner funded")
			}
			command := factory.NewID()
			if _, err = db.FundRetainedCandidate(ctx, command, "owner", offer, now); err != nil {
				t.Fatal("ordinary review continuation", err)
			}
			adopted, err := db.PublicationByID(ctx, command)
			if err != nil || !reflect.DeepEqual(adopted.Publish, p.Publish) || !reflect.DeepEqual(adopted.PRCreate, p.PRCreate) || adopted.Run != p.Run {
				t.Fatal("ordinary publication receipts changed", err)
			}
			owner, err := db.Assignment(ctx, command)
			if err != nil || owner.Run != "" || owner.RetainedPublication.AdoptedFrom != p.ID || owner.RetainedPublication.NotAfter != 0 {
				t.Fatal("ordinary continuation obtained execution/send authority", err)
			}
			tx, err := db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _, grants, err := retainedFundingScopeTx(ctx, tx, command)
			if err == nil {
				owner.Connection = origin.Connection
				err = checkRetainedContinuationTx(ctx, tx, owner, grants)
			}
			_ = tx.Rollback()
			if err != nil {
				t.Fatal("ordinary continuation source admission", err)
			}
			var after factory.AttemptAllowance
			if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, origin.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &after) != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("ordinary original allowance changed", err)
			}
			original, err := db.PublicationByID(ctx, p.ID)
			if err != nil || !reflect.DeepEqual(original, p) {
				t.Fatal("ordinary original publication changed", err)
			}
			// Controlled typed findings and a cancelled final reviewer exercise
			// retained history without executing a model or native provider.
			inputOwner := owner
			inputOwner.Connection = origin.Connection
			reviewer, reviewReservation, reviewRun, reviewView := reviewCorrectChild(t, ctx, db, now, inputOwner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("d", 24))
			reviewReservation.PlannedMinutes, reviewRun.Deadline = 5, now.Add(5*time.Minute)
			cycle := reviewCorrectCycle(t, adopted, inputOwner, 1, reviewer.ID, "")
			adopted.ReviewCycles = append(adopted.ReviewCycles, cycle)
			adopted.Revision++
			if err = db.UpdatePublication(ctx, adopted); err != nil {
				t.Fatal("retained history cycle", err)
			}
			if _, exhausted, err := db.ChargeReviewDispatch(ctx, owner.Repository, owner.Issue, owner.ID, reviewer.ID, adopted.Candidate, false); err != nil || exhausted {
				t.Fatal("first retained review charge", err)
			}
			if err = recordDispatchTestPacket(t, ctx, db, reviewer, reviewReservation, reviewRun, reviewView); err != nil {
				t.Fatal("retained history reviewer", err)
			}
			reviewer = finishReviewCorrectChild(t, ctx, db, reviewer, reviewRun, now, cycle.ID, false)
			target := adopted.PullTarget()
			reviewID := "review-" + reviewer.Run
			adopted.ReviewOperations = append(adopted.ReviewOperations, factory.ReviewOperation{RunID: reviewer.Run,
				Work: factory.ReviewWork{OperationID: reviewID, AuthRevision: factory.ReviewAuthRevision(adopted.AssignmentID, reviewer.Run), Repository: adopted.Repository, ActorID: 14,
					PRNumber: adopted.PRNumber, PRID: adopted.PRID, IssueID: target.IssueID, PRAuthorID: target.AuthorID, HeadRef: target.HeadRef, BaseRef: target.BaseRef,
					HeadOID: adopted.Candidate, BaseOID: target.BaseOID, NativeRev: 9, NotAfter: now.Add(5 * time.Minute).Unix(), Event: "REQUEST_CHANGES", Body: "controlled retained finding"},
				Outcome: factory.OperationOutcome{OperationID: reviewID, InstallationID: "controlled-retained-review", Kind: factory.OpReviewSubmit, ActorID: 14, RepositoryID: adopted.Repository,
					Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, Receipt: []byte(`{"review_id":31}`)},
			})
			adopted.Revision++
			if err = db.UpdatePublication(ctx, adopted); err != nil {
				t.Fatal("retained controlled review receipt", err)
			}
			last, lastReservation, lastRun, lastView := reviewCorrectChild(t, ctx, db, now, inputOwner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("e", 24))
			lastReservation.PlannedMinutes, lastRun.Deadline = 5, now.Add(5*time.Minute)
			lastCycle := reviewCorrectCycle(t, adopted, inputOwner, 2, last.ID, "")
			adopted.ReviewCycles = append(adopted.ReviewCycles, lastCycle)
			adopted.Revision++
			if err = db.UpdatePublication(ctx, adopted); err != nil {
				t.Fatal("retained final review cycle", err)
			}
			if _, exhausted, err := db.ChargeReviewDispatch(ctx, owner.Repository, owner.Issue, owner.ID, last.ID, adopted.Candidate, false); err != nil || exhausted {
				t.Fatal("final retained review charge", err)
			}
			if err = recordDispatchTestPacket(t, ctx, db, last, lastReservation, lastRun, lastView); err != nil {
				t.Fatal("retained final reviewer", err)
			}
			lastRun.Outcome, lastRun.Reconciled, lastRun.Summary = factory.Cancelled, true, "cancelled before report"
			if err = db.SaveFactoryRun(ctx, lastRun); err != nil {
				t.Fatal(err)
			}
			if err = db.ConsumeReservation(ctx, last.ID); err != nil {
				t.Fatal(err)
			}
			last.Stage, last.Outcome, last.Reason, last.FinishedUnix = factory.AssignmentFinished, factory.Cancelled, factory.AssignReasonCancelled, now.Unix()
			last.Result = &factory.AssignmentResult{AssignmentID: last.ID, RunID: last.Run, Status: "blocked", Summary: "cancelled before report", RecordedUnix: now.Unix(), Findings: []string{}}
			if err = db.FinishAssignment(ctx, last); err != nil {
				t.Fatal(err)
			}
			adopted.Stage, adopted.Outcome, adopted.Reason = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced
			adopted.Revision++
			if err = db.UpdatePublication(ctx, adopted); err != nil {
				t.Fatal(err)
			}
			if _, err = db.CloseAttempt(ctx, owner.ID); err != nil {
				t.Fatal(err)
			}
			spent, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
			if err != nil || !spent.Closed || len(spent.Reviews) != 2 || len(spent.Corrections) != 0 {
				t.Fatal("retained history consumption", err)
			}
			freshOffer, err := db.RetainedCandidateFundingOffer(ctx, adopted.ID)
			if err != nil || freshOffer.CarryUnused || freshOffer.PreviousRoot != owner.ID || freshOffer.Limits.ReviewRounds != 2 || freshOffer.Limits.CorrectionCycles != 1 || freshOffer.Limits.ActiveMinutes > 15 {
				t.Fatal("finite fresh history offer", err)
			}
			pending := adopted
			pending.ReviewOperations = append([]factory.ReviewOperation(nil), adopted.ReviewOperations...)
			pending.ReviewOperations[0].Outcome.NotObserved = true
			tx, err = db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = admitFreshRetainedReviewTx(ctx, tx, pending, owner)
			_ = tx.Rollback()
			if !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("unknown review send admitted fresh review", err)
			}
			operator, err := db.OperatorGrant(ctx, owner.Repository)
			if err != nil {
				t.Fatal(err)
			}
			operator.Active = false
			if err = db.SaveOperatorGrant(ctx, operator); err != nil {
				t.Fatal(err)
			}
			if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", freshOffer, now); !errors.Is(err, ErrAdmissionChanged) {
				t.Fatal("revoked current authority funded history", err)
			}
			operator, err = db.OperatorGrant(ctx, owner.Repository)
			if err != nil {
				t.Fatal(err)
			}
			operator.Active = true
			if err = db.SaveOperatorGrant(ctx, operator); err != nil {
				t.Fatal(err)
			}
			stale := freshOffer
			stale.Policy++
			if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", stale, now); !errors.Is(err, ErrRetainedPublicationIneligible) {
				t.Fatal("stale history offer admitted", err)
			}
			var oldRuns, oldReservations int
			if err = db.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_runs),(SELECT count(*) FROM factory_reservations)`).Scan(&oldRuns, &oldReservations); err != nil {
				t.Fatal(err)
			}
			oldAssignments := make(map[string]string)
			for _, id := range []string{origin.ID, owner.ID, reviewer.ID, last.ID} {
				var stored string
				if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_assignments WHERE id=$1`, id).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				oldAssignments[id] = stored
			}
			freshCommand := factory.NewID()
			receipt, err := db.FundRetainedCandidate(ctx, freshCommand, "owner", freshOffer, now)
			if err != nil {
				t.Fatal("fresh funding with retained typed history", err)
			}
			if replay, err := db.FundRetainedCandidate(ctx, freshCommand, "owner", freshOffer, now.Add(time.Hour)); err != nil || replay != receipt {
				t.Fatal("history funding replay changed", err)
			}
			freshPublication, err := db.PublicationByID(ctx, freshCommand)
			if err != nil || freshPublication.Run != adopted.Run || len(freshPublication.ReviewCycles)+len(freshPublication.CorrectionJournals) != 0 || !reflect.DeepEqual(freshPublication.Publish, adopted.Publish) || !reflect.DeepEqual(freshPublication.PRCreate, adopted.PRCreate) {
				t.Fatal("fresh history owner replayed execution/publication", err)
			}
			freshOwner, err := db.Assignment(ctx, freshCommand)
			if err != nil || freshOwner.Run != "" || freshOwner.Model != "" || freshOwner.NativeTool != nil || freshOwner.RetainedPublication == nil || freshOwner.RetainedPublication.NotAfter != 0 {
				t.Fatal("fresh history owner acquired execution/send authority", err)
			}
			var runs, reservations int
			if err = db.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_runs),(SELECT count(*) FROM factory_reservations)`).Scan(&runs, &reservations); err != nil || runs != oldRuns || reservations != oldReservations {
				t.Fatal("history funding replayed a model reservation/run", err)
			}
			history, err := db.PublicationReviewHistory(ctx, freshPublication)
			if err != nil || len(history) != 3 || !reflect.DeepEqual(history[0], p) || !reflect.DeepEqual(history[1], adopted) || !reflect.DeepEqual(history[2], freshPublication) {
				t.Fatal("canonical finding history lost", err)
			}
			freshAllowance, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
			if err != nil || freshAllowance.RootAssignment != freshCommand || freshAllowance.Closed || len(freshAllowance.Reviews)+len(freshAllowance.Corrections) != 0 || freshAllowance.Limits != freshOffer.Limits {
				t.Fatal("fresh history budget was inherited or unbounded", err)
			}
			for id, old := range oldAssignments {
				var stored string
				if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_assignments WHERE id=$1`, id).Scan(&stored); err != nil || stored != old {
					t.Fatal("history funding changed old assignment", err)
				}
			}
			if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, owner.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &after) != nil || !reflect.DeepEqual(spent, after) {
				t.Fatal("history funding renewed old consumption", err)
			}
			// A reserved reviewer identity without an admitted child or candidate
			// preparation must not strand this old setup's open funding forever.
			reserved := factory.NewID()
			freshPublication.ReviewCycles = append(freshPublication.ReviewCycles, reviewCorrectCycle(t, freshPublication, freshOwner, 1, reserved, ""))
			freshPublication.Revision++
			if err = db.UpdatePublication(ctx, freshPublication); err != nil {
				t.Fatal(err)
			}
			if _, exhausted, err := db.ChargeReviewDispatch(ctx, freshOwner.Repository, freshOwner.Issue, freshOwner.ID, reserved, freshPublication.Candidate, false); err != nil || exhausted {
				t.Fatal("unlaunched retained review charge", err)
			}
			unlaunched, err := db.AttemptAllowance(ctx, freshOwner.Repository, freshOwner.Issue)
			if err != nil || unlaunched.Closed || !unlaunched.Active || len(unlaunched.Reviews) != 1 {
				t.Fatal("unlaunched retained funding", err)
			}
			if _, err = db.RetainedCandidateFundingOffer(ctx, freshPublication.ID); !errors.Is(err, ErrRetainedPublicationIneligible) {
				t.Fatal("same setup replenished active funding", err)
			}
			sameSetup := freshOffer
			sameSetup.Publication, sameSetup.PreviousRoot = freshPublication.ID, freshOwner.ID
			if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", sameSetup, time.Now()); !errors.Is(err, ErrRetainedPublicationIneligible) {
				t.Fatal("same setup duplicate funding admitted", err)
			}
			requirement, err := db.RequirementDecision(ctx, freshOwner.Authority.RequirementsID)
			if err != nil {
				t.Fatal(err)
			}
			requirement.Predecessor, requirement.ID = requirement.ID, "d"+strings.Repeat("4", 24)
			if err = db.AdmitRequirementDecision(ctx, requirement); err != nil {
				t.Fatal(err)
			}
			approval, err := db.ApprovalDecision(ctx, freshOwner.Authority.ApprovalID)
			if err != nil {
				t.Fatal(err)
			}
			approval.Predecessor, approval.ID, approval.Requirement = approval.ID, "d"+strings.Repeat("5", 24), requirement.ID
			if err = db.AdmitApprovalDecision(ctx, approval); err != nil {
				t.Fatal(err)
			}
			replacement, err := db.RetainedCandidateFundingOffer(ctx, freshPublication.ID)
			if err != nil || replacement.CarryUnused || replacement.PreviousRoot != freshOwner.ID || replacement.Requirements != requirement.ID || replacement.Approval != approval.ID {
				t.Fatal("changed setup unlaunched funding offer", err)
			}
			var offerAllowance factory.AttemptAllowance
			if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, freshOwner.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &offerAllowance) != nil || !reflect.DeepEqual(unlaunched, offerAllowance) {
				t.Fatal("funding offer mutated the old grant", err)
			}
			guardScope := freshPublication
			guardScope.Authority.RequirementsID, guardScope.Authority.ApprovalID = requirement.ID, approval.ID
			// These controlled rows exist only in rolled-back transactions. They
			// prove that any admitted child or uncertain preparation blocks this
			// recovery; they do not claim native execution or preparation ran.
			child := reviewer
			child.ID, child.Run = factory.NewID(), factory.NewID()
			child.AttemptRoot, child.PublicationAssignment, child.ParentAssignment = freshOwner.ID, freshOwner.ID, freshOwner.ID
			child.RunHistory, child.Stage, child.Result = []string{child.Run}, factory.AssignmentAssigned, nil
			child.Outcome, child.Reason, child.FinishedUnix = "", "", 0
			if err = child.Validate(); err != nil {
				t.Fatal(err)
			}
			childRaw, _ := json.Marshal(child)
			for _, blocked := range []string{"child", "preparation"} {
				guardTx, err := db.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if blocked == "child" {
					_, err = guardTx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, child.ID, child.Repository, child.Issue, child.Run, child.Stage, child.Revision, string(childRaw))
				} else {
					_, err = guardTx.ExecContext(ctx, `INSERT INTO project_preparations(id,project_id,role,revision,requirements,approval,data)
					 SELECT $1,project_id,role,revision,requirements,approval,data||jsonb_build_object('factory_admission',jsonb_build_object('attempt_root',$2::text)) FROM project_preparations WHERE id=$3`, "f"+strings.Repeat("9", 24), freshOwner.ID, freshOwner.Preparation)
				}
				if err == nil {
					err = admitFreshRetainedReviewTx(ctx, guardTx, guardScope, freshOwner)
				}
				_ = guardTx.Rollback()
				if !errors.Is(err, ErrAttemptRunsPending) {
					t.Fatal("changed setup bypassed retained effect guard", blocked, err)
				}
			}
			recoveryCommand := factory.NewID()
			if _, err = db.FundRetainedCandidate(ctx, recoveryCommand, "owner", replacement, time.Now()); err != nil {
				t.Fatal("changed setup unlaunched funding recovery", err)
			}
			var closedUnlaunched factory.AttemptAllowance
			if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, freshOwner.ID).Scan(&raw); err != nil || json.Unmarshal(raw, &closedUnlaunched) != nil || !closedUnlaunched.Closed || closedUnlaunched.Active || !reflect.DeepEqual(unlaunched.Reviews, closedUnlaunched.Reviews) || !reflect.DeepEqual(unlaunched.ReviewedHeads, closedUnlaunched.ReviewedHeads) || unlaunched.Limits != closedUnlaunched.Limits {
				t.Fatal("setup recovery reopened or refunded the old grant", err)
			}
			unchanged, err := db.Assignment(ctx, freshOwner.ID)
			if err != nil || !reflect.DeepEqual(unchanged, freshOwner) {
				t.Fatal("setup recovery changed the old owner", err)
			}
			recovered, err := db.Assignment(ctx, recoveryCommand)
			if err != nil || recovered.Run != "" || recovered.Model != "" || recovered.Authority.RequirementsID != requirement.ID || recovered.Authority.ApprovalID != approval.ID {
				t.Fatal("setup recovery lost fresh authority or replayed execution", err)
			}
			recoveredPublication, err := db.PublicationByID(ctx, recoveryCommand)
			if err != nil {
				t.Fatal(err)
			}
			history, err = db.PublicationReviewHistory(ctx, recoveredPublication)
			if err != nil || len(history) != 4 || !reflect.DeepEqual(history[1], adopted) || history[2].ID != freshPublication.ID || len(history[2].ReviewCycles) != 0 || history[3].ID != recoveryCommand {
				t.Fatal("setup recovery lost finding history or retained an unlaunched derived cycle", err)
			}
			canonicalPublication, err := db.PublicationByID(ctx, freshPublication.ID)
			if err != nil || !reflect.DeepEqual(canonicalPublication, freshPublication) || len(canonicalPublication.ReviewCycles) != 1 || canonicalPublication.ReviewCycles[0].ReviewerAssignment != reserved {
				t.Fatal("derived history erased the canonical reserved cycle", err)
			}
			if err = db.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_runs),(SELECT count(*) FROM factory_reservations)`).Scan(&runs, &reservations); err != nil || runs != oldRuns || reservations != oldReservations {
				t.Fatal("setup recovery replayed a run/reservation", err)
			}
			if replay, err := db.FundRetainedCandidate(ctx, freshCommand, "owner", freshOffer, time.Now()); err != nil || replay != receipt {
				t.Fatal("setup recovery changed the original funding receipt", err)
			}
		}
		ctx, db := context.Background(), dispatchStoreFixture(t)
		sponsor, err := db.Sponsorship(ctx, 7, "conn")
		if err != nil {
			t.Fatal(err)
		}
		sponsor.Roles = append(sponsor.Roles, project.RoleReviewer)
		if err = db.SaveSponsorship(ctx, sponsor); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		source := recordFinishedAssignment(t, db, now, 3, true, "completed")
		original := publicationTestRecord()
		original.Authority = source.Authority
		original.AssignmentID, original.Run, original.ProjectID = source.ID, source.Run, source.ProjectID
		original.BaseSHA, original.Candidate, original.Preparation = source.SourceCommit, source.Result.Candidate, source.Preparation
		if err := db.RecordPublication(ctx, original); err != nil {
			t.Fatal(err)
		}
		original.Stage, original.Outcome, original.Reason, original.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, now.Unix()
		original.Revision++
		if err := db.UpdatePublication(ctx, original); err != nil {
			t.Fatal(err)
		}
		originalBytes, _ := json.Marshal(original)
		allowance, err := db.AttemptAllowance(ctx, source.Repository, source.Issue)
		if err != nil || !allowance.Closed || allowance.Active {
			t.Fatalf("old allowance: %+v %v", allowance, err)
		}
		// An unresolved old publication may retain its accounting checkpoint.
		// Closed still forbids new inference; this explicit publication neither
		// requires inventing its outcome nor changes that old accounting state.
		allowance.Active, allowance.CheckpointUnix = true, max(allowance.CheckpointUnix, now.Unix())
		allowance.Revision++
		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		allowanceBytes, _ := json.Marshal(allowance)
		command := factory.NewID()
		r := &factory.RetainedPublication{Publication: original.ID, Assignment: source.ID, Run: source.Run, Candidate: original.Candidate, Connection: source.Connection, NotAfter: now.Add(5 * time.Minute).Unix()}
		a := factory.Assignment{ID: command, AttemptRoot: command, PublicationAssignment: command, RetainedPublication: r,
			Subject: source.Subject, Authority: source.Authority, ProjectID: source.ProjectID, Role: source.Role, Acceptance: source.Acceptance,
			Preparation: source.Preparation, SourceCommit: source.SourceCommit, Stage: factory.AssignmentRetained,
			Repository: source.Repository, Issue: source.Issue, NativeRev: source.NativeRev, ActorID: source.ActorID, CreatedUnix: now.Unix()}
		fresh := original
		fresh.ID, fresh.AssignmentID, fresh.Stage, fresh.Outcome, fresh.Reason, fresh.Revision, fresh.FinishedUnix = command, command, factory.PublicationOpen, "", "", 0, 0
		payload, _ := json.Marshal(map[string]string{"publication": original.ID})
		cmd := factory.Command{ID: command, Type: factory.CommandPublishRetained, Target: "publication/" + original.ID, Principal: "owner", Payload: string(payload)}
		cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
		if _, err = db.RecordRetainedPublicationCommand(ctx, cmd, a, fresh, now); err != nil {
			t.Fatal(err)
		}
		if _, err = db.RecordRetainedPublicationCommand(ctx, cmd, a, fresh, now.Add(time.Hour)); err != nil {
			t.Fatalf("replay renewed/rejected old authority: %v", err)
		}
		changed := cmd
		changed.Payload = `{"publication":"changed"}`
		changed.Digest = factory.SettingsDigest(changed.Type, changed.Target, changed.Payload)
		if _, err = db.RecordRetainedPublicationCommand(ctx, changed, a, fresh, now); !errors.Is(err, ErrCommandConflict) {
			t.Fatalf("changed command: %v", err)
		}
		if factory.PublishBranchName(source.ID) == factory.PublishBranchName(a.ID) {
			t.Fatal("reused uncertain branch")
		}
		var runs, reservations, allowances int
		if err = db.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_runs),(SELECT count(*) FROM factory_reservations),(SELECT count(*) FROM factory_attempt_allowances)`).Scan(&runs, &reservations, &allowances); err != nil || runs != 1 || reservations != 1 || allowances != 1 {
			t.Fatalf("paid work replayed: %d %d %d %v", runs, reservations, allowances, err)
		}
		if _, err = db.CapAttemptDeadline(ctx, a.ID, now.Add(time.Minute).Unix()); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
			t.Fatalf("publication acquired inference allowance: %v", err)
		}
		intent := publicationStoreIntent("retained-publish-op")
		intent.NotAfter = r.NotAfter + 1
		fresh.Publish = factory.PublicationOperation{Kind: factory.OpRefPublish, OperationID: intent.OperationID, Attempts: 1, UpdatedUnix: now.Unix(), Work: intent}
		fresh.Revision++
		if err = db.UpdatePublication(ctx, fresh); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
			t.Fatalf("renewed publication deadline: %v", err)
		}
		intent.NotAfter = r.NotAfter
		if err = db.UpdatePublication(ctx, fresh); err != nil {
			t.Fatalf("fresh bounded branch intent: %v", err)
		}
		stored, err := db.PublicationByID(ctx, original.ID)
		storedBytes, _ := json.Marshal(stored)
		if err != nil || string(storedBytes) != string(originalBytes) {
			t.Fatalf("changed original uncertain publication: %v", err)
		}
		storedAllowance, err := db.AttemptAllowance(ctx, source.Repository, source.Issue)
		storedAllowanceBytes, _ := json.Marshal(storedAllowance)
		if err != nil || string(storedAllowanceBytes) != string(allowanceBytes) {
			t.Fatalf("renewed original allowance: %v", err)
		}
		fresh.Publish.Effect, fresh.Publish.Completion, fresh.Publish.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, "fresh-branch-receipt"
		fresh.Comparison, fresh.NativeRev, fresh.ObservedUnix = fresh.BaseSHA, 9, now.Unix()
		fresh.Revision++
		if err = db.UpdatePublication(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		fresh.PRCreate = publishedStoreOperation(fresh, factory.OpPRCreate, 1, "")
		fresh.PRCreate.Work.NotAfter = r.NotAfter
		fresh.PRCreate.Effect, fresh.PRCreate.Completion = factory.OpEffectPending, factory.OpCompletionPending
		fresh.Revision++
		if err = db.UpdatePublication(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		fresh.PRCreate.Effect, fresh.PRCreate.Completion, fresh.PRCreate.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, "fresh-pr-receipt"
		fresh.PRNumber, fresh.PRID = 19, 190
		fresh.PRCreate.PRNumber, fresh.PRCreate.PRID, fresh.PRCreate.IssueID = 19, 190, fresh.Issue
		fresh.PRCreate.HeadRef, fresh.PRCreate.BaseRef = factory.PublishBranchName(fresh.AssignmentID), fresh.TargetBranch
		fresh.PRCreate.HeadOID, fresh.PRCreate.BaseOID = fresh.Candidate, fresh.BaseSHA
		fresh.Stage, fresh.Outcome, fresh.Reason, fresh.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, now.Unix()
		fresh.Revision++
		if err = db.UpdatePublication(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		funding, err := db.RetainedCandidateFundingOffer(ctx, fresh.ID)
		if err != nil {
			t.Fatal("funding offer", err)
		}
		changedFunding := funding
		changedFunding.Generation++
		if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", changedFunding, now); !errors.Is(err, ErrRetainedPublicationIneligible) {
			t.Fatal("changed source funded", err)
		}
		fundCommand := factory.NewID()
		receipt, err := db.FundRetainedCandidate(ctx, fundCommand, "owner", funding, now)
		if err != nil {
			t.Fatal("finite continuation", err)
		}
		replay, err := db.FundRetainedCandidate(ctx, fundCommand, "owner", funding, now.Add(time.Hour))
		if err != nil || receipt != replay {
			t.Fatal("funding replay changed or renewed", err)
		}
		if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", funding, now); err == nil {
			t.Fatal("second command replenished continuation")
		}
		var oldRaw []byte
		if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, source.ID).Scan(&oldRaw); err != nil {
			t.Fatal(err)
		}
		var old factory.AttemptAllowance
		if json.Unmarshal(oldRaw, &old) != nil {
			t.Fatal("old allowance unreadable")
		}
		oldBytes, _ := json.Marshal(old)
		if string(oldBytes) != string(allowanceBytes) {
			t.Fatal("funding changed old allowance")
		}
		if _, err = db.CapAttemptDeadline(ctx, a.ID, now.Add(time.Minute).Unix()); err != nil {
			t.Fatal("new continuation lacks finite time", err)
		}
		// A shutdown before the first reviewer packet must not reopen an old
		// grant or replay publication. An explicit fresh command adopts only
		// complete PR receipts into a distinct review-only owner.
		unlaunched := factory.NewID()
		if _, exhausted, err := db.ChargeReviewDispatch(ctx, a.Repository, a.Issue, a.ID, unlaunched, fresh.Candidate, false); err != nil || exhausted {
			t.Fatal("unlaunched review charge", err)
		}
		if closed, err := db.CloseAttempt(ctx, a.ID); err != nil || !closed {
			t.Fatal("close prior continuation", err)
		}
		closedAllowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
		if err != nil {
			t.Fatal(err)
		}
		closedRaw, _ := json.Marshal(closedAllowance)
		oldPublication := fresh
		offer, err := db.RetainedCandidateFundingOffer(ctx, fresh.ID)
		if err != nil || offer.PreviousRoot != a.ID {
			t.Fatal("fresh review continuation offer", err)
		}
		newCommand := factory.NewID()
		newReceipt, err := db.FundRetainedCandidate(ctx, newCommand, "owner", offer, now)
		if err != nil {
			t.Fatal("fresh review-only owner", err)
		}
		if replay, err := db.FundRetainedCandidate(ctx, newCommand, "owner", offer, now.Add(time.Hour)); err != nil || replay != newReceipt {
			t.Fatal("review-only replay changed", err)
		}
		var afterRaw []byte
		if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&afterRaw); err != nil {
			t.Fatal(err)
		}
		var afterClosed factory.AttemptAllowance
		_ = json.Unmarshal(afterRaw, &afterClosed)
		afterClosedRaw, _ := json.Marshal(afterClosed)
		if string(afterClosedRaw) != string(closedRaw) {
			t.Fatal("fresh review renewed old allowance or erased spent review")
		}
		a, err = db.Assignment(ctx, newCommand)
		if err != nil || a.RetainedPublication.AdoptedFrom != oldPublication.ID || a.RetainedPublication.NotAfter != 0 {
			t.Fatal("fresh review obtained publication authority", err)
		}
		fresh, err = db.PublicationByID(ctx, newCommand)
		oldPR, _ := json.Marshal(oldPublication.PRCreate)
		adoptedPR, _ := json.Marshal(fresh.PRCreate)
		oldBranch, _ := json.Marshal(oldPublication.Publish)
		adoptedBranch, _ := json.Marshal(fresh.Publish)
		if err != nil || string(oldPR) != string(adoptedPR) || string(oldBranch) != string(adoptedBranch) || fresh.Run != oldPublication.Run {
			t.Fatal("publication receipt or execution changed", err)
		}
		if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", offer, now); err == nil {
			t.Fatal("stale prior-owner offer funded again")
		}
		// The actual retained owner has no run/model. Its separately funded
		// reviewer still enters the ordinary phase and atomic packet guards.
		inputOwner := a
		inputOwner.Connection = source.Connection
		child, reservation, childRun, childView := reviewCorrectChild(t, ctx, db, now, inputOwner, project.ExecutionReview, project.RoleReviewer, a.ID, "f"+strings.Repeat("d", 24))
		childRun.Deadline = time.Unix(now.Unix()+5*60, 0)
		reservation.PlannedMinutes = 5
		cycle := reviewCorrectCycle(t, fresh, a, 1, child.ID, "")
		cycle.Head, cycle.BaseOID = fresh.Candidate, fresh.PRCreate.BaseOID
		fresh.ReviewCycles = append(fresh.ReviewCycles, cycle)
		fresh.Revision++
		if err = db.UpdatePublication(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		if err = recordDispatchTestPacket(t, ctx, db, child, reservation, childRun, childView); err != nil {
			t.Fatal("funded retained reviewer packet", err)
		}
		origin, err := db.PublicationExecutionOwner(ctx, fresh, a)
		if err != nil || origin.ID != source.ID || origin.Run != source.Run {
			t.Fatal("original execution provenance was replaced", err)
		}
		// New review/correction operations use the new finite allowance, not
		// the expired original branch/PR send deadline (zero on this owner).
		trial := fresh
		appendPendingCorrection(&trial)
		trial.Corrections[len(trial.Corrections)-1].Work.NotAfter = time.Now().Add(time.Minute).Unix()
		registration, deadlines, err := publicationRegistration(fresh, trial)
		if err != nil {
			t.Fatal(err)
		}
		tx, err = db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = admitPublicationRegistrationTx(ctx, tx, fresh, trial, registration, deadlines)
		_ = tx.Rollback()
		if err != nil {
			t.Fatal("fresh correction used original publication deadline", err)
		}
		// An explicitly incomplete reviewer may hand off only its unused
		// budget. Its charged identity/head ledger remains cumulative and the
		// earlier result is immutable; this cannot buy another full allowance.
		if _, exhausted, err := db.ChargeReviewDispatch(ctx, child.Repository, child.Issue, child.AttemptRoot, child.ID, fresh.Candidate, false); err != nil || exhausted {
			t.Fatal("review charge", err)
		}
		childRun.Outcome, childRun.Reconciled = factory.Succeeded, true
		if err = db.SaveFactoryRun(ctx, childRun); err != nil {
			t.Fatal(err)
		}
		if err = db.ConsumeReservation(ctx, child.ID); err != nil {
			t.Fatal(err)
		}
		child.Stage, child.Outcome, child.Reason, child.FinishedUnix = factory.AssignmentFinished, factory.NeedsHuman, factory.AssignReasonNoReport, time.Now().Unix()
		child.Result = &factory.AssignmentResult{AssignmentID: child.ID, RunID: child.Run, Status: "blocked", Summary: "context unavailable", RecordedUnix: time.Now().Unix(), Findings: []string{}}
		if err = db.FinishAssignment(ctx, child); err != nil {
			t.Fatal(err)
		}
		unused, err := db.RetainedCandidateFundingOffer(ctx, fresh.ID)
		if err != nil || !unused.CarryUnused {
			t.Fatal("unused review offer", err)
		}
		refill := unused
		refill.CarryUnused = false
		if _, err = db.FundRetainedCandidate(ctx, factory.NewID(), "owner", refill, time.Now()); err == nil {
			t.Fatal("incomplete reviewer bought a fresh full allowance")
		}
		continuation := factory.NewID()
		if _, err = db.FundRetainedCandidate(ctx, continuation, "owner", unused, time.Now()); err != nil {
			t.Fatal("unused review handoff", err)
		}
		carried, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
		if err != nil || carried.RootAssignment != continuation || len(carried.Reviews) != 1 || carried.Reviews[0] != child.ID || carried.ReviewedHeads[fresh.Candidate] != child.ID || carried.Limits != unused.Limits {
			t.Fatal("unused handoff refunded a round/head or changed bounds", err)
		}
		lastReview := factory.NewID()
		if _, exhausted, err := db.ChargeReviewDispatch(ctx, child.Repository, child.Issue, continuation, lastReview, fresh.Candidate, false); err != nil || exhausted {
			t.Fatal("remaining review withheld", err)
		}
		if _, exhausted, err := db.ChargeReviewDispatch(ctx, child.Repository, child.Issue, continuation, factory.NewID(), fresh.Candidate, false); err != nil || !exhausted {
			t.Fatal("handoff added an unauthorized third review", err)
		}
		// Explicit fresh funding can adopt a definitively published candidate
		// after a prelaunch fence. The old exhausted ledger stays closed.
		fenced, err := db.PublicationByID(ctx, continuation)
		if err != nil {
			t.Fatal(err)
		}
		fenced.Stage, fenced.Outcome, fenced.Reason, fenced.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, time.Now().Unix()
		fenced.Revision++
		if err = db.UpdatePublication(ctx, fenced); err != nil {
			t.Fatal(err)
		}
		if closed, err := db.CloseAttempt(ctx, continuation); err != nil || !closed {
			t.Fatal("prior root not closed", err)
		}
		freshOffer, err := db.RetainedCandidateFundingOffer(ctx, continuation)
		if err != nil || freshOffer.CarryUnused || freshOffer.PreviousRoot != continuation {
			t.Fatal("fenced unlaunched offer", err)
		}
		freshCommand := factory.NewID()
		if _, err = db.FundRetainedCandidate(ctx, freshCommand, "owner", freshOffer, time.Now()); err != nil {
			t.Fatal("fresh funding of confirmed publication", err)
		}
		newPublication, err := db.PublicationByID(ctx, freshCommand)
		if err != nil || newPublication.Stage != factory.PublicationPublished || !reflect.DeepEqual(newPublication.Publish, fenced.Publish) || !reflect.DeepEqual(newPublication.PRCreate, fenced.PRCreate) {
			t.Fatal("fresh owner lost confirmed receipts", err)
		}
		tx, err = db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		spentLedger, err := lockAttemptAllowanceRootTx(ctx, tx, source.Repository, source.Issue, continuation)
		_ = tx.Rollback()
		if err != nil || !spentLedger.Closed || len(spentLedger.Reviews) != 2 {
			t.Fatal("fresh funding changed old consumption", err)
		}
		policy, err := db.RepositoryPolicy(ctx, newPublication.Repository)
		if err != nil {
			t.Fatal(err)
		}
		m := mergeTestRecord()
		m.PublicationID, m.AssignmentID, m.ProjectID, m.Acceptance = newPublication.ID, newPublication.AssignmentID, newPublication.ProjectID, newPublication.Acceptance
		m.Repository, m.Issue, m.PRNumber, m.PRID = newPublication.Repository, newPublication.Issue, newPublication.PRNumber, newPublication.PRID
		m.IssueID, m.PRAuthorID, m.ReviewerID = newPublication.PRCreate.IssueID, newPublication.PRCreate.Work.ActorID, policy.Review.ActorID
		m.HeadOID, m.BaseOID, m.HeadRef, m.BaseRef = newPublication.Candidate, newPublication.PRCreate.BaseOID, newPublication.PRCreate.HeadRef, newPublication.PRCreate.BaseRef
		m.Authority = newPublication.Authority
		m.Authority.Policy = policy.Revision + 1 // a stale, never-authorized preparation
		if err = db.RecordMerge(ctx, m); err != nil {
			t.Fatal(err)
		}
		var prior string
		if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_merges WHERE publication=$1`, m.PublicationID).Scan(&prior); err != nil {
			t.Fatal(err)
		}
		prepare, err := db.UnsentMergePreparation(ctx, m.PublicationID)
		if err != nil {
			t.Fatal("unsent merge offer", err)
		}
		stalePrepare := prepare
		stalePrepare.Revision++
		if _, err = db.PrepareUnsentMerge(ctx, factory.NewID(), "owner", stalePrepare, time.Now()); !errors.Is(err, ErrStaleRevision) {
			t.Fatal("stale merge preparation", err)
		}
		mergeCommand := factory.NewID()
		mergeReceipt, err := db.PrepareUnsentMerge(ctx, mergeCommand, "owner", prepare, time.Now())
		if err != nil {
			t.Fatal("fresh unsent merge preparation", err)
		}
		mergeReplay, err := db.PrepareUnsentMerge(ctx, mergeCommand, "owner", prepare, time.Now().Add(time.Hour))
		if err != nil || mergeReplay != mergeReceipt {
			t.Fatal("merge preparation replay", err)
		}
		archived, err := db.FactoryCommand(ctx, mergeCommand)
		var capture struct {
			Prior string `json:"prior"`
		}
		if err != nil || json.Unmarshal([]byte(archived.Outcome), &capture) != nil || capture.Prior != prior {
			t.Fatal("old merge preparation changed", err)
		}
		prepared, err := db.MergeByPublication(ctx, m.PublicationID)
		if err != nil || prepared.ID != mergeCommand || prepared.Authority.Policy != policy.Revision || prepared.Operation.OperationID != "" || prepared.Operation.Attempts != 0 {
			t.Fatal("fresh merge identity/authority", err)
		}
		active, err := db.ActivePublishedPublications(ctx, 100)
		if err != nil || !slices.ContainsFunc(active, func(p factory.Publication) bool { return p.ID == newPublication.ID }) {
			t.Fatal("recorded undriven merge excluded from current check assessment", err)
		}
		if _, err = db.UnsentMergePreparation(ctx, m.PublicationID); !errors.Is(err, ErrMergeConflict) {
			t.Fatal("current merge offered needless replacement", err)
		}
		// Even a pending allocated operation cannot be replaced on lookup absence.
		tx, err = db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, owner, grants, err := retainedFundingScopeTx(ctx, tx, m.PublicationID)
		if err != nil {
			t.Fatal(err)
		}
		prepared.Authority.Policy++
		prepared.Operation.OperationID = factory.MergeOperationIDForGeneration(prepared.PublicationID, 1)
		if err = admitUnsentMergePreparationTx(ctx, tx, newPublication, owner, grants, prepared); !errors.Is(err, ErrMergeConflict) {
			t.Fatal("allocated operation could be replaced", err)
		}
		_ = tx.Rollback()
		// Retain a definitive no-send predecessor on a closed inference root.
		// Native receipt equality is checked by the coordinator before this
		// store command; this fixture exercises only the atomic product ledger.
		refused, err := db.MergeByPublication(ctx, newPublication.ID)
		if err != nil {
			t.Fatal(err)
		}
		refused.Authorization = mergeTestAuthorization(refused)
		retryIntent := mergeTestIntent(factory.MergeOperationIDForGeneration(refused.PublicationID, 1))
		retryIntent.HeadOID, retryIntent.BaseOID, retryIntent.HeadRef, retryIntent.BaseRef = refused.HeadOID, refused.BaseOID, refused.HeadRef, refused.BaseRef
		retryIntent.Repository, retryIntent.PRNumber, retryIntent.PRID, retryIntent.IssueID = refused.Repository, refused.PRNumber, refused.PRID, refused.IssueID
		retryIntent.PRAuthorID, retryIntent.ReviewerID, retryIntent.NotAfter = refused.PRAuthorID, refused.ReviewerID, 1200
		refused.Operation = factory.MergeOperation{Kind: factory.OpMerge, Work: retryIntent, OperationID: retryIntent.OperationID, Attempts: 1, UpdatedUnix: time.Now().Unix(), Effect: factory.OpEffectNotCommitted, Completion: factory.OpCompletionComplete, Cancellation: factory.OpCancelNone, Reason: "invalid_native_write", RepositoryID: refused.Repository, ActorID: retryIntent.ActorID, InstallationID: "native-fixture"}
		refused.Stage, refused.Outcome, refused.Reason, refused.FinishedUnix = factory.MergeFailed, factory.Failed, factory.MergeReasonRefused, time.Now().Unix()
		if err = refused.Validate(); err != nil {
			t.Fatal(err)
		}
		refusedRaw, _ := json.Marshal(refused)
		if _, err = db.db.ExecContext(ctx, `UPDATE factory_merges SET stage='failed',data=$1 WHERE publication=$2`, string(refusedRaw), refused.PublicationID); err != nil {
			t.Fatal(err)
		}
		if _, err = db.CloseAttempt(ctx, refused.AssignmentID); err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.ExecContext(ctx, `UPDATE factory_attempt_allowances SET data=jsonb_set(data,'{active}','true'::jsonb) WHERE root_assignment=$1`, freshCommand); err != nil {
			t.Fatal(err)
		}
		var closedBefore, closedAfter string
		if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_attempt_allowances WHERE root_assignment=$1`, freshCommand).Scan(&closedBefore); err != nil {
			t.Fatal(err)
		}
		noSend, err := db.UnsentMergePreparation(ctx, refused.PublicationID)
		if err != nil || !noSend.RetryNoSend {
			t.Fatal("definitive no-send offer", err)
		}
		retryCommand := factory.NewID()
		retryReceipt, err := db.PrepareUnsentMerge(ctx, retryCommand, "owner", noSend, time.Now())
		if err != nil || retryReceipt.NotAfter <= time.Now().Unix() || retryReceipt.NotAfter > time.Now().Unix()+300 {
			t.Fatal("finite merge-only admission", err)
		}
		retryReplay, err := db.PrepareUnsentMerge(ctx, retryCommand, "owner", noSend, time.Now().Add(time.Hour))
		if err != nil || retryReplay != retryReceipt {
			t.Fatal("merge-only replay renewed deadline", err)
		}
		if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_attempt_allowances WHERE root_assignment=$1`, freshCommand).Scan(&closedAfter); err != nil || closedAfter != closedBefore {
			t.Fatal("merge-only retry renewed inference", err)
		}
		retried, err := db.MergeByPublication(ctx, refused.PublicationID)
		priorGeneration := factory.SnapshotMergeGeneration(refused.Authorization, refused.Operation, refused.Stage, refused.Outcome, refused.Reason, refused.FinishedUnix)
		if err != nil || len(retried.History) != 1 || !reflect.DeepEqual(retried.History[0], priorGeneration) || retried.Authorization.Bound() || retried.Operation.Attempts != 0 {
			t.Fatal("no-send generation history changed", err)
		}
		deadline, err := db.MergeSendDeadline(ctx, retried, time.Now().Add(time.Hour).Unix())
		if err != nil || deadline != retryReceipt.NotAfter {
			t.Fatal("merge-only deadline", err)
		}
		if _, err = db.CapAttemptDeadline(ctx, retried.AssignmentID, time.Now().Add(time.Minute).Unix()); !errors.Is(err, factory.ErrAttemptClosed) {
			t.Fatal("merge-only grant acquired model authority", err)
		}
		active, err = db.ActivePublishedPublications(ctx, 100)
		if err != nil || slices.ContainsFunc(active, func(p factory.Publication) bool { return p.ID == newPublication.ID }) {
			t.Fatal("merge-only root entered paid child enumeration", err)
		}
		checkable, err := db.CheckablePublications(ctx, 100)
		if err != nil || !slices.ContainsFunc(checkable, func(p factory.Publication) bool { return p.ID == newPublication.ID }) {
			t.Fatal("merge-only root cannot refresh CI", err)
		}
		renewed := retried
		renewed.RetryNotAfter++
		renewed.Revision++
		if err = db.UpdateMerge(ctx, renewed); err == nil {
			t.Fatal("ordinary merge update renewed finite retry")
		}
		// A lost reply can leave a bound preparation with no allocated native
		// operation. A distinct command archives that unused authorization;
		// it cannot alter the original command or duplicate a recorded send.
		retried.Authorization = mergeTestAuthorization(retried)
		retried.Authorization.Generation.Generation = 2
		retried.Authorization.Generation.NativeOperationID = factory.MergeGenerationOperationID(retried.PublicationID, 2)
		retried.Authorization.Generation.NotAfter = retryReceipt.NotAfter - 1
		retried.Revision++
		if err = db.UpdateMerge(ctx, retried); err != nil {
			t.Fatal("bind unused retry generation", err)
		}
		var boundPrior string
		if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_merges WHERE publication=$1`, retried.PublicationID).Scan(&boundPrior); err != nil {
			t.Fatal(err)
		}
		if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		unusedMerge, err := db.UnsentMergePreparation(ctx, retried.PublicationID)
		if err != nil || !unusedMerge.RetryNoSend {
			t.Fatal("unused bound merge preparation", err)
		}
		nextCommand := factory.NewID()
		if _, err = db.PrepareUnsentMerge(ctx, nextCommand, "owner", unusedMerge, time.Now()); err != nil {
			t.Fatal("fresh unused merge command", err)
		}
		freshMerge, err := db.MergeByPublication(ctx, retried.PublicationID)
		if err != nil || freshMerge.ID != nextCommand || freshMerge.Authorization.Bound() || len(freshMerge.History) != 1 || !reflect.DeepEqual(freshMerge.History[0], priorGeneration) {
			t.Fatal("unused preparation rewrote native history", err)
		}
		unusedArchive, err := db.FactoryCommand(ctx, nextCommand)
		if err != nil || json.Unmarshal([]byte(unusedArchive.Outcome), &capture) != nil || capture.Prior != boundPrior {
			t.Fatal("unused authorization/deadline was not archived exactly", err)
		}
		oldReceipt, err := db.PrepareUnsentMerge(ctx, retryCommand, "owner", noSend, time.Now().Add(time.Hour))
		if err != nil || oldReceipt != retryReceipt {
			t.Fatal("old retry was renewed after fresh preparation", err)
		}
		if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_attempt_allowances WHERE root_assignment=$1`, freshCommand).Scan(&closedAfter); err != nil || closedAfter != closedBefore {
			t.Fatal("unused preparation changed closed inference", err)
		}
		if _, err = db.WithdrawDispatch(ctx, source.Repository, factory.CauseControlPaused, "owner"); err != nil {
			t.Fatal(err)
		}
		appendPendingCorrection(&fresh)
		if err = db.UpdatePublication(ctx, fresh); !errors.Is(err, ErrDispatchClosed) {
			t.Fatalf("withdrawn new authority: %v", err)
		}
	})
	db := publicationStoreFixture(t)
	ctx := context.Background()
	p := publicationTestRecord()
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != p.ID || got.Stage != factory.PublicationOpen {
		t.Fatalf("round trip: %+v", got)
	}
	if err := db.RecordPublication(ctx, p); err == nil {
		t.Fatal("duplicate assignment publication accepted")
	}
	if _, err := db.PublicationByAssignment(ctx, factory.NewID()); err == nil {
		t.Fatal("missing publication read accepted")
	}
}

func TestUpdatePublicationComparesAndSwaps(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("record: %v", err)
	}
	p.Revision++
	p.NativeRev, p.ObservedUnix = 9, 1150
	p.Publish = factory.PublicationOperation{
		Work:        publicationStoreIntent("soda-test-publish-1"),
		OperationID: "soda-test-publish-1", Kind: factory.OpRefPublish,
		Effect: factory.OpEffectPending, Attempts: 1, UpdatedUnix: 1150,
	}
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	stale := p
	stale.Revision = 1
	stale.Comparison = strings.Repeat("c", 40)
	if err := db.UpdatePublication(ctx, stale); err == nil {
		t.Fatal("stale publication update accepted")
	}
	got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Revision != 1 || got.Publish.OperationID != "soda-test-publish-1" {
		t.Fatalf("stored: %+v", got)
	}
}

func finishedPublishableAssignment(t *testing.T, now time.Time, issue int64, reported bool, status string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	a, r, run, view := dispatchTestPacket(t, now)
	a.Issue = issue
	a.Subject.Issue.Issue = issue
	view.Issue = issue
	result := factory.AssignmentResult{
		AssignmentID: a.ID, RunID: a.Run, Status: status,
		Summary: "done", Candidate: strings.Repeat("2", 40),
		Findings: []string{}, Reported: reported, RecordedUnix: now.Unix(),
	}
	a.Stage, a.Outcome, a.Reason, a.Result, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, &result, now.Unix()
	return a, r, run, view
}

func recordFinishedAssignment(t *testing.T, db *Store, now time.Time, issue int64, reported bool, status string) factory.Assignment {
	t.Helper()
	ctx := context.Background()
	a, r, run, view := finishedPublishableAssignment(t, now, issue, reported, status)
	a.Authority = dispatchCurrentAuthority(t, db, a)
	assigned := a
	assigned.Stage, assigned.Outcome, assigned.Reason, assigned.Result, assigned.FinishedUnix = factory.AssignmentAssigned, "", "", nil, 0
	if err := recordDispatchTestPacket(t, ctx, db, assigned, r, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishAssignment(ctx, a); err != nil {
		t.Fatal(err)
	}
	settled := run
	settled.Outcome, settled.Reconciled = factory.Succeeded, true
	if err := db.SaveFactoryRun(ctx, settled); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPublishableAssignmentsSelectsReportedCompleted(t *testing.T) {
	t.Run("reported completed result is eligible", func(t *testing.T) {
		db := publicationStoreFixture(t)
		ctx := context.Background()
		want := recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, true, "completed")
		got, err := db.PublishableAssignments(ctx, 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].ID != want.ID {
			t.Fatalf("publishable: %+v", got)
		}
	})
	t.Run("no report is ineligible", func(t *testing.T) {
		db := publicationStoreFixture(t)
		recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, false, "completed")
		got, err := db.PublishableAssignments(context.Background(), 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("publishable without report: %+v %v", got, err)
		}
	})
	t.Run("blocked result is ineligible", func(t *testing.T) {
		db := publicationStoreFixture(t)
		recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, true, "blocked")
		got, err := db.PublishableAssignments(context.Background(), 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("publishable blocked result: %+v %v", got, err)
		}
	})
	t.Run("recorded publication excludes assignment", func(t *testing.T) {
		db := publicationStoreFixture(t)
		ctx := context.Background()
		want := recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, true, "completed")
		p := publicationTestRecord()
		p.AssignmentID = want.ID
		p.Repository, p.Issue = want.Repository, want.Issue
		if err := db.RecordPublication(ctx, p); err != nil {
			t.Fatalf("record: %v", err)
		}
		got, err := db.PublishableAssignments(ctx, 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("published assignment still publishable: %+v %v", got, err)
		}
	})
}

func TestOutstandingPublicationsListsOpenAndFenced(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	open := publicationTestRecord()
	if err := db.RecordPublication(ctx, open); err != nil {
		t.Fatalf("record open: %v", err)
	}
	fenced := publicationTestRecord()
	fenced.AssignmentID = factory.NewID()
	if err := db.RecordPublication(ctx, fenced); err != nil {
		t.Fatalf("record fenced: %v", err)
	}
	fenced.Stage, fenced.Outcome, fenced.Reason, fenced.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, 1200
	fenced.Revision++
	if err := db.UpdatePublication(ctx, fenced); err != nil {
		t.Fatalf("fence: %v", err)
	}
	done := publicationTestRecord()
	done.AssignmentID = factory.NewID()
	done.Issue = 9
	if err := db.RecordPublication(ctx, done); err != nil {
		t.Fatalf("record done: %v", err)
	}
	done.Stage, done.Outcome, done.Reason, done.FinishedUnix = factory.PublicationFailed, factory.Failed, factory.PublishReasonRefused, 1200
	done.Revision++
	if err := db.UpdatePublication(ctx, done); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got, err := db.OutstandingPublications(ctx, 7, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("outstanding: %+v", got)
	}
	if got[0].AssignmentID != open.AssignmentID || got[1].AssignmentID != fenced.AssignmentID {
		t.Fatalf("order: %+v", got)
	}
}

func TestCorrectionRegistrationAndWithdrawalShareAbsentGate(t *testing.T) {
	t.Run("registration first is captured for recovery", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		var rows int
		if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_dispatch WHERE repository=$1`, p.Repository).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("gate fixture was not absent: rows=%d err=%v", rows, err)
		}
		appendPendingCorrection(&p)
		if err := db.UpdatePublication(ctx, p); err != nil {
			t.Fatalf("register correction under default-open gate: %v", err)
		}
		recovery, err := db.OpenPublications(ctx, 10)
		if err != nil || len(recovery) != 1 || len(recovery[0].Corrections) != 1 {
			t.Fatalf("published correction omitted from recovery pass: %+v %v", recovery, err)
		}
		if _, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
			t.Fatal(err)
		}
		pending, err := db.OutstandingPublications(ctx, p.Repository, 10)
		if err != nil || len(pending) != 1 || len(pending[0].Corrections) != 1 {
			t.Fatalf("registered correction escaped recovery: %+v %v", pending, err)
		}
	})
	t.Run("withdrawal first rejects correction", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		if _, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
			t.Fatal(err)
		}
		appendPendingCorrection(&p)
		if err := db.UpdatePublication(ctx, p); !errors.Is(err, ErrDispatchClosed) {
			t.Fatalf("correction registered after withdrawal: %v", err)
		}
		stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil || len(stored.Corrections) != 0 {
			t.Fatalf("refused correction changed publication: %+v %v", stored, err)
		}
	})
	t.Run("concurrent absent-row race has no lost registration", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		appendPendingCorrection(&p)
		start := make(chan struct{})
		registered := make(chan error, 1)
		withdrawn := make(chan error, 1)
		go func() { <-start; registered <- db.UpdatePublication(ctx, p) }()
		go func() {
			<-start
			_, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator")
			withdrawn <- err
		}()
		close(start)
		regErr, withdrawErr := <-registered, <-withdrawn
		if withdrawErr != nil {
			t.Fatal(withdrawErr)
		}
		if regErr != nil && !errors.Is(regErr, ErrDispatchClosed) {
			t.Fatalf("unexpected registration result: %v", regErr)
		}
		stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		if regErr == nil {
			pending, err := db.OutstandingPublications(ctx, p.Repository, 10)
			if err != nil || len(pending) != 1 || len(pending[0].Corrections) != 1 {
				t.Fatalf("accepted correction was lost to withdrawal: %+v %v", pending, err)
			}
		} else if len(stored.Corrections) != 0 {
			t.Fatalf("refused concurrent correction persisted: %+v", stored.Corrections)
		}
	})
	t.Run("withdrawal waits for registered update at publication row", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		appendPendingCorrection(&p)

		// Hold the publication row so registration blocks after acquiring the
		// shared dispatch gate. The observer below waits for PostgreSQL to
		// report that exact statement waiting on a lock; this does not depend
		// on goroutine scheduling or an arbitrary sleep.
		blocker, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback() }()
		var locked []byte
		if err = blocker.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, p.AssignmentID).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		registered := make(chan error, 1)
		go func() { registered <- db.UpdatePublication(ctx, p) }()

		waitCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
		defer cancelWait()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting int
			err = db.db.QueryRowContext(waitCtx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname=current_database() AND state='active' AND wait_event_type='Lock'
				AND query LIKE '%UPDATE factory_publications SET stage=%'`).Scan(&waiting)
			if err != nil {
				_ = blocker.Rollback()
				t.Fatalf("observe blocked publication update: %v", err)
			}
			if waiting > 0 {
				break
			}
			select {
			case err = <-registered:
				_ = blocker.Rollback()
				t.Fatalf("registration returned before reaching its locked publication row: %v", err)
			case <-waitCtx.Done():
				_ = blocker.Rollback()
				t.Fatal("registration never reached its publication-row wait")
			case <-ticker.C:
			}
		}

		withdrawCtx, cancelWithdraw := context.WithTimeout(ctx, 150*time.Millisecond)
		_, withdrawErr := db.WithdrawDispatch(withdrawCtx, p.Repository, "pause", "operator")
		cancelWithdraw()
		if withdrawErr == nil {
			_ = blocker.Rollback()
			<-registered
			t.Fatal("withdrawal closed a gate already owned by registration")
		}
		if !errors.Is(withdrawErr, context.DeadlineExceeded) && !errors.Is(withdrawErr, context.Canceled) {
			_ = blocker.Rollback()
			<-registered
			t.Fatalf("withdrawal did not wait on the registration-owned gate: %v", withdrawErr)
		}
		open, revision, _, err := db.DispatchState(ctx, p.Repository)
		if err != nil || !open || revision != 0 {
			_ = blocker.Rollback()
			<-registered
			t.Fatalf("timed-out withdrawal changed the open gate: open=%v revision=%d err=%v", open, revision, err)
		}
		if err = blocker.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-registered; err != nil {
			t.Fatalf("registered correction did not commit: %v", err)
		}
		if _, err = db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
			t.Fatalf("withdraw after registration commit: %v", err)
		}
		pending, err := db.OutstandingPublications(ctx, p.Repository, 10)
		if err != nil || len(pending) != 1 || len(pending[0].Corrections) != 1 {
			t.Fatalf("registered correction escaped recovery after ordered withdrawal: %+v %v", pending, err)
		}
	})
}

func recordPublishedCorrectionFixture(t *testing.T, db *Store, ctx context.Context) factory.Publication {
	t.Helper()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("record initial publication: %v", err)
	}
	p.NativeRev, p.ObservedUnix, p.Comparison = 9, 1150, strings.Repeat("c", 40)
	publish := publishedStoreOperation(p, factory.OpRefPublish, 1, "branch-receipt")
	publish.Effect, publish.Completion = factory.OpEffectPending, factory.OpCompletionPending
	publish.Receipt = ""
	p.Publish = publish
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("register initial branch intent: %v", err)
	}
	p.Publish.Effect, p.Publish.Completion, p.Publish.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, "branch-receipt"
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record initial branch receipt: %v", err)
	}
	pr := publishedStoreOperation(p, factory.OpPRCreate, 1, `{"pr_number":9}`)
	pr.Effect, pr.Completion, pr.Receipt = factory.OpEffectPending, factory.OpCompletionPending, ""
	pr.PRNumber, pr.PRID, pr.IssueID, pr.HeadRef, pr.BaseRef, pr.HeadOID, pr.BaseOID = 0, 0, 0, "", "", "", ""
	p.PRCreate = pr
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("register initial PR intent: %v", err)
	}
	p.PRCreate.Effect, p.PRCreate.Completion, p.PRCreate.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, `{"pr_number":9}`
	p.PRNumber, p.PRID = 9, 8
	p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 9, 8, p.Issue
	p.PRCreate.HeadRef, p.PRCreate.BaseRef = factory.PublishBranchName(p.AssignmentID), p.TargetBranch
	p.PRCreate.HeadOID, p.PRCreate.BaseOID = p.Candidate, p.Comparison
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, 1200
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record linked PR: %v", err)
	}
	// Publication registration owns its own gate lock. Remove the dispatch
	// row created by the assignment fixture to exercise the logical default-
	// open case before either correction registration or withdrawal.
	if _, err := db.db.ExecContext(ctx, `DELETE FROM factory_dispatch WHERE repository=$1`, p.Repository); err != nil {
		t.Fatalf("reset dispatch gate fixture: %v", err)
	}
	return p
}

func publishedStoreOperation(p factory.Publication, kind string, ordinal int, receipt string) factory.PublicationOperation {
	id := factory.PublicationOperationID(p.ID, kind, ordinal)
	intent := publicationStoreIntent(id)
	intent.TargetBranch, intent.ComparisonRef, intent.ComparisonOID = p.TargetBranch, p.TargetBranch, p.Comparison
	intent.Candidate, intent.ExpectedOld = p.Candidate, "absent"
	if kind == factory.OpPRCreate {
		intent.ExpectedOld = p.Candidate
	}
	return factory.PublicationOperation{
		Work: intent, OperationID: id, Kind: kind, Effect: factory.OpEffectCommitted,
		Completion: factory.OpCompletionComplete, Receipt: receipt, Attempts: 1, UpdatedUnix: 1150,
	}
}

func appendPendingCorrection(p *factory.Publication) {
	id := factory.PublicationOperationID(p.ID, factory.OpRefPublish, len(p.Corrections)+2)
	intent := publicationStoreIntent(id)
	intent.TargetBranch, intent.ComparisonRef, intent.ComparisonOID = p.TargetBranch, p.TargetBranch, p.Comparison
	intent.Candidate, intent.ExpectedOld = strings.Repeat("d", 40), p.Candidate
	intent.CorrectionNumber, intent.CorrectionAuthor = p.PRNumber, p.PRCreate.Work.ActorID
	p.Corrections = append(p.Corrections, factory.PublicationOperation{Work: intent, RunID: p.Run, OperationID: id, Kind: factory.OpRefPublish, Attempts: 1, UpdatedUnix: 1250})
	p.Revision++
}

func TestPublicationCorrectionRunIDIsImmutable(t *testing.T) {
	old := factory.PublicationOperation{
		RunID: "0123456789abcdef0123456789abcdef", Kind: factory.OpRefPublish,
		OperationID: "correction-op", Attempts: 1,
	}
	next := old
	next.RunID = "1123456789abcdef0123456789abcdef"
	if publicationOperationUpdateAllowed(old, next) {
		t.Fatal("registered correction RunID changed during update")
	}
}

func publicationStoreIntent(id string) *factory.PublicationIntent {
	return &factory.PublicationIntent{
		OperationID: id, AuthRevision: "accepted-revision", Candidate: strings.Repeat("2", 40),
		TargetBranch: "refs/heads/main", ExpectedOld: "absent", ComparisonRef: "refs/heads/main", ComparisonOID: strings.Repeat("1", 40),
		PRTitle: "Factory candidate for #3", Repository: 7, ActorID: 5, NativeRev: 9, NotAfter: time.Now().Add(10 * time.Minute).Unix(),
	}
}

func seedPublicationAssignment(t *testing.T, db *Store, p factory.Publication) {
	t.Helper()
	a, reservation, run, view := dispatchTestPacket(t, time.Now())
	a.ID, a.Run, a.RunHistory = p.AssignmentID, p.Run, []string{p.Run}
	a.AttemptRoot, a.PublicationAssignment = a.ID, a.ID
	a.Repository, a.Issue, a.ProjectID, a.Acceptance, a.Authority = p.Repository, p.Issue, p.ProjectID, p.Acceptance, p.Authority
	a.Preparation, a.SourceCommit = p.Preparation, p.BaseSHA
	a.Subject.Issue.Issue, a.Subject.Issue.Acceptance = p.Issue, p.Acceptance
	reservation.AssignmentID, reservation.Repository = a.ID, a.Repository
	run.ID, run.ProjectID = a.Run, a.ProjectID
	run.InputSHA = a.SourceCommit
	view.RunID, view.Attempt, view.Repository, view.Issue = a.Run, a.ID, a.Repository, a.Issue
	if err := recordDispatchTestPacket(t, context.Background(), db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
}
