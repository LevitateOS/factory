package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// The database is dropped after the test; the returned DSN permits reopen
// checks. Supply soda-pg-fixture's SODA_PG_* inputs or soda-native-pg-check's
// SODA_NATIVE_PG_DSN_FILE; absent fixture inputs skip the test.
func postgresFixture(t *testing.T) (*Store, string) {
	t.Helper()
	admin, ok := TestFixtureDSN()
	if !ok {
		t.Skip("SODA_PG_* fixture unavailable")
	}
	s, dsn, cleanup, err := OpenEphemeral(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return s, dsn
}

// Journal upgrade fixtures start from a current private database. Versions before
// 81 had neither dispatch_state nor the guard that references it. Restore both
// before applying their historical payload rule; Open must perform the real
// dispatch migration, preserving every retained command and immutable guard.
func restorePreDispatchCommandFixture(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) {
	t.Helper()
	for _, statement := range []string{
		`CREATE OR REPLACE FUNCTION soda_guard_command() RETURNS trigger AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.type IS DISTINCT FROM OLD.type
    OR NEW.target IS DISTINCT FROM OLD.target OR NEW.principal IS DISTINCT FROM OLD.principal
    OR NEW.digest IS DISTINCT FROM OLD.digest OR NEW.payload IS DISTINCT FROM OLD.payload
    OR NEW.created IS DISTINCT FROM OLD.created
    OR (OLD.finished!='' AND (NEW.finished IS DISTINCT FROM OLD.finished OR NEW.outcome IS DISTINCT FROM OLD.outcome))
  THEN
    RAISE EXCEPTION 'factory command is immutable';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql`,
		`ALTER TABLE factory_commands DROP COLUMN dispatch_state`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal("restore pre-dispatch command fixture", err)
		}
	}
}

// Storage preserves provider preparation as opaque bytes. Provider codecs and
// authenticated receipts are exercised by their native integration checks.
func opaqueHumanPreparationFixture(t *testing.T, size int) []byte {
	t.Helper()
	prefix, suffix := "{ \"opaque\":\"", "\\n完整 🌋\" }\n "
	raw := []byte(prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix)
	if len(raw) != size || !json.Valid(raw) {
		t.Fatal("invalid opaque preparation fixture")
	}
	return raw
}

type nativePostgresCheck struct {
	name, input, phase string
}

func resolveNativePostgresCheck(t *testing.T) nativePostgresCheck {
	t.Helper()
	selected := nativePostgresCheck{
		name:  os.Getenv("SODA_NATIVE_PG_CASE"),
		input: os.Getenv("SODA_NATIVE_PG_CASE_INPUT"),
		phase: os.Getenv("SODA_NATIVE_PG_PHASE"),
	}
	switch selected.phase {
	case "write", "restart", "recover":
	default:
		t.Fatalf("unknown native PostgreSQL phase %q", selected.phase)
	}
	cases := []struct {
		name, legacy string
		input        bool
	}{
		{"transaction", "", false},
		{"collaboration-bindings", "SODA_NATIVE_COLLABORATION_BINDINGS", false},
		{"human-pull-create-observations", "SODA_NATIVE_HUMAN_PULL_CREATE_OBSERVATIONS", true},
		{"human-pull-create-journal", "SODA_NATIVE_HUMAN_PULL_CREATE_JOURNAL", false},
		{"human-pull-create-schema", "SODA_NATIVE_HUMAN_PULL_CREATE_SCHEMA_FILE", true},
		{"human-merge-observations", "SODA_NATIVE_HUMAN_MERGE_OBSERVATIONS", false},
		{"human-merge-schema", "SODA_NATIVE_HUMAN_MERGE_SCHEMA_FILE", true},
		{"human-merge-journal", "SODA_NATIVE_HUMAN_MERGE_JOURNAL", false},
		{"human-review-request-observations", "SODA_NATIVE_HUMAN_REVIEW_REQUEST_OBSERVATIONS", false},
		{"human-review-request-journal", "SODA_NATIVE_HUMAN_REVIEW_REQUEST_JOURNAL", false},
		{"human-review-request-schema", "SODA_NATIVE_HUMAN_REVIEW_REQUEST_SCHEMA_FILE", true},
		{"collaboration-teams", "SODA_NATIVE_COLLABORATION_TEAMS", false},
		{"human-thread-schema", "SODA_NATIVE_HUMAN_THREAD_SCHEMA_FILE", true},
		{"human-thread-journal", "SODA_NATIVE_HUMAN_THREAD_JOURNAL", false},
		{"human-inline-schema", "SODA_NATIVE_HUMAN_INLINE_SCHEMA_FILE", true},
		{"human-inline-journal", "SODA_NATIVE_HUMAN_INLINE_JOURNAL", false},
		{"human-review-schema", "SODA_NATIVE_HUMAN_REVIEW_SCHEMA_FILE", true},
		{"human-review-journal", "SODA_NATIVE_HUMAN_REVIEW_JOURNAL", false},
		{"human-state-journal", "SODA_NATIVE_HUMAN_STATE_JOURNAL", false},
		{"human-lock-journal", "SODA_NATIVE_HUMAN_LOCK_JOURNAL", false},
		{"human-issue-create-journal", "SODA_NATIVE_HUMAN_ISSUE_CREATE_JOURNAL", false},
		{"human-edit-journal", "SODA_NATIVE_HUMAN_EDIT_JOURNAL", false},
		{"human-comment-journal", "SODA_NATIVE_HUMAN_COMMENT_JOURNAL", false},
	}
	// Resolve historical environment callers once. Ambiguous runs must fail
	// instead of silently proving only the first selected case.
	for _, candidate := range cases {
		if candidate.legacy == "" {
			continue
		}
		value := os.Getenv(candidate.legacy)
		if candidate.input && value != "" || !candidate.input && value == "1" {
			if selected.name != "" {
				t.Fatalf("multiple native PostgreSQL cases selected: %q and %q", selected.name, candidate.name)
			}
			if selected.input != "" {
				t.Fatal("native PostgreSQL case input requires an explicit case")
			}
			selected.name = candidate.name
			if candidate.input {
				selected.input = value
			}
		}
	}
	if selected.name == "" {
		selected.name = "transaction"
	}
	for _, candidate := range cases {
		if selected.name == candidate.name {
			if candidate.input != (selected.input != "") {
				t.Fatalf("native PostgreSQL case %q requires input=%t", selected.name, candidate.input)
			}
			return selected
		}
	}
	t.Fatalf("unknown native PostgreSQL case %q", selected.name)
	return selected
}

// Driven by soda-native-pg-check across three actual server lifetimes. Each
// phase opens the same private native cluster, rather than cloning a fixture DB.
func TestNativePostgresRetainedTransaction(t *testing.T) {
	file := os.Getenv("SODA_NATIVE_PG_DSN_FILE")
	if file == "" {
		t.Skip("native PostgreSQL driver unavailable")
	}
	selected := resolveNativePostgresCheck(t)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	var listen, fsync, synchronous, sharedMemory string
	if err := s.db.QueryRowContext(ctx, `SELECT current_setting('listen_addresses'), current_setting('fsync'), current_setting('synchronous_commit'), current_setting('dynamic_shared_memory_type')`).Scan(&listen, &fsync, &synchronous, &sharedMemory); err != nil {
		t.Fatal(err)
	}
	if listen != "" || fsync != "on" || synchronous != "on" || sharedMemory != "mmap" {
		t.Fatalf("native durability/listener settings: listen=%q fsync=%q synchronous=%q shared_memory=%q", listen, fsync, synchronous, sharedMemory)
	}
	// The socket is private and a wrong password must still be refused.
	uri, err := url.Parse(string(raw))
	if err != nil {
		t.Fatal("invalid native connection URI")
	}
	query := uri.Query()
	query.Set("password", "wrong")
	uri.RawQuery = query.Encode()
	unauthorized, err := sql.Open("pgx", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err = unauthorized.PingContext(probeCtx)
	cancel()
	unauthorized.Close()
	if err == nil {
		t.Fatal("native socket admitted an incorrect password")
	}
	phase := selected.phase
	identityFile := os.Getenv("SODA_NATIVE_PG_POST_ID_FILE")
	switch selected.name {
	case "collaboration-bindings":
		checkNativeCollaborationBindings(t, s, string(raw), phase, identityFile+".bindings.json")
		return
	case "human-pull-create-observations":
		checkNativeHumanPullCreateObservations(t, s, phase, selected.input, identityFile+".pull-create-observations.json")
		return
	case "human-pull-create-journal":
		checkNativeHumanPullCreateJournal(t, s, string(raw), phase, identityFile+".pull-create.json")
		return
	case "human-merge-observations":
		checkNativeHumanMergeObservations(t, s, phase, identityFile+".merge-native", identityFile+".merge-observations.json")
		return
	case "human-merge-journal":
		checkNativeHumanMergeJournal(t, s, string(raw), phase, identityFile+".merge.json")
		return
	case "human-review-request-observations":
		checkNativeHumanReviewRequestObservations(t, s, phase, identityFile+".reviewer-observations.json")
		return
	case "human-review-request-journal":
		checkNativeHumanReviewRequestJournal(t, s, string(raw), phase, identityFile+".reviewer.json")
		return
	case "collaboration-teams":
		checkNativeCollaborationTeams(t, s, string(raw), phase, identityFile+".teams.json")
		return
	case "human-thread-journal":
		checkNativeHumanThreadJournal(t, s, string(raw), phase, identityFile+".thread.json")
		return
	case "human-inline-journal":
		checkNativeHumanInlineJournal(t, s, string(raw), phase, identityFile+".inline.json")
		return
	case "human-review-journal":
		checkNativeHumanReviewJournal(t, s, string(raw), phase, identityFile+".review.json")
		return
	case "human-state-journal":
		checkNativeHumanStateJournal(t, s, phase, identityFile+".state.json")
		return
	case "human-lock-journal":
		checkNativeHumanLockJournal(t, s, string(raw), phase, identityFile+".lock.json")
		return
	case "human-issue-create-journal":
		checkNativeHumanIssueCreateJournal(t, s, string(raw), phase, identityFile+".issue-create.json")
		return
	case "human-edit-journal":
		checkNativeHumanEditJournal(t, s, string(raw), phase, identityFile+".edit.json")
		return
	case "human-comment-journal":
		checkNativeHumanCommentJournal(t, s, string(raw), phase, identityFile+".comment.json")
		return
	case "human-pull-create-schema", "human-merge-schema", "human-review-request-schema", "human-thread-schema", "human-inline-schema", "human-review-schema":
		checkNativeHumanReviewDevelopmentSchema(t, s, string(raw), selected.input, selected.name)
		return
	}
	var id string
	if phase == "write" {
		id, _, err = s.SaveFeedPost(ctx, factory.PostRequest{Repository: 7, Body: "original", IdempotencyKey: "native-retained"}, "42", "Native person", 1729000000)
		if err == nil {
			err = s.RecordPostIntake(ctx, id, 0)
		}
		if err == nil {
			_, err = s.EditFeedPost(ctx, id, "42", "retained native transaction", 0, 1729000001)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(identityFile, []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		stored, err := os.ReadFile(identityFile)
		if err != nil {
			t.Fatal(err)
		}
		id = string(stored)
	}
	wantRevision, wantBody := int64(1), "retained native transaction"
	if phase == "recover" {
		wantRevision, wantBody = 2, "committed before crash"
	} else if phase != "write" && phase != "restart" {
		t.Fatal("unknown native check phase")
	}
	post, err := s.FeedPost(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if post.Record.Revision != wantRevision || post.Body != wantBody || pipe.Revision != wantRevision {
		t.Fatalf("%s lost the atomic post/intake revision: post=%d body=%q intake=%d", phase, post.Record.Revision, post.Body, pipe.Revision)
	}
	if _, err = s.EditFeedPost(ctx, id, "42", "stale overwrite", 0, 1729000002); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale edit admitted: %v", err)
	}
	replay, revision, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 7, Body: "duplicate", IdempotencyKey: "native-retained"}, "42", "Native person", 1729000002)
	if err != nil || replay != id || revision != wantRevision {
		t.Fatalf("%s duplicated the durable command: %v", phase, err)
	}
	if phase == "restart" {
		if _, err := s.EditFeedPost(ctx, id, "42", "committed before crash", 1, 1729000003); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%s verified native post/intake transaction and idempotent command", phase)
	checkNativePostRetryReceipt(t, s, phase, identityFile+".retry.json")
}

// The existing retained native transaction drives this through actual graceful
// restart and abrupt WAL recovery. Inputs are explicitly declared observations;
// this does not impersonate provider verification or current permissions.
func checkNativeCollaborationBindings(t *testing.T, s *Store, dsn, phase, file string) {
	t.Helper()
	ctx := t.Context()
	type proof struct {
		Binding    collaboration.ConnectionBinding
		Other      collaboration.ConnectionBinding
		Repository collaboration.RepositoryRef
	}
	var retained proof
	if phase != "write" {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &retained); err != nil {
			t.Fatal(err)
		}
		for _, binding := range []collaboration.ConnectionBinding{retained.Binding, retained.Other} {
			observed := collaboration.ConnectionObservation{Profile: binding.Profile, Provider: binding.Host.Provider,
				APIBase: binding.Host.APIBase, Connection: binding.Connection.ID, Account: binding.Actor.ExternalID, Login: binding.Actor.Login}
			if got, err := s.BindCollaborationConnection(ctx, observed); err != nil || got != binding {
				t.Fatalf("retained reconnect changed binding: %+v %v", got, err)
			}
		}
		if got, err := s.CollaborationRepository(ctx, retained.Repository.ID); err != nil || got != retained.Repository {
			t.Fatalf("retained repository changed: %+v %v", got, err)
		}
		var links int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_repository_connections WHERE repository=$1`, retained.Repository.ID).Scan(&links); err != nil || links != 2 {
			t.Fatalf("retained repository account associations: %d %v", links, err)
		}
		t.Log("canonical account/repository IDs and associations survived", phase)
		return
	}
	observed := collaboration.ConnectionObservation{
		Profile:  collaboration.Profile{ID: collaboration.ProfileID(collaboration.NewID()), Name: "Native profile"},
		Provider: collaboration.GitHub, APIBase: "https://API.GITHUB.COM:443/", Connection: collaboration.ConnectionID(collaboration.NewID()),
		Account: "9007199254740993", Login: "observed-account",
	}
	// A late DB failure must roll back the initial adoption and all partial rows.
	for _, ddl := range []string{
		`CREATE FUNCTION binding_fixture_refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'declared late failure'; END $$`,
		`CREATE TRIGGER binding_fixture_refuse BEFORE INSERT ON collaboration_connections FOR EACH ROW EXECUTE FUNCTION binding_fixture_refuse()`,
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.BindCollaborationConnection(ctx, observed); !errors.Is(err, collaboration.ErrIdentityConflict) || got != (collaboration.ConnectionBinding{}) {
		t.Fatalf("late failed bind returned partial identity: %+v %v", got, err)
	}
	var adopted bool
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT adopted FROM collaboration_ledger WHERE singleton`).Scan(&adopted); err != nil || adopted {
		t.Fatalf("failed bind adopted ledger: %v %v", adopted, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM collaboration_hosts)+(SELECT count(*) FROM collaboration_profiles)+(SELECT count(*) FROM collaboration_actors)+(SELECT count(*) FROM collaboration_connections)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed bind left partial rows: %d %v", count, err)
	}
	for _, ddl := range []string{`DROP TRIGGER binding_fixture_refuse ON collaboration_connections`, `DROP FUNCTION binding_fixture_refuse()`} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	otherOwner, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer otherOwner.Close()
	type result struct {
		binding collaboration.ConnectionBinding
		err     error
	}
	results := make(chan result, 8)
	for i := range 8 {
		owner := s
		if i%2 != 0 {
			owner = otherOwner
		}
		go func() { b, err := owner.BindCollaborationConnection(ctx, observed); results <- result{b, err} }()
	}
	for i := range 8 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if i == 0 {
			retained.Binding = r.binding
		} else if r.binding != retained.Binding {
			t.Fatal("concurrent first binding forked canonical IDs")
		}
	}
	binding := retained.Binding
	if binding.Host.APIBase != "https://api.github.com" || binding.Actor.ExternalID != observed.Account || binding.Actor.ID < 1 || binding.Connection.Actor != binding.Actor.ID {
		t.Fatal("binding lost canonical/provider identity distinction")
	}
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM collaboration_hosts)+(SELECT count(*) FROM collaboration_profiles)+(SELECT count(*) FROM collaboration_actors)+(SELECT count(*) FROM collaboration_connections)`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("concurrent binding left extras: %d %v", count, err)
	}
	// Same account reconnects and renames; preserve a locally edited profile name.
	profile := binding.Profile
	profile.Name = "Locally renamed profile"
	if err := s.PutCollaborationProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	observed.Profile.Name = "Connector default"
	observed.Login = "renamed-account"
	renamed, err := s.BindCollaborationConnection(ctx, observed)
	if err != nil || renamed.Profile != profile || renamed.Actor.ID != binding.Actor.ID || renamed.Connection != binding.Connection {
		t.Fatalf("rename replaced canonical scope: %+v %v", renamed, err)
	}
	observed.Login = ""
	if got, err := s.BindCollaborationConnection(ctx, observed); err != nil || got != renamed {
		t.Fatalf("identity-only reconnect lost metadata: %+v %v", got, err)
	}
	retained.Binding = renamed
	// A new native credential connection for the same provider account shares
	// actor/repository identities, without merging distinct connection ownership.
	second := observed
	second.Connection = collaboration.ConnectionID(collaboration.NewID())
	secondBinding, err := s.BindCollaborationConnection(ctx, second)
	if err != nil || secondBinding.Actor != renamed.Actor || secondBinding.Host != renamed.Host || secondBinding.Connection.ID == renamed.Connection.ID {
		t.Fatalf("reauthentication forked account: %+v %v", secondBinding, err)
	}
	// Provider collisions never alias remote IDs or display names.
	foreign := observed
	foreign.Provider = collaboration.Fountain
	foreign.APIBase = "https://forge.example.test"
	foreign.Connection = collaboration.ConnectionID(collaboration.NewID())
	retained.Other, err = s.BindCollaborationConnection(ctx, foreign)
	if err != nil || retained.Other.Host.ID == renamed.Host.ID || retained.Other.Actor.ID == renamed.Actor.ID {
		t.Fatalf("provider identity collision: %+v %v", retained.Other, err)
	}
	for _, change := range []string{"profile", "account", "provider", "locator"} {
		wrong := observed
		wrong.Login = "must-roll-back"
		switch change {
		case "profile":
			wrong.Profile.ID = collaboration.ProfileID(collaboration.NewID())
		case "account":
			wrong.Account = "other-account"
		case "provider":
			wrong.Provider = collaboration.Fountain
		case "locator":
			wrong.APIBase = "https://other-api.example.test"
		}
		if got, err := s.BindCollaborationConnection(ctx, wrong); !errors.Is(err, collaboration.ErrIdentityConflict) || got != (collaboration.ConnectionBinding{}) {
			t.Fatalf("connection rebound %s: %+v %v", change, got, err)
		}
	}
	if got, err := s.CollaborationActor(ctx, renamed.Actor.ID); err != nil || got != renamed.Actor {
		t.Fatalf("failed bind changed display metadata: %+v %v", got, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM collaboration_hosts)+(SELECT count(*) FROM collaboration_profiles)+(SELECT count(*) FROM collaboration_actors)+(SELECT count(*) FROM collaboration_connections)`).Scan(&count); err != nil || count != 8 {
		t.Fatalf("refused scope changes left partial identities: %d %v", count, err)
	}
	type repoResult struct {
		ref collaboration.RepositoryRef
		err error
	}
	repos := make(chan repoResult, 8)
	for i := range 8 {
		owner := s
		if i%2 != 0 {
			owner = otherOwner
		}
		go func() {
			r, err := owner.ObserveCollaborationConnectionRepository(ctx, renamed.Connection, "9007199254740995", "owner/repo")
			repos <- repoResult{r, err}
		}()
	}
	for i := range 8 {
		r := <-repos
		if r.err != nil {
			t.Fatal(r.err)
		}
		if i == 0 {
			retained.Repository = r.ref
		} else if r.ref != retained.Repository {
			t.Fatal("concurrent repository observation forked canonical IDs")
		}
	}
	repo, err := s.ObserveCollaborationConnectionRepository(ctx, secondBinding.Connection, retained.Repository.ExternalID, "new-owner/renamed")
	if err != nil || repo.ID != retained.Repository.ID {
		t.Fatalf("repository rename/second account changed ID: %+v %v", repo, err)
	}
	retained.Repository = repo
	if got, err := s.ObserveCollaborationConnectionRepository(ctx, renamed.Connection, repo.ExternalID, ""); err != nil || got != repo {
		t.Fatalf("identity-only repo observation lost name: %+v %v", got, err)
	}
	wrong := renamed.Connection
	wrong.Actor = retained.Other.Actor.ID
	if got, err := s.ObserveCollaborationConnectionRepository(ctx, wrong, "not-observed", "bad/repo"); !errors.Is(err, collaboration.ErrIdentityConflict) || got != (collaboration.RepositoryRef{}) {
		t.Fatalf("foreign connection scope observed repo: %+v %v", got, err)
	}
	for _, ddl := range []string{
		`CREATE FUNCTION binding_fixture_refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'declared association failure'; END $$`,
		`CREATE TRIGGER binding_fixture_refuse BEFORE INSERT ON collaboration_repository_connections FOR EACH ROW EXECUTE FUNCTION binding_fixture_refuse()`,
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, external := range []string{repo.ExternalID, "not-observed"} {
		if got, err := s.ObserveCollaborationConnectionRepository(ctx, renamed.Connection, external, "must-roll-back"); !errors.Is(err, collaboration.ErrIdentityConflict) || got != (collaboration.RepositoryRef{}) {
			t.Fatalf("failed association returned partial repo: %+v %v", got, err)
		}
	}
	for _, ddl := range []string{`DROP TRIGGER binding_fixture_refuse ON collaboration_repository_connections`, `DROP FUNCTION binding_fixture_refuse()`} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.CollaborationRepository(ctx, repo.ID); err != nil || got != repo {
		t.Fatalf("failed association changed repo metadata: %+v %v", got, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_repositories`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed association left orphan repo: %d %v", count, err)
	}
	// An already-running old ledger writer must finish before the empty native
	// composition can decide whether initialization is safe.
	legacy, _, cleanup, err := OpenEphemeral(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	tx, err := legacy.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,login) VALUES(42,'retained-person')`); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	waiting := make(chan result, 1)
	go func() { b, err := legacy.BindCollaborationConnection(waitCtx, observed); waiting <- result{b, err} }()
	blocked := false
	for range 200 {
		if err := legacy.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='relation' AND relation='users'::regclass AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("empty composition did not wait for retained writer")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r := <-waiting
	if !errors.Is(r.err, collaboration.ErrUnboundLedger) || r.binding != (collaboration.ConnectionBinding{}) {
		t.Fatalf("claimed concurrently retained ledger: %+v %v", r.binding, r.err)
	}
	if got, err := legacy.User(ctx, 42); err != nil || got.Login != "retained-person" {
		t.Fatalf("retained data changed: %+v %v", got, err)
	}
	if err := legacy.db.QueryRowContext(ctx, `SELECT adopted FROM collaboration_ledger WHERE singleton`).Scan(&adopted); err != nil || adopted {
		t.Fatalf("retained refusal adopted ledger: %v %v", adopted, err)
	}
	if err := legacy.db.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_hosts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retained refusal left partial host: %d %v", count, err)
	}
	raw, err := json.Marshal(retained)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Log("atomic account/repository mappings, concurrency, rename, rollback, scope and retained-writer refusal passed")
}

// Exercise the existing command journal on the native server across independent
// Go processes, a retained schema upgrade, clean restart and real WAL recovery.
func checkNativeHumanCommentJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved factory.HumanCommentCapture
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		// Keep an existing immutable settings command through the historical
		// command-shape and payload-bound upgrade.
		legacy := factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained", Principal: "42", Payload: `{"retained":"原文"}`}
		legacy.Digest = factory.SettingsDigest(legacy.Type, legacy.Target, legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = recordFactoryCommandTx(ctx, tx, legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		restorePreDispatchCommandFixture(t, tx)
		for _, statement := range []string{
			`ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check`,
			`ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK(char_length(payload)<=8192)`,
			`UPDATE schema_version SET version=70`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-70 upgrade", err)
		}
		defer s.Close()
		retained, err := s.FactoryCommand(ctx, legacy.ID)
		if err != nil || retained.Payload != legacy.Payload || retained.Digest != legacy.Digest {
			t.Fatal("upgrade changed retained command", err)
		}
		var version int
		if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("upgrade marker", version, err)
		}
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "7", "team/repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "42", "human")
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("<", factory.MaxHumanCommentBytes-len("🧋\n")) + "🧋\n"
		encodedBody, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		saved = factory.HumanCommentCapture{
			Request:   factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: body},
			Authority: "installation-retained", OperationID: "comment-operation-retained",
			ProviderIntent: append(append([]byte("{ \"body\": "), encodedBody...), []byte(", \"native_revision\": 1, \"not_after\": 1 }\n")...),
		}
		// Registered identity numbers on the wrong host are never admitted.
		unbound := saved
		unbound.Request.ID = factory.NewID()
		unbound.Request.Host = collaboration.HostID(collaboration.NewID())
		if _, _, err := s.CaptureHumanComment(ctx, unbound); !errors.Is(err, collaboration.ErrUnboundLedger) {
			t.Fatal("foreign host admitted", err)
		}
		if _, err := s.FactoryCommand(ctx, unbound.Request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("refused capture left a command", err)
		}
		got, replay, err := s.CaptureHumanComment(ctx, saved)
		if err != nil || replay || got.Request != saved.Request || !bytes.Equal(got.ProviderIntent, saved.ProviderIntent) {
			t.Fatal("initial capture", replay, err)
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(identityFile, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		// SQL itself preserves the narrow limit and immutable original payload.
		cmd, err := saved.Command()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE factory_commands SET payload=payload||' ' WHERE id=$1`, cmd.ID); err == nil {
			t.Fatal("captured payload was mutable")
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,'retry','x','42','x',$2,'now')`, factory.NewID(), strings.Repeat("x", 8193)); err == nil {
			t.Fatal("other command SQL bound was widened")
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,'discussion.comment.create','x','42','x',$2,'now')`, factory.NewID(), strings.Repeat("x", factory.MaxHumanCommentCaptureBytes+1)); err == nil {
			t.Fatal("comment SQL bound was absent")
		}
		// Actual concurrent insert conflict must return the winner's preparation.
		left, right := saved, saved
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-left", "race-right"
		type result struct {
			capture factory.HumanCommentCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, capture := range []factory.HumanCommentCapture{left, right} {
			go func() { c, replay, err := s.CaptureHumanComment(ctx, capture); results <- result{c, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || a.capture.OperationID != b.capture.OperationID || a.replay == b.replay {
			t.Fatal("concurrent capture did not retain one preparation", a.err, b.err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained capture unavailable", err)
		}
	}
	got, err := s.HumanCommentCapture(ctx, saved.Request)
	if err != nil || got.Request != saved.Request || got.Authority != saved.Authority || got.OperationID != saved.OperationID || !bytes.Equal(got.ProviderIntent, saved.ProviderIntent) {
		t.Fatal("retained exact preparation differs", err)
	}
	// Expired or freshly changed preparation is NOT substituted during replay.
	fresh := saved
	fresh.OperationID, fresh.Authority, fresh.ProviderIntent = "replacement", "replacement", []byte(`{"native_revision":999,"not_after":999}`)
	got, replay, err := s.CaptureHumanComment(ctx, fresh)
	if err != nil || !replay || got.OperationID != saved.OperationID || got.Authority != saved.Authority || !bytes.Equal(got.ProviderIntent, saved.ProviderIntent) {
		t.Fatal("replay prepared a different effect", err)
	}
	for _, change := range []func(*factory.HumanCommentCommand){
		func(q *factory.HumanCommentCommand) { q.Body += "changed"; q.Body = q.Body[16:] },
		func(q *factory.HumanCommentCommand) { q.Number++ },
		func(q *factory.HumanCommentCommand) { q.Subject = "issue" },
		func(q *factory.HumanCommentCommand) { q.Actor++ },
		func(q *factory.HumanCommentCommand) { q.Repository++ },
		func(q *factory.HumanCommentCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
	} {
		q := saved.Request
		change(&q)
		if _, err := s.HumanCommentCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed request replay admitted", err)
		}
	}
	changed := saved
	changed.Request.Body = "changed"
	if _, _, err := s.CaptureHumanComment(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed capture admitted", err)
	}
	cmd, err := s.FactoryCommand(ctx, saved.Request.ID)
	if err != nil || cmd.Finished != "" || cmd.Outcome != "" {
		t.Fatal("capture invented provider completion", err)
	}
	t.Logf("%s: exact 64 KiB Markdown/provider bytes, canonical binding, original preparation replay and immutable command retained; no provider dispatch", phase)
}

// Provider JSON is opaque fixture preparation; no provider operation is sent.
func checkNativeHumanReviewDevelopmentSchema(t *testing.T, s *Store, dsn, path, selectedCase string) {
	t.Helper()
	ctx := t.Context()
	var pull, merge, reviewer, thread, inline bool
	switch selectedCase {
	case "human-pull-create-schema":
		pull = true
		fallthrough
	case "human-merge-schema":
		merge = true
		fallthrough
	case "human-review-request-schema":
		reviewer = true
		fallthrough
	case "human-thread-schema":
		thread = true
		fallthrough
	case "human-inline-schema":
		inline = true
	}
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The generated development-copy SQL must refuse the actual native database.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, string(script)); err == nil {
		t.Fatal("development SQL admitted native database")
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatal("development SQL changed native marker", version, err)
	}
	// This database belongs only to the driver's new private cluster.
	if _, err := s.db.ExecContext(ctx, "CREATE DATABASE soda_dev"); err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid private native DSN")
	}
	uri.Path = "/soda_dev"
	development, err := Open(uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer development.Close()
	id := factory.NewID()
	retained := strings.Repeat("Retained original 完整 🌋\r\n", 1000)
	if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'retained','420','retained',$3,'now')", id, factory.CommandHumanComment, retained); err != nil {
		t.Fatal(err)
	}
	previousDDL := []string{
		"ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check",
		"ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply') AND char_length(payload)<=8192))",
		"UPDATE schema_version SET version=74",
	}
	if inline {
		previousDDL = HumanReviewJournalUpgradeDDL
	}
	if thread {
		previousDDL = HumanInlineJournalUpgradeDDL
	}
	if reviewer {
		previousDDL = HumanThreadJournalUpgradeDDL
	}
	if merge {
		previousDDL = HumanReviewRequestJournalUpgradeDDL
	}
	if pull {
		previousDDL = HumanMergeJournalUpgradeDDL
	}
	restorePreDispatchCommandFixture(t, development.db)
	for _, statement := range previousDDL {
		if _, err := development.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := development.db.ExecContext(ctx, string(script)); err != nil {
			t.Fatal("generated development schema-74 upgrade/replay", err)
		}
	}
	if err := development.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatal("development marker", version, err)
	}
	var body string
	if err := development.db.QueryRowContext(ctx, "SELECT payload FROM factory_commands WHERE id=$1", id).Scan(&body); err != nil || body != retained {
		t.Fatal("development upgrade changed original prose", err)
	}
	if merge {
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'merge','420','merge',$3,'now')", factory.NewID(), factory.CommandHumanMerge, strings.Repeat("x", factory.MaxHumanMergeCaptureBytes)); err != nil {
			t.Fatal("development merge allowance missing", err)
		}
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'merge','420','merge',$3,'now')", factory.NewID(), factory.CommandHumanMerge, strings.Repeat("x", factory.MaxHumanMergeCaptureBytes+1)); err == nil {
			t.Fatal("development merge limit missing")
		}
	}
	if pull {
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'pulls','420','pulls',$3,'now')", factory.NewID(), factory.CommandHumanPullCreate, strings.Repeat("x", factory.MaxHumanPullCreateCaptureBytes)); err != nil {
			t.Fatal("development pull creation allowance missing", err)
		}
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'pulls','420','pulls',$3,'now')", factory.NewID(), factory.CommandHumanPullCreate, strings.Repeat("x", factory.MaxHumanPullCreateCaptureBytes+1)); err == nil {
			t.Fatal("development pull creation limit missing")
		}
	}
	if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'review','420','review',$3,'now')", factory.NewID(), factory.CommandHumanReview, strings.Repeat("x", 8193)); err != nil {
		t.Fatal("development review allowance missing", err)
	}
	if inline {
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'inline','420','inline',$3,'now')", factory.NewID(), factory.CommandHumanInline, strings.Repeat("x", 8193)); err != nil {
			t.Fatal("development inline allowance missing", err)
		}
	}
	if thread {
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'thread','420','thread',$3,'now')", factory.NewID(), factory.CommandHumanThread, strings.Repeat("x", 8193)); err != nil {
			t.Fatal("development thread allowance missing", err)
		}
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'thread','420','thread',$3,'now')", factory.NewID(), factory.CommandHumanThread, strings.Repeat("x", factory.MaxHumanThreadCaptureBytes+1)); err == nil {
			t.Fatal("development thread limit missing")
		}
	}
	if reviewer {
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'reviewer','420','reviewer',$3,'now')", factory.NewID(), factory.CommandHumanReviewRequest, strings.Repeat("x", 8193)); err != nil {
			t.Fatal("development reviewer allowance missing", err)
		}
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'reviewer','420','reviewer',$3,'now')", factory.NewID(), factory.CommandHumanReviewRequest, strings.Repeat("x", factory.MaxHumanReviewRequestCaptureBytes+1)); err == nil {
			t.Fatal("development reviewer limit missing")
		}
	}
	for _, entry := range []struct {
		kind  string
		limit int
	}{{factory.CommandRetry, 8192}, {factory.CommandHumanState, 8192}, {factory.CommandHumanReview, factory.MaxHumanReviewCaptureBytes}} {
		if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'limit','420','limit',$3,'now')", factory.NewID(), entry.kind, strings.Repeat("x", entry.limit+1)); err == nil {
			t.Fatal("development payload limit missing", entry.kind)
		}
	}
	if _, err := development.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'inline','420','inline',$3,'now')", factory.NewID(), factory.CommandHumanInline, strings.Repeat("x", factory.MaxHumanInlineCaptureBytes+1)); err == nil {
		t.Fatal("development inline limit missing")
	}
	if err := development.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := Open(uri.String())
	if err != nil {
		t.Fatal("development upgraded store reopen", err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("PASS: generated development-copy upgrade to schema %d, idempotent replay, retained original prose, bounded review/inline allowance and unchanged small-command limits; native database refused and unchanged; upgraded store reopens", SchemaVersion())
}

// Retain outcomes produced by the real admitted native adapter fixture. This
// phase performs no provider calls; it checks the canonical journal after actual
// PostgreSQL restart/WAL recovery and cannot repeat native SQL or notifications.
// Receipts come from actual admitted SDK calls in the paired native fixture.
// The fixture-only translation below is not a production provider adapter or
// receipt authenticator; canonical binding/state variants exercise real SQL.
func checkNativeHumanPullCreateObservations(t *testing.T, s *Store, phase, directory, path string) {
	t.Helper()
	ctx := t.Context()
	var saved []factory.Command
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('collaboration_actors','id'),9007199254740992,true)"); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 8 {
			t.Fatal("missing actual SDK pull creation observation matrix", len(entries), err)
		}
		for index, entry := range entries {
			var proof struct {
				CommandID, Repository, HeadRepository, Actor, BaseBranch, HeadBranch, Title, Body string
				AllowMaintainerEdit                                                               bool
				ProviderIntent                                                                    []byte
				Record                                                                            struct {
					InstallationID string          `json:"installation_id"`
					OperationID    string          `json:"operation_id"`
					Kind           string          `json:"kind"`
					Effect         string          `json:"effect_state"`
					Cancellation   string          `json:"cancellation_status"`
					Completion     string          `json:"completion_state"`
					Digest         string          `json:"intent_digest"`
					Reason         string          `json:"reason_code"`
					Receipt        json.RawMessage `json:"receipt"`
				}
			}
			raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &proof); err != nil {
				t.Fatal(err)
			}
			host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://pull-create-observation-" + strconv.Itoa(index) + ".example.test"}
			if _, err := s.PutCollaborationHost(ctx, host); err != nil {
				t.Fatal(err)
			}
			repo, err := s.ObserveCollaborationRepository(ctx, host.ID, proof.Repository, "fixture/project")
			if err != nil {
				t.Fatal(err)
			}
			head := repo
			if proof.HeadRepository != proof.Repository {
				head, err = s.ObserveCollaborationRepository(ctx, host.ID, proof.HeadRepository, "fixture/source")
				if err != nil {
					t.Fatal(err)
				}
			}
			actor, err := s.ObserveCollaborationActor(ctx, host.ID, proof.Actor, "operator")
			if err != nil {
				t.Fatal(err)
			}
			c := factory.HumanPullCreateCapture{Request: factory.HumanPullCreateCommand{ID: proof.CommandID, Host: host.ID, Repository: repo.ID, Actor: actor.ID, HeadRepository: head.ID, BaseBranch: proof.BaseBranch, HeadBranch: proof.HeadBranch, Title: proof.Title, Body: proof.Body, AllowMaintainerEdit: proof.AllowMaintainerEdit}, Authority: proof.Record.InstallationID, OperationID: proof.Record.OperationID, ProviderIntent: proof.ProviderIntent}
			c, replay, err := s.CaptureHumanPullCreate(ctx, c)
			if err != nil || replay {
				t.Fatal("actual pull creation preparation capture", err)
			}
			cmd, err := c.Command()
			if err != nil {
				t.Fatal(err)
			}
			observe := func(o collaboration.OperationLookup) collaboration.OperationLookup {
				t.Helper()
				got, err := s.ObserveHumanPullCreate(ctx, c, o)
				if err != nil {
					t.Fatal(entry.Name(), "pull creation observation", err)
				}
				return got
			}
			without := func(state collaboration.LookupState) collaboration.OperationLookup {
				return collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: state}
			}
			for _, state := range []collaboration.LookupState{collaboration.LookupNotObserved, collaboration.LookupUnknown, collaboration.LookupUnavailable} {
				observe(without(state))
			}
			binding := &collaboration.OperationBinding{Repository: repo.ID, Actor: actor.ID, Kind: collaboration.OpenPull, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}
			pending := collaboration.OperationRecord{Host: host.ID, Authority: c.Authority, OperationID: c.OperationID, Binding: binding, Effect: collaboration.EffectPending, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionPending}
			wrap := func(r collaboration.OperationRecord) collaboration.OperationLookup {
				return collaboration.OperationLookup{Host: host.ID, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
			}
			if proof.Record.Kind != "" {
				observe(wrap(pending))
				uncertain := pending
				uncertain.Effect = collaboration.EffectIndeterminate
				observe(wrap(uncertain))
				kept := observe(wrap(pending))
				if kept.Record.Effect != collaboration.EffectIndeterminate {
					t.Fatal("stale pending erased uncertainty")
				}
			}
			// This projection uses actual native result/receipt bytes. Pending and
			// conflicting variants above/below are explicit ordering fixtures.
			native := proof.Record
			actual := pending
			actual.Effect = collaboration.EffectState(native.Effect)
			actual.Cancellation = collaboration.CancellationState(native.Cancellation)
			actual.Reason = native.Reason
			actual.Completion = collaboration.CompletionState(native.Completion)
			if actual.Effect != collaboration.EffectCommitted {
				actual.Completion = collaboration.CompletionComplete
			}
			if native.Kind == "" {
				actual.Binding = nil
			} else if native.Kind != "pull_request.human_create.apply" {
				t.Fatal("foreign native result kind")
			}
			if len(native.Receipt) > 0 {
				actual.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: native.Digest, Payload: append(json.RawMessage(nil), native.Receipt...)}
			}
			beforeActual, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if actual.Receipt != nil {
				large := actual
				r := *large.Receipt
				large.Receipt = &r
				r.Payload = json.RawMessage(`{"ordering_fixture":"` + strings.Repeat("<", 12000) + `"}`)
				if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(large)); err == nil {
					t.Fatal("escaped outcome size exceeded journal bound")
				}
			}
			if index == 0 {
				if _, err := s.db.ExecContext(ctx, `CREATE FUNCTION fail_pull_create_observation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture pull creation observation rollback'; END $$`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_pull_create_observation BEFORE UPDATE OF outcome ON factory_commands FOR EACH ROW EXECUTE FUNCTION fail_pull_create_observation()`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(actual)); err == nil {
					t.Fatal("observation SQL failure ignored")
				}
				if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_pull_create_observation ON factory_commands"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, "DROP FUNCTION fail_pull_create_observation()"); err != nil {
					t.Fatal(err)
				}
			}
			unchanged, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil || unchanged != beforeActual {
				t.Fatal("refused outcome changed command", err)
			}
			changedCapture := c
			changedCapture.OperationID = "foreign-captured-operation"
			foreignCaptureObservation := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: changedCapture.OperationID, State: collaboration.LookupUnknown}
			if _, err := s.ObserveHumanPullCreate(ctx, changedCapture, foreignCaptureObservation); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("changed saved capture adopted", err)
			}
			final := observe(wrap(actual))
			if final.Record.Effect != actual.Effect || final.Record.Completion != actual.Completion {
				t.Fatal("native result did not advance")
			}
			before, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (before.Finished != "") != factory.HumanPullCreateFinished(final) {
				t.Fatal("merge finished before completion")
			}
			for _, state := range []collaboration.LookupState{collaboration.LookupUnknown, collaboration.LookupNotObserved, collaboration.LookupUnavailable} {
				observe(without(state))
			}
			if actual.Binding != nil {
				stale := pending
				observe(wrap(stale))
				if actual.Effect == collaboration.EffectCommitted && actual.Completion == collaboration.CompletionNeedsIntervention {
					stale = actual
					stale.Completion = collaboration.CompletionPending
					observe(wrap(stale))
				}
			}
			for _, mutate := range []func(*collaboration.OperationLookup){
				func(o *collaboration.OperationLookup) { o.Host = collaboration.HostID(collaboration.NewID()) },
				func(o *collaboration.OperationLookup) { o.Authority = "foreign-installation" },
				func(o *collaboration.OperationLookup) { o.OperationID = "foreign-operation" },
			} {
				bad := wrap(actual)
				mutate(&bad)
				if _, err := s.ObserveHumanPullCreate(ctx, c, bad); err == nil {
					t.Fatal("foreign observation accepted")
				}
			}
			if actual.Binding != nil {
				for _, field := range []string{"actor", "repository", "kind", "digest", "atomicity"} {
					bad := actual
					b := *bad.Binding
					bad.Binding = &b
					switch field {
					case "actor":
						b.Actor++
					case "repository":
						b.Repository++
					case "kind":
						b.Kind = collaboration.RequestReview
					case "digest":
						b.IntentDigest = strings.Repeat("b", 64)
					case "atomicity":
						b.Atomicity = collaboration.Ordered
					}
					if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(bad)); err == nil {
						t.Fatal("changed pull creation binding accepted", field)
					}
				}
			}
			for _, field := range []string{"partial", "checkpoint", "missing-receipt", "unexpected-receipt", "incomplete-no-effect", "unbound"} {
				bad := actual
				switch field {
				case "partial":
					bad.Effect = collaboration.EffectPartial
				case "checkpoint":
					bad.Checkpoints = []collaboration.EffectCheckpoint{{ID: "unexpected", Effect: collaboration.EffectCommitted}}
				case "missing-receipt":
					if actual.Effect != collaboration.EffectCommitted {
						continue
					}
					bad.Receipt = nil
				case "unexpected-receipt":
					if actual.Effect == collaboration.EffectCommitted {
						continue
					}
					bad.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: strings.Repeat("a", 64), Payload: json.RawMessage("{}")}
				case "incomplete-no-effect":
					if actual.Effect != collaboration.EffectNotCommitted {
						continue
					}
					bad.Completion = collaboration.CompletionPending
				case "unbound":
					bad.Binding = nil
					bad.Effect = collaboration.EffectPending
					bad.Cancellation = collaboration.CancellationPending
					bad.Completion = collaboration.CompletionPending
					bad.Receipt = nil
				}
				if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(bad)); err == nil {
					t.Fatal("malformed PR observation accepted", field)
				}
			}
			if actual.Receipt != nil {
				for _, field := range []string{"authority", "reference", "digest", "payload"} {
					bad := actual
					r := *bad.Receipt
					bad.Receipt = &r
					switch field {
					case "authority":
						r.Authority = "foreign"
					case "reference":
						r.Reference = "foreign"
					case "digest":
						r.IntentDigest = strings.Repeat("c", 64)
					case "payload":
						r.Payload = append(append(json.RawMessage(nil), r.Payload...), byte(' '))
					}
					if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(bad)); err == nil {
						t.Fatal("changed exact pull creation receipt accepted", field)
					}
				}
				contradiction := pending
				contradiction.Effect = collaboration.EffectNotCommitted
				contradiction.Completion = collaboration.CompletionComplete
				if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(contradiction)); err == nil {
					t.Fatal("contradictory terminal effect accepted")
				}
			} else if actual.Binding != nil {
				contradiction := pending
				contradiction.Effect = collaboration.EffectCommitted
				contradiction.Completion = collaboration.CompletionComplete
				contradiction.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: strings.Repeat("a", 64), Payload: json.RawMessage(`{"fixture":"conflict"}`)}
				if _, err := s.ObserveHumanPullCreate(ctx, c, wrap(contradiction)); err == nil {
					t.Fatal("refusal was overwritten by contradictory success")
				}
			}
			// The command row orders simultaneous stale and exact replies.
			failures := make(chan error, 2)
			for _, o := range []collaboration.OperationLookup{without(collaboration.LookupUnknown), wrap(actual)} {
				go func() { _, err := s.ObserveHumanPullCreate(ctx, c, o); failures <- err }()
			}
			if a, b := <-failures, <-failures; a != nil || b != nil {
				t.Fatal("concurrent observation", a, b)
			}
			after, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil || after != before {
				t.Fatal("stale/exact reply changed retained outcome", err)
			}
			saved = append(saved, after)
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
	}
	complete, pending, intervention, tombstones := 0, 0, 0, 0
	for _, want := range saved {
		got, err := s.FactoryCommand(ctx, want.ID)
		if err != nil || got != want {
			t.Fatal("retained pull creation command changed", phase, err)
		}
		c, err := factory.DecodeHumanPullCreateCapture(got)
		if err != nil {
			t.Fatal(err)
		}
		var o collaboration.OperationLookup
		if err := json.Unmarshal([]byte(got.Outcome), &o); err != nil || factory.ValidateHumanPullCreateObservation(c, o) != nil {
			t.Fatal("retained pull creation observation differs", err)
		}
		if (got.Finished != "") != factory.HumanPullCreateFinished(o) {
			t.Fatal("retained finish marker differs")
		}
		if got.Finished != "" {
			complete++
		}
		if o.Record.Completion == collaboration.CompletionPending {
			pending++
		}
		if o.Record.Completion == collaboration.CompletionNeedsIntervention {
			intervention++
		}
		if o.Record.Binding == nil {
			tombstones++
		}
		for _, next := range []collaboration.OperationLookup{o, {Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown}} {
			if _, err := s.ObserveHumanPullCreate(ctx, c, next); err != nil {
				t.Fatal("retained observation replay", phase, err)
			}
		}
		after, err := s.FactoryCommand(ctx, want.ID)
		if err != nil || after != want {
			t.Fatal("reopen replay changed exact pull creation command", phase, err)
		}
	}
	if len(saved) != 8 || complete != 6 || pending != 1 || intervention != 1 || tombstones != 1 {
		t.Fatal("incomplete real pull creation observation matrix", len(saved), complete, pending, intervention, tombstones)
	}
	t.Logf("PASS %s: %d actual native human PR creation outcomes, %d complete, %d pending, %d intervention, %d early cancellation; exact receipts/captures and monotonic SQL retained", phase, len(saved), complete, pending, intervention, tombstones)
}

func checkNativeHumanMergeObservations(t *testing.T, s *Store, phase, directory, path string) {
	t.Helper()
	ctx := t.Context()
	var saved []factory.Command
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('collaboration_actors','id'),9007199254740992,true)"); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 8 {
			t.Fatal("missing actual SDK merge observation matrix", len(entries), err)
		}
		for index, entry := range entries {
			var proof struct {
				CommandID, Method, Message, CommitOID, Number, Repository, Actor string
				ProviderIntent                                                   []byte
				Record                                                           struct {
					InstallationID string          `json:"installation_id"`
					OperationID    string          `json:"operation_id"`
					Kind           string          `json:"kind"`
					Effect         string          `json:"effect_state"`
					Cancellation   string          `json:"cancellation_status"`
					Completion     string          `json:"completion_state"`
					Digest         string          `json:"intent_digest"`
					Reason         string          `json:"reason_code"`
					Receipt        json.RawMessage `json:"receipt"`
				}
			}
			raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &proof); err != nil {
				t.Fatal(err)
			}
			host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://merge-observation-" + strconv.Itoa(index) + ".example.test"}
			if _, err := s.PutCollaborationHost(ctx, host); err != nil {
				t.Fatal(err)
			}
			repo, err := s.ObserveCollaborationRepository(ctx, host.ID, proof.Repository, "fixture/project")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := s.ObserveCollaborationActor(ctx, host.ID, proof.Actor, "operator")
			if err != nil {
				t.Fatal(err)
			}
			number, err := strconv.ParseInt(proof.Number, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			c := factory.HumanMergeCapture{Request: factory.HumanMergeCommand{ID: proof.CommandID, Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: number, Method: proof.Method, Message: proof.Message, CommitOID: proof.CommitOID}, Authority: proof.Record.InstallationID, OperationID: proof.Record.OperationID, ProviderIntent: proof.ProviderIntent}
			c, replay, err := s.CaptureHumanMerge(ctx, c)
			if err != nil || replay {
				t.Fatal("actual merge preparation capture", err)
			}
			cmd, err := c.Command()
			if err != nil {
				t.Fatal(err)
			}
			observe := func(o collaboration.OperationLookup) collaboration.OperationLookup {
				t.Helper()
				got, err := s.ObserveHumanMerge(ctx, c, o)
				if err != nil {
					t.Fatal(entry.Name(), "merge observation", err)
				}
				return got
			}
			without := func(state collaboration.LookupState) collaboration.OperationLookup {
				return collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: state}
			}
			for _, state := range []collaboration.LookupState{collaboration.LookupNotObserved, collaboration.LookupUnknown, collaboration.LookupUnavailable} {
				observe(without(state))
			}
			binding := &collaboration.OperationBinding{Repository: repo.ID, Actor: actor.ID, Kind: collaboration.Merge, Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest}
			pending := collaboration.OperationRecord{Host: host.ID, Authority: c.Authority, OperationID: c.OperationID, Binding: binding, Effect: collaboration.EffectPending, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionPending}
			wrap := func(r collaboration.OperationRecord) collaboration.OperationLookup {
				return collaboration.OperationLookup{Host: host.ID, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupFound, Record: &r}
			}
			if proof.Record.Kind != "" {
				observe(wrap(pending))
				uncertain := pending
				uncertain.Effect = collaboration.EffectIndeterminate
				observe(wrap(uncertain))
				kept := observe(wrap(pending))
				if kept.Record.Effect != collaboration.EffectIndeterminate {
					t.Fatal("stale pending erased uncertainty")
				}
			}
			// This projection uses actual native result/receipt bytes. Pending and
			// conflicting variants above/below are explicit ordering fixtures.
			native := proof.Record
			actual := pending
			actual.Effect = collaboration.EffectState(native.Effect)
			actual.Cancellation = collaboration.CancellationState(native.Cancellation)
			actual.Reason = native.Reason
			actual.Completion = collaboration.CompletionState(native.Completion)
			if actual.Effect != collaboration.EffectCommitted {
				actual.Completion = collaboration.CompletionComplete
			}
			if native.Kind == "" {
				actual.Binding = nil
			} else if native.Kind != "pull_request.human_merge.apply" && native.Kind != "pull_request.human_manual_merge.apply" {
				t.Fatal("foreign native result kind")
			}
			if len(native.Receipt) > 0 {
				actual.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: native.Digest, Payload: append(json.RawMessage(nil), native.Receipt...)}
			}
			beforeActual, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if actual.Receipt != nil {
				large := actual
				r := *large.Receipt
				large.Receipt = &r
				r.Payload = json.RawMessage(`{"ordering_fixture":"` + strings.Repeat("<", 12000) + `"}`)
				if _, err := s.ObserveHumanMerge(ctx, c, wrap(large)); err == nil {
					t.Fatal("escaped outcome size exceeded journal bound")
				}
			}
			if index == 0 {
				if _, err := s.db.ExecContext(ctx, `CREATE FUNCTION fail_merge_observation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture merge observation rollback'; END $$`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_merge_observation BEFORE UPDATE OF outcome ON factory_commands FOR EACH ROW EXECUTE FUNCTION fail_merge_observation()`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ObserveHumanMerge(ctx, c, wrap(actual)); err == nil {
					t.Fatal("observation SQL failure ignored")
				}
				if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_merge_observation ON factory_commands"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(ctx, "DROP FUNCTION fail_merge_observation()"); err != nil {
					t.Fatal(err)
				}
			}
			unchanged, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil || unchanged != beforeActual {
				t.Fatal("refused outcome changed command", err)
			}
			changedCapture := c
			changedCapture.OperationID = "foreign-captured-operation"
			foreignCaptureObservation := collaboration.OperationLookup{Host: c.Request.Host, Authority: c.Authority, OperationID: changedCapture.OperationID, State: collaboration.LookupUnknown}
			if _, err := s.ObserveHumanMerge(ctx, changedCapture, foreignCaptureObservation); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("changed saved capture adopted", err)
			}
			final := observe(wrap(actual))
			if final.Record.Effect != actual.Effect || final.Record.Completion != actual.Completion {
				t.Fatal("native result did not advance")
			}
			before, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (before.Finished != "") != factory.HumanMergeFinished(final) {
				t.Fatal("merge finished before completion")
			}
			for _, state := range []collaboration.LookupState{collaboration.LookupUnknown, collaboration.LookupNotObserved, collaboration.LookupUnavailable} {
				observe(without(state))
			}
			if actual.Binding != nil {
				stale := pending
				observe(wrap(stale))
				if actual.Effect == collaboration.EffectCommitted && actual.Completion == collaboration.CompletionNeedsIntervention {
					stale = actual
					stale.Completion = collaboration.CompletionPending
					observe(wrap(stale))
				}
			}
			for _, mutate := range []func(*collaboration.OperationLookup){
				func(o *collaboration.OperationLookup) { o.Host = collaboration.HostID(collaboration.NewID()) },
				func(o *collaboration.OperationLookup) { o.Authority = "foreign-installation" },
				func(o *collaboration.OperationLookup) { o.OperationID = "foreign-operation" },
			} {
				bad := wrap(actual)
				mutate(&bad)
				if _, err := s.ObserveHumanMerge(ctx, c, bad); err == nil {
					t.Fatal("foreign observation accepted")
				}
			}
			if actual.Binding != nil {
				for _, field := range []string{"actor", "repository", "kind", "digest", "atomicity"} {
					bad := actual
					b := *bad.Binding
					bad.Binding = &b
					switch field {
					case "actor":
						b.Actor++
					case "repository":
						b.Repository++
					case "kind":
						b.Kind = collaboration.RequestReview
					case "digest":
						b.IntentDigest = strings.Repeat("b", 64)
					case "atomicity":
						b.Atomicity = collaboration.Ordered
					}
					if _, err := s.ObserveHumanMerge(ctx, c, wrap(bad)); err == nil {
						t.Fatal("changed merge binding accepted", field)
					}
				}
			}
			if actual.Receipt != nil {
				for _, field := range []string{"authority", "reference", "digest", "payload"} {
					bad := actual
					r := *bad.Receipt
					bad.Receipt = &r
					switch field {
					case "authority":
						r.Authority = "foreign"
					case "reference":
						r.Reference = "foreign"
					case "digest":
						r.IntentDigest = strings.Repeat("c", 64)
					case "payload":
						r.Payload = append(append(json.RawMessage(nil), r.Payload...), byte(' '))
					}
					if _, err := s.ObserveHumanMerge(ctx, c, wrap(bad)); err == nil {
						t.Fatal("changed exact merge receipt accepted", field)
					}
				}
				contradiction := pending
				contradiction.Effect = collaboration.EffectNotCommitted
				contradiction.Completion = collaboration.CompletionComplete
				if _, err := s.ObserveHumanMerge(ctx, c, wrap(contradiction)); err == nil {
					t.Fatal("contradictory terminal effect accepted")
				}
			} else if actual.Binding != nil {
				contradiction := pending
				contradiction.Effect = collaboration.EffectCommitted
				contradiction.Completion = collaboration.CompletionComplete
				contradiction.Receipt = &collaboration.ProviderReceipt{Authority: c.Authority, Reference: c.OperationID, IntentDigest: strings.Repeat("a", 64), Payload: json.RawMessage(`{"fixture":"conflict"}`)}
				if _, err := s.ObserveHumanMerge(ctx, c, wrap(contradiction)); err == nil {
					t.Fatal("refusal was overwritten by contradictory success")
				}
			}
			// The command row orders simultaneous stale and exact replies.
			failures := make(chan error, 2)
			for _, o := range []collaboration.OperationLookup{without(collaboration.LookupUnknown), wrap(actual)} {
				go func() { _, err := s.ObserveHumanMerge(ctx, c, o); failures <- err }()
			}
			if a, b := <-failures, <-failures; a != nil || b != nil {
				t.Fatal("concurrent observation", a, b)
			}
			after, err := s.FactoryCommand(ctx, c.Request.ID)
			if err != nil || after != before {
				t.Fatal("stale/exact reply changed retained outcome", err)
			}
			saved = append(saved, after)
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
	}
	complete, pending, intervention, tombstones := 0, 0, 0, 0
	for _, want := range saved {
		got, err := s.FactoryCommand(ctx, want.ID)
		if err != nil || got != want {
			t.Fatal("retained merge command changed", phase, err)
		}
		c, err := factory.DecodeHumanMergeCapture(got)
		if err != nil {
			t.Fatal(err)
		}
		var o collaboration.OperationLookup
		if err := json.Unmarshal([]byte(got.Outcome), &o); err != nil || factory.ValidateHumanMergeObservation(c, o) != nil {
			t.Fatal("retained merge observation differs", err)
		}
		if (got.Finished != "") != factory.HumanMergeFinished(o) {
			t.Fatal("retained finish marker differs")
		}
		if got.Finished != "" {
			complete++
		}
		if o.Record.Completion == collaboration.CompletionPending {
			pending++
		}
		if o.Record.Completion == collaboration.CompletionNeedsIntervention {
			intervention++
		}
		if o.Record.Binding == nil {
			tombstones++
		}
		for _, next := range []collaboration.OperationLookup{o, {Host: c.Request.Host, Authority: c.Authority, OperationID: c.OperationID, State: collaboration.LookupUnknown}} {
			if _, err := s.ObserveHumanMerge(ctx, c, next); err != nil {
				t.Fatal("retained observation replay", phase, err)
			}
		}
		after, err := s.FactoryCommand(ctx, want.ID)
		if err != nil || after != want {
			t.Fatal("reopen replay changed exact merge command", phase, err)
		}
	}
	if len(saved) != 8 || complete != 4 || pending != 2 || intervention != 2 || tombstones != 1 {
		t.Fatal("incomplete real merge observation matrix", len(saved), complete, pending, intervention, tombstones)
	}
	t.Logf("PASS %s: %d actual native merge/manual outcomes, %d complete, %d pending, %d intervention, %d early cancellation; exact receipts/captures and monotonic SQL retained", phase, len(saved), complete, pending, intervention, tombstones)
}

func checkNativeHumanReviewRequestObservations(t *testing.T, s *Store, phase, path string) {
	t.Helper()
	ctx := t.Context()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved []factory.Command
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved) < 12 {
		t.Fatal("missing real reviewer outcome proof", len(saved), err)
	}
	complete, intervention, tombstones := 0, 0, 0
	for _, expected := range saved {
		actual, err := s.FactoryCommand(ctx, expected.ID)
		if err != nil || actual != expected {
			t.Fatal("retained reviewer journal changed", phase, expected.ID, err)
		}
		capture, err := factory.DecodeHumanReviewRequestCapture(actual)
		if err != nil {
			t.Fatal(err)
		}
		var outcome collaboration.OperationLookup
		if err := json.Unmarshal([]byte(actual.Outcome), &outcome); err != nil {
			t.Fatal(err)
		}
		if factory.ValidateHumanReviewRequestObservation(capture, outcome) != nil || (actual.Finished != "") != factory.HumanReviewRequestFinished(outcome) {
			t.Fatal("retained reviewer binding/finish differs", phase)
		}
		if actual.Finished != "" {
			complete++
		}
		if outcome.Record != nil {
			if outcome.Record.Completion == collaboration.CompletionNeedsIntervention {
				intervention++
				if actual.Finished != "" {
					t.Fatal("intervention finished")
				}
			}
			if outcome.Record.Binding == nil && outcome.Record.Cancellation == collaboration.CancellationCancelled {
				tombstones++
			}
		}
		if _, err := s.ObserveHumanReviewRequest(ctx, capture, outcome); err != nil {
			t.Fatal("retained exact observation replay", phase, err)
		}
		stale := collaboration.OperationLookup{Host: capture.Request.Host, Authority: capture.Authority, OperationID: capture.OperationID, State: collaboration.LookupUnknown}
		if _, err := s.ObserveHumanReviewRequest(ctx, capture, stale); err != nil {
			t.Fatal("retained stale observation", phase, err)
		}
		actual, err = s.FactoryCommand(ctx, expected.ID)
		if err != nil || actual != expected {
			t.Fatal("retained stale/replay changed exact outcome or capture", phase, err)
		}
	}
	if complete < 8 || intervention < 1 || tombstones < 1 {
		t.Fatal("incomplete real reviewer outcome matrix", complete, intervention, tombstones)
	}
	t.Logf("PASS %s: %d actual native reviewer outcomes retain exact capture/receipt/binding/finished bytes; %d complete, %d intervention and %d early cancellation; stale/exact replay never repeats provider effects", phase, len(saved), complete, intervention, tombstones)
}

func checkNativeHumanPullCreateJournal(t *testing.T, s *Store, dsn, phase, path string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Captures []factory.HumanPullCreateCapture
		Legacy   factory.HumanMergeCapture
	}
	same := func(a, b factory.HumanPullCreateCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://pull-journal.example.test"}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		for _, h := range []collaboration.Host{host, other} {
			if _, err := s.PutCollaborationHost(ctx, h); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"collaboration_actors", "collaboration_repositories"} {
			if _, err := s.db.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('"+table+"','id'),9007199254740992,true)"); err != nil {
				t.Fatal(err)
			}
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "42", "operator")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := s.ObserveCollaborationActor(ctx, other.ID, "42", "foreign")
		if err != nil {
			t.Fatal(err)
		}
		base, err := s.ObserveCollaborationRepository(ctx, host.ID, "repo:7", "org/project")
		if err != nil {
			t.Fatal(err)
		}
		head, err := s.ObserveCollaborationRepository(ctx, host.ID, "repo:8", "other/private-source")
		if err != nil {
			t.Fatal(err)
		}
		foreignRepo, err := s.ObserveCollaborationRepository(ctx, other.ID, "repo:7", "foreign/project")
		if err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.HumanMergeCapture{Request: factory.HumanMergeCommand{ID: factory.NewID(), Host: host.ID, Repository: base.ID, Actor: actor.ID, Number: 2, Method: "merge", Message: "Original retained merge 完整\r\n"}, Authority: "retained-installation", OperationID: "retained-operation", ProviderIntent: []byte("{ \"retained\":true }\n ")}
		if _, _, err := s.CaptureHumanMerge(ctx, saved.Legacy); err != nil {
			t.Fatal(err)
		}
		// Restore the actual preceding payload format before any new PR row.
		restorePreDispatchCommandFixture(t, s.db)
		for _, ddl := range HumanMergeJournalUpgradeDDL {
			if _, err := s.db.ExecContext(ctx, ddl); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-79 pull creation upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("pull creation upgrade marker", version, err)
		}
		for _, variant := range []string{"normal", "same-repository", "empty-body", "maximum", "unicode-branches", "long-branch", "maintainer-edit", "github-scope"} {
			q := factory.HumanPullCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: base.ID, HeadRepository: head.ID, Actor: actor.ID, BaseBranch: "main", HeadBranch: "work", Title: "Human PR 完整 🌋", Body: "## Original human writing\r\n\n完整 body.\n"}
			intent := []byte("{ \"opaque_preparation\":\"完整 🌋\\r\\n\", \"revision\":42 }\n ")
			switch variant {
			case "same-repository":
				q.HeadRepository = q.Repository
			case "empty-body":
				q.Body = ""
			case "maximum":
				q.Body = strings.Repeat("<", factory.MaxHumanPullCreateBodyBytes)
				q.Title = strings.Repeat("t", factory.MaxHumanPullCreateTitleBytes)
				prefix, suffix := "{ \"opaque\":\"", "\"}\n "
				intent = []byte(prefix + strings.Repeat("x", factory.MaxHumanPullCreateProviderIntentBytes-len(prefix)-len(suffix)) + suffix)
			case "unicode-branches":
				q.BaseBranch = "release/完整"
				q.HeadBranch = "feature/🌋"
			case "long-branch":
				q.HeadBranch = strings.Repeat("b", 512-len("refs/heads/"))
			case "maintainer-edit":
				q.AllowMaintainerEdit = true
			case "github-scope":
				q.Host = other.ID
				q.Repository = foreignRepo.ID
				q.HeadRepository = foreignRepo.ID
				q.Actor = foreign.ID
			}
			c := factory.HumanPullCreateCapture{Request: q, Authority: "original-installation", OperationID: "original-operation-" + variant, ProviderIntent: intent}
			if _, err := s.HumanPullCreateCapture(ctx, q); !errors.Is(err, ErrNotFound) {
				t.Fatal("absent capture", err)
			}
			got, replay, err := s.CaptureHumanPullCreate(ctx, c)
			if err != nil || replay || !same(got, c) {
				t.Fatal("pull creation capture changed", variant, replay, err)
			}
			cmd, err := got.Command()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(cmd.Payload, `"actor":"900719925474099`) || !strings.Contains(cmd.Payload, `"repository":"900719925474099`) || !strings.Contains(cmd.Payload, `"head_repository":"900719925474099`) {
				t.Fatal("canonical BIGINT passed through numeric JSON")
			}
			if variant == "maximum" && (len(intent) != factory.MaxHumanPullCreateProviderIntentBytes || len(cmd.Payload) <= 1<<20 || len(cmd.Payload) > factory.MaxHumanPullCreateCaptureBytes) {
				t.Fatal("maximum escaped pull capture does not fit", len(intent), len(cmd.Payload))
			}
			changed := c
			changed.Authority = "replacement-installation"
			changed.OperationID = "refreshed-operation"
			changed.ProviderIntent = []byte(`{"different":true}`)
			winner, replay, err := s.CaptureHumanPullCreate(ctx, changed)
			if err != nil || !replay || !same(winner, c) {
				t.Fatal("pull preparation refreshed", variant, err)
			}
			saved.Captures = append(saved.Captures, got)
		}
		first := saved.Captures[0]
		for _, mutate := range []func(*factory.HumanPullCreateCapture){
			func(c *factory.HumanPullCreateCapture) { c.Request.Actor = foreign.ID }, func(c *factory.HumanPullCreateCapture) { c.Request.Actor = actor.ID + 999999 }, func(c *factory.HumanPullCreateCapture) { c.Request.Host = other.ID }, func(c *factory.HumanPullCreateCapture) { c.Request.Repository = foreignRepo.ID }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadRepository = foreignRepo.ID }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadRepository = head.ID + 999999 },
		} {
			c := first
			c.Request.ID = factory.NewID()
			mutate(&c)
			if _, _, err := s.CaptureHumanPullCreate(ctx, c); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("unbound base/source/actor admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, c.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("unbound scope allocated command", err)
			}
		}
		for _, mutate := range []func(*factory.HumanPullCreateCapture){
			func(c *factory.HumanPullCreateCapture) { c.Request.HeadRepository = 0 }, func(c *factory.HumanPullCreateCapture) { c.Request.Title = " " }, func(c *factory.HumanPullCreateCapture) { c.Request.Title = strings.Repeat("x", 256) }, func(c *factory.HumanPullCreateCapture) { c.Request.Title = "\xff" }, func(c *factory.HumanPullCreateCapture) { c.Request.Body = "\x00" }, func(c *factory.HumanPullCreateCapture) { c.Request.Body = "\xff" }, func(c *factory.HumanPullCreateCapture) {
				c.Request.Body = strings.Repeat("x", factory.MaxHumanPullCreateBodyBytes+1)
			}, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = "work@{1}" }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = "work~1" }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = ".hidden/work" }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = "foo.lock" }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = "foo//bar" }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = "a[1]" }, func(c *factory.HumanPullCreateCapture) {
				c.Request.HeadBranch = strings.Repeat("b", 513-len("refs/heads/"))
			}, func(c *factory.HumanPullCreateCapture) {
				c.Request.HeadRepository = c.Request.Repository
				c.Request.HeadBranch = c.Request.BaseBranch
			}, func(c *factory.HumanPullCreateCapture) { c.Authority = "" }, func(c *factory.HumanPullCreateCapture) { c.OperationID = "" }, func(c *factory.HumanPullCreateCapture) { c.ProviderIntent = []byte("invalid") }, func(c *factory.HumanPullCreateCapture) {
				c.ProviderIntent = []byte(`{"opaque":"` + strings.Repeat("x", factory.MaxHumanPullCreateProviderIntentBytes) + `"}`)
			},
		} {
			c := first
			c.Request.ID = factory.NewID()
			mutate(&c)
			if _, _, err := s.CaptureHumanPullCreate(ctx, c); err == nil {
				t.Fatal("invalid pull capture accepted")
			}
		}
		for _, mutate := range []func(*factory.HumanPullCreateCapture){func(c *factory.HumanPullCreateCapture) { c.Request.Body += "changed" }, func(c *factory.HumanPullCreateCapture) { c.Request.Title += "changed" }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadRepository = c.Request.Repository }, func(c *factory.HumanPullCreateCapture) { c.Request.HeadBranch = "different" }, func(c *factory.HumanPullCreateCapture) { c.Request.BaseBranch = "different" }, func(c *factory.HumanPullCreateCapture) { c.Request.AllowMaintainerEdit = true }, func(c *factory.HumanPullCreateCapture) { c.Request.ID = saved.Legacy.Request.ID }} {
			c := first
			mutate(&c)
			if _, _, err := s.CaptureHumanPullCreate(ctx, c); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("changed creation or foreign command adopted", err)
			}
		}
		left, right := first, first
		left.Request.ID = factory.NewID()
		right.Request = left.Request
		right.Authority = "other-winner"
		right.OperationID = "other-operation"
		right.ProviderIntent = []byte("{ \"other\":true }\n")
		type result struct {
			c      factory.HumanPullCreateCapture
			replay bool
			err    error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanPullCreateCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanPullCreate(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || a.replay == b.replay || !same(a.c, b.c) {
			t.Fatal("concurrent preparation split", a.err, b.err)
		}
		saved.Captures = append(saved.Captures, a.c)
		for _, column := range []string{"payload", "target", "principal", "digest"} {
			if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET "+column+"="+column+"||'changed' WHERE id=$1", first.Request.ID); err == nil {
				t.Fatal("immutable pull capture changed", column)
			}
		}
		for _, bound := range []struct {
			kind  string
			limit int
		}{{factory.CommandHumanPullCreate, factory.MaxHumanPullCreateCaptureBytes}, {factory.CommandHumanMerge, factory.MaxHumanMergeCaptureBytes}, {factory.CommandHumanReviewRequest, factory.MaxHumanReviewRequestCaptureBytes}, {factory.CommandRetry, 8192}} {
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'bounds','42',$3,$4,'fixture')", factory.NewID(), bound.kind, strings.Repeat("a", 64), strings.Repeat("x", bound.limit+1)); err == nil {
				t.Fatal("SQL capture bound bypassed", bound.kind)
			}
		}
		if _, err := s.db.ExecContext(ctx, `CREATE FUNCTION fail_pull_capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture pull capture rollback'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_pull_capture BEFORE INSERT ON factory_commands FOR EACH ROW WHEN (NEW.type='pull_request.create') EXECUTE FUNCTION fail_pull_capture()`); err != nil {
			t.Fatal(err)
		}
		failed := first
		failed.Request.ID = factory.NewID()
		if _, _, err := s.CaptureHumanPullCreate(ctx, failed); err == nil {
			t.Fatal("capture SQL failure ignored")
		}
		if _, err := s.FactoryCommand(ctx, failed.Request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("failed capture survived rollback", err)
		}
		if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_pull_capture ON factory_commands"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "DROP FUNCTION fail_pull_capture()"); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := s.HumanMergeCapture(ctx, saved.Legacy.Request)
	if err != nil {
		t.Fatal("retained previous capture", err)
	}
	wantLegacy, _ := saved.Legacy.Command()
	gotLegacy, _ := legacy.Command()
	if wantLegacy.Payload != gotLegacy.Payload {
		t.Fatal("upgrade changed prior writing/preparation")
	}
	for _, want := range saved.Captures {
		got, err := s.HumanPullCreateCapture(ctx, want.Request)
		if err != nil || !same(want, got) {
			t.Fatal("retained pull capture changed", phase, err)
		}
		changed := want
		changed.Authority = "new-installation"
		changed.OperationID = "new-operation"
		changed.ProviderIntent = []byte(`{"refreshed":true}`)
		got, replay, err := s.CaptureHumanPullCreate(ctx, changed)
		if err != nil || !replay || !same(want, got) {
			t.Fatal("retained replay refreshed", phase, err)
		}
		cmd, err := s.FactoryCommand(ctx, want.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" {
			t.Fatal("capture invented completion", err)
		}
		decoded, err := factory.DecodeHumanPullCreateCapture(cmd)
		if err != nil || !same(want, decoded) {
			t.Fatal("retained envelope differs", err)
		}
		for _, field := range []string{"target", "principal", "payload"} {
			bad := cmd
			switch field {
			case "target":
				bad.Target += "changed"
			case "principal":
				bad.Principal += "1"
			case "payload":
				bad.Payload += " "
			}
			bad.Digest = factory.SettingsDigest(bad.Type, bad.Target, bad.Payload)
			if _, err := factory.DecodeHumanPullCreateCapture(bad); err == nil {
				t.Fatal("journal binding tamper accepted", field)
			}
		}
	}
	t.Logf("PASS %s: %d canonical human PR captures retain exact base/source/actor BIGINT, branches/writing/delegation, opaque preparation and winning concurrent capture across native PostgreSQL lifetimes; schema-79 upgrade, bounds and rollback; no provider effects/outcomes", phase, len(saved.Captures))
}

func checkNativeHumanMergeJournal(t *testing.T, s *Store, dsn, phase, path string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Captures []factory.HumanMergeCapture
		Legacy   factory.HumanStateCapture
	}
	same := func(a, b factory.HumanMergeCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://merge-journal.example.test"}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		for _, h := range []collaboration.Host{host, other} {
			if _, err := s.PutCollaborationHost(ctx, h); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('collaboration_actors','id'),9007199254740992,true)"); err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "42", "operator")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := s.ObserveCollaborationActor(ctx, other.ID, "42", "foreign")
		if err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "repo:7", "org/project")
		if err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 2, Close: true}, Authority: "retained-installation", OperationID: "retained-operation", ProviderIntent: []byte("{ \"retained\":\"完整 🌋\\r\\n\" }\n ")}
		if _, _, err := s.CaptureHumanState(ctx, saved.Legacy); err != nil {
			t.Fatal(err)
		}
		// Restore the actual prior format before any large merge command exists.
		restorePreDispatchCommandFixture(t, s.db)
		for _, ddl := range HumanReviewRequestJournalUpgradeDDL {
			if _, err := s.db.ExecContext(ctx, ddl); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-78 merge upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("merge upgrade marker", version, err)
		}
		for _, method := range []string{"merge", "rebase", "rebase-merge", "squash", "fast-forward-only", "manual", "manual-sha256"} {
			q := factory.HumanMergeCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 9007199254740993, Method: method, Message: "Original human message 完整 🌋\r\n\n"}
			if strings.HasPrefix(method, "manual") {
				q.Method = "manual"
				q.Message = ""
				q.CommitOID = strings.Repeat("a", 40)
				if method == "manual-sha256" {
					q.CommitOID = strings.Repeat("b", 64)
				}
			}
			intent := []byte("{ \"opaque_preparation\":\"完整 🌋\\r\\n\", \"revision\":42 }\n ")
			if method == "squash" {
				q.Message = strings.Repeat("<", factory.MaxHumanMergeMessageBytes)
				prefix, suffix := "{ \"opaque\":\"", "\"}\n "
				intent = []byte(prefix + strings.Repeat("x", factory.MaxHumanMergeProviderIntentBytes-len(prefix)-len(suffix)) + suffix)
			}
			c := factory.HumanMergeCapture{Request: q, Authority: "original-installation", OperationID: "original-operation-" + method, ProviderIntent: intent}
			if _, err := s.HumanMergeCapture(ctx, q); !errors.Is(err, ErrNotFound) {
				t.Fatal("absent merge capture", err)
			}
			got, replay, err := s.CaptureHumanMerge(ctx, c)
			if err != nil || replay || !same(c, got) {
				t.Fatal("merge capture changed", method, replay, err)
			}
			cmd, err := got.Command()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(cmd.Payload, `"number":"9007199254740993"`) || !strings.Contains(cmd.Payload, `"actor":"9007199254740993"`) {
				t.Fatal("canonical BIGINT passed through numeric JSON")
			}
			if method == "squash" && (len(intent) != factory.MaxHumanMergeProviderIntentBytes || len(cmd.Payload) <= 1<<20 || len(cmd.Payload) > factory.MaxHumanMergeCaptureBytes) {
				t.Fatal("maximum escaped merge envelope does not fit", len(intent), len(cmd.Payload))
			}
			changed := c
			changed.Authority = "replacement-installation"
			changed.OperationID = "refreshed-operation"
			changed.ProviderIntent = []byte("{\"different\":true}")
			winner, replay, err := s.CaptureHumanMerge(ctx, changed)
			if err != nil || !replay || !same(c, winner) {
				t.Fatal("merge preparation refreshed", method, err)
			}
			saved.Captures = append(saved.Captures, got)
		}
		base := saved.Captures[0]
		for _, mutate := range []func(*factory.HumanMergeCapture){
			func(c *factory.HumanMergeCapture) { c.Request.Actor = foreign.ID },
			func(c *factory.HumanMergeCapture) { c.Request.Actor = actor.ID + 999999 },
			func(c *factory.HumanMergeCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanMergeCapture) { c.Request.Repository = repo.ID + 999999 },
		} {
			c := base
			c.Request.ID = factory.NewID()
			mutate(&c)
			if _, _, err := s.CaptureHumanMerge(ctx, c); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("unbound merge scope accepted", err)
			}
			if _, err := s.FactoryCommand(ctx, c.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("unbound scope allocated command", err)
			}
		}
		for _, mutate := range []func(*factory.HumanMergeCapture){
			func(c *factory.HumanMergeCapture) { c.Request.Number = 0 },
			func(c *factory.HumanMergeCapture) { c.Request.Method = "unknown" },
			func(c *factory.HumanMergeCapture) { c.Request.Message = "\x00" },
			func(c *factory.HumanMergeCapture) { c.Request.Message = "\xff" },
			func(c *factory.HumanMergeCapture) { c.Request.Message = " " },
			func(c *factory.HumanMergeCapture) {
				c.Request.Message = strings.Repeat("x", factory.MaxHumanMergeMessageBytes+1)
			},
			func(c *factory.HumanMergeCapture) { c.Request.CommitOID = strings.Repeat("a", 40) },
			func(c *factory.HumanMergeCapture) {
				c.Request.Method = "manual"
				c.Request.Message = ""
				c.Request.CommitOID = "HEAD"
			},
			func(c *factory.HumanMergeCapture) {
				c.Request.Method = "manual"
				c.Request.Message = ""
				c.Request.CommitOID = strings.Repeat("A", 40)
			},
			func(c *factory.HumanMergeCapture) {
				c.Request.Method = "manual"
				c.Request.Message = ""
				c.Request.CommitOID = strings.Repeat("0", 64)
			},
			func(c *factory.HumanMergeCapture) {
				c.Request.Method = "manual"
				c.Request.CommitOID = strings.Repeat("a", 40)
			},
			func(c *factory.HumanMergeCapture) { c.Authority = "" },
			func(c *factory.HumanMergeCapture) { c.OperationID = "" },
			func(c *factory.HumanMergeCapture) { c.ProviderIntent = []byte("invalid") },
			func(c *factory.HumanMergeCapture) {
				c.ProviderIntent = []byte("{\"opaque\":\"" + strings.Repeat("x", factory.MaxHumanMergeProviderIntentBytes) + "\"}")
			},
		} {
			c := base
			c.Request.ID = factory.NewID()
			mutate(&c)
			if _, _, err := s.CaptureHumanMerge(ctx, c); err == nil {
				t.Fatal("invalid merge capture accepted")
			}
		}
		changed := base
		changed.Request.Message += "changed"
		if _, _, err := s.CaptureHumanMerge(ctx, changed); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed message reused command ID", err)
		}
		changed = base
		changed.Request.Method = "rebase"
		if _, _, err := s.CaptureHumanMerge(ctx, changed); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed method reused command ID", err)
		}
		changed = base
		changed.Request.ID = saved.Legacy.Request.ID
		if _, _, err := s.CaptureHumanMerge(ctx, changed); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("foreign command kind adopted", err)
		}
		left, right := base, base
		left.Request.ID = factory.NewID()
		right.Request = left.Request
		right.Authority = "other-winner"
		right.OperationID = "other-operation"
		right.ProviderIntent = []byte("{ \"other\":true }\n")
		type result struct {
			c      factory.HumanMergeCapture
			replay bool
			err    error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanMergeCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanMerge(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || a.replay == b.replay || !same(a.c, b.c) {
			t.Fatal("concurrent merge preparation split", a.err, b.err)
		}
		saved.Captures = append(saved.Captures, a.c)
		for _, column := range []string{"payload", "target", "principal", "digest"} {
			if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET "+column+"="+column+"||'changed' WHERE id=$1", base.Request.ID); err == nil {
				t.Fatal("immutable merge column changed", column)
			}
		}
		for _, bound := range []struct {
			kind  string
			limit int
		}{{factory.CommandHumanMerge, factory.MaxHumanMergeCaptureBytes}, {factory.CommandHumanReviewRequest, factory.MaxHumanReviewRequestCaptureBytes}, {factory.CommandRetry, 8192}} {
			_, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'bounds','42',$3,$4,'fixture')", factory.NewID(), bound.kind, strings.Repeat("a", 64), strings.Repeat("x", bound.limit+1))
			if err == nil {
				t.Fatal("SQL payload bound bypassed", bound.kind)
			}
		}
		if _, err := s.db.ExecContext(ctx, `CREATE FUNCTION fail_merge_capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture merge capture rollback'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_merge_capture BEFORE INSERT ON factory_commands FOR EACH ROW WHEN (NEW.type='pull_request.merge') EXECUTE FUNCTION fail_merge_capture()`); err != nil {
			t.Fatal(err)
		}
		failed := base
		failed.Request.ID = factory.NewID()
		if _, _, err := s.CaptureHumanMerge(ctx, failed); err == nil {
			t.Fatal("injected SQL failure was ignored")
		}
		if _, err := s.FactoryCommand(ctx, failed.Request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("failed capture survived rollback", err)
		}
		if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_merge_capture ON factory_commands"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "DROP FUNCTION fail_merge_capture()"); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range saved.Captures {
		got, err := s.HumanMergeCapture(ctx, want.Request)
		if err != nil || !same(want, got) {
			t.Fatal("retained merge capture changed", phase, err)
		}
		changed := want
		changed.Authority = "new-installation"
		changed.OperationID = "new-operation"
		changed.ProviderIntent = []byte("{\"refreshed\":true}")
		got, replay, err := s.CaptureHumanMerge(ctx, changed)
		if err != nil || !replay || !same(want, got) {
			t.Fatal("retained merge replay refreshed", phase, err)
		}
		cmd, err := s.FactoryCommand(ctx, want.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" {
			t.Fatal("capture invented provider completion", err)
		}
		decoded, err := factory.DecodeHumanMergeCapture(cmd)
		if err != nil || !same(want, decoded) {
			t.Fatal("retained capture envelope differs", err)
		}
		for _, field := range []string{"target", "principal", "payload"} {
			bad := cmd
			switch field {
			case "target":
				bad.Target += "changed"
			case "principal":
				bad.Principal += "1"
			case "payload":
				bad.Payload += " "
			}
			bad.Digest = factory.SettingsDigest(bad.Type, bad.Target, bad.Payload)
			if _, err := factory.DecodeHumanMergeCapture(bad); err == nil {
				t.Fatal("journal binding tamper accepted", field)
			}
		}
	}
	legacy, err := s.HumanStateCapture(ctx, saved.Legacy.Request)
	if err != nil || legacy.Authority != saved.Legacy.Authority || legacy.OperationID != saved.Legacy.OperationID || !bytes.Equal(legacy.ProviderIntent, saved.Legacy.ProviderIntent) {
		t.Fatal("legacy command rewritten by merge upgrade", err)
	}
	t.Logf("%s: canonical merge/manual capture, actual PostgreSQL upgrade/immutability/bounds/concurrency/rollback; exact provider bytes retained and no provider outcome invented", phase)
}

func checkNativeHumanReviewRequestJournal(t *testing.T, s *Store, dsn, phase, path string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Captures []factory.HumanReviewRequestCapture
		Legacy   factory.HumanStateCapture
	}
	same := func(a, b factory.HumanReviewRequestCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://reviewer-journal.example.test"}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		for _, h := range []collaboration.Host{host, other} {
			if _, err := s.PutCollaborationHost(ctx, h); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		for _, sequence := range []string{"collaboration_actors", "collaboration_teams"} {
			if _, err := s.db.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('"+sequence+"','id'),9007199254740992,true)"); err != nil {
				t.Fatal(err)
			}
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "42", "operator")
		if err != nil {
			t.Fatal(err)
		}
		reviewer, err := s.ObserveCollaborationActor(ctx, host.ID, "43", "reviewer")
		if err != nil {
			t.Fatal(err)
		}
		team, err := s.ObserveCollaborationTeam(ctx, host.ID, "provider:team/完整", "reviewers")
		if err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "43", "foreign")
		if err != nil {
			t.Fatal(err)
		}
		foreignTeam, err := s.ObserveCollaborationTeam(ctx, other.ID, "provider:team/完整", "foreign")
		if err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "repo:7", "org/project")
		if err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 2, Close: true}, Authority: "retained-installation", OperationID: "retained-operation", ProviderIntent: []byte("{ \"original\": \"完整 🌋\\r\\n\" }\n ")}
		if _, _, err := s.CaptureHumanState(ctx, saved.Legacy); err != nil {
			t.Fatal(err)
		}
		// Restore the exact former bound in this disposable native cluster, then
		// qualify the real schema-77 upgrade without rewriting the retained command.
		restorePreDispatchCommandFixture(t, s.db)
		for _, ddl := range HumanThreadJournalUpgradeDDL {
			if _, err := s.db.ExecContext(ctx, ddl); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("schema-77 reviewer upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("reviewer upgrade marker", version, err)
		}
		preparation := opaqueHumanPreparationFixture(t, factory.MaxHumanReviewRequestProviderIntentBytes)
		capture := factory.HumanReviewRequestCapture{Request: factory.HumanReviewRequestCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 9007199254740993, Reviewer: reviewer.ID, Requested: true}, Authority: "reviewer-original-installation", OperationID: "reviewer-original-operation", ProviderIntent: preparation}
		if _, err := s.HumanReviewRequestCapture(ctx, capture.Request); !errors.Is(err, ErrNotFound) {
			t.Fatal("absent reviewer capture invented", err)
		}
		for _, mutate := range []func(*factory.HumanReviewRequestCapture){
			func(c *factory.HumanReviewRequestCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Actor = foreignActor.ID },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Reviewer = foreignActor.ID },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Reviewer = 0; c.Request.Team = foreignTeam.ID },
			func(c *factory.HumanReviewRequestCapture) {
				c.Request.Reviewer = 0
				c.Request.Team = collaboration.TeamID(9223372036854775807)
			},
			func(c *factory.HumanReviewRequestCapture) {
				c.Request.Reviewer = collaboration.ActorID(9223372036854775807)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanReviewRequest(ctx, bad); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign/missing canonical reviewer admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("refused reviewer left journal entry", err)
			}
		}
		for _, target := range []string{"user", "team", "self"} {
			for _, requested := range []bool{true, false} {
				c := capture
				c.Request.ID = factory.NewID()
				c.Request.Requested = requested
				switch target {
				case "team":
					c.Request.Reviewer = 0
					c.Request.Team = team.ID
				case "self":
					c.Request.Reviewer = actor.ID
				}
				c.OperationID = "reviewer-" + c.Request.ID
				if target == "team" {
					c.ProviderIntent = []byte("{\"boundary\":\"" + strings.Repeat("x", factory.MaxHumanReviewRequestProviderIntentBytes-17) + "\"}\n ")
				}
				if target == "team" && len(c.ProviderIntent) != factory.MaxHumanReviewRequestProviderIntentBytes {
					t.Fatal("boundary fixture size", len(c.ProviderIntent))
				}
				got, replay, err := s.CaptureHumanReviewRequest(ctx, c)
				if err != nil || replay || !same(got, c) {
					t.Fatal("reviewer capture", target, requested, replay, err)
				}
				saved.Captures = append(saved.Captures, got)
			}
		}
		// Same numeric value in separate target types must still conflict on one ID.
		changed := saved.Captures[4]
		changed.Request.Reviewer = 0
		changed.Request.Team = team.ID
		if _, _, err := s.CaptureHumanReviewRequest(ctx, changed); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("team aliased self actor", err)
		}
		left, right := capture, capture
		right.OperationID = "losing-preparation"
		right.Authority = "other-installation"
		right.ProviderIntent = []byte("{\"new_revision\":9223372036854775807}\n")
		type result struct {
			capture factory.HumanReviewRequestCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanReviewRequestCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanReviewRequest(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || a.replay == b.replay || !same(a.capture, b.capture) {
			t.Fatal("concurrent capture changed original preparation", a.err, b.err)
		}
		saved.Captures = append(saved.Captures, a.capture)
		for _, mutate := range []func(*factory.HumanReviewRequestCapture){
			func(c *factory.HumanReviewRequestCapture) { c.Request.Team = team.ID },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Reviewer = 0 },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Reviewer = -1 },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Team = -1 },
			func(c *factory.HumanReviewRequestCapture) { c.Request.Number = 0 },
			func(c *factory.HumanReviewRequestCapture) { c.Authority = "" },
			func(c *factory.HumanReviewRequestCapture) { c.OperationID = "" },
			func(c *factory.HumanReviewRequestCapture) { c.ProviderIntent = []byte("invalid") },
			func(c *factory.HumanReviewRequestCapture) {
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", factory.MaxHumanReviewRequestProviderIntentBytes) + `"`)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanReviewRequest(ctx, bad); err == nil {
				t.Fatal("invalid reviewer capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid reviewer capture persisted", err)
			}
		}
		collision := capture
		collision.Request.ID = saved.Legacy.Request.ID
		if _, _, err := s.CaptureHumanReviewRequest(ctx, collision); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("reviewer replaced other command type", err)
		}
		if _, err := s.HumanReviewRequestCapture(ctx, collision.Request); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("reviewer read other command type", err)
		}
		for _, query := range []string{"UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", "UPDATE factory_commands SET target=target||':changed' WHERE id=$1", "UPDATE factory_commands SET principal='changed' WHERE id=$1"} {
			if _, err := s.db.ExecContext(ctx, query, saved.Captures[0].Request.ID); err == nil {
				t.Fatal("reviewer immutable journal guard absent")
			}
		}
		for _, entry := range []struct {
			typ   string
			limit int
		}{{factory.CommandRetry, 8192}, {factory.CommandHumanState, 8192}, {factory.CommandHumanThread, factory.MaxHumanThreadCaptureBytes}, {factory.CommandHumanReviewRequest, factory.MaxHumanReviewRequestCaptureBytes}, {factory.CommandHumanReview, factory.MaxHumanReviewCaptureBytes}} {
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'limit','42','limit',$3,'now')", factory.NewID(), entry.typ, strings.Repeat("x", entry.limit+1)); err == nil {
				t.Fatal("SQL command bound missing", entry.typ)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range saved.Captures {
		got, err := s.HumanReviewRequestCapture(ctx, c.Request)
		if err != nil || !same(got, c) {
			t.Fatal("retained reviewer capture changed", phase, err)
		}
		fresh := c
		fresh.Authority = "replacement-installation"
		fresh.OperationID = "replacement-operation"
		fresh.ProviderIntent = []byte("{\"new_revision\":1}\n")
		got, replay, err := s.CaptureHumanReviewRequest(ctx, fresh)
		if err != nil || !replay || !same(got, c) {
			t.Fatal("replay refreshed captured proof", phase, err)
		}
		cmd, err := s.FactoryCommand(ctx, c.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" {
			t.Fatal("capture invented provider outcome", phase, err)
		}
		decoded, err := factory.DecodeHumanReviewRequestCapture(cmd)
		if err != nil || !same(decoded, c) || !strings.Contains(cmd.Payload, `"number":"9007199254740993"`) {
			t.Fatal("capture binding or wide IDs lost", phase, err)
		}
		for _, mutate := range []func(*factory.Command){func(c *factory.Command) { c.Target += "x" }, func(c *factory.Command) { c.Principal = "other" }, func(c *factory.Command) { c.Payload += " " }, func(c *factory.Command) { c.Type = factory.CommandHumanThread }} {
			tampered := cmd
			mutate(&tampered)
			tampered.Digest = factory.SettingsDigest(tampered.Type, tampered.Target, tampered.Payload)
			if _, err := factory.DecodeHumanReviewRequestCapture(tampered); err == nil {
				t.Fatal("tampered envelope accepted", phase)
			}
		}
		changed := c
		changed.Request.Requested = !changed.Request.Requested
		if _, _, err := s.CaptureHumanReviewRequest(ctx, changed); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed desired state reused command", phase, err)
		}
		for _, change := range []func(*factory.HumanReviewRequestCommand){
			func(q *factory.HumanReviewRequestCommand) { q.Number++ }, func(q *factory.HumanReviewRequestCommand) { q.Actor++ }, func(q *factory.HumanReviewRequestCommand) { q.Repository++ }, func(q *factory.HumanReviewRequestCommand) { q.Host = collaboration.HostID(collaboration.NewID()) }, func(q *factory.HumanReviewRequestCommand) { q.Requested = !q.Requested }, func(q *factory.HumanReviewRequestCommand) {
				if q.Team > 0 {
					q.Team++
				} else {
					q.Reviewer++
				}
			},
		} {
			q := c.Request
			change(&q)
			if _, err := s.HumanReviewRequestCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("changed request read original capture", phase, err)
			}
		}
	}
	legacy, err := s.HumanStateCapture(ctx, saved.Legacy.Request)
	if err != nil || legacy.Authority != saved.Legacy.Authority || legacy.OperationID != saved.Legacy.OperationID || !bytes.Equal(legacy.ProviderIntent, saved.Legacy.ProviderIntent) {
		t.Fatal("upgrade changed retained original command", phase, err)
	}
	t.Logf("PASS %s: canonical user/team/self add/remove capture, exact concurrent winner/replay, wide IDs, same-host targets, immutable bounds and retained schema upgrade; no provider dispatched", phase)
}

func checkNativeCollaborationTeams(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Hosts      [2]collaboration.Host
		Actor      collaboration.ActorRef
		Repository collaboration.RepositoryRef
		Teams      []collaboration.TeamRef
		Retained   factory.HumanStateCapture
	}
	if phase == "write" {
		saved.Hosts = [2]collaboration.Host{{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://teams-native.example.test"}, {ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}}
		for _, host := range saved.Hosts {
			if _, err := s.PutCollaborationHost(ctx, host); err != nil {
				t.Fatal(err)
			}
		}
		sequence := func() string {
			var value string
			if err := s.db.QueryRowContext(ctx, "SELECT last_value::text||':'||is_called::text FROM collaboration_teams_id_seq").Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		before := sequence()
		if _, err := s.ObserveCollaborationTeam(ctx, saved.Hosts[0].ID, "42", "Reviewers"); !errors.Is(err, collaboration.ErrUnboundLedger) {
			t.Fatal("unadopted ledger allocated a team", err)
		}
		if sequence() != before {
			t.Fatal("refused Go observation consumed a team surrogate")
		}
		if _, err := s.db.ExecContext(ctx, "INSERT INTO collaboration_teams(host,external_id,name) VALUES($1,'42','Reviewers')", saved.Hosts[0].ID); err == nil {
			t.Fatal("database allocation guard admitted unbound team")
		}
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		var err error
		saved.Actor, err = s.ObserveCollaborationActor(ctx, saved.Hosts[0].ID, "42", "operator")
		if err != nil {
			t.Fatal(err)
		}
		saved.Repository, err = s.ObserveCollaborationRepository(ctx, saved.Hosts[0].ID, "opaque-repository", "org/project")
		if err != nil {
			t.Fatal(err)
		}
		saved.Retained = factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: saved.Hosts[0].ID, Actor: saved.Actor.ID, Repository: saved.Repository.ID, Subject: "pull", Number: 2, Close: true}, Authority: "retained-pre-team-namespace", OperationID: "retained-pre-team-operation", ProviderIntent: []byte("{\"original\":\"Retained 完整 \\r\\n\"}\n ")}
		if _, _, err := s.CaptureHumanState(ctx, saved.Retained); err != nil {
			t.Fatal(err)
		}
		// Stage exactly the former auxiliary registry, with no existing team IDs,
		// and preserve original identities, allocator state and immutable command.
		snapshot := func() string {
			var value string
			err := s.db.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'hosts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM collaboration_hosts x),
 'actors',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM collaboration_actors x),
 'repositories',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM collaboration_repositories x),
 'ledger',(SELECT to_jsonb(x) FROM collaboration_ledger x),
 'commands',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM factory_commands x),
 'actor_sequence',(SELECT jsonb_build_array(last_value,is_called) FROM collaboration_actors_id_seq),
 'repo_sequence',(SELECT jsonb_build_array(last_value,is_called) FROM collaboration_repositories_id_seq))::text`).Scan(&value)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		retained := snapshot()
		if _, err := s.db.ExecContext(ctx, "DROP TABLE collaboration_teams"); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		type opened struct {
			s   *Store
			err error
		}
		openedStores := make(chan opened, 2)
		for range 2 {
			go func() { current, err := Open(dsn); openedStores <- opened{current, err} }()
		}
		first, second := <-openedStores, <-openedStores
		if first.err != nil || second.err != nil {
			if first.s != nil {
				first.s.Close()
			}
			if second.s != nil {
				second.s.Close()
			}
			t.Fatal("concurrent retained registry expansion", first.err, second.err)
		}
		s = first.s
		defer s.Close()
		if err := second.s.Close(); err != nil {
			t.Fatal(err)
		}
		if snapshot() != retained {
			t.Fatal("team registry expansion changed retained identity, allocators or command")
		}
		for _, host := range saved.Hosts {
			ref, err := s.ObserveCollaborationTeam(ctx, host.ID, "42", "Reviewers")
			if err != nil {
				t.Fatal(err)
			}
			saved.Teams = append(saved.Teams, ref)
		}
		if saved.Teams[0].ID == saved.Teams[1].ID {
			t.Fatal("different hosts shared one canonical team")
		}
		ref := saved.Teams[0]
		observed, err := s.ObserveCollaborationTeam(ctx, ref.Host, ref.ExternalID, "")
		if err != nil || observed != ref {
			t.Fatal("identity-only observation replaced display metadata", err)
		}
		ref, err = s.ObserveCollaborationTeam(ctx, ref.Host, ref.ExternalID, "Renamed reviewers 完整")
		if err != nil || ref.ID != observed.ID {
			t.Fatal("rename forked identity", err)
		}
		saved.Teams[0] = ref
		opaque := "provider-team-node:org/完整 🌋"
		results := make(chan openedTeam, 16)
		for range 16 {
			go func() {
				ref, err := s.ObserveCollaborationTeam(ctx, saved.Hosts[0].ID, opaque, "Concurrent reviewers")
				results <- openedTeam{ref, err}
			}()
		}
		var shared collaboration.TeamRef
		for range 16 {
			got := <-results
			if got.err != nil {
				t.Fatal(got.err)
			}
			if shared.ID != 0 && shared.ID != got.ref.ID {
				t.Fatal("concurrent observations forked team identity")
			}
			shared = got.ref
		}
		saved.Teams = append(saved.Teams, shared)
		for _, input := range []struct {
			host           collaboration.HostID
			external, name string
		}{{"bad", "x", "x"}, {saved.Hosts[0].ID, "", "x"}, {saved.Hosts[0].ID, "x\n", "x"}, {saved.Hosts[0].ID, strings.Repeat("x", 513), "x"}, {saved.Hosts[0].ID, "x", strings.Repeat("x", 513)}} {
			if _, err := s.ObserveCollaborationTeam(ctx, input.host, input.external, input.name); err == nil {
				t.Fatal("invalid team observation admitted")
			}
		}
		if _, err := s.ObserveCollaborationTeam(ctx, collaboration.HostID(collaboration.NewID()), "unknown-host", ""); !errors.Is(err, collaboration.ErrIdentityConflict) {
			t.Fatal("missing host identity was accepted", err)
		}
		if _, err := s.CollaborationTeam(ctx, 0); !errors.Is(err, ErrNotFound) {
			t.Fatal("missing team was invented", err)
		}
		for _, query := range []string{"UPDATE collaboration_teams SET id=id+100000 WHERE id=$1", "UPDATE collaboration_teams SET host='ffffffffffffffffffffffffffffffff' WHERE id=$1", "UPDATE collaboration_teams SET external_id='changed' WHERE id=$1", "DELETE FROM collaboration_teams WHERE id=$1"} {
			if _, err := s.db.ExecContext(ctx, query, ref.ID); err == nil {
				t.Fatal("team identity guard admitted rebinding/deletion")
			}
		}
		if _, err := s.db.ExecContext(ctx, "INSERT INTO collaboration_teams(host,external_id,name) VALUES($1,$2,'duplicate')", ref.Host, ref.ExternalID); err == nil {
			t.Fatal("duplicate host/team identity admitted")
		}
		if _, err := s.db.ExecContext(ctx, "SELECT setval(pg_get_serial_sequence('collaboration_teams','id'),9007199254740992,true)"); err != nil {
			t.Fatal(err)
		}
		large, err := s.ObserveCollaborationTeam(ctx, saved.Hosts[0].ID, "opaque-large-team", "")
		if err != nil || large.ID != 9007199254740993 {
			t.Fatal("wide canonical team ID lost", err)
		}
		raw, err := json.Marshal(large)
		if err != nil || !bytes.Contains(raw, []byte(`"id":"9007199254740993"`)) {
			t.Fatal("team JSON lost integer precision", err)
		}
		saved.Teams = append(saved.Teams, large)
		// Existing partial guards must fail closed, rather than being repaired
		// into silently weaker identity or allocation semantics on Open.
		for _, trigger := range []string{"collaboration_teams_identity", "collaboration_teams_allocation"} {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE collaboration_teams DISABLE TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
			current, err := Open(dsn)
			if current != nil {
				current.Close()
			}
			if err == nil {
				t.Fatal("store opened with disabled team guard")
			}
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE collaboration_teams ENABLE TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
		}
		raw, err = json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("persist team registry proof", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("load retained team proof", err)
		}
	}
	for _, expected := range saved.Teams {
		actual, err := s.CollaborationTeam(ctx, expected.ID)
		if err != nil || actual != expected {
			t.Fatal("retained team identity changed", phase, err)
		}
		actual, err = s.CollaborationTeamOnHost(ctx, expected.Host, expected.ID)
		if err != nil || actual != expected {
			t.Fatal("host-scoped team lookup changed identity", phase, err)
		}
		actual, err = s.CollaborationTeamByExternalID(ctx, expected.Host, expected.ExternalID)
		if err != nil || actual != expected {
			t.Fatal("opaque team lookup changed identity", phase, err)
		}
		foreign := saved.Hosts[0].ID
		if expected.Host == foreign {
			foreign = saved.Hosts[1].ID
		}
		if _, err := s.CollaborationTeamOnHost(ctx, foreign, expected.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign host selected canonical team", phase, err)
		}
		actual, err = s.ObserveCollaborationTeam(ctx, expected.Host, expected.ExternalID, "")
		if err != nil || actual != expected {
			t.Fatal("retained team observation forked identity", phase, err)
		}
	}
	var before, after string
	if err := s.db.QueryRowContext(ctx, "SELECT last_value::text||':'||is_called::text FROM collaboration_teams_id_seq").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollaborationTeamByExternalID(ctx, saved.Hosts[1].ID, "provider-team-node:org/完整 🌋"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign opaque team lookup allocated or inferred identity", phase, err)
	}
	if _, err := s.CollaborationTeamByExternalID(ctx, saved.Hosts[0].ID, "missing-team"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing team lookup invented identity", phase, err)
	}
	if _, err := s.CollaborationTeamOnHost(ctx, "invalid", saved.Teams[0].ID); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatal("invalid lookup scope admitted", phase, err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT last_value::text||':'||is_called::text FROM collaboration_teams_id_seq").Scan(&after); err != nil || before != after {
		t.Fatal("team lookup consumed allocator", phase, err)
	}
	actor, err := s.CollaborationActor(ctx, saved.Actor.ID)
	if err != nil || actor != saved.Actor {
		t.Fatal("team registry aliased actor namespace", phase, err)
	}
	retained, err := s.HumanStateCapture(ctx, saved.Retained.Request)
	if err != nil || !bytes.Equal(retained.ProviderIntent, saved.Retained.ProviderIntent) || retained.Authority != saved.Retained.Authority || retained.OperationID != saved.Retained.OperationID {
		t.Fatal("team expansion changed retained immutable command", phase, err)
	}
	t.Logf("PASS %s: canonical teams use separate host-scoped identity, exact opaque provider IDs and names, concurrent observation and immutable guards; retained actor/repository/command and wide IDs survive", phase)
}

type openedTeam struct {
	ref collaboration.TeamRef
	err error
}

func checkNativeHumanThreadJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Inline   factory.HumanInlineCapture
		Review   factory.HumanReviewCapture
		State    factory.HumanStateCapture
		Capture  factory.HumanThreadCapture
		Variants []factory.HumanThreadCapture
		Lock     factory.HumanLockCapture
		Creation factory.HumanIssueCreateCapture
		Comment  factory.HumanCommentCapture
		Edit     factory.HumanEditCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanThreadCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://thread-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/review-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		if _, err := s.PutCollaborationHost(ctx, other); err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "420", "other-human")
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("Retained original 🌋\r\n", 1000)
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: body}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Edit = factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: &body}, Authority: "retained-edit-installation", OperationID: "retained-edit-operation", ProviderIntent: []byte("{ \"old_edit\": true }\n")}
		if _, _, err := s.CaptureHumanEdit(ctx, saved.Edit); err != nil {
			t.Fatal(err)
		}
		saved.Creation = factory.HumanIssueCreateCapture{Request: factory.HumanIssueCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Title: "Retained creation 🌋", Body: body}, Authority: "retained-create-installation", OperationID: "retained-create-operation", ProviderIntent: []byte("{ \"old_create\": true }\n")}
		if _, _, err := s.CaptureHumanIssueCreate(ctx, saved.Creation); err != nil {
			t.Fatal(err)
		}
		saved.Lock = factory.HumanLockCapture{Request: factory.HumanLockCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 2, Lock: true, Reason: body}, Authority: "retained-lock-installation", OperationID: "retained-lock-operation", ProviderIntent: []byte("{ \"old_lock\": true }\n")}
		if _, _, err := s.CaptureHumanLock(ctx, saved.Lock); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-thread-journal", Principal: "420", Payload: "{\"retained\":\"原文\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, _, err := recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		saved.State = factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 1, Close: true}, Authority: "retained-state-installation", OperationID: "retained-state-operation", ProviderIntent: []byte("{ \"old_state\": true }\n")}
		if _, _, err := s.CaptureHumanState(ctx, saved.State); err != nil {
			t.Fatal(err)
		}
		saved.Review = factory.HumanReviewCapture{Request: factory.HumanReviewCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 1, Event: factory.HumanReviewDraft, Body: body}, Authority: "retained-review-installation", OperationID: "retained-review-operation", ProviderIntent: []byte("{ \"old_review\": true }\n")}
		if _, _, err := s.CaptureHumanReview(ctx, saved.Review); err != nil {
			t.Fatal(err)
		}
		saved.Inline = factory.HumanInlineCapture{Request: factory.HumanInlineCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 1, Anchor: factory.ReviewThreadAnchor{Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40), Path: "old.txt", Side: "old", Scope: "file"}, Draft: true, Body: body}, Authority: "retained-inline-installation", OperationID: "retained-inline-operation", ProviderIntent: []byte("{ \"old_inline\": true }\n")}
		if _, _, err := s.CaptureHumanInline(ctx, saved.Inline); err != nil {
			t.Fatal(err)
		}
		// Restore schema 76's exact bound only in this new private fixture.
		restorePreDispatchCommandFixture(t, s.db)
		for _, statement := range HumanInlineJournalUpgradeDDL {
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-76 thread upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("thread upgrade marker", version, err)
		}
		preparation := opaqueHumanPreparationFixture(t, factory.MaxHumanThreadProviderIntentBytes)
		selection := factory.ReviewThreadAnchor{Base: strings.Repeat("c", 40), Head: strings.Repeat("a", 40), Path: strings.Repeat("<", 4096), Side: "new", Scope: "lines", StartLine: 1, EndLine: 9007199254740991}
		capture := factory.HumanThreadCapture{Request: factory.HumanThreadCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 1, ThreadID: strings.Repeat("<", 1024), Anchor: &selection, Resolved: true}, Authority: "thread-installation-retained", OperationID: "thread-operation-retained", ProviderIntent: preparation}

		if _, err := s.HumanThreadCapture(ctx, capture.Request); !errors.Is(err, ErrNotFound) {
			t.Fatal("absent review capture invented", err)
		}
		for _, mutate := range []func(*factory.HumanThreadCapture){
			func(c *factory.HumanThreadCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanThreadCapture) { c.Request.Actor = foreignActor.ID },
		} {
			foreign := capture
			foreign.Request.ID = factory.NewID()
			mutate(&foreign)
			if _, _, err := s.CaptureHumanThread(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign review capture admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign capture left command", err)
			}
		}
		got, replay, err := s.CaptureHumanThread(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial thread capture", replay, err)
		}
		saved.Capture = got
		for _, resolved := range []bool{true, false} {
			for _, anchor := range []*factory.ReviewThreadAnchor{nil,
				{Base: selection.Base, Head: selection.Head, Path: "rename-old.txt", Side: "old", Scope: "file"},
				{Base: selection.Base, Head: selection.Head, Path: "rename-new.txt", Side: "new", Scope: "file"},
				{Base: selection.Base, Head: selection.Head, Path: "literal[*].txt", Side: "new", Scope: "lines", StartLine: 4, EndLine: 6},
				{Base: selection.Base, Head: selection.Head, Path: "literal[*].txt", Side: "new", Scope: "lines", StartLine: 5, EndLine: 6},
				{Base: strings.Repeat("d", 40), Head: strings.Repeat("e", 40), Path: "historical.txt", Side: "old", Scope: "lines", StartLine: 2, EndLine: 3},
			} {
				c := capture
				c.Request.ID = factory.NewID()
				c.Request.Resolved = resolved
				c.Request.Anchor = anchor
				c.OperationID = "thread-variant-" + c.Request.ID
				c.ProviderIntent = []byte("{\"x\":\"" + strings.Repeat("x", factory.MaxHumanThreadProviderIntentBytes-10) + "\"}\n ")
				if len(c.ProviderIntent) != factory.MaxHumanThreadProviderIntentBytes {
					t.Fatal("thread preparation boundary differs")
				}
				got, replay, err := s.CaptureHumanThread(ctx, c)
				if err != nil || replay || !sameCapture(got, c) {
					t.Fatal("thread variant capture", resolved, err)
				}
				saved.Variants = append(saved.Variants, got)
			}
		}
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-review-left", "race-review-right"
		right.Authority = "other-race-preparation"
		right.ProviderIntent = []byte("{ \"native_revision\": 2, \"not_after\": 2 }\n")
		type result struct {
			capture factory.HumanThreadCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanThreadCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanThread(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent thread preparation differs", a.err, b.err)
		}
		saved.Variants = append(saved.Variants, a.capture)
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", capture.Request.ID); err == nil {
			t.Fatal("thread payload mutable")
		}
		for _, typ := range []string{factory.CommandRetry, factory.CommandHumanState, factory.CommandHumanComment, factory.CommandHumanEdit, factory.CommandHumanIssueCreate, factory.CommandHumanLock, factory.CommandHumanReview, factory.CommandHumanInline, factory.CommandHumanThread} {
			limit := factory.MaxHumanInlineCaptureBytes
			if typ == factory.CommandHumanThread {
				limit = factory.MaxHumanThreadCaptureBytes
			}
			if typ == factory.CommandRetry || typ == factory.CommandHumanState {
				limit = 8192
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), typ, strings.Repeat("x", limit+1)); err == nil {
				t.Fatal("SQL payload limit missing", typ)
			}
		}
		for _, id := range []string{saved.Comment.Request.ID, saved.Edit.Request.ID, saved.Creation.Request.ID, saved.Lock.Request.ID, saved.State.Request.ID, saved.Review.Request.ID, saved.Inline.Request.ID, saved.Legacy.ID} {
			collision := capture
			collision.Request.ID = id
			if _, _, err := s.CaptureHumanThread(ctx, collision); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("thread replaced another command kind", err)
			}
			if _, err := s.HumanThreadCapture(ctx, collision.Request); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("thread read another command kind", err)
			}
		}
		for _, mutate := range []func(*factory.HumanThreadCapture){
			func(c *factory.HumanThreadCapture) { c.Request.ThreadID = "" }, func(c *factory.HumanThreadCapture) { c.Request.ThreadID = strings.Repeat("x", 1025) }, func(c *factory.HumanThreadCapture) { c.Request.ThreadID = "nul\x00thread" }, func(c *factory.HumanThreadCapture) { c.Request.ThreadID = string([]byte{0xff}) },
			func(c *factory.HumanThreadCapture) { c.Request.Anchor.Scope = "unknown" }, func(c *factory.HumanThreadCapture) { c.Request.Anchor.Base = "unknown" }, func(c *factory.HumanThreadCapture) { c.Request.Anchor.Path = "../escape" }, func(c *factory.HumanThreadCapture) { c.Request.Anchor.Side = "other" }, func(c *factory.HumanThreadCapture) { c.Request.Anchor.StartLine = 0 }, func(c *factory.HumanThreadCapture) { c.Request.Anchor.EndLine = 9007199254740992 }, func(c *factory.HumanThreadCapture) { c.Request.Anchor.Scope = "file" },
			func(c *factory.HumanThreadCapture) { c.Request.Number = 0 }, func(c *factory.HumanThreadCapture) { c.Authority = "" }, func(c *factory.HumanThreadCapture) { c.OperationID = "" }, func(c *factory.HumanThreadCapture) { c.ProviderIntent = []byte("invalid JSON") }, func(c *factory.HumanThreadCapture) {
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", factory.MaxHumanThreadProviderIntentBytes) + `"`)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			anchor := *capture.Request.Anchor
			bad.Request.Anchor = &anchor
			mutate(&bad)
			if _, _, err := s.CaptureHumanThread(ctx, bad); err == nil {
				t.Fatal("invalid thread capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid thread left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save review identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained review identity unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanThreadCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanThreadCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained thread preparation differs", err)
		}
		fresh := capture
		fresh.Authority, fresh.OperationID = "replacement", "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanThread(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("thread replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" || len(cmd.Payload) > factory.MaxHumanThreadCaptureBytes {
			t.Fatal("capture invented an outcome or exceeded review bound", err)
		}
		if cmd.Target != string(capture.Request.Host)+":"+strconv.FormatInt(int64(capture.Request.Repository), 10)+":pull:"+strconv.FormatInt(capture.Request.Number, 10) {
			t.Fatal("thread target differs")
		}
		bound, err := factory.DecodeHumanThreadCapture(cmd)
		if err != nil || !sameCapture(bound, capture) {
			t.Fatal("thread command binding differs", err)
		}
	}
	for _, change := range []func(*factory.HumanThreadCommand){
		func(q *factory.HumanThreadCommand) { q.Resolved = !q.Resolved }, func(q *factory.HumanThreadCommand) { q.ThreadID = "other-thread" },
		func(q *factory.HumanThreadCommand) { q.Number++ }, func(q *factory.HumanThreadCommand) { q.Actor++ }, func(q *factory.HumanThreadCommand) { q.Repository++ }, func(q *factory.HumanThreadCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
		func(q *factory.HumanThreadCommand) { q.Anchor.Base = strings.Repeat("b", 40) }, func(q *factory.HumanThreadCommand) { q.Anchor.Head = strings.Repeat("e", 40) }, func(q *factory.HumanThreadCommand) { q.Anchor.Path = "other.txt" }, func(q *factory.HumanThreadCommand) { q.Anchor.Side = "old" }, func(q *factory.HumanThreadCommand) { q.Anchor.StartLine++ }, func(q *factory.HumanThreadCommand) { q.Anchor.EndLine-- }, func(q *factory.HumanThreadCommand) {
			q.Anchor.Scope = "file"
			q.Anchor.StartLine = 0
			q.Anchor.EndLine = 0
		}, func(q *factory.HumanThreadCommand) { q.Anchor = nil },
	} {
		q := saved.Capture.Request
		anchor := *q.Anchor
		q.Anchor = &anchor
		change(&q)
		if _, err := s.HumanThreadCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed thread replay admitted", err)
		}
		changed := saved.Capture
		changed.Request = q
		if _, _, err := s.CaptureHumanThread(ctx, changed); !errors.Is(err, ErrCommandConflict) && !errors.Is(err, collaboration.ErrUnboundLedger) {
			t.Fatal("changed thread capture admitted", err)
		}
	}
	inline, err := s.HumanInlineCapture(ctx, saved.Inline.Request)
	if err != nil || !inline.Request.SameRequest(saved.Inline.Request) || inline.Authority != saved.Inline.Authority || inline.OperationID != saved.Inline.OperationID || !bytes.Equal(inline.ProviderIntent, saved.Inline.ProviderIntent) {
		t.Fatal("thread journal changed retained inline", err)
	}
	review, err := s.HumanReviewCapture(ctx, saved.Review.Request)
	if err != nil || !review.Request.SameRequest(saved.Review.Request) || review.Authority != saved.Review.Authority || review.OperationID != saved.Review.OperationID || !bytes.Equal(review.ProviderIntent, saved.Review.ProviderIntent) {
		t.Fatal("thread journal changed retained review", err)
	}
	state, err := s.HumanStateCapture(ctx, saved.State.Request)
	if err != nil || !state.Request.SameRequest(saved.State.Request) || state.Authority != saved.State.Authority || state.OperationID != saved.State.OperationID || !bytes.Equal(state.ProviderIntent, saved.State.ProviderIntent) {
		t.Fatal("review journal changed retained state", err)
	}
	lock, err := s.HumanLockCapture(ctx, saved.Lock.Request)
	if err != nil || !lock.Request.SameRequest(saved.Lock.Request) || lock.Authority != saved.Lock.Authority || lock.OperationID != saved.Lock.OperationID || !bytes.Equal(lock.ProviderIntent, saved.Lock.ProviderIntent) {
		t.Fatal("review journal changed retained lock", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("review journal changed retained comment", err)
	}
	edit, err := s.HumanEditCapture(ctx, saved.Edit.Request)
	if err != nil || !edit.Request.SameRequest(saved.Edit.Request) || edit.Authority != saved.Edit.Authority || edit.OperationID != saved.Edit.OperationID || !bytes.Equal(edit.ProviderIntent, saved.Edit.ProviderIntent) {
		t.Fatal("review journal changed retained edit", err)
	}
	creation, err := s.HumanIssueCreateCapture(ctx, saved.Creation.Request)
	if err != nil || !creation.Request.SameRequest(saved.Creation.Request) || creation.Authority != saved.Creation.Authority || creation.OperationID != saved.Creation.OperationID || !bytes.Equal(creation.ProviderIntent, saved.Creation.ProviderIntent) {
		t.Fatal("review journal changed retained issue creation", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("review journal changed retained settings", err)
	}
	t.Logf("%s: exact resolve/unresolve full/unknown file/side/range/tree anchors and opaque thread identity; full 64 KiB proof, schema-76 upgrade, immutable 128 KiB thread captures, concurrent winner, exact replay/conflicts and unchanged prior commands; no provider dispatch", phase)
}

func checkNativeHumanInlineJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Review   factory.HumanReviewCapture
		State    factory.HumanStateCapture
		Capture  factory.HumanInlineCapture
		Variants []factory.HumanInlineCapture
		Lock     factory.HumanLockCapture
		Creation factory.HumanIssueCreateCapture
		Comment  factory.HumanCommentCapture
		Edit     factory.HumanEditCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanInlineCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://inline-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/review-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		if _, err := s.PutCollaborationHost(ctx, other); err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "420", "other-human")
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("Retained original 🌋\r\n", 1000)
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: body}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Edit = factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: &body}, Authority: "retained-edit-installation", OperationID: "retained-edit-operation", ProviderIntent: []byte("{ \"old_edit\": true }\n")}
		if _, _, err := s.CaptureHumanEdit(ctx, saved.Edit); err != nil {
			t.Fatal(err)
		}
		saved.Creation = factory.HumanIssueCreateCapture{Request: factory.HumanIssueCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Title: "Retained creation 🌋", Body: body}, Authority: "retained-create-installation", OperationID: "retained-create-operation", ProviderIntent: []byte("{ \"old_create\": true }\n")}
		if _, _, err := s.CaptureHumanIssueCreate(ctx, saved.Creation); err != nil {
			t.Fatal(err)
		}
		saved.Lock = factory.HumanLockCapture{Request: factory.HumanLockCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 2, Lock: true, Reason: body}, Authority: "retained-lock-installation", OperationID: "retained-lock-operation", ProviderIntent: []byte("{ \"old_lock\": true }\n")}
		if _, _, err := s.CaptureHumanLock(ctx, saved.Lock); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-inline-journal", Principal: "420", Payload: "{\"retained\":\"原文\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, _, err := recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		saved.State = factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 1, Close: true}, Authority: "retained-state-installation", OperationID: "retained-state-operation", ProviderIntent: []byte("{ \"old_state\": true }\n")}
		if _, _, err := s.CaptureHumanState(ctx, saved.State); err != nil {
			t.Fatal(err)
		}
		saved.Review = factory.HumanReviewCapture{Request: factory.HumanReviewCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 1, Event: factory.HumanReviewDraft, Body: body}, Authority: "retained-review-installation", OperationID: "retained-review-operation", ProviderIntent: []byte("{ \"old_review\": true }\n")}
		if _, _, err := s.CaptureHumanReview(ctx, saved.Review); err != nil {
			t.Fatal(err)
		}
		// Restore schema 75's exact bound only in this new private fixture.
		restorePreDispatchCommandFixture(t, s.db)
		for _, statement := range HumanReviewJournalUpgradeDDL {
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-75 inline upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("inline upgrade marker", version, err)
		}
		preparation := opaqueHumanPreparationFixture(t, factory.MaxHumanInlineProviderIntentBytes)
		selection := factory.ReviewThreadAnchor{Base: strings.Repeat("c", 40), Head: strings.Repeat("a", 40), Path: strings.Repeat("<", 4096), Side: "new", Scope: "lines", StartLine: 1, EndLine: 9007199254740991}
		capture := factory.HumanInlineCapture{Request: factory.HumanInlineCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 1, Anchor: selection, Draft: true, Body: strings.Repeat("<", factory.MaxHumanInlineBytes-len("完整 🌋\r\n")) + "完整 🌋\r\n"}, Authority: "inline-installation-retained", OperationID: "inline-operation-retained", ProviderIntent: preparation}

		if _, err := s.HumanInlineCapture(ctx, capture.Request); !errors.Is(err, ErrNotFound) {
			t.Fatal("absent review capture invented", err)
		}
		for _, mutate := range []func(*factory.HumanInlineCapture){
			func(c *factory.HumanInlineCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanInlineCapture) { c.Request.Actor = foreignActor.ID },
		} {
			foreign := capture
			foreign.Request.ID = factory.NewID()
			mutate(&foreign)
			if _, _, err := s.CaptureHumanInline(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign review capture admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign capture left command", err)
			}
		}
		got, replay, err := s.CaptureHumanInline(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial inline capture", replay, err)
		}
		saved.Capture = got
		for _, draft := range []bool{true, false} {
			for _, anchor := range []factory.ReviewThreadAnchor{
				{Base: selection.Base, Head: selection.Head, Path: "rename-old.txt", Side: "old", Scope: "file"},
				{Base: selection.Base, Head: selection.Head, Path: "rename-new.txt", Side: "new", Scope: "file"},
				{Base: selection.Base, Head: selection.Head, Path: "literal[*].txt", Side: "new", Scope: "lines", StartLine: 4, EndLine: 6},
				{Base: selection.Base, Head: selection.Head, Path: "literal[*].txt", Side: "new", Scope: "lines", StartLine: 5, EndLine: 6},
			} {
				c := capture
				c.Request.ID = factory.NewID()
				c.Request.Draft = draft
				c.Request.Anchor = anchor
				c.OperationID = "inline-variant-" + c.Request.ID
				c.ProviderIntent = []byte("{\"x\":\"" + strings.Repeat("x", factory.MaxHumanInlineProviderIntentBytes-10) + "\"}\n ")
				if len(c.ProviderIntent) != factory.MaxHumanInlineProviderIntentBytes {
					t.Fatal("inline preparation boundary differs")
				}
				got, replay, err := s.CaptureHumanInline(ctx, c)
				if err != nil || replay || !sameCapture(got, c) {
					t.Fatal("inline variant capture", draft, err)
				}
				saved.Variants = append(saved.Variants, got)
			}
		}
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-review-left", "race-review-right"
		right.Authority = "other-race-preparation"
		right.ProviderIntent = []byte("{ \"native_revision\": 2, \"not_after\": 2 }\n")
		type result struct {
			capture factory.HumanInlineCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanInlineCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanInline(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent inline preparation differs", a.err, b.err)
		}
		saved.Variants = append(saved.Variants, a.capture)
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", capture.Request.ID); err == nil {
			t.Fatal("inline payload mutable")
		}
		for _, typ := range []string{factory.CommandRetry, factory.CommandHumanState, factory.CommandHumanComment, factory.CommandHumanEdit, factory.CommandHumanIssueCreate, factory.CommandHumanLock, factory.CommandHumanReview, factory.CommandHumanInline} {
			limit := factory.MaxHumanInlineCaptureBytes
			if typ == factory.CommandRetry || typ == factory.CommandHumanState {
				limit = 8192
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), typ, strings.Repeat("x", limit+1)); err == nil {
				t.Fatal("SQL payload limit missing", typ)
			}
		}
		for _, id := range []string{saved.Comment.Request.ID, saved.Edit.Request.ID, saved.Creation.Request.ID, saved.Lock.Request.ID, saved.State.Request.ID, saved.Review.Request.ID, saved.Legacy.ID} {
			collision := capture
			collision.Request.ID = id
			if _, _, err := s.CaptureHumanInline(ctx, collision); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("inline replaced another command kind", err)
			}
			if _, err := s.HumanInlineCapture(ctx, collision.Request); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("inline read another command kind", err)
			}
		}
		for _, mutate := range []func(*factory.HumanInlineCapture){
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.Scope = "unknown" },
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.Base = "unknown" },
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.Path = "../escape" },
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.Side = "other" },
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.StartLine = 0 },
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.EndLine = 9007199254740992 },
			func(c *factory.HumanInlineCapture) { c.Request.Anchor.Scope = "file" },
			func(c *factory.HumanInlineCapture) {
				c.Request.Body = strings.Repeat("x", factory.MaxHumanInlineBytes+1)
			},
			func(c *factory.HumanInlineCapture) { c.Request.Body = "nul\x00review" },
			func(c *factory.HumanInlineCapture) { c.Request.Body = string([]byte{0xff}) },
			func(c *factory.HumanInlineCapture) {
				c.Request.Body = " \r\n"
			},
			func(c *factory.HumanInlineCapture) { c.Request.Number = 0 },
			func(c *factory.HumanInlineCapture) { c.Authority = "" },
			func(c *factory.HumanInlineCapture) { c.OperationID = "" },
			func(c *factory.HumanInlineCapture) { c.ProviderIntent = []byte("invalid JSON") },
			func(c *factory.HumanInlineCapture) {
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", factory.MaxHumanInlineProviderIntentBytes) + `"`)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanInline(ctx, bad); err == nil {
				t.Fatal("invalid inline capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid inline left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save review identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained review identity unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanInlineCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanInlineCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained inline preparation differs", err)
		}
		fresh := capture
		fresh.Authority, fresh.OperationID = "replacement", "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanInline(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("inline replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" || len(cmd.Payload) > factory.MaxHumanInlineCaptureBytes {
			t.Fatal("capture invented an outcome or exceeded review bound", err)
		}
		if cmd.Target != string(capture.Request.Host)+":"+strconv.FormatInt(int64(capture.Request.Repository), 10)+":pull:"+strconv.FormatInt(capture.Request.Number, 10) {
			t.Fatal("inline target differs")
		}
		bound, err := factory.DecodeHumanInlineCapture(cmd)
		if err != nil || !sameCapture(bound, capture) {
			t.Fatal("inline command binding differs", err)
		}
	}
	for _, change := range []func(*factory.HumanInlineCommand){
		func(q *factory.HumanInlineCommand) { q.Draft = !q.Draft }, func(q *factory.HumanInlineCommand) { q.Body = "Changed inline prose" },
		func(q *factory.HumanInlineCommand) { q.Number++ }, func(q *factory.HumanInlineCommand) { q.Actor++ }, func(q *factory.HumanInlineCommand) { q.Repository++ }, func(q *factory.HumanInlineCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
		func(q *factory.HumanInlineCommand) { q.Anchor.Base = strings.Repeat("b", 40) }, func(q *factory.HumanInlineCommand) { q.Anchor.Head = strings.Repeat("e", 40) },
		func(q *factory.HumanInlineCommand) { q.Anchor.Path = "other.txt" }, func(q *factory.HumanInlineCommand) { q.Anchor.Side = "old" },
		func(q *factory.HumanInlineCommand) { q.Anchor.StartLine++ }, func(q *factory.HumanInlineCommand) { q.Anchor.EndLine-- },
		func(q *factory.HumanInlineCommand) {
			q.Anchor.Scope = "file"
			q.Anchor.StartLine = 0
			q.Anchor.EndLine = 0
		},
	} {
		q := saved.Capture.Request
		change(&q)
		if _, err := s.HumanInlineCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed inline replay admitted", err)
		}
		changed := saved.Capture
		changed.Request = q
		if _, _, err := s.CaptureHumanInline(ctx, changed); !errors.Is(err, ErrCommandConflict) && !errors.Is(err, collaboration.ErrUnboundLedger) {
			t.Fatal("changed inline capture admitted", err)
		}
	}
	review, err := s.HumanReviewCapture(ctx, saved.Review.Request)
	if err != nil || !review.Request.SameRequest(saved.Review.Request) || review.Authority != saved.Review.Authority || review.OperationID != saved.Review.OperationID || !bytes.Equal(review.ProviderIntent, saved.Review.ProviderIntent) {
		t.Fatal("inline journal changed retained review", err)
	}
	state, err := s.HumanStateCapture(ctx, saved.State.Request)
	if err != nil || !state.Request.SameRequest(saved.State.Request) || state.Authority != saved.State.Authority || state.OperationID != saved.State.OperationID || !bytes.Equal(state.ProviderIntent, saved.State.ProviderIntent) {
		t.Fatal("review journal changed retained state", err)
	}
	lock, err := s.HumanLockCapture(ctx, saved.Lock.Request)
	if err != nil || !lock.Request.SameRequest(saved.Lock.Request) || lock.Authority != saved.Lock.Authority || lock.OperationID != saved.Lock.OperationID || !bytes.Equal(lock.ProviderIntent, saved.Lock.ProviderIntent) {
		t.Fatal("review journal changed retained lock", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("review journal changed retained comment", err)
	}
	edit, err := s.HumanEditCapture(ctx, saved.Edit.Request)
	if err != nil || !edit.Request.SameRequest(saved.Edit.Request) || edit.Authority != saved.Edit.Authority || edit.OperationID != saved.Edit.OperationID || !bytes.Equal(edit.ProviderIntent, saved.Edit.ProviderIntent) {
		t.Fatal("review journal changed retained edit", err)
	}
	creation, err := s.HumanIssueCreateCapture(ctx, saved.Creation.Request)
	if err != nil || !creation.Request.SameRequest(saved.Creation.Request) || creation.Authority != saved.Creation.Authority || creation.OperationID != saved.Creation.OperationID || !bytes.Equal(creation.ProviderIntent, saved.Creation.ProviderIntent) {
		t.Fatal("review journal changed retained issue creation", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("review journal changed retained settings", err)
	}
	t.Logf("%s: exact draft/immediate full file/side/range/tree anchors, 64 KiB prose and full 512 KiB opaque proof; schema-75 upgrade, immutable 2 MiB inline captures, concurrent winner, exact replay/conflicts and unchanged prior commands; no provider dispatch", phase)
}

func checkNativeHumanReviewJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		State    factory.HumanStateCapture
		Capture  factory.HumanReviewCapture
		Variants []factory.HumanReviewCapture
		Lock     factory.HumanLockCapture
		Creation factory.HumanIssueCreateCapture
		Comment  factory.HumanCommentCapture
		Edit     factory.HumanEditCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanReviewCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://review-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/review-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		if _, err := s.PutCollaborationHost(ctx, other); err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "420", "other-human")
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("Retained original 🌋\r\n", 1000)
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: body}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Edit = factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: &body}, Authority: "retained-edit-installation", OperationID: "retained-edit-operation", ProviderIntent: []byte("{ \"old_edit\": true }\n")}
		if _, _, err := s.CaptureHumanEdit(ctx, saved.Edit); err != nil {
			t.Fatal(err)
		}
		saved.Creation = factory.HumanIssueCreateCapture{Request: factory.HumanIssueCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Title: "Retained creation 🌋", Body: body}, Authority: "retained-create-installation", OperationID: "retained-create-operation", ProviderIntent: []byte("{ \"old_create\": true }\n")}
		if _, _, err := s.CaptureHumanIssueCreate(ctx, saved.Creation); err != nil {
			t.Fatal(err)
		}
		saved.Lock = factory.HumanLockCapture{Request: factory.HumanLockCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 2, Lock: true, Reason: body}, Authority: "retained-lock-installation", OperationID: "retained-lock-operation", ProviderIntent: []byte("{ \"old_lock\": true }\n")}
		if _, _, err := s.CaptureHumanLock(ctx, saved.Lock); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-review-journal", Principal: "420", Payload: "{\"retained\":\"原文\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, _, err := recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		saved.State = factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 1, Close: true}, Authority: "retained-state-installation", OperationID: "retained-state-operation", ProviderIntent: []byte("{ \"old_state\": true }\n")}
		if _, _, err := s.CaptureHumanState(ctx, saved.State); err != nil {
			t.Fatal(err)
		}
		// Restore the exact schema-74 payload rule in this owned fixture only.
		restorePreDispatchCommandFixture(t, s.db)
		for _, statement := range []string{
			"ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check",
			"ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create','discussion.lock.apply') AND char_length(payload)<=8192))",
			"UPDATE schema_version SET version=74",
		} {
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-74 review upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("review upgrade marker", version, err)
		}
		preparation := opaqueHumanPreparationFixture(t, factory.MaxHumanReviewProviderIntentBytes)
		capture := factory.HumanReviewCapture{Request: factory.HumanReviewCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Number: 1, Event: factory.HumanReviewDraft, Body: strings.Repeat("<", factory.MaxHumanReviewBytes-len("完整 🌋\r\n")) + "完整 🌋\r\n"}, Authority: "review-installation-retained", OperationID: "review-operation-retained", ProviderIntent: preparation}
		if _, err := s.HumanReviewCapture(ctx, capture.Request); !errors.Is(err, ErrNotFound) {
			t.Fatal("absent review capture invented", err)
		}
		for _, mutate := range []func(*factory.HumanReviewCapture){
			func(c *factory.HumanReviewCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanReviewCapture) { c.Request.Actor = foreignActor.ID },
		} {
			foreign := capture
			foreign.Request.ID = factory.NewID()
			mutate(&foreign)
			if _, _, err := s.CaptureHumanReview(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign review capture admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign capture left command", err)
			}
		}
		got, replay, err := s.CaptureHumanReview(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial review capture", replay, err)
		}
		saved.Capture = got
		for _, event := range []factory.HumanReviewEvent{factory.HumanReviewDraft, factory.HumanReviewComment, factory.HumanReviewApprove, factory.HumanReviewRequestChanges} {
			for _, body := range []string{"", "Original review prose 完整 🌋\r\n"} {
				if event == factory.HumanReviewRequestChanges && body == "" {
					continue
				}
				c := capture
				c.Request.ID = factory.NewID()
				c.Request.Event, c.Request.Body = event, body
				c.OperationID = "review-variant-" + c.Request.ID
				c.ProviderIntent = []byte("{\"x\":\"" + strings.Repeat("x", factory.MaxHumanReviewProviderIntentBytes-10) + "\"}\n ")
				if len(c.ProviderIntent) != factory.MaxHumanReviewProviderIntentBytes {
					t.Fatal("review preparation boundary differs")
				}
				got, replay, err := s.CaptureHumanReview(ctx, c)
				if err != nil || replay || !sameCapture(got, c) {
					t.Fatal("review variant capture", event, err)
				}
				saved.Variants = append(saved.Variants, got)
			}
		}
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-review-left", "race-review-right"
		right.Authority = "other-race-preparation"
		right.ProviderIntent = []byte("{ \"native_revision\": 2, \"not_after\": 2 }\n")
		type result struct {
			capture factory.HumanReviewCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanReviewCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanReview(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent review preparation differs", a.err, b.err)
		}
		saved.Variants = append(saved.Variants, a.capture)
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", capture.Request.ID); err == nil {
			t.Fatal("review payload mutable")
		}
		for _, typ := range []string{factory.CommandRetry, factory.CommandHumanState, factory.CommandHumanComment, factory.CommandHumanEdit, factory.CommandHumanIssueCreate, factory.CommandHumanLock, factory.CommandHumanReview} {
			limit := factory.MaxHumanReviewCaptureBytes
			if typ == factory.CommandRetry || typ == factory.CommandHumanState {
				limit = 8192
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), typ, strings.Repeat("x", limit+1)); err == nil {
				t.Fatal("SQL payload limit missing", typ)
			}
		}
		for _, id := range []string{saved.Comment.Request.ID, saved.Edit.Request.ID, saved.Creation.Request.ID, saved.Lock.Request.ID, saved.State.Request.ID, saved.Legacy.ID} {
			collision := capture
			collision.Request.ID = id
			if _, _, err := s.CaptureHumanReview(ctx, collision); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("review replaced another command kind", err)
			}
			if _, err := s.HumanReviewCapture(ctx, collision.Request); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("review read another command kind", err)
			}
		}
		for _, mutate := range []func(*factory.HumanReviewCapture){
			func(c *factory.HumanReviewCapture) { c.Request.Event = "dismiss" },
			func(c *factory.HumanReviewCapture) {
				c.Request.Body = strings.Repeat("x", factory.MaxHumanReviewBytes+1)
			},
			func(c *factory.HumanReviewCapture) { c.Request.Body = "nul\x00review" },
			func(c *factory.HumanReviewCapture) { c.Request.Body = string([]byte{0xff}) },
			func(c *factory.HumanReviewCapture) {
				c.Request.Event = factory.HumanReviewRequestChanges
				c.Request.Body = " \r\n"
			},
			func(c *factory.HumanReviewCapture) { c.Request.Number = 0 },
			func(c *factory.HumanReviewCapture) { c.Authority = "" },
			func(c *factory.HumanReviewCapture) { c.OperationID = "" },
			func(c *factory.HumanReviewCapture) { c.ProviderIntent = []byte("invalid JSON") },
			func(c *factory.HumanReviewCapture) {
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", factory.MaxHumanReviewProviderIntentBytes) + `"`)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanReview(ctx, bad); err == nil {
				t.Fatal("invalid review capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid review left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save review identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained review identity unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanReviewCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanReviewCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained review preparation differs", err)
		}
		fresh := capture
		fresh.Authority, fresh.OperationID = "replacement", "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanReview(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("review replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" || len(cmd.Payload) > factory.MaxHumanReviewCaptureBytes {
			t.Fatal("capture invented an outcome or exceeded review bound", err)
		}
		if cmd.Target != string(capture.Request.Host)+":"+strconv.FormatInt(int64(capture.Request.Repository), 10)+":pull:"+strconv.FormatInt(capture.Request.Number, 10) {
			t.Fatal("review target differs")
		}
		bound, err := factory.DecodeHumanReviewCapture(cmd)
		if err != nil || !sameCapture(bound, capture) {
			t.Fatal("review command binding differs", err)
		}
	}
	for _, change := range []func(*factory.HumanReviewCommand){
		func(q *factory.HumanReviewCommand) { q.Event = factory.HumanReviewApprove },
		func(q *factory.HumanReviewCommand) { q.Body = "Changed review prose" },
		func(q *factory.HumanReviewCommand) { q.Number++ },
		func(q *factory.HumanReviewCommand) { q.Actor++ },
		func(q *factory.HumanReviewCommand) { q.Repository++ },
		func(q *factory.HumanReviewCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
	} {
		q := saved.Capture.Request
		change(&q)
		if _, err := s.HumanReviewCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed review replay admitted", err)
		}
	}
	changed := saved.Capture
	changed.Request.Event = factory.HumanReviewApprove
	if _, _, err := s.CaptureHumanReview(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed review capture admitted", err)
	}
	state, err := s.HumanStateCapture(ctx, saved.State.Request)
	if err != nil || !state.Request.SameRequest(saved.State.Request) || state.Authority != saved.State.Authority || state.OperationID != saved.State.OperationID || !bytes.Equal(state.ProviderIntent, saved.State.ProviderIntent) {
		t.Fatal("review journal changed retained state", err)
	}
	lock, err := s.HumanLockCapture(ctx, saved.Lock.Request)
	if err != nil || !lock.Request.SameRequest(saved.Lock.Request) || lock.Authority != saved.Lock.Authority || lock.OperationID != saved.Lock.OperationID || !bytes.Equal(lock.ProviderIntent, saved.Lock.ProviderIntent) {
		t.Fatal("review journal changed retained lock", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("review journal changed retained comment", err)
	}
	edit, err := s.HumanEditCapture(ctx, saved.Edit.Request)
	if err != nil || !edit.Request.SameRequest(saved.Edit.Request) || edit.Authority != saved.Edit.Authority || edit.OperationID != saved.Edit.OperationID || !bytes.Equal(edit.ProviderIntent, saved.Edit.ProviderIntent) {
		t.Fatal("review journal changed retained edit", err)
	}
	creation, err := s.HumanIssueCreateCapture(ctx, saved.Creation.Request)
	if err != nil || !creation.Request.SameRequest(saved.Creation.Request) || creation.Authority != saved.Creation.Authority || creation.OperationID != saved.Creation.OperationID || !bytes.Equal(creation.ProviderIntent, saved.Creation.ProviderIntent) {
		t.Fatal("review journal changed retained issue creation", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("review journal changed retained settings", err)
	}
	t.Logf("%s: exact draft/comment/approve/request-changes, 64 KiB escaped prose, full 512 KiB opaque preparation; schema-74 upgrade, original replay, concurrent winner, immutable 2 MiB review and unchanged ordinary limits; scope/kind conflicts and retained state/lock/comment/edit/create/settings; no provider dispatch", phase)
}

func checkNativeHumanStateJournal(t *testing.T, s *Store, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Capture  factory.HumanStateCapture
		Variants []factory.HumanStateCapture
		Lock     factory.HumanLockCapture
		Creation factory.HumanIssueCreateCapture
		Comment  factory.HumanCommentCapture
		Edit     factory.HumanEditCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanStateCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://state-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/state-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		if _, err := s.PutCollaborationHost(ctx, other); err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "420", "other-human")
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat("Retained original 🌋\r\n", 1000)
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: body}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Edit = factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: &body}, Authority: "retained-edit-installation", OperationID: "retained-edit-operation", ProviderIntent: []byte("{ \"old_edit\": true }\n")}
		if _, _, err := s.CaptureHumanEdit(ctx, saved.Edit); err != nil {
			t.Fatal(err)
		}
		saved.Creation = factory.HumanIssueCreateCapture{Request: factory.HumanIssueCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Title: "Retained creation 🌋", Body: body}, Authority: "retained-create-installation", OperationID: "retained-create-operation", ProviderIntent: []byte("{ \"old_create\": true }\n")}
		if _, _, err := s.CaptureHumanIssueCreate(ctx, saved.Creation); err != nil {
			t.Fatal(err)
		}
		saved.Lock = factory.HumanLockCapture{Request: factory.HumanLockCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 2, Lock: true, Reason: body}, Authority: "retained-lock-installation", OperationID: "retained-lock-operation", ProviderIntent: []byte("{ \"old_lock\": true }\n")}
		if _, _, err := s.CaptureHumanLock(ctx, saved.Lock); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-state-journal", Principal: "420", Payload: "{\"retained\":\"原文\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, _, err := recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		preparation := opaqueHumanPreparationFixture(t, factory.MaxHumanStateProviderIntentBytes)
		capture := factory.HumanStateCapture{Request: factory.HumanStateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 1, Close: true}, Authority: "state-installation-retained", OperationID: "state-operation-retained", ProviderIntent: preparation}
		if _, err := s.HumanStateCapture(ctx, capture.Request); !errors.Is(err, ErrNotFound) {
			t.Fatal("absent state capture invented", err)
		}
		for _, mutate := range []func(*factory.HumanStateCapture){
			func(c *factory.HumanStateCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanStateCapture) { c.Request.Actor = foreignActor.ID },
		} {
			foreign := capture
			foreign.Request.ID = factory.NewID()
			mutate(&foreign)
			if _, _, err := s.CaptureHumanState(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign state capture admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign capture left command", err)
			}
		}
		got, replay, err := s.CaptureHumanState(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial state capture", replay, err)
		}
		saved.Capture = got
		for _, subject := range []string{"issue", "pull"} {
			for _, close := range []bool{true, false} {
				c := capture
				c.Request.ID = factory.NewID()
				c.Request.Subject, c.Request.Close = subject, close
				c.OperationID = "state-variant-" + c.Request.ID
				// Exercise the full opaque preparation bound, including exact
				// whitespace which must survive the JSON/base64 envelope.
				c.ProviderIntent = []byte("{\"x\":\"" + strings.Repeat("x", factory.MaxHumanStateProviderIntentBytes-10) + "\"}\n ")
				if len(c.ProviderIntent) != factory.MaxHumanStateProviderIntentBytes {
					t.Fatal("state preparation boundary fixture differs")
				}
				got, replay, err := s.CaptureHumanState(ctx, c)
				if err != nil || replay || !sameCapture(got, c) {
					t.Fatal("state variant capture", subject, close, err)
				}
				saved.Variants = append(saved.Variants, got)
			}
		}
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-state-left", "race-state-right"
		right.Authority = "other-race-preparation"
		right.ProviderIntent = []byte("{ \"native_revision\": 2, \"not_after\": 2 }\n")
		type result struct {
			capture factory.HumanStateCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanStateCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanState(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent state preparation differs", a.err, b.err)
		}
		saved.Variants = append(saved.Variants, a.capture)
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", capture.Request.ID); err == nil {
			t.Fatal("state payload mutable")
		}
		if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), factory.CommandHumanState, strings.Repeat("x", 8193)); err == nil {
			t.Fatal("ordinary SQL state payload bound missing")
		}
		for _, id := range []string{saved.Comment.Request.ID, saved.Edit.Request.ID, saved.Creation.Request.ID, saved.Lock.Request.ID, saved.Legacy.ID} {
			collision := capture
			collision.Request.ID = id
			if _, _, err := s.CaptureHumanState(ctx, collision); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("state replaced another command kind", err)
			}
			if _, err := s.HumanStateCapture(ctx, collision.Request); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("state read another command kind", err)
			}
		}
		for _, mutate := range []func(*factory.HumanStateCapture){
			func(c *factory.HumanStateCapture) { c.Request.Subject = "review" },
			func(c *factory.HumanStateCapture) { c.Request.Number = 0 },
			func(c *factory.HumanStateCapture) { c.Authority = "" },
			func(c *factory.HumanStateCapture) { c.OperationID = "" },
			func(c *factory.HumanStateCapture) { c.ProviderIntent = []byte("invalid JSON") },
			func(c *factory.HumanStateCapture) {
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", factory.MaxHumanStateProviderIntentBytes) + `"`)
			},
			func(c *factory.HumanStateCapture) {
				c.Authority, c.OperationID = strings.Repeat("<", 256), strings.Repeat("<", 256)
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", factory.MaxHumanStateProviderIntentBytes-2) + `"`)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanState(ctx, bad); err == nil {
				t.Fatal("invalid state capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid state left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save state identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained state identity unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanStateCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanStateCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained state preparation differs", err)
		}
		fresh := capture
		fresh.Authority, fresh.OperationID = "replacement", "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanState(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("state replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" || len(cmd.Payload) > 8192 {
			t.Fatal("capture invented an outcome or exceeded ordinary bound", err)
		}
		if cmd.Target != string(capture.Request.Host)+":"+strconv.FormatInt(int64(capture.Request.Repository), 10)+":"+capture.Request.Subject+":"+strconv.FormatInt(capture.Request.Number, 10) {
			t.Fatal("state target differs")
		}
		bound, err := factory.DecodeHumanStateCapture(cmd)
		if err != nil || !sameCapture(bound, capture) {
			t.Fatal("state command binding differs", err)
		}
	}
	for _, change := range []func(*factory.HumanStateCommand){
		func(q *factory.HumanStateCommand) { q.Close = !q.Close },
		func(q *factory.HumanStateCommand) { q.Subject = "issue" },
		func(q *factory.HumanStateCommand) { q.Number++ },
		func(q *factory.HumanStateCommand) { q.Actor++ },
		func(q *factory.HumanStateCommand) { q.Repository++ },
		func(q *factory.HumanStateCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
	} {
		q := saved.Capture.Request
		change(&q)
		if _, err := s.HumanStateCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed state replay admitted", err)
		}
	}
	changed := saved.Capture
	changed.Request.Close = !changed.Request.Close
	if _, _, err := s.CaptureHumanState(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed state capture admitted", err)
	}
	lock, err := s.HumanLockCapture(ctx, saved.Lock.Request)
	if err != nil || !lock.Request.SameRequest(saved.Lock.Request) || lock.Authority != saved.Lock.Authority || lock.OperationID != saved.Lock.OperationID || !bytes.Equal(lock.ProviderIntent, saved.Lock.ProviderIntent) {
		t.Fatal("state journal changed retained lock", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("state journal changed retained comment", err)
	}
	edit, err := s.HumanEditCapture(ctx, saved.Edit.Request)
	if err != nil || !edit.Request.SameRequest(saved.Edit.Request) || edit.Authority != saved.Edit.Authority || edit.OperationID != saved.Edit.OperationID || !bytes.Equal(edit.ProviderIntent, saved.Edit.ProviderIntent) {
		t.Fatal("state journal changed retained edit", err)
	}
	creation, err := s.HumanIssueCreateCapture(ctx, saved.Creation.Request)
	if err != nil || !creation.Request.SameRequest(saved.Creation.Request) || creation.Authority != saved.Creation.Authority || creation.OperationID != saved.Creation.OperationID || !bytes.Equal(creation.ProviderIntent, saved.Creation.ProviderIntent) {
		t.Fatal("state journal changed retained issue creation", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("state journal changed retained settings", err)
	}
	t.Logf("%s: exact issue/PR close/reopen, full 4 KiB opaque preparation; original replay, concurrent winner, immutable ordinary 8192-byte command, scope/kind conflicts; retained lock/comment/edit/create/settings; no schema change or provider dispatch", phase)
}

func checkNativeHumanLockJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Capture  factory.HumanLockCapture
		Variants []factory.HumanLockCapture
		Creation factory.HumanIssueCreateCapture
		Comment  factory.HumanCommentCapture
		Edit     factory.HumanEditCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanLockCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://issue-lock-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/lock-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		if _, err := s.PutCollaborationHost(ctx, other); err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "420", "other-human")
		if err != nil {
			t.Fatal(err)
		}
		retainedBody := strings.Repeat("Retained original 🌋\r\n", 1000)
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: retainedBody}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Edit = factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: &retainedBody}, Authority: "retained-edit-installation", OperationID: "retained-edit-operation", ProviderIntent: []byte("{ \"old_edit\": true }\n")}
		if _, _, err := s.CaptureHumanEdit(ctx, saved.Edit); err != nil {
			t.Fatal(err)
		}
		saved.Creation = factory.HumanIssueCreateCapture{Request: factory.HumanIssueCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Title: "Retained creation 🌋", Body: retainedBody}, Authority: "retained-create-installation", OperationID: "retained-create-operation", ProviderIntent: []byte("{ \"old_create\": true }\n")}
		if _, _, err := s.CaptureHumanIssueCreate(ctx, saved.Creation); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-create-upgrade", Principal: "420", Payload: "{\"retained\":\"原文\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		// Restore the exact preceding payload rule only in this owned fixture.
		restorePreDispatchCommandFixture(t, tx)
		for _, statement := range []string{
			"ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check",
			"ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit','issue.create') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit','issue.create') AND char_length(payload)<=8192))",
			"UPDATE schema_version SET version=73",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-73 lock upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("lock upgrade marker", version, err)
		}
		body := strings.Repeat("<", factory.MaxHumanLockReasonBytes-len("🌋\r\n")) + "🌋\r\n"
		encodedBody, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		capture := factory.HumanLockCapture{Request: factory.HumanLockCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Lock: true, Reason: body}, Authority: "lock-installation-retained", OperationID: "lock-operation-retained", ProviderIntent: append(append([]byte("{ \"reason\": "), encodedBody...), []byte(", \"native_revision\": 1, \"not_after\": 1 }\n")...)}
		for _, mutate := range []func(*factory.HumanLockCapture){
			func(c *factory.HumanLockCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanLockCapture) { c.Request.Actor = foreignActor.ID },
		} {
			foreign := capture
			foreign.Request.ID = factory.NewID()
			mutate(&foreign)
			if _, _, err := s.CaptureHumanLock(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign lock capture admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign capture left command", err)
			}
		}
		got, replay, err := s.CaptureHumanLock(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial lock capture", replay, err)
		}
		saved.Capture = got
		for _, body := range []string{"", "Original smaller body 🌋\r\n"} {
			c := capture
			c.Request.ID = factory.NewID()
			c.Request.Reason = body
			c.OperationID = "create-variant-" + c.Request.ID
			got, _, err := s.CaptureHumanLock(ctx, c)
			if err != nil || !sameCapture(got, c) {
				t.Fatal("lock body variant capture", err)
			}
			saved.Variants = append(saved.Variants, got)
		}
		unlock := capture
		unlock.Request.ID = factory.NewID()
		unlock.Request.Subject, unlock.Request.Number = "pull", 2
		unlock.Request.Lock, unlock.Request.Reason = false, ""
		unlock.OperationID = "unlock-variant"
		if got, replay, err := s.CaptureHumanLock(ctx, unlock); err != nil || replay || !sameCapture(got, unlock) {
			t.Fatal("unlock capture", err)
		}
		saved.Variants = append(saved.Variants, unlock)
		// Different preparations racing for the same human intent must converge.
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-create-left", "race-create-right"
		right.Authority = "other-race-preparation"
		right.ProviderIntent = []byte("{ \"native_revision\": 2, \"not_after\": 2 }\n")
		type result struct {
			capture factory.HumanLockCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanLockCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanLock(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent lock preparation differs", a.err, b.err)
		}
		saved.Variants = append(saved.Variants, a.capture)
		cmd, err := capture.Command()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", cmd.ID); err == nil {
			t.Fatal("lock payload mutable")
		}
		for _, typ := range []string{"retry", "discussion.comment.create", "discussion.edit", "issue.create", "discussion.lock.apply"} {
			limit := 8192
			if typ != "retry" {
				limit = factory.MaxHumanLockCaptureBytes
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), typ, strings.Repeat("x", limit+1)); err == nil {
				t.Fatal("SQL payload limit missing for", typ)
			}
		}
		for _, id := range []string{saved.Comment.Request.ID, saved.Edit.Request.ID, saved.Creation.Request.ID, saved.Legacy.ID} {
			collision := capture
			collision.Request.ID = id
			if _, _, err := s.CaptureHumanLock(ctx, collision); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("lock replaced another command kind", err)
			}
		}
		for _, mutate := range []func(*factory.HumanLockCapture){
			func(c *factory.HumanLockCapture) { c.Request.Subject = "review" },
			func(c *factory.HumanLockCapture) { c.Request.Number = 0 },
			func(c *factory.HumanLockCapture) { c.Request.Lock = false },
			func(c *factory.HumanLockCapture) {
				c.Request.Reason = strings.Repeat("x", factory.MaxHumanLockReasonBytes+1)
			},
			func(c *factory.HumanLockCapture) { c.Request.Reason = "nul\x00reason" },
			func(c *factory.HumanLockCapture) { c.Request.Reason = string([]byte{0xff}) },
			func(c *factory.HumanLockCapture) { c.ProviderIntent = []byte("invalid JSON") },
			func(c *factory.HumanLockCapture) {
				c.ProviderIntent = append([]byte{34}, append([]byte(strings.Repeat("x", 512<<10)), 34)...)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanLock(ctx, bad); err == nil {
				t.Fatal("invalid lock capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid lock left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save lock identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained lock identity unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanLockCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanLockCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained lock preparation differs", err)
		}
		fresh := capture
		fresh.Authority = "replacement"
		fresh.OperationID = "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanLock(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("lock replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" {
			t.Fatal("capture invented an outcome", err)
		}
		if cmd.Target != string(capture.Request.Host)+":"+strconv.FormatInt(int64(capture.Request.Repository), 10)+":"+capture.Request.Subject+":"+strconv.FormatInt(capture.Request.Number, 10) {
			t.Fatal("lock target differs")
		}
		bound, err := factory.DecodeHumanLockCapture(cmd)
		if err != nil || !sameCapture(bound, capture) {
			t.Fatal("lock command binding differs", err)
		}
	}
	for _, change := range []func(*factory.HumanLockCommand){
		func(q *factory.HumanLockCommand) { q.Lock = false; q.Reason = "" },
		func(q *factory.HumanLockCommand) { q.Subject = "pull" },
		func(q *factory.HumanLockCommand) { q.Number++ },
		func(q *factory.HumanLockCommand) { q.Reason = "Changed reason" },
		func(q *factory.HumanLockCommand) { q.Actor++ },
		func(q *factory.HumanLockCommand) { q.Repository++ },
		func(q *factory.HumanLockCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
	} {
		q := saved.Capture.Request
		change(&q)
		if _, err := s.HumanLockCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed lock replay admitted", err)
		}
	}
	changed := saved.Capture
	changed.Request.Reason = "Changed capture reason"
	if _, _, err := s.CaptureHumanLock(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed lock capture admitted", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("lock upgrade changed retained comment", err)
	}
	edit, err := s.HumanEditCapture(ctx, saved.Edit.Request)
	if err != nil || !edit.Request.SameRequest(saved.Edit.Request) || edit.Authority != saved.Edit.Authority || edit.OperationID != saved.Edit.OperationID || !bytes.Equal(edit.ProviderIntent, saved.Edit.ProviderIntent) {
		t.Fatal("lock upgrade changed retained edit", err)
	}
	creation, err := s.HumanIssueCreateCapture(ctx, saved.Creation.Request)
	if err != nil || !creation.Request.SameRequest(saved.Creation.Request) || creation.Authority != saved.Creation.Authority || creation.OperationID != saved.Creation.OperationID || !bytes.Equal(creation.ProviderIntent, saved.Creation.ProviderIntent) {
		t.Fatal("lock upgrade changed retained issue creation", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("lock upgrade changed retained settings", err)
	}
	t.Logf("%s: schema-73 upgrade, exact lock state/64 KiB reason/provider bytes, unlock and blank reason, original preparation replay, concurrent winner and immutable command; retained comments/edits/creations/settings; no provider dispatch", phase)
}

func checkNativeHumanIssueCreateJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var saved struct {
		Capture  factory.HumanIssueCreateCapture
		Variants []factory.HumanIssueCreateCapture
		Comment  factory.HumanCommentCapture
		Edit     factory.HumanEditCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanIssueCreateCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://issue-create-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/create-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"}
		if _, err := s.PutCollaborationHost(ctx, other); err != nil {
			t.Fatal(err)
		}
		foreignActor, err := s.ObserveCollaborationActor(ctx, other.ID, "420", "other-human")
		if err != nil {
			t.Fatal(err)
		}
		retainedBody := strings.Repeat("Retained original 🌋\r\n", 1000)
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: retainedBody}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Edit = factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Body: &retainedBody}, Authority: "retained-edit-installation", OperationID: "retained-edit-operation", ProviderIntent: []byte("{ \"old_edit\": true }\n")}
		if _, _, err := s.CaptureHumanEdit(ctx, saved.Edit); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-create-upgrade", Principal: "420", Payload: "{\"retained\":\"原文\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		// Restore the exact preceding payload rule only in this owned fixture.
		restorePreDispatchCommandFixture(t, tx)
		for _, statement := range []string{
			"ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check",
			"ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type IN ('discussion.comment.create','discussion.edit') AND octet_length(payload)<=2097152) OR (type NOT IN ('discussion.comment.create','discussion.edit') AND char_length(payload)<=8192))",
			"UPDATE schema_version SET version=72",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-72 creation upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("creation upgrade marker", version, err)
		}
		body := strings.Repeat("<", factory.MaxHumanIssueCreateBytes-len("🌋\r\n")) + "🌋\r\n"
		encodedBody, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		capture := factory.HumanIssueCreateCapture{Request: factory.HumanIssueCreateCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Title: "Original title 🌋", Body: body}, Authority: "create-installation-retained", OperationID: "create-operation-retained", ProviderIntent: append(append([]byte("{ \"body\": "), encodedBody...), []byte(", \"native_revision\": 1, \"not_after\": 1 }\n")...)}
		for _, mutate := range []func(*factory.HumanIssueCreateCapture){
			func(c *factory.HumanIssueCreateCapture) { c.Request.Host = other.ID },
			func(c *factory.HumanIssueCreateCapture) { c.Request.Actor = foreignActor.ID },
		} {
			foreign := capture
			foreign.Request.ID = factory.NewID()
			mutate(&foreign)
			if _, _, err := s.CaptureHumanIssueCreate(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
				t.Fatal("foreign creation capture admitted", err)
			}
			if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign capture left command", err)
			}
		}
		got, replay, err := s.CaptureHumanIssueCreate(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial creation capture", replay, err)
		}
		saved.Capture = got
		for _, body := range []string{"", "Original smaller body 🌋\r\n"} {
			c := capture
			c.Request.ID = factory.NewID()
			c.Request.Body = body
			c.OperationID = "create-variant-" + c.Request.ID
			got, _, err := s.CaptureHumanIssueCreate(ctx, c)
			if err != nil || !sameCapture(got, c) {
				t.Fatal("creation body variant capture", err)
			}
			saved.Variants = append(saved.Variants, got)
		}
		// Different preparations racing for the same human intent must converge.
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		left.OperationID, right.OperationID = "race-create-left", "race-create-right"
		right.Authority = "other-race-preparation"
		right.ProviderIntent = []byte("{ \"native_revision\": 2, \"not_after\": 2 }\n")
		type result struct {
			capture factory.HumanIssueCreateCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanIssueCreateCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanIssueCreate(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent creation preparation differs", a.err, b.err)
		}
		saved.Variants = append(saved.Variants, a.capture)
		cmd, err := capture.Command()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", cmd.ID); err == nil {
			t.Fatal("creation payload mutable")
		}
		for _, typ := range []string{"retry", "discussion.comment.create", "discussion.edit", "issue.create"} {
			limit := 8192
			if typ != "retry" {
				limit = factory.MaxHumanIssueCreateCaptureBytes
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), typ, strings.Repeat("x", limit+1)); err == nil {
				t.Fatal("SQL payload limit missing for", typ)
			}
		}
		for _, id := range []string{saved.Comment.Request.ID, saved.Edit.Request.ID, saved.Legacy.ID} {
			collision := capture
			collision.Request.ID = id
			if _, _, err := s.CaptureHumanIssueCreate(ctx, collision); !errors.Is(err, ErrCommandConflict) {
				t.Fatal("creation replaced another command kind", err)
			}
		}
		for _, mutate := range []func(*factory.HumanIssueCreateCapture){
			func(c *factory.HumanIssueCreateCapture) { c.Request.Title = "" },
			func(c *factory.HumanIssueCreateCapture) { c.Request.Title = " " },
			func(c *factory.HumanIssueCreateCapture) { c.Request.Title = " padded" },
			func(c *factory.HumanIssueCreateCapture) { c.Request.Title = "padded " },
			func(c *factory.HumanIssueCreateCapture) { c.Request.Title = strings.Repeat("x", 256) },
			func(c *factory.HumanIssueCreateCapture) {
				c.Request.Body = strings.Repeat("x", factory.MaxHumanIssueCreateBytes+1)
			},
			func(c *factory.HumanIssueCreateCapture) { c.Request.Body = "nul\x00body" },
			func(c *factory.HumanIssueCreateCapture) { c.Request.Body = string([]byte{0xff}) },
			func(c *factory.HumanIssueCreateCapture) { c.ProviderIntent = []byte("invalid JSON") },
			func(c *factory.HumanIssueCreateCapture) {
				c.ProviderIntent = []byte(`"` + strings.Repeat("x", 512<<10) + `"`)
			},
		} {
			bad := capture
			bad.Request.ID = factory.NewID()
			mutate(&bad)
			if _, _, err := s.CaptureHumanIssueCreate(ctx, bad); err == nil {
				t.Fatal("invalid creation capture admitted")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid creation left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save creation identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained creation identity unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanIssueCreateCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanIssueCreateCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained creation preparation differs", err)
		}
		fresh := capture
		fresh.Authority = "replacement"
		fresh.OperationID = "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanIssueCreate(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("creation replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" {
			t.Fatal("capture invented an outcome", err)
		}
		if cmd.Target != string(capture.Request.Host)+":"+strconv.FormatInt(int64(capture.Request.Repository), 10)+":issues" {
			t.Fatal("creation target predicts an issue")
		}
		bound, err := factory.DecodeHumanIssueCreateCapture(cmd)
		if err != nil || !sameCapture(bound, capture) {
			t.Fatal("creation command binding differs", err)
		}
	}
	for _, change := range []func(*factory.HumanIssueCreateCommand){
		func(q *factory.HumanIssueCreateCommand) { q.Title = "Changed title" },
		func(q *factory.HumanIssueCreateCommand) { q.Body = "Changed body" },
		func(q *factory.HumanIssueCreateCommand) { q.Actor++ },
		func(q *factory.HumanIssueCreateCommand) { q.Repository++ },
		func(q *factory.HumanIssueCreateCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
	} {
		q := saved.Capture.Request
		change(&q)
		if _, err := s.HumanIssueCreateCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed creation replay admitted", err)
		}
	}
	changed := saved.Capture
	changed.Request.Title = "Changed capture title"
	if _, _, err := s.CaptureHumanIssueCreate(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed creation capture admitted", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("creation upgrade changed retained comment", err)
	}
	edit, err := s.HumanEditCapture(ctx, saved.Edit.Request)
	if err != nil || !edit.Request.SameRequest(saved.Edit.Request) || edit.Authority != saved.Edit.Authority || edit.OperationID != saved.Edit.OperationID || !bytes.Equal(edit.ProviderIntent, saved.Edit.ProviderIntent) {
		t.Fatal("creation upgrade changed retained edit", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("creation upgrade changed retained settings", err)
	}
	t.Logf("%s: schema-72 upgrade, exact creation title/64 KiB Markdown/provider bytes, empty body, original preparation replay, concurrent winner and immutable command; retained comments/edits/settings; no provider dispatch", phase)
}

func checkNativeHumanEditJournal(t *testing.T, s *Store, dsn, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	text := func(v string) *string { return &v }
	var saved struct {
		Capture  factory.HumanEditCapture
		Variants []factory.HumanEditCapture
		Comment  factory.HumanCommentCapture
		Legacy   factory.Command
	}
	sameCapture := func(a, b factory.HumanEditCapture) bool {
		return a.Request.SameRequest(b.Request) && a.Authority == b.Authority && a.OperationID == b.OperationID && bytes.Equal(a.ProviderIntent, b.ProviderIntent)
	}
	if phase == "write" {
		if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
			t.Fatal(err)
		}
		host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://edit-journal.example.test"}
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "700", "team/edit-repo")
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "420", "human")
		if err != nil {
			t.Fatal(err)
		}
		saved.Comment = factory.HumanCommentCapture{Request: factory.HumanCommentCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "issue", Number: 1, Body: strings.Repeat("Retained comment 🌋\r\n", 1000)}, Authority: "retained-comment-installation", OperationID: "retained-comment-operation", ProviderIntent: []byte("{ \"old\": true }\n")}
		if _, _, err := s.CaptureHumanComment(ctx, saved.Comment); err != nil {
			t.Fatal(err)
		}
		saved.Legacy = factory.Command{ID: factory.NewID(), Type: factory.CommandRetry, Target: "retained-edit-upgrade", Principal: "420", Payload: "{\"retained\":\"\u539F\u6587\"}"}
		saved.Legacy.Digest = factory.SettingsDigest(saved.Legacy.Type, saved.Legacy.Target, saved.Legacy.Payload)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = recordFactoryCommandTx(ctx, tx, saved.Legacy, time.Now()); err != nil {
			t.Fatal(err)
		}
		restorePreDispatchCommandFixture(t, tx)
		for _, statement := range []string{
			"ALTER TABLE factory_commands DROP CONSTRAINT factory_commands_payload_check",
			"ALTER TABLE factory_commands ADD CONSTRAINT factory_commands_payload_check CHECK((type='discussion.comment.create' AND octet_length(payload)<=2097152) OR (type<>'discussion.comment.create' AND char_length(payload)<=8192))",
			"UPDATE schema_version SET version=71",
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatal("retained schema-71 edit upgrade", err)
		}
		defer s.Close()
		var version int
		if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != SchemaVersion() {
			t.Fatal("edit upgrade marker", version, err)
		}
		body := strings.Repeat("<", factory.MaxHumanEditBytes-len("🌋\r\n")) + "🌋\r\n"
		encodedBody, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		capture := factory.HumanEditCapture{Request: factory.HumanEditCommand{ID: factory.NewID(), Host: host.ID, Repository: repo.ID, Actor: actor.ID, Subject: "pull", Number: 3, Title: text("Edited title 🌋"), Body: text(body)}, Authority: "edit-installation-retained", OperationID: "edit-operation-retained", ProviderIntent: append(append([]byte("{ \"body\": "), encodedBody...), []byte(", \"native_revision\": 1, \"content_version\": 7, \"not_after\": 1 }\n")...)}
		foreign := capture
		foreign.Request.ID = factory.NewID()
		foreign.Request.Host = collaboration.HostID(collaboration.NewID())
		if _, _, err := s.CaptureHumanEdit(ctx, foreign); !errors.Is(err, collaboration.ErrUnboundLedger) {
			t.Fatal("foreign edit host admitted", err)
		}
		if _, err := s.FactoryCommand(ctx, foreign.Request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign capture left command", err)
		}
		got, replay, err := s.CaptureHumanEdit(ctx, capture)
		if err != nil || replay || !sameCapture(got, capture) {
			t.Fatal("initial edit capture", replay, err)
		}
		saved.Capture = got
		for _, fields := range [][2]*string{{text("Title only"), nil}, {nil, text("Body only 🌋\r\n")}, {nil, text("")}} {
			c := capture
			c.Request.ID = factory.NewID()
			c.Request.Title, c.Request.Body = fields[0], fields[1]
			c.OperationID = "edit-variant-" + c.Request.ID
			got, _, err := s.CaptureHumanEdit(ctx, c)
			if err != nil || !sameCapture(got, c) {
				t.Fatal("edit field presence capture", err)
			}
			saved.Variants = append(saved.Variants, got)
		}
		// Two real concurrent writers with separately allocated but equal fields.
		left, right := capture, capture
		left.Request.ID = factory.NewID()
		right.Request.ID = left.Request.ID
		right.Request.Title = text(*left.Request.Title)
		right.Request.Body = text(*left.Request.Body)
		left.OperationID, right.OperationID = "race-edit-left", "race-edit-right"
		right.Authority = "race-edit-other-preparation"
		type result struct {
			capture factory.HumanEditCapture
			replay  bool
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []factory.HumanEditCapture{left, right} {
			go func() { got, replay, err := s.CaptureHumanEdit(ctx, c); results <- result{got, replay, err} }()
		}
		a, b := <-results, <-results
		if a.err != nil || b.err != nil || !sameCapture(a.capture, b.capture) || a.replay == b.replay {
			t.Fatal("concurrent edit preparation differs", a.err, b.err)
		}
		cmd, err := capture.Command()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE factory_commands SET payload=payload||' ' WHERE id=$1", cmd.ID); err == nil {
			t.Fatal("edit capture payload mutable")
		}
		for _, typ := range []string{"retry", "discussion.comment.create", "discussion.edit"} {
			limit := 8192
			if typ != "retry" {
				limit = factory.MaxHumanEditCaptureBytes
			}
			if _, err := s.db.ExecContext(ctx, "INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,'x','420','x',$3,'now')", factory.NewID(), typ, strings.Repeat("x", limit+1)); err == nil {
				t.Fatal("SQL command limit missing for", typ)
			}
		}
		// A command of another kind cannot be reused as an edit.
		collision := capture
		collision.Request.ID = saved.Comment.Request.ID
		if _, _, err := s.CaptureHumanEdit(ctx, collision); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("edit replaced comment command", err)
		}
		// Invalid field intent must fail before leaving any command row.
		for _, fields := range [][2]*string{{nil, nil}, {text(""), nil}, {text(strings.Repeat("x", 256)), nil}, {nil, text(strings.Repeat("x", factory.MaxHumanEditBytes+1))}, {nil, text("nul\x00body")}, {nil, text(string([]byte{0xff}))}} {
			bad := capture
			bad.Request.ID = factory.NewID()
			bad.Request.Title, bad.Request.Body = fields[0], fields[1]
			if _, _, err := s.CaptureHumanEdit(ctx, bad); err == nil {
				t.Fatal("invalid edit fields captured")
			}
			if _, err := s.FactoryCommand(ctx, bad.Request.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid edit left command", err)
			}
		}
		raw, err := json.Marshal(saved)
		if err != nil || os.WriteFile(identityFile, raw, 0o600) != nil {
			t.Fatal("save edit identity", err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("retained edit capture unavailable", err)
		}
	}
	for _, capture := range append([]factory.HumanEditCapture{saved.Capture}, saved.Variants...) {
		got, err := s.HumanEditCapture(ctx, capture.Request)
		if err != nil || !sameCapture(got, capture) {
			t.Fatal("retained edit preparation differs", err)
		}
		fresh := capture
		fresh.OperationID = "replacement"
		fresh.Authority = "replacement"
		fresh.ProviderIntent = []byte("{\"native_revision\":999,\"not_after\":999}")
		got, replay, err := s.CaptureHumanEdit(ctx, fresh)
		if err != nil || !replay || !sameCapture(got, capture) {
			t.Fatal("edit replay substituted preparation", err)
		}
		cmd, err := s.FactoryCommand(ctx, capture.Request.ID)
		if err != nil || cmd.Outcome != "" || cmd.Finished != "" {
			t.Fatal("capture invented outcome", err)
		}
	}
	for _, change := range []func(*factory.HumanEditCommand){
		func(q *factory.HumanEditCommand) { q.Title = text("Changed title") },
		func(q *factory.HumanEditCommand) { q.Title = nil },
		func(q *factory.HumanEditCommand) { q.Body = text("Changed body") },
		func(q *factory.HumanEditCommand) { q.Body = nil },
		func(q *factory.HumanEditCommand) { q.Number++ },
		func(q *factory.HumanEditCommand) { q.Subject = "issue" },
		func(q *factory.HumanEditCommand) { q.Actor++ },
		func(q *factory.HumanEditCommand) { q.Repository++ },
		func(q *factory.HumanEditCommand) { q.Host = collaboration.HostID(collaboration.NewID()) },
	} {
		q := saved.Capture.Request
		change(&q)
		if _, err := s.HumanEditCapture(ctx, q); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed edit replay accepted", err)
		}
	}
	changed := saved.Capture
	changed.Request.Body = text("Changed capture body")
	if _, _, err := s.CaptureHumanEdit(ctx, changed); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed edit capture admitted", err)
	}
	empty := saved.Variants[2].Request
	empty.Body = nil
	empty.Title = text("Title only")
	if _, err := s.HumanEditCapture(ctx, empty); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("omitted body adopted clear-body command", err)
	}
	comment, err := s.HumanCommentCapture(ctx, saved.Comment.Request)
	if err != nil || comment.Request != saved.Comment.Request || comment.Authority != saved.Comment.Authority || comment.OperationID != saved.Comment.OperationID || !bytes.Equal(comment.ProviderIntent, saved.Comment.ProviderIntent) {
		t.Fatal("schema upgrade changed retained large comment", err)
	}
	legacy, err := s.FactoryCommand(ctx, saved.Legacy.ID)
	if err != nil || legacy.Payload != saved.Legacy.Payload || legacy.Digest != saved.Legacy.Digest {
		t.Fatal("schema upgrade changed retained settings", err)
	}
	t.Logf("%s: retained schema-71 upgrade, exact edit title/64 KiB body/provider bytes and omitted/empty field presence, original preparation replay, concurrency and immutable command; no provider dispatch", phase)
}

// Retain command identity outside the Go process and receipts in PostgreSQL.
// Both clean restart and abrupt server death must preserve exact replay without
// disturbing attempts made after the committed retry.
func checkNativePostRetryReceipt(t *testing.T, s *Store, phase, identityFile string) {
	t.Helper()
	ctx := t.Context()
	var request factory.FeedPostRetryCommand
	if phase == "write" {
		post := saveTestPost(t, s, 7, "42", "Native retry original", "native-retry-receipt")
		if err := s.RecordPostIntake(ctx, post, 0); err != nil {
			t.Fatal(err)
		}
		request = factory.FeedPostRetryCommand{ID: factory.NewID(), Post: post, Repository: 7, Author: "42", Body: "Native retry retained"}
		if _, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, request, 1729000100); err != nil || replayed {
			t.Fatal("native retry write", replayed, err)
		}
		if err := s.NotePostAttempt(ctx, post); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(identityFile, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		raw, err := os.ReadFile(identityFile)
		if err != nil || json.Unmarshal(raw, &request) != nil {
			t.Fatal("native retry identity unavailable", err)
		}
	}
	wantRevision := int64(1)
	if phase == "recover" {
		wantRevision = 2
	}
	post, err := s.FeedPost(ctx, request.Post)
	if err != nil || post.Record.Revision != wantRevision {
		t.Fatal("native retry head lost", post.Record.Revision, err)
	}
	pipe, err := s.PostIntake(ctx, request.Post)
	if err != nil || pipe.Attempts != 1 || pipe.Revision != wantRevision {
		t.Fatal("native retry attempt state lost", pipe, err)
	}
	receipt, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, request, 1729000101)
	if err != nil || !replayed || receipt.Revision != 1 || receipt.Validate(request) != nil {
		t.Fatal("native retry original receipt lost", receipt, replayed, err)
	}
	crashCommand := request
	crashCommand.ID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	crashCommand.ExpectedRevision, crashCommand.Body = 1, "Native retry before crash"
	if phase == "restart" {
		if _, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, crashCommand, 1729000102); err != nil || replayed {
			t.Fatal("native retry before crash", replayed, err)
		}
		if err := s.NotePostAttempt(ctx, request.Post); err != nil {
			t.Fatal(err)
		}
	} else if phase == "recover" {
		if receipt, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, crashCommand, 1729000103); err != nil || !replayed || receipt.Revision != 2 || receipt.Validate(crashCommand) != nil {
			t.Fatal("native retry crash receipt lost", receipt, replayed, err)
		}
	}
	pipe, err = s.PostIntake(ctx, request.Post)
	if err != nil || pipe.Attempts != 1 {
		t.Fatal("native receipt replay reset attempts", pipe, err)
	}
	t.Logf("%s verified atomic retry receipt and non-mutating historical replay", phase)
}
