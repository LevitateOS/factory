package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

// This file owns the durable priority-inventory cursor for the D02
// decomposition-and-priority contract: one current scan record plus
// candidate rows keyed by (scan_id, subject_key). The scan captures the
// complete ordered membership and the normalized local inputs in one
// repeatable-read transaction, so passes resume from the saved cursor
// instead of repeating a full native scan. Control compares the native
// revision and the local digest; any change invalidates the scan.

// PriorityInventoryScanID is the single current scan. Replacement, not
// versioning: a changed revision or digest replaces this row and drops
// its candidate rows. Spent judgment receipts stay in the existing
// judgment ledger.
const PriorityInventoryScanID = "current"

// MaxPriorityInventorySubjects keeps the D02 4,096-item ceiling. A larger
// membership refuses capture instead of truncating the set.
const MaxPriorityInventorySubjects = 4096

// ErrPriorityInventoryTooLarge reports a membership beyond the ceiling.
// The pass reports an incomplete inventory; it never elects a subset.
var ErrPriorityInventoryTooLarge = errors.New("priority inventory exceeds its bound")

// PriorityInventorySubject is one ordered scan member.
type PriorityInventorySubject struct {
	Repository    int64 `json:"repository"`
	Issue         int64 `json:"issue"`
	FirstSeenUnix int64 `json:"first_seen_unix"`
}

// PriorityInventory is one captured membership plus its local digest.
// The native revision is observed by control and compared alongside.
type PriorityInventory struct {
	LocalDigest string                     `json:"local_digest"`
	Subjects    []PriorityInventorySubject `json:"subjects"`
}

// PriorityCandidateObservation is one observed member: either a complete
// candidate for the tournament or an exclusion with its rule reason.
// Partial prerequisite observations land here as complete per-member
// records; members before the cursor are observed, members after it
// are not.
type PriorityCandidateObservation struct {
	Subject     string `json:"subject"`
	Repository  int64  `json:"repository"`
	Issue       int64  `json:"issue"`
	Candidate   []byte `json:"candidate,omitempty"`
	Exclusion   string `json:"exclusion,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

// PriorityScanState is the loaded current scan with its observations.
type PriorityScanState struct {
	ScanID         string
	NativeRevision int64
	LocalDigest    string
	Subjects       []PriorityInventorySubject
	Cursor         int
	Observations   map[string]PriorityCandidateObservation
}

// PrioritySubjectKey names one member. Ordering uses the stored subject
// list; the key is only a row identity.
func PrioritySubjectKey(repository, issue int64) string {
	return fmt.Sprintf("%d/%d", repository, issue)
}

// CapturePriorityInventory captures the complete ordered membership and
// the normalized local-input digest in one repeatable-read transaction.
// Assessment timestamps, observation counters, exact balances, remaining
// allowances and deadlines are excluded: refreshing a control or ticking
// a clock cannot invalidate its own scan. Time-dependent budget
// eligibility enters as booleans evaluated now; exact balances are
// rechecked in the final packet admission.
func (s *Store) CapturePriorityInventory(ctx context.Context) (PriorityInventory, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return PriorityInventory{}, err
	}
	defer func() { _ = tx.Rollback() }()
	controls, err := priorityInventoryMembership(ctx, tx)
	if err != nil {
		return PriorityInventory{}, err
	}
	digest, err := priorityInventoryDigest(ctx, tx, controls)
	if err != nil {
		return PriorityInventory{}, err
	}
	if err = tx.Commit(); err != nil {
		return PriorityInventory{}, err
	}
	subjects := make([]PriorityInventorySubject, 0, len(controls))
	for _, control := range controls {
		subjects = append(subjects, PriorityInventorySubject{
			Repository: control.Repository, Issue: control.Issue, FirstSeenUnix: control.FirstSeenUnix,
		})
	}
	return PriorityInventory{LocalDigest: digest, Subjects: subjects}, nil
}

// priorityInventoryMembership lists the complete ordered candidate
// membership: queued controls plus accepted authority-blocked controls
// that may have become ready. The predicate matches
// DispatchControlsAfter; the ceiling refuses instead of truncating.
func priorityInventoryMembership(ctx context.Context, tx *sql.Tx) ([]factory.IssueControl, error) {
	rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_controls
		WHERE (data->>'readiness'='queued' OR (data->>'readiness'='not_authorized'
			AND data->>'reason'='authority_missing' AND data->>'acceptance'<>''))
		ORDER BY (data->>'first_seen_unix')::bigint ASC, repository ASC, issue ASC LIMIT $1`,
		MaxPriorityInventorySubjects+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var controls []factory.IssueControl
	for rows.Next() {
		var data []byte
		var control factory.IssueControl
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &control); err != nil {
			return nil, err
		}
		controls = append(controls, control)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(controls) > MaxPriorityInventorySubjects {
		return nil, ErrPriorityInventoryTooLarge
	}
	return controls, nil
}

// priorityInventoryDigest binds the membership and every semantic local
// input that can change candidacy: acceptance heads and withdrawals,
// per-member control evidence, assignment/retry/
// allowance state, per-repository authority verdicts, dispatch gates,
// requirement/approval heads, preparations, and current capacity and
// connection-budget facts with eligibility as booleans.
func priorityInventoryDigest(ctx context.Context, tx *sql.Tx, controls []factory.IssueControl) (string, error) {
	now := time.Now()
	collector := priorityDigestInputs{tx: tx, now: now, repos: map[int64]struct{}{}}
	for _, control := range controls {
		collector.members = append(collector.members, priorityDigestMember{
			repository: control.Repository, issue: control.Issue, firstSeen: control.FirstSeenUnix,
			acceptance: control.Acceptance, fingerprint: control.Fingerprint,
			readiness: control.Readiness, reason: control.Reason, authority: control.Authority,
		})
		if _, ok := collector.repos[control.Repository]; !ok {
			collector.repos[control.Repository] = struct{}{}
		}
	}
	if err := collector.batchMemberInputs(ctx); err != nil {
		return "", err
	}
	if err := collector.batchRepoInputs(ctx); err != nil {
		return "", err
	}
	if err := collector.batchBudgetInputs(ctx); err != nil {
		return "", err
	}
	return collector.digest()
}

type priorityDigestMember struct {
	repository  int64
	issue       int64
	firstSeen   int64
	acceptance  string
	fingerprint string
	readiness   string
	reason      string
	authority   string

	head          string
	withdrawn     bool
	assignStage   string
	assignHead    string
	retryPending  bool
	allowanceShut bool
}

type priorityDigestInputs struct {
	tx      *sql.Tx
	now     time.Time
	members []priorityDigestMember
	repos   map[int64]struct{}

	repoAuthority map[int64]string
	repoGate      map[int64]string
	repoReqAppr   map[int64]string
	repoPreps     map[int64]string
	capacityRev   int64
	occupancy     string
	allowRoom     map[int64]bool
}

func (c *priorityDigestInputs) digest() (string, error) {
	type member struct {
		Repository   int64  `json:"repository"`
		Issue        int64  `json:"issue"`
		FirstSeen    int64  `json:"first_seen"`
		Acceptance   string `json:"acceptance"`
		Fingerprint  string `json:"fingerprint"`
		Readiness    string `json:"readiness"`
		Reason       string `json:"reason"`
		Authority    string `json:"authority"`
		Head         string `json:"head"`
		Withdrawn    bool   `json:"withdrawn"`
		AssignStage  string `json:"assign_stage"`
		AssignHead   string `json:"assign_head"`
		RetryPending bool   `json:"retry_pending"`
		Shut         bool   `json:"allowance_shut"`
	}
	doc := struct {
		Members  []member          `json:"members"`
		Repos    map[string]string `json:"repos"`
		Capacity int64             `json:"capacity"`
		Occupied string            `json:"occupied"`
		Conns    map[string]string `json:"conns"`
	}{
		Repos:    map[string]string{},
		Conns:    map[string]string{},
		Capacity: c.capacityRev,
		Occupied: c.occupancy,
	}
	for _, m := range c.members {
		doc.Members = append(doc.Members, member{
			Repository: m.repository, Issue: m.issue, FirstSeen: m.firstSeen,
			Acceptance: m.acceptance, Fingerprint: m.fingerprint,
			Readiness: m.readiness, Reason: m.reason, Authority: m.authority,
			Head: m.head, Withdrawn: m.withdrawn, AssignStage: m.assignStage,
			AssignHead: m.assignHead, RetryPending: m.retryPending,
			Shut: m.allowanceShut,
		})
	}
	for repo := range c.repos {
		key := fmt.Sprintf("%d", repo)
		doc.Repos[key] = c.repoAuthority[repo] + "|" + c.repoGate[repo] + "|" +
			c.repoReqAppr[repo] + "|" + c.repoPreps[repo]
	}
	repoKeys := make([]int64, 0, len(c.repos))
	for repo := range c.repos {
		repoKeys = append(repoKeys, repo)
	}
	sort.Slice(repoKeys, func(i, j int) bool { return repoKeys[i] < repoKeys[j] })
	for _, repo := range repoKeys {
		doc.Repos[fmt.Sprintf("%d", repo)] += fmt.Sprintf("|allow_room=%t", c.allowRoom[repo])
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// batchMemberInputs loads per-member acceptance, withdrawal, assignment,
// allowance state in chunked membership queries. Retry
// lookup runs only for terminal same-head members, the single case where
// it changes candidacy.
func (c *priorityDigestInputs) batchMemberInputs(ctx context.Context) error {
	keys := make([][2]int64, 0, len(c.members))
	for _, m := range c.members {
		keys = append(keys, [2]int64{m.repository, m.issue})
	}
	heads := map[[2]int64]string{}
	if err := priorityBatchPairs(ctx, c.tx, keys, `SELECT repository, issue, decision FROM issue_acceptance_heads WHERE (repository, issue) IN `,
		func(repository, issue int64, head string) { heads[[2]int64{repository, issue}] = head }); err != nil {
		return err
	}
	withdrawn := map[[3]string]bool{}
	var wkeys [][2]int64
	for _, m := range c.members {
		if head := heads[[2]int64{m.repository, m.issue}]; head != "" {
			wkeys = append(wkeys, [2]int64{m.repository, m.issue})
		}
	}
	if err := priorityBatchPairs(ctx, c.tx, wkeys, `SELECT repository, issue, decision FROM issue_acceptance_withdrawals WHERE (repository, issue) IN `,
		func(repository, issue int64, decision string) {
			withdrawn[[3]string{fmt.Sprintf("%d", repository), fmt.Sprintf("%d", issue), decision}] = true
		}); err != nil {
		return err
	}
	assigns := map[[2]int64]factory.Assignment{}
	if err := priorityBatchAssignments(ctx, c.tx, keys, assigns); err != nil {
		return err
	}
	shut := map[[2]int64]bool{}
	if err := priorityBatchAllowanceShut(ctx, c.tx, keys, shut); err != nil {
		return err
	}
	for i, m := range c.members {
		key := [2]int64{m.repository, m.issue}
		c.members[i].head = heads[key]
		c.members[i].withdrawn = withdrawn[[3]string{fmt.Sprintf("%d", m.repository), fmt.Sprintf("%d", m.issue), heads[key]}]
		if assign, ok := assigns[key]; ok {
			c.members[i].assignStage = assign.Stage
			c.members[i].assignHead = assign.Acceptance
			if assign.Stage == factory.AssignmentFinished && assign.Acceptance != "" &&
				assign.Acceptance == heads[key] && assign.Acceptance == m.acceptance {
				pending, err := pendingExplicitRetry(ctx, c.tx, m.repository, m.issue, assign.Acceptance)
				if errors.Is(err, ErrNotFound) {
					pending, err = pendingImplementationRetry(ctx, c.tx, m.repository, m.issue, assign.Acceptance)
				}
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				c.members[i].retryPending = err == nil && pending.CommandID != ""
			}
		}
		c.members[i].allowanceShut = shut[key]
	}
	return nil
}

// priorityBatchPairs runs a chunked (repository, issue) IN query and calls
// visit per row. Chunking keeps one capture bounded for large memberships.
func priorityBatchPairs(ctx context.Context, tx *sql.Tx, keys [][2]int64, prefix string, visit func(repository, issue int64, value string)) error {
	for _, chunk := range priorityKeyChunks(keys) {
		query, args := priorityPairQuery(prefix, chunk)
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var repository, issue int64
			var value string
			if err := rows.Scan(&repository, &issue, &value); err != nil {
				_ = rows.Close()
				return err
			}
			visit(repository, issue, value)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func priorityKeyChunks(keys [][2]int64) [][][2]int64 {
	const chunk = 500
	var out [][][2]int64
	for len(keys) > 0 {
		n := chunk
		if len(keys) < n {
			n = len(keys)
		}
		out = append(out, keys[:n])
		keys = keys[n:]
	}
	return out
}

func priorityPairQuery(prefix string, chunk [][2]int64) (string, []any) {
	args := make([]any, 0, len(chunk)*2)
	placeholders := ""
	for i, key := range chunk {
		if i > 0 {
			placeholders += ","
		}
		placeholders += fmt.Sprintf("($%d,$%d)", len(args)+1, len(args)+2)
		args = append(args, key[0], key[1])
	}
	return prefix + "(" + placeholders + ")", args
}

// priorityBatchAssignments loads the latest assignment per member.
func priorityBatchAssignments(ctx context.Context, tx *sql.Tx, keys [][2]int64, out map[[2]int64]factory.Assignment) error {
	for _, chunk := range priorityKeyChunks(keys) {
		query, args := priorityPairQuery(`SELECT DISTINCT ON (repository, issue) repository, issue, data FROM factory_assignments WHERE (repository, issue) IN `, chunk)
		query += ` ORDER BY repository, issue, seq DESC`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var repository, issue int64
			var data []byte
			var assign factory.Assignment
			if err := rows.Scan(&repository, &issue, &data); err != nil {
				_ = rows.Close()
				return err
			}
			if err := json.Unmarshal(data, &assign); err != nil {
				_ = rows.Close()
				return err
			}
			out[[2]int64{repository, issue}] = assign
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// priorityBatchAllowanceShut reports closed attempt allowances per member.
func priorityBatchAllowanceShut(ctx context.Context, tx *sql.Tx, keys [][2]int64, out map[[2]int64]bool) error {
	for _, chunk := range priorityKeyChunks(keys) {
		query, args := priorityPairQuery(`SELECT repository, issue, data FROM factory_attempt_allowances WHERE (repository, issue) IN `, chunk)
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var repository, issue int64
			var data []byte
			var allowance factory.AttemptAllowance
			if err := rows.Scan(&repository, &issue, &data); err != nil {
				_ = rows.Close()
				return err
			}
			if err := json.Unmarshal(data, &allowance); err != nil {
				_ = rows.Close()
				return err
			}
			if allowance.Closed {
				out[[2]int64{repository, issue}] = true
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// batchRepoInputs binds per-repository authority verdicts, dispatch gates,
// requirement/approval heads and preparations.
func (c *priorityDigestInputs) batchRepoInputs(ctx context.Context) error {
	c.repoAuthority = map[int64]string{}
	c.repoGate = map[int64]string{}
	c.repoReqAppr = map[int64]string{}
	c.repoPreps = map[int64]string{}
	for repo := range c.repos {
		in, err := factoryAuthorityInput(ctx, c.tx, repo)
		if err != nil {
			return err
		}
		c.repoAuthority[repo] = factory.AuthorityFingerprint(factory.EvaluateAuthority(in))
		open, rev, _, err := dispatchStateQuery(ctx, c.tx, repo)
		if err != nil {
			return err
		}
		c.repoGate[repo] = fmt.Sprintf("open=%t rev=%d", open, rev)
		var projectID string
		err = c.tx.QueryRowContext(ctx, `SELECT p.id`+nativeProjectCollaborationJoin+` WHERE r.id=$1`, repo).Scan(&projectID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil {
			continue
		}
		var req, appr string
		if err = c.tx.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1`, projectID).Scan(&req); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = c.tx.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1`, projectID).Scan(&appr); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		c.repoReqAppr[repo] = req + "|" + appr
		rows, err := c.tx.QueryContext(ctx, `SELECT id, role, revision, data FROM project_preparations WHERE project_id=$1 ORDER BY id`, projectID)
		if err != nil {
			return err
		}
		summary := ""
		for rows.Next() {
			var id, role string
			var revision int64
			var data []byte
			if err := rows.Scan(&id, &role, &revision, &data); err != nil {
				_ = rows.Close()
				return err
			}
			var prep execution.PreparationRecord
			if err := json.Unmarshal(data, &prep); err != nil {
				_ = rows.Close()
				return err
			}
			summary += fmt.Sprintf("%s/%s/%d/ready=%t;", id, role, revision, prep.State.Ready)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		c.repoPreps[repo] = summary
	}
	return nil
}

// batchBudgetInputs binds capacity and per-connection budget revisions
// with eligibility as booleans evaluated now. Exact balances stay out of
// the digest; the packet admission rechecks them.
func (c *priorityDigestInputs) batchBudgetInputs(ctx context.Context) error {
	c.allowRoom = map[int64]bool{}
	var capacityRev int64
	if err := c.tx.QueryRowContext(ctx, `SELECT revision FROM factory_capacity WHERE id=1`).Scan(&capacityRev); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	c.capacityRev = capacityRev
	// Early authenticated completion can finish an assignment while its
	// native unit still occupies capacity. Bind the actual occupancy so
	// retirement invalidates that exclusion even when the assignment,
	// native repository revision and grants remain unchanged. Stable held
	// sessions keep the same scan and never spend another selection call.
	if err := c.tx.QueryRowContext(ctx, `SELECT COALESCE(string_agg(kind||':'||slot||':'||occupied::text,'|' ORDER BY kind,slot),'') FROM (
		SELECT 'held' AS kind,repository::text||':'||connection AS slot,count(*) AS occupied
		FROM factory_reservations WHERE state='held' GROUP BY repository,connection
		UNION ALL
		SELECT 'run',data->>'project_id'||':'||active::text,count(*)
		FROM factory_runs WHERE active OR NOT settled GROUP BY data->>'project_id',active
	) occupancy`).Scan(&c.occupancy); err != nil {
		return err
	}
	conns := map[string]struct{}{}
	repoConn := map[int64]string{}
	for repo := range c.repos {
		rows, err := c.tx.QueryContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1`, repo)
		if err != nil {
			return err
		}
		for rows.Next() {
			var data []byte
			var sponsorship factory.Sponsorship
			if err := rows.Scan(&data); err != nil {
				_ = rows.Close()
				return err
			}
			if err := json.Unmarshal(data, &sponsorship); err != nil {
				_ = rows.Close()
				return err
			}
			if sponsorship.Active && repoConn[repo] == "" {
				repoConn[repo] = sponsorship.Connection
			}
			conns[sponsorship.Connection] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	for repo, conn := range repoConn {
		var used int
		if err := c.tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(minutes),0) FROM factory_usage WHERE repository=$1 AND connection=$2`, repo, conn).Scan(&used); err != nil {
			return err
		}
		c.allowRoom[repo] = used < prioritySponsorshipAllowance(ctx, c.tx, repo, conn)
	}
	return nil
}

// prioritySponsorshipAllowance returns the active sponsorship allowance
// minutes for one repository, or zero when none is active.
func prioritySponsorshipAllowance(ctx context.Context, tx *sql.Tx, repository int64, connection string) int {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 AND connection=$2`, repository, connection).Scan(&data); err != nil {
		return 0
	}
	var sponsorship factory.Sponsorship
	if err := json.Unmarshal(data, &sponsorship); err != nil || !sponsorship.Active {
		return 0
	}
	return sponsorship.AllowanceMinutes
}

// LoadPriorityScan returns the current scan with its observations, or
// ErrNotFound when no scan is retained.
func (s *Store) LoadPriorityScan(ctx context.Context) (PriorityScanState, error) {
	var state PriorityScanState
	var subjectsData []byte
	err := s.db.QueryRowContext(ctx, `SELECT scan, native_revision, local_digest, subjects, cursor
		FROM factory_priority_scans WHERE scan=$1`, PriorityInventoryScanID).
		Scan(&state.ScanID, &state.NativeRevision, &state.LocalDigest, &subjectsData, &state.Cursor)
	if err != nil {
		return PriorityScanState{}, err
	}
	if err := json.Unmarshal(subjectsData, &state.Subjects); err != nil {
		return PriorityScanState{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT subject, repository, issue, candidate, exclusion, fingerprint
		FROM factory_priority_candidates WHERE scan=$1`, PriorityInventoryScanID)
	if err != nil {
		return PriorityScanState{}, err
	}
	defer func() { _ = rows.Close() }()
	state.Observations = map[string]PriorityCandidateObservation{}
	for rows.Next() {
		var obs PriorityCandidateObservation
		var candidate []byte
		var exclusion sql.NullString
		if err := rows.Scan(&obs.Subject, &obs.Repository, &obs.Issue, &candidate, &exclusion, &obs.Fingerprint); err != nil {
			return PriorityScanState{}, err
		}
		if len(candidate) > 0 {
			obs.Candidate = candidate
		}
		if exclusion.Valid {
			obs.Exclusion = exclusion.String
		}
		state.Observations[obs.Subject] = obs
	}
	return state, rows.Err()
}

// SavePriorityScan stores a captured scan as current. An identical saved
// scan resumes with its cursor and observations; any change replaces the
// row and drops invalid observations.
func (s *Store) SavePriorityScan(ctx context.Context, nativeRevision int64, inventory PriorityInventory) (bool, error) {
	subjectsData, err := json.Marshal(inventory.Subjects)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var currentRev int64
	var currentDigest string
	var currentSubjects []byte
	err = tx.QueryRowContext(ctx, `SELECT native_revision, local_digest, subjects FROM factory_priority_scans WHERE scan=$1 FOR UPDATE`,
		PriorityInventoryScanID).Scan(&currentRev, &currentDigest, &currentSubjects)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && currentRev == nativeRevision && currentDigest == inventory.LocalDigest &&
		prioritySubjectsEqual(currentSubjects, subjectsData) {
		return true, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_priority_scans(scan, native_revision, local_digest, subjects, cursor)
		VALUES($1,$2,$3,$4,0)
		ON CONFLICT(scan) DO UPDATE SET native_revision=$2, local_digest=$3, subjects=$4, cursor=0`,
		PriorityInventoryScanID, nativeRevision, inventory.LocalDigest, string(subjectsData)); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM factory_priority_candidates WHERE scan=$1`, PriorityInventoryScanID); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// prioritySubjectsEqual compares a stored scan membership with a freshly
// captured one. The column is JSONB, which normalizes key order and spacing
// on write, so raw bytes never match a fresh marshal; both sides compare in
// canonical member order instead. A byte compare would mistake every resumed
// pass for changed inputs and drop its persisted cursor.
func prioritySubjectsEqual(stored, captured []byte) bool {
	var current, fresh []PriorityInventorySubject
	if err := json.Unmarshal(stored, &current); err != nil {
		return false
	}
	if err := json.Unmarshal(captured, &fresh); err != nil {
		return false
	}
	if len(current) != len(fresh) {
		return false
	}
	for i := range current {
		if current[i] != fresh[i] {
			return false
		}
	}
	return true
}

// RecordPriorityProgress persists observations and advances the cursor.
// A changed scan digest refuses: the pass must revalidate before writing.
func (s *Store) RecordPriorityProgress(ctx context.Context, nativeRevision int64, localDigest string, observations []PriorityCandidateObservation, cursor int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var currentRev int64
	var currentDigest string
	if err = tx.QueryRowContext(ctx, `SELECT native_revision, local_digest FROM factory_priority_scans WHERE scan=$1 FOR UPDATE`,
		PriorityInventoryScanID).Scan(&currentRev, &currentDigest); err != nil {
		return err
	}
	if currentRev != nativeRevision || currentDigest != localDigest {
		return ErrStaleRevision
	}
	for _, obs := range observations {
		var candidate any
		var exclusion any
		if len(obs.Candidate) > 0 {
			candidate = string(obs.Candidate)
		}
		if obs.Exclusion != "" {
			exclusion = obs.Exclusion
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_priority_candidates(scan, subject, repository, issue, candidate, exclusion, fingerprint)
			VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT(scan, subject) DO UPDATE SET candidate=excluded.candidate, exclusion=excluded.exclusion, fingerprint=excluded.fingerprint`,
			PriorityInventoryScanID, obs.Subject, obs.Repository, obs.Issue, candidate, exclusion, obs.Fingerprint); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_priority_scans SET cursor=$2 WHERE scan=$1`, PriorityInventoryScanID, cursor); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePriorityScan drops the current scan and its observations after
// completion or proven invalidation.
func (s *Store) DeletePriorityScan(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM factory_priority_scans WHERE scan=$1`, PriorityInventoryScanID)
	return err
}
