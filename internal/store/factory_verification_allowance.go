package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func verificationAllowanceIntentTx(ctx context.Context, tx *sql.Tx, intent string, owner int64, exclusive bool) (factory.IntentRevision, error) {
	var revision factory.IntentRevision
	if !factory.ValidVerificationAllowanceIntent(intent) || owner <= 0 {
		return revision, ErrNotFound
	}
	lock := " FOR SHARE OF h"
	if exclusive {
		lock = " FOR UPDATE OF h"
	}
	var raw []byte
	var actualOwner int64
	err := tx.QueryRowContext(ctx, `SELECT h.owner_id,r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision WHERE h.id=$1`+lock, intent).Scan(&actualOwner, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, ErrNotFound
	}
	if err != nil {
		return revision, err
	}
	if actualOwner != owner {
		return revision, factory.ErrVerificationAllowanceOwner
	}
	if err = json.Unmarshal(raw, &revision); err != nil {
		return revision, err
	}
	if revision.Intent != intent || revision.Owner != owner {
		return revision, factory.ErrVerificationAllowanceOwner
	}
	return revision, nil
}

// The unique predecessor-chain tip names the allowance revision. Wall-clock
// command timestamps are deliberately irrelevant. The stable intent head lock
// serializes both this chain's append and every delivery packet admission.
func verificationAllowanceLimits(ctx context.Context, q grantCommandSQL, intent string) (string, factory.AppVerificationLimits, error) {
	defaults := factory.AppVerificationLimits{Runs: factory.DefaultAppVerificationRuns, ActiveMinutes: factory.DefaultAppVerificationActiveMinutes}
	var id string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT c.id,c.payload FROM factory_commands c
    WHERE c.type=$1 AND c.target=$2 AND c.finished<>''
      AND NOT EXISTS (SELECT 1 FROM factory_commands successor
        WHERE successor.type=c.type AND successor.target=c.target AND successor.finished<>''
          AND successor.payload::jsonb->>'expected_revision'=c.id)
    LIMIT 1`, factory.CommandVerificationAllowance, factory.AppVerificationAllowanceTarget(intent)).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", defaults, nil
	}
	if err != nil {
		return "", defaults, err
	}
	var change factory.AppVerificationAllowanceChange
	if err = json.Unmarshal(raw, &change); err != nil {
		return "", defaults, err
	}
	return id, change.Limits, nil
}

func verificationLimitsIncrease(old, next factory.AppVerificationLimits) bool {
	return next.Runs >= old.Runs && next.ActiveMinutes >= old.ActiveMinutes && next != old
}

func verificationConsumptionTx(ctx context.Context, tx *sql.Tx, intent string, now time.Time) (factory.AppVerificationUsage, error) {
	var usage factory.AppVerificationUsage
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE issue=0 AND data->'delivery'->>'intent_id'=$1 ORDER BY root_assignment FOR SHARE`, intent)
	if err != nil {
		return usage, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var allowance factory.AttemptAllowance
		if err = rows.Scan(&raw); err != nil {
			return usage, err
		}
		if err = json.Unmarshal(raw, &allowance); err != nil {
			return usage, err
		}
		if now.Unix() < allowance.CheckpointUnix {
			return usage, factory.ErrAllowanceClock
		}
		spent := allowance.ActiveSeconds
		if allowance.Active {
			if now.Unix()-allowance.CheckpointUnix > math.MaxInt64-spent {
				return usage, factory.ErrAllowanceClock
			}
			spent += now.Unix() - allowance.CheckpointUnix
		}
		if spent > math.MaxInt64-usage.ActiveSeconds {
			return usage, errors.New("verification consumption overflow")
		}
		usage.Runs++
		usage.ActiveSeconds += spent
	}
	return usage, rows.Err()
}

func verificationAllowanceStatusTx(ctx context.Context, tx *sql.Tx, intent string, now time.Time) (factory.AppVerificationAllowance, error) {
	view := factory.AppVerificationAllowance{Intent: intent}
	var err error
	if view.Revision, view.Limits, err = verificationAllowanceLimits(ctx, tx, intent); err != nil {
		return view, err
	}
	if view.Consumption, err = verificationConsumptionTx(ctx, tx, intent, now); err != nil {
		return view, err
	}
	view.UpdateRemaining()
	return view, nil
}

func (s *Store) AppVerificationAllowance(ctx context.Context, intent string, owner int64, now time.Time) (factory.AppVerificationAllowance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.AppVerificationAllowance{}, err
	}
	defer tx.Rollback()
	head, err := verificationAllowanceIntentTx(ctx, tx, intent, owner, false)
	if err != nil {
		return factory.AppVerificationAllowance{}, err
	}
	view, err := verificationAllowanceStatusTx(ctx, tx, intent, now)
	if err != nil {
		return view, err
	}
	view.Repository = head.Repository
	view.CanIncrease = head.State == "accepted" && head.Repository > 0
	return view, tx.Commit()
}

// Mutation authorization reads the current owner and repository.
func (s *Store) AppVerificationAllowanceScope(ctx context.Context, intent string, owner int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	head, err := verificationAllowanceIntentTx(ctx, tx, intent, owner, false)
	if err != nil {
		return 0, err
	}
	return head.Repository, tx.Commit()
}

// IncreaseAppVerificationAllowance admits one finite limit increase. The grant
// command owns its identity and limits; responses read current limits and usage.
// Owner is canonical; principal is supplied by the authenticated admission
// adapter and retains its original audit meaning on command replay.
// repository is the canonical scope whose current authority was checked. It is
// compared under the head lock before recording or replaying any command.
func (s *Store) IncreaseAppVerificationAllowance(ctx context.Context, intent string, owner, repository int64, principal string, change factory.AppVerificationAllowanceChange, now time.Time) (factory.AppVerificationAllowance, error) {
	var none factory.AppVerificationAllowance
	if err := change.Validate(); err != nil {
		return none, err
	}
	raw, err := json.Marshal(change)
	if err != nil {
		return none, err
	}
	cmd := factory.Command{
		ID: change.CommandID, Type: factory.CommandVerificationAllowance,
		Target: factory.AppVerificationAllowanceTarget(intent), Principal: principal, Payload: string(raw),
	}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	head, err := verificationAllowanceIntentTx(ctx, tx, intent, owner, true)
	if err != nil {
		return none, err
	}
	if head.Repository != repository {
		return none, ErrStaleRevision
	}
	_, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return none, err
	}
	view, err := verificationAllowanceStatusTx(ctx, tx, intent, now)
	if err != nil {
		return none, err
	}
	if created {
		if change.ExpectedRevision != view.Revision {
			return none, ErrStaleRevision
		}
		if head.State != "accepted" || head.Repository <= 0 {
			return none, factory.ErrVerificationAllowanceState
		}
		if !verificationLimitsIncrease(view.Limits, change.Limits) {
			return none, factory.ErrVerificationAllowanceIncrease
		}
		if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
			return none, err
		}
		view.Revision, view.Limits = cmd.ID, change.Limits
		view.UpdateRemaining()
	}
	view.CommandID, view.Repository = change.CommandID, head.Repository
	view.CanIncrease = head.State == "accepted" && head.Repository > 0
	return view, tx.Commit()
}
