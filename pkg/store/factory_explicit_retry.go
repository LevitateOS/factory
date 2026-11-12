package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// ErrExplicitRetryIneligible identifies a refused retry, rather than a storage failure.
var ErrExplicitRetryIneligible = errors.New("explicit retry is no longer dispatchable")

// LatestIssueAssignment reads the newest issue root, which owns its
// publication. Child history cannot hide a newer active or terminal root.
func (s *Store) LatestIssueAssignment(ctx context.Context, repository, issue int64) (factory.Assignment, error) {
	var assignment factory.Assignment
	var data []byte
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id,data FROM factory_assignments WHERE repository=$1 AND issue=$2
AND (data->>'publication_assignment'=id OR (data->>'attempt_root'=id AND coalesce(data->>'publication_assignment','')=''))
ORDER BY seq DESC LIMIT 1`, repository, issue).Scan(&id, &data)
	if err == nil {
		err = json.Unmarshal(data, &assignment)
	}
	if err == nil && assignment.PublicationAssignment == "" {
		err = errors.New("issue root lacks its bound publication")
	}
	return assignment, err
}

// RecordExplicitRetryCommand atomically records the request after verifying
// the failed assignment still names the
// issue's current accepted head and its prior run is accounted or confirmed
// unused.
type admittedRetryPayload struct {
	priorOutcome string
}

func admitRetryCommand(cmd factory.Command, decision factory.RetryDecision, repository, issue int64, acceptance string) (admittedRetryPayload, error) {
	var admitted admittedRetryPayload
	if err := decision.Validate(); err != nil || cmd.ID != decision.CommandID || cmd.Type != factory.CommandRetry || cmd.Target != "run/"+decision.Prior || repository <= 0 || issue <= 0 || acceptance == "" {
		return admitted, ErrExplicitRetryIneligible
	}
	// The payload includes the immutable prior outcome. Run truth below
	// verifies that value before the retry request is committed.
	var payload struct {
		PriorRun     string `json:"prior_run"`
		PriorOutcome string `json:"prior_outcome"`
	}
	if err := json.Unmarshal([]byte(cmd.Payload), &payload); err != nil || payload.PriorRun != decision.Prior || !retryableRunOutcome(payload.PriorOutcome) {
		return admitted, ErrExplicitRetryIneligible
	}
	return admittedRetryPayload{priorOutcome: payload.PriorOutcome}, nil
}

func (s *Store) RecordExplicitRetryCommand(ctx context.Context, cmd factory.Command, decision factory.RetryDecision, repository, issue int64, acceptance string, now time.Time) (factory.Command, bool, error) {
	admitted, err := admitRetryCommand(cmd, decision, repository, issue, acceptance)
	if err != nil {
		return factory.Command{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Command{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, _, _, err = ensureDispatchGateTx(ctx, tx, repository); err != nil {
		return factory.Command{}, false, err
	}
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil || !created {
		if err != nil {
			return factory.Command{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return factory.Command{}, false, err
		}
		return stored, false, nil
	}
	var collision bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE id=$1)`, cmd.ID).Scan(&collision); err != nil {
		return factory.Command{}, false, err
	}
	if collision {
		return factory.Command{}, false, ErrExplicitRetryIneligible
	}
	if err = validateExplicitRetryFactsTx(ctx, tx, decision.Prior, repository, issue, acceptance, admitted.priorOutcome); err != nil {
		return factory.Command{}, false, err
	}
	if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
		return factory.Command{}, false, err
	}
	stored.Finished = now.UTC().Format(time.RFC3339Nano)
	if err = tx.Commit(); err != nil {
		return factory.Command{}, false, err
	}
	return stored, true, nil
}

// PendingExplicitRetry returns one finished retry command for the latest
// finished assignment on the issue's unchanged acceptance. Its command ID
// becomes the fresh assignment identity, so a recorded packet consumes it.
// Its exact coder preparation's confirmed native stop also ends that command's
// admission; retained command/preparation history cannot hide a later Retry.
func (s *Store) PendingExplicitRetry(ctx context.Context, repository, issue int64, acceptance string) (factory.RetryDecision, error) {
	return pendingExplicitRetry(ctx, s.db, repository, issue, acceptance)
}

type explicitRetryQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func pendingExplicitRetry(ctx context.Context, q explicitRetryQuerier, repository, issue int64, acceptance string) (factory.RetryDecision, error) {
	var cmd factory.Command
	err := q.QueryRowContext(ctx, `SELECT c.id,c.type,c.payload FROM factory_assignments owner
		JOIN factory_commands c ON c.type=$1 AND c.finished<>''
		JOIN factory_runs prior ON c.target='run/'||prior.id
		JOIN factory_run_views v ON v.run=prior.id
		JOIN factory_assignments source ON source.id=v.attempt
		WHERE owner.repository=$2 AND owner.issue=$3 AND owner.stage='finished' AND owner.data->>'acceptance'=$4
		AND owner.data->>'publication_assignment'=owner.id
		AND owner.id=(SELECT latest.id FROM factory_assignments latest WHERE latest.repository=$2 AND latest.issue=$3 AND latest.data->>'publication_assignment'=latest.id ORDER BY latest.seq DESC LIMIT 1)
		AND source.repository=$2 AND source.issue=$3 AND source.stage='finished' AND source.data->>'acceptance'=$4
		AND source.data->>'attempt_root'=owner.data->>'attempt_root' AND source.data->>'publication_assignment'=owner.id
		AND prior.settled AND COALESCE(prior.data->>'reconciled','false')='true'
		AND prior.data->>'outcome' IN ($5,$6,$7)
		AND ((source.id=owner.id AND source.data->>'role'=$8 AND source.data->>'outcome' IN ($6,$7))
			OR EXISTS(SELECT 1 FROM factory_attempt_allowances allowance
				WHERE allowance.repository=$2 AND allowance.issue=$3 AND allowance.root_assignment=owner.id
				AND COALESCE((allowance.data->>'closed')::boolean,FALSE)
				AND NOT COALESCE((allowance.data->>'active')::boolean,FALSE)))
		AND NOT EXISTS(SELECT 1 FROM factory_assignments consumed WHERE consumed.id=c.id)
		AND NOT EXISTS(SELECT 1 FROM project_preparations p
			WHERE p.project_id=source.data->>'project_id' AND p.role=$8
			AND p.data#>>'{preparation,id}'=p.id
			AND p.data#>>'{preparation,project}'=p.project_id
			AND p.data#>>'{preparation,role}'=$8
			AND p.data#>>'{factory_admission,repository}'=$2::text
			AND p.data#>>'{factory_admission,issue}'=$3::text
			AND p.data#>>'{factory_admission,project}'=p.project_id
			AND p.data#>>'{factory_admission,owner_assignment}'=c.id
			AND p.data#>>'{factory_admission,child_assignment}'=c.id
			AND p.data#>>'{factory_admission,attempt_root}'=c.id
			AND p.data#>>'{factory_admission,publication_id}'=''
			AND p.data#>>'{factory_admission,control,acceptance}'=$4
			AND p.data#>'{factory_admission,stopped}'='true'::jsonb
			AND p.data#>>'{factory_admission,retirement}'='confirmed')
		ORDER BY c.created,c.id LIMIT 1`, factory.CommandRetry, repository, issue, acceptance,
		string(factory.Succeeded), string(factory.Failed), string(factory.Cancelled), project.RoleCoder).Scan(&cmd.ID, &cmd.Type, &cmd.Payload)
	if err != nil {
		return factory.RetryDecision{}, err
	}
	return cmd.RetryDecision()
}

// RecordExplicitRetryPacket serializes competing dispatchers on the gate and
// command, rechecks prior accounting and the accepted head, then commits the
// retry assignment/run/reservation through the regular admission checks.
type admittedRetryCommand struct {
	priorOutcome string
}

func admitRetryGateTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if _, _, _, err := ensureDispatchGateTx(ctx, tx, a.Repository); err != nil {
		return err
	}
	var collision bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE id=$1)`, a.ID).Scan(&collision); err != nil {
		return err
	}
	if collision {
		return ErrAssignmentActive
	}
	return nil
}

func admitRetryCommandTx(ctx context.Context, tx *sql.Tx, decision factory.RetryDecision) (admittedRetryCommand, error) {
	var admitted admittedRetryCommand
	var typ, target, payload, finished string
	if err := tx.QueryRowContext(ctx, `SELECT type,target,payload,finished FROM factory_commands WHERE id=$1 FOR UPDATE`, decision.CommandID).Scan(&typ, &target, &payload, &finished); err != nil {
		return admitted, err
	}
	if typ != factory.CommandRetry || target != "run/"+decision.Prior || finished == "" {
		return admitted, ErrExplicitRetryIneligible
	}
	var priorPayload struct {
		PriorRun     string `json:"prior_run"`
		PriorOutcome string `json:"prior_outcome"`
	}
	if err := json.Unmarshal([]byte(payload), &priorPayload); err != nil {
		return admitted, err
	}
	if priorPayload.PriorRun != decision.Prior || !retryableRunOutcome(priorPayload.PriorOutcome) {
		return admitted, ErrExplicitRetryIneligible
	}
	return admittedRetryCommand{priorOutcome: priorPayload.PriorOutcome}, nil
}

func (s *Store) RecordExplicitRetryPacket(ctx context.Context, decision factory.RetryDecision, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	return s.recordExplicitRetryPacket(ctx, decision, expected, a, r, run, view, nil)
}

func (s *Store) RecordNativeExplicitRetryPacket(ctx context.Context, decision factory.RetryDecision, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView, native execution.NativeLaunch) error {
	return s.recordExplicitRetryPacket(ctx, decision, expected, a, r, run, view, &native)
}

func (s *Store) recordExplicitRetryPacket(ctx context.Context, decision factory.RetryDecision, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView, native *execution.NativeLaunch) error {
	if a.ID != decision.CommandID || a.Role != project.RoleCoder || a.AttemptRoot != a.ID || a.PublicationAssignment != a.ID {
		return ErrExplicitRetryIneligible
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = admitRetryGateTx(ctx, tx, a); err != nil {
		return err
	}
	admitted, err := admitRetryCommandTx(ctx, tx, decision)
	if err != nil {
		return err
	}
	if err = validateExplicitRetryFactsTx(ctx, tx, decision.Prior, a.Repository, a.Issue, a.Acceptance, admitted.priorOutcome); err != nil {
		return err
	}
	if err = checkDispatchOpenTx(ctx, tx, a.Repository); err != nil {
		return err
	}
	if err = recordDispatchPacketTx(ctx, tx, expected, a, r, run, view, true, native); err != nil {
		return err
	}
	return tx.Commit()
}

func loadRetryPriorAssignmentTx(ctx context.Context, tx *sql.Tx, priorRun string, repository, issue int64, acceptance, priorOutcome string) (factory.Assignment, error) {
	var empty factory.Assignment
	var head string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 FOR UPDATE`, repository, issue).Scan(&head); err != nil {
		return empty, err
	}
	if head != acceptance {
		return empty, ErrExplicitRetryIneligible
	}
	var priorAssignmentData []byte
	err := tx.QueryRowContext(ctx, `SELECT a.data FROM factory_assignments a
		JOIN factory_run_views v ON v.attempt=a.id WHERE v.run=$1 AND v.repository=$2 AND v.issue=$3 FOR UPDATE OF a`, priorRun, repository, issue).Scan(&priorAssignmentData)
	if err != nil {
		return empty, err
	}
	var priorAssignment factory.Assignment
	if err = json.Unmarshal(priorAssignmentData, &priorAssignment); err != nil {
		return empty, err
	}
	if err = priorAssignment.Validate(); err != nil {
		return empty, ErrExplicitRetryIneligible
	}
	if priorAssignment.Run != priorRun || priorAssignment.Acceptance != acceptance || !priorAssignment.Finished() ||
		!project.ValidFactoryRole(priorAssignment.Role) || !assignmentRunOutcomeMatches(priorAssignment, priorOutcome) || !retryableRunOutcome(priorOutcome) ||
		priorAssignment.Repository != repository || priorAssignment.Issue != issue {
		return empty, ErrExplicitRetryIneligible
	}
	return priorAssignment, nil
}

func loadRetryOwnerTx(ctx context.Context, tx *sql.Tx, priorAssignment factory.Assignment, repository, issue int64, acceptance string) (factory.Assignment, error) {
	var empty factory.Assignment
	var latestData []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE repository=$1 AND issue=$2 ORDER BY seq DESC LIMIT 1 FOR UPDATE`, repository, issue).Scan(&latestData)
	if err != nil {
		return empty, err
	}
	var latest factory.Assignment
	if err = json.Unmarshal(latestData, &latest); err != nil || latest.Validate() != nil {
		return empty, ErrExplicitRetryIneligible
	}
	var ownerData []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE repository=$1 AND issue=$2
		AND data->>'publication_assignment'=id ORDER BY seq DESC LIMIT 1 FOR UPDATE`, repository, issue).Scan(&ownerData)
	if err != nil {
		return empty, err
	}
	var owner factory.Assignment
	if err = json.Unmarshal(ownerData, &owner); err != nil {
		return empty, err
	}
	if err = owner.Validate(); err != nil || !owner.Finished() || owner.Role != project.RoleCoder ||
		owner.Acceptance != acceptance || owner.AttemptRoot != owner.ID || priorAssignment.AttemptRoot != owner.ID ||
		priorAssignment.PublicationAssignment != owner.ID || latest.AttemptRoot != owner.ID || latest.PublicationAssignment != owner.ID ||
		latest.Acceptance != acceptance || (latest.ID != priorAssignment.ID && priorAssignment.ID != owner.ID) {
		return empty, ErrExplicitRetryIneligible
	}
	// The issue view exposes its publication root. Retrying that exact root
	// restarts the fully settled attempt, including a failed child. A child
	// target must still be the newest assignment; older roots stay refused.
	return owner, nil
}

func admitRetryRunTx(ctx context.Context, tx *sql.Tx, priorRun string, priorAssignment, owner factory.Assignment, repository, issue int64, priorOutcome string) (factory.Run, error) {
	var empty factory.Run
	var runData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, priorRun).Scan(&runData); err != nil {
		return empty, err
	}
	var run factory.Run
	if err := json.Unmarshal(runData, &run); err != nil {
		return empty, err
	}
	if !run.Reconciled || run.Role != priorAssignment.Role || string(run.Outcome) != priorOutcome || run.ProjectID != priorAssignment.ProjectID {
		return empty, ErrExplicitRetryIneligible
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, repository, issue, owner.ID)
	if err != nil || allowance.RootAssignment != owner.ID {
		return empty, ErrExplicitRetryIneligible
	}
	// Retry is eligible once every run of the root has settled; the fresh
	// attempt closes this root. Attempts never time out on their own, so
	// Retry is the way past a stuck or finished attempt.
	settled, err := attemptRootRunsSettledTx(ctx, tx, repository, issue, owner.ID)
	if err != nil {
		return empty, err
	}
	if !settled {
		return empty, ErrExplicitRetryIneligible
	}
	if err = retryOperationsSettledTx(ctx, tx, owner); err != nil {
		return empty, err
	}
	return run, nil
}

func admitRetryReservationTx(ctx context.Context, tx *sql.Tx, priorRun string, priorAssignment factory.Assignment, repository int64, run factory.Run) error {
	var state string
	var reservationData []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_reservations WHERE assignment=$1 FOR UPDATE`, priorAssignment.ID).Scan(&state, &reservationData); err != nil {
		return err
	}
	var reservation factory.Reservation
	if err := json.Unmarshal(reservationData, &reservation); err != nil {
		return err
	}
	if err := reservation.Validate(); err != nil || reservation.AssignmentID != priorAssignment.ID || reservation.Repository != repository || reservation.Connection != priorAssignment.Connection || reservation.State != state {
		return ErrExplicitRetryIneligible
	}
	var usageData []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM factory_usage WHERE run=$1`, priorRun).Scan(&usageData)
	if err == nil {
		var usage factory.Usage
		if err = json.Unmarshal(usageData, &usage); err != nil {
			return err
		}
		if err = usage.Validate(); err != nil || usage.RunID != priorRun || usage.Repository != repository || usage.Connection != priorAssignment.Connection || !usage.StartedAt.Equal(run.Started) || reservation.State != factory.ReservationConsumed {
			return ErrExplicitRetryIneligible
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if reservation.State != factory.ReservationReleased {
		return ErrExplicitRetryIneligible
	}
	return nil
}

func validateExplicitRetryFactsTx(ctx context.Context, tx *sql.Tx, priorRun string, repository, issue int64, acceptance, priorOutcome string) error {
	priorAssignment, err := loadRetryPriorAssignmentTx(ctx, tx, priorRun, repository, issue, acceptance, priorOutcome)
	if err != nil {
		return err
	}
	owner, err := loadRetryOwnerTx(ctx, tx, priorAssignment, repository, issue, acceptance)
	if err != nil {
		return err
	}
	run, err := admitRetryRunTx(ctx, tx, priorRun, priorAssignment, owner, repository, issue, priorOutcome)
	if err != nil {
		return err
	}
	return admitRetryReservationTx(ctx, tx, priorRun, priorAssignment, repository, run)
}

func retryableRunOutcome(outcome string) bool {
	return outcome == string(factory.Succeeded) || outcome == string(factory.Failed) || outcome == string(factory.Cancelled)
}

// Terminal role results and process exits are retained independently. A missing
// report can fence work before the process subsequently fails. Compatibility
// grants no retry authority; callers retain their scope, accounting and effect
// gates before admitting a fresh command.
func assignmentRunOutcomeMatches(a factory.Assignment, runOutcome string) bool {
	if !retryableRunOutcome(runOutcome) {
		return false
	}
	if string(a.Outcome) == runOutcome {
		return true
	}
	return (runOutcome == string(factory.Succeeded) || runOutcome == string(factory.Failed)) && a.Outcome == factory.NeedsHuman &&
		a.Reason == factory.AssignReasonNoReport && a.Result != nil && !a.Result.Reported && a.Result.Status == "blocked"
}

func retryOperationNeedsResolution(attempts int, effect collaboration.EffectState, cancellation collaboration.CancellationState, completion collaboration.CompletionState) bool {
	if attempts == 0 {
		return false
	}
	return effect == "" || effect == factory.OpEffectPending || effect == factory.OpEffectIndeterminate ||
		cancellation == factory.OpCancelPending || cancellation == factory.OpCancelIndeterminate ||
		completion == factory.OpCompletionPending || completion == factory.OpCompletionNeedsIntervention ||
		effect == factory.OpEffectCommitted && completion != factory.OpCompletionComplete
}

func retryPublicationSettled(publication factory.Publication) bool {
	if publication.Stage == factory.PublicationOpen ||
		retryOperationNeedsResolution(publication.Publish.Attempts, publication.Publish.Effect, publication.Publish.Cancellation, publication.Publish.Completion) ||
		retryOperationNeedsResolution(publication.PRCreate.Attempts, publication.PRCreate.Effect, publication.PRCreate.Cancellation, publication.PRCreate.Completion) {
		return false
	}
	for _, correction := range publication.Corrections {
		if retryOperationNeedsResolution(correction.Attempts, correction.Effect, correction.Cancellation, correction.Completion) {
			return false
		}
	}
	for _, review := range publication.ReviewOperations {
		if review.Outcome.OperationID == "" || retryOperationNeedsResolution(1, review.Outcome.Effect, review.Outcome.Cancellation, review.Outcome.Completion) {
			return false
		}
	}
	return true
}

func retryMergeSettled(merge factory.Merge) bool {
	return merge.Stage != factory.MergeOpen && merge.Stage != factory.MergeMerged &&
		!retryOperationNeedsResolution(merge.Operation.Attempts, merge.Operation.Effect, merge.Operation.Cancellation, merge.Operation.Completion)
}

func loadRetryPublicationsTx(ctx context.Context, tx *sql.Tx, owner factory.Assignment) ([]factory.Publication, error) {
	pubRows, err := tx.QueryContext(ctx, `SELECT data FROM factory_publications WHERE repository=$1 AND issue=$2 ORDER BY seq LIMIT $3 FOR UPDATE`, owner.Repository, owner.Issue, storePublicationLimit+1)
	if err != nil {
		return nil, err
	}
	publications := make([]factory.Publication, 0)
	for pubRows.Next() {
		var data []byte
		var publication factory.Publication
		if err = pubRows.Scan(&data); err != nil || json.Unmarshal(data, &publication) != nil || publication.Validate() != nil {
			_ = pubRows.Close()
			return nil, ErrExplicitRetryIneligible
		}
		publications = append(publications, publication)
	}
	if err = pubRows.Err(); err != nil {
		_ = pubRows.Close()
		return nil, err
	}
	if err = pubRows.Close(); err != nil {
		return nil, err
	}
	if len(publications) > storePublicationLimit {
		return nil, ErrExplicitRetryIneligible
	}
	return publications, nil
}

func loadRetryMergesTx(ctx context.Context, tx *sql.Tx, owner factory.Assignment) ([]factory.Merge, error) {
	mergeRows, err := tx.QueryContext(ctx, `SELECT data FROM factory_merges WHERE repository=$1 AND issue=$2 ORDER BY seq LIMIT $3 FOR UPDATE`, owner.Repository, owner.Issue, storeMergeLimit+1)
	if err != nil {
		return nil, err
	}
	merges := make([]factory.Merge, 0)
	for mergeRows.Next() {
		var data []byte
		var merge factory.Merge
		if err = mergeRows.Scan(&data); err != nil || json.Unmarshal(data, &merge) != nil || merge.Validate() != nil {
			_ = mergeRows.Close()
			return nil, ErrExplicitRetryIneligible
		}
		merges = append(merges, merge)
	}
	if err = mergeRows.Err(); err != nil {
		_ = mergeRows.Close()
		return nil, err
	}
	if err = mergeRows.Close(); err != nil {
		return nil, err
	}
	if len(merges) > storeMergeLimit {
		return nil, ErrExplicitRetryIneligible
	}
	return merges, nil
}

func retryOperationsSettledTx(ctx context.Context, tx *sql.Tx, owner factory.Assignment) error {
	publications, err := loadRetryPublicationsTx(ctx, tx, owner)
	if err != nil {
		return err
	}
	for _, publication := range publications {
		if !retryPublicationSettled(publication) {
			return ErrExplicitRetryIneligible
		}
	}
	merges, err := loadRetryMergesTx(ctx, tx, owner)
	if err != nil {
		return err
	}
	for _, merge := range merges {
		if !retryMergeSettled(merge) {
			return ErrExplicitRetryIneligible
		}
	}
	return nil
}
