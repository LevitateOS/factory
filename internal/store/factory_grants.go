package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/levitateos/sodaos/internal/factory"
)

// ErrStaleRevision reports a CAS write against a superseded grant, dispatch
// or decision revision. Callers refresh and retry with a new command.
var ErrStaleRevision = errors.New("stale record revision")

// ErrDispatchClosed reports a registration against withdrawn dispatch. The
// caller waits for an authorized resume instead of dispatching around it.
var ErrDispatchClosed = errors.New("factory dispatch is closed")

// saveRevisionedGrant CAS-saves one revisioned JSON grant row. Withdrawn
// grants stay as inactive rows, never deleted ones.
func (s *Store) saveRevisionedGrant(ctx context.Context, table, key string, id any, revision int64, next any, what string) error {
	return saveRevisionedGrantQuery(ctx, s.db, table, key, id, revision, next, what)
}

func (s *Store) loadGrant(ctx context.Context, table, key string, id any, out any) error {
	return loadGrantQuery(ctx, s.db, table, key, id, out)
}

func loadGrantQuery(ctx context.Context, q grantCommandSQL, table, key string, id any, out any) error {
	var data []byte
	err := q.QueryRowContext(ctx, `SELECT data FROM `+table+` WHERE `+key+`=$1`, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, out)
	}
	return err
}

// SaveRepositoryPolicy records the owner's policy under CAS revision.
func (s *Store) SaveRepositoryPolicy(ctx context.Context, p factory.RepositoryPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	next := p
	next.Roles = maps.Clone(p.Roles)
	for role, selection := range next.Roles {
		selection.Profile = factory.EffectiveExecutionProfile(&selection.Profile)
		next.Roles[role] = selection
	}
	next.Revision++
	return s.saveRevisionedGrant(ctx, "factory_policies", "repository", p.Repository, p.Revision, next, "repository policy")
}

// RepositoryPolicy returns the standing policy for one repository.
func (s *Store) RepositoryPolicy(ctx context.Context, repository int64) (factory.RepositoryPolicy, error) {
	var p factory.RepositoryPolicy
	return p, s.loadGrant(ctx, "factory_policies", "repository", repository, &p)
}

// SaveCapacity records the appliance capacity under CAS revision.
func (s *Store) SaveCapacity(ctx context.Context, c factory.Capacity) error {
	if err := c.Validate(); err != nil {
		return err
	}
	next := c
	next.Revision++
	return s.saveRevisionedGrant(ctx, "factory_capacity", "id", 1, c.Revision, next, "appliance capacity")
}

// Capacity returns the appliance capacity record.
func (s *Store) Capacity(ctx context.Context) (factory.Capacity, error) {
	var c factory.Capacity
	return c, s.loadGrant(ctx, "factory_capacity", "id", 1, &c)
}

// SaveOperatorGrant records the operator's repository permission under CAS.
func (s *Store) SaveOperatorGrant(ctx context.Context, g factory.OperatorGrant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	next := g
	next.Revision++
	return s.saveRevisionedGrant(ctx, "factory_operator_grants", "repository", g.Repository, g.Revision, next, "operator grant")
}

// OperatorGrant returns the operator permission for one repository.
func (s *Store) OperatorGrant(ctx context.Context, repository int64) (factory.OperatorGrant, error) {
	var g factory.OperatorGrant
	return g, s.loadGrant(ctx, "factory_operator_grants", "repository", repository, &g)
}

// SaveSponsorship records one connection sponsorship under CAS revision.
func (s *Store) SaveSponsorship(ctx context.Context, sp factory.Sponsorship) error {
	if err := sp.Validate(); err != nil {
		return err
	}
	next := sp
	next.Revision++
	return saveSponsorshipQuery(ctx, s.db, sp, next)
}

// Sponsorship returns one connection sponsorship for a repository.
func (s *Store) Sponsorship(ctx context.Context, repository int64, connection string) (factory.Sponsorship, error) {
	var sp factory.Sponsorship
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 AND connection=$2`, repository, connection).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &sp)
	}
	return sp, err
}

// Sponsorships lists the recorded connection sponsorships for a repository.
func (s *Store) Sponsorships(ctx context.Context, repository int64) ([]factory.Sponsorship, error) {
	return sponsorshipsQuery(ctx, s.db, repository)
}

func sponsorshipsQuery(ctx context.Context, q grantCommandSQL, repository int64) ([]factory.Sponsorship, error) {
	rows, err := q.QueryContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 ORDER BY connection LIMIT 33`, repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []factory.Sponsorship{}
	for rows.Next() {
		var data []byte
		var sp factory.Sponsorship
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &sp); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// DispatchState returns the dispatch gate for one repository. A missing row
// means dispatch was never withdrawn: open at revision zero.
func (s *Store) DispatchState(ctx context.Context, repository int64) (bool, int64, factory.Withdrawal, error) {
	return dispatchStateQuery(ctx, s.db, repository)
}

func dispatchStateQuery(ctx context.Context, q grantCommandSQL, repository int64) (bool, int64, factory.Withdrawal, error) {
	var open bool
	var revision int64
	var data []byte
	var withdrawal factory.Withdrawal
	err := q.QueryRowContext(ctx, `SELECT open,revision,data FROM factory_dispatch WHERE repository=$1`, repository).Scan(&open, &revision, &data)
	if errors.Is(err, ErrNotFound) {
		return true, 0, factory.Withdrawal{}, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &withdrawal)
	}
	if err == nil && !open {
		if err = withdrawal.Validate(); err == nil && len(withdrawal.ActiveCauses) == 0 {
			err = errors.New("closed dispatch has no active cause")
		}
	} else if err == nil && len(withdrawal.ActiveCauses) != 0 {
		err = errors.New("open dispatch has active closure causes")
	}
	return open, revision, withdrawal, err
}

// WithdrawDispatch atomically closes dispatch for one repository. Repeated
// requests retain the first closure and add any independent active cause.
func (s *Store) WithdrawDispatch(ctx context.Context, repository int64, cause, closedBy string) (factory.Withdrawal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Withdrawal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	withdrawal, err := withdrawDispatchTx(ctx, tx, repository, cause, closedBy)
	if err != nil {
		return factory.Withdrawal{}, err
	}
	if err = tx.Commit(); err != nil {
		return factory.Withdrawal{}, err
	}
	return withdrawal, nil
}

// ensureDispatchGateTx materializes and locks the repository gate. Grant
// commands, dispatch packets and other repository writers take this
// lock before their own records to preserve one lock order.
func ensureDispatchGateTx(ctx context.Context, tx *sql.Tx, repository int64) (bool, int64, []byte, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_dispatch(repository,revision,open,data)
		VALUES($1,0,TRUE,'{}'::jsonb) ON CONFLICT(repository) DO NOTHING`, repository); err != nil {
		return false, 0, nil, err
	}
	var open bool
	var revision int64
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT open,revision,data FROM factory_dispatch WHERE repository=$1 FOR UPDATE`, repository).Scan(&open, &revision, &raw)
	return open, revision, raw, err
}

// checkDispatchOpenTx holds the gate lock until the admitted packet commits.
func checkDispatchOpenTx(ctx context.Context, tx *sql.Tx, repository int64) error {
	open, _, _, err := ensureDispatchGateTx(ctx, tx, repository)
	if err != nil {
		return err
	}
	if !open {
		return ErrDispatchClosed
	}
	return nil
}

// withdrawDispatchTx locks and closes the gate in the caller's transaction,
// so closure commits with the settings change.
func withdrawDispatchTx(ctx context.Context, tx *sql.Tx, repository int64, cause, closedBy string) (factory.Withdrawal, error) {
	if repository <= 0 || cause == "" || len(cause) > 256 || closedBy == "" || len(closedBy) > 128 {
		return factory.Withdrawal{}, errors.New("invalid dispatch withdrawal")
	}
	// Dispatch and publication admission take this same lock.
	open, revision, raw, err := ensureDispatchGateTx(ctx, tx, repository)
	if err != nil {
		return factory.Withdrawal{}, err
	}
	var recorded factory.Withdrawal
	if err = json.Unmarshal(raw, &recorded); err != nil {
		return factory.Withdrawal{}, err
	}
	if !open {
		return replayDispatchWithdrawal(ctx, tx, repository, revision, recorded, cause)
	}
	return closeDispatchGateTx(ctx, tx, repository, revision, cause, closedBy)
}

func replayDispatchWithdrawal(ctx context.Context, tx *sql.Tx, repository, revision int64, recorded factory.Withdrawal, cause string) (factory.Withdrawal, error) {
	if err := recorded.Validate(); err != nil || len(recorded.ActiveCauses) == 0 {
		if err == nil {
			err = errors.New("closed dispatch has no active cause")
		}
		return factory.Withdrawal{}, err
	}
	for _, active := range recorded.ActiveCauses {
		if active == cause {
			return recorded, nil
		}
	}
	if len(recorded.ActiveCauses) >= factory.MaxActiveDispatchCauses {
		return factory.Withdrawal{}, errors.New("too many active dispatch causes")
	}
	recorded.ActiveCauses = append(recorded.ActiveCauses, cause)
	if err := recorded.Validate(); err != nil {
		return factory.Withdrawal{}, err
	}
	nextRevision := revision + 1
	data, marshalErr := json.Marshal(recorded)
	if marshalErr != nil {
		return factory.Withdrawal{}, marshalErr
	}
	result, updateErr := tx.ExecContext(ctx, `UPDATE factory_dispatch SET revision=$1,data=$2
		WHERE repository=$3 AND revision=$4 AND NOT open`, nextRevision, string(data), repository, revision)
	if updateErr != nil {
		return factory.Withdrawal{}, fmt.Errorf("dispatch cause update failed: %w", updateErr)
	}
	n, affectedErr := result.RowsAffected()
	if affectedErr != nil {
		return factory.Withdrawal{}, affectedErr
	}
	if n != 1 {
		return factory.Withdrawal{}, ErrStaleRevision
	}
	return recorded, nil
}

func closeDispatchGateTx(ctx context.Context, tx *sql.Tx, repository, revision int64, cause, closedBy string) (factory.Withdrawal, error) {
	withdrawal := factory.Withdrawal{Repository: repository, Revision: revision + 1, Cause: cause, ClosedBy: closedBy, ActiveCauses: []string{cause}}
	if err := withdrawal.Validate(); err != nil {
		return factory.Withdrawal{}, err
	}
	data, err := json.Marshal(withdrawal)
	if err != nil {
		return factory.Withdrawal{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_dispatch SET revision=$1,open=FALSE,data=$2
		WHERE repository=$3 AND revision=$4 AND open`, withdrawal.Revision, string(data), repository, revision)
	if err != nil {
		return factory.Withdrawal{}, fmt.Errorf("dispatch withdrawal failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return factory.Withdrawal{}, err
	}
	if n != 1 {
		return factory.Withdrawal{}, ErrStaleRevision
	}
	var assigned int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_assignments WHERE repository=$1 AND stage=$2`, repository, factory.AssignmentAssigned).Scan(&assigned); err != nil {
		return factory.Withdrawal{}, err
	}
	if err = recordFactoryFeedEventTx(ctx, tx, repository, 0, "", fmt.Sprintf("pause:%d", withdrawal.Revision), "dispatch:paused", factory.FeedCommunication{
		ComponentID: "F08", Title: "Factory paused", Summary: cause,
		Items: []factory.FeedCommunicationItem{{Label: "New worker dispatch", State: "paused"}, {Label: "Outstanding work", Detail: fmt.Sprintf("%d assigned work items", assigned), State: "stopping"}},
	}, 0); err != nil {
		return factory.Withdrawal{}, err
	}
	return withdrawal, nil
}

// ReopenDispatch clears non-Project-stop causes under the locked dispatch
// row. A Project stop remains in force until a verified Project start clears it.
func (s *Store) ReopenDispatch(ctx context.Context, repository, expectedRevision int64) (bool, int64, error) {
	return s.clearDispatchCauses(ctx, repository, expectedRevision, true)
}

// ClearProjectStop removes only the Project lifecycle cause after verified
// start and run quiescence. First-closure attribution remains a receipt.
func (s *Store) ClearProjectStop(ctx context.Context, repository, expectedRevision int64) (bool, int64, error) {
	return s.clearDispatchCauses(ctx, repository, expectedRevision, false)
}

// clearDispatchCauses is the single row-lock/CAS transaction for the two
// authorized cause-clear operations. Resume retains project_stop; verified
// Start removes only project_stop.
func filterDispatchCauses(active []string, retainProjectStop bool) []string {
	remaining := make([]string, 0, len(active))
	for _, cause := range active {
		keep := cause == factory.CauseProjectStop
		if !retainProjectStop {
			keep = cause != factory.CauseProjectStop
		}
		if keep {
			remaining = append(remaining, cause)
		}
	}
	return remaining
}

func loadDispatchWithdrawal(ctx context.Context, tx *sql.Tx, repository, expectedRevision int64) (factory.Withdrawal, int64, error) {
	var empty factory.Withdrawal
	var open bool
	var revision int64
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT open,revision,data FROM factory_dispatch WHERE repository=$1 FOR UPDATE`, repository).Scan(&open, &revision, &raw); err != nil {
		return empty, 0, err
	}
	if open || revision != expectedRevision {
		return empty, revision, ErrStaleRevision
	}
	var withdrawal factory.Withdrawal
	if err := json.Unmarshal(raw, &withdrawal); err != nil {
		return empty, revision, err
	}
	if err := withdrawal.Validate(); err != nil || len(withdrawal.ActiveCauses) == 0 {
		if err == nil {
			err = errors.New("closed dispatch has no active cause")
		}
		return empty, revision, err
	}
	return withdrawal, revision, nil
}

func (s *Store) clearDispatchCauses(ctx context.Context, repository, expectedRevision int64, retainProjectStop bool) (bool, int64, error) {
	if repository <= 0 || expectedRevision < 0 {
		return false, 0, errors.New("invalid dispatch cause clear")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	withdrawal, revision, err := loadDispatchWithdrawal(ctx, tx, repository, expectedRevision)
	if err != nil {
		return false, revision, err
	}
	remaining := filterDispatchCauses(withdrawal.ActiveCauses, retainProjectStop)
	if len(remaining) == len(withdrawal.ActiveCauses) {
		if err = tx.Commit(); err != nil {
			return false, revision, err
		}
		return false, revision, nil
	}
	withdrawal.ActiveCauses = remaining
	nextRevision := revision + 1
	data, err := json.Marshal(withdrawal)
	if err != nil {
		return false, revision, fmt.Errorf("dispatch cause clear failed: %w", err)
	}
	nextOpen := len(remaining) == 0
	result, err := tx.ExecContext(ctx, `UPDATE factory_dispatch SET revision=$1,open=$2,data=$3 WHERE repository=$4 AND revision=$5 AND NOT open`,
		nextRevision, nextOpen, string(data), repository, revision)
	if err != nil {
		return false, revision, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, revision, err
	}
	if n != 1 {
		return false, revision, ErrStaleRevision
	}
	state, title, summary := "paused", "Factory pause updated", "Some stop causes remain active."
	if nextOpen {
		state, title, summary = "resumed", "Factory resumed", "The recorded stop causes are cleared. Eligible work can be selected again."
	}
	if err = recordFactoryFeedEventTx(ctx, tx, repository, 0, "", fmt.Sprintf("resume:%d", nextRevision), "dispatch:"+state, factory.FeedCommunication{
		ComponentID: "F08", Title: title, Summary: summary, Items: []factory.FeedCommunicationItem{{Label: "Worker dispatch", State: state}},
	}, 0); err != nil {
		return false, revision, err
	}
	if err = tx.Commit(); err != nil {
		return false, revision, err
	}
	return nextOpen, nextRevision, nil
}
