package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// MergedPreparationSource addresses only an exact successfully completed
// publication whose attributable merge is the selected current target tip.
// Its export remains the reviewed coder candidate, which can differ from a
// squash/rebase/merge result. It does not manufacture export bytes for that result.
func (s *Store) MergedPreparationSource(ctx context.Context, repository int64, projectID, target, tip string) (project.FactoryMergedSource, error) {
	var publicationData, mergeData, runData []byte
	err := s.db.QueryRowContext(ctx, `SELECT p.data,m.data,COALESCE(r.data,'null'::jsonb) FROM factory_publications p
 JOIN factory_merges m ON m.publication=p.data->>'id'
 LEFT JOIN factory_runs r ON r.id=p.data->>'run'
 WHERE p.repository=$1 AND m.stage='merged' AND m.data->>'project_id'=$2
 AND m.data->>'base_ref'=$3 AND m.data->>'merged_commit'=$4
 ORDER BY m.seq DESC LIMIT 1`, repository, projectID, target, tip).Scan(&publicationData, &mergeData, &runData)
	if err != nil {
		return project.FactoryMergedSource{}, err
	}
	return mergedPreparationSource(publicationData, mergeData, runData, repository, projectID, target, tip)
}

func admitMergedSource(m factory.Merge, p factory.Publication, repository int64, projectID, target, tip string) error {
	if m.Validate() != nil || m.Stage != factory.MergeMerged ||
		m.PublicationID != p.ID || m.AssignmentID != p.AssignmentID || m.ProjectID != projectID ||
		m.Repository != repository || m.BaseRef != target || m.MergedCommit != tip ||
		m.HeadOID != p.Candidate {
		return ErrCandidatePreparationConflict
	}
	return nil
}

func admitMergedPublication(p factory.Publication, run factory.Run, repository int64, projectID, candidate string) error {
	if p.Validate() != nil || p.ProjectID != projectID ||
		p.Repository != repository || p.Candidate != candidate || p.Role != project.RoleCoder {
		return ErrCandidatePreparationConflict
	}
	if run.Validate() != nil || run.ID != p.Run || run.ProjectID != projectID || run.Role != project.RoleCoder || !run.Reconciled || run.Outcome != factory.Succeeded {
		return ErrCandidatePreparationConflict
	}
	return nil
}

func mergedPreparationSource(publicationData, mergeData, runData []byte, repository int64, projectID, target, tip string) (project.FactoryMergedSource, error) {
	var p factory.Publication
	var m factory.Merge
	var run factory.Run
	if err := json.Unmarshal(publicationData, &p); err != nil {
		return project.FactoryMergedSource{}, err
	}
	if err := json.Unmarshal(mergeData, &m); err != nil {
		return project.FactoryMergedSource{}, err
	}
	if p.External != nil && p.Run == "" {
		if p.Validate() != nil || p.ProjectID != projectID || p.Repository != repository || p.Candidate != m.HeadOID || admitMergedSource(m, p, repository, projectID, target, tip) != nil {
			return project.FactoryMergedSource{}, ErrCandidatePreparationConflict
		}
		return project.FactoryMergedSource{Pull: p.PullSource(), Project: projectID, MergeID: m.ID, Commit: tip, Repository: repository, Target: target}, nil
	}
	if err := json.Unmarshal(runData, &run); err != nil {
		return project.FactoryMergedSource{}, err
	}
	if err := admitMergedSource(m, p, repository, projectID, target, tip); err != nil {
		return project.FactoryMergedSource{}, err
	}
	if err := admitMergedPublication(p, run, repository, projectID, m.HeadOID); err != nil {
		return project.FactoryMergedSource{}, err
	}
	return project.FactoryMergedSource{Export: project.FactoryExport{Project: projectID, ID: p.Run, Role: project.RoleCoder, Preparation: p.Preparation, Candidate: p.Candidate}, MergeID: m.ID, Commit: tip, Repository: repository, Target: target}, nil
}

// mergedPreparationSourceTx runs the existing publication+merged+run
// source predicate inside the caller's repeatable-read capture
// transaction: the exact repo/project/target-branch/tip relation,
// validated by the existing merged preparation source validator. It
// performs no commit and owns no other query.
func mergedPreparationSourceTx(ctx context.Context, tx *sql.Tx, repository int64, projectID, target, tip string) (project.FactoryMergedSource, error) {
	var publicationData, mergeData, runData []byte
	err := tx.QueryRowContext(ctx, `SELECT p.data,m.data,COALESCE(r.data,'null'::jsonb) FROM factory_publications p
	 JOIN factory_merges m ON m.publication=p.data->>'id'
	 LEFT JOIN factory_runs r ON r.id=p.data->>'run'
	 WHERE p.repository=$1 AND m.stage='merged' AND m.data->>'project_id'=$2
	 AND m.data->>'base_ref'=$3 AND m.data->>'merged_commit'=$4
	 ORDER BY m.seq DESC LIMIT 1`, repository, projectID, target, tip).Scan(&publicationData, &mergeData, &runData)
	if err != nil {
		return project.FactoryMergedSource{}, err
	}
	return mergedPreparationSource(publicationData, mergeData, runData, repository, projectID, target, tip)
}

func checkMergedPreparationSourceTx(ctx context.Context, tx *sql.Tx, r CandidatePreparationRegistration, tip, target string) error {
	if r.SourceMerge != nil && r.SourceMerge.Pull != nil {
		if r.SourceRun != nil || r.SourceMerge.Validate() != nil || r.SourceMerge.Workspace() != r.Project {
			return ErrCandidatePreparationConflict
		}
		bound, err := mergedPreparationSourceTx(ctx, tx, r.Repository, r.Project, target, tip)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(bound, *r.SourceMerge) {
			return ErrCandidatePreparationConflict
		}
		return nil
	}
	source := r.SourceRun
	if r.SourceMerge != nil {
		source = &r.SourceMerge.Export
	}
	if source == nil || source.Validate() != nil || source.Project != r.Project || source.Role != project.RoleCoder || r.SourceMerge == nil && source.Candidate != tip {
		return ErrCandidatePreparationConflict
	}
	var p, m, run []byte
	err := tx.QueryRowContext(ctx, `SELECT p.data,m.data,r.data FROM factory_publications p
 JOIN factory_merges m ON m.publication=p.data->>'id' JOIN factory_runs r ON r.id=p.data->>'run'
 WHERE p.repository=$1 AND p.data->>'run'=$2 AND m.stage='merged' ORDER BY m.seq DESC LIMIT 1`, r.Repository, source.ID).Scan(&p, &m, &run)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCandidatePreparationConflict
	}
	if err != nil {
		return err
	}
	bound, err := mergedPreparationSource(p, m, run, r.Repository, r.Project, target, tip)
	if err != nil {
		return err
	}
	if r.SourceMerge != nil && !reflect.DeepEqual(bound, *r.SourceMerge) || r.SourceMerge == nil && (bound.Export != *source || bound.Commit != source.Candidate) {
		return ErrCandidatePreparationConflict
	}
	return nil
}

func checkQueuedPreparationOwnerTx(ctx context.Context, tx *sql.Tx, r CandidatePreparationRegistration) error {
	var commandExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_commands WHERE id=$1)`, r.OwnerAssignment).Scan(&commandExists); err != nil {
		return err
	}
	if commandExists {
		return checkRetryPreparationOwnerTx(ctx, tx, r)
	}
	var assigned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE repository=$1 AND issue=$2 AND data->>'acceptance'=$3)`, r.Repository, r.Issue, r.Control.Acceptance).Scan(&assigned); err != nil {
		return err
	}
	if assigned {
		return ErrAssignmentActive
	}
	return nil
}

// A separately funded external command may follow a fully settled issue root.
// Its source setup is journaled here before the normal Architect packet exists;
// neither the old root nor the ordinary queued/Retry admission is reopened.
func checkExternalPreparationOwnerTx(ctx context.Context, tx *sql.Tx, preparation execution.PreparationRecord, r CandidatePreparationRegistration, target string) error {
	e := r.ExternalPlanning
	if e == nil || e.Validate(r.OwnerAssignment) != nil {
		return ErrAdmissionChanged
	}
	a := factory.Assignment{ID: r.OwnerAssignment, AttemptRoot: r.AttemptRoot, PublicationAssignment: r.OwnerAssignment,
		Repository: r.Repository, Issue: r.Issue, ProjectID: r.Project, ActorID: r.ActorID, Role: project.RoleCoder,
		Acceptance: r.Control.Acceptance, Preparation: preparation.Preparation.ID, SourceCommit: preparation.Preparation.SourceCommit,
		Authority: r.Authority, Connection: r.Connection, ExternalPlanning: e}
	var retained, commanded bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_preparations WHERE id=$1), EXISTS(SELECT 1 FROM factory_commands WHERE id=$2)`, a.Preparation, a.ID).Scan(&retained, &commanded); err != nil {
		return err
	}
	if commanded {
		return ErrAssignmentActive
	}
	if err := checkExternalAdmissionScopeTx(ctx, tx, e.Request, a, externalPlanningTarget(a, time.Now()), time.Now(), !retained); err != nil {
		return err
	}
	if r.SourceRun != nil || r.SourceMerge != nil {
		return checkMergedPreparationSourceTx(ctx, tx, r, a.SourceCommit, target)
	}
	return nil
}

func checkCoderPreparationCustodyTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if a.Role != project.RoleCoder || a.PublicationAssignment != a.ID {
		return nil
	}
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 AND data ? 'factory_admission' FOR UPDATE`, a.Preparation).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p, meta, err := unmarshalCandidatePreparation(data)
	if err != nil {
		return err
	}
	if meta != nil && meta.NotAfter <= time.Now().Unix() {
		return factory.ErrAttemptTimeExhausted
	}
	if meta == nil || !p.State.Ready || meta.Stopped || meta.Retirement == "confirmed" ||
		meta.Repository != a.Repository || meta.Issue != a.Issue || meta.OwnerAssignment != a.ID ||
		meta.ChildAssignment != a.ID || meta.AttemptRoot != a.AttemptRoot || meta.PublicationID != "" ||
		p.Preparation.SourceCommit != a.SourceCommit || meta.Authority != a.Authority {
		return ErrAdmissionChanged
	}
	if a.ExternalPlanning != nil || meta.ExternalPlanning != nil {
		captured := a.ExternalPlanning
		if captured != nil {
			original := *captured
			original.CheckedUnix = 0
			captured = &original
		}
		if !reflect.DeepEqual(meta.ExternalPlanning, captured) {
			return ErrAdmissionChanged
		}
	}
	return nil
}
