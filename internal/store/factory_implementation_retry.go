package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// Implementation continuation replaces only a confirmed unused context-held
// child. The completed Architect and its cumulative allowance remain owners.
type implementationRetryFacts struct {
	parent factory.Assignment
	prior  factory.Assignment
	run    factory.Run
	plan   factory.InvestigationPlan
}

func implementationRetryPayload(cmd factory.Command, decision factory.RetryDecision, repository, issue int64, acceptance string) error {
	if decision.Validate() != nil || cmd.Type != factory.CommandRetryImplementation || cmd.ID != decision.CommandID || cmd.Target != "run/"+decision.Prior || repository <= 0 || issue <= 0 || acceptance == "" || !decision.Queued {
		return ErrExplicitRetryIneligible
	}
	var payload struct {
		PriorRun     string `json:"prior_run"`
		PriorOutcome string `json:"prior_outcome"`
	}
	if json.Unmarshal([]byte(cmd.Payload), &payload) != nil || payload.PriorRun != decision.Prior || payload.PriorOutcome != string(factory.Cancelled) {
		return ErrExplicitRetryIneligible
	}
	return nil
}

func implementationRetryFactsTx(ctx context.Context, tx *sql.Tx, priorRun string, repository, issue int64, acceptance string, now time.Time) (implementationRetryFacts, error) {
	var facts implementationRetryFacts
	prior, err := loadRetryPriorAssignmentTx(ctx, tx, priorRun, repository, issue, acceptance, string(factory.Cancelled))
	if err != nil {
		return facts, err
	}
	if prior.Phase != project.ExecutionImplement || prior.Role != project.RoleCoder || prior.ParentAssignment == "" || len(prior.RunHistory) != 1 || prior.Attempts != 1 || prior.Reason != factory.AssignReasonCancelled {
		return facts, ErrExplicitRetryIneligible
	}
	parent, err := loadRetryOwnerTx(ctx, tx, prior, repository, issue, acceptance)
	if err != nil {
		return facts, err
	}
	if checkInvestigationParentComplete(parent, prior) != nil || checkImplementationLineage(parent, prior) != nil || prior.ParentAssignment != parent.ID {
		return facts, ErrExplicitRetryIneligible
	}
	var run factory.Run
	if err = contextJSONRow(ctx, tx, &run, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, priorRun); err != nil {
		return facts, err
	}
	if run.Validate() != nil || run.ID != prior.Run || run.ProjectID != prior.ProjectID || run.Role != prior.Role || run.InputSHA != prior.SourceCommit || !run.ContextPending || !run.Reconciled || run.Outcome != factory.Cancelled || run.Summary != "stop-before-start" || run.HasExecutionIdentity() || run.CredentialsReturned() || run.IdentityGeneration != 0 || run.FinalCheckpoint != nil {
		return facts, ErrExplicitRetryIneligible
	}
	var state string
	var raw []byte
	var observed bool
	if err = tx.QueryRowContext(ctx, `SELECT state,data,COALESCE((data->>'live_observed')::boolean,FALSE) FROM factory_mcp_capabilities WHERE assignment=$1 AND run=$2 FOR UPDATE`, prior.ID, run.ID).Scan(&state, &raw, &observed); err != nil {
		return facts, err
	}
	var cap factory.MCPCapability
	if json.Unmarshal(raw, &cap) != nil || cap.Validate() != nil || state != factory.MCPCapabilityStaged || observed || cap.AssignmentID != prior.ID || cap.RunID != run.ID || cap.Repository != repository || cap.Project != prior.ProjectID || cap.Profile != factory.MCPProfileBuilder {
		return facts, ErrExplicitRetryIneligible
	}
	settled, err := attemptRootRunsSettledTx(ctx, tx, repository, issue, parent.ID)
	if err != nil {
		return facts, err
	}
	if !settled {
		return facts, ErrExplicitRetryIneligible
	}
	if err = retryOperationsSettledTx(ctx, tx, parent); err != nil {
		return facts, err
	}
	if err = admitRetryReservationTx(ctx, tx, priorRun, prior, repository, run); err != nil {
		return facts, err
	}
	var unsettled bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_reservations r JOIN factory_assignments a ON a.id=r.assignment WHERE a.repository=$1 AND a.issue=$2 AND a.data->>'attempt_root'=$3 AND (r.state='held' OR (r.state='consumed' AND NOT EXISTS(SELECT 1 FROM factory_run_views v JOIN factory_usage u ON u.run=v.run WHERE v.attempt=a.id))))`, repository, issue, parent.ID).Scan(&unsettled); err != nil {
		return facts, err
	}
	if unsettled {
		return facts, ErrExplicitRetryIneligible
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, repository, issue, parent.ID)
	if err != nil {
		return facts, err
	}
	if allowance.Active || allowance.Closed || allowance.RemainingSeconds(now) <= 0 {
		return facts, ErrExplicitRetryIneligible
	}
	var plan factory.InvestigationPlan
	if err = contextJSONRow(ctx, tx, &plan, `SELECT r.data FROM factory_plan_heads h JOIN factory_plan_revisions r ON r.attempt_root=h.attempt_root AND r.plan_id=h.plan_id WHERE h.attempt_root=$1 FOR UPDATE OF h`, parent.ID); err != nil {
		return facts, err
	}
	if plan.Validate() != nil || plan.Executable() != nil || !reflect.DeepEqual(plan, *parent.Result.PhaseResult.Output.Plan) || plan.AttemptRoot != parent.ID || plan.SourceCommit != parent.SourceCommit || plan.Preparation != parent.Preparation {
		return facts, ErrExplicitRetryIneligible
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	var bound factory.InvestigationPlan
	if err != nil || json.Unmarshal(manifest.Plan, &bound) != nil || !reflect.DeepEqual(bound, plan) || manifest.Run != run.ID || manifest.Phase != project.ExecutionImplement {
		return facts, ErrExplicitRetryIneligible
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_plan_tasks WHERE attempt_root=$1 AND plan_id=$2) OR EXISTS(SELECT 1 FROM factory_feed_posts WHERE data->>'assignment'=$3 AND data ? 'work')`, parent.ID, plan.ID, prior.ID).Scan(&unsettled); err != nil {
		return facts, err
	}
	if unsettled {
		return facts, ErrExplicitRetryIneligible
	}
	return implementationRetryFacts{parent: parent, prior: prior, run: run, plan: plan}, nil
}

func (s *Store) RecordImplementationRetryCommand(ctx context.Context, cmd factory.Command, decision factory.RetryDecision, repository, issue int64, acceptance string, now time.Time) (factory.Command, bool, error) {
	if err := implementationRetryPayload(cmd, decision, repository, issue, acceptance); err != nil {
		return factory.Command{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Command{}, false, err
	}
	defer tx.Rollback()
	if _, _, _, err = ensureDispatchGateTx(ctx, tx, repository); err != nil {
		return factory.Command{}, false, err
	}
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return factory.Command{}, false, err
	}
	if !created {
		return stored, false, tx.Commit()
	}
	var collision bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE id=$1)`, cmd.ID).Scan(&collision); err != nil {
		return factory.Command{}, false, err
	}
	if collision {
		return factory.Command{}, false, ErrExplicitRetryIneligible
	}
	if _, err = implementationRetryFactsTx(ctx, tx, decision.Prior, repository, issue, acceptance, now); err != nil {
		return factory.Command{}, false, err
	}
	if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
		return factory.Command{}, false, err
	}
	stored.Finished = now.UTC().Format(time.RFC3339Nano)
	return stored, true, tx.Commit()
}

func implementationRetryCommandTx(ctx context.Context, tx *sql.Tx, decision factory.RetryDecision, repository, issue int64, acceptance string) error {
	var cmd factory.Command
	if err := tx.QueryRowContext(ctx, `SELECT id,type,target,principal,payload,digest,finished FROM factory_commands WHERE id=$1 FOR UPDATE`, decision.CommandID).Scan(&cmd.ID, &cmd.Type, &cmd.Target, &cmd.Principal, &cmd.Payload, &cmd.Digest, &cmd.Finished); err != nil {
		return err
	}
	if err := implementationRetryPayload(cmd, decision, repository, issue, acceptance); err != nil {
		return err
	}
	if cmd.Finished == "" {
		return ErrExplicitRetryIneligible
	}
	return nil
}

func (s *Store) PendingImplementationRetry(ctx context.Context, repository, issue int64, acceptance string) (factory.RetryDecision, error) {
	var decision factory.RetryDecision
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return decision, err
	}
	defer tx.Rollback()
	if _, _, _, err = ensureDispatchGateTx(ctx, tx, repository); err != nil {
		return decision, err
	}
	decision, err = pendingImplementationRetry(ctx, tx, repository, issue, acceptance)
	if err != nil {
		return decision, err
	}
	if err = implementationRetryCommandTx(ctx, tx, decision, repository, issue, acceptance); err != nil {
		return decision, err
	}
	if _, err = implementationRetryFactsTx(ctx, tx, decision.Prior, repository, issue, acceptance, time.Now()); err != nil {
		return decision, err
	}
	return decision, tx.Commit()
}

// The priority snapshot observes this command in its own transaction so an
// explicit continuation invalidates a previously recorded terminal wait.
func pendingImplementationRetry(ctx context.Context, q explicitRetryQuerier, repository, issue int64, acceptance string) (factory.RetryDecision, error) {
	var decision factory.RetryDecision
	var cmd factory.Command
	err := q.QueryRowContext(ctx, `SELECT c.id,c.type,c.payload FROM factory_commands c
	JOIN factory_runs r ON c.target='run/'||r.id
	JOIN factory_run_views v ON v.run=r.id
	JOIN factory_assignments a ON a.id=v.attempt
	JOIN factory_assignments owner ON owner.id=a.data->>'parent_assignment'
	JOIN factory_attempt_allowances l ON l.root_assignment=owner.id
	JOIN factory_plan_heads h ON h.attempt_root=owner.id
	JOIN factory_plan_revisions p ON p.attempt_root=h.attempt_root AND p.plan_id=h.plan_id
	JOIN issue_acceptance_heads accepted ON accepted.repository=a.repository AND accepted.issue=a.issue
	WHERE c.type=$1 AND c.finished<>'' AND a.repository=$2 AND a.issue=$3 AND a.data->>'acceptance'=$4 AND accepted.decision=$4
	AND a.stage='finished' AND a.data->>'phase'=$5 AND a.data->>'outcome'=$6 AND r.settled AND r.data->>'context_pending'='true'
	AND owner.stage='finished' AND owner.data->>'phase'=$7 AND owner.data->>'outcome'=$8
	AND a.data->>'attempt_root'=owner.id AND a.data->>'publication_assignment'=owner.id
	AND NOT (l.data->>'active')::boolean AND NOT (l.data->>'closed')::boolean
	AND p.plan_id=owner.data#>>'{result,phase_result,output,plan,id}'
	AND a.id=(SELECT id FROM factory_assignments WHERE repository=$2 AND issue=$3 ORDER BY seq DESC LIMIT 1)
	AND NOT EXISTS(SELECT 1 FROM factory_assignments consumed WHERE consumed.id=c.id)
	ORDER BY c.created,c.id LIMIT 1`, factory.CommandRetryImplementation, repository, issue, acceptance, project.ExecutionImplement, string(factory.Cancelled), project.ExecutionInvestigate, string(factory.Succeeded)).Scan(&cmd.ID, &cmd.Type, &cmd.Payload)
	if err != nil {
		return decision, err
	}
	return cmd.RetryDecision()
}

func (s *Store) RecordImplementationRetryPacket(ctx context.Context, decision factory.RetryDecision, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	if a.ID != decision.CommandID {
		return ErrExplicitRetryIneligible
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = admitRetryGateTx(ctx, tx, a); err != nil {
		return err
	}
	if err = implementationRetryCommandTx(ctx, tx, decision, a.Repository, a.Issue, a.Acceptance); err != nil {
		return err
	}
	facts, err := implementationRetryFactsTx(ctx, tx, decision.Prior, a.Repository, a.Issue, a.Acceptance, time.Now())
	if err != nil {
		return err
	}
	if err = checkImplementationRetryPacket(facts, a, run); err != nil {
		return err
	}
	if err = checkDispatchOpenTx(ctx, tx, a.Repository); err != nil {
		return err
	}
	if err = recordDispatchPacketTx(ctx, tx, expected, a, r, run, view, false); err != nil {
		return err
	}
	return tx.Commit()
}

func checkImplementationRetryPacket(facts implementationRetryFacts, a factory.Assignment, run factory.Run) error {
	if checkImplementationLineage(facts.parent, a) != nil || checkInvestigationParentComplete(facts.parent, a) != nil || a.Connection != facts.prior.Connection || a.ActorID != facts.prior.ActorID || !run.ContextPending {
		return ErrExplicitRetryIneligible
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	var plan factory.InvestigationPlan
	if err != nil || json.Unmarshal(manifest.Plan, &plan) != nil || !reflect.DeepEqual(plan, facts.plan) || manifest.Run != run.ID || manifest.Phase != a.Phase {
		return ErrExplicitRetryIneligible
	}
	return nil
}

func validateImplementationRetryChildTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	var cmd factory.Command
	if err := tx.QueryRowContext(ctx, `SELECT id,type,payload FROM factory_commands WHERE id=$1 AND type=$2 AND finished<>'' FOR UPDATE`, a.ID, factory.CommandRetryImplementation).Scan(&cmd.ID, &cmd.Type, &cmd.Payload); err != nil {
		return err
	}
	decision, err := cmd.RetryDecision()
	if err != nil {
		return err
	}
	if err := implementationRetryCommandTx(ctx, tx, decision, a.Repository, a.Issue, a.Acceptance); err != nil {
		return err
	}
	// Once admitted, the existing bounded launch-retry path validates this
	// exact assignment. Its root is active and its prior is historical now;
	// the immutable owner admission must not be treated as a new command.
	var stored factory.Assignment
	err = contextJSONRow(ctx, tx, &stored, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, a.ID)
	if err == nil {
		if !reflect.DeepEqual(stored, a) {
			return ErrExplicitRetryIneligible
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	facts, err := implementationRetryFactsTx(ctx, tx, decision.Prior, a.Repository, a.Issue, a.Acceptance, time.Now())
	if err != nil {
		return err
	}
	if checkImplementationLineage(facts.parent, a) != nil || a.Connection != facts.prior.Connection || a.ActorID != facts.prior.ActorID {
		return ErrExplicitRetryIneligible
	}
	return nil
}
