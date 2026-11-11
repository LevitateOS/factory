package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// ExternalIssuePlan resolves actual planning provenance under the current
// accepted issue and exact base. Caller text, a previous PR description and
// another issue's plan are not substitutes for an admitted plan.
func (s *Store) ExternalIssuePlan(ctx context.Context, repository, issue int64, base string) (factory.Assignment, error) {
	var a factory.Assignment
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT a.data FROM factory_assignments a JOIN issue_acceptance_heads h ON h.repository=a.repository AND h.issue=a.issue AND h.decision=a.data->>'acceptance'
	 WHERE a.repository=$1 AND a.issue=$2 AND a.stage='finished' AND a.data->>'phase'='investigate' AND a.data->>'source_commit'=$3 AND a.data->>'outcome'='succeeded'
	 ORDER BY a.seq DESC LIMIT 1`, repository, issue, base).Scan(&raw)
	if err != nil {
		return a, err
	}
	if json.Unmarshal(raw, &a) != nil || a.Validate() != nil {
		return factory.Assignment{}, ErrAdmissionChanged
	}
	if a.NativeTool == nil || a.Result == nil || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Plan == nil || a.Result.PhaseResult.Output.PlanReview == "" {
		return factory.Assignment{}, ErrNotFound
	}
	return a, nil
}

// RecordExternalPublicationCommand admits one explicitly funded external PR.
// It uses the same dispatch gate, canonical publication, assignment, command
// journal and finite attempt allowance as own delivery. No native send is
// authorized by this transaction and no old authority is renewed.
func (s *Store) RecordExternalPublicationCommand(ctx context.Context, cmd factory.Command, request factory.ExternalPullAdoption, a factory.Assignment, p factory.Publication, now time.Time) (factory.Publication, error) {
	var empty factory.Publication
	if request.Validate() != nil || cmd.Validate() != nil || cmd.Type != factory.CommandAdoptExternalPull || cmd.ID != a.ID || cmd.ID != p.ID || cmd.Target != factory.ExternalPullTarget(request.Repository, request.PRNumber) || a.Validate() != nil || a.ExternalPublication == nil || p.Validate() != nil || p.Revision != 0 || p.Stage != factory.PublicationPublished || p.Run != "" ||
		a.ExternalPublication.PlanAssignment != request.PlanAssignment || a.ExternalPublication.Connection != request.Connection || !reflect.DeepEqual(p.External, &a.ExternalPublication.Origin) || p.Repository != request.Repository || p.Issue != request.Issue || p.Acceptance != request.Acceptance || p.PRNumber != request.PRNumber || p.PRID != request.PRID || p.External.Target != request.Target ||
		len(p.ReviewOperations)+len(p.ReviewCycles)+len(p.Corrections)+len(p.CorrectionJournals)+len(p.DeliveryAcceptances)+len(p.ResolutionOperations) != 0 {
		return empty, ErrAdmissionChanged
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err = checkDispatchOpenTx(ctx, tx, p.Repository); err != nil {
		return empty, err
	}
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return empty, err
	}
	if !created {
		if stored.Principal != cmd.Principal || stored.Finished == "" {
			return empty, ErrCommandConflict
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, cmd.ID).Scan(&raw); err != nil {
			return empty, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return empty, err
		}
		return p, p.Validate()
	}
	if err = checkExternalAdmissionTx(ctx, tx, request, a, p, now, true); err != nil {
		return empty, err
	}
	adata, err := json.Marshal(a)
	if err != nil {
		return empty, err
	}
	pdata, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'',$4,0,$5)`, a.ID, a.Repository, a.Issue, a.Stage, string(adata)); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'',$4,0,$5)`, p.AssignmentID, p.Repository, p.Issue, p.Stage, string(pdata)); err != nil {
		return empty, err
	}
	if err = insertAttemptAllowanceTx(ctx, tx, p.Repository, p.Issue, a.ID, request.Limits, now.Add(time.Duration(request.Limits.ActiveMinutes)*time.Minute), now); err != nil {
		return empty, err
	}
	if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return p, nil
}

func checkExternalAdmissionTx(ctx context.Context, tx *sql.Tx, request factory.ExternalPullAdoption, a factory.Assignment, p factory.Publication, now time.Time, fresh bool) error {
	if request.Automatic != p.External.Automatic {
		return ErrAdmissionChanged
	}
	if err := checkExternalPullOrigin(ctx, tx, request, a.ID); err != nil {
		return err
	}
	if p.External.ObservedUnix > now.Unix() || fresh && now.Unix()-p.External.ObservedUnix > 30 || (p.External.Target.HeadRepository != p.Repository) != request.ForkConsent {
		return ErrAdmissionChanged
	}
	var sameHost bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM collaboration_repositories b JOIN collaboration_repositories h ON h.host=b.host WHERE b.id=$1 AND h.id=$2 AND b.host=$3)`, p.Repository, p.External.Target.HeadRepository, p.External.Host).Scan(&sameHost); err != nil {
		return err
	}
	if !sameHost {
		return ErrAdmissionChanged
	}
	var revision int64
	var err error
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM factory_native_review_heads WHERE repository=$1 AND pull=$2 FOR UPDATE`, p.Repository, p.PRNumber).Scan(&revision); err != nil || revision != p.External.ObservationRevision {
		return errors.Join(ErrAdmissionChanged, err)
	}
	grants, err := loadAdmissionGrantsTx(ctx, tx, p.Repository, request.Connection)
	if err != nil {
		return err
	}
	if request.Automatic && (!grants.policy.AutomaticExternalPulls || request.ForkConsent) {
		return ErrAdmissionChanged
	}
	// Both model roles must use the explicitly funded source. Saving policy,
	// enrollment or placement cannot silently redirect this command's spend.
	if grants.policy.Revision != request.Policy || grants.policy.Revision != p.External.Policy || grants.policy.TargetBranch != p.TargetBranch || grants.policy.Roles[project.RoleCoder].Account != request.Connection || grants.policy.Roles[project.RoleReviewer].Account != request.Connection ||
		grants.policy.Judgments != nil && grants.policy.Judgments.Account != request.Connection ||
		request.Limits.ActiveMinutes > grants.policy.AttemptLimits.ActiveMinutes || request.Limits.ReviewRounds > grants.policy.AttemptLimits.ReviewRounds || request.Limits.CorrectionCycles > grants.policy.AttemptLimits.CorrectionCycles || request.Limits.MaxChildren > grants.policy.AttemptLimits.MaxChildren ||
		grants.environment.Native == nil || grants.environment.Native.Profile != p.External.Profile || grants.policy.Review.NativeProfile != p.External.Profile || grants.policy.Review.NativeConnection != p.External.Connection || grants.policy.Review.ActorID != p.External.ActorID {
		return ErrAdmissionChanged
	}
	admission := a
	admission.Connection = request.Connection
	if err = checkAssignmentStandingAuthorityTx(ctx, tx, a.Authority, admission, grants); err != nil {
		return err
	}
	if err = checkControlHeadCurrentTx(ctx, tx, p.Repository, p.Issue, p.Acceptance); err != nil {
		return err
	}
	if request.PlanAssignment != "" {
		origin, err := publicationExecutionOwner(ctx, tx, p, a)
		if err != nil {
			return err
		}
		var admitted bool
		plan := origin.Result.PhaseResult.Output.Plan
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_heads h JOIN factory_plan_revisions p ON p.attempt_root=h.attempt_root AND p.plan_id=h.plan_id WHERE h.attempt_root=$1 AND h.plan_id=$2 AND p.digest=$3)`, origin.AttemptRoot, plan.ID, plan.Digest).Scan(&admitted); err != nil {
			return err
		}
		if !admitted {
			return ErrStalePlanBindings
		}
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR UPDATE`, a.Preparation).Scan(&raw); err != nil {
		return err
	}
	preparation, _, err := unmarshalCandidatePreparation(raw)
	if err != nil || preparation.Native == nil || preparation.Native.Profile != p.External.Profile || preparation.Preparation.SourceCommit != p.BaseSHA || !preparation.State.Ready || preparation.State.Stopped || preparation.Preparation.Requirements.ID != a.Authority.RequirementsID || preparation.Preparation.Approval.ID != a.Authority.ApprovalID {
		return ErrAdmissionChanged
	}
	// Serialize on the repository dispatch gate above, including all own and
	// external producers. A Factory-created PR never re-enters external intake.
	var unsafe bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_publications WHERE repository=$1 AND (data->>'pr_number')::bigint=$2 AND
	 (NOT (data ? 'external') OR stage NOT IN ('fenced','withdrawn') OR data#>>'{external,target,head_oid}'=$3))
	 OR EXISTS(SELECT 1 FROM factory_assignments WHERE repository=$1 AND issue=$4 AND stage='assigned')
	 OR EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE repository=$1 AND issue=$4 AND data->>'closed' IS DISTINCT FROM 'true' AND root_assignment<>$5)`, p.Repository, p.PRNumber, p.Candidate, p.Issue, a.ID).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return ErrAttemptRunsPending
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_merges WHERE repository=$1 AND (issue=$2 OR pr=$3)
	 AND COALESCE((data#>>'{operation,attempts}')::int,0)>0 AND data#>>'{operation,completion}' IS DISTINCT FROM 'complete')
	 OR EXISTS(SELECT 1 FROM project_preparations WHERE project_id=$4 AND data ? 'factory_admission'
	 AND data#>>'{factory_admission,repository}'=$1::text AND data#>>'{factory_admission,issue}'=$2::text
	 AND data#>>'{factory_admission,attempt_root}'<>$5 AND NOT soda_preparation_quiescent(data,id,project_id,role))`, p.Repository, p.Issue, p.PRNumber, p.ProjectID, a.ID).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return ErrAttemptRunsPending
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_runs r JOIN factory_run_views v ON v.run=r.id JOIN factory_assignments a ON a.id=v.attempt
	 WHERE a.repository=$1 AND a.issue=$2 AND a.data->>'attempt_root'<>$3 AND (r.active OR NOT r.settled OR r.data->>'reconciled' IS DISTINCT FROM 'true' OR
	 (NULLIF(r.data->'native','null'::jsonb) IS NOT NULL AND (r.data#>>'{native,retirement}' IS DISTINCT FROM 'retired' OR r.data#>>'{native,sealed}' IS DISTINCT FROM 'true'
	 OR COALESCE(r.data#>>'{native,provenance,custody,state}','') NOT IN ('released','not_acquired')))))`, p.Repository, p.Issue, a.ID).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return ErrAttemptRunsPending
	}
	// No unknown run, preparation, review or branch publication can disappear
	// into a fresh command. Terminal labels alone do not settle these owners.
	if request.PlanAssignment != "" {
		settled, err := attemptRootRunsSettledTx(ctx, tx, p.Repository, p.Issue, request.PlanAssignment)
		if err != nil || !settled {
			return errors.Join(ErrAttemptRunsPending, err)
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_publications WHERE repository=$1 AND
	 ((issue=$2) OR (data->>'pr_number')::bigint=$3) AND (
	 EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(data->'review_operations','[]'::jsonb)) o WHERE o#>>'{outcome,Completion}' IS DISTINCT FROM 'complete')
	 OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(data->'corrections','[]'::jsonb)) o WHERE (o->>'attempts')::int>0 AND o->>'completion' IS DISTINCT FROM 'complete')
	 OR ((data#>>'{publish,attempts}')::int>0 AND data#>>'{publish,completion}' IS DISTINCT FROM 'complete')
	 OR ((data#>>'{pr_create,attempts}')::int>0 AND data#>>'{pr_create,completion}' IS DISTINCT FROM 'complete')))`, p.Repository, p.Issue, p.PRNumber).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return ErrAttemptRunsPending
	}
	return nil
}

// Causal publication records identify Factory-created work. An account may also
// author human PRs; its account identity alone cannot exclude those PRs.
func (s *Store) ExternalPullOriginAvailable(ctx context.Context, request factory.ExternalPullAdoption) error {
	if request.Validate() != nil {
		return ErrAdmissionChanged
	}
	return checkExternalPullOrigin(ctx, s.db, request, "")
}

func checkExternalPullOrigin(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, request factory.ExternalPullAdoption, owner string) error {
	var unsafe bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_publications WHERE repository=$1 AND assignment<>$4 AND
	 ((NOT (data ? 'external') AND ((data->>'pr_number')::bigint=$2 OR
	 (COALESCE((data#>>'{pr_create,attempts}')::int,0)>0 AND
	 (data#>>'{pr_create,completion}' IS DISTINCT FROM 'complete' OR COALESCE(data#>>'{pr_create,effect}','') NOT IN ('committed','not_committed') OR
	 (data#>>'{pr_create,effect}'='committed' AND COALESCE((data->>'pr_number')::bigint,0)=0)))))
	 OR (data ? 'external' AND (data#>>'{external,target,head_oid}'=$3 OR data->>'candidate'=$3
	 OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(data->'corrections','[]'::jsonb)) correction
	 WHERE correction->>'effect'='committed' AND correction#>>'{work,candidate}'=$3)
	 OR (data->>'pr_number')::bigint=$2 AND stage NOT IN ('fenced','withdrawn')))))
	 OR EXISTS(SELECT 1 FROM factory_assignments WHERE repository=$1 AND id<>$4 AND data ? 'external_planning'
	 AND (data#>>'{external_planning,request,target,head_oid}'=$3 OR (data#>>'{external_planning,request,pr_number}')::bigint=$2)
	 AND NOT EXISTS(SELECT 1 FROM factory_publications p WHERE p.assignment=factory_assignments.id AND p.stage IN ('fenced','withdrawn') AND p.data#>>'{external,target,head_oid}'<>$3))`, request.Repository, request.PRNumber, request.Target.HeadOID, owner).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return ErrAttemptRunsPending
	}
	return nil
}
