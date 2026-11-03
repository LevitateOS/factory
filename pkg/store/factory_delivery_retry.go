package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// RecordDeliveryRetryCommand uses the ordinary owner Retry request for a
// terminal, accounted issue-zero verifier. It does not reopen its old root.
func (s *Store) RecordDeliveryRetryCommand(ctx context.Context, cmd factory.Command, decision factory.RetryDecision, repository int64, now time.Time) (factory.Command, bool, error) {
	var payload struct {
		PriorRun     string `json:"prior_run"`
		PriorOutcome string `json:"prior_outcome"`
	}
	if decision.Validate() != nil || cmd.Type != factory.CommandRetry || cmd.ID != decision.CommandID || cmd.Target != "run/"+decision.Prior || repository <= 0 ||
		json.Unmarshal([]byte(cmd.Payload), &payload) != nil || payload.PriorRun != decision.Prior || !retryableRunOutcome(payload.PriorOutcome) {
		return factory.Command{}, false, ErrExplicitRetryIneligible
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
		if err = tx.Commit(); err != nil {
			return factory.Command{}, false, err
		}
		return stored, false, nil
	}
	if _, err = deliveryRetryPriorTx(ctx, tx, repository, decision.Prior, payload.PriorOutcome); err != nil {
		return factory.Command{}, false, err
	}
	var occupied bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE id=$1)
		OR EXISTS(SELECT 1 FROM factory_commands c WHERE c.type=$2 AND c.target=$3 AND c.id<>$1 AND c.finished<>''
		AND NOT EXISTS(SELECT 1 FROM factory_assignments a WHERE a.id=c.id))`, cmd.ID, factory.CommandRetry, cmd.Target).Scan(&occupied)
	if err != nil {
		return factory.Command{}, false, err
	}
	if occupied {
		return factory.Command{}, false, ErrExplicitRetryIneligible
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

// PendingDeliveryRetry selects only an explicit unconsumed command for the
// same immutable version. Normal polling never retries an incomplete report.
func (s *Store) PendingDeliveryRetry(ctx context.Context, repository int64, subject factory.DeliverySubject) (factory.RetryDecision, error) {
	raw, err := json.Marshal(subject)
	if err != nil {
		return factory.RetryDecision{}, err
	}
	var cmd factory.Command
	err = s.db.QueryRowContext(ctx, `SELECT c.id,c.type,c.payload FROM factory_commands c
		JOIN factory_run_views v ON c.target='run/'||v.run
		JOIN factory_assignments a ON a.id=v.attempt
		WHERE c.type=$1 AND c.finished<>'' AND v.repository=$2 AND v.issue=0
		AND a.data->>'phase'='verify' AND a.data->'subject'->>'kind'='delivery'
		AND (a.data->'subject'->'delivery')-'delivery_id'=($3::jsonb)-'delivery_id'
		AND NOT EXISTS(SELECT 1 FROM factory_assignments consumed WHERE consumed.id=c.id)
		ORDER BY c.created,c.id LIMIT 1`, factory.CommandRetry, repository, string(raw)).Scan(&cmd.ID, &cmd.Type, &cmd.Payload)
	if err != nil {
		return factory.RetryDecision{}, err
	}
	return cmd.RetryDecision()
}

func sameDeliveryVersion(left, right factory.DeliverySubject) bool {
	left.DeliveryID, right.DeliveryID = "", ""
	return left == right
}

// DeliveryRetryAvailable projects the same retirement/accounting/version
// checks without recording a command or spending. The action rechecks them.
func (s *Store) DeliveryRetryAvailable(ctx context.Context, runID string) (bool, error) {
	run, err := s.FactoryRun(ctx, runID)
	if err != nil {
		return false, err
	}
	view, err := s.FactoryRunView(ctx, runID)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = deliveryRetryPriorTx(ctx, tx, view.Repository, runID, string(run.Outcome)); err != nil {
		if errors.Is(err, ErrExplicitRetryIneligible) || errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var queued bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_commands c WHERE c.type=$1 AND c.target=$2 AND c.finished<>''
		AND NOT EXISTS(SELECT 1 FROM factory_assignments a WHERE a.id=c.id))`, factory.CommandRetry, "run/"+runID).Scan(&queued)
	return !queued && err == nil, err
}

func deliveryRetryPriorTx(ctx context.Context, tx *sql.Tx, repository int64, priorRun, priorOutcome string) (factory.Assignment, error) {
	var prior factory.Assignment
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT a.data FROM factory_assignments a JOIN factory_run_views v ON v.attempt=a.id
		WHERE v.run=$1 AND v.repository=$2 AND v.issue=0 FOR UPDATE OF a`, priorRun, repository).Scan(&raw)
	if err != nil {
		return prior, err
	}
	if json.Unmarshal(raw, &prior) != nil || prior.Validate() != nil {
		return prior, ErrExplicitRetryIneligible
	}
	failedReport := prior.Result != nil && prior.Result.Reported && prior.Result.PhaseResult != nil && prior.Result.PhaseResult.Output != nil && prior.Result.PhaseResult.Output.Verification != nil && prior.Result.PhaseResult.Output.Verification.Outcome == factory.AppVerificationFailed
	if prior.Repository != repository || prior.Run != priorRun || prior.Issue != 0 || prior.Phase != project.ExecutionVerify || prior.Role != project.RoleReviewer ||
		prior.Subject.Kind != factory.SubjectDelivery || prior.Subject.Delivery == nil || prior.Stage != factory.AssignmentFinished || prior.AttemptRoot != prior.ID || prior.ParentAssignment != "" || prior.PublicationAssignment != "" ||
		(prior.Outcome == factory.Succeeded && !failedReport) || !retryableRunOutcome(priorOutcome) || !retryAssignmentOutcomeMatches(prior, priorOutcome) {
		return prior, ErrExplicitRetryIneligible
	}
	var head string
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1 FOR UPDATE`, prior.Subject.Delivery.IntentID).Scan(&head); err != nil {
		return prior, err
	}
	if head != prior.Subject.Delivery.AcceptedRevision {
		return prior, ErrExplicitRetryIneligible
	}
	remaining, err := remainingAppVerificationSecondsTx(ctx, tx, repository, prior.Subject.Delivery.IntentID, time.Now())
	if err != nil {
		return prior, err
	}
	if remaining < 60 {
		return prior, ErrExplicitRetryIneligible
	}
	if _, _, err = applicationVerificationInputsTx(ctx, tx, prior); err != nil {
		return prior, err
	}
	version, _ := json.Marshal(prior.Subject.Delivery)
	var latest string
	err = tx.QueryRowContext(ctx, `SELECT id FROM factory_assignments WHERE repository=$1 AND issue=0 AND data->>'phase'='verify'
		AND data->'subject'->>'kind'='delivery' AND (data->'subject'->'delivery')-'delivery_id'=($2::jsonb)-'delivery_id'
		ORDER BY seq DESC LIMIT 1 FOR UPDATE`, repository, string(version)).Scan(&latest)
	if err != nil {
		return prior, err
	}
	if latest != prior.ID {
		return prior, ErrExplicitRetryIneligible
	}
	run, err := admitRetryRunTx(ctx, tx, priorRun, prior, prior, repository, 0, priorOutcome)
	if err != nil {
		return prior, err
	}
	if err = admitRetryReservationTx(ctx, tx, priorRun, prior, repository, run); err != nil {
		return prior, err
	}
	return prior, nil
}

// A second verification of the same version requires the exact command-owned
// root. All native/current-input/funding checks still run in the normal packet.
func validateDeliveryRetryPacketTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	version, _ := json.Marshal(a.Subject.Delivery)
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_assignments WHERE repository=$1 AND issue=0
		AND data->>'phase'='verify' AND data->'subject'->>'kind'='delivery'
		AND (data->'subject'->'delivery')-'delivery_id'=($2::jsonb)-'delivery_id')`, a.Repository, string(version)).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var cmd factory.Command
	if err := tx.QueryRowContext(ctx, `SELECT id,type,payload FROM factory_commands WHERE id=$1 AND type=$2 AND finished<>'' FOR UPDATE`, a.ID, factory.CommandRetry).Scan(&cmd.ID, &cmd.Type, &cmd.Payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrExplicitRetryIneligible
		}
		return err
	}
	decision, err := cmd.RetryDecision()
	if err != nil {
		return err
	}
	admitted, err := admitRetryCommandTx(ctx, tx, decision)
	if err != nil {
		return err
	}
	prior, err := deliveryRetryPriorTx(ctx, tx, a.Repository, decision.Prior, admitted.priorOutcome)
	if err != nil {
		return err
	}
	if !sameDeliveryVersion(*prior.Subject.Delivery, *a.Subject.Delivery) || a.ProjectID != prior.ProjectID || a.Subject.Delivery.DeliveryID != factory.DeliveryRetryID(*a.Subject.Delivery, decision.CommandID) {
		return ErrExplicitRetryIneligible
	}
	return nil
}
