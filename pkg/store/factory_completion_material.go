package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// completionMaterialRowLimit reads one row past the per-section safety
// cap (MaxReassessmentIssues) so excess refuses instead of returning a
// truncated prefix. No section ever returns partial data.
const completionMaterialRowLimit = factory.MaxReassessmentIssues + 1

func completionMaterialLimit() string {
	return strconv.Itoa(completionMaterialRowLimit)
}

// completionMaterialTx captures the canonical model-safe local context
// for one repository inside the caller's LevelRepeatableRead capture
// transaction (the SAME tx CaptureCompletionLocal holds). Every query is
// scoped to the repository or to exact pinned references; another
// repository's bodies are never read. Only observation timestamps,
// global NativeRev refresh and other explicitly nonsemantic observation
// fields are normalized before hashing; material revisions, state, IDs,
// digests and outcomes are retained. An unavailable required join or an
// invalid row is a named capture error, never a fabricated empty
// section.
func completionMaterialTx(ctx context.Context, tx *sql.Tx, repository int64) (*factory.CompletionMaterial, error) {
	if repository <= 0 {
		return nil, errors.New("completion material lacks its repository")
	}
	projectID, err := recordedExecutionWorkspaceTx(ctx, tx, repository)
	if err != nil {
		return nil, errors.New("completion material lacks its project")
	}
	needed := make(map[string]bool)
	intents, intentAcceptances, err := completionIntentScopesTx(ctx, tx, repository)
	if err != nil {
		return nil, err
	}
	for id := range intentAcceptances {
		needed[id] = true
	}
	assignments, assignmentAcceptances, runViews, err := completionAssignmentsTx(ctx, tx, repository, projectID)
	if err != nil {
		return nil, err
	}
	for id := range assignmentAcceptances {
		needed[id] = true
	}
	publications, publicationAcceptances, err := completionPublicationsTx(ctx, tx, repository, assignments, runViews)
	if err != nil {
		return nil, err
	}
	for id := range publicationAcceptances {
		needed[id] = true
	}
	merges, mergeAcceptances, err := completionMergesTx(ctx, tx, repository, assignments, publications)
	if err != nil {
		return nil, err
	}
	for id := range mergeAcceptances {
		needed[id] = true
	}
	withdrawals, withdrawalAcceptances, err := completionWithdrawalsTx(ctx, tx, repository)
	if err != nil {
		return nil, err
	}
	for id := range withdrawalAcceptances {
		needed[id] = true
	}
	bodies, err := completionAcceptanceBodiesTx(ctx, tx, repository, needed)
	if err != nil {
		return nil, err
	}
	controls, err := completionControlsTx(ctx, tx, repository)
	if err != nil {
		return nil, err
	}
	preparations, err := completionPreparationsTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	plans, planHeads, planTasks, err := completionPlansTx(ctx, tx, assignments)
	if err != nil {
		return nil, err
	}
	checks, err := completionChecksTx(ctx, tx, repository)
	if err != nil {
		return nil, err
	}
	authority, err := completionAuthorityTx(ctx, tx, repository, projectID)
	if err != nil {
		return nil, err
	}
	material := &factory.CompletionMaterial{
		Intents:      intents,
		Acceptances:  bodies,
		Withdrawals:  withdrawals,
		Controls:     controls,
		Preparations: preparations,
		Plans:        plans,
		PlanHeads:    planHeads,
		PlanTasks:    planTasks,
		Assignments:  assignments,
		Publications: publications,
		Merges:       merges,
		Checks:       checks,
		Authority:    authority,
		Repository:   repository,
	}
	if err := material.Validate(); err != nil {
		return nil, err
	}
	return material, nil
}

// completionIntentScopesTx projects every admitted historical accepted
// revision: for each intent with accepted scope in this repository, the
// head revision is followed through the immutable predecessor chain and
// every accepted revision on it is exported with its generation and task
// bindings. LatestAccepted marks the newest accepted revision from the
// head. Draft successors contribute head-state metadata only, never
// their IDs or content.
func completionIntentScopesTx(ctx context.Context, tx *sql.Tx, repository int64) ([]factory.CompletionIntentScope, map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, intent, data FROM factory_intent_revisions
		WHERE data->>'repository'=$1 ORDER BY intent, id LIMIT `+completionMaterialLimit(),
		strconv.FormatInt(repository, 10))
	if err != nil {
		return nil, nil, err
	}
	type revisionRow struct {
		id     string
		intent string
		data   []byte
	}
	var all []revisionRow
	for rows.Next() {
		var row revisionRow
		if err = rows.Scan(&row.id, &row.intent, &row.data); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		all = append(all, row)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()
	if len(all) > factory.MaxReassessmentIssues {
		return nil, nil, errors.New("completion material intents exceed their bound")
	}
	byIntent := make(map[string][]revisionRow)
	var order []string
	for _, row := range all {
		if _, ok := byIntent[row.intent]; !ok {
			order = append(order, row.intent)
		}
		byIntent[row.intent] = append(byIntent[row.intent], row)
	}
	scopes := []factory.CompletionIntentScope{}
	referenced := make(map[string]bool)
	for _, intent := range order {
		var head string
		if err = tx.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1`, intent).Scan(&head); err != nil {
			return nil, nil, errors.New("completion material lacks its intent head")
		}
		if head == "" {
			return nil, nil, errors.New("completion material lacks its intent head")
		}
		chain, err := completionIntentChainTx(ctx, tx, intent, head, repository)
		if err != nil {
			return nil, nil, err
		}
		var headState string
		if len(chain) > 0 {
			headState = chain[0].State
		}
		latestMarked := false
		for _, revision := range chain {
			if revision.State != "accepted" {
				continue
			}
			latest := !latestMarked
			latestMarked = true
			scope, err := projectCompletionIntentScopeTx(ctx, tx, revision, headState, latest)
			if err != nil {
				return nil, nil, err
			}
			scopes = append(scopes, scope)
			for _, binding := range scope.Bindings {
				referenced[binding.AcceptanceID] = true
			}
		}
	}
	// LatestAccepted and head state stay chain-determined above; the
	// final projection sorts by (IntentID, RevisionID) for canonical
	// encoding, since accepted revision IDs are random and chain order
	// is newest-first.
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].IntentID != scopes[j].IntentID {
			return scopes[i].IntentID < scopes[j].IntentID
		}
		return scopes[i].RevisionID < scopes[j].RevisionID
	})
	return scopes, referenced, nil
}

// completionIntentChainTx follows the immutable predecessor chain from
// the head revision, verifying every link belongs to the same intent.
// Revisions outside the chain (none: predecessors are single-linked)
// never enter; a broken link or cycle fails closed.
func completionIntentChainTx(ctx context.Context, tx *sql.Tx, intent, head string, repository int64) ([]factory.IntentRevision, error) {
	var chain []factory.IntentRevision
	seen := make(map[string]bool)
	current := head
	for current != "" {
		if seen[current] {
			return nil, errors.New("completion material intent history repeats its revision")
		}
		if len(chain) > factory.MaxReassessmentIssues {
			return nil, errors.New("completion material intent history exceeds its bound")
		}
		seen[current] = true
		var owner string
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT intent, data FROM factory_intent_revisions WHERE id=$1`, current).Scan(&owner, &raw); err != nil {
			return nil, errors.New("completion material lacks its intent revision")
		}
		if owner != intent {
			return nil, errors.New("completion material intent history names another intent")
		}
		var revision factory.IntentRevision
		if err := json.Unmarshal(raw, &revision); err != nil {
			return nil, errors.New("completion material intent revision is malformed")
		}
		if err := revision.Validate(); err != nil {
			return nil, errors.New("completion material intent revision is invalid")
		}
		if revision.Repository != repository {
			return nil, errors.New("completion material intent history names another repository")
		}
		chain = append(chain, revision)
		current = revision.Predecessor
	}
	if len(chain) == 0 || chain[0].ID != head {
		return nil, errors.New("completion material lacks its intent head revision")
	}
	return chain, nil
}

func projectCompletionIntentScopeTx(ctx context.Context, tx *sql.Tx, revision factory.IntentRevision, headState string, latest bool) (factory.CompletionIntentScope, error) {
	var generation *factory.IntentGeneration
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_generations WHERE revision=$1`, revision.ID).Scan(&raw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return factory.CompletionIntentScope{}, err
	} else if err == nil {
		var stored factory.IntentGeneration
		if err = json.Unmarshal(raw, &stored); err != nil {
			return factory.CompletionIntentScope{}, errors.New("completion material generation is malformed")
		}
		if err = checkCompletionGeneration(stored, revision); err != nil {
			return factory.CompletionIntentScope{}, err
		}
		generation = &stored
	}
	bindings, err := completionTaskBindingsTx(ctx, tx, revision.ID)
	if err != nil {
		return factory.CompletionIntentScope{}, err
	}
	return factory.NewCompletionIntentScope(revision, headState, latest, generation, bindings)
}

func checkCompletionGeneration(generation factory.IntentGeneration, revision factory.IntentRevision) error {
	if generation.RevisionID != revision.ID || generation.IntentID != revision.Intent {
		return errors.New("completion material generation names another revision")
	}
	if (generation.OperationID == "" && !(generation.LocalOnly && generation.State == "completed" && factory.ValidID(generation.ReassessmentCycle))) || generation.State == "" || len(generation.State) > 64 {
		return errors.New("completion material generation lacks its identity")
	}
	if len(generation.SubmittedTasks) == 0 {
		return errors.New("completion material generation lacks its submitted tasks")
	}
	submitted := make(map[string]bool, len(generation.SubmittedTasks))
	for _, task := range generation.SubmittedTasks {
		if !project.ValidDecisionID(task.Key) || submitted[task.Key] {
			return errors.New("completion material generation tasks are not unique")
		}
		submitted[task.Key] = true
	}
	for key, ref := range generation.Delivered {
		if !submitted[key] {
			return errors.New("completion material delivered names an unsubmitted task")
		}
		if ref.IssueID <= 0 || ref.IssueIndex <= 0 || ref.ContentVersion < 0 {
			return errors.New("completion material delivered ref is invalid")
		}
	}
	return nil
}

func completionTaskBindingsTx(ctx context.Context, tx *sql.Tx, revision string) ([]factory.IntentTaskBinding, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_intent_tasks WHERE revision=$1 ORDER BY task LIMIT `+completionMaterialLimit(), revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	bindings := []factory.IntentTaskBinding{}
	for rows.Next() {
		var raw []byte
		var binding factory.IntentTaskBinding
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &binding); err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material task binding is malformed")
		}
		if binding.RevisionID != revision || !project.ValidDecisionID(binding.TaskKey) ||
			binding.IssueID <= 0 || binding.IssueIndex <= 0 || binding.ContentVersion < 0 ||
			!project.ValidDecisionID(binding.AcceptanceID) || binding.CreatedUnix < 0 {
			_ = rows.Close()
			return nil, errors.New("completion material task binding is invalid")
		}
		bindings = append(bindings, binding)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(bindings) > factory.MaxReassessmentIssues {
		return nil, errors.New("completion material task bindings exceed their bound")
	}
	return bindings, nil
}

// completionAssignmentsTx projects repository-owned issue/intent work
// with exact run, run-view, project and root joins. Completion-subject
// workflow roots (including verification and its repair) are excluded. A missing run or run-view row fails
// closed: settlement is never inferred. Assignment.Prompt,
// account/lease/binding/credential data and run output never enter.
func completionAssignmentsTx(ctx context.Context, tx *sql.Tx, repository int64, projectID string) ([]factory.CompletionAssignment, map[string]bool, map[string]bool, error) {
	all, err := loadCompletionAssignmentRowsTx(ctx, tx, repository)
	if err != nil {
		return nil, nil, nil, err
	}
	assignments := []factory.CompletionAssignment{}
	referenced := make(map[string]bool)
	runs := make(map[string]bool)
	runCache := make(map[string]factory.CompletionRunState)
	for _, row := range all {
		var stored factory.Assignment
		if err = json.Unmarshal(row.data, &stored); err != nil {
			return nil, nil, nil, errors.New("completion material assignment is malformed")
		}
		if err = stored.Validate(); err != nil {
			return nil, nil, nil, errors.New("completion material assignment is invalid")
		}
		if stored.Subject.Kind == factory.SubjectCompletion || stored.Subject.Kind == factory.SubjectStall || stored.Subject.Kind == factory.SubjectDelivery || stored.Subject.Kind == factory.SubjectDeliveryRepair {
			continue
		}
		if stored.ProjectID != projectID {
			return nil, nil, nil, errors.New("completion material assignment names another project")
		}
		projected, err := projectCompletionAssignmentTx(ctx, tx, runCache, repository, stored, runs)
		if err != nil {
			return nil, nil, nil, err
		}
		assignments = append(assignments, projected)
		if stored.Acceptance != "" {
			referenced[stored.Acceptance] = true
		}
	}
	ids := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		ids[assignment.ID] = true
	}
	for _, assignment := range assignments {
		if !ids[assignment.AttemptRoot] {
			return nil, nil, nil, errors.New("completion material assignment lacks its attempt root")
		}
	}
	return assignments, referenced, runs, nil
}

type completionAssignmentRow struct {
	id   string
	data []byte
}

func loadCompletionAssignmentRowsTx(ctx context.Context, tx *sql.Tx, repository int64) ([]completionAssignmentRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM factory_assignments WHERE repository=$1 ORDER BY id LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, err
	}
	var all []completionAssignmentRow
	for rows.Next() {
		var row completionAssignmentRow
		if err = rows.Scan(&row.id, &row.data); err != nil {
			_ = rows.Close()
			return nil, err
		}
		all = append(all, row)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(all) > factory.MaxReassessmentIssues {
		return nil, errors.New("completion material assignments exceed their bound")
	}
	return all, nil
}

func resolveCompletionRunStateTx(ctx context.Context, tx *sql.Tx, runCache map[string]factory.CompletionRunState, runID string) (factory.CompletionRunState, error) {
	if state, ok := runCache[runID]; ok {
		return state, nil
	}
	var active, settled bool
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT active, settled, data FROM factory_runs WHERE id=$1`, runID).Scan(&active, &settled, &raw); err != nil {
		return factory.CompletionRunState{}, errors.New("completion material lacks its run")
	}
	var run factory.Run
	if err := json.Unmarshal(raw, &run); err != nil {
		return factory.CompletionRunState{}, errors.New("completion material run is malformed")
	}
	if err := run.Validate(); err != nil {
		return factory.CompletionRunState{}, errors.New("completion material run is invalid")
	}
	state := factory.CompletionRunState{
		ID: run.ID, Outcome: run.Outcome, Reconciled: run.Reconciled,
		Active: active, Settled: settled,
	}
	if err := state.Validate(); err != nil {
		return factory.CompletionRunState{}, errors.New("completion material run is invalid")
	}
	runCache[runID] = state
	return state, nil
}

func projectCompletionAssignmentTx(ctx context.Context, tx *sql.Tx, runCache map[string]factory.CompletionRunState, repository int64, stored factory.Assignment, runs map[string]bool) (factory.CompletionAssignment, error) {
	projected := factory.CompletionAssignment{
		RunHistory:   make([]factory.CompletionRunState, 0, len(stored.RunHistory)),
		Subject:      stored.Subject,
		ID:           stored.ID,
		AttemptRoot:  stored.AttemptRoot,
		ProjectID:    stored.ProjectID,
		Role:         stored.Role,
		Phase:        stored.Phase,
		Stage:        stored.Stage,
		Outcome:      stored.Outcome,
		Reason:       stored.Reason,
		Acceptance:   stored.Acceptance,
		Preparation:  stored.Preparation,
		SourceCommit: stored.SourceCommit,
		Issue:        stored.Issue,
		Revision:     stored.Revision,
	}
	for _, runID := range stored.RunHistory {
		state, err := resolveCompletionRunStateTx(ctx, tx, runCache, runID)
		if err != nil {
			return factory.CompletionAssignment{}, err
		}
		projected.RunHistory = append(projected.RunHistory, state)
		runs[runID] = true
	}
	var viewRepository, viewIssue int64
	var viewAttempt string
	if err := tx.QueryRowContext(ctx, `SELECT repository, issue, attempt FROM factory_run_views WHERE run=$1`, stored.Run).Scan(&viewRepository, &viewIssue, &viewAttempt); err != nil {
		return factory.CompletionAssignment{}, errors.New("completion material lacks its run view")
	}
	if viewRepository != repository || viewIssue != stored.Issue {
		return factory.CompletionAssignment{}, errors.New("completion material run view names another scope")
	}
	var reservationRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_reservations WHERE assignment=$1`, stored.ID).Scan(&reservationRaw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return factory.CompletionAssignment{}, err
	} else if err == nil {
		var reservation factory.Reservation
		if err = json.Unmarshal(reservationRaw, &reservation); err != nil {
			return factory.CompletionAssignment{}, errors.New("completion material reservation is malformed")
		}
		if err = reservation.Validate(); err != nil {
			return factory.CompletionAssignment{}, errors.New("completion material reservation is invalid")
		}
		if reservation.Repository != repository || reservation.AssignmentID != stored.ID {
			return factory.CompletionAssignment{}, errors.New("completion material reservation names another scope")
		}
		projected.Reservation = &factory.CompletionReservation{
			Connection: reservation.Connection, State: reservation.State,
			PlannedMinutes: reservation.PlannedMinutes, Revision: reservation.Revision,
		}
	}
	if err := projected.Validate(); err != nil {
		return factory.CompletionAssignment{}, err
	}
	return projected, nil
}

// completionPublicationsTx projects repository publications with exact
// ownership and run joins. The canonical record validates before
// projection; raw operation/authorization payloads never enter.
func completionPublicationsTx(ctx context.Context, tx *sql.Tx, repository int64, assignments []factory.CompletionAssignment, runs map[string]bool) ([]factory.CompletionPublication, map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_publications WHERE repository=$1 ORDER BY assignment LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	owned := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		owned[assignment.ID] = true
	}
	publications := []factory.CompletionPublication{}
	referenced := make(map[string]bool)
	for rows.Next() {
		var raw []byte
		var stored factory.Publication
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			_ = rows.Close()
			return nil, nil, errors.New("completion material publication is malformed")
		}
		if err = stored.Validate(); err != nil {
			_ = rows.Close()
			return nil, nil, errors.New("completion material publication is invalid")
		}
		if !owned[stored.AssignmentID] {
			_ = rows.Close()
			return nil, nil, errors.New("completion material publication lacks its assignment")
		}
		if !runs[stored.Run] {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_runs WHERE id=$1)`, stored.Run).Scan(&exists); err != nil || !exists {
				_ = rows.Close()
				return nil, nil, errors.New("completion material publication lacks its run")
			}
			runs[stored.Run] = true
		}
		projected := factory.CompletionPublication{
			ID: stored.ID, AssignmentID: stored.AssignmentID, ProjectID: stored.ProjectID,
			Role: stored.Role, Acceptance: stored.Acceptance, Run: stored.Run,
			Candidate: stored.Candidate, BaseSHA: stored.BaseSHA, TargetBranch: stored.TargetBranch,
			Stage: stored.Stage, Outcome: stored.Outcome, Reason: stored.Reason,
			Repository: stored.Repository, Issue: stored.Issue, Revision: stored.Revision,
			PRNumber: stored.PRNumber, PRID: stored.PRID,
		}
		if err = projected.Validate(); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		publications = append(publications, projected)
		referenced[stored.Acceptance] = true
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()
	if len(publications) > factory.MaxReassessmentIssues {
		return nil, nil, errors.New("completion material publications exceed their bound")
	}
	return publications, referenced, nil
}

// completionMergesTx projects repository merges with exact publication
// and assignment joins. Recorded provenance is preserved; no verified
// boolean is invented from a closed issue.
func completionMergesTx(ctx context.Context, tx *sql.Tx, repository int64, assignments []factory.CompletionAssignment, publications []factory.CompletionPublication) ([]factory.CompletionMerge, map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_merges WHERE repository=$1 ORDER BY publication LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	owned := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		owned[assignment.ID] = true
	}
	published := make(map[string]bool, len(publications))
	for _, publication := range publications {
		published[publication.ID] = true
	}
	merges := []factory.CompletionMerge{}
	referenced := make(map[string]bool)
	for rows.Next() {
		var raw []byte
		var stored factory.Merge
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			_ = rows.Close()
			return nil, nil, errors.New("completion material merge is malformed")
		}
		if err = stored.Validate(); err != nil {
			_ = rows.Close()
			return nil, nil, errors.New("completion material merge is invalid")
		}
		if !published[stored.PublicationID] {
			_ = rows.Close()
			return nil, nil, errors.New("completion material merge lacks its publication")
		}
		if !owned[stored.AssignmentID] {
			_ = rows.Close()
			return nil, nil, errors.New("completion material merge lacks its assignment")
		}
		projected := factory.CompletionMerge{
			OriginalClosingOccurrence: stored.OriginalClosingOccurrence,
			MergedCommit:              stored.MergedCommit,
			ID:                        stored.ID,
			PublicationID:             stored.PublicationID,
			AssignmentID:              stored.AssignmentID,
			ProjectID:                 stored.ProjectID,
			Role:                      stored.Role,
			Acceptance:                stored.Acceptance,
			HeadRef:                   stored.HeadRef,
			BaseRef:                   stored.BaseRef,
			HeadOID:                   stored.HeadOID,
			BaseOID:                   stored.BaseOID,
			Stage:                     stored.Stage,
			Outcome:                   stored.Outcome,
			Reason:                    stored.Reason,
			Repository:                stored.Repository,
			Issue:                     stored.Issue,
			PRNumber:                  stored.PRNumber,
			PRID:                      stored.PRID,
			IssueID:                   stored.IssueID,
			Revision:                  stored.Revision,
		}
		if stored.Operation.Work != nil {
			projected.Method = stored.Operation.Work.Method
		}
		if err = projected.Validate(); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		merges = append(merges, projected)
		referenced[stored.Acceptance] = true
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()
	if len(merges) > factory.MaxReassessmentIssues {
		return nil, nil, errors.New("completion material merges exceed their bound")
	}
	return merges, referenced, nil
}

// completionWithdrawalsTx projects the acceptance withdrawal references
// for one repository in deterministic order. Bodies are loaded with the
// acceptances section; this section carries references only.
func completionWithdrawalsTx(ctx context.Context, tx *sql.Tx, repository int64) ([]factory.CompletionWithdrawal, map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT issue, decision FROM issue_acceptance_withdrawals
		WHERE repository=$1 ORDER BY issue, decision LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	withdrawals := []factory.CompletionWithdrawal{}
	referenced := make(map[string]bool)
	for rows.Next() {
		var withdrawal factory.CompletionWithdrawal
		if err = rows.Scan(&withdrawal.Issue, &withdrawal.AcceptanceID); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if err = withdrawal.Validate(); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		withdrawals = append(withdrawals, withdrawal)
		referenced[withdrawal.AcceptanceID] = true
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()
	if len(withdrawals) > factory.MaxReassessmentIssues {
		return nil, nil, errors.New("completion material withdrawals exceed their bound")
	}
	return withdrawals, referenced, nil
}

// completionAcceptanceBodiesTx loads the current acceptance head bodies
// plus every historically referenced task/publication/merge/withdrawal
// acceptance body, ordered by issue index then decision ID. A referenced
// body that cannot be read fails closed instead of sealing without it.
func completionAcceptanceBodiesTx(ctx context.Context, tx *sql.Tx, repository int64, needed map[string]bool) ([]factory.Acceptance, error) {
	headRows, err := tx.QueryContext(ctx, `SELECT decision FROM issue_acceptance_heads
		WHERE repository=$1 ORDER BY issue LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, err
	}
	for headRows.Next() {
		var decision string
		if err = headRows.Scan(&decision); err != nil {
			_ = headRows.Close()
			return nil, err
		}
		needed[decision] = true
	}
	if err = headRows.Err(); err != nil {
		_ = headRows.Close()
		return nil, err
	}
	_ = headRows.Close()
	if len(needed) > factory.MaxReassessmentIssues {
		return nil, errors.New("completion material acceptances exceed their bound")
	}
	bodies := []factory.Acceptance{}
	for id := range needed {
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM issue_acceptance_decisions WHERE id=$1`, id).Scan(&raw); err != nil {
			return nil, errors.New("completion material lacks its acceptance body")
		}
		var acceptance factory.Acceptance
		if err = json.Unmarshal(raw, &acceptance); err != nil {
			return nil, errors.New("completion material acceptance body is malformed")
		}
		if err = acceptance.Validate(); err != nil {
			return nil, errors.New("completion material acceptance body is invalid")
		}
		if acceptance.Repository != repository {
			return nil, errors.New("completion material acceptance names another repository")
		}
		bodies = append(bodies, acceptance)
	}
	for i := range bodies {
		for j := i + 1; j < len(bodies); j++ {
			ai, aj := bodies[i].IssueIndex, bodies[j].IssueIndex
			swap := len(ai) > len(aj) || (len(ai) == len(aj) && ai > aj) ||
				(ai == aj && bodies[i].ID > bodies[j].ID)
			if swap {
				bodies[i], bodies[j] = bodies[j], bodies[i]
			}
		}
	}
	return bodies, nil
}

// completionControlsTx projects the material control state with the 073
// observation normalization: assessed/first-seen timestamps and the
// global NativeRev refresh never enter the semantic hash, while
// revisions, state, IDs, digests and outcomes are retained.
func completionControlsTx(ctx context.Context, tx *sql.Tx, repository int64) ([]factory.IssueControl, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_controls
		WHERE repository=$1 ORDER BY issue LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	controls := []factory.IssueControl{}
	for rows.Next() {
		var raw []byte
		var control factory.IssueControl
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &control); err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material control is malformed")
		}
		if err = control.Validate(); err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material control is invalid")
		}
		control.AssessedUnix, control.FirstSeenUnix, control.NativeRev = 0, 0, 0
		controls = append(controls, control)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(controls) > factory.MaxReassessmentIssues {
		return nil, errors.New("completion material controls exceed their bound")
	}
	return controls, nil
}

// completionPreparationsTx projects the standing preparations for one
// project: exact identity/source/requirement/approval digests plus the
// Ready verdict and Phase. No setup maps, config values, tools or
// container custody enter.
func completionPreparationsTx(ctx context.Context, tx *sql.Tx, projectID string) ([]factory.CompletionPreparation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM project_preparations
		WHERE project_id=$1 AND NOT (data ? 'factory_admission') ORDER BY id LIMIT `+completionMaterialLimit(), projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	preparations := []factory.CompletionPreparation{}
	for rows.Next() {
		var raw []byte
		var stored execution.PreparationRecord
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material preparation is malformed")
		}
		if err = stored.Validate(); err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material preparation is invalid")
		}
		preparation := factory.CompletionPreparation{
			RequirementDigest: stored.Preparation.Requirements.Digest,
			ApprovalDigest:    stored.Preparation.Approval.EffectsDigest,
			SetupDigest:       stored.Preparation.SetupDigest,
			SourceCommit:      stored.Preparation.SourceCommit,
			ID:                stored.Preparation.ID,
			Role:              stored.Preparation.Role,
			Phase:             stored.State.Phase,
			Revision:          stored.Preparation.Revision,
			Ready:             stored.State.Ready,
		}
		if err = preparation.Validate(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		preparations = append(preparations, preparation)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(preparations) > factory.MaxReassessmentIssues {
		return nil, errors.New("completion material preparations exceed their bound")
	}
	return preparations, nil
}

// completionPlansTx projects retained plan revisions, heads and task
// receipts for repository-owned non-completion attempt roots. Plans reuse
// the validated immutable revisions (retained consumed revisions, not
// only the current head); tasks project validated receipts without event
// or reason strings.
func completionPlansTx(ctx context.Context, tx *sql.Tx, assignments []factory.CompletionAssignment) ([]factory.InvestigationPlan, []factory.CompletionPlanHead, []factory.CompletionPlanTask, error) {
	plans := []factory.InvestigationPlan{}
	heads := []factory.CompletionPlanHead{}
	tasks := []factory.CompletionPlanTask{}
	roots := []string{}
	seen := make(map[string]bool)
	for _, assignment := range assignments {
		if !seen[assignment.AttemptRoot] {
			seen[assignment.AttemptRoot] = true
			roots = append(roots, assignment.AttemptRoot)
		}
	}
	// Canonical encoding order is by root, not by assignment
	// first-encounter: random root/child IDs would otherwise emit
	// plans/heads/tasks out of the validated ascending order.
	sort.Strings(roots)
	for _, root := range roots {
		var err error
		if plans, err = appendCompletionRootPlansTx(ctx, tx, root, plans); err != nil {
			return nil, nil, nil, err
		}
		if heads, err = appendCompletionRootHeadsTx(ctx, tx, root, heads); err != nil {
			return nil, nil, nil, err
		}
		if tasks, err = appendCompletionRootTasksTx(ctx, tx, root, tasks); err != nil {
			return nil, nil, nil, err
		}
	}
	if len(plans) > factory.MaxReassessmentIssues || len(heads) > factory.MaxReassessmentIssues ||
		len(tasks) > factory.MaxReassessmentIssues {
		return nil, nil, nil, errors.New("completion material plans exceed their bound")
	}
	return plans, heads, tasks, nil
}

func appendCompletionRootPlansTx(ctx context.Context, tx *sql.Tx, root string, plans []factory.InvestigationPlan) ([]factory.InvestigationPlan, error) {
	revisionRows, err := tx.QueryContext(ctx, `SELECT plan_id, data FROM factory_plan_revisions
		WHERE attempt_root=$1 ORDER BY plan_id LIMIT `+completionMaterialLimit(), root)
	if err != nil {
		return nil, err
	}
	for revisionRows.Next() {
		var planID string
		var raw []byte
		var plan factory.InvestigationPlan
		if err = revisionRows.Scan(&planID, &raw); err != nil {
			_ = revisionRows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &plan); err != nil {
			_ = revisionRows.Close()
			return nil, errors.New("completion material plan is malformed")
		}
		if err = plan.Validate(); err != nil {
			_ = revisionRows.Close()
			return nil, errors.New("completion material plan is invalid")
		}
		plans = append(plans, plan)
	}
	if err = revisionRows.Err(); err != nil {
		_ = revisionRows.Close()
		return nil, err
	}
	_ = revisionRows.Close()
	return plans, nil
}

func appendCompletionRootHeadsTx(ctx context.Context, tx *sql.Tx, root string, heads []factory.CompletionPlanHead) ([]factory.CompletionPlanHead, error) {
	headRows, err := tx.QueryContext(ctx, `SELECT plan_id, generation FROM factory_plan_heads
		WHERE attempt_root=$1 ORDER BY plan_id LIMIT `+completionMaterialLimit(), root)
	if err != nil {
		return nil, err
	}
	for headRows.Next() {
		var head factory.CompletionPlanHead
		head.AttemptRoot = root
		if err = headRows.Scan(&head.PlanID, &head.Generation); err != nil {
			_ = headRows.Close()
			return nil, err
		}
		if err = head.Validate(); err != nil {
			_ = headRows.Close()
			return nil, errors.New("completion material plan head is invalid")
		}
		heads = append(heads, head)
	}
	if err = headRows.Err(); err != nil {
		_ = headRows.Close()
		return nil, err
	}
	_ = headRows.Close()
	return heads, nil
}

func appendCompletionRootTasksTx(ctx context.Context, tx *sql.Tx, root string, tasks []factory.CompletionPlanTask) ([]factory.CompletionPlanTask, error) {
	taskRows, err := tx.QueryContext(ctx, `SELECT data FROM factory_plan_tasks
		WHERE attempt_root=$1 ORDER BY plan_id, task_id LIMIT `+completionMaterialLimit(), root)
	if err != nil {
		return nil, err
	}
	for taskRows.Next() {
		var raw []byte
		var receipt PlanTaskReceipt
		if err = taskRows.Scan(&raw); err != nil {
			_ = taskRows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &receipt); err != nil {
			_ = taskRows.Close()
			return nil, errors.New("completion material plan task is malformed")
		}
		if err = receipt.Validate(); err != nil {
			_ = taskRows.Close()
			return nil, errors.New("completion material plan task is invalid")
		}
		if receipt.AttemptRoot != root {
			_ = taskRows.Close()
			return nil, errors.New("completion material plan task names another root")
		}
		projected := factory.CompletionPlanTask{
			Checks:       append([]string(nil), receipt.Checks...),
			InputDigest:  receipt.InputDigest,
			OutputDigest: receipt.OutputDigest,
			Tree:         receipt.Tree,
			Commit:       receipt.Commit,
			AttemptRoot:  receipt.AttemptRoot,
			PlanID:       receipt.PlanID,
			TaskID:       receipt.TaskID,
			Revision:     receipt.Revision,
			State:        receipt.State,
			Child:        receipt.Child,
		}
		if projected.Checks == nil {
			projected.Checks = []string{}
		}
		if err = projected.Validate(); err != nil {
			_ = taskRows.Close()
			return nil, err
		}
		tasks = append(tasks, projected)
	}
	if err = taskRows.Err(); err != nil {
		_ = taskRows.Close()
		return nil, err
	}
	_ = taskRows.Close()
	return tasks, nil
}

// completionChecksTx projects the recorded check verdicts for one
// repository. Source assessments validate before observation metadata
// is omitted.
func completionChecksTx(ctx context.Context, tx *sql.Tx, repository int64) ([]factory.CompletionCheck, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM factory_check_assessments
		WHERE repository=$1 ORDER BY pr LIMIT `+completionMaterialLimit(), repository)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	checks := []factory.CompletionCheck{}
	for rows.Next() {
		var raw []byte
		var stored factory.CheckAssessment
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &stored); err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material check is malformed")
		}
		projected, err := factory.NewCompletionCheck(stored)
		if err != nil {
			_ = rows.Close()
			return nil, errors.New("completion material check is invalid")
		}
		checks = append(checks, projected)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(checks) > factory.MaxReassessmentIssues {
		return nil, errors.New("completion material checks exceed their bound")
	}
	return checks, nil
}

// completionAuthorityTx projects the standing authority: the actual
// effective verdict with its missing reasons and bound revisions, the
// current-head context (project, admitted requirement and approval
// head digests with the verified flag, and the policy target branch),
// the permitted role names and the admitted reassessment minute
// allowance. The existing Effective.Authority.RequirementsID and
// ApprovalID carry the actual current heads; no duplicate identity
// fields exist. Stable semantic grant state only: no secret grant or
// config documents, no SourceInputs map and no raw setup/config/secret
// bodies enter, and a missing policy or head fails closed instead of
// projecting an empty authority. This grants no new authority: the
// coarse preparation-ready predicate behind EvaluateAuthority never
// proves a head-matching prepared checkout, and the existing
// AuthorityFingerprint still hashes grant revisions only.
func completionAuthorityTx(ctx context.Context, tx *sql.Tx, repository int64, projectID string) (factory.CompletionAuthority, error) {
	input, err := factoryAuthorityInput(ctx, tx, repository, project.RoleCoder)
	if err != nil {
		return factory.CompletionAuthority{}, err
	}
	if input.Policy == nil {
		return factory.CompletionAuthority{}, errors.New("completion material lacks its authority policy")
	}
	head, err := completionHeadContextTx(ctx, tx, projectID)
	if err != nil {
		return factory.CompletionAuthority{}, err
	}
	effective := factory.EvaluateAuthority(input)
	if effective.Missing == nil {
		effective.Missing = []string{}
	}
	effective.Authority.RequirementsID = head.requirement.ID
	effective.Authority.ApprovalID = head.approval.ID
	authority := factory.CompletionAuthority{
		Effective:               effective,
		ProjectID:               projectID,
		RequirementSourceCommit: head.requirement.SourceCommit,
		RequirementSetupDigest:  head.requirement.SetupDigest,
		RequirementInputsDigest: head.requirement.InputsDigest,
		ApprovalEffectsDigest:   head.approval.EffectsDigest,
		ApprovalReadinessDigest: head.approval.ReadinessDigest,
		ApprovalVerified:        head.approval.Verified,
		ApprovalReviewed:        head.approval.Native != nil && head.approval.Reviewed,
		TargetBranch:            input.Policy.TargetBranch,
		Roles:                   factory.PermittedCompletionRoles(input.Policy.Roles, input.Sponsorships),
		ReassessmentMinutes:     factory.EffectiveReassessmentMinutes(input.Policy.ReassessmentMinutes),
	}
	if err = authority.Validate(); err != nil {
		return factory.CompletionAuthority{}, err
	}
	return authority, nil
}

// completionHeadContext is the admitted current-head pair behind one
// capture: the immutable requirement and approval decisions selected
// by the project heads.
type completionHeadContext struct {
	requirement project.RequirementDecision
	approval    project.ApprovalDecision
}

// completionHeadContextTx loads the repo project head decision pair in
// the caller's repeatable-read capture transaction: BOTH current head
// IDs plus their immutable RequirementDecision/ApprovalDecision rows.
// Every row validates its full shape, the selected head equals the
// decoded decision ID, the decision project matches the repo project,
// and the approval requirement equals the current requirement ID.
// Missing, unbound or invalid rows hold the capture with a named
// error, never an empty authority fallback. Row identity and
// provenance stay tight: plain pinned SELECTs observe the immutable
// rows, never bypassing their immutable triggers.
func completionHeadContextTx(ctx context.Context, tx *sql.Tx, projectID string) (completionHeadContext, error) {
	var head completionHeadContext
	var requirementID string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1`, projectID).Scan(&requirementID); err != nil {
		return head, errors.New("completion material lacks its requirement head")
	}
	if !project.ValidDecisionID(requirementID) {
		return head, errors.New("completion material requirement head is invalid")
	}
	var requirementData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM project_requirement_decisions WHERE id=$1`, requirementID).Scan(&requirementData); err != nil {
		return head, errors.New("completion material lacks its requirement decision")
	}
	var requirement project.RequirementDecision
	if err := json.Unmarshal(requirementData, &requirement); err != nil {
		return head, errors.New("completion material requirement decision is malformed")
	}
	if err := requirement.Validate(); err != nil {
		return head, errors.New("completion material requirement decision is invalid")
	}
	if requirement.ID != requirementID {
		return head, errors.New("completion material requirement head differs from its decision")
	}
	if requirement.Project != projectID {
		return head, errors.New("completion material requirement names another project")
	}
	var approvalID string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1`, projectID).Scan(&approvalID); err != nil {
		return head, errors.New("completion material lacks its approval head")
	}
	if !project.ValidDecisionID(approvalID) {
		return head, errors.New("completion material approval head is invalid")
	}
	var approvalData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM project_approval_decisions WHERE id=$1`, approvalID).Scan(&approvalData); err != nil {
		return head, errors.New("completion material lacks its approval decision")
	}
	var approval project.ApprovalDecision
	if err := json.Unmarshal(approvalData, &approval); err != nil {
		return head, errors.New("completion material approval decision is malformed")
	}
	if err := approval.Validate(); err != nil {
		return head, errors.New("completion material approval decision is invalid")
	}
	if approval.ID != approvalID {
		return head, errors.New("completion material approval head differs from its decision")
	}
	if approval.Project != projectID {
		return head, errors.New("completion material approval names another project")
	}
	if approval.Requirement != requirement.ID {
		return head, errors.New("completion material approval names another requirement")
	}
	head.requirement = requirement
	head.approval = approval
	return head, nil
}
