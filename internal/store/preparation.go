package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

// SaveLifecycleGrant records the owner's standing create/start grant. The
// revision CAS rejects stale writers; a withdrawn grant is an inactive row,
// never a deleted one.
func (s *Store) SaveLifecycleGrant(ctx context.Context, g project.LifecycleGrant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	next := g
	next.Revision++
	nextData, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO project_lifecycle_grants(project_id,revision,data) VALUES($1,$2,$3)
		ON CONFLICT(project_id) DO UPDATE SET revision=$4,data=$5 WHERE project_lifecycle_grants.revision=$6`,
		g.Project, next.Revision, string(nextData), next.Revision, string(nextData), g.Revision)
	if err != nil {
		return fmt.Errorf("lifecycle grant save failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("stale lifecycle grant revision")
	}
	return nil
}

// LifecycleGrant returns the standing create/start grant for one project.
func (s *Store) LifecycleGrant(ctx context.Context, projectID string) (project.LifecycleGrant, error) {
	var g project.LifecycleGrant
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM project_lifecycle_grants WHERE project_id=$1`, projectID).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &g)
	}
	return g, err
}

// SaveMaintenanceHold records the maintenance hold with CAS revision. Callers
// sync the native marker through the host; this row stays the source of truth.
func (s *Store) SaveMaintenanceHold(ctx context.Context, m project.MaintenanceHold) error {
	if err := m.Validate(); err != nil {
		return err
	}
	next := m
	next.Revision++
	nextData, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO project_maintenance(project_id,revision,hold,data) VALUES($1,$2,$3,$4)
		ON CONFLICT(project_id) DO UPDATE SET revision=$5,hold=$6,data=$7 WHERE project_maintenance.revision=$8`,
		m.Project, next.Revision, next.Hold, string(nextData), next.Revision, next.Hold, string(nextData), m.Revision)
	if err != nil {
		return fmt.Errorf("maintenance hold save failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("stale maintenance hold revision")
	}
	return nil
}

// MaintenanceHold returns the current maintenance hold for one project.
// A missing row means no hold was ever recorded.
func (s *Store) MaintenanceHold(ctx context.Context, projectID string) (project.MaintenanceHold, error) {
	var m project.MaintenanceHold
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM project_maintenance WHERE project_id=$1`, projectID).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	return m, err
}

// AdmitPreparation records one preparation identity with its immutable
// requirement/admin references. Repeating the same identity returns the
// recorded row; changed references for that identity conflict.
func (s *Store) AdmitPreparation(ctx context.Context, p execution.PreparationRecord) (execution.PreparationRecord, bool, error) {
	if err := p.Validate(); err != nil {
		return execution.PreparationRecord{}, false, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	prep := p.Preparation
	result, err := s.db.ExecContext(ctx, `INSERT INTO project_preparations(id,project_id,role,revision,requirements,approval,data) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(id) DO NOTHING`, prep.ID, prep.Project, prep.Role, prep.Revision, prep.Requirements.ID, prep.Approval.ID, string(data))
	if err != nil {
		return execution.PreparationRecord{}, false, fmt.Errorf("preparation admission failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	if n == 1 {
		return p, true, nil
	}
	existing, err := s.Preparation(ctx, prep.ID)
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	if !sameStandingPreparationIdentity(existing, p) {
		return execution.PreparationRecord{}, false, errors.New("preparation identity already carries different approved inputs")
	}
	return existing, false, nil
}

// AdmitStandingPreparation records an explicit initial preparation against the
// current separate human decisions. No caller observation can seed readiness.
func (s *Store) AdmitStandingPreparation(ctx context.Context, record execution.PreparationRecord) (execution.PreparationRecord, bool, error) {
	if err := record.Validate(); err != nil {
		return execution.PreparationRecord{}, false, err
	}
	p := record.Preparation
	if !reflect.DeepEqual(record.State, execution.PreparationState{}) {
		return execution.PreparationRecord{}, false, ErrCandidatePreparationConflict
	}
	if p.Revision != 0 {
		return execution.PreparationRecord{}, false, ErrStaleRevision
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = authorizeStandingPreparationTx(ctx, tx, p); err != nil {
		return execution.PreparationRecord{}, false, err
	}
	stored := record
	data, err := json.Marshal(stored)
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO project_preparations(id,project_id,role,revision,requirements,approval,data) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(id) DO NOTHING`, p.ID, p.Project, p.Role, p.Revision, p.Requirements.ID, p.Approval.ID, string(data))
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return execution.PreparationRecord{}, false, err
	}
	if n == 0 {
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR UPDATE`, p.ID).Scan(&raw); err != nil {
			return execution.PreparationRecord{}, false, err
		}
		var meta *candidatePreparationRegistration
		stored, meta, err = unmarshalCandidatePreparation(raw)
		if err != nil {
			return execution.PreparationRecord{}, false, err
		}
		if meta != nil || !sameStandingPreparationIdentity(stored, record) {
			return execution.PreparationRecord{}, false, ErrCandidatePreparationConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return execution.PreparationRecord{}, false, err
	}
	return stored, n == 1, nil
}

// AuthorizeStandingPreparation rechecks the admission immediately before a
// native effect. Authority reads remain the caller's separate native callback.
func (s *Store) AuthorizeStandingPreparation(ctx context.Context, record execution.PreparationRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	p := record.Preparation
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = authorizeStandingPreparationTx(ctx, tx, p); err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR UPDATE`, p.ID).Scan(&raw); err != nil {
		return err
	}
	stored, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil {
		return err
	}
	if meta != nil || !sameStandingPreparationIdentity(stored, record) {
		return ErrCandidatePreparationConflict
	}
	if stored.State.Stopped || stored.State.Phase == project.PrepareFailed || stored.State.Phase == project.PrepareInterrupted || stored.State.Phase == project.PrepareStopped {
		return ErrCandidatePreparationConflict
	}
	return tx.Commit()
}

func authorizeStandingPreparationTx(ctx context.Context, tx *sql.Tx, p project.Preparation) error {
	var requirement, approval string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1 FOR SHARE`, p.Project).Scan(&requirement); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleRevision
		}
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1 FOR SHARE`, p.Project).Scan(&approval); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleRevision
		}
		return err
	}
	if requirement != p.Requirements.ID || approval != p.Approval.ID || p.SourceCommit != p.Requirements.SourceCommit {
		return ErrStaleRevision
	}
	if err := checkCandidatePreparationSetupTx(ctx, tx, p); err != nil {
		return err
	}
	var held bool
	err := tx.QueryRowContext(ctx, `SELECT hold FROM project_maintenance WHERE project_id=$1 FOR SHARE`, p.Project).Scan(&held)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if held {
		return ErrAdmissionChanged
	}
	return nil
}

func sameStandingPreparationIdentity(left, right execution.PreparationRecord) bool {
	if !left.Preparation.Requirements.Equal(right.Preparation.Requirements) {
		return false
	}
	// SourceInputs uses omitempty; an absent selection and an explicitly empty
	// selection retain the same exact-source-only meaning after JSON storage.
	left.Preparation.Requirements.SourceInputs = nil
	right.Preparation.Requirements.SourceInputs = nil
	return samePreparationIdentity(left, right)
}

// ObservePreparation advances only the observed state under CAS revision. The
// approved references are additionally guarded by a schema trigger.
func readPreparationHead(ctx context.Context, db *sql.DB, p execution.PreparationRecord) (*candidatePreparationRegistration, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, p.Preparation.ID).Scan(&raw); err != nil {
		return nil, err
	}
	_, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

func lockPreparationObservationTx(ctx context.Context, tx *sql.Tx, meta *candidatePreparationRegistration) (factory.AttemptAllowance, error) {
	if meta == nil || meta.PublicationID == "" {
		return factory.AttemptAllowance{}, nil
	}
	return lockAttemptAllowanceRootTx(ctx, tx, meta.Repository, meta.Issue, meta.AttemptRoot)
}

func checkPreparationObservationCurrent(current execution.PreparationRecord, currentMeta, meta *candidatePreparationRegistration, p execution.PreparationRecord, revision int64) error {
	if (meta == nil) != (currentMeta == nil) || meta != nil && !sameCandidatePreparationAdmission(*meta, *currentMeta, false) {
		return errors.New("preparation admission changed during observation")
	}
	if currentMeta != nil && currentMeta.Stopped && currentMeta.Retirement == "confirmed" && !p.State.Stopped {
		return errors.New("confirmed preparation stop cannot be replaced by a later observation")
	}
	if current.State.Stopped && current.State.Retirement == "confirmed" && p.State.RetirementState() != execution.Retired {
		return errors.New("confirmed preparation retirement cannot be downgraded")
	}
	if current.State.Container != "" && current.State.Container != p.State.Container {
		return errors.New("preparation container identity changed during observation")
	}
	if revision != p.Preparation.Revision || !samePreparationIdentity(current, p) {
		return errors.New("stale or changed preparation observation")
	}
	return nil
}

func writePreparationObservationTx(ctx context.Context, tx *sql.Tx, current execution.PreparationRecord, currentMeta, meta *candidatePreparationRegistration, p execution.PreparationRecord, revision int64, allowance factory.AttemptAllowance) error {
	previousPhase := current.State.Phase
	current.State = p.State
	current.Preparation.Revision++
	var data []byte
	var err error
	if currentMeta == nil {
		data, err = json.Marshal(current)
	} else {
		data, err = marshalCandidatePreparation(current, *currentMeta)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_preparations SET revision=$1,data=$2 WHERE id=$3 AND revision=$4`,
		current.Preparation.Revision, string(data), p.Preparation.ID, revision); err != nil {
		return fmt.Errorf("preparation observation failed: %w", err)
	}
	if meta != nil && meta.PublicationID != "" && allowance.Closed {
		if err = transitionLockedAttemptRootTx(ctx, tx, allowance, true, time.Now()); err != nil {
			return err
		}
	}
	if p.State.Phase != "" && p.State.Phase != previousPhase {
		var repository, issue int64
		var assignment string
		if meta != nil {
			repository, issue, assignment = meta.Repository, meta.Issue, meta.OwnerAssignment
		} else if err = tx.QueryRowContext(ctx, `SELECT repository_id FROM projects WHERE id=$1`, p.Preparation.Project).Scan(&repository); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if repository > 0 {
			content := factory.FeedCommunication{
				ComponentID: "F01", Title: "Worker environment " + p.State.Phase, Summary: "Preparation observed the recorded environment state.",
				Items: []factory.FeedCommunicationItem{{Label: p.Preparation.Role, Detail: p.Preparation.ID, State: p.State.Phase}},
			}
			if p.State.Phase == project.PrepareFailed || p.State.Phase == project.PrepareInterrupted {
				content.Severity = "warning"
			}
			if err = recordFactoryFeedEventTx(ctx, tx, repository, issue, assignment, fmt.Sprintf("%s:%d", p.Preparation.ID, current.Preparation.Revision), "preparation:"+p.State.Phase, content, 0); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ObservePreparation(ctx context.Context, p execution.PreparationRecord) error {
	meta, err := readPreparationHead(ctx, s.db, p)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	allowance, err := lockPreparationObservationTx(ctx, tx, meta)
	if err != nil {
		return err
	}
	var currentRaw []byte
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision,data FROM project_preparations WHERE id=$1 FOR UPDATE`, p.Preparation.ID).Scan(&revision, &currentRaw); err != nil {
		return err
	}
	current, currentMeta, err := unmarshalCandidatePreparation(currentRaw)
	if err != nil {
		return err
	}
	if err = checkPreparationObservationCurrent(current, currentMeta, meta, p, revision); err != nil {
		return err
	}
	return writePreparationObservationTx(ctx, tx, current, currentMeta, meta, p, revision, allowance)
}

// Preparation returns one durable preparation record by identity.
func (s *Store) Preparation(ctx context.Context, id string) (execution.PreparationRecord, error) {
	var p execution.PreparationRecord
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &p)
	}
	if err == nil {
		err = p.Validate()
	}
	return p, err
}

// ProjectPreparations lists the durable preparation records for one project.
func (s *Store) ProjectPreparations(ctx context.Context, projectID string) ([]execution.PreparationRecord, error) {
	return s.projectPreparations(ctx, projectID, false)
}

// StandingProjectPreparations excludes private candidate custody: one issue's
// consumed checkout cannot become another issue's approved source template.
func (s *Store) StandingProjectPreparations(ctx context.Context, projectID string) ([]execution.PreparationRecord, error) {
	return s.projectPreparations(ctx, projectID, true)
}

// PendingStandingPreparations rotates a bounded observation page across
// Projects. Private candidate preparation keeps its existing custody owner.
func (s *Store) PendingStandingPreparations(ctx context.Context, after string, limit int) ([]execution.PreparationRecord, error) {
	if limit < 1 || limit > 16 || after != "" && !project.ValidPreparationID(after) {
		return nil, errors.New("invalid standing preparation observation page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM project_preparations
		WHERE NOT (data ? 'factory_admission')
		AND COALESCE(data#>>'{state,phase}','') IN ('',$1,$2,$3)
		AND NOT COALESCE((data#>>'{state,ready}')::boolean,FALSE)
		AND NOT COALESCE((data#>>'{state,stopped}')::boolean,FALSE)
		ORDER BY (id>$4) DESC,id LIMIT $5`, project.PrepareApproved, project.PrepareWaiting, project.PrepareRunning, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []execution.PreparationRecord
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var record execution.PreparationRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		if err := record.Validate(); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) projectPreparations(ctx context.Context, projectID string, standing bool) ([]execution.PreparationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM project_preparations WHERE project_id=$1 AND (NOT $2 OR NOT (data ? 'factory_admission')) ORDER BY id LIMIT 129`, projectID, standing)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []execution.PreparationRecord{}
	for rows.Next() {
		var data []byte
		var p execution.PreparationRecord
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		if err = p.Validate(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
