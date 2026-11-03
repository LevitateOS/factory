package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Extend only the two existing issue-scope checks. Retained work stays in place;
// each existing alternative is preserved verbatim, and subsequent opens do no DDL.
func initializeDeliverySchema(ctx context.Context, db *sql.DB) error {
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
			return errors.New("delivery scope constraint is unavailable")
		}
		if strings.Contains(definition, "delivery_id") {
			continue
		}
		if !strings.HasPrefix(definition, "CHECK (") || !strings.HasSuffix(definition, ")") {
			return errors.New("unexpected delivery scope constraint")
		}
		branch := `issue=0 AND COALESCE(data->'delivery'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'intent','null'::JSONB)='null'::JSONB AND COALESCE(data->'completion','null'::JSONB)='null'::JSONB AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'root_assignment','')=root_assignment`
		if table == "factory_assignments" {
			branch = `issue=0 AND COALESCE(data->'subject'->>'kind','')='delivery' AND COALESCE(data->'subject'->'delivery'->>'delivery_id','')~'^[a-f0-9]{32}$' AND COALESCE(data->'subject'->'delivery'->>'intent_id','')~'^d[0-9a-f]{24}$' AND COALESCE(data->'subject'->'delivery'->>'accepted_revision','')~'^d[0-9a-f]{24}$' AND COALESCE(data->>'phase','')='verify' AND COALESCE(data->>'role','')='soda-reviewer' AND COALESCE(data->>'parent_assignment','')='' AND COALESCE(data->>'publication_assignment','')='' AND COALESCE(data->>'id','')=id AND COALESCE(data->>'attempt_root','')=id AND COALESCE(data->>'repository','')=repository::TEXT AND COALESCE(data->>'issue','')='0' AND COALESCE(data->>'acceptance','')=''`
		}
		quote := func(id string) string { return `"` + strings.ReplaceAll(id, `"`, `""`) + `"` }
		next := definition[:len(definition)-1] + " OR (" + branch + "))"
		if _, err = tx.ExecContext(ctx, `ALTER TABLE `+quote(table)+` DROP CONSTRAINT `+quote(name)+`, ADD CONSTRAINT `+quote(table+"_issue_scope_check")+` `+next); err != nil {
			return err
		}
	}
	return tx.Commit()
}
