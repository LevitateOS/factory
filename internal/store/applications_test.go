package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/application"
)

func applicationFixture() application.Record {
	return application.Record{Application: application.Spec{ID: "a" + strings.Repeat("1", 24), Revision: 1, Name: "Example", ArtifactSHA256: strings.Repeat("2", 64), BundleSHA256: strings.Repeat("3", 64), ToolsSHA256: strings.Repeat("4", 64), RuntimeImage: "sha256:" + strings.Repeat("5", 64), Image: "sha256:" + strings.Repeat("6", 64), Data: "a" + strings.Repeat("7", 24), OwnerTeam: "12345678-1234-1234-1234-123456789abc", Origin: "https://app.example.test", Port: 20001}, AdministratorID: 41}
}

func TestApplicationRestorePreservesIdentityAndDurablePendingState(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	record := applicationFixture()
	if err := s.CreateApplication(ctx, record); err != nil {
		t.Fatal(err)
	}
	before := application.Observation{ID: record.Application.ID, Revision: 1, Container: strings.Repeat("a", 64), Image: record.Application.Image, Origin: record.Application.Origin}
	if err := s.ObserveApplication(ctx, record.Application.ID, 41, 1, nil, &before); err != nil {
		t.Fatal(err)
	}
	op := application.RestoreOperation{ID: "a" + strings.Repeat("8", 24), Snapshot: "a" + strings.Repeat("9", 24), Previous: record.Application, Next: record.Application, ExpectedContainer: before.Container, State: "pending"}
	op.Next.Revision++
	op.Next.Data = "a" + strings.Repeat("b", 24)
	if _, err := s.db.ExecContext(ctx, `UPDATE applications SET data_id=$1 WHERE id=$2`, op.Next.Data, record.Application.ID); err == nil {
		t.Fatal("data changed outside restore")
	}
	if err := s.BeginApplicationRestore(ctx, 42, op); err == nil {
		t.Fatal("foreign administrator reserved restore")
	}
	if err := s.BeginApplicationRestore(ctx, 41, op); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pending, err := reopened.PendingApplicationRestore(ctx, record.Application.ID)
	if err != nil || pending == nil || *pending != op {
		t.Fatalf("pending restore lost on restart: %+v %v", pending, err)
	}
	current, err := reopened.Application(ctx, record.Application.ID, 41)
	if err != nil || current.Application != record.Application || current.Observation != nil {
		t.Fatalf("uncertain restore changed binding: %+v %v", current, err)
	}
	other := op
	other.ID = "a" + strings.Repeat("c", 24)
	other.Next.Data = "a" + strings.Repeat("d", 24)
	if err := s.BeginApplicationRestore(ctx, 41, other); err == nil {
		t.Fatal("second pending restore admitted")
	}
	observed := before
	observed.Revision = op.Next.Revision
	if err := s.SettleApplicationRestore(ctx, 41, record.Application.ID, op.ID, observed, false); err == nil {
		t.Fatal("old container accepted as restored")
	}
	observed.Container = strings.Repeat("b", 64)
	if err := s.SettleApplicationRestore(ctx, 41, record.Application.ID, op.ID, observed, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleApplicationRestore(ctx, 41, record.Application.ID, op.ID, observed, false); err != nil {
		t.Fatalf("completion replay: %v", err)
	}
	current, err = s.Application(ctx, record.Application.ID, 41)
	if err != nil || current.Application != op.Next || !application.SameObservation(current.Observation, &observed) {
		t.Fatalf("same-address restore not committed: %+v %v", current, err)
	}
	old, err := s.ApplicationRevision(ctx, record.Application.ID, 41, 1)
	if err != nil || old != record.Application {
		t.Fatalf("old data/reference lost: %+v %v", old, err)
	}
	if pending, err := s.PendingApplicationRestore(ctx, record.Application.ID); err != nil || pending != nil {
		t.Fatalf("completed restore remained pending: %+v %v", pending, err)
	}
	second := op
	second.ID = "a" + strings.Repeat("e", 24)
	second.Previous, second.Next = op.Next, op.Next
	second.Next.Revision++
	second.Next.Data = record.Application.Data
	second.ExpectedContainer = observed.Container
	if err := s.BeginApplicationRestore(ctx, 41, second); err == nil {
		t.Fatal("restore reused retained historical data")
	}
	second.Next.Data = "a" + strings.Repeat("f", 24)
	if err := s.BeginApplicationRestore(ctx, 41, second); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleApplicationRestore(ctx, 41, record.Application.ID, second.ID, observed, true); err != nil {
		t.Fatal(err)
	}
	current, err = s.Application(ctx, record.Application.ID, 41)
	if err != nil || current.Application != op.Next || !application.SameObservation(current.Observation, &observed) {
		t.Fatalf("aborted restore changed current data: %+v %v", current, err)
	}
}

func TestApplicationProjectAssociationNeedsTheApplicationAdministrator(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	record := applicationFixture()
	if err := s.CreateApplication(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := s.SetApplicationProject(ctx, record.Application.ID, 42, 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account changed project: %v", err)
	}
	if err := s.SetApplicationProject(ctx, record.Application.ID, 41, 7); err != nil {
		t.Fatal(err)
	}
	repository, err := s.ApplicationProject(ctx, record.Application.ID)
	if err != nil || repository != 7 {
		t.Fatalf("source project lost: %d %v", repository, err)
	}
	if _, err = s.Application(ctx, record.Application.ID, 42); !errors.Is(err, ErrNotFound) {
		t.Fatal("project link expanded application permission")
	}
}

func TestApplicationPersistenceAndAdministration(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	r := applicationFixture()
	if err := s.CreateApplication(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Application(ctx, r.Application.ID, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private record leaked: %v", err)
	}
	got, err := s.Application(ctx, r.Application.ID, 41)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("initial unknown %+v %v", got, err)
	}
	o := application.Observation{ID: r.Application.ID, Revision: 1, Container: strings.Repeat("a", 64), Image: r.Application.Image, Origin: r.Application.Origin, Running: true, Ready: true}
	if err := s.ObserveApplication(ctx, r.Application.ID, 41, 1, nil, &o); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveApplication(ctx, r.Application.ID, 41, 1, nil, nil); err == nil {
		t.Fatal("stale observation CAS accepted")
	}
	bad := o
	bad.Image = "sha256:" + strings.Repeat("b", 64)
	if err := s.ObserveApplication(ctx, r.Application.ID, 41, 1, &o, &bad); err == nil {
		t.Fatal("unadmitted image observation accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err = s.Application(ctx, r.Application.ID, 41)
	if err != nil || !application.SameObservation(got.Observation, &o) {
		t.Fatalf("reopen observation %+v %v", got, err)
	}
	if err := s.ObserveApplication(ctx, r.Application.ID, 41, 1, &o, nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.Application(ctx, r.Application.ID, 41)
	if err != nil || got.Observation != nil {
		t.Fatal("uncertain native result inferred readiness")
	}
}

func TestApplicationReplacementCASAndHistory(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	r := applicationFixture()
	if err := s.CreateApplication(ctx, r); err != nil {
		t.Fatal(err)
	}
	a, b := r.Application, r.Application
	a.Revision = 2
	b.Revision = 2
	a.Image = "sha256:" + strings.Repeat("a", 64)
	b.Image = "sha256:" + strings.Repeat("b", 64)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, next := range []application.Spec{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ReplaceApplication(ctx, 41, 1, next) }()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("CAS winners %d", success)
	}
	got, err := s.Application(ctx, r.Application.ID, 41)
	if err != nil || got.Application.Revision != 2 || got.Observation != nil {
		t.Fatalf("replacement %+v %v", got, err)
	}
	old, err := s.ApplicationRevision(ctx, r.Application.ID, 41, 1)
	if err != nil || old != r.Application {
		t.Fatalf("prior identity lost %+v %v", old, err)
	}
	changed := got.Application
	changed.Revision = 3
	changed.Data = "a" + strings.Repeat("c", 24)
	if err := s.ReplaceApplication(ctx, 41, 2, changed); err == nil {
		t.Fatal("private data silently changed")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE application_revisions SET spec='{}' WHERE application_id=$1`, r.Application.ID); err == nil {
		t.Fatal("revision history mutable")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE applications SET administrator_id=42 WHERE id=$1`, r.Application.ID); err == nil {
		t.Fatal("administrator grant mutable")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM application_revisions WHERE application_id=$1`, r.Application.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("history count %d %v", n, err)
	}
}

func setupRenewalFixture(tag string, port int, expiresAt int64) application.SetupRecord {
	return application.SetupRecord{
		ID: "a" + strings.Repeat(tag, 24),
		Application: application.SetupSpec{
			ID: "a" + strings.Repeat(tag, 24), Name: "Example",
			ArtifactSHA256: strings.Repeat("2", 64), BundleSHA256: strings.Repeat("3", 64), ToolsSHA256: strings.Repeat("4", 64),
			RuntimeImage: "sha256:" + strings.Repeat("5", 64), Image: "sha256:" + strings.Repeat("6", 64),
			Data: "a" + strings.Repeat(tag, 24), Origin: "https://setup-" + tag + ".example.test", Port: port,
		},
		AdministratorID: 41, ExpiresAt: expiresAt, State: "pending",
	}
}

func TestApplicationSetupRenewalPreservesReservation(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	now := time.Now().Unix()

	expired := setupRenewalFixture("b", 20011, now-10)
	if err := s.CreateApplicationSetup(ctx, expired); err != nil {
		t.Fatal(err)
	}
	renewed, err := s.RenewApplicationSetup(ctx, expired, 1)
	if err != nil {
		t.Fatalf("expired renewal refused: %v", err)
	}
	if renewed.ID != expired.ID || renewed.Application != expired.Application || renewed.AdministratorID != expired.AdministratorID || renewed.State != "pending" || renewed.Observation != nil {
		t.Fatalf("renewal changed the retained reservation: %+v", renewed)
	}
	if renewed.ExpiresAt < now+SetupRenewTTLSeconds-60 || renewed.ExpiresAt > time.Now().Unix()+SetupRenewTTLSeconds+60 {
		t.Fatalf("renewal window wrong: %d", renewed.ExpiresAt)
	}
	var renews, renewAdmin, renewExpiry int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*),max(administrator_id),max(expires_at) FROM application_setup_renewals WHERE setup_id=$1`, expired.ID).Scan(&renews, &renewAdmin, &renewExpiry); err != nil || renews != 1 || renewAdmin != 1 || renewExpiry != renewed.ExpiresAt {
		t.Fatalf("renewal audit lost: %d %d %d %v", renews, renewAdmin, renewExpiry, err)
	}
	// The namespace stays reserved throughout: no unsafe reset onto a fresh setup.
	clash := setupRenewalFixture("b", 20011, now+1800)
	clash.ID = "a" + strings.Repeat("c", 24)
	if err := s.CreateApplicationSetup(ctx, clash); err == nil {
		t.Fatal("renewed reservation released its address")
	}
	// A fresh window needs no renewal.
	if _, err := s.RenewApplicationSetup(ctx, renewed, 1); err == nil || err.Error() != "setup has not expired" {
		t.Fatalf("unexpired renewal admitted: %v", err)
	}
	// A superseded read cannot renew over the fresh window.
	if _, err := s.RenewApplicationSetup(ctx, expired, 1); err == nil || err.Error() != "setup changed" {
		t.Fatalf("stale renewal admitted: %v", err)
	}

	pastGrace := setupRenewalFixture("d", 20012, now-SetupRenewGraceSeconds-10)
	if err := s.CreateApplicationSetup(ctx, pastGrace); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenewApplicationSetup(ctx, pastGrace, 1); err == nil || err.Error() != "setup renewal is closed" {
		t.Fatalf("past-grace renewal admitted: %v", err)
	}

	// The renewal cap is finite: three prior audits close any further re-arm.
	capped := setupRenewalFixture("e", 20013, now-20)
	if err := s.CreateApplicationSetup(ctx, capped); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO application_setup_renewals(setup_id,renewed_at,expires_at,administrator_id) VALUES($1,$2,$3,1),($1,$4,$5,1),($1,$6,$7,1)`, capped.ID, now-50, now-40, now-30, now-25, now-15, now-5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenewApplicationSetup(ctx, capped, 1); err == nil || err.Error() != "setup renewal is closed" {
		t.Fatalf("uncapped renewal admitted: %v", err)
	}

	// Settled setups never renew.
	settled := setupRenewalFixture("f", 20014, now-10)
	if err := s.CreateApplicationSetup(ctx, settled); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleApplicationSetup(ctx, settled, application.SetupResult{Operation: settled.ID}, true); err != nil {
		t.Fatal(err)
	}
	aborted, err := s.ApplicationSetup(ctx, settled.ID)
	if err != nil || aborted.State != "aborted" {
		t.Fatalf("abort not recorded: %+v %v", aborted, err)
	}
	if _, err := s.RenewApplicationSetup(ctx, aborted, 1); err == nil || err.Error() != "setup is already settled" {
		t.Fatalf("settled renewal admitted: %v", err)
	}
	if _, err := s.RenewApplicationSetup(ctx, expired, 0); err == nil || err.Error() != "setup renewal needs its operator" {
		t.Fatalf("anonymous renewal admitted: %v", err)
	}
}

func TestApplicationEndpointAndDataIsolation(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	r := applicationFixture()
	if err := s.CreateApplication(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"hostname", "port", "data"} {
		next := r
		next.Application.ID = "a" + strings.Repeat("a", 24)
		next.Application.Data = "a" + strings.Repeat("b", 24)
		next.Application.Port = 20002
		next.Application.Origin = "https://other.example.test"
		switch field {
		case "hostname":
			next.Application.Origin = "https://app.example.test:8443"
		case "port":
			next.Application.Port = r.Application.Port
		case "data":
			next.Application.Data = r.Application.Data
		}
		if err := s.CreateApplication(ctx, next); err == nil {
			t.Fatalf("shared %s admitted", field)
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM applications`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("partial failed creation retained %d %v", n, err)
	}
}
