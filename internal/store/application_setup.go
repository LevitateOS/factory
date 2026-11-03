package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/levitateos/sodaos/internal/application"
)

const ApplicationSetupsDDL = `CREATE TABLE application_setups(id TEXT PRIMARY KEY,application_id TEXT NOT NULL UNIQUE,administrator_id BIGINT NOT NULL CHECK(administrator_id>0),data_id TEXT NOT NULL UNIQUE,origin TEXT NOT NULL UNIQUE,hostname TEXT NOT NULL UNIQUE,port INTEGER NOT NULL UNIQUE,state TEXT NOT NULL CHECK(state IN ('pending','complete','aborted')),data JSONB NOT NULL CHECK(octet_length(data::text)<=8192))`

// ApplicationSetupRenewalsDDL audits every expired-setup renewal. Each row
// re-arms the retained reservation once; the row count finitely bounds
// renewals per setup without touching the canonical setup record.
const ApplicationSetupRenewalsDDL = `CREATE TABLE application_setup_renewals(setup_id TEXT NOT NULL REFERENCES application_setups(id),renewed_at BIGINT NOT NULL CHECK(renewed_at>0),expires_at BIGINT NOT NULL CHECK(expires_at>renewed_at),administrator_id BIGINT NOT NULL CHECK(administrator_id>0),PRIMARY KEY(setup_id,renewed_at))`

const (
	// SetupRenewTTLSeconds re-arms an expired reservation for one fresh
	// creation window, the same length the initial admission grants.
	SetupRenewTTLSeconds = 30 * 60
	// SetupRenewGraceSeconds bounds renewal after expiry. Past this window
	// the reservation stays retained but unrecoverable by design: no
	// supported path resets it onto a fresh setup.
	SetupRenewGraceSeconds = 24 * 60 * 60
	// MaxSetupRenews finitely bounds operator re-arms per setup.
	MaxSetupRenews = 3
)

// Both normal registration and provisional setup serialize their namespace
// reservation so a foreign provisional address cannot be claimed in between.
func lockApplicationAdmissions(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1936684132,1)`)
	return err
}

func reserveSetupNamespace(ctx context.Context, tx *sql.Tx, s application.SetupSpec) error {
	var conflict bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM applications WHERE id=$1 OR data_id=$2 OR origin=$3 OR hostname=$4 OR port=$5) OR EXISTS(SELECT 1 FROM application_setups WHERE application_id=$1 OR data_id=$2 OR origin=$3 OR hostname=$4 OR port=$5)`, s.ID, s.Data, s.Origin, mustHostname(s.Origin), s.Port).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return errors.New("application namespace is already reserved")
	}
	return nil
}
func mustHostname(origin string) string { u, _ := url.Parse(origin); return u.Hostname() }
func (s *Store) CreateApplicationSetup(ctx context.Context, record application.SetupRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.State != "pending" || record.Observation != nil {
		return errors.New("new setup must be pending and unobserved")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockApplicationAdmissions(ctx, tx); err != nil {
		return err
	}
	if err = reserveSetupNamespace(ctx, tx, record.Application); err != nil {
		return err
	}
	if err = insertApplicationSetupTx(ctx, tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

func insertApplicationSetupTx(ctx context.Context, tx *sql.Tx, record application.SetupRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	a := record.Application
	_, err = tx.ExecContext(ctx, `INSERT INTO application_setups(id,application_id,administrator_id,data_id,origin,hostname,port,state,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, record.ID, a.ID, record.AdministratorID, a.Data, a.Origin, mustHostname(a.Origin), a.Port, record.State, string(raw))
	if err != nil {
		return err
	}
	return nil
}

func scanApplicationSetup(row interface{ Scan(...any) error }) (application.SetupRecord, error) {
	var r application.SetupRecord
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

func (s *Store) ApplicationSetup(ctx context.Context, id string) (application.SetupRecord, error) {
	return scanApplicationSetup(s.db.QueryRowContext(ctx, `SELECT data FROM application_setups WHERE id=$1`, id))
}

func (s *Store) ApplicationSetups(ctx context.Context) ([]application.SetupRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM application_setups WHERE state='pending' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []application.SetupRecord{}
	for rows.Next() {
		r, err := scanApplicationSetup(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) ObserveApplicationSetup(ctx context.Context, before application.SetupRecord, next *application.SetupObservation) error {
	if next != nil {
		if err := next.Validate(); err != nil {
			return err
		}
	}
	after := before
	after.Observation = next
	return s.updateApplicationSetup(ctx, before, after)
}

func (s *Store) updateApplicationSetup(ctx context.Context, before, after application.SetupRecord) error {
	old, _ := json.Marshal(before)
	raw, _ := json.Marshal(after)
	result, err := s.db.ExecContext(ctx, `UPDATE application_setups SET data=$1,state=$2 WHERE id=$3 AND data=$4::jsonb`, string(raw), after.State, before.ID, string(old))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("setup changed")
	}
	return nil
}

// SetupRenewClaim is the read-only committed-claim reconciliation outcome
// for a setup renewal. It names only durable owner-claim states the store
// can prove; it never mints, extends, or releases anything.
type SetupRenewClaim string

const (
	// SetupRenewClaimUnclaimed is the Soda-side unproven outcome: no
	// committed owner binding is visible in Soda's database for this
	// application context and no other durable holder claims its
	// namespace there. That proves nothing about the native owner state,
	// which lives outside Soda's database — interim fail-closed, this
	// outcome authorizes no rotation or replacement. Callers must refuse
	// rotation on it exactly as on committed/conflict, until the queued
	// native reconciliation seam can prove the durable claim.
	SetupRenewClaimUnclaimed SetupRenewClaim = "unclaimed"
	// SetupRenewClaimCommitted means a committed owner binding already
	// exists for this exact application context. Expiry never uncommits
	// it: the setup reservation expiry is handled by the receipt path
	// (complete with a validated Canlang receipt), never by re-admission.
	SetupRenewClaimCommitted SetupRenewClaim = "committed"
	// SetupRenewClaimConflict means a different durable holder claims the
	// same namespace (data, origin, hostname, or port).
	SetupRenewClaimConflict SetupRenewClaim = "conflict"
)

// InspectSetupRenewClaim reconciles the durable owner claim for the exact
// setup BEFORE any token rotation or replacement. The check binds the same
// generation the renew acts on (setup identity and observed generation),
// the same owner (administrator), and the same application context
// (application references and namespace). It is read-only: interim
// fail-closed, the caller must refuse rotation and perform no
// renewal-audit mutation on EVERY outcome — committed, conflicting,
// unclaimed (Soda-side unproven), and uncertain alike — until the queued
// native reconciliation seam exists. A read failure is uncertain and
// returns an error; the caller must refuse without mutating, never guess.
// The same-operation holder generation is not a competing claim here: it
// is pinned by the caller's expected-container check and the host's own
// credential guards, which stay authoritative for host-side uncertainty.
func (s *Store) InspectSetupRenewClaim(ctx context.Context, setup application.SetupRecord) (SetupRenewClaim, error) {
	if err := setup.Validate(); err != nil {
		return "", err
	}
	if _, err := s.OperatorApplication(ctx, setup.Application.ID); err == nil {
		return SetupRenewClaimCommitted, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var conflict bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM applications WHERE id<>$1 AND (data_id=$2 OR origin=$3 OR hostname=$4 OR port=$5))`, setup.Application.ID, setup.Application.Data, setup.Application.Origin, mustHostname(setup.Application.Origin), setup.Application.Port).Scan(&conflict); err != nil {
		return "", err
	}
	if conflict {
		return SetupRenewClaimConflict, nil
	}
	return SetupRenewClaimUnclaimed, nil
}

// RenewApplicationSetup re-arms an expired pending reservation on its
// retained address and data. Only the expiry advances, within a bounded
// grace window and a finite renewal count; id, references, administrator
// and state are unchanged, and the namespace reservation is never released.
// administratorID records the renewing operator for audit. The caller
// re-begins the returned record through the ordinary host begin flow.
// Interim fail-closed: no HTTP caller may invoke this until the queued
// native reconciliation seam proves the durable claim (the API refuses
// every rotation-adjacent outcome first). The primitive is retained for
// that seam — and for tests staging its committed-renewal premise below
// the HTTP refusal — not as a currently reachable path.
func (s *Store) RenewApplicationSetup(ctx context.Context, before application.SetupRecord, administratorID int64) (application.SetupRecord, error) {
	if err := before.Validate(); err != nil {
		return application.SetupRecord{}, err
	}
	if before.State != "pending" {
		return application.SetupRecord{}, errors.New("setup is already settled")
	}
	if administratorID <= 0 {
		return application.SetupRecord{}, errors.New("setup renewal needs its operator")
	}
	now := time.Now().Unix()
	if now < before.ExpiresAt {
		return application.SetupRecord{}, errors.New("setup has not expired")
	}
	if now > before.ExpiresAt+SetupRenewGraceSeconds {
		return application.SetupRecord{}, errors.New("setup renewal is closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return application.SetupRecord{}, err
	}
	defer tx.Rollback()
	if err = lockApplicationAdmissions(ctx, tx); err != nil {
		return application.SetupRecord{}, err
	}
	current, err := scanApplicationSetup(tx.QueryRowContext(ctx, `SELECT data FROM application_setups WHERE id=$1 FOR UPDATE`, before.ID))
	if err != nil {
		return application.SetupRecord{}, err
	}
	old, _ := json.Marshal(before)
	actual, _ := json.Marshal(current)
	if string(old) != string(actual) {
		return application.SetupRecord{}, errors.New("setup changed")
	}
	var renews int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM application_setup_renewals WHERE setup_id=$1`, before.ID).Scan(&renews); err != nil {
		return application.SetupRecord{}, err
	}
	if renews >= MaxSetupRenews {
		return application.SetupRecord{}, errors.New("setup renewal is closed")
	}
	after := before
	after.ExpiresAt = now + SetupRenewTTLSeconds
	raw, _ := json.Marshal(after)
	if _, err = tx.ExecContext(ctx, `UPDATE application_setups SET data=$1 WHERE id=$2 AND data=$3::jsonb`, string(raw), before.ID, string(old)); err != nil {
		return application.SetupRecord{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO application_setup_renewals(setup_id,renewed_at,expires_at,administrator_id) VALUES($1,$2,$3,$4)`, before.ID, now, after.ExpiresAt, administratorID); err != nil {
		return application.SetupRecord{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.SetupRecord{}, err
	}
	return after, nil
}

func (s *Store) SettleApplicationSetup(ctx context.Context, before application.SetupRecord, result application.SetupResult, abort bool) error {
	if err := before.Validate(); err != nil {
		return err
	}
	if before.State != "pending" || result.Operation != before.ID || result.Observation.Running || result.Observation.Ready {
		return errors.New("setup writer must stop before settlement")
	}
	if err := result.Observation.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockApplicationAdmissions(ctx, tx); err != nil {
		return err
	}
	current, err := scanApplicationSetup(tx.QueryRowContext(ctx, `SELECT data FROM application_setups WHERE id=$1 FOR UPDATE`, before.ID))
	if err != nil {
		return err
	}
	old, _ := json.Marshal(before)
	actual, _ := json.Marshal(current)
	if string(old) != string(actual) {
		return errors.New("setup changed")
	}
	after := before
	after.Observation = &result.Observation
	if abort {
		after.State = "aborted"
	} else {
		if result.Receipt == nil || result.Application == nil {
			return errors.New("confirmed setup receipt required")
		}
		if err = result.Receipt.Validate(before); err != nil {
			return err
		}
		spec := before.Application.Bind(result.Receipt.OwnerTeam)
		if err = result.Application.Validate(spec); err != nil {
			return err
		}
		if result.Application.Running || result.Application.Ready || result.Application.Container == "" {
			return errors.New("normal application must be installed stopped")
		}
		record := application.Record{Application: spec, AdministratorID: before.AdministratorID, Observation: result.Application}
		if err = insertApplicationRecord(ctx, tx, record); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO application_projects(application_id,repository) SELECT $1,repository FROM application_builds WHERE setup_id=$2`, spec.ID, before.ID); err != nil {
			return err
		}
		after.State = "complete"
	}
	raw, _ := json.Marshal(after)
	_, err = tx.ExecContext(ctx, `UPDATE application_setups SET state=$1,data=$2 WHERE id=$3`, after.State, string(raw), after.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
