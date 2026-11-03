package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestPublicationRegistrationPersistsImmutableIntent(t *testing.T) {
	db, ctx := publicationStoreFixture(t), context.Background()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Publish = factory.PublicationOperation{Work: publicationStoreIntent("publish-op"), OperationID: "publish-op", Kind: factory.OpRefPublish, Attempts: 1, UpdatedUnix: 1200}
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*factory.Publication)
	}{
		{"deadline", func(p *factory.Publication) { p.Publish.Work.NotAfter++ }},
		{"revision", func(p *factory.Publication) { p.Publish.Work.NativeRev++ }},
		{"authority", func(p *factory.Publication) { p.Publish.Work.AuthRevision = "replacement" }},
		{"identity", func(p *factory.Publication) {
			p.Publish.Work.OperationID = "replacement"
			p.Publish.OperationID = "replacement"
		}},
		{"candidate", func(p *factory.Publication) {
			p.Candidate = strings.Repeat("3", 40)
			p.Publish.Work.Candidate = p.Candidate
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			next, err := db.PublicationByAssignment(ctx, p.AssignmentID)
			if err != nil {
				t.Fatal(err)
			}
			next.Revision++
			change.mutate(&next)
			if err = db.UpdatePublication(ctx, next); !errors.Is(err, ErrPublicationConflict) {
				t.Fatalf("immutable change: %v", err)
			}
		})
	}
	// A lost submit reply remains unresolved under this same identity.
	got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Publish.Effect != "" || *got.Publish.Work != *p.Publish.Work {
		t.Fatalf("intent changed: %+v", got.Publish)
	}
}

func TestWithdrawalOrdersPublicationRegistrationAndKeepsReconciliation(t *testing.T) {
	db, ctx := publicationStoreFixture(t), context.Background()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Publish = factory.PublicationOperation{Work: publicationStoreIntent("publish-op"), OperationID: "publish-op", Kind: factory.OpRefPublish, Attempts: 1, UpdatedUnix: 1200}
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
		t.Fatal(err)
	}
	// Replies to registered work must remain recordable while dispatch is shut.
	p.WithdrawRequested = true
	p.Publish.Effect = factory.OpEffectCommitted
	p.Publish.Completion = factory.OpCompletionComplete
	p.Publish.Receipt = "branch-receipt"
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("reconcile after withdrawal: %v", err)
	}
	next := p
	next.PRCreate = factory.PublicationOperation{Work: publicationStoreIntent("pr-op"), OperationID: "pr-op", Kind: factory.OpPRCreate, Attempts: 1, UpdatedUnix: 1250}
	next.Revision++
	if err := db.UpdatePublication(ctx, next); !errors.Is(err, ErrDispatchClosed) {
		t.Fatalf("new operation escaped withdrawal: %v", err)
	}
	late := publicationTestRecord()
	if err := db.RecordPublication(ctx, late); err != nil {
		t.Fatal(err)
	}
	late.Publish = p.Publish
	late.Revision++
	if err := db.UpdatePublication(ctx, late); !errors.Is(err, ErrDispatchClosed) {
		t.Fatalf("new registration after gate close: %v", err)
	}
}

func TestPublicationReceiptAndTerminalOutcomeCannotBeReplaced(t *testing.T) {
	db, ctx := publicationStoreFixture(t), context.Background()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Publish = factory.PublicationOperation{Work: publicationStoreIntent("publish-op"), OperationID: "publish-op", Kind: factory.OpRefPublish, Attempts: 1, UpdatedUnix: 1200, Effect: factory.OpEffectNotCommitted, Receipt: "refusal"}
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Publish.OperationID = "replacement"
	intent := *p.Publish.Work
	intent.OperationID = p.Publish.OperationID
	p.Publish.Work = &intent
	p.Revision++
	if err := db.UpdatePublication(ctx, p); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("old verdict moved to new identity: %v", err)
	}
}

func TestPublicationRegistrationRejectsAuthorityChangesAfterPreflight(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(context.Context, *Store, factory.Publication) error
	}{
		{"acceptance withdrawn", func(ctx context.Context, db *Store, p factory.Publication) error {
			return db.WithdrawAcceptanceDecision(ctx, p.Repository, p.Issue, p.Acceptance, 7)
		}},
		{"policy", func(ctx context.Context, db *Store, p factory.Publication) error {
			v, err := db.RepositoryPolicy(ctx, p.Repository)
			if err != nil {
				return err
			}
			v.Paused = true
			return db.SaveRepositoryPolicy(ctx, v)
		}},
		{"operator", func(ctx context.Context, db *Store, p factory.Publication) error {
			v, err := db.OperatorGrant(ctx, p.Repository)
			if err != nil {
				return err
			}
			v.Active = false
			return db.SaveOperatorGrant(ctx, v)
		}},
		{"environment", func(ctx context.Context, db *Store, p factory.Publication) error {
			v, err := db.EnvironmentGrant(ctx, p.Repository)
			if err != nil {
				return err
			}
			v.Active = false
			return db.SaveEnvironmentGrant(ctx, v)
		}},
		{"sponsorship", func(ctx context.Context, db *Store, p factory.Publication) error {
			v, err := db.Sponsorship(ctx, p.Repository, "conn")
			if err != nil {
				return err
			}
			v.Active = false
			return db.SaveSponsorship(ctx, v)
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			db, ctx := publicationStoreFixture(t), context.Background()
			p := publicationTestRecord()
			seedPublicationAssignment(t, db, p)
			if err := db.RecordPublication(ctx, p); err != nil {
				t.Fatal(err)
			}
			p.Publish = factory.PublicationOperation{Work: publicationStoreIntent("publish-op"), OperationID: "publish-op", Kind: factory.OpRefPublish, Attempts: 1, UpdatedUnix: 1200}
			p.Revision++
			if err := change.mutate(ctx, db, p); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdatePublication(ctx, p); !errors.Is(err, ErrStaleRevision) {
				t.Fatalf("operation escaped changed authority: %v", err)
			}
			got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Publish.Attempts != 0 {
				t.Fatal("rejected operation was persisted")
			}
		})
	}
}
