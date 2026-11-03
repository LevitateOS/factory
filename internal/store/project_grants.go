package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/levitateos/sodaos/internal/project"
)

// SaveEnvironmentGrant records the owner's standing environment permission
// under CAS revision. A withdrawn grant is an inactive row, never deleted.
func (s *Store) SaveEnvironmentGrant(ctx context.Context, g project.EnvironmentGrant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	next := g
	next.Revision++
	return s.saveRevisionedGrant(ctx, "project_environment_grants", "repository", g.Repository, g.Revision, next, "environment grant")
}

// EnvironmentGrant returns the standing environment permission for one repository.
func (s *Store) EnvironmentGrant(ctx context.Context, repository int64) (project.EnvironmentGrant, error) {
	var g project.EnvironmentGrant
	return g, s.loadGrant(ctx, "project_environment_grants", "repository", repository, &g)
}

// AdmitRequirementDecision records the maintainer's requirement acceptance.
func (s *Store) AdmitRequirementDecision(ctx context.Context, d project.RequirementDecision) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return s.admitProjectDecision(ctx, "project_requirement_decisions", "project_requirement_heads", d.ID, d.Predecessor, d.Project, d)
}

// RequirementDecision returns one recorded requirement acceptance.
func (s *Store) RequirementDecision(ctx context.Context, id string) (project.RequirementDecision, error) {
	var d project.RequirementDecision
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM project_requirement_decisions WHERE id=$1`, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &d)
	}
	return d, err
}

// RequirementHead returns the current requirement decision for one project.
func (s *Store) RequirementHead(ctx context.Context, projectID string) (string, error) {
	var head string
	err := s.db.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1`, projectID).Scan(&head)
	return head, err
}

// RequirementDepth counts the recorded requirement chain from the first
// decision to the head. Preparation references carry this depth.
func (s *Store) RequirementDepth(ctx context.Context, projectID string) (int64, error) {
	var depth int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_requirement_decisions WHERE project_id=$1`, projectID).Scan(&depth)
	return depth, err
}

// AdmitApprovalDecision records the administrator's privileged-effect approval.
func (s *Store) AdmitApprovalDecision(ctx context.Context, d project.ApprovalDecision) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return s.admitProjectDecision(ctx, "project_approval_decisions", "project_approval_heads", d.ID, d.Predecessor, d.Project, d)
}

// ApprovalDecision returns one recorded privileged-effect approval.
func (s *Store) ApprovalDecision(ctx context.Context, id string) (project.ApprovalDecision, error) {
	var d project.ApprovalDecision
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM project_approval_decisions WHERE id=$1`, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &d)
	}
	return d, err
}

// ApprovalHead returns the current privileged-effect approval for one project.
func (s *Store) ApprovalHead(ctx context.Context, projectID string) (string, error) {
	var head string
	err := s.db.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1`, projectID).Scan(&head)
	return head, err
}

// ApprovalDepth counts the recorded approval chain for preparation references.
func (s *Store) ApprovalDepth(ctx context.Context, projectID string) (int64, error) {
	var depth int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM project_approval_decisions WHERE project_id=$1`, projectID).Scan(&depth)
	return depth, err
}

// admitProjectDecision records one immutable decision and advances its
// project head. The expected predecessor must equal the current head, or be
// empty when no decision was ever recorded. Repeating the identical
// decision replays without advancing twice.
func (s *Store) admitProjectDecision(ctx context.Context, table, heads, id, predecessor, projectID string, decision any) error {
	data, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var head sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT decision FROM `+heads+` WHERE project_id=$1`, projectID).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := ""
	if head.Valid {
		current = head.String
	}
	if current == id {
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM `+table+` WHERE id=$1`, id).Scan(&raw); err != nil {
			return err
		}
		same, err := sameJSONDocument(ctx, tx, raw, data)
		if err != nil {
			return err
		}
		if !same {
			return ErrCommandConflict
		}
		return tx.Commit()
	}
	if current != predecessor {
		return ErrStaleRevision
	}
	var taken int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE id=$1`, id).Scan(&taken); err != nil {
		return err
	}
	if taken != 0 {
		return ErrCommandConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO `+table+`(id,project_id,predecessor,data) VALUES($1,$2,$3,$4)`, id, projectID, predecessor, string(data)); err != nil {
		return fmt.Errorf("decision admission failed: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO `+heads+`(project_id,decision) VALUES($1,$2)
		ON CONFLICT(project_id) DO UPDATE SET decision=$3 WHERE `+heads+`.decision=$4`, projectID, id, id, predecessor)
	if err != nil {
		return fmt.Errorf("decision head advance failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return tx.Commit()
}
