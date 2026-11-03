package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func retainedLedgerFingerprint(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname='public' AND left(tablename,14)<>'collaboration_' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	digest := sha256.New()
	for _, table := range tables {
		var data string
		if err := s.db.QueryRowContext(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(row) ORDER BY to_jsonb(row)::text),'[]'::jsonb)::text FROM public.`+quoteCollaborationIdentifier(table)+` row`).Scan(&data); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(digest, table, data)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func TestCollaborationFountainAdoptionPreservesRetainedNamespace(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	const jsonRepo int64 = 9007199254740993
	const jsonActor int64 = 9007199254740995
	if err := s.UpsertUser(ctx, User{ID: 42, Login: "retained-person", Name: "Original person"}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "p0123456789abcdef01234567", Name: "Retained", RepositoryID: 7, OwnerID: 42, Repository: "team/retained"}
	if err := s.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(ctx, project.ID, 42, "retained-person"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddKey(ctx, 42, "synthetic public key", "synthetic fingerprint"); err != nil {
		t.Fatal(err)
	}
	post := saveTestPost(t, s, 7, "42", "retain this request", "adoption-retained")
	if err := s.RecordPostIntake(ctx, post, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE factory_post_intakes SET data=jsonb_build_object('snapshot',jsonb_build_object('repository_id',$1::bigint,'actor',jsonb_build_object('id',$2::bigint))) WHERE post=$3`, jsonRepo, jsonActor, post); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 4294967305, Body: "bigint-only repo", IdempotencyKey: "large-column"}, "42", "Original person", 1729000001); err != nil {
		t.Fatal(err)
	}
	intent := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 77, Repository: 301, Objective: "Retained draft", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFeedSubscription(ctx, "300", 0, factory.FeedSubscription{Subscriber: "300", Repositories: []int64{7, 600}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,outcome,created) VALUES($1,'retained','repository/500/repair-grant','native:700','retained-digest','{"source_actor":"701"}','{"repository_id":"501"}','2026-10-31T00:00:00Z')`, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO identity_connections(id,owner_id,generation,state,data,credential) VALUES('retained-agent',42,1,'ready','{"provider":"muse"}',decode('deadbeef','hex'))`); err != nil {
		t.Fatal(err)
	}
	host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://forge.example.test"}
	if _, err := s.PutCollaborationHost(ctx, host); err != nil {
		t.Fatal(err)
	}
	before := retainedLedgerFingerprint(t, s)
	if err := s.AdoptFountainLedger(ctx, host.ID, "https://FORGE.example.test:443/"); err != nil {
		t.Fatal(err)
	}
	if after := retainedLedgerFingerprint(t, s); after != before {
		t.Fatal("adoption modified retained ledger/user/credential bytes")
	}
	for _, id := range []int64{7, 301, 500, 501, 600, 4294967305, jsonRepo} {
		got, err := s.CollaborationRepository(ctx, collaboration.RepositoryID(id))
		if err != nil || got.ID != collaboration.RepositoryID(id) || got.ExternalID != strconv.FormatInt(id, 10) || got.Host != host.ID {
			t.Fatalf("retained repository %d not bound: %+v %v", id, got, err)
		}
	}
	for _, id := range []int64{42, 77, 300, 700, 701, jsonActor} {
		got, err := s.CollaborationActor(ctx, collaboration.ActorID(id))
		if err != nil || got.ID != collaboration.ActorID(id) || got.ExternalID != strconv.FormatInt(id, 10) || got.Host != host.ID {
			t.Fatalf("retained actor %d not bound: %+v %v", id, got, err)
		}
	}
	if got, err := s.CollaborationRepository(ctx, 7); err != nil || got.Name != project.Repository {
		t.Fatalf("retained repo label lost: %+v %v", got, err)
	}
	if got, err := s.CollaborationActor(ctx, 42); err != nil || got.Login != "retained-person" {
		t.Fatalf("retained login lost: %+v %v", got, err)
	}
	github := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.example.test"}
	if _, err := s.PutCollaborationHost(ctx, github); err != nil {
		t.Fatal(err)
	}
	repo, err := s.ObserveCollaborationRepository(ctx, github.ID, "7", "team/retained")
	if err != nil || repo.ID <= collaboration.RepositoryID(jsonRepo) {
		t.Fatalf("new repo overlaps retained namespace: %+v %v", repo, err)
	}
	actor, err := s.ObserveCollaborationActor(ctx, github.ID, "42", "retained-person")
	if err != nil || actor.ID <= collaboration.ActorID(jsonActor) {
		t.Fatalf("new actor overlaps retained namespace: %+v %v", actor, err)
	}
	if _, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: int64(repo.ID), Body: "new-provider history", IdempotencyKey: "new-provider"}, strconv.FormatInt(int64(actor.ID), 10), "New account", 1729000003); err != nil {
		t.Fatal(err)
	}
	mixed := retainedLedgerFingerprint(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AdoptFountainLedger(ctx, host.ID, host.APIBase); err != nil {
		t.Fatal(err)
	}
	if configured, err := s.BindConfiguredFountainLedger(ctx, "https://FORGE.example.test:443/"); err != nil || configured != host {
		t.Fatalf("configured composition changed adopted host: %+v %v", configured, err)
	}
	if after := retainedLedgerFingerprint(t, s); after != mixed {
		t.Fatal("repeat adoption rewrote mixed-provider history")
	}
	if got, err := s.CollaborationRepository(ctx, repo.ID); err != nil || got != repo {
		t.Fatalf("repeat adoption claimed a GitHub repository: %+v %v", got, err)
	}
	if got, err := s.ObserveCollaborationRepository(ctx, host.ID, "7", "new-owner/renamed"); err != nil || got.ID != 7 {
		t.Fatalf("Fountain rename lost retained ID: %+v %v", got, err)
	}
}

func TestCollaborationFountainAdoptionRejectsWrongOrAmbiguousSource(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := t.Context()
	if err := s.UpsertUser(ctx, User{ID: 42, Login: "retained"}); err != nil {
		t.Fatal(err)
	}
	hosts := []collaboration.Host{
		{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://source.example.test"},
		{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://wrong.example.test"},
		{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://github.example.test"},
	}
	for _, host := range hosts {
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
	}
	before := retainedLedgerFingerprint(t, s)
	for _, attempt := range []struct {
		id   collaboration.HostID
		base string
	}{{hosts[2].ID, hosts[2].APIBase}, {hosts[1].ID, hosts[0].APIBase}, {hosts[0].ID, hosts[1].APIBase}} {
		if err := s.AdoptFountainLedger(ctx, attempt.id, attempt.base); !errors.Is(err, collaboration.ErrIdentityConflict) {
			t.Fatalf("wrong source accepted: %v", err)
		}
	}
	if after := retainedLedgerFingerprint(t, s); after != before {
		t.Fatal("refused source changed retained data")
	}
	if err := s.AdoptFountainLedger(ctx, hosts[0].ID, hosts[0].APIBase); err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptFountainLedger(ctx, hosts[1].ID, hosts[1].APIBase); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatalf("bound source was replaced: %v", err)
	}
	other, _ := postgresFixture(t)
	if err := other.InitializeEmptyCollaborationLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := other.PutCollaborationHost(ctx, hosts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ObserveCollaborationRepository(ctx, hosts[0].ID, "700", "already-mapped"); err != nil {
		t.Fatal(err)
	}
	if err := other.AdoptFountainLedger(ctx, hosts[0].ID, hosts[0].APIBase); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatalf("unbound existing mappings were silently reconciled: %v", err)
	}
	if configured, err := other.BindConfiguredFountainLedger(ctx, "https://new-source.example.test"); !errors.Is(err, collaboration.ErrIdentityConflict) || configured.ID != "" {
		t.Fatalf("configured composition claimed an unbound provider namespace: %+v %v", configured, err)
	}
	if _, err := other.CollaborationHostByAPIBase(ctx, collaboration.Fountain, "https://new-source.example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed configured composition left a registered host: %v", err)
	}
}

func TestCollaborationFountainAdoptionRollsBackAndSerializes(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := t.Context()
	if err := s.UpsertUser(ctx, User{ID: 42, Login: "retained"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, Project{ID: "p1123456789abcdef01234567", Name: "Retained", RepositoryID: 7, OwnerID: 42, Repository: "team/retained"}); err != nil {
		t.Fatal(err)
	}
	host := collaboration.Host{Provider: collaboration.Fountain, APIBase: "https://source.example.test"}
	for _, ddl := range []string{
		`CREATE FUNCTION soda_test_refuse_actor() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected import failure'; END $$`,
		`CREATE TRIGGER test_refuse_actor BEFORE INSERT ON collaboration_actors FOR EACH ROW EXECUTE FUNCTION soda_test_refuse_actor()`,
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	var sequenceBefore string
	if err := s.db.QueryRowContext(ctx, `SELECT last_value::text||':'||is_called::text FROM collaboration_repositories_id_seq`).Scan(&sequenceBefore); err != nil {
		t.Fatal(err)
	}
	before := retainedLedgerFingerprint(t, s)
	if _, err := s.BindConfiguredFountainLedger(ctx, host.APIBase); err == nil {
		t.Fatal("injected failure was ignored")
	}
	if _, err := s.CollaborationHostByAPIBase(ctx, collaboration.Fountain, host.APIBase); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed configured adoption committed a host: %v", err)
	}
	var adopted bool
	var mappings int
	var sequenceAfter string
	if err := s.db.QueryRowContext(ctx, `SELECT adopted FROM collaboration_ledger WHERE singleton`).Scan(&adopted); err != nil || adopted {
		t.Fatalf("partial binding committed: %v %v", adopted, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_repositories`).Scan(&mappings); err != nil || mappings != 0 {
		t.Fatalf("partial mappings committed: %d %v", mappings, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT last_value::text||':'||is_called::text FROM collaboration_repositories_id_seq`).Scan(&sequenceAfter); err != nil || sequenceAfter != sequenceBefore {
		t.Fatalf("failed adoption advanced allocator: %s -> %s %v", sequenceBefore, sequenceAfter, err)
	}
	if after := retainedLedgerFingerprint(t, s); after != before {
		t.Fatal("failed adoption changed retained data")
	}
	for _, ddl := range []string{`DROP TRIGGER test_refuse_actor ON collaboration_actors`, `DROP FUNCTION soda_test_refuse_actor()`} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	const attempts = 8
	type bindingResult struct {
		host collaboration.Host
		err  error
	}
	results := make(chan bindingResult, attempts)
	for range attempts {
		go func() {
			configured, err := s.BindConfiguredFountainLedger(ctx, "https://SOURCE.example.test:443/")
			results <- bindingResult{configured, err}
		}()
	}
	for i := range attempts {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if i == 0 {
			host = result.host
		} else if result.host != host {
			t.Fatalf("concurrent configured binding selected different hosts: %+v %+v", host, result.host)
		}
	}
	if got, err := s.CollaborationRepository(ctx, 7); err != nil || got.Host != host.ID {
		t.Fatalf("concurrent adoption lost retained mapping: %+v %v", got, err)
	}
	if configured, err := s.BindConfiguredFountainLedger(ctx, "https://different-source.example.test"); !errors.Is(err, collaboration.ErrIdentityConflict) || configured.ID != "" {
		t.Fatalf("configured composition replaced a bound source: %+v %v", configured, err)
	}
	var hosts int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_hosts`).Scan(&hosts); err != nil || hosts != 1 {
		t.Fatalf("concurrent/refused composition forked host identity: %d %v", hosts, err)
	}
	if after := retainedLedgerFingerprint(t, s); after != before {
		t.Fatal("configured composition changed native/workflow data")
	}
}

func TestCollaborationRegistryIdentityAndConnections(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
		t.Fatal(err)
	}
	profile := collaboration.Profile{ID: collaboration.ProfileID(collaboration.NewID()), Name: "Native profile"}
	if err := s.PutCollaborationProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	hosts := []collaboration.Host{
		{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://forge.example.test"},
		{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://GITHUB.example.test:443/api/v3/"},
		{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://other-forge.example.test"},
	}
	var repositories []collaboration.RepositoryRef
	var actors []collaboration.ActorRef
	for i, host := range hosts {
		stored, err := s.PutCollaborationHost(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		hosts[i] = stored
		repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "7", "team/shared")
		if err != nil {
			t.Fatal(err)
		}
		repositories = append(repositories, repo)
		actor, err := s.ObserveCollaborationActor(ctx, host.ID, "42", "same-login")
		if err != nil {
			t.Fatal(err)
		}
		actors = append(actors, actor)
	}
	if hosts[1].APIBase != "https://github.example.test/api/v3" {
		t.Fatalf("uncanonical instance: %s", hosts[1].APIBase)
	}
	if found, err := s.CollaborationHostByAPIBase(ctx, collaboration.GitHub, "https://GITHUB.EXAMPLE.TEST:443/api/v3/"); err != nil || found != hosts[1] {
		t.Fatalf("reconnect lost configured host: %+v %v", found, err)
	}
	for i := range repositories {
		for j := 0; j < i; j++ {
			if repositories[i].ID == repositories[j].ID || actors[i].ID == actors[j].ID {
				t.Fatal("provider instances aliased remote numeric IDs")
			}
		}
	}
	duplicate := hosts[1]
	duplicate.ID = collaboration.HostID(collaboration.NewID())
	if _, err := s.PutCollaborationHost(ctx, duplicate); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatalf("duplicate instance locator accepted: %v", err)
	}
	var connections []collaboration.ConnectionRef
	for range 2 {
		connection := collaboration.ConnectionRef{ID: collaboration.ConnectionID(collaboration.NewID()), Profile: profile.ID, Host: hosts[1].ID, Actor: actors[1].ID}
		if err := s.PutCollaborationConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
		if err := s.AssociateCollaborationRepository(ctx, repositories[1].ID, connection.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.PutCollaborationConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	otherActor, err := s.ObserveCollaborationActor(ctx, hosts[1].ID, "43", "another-account")
	if err != nil {
		t.Fatal(err)
	}
	otherConnection := collaboration.ConnectionRef{ID: collaboration.ConnectionID(collaboration.NewID()), Profile: profile.ID, Host: hosts[1].ID, Actor: otherActor.ID}
	if err := s.PutCollaborationConnection(ctx, otherConnection); err != nil {
		t.Fatal(err)
	}
	if err := s.AssociateCollaborationRepository(ctx, repositories[1].ID, otherConnection.ID); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collaboration_repository_connections WHERE repository=$1`, repositories[1].ID).Scan(&links); err != nil || links != 3 {
		t.Fatalf("connections forked repo identity: %d %v", links, err)
	}
	renamed, err := s.ObserveCollaborationRepository(ctx, hosts[1].ID, "7", "new-owner/renamed")
	if err != nil || renamed.ID != repositories[1].ID {
		t.Fatalf("repo rename changed identity: %+v %v", renamed, err)
	}
	actor, err := s.ObserveCollaborationActor(ctx, hosts[1].ID, "42", "renamed-account")
	if err != nil || actor.ID != actors[1].ID {
		t.Fatalf("actor rename changed identity: %+v %v", actor, err)
	}
	hosts[1].APIBase = "https://moved-github.example.test/api/v3"
	if _, err := s.PutCollaborationHost(ctx, hosts[1]); err != nil {
		t.Fatal(err)
	}
	profile.Name = "Renamed profile"
	if err := s.PutCollaborationProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	runtime := collaboration.RuntimeRef{ID: collaboration.RuntimeID(collaboration.NewID()), Profile: profile.ID, Kind: collaboration.Native, Name: "Workstation"}
	if err := s.PutCollaborationRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	runtime.Name = "Renamed workstation"
	if err := s.PutCollaborationRuntime(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.CollaborationProfile(ctx, profile.ID); err != nil || got != profile {
		t.Fatalf("reopen lost profile: %+v %v", got, err)
	}
	if got, err := reopened.CollaborationRepository(ctx, renamed.ID); err != nil || got != renamed {
		t.Fatalf("reopen lost repo identity: %+v %v", got, err)
	}
	if got, err := reopened.CollaborationActor(ctx, actor.ID); err != nil || got != actor {
		t.Fatalf("reopen lost actor: %+v %v", got, err)
	}
	if got, err := reopened.CollaborationHost(ctx, hosts[1].ID); err != nil || got != hosts[1] {
		t.Fatalf("reopen lost moved instance: %+v %v", got, err)
	}
	if got, err := reopened.CollaborationConnection(ctx, connections[0].ID); err != nil || got != connections[0] {
		t.Fatalf("reopen lost connection: %+v %v", got, err)
	}
	if got, err := reopened.CollaborationRuntime(ctx, runtime.ID); err != nil || got != runtime {
		t.Fatalf("reopen lost runtime: %+v %v", got, err)
	}
}

func TestCollaborationRegistryConstraintsAndConcurrentObservation(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := t.Context()
	if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
		t.Fatal(err)
	}
	profile := collaboration.Profile{ID: collaboration.ProfileID(collaboration.NewID()), Name: "Profile"}
	if err := s.PutCollaborationProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	a := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://github.example.test"}
	b := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://forge.example.test"}
	for _, host := range []collaboration.Host{a, b} {
		if _, err := s.PutCollaborationHost(ctx, host); err != nil {
			t.Fatal(err)
		}
	}
	actor, err := s.ObserveCollaborationActor(ctx, a.ID, "42", "author")
	if err != nil {
		t.Fatal(err)
	}
	wrong := collaboration.ConnectionRef{ID: collaboration.ConnectionID(collaboration.NewID()), Profile: profile.ID, Host: b.ID, Actor: actor.ID}
	if err := s.PutCollaborationConnection(ctx, wrong); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatalf("cross-host actor binding accepted: %v", err)
	}
	connection := wrong
	connection.Host = a.ID
	if err := s.PutCollaborationConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	foreign, err := s.ObserveCollaborationRepository(ctx, b.ID, "7", "team/shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssociateCollaborationRepository(ctx, foreign.ID, connection.ID); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatalf("cross-host repository binding accepted: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO collaboration_repository_connections(repository,connection,host) VALUES($1,$2,$3)`, foreign.ID, connection.ID, b.ID); err == nil {
		t.Fatal("database allowed cross-host relation")
	}
	otherProfile := collaboration.Profile{ID: collaboration.ProfileID(collaboration.NewID()), Name: "Other profile"}
	if err := s.PutCollaborationProfile(ctx, otherProfile); err != nil {
		t.Fatal(err)
	}
	rebound := connection
	rebound.Profile = otherProfile.ID
	if err := s.PutCollaborationConnection(ctx, rebound); !errors.Is(err, collaboration.ErrIdentityConflict) {
		t.Fatalf("connection moved profiles: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE collaboration_repositories SET external_id='different-repository' WHERE id=$1`, foreign.ID); err == nil {
		t.Fatal("immutable repository was rebound")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM collaboration_actors WHERE id=$1`, actor.ID); err == nil {
		t.Fatal("identity history was deleted")
	}
	const count = 16
	type result struct {
		id  collaboration.RepositoryID
		err error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			repo, err := s.ObserveCollaborationRepository(ctx, a.ID, "opaque-repo-node", "team/concurrent")
			results <- result{repo.ID, err}
		}()
	}
	wg.Wait()
	close(results)
	var id collaboration.RepositoryID
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if id != 0 && id != got.id {
			t.Fatal("concurrent observation forked repository")
		}
		id = got.id
	}
	// Retained BIGINT identities must fit the registry and every composite FK,
	// even when the old INTEGER-oriented core is not yet cut over to its ports.
	for _, table := range []string{"collaboration_repositories", "collaboration_actors"} {
		if _, err := s.db.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence($1,'id'),4294967296,true)`, table); err != nil {
			t.Fatal(err)
		}
	}
	largeRepo, err := s.ObserveCollaborationRepository(ctx, a.ID, "large-repository", "team/large")
	if err != nil || largeRepo.ID != 4294967297 {
		t.Fatalf("large repository surrogate lost: %+v %v", largeRepo, err)
	}
	largeActor, err := s.ObserveCollaborationActor(ctx, a.ID, "large-actor", "large-account")
	if err != nil || largeActor.ID != 4294967297 {
		t.Fatalf("large actor surrogate lost: %+v %v", largeActor, err)
	}
	largeConnection := collaboration.ConnectionRef{ID: collaboration.ConnectionID(collaboration.NewID()), Profile: profile.ID, Host: a.ID, Actor: largeActor.ID}
	if err := s.PutCollaborationConnection(ctx, largeConnection); err != nil {
		t.Fatal(err)
	}
	if err := s.AssociateCollaborationRepository(ctx, largeRepo.ID, largeConnection.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCollaborationRegistryRetainedDataWaitsForAdoption(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	if err := s.UpsertUser(ctx, User{ID: 42, Login: "retained-person"}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "p0123456789abcdef01234567", Name: "Retained project", RepositoryID: 7, OwnerID: 42, Repository: "team/retained"}
	if err := s.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	post := saveTestPost(t, s, 7, "42", "retained work", "registry-adoption")
	if err := s.RecordPostIntake(ctx, post, 0); err != nil {
		t.Fatal(err)
	}
	// Stage a genuine pre-registry database only in this disposable fixture.
	for _, ddl := range []string{
		`DROP TABLE collaboration_repository_connections,collaboration_connections,collaboration_runtimes,collaboration_repositories,collaboration_actors,collaboration_teams,collaboration_ledger,collaboration_profiles,collaboration_hosts`,
		`DROP FUNCTION soda_collaboration_allocation()`, `DROP FUNCTION soda_collaboration_identity()`,
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	host := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://github.example.test"}
	if err := reopened.InitializeEmptyCollaborationLedger(ctx); !errors.Is(err, collaboration.ErrUnboundLedger) {
		t.Fatalf("retained ledger initialized as empty: %v", err)
	}
	if _, err := reopened.PutCollaborationHost(ctx, host); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ObserveCollaborationRepository(ctx, host.ID, "7", "team/new"); !errors.Is(err, collaboration.ErrUnboundLedger) {
		t.Fatalf("retained ledger was implicitly claimed: %v", err)
	}
	if _, err := reopened.ObserveCollaborationActor(ctx, host.ID, "42", "new-account"); !errors.Is(err, collaboration.ErrUnboundLedger) {
		t.Fatalf("retained actor namespace was reused: %v", err)
	}
	if got, err := reopened.ProjectByRepository(ctx, 7); err != nil || got.RepositoryID != project.RepositoryID || got.OwnerID != 42 {
		t.Fatalf("retained Project changed: %+v %v", got, err)
	}
	if got, err := reopened.FeedPost(ctx, post); err != nil || got.Body != "retained work" || got.Record.Repository != 7 {
		t.Fatalf("retained work changed: %+v %v", got, err)
	}
	if got, err := reopened.PostIntake(ctx, post); err != nil || got.SourcePost != post || got.Revision != 0 {
		t.Fatalf("retained intake changed: %+v %v", got, err)
	}
	t.Run("scoped_resolution_after_adoption", func(t *testing.T) {
		if _, err := reopened.NativeProjectForCollaborationRepository(ctx, 7); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unadopted native Project inferred from numeric equality: %v", err)
		}
		if _, err := reopened.CollaborationRepositoryForNativeProject(ctx, project.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unadopted native Project selected a provider: %v", err)
		}
		if _, err := reopened.CollaborationRepositoryByExternalID(ctx, host.ID, "7"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing repository lookup allocated an identity: %v", err)
		}
		if _, err := reopened.CollaborationActorByExternalID(ctx, host.ID, "42"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing actor lookup allocated an identity: %v", err)
		}
		fountain := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://retained-fountain.example.test"}
		other := collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://other-fountain.example.test"}
		for _, ref := range []collaboration.Host{fountain, other} {
			if _, err := reopened.PutCollaborationHost(ctx, ref); err != nil {
				t.Fatal(err)
			}
		}
		if err := reopened.AdoptFountainLedger(ctx, fountain.ID, fountain.APIBase); err != nil {
			t.Fatal(err)
		}
		githubRepo, err := reopened.ObserveCollaborationRepository(ctx, host.ID, "7", "same/name")
		if err != nil {
			t.Fatal(err)
		}
		githubActor, err := reopened.ObserveCollaborationActor(ctx, host.ID, "42", "same-login")
		if err != nil {
			t.Fatal(err)
		}
		// A later native Project's raw keys coincide with GitHub's local IDs.
		// Its own Fountain surrogates will deliberately be larger than 2^53.
		native := Project{ID: "later-native-project", Name: "Later", RepositoryID: int64(githubRepo.ID), OwnerID: int64(githubActor.ID), Repository: "same/name"}
		if err := reopened.UpsertUser(ctx, User{ID: native.OwnerID, Login: "later-native-owner"}); err != nil {
			t.Fatal(err)
		}
		if err := reopened.CreateProject(ctx, native); err != nil {
			t.Fatal(err)
		}
		tx, err := reopened.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		const large int64 = 9007199254740993
		for _, table := range []string{"collaboration_repositories", "collaboration_actors"} {
			if err := restartCollaborationSequence(ctx, tx, table, large); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		externalRepo, externalActor := strconv.FormatInt(native.RepositoryID, 10), strconv.FormatInt(native.OwnerID, 10)
		localRepo, err := reopened.ObserveCollaborationRepository(ctx, fountain.ID, externalRepo, "same/name")
		if err != nil || int64(localRepo.ID) < large {
			t.Fatalf("large Fountain repository observation: %+v %v", localRepo, err)
		}
		localActor, err := reopened.ObserveCollaborationActor(ctx, fountain.ID, externalActor, "same-login")
		if err != nil || int64(localActor.ID) < large {
			t.Fatalf("large Fountain actor observation: %+v %v", localActor, err)
		}
		foreignRepo, err := reopened.ObserveCollaborationRepository(ctx, other.ID, externalRepo, "same/name")
		if err != nil {
			t.Fatal(err)
		}
		opaqueRepo, err := reopened.ObserveCollaborationRepository(ctx, fountain.ID, "0"+externalRepo, "same/name")
		if err != nil {
			t.Fatal(err)
		}
		localRepo, err = reopened.ObserveCollaborationRepository(ctx, fountain.ID, externalRepo, "renamed/team")
		if err != nil {
			t.Fatal(err)
		}
		localActor, err = reopened.ObserveCollaborationActor(ctx, fountain.ID, externalActor, "renamed-account")
		if err != nil {
			t.Fatal(err)
		}
		fountain.APIBase = "https://moved-fountain.example.test"
		if _, err := reopened.PutCollaborationHost(ctx, fountain); err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
		db, err := Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		registrySnapshot := func() string {
			var snapshot string
			if err := db.db.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'repos',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM collaboration_repositories r),
 'actors',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM collaboration_actors a),
 'repo_sequence',(SELECT jsonb_build_array(last_value,is_called) FROM collaboration_repositories_id_seq),
 'actor_sequence',(SELECT jsonb_build_array(last_value,is_called) FROM collaboration_actors_id_seq))::text`).Scan(&snapshot); err != nil {
				t.Fatal(err)
			}
			return snapshot
		}
		registryBefore, workflowBefore := registrySnapshot(), retainedLedgerFingerprint(t, db)
		localAuthority, err := db.FactoryAuthorityInput(ctx, int64(localRepo.ID))
		if err != nil || !localAuthority.ProjectExists {
			t.Fatalf("large canonical authority lost its native Project: %+v %v", localAuthority, err)
		}
		foreignAuthority, err := db.FactoryAuthorityInput(ctx, int64(githubRepo.ID))
		if err != nil || foreignAuthority.ProjectExists {
			t.Fatalf("foreign authority borrowed a colliding native Project: %+v %v", foreignAuthority, err)
		}
		read, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Rollback()
		repository, issue, assignment, err := factoryRunFeedScopeTx(ctx, read, "unassigned-run", native.ID)
		if err != nil || repository != int64(localRepo.ID) || issue != 0 || assignment != "" {
			t.Fatalf("unassigned run feed used native rather than canonical repository: %d %d %q %v", repository, issue, assignment, err)
		}
		if err := read.Commit(); err != nil {
			t.Fatal(err)
		}
		if got, err := db.CollaborationHostByAPIBase(ctx, collaboration.Fountain, fountain.APIBase); err != nil || got != fountain {
			t.Fatalf("reconnect to moved locator lost stable host: %+v %v", got, err)
		}
		if got, err := db.CollaborationRepositoryOnHost(ctx, fountain.ID, localRepo.ID); err != nil || got != localRepo {
			t.Fatalf("local repository resolution after rename/reopen: %+v %v", got, err)
		}
		if got, err := db.CollaborationRepositoryByExternalID(ctx, fountain.ID, externalRepo); err != nil || got != localRepo {
			t.Fatalf("external repository resolution after rename/reopen: %+v %v", got, err)
		}
		if got, err := db.CollaborationActorOnHost(ctx, fountain.ID, localActor.ID); err != nil || got != localActor {
			t.Fatalf("local actor resolution after rename/reopen: %+v %v", got, err)
		}
		if got, err := db.CollaborationActorByExternalID(ctx, fountain.ID, externalActor); err != nil || got != localActor {
			t.Fatalf("external actor resolution after rename/reopen: %+v %v", got, err)
		}
		if got, err := db.NativeProjectForCollaborationRepository(ctx, localRepo.ID); err != nil || got != native {
			t.Fatalf("local surrogate did not resolve exact native Project/owner: %+v %v", got, err)
		}
		if got, err := db.CollaborationRepositoryForNativeProject(ctx, native.ID); err != nil || got != localRepo {
			t.Fatalf("native Project inverse resolution: %+v %v", got, err)
		}
		if got, err := db.NativeProjectForCollaborationRepository(ctx, 7); err != nil || got != project {
			t.Fatalf("retained Project resolution: %+v %v", got, err)
		}
		for _, repository := range []collaboration.RepositoryID{githubRepo.ID, foreignRepo.ID, opaqueRepo.ID, collaboration.RepositoryID(large + 100)} {
			if _, err := db.NativeProjectForCollaborationRepository(ctx, repository); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign/missing/noncanonical native mapping selected a Project (%d): %v", repository, err)
			}
		}
		if _, err := db.CollaborationRepositoryOnHost(ctx, fountain.ID, githubRepo.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("local repository crossed hosts: %v", err)
		}
		if _, err := db.CollaborationActorOnHost(ctx, fountain.ID, githubActor.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("local actor crossed hosts: %v", err)
		}
		if got, err := db.CollaborationRepositoryByExternalID(ctx, host.ID, "7"); err != nil || got != githubRepo {
			t.Fatalf("colliding GitHub repository changed host: %+v %v", got, err)
		}
		if got, err := db.CollaborationActorByExternalID(ctx, host.ID, "42"); err != nil || got != githubActor {
			t.Fatalf("colliding GitHub actor changed host: %+v %v", got, err)
		}
		if _, err := db.CollaborationRepositoryByExternalID(ctx, fountain.ID, "renamed/team"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("display name selected a repository: %v", err)
		}
		if _, err := db.CollaborationActorByExternalID(ctx, fountain.ID, "renamed-account"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("login selected an actor: %v", err)
		}
		if _, err := db.CollaborationRepositoryForNativeProject(ctx, "missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing native Project inferred a mapping: %v", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := db.NativeProjectForCollaborationRepository(canceled, localRepo.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled resolution became absence: %v", err)
		}
		if _, err := db.CollaborationRepositoryOnHost(ctx, fountain.ID, 0); !errors.Is(err, collaboration.ErrIdentityConflict) {
			t.Fatalf("invalid local identity accepted: %v", err)
		}
		if registrySnapshot() != registryBefore || retainedLedgerFingerprint(t, db) != workflowBefore {
			t.Fatal("lookup changed identities, allocator state or native/workflow records")
		}
	})
}

func TestOpenCreatesOnlyTheCurrentSchema(t *testing.T) {
	t.Run("factory_bigint_retained_storage", func(t *testing.T) {
		s, dsn := postgresFixture(t)
		ctx := t.Context()
		t.Cleanup(func() {
			if s != nil {
				_ = s.Close()
			}
		})
		for _, group := range factoryIdentityColumns {
			for _, column := range group.columns {
				var kind string
				if err := s.db.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, group.table, column).Scan(&kind); err != nil || kind != "bigint" {
					t.Fatalf("fresh %s.%s: %s %v", group.table, column, kind, err)
				}
			}
		}
		if err := s.UpsertUser(ctx, User{ID: 42, Login: "native-owner"}); err != nil {
			t.Fatal(err)
		}
		native := Project{ID: "native-project", Name: "Native", RepositoryID: 7, OwnerID: 42, Repository: "team/native"}
		if err := s.CreateProject(ctx, native); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveRepositoryPolicy(ctx, grantTestPolicy()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES('retained-bigint',7,3,'retained-run','finished',0,'{"repository":"7","issue":"3","keep":true}')`); err != nil {
			t.Fatal(err)
		}
		// Recreate the retained INTEGER shapes with actual rows. The upgrade
		// must keep every value, including JSON and native Project ownership.
		for _, group := range factoryIdentityColumns {
			for _, column := range group.columns {
				if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+quoteCollaborationIdentifier(group.table)+` ALTER COLUMN `+quoteCollaborationIdentifier(column)+` TYPE INTEGER`); err != nil {
					t.Fatal(err)
				}
			}
		}
		before := retainedLedgerFingerprint(t, s)
		// An unsupported later column must roll back the earlier widenings.
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_environment_grants ALTER COLUMN repository TYPE NUMERIC`); err != nil {
			t.Fatal(err)
		}
		if err := initializeFactoryIdentitySchema(ctx, s.db); err == nil {
			t.Fatal("unknown retained identity type accepted")
		}
		var kind string
		if err := s.db.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns WHERE table_name='factory_policies' AND column_name='repository'`).Scan(&kind); err != nil || kind != "integer" || retainedLedgerFingerprint(t, s) != before {
			t.Fatalf("refused widening changed retained data/schema: %s %v", kind, err)
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_environment_grants ALTER COLUMN repository TYPE INTEGER`); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		s, err = Open(dsn)
		if err != nil {
			t.Fatalf("open retained INTEGER Factory: %v", err)
		}
		if retainedLedgerFingerprint(t, s) != before {
			t.Fatal("widening rewrote retained workflow rows")
		}
		for _, group := range factoryIdentityColumns {
			for _, column := range group.columns {
				if err := s.db.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, group.table, column).Scan(&kind); err != nil || kind != "bigint" {
					t.Fatalf("retained %s.%s: %s %v", group.table, column, kind, err)
				}
			}
		}
		hosts := []collaboration.Host{
			{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: "https://retained.example.test"},
			{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"},
		}
		for _, host := range hosts {
			if _, err := s.PutCollaborationHost(ctx, host); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.AdoptFountainLedger(ctx, hosts[0].ID, hosts[0].APIBase); err != nil {
			t.Fatal(err)
		}
		// Exercise values above both int32 and JavaScript's exact-number limit.
		const large int64 = 9007199254740993
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, table := range []string{"collaboration_repositories", "collaboration_actors"} {
			if err := restartCollaborationSequence(ctx, tx, table, large); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var repos []collaboration.RepositoryRef
		for i, host := range hosts {
			repo, err := s.ObserveCollaborationRepository(ctx, host.ID, "7", "team/shared")
			if err != nil {
				t.Fatal(err)
			}
			actor, err := s.ObserveCollaborationActor(ctx, host.ID, "42", "same-login")
			if err != nil {
				t.Fatal(err)
			}
			if i == 1 && (int64(repo.ID) < large || int64(actor.ID) < large) {
				t.Fatal("large registry allocator did not feed Factory persistence")
			}
			repos = append(repos, repo)
			policy := grantTestPolicy()
			policy.Repository, policy.GrantedBy = int64(repo.ID), int64(actor.ID)
			if err := s.SaveRepositoryPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_acceptance_withdrawals(repository,issue,decision,withdrawer) VALUES($1,$2,'large-withdrawal',$3)`, repo.ID, large+1, actor.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_check_assessments(repository,pr,revision,data) VALUES($1,$2,1,'{}')`, repo.ID, large+2); err != nil {
				t.Fatal(err)
			}
		}
		if repos[0].ID != 7 || repos[0].ID == repos[1].ID {
			t.Fatal("retained and GitHub numeric identities collided")
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO keys(user_id,public,fingerprint) VALUES(999,'x','y')`); err == nil {
			t.Fatal("native user FK lost during Factory widening")
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES('invalid-bigint',1,0,'r','finished',0,'{}')`); err == nil {
			t.Fatal("assignment issue scope check lost during widening")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dsn)
		if err != nil {
			t.Fatalf("reopen widened cross-provider ledger: %v", err)
		}
		for _, repo := range repos {
			policy, err := s.RepositoryPolicy(ctx, int64(repo.ID))
			if err != nil || policy.Repository != int64(repo.ID) || policy.Revision != 1 {
				t.Fatalf("cross-provider policy round trip: %+v %v", policy, err)
			}
			actor, err := s.CollaborationActor(ctx, collaboration.ActorID(policy.GrantedBy))
			if err != nil || actor.Host != repo.Host || actor.ExternalID != "42" {
				t.Fatalf("cross-provider actor round trip: %+v %v", actor, err)
			}
			var issue, withdrawer, pr int64
			if err := s.db.QueryRowContext(ctx, `SELECT issue,withdrawer FROM issue_acceptance_withdrawals WHERE repository=$1`, repo.ID).Scan(&issue, &withdrawer); err != nil || issue != large+1 || withdrawer != policy.GrantedBy {
				t.Fatalf("large withdrawal round trip: %d %d %v", issue, withdrawer, err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT pr FROM factory_check_assessments WHERE repository=$1`, repo.ID).Scan(&pr); err != nil || pr != large+2 {
				t.Fatalf("large PR round trip: %d %v", pr, err)
			}
		}
		if got, err := s.Project(ctx, native.ID); err != nil || got != native {
			t.Fatalf("native Project identity was reinterpreted: %+v %v", got, err)
		}
	})
	s, _ := postgresFixture(t)
	ctx := context.Background()
	var version, count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*),max(version) FROM schema_version`).Scan(&count, &version); err != nil || count != 1 || version != SchemaVersion() {
		t.Fatalf("schema version: count=%d version=%d err=%v", count, version, err)
	}
	var retired int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('sessions','oauth','session_grants','login_contexts')`).Scan(&retired); err != nil || retired != 0 {
		t.Fatalf("retired browser auth tables: count=%d err=%v", retired, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO keys(user_id,public,fingerprint) VALUES(999,'x','y')`); err == nil {
		t.Fatal("foreign key enforcement lost")
	}
	// Adopt only the exact prior scope checks, without replacing retained
	// assignment/allowance rows. A forged extra alternative must stay refused.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES('retained-scope',7,3,'retained-run','finished',0,'{"keep":true}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_attempt_allowances(repository,issue,root_assignment,revision,data) VALUES(7,3,'retained-scope',0,'{"keep":true}')`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"factory_assignments", "factory_attempt_allowances"} {
		branch, end := deliveryRepairAssignmentScope, "), run TEXT NOT NULL"
		if table == "factory_attempt_allowances" {
			branch, end = deliveryRepairAllowanceScope, "), root_assignment TEXT PRIMARY KEY"
		}
		var old string
		for _, statement := range schemaStatements {
			if strings.HasPrefix(statement, "CREATE TABLE "+table+"(") {
				_, tail, _ := strings.Cut(statement, "issue BIGINT NOT NULL CHECK(")
				expression, _, _ := strings.Cut(tail, end)
				old, _ = strings.CutSuffix(expression, " OR ("+branch+")")
			}
		}
		var name string
		if err := s.db.QueryRowContext(ctx, `SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%delivery_repair%'`, table).Scan(&name); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` DROP CONSTRAINT `+name+`, ADD CONSTRAINT `+table+`_issue_scope_check CHECK(`+old+`)`); err != nil {
			t.Fatal(err)
		}
	}
	if err := initializeDeliveryRepairSchema(ctx, s.db); err != nil {
		t.Fatalf("exact retained delivery repair scope adoption: %v", err)
	}
	if err := initializeDeliveryRepairSchema(ctx, s.db); err != nil {
		t.Fatalf("delivery repair scope adoption replay: %v", err)
	}
	for _, table := range []string{"factory_assignments", "factory_attempt_allowances"} {
		var retained bool
		if err := s.db.QueryRowContext(ctx, `SELECT data->>'keep'='true' FROM `+table+` WHERE repository=7 AND issue=3`).Scan(&retained); err != nil || !retained {
			t.Fatalf("%s retained row changed: %v", table, err)
		}
	}
	var scope string
	if err := s.db.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='factory_assignments_issue_scope_check'`).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	unknown := scope[:len(scope)-1] + " OR true)"
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE factory_assignments DROP CONSTRAINT factory_assignments_issue_scope_check, ADD CONSTRAINT factory_assignments_issue_scope_check `+unknown); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='factory_assignments_issue_scope_check'`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if err := initializeDeliveryRepairSchema(ctx, s.db); err == nil {
		t.Fatal("unknown retained scope was silently adopted")
	}
	var after string
	if err := s.db.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='factory_assignments_issue_scope_check'`).Scan(&after); err != nil || after != unknown {
		t.Fatalf("refused scope changed: %v", err)
	}
}

// legacyFingerprint captures the staged legacy content a refused Open must
// leave untouched: the version marker, the table inventory and one legacy
// row.
func legacyFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	var version int
	if err := db.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := db.QueryRow(`SELECT token FROM sessions LIMIT 1`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(version) + "|" + strings.Join(tables, ",") + "|" + token
}

func TestOpenRejectsOldSchemaWithoutMutation(t *testing.T) {
	admin, ok := TestFixtureDSN()
	if !ok {
		t.Skip("SODA_PG_* fixture unavailable")
	}
	for _, version := range []int{1, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29} {
		t.Run("version_"+strconv.Itoa(version), func(t *testing.T) {
			ctx := context.Background()
			dsn, drop, err := createEphemeralDatabase(ctx, admin)
			if err != nil {
				t.Fatal(err)
			}
			defer drop()
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, statement := range []string{
				`CREATE TABLE schema_version(version INTEGER PRIMARY KEY)`,
				`CREATE TABLE sessions(token TEXT PRIMARY KEY)`,
				`CREATE TABLE oauth(state TEXT PRIMARY KEY)`,
				`INSERT INTO sessions(token) VALUES('preserve-this-file')`,
			} {
				if _, err = db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = db.Exec(`INSERT INTO schema_version(version) VALUES($1)`, version); err != nil {
				t.Fatal(err)
			}
			before := legacyFingerprint(t, db)
			if s, err := Open(dsn); err == nil {
				_ = s.Close()
				t.Fatal("unsupported schema accepted")
			}
			if after := legacyFingerprint(t, db); after != before {
				t.Fatal("unsupported database was modified")
			}
			observed, err := ReadSchemaVersion(ctx, dsn)
			if err != nil || observed != version {
				t.Fatalf("historical version observation: got %d, err %v", observed, err)
			}
		})
	}
}

func TestOpenRejectsUnversionedDataWithoutMutation(t *testing.T) {
	admin, ok := TestFixtureDSN()
	if !ok {
		t.Skip("SODA_PG_* fixture unavailable")
	}
	ctx := context.Background()
	dsn, drop, err := createEphemeralDatabase(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer drop()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE unrelated(value TEXT)`,
		`INSERT INTO unrelated VALUES('keep')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err = db.QueryRow(`SELECT value FROM unrelated LIMIT 1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(dsn); err == nil {
		_ = s.Close()
		t.Fatal("unversioned data accepted")
	}
	var after string
	if err = db.QueryRow(`SELECT value FROM unrelated LIMIT 1`).Scan(&after); err != nil || after != before {
		t.Fatalf("unversioned database was modified: %v", err)
	}
}
