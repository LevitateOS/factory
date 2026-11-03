package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// Count both prelaunch charges and retained roots through the original failed
// verification's stable intent. New cause, delivery, revision and Retry IDs
// cannot refill the two-run ceiling; a failed launch remains spent.
func checkDeliveryRepairRunsTx(ctx context.Context, tx *sql.Tx, record DeliveryRepairRecord, run string) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT spent.run) FROM (
 SELECT c.id AS run FROM factory_commands c JOIN factory_commands j ON j.id=c.payload::jsonb->>'episode_id' AND j.type=$3
 JOIN factory_assignments f ON f.id=j.outcome::jsonb->'cause'->>'failed_assignment_id' AND f.run=j.outcome::jsonb->'cause'->>'failed_verification_run_id'
 WHERE c.type=$4 AND c.target=$5 AND c.finished<>'' AND c.payload::jsonb->>'kind'='run' AND f.repository=$1 AND f.data->'subject'->'delivery'->>'intent_id'=$2
 UNION SELECT a.run FROM factory_assignments a JOIN factory_assignments f ON f.run=a.data->'subject'->'delivery_repair'->>'failed_verification_run_id'
 WHERE a.repository=$1 AND a.data->'subject'->>'kind'='delivery_repair' AND f.repository=$1 AND f.data->'subject'->'delivery'->>'intent_id'=$2
 ) spent WHERE spent.run<>$6`, record.Cause.Repository, record.Cause.Delivery.IntentID, factory.CommandDeliveryRepair, factory.CommandRepairCharge, repairChargeTarget(record.Cause.Repository), run).Scan(&count)
	if err != nil {
		return err
	}
	if count >= factory.DefaultDeliveryRepairRuns {
		return factory.ErrRepairRunsExhausted
	}
	return nil
}

func intentExecutionAllowances(ctx context.Context, q grantCommandSQL, repository int64, intent string) ([]factory.AttemptAllowance, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT l.root_assignment,l.data FROM factory_attempt_allowances l JOIN factory_assignments a ON a.id=l.root_assignment
 LEFT JOIN factory_assignments f ON f.run=a.data->'subject'->'delivery_repair'->>'failed_verification_run_id'
 WHERE l.repository=$1 AND (
 a.data->'subject'->'intent'->>'intent_id'=$2 OR a.data->'subject'->'delivery'->>'intent_id'=$2 OR f.data->'subject'->'delivery'->>'intent_id'=$2
 OR EXISTS(SELECT 1 FROM factory_intent_tasks b JOIN factory_intent_revisions r ON r.id=b.revision WHERE r.intent=$2 AND (r.data->>'repository')::bigint=$1 AND (
 b.data->>'issue_index'=l.issue::text OR EXISTS(SELECT 1 FROM factory_completions e WHERE e.repository=$1 AND e.event=a.data->'subject'->'completion'->>'completion_id' AND e.data->>'issue'=b.data->>'issue_index')))) ORDER BY l.root_assignment`, repository, intent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var allowances []factory.AttemptAllowance
	for rows.Next() {
		var root string
		var raw []byte
		var allowance factory.AttemptAllowance
		if err = rows.Scan(&root, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &allowance) != nil || allowance.Validate() != nil || allowance.RootAssignment != root {
			return nil, errors.New("invalid retained origin intent allowance")
		}
		allowances = append(allowances, allowance)
	}
	return allowances, rows.Err()
}

func checkDeliveryRepairIntentTimeTx(ctx context.Context, tx *sql.Tx, record DeliveryRepairRecord, run string, seconds int64, now time.Time) error {
	return deliveryRepairIntentTimeTx(ctx, tx, record, run, seconds, now, false)
}

func deliveryRepairIntentTimeTx(ctx context.Context, tx *sql.Tx, record DeliveryRepairRecord, run string, seconds int64, now time.Time, defaultIfMissing bool) error {
	remaining, err := deliveryRepairIntentRemainingTx(ctx, tx, record, run, now, defaultIfMissing)
	if err != nil {
		return err
	}
	if remaining < seconds {
		return factory.ErrIntentTimeExhausted
	}
	return nil
}

func deliveryRepairIntentRemainingTx(ctx context.Context, tx *sql.Tx, record DeliveryRepairRecord, run string, now time.Time, defaultIfMissing bool) (int64, error) {
	var owner int64
	var head string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id,revision FROM factory_intent_heads WHERE id=$1 FOR UPDATE`, record.Cause.Delivery.IntentID).Scan(&owner, &head); err != nil {
		return 0, err
	}
	if head != record.Cause.Delivery.AcceptedRevision {
		return 0, ErrStaleRevision
	}
	heads, err := finishedCommands(ctx, tx, factory.CommandIntentExecutionLimit, factory.IntentExecutionTarget(record.Cause.Delivery.IntentID))
	if err != nil {
		return 0, err
	}
	command, err := headExecutionLimit(heads)
	if err != nil && !(defaultIfMissing && errors.Is(err, ErrNotFound)) {
		return 0, err
	}
	var limit factory.IntentExecutionAllowance
	if errors.Is(err, ErrNotFound) {
		// Read-only eligibility anticipates only the canonical default which
		// the explicit owner action records. Charge admission never infers it.
		limit = factory.DefaultIntentExecutionAllowance()
		limit.IntentID, limit.OwnerID, limit.RepositoryID = record.Cause.Delivery.IntentID, owner, record.Cause.Repository
		limit.Active, limit.CheckpointUnix = true, now.Unix()
	} else if json.Unmarshal([]byte(command.Payload), &limit) != nil {
		return 0, errors.New("invalid delivery repair origin intent allowance")
	}
	if limit.Validate() != nil || limit.OwnerID != owner || limit.RepositoryID != record.Cause.Repository || limit.IntentID != record.Cause.Delivery.IntentID {
		return 0, errors.New("delivery repair origin intent allowance is unavailable")
	}
	if !limit.Active {
		return 0, factory.ErrIntentTimeExhausted
	}
	allowances, err := intentExecutionAllowances(ctx, tx, record.Cause.Repository, record.Cause.Delivery.IntentID)
	if err != nil {
		return 0, err
	}
	remaining := limit.ActiveLimitMs / 1000
	for _, allowance := range allowances {
		if allowance.RootAssignment == record.Cycle.ID {
			continue // The exact packet is funded below, never charged twice.
		}
		if now.Unix() < allowance.CheckpointUnix {
			return 0, factory.ErrAllowanceClock
		}
		if allowance.Active {
			elapsed := now.Unix() - allowance.CheckpointUnix
			if elapsed > remaining {
				return 0, factory.ErrIntentTimeExhausted
			}
			remaining -= elapsed
		}
		if allowance.ActiveSeconds > remaining {
			return 0, factory.ErrIntentTimeExhausted
		}
		remaining -= allowance.ActiveSeconds
	}
	// Prelaunch repair charges reserve their finite window until an allowance
	// exists. Retired allowance rows above then supply actual consumed time.
	var reserved int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum((c.payload::jsonb->>'active_minutes')::bigint*60),0)
 FROM factory_commands c JOIN factory_commands j ON j.id=c.payload::jsonb->>'episode_id' AND j.type=$3
 JOIN factory_assignments f ON f.id=j.outcome::jsonb->'cause'->>'failed_assignment_id'
 WHERE c.type=$4 AND c.target=$5 AND c.finished<>'' AND c.payload::jsonb->>'kind'='run' AND c.id<>$6
 AND f.data->'subject'->'delivery'->>'intent_id'=$2 AND f.repository=$1
 AND NOT EXISTS(SELECT 1 FROM factory_assignments a JOIN factory_attempt_allowances l ON l.root_assignment=a.id WHERE a.run=c.id)`, record.Cause.Repository, record.Cause.Delivery.IntentID, factory.CommandDeliveryRepair, factory.CommandRepairCharge, repairChargeTarget(record.Cause.Repository), run).Scan(&reserved); err != nil {
		return 0, err
	}
	return max(int64(0), remaining-reserved), nil
}
