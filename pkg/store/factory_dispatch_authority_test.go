package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func TestRecordDispatchPacketRejectsStalePolicyAuthority(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	a, reservation, run, view := dispatchTestPacket(t, now)
	a.Authority = dispatchCurrentAuthority(t, db, a)

	// Change one member after the dispatch tuple has been captured.
	policy, err := db.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	policy.Paused = true
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	err = recordDispatchTestPacket(t, ctx, db, a, reservation, run, view)
	if !errors.Is(err, ErrAdmissionChanged) {
		t.Fatalf("stale policy packet error = %v, want %v", err, ErrAdmissionChanged)
	}
	assertDispatchPacketAbsent(t, db, a)
}

func TestRecordDispatchPacketRejectsStaleAuthorityMembers(t *testing.T) {
	db := dispatchStoreFixture(t)
	mutations := []struct {
		name   string
		mutate func(*testing.T, *Store, factory.Assignment) func()
	}{
		{"enabled policy revision", func(t *testing.T, db *Store, a factory.Assignment) func() {
			policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SaveRepositoryPolicy(context.Background(), policy); err != nil {
				t.Fatal(err)
			}
			return func() {
				policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.SaveRepositoryPolicy(context.Background(), policy); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"operator revision", func(t *testing.T, db *Store, a factory.Assignment) func() {
			grant, err := db.OperatorGrant(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.SaveOperatorGrant(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			return func() {
				grant, err := db.OperatorGrant(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.SaveOperatorGrant(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"capacity revision", func(t *testing.T, db *Store, _ factory.Assignment) func() {
			capacity, err := db.Capacity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			capacity.MaxConcurrentRuns++
			if err := db.SaveCapacity(context.Background(), capacity); err != nil {
				t.Fatal(err)
			}
			return func() {
				capacity, err := db.Capacity(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				capacity.MaxConcurrentRuns = 2
				if err := db.SaveCapacity(context.Background(), capacity); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"sponsorship revision", func(t *testing.T, db *Store, a factory.Assignment) func() {
			grant, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
			if err != nil {
				t.Fatal(err)
			}
			grant.AllowanceMinutes--
			if err := db.SaveSponsorship(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			return func() {
				grant, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
				if err != nil {
					t.Fatal(err)
				}
				grant.AllowanceMinutes = 120
				if err := db.SaveSponsorship(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"environment revision", func(t *testing.T, db *Store, a factory.Assignment) func() {
			grant, err := db.EnvironmentGrant(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			grant.Profile.Version = "9.7"
			if err := db.SaveEnvironmentGrant(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			return func() {
				grant, err := db.EnvironmentGrant(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				grant.Profile.Version = "9.6"
				if err := db.SaveEnvironmentGrant(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"requirement head", func(t *testing.T, db *Store, a factory.Assignment) func() {
			if err := db.AdmitRequirementDecision(context.Background(), project.RequirementDecision{
				ID: "d" + strings.Repeat("f", 24), Predecessor: dispatchTestRequirementID(),
				Project: a.ProjectID, Approver: 7, SourceCommit: strings.Repeat("2", 40),
				SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64),
			}); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}},
		{"approval head", func(t *testing.T, db *Store, a factory.Assignment) func() {
			requirement, err := db.RequirementHead(context.Background(), a.ProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AdmitApprovalDecision(context.Background(), project.ApprovalDecision{
				ID: "d" + strings.Repeat("1", 24), Predecessor: dispatchTestApprovalID(),
				Project: a.ProjectID, Requirement: requirement, Approver: 7,
				EffectsDigest: strings.Repeat("a", 64), ReadinessDigest: strings.Repeat("b", 64), Verified: true,
			}); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}},
	}

	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			a, reservation, run, view := dispatchTestPacket(t, time.Now())
			a.Authority = dispatchCurrentAuthority(t, db, a)
			restore := tc.mutate(t, db, a)
			t.Cleanup(restore)
			err := recordDispatchTestPacket(t, context.Background(), db, a, reservation, run, view)
			if !errors.Is(err, ErrAdmissionChanged) {
				t.Fatalf("stale %s error = %v", tc.name, err)
			}
			assertDispatchPacketAbsent(t, db, a)
		})
	}
}

func TestRecordDispatchPacketRequiresEffectiveAuthority(t *testing.T) {
	db := dispatchStoreFixture(t)
	mutations := []struct {
		name   string
		mutate func(*testing.T, *Store, *factory.Assignment) func()
	}{
		{"paused policy", func(t *testing.T, db *Store, a *factory.Assignment) func() {
			policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			policy.Paused = true
			if err := db.SaveRepositoryPolicy(context.Background(), policy); err != nil {
				t.Fatal(err)
			}
			a.Authority = dispatchCurrentAuthority(t, db, *a)
			return func() {
				policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				policy.Paused = false
				if err := db.SaveRepositoryPolicy(context.Background(), policy); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"disabled policy", func(t *testing.T, db *Store, a *factory.Assignment) func() {
			policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			policy.Enabled = false
			if err := db.SaveRepositoryPolicy(context.Background(), policy); err != nil {
				t.Fatal(err)
			}
			a.Authority = dispatchCurrentAuthority(t, db, *a)
			return func() {
				policy, err := db.RepositoryPolicy(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				policy.Enabled = true
				if err := db.SaveRepositoryPolicy(context.Background(), policy); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"withdrawn operator", func(t *testing.T, db *Store, a *factory.Assignment) func() {
			grant, err := db.OperatorGrant(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			grant.Active = false
			if err := db.SaveOperatorGrant(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			a.Authority = dispatchCurrentAuthority(t, db, *a)
			return func() {
				grant, err := db.OperatorGrant(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				grant.Active = true
				if err := db.SaveOperatorGrant(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"withdrawn sponsorship", func(t *testing.T, db *Store, a *factory.Assignment) func() {
			grant, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
			if err != nil {
				t.Fatal(err)
			}
			grant.Active = false
			if err := db.SaveSponsorship(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			a.Authority = dispatchCurrentAuthority(t, db, *a)
			return func() {
				grant, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
				if err != nil {
					t.Fatal(err)
				}
				grant.Active = true
				if err := db.SaveSponsorship(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"sponsorship without coder", func(t *testing.T, db *Store, a *factory.Assignment) func() {
			grant, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
			if err != nil {
				t.Fatal(err)
			}
			grant.Roles = []string{project.RoleReviewer}
			if err := db.SaveSponsorship(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			a.Authority = dispatchCurrentAuthority(t, db, *a)
			return func() {
				grant, err := db.Sponsorship(context.Background(), a.Repository, a.Connection)
				if err != nil {
					t.Fatal(err)
				}
				grant.Roles = []string{project.RoleCoder}
				if err := db.SaveSponsorship(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"withdrawn environment", func(t *testing.T, db *Store, a *factory.Assignment) func() {
			grant, err := db.EnvironmentGrant(context.Background(), a.Repository)
			if err != nil {
				t.Fatal(err)
			}
			grant.Active = false
			if err := db.SaveEnvironmentGrant(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			a.Authority = dispatchCurrentAuthority(t, db, *a)
			return func() {
				grant, err := db.EnvironmentGrant(context.Background(), a.Repository)
				if err != nil {
					t.Fatal(err)
				}
				grant.Active = true
				if err := db.SaveEnvironmentGrant(context.Background(), grant); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"assignment authority mismatch", func(_ *testing.T, _ *Store, a *factory.Assignment) func() {
			a.Authority.Policy++
			return func() {}
		}},
	}

	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			a, reservation, run, view := dispatchTestPacket(t, time.Now())
			a.Authority = dispatchCurrentAuthority(t, db, a)
			restore := tc.mutate(t, db, &a)
			t.Cleanup(restore)
			err := recordDispatchTestPacket(t, context.Background(), db, a, reservation, run, view)
			if !errors.Is(err, ErrAdmissionChanged) {
				t.Fatalf("ineffective/mismatched authority error = %v", err)
			}
			assertDispatchPacketAbsent(t, db, a)
		})
	}
}

func TestRecordDispatchPacketRejectsMissingAuthorityRows(t *testing.T) {
	deletions := []struct {
		name, query string
		key         string
	}{
		{"environment grant", `DELETE FROM project_environment_grants WHERE repository=$1`, "repository"},
		{"requirement head", `DELETE FROM project_requirement_heads WHERE project_id=$1`, "project_id"},
		{"approval head", `DELETE FROM project_approval_heads WHERE project_id=$1`, "project_id"},
	}
	for _, tc := range deletions {
		t.Run(tc.name, func(t *testing.T) {
			db := dispatchStoreFixture(t)
			a, reservation, run, view := dispatchTestPacket(t, time.Now())
			a.Authority = dispatchCurrentAuthority(t, db, a)
			value := any(a.ProjectID)
			if tc.key == "repository" {
				value = a.Repository
			}
			if _, err := db.db.ExecContext(context.Background(), tc.query, value); err != nil {
				t.Fatal(err)
			}
			err := recordDispatchTestPacket(t, context.Background(), db, a, reservation, run, view)
			if !errors.Is(err, ErrAdmissionChanged) {
				t.Fatalf("missing %s error = %v", tc.name, err)
			}
			assertDispatchPacketAbsent(t, db, a)
		})
	}
}
