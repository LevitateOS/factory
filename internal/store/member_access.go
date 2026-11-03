package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

var ErrProjectAccessHeld = errors.New("project access is held; restoration and explicit Join are required")

type MemberAccess struct {
	Revision     int64                      `json:"revision"`
	Held         bool                       `json:"held"`
	RequiresJoin bool                       `json:"requires_join"`
	Action       string                     `json:"action"`
	Complete     bool                       `json:"complete"`
	Native       *project.MemberAccessState `json:"native"`
}

func (s *Store) MemberIdentity(ctx context.Context, pid string, uid int64) (string, error) {
	var login string
	err := s.db.QueryRowContext(ctx, `SELECT login FROM memberships WHERE project_id=$1 AND user_id=$2`, pid, uid).Scan(&login)
	return login, err
}

func (s *Store) MemberAccess(ctx context.Context, pid string, uid int64) (MemberAccess, error) {
	var out MemberAccess
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision,held,rejoin,data FROM project_member_access WHERE project_id=$1 AND user_id=$2`, pid, uid).Scan(&out.Revision, &out.Held, &out.RequiresJoin, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var detail struct {
		Action   string                     `json:"action"`
		Complete bool                       `json:"complete"`
		Native   *project.MemberAccessState `json:"native"`
	}
	if err = json.Unmarshal(data, &detail); err != nil {
		return out, err
	}
	out.Action, out.Complete, out.Native = detail.Action, detail.Complete, detail.Native
	return out, nil
}

// BeginMemberAccess records the hold before any native dispatch. A retry of
// an unfinished operation preserves its revision and reconciles the same effects.
func (s *Store) BeginMemberAccess(ctx context.Context, pid string, uid, revision int64, action string) (MemberAccess, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemberAccess{}, err
	}
	defer tx.Rollback()
	var login string
	if err = tx.QueryRowContext(ctx, `SELECT login FROM memberships WHERE project_id=$1 AND user_id=$2 FOR UPDATE`, pid, uid).Scan(&login); err != nil {
		return MemberAccess{}, err
	}
	var old MemberAccess
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,held,rejoin,data FROM project_member_access WHERE project_id=$1 AND user_id=$2 FOR UPDATE`, pid, uid).Scan(&old.Revision, &old.Held, &old.RequiresJoin, &data)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return old, err
	}
	if old.Revision != revision {
		return old, ErrStaleRevision
	}
	if len(data) > 0 {
		var detail struct {
			Action   string `json:"action"`
			Complete bool   `json:"complete"`
		}
		if err = json.Unmarshal(data, &detail); err != nil {
			return old, err
		}
		old.Action, old.Complete = detail.Action, detail.Complete
	}
	if old.Action == action && !old.Complete {
		return old, tx.Commit()
	}
	if action == "restore" && (!old.Held || !old.Complete) {
		return old, ErrStaleRevision
	}
	next := MemberAccess{Revision: revision + 1, Held: true, Action: action}
	data, err = json.Marshal(next)
	if err != nil {
		return old, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO project_member_access(project_id,user_id,revision,held,rejoin,data) VALUES($1,$2,$3,TRUE,FALSE,$4) ON CONFLICT(project_id,user_id) DO UPDATE SET revision=EXCLUDED.revision,held=TRUE,rejoin=FALSE,data=EXCLUDED.data`, pid, uid, next.Revision, data)
	if err != nil {
		return old, err
	}
	return next, tx.Commit()
}

func (s *Store) FinishMemberAccess(ctx context.Context, pid string, uid int64, record MemberAccess, native project.MemberAccessState) error {
	record.Native = &native
	record.Complete = true
	if record.Action == "restore" {
		record.Held = false
		record.RequiresJoin = true
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE project_member_access SET held=$1,rejoin=$2,data=$3 WHERE project_id=$4 AND user_id=$5 AND revision=$6`, record.Held, record.RequiresJoin, data, pid, uid, record.Revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrStaleRevision
	}
	return err
}
