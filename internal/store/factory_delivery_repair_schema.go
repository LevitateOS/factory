package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const deliveryRepairAssignmentScope = `issue=0 AND COALESCE(data->'subject'->>'kind','')='delivery_repair' AND COALESCE(data->'subject'->'delivery_repair'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'delivery_repair'->>'failed_verification_run_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'delivery_repair'->>'failure_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'subject'->'delivery_repair'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'subject'->'delivery_repair'->>'cycle_id','')=id AND COALESCE(data->>'phase','')='repair' AND COALESCE(data->>'role','')='soda-coder' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')=''`

const deliveryRepairAllowanceScope = `issue=0 AND COALESCE(data->'delivery_repair'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'delivery_repair'->>'failed_verification_run_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'delivery_repair'->>'failure_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'delivery_repair'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'delivery_repair'->>'cycle_id','')=root_assignment AND COALESCE(data->'intent','null'::JSONB)='null'::JSONB AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->'delivery','null'::JSONB)='null'::JSONB AND COALESCE(data->'stall','null'::JSONB)='null'::JSONB AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment`

// Normalize only the previous and new canonical expressions with PostgreSQL's
// own parser. The temporary shape is transaction-local; no retained table or
// row changes until the existing constraint exactly matches the known old form.
func canonicalDeliveryRepairCheck(ctx context.Context, tx *sql.Tx, expression string) (string, error) {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE soda_delivery_repair_shape(id TEXT,repository BIGINT,issue BIGINT,root_assignment TEXT,data JSONB,CHECK(`+expression+`)) ON COMMIT DROP`); err != nil {
		return "", err
	}
	var definition string
	err := tx.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='pg_temp.soda_delivery_repair_shape'::regclass AND contype='c'`).Scan(&definition)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `DROP TABLE pg_temp.soda_delivery_repair_shape`)
	return definition, err
}

func initializeDeliveryRepairSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	for _, table := range []string{"factory_assignments", "factory_attempt_allowances"} {
		branch, end := deliveryRepairAssignmentScope, "), run TEXT NOT NULL"
		if table == "factory_attempt_allowances" {
			branch, end = deliveryRepairAllowanceScope, "), root_assignment TEXT PRIMARY KEY"
		}
		var expression string
		for _, statement := range schemaStatements {
			if strings.HasPrefix(statement, "CREATE TABLE "+table+"(") {
				_, tail, ok := strings.Cut(statement, "issue BIGINT NOT NULL CHECK(")
				if !ok {
					return errors.New("canonical repair scope is unavailable")
				}
				expression, _, ok = strings.Cut(tail, end)
				if !ok {
					return errors.New("canonical repair scope end is unavailable")
				}
			}
		}
		old, ok := strings.CutSuffix(expression, " OR ("+branch+")")
		if !ok {
			return errors.New("canonical delivery repair branch differs")
		}
		previous, err := canonicalDeliveryRepairCheck(ctx, tx, old)
		if err != nil {
			return err
		}
		next, err := canonicalDeliveryRepairCheck(ctx, tx, expression)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT conname,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%completion%' AND pg_get_constraintdef(oid) LIKE '%intent%'`, table)
		if err != nil {
			return err
		}
		var name, definition string
		count := 0
		for rows.Next() {
			count++
			if err = rows.Scan(&name, &definition); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("delivery repair scope constraint is unavailable")
		}
		if definition == next {
			continue
		}
		if definition != previous {
			return errors.New("delivery repair refuses an unknown retained scope constraint")
		}
		quote := func(id string) string { return "\"" + strings.ReplaceAll(id, "\"", "\"\"") + "\"" }
		if _, err = tx.ExecContext(ctx, `ALTER TABLE `+quote(table)+` DROP CONSTRAINT `+quote(name)+`, ADD CONSTRAINT `+quote(table+"_issue_scope_check")+` `+next); err != nil {
			return err
		}
	}
	return tx.Commit()
}
