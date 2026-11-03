package store

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/application"
	"github.com/levitateos/sodaos/internal/factory"
)

// Restore the retained feed layout only inside a disposable integration fixture.
func legacyFeedSchema(t *testing.T, s *Store) {
	t.Helper()
	for _, statement := range []string{
		`DROP INDEX factory_feed_posts_parent`,
		`ALTER TABLE factory_feed_posts RENAME TO factory_feed_comments`,
		`ALTER INDEX factory_feed_posts_repository RENAME TO factory_feed_comments_repository`,
		`ALTER TABLE factory_post_intakes RENAME TO factory_feed_comment_requests`,
		`ALTER TABLE factory_feed_comment_requests RENAME COLUMN post TO comment`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFeedPostMigrationPreservesConversationAndIntake(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	root := saveTestPost(t, s, 7, "42", "Original human request", "migration-root")
	child, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 7, Body: "Original reply", ParentID: root, IdempotencyKey: "migration-child"}, "43", "Another person", 1729000001)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPostIntake(ctx, root, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE factory_post_intakes SET attempts=2,data='{"operation":"retained-operation","draft":{"comment":"`+root+`"}}' WHERE post=$1`, root); err != nil {
		t.Fatal(err)
	}
	legacyFeedSchema(t, s)
	if _, err := s.db.Exec(`UPDATE factory_feed_comments SET data=(data-'parent_id') || jsonb_build_object('reply_to',$2::text) WHERE id=$1`, child, root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE schema_version SET version=69`); err != nil {
		t.Fatal(err)
	}
	current, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	post, err := current.FeedPost(ctx, child)
	if err != nil || post.Record.ParentID != root || post.Body != "Original reply" || post.Record.Author != "43" {
		t.Fatalf("reply changed: %+v %v", post, err)
	}
	parent, err := current.FeedPost(ctx, root)
	if err != nil || parent.Body != "Original human request" {
		t.Fatalf("root changed: %+v %v", parent, err)
	}
	intake, err := current.PostIntake(ctx, root)
	if err != nil || intake.SourcePost != root || intake.Attempts != 2 {
		t.Fatalf("intake changed: %+v %v", intake, err)
	}
	var operation, source string
	if err := current.db.QueryRow(`SELECT data->>'operation',data->'draft'->>'source_post' FROM factory_post_intakes WHERE post=$1`, root).Scan(&operation, &source); err != nil || operation != "retained-operation" || source != root {
		t.Fatalf("receipt source changed: %q %q %v", operation, source, err)
	}
	entries, _, _, err := current.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10})
	if err != nil || len(entries) != 2 {
		t.Fatalf("reply missing from main timeline: %+v %v", entries, err)
	}
	var old int
	if err := current.db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('factory_feed_comments','factory_feed_comment_requests') AND table_schema='public'`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("old store remains: %d %v", old, err)
	}
}

func TestOpenRefusesIncompleteApplicationSchema(t *testing.T) {
	for _, test := range []struct{ name, damage, probe string }{
		{"applications_table", `ALTER TABLE applications RENAME TO applications_missing`, `SELECT count(*) FROM information_schema.tables WHERE table_name='applications' AND table_schema='public'`},
		{"revisions_table", `ALTER TABLE application_revisions RENAME TO application_revisions_missing`, `SELECT count(*) FROM information_schema.tables WHERE table_name='application_revisions' AND table_schema='public'`},
		{"observation_column", `ALTER TABLE applications DROP COLUMN observation`, `SELECT count(*) FROM information_schema.columns WHERE table_name='applications' AND column_name='observation' AND table_schema='public'`},
		{"identity_guard", `DROP TRIGGER application_identity_immutable ON applications`, `SELECT count(*) FROM information_schema.triggers WHERE trigger_name='application_identity_immutable' AND event_object_table='applications'`},
		{"revision_update_guard", `DROP TRIGGER application_revision_immutable_update ON application_revisions`, `SELECT count(*) FROM information_schema.triggers WHERE trigger_name='application_revision_immutable_update' AND event_object_table='application_revisions'`},
		{"revision_delete_guard", `DROP TRIGGER application_revision_immutable_delete ON application_revisions`, `SELECT count(*) FROM information_schema.triggers WHERE trigger_name='application_revision_immutable_delete' AND event_object_table='application_revisions'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, dsn := postgresFixture(t)
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, test.damage); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(dsn)
			if err == nil {
				_ = opened.Close()
				t.Fatal("incomplete application schema accepted")
			}
			if !strings.Contains(err.Error(), "database schema is incomplete") {
				t.Fatalf("unexpected refusal: %v", err)
			}
			var missing int
			if err := s.db.QueryRowContext(ctx, test.probe).Scan(&missing); err != nil || missing != 0 {
				t.Fatalf("startup repaired incomplete schema: count=%d err=%v", missing, err)
			}
			var version int
			if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion() {
				t.Fatalf("startup changed schema marker %d %v", version, err)
			}
		})
	}
}

// The seeded marker, the version constant, and the renewal audit table must
// agree: a fresh database carries exactly one version row matching
// SchemaVersion, and the renewals journal the renewal path writes to.
func TestSchemaSeedMatchesVersionConstant(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion() || SchemaVersion() != 77 {
		t.Fatalf("seeded marker %d disagrees with constant %d", version, SchemaVersion())
	}
	var columns int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='application_setup_renewals' AND column_name IN ('setup_id','renewed_at','expires_at','administrator_id')`).Scan(&columns); err != nil || columns != 4 {
		t.Fatalf("renewal journal columns %d %v", columns, err)
	}
}

// Mixed-version behavior: only the adopted 68 to 69 upgrade runs; any
// other foreign version is rejected on open without mutation. Both the Go
// store and the Rust identity mirror carry 69. A 68 marker beside a
// present renewals journal is not a 68 database but shape corruption, and
// is refused the same way.
func TestOpenRefusesForeignSchemaVersion(t *testing.T) {
	for _, test := range []struct {
		name    string
		foreign int
	}{{"older", 68}, {"newer", SchemaVersion() + 1}} {
		t.Run(test.name, func(t *testing.T) {
			s, dsn := postgresFixture(t)
			ctx := context.Background()
			if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version=$1`, test.foreign); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(dsn)
			if err == nil {
				_ = opened.Close()
				t.Fatalf("foreign schema version %d accepted", test.foreign)
			}
			if !strings.Contains(err.Error(), "database schema differs from this application") {
				t.Fatalf("unexpected refusal for version %d: %v", test.foreign, err)
			}
			var version int
			if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != test.foreign {
				t.Fatalf("startup mutated foreign marker to %d %v", version, err)
			}
			var journal int
			if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='application_setup_renewals'`).Scan(&journal); err != nil || journal != 1 {
				t.Fatalf("startup touched foreign schema tables %d %v", journal, err)
			}
		})
	}
}

// The adopted 68 to 69 upgrade runs against a genuine retained 68 copy:
// builds present, renewals journal absent, marker 68. The copy migrates
// to 69 with every retained row intact, the migrated journal re-arms an
// expired setup, and a second open is a clean no-op. Only isolated
// copies are ever degraded here; the live database is never touched.
func TestOpenMigratesRetained68To69(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	setup := application.SetupRecord{
		ID:              "a" + strings.Repeat("6", 24),
		Application:     application.SetupSpec{ID: "a" + strings.Repeat("6", 24), Name: "Example", ArtifactSHA256: strings.Repeat("2", 64), BundleSHA256: strings.Repeat("3", 64), ToolsSHA256: strings.Repeat("4", 64), RuntimeImage: "sha256:" + strings.Repeat("5", 64), Image: "sha256:" + strings.Repeat("6", 64), Data: "a" + strings.Repeat("6", 24), Origin: "https://migrate-68.example.test", Port: 20031},
		AdministratorID: 41, ExpiresAt: time.Now().Add(-time.Minute).Unix(), State: "pending",
	}
	if err := s.CreateApplicationSetup(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO application_builds(id,repository,delivery_id,environment,data,setup_id) VALUES($1,$2,$3,$4,$5,$6)`, strings.Repeat("6", 32), 7, "delivery-68", "company", `{"build":"retained-68"}`, setup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE application_setup_renewals`); err != nil {
		t.Fatal(err)
	}
	legacyFeedSchema(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version=68`); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(dsn)
	if err != nil {
		t.Fatalf("genuine 68 copy refused: %v", err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatalf("migrated marker %d %v", version, err)
	}
	var journal int
	if err := migrated.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='application_setup_renewals' AND table_type='BASE TABLE'`).Scan(&journal); err != nil || journal != 1 {
		t.Fatalf("migrated journal tables %d %v", journal, err)
	}
	kept, err := migrated.ApplicationSetup(ctx, setup.ID)
	if err != nil || kept != setup {
		t.Fatalf("migrated setup changed: %+v %v", kept, err)
	}
	var build string
	if err := migrated.db.QueryRowContext(ctx, `SELECT data->>'build' FROM application_builds WHERE id=$1`, strings.Repeat("6", 32)).Scan(&build); err != nil || build != "retained-68" {
		t.Fatalf("migrated build changed: %s %v", build, err)
	}
	// The migrated journal actually re-arms: the expired retained setup
	// renews through it, preserving identity, address, and administrator.
	renewed, err := migrated.RenewApplicationSetup(ctx, kept, 41)
	if err != nil {
		t.Fatalf("migrated journal refused renewal: %v", err)
	}
	if renewed.ExpiresAt <= time.Now().Unix() || renewed.Application != setup.Application || renewed.AdministratorID != setup.AdministratorID || renewed.State != "pending" {
		t.Fatalf("renewal changed the retained reservation: %+v", renewed)
	}
	var renews int
	if err := migrated.db.QueryRowContext(ctx, `SELECT count(*) FROM application_setup_renewals WHERE setup_id=$1`, setup.ID).Scan(&renews); err != nil || renews != 1 {
		t.Fatalf("migrated journal rows %d %v", renews, err)
	}
	again, err := Open(dsn)
	if err != nil {
		t.Fatalf("migrated database refused reopen: %v", err)
	}
	defer again.Close()
	if err := again.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatalf("reopened marker %d %v", version, err)
	}
}

// Concurrent opens of one retained 68 copy migrate exactly once: every
// opener succeeds instead of the second failing on the shared CREATE, the
// marker lands on 69, and the retained setup and build rows survive intact.
// Only isolated copies are ever degraded here; the live database is never
// touched.
func TestConcurrentOpensMigrateRetained68Once(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	setup := application.SetupRecord{
		ID:              "a" + strings.Repeat("8", 24),
		Application:     application.SetupSpec{ID: "a" + strings.Repeat("8", 24), Name: "Example", ArtifactSHA256: strings.Repeat("2", 64), BundleSHA256: strings.Repeat("3", 64), ToolsSHA256: strings.Repeat("4", 64), RuntimeImage: "sha256:" + strings.Repeat("5", 64), Image: "sha256:" + strings.Repeat("6", 64), Data: "a" + strings.Repeat("8", 24), Origin: "https://migrate-68-race.example.test", Port: 20033},
		AdministratorID: 41, ExpiresAt: time.Now().Add(-time.Minute).Unix(), State: "pending",
	}
	if err := s.CreateApplicationSetup(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO application_builds(id,repository,delivery_id,environment,data,setup_id) VALUES($1,$2,$3,$4,$5,$6)`, strings.Repeat("8", 32), 9, "delivery-68", "company", `{"build":"retained-68-race"}`, setup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE application_setup_renewals`); err != nil {
		t.Fatal(err)
	}
	legacyFeedSchema(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version=68`); err != nil {
		t.Fatal(err)
	}
	const openers = 8
	type outcome struct {
		opened *Store
		err    error
	}
	outcomes := make([]outcome, openers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range outcomes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			opened, err := Open(dsn)
			outcomes[i] = outcome{opened: opened, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	for i, o := range outcomes {
		if o.err != nil {
			t.Fatalf("concurrent opener %d refused: %v", i, o.err)
		}
		if o.opened == nil {
			t.Fatalf("concurrent opener %d returned no store", i)
		}
		defer o.opened.Close()
	}
	first := outcomes[0].opened
	var version int
	if err := first.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatalf("concurrent migration marker %d %v", version, err)
	}
	kept, err := first.ApplicationSetup(ctx, setup.ID)
	if err != nil || kept != setup {
		t.Fatalf("concurrent migration changed retained setup: %+v %v", kept, err)
	}
	var build string
	if err := first.db.QueryRowContext(ctx, `SELECT data->>'build' FROM application_builds WHERE id=$1`, strings.Repeat("8", 32)).Scan(&build); err != nil || build != "retained-68-race" {
		t.Fatalf("concurrent migration changed retained build: %s %v", build, err)
	}
	var journal int
	if err := first.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='application_setup_renewals' AND table_type='BASE TABLE'`).Scan(&journal); err != nil || journal != 1 {
		t.Fatalf("concurrent migration journals %d %v", journal, err)
	}
}

// A loser that fails the 68 to 69 upgrade after the winner committed still
// opens: the reread finds the now-current 69 marker, runs the full shape
// verification, and succeeds. The simulated loser reads 68, then a
// concurrent winner commits the upgrade, then the loser's own journal
// CREATE fails with a genuine duplicate-table error — the exact shape of a
// cross-process lost upgrade race.
func TestLostUpgradeRaceRereadAcceptsCommitted69(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `DROP TABLE application_setup_renewals`); err != nil {
		t.Fatal(err)
	}
	legacyFeedSchema(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version=68`); err != nil {
		t.Fatal(err)
	}
	loser, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer loser.Close()
	tx, err := loser.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var staged int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&staged); err != nil || staged != 68 {
		t.Fatalf("loser staged marker %d %v", staged, err)
	}
	winner, err := Open(dsn)
	if err != nil {
		t.Fatalf("winner refused: %v", err)
	}
	defer winner.Close()
	_, lostErr := tx.ExecContext(ctx, ApplicationSetupRenewalsDDL)
	if lostErr == nil {
		t.Fatal("loser CREATE did not fail after the committed upgrade")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := reopenAfterLostUpgradeRace(ctx, s.db, lostErr); err != nil {
		t.Fatalf("reread refused a committed 69: %v", err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatalf("reread moved marker to %d %v", version, err)
	}
}

// A failed 68 to 69 upgrade rolls back to 68 without mutation: with a
// conflicting object holding the journal name, the CREATE TABLE fails
// inside the open transaction and the retained rows and marker survive.
func TestFailedMigrationLeaves68Intact(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	setup := application.SetupRecord{
		ID:              "a" + strings.Repeat("7", 24),
		Application:     application.SetupSpec{ID: "a" + strings.Repeat("7", 24), Name: "Example", ArtifactSHA256: strings.Repeat("2", 64), BundleSHA256: strings.Repeat("3", 64), ToolsSHA256: strings.Repeat("4", 64), RuntimeImage: "sha256:" + strings.Repeat("5", 64), Image: "sha256:" + strings.Repeat("6", 64), Data: "a" + strings.Repeat("7", 24), Origin: "https://migrate-68-fail.example.test", Port: 20032},
		AdministratorID: 41, ExpiresAt: time.Now().Add(-time.Minute).Unix(), State: "pending",
	}
	if err := s.CreateApplicationSetup(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE application_setup_renewals`); err != nil {
		t.Fatal(err)
	}
	legacyFeedSchema(t, s)
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET version=68`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE VIEW application_setup_renewals AS SELECT 1 AS setup_id`); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(dsn)
	if err == nil {
		_ = opened.Close()
		t.Fatal("conflicted migration accepted")
	}
	if !strings.Contains(err.Error(), "migrate database schema 68 to 69") {
		t.Fatalf("unexpected migration failure: %v", err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil || version != 68 {
		t.Fatalf("failed migration moved marker to %d %v", version, err)
	}
	kept, err := s.ApplicationSetup(ctx, setup.ID)
	if err != nil || kept != setup {
		t.Fatalf("failed migration changed retained setup: %+v %v", kept, err)
	}
	var journal int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='application_setup_renewals' AND table_type='BASE TABLE'`).Scan(&journal); err != nil || journal != 0 {
		t.Fatalf("failed migration left journal tables %d %v", journal, err)
	}
}
