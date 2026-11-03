package control

import (
	"context"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

type fakeMerger struct {
	ledger  map[string]factory.OperationOutcome
	observe func(factory.MergeWork) (factory.MergeObservation, error)
	submit  func(factory.MergeWork) (factory.OperationOutcome, error)
	lookup  func(string) (factory.OperationOutcome, error)
	cancel  func(string) (factory.OperationOutcome, error)
	adopt   func(factory.MergeWork, factory.OperationOutcome) (factory.MergeOutcome, error)
	confirm func(factory.MergeWork) (factory.MergeConfirmation, error)

	observes []factory.MergeWork
	submits  []factory.MergeWork
	lookups  []string
	cancels  []string
	confirms []factory.MergeWork
}

func (f *fakeMerger) ObserveMerge(ctx context.Context, work factory.MergeWork) (factory.MergeObservation, error) {
	f.observes = append(f.observes, work)
	return f.observe(work)
}

func (f *fakeMerger) SubmitMerge(ctx context.Context, work factory.MergeWork) (factory.OperationOutcome, error) {
	f.submits = append(f.submits, work)
	outcome, err := f.submit(work)
	if err == nil && !outcome.NotObserved {
		outcome.OperationID, outcome.InstallationID, outcome.Kind = work.OperationID, "test-installation", factory.OpMerge
		outcome.ActorID, outcome.RepositoryID = work.ActorID, work.Repository
		f.ledger[work.OperationID] = outcome
	}
	return outcome, err
}

func (f *fakeMerger) LookupOp(ctx context.Context, operationID string) (factory.OperationOutcome, error) {
	f.lookups = append(f.lookups, operationID)
	if f.lookup != nil {
		return f.lookup(operationID)
	}
	outcome, ok := f.ledger[operationID]
	if !ok {
		return factory.OperationOutcome{NotObserved: true}, nil
	}
	return outcome, nil
}

func (f *fakeMerger) CancelOp(ctx context.Context, operationID string) (factory.OperationOutcome, error) {
	f.cancels = append(f.cancels, operationID)
	if f.cancel != nil {
		return f.cancel(operationID)
	}
	outcome := f.ledger[operationID]
	if outcome.Effect == factory.OpEffectCommitted {
		outcome.Cancellation = factory.OpCancelTooLate
	} else {
		outcome.Effect, outcome.Cancellation, outcome.Reason = factory.OpEffectNotCommitted, factory.OpCancelCancelled, "cancelled_before_admission"
	}
	outcome.OperationID, outcome.InstallationID = operationID, "test-installation"
	f.ledger[operationID] = outcome
	return outcome, nil
}

func (f *fakeMerger) AdoptMerge(_ context.Context, work factory.MergeWork, outcome factory.OperationOutcome) (factory.MergeOutcome, error) {
	return f.adopt(work, outcome)
}

func (f *fakeMerger) ObserveCompletion(ctx context.Context, work factory.MergeWork) (factory.MergeConfirmation, error) {
	f.confirms = append(f.confirms, work)
	return f.confirm(work)
}

func happyMerger() *fakeMerger {
	return &fakeMerger{
		ledger: map[string]factory.OperationOutcome{},
		observe: func(w factory.MergeWork) (factory.MergeObservation, error) {
			return factory.MergeObservation{
				Checks: factory.ObservedChecks{
					Checks:    []factory.ObservedCheck{{Context: "ci", State: factory.CheckStateSuccess}},
					NativeRev: 9, ObservedContexts: 1, HeadTip: w.HeadOID, BaseTip: w.BaseOID, Complete: true,
				},
				NativeRev: 9, ObservedUnix: time.Now().Unix(),
				PRID: w.PRID, PRNumber: w.PRNumber, IssueID: w.IssueID, PRAuthorID: w.PRAuthorID,
				HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID,
				ReviewerID: w.ReviewerID, ReviewID: 31,
			}, nil
		},
		submit: func(w factory.MergeWork) (factory.OperationOutcome, error) {
			return committedOutcome(`{"pr_number":9}`), nil
		},
		adopt: func(w factory.MergeWork, o factory.OperationOutcome) (factory.MergeOutcome, error) {
			return factory.MergeOutcome{
				Operation: o, HeadRef: w.HeadRef, BaseRef: w.BaseRef, HeadOID: w.HeadOID, BaseOID: w.BaseOID,
				MergedCommit: w.HeadOID, PRNumber: w.PRNumber, PRID: w.PRID, IssueID: w.IssueID, ActorID: w.ActorID,
			}, nil
		},
		confirm: func(w factory.MergeWork) (factory.MergeConfirmation, error) {
			now := time.Now().Unix()
			return factory.MergeConfirmation{MergedCommit: w.HeadOID, BaseTip: w.HeadOID, MergerID: w.ActorID, MergedUnix: now, ClosedUnix: now, NativeRev: 9, ObservedUnix: now, IssueClosed: true, OriginalClosed: true, OriginalClosingOccurrence: "501", OriginalClosedUnix: now}, nil
		},
	}
}

type mergeFixture struct {
	publish *publishFixture
	exec    *fakeMerger
}

func mergeSeed(t *testing.T, issue int64, sourceInputs ...map[string]string) (*mergeFixture, factory.Publication) {
	t.Helper()
	fx := publishSeed(t, sourceInputs...)
	ctx := context.Background()
	policy, err := fx.db.RepositoryPolicy(ctx, fx.seed.repo)
	if err != nil {
		t.Fatal(err)
	}
	// The merge reviewer must be independent of the PR author.
	policy.Review = factory.ActorBindingRef{TokenID: 2, ActorID: 6, Kind: factory.OpReviewSubmit}
	if err := fx.db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	a := fx.finishReported(t, issue)
	fx.wire(happyPublisher())
	fx.pass(t)
	p, err := fx.db.PublicationByAssignment(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Stage != factory.PublicationPublished {
		t.Fatalf("merge seed publication not published: %+v", p)
	}
	current, err := fx.db.RepositoryPolicy(ctx, fx.seed.repo)
	if err != nil {
		t.Fatal(err)
	}
	assessment := factory.CheckAssessment{
		Results: []factory.CheckResult{{Context: "ci", State: factory.CheckStateSuccess, Passed: true}},
		Checks:  []string{"ci"}, Repository: p.Repository, PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID,
		PolicyRevision: current.Revision, NativeRev: 9, AssessedUnix: time.Now().Unix(), ObservedContexts: 1,
		HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef, HeadOID: p.PRCreate.HeadOID, BaseOID: p.PRCreate.BaseOID,
		ChecksDigest: factory.ChecksDigest([]string{"ci"}), Verdict: factory.CheckPass, Reason: factory.CheckReasonPass,
	}
	if _, err := fx.db.RecordCheckAssessment(ctx, assessment); err != nil {
		t.Fatal(err)
	}
	return &mergeFixture{publish: fx}, p
}

func (fx *mergeFixture) wire(exec *fakeMerger) {
	fx.exec = exec
	fx.publish.coord.Merges = exec
}

func (fx *mergeFixture) pass(t *testing.T) MergeReport {
	t.Helper()
	report := fx.publish.coord.MergePass(context.Background())
	if len(report.Errors) != 0 {
		t.Fatalf("merge pass: %+v", report)
	}
	return report
}

func (fx *mergeFixture) merge(t *testing.T, p factory.Publication) factory.Merge {
	t.Helper()
	m, err := fx.publish.db.MergeByPublication(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
