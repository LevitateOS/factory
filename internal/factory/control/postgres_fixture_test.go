package control

import (
	"context"
	"testing"

	"github.com/levitateos/sodaos/internal/store"
)

// postgresFixture opens an isolated ephemeral PostgreSQL database for one
// test. key selects the encrypted entrypoint; nil opens without identity
// credentials. It returns the store and its connection URL, so close/reopen
// tests can reconnect to the same database. Tests skip when the A10
// disposable fixture environment is absent.
func postgresFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	admin, ok := store.TestFixtureDSN()
	if !ok {
		t.Skip("SODA_PG_* fixture unavailable")
	}
	s, dsn, cleanup, err := store.OpenEphemeral(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return s, dsn
}
