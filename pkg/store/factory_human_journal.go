package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// Provider observations and local dispatch cancellation share the same reducer
// under the command row lock. Retained SQL grants no current provider authority.
type humanJournal struct {
	host                       collaboration.HostID
	repository, headRepository collaboration.RepositoryID
	actor, reviewer            collaboration.ActorID
	team                       collaboration.TeamID
	authority, operation       string
	invalid                    error
	validate                   func(collaboration.OperationLookup) error
	advance                    func(collaboration.OperationLookup, collaboration.OperationLookup) (collaboration.OperationLookup, error)
	finished                   func(collaboration.OperationLookup) bool
	ready                      func() bool
	saveFailure                string
}

func humanJournalCommand(cmd factory.Command) (humanJournal, error) {
	var s humanJournal
	switch cmd.Type {
	case factory.CommandRefPublish:
		c, err := factory.DecodeRefPublicationCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrRefPublicationObservation, validate: func(o collaboration.OperationLookup) error { return factory.ValidateRefPublicationObservation(c, o) }, finished: factory.RefPublicationFinished, advance: func(old, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
			return factory.AdvanceRefPublicationObservation(c, old, next)
		}, saveFailure: "ref publication observation save failed"}

	case factory.CommandHumanComment:
		c, err := factory.DecodeHumanCommentCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanCommentObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanCommentObservation(c, o) }, finished: factory.HumanCommentFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanCommentObservation(c, prior, next)
			}, saveFailure: "human comment observation save failed"}
	case factory.CommandHumanIssueCreate:
		c, err := factory.DecodeHumanIssueCreateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanIssueCreateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanIssueCreateObservation(c, o) }, finished: factory.HumanIssueCreateFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanIssueCreateObservation(c, prior, next)
			}, saveFailure: "human issue creation observation save failed"}
	case factory.CommandHumanState:
		c, err := factory.DecodeHumanStateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanStateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanStateObservation(c, o) }, finished: factory.HumanStateFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanStateObservation(c, prior, next)
			}, saveFailure: "human state observation save failed"}
	case factory.CommandHumanEdit:
		c, err := factory.DecodeHumanEditCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanEditObservation,
			ready:    c.ExpectedCurrent,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanEditObservation(c, o) }, finished: factory.HumanEditFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanEditObservation(c, prior, next)
			}, saveFailure: "human edit observation save failed"}
	case factory.CommandHumanLock:
		c, err := factory.DecodeHumanLockCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanLockObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanLockObservation(c, o) }, finished: factory.HumanLockFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanLockObservation(c, prior, next)
			}, saveFailure: "human lock observation save failed"}
	case factory.CommandHumanThread:
		c, err := factory.DecodeHumanThreadCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanThreadObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanThreadObservation(c, o) }, finished: factory.HumanThreadFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanThreadObservation(c, prior, next)
			}, saveFailure: "human thread observation save failed"}
	case factory.CommandHumanReviewRequest:
		c, err := factory.DecodeHumanReviewRequestCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{reviewer: c.Request.Reviewer, team: c.Request.Team, host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanReviewRequestObservation,
			validate: func(o collaboration.OperationLookup) error {
				return factory.ValidateHumanReviewRequestObservation(c, o)
			}, finished: factory.HumanReviewRequestFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanReviewRequestObservation(c, prior, next)
			}, saveFailure: "human reviewer observation save failed"}
	case factory.CommandHumanPullCreate:
		c, err := factory.DecodeHumanPullCreateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{headRepository: c.Request.HeadRepository, host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanPullCreateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanPullCreateObservation(c, o) }, finished: factory.HumanPullCreateFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanPullCreateObservation(c, prior, next)
			}, saveFailure: "human pull creation observation save failed"}
	case factory.CommandHumanReview:
		c, err := factory.DecodeHumanReviewCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanReviewObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanReviewObservation(c, o) }, finished: factory.HumanReviewFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanReviewObservation(c, prior, next)
			}, saveFailure: "human review observation save failed"}
	case factory.CommandHumanBranchCreate:
		c, err := factory.DecodeHumanBranchCreateCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanBranchCreateObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanBranchCreateObservation(c, o) }, finished: factory.HumanBranchCreateFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanBranchCreateObservation(c, prior, next)
			}, saveFailure: "human branch creation observation save failed"}
	case factory.CommandHumanInline:
		c, err := factory.DecodeHumanInlineCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanInlineObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanInlineObservation(c, o) }, finished: factory.HumanInlineFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanInlineObservation(c, prior, next)
			}, saveFailure: "human inline observation save failed"}
	case factory.CommandHumanMerge:
		c, err := factory.DecodeHumanMergeCapture(cmd)
		if err != nil {
			return s, err
		}
		s = humanJournal{host: c.Request.Host, repository: c.Request.Repository, actor: c.Request.Actor, authority: c.Authority, operation: c.OperationID, invalid: factory.ErrHumanMergeObservation,
			validate: func(o collaboration.OperationLookup) error { return factory.ValidateHumanMergeObservation(c, o) }, finished: factory.HumanMergeFinished,
			advance: func(prior, next collaboration.OperationLookup) (collaboration.OperationLookup, error) {
				return factory.AdvanceHumanMergeObservation(c, prior, next)
			}, saveFailure: "human merge observation save failed"}
	default:
		return s, ErrCommandConflict
	}
	return s, nil
}

const humanCommandQuery = `SELECT id,type,target,principal,digest,payload,outcome,created,finished FROM factory_commands WHERE id=$1`

func scanHumanCommand(row *sql.Row) (factory.Command, error) {
	var saved factory.Command
	err := row.Scan(&saved.ID, &saved.Type, &saved.Target, &saved.Principal, &saved.Digest, &saved.Payload, &saved.Outcome, &saved.Created, &saved.Finished)
	return saved, err
}

func (j humanJournal) checkBinding(ctx context.Context, tx *sql.Tx, kind string) error {
	var bound bool
	var err error
	switch kind {
	case factory.CommandHumanPullCreate:
		err = tx.QueryRowContext(ctx, `SELECT l.adopted AND EXISTS(SELECT 1 FROM collaboration_repositories r JOIN collaboration_actors a ON a.host=r.host JOIN collaboration_repositories h ON h.host=r.host WHERE r.host=$1 AND r.id=$2 AND a.id=$3 AND h.id=$4) FROM collaboration_ledger l WHERE singleton FOR SHARE`, j.host, j.repository, j.actor, j.headRepository).Scan(&bound)
	case factory.CommandHumanReviewRequest:
		err = tx.QueryRowContext(ctx, `SELECT l.adopted AND EXISTS(SELECT 1 FROM collaboration_repositories r JOIN collaboration_actors a ON a.host=r.host WHERE r.host=$1 AND r.id=$2 AND a.id=$3) AND (($4::bigint>0 AND $5::bigint=0 AND EXISTS(SELECT 1 FROM collaboration_actors WHERE host=$1 AND id=$4)) OR ($5::bigint>0 AND $4::bigint=0 AND EXISTS(SELECT 1 FROM collaboration_teams WHERE host=$1 AND id=$5))) FROM collaboration_ledger l WHERE singleton FOR SHARE`, j.host, j.repository, j.actor, j.reviewer, j.team).Scan(&bound)
	default:
		err = tx.QueryRowContext(ctx, `SELECT l.adopted AND EXISTS(SELECT 1 FROM collaboration_repositories r JOIN collaboration_actors a ON a.host=r.host WHERE r.host=$1 AND r.id=$2 AND a.id=$3) FROM collaboration_ledger l WHERE singleton FOR SHARE`, j.host, j.repository, j.actor).Scan(&bound)
	}
	if err != nil {
		return err
	}
	if !bound {
		return collaboration.ErrUnboundLedger
	}
	return nil
}

type humanCapture interface {
	Command() (factory.Command, error)
}

// Dispatch only the returned winning capture. Concurrent preparations may carry
// different snapshots or deadlines; replay preserves the original saved bytes.
func captureHumanCommand[C humanCapture](ctx context.Context, s *Store, capture C, decode func(factory.Command) (C, error), matches func(C) bool) (C, bool, error) {
	var empty C
	cmd, err := capture.Command()
	if err != nil {
		return empty, false, err
	}
	journal, err := humanJournalCommand(cmd)
	if err != nil {
		return empty, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback()
	if err := journal.checkBinding(ctx, tx, cmd.Type); err != nil {
		return empty, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, cmd.ID, cmd.Type, cmd.Target, cmd.Principal, cmd.Digest, cmd.Payload, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return empty, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return empty, false, err
	}
	stored, err := scanHumanCommand(tx.QueryRowContext(ctx, humanCommandQuery+` FOR UPDATE`, cmd.ID))
	if err != nil {
		return empty, false, err
	}
	if stored.Type != cmd.Type {
		return empty, false, ErrCommandConflict
	}
	saved, err := decode(stored)
	if err != nil {
		return empty, false, err
	}
	if !matches(saved) {
		return empty, false, ErrCommandConflict
	}
	if err := tx.Commit(); err != nil {
		return empty, false, err
	}
	return saved, n == 0, nil
}

// Absence is ErrNotFound, never proof of a remote outcome. Call before preparing
// another provider snapshot and only under current human admission.
func lookupHumanCapture[C any](ctx context.Context, s *Store, id, kind string, decode func(factory.Command) (C, error), matches func(C) bool) (C, error) {
	var empty C
	cmd, err := s.FactoryCommand(ctx, id)
	if err != nil {
		return empty, err
	}
	if cmd.Type != kind {
		return empty, ErrCommandConflict
	}
	saved, err := decode(cmd)
	if err != nil {
		return empty, err
	}
	if !matches(saved) {
		return empty, ErrCommandConflict
	}
	return saved, nil
}

func observeHumanCommand[C humanCapture](ctx context.Context, s *Store, capture C, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	cmd, err := capture.Command()
	if err != nil {
		return empty, err
	}
	journal, err := humanJournalCommand(cmd)
	if err != nil {
		return empty, err
	}
	if err := journal.validate(observation); err != nil {
		return empty, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	stored, err := scanHumanCommand(tx.QueryRowContext(ctx, humanCommandQuery+` FOR UPDATE`, cmd.ID))
	if err != nil {
		return empty, err
	}
	if !sameHumanCommand(stored, cmd) {
		return empty, ErrCommandConflict
	}
	if _, err := humanJournalCommand(stored); err != nil {
		return empty, err
	}
	observation, err = journal.saveObservation(ctx, tx, stored, observation)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return observation, nil
}

func sameHumanCommand(saved, want factory.Command) bool {
	return saved.ID == want.ID && saved.Type == want.Type && saved.Target == want.Target && saved.Principal == want.Principal && saved.Digest == want.Digest && saved.Payload == want.Payload
}

// LookupHumanCommandDispatch reads local dispatch history only. The adapter must
// admit its current account and decode the saved provider intent before calling;
// neither an unclaimed row nor an absent reply grants provider authority.
func (s *Store) LookupHumanCommandDispatch(ctx context.Context, cmd factory.Command) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	state, err := s.humanDispatchState(ctx, cmd)
	if err != nil {
		return empty, err
	}
	stored, err := s.FactoryCommand(ctx, cmd.ID)
	if err != nil {
		return empty, err
	}
	journal, err := humanJournalCommand(cmd)
	if err != nil {
		return empty, err
	}
	if stored.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(stored.Outcome), &prior) != nil || journal.validate(prior) != nil {
			return empty, journal.invalid
		}
		if prior.Record != nil {
			return prior, nil
		}
	}
	lookup := collaboration.OperationLookup{Host: journal.host, Authority: journal.authority, OperationID: journal.operation, State: collaboration.LookupNotObserved, Reason: "local_dispatch_unclaimed"}
	if state != factory.DispatchUnclaimed {
		record := &collaboration.OperationRecord{Host: journal.host, Authority: journal.authority, OperationID: journal.operation,
			Binding: &collaboration.OperationBinding{Repository: journal.repository, Actor: journal.actor, Kind: collaboration.OperationKind(cmd.Type), Atomicity: collaboration.Atomic, IntentDigest: cmd.Digest},
			Effect:  collaboration.EffectIndeterminate, Cancellation: collaboration.CancellationNone, Completion: collaboration.CompletionNeedsIntervention, Reason: "dispatch_history_unresolved"}
		if state == factory.DispatchLegacyUnknown {
			record.Reason = "legacy_dispatch_unknown"
		}
		lookup.State, lookup.Record = collaboration.LookupFound, record
		lookup.Reason = record.Reason
	}
	return lookup, nil
}

// The caller holds the exact captured command's row lock. No network runs in
// this transaction; terminal receipts and complete outcomes remain immutable.
func (j humanJournal) saveObservation(ctx context.Context, tx *sql.Tx, stored factory.Command, observation collaboration.OperationLookup) (collaboration.OperationLookup, error) {
	var empty collaboration.OperationLookup
	if err := j.validate(observation); err != nil {
		return empty, err
	}
	if stored.Outcome != "" {
		var prior collaboration.OperationLookup
		if json.Unmarshal([]byte(stored.Outcome), &prior) != nil {
			return empty, j.invalid
		}
		var err error
		observation, err = j.advance(prior, observation)
		if err != nil {
			return empty, err
		}
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return empty, err
	}
	if len(raw) > 64<<10 {
		return empty, j.invalid
	}
	if stored.Finished != "" {
		if !j.finished(observation) || string(raw) != stored.Outcome {
			return empty, j.invalid
		}
	} else if string(raw) != stored.Outcome {
		finished := ""
		if j.finished(observation) {
			finished = time.Now().UTC().Format(time.RFC3339Nano)
		}
		result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET outcome=$2,finished=$3 WHERE id=$1 AND finished=''`, stored.ID, string(raw), finished)
		if err := checkGrantRowsAffected(result, err, j.saveFailure); err != nil {
			return empty, err
		}
	}
	return observation, nil
}
