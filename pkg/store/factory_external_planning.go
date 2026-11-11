package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// Promotion retains the Architect's exact ready source, rather than retiring
// it before the publication can use its recipe. This transfers no new send
// authority: the recorded native run, preparation and original command must
// still describe the same source custody.
func checkExternalPlanningSourceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	var preparationData, runData []byte
	if err := tx.QueryRowContext(ctx, `SELECT p.data,r.data FROM project_preparations p JOIN factory_runs r ON r.id=$2 WHERE p.id=$1 FOR UPDATE OF p`, a.Preparation, a.Run).Scan(&preparationData, &runData); err != nil {
		return err
	}
	p, meta, err := unmarshalCandidatePreparation(preparationData)
	if err != nil || checkCoderPreparationBinding(p, meta, a) != nil || p.Native == nil || p.Native.Profile != a.ExternalPlanning.Origin.Profile ||
		p.Preparation.Requirements.ID != a.Authority.RequirementsID || p.Preparation.Approval.ID != a.Authority.ApprovalID ||
		(!p.State.Ready || p.State.Stopped) && !p.RetiredRecipeCandidate() {
		return ErrAdmissionChanged
	}
	if meta.Stopped && meta.Retirement != "confirmed" || meta.Retirement == "uncertain" {
		return ErrAttemptRunsPending
	}
	var run factory.Run
	if json.Unmarshal(runData, &run) != nil || run.Validate() != nil || run.Native == nil || !run.Native.Settled() || !run.Reconciled || run.ID != a.Run || run.ProjectID != a.ProjectID || run.InputSHA != a.SourceCommit || a.NativeTool == nil || run.Native.Tool != *a.NativeTool ||
		run.Native.Provenance.Profile != p.Native.Profile || run.Native.Provenance.InstanceID != p.Native.InstanceID || run.Native.Provenance.WorktreeID != p.Native.WorktreeID || run.Native.BundleSHA256 != p.State.SourceBundleSHA256 {
		return ErrAttemptRunsPending
	}
	return nil
}

func externalPlanningTarget(a factory.Assignment, now time.Time) factory.Publication {
	e := a.ExternalPlanning
	r := e.Request
	return factory.Publication{ID: a.ID, AssignmentID: a.ID, ProjectID: a.ProjectID, Role: project.RoleCoder, Authority: a.Authority, Acceptance: a.Acceptance, Preparation: a.Preparation, External: &e.Origin,
		Candidate: r.Target.HeadOID, BaseSHA: a.SourceCommit, TargetBranch: r.Target.BaseRef, PRNumber: r.PRNumber, PRID: r.PRID, Stage: factory.PublicationPublished, Repository: a.Repository, Issue: a.Issue,
		Publish: factory.PublicationOperation{Kind: factory.OpRefPublish}, PRCreate: factory.PublicationOperation{Kind: factory.OpPRCreate}, CreatedUnix: now.Unix(), FinishedUnix: now.Unix(), Outcome: factory.Succeeded, Reason: factory.PublishReasonLinked}
}

// Called inside the ordinary packet transaction, before assignment/reservation
// rows. The proposal is used only for common scope predicates; no publication
// exists until the real Architect has completed and its plan is admitted.
func recordExternalPlanningCommandTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, now time.Time) error {
	e := a.ExternalPlanning
	if e == nil {
		return nil
	}
	if a.Validate() != nil || a.Stage != factory.AssignmentAssigned || a.NativeTool == nil {
		return ErrAdmissionChanged
	}
	payload, err := json.Marshal(e.Request)
	if err != nil {
		return err
	}
	cmd := factory.Command{ID: a.ID, Type: factory.CommandAdoptExternalPull, Target: factory.ExternalPullTarget(a.Repository, e.Request.PRNumber), Principal: e.Principal, Payload: string(payload)}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	if cmd.Validate() != nil {
		return ErrCommandConflict
	}
	_, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return err
	}
	if !created {
		return ErrAssignmentActive
	}
	if err = checkExternalAdmissionTx(ctx, tx, e.Request, a, externalPlanningTarget(a, now), now, true); err != nil {
		return err
	}
	return finishFactoryCommandTx(ctx, tx, cmd.ID, now)
}

// RecordExternalPlanningPublication promotes only a real completed Architect
// with its paid completeness opinion and admitted plan. It reuses the original
// root allowance, source, policy and PR origin, including the planning charge.
func (s *Store) RecordExternalPlanningPublication(ctx context.Context, a factory.Assignment, p factory.Publication, now time.Time) (factory.Publication, error) {
	var empty factory.Publication
	if a.Validate() != nil || a.ExternalPlanning == nil || !a.Succeeded() || a.Result == nil || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Plan == nil || a.Result.PhaseResult.Output.PlanReview == "" || p.Validate() != nil || !reflect.DeepEqual(p, externalPlanningTarget(a, time.Unix(p.CreatedUnix, 0))) {
		return empty, ErrAdmissionChanged
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err = checkDispatchOpenTx(ctx, tx, a.Repository); err != nil {
		return empty, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, a.ID).Scan(&raw); err != nil {
		return empty, err
	}
	var current factory.Assignment
	if json.Unmarshal(raw, &current) != nil || !reflect.DeepEqual(current, a) {
		return empty, ErrAdmissionChanged
	}
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, a.ID).Scan(&existing)
	if err == nil {
		if json.Unmarshal(existing, &p) != nil {
			return empty, ErrAdmissionChanged
		}
		return p, p.Validate()
	}
	if err != sql.ErrNoRows {
		return empty, err
	}
	if err = checkExternalAdmissionScopeTx(ctx, tx, a.ExternalPlanning.Request, a, p, now, false); err != nil {
		return empty, err
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, a.Repository, a.Issue, a.ID)
	if err != nil || allowance.Closed || allowance.RemainingSeconds(now) <= 0 {
		return empty, factory.ErrAttemptClosed
	}
	if err = checkExternalPlanningSourceTx(ctx, tx, a); err != nil {
		return empty, err
	}
	settled, err := attemptRootCustodySettledTx(ctx, tx, a.Repository, a.Issue, a.ID, a.Preparation)
	if err != nil || !settled {
		return empty, ErrAttemptRunsPending
	}
	var planAdmitted bool
	// The canonical plan owner validates all bindings before this promotion.
	// Re-read its actual admitted head in the same transaction as publication.
	planID := a.Result.PhaseResult.Output.Plan.ID
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_heads h JOIN factory_plan_revisions p ON p.attempt_root=h.attempt_root AND p.plan_id=h.plan_id
	 WHERE h.attempt_root=$1 AND h.plan_id=$2 AND p.digest=$3)`, a.ID, planID, a.Result.PhaseResult.Output.Plan.Digest).Scan(&planAdmitted)
	if err != nil || !planAdmitted {
		return empty, ErrStalePlanBindings
	}
	pdata, err := json.Marshal(p)
	if err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,'',$4,0,$5)`, p.AssignmentID, p.Repository, p.Issue, p.Stage, string(pdata)); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return p, nil
}
