package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/levitateos/sodaos/internal/project"
)

func TestMemberAccessHoldRestoreAndExplicitRejoin(t *testing.T) {
	ctx := t.Context()
	s, dsn := postgresFixture(t)
	for _, u := range []User{{ID: 1, Login: "alice"}, {ID: 2, Login: "bob"}} {
		if err := s.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	const pid = "p0123456789abcdef01234567"
	if err := s.CreateProject(ctx, Project{ID: pid, Name: "Access", RepositoryID: 42, OwnerID: 1, Repository: "alice/access"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(ctx, pid, 1, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(ctx, pid, 2, "bob"); err != nil {
		t.Fatal(err)
	}
	revocation, err := s.BeginMemberAccess(ctx, pid, 2, 0, "revoke")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MemberLogin(ctx, pid, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("held login admitted: %v", err)
	}
	if err = s.Join(ctx, pid, 2, "bob"); !errors.Is(err, ErrProjectAccessHeld) {
		t.Fatalf("Join bypassed hold: %v", err)
	}
	if _, err = s.BeginMemberAccess(ctx, pid, 2, 0, "revoke"); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale revocation: %v", err)
	}
	if _, err = s.BeginMemberAccess(ctx, pid, 2, revocation.Revision, "restore"); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("unconfirmed native effects restored: %v", err)
	}
	// A lost response retries the retained operation rather than minting a
	// new revision or clearing its admission hold, including after reconnect.
	s.Close()
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retry, err := s.BeginMemberAccess(ctx, pid, 2, revocation.Revision, "revoke")
	if err != nil || retry.Revision != revocation.Revision {
		t.Fatalf("revocation retry: %+v %v", retry, err)
	}
	native := project.MemberAccessState{Project: pid, Login: "bob", Identity: 2, Revision: retry.Revision, Held: true, LoginDisabled: true, KeysRemoved: true, ProcessesStopped: true, Keys: []string{}, Processes: []project.MemberProcess{}}
	if err = s.FinishMemberAccess(ctx, pid, 2, retry, native); err != nil {
		t.Fatal(err)
	}
	restored, err := s.BeginMemberAccess(ctx, pid, 2, retry.Revision, "restore")
	if err != nil {
		t.Fatal(err)
	}
	native.Revision = restored.Revision
	native.Held = false
	native.RequiresJoin = true
	if err = s.FinishMemberAccess(ctx, pid, 2, restored, native); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MemberLogin(ctx, pid, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore silently rejoined: %v", err)
	}
	if login, err := s.MemberIdentity(ctx, pid, 2); err != nil || login != "bob" {
		t.Fatalf("retained identity: %s %v", login, err)
	}
	if err = s.Join(ctx, pid, 2, "renamed"); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stable login remapped: %v", err)
	}
	if err = s.Join(ctx, pid, 2, "bob"); err != nil {
		t.Fatal(err)
	}
	if login, err := s.MemberLogin(ctx, pid, 2); err != nil || login != "bob" {
		t.Fatalf("explicit rejoin: %s %v", login, err)
	}
	if login, err := s.MemberLogin(ctx, pid, 1); err != nil || login != "alice" {
		t.Fatalf("other member affected: %s %v", login, err)
	}
}

func TestMembersListingIsBounded(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	if err := s.UpsertUser(ctx, User{ID: 1, Login: "member-000"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, Project{ID: "p123", Name: "Demo", RepositoryID: 42, OwnerID: 1, Repository: "alice/demo"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 135; i++ {
		login := fmt.Sprintf("member-%03d", i)
		if err := s.UpsertUser(ctx, User{ID: int64(i + 1), Login: login}); err != nil {
			t.Fatal(err)
		}
		if err := s.Join(ctx, "p123", int64(i+1), login); err != nil {
			t.Fatal(err)
		}
	}
	members, err := s.Members(ctx, "p123")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 129 {
		t.Fatalf("members listing returned %d rows, want the bounded 129", len(members))
	}
}

func TestPersistenceAndMembership(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	if err := s.UpsertUser(ctx, User{ID: 1, Login: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, Project{ID: "p123", Name: "Demo", RepositoryID: 42, OwnerID: 1, Repository: "alice/demo"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project(ctx, "p123")
	if err != nil || p.Ready {
		t.Fatalf("project: %+v %v", p, err)
	}
	if err = s.Join(ctx, "p123", 1, "alice"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if login, err := s.MemberLogin(ctx, "p123", 1); err != nil || login != "alice" {
		t.Fatalf("%q %v", login, err)
	}
}

// TestSetupSocketDSNParses pins the setup-generated unix-socket DSN shape:
// the appliance driver must resolve the socket directory, role and database
// without a TCP host. Parsing needs no server.
func TestSetupSocketDSNParses(t *testing.T) {
	dsn := "postgres://soda:" + strings.Repeat("a", 64) + "@/soda?host=/run/soda/postgres&sslmode=disable"
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "/run/soda/postgres" {
		t.Fatalf("host %q, want socket directory", cfg.Host)
	}
	if cfg.User != "soda" || cfg.Database != "soda" {
		t.Fatalf("user %q database %q", cfg.User, cfg.Database)
	}
	if cfg.Password != strings.Repeat("a", 64) {
		t.Fatal("password did not survive the socket DSN")
	}
}
