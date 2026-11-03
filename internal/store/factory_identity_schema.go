package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Only collaboration keys are widened. Native Project/user ownership, revisions,
// counters and ports retain their own schemas; widening never maps an identity.
var factoryIdentityColumns = []struct {
	table   string
	columns []string
}{
	{"factory_policies", []string{"repository"}},
	{"factory_operator_grants", []string{"repository"}},
	{"factory_sponsorships", []string{"repository"}},
	{"factory_dispatch", []string{"repository"}},
	{"factory_run_views", []string{"repository", "issue"}},
	{"factory_assignments", []string{"repository", "issue"}},
	{"factory_attempt_allowances", []string{"repository", "issue"}},
	{"factory_reservations", []string{"repository"}},
	{"factory_usage", []string{"repository"}},
	{"issue_acceptance_decisions", []string{"repository", "issue"}},
	{"issue_acceptance_heads", []string{"repository", "issue"}},
	{"issue_acceptance_withdrawals", []string{"repository", "issue", "withdrawer"}},
	{"issue_controls", []string{"repository", "issue"}},
	{"intake_deliveries", []string{"repository", "issue"}},
	{"factory_publications", []string{"repository", "issue"}},
	{"factory_check_assessments", []string{"repository", "pr"}},
	{"factory_merges", []string{"repository", "issue", "pr"}},
	{"project_environment_grants", []string{"repository"}},
}

// Retained profiles already have these columns as INTEGER. PostgreSQL widens
// them transactionally, retaining values, indexes, checks and references. The
// shared schema-row lock serializes this with other store initializers.
func initializeFactoryIdentitySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	for _, group := range factoryIdentityColumns {
		var changes []string
		for _, column := range group.columns {
			var kind string
			if err := tx.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, group.table, column).Scan(&kind); err != nil {
				return fmt.Errorf("Factory identity column %s.%s: %w", group.table, column, err)
			}
			switch kind {
			case "bigint":
			case "integer":
				changes = append(changes, `ALTER COLUMN `+quoteCollaborationIdentifier(column)+` TYPE BIGINT`)
			default:
				return fmt.Errorf("Factory identity column %s.%s has unsupported type %s", group.table, column, kind)
			}
		}
		if len(changes) != 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE public.`+quoteCollaborationIdentifier(group.table)+` `+strings.Join(changes, ", ")); err != nil {
				return fmt.Errorf("widen Factory identities in %s: %w", group.table, err)
			}
		}
	}
	return tx.Commit()
}
