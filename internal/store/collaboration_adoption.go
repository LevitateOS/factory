package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/levitateos/sodaos/internal/collaboration"
)

// AdoptFountainLedger is an explicit, pre-admission composition step. The caller
// supplies the already verified source locator from the retained appliance's
// configuration, not an endpoint supplied by a browser or inferred from rows.
// Nothing here discovers a provider, grants authority or rewrites workflow data.
func (s *Store) AdoptFountainLedger(ctx context.Context, host collaboration.HostID, sourceAPIBase string) error {
	base, err := collaboration.CanonicalAPIBase(sourceAPIBase)
	if err != nil {
		return err
	}
	if !collaboration.ValidID(string(host)) {
		return collaboration.ErrIdentityConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := adoptFountainLedger(ctx, tx, host, base); err != nil {
		return err
	}
	return tx.Commit()
}

// BindConfiguredFountainLedger composes the appliance Factory from its trusted
// configuration. The configured public instance locator selects the initial
// host, never a browser URL or inferred ledger label. Once bound, a changed
// locator must already have been recorded against that same stable host; this
// call cannot silently select another source. Registration and adoption commit
// together, so a refused composition leaves no new host or allocator changes.
func (s *Store) BindConfiguredFountainLedger(ctx context.Context, sourceAPIBase string) (collaboration.Host, error) {
	var host collaboration.Host
	base, err := collaboration.CanonicalAPIBase(sourceAPIBase)
	if err != nil {
		return host, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return host, err
	}
	defer tx.Rollback()
	var bound sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT fountain_host FROM collaboration_ledger WHERE singleton FOR UPDATE`).Scan(&bound); err != nil {
		return host, err
	}
	if bound.Valid {
		err = tx.QueryRowContext(ctx, `SELECT id,provider,api_base FROM collaboration_hosts WHERE id=$1 FOR SHARE`, bound.String).Scan(&host.ID, &host.Provider, &host.APIBase)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT id,provider,api_base FROM collaboration_hosts WHERE provider='fountain' AND api_base=$1 FOR SHARE`, base).Scan(&host.ID, &host.Provider, &host.APIBase)
		if err == sql.ErrNoRows {
			host = collaboration.Host{ID: collaboration.HostID(collaboration.NewID()), Provider: collaboration.Fountain, APIBase: base}
			_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_hosts(id,provider,api_base) VALUES($1,$2,$3)`, host.ID, host.Provider, host.APIBase)
		}
	}
	if err != nil {
		return collaboration.Host{}, registryWriteError(err)
	}
	if err := adoptFountainLedger(ctx, tx, host.ID, base); err != nil {
		return collaboration.Host{}, err
	}
	if err := tx.Commit(); err != nil {
		return collaboration.Host{}, err
	}
	return host, nil
}

func adoptFountainLedger(ctx context.Context, tx *sql.Tx, host collaboration.HostID, base string) error {
	var adopted bool
	var bound sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT adopted,fountain_host FROM collaboration_ledger WHERE singleton FOR UPDATE`).Scan(&adopted, &bound); err != nil {
		return err
	}
	var provider, registeredBase string
	if err := tx.QueryRowContext(ctx, `SELECT provider,api_base FROM collaboration_hosts WHERE id=$1 FOR SHARE`, host).Scan(&provider, &registeredBase); err != nil {
		return registryWriteError(err)
	}
	if provider != string(collaboration.Fountain) || registeredBase != base {
		return fmt.Errorf("%w: configured Fountain source differs", collaboration.ErrIdentityConflict)
	}
	if bound.Valid {
		if bound.String != string(host) || !adopted {
			return collaboration.ErrIdentityConflict
		}
		// Do not rescan a ledger that may now contain other providers' local IDs.
		return nil
	}
	var mapped bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM collaboration_repositories) OR EXISTS(SELECT 1 FROM collaboration_actors) OR EXISTS(SELECT 1 FROM collaboration_teams)`).Scan(&mapped); err != nil {
		return err
	}
	if mapped {
		return fmt.Errorf("%w: numeric mappings already exist without a retained source binding", collaboration.ErrIdentityConflict)
	}

	columns, err := retainedCollaborationColumns(ctx, tx)
	if err != nil {
		return err
	}
	if err := lockRetainedCollaborationTables(ctx, tx, columns); err != nil {
		return err
	}
	refs, err := readRetainedCollaborationRefs(ctx, tx, columns)
	if err != nil {
		return err
	}
	for _, ids := range []map[int64]string{refs.repositories, refs.actors} {
		if _, ok := ids[math.MaxInt64]; ok {
			return fmt.Errorf("%w: retained numeric identity range is exhausted", collaboration.ErrIdentityConflict)
		}
	}
	// The gate changes only inside this transaction. Other observers cannot
	// allocate until import and transactional sequence restarts have committed.
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_ledger SET adopted=true,fountain_host=$1 WHERE singleton`, host); err != nil {
		return registryWriteError(err)
	}
	for _, group := range []struct {
		table, label string
		ids          map[int64]string
	}{
		{"collaboration_repositories", "name", refs.repositories}, {"collaboration_actors", "login", refs.actors},
	} {
		ids := make([]int64, 0, len(group.ids))
		for id := range group.ids {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var maximum int64
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+group.table+`(id,host,external_id,`+group.label+`) VALUES($1,$2,$3,$4)`, id, host, strconv.FormatInt(id, 10), group.ids[id]); err != nil {
				return registryWriteError(err)
			}
			maximum = id
		}
		if err := restartCollaborationSequence(ctx, tx, group.table, maximum+1); err != nil {
			return err
		}
	}
	return nil
}

func restartCollaborationSequence(ctx context.Context, tx *sql.Tx, table string, next int64) error {
	var schema, name string
	if err := tx.QueryRowContext(ctx, `SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=pg_get_serial_sequence($1,'id')::regclass`, "public."+table).Scan(&schema, &name); err != nil {
		return err
	}
	// RESTART is transactional; setval is not. A refused/failed adoption must
	// roll the allocator back together with the binding and imported identities.
	_, err := tx.ExecContext(ctx, `ALTER SEQUENCE `+quoteCollaborationIdentifier(schema)+`.`+quoteCollaborationIdentifier(name)+` RESTART WITH `+strconv.FormatInt(next, 10))
	return err
}

// Observe operations acquire the gate before INSERT evaluates identity defaults,
// keeping their lock order consistent with adoption's sequence restart.
func (s *Store) observeCollaborationIdentity(ctx context.Context, query string, args []any, dest ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var adopted bool
	if err := tx.QueryRowContext(ctx, `SELECT adopted FROM collaboration_ledger WHERE singleton FOR SHARE`).Scan(&adopted); err != nil {
		return err
	}
	if !adopted {
		return collaboration.ErrUnboundLedger
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(dest...); err != nil {
		return registryWriteError(err)
	}
	return tx.Commit()
}
