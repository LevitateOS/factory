package store

import (
	"context"
	"encoding/json"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

// NativeFactoryRun resolves a broker selector through retained canonical data.
// It reads settled history too: a leftover native root is not necessarily an
// unsettled product run. No match, invalid data and ambiguous selectors supply
// no recovery authority. This read neither admits launch nor settles a run.
func (s *Store) NativeFactoryRun(ctx context.Context, profile collaboration.ProfileID, brokerRun string) (factory.Run, error) {
	if !collaboration.ValidID(string(profile)) || !collaboration.ValidID(brokerRun) {
		return factory.Run{}, ErrAdmissionChanged
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,data FROM factory_runs
		WHERE data#>>'{native,provenance,profile}'=$1
		AND data#>>'{native,provenance,broker_run}'=$2 LIMIT 2`, string(profile), brokerRun)
	if err != nil {
		return factory.Run{}, err
	}
	defer func() { _ = rows.Close() }()
	var found *factory.Run
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return factory.Run{}, err
		}
		var run factory.Run
		if found != nil || json.Unmarshal(data, &run) != nil || run.Validate() != nil || run.ID != id ||
			run.Native == nil || (run.Native.Provenance.TrustMode != "isolated_system_worker" && run.Native.Provenance.TrustMode != execution.NativeCLIProvider) ||
			run.Native.Provenance.Profile != profile || run.Native.Provenance.BrokerRun != brokerRun {
			return factory.Run{}, ErrAdmissionChanged
		}
		found = &run
	}
	if err := rows.Err(); err != nil {
		return factory.Run{}, err
	}
	if found == nil {
		return factory.Run{}, ErrNotFound
	}
	return *found, nil
}
