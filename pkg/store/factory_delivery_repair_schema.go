package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const deliveryRepairAssignmentScope = `issue=0 AND COALESCE(data->'subject'->>'kind','')='delivery_repair' AND COALESCE(data->'subject'->'delivery_repair'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'delivery_repair'->>'failed_verification_run_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'delivery_repair'->>'failure_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'subject'->'delivery_repair'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'subject'->'delivery_repair'->>'cycle_id','')=id AND COALESCE(data->>'phase','')='repair' AND COALESCE(data->>'role','')='soda-coder' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')=''`

const deliveryRepairAllowanceScope = `issue=0 AND COALESCE(data->'delivery_repair'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'delivery_repair'->>'failed_verification_run_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'delivery_repair'->>'failure_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'delivery_repair'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'delivery_repair'->>'cycle_id','')=root_assignment AND COALESCE(data->'intent','null'::JSONB)='null'::JSONB AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->'delivery','null'::JSONB)='null'::JSONB AND COALESCE(data->'stall','null'::JSONB)='null'::JSONB AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment`

// Frozen predecessor: historical admission must not follow future fresh-schema edits.
const deliveryRepairPriorAssignmentCheck = `issue>0 OR (issue=0 AND COALESCE(data->'subject'->>'kind','')='completion' AND COALESCE(data->'subject'->'completion'->>'completion_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'completion'->>'cycle_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'completion'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->>'phase','')='reassess' AND COALESCE(data->>'role','')='soda-coder' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')~'^[a-f0-9]{32}$' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')='') OR (issue=0 AND COALESCE(data->'subject'->>'kind','')='intent' AND COALESCE(data->'subject'->'intent'->>'intent_id','')~'^d[0-9a-f]{24}$' AND COALESCE(data->'subject'->'intent'->>'owner_id','')~'^[1-9][0-9]*$' AND COALESCE(data->>'phase','')='decompose' AND COALESCE(data->>'role','')='soda-coder' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')~'^[a-f0-9]{32}$' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')='') OR (issue=0 AND COALESCE(data->'subject'->>'kind','')='delivery' AND COALESCE(data->'subject'->'delivery'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'delivery'->>'intent_id','')~'^d[0-9a-f]{24}$' AND COALESCE(data->'subject'->'delivery'->>'accepted_revision','')~'^d[0-9a-f]{24}$' AND COALESCE(data->>'phase','')='verify' AND COALESCE(data->>'role','')='soda-reviewer' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')='') OR (issue=0 AND COALESCE(data->'subject'->>'kind','')='stall' AND COALESCE(data->'subject'->'stall'->>'stall_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'stall'->>'cycle_id','')=id AND COALESCE(data->'subject'->'stall'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->>'phase','')='repair' AND COALESCE(data->>'role','')='soda-coder' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')='')`

const deliveryRepairAssignmentCheck = deliveryRepairPriorAssignmentCheck + ` OR (` + deliveryRepairAssignmentScope + `)`

// Frozen predecessor: historical admission must not follow future fresh-schema edits.
const deliveryRepairPriorAllowanceCheck = `(issue>0 AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->'intent','null'::JSONB)='null'::JSONB) OR (issue=0 AND COALESCE(data->'completion'->>'completion_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'completion'->>'cycle_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'completion'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment) OR (issue=0 AND COALESCE(data->'intent'->>'intent_id','')~'^d[0-9a-f]{24}$' AND COALESCE(data->'intent'->>'owner_id','')~'^[1-9][0-9]*$' AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment) OR (issue=0 AND COALESCE(data->'delivery'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'intent','null'::JSONB)='null'::JSONB AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment) OR (issue=0 AND COALESCE(data->'stall'->>'stall_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'stall'->>'cycle_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'stall'->>'inventory_digest','')~'^[a-f0-9]{64}$' AND COALESCE(data->'intent','null'::JSONB)='null'::JSONB AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->'delivery','null'::JSONB)='null'::JSONB AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment)`

const deliveryRepairAllowanceCheck = deliveryRepairPriorAllowanceCheck + ` OR (` + deliveryRepairAllowanceScope + `)`

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
	for _, scope := range []struct{ table, prior, current string }{
		{"factory_assignments", deliveryRepairPriorAssignmentCheck, deliveryRepairAssignmentCheck},
		{"factory_attempt_allowances", deliveryRepairPriorAllowanceCheck, deliveryRepairAllowanceCheck},
	} {
		table := scope.table
		previous, err := canonicalDeliveryRepairCheck(ctx, tx, scope.prior)
		if err != nil {
			return err
		}
		next, err := canonicalDeliveryRepairCheck(ctx, tx, scope.current)
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
