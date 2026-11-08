package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var ErrNativeJudgmentSource = errors.New("selected judgment source is unavailable or exhausted")

func (s *Store) PendingNativeJudgments(ctx context.Context) ([]factory.Command, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,target,principal,digest,payload,created FROM factory_commands WHERE type=$1 AND finished='' ORDER BY created,id LIMIT 32`, factory.CommandNativeJudgment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var commands []factory.Command
	for rows.Next() {
		cmd := factory.Command{Type: factory.CommandNativeJudgment}
		if err = rows.Scan(&cmd.ID, &cmd.Target, &cmd.Principal, &cmd.Digest, &cmd.Payload, &cmd.Created); err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}
	return commands, rows.Err()
}

func nativeJudgmentSource(ctx context.Context, q grantCommandSQL, c factory.NativeJudgmentCapture) (factory.Sponsorship, error) {
	var policy factory.RepositoryPolicy
	if err := loadGrantQuery(ctx, q, "factory_policies", "repository", c.Repository, &policy); err != nil {
		return factory.Sponsorship{}, err
	}
	if policy.Judgments == nil || !reflect.DeepEqual(*policy.Judgments, c.Selection) || c.PolicyRevision != nil && policy.Revision != *c.PolicyRevision {
		return factory.Sponsorship{}, ErrNativeJudgmentSource
	}
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 AND connection=$2`, c.Repository, c.Selection.Account).Scan(&raw); err != nil {
		return factory.Sponsorship{}, err
	}
	var source factory.Sponsorship
	if json.Unmarshal(raw, &source) != nil || source.Validate() != nil || source.Repository != c.Repository || source.Connection != c.Selection.Account || !source.Active ||
		source.Native == nil || source.Native.Profile != c.Profile || source.Native.Authentication != factory.NativeMuseLogin ||
		!slices.Contains(source.Roles, factory.RoleJudgment) || c.FundingRevision != nil && source.Revision != *c.FundingRevision {
		return factory.Sponsorship{}, ErrNativeJudgmentSource
	}
	return source, nil
}

func (s *Store) CheckNativeJudgmentSource(ctx context.Context, c factory.NativeJudgmentCapture) error {
	_, err := nativeJudgmentSource(ctx, s.db, c)
	return err
}

// NativeJudgmentReady is a read-only preflight. BeginNativeJudgment repeats it
// under the repository grant lock before reserving any source or dispatch.
func (s *Store) NativeJudgmentReady(ctx context.Context, c factory.NativeJudgmentCapture, requiredMinutes int) error {
	return nativeJudgmentRoom(ctx, s.db, c, requiredMinutes)
}

func nativeJudgmentRoom(ctx context.Context, q grantCommandSQL, c factory.NativeJudgmentCapture, requiredMinutes int) error {
	source, err := nativeJudgmentSource(ctx, q, c)
	if err != nil {
		return err
	}
	var used, pending int
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(minutes),0) FROM factory_usage WHERE repository=$1 AND connection=$2`, c.Repository, c.Selection.Account).Scan(&used); err != nil {
		return err
	}
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM factory_commands WHERE type=$1 AND target=$2 AND principal=$3 AND finished='' AND id<>$4`, factory.CommandNativeJudgment, strconv.FormatInt(c.Repository, 10), c.Selection.Account, c.ID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 || requiredMinutes <= 0 || requiredMinutes > source.AllowanceMinutes-used {
		return ErrNativeJudgmentSource
	}
	return nil
}

// BeginNativeJudgment atomically captures the selected source, checks its shared
// minute allowance and reserves one bounded call. Only fresh=true may dispatch.
// Unknown sends stay reserved; restart never turns a missing receipt into a call.
func (s *Store) BeginNativeJudgment(ctx context.Context, c factory.NativeJudgmentCapture) (factory.Command, bool, error) {
	cmd, err := c.Command()
	if err != nil {
		return factory.Command{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Command{}, false, err
	}
	defer tx.Rollback()
	if err = lockGrantScopeTx(ctx, tx, c.Repository); err != nil {
		return factory.Command{}, false, err
	}
	if err = nativeJudgmentRoom(ctx, tx, c, 1); err != nil {
		return factory.Command{}, false, err
	}
	if c.DeadlineUnixMS <= time.Now().UnixMilli() || c.DeadlineUnixMS > time.Now().Add(30*time.Second).UnixMilli() {
		return factory.Command{}, false, ErrNativeJudgmentSource
	}
	saved, fresh, err := recordFactoryCommandTx(ctx, tx, cmd, time.Now())
	if err != nil {
		return factory.Command{}, false, err
	}
	return saved, fresh, tx.Commit()
}

// CompleteNativeJudgment records actual retired-process usage and its bounded
// result together. Withdrawal cannot suppress accounting for an original call.
func (s *Store) CompleteNativeJudgment(ctx context.Context, c factory.NativeJudgmentCapture, outcome json.RawMessage, usage factory.Usage) error {
	cmd, err := c.Command()
	if err != nil || len(outcome) == 0 || len(outcome) > 65536 || !json.Valid(outcome) || usage.Validate() != nil ||
		usage.RunID != c.ID || usage.Repository != c.Repository || usage.Connection != c.Selection.Account {
		return errors.New("invalid native judgment result")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var digest, prior string
	if err = tx.QueryRowContext(ctx, `SELECT digest,outcome FROM factory_commands WHERE id=$1 AND type=$2 FOR UPDATE`, cmd.ID, cmd.Type).Scan(&digest, &prior); err != nil {
		return err
	}
	if digest != cmd.Digest || prior != "" && prior != string(outcome) {
		return ErrCommandConflict
	}
	if prior != "" {
		return tx.Commit()
	}
	data, _ := json.Marshal(usage)
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_usage(run,repository,connection,minutes,data,started_at,ended_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run) DO NOTHING`, usage.RunID, usage.Repository, usage.Connection, usage.Minutes, string(data), floorUsageMicro(usage.StartedAt), ceilUsageMicro(usage.EndedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$1,finished=$2 WHERE id=$3`, string(outcome), time.Now().UTC().Format(time.RFC3339Nano), cmd.ID); err != nil {
		return err
	}
	return tx.Commit()
}
