package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// This file owns D15 repair-grant and intent-execution admission on the
// shared command ledger. No new table is introduced: schema registration
// stays coordinator-serialized, so repair grants, immutable charges and
// intent aggregate bounds ride the existing factory_commands journal
// with their own command types. Consumption derives from existing
// allowance rows, never from the commands themselves, so retries,
// revisions, splits and replacement issues cannot refill anything.

// repairChargeTarget scopes every charge under its repository grant.
// Charges outlive grant revisions: an explicit increase preserves the
// whole history, so the scope key carries the repository, not one
// revision.
func repairChargeTarget(repository int64) string {
	return factory.RepairTarget(repository) + "/charges"
}

// finishedCommands returns every finished command for one type and
// target. Rows are few per target; callers pick the head by the
// document revision, never by wall-clock timestamps that can tie.
func finishedCommands(ctx context.Context, q grantCommandSQL, typ, target string) ([]factory.Command, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,type,target,principal,digest,payload,outcome,created,finished FROM factory_commands WHERE type=$1 AND target=$2 AND finished<>'' ORDER BY created DESC, id DESC`, typ, target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Command
	for rows.Next() {
		var cmd factory.Command
		var created, finished string
		if err := rows.Scan(&cmd.ID, &cmd.Type, &cmd.Target, &cmd.Principal, &cmd.Digest, &cmd.Payload, &cmd.Outcome, &created, &finished); err != nil {
			return nil, err
		}
		cmd.Created, cmd.Finished = created, finished
		out = append(out, cmd)
	}
	return out, rows.Err()
}

// ApplyRepairGrantCommand records one explicit repository repair grant
// revision. The predecessor must name the currently finished grant
// command, empty for the first grant. A limit increase above the
// predecessor must state its effect; consumption and history carry
// forward in the separate charge rows, so an increase never refills.
func (s *Store) ApplyRepairGrantCommand(ctx context.Context, cmd factory.Command, grant factory.RepairGrant, now time.Time) (factory.Command, bool, error) {
	if err := admitRepairGrantCommand(cmd, grant); err != nil {
		return factory.Command{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Command{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, factory.RepairTarget(grant.Repository)); err != nil {
		return factory.Command{}, false, err
	}
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return factory.Command{}, false, err
	}
	if !created {
		return stored, false, tx.Commit()
	}
	heads, err := finishedCommands(ctx, tx, factory.CommandRepairGrant, cmd.Target)
	if err != nil {
		return factory.Command{}, false, err
	}
	if err = checkRepairGrantHead(grant, heads); err != nil {
		return factory.Command{}, false, err
	}
	return finishGrantCommandTx(ctx, tx, stored, now)
}

// admitRepairGrantCommand validates one repair grant command against
// its change.
func admitRepairGrantCommand(cmd factory.Command, grant factory.RepairGrant) error {
	if err := grant.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	if cmd.Type != factory.CommandRepairGrant || cmd.Target != factory.RepairTarget(grant.Repository) ||
		string(payload) != cmd.Payload {
		return ErrCommandConflict
	}
	return nil
}

// finishGrantCommandTx marks the grant admitted in the same transaction.
func finishGrantCommandTx(ctx context.Context, tx *sql.Tx, stored factory.Command, now time.Time) (factory.Command, bool, error) {
	if err := finishFactoryCommandTx(ctx, tx, stored.ID, now); err != nil {
		return factory.Command{}, false, err
	}
	stored.Finished = now.UTC().Format(time.RFC3339Nano)
	if err := tx.Commit(); err != nil {
		return factory.Command{}, false, err
	}
	return stored, true, nil
}

// checkRepairGrantHead requires one repair grant to chain its head's
// predecessor with a stated effect behind increases.
func checkRepairGrantHead(grant factory.RepairGrant, heads []factory.Command) error {
	// The recorded row above is still unfinished, so the finished
	// heads below exclude it: the head is the predecessor this
	// revision must name.
	if len(heads) == 0 {
		if grant.Predecessor != "" || grant.Revision != 0 {
			return ErrStaleRevision
		}
		return nil
	}
	head, err := headRepairGrant(heads)
	if err != nil {
		return err
	}
	var old factory.RepairGrant
	if err := json.Unmarshal([]byte(head.Payload), &old); err != nil {
		return err
	}
	if grant.Predecessor != head.ID {
		return ErrStaleRevision
	}
	if grant.Revision != old.Revision+1 || grant.Repository != old.Repository || grant.Sponsor != old.Sponsor {
		return errors.New("repair grant identity is immutable")
	}
	if (grant.InvestigatorRuns > old.InvestigatorRuns || grant.ProposalJudgments > old.ProposalJudgments ||
		grant.ActiveMinutes > old.ActiveMinutes) && grant.Effect == "" {
		return errors.New("repair grant increase must state its effect")
	}
	return nil
}

// headRepairGrant selects the current head by document revision: the
// highest finished revision wins, independent of commit timestamps.
func headRepairGrant(heads []factory.Command) (factory.Command, error) {
	var head factory.Command
	picked := false
	var revision int64 = -1
	for _, cmd := range heads {
		var grant factory.RepairGrant
		if err := json.Unmarshal([]byte(cmd.Payload), &grant); err != nil {
			return factory.Command{}, err
		}
		if !picked || grant.Revision > revision {
			head, revision, picked = cmd, grant.Revision, true
		}
	}
	if !picked {
		return factory.Command{}, ErrNotFound
	}
	return head, nil
}

// RepairGrantCommand returns the currently finished repair grant for
// one repository.
func (s *Store) RepairGrantCommand(ctx context.Context, repository int64) (factory.RepairGrant, error) {
	heads, err := finishedCommands(ctx, s.db, factory.CommandRepairGrant, factory.RepairTarget(repository))
	if err != nil {
		return factory.RepairGrant{}, err
	}
	head, err := headRepairGrant(heads)
	if err != nil {
		return factory.RepairGrant{}, err
	}
	var grant factory.RepairGrant
	if err := json.Unmarshal([]byte(head.Payload), &grant); err != nil {
		return factory.RepairGrant{}, err
	}
	return grant, nil
}

// RepairCharges lists every recorded charge under one repository
// grant scope in spend order, across all grant revisions. An explicit
// increase preserves this history; nothing is orphaned by a raise.
func (s *Store) RepairCharges(ctx context.Context, repository int64) ([]factory.RepairCharge, error) {
	return repairCharges(ctx, s.db, repository)
}

func repairCharges(ctx context.Context, q grantCommandSQL, repository int64) ([]factory.RepairCharge, error) {
	rows, err := q.QueryContext(ctx, `SELECT payload FROM factory_commands WHERE type='repair-charge' AND target=$1 AND finished<>'' ORDER BY created ASC, id ASC`, repairChargeTarget(repository))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.RepairCharge
	for rows.Next() {
		var raw string
		var charge factory.RepairCharge
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &charge); err != nil {
			return nil, err
		}
		out = append(out, charge)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// AdmitStallInvestigation admits one repair run or proposal judgment
// against the repository's explicit grant. The grant must exist and be
// active; episode cycles, run, judgment and active-minute bounds come
// from the immutable charge history, so duplicate ticks, restarts,
// hash changes, issue replacement, graph reshuffles and retries never
// replenish room. The charge persists before the caller launches; the result
// reports whether this call admitted a new charge.
func (s *Store) AdmitStallInvestigation(ctx context.Context, repository int64, charge factory.RepairCharge, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// All grant reads and charge admission share one repository lock and transaction.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, factory.RepairTarget(repository)); err != nil {
		return false, err
	}
	created, err := admitRepairChargeTx(ctx, tx, repository, charge, now)
	if err != nil {
		return false, err
	}
	return created, tx.Commit()
}

func admitRepairChargeTx(ctx context.Context, tx *sql.Tx, repository int64, charge factory.RepairCharge, now time.Time) (bool, error) {
	heads, err := finishedCommands(ctx, tx, factory.CommandRepairGrant, factory.RepairTarget(repository))
	if err != nil {
		return false, err
	}
	head, err := headRepairGrant(heads)
	if err != nil {
		return false, err
	}
	var grant factory.RepairGrant
	if err = json.Unmarshal([]byte(head.Payload), &grant); err != nil {
		return false, err
	}
	charges, err := repairCharges(ctx, tx, repository)
	if err != nil {
		return false, err
	}
	account := factory.RepairAccount{Grant: grant, Charges: charges}
	_, spent, err := account.AdmitCharge(charge)
	if err != nil {
		return false, err
	}
	if !spent {
		for _, prior := range charges {
			if prior.ID == charge.ID {
				prior.SpentUnix = charge.SpentUnix
				if prior != charge {
					return false, ErrCommandConflict
				}
				break
			}
		}
		return false, nil
	}
	payload, err := json.Marshal(charge)
	if err != nil {
		return false, err
	}
	cmd := factory.Command{
		ID: charge.ID, Type: factory.CommandRepairCharge,
		Target: repairChargeTarget(repository), Principal: "factory-repair", Payload: string(payload),
	}
	cmd.Digest = factory.SettingsDigest(cmd.Type, cmd.Target, cmd.Payload)
	_, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return false, err
	}
	if created {
		if err = finishFactoryCommandTx(ctx, tx, cmd.ID, now); err != nil {
			return false, err
		}
	}
	return created, nil
}

// ApplyIntentExecutionLimitCommand records one origin intent's
// explicit active-time aggregate bound. The predecessor must name the
// currently finished limit command, empty for the first. Consumption
// derives from existing attempt rows, so the bound never refills: an
// increase retains spent time by construction.
func (s *Store) ApplyIntentExecutionLimitCommand(ctx context.Context, cmd factory.Command, limit factory.IntentExecutionAllowance, now time.Time) (factory.Command, bool, error) {
	if err := admitExecutionLimitCommand(cmd, limit); err != nil {
		return factory.Command{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.Command{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, created, err := recordFactoryCommandTx(ctx, tx, cmd, now)
	if err != nil {
		return factory.Command{}, false, err
	}
	if !created {
		return stored, false, tx.Commit()
	}
	heads, err := finishedCommands(ctx, tx, factory.CommandIntentExecutionLimit, cmd.Target)
	if err != nil {
		return factory.Command{}, false, err
	}
	if err = checkExecutionLimitHead(limit, heads); err != nil {
		return factory.Command{}, false, err
	}
	return finishGrantCommandTx(ctx, tx, stored, now)
}

// admitExecutionLimitCommand validates one execution limit command
// against its change.
func admitExecutionLimitCommand(cmd factory.Command, limit factory.IntentExecutionAllowance) error {
	if err := limit.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(limit)
	if err != nil {
		return err
	}
	if cmd.Type != factory.CommandIntentExecutionLimit || cmd.Target != factory.IntentExecutionTarget(limit.IntentID) ||
		string(payload) != cmd.Payload {
		return ErrCommandConflict
	}
	return nil
}

// checkExecutionLimitHead requires one execution limit to chain its
// head's predecessor without ever decreasing the aggregate bound.
func checkExecutionLimitHead(limit factory.IntentExecutionAllowance, heads []factory.Command) error {
	if len(heads) == 0 {
		if limit.Predecessor != "" || limit.Revision != 0 {
			return ErrStaleRevision
		}
		return nil
	}
	head, err := headExecutionLimit(heads)
	if err != nil {
		return err
	}
	var old factory.IntentExecutionAllowance
	if err := json.Unmarshal([]byte(head.Payload), &old); err != nil {
		return err
	}
	if limit.Predecessor != head.ID {
		return ErrStaleRevision
	}
	if limit.Revision != old.Revision+1 || limit.IntentID != old.IntentID || limit.OwnerID != old.OwnerID || limit.RepositoryID != old.RepositoryID {
		return errors.New("intent execution aggregate identity is immutable")
	}
	if limit.ActiveLimitMs < old.ActiveLimitMs {
		return errors.New("intent execution aggregate limit cannot decrease")
	}
	return nil
}

// headExecutionLimit selects the current bound by document revision:
// the highest finished revision wins, independent of timestamps.
func headExecutionLimit(heads []factory.Command) (factory.Command, error) {
	var head factory.Command
	picked := false
	var revision int64 = -1
	for _, cmd := range heads {
		var limit factory.IntentExecutionAllowance
		if err := json.Unmarshal([]byte(cmd.Payload), &limit); err != nil {
			return factory.Command{}, err
		}
		if !picked || limit.Revision > revision {
			head, revision, picked = cmd, limit.Revision, true
		}
	}
	if !picked {
		return factory.Command{}, ErrNotFound
	}
	return head, nil
}

// IntentExecutionLimit returns the currently recorded aggregate bound
// for one origin intent.
func (s *Store) IntentExecutionLimit(ctx context.Context, intent string) (factory.IntentExecutionAllowance, error) {
	heads, err := finishedCommands(ctx, s.db, factory.CommandIntentExecutionLimit, factory.IntentExecutionTarget(intent))
	if err != nil {
		return factory.IntentExecutionAllowance{}, err
	}
	head, err := headExecutionLimit(heads)
	if err != nil {
		return factory.IntentExecutionAllowance{}, err
	}
	var limit factory.IntentExecutionAllowance
	if err := json.Unmarshal([]byte(head.Payload), &limit); err != nil {
		return factory.IntentExecutionAllowance{}, err
	}
	return limit, nil
}

// AllIssueControls returns every recorded issue control in
// deterministic order for the D15 complete-inventory detector. Unlike
// the dispatch queue listing, blocked and unauthorized controls are
// included: a stall classifier must see the whole sealed set, never a
// runnable subset.
// AllIssueControls returns every recorded issue control up to limit and
// reports whether the inventory is complete. A set past the bound fails
// closed to incomplete: the extra probe row is discarded and no
// truncated set is returned, so callers can never prove deadlock from
// a partial inventory. Bounded cost is preserved: at most limit+1 rows
// are read.
func (s *Store) AllIssueControls(ctx context.Context, limit int) ([]factory.IssueControl, bool, error) {
	if limit < 1 || limit > 4096 {
		return nil, false, errors.New("invalid stall inventory bound")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM issue_controls ORDER BY repository ASC, issue ASC LIMIT $1`, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.IssueControl
	for rows.Next() {
		var data []byte
		var control factory.IssueControl
		if err := rows.Scan(&data); err != nil {
			return nil, false, err
		}
		if err := json.Unmarshal(data, &control); err != nil {
			return nil, false, err
		}
		out = append(out, control)
		if len(out) > limit {
			_ = rows.Close()
			return nil, false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// AttemptAllowanceHistory returns every attempt allowance row for one
// issue, oldest root first. Explicit retries keep earlier rows, so the
// origin aggregate sums them instead of refilling.
func (s *Store) AttemptAllowanceHistory(ctx context.Context, repository, issue int64) ([]factory.AttemptAllowance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.data FROM factory_attempt_allowances l JOIN factory_assignments a ON a.id=l.root_assignment WHERE l.repository=$1 AND l.issue=$2 ORDER BY a.seq ASC`, repository, issue)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.AttemptAllowance
	for rows.Next() {
		var data []byte
		var allowance factory.AttemptAllowance
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &allowance); err != nil {
			return nil, err
		}
		out = append(out, allowance)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// IntentExecutionAggregate is the stable active-time aggregate for one
// origin intent: every attempt root of every issue bound to any of the
// intent's revisions, each root charged once. New attempts,
// decomposition revisions, split or replacement issues and
// verification add rows; they never reset consumption.
type IntentExecutionAggregate struct {
	Intent      string
	Owner       int64
	ConsumedMs  int64
	Roots       []string
	Issues      []string
	LimitMs     int64
	LimitActive bool
}

// IntentExecutionAggregateForIntent computes the aggregate over the
// intent's revision history and their bound issues.
func (s *Store) IntentExecutionAggregateForIntent(ctx context.Context, intent string, owner int64) (IntentExecutionAggregate, error) {
	aggregate := IntentExecutionAggregate{Intent: intent, Owner: owner}
	if limit, err := s.IntentExecutionLimit(ctx, intent); err == nil {
		aggregate.LimitMs, aggregate.LimitActive = limit.ActiveLimitMs, limit.Active
	} else if !errors.Is(err, ErrNotFound) {
		return IntentExecutionAggregate{}, err
	}
	history, err := s.IntentHistory(ctx, intent, owner, 50)
	if err != nil {
		return IntentExecutionAggregate{}, err
	}
	seenRoot := map[string]bool{}
	seenIssue := map[string]bool{}
	for _, revision := range history {
		bindings, err := s.IntentTaskBindings(ctx, revision.ID)
		if err != nil {
			return IntentExecutionAggregate{}, err
		}
		for _, binding := range bindings {
			key := intentIssueKey(revision.Repository, binding.IssueIndex)
			if !seenIssue[key] {
				seenIssue[key] = true
				aggregate.Issues = append(aggregate.Issues, key)
			}
			allowances, err := s.AttemptAllowanceHistory(ctx, revision.Repository, binding.IssueIndex)
			if err != nil {
				return IntentExecutionAggregate{}, err
			}
			for _, allowance := range allowances {
				if seenRoot[allowance.RootAssignment] {
					continue
				}
				seenRoot[allowance.RootAssignment] = true
				aggregate.Roots = append(aggregate.Roots, allowance.RootAssignment)
				aggregate.ConsumedMs += allowance.ActiveSeconds * 1000
			}
		}
	}
	if len(history) > 0 {
		allowances, err := intentExecutionAllowances(ctx, s.db, history[0].Repository, intent)
		if err != nil {
			return IntentExecutionAggregate{}, err
		}
		for _, allowance := range allowances {
			if !seenRoot[allowance.RootAssignment] {
				seenRoot[allowance.RootAssignment] = true
				aggregate.Roots = append(aggregate.Roots, allowance.RootAssignment)
				aggregate.ConsumedMs += allowance.ActiveSeconds * 1000
			}
		}
	}
	return aggregate, nil
}

func intentIssueKey(repository, issue int64) string {
	return strconv.FormatInt(repository, 10) + "/" + strconv.FormatInt(issue, 10)
}
