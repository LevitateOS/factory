package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

var ErrCompletionRetryIneligible = errors.New("completion retry is no longer admissible")

// RecordCompletionRetryCommand admits the owner's request and its single
// successor atomically. The previous cycle and paid ledger stay intact;
// responses derive from the request instead of a separately saved outcome.
func (s *Store) RecordCompletionRetryCommand(ctx context.Context, cmd factory.Command, repository int64, request factory.CompletionRetryRequest, now time.Time) (factory.CompletionRetryDecision, error) {
	var zero factory.CompletionRetryDecision
	payload, _ := json.Marshal(request)
	if request.Validate() != nil || repository <= 0 || cmd.Type != factory.CommandCompletionRetry ||
		cmd.Target != fmt.Sprintf("repository/%d/completion/%s", repository, request.EventID) || cmd.Payload != string(payload) {
		return zero, ErrCompletionRetryIneligible
	}
	decision := factory.CompletionRetryDecision{CommandID: cmd.ID, EventID: request.EventID, PriorCycle: request.ExpectedPriorCycle, SuccessorCycle: cmd.ID, Repository: repository, Queued: true}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return zero, err
	}
	if !created {
		if stored.Principal != cmd.Principal {
			return zero, ErrCommandConflict
		}
		return decision, tx.Commit()
	}
	retry := factory.ReassessmentRetry{CommandID: cmd.ID, EventID: request.EventID, ExpectedPriorCycle: request.ExpectedPriorCycle, SuccessorCycleID: cmd.ID}
	if _, err = startReassessmentRetryTx(ctx, tx, retry, repository); err != nil {
		return zero, err
	}
	if err := finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
		return zero, err
	}
	return decision, tx.Commit()
}
