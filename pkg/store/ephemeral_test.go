package store

import (
	"context"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	dropEphemeralTemplates()
	os.Exit(code)
}

// TestEphemeralTemplateCloneIsolation proves the template fast path hands
// out independent databases: two clones open on the migrated schema, both
// read empty, and dropping one leaves the other usable.
func TestEphemeralTemplateCloneIsolation(t *testing.T) {
	ctx := context.Background()
	admin, ok := TestFixtureDSN()
	if !ok {
		t.Skip("SODA_PG_* fixture unavailable")
	}
	first, _, cleanupFirst, err := OpenEphemeral(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFirst()
	second, _, cleanupSecond, err := OpenEphemeral(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupSecond()
	for name, s := range map[string]*Store{"first": first, "second": second} {
		standing, err := s.StandingStallEvents(ctx)
		if err != nil || len(standing) != 0 {
			t.Fatalf("%s standing=%+v err=%v, want empty", name, standing, err)
		}
	}
	cleanupFirst()
	if standing, err := second.StandingStallEvents(ctx); err != nil || len(standing) != 0 {
		t.Fatalf("second after first drop: standing=%+v err=%v, want empty", standing, err)
	}
}
