package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var ErrRetainedPublicationIneligible = errors.New("retained candidate publication is no longer eligible")

// RecordRetainedPublicationCommand creates only a publication owner and its
// branch/PR journal. Original executions, uncertain effects and allowances are
// immutable. The command ID is the new owner and publication identity.
func (s *Store) RecordRetainedPublicationCommand(ctx context.Context, cmd factory.Command, a factory.Assignment, p factory.Publication, now time.Time) (factory.Publication, error) {
	if cmd.Validate() != nil || cmd.Type != factory.CommandPublishRetained || a.Validate() != nil ||
		a.RetainedPublication == nil || cmd.ID != a.ID || p.Validate() != nil || p.ID != cmd.ID || p.AssignmentID != a.ID ||
		p.Stage != factory.PublicationOpen || p.Revision != 0 || p.Publish.Attempts != 0 || p.PRCreate.Attempts != 0 ||
		p.Run != a.RetainedPublication.Run || p.Candidate != a.RetainedPublication.Candidate ||
		cmd.Target != "publication/"+a.RetainedPublication.Publication {
		return factory.Publication{}, fmt.Errorf("%w: invalid publication command or non-executing owner", ErrRetainedPublicationIneligible)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Publication{}, err
	}
	defer tx.Rollback()
	if err = checkDispatchOpenTx(ctx, tx, a.Repository); err != nil {
		return factory.Publication{}, err
	}
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return factory.Publication{}, err
	}
	if !created {
		if stored.Principal != cmd.Principal || stored.Finished == "" {
			return factory.Publication{}, ErrCommandConflict
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, cmd.ID).Scan(&raw); err != nil {
			return factory.Publication{}, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return factory.Publication{}, err
		}
		return p, p.Validate()
	}
	var original factory.Publication
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, a.RetainedPublication.Assignment).Scan(&raw); err != nil {
		return factory.Publication{}, err
	}
	if json.Unmarshal(raw, &original) != nil || original.Validate() != nil || original.ID != a.RetainedPublication.Publication ||
		original.Stage != factory.PublicationFenced || original.WithdrawRequested || original.PRNumber != 0 ||
		original.Run != p.Run || original.Candidate != p.Candidate || original.BaseSHA != p.BaseSHA ||
		original.Repository != p.Repository || original.Issue != p.Issue || original.Acceptance != p.Acceptance ||
		original.ProjectID != p.ProjectID || original.Preparation != p.Preparation || original.TargetBranch != p.TargetBranch ||
		original.Authority.RequirementsID != p.Authority.RequirementsID || original.Authority.ApprovalID != p.Authority.ApprovalID ||
		original.Authority.SponsorshipConnection != p.Authority.SponsorshipConnection {
		return factory.Publication{}, fmt.Errorf("%w: original publication scope changed", ErrRetainedPublicationIneligible)
	}
	var latest string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_assignments WHERE repository=$1 AND issue=$2 AND data->>'publication_assignment'=id ORDER BY seq DESC LIMIT 1`, p.Repository, p.Issue).Scan(&latest); err != nil {
		return factory.Publication{}, err
	}
	if latest != original.AssignmentID {
		return factory.Publication{}, fmt.Errorf("%w: a later publication owner exists", ErrRetainedPublicationIneligible)
	}
	var source factory.Assignment
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, latest).Scan(&raw); err != nil {
		return factory.Publication{}, err
	}
	if json.Unmarshal(raw, &source) != nil || source.Validate() != nil || source.Stage != factory.AssignmentFinished ||
		source.Outcome != factory.Succeeded || source.Result == nil || !source.Result.Reported || source.Result.Status != "completed" ||
		source.Repository != a.Repository || source.Issue != a.Issue || source.ProjectID != a.ProjectID || source.Preparation != a.Preparation ||
		source.Acceptance != a.Acceptance || a.Acceptance != p.Acceptance || source.SourceCommit != a.SourceCommit ||
		a.SourceCommit != p.BaseSHA || a.ProjectID != p.ProjectID || a.Repository != p.Repository || a.Issue != p.Issue ||
		a.Preparation != p.Preparation || !a.Authority.SameGrants(p.Authority) {
		return factory.Publication{}, fmt.Errorf("%w: source execution scope changed", ErrRetainedPublicationIneligible)
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT
	 EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 AND decision=$3)
	 AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3)
	 AND EXISTS(SELECT 1 FROM factory_assignments WHERE id=$4 AND data->>'connection'=$5)
	 AND EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE root_assignment=$4 AND data->>'closed'='true')
	 AND EXISTS(SELECT 1 FROM factory_runs WHERE id=$6 AND settled AND data->>'reconciled'='true' AND data->>'outcome'='succeeded')
	 AND NOT EXISTS(SELECT 1 FROM factory_runs r JOIN factory_run_views v ON v.run=r.id JOIN factory_assignments x ON x.id=v.attempt
	 WHERE x.data->>'attempt_root'=$4 AND (r.active OR NOT r.settled OR r.data->>'reconciled' IS DISTINCT FROM 'true'
	 OR (NULLIF(r.data->'native','null'::jsonb) IS NOT NULL AND
	 (r.data#>>'{native,retirement}' IS DISTINCT FROM 'retired' OR r.data#>>'{native,sealed}' IS DISTINCT FROM 'true'
	 OR COALESCE(r.data#>>'{native,provenance,custody,state}','') NOT IN ('released','not_acquired')))))`,
		p.Repository, p.Issue, p.Acceptance, original.AssignmentID, a.RetainedPublication.Connection, p.Run).Scan(&eligible)
	if err != nil {
		return factory.Publication{}, err
	}
	if !eligible || a.RetainedPublication.NotAfter <= now.Unix() {
		return factory.Publication{}, fmt.Errorf("%w: original runs or allowance remain unsettled, or admission expired", ErrRetainedPublicationIneligible)
	}
	in, err := factoryAuthorityInput(ctx, tx, a.Repository, a.Role)
	if err != nil {
		return factory.Publication{}, err
	}
	in.PreparationReady = true
	effective := factory.EvaluateAuthority(in)
	current := effective.Authority
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1`, a.ProjectID).Scan(&current.RequirementsID); err != nil {
		return factory.Publication{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1`, a.ProjectID).Scan(&current.ApprovalID); err != nil {
		return factory.Publication{}, err
	}
	if !effective.Effective || !current.SameGrants(p.Authority) {
		return factory.Publication{}, fmt.Errorf("%w: current grants changed", ErrRetainedPublicationIneligible)
	}
	assignmentRaw, err := json.Marshal(a)
	if err != nil {
		return factory.Publication{}, err
	}
	publicationRaw, err := json.Marshal(p)
	if err != nil {
		return factory.Publication{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'',$4,0,$5)`, a.ID, a.Repository, a.Issue, a.Stage, string(assignmentRaw)); err != nil {
		return factory.Publication{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,0,$6)`, a.ID, p.Repository, p.Issue, p.Run, p.Stage, string(publicationRaw)); err != nil {
		return factory.Publication{}, err
	}
	if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
		return factory.Publication{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.Publication{}, err
	}
	return p, nil
}
