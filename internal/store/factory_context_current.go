package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// FactoryCurrentContext reads one coherent current snapshot. Every join starts
// with the recorded capability and assignment; no repository inventory, parent
// prompt or historical sibling becomes readable through this method.
func (s *Store) FactoryCurrentContext(ctx context.Context, cap factory.MCPCapability, now time.Time) (factory.CurrentContext, error) {
	var out factory.CurrentContext
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = factoryCurrentContextTx(ctx, tx, cap, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func factoryCurrentContextTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, now time.Time) (factory.CurrentContext, error) {
	var out factory.CurrentContext
	var recorded factory.MCPCapability
	var assignment factory.Assignment
	var capabilityJSON []byte
	var capabilityState string
	err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1`, cap.ID).Scan(&capabilityState, &capabilityJSON)
	if err != nil || json.Unmarshal(capabilityJSON, &recorded) != nil {
		return out, errors.New("current context capability is unavailable")
	}
	recorded.State = capabilityState
	if recorded.ScopeDigest() != cap.ScopeDigest() || recorded.State != cap.State || recorded.Expired(now) {
		return out, errors.New("current context capability is unavailable")
	}
	if err = contextJSONRow(ctx, tx, &assignment, `SELECT data FROM factory_assignments WHERE id=$1`, cap.AssignmentID); err != nil {
		return out, err
	}
	if assignment.ID != cap.AssignmentID || assignment.Run != cap.RunID || assignment.Repository != cap.Repository || assignment.ProjectID != cap.Project || (assignment.SourceCommit != "" && assignment.SourceCommit != cap.SourceDigest) || strconv.FormatInt(assignment.NativeRev, 10) != cap.AcceptedRevision || assignment.Subject.SubjectKey(assignment.Repository) != cap.Subject {
		return out, errors.New("current context assignment differs from capability")
	}
	out = factory.CurrentContext{
		Version:  "context-current-v1",
		Binding:  factory.CurrentContextBinding{RunID: cap.RunID, Project: cap.Project, PreparationDigest: cap.PreparationDigest, AcceptedRevision: cap.AcceptedRevision, AttemptRoot: assignment.AttemptRoot, ParentAssignment: assignment.ParentAssignment, PublicationAssignment: assignment.PublicationAssignment, AssignmentRevision: assignment.Revision, Stage: assignment.Stage},
		Intent:   factory.CurrentContextIntent{AcceptanceID: assignment.Acceptance, ContextDigest: cap.SourceDigest, ObjectiveRef: cap.Subject},
		Evidence: factory.CurrentContextEvidence{RequiredStreams: []string{"required_current"}, Availability: "incomplete", MissingReasons: []string{}},
		Role:     factory.CurrentRoleContext{Kind: cap.Profile},
	}
	var run factory.Run
	var raw []byte
	var active, settled bool
	err = tx.QueryRowContext(ctx, `SELECT data,active,settled FROM factory_runs WHERE id=$1`, cap.RunID).Scan(&raw, &active, &settled)
	if errors.Is(err, sql.ErrNoRows) {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "bound_run_unavailable")
	} else if err != nil {
		return out, err
	} else {
		if json.Unmarshal(raw, &run) != nil || run.ID != cap.RunID || run.ProjectID != cap.Project || run.InputSHA != cap.SourceDigest {
			return out, errors.New("current context run differs from capability")
		}
		out.Run = &factory.CurrentContextRun{ID: run.ID, Outcome: run.Outcome, Reconciled: run.Reconciled, Active: active, Settled: settled, ObservedState: "not_wired"}
	}
	if err = currentAllowanceTx(ctx, tx, assignment, cap, now, &out); err != nil {
		return out, err
	}
	if assignment.Acceptance != "" && assignment.Issue > 0 {
		if err = currentAcceptanceTx(ctx, tx, assignment, &out); err != nil {
			return out, err
		}
	}
	switch cap.Profile {
	case factory.MCPProfileBuilder, factory.MCPProfileFixer:
		err = currentPlanTx(ctx, tx, assignment, run, &out)
		if err == nil && cap.Profile == factory.MCPProfileFixer {
			err = currentReviewTx(ctx, tx, assignment, &out)
		}
	case factory.MCPProfileInspector:
		err = currentReviewTx(ctx, tx, assignment, &out)
	case factory.MCPProfileArchitect:
		if out.Role.Accepted == nil || out.Role.Accepted.RepositoryContext == nil {
			out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "accepted_repository_context_unavailable")
		}
	case factory.MCPProfileProductLead, factory.MCPProfileIssueMaker:
		err = currentProductTx(ctx, tx, assignment, &out)
	case factory.MCPProfileStrategist:
		err = currentStrategyTx(ctx, tx, assignment, &out)
	case factory.MCPProfileTestPilot:
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "current_native_verification_unavailable")
		if assignment.Subject.Delivery == nil {
			out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "delivery_scope_unavailable")
		} else {
			revision, _, inputErr := applicationVerificationInputsTx(ctx, tx, assignment)
			if inputErr != nil {
				out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "accepted_delivery_inputs_unavailable")
			} else {
				out.Role.Delivery = assignment.Subject.Delivery
				setCurrentProduct(revision, &out)
			}
		}
	default:
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "required_role_inputs_unavailable")
	}
	if err != nil {
		return out, err
	}
	if len(out.Evidence.MissingReasons) == 0 {
		out.Evidence.Availability = "complete"
	}
	return out, nil
}

func contextJSONRow(ctx context.Context, tx *sql.Tx, out any, query string, args ...any) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func currentAllowanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, cap factory.MCPCapability, now time.Time, out *factory.CurrentContext) error {
	var allowance factory.AttemptAllowance
	err := contextJSONRow(ctx, tx, &allowance, `SELECT data FROM factory_attempt_allowances WHERE repository=$1 AND root_assignment=$2`, cap.Repository, a.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "bound_allowance_unavailable")
		return nil
	}
	if err != nil {
		return err
	}
	if allowance.Repository != cap.Repository || allowance.RootAssignment != a.AttemptRoot || allowance.Issue != a.Issue {
		return errors.New("current context allowance differs from assignment")
	}
	remaining := max(int64(0), cap.DeadlineUnix-now.Unix())
	out.Allowance = &factory.CurrentContextAllowance{Revision: allowance.Revision, DeadlineRemainingSeconds: remaining, ActiveSecondsRemaining: allowance.RemainingSeconds(now), MaxChildren: allowance.Limits.MaxChildren, Closed: allowance.Closed}
	if allowance.Issue > 0 {
		corrections := max(0, allowance.Limits.CorrectionCycles-len(allowance.Corrections))
		reviews := max(0, allowance.Limits.ReviewRounds-len(allowance.Reviews))
		out.Allowance.CorrectionCyclesRemaining, out.Allowance.ReviewRoundsRemaining = &corrections, &reviews
	}
	return nil
}

func currentAcceptanceTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, out *factory.CurrentContext) error {
	var accepted factory.Acceptance
	err := contextJSONRow(ctx, tx, &accepted, `SELECT data FROM issue_acceptance_decisions WHERE id=$1 AND repository=$2 AND issue=$3`, a.Acceptance, a.Repository, a.Issue)
	if errors.Is(err, sql.ErrNoRows) {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "accepted_scope_unavailable")
		return nil
	}
	if err != nil {
		return err
	}
	if accepted.ID != a.Acceptance || accepted.Repository != a.Repository || accepted.IssueIndex != strconv.FormatInt(a.Issue, 10) {
		return errors.New("current accepted scope differs from assignment")
	}
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 AND decision=$3) AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3)`, a.Repository, a.Issue, a.Acceptance).Scan(&current); err != nil {
		return err
	}
	if !current {
		return errors.New("accepted context is withdrawn or superseded")
	}
	if a.AcceptedContext != nil {
		out.Role.Accepted = a.AcceptedContext
	}
	out.Role.Acceptance = &factory.CurrentAcceptanceContext{ID: accepted.ID, NativeRevision: accepted.NativeRev, TitleDigest: accepted.TitleDigest, ContentDigest: accepted.ContentDigest, Sources: accepted.Sources, Prerequisites: accepted.Prerequisites, Resolutions: accepted.Resolutions}
	if out.Role.Accepted == nil {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "accepted_objective_text_unavailable")
	}
	return nil
}

func currentProductTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, out *factory.CurrentContext) error {
	if a.Subject.Intent == nil || a.Subject.Intent.OwnerID != a.ActorID || a.ActorID <= 0 {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "accepted_intent_unavailable")
		return nil
	}
	bound := a.Subject.Intent
	var revision factory.IntentRevision
	err := contextJSONRow(ctx, tx, &revision, `SELECT r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision WHERE h.id=$1 AND h.owner_id=$2 AND h.revision=$3`, bound.IntentID, bound.OwnerID, bound.RevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "accepted_intent_unavailable")
		return nil
	}
	if err != nil {
		return err
	}
	if revision.ID != bound.RevisionID || revision.Intent != bound.IntentID || revision.Owner != a.ActorID || revision.Repository != a.Repository || revision.Digest() != bound.ScopeDigest {
		return errors.New("current intent differs from admitted subject")
	}
	setCurrentProduct(revision, out)
	if a.PlanningContext == nil {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "admitted_planning_inputs_unavailable")
	} else {
		out.Role.Planning = a.PlanningContext.CurrentContext()
	}
	return nil
}

func setCurrentProduct(revision factory.IntentRevision, out *factory.CurrentContext) {
	ideas := make([]factory.CurrentProductIdea, 0, len(revision.Ideas))
	for _, idea := range revision.Ideas {
		ideas = append(ideas, factory.CurrentProductIdea{ID: idea.ID, Description: idea.Description, Consequence: idea.Consequence, Assumption: idea.Assumption, Decision: idea.Decision, Correction: idea.Correction, Source: idea.Source, Provenance: idea.Provenance, Evidence: idea.Evidence, Material: idea.Material})
	}
	resolved := factory.ResolvedScope(revision)
	out.Role.Product = &factory.CurrentProductContext{Intent: revision.Intent, AcceptedRevision: revision.ID, ScopeDigest: revision.Digest(), Objective: revision.Objective, Approach: revision.Approach, Ideas: ideas, Tasks: revision.Tasks, EffectiveOutcomes: append([]string(nil), resolved...)}
	count := len(resolved)
	out.Intent.OutcomeCount = &count
	out.Intent.ContextDigest = revision.Digest()
}

func currentPlanTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, run factory.Run, out *factory.CurrentContext) error {
	if len(run.ExecutionManifest) == 0 {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "execution_manifest_unavailable")
		return nil
	}
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	if err != nil {
		return err
	}
	var plan factory.InvestigationPlan
	if json.Unmarshal(manifest.Plan, &plan) != nil || manifest.Run != run.ID || manifest.Phase != a.Phase || plan.AttemptRoot != a.AttemptRoot || plan.Preparation != a.Preparation {
		return errors.New("current plan differs from assigned manifest")
	}
	projected := &factory.CurrentPlanContext{AttemptRoot: plan.AttemptRoot, PlanID: plan.ID, Generation: plan.Generation, Digest: plan.Digest, InputDigest: plan.InputDigest, ScopeDigest: plan.ScopeDigest, SourceCommit: plan.SourceCommit, IntegrationCommit: plan.IntegrationCommit, Preparation: plan.Preparation, ManifestDigest: project.ExecutionManifestDigest(run.ExecutionManifest), Tasks: plan.Tasks, Checks: plan.Checks, Receipts: []factory.CurrentTaskReceipt{}}
	if checkpoint := run.FinalCheckpoint; checkpoint != nil {
		if checkpoint.Request.Project != a.ProjectID || run.CheckpointBinding(checkpoint.Request) != nil || checkpoint.ValidateFor(checkpoint.Request, true) != nil {
			return errors.New("current integration checkpoint differs from assigned manifest")
		}
		projected.FinalCheckpoint = checkpoint
		if checkpoint.Status == "committed" {
			projected.VerifiedIntegrationCommit = &checkpoint.Candidate
		}
	}
	var head factory.CurrentPlanHead
	err = tx.QueryRowContext(ctx, `SELECT plan_id,generation FROM factory_plan_heads WHERE attempt_root=$1`, plan.AttemptRoot).Scan(&head.PlanID, &head.Generation)
	if err == nil {
		projected.CurrentPlanHead = &head
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	} else {
		out.Evidence.MissingReasons = append(out.Evidence.MissingReasons, "current_plan_head_unavailable")
	}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_plan_tasks WHERE attempt_root=$1 AND plan_id=$2 ORDER BY task_id LIMIT $3`, plan.AttemptRoot, plan.ID, factory.MaxPlanTasks+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	known := map[string]bool{}
	for _, task := range plan.Tasks {
		known[task.ID] = true
	}
	for rows.Next() {
		var raw []byte
		var receipt PlanTaskReceipt
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt.AttemptRoot != plan.AttemptRoot || receipt.PlanID != plan.ID || !known[receipt.TaskID] || len(projected.Receipts) >= factory.MaxPlanTasks {
			return errors.New("current task receipt differs from bound plan")
		}
		projected.Receipts = append(projected.Receipts, factory.CurrentTaskReceipt{AttemptRoot: receipt.AttemptRoot, PlanID: receipt.PlanID, TaskID: receipt.TaskID, Revision: receipt.Revision, State: receipt.State, Child: receipt.Child, InputDigest: receipt.InputDigest, OutputDigest: receipt.OutputDigest, Tree: receipt.Tree, Commit: receipt.Commit, Checks: receipt.Checks, Reason: receipt.Reason})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	out.Role.Plan = projected
	return nil
}
