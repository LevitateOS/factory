package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/project"
)

func grantProjectFixture(t *testing.T, db *Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertUser(ctx, User{ID: 7, Login: "soda-tester"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProject(ctx, Project{ID: id, Name: "repo", RepositoryID: 42, OwnerID: 7, Repository: "alice/repo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentGrantIsCAS(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	grant := project.EnvironmentGrant{Repository: 42, Owner: 7, Profile: &project.Profile{
		ID: project.RockyHeadless, Distribution: "rocky", Version: "9.6", Interface: "headless",
		Architecture: "amd64", Image: "sha256:" + strings.Repeat("b", 64), Revision: strings.Repeat("c", 40),
	}, Active: true}
	if err := db.SaveEnvironmentGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEnvironmentGrant(ctx, grant); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale environment grant saved", err)
	}
	stored, err := db.EnvironmentGrant(ctx, 42)
	if err != nil || stored.Revision != 1 || !stored.Active {
		t.Fatalf("grant: %+v %v", stored, err)
	}
	if _, err = db.EnvironmentGrant(ctx, 43); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing grant reported", err)
	}
}

func TestRequirementDecisionChain(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	projectID := "p123456789012345678901234"
	grantProjectFixture(t, db, projectID)
	digest := strings.Repeat("d", 64)
	first := project.RequirementDecision{
		ID: "d123456789012345678901234", Project: projectID,
		Approver: 7, SourceCommit: strings.Repeat("e", 40), SetupDigest: digest, InputsDigest: digest,
	}
	if err := db.AdmitRequirementDecision(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitRequirementDecision(ctx, first); err != nil {
		t.Fatal("identical decision refused", err)
	}
	head, err := db.RequirementHead(ctx, projectID)
	if err != nil || head != first.ID {
		t.Fatalf("head: %q %v", head, err)
	}
	changed := first
	changed.SetupDigest = strings.Repeat("f", 64)
	if err = db.AdmitRequirementDecision(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed decision reused its identity", err)
	}
	second := first
	second.ID = "d223456789012345678901234"
	if err = db.AdmitRequirementDecision(ctx, second); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("decision skipped its predecessor", err)
	}
	second.Predecessor = first.ID
	if err = db.AdmitRequirementDecision(ctx, second); err != nil {
		t.Fatal(err)
	}
	depth, err := db.RequirementDepth(ctx, projectID)
	if err != nil || depth != 2 {
		t.Fatalf("depth: %d %v", depth, err)
	}
}

func TestApprovalDecisionChain(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	projectID := "p123456789012345678901234"
	grantProjectFixture(t, db, projectID)
	digest := strings.Repeat("d", 64)
	approval := project.ApprovalDecision{
		ID: "d323456789012345678901234", Project: projectID,
		Requirement: "d123456789012345678901234", Approver: 7,
		EffectsDigest: digest, ReadinessDigest: digest, Verified: true,
	}
	if err := db.AdmitApprovalDecision(ctx, approval); err != nil {
		t.Fatal(err)
	}
	head, err := db.ApprovalHead(ctx, projectID)
	if err != nil || head != approval.ID {
		t.Fatalf("head: %q %v", head, err)
	}
	if _, err = db.ApprovalHead(ctx, "p999999999999999999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing approval head reported", err)
	}
}
