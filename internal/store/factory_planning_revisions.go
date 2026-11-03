package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/levitateos/sodaos/internal/factory"
)

// ErrPlanConflict reports a reused (attempt_root, plan_id) identity carrying
// different content. Plan revisions are immutable: a replacement uses a new
// plan ID and moves the head pointer, so the consumed original always
// survives. Replaying identical content is idempotent, never a conflict.
var ErrPlanConflict = errors.New("plan revision identity reused for different content")

// ErrStalePlanBindings reports an admit against a head whose recorded source,
// prerequisite or preparation bindings no longer match the presented current
// values. The caller re-investigates instead of launching implementation
// from the stale plan.
var ErrStalePlanBindings = errors.New("plan bindings are stale for implementation")

// ErrUnknownPlanTask reports a task receipt for a task key the stored plan
// revision does not declare, or check evidence naming an unknown plan check.
var ErrUnknownPlanTask = errors.New("plan task is not part of the stored plan revision")

// ErrPlanTaskTransition reports a task state change outside the admitted
// lifecycle. Terminal states accept only identical replays.
var ErrPlanTaskTransition = errors.New("invalid plan task state transition")

// Admitted plan task states. Tasks advance pending → running → output_ready
// → completed, or stop as failed/cancelled; FW14 executes against these.
const (
	PlanTaskPending     = "pending"
	PlanTaskRunning     = "running"
	PlanTaskOutputReady = "output_ready"
	PlanTaskCompleted   = "completed"
	PlanTaskFailed      = "failed"
	PlanTaskCancelled   = "cancelled"
)

// maxPlanReceiptBytes bounds one encoded task receipt, mirroring the 64 KiB
// per-task checkpoint metadata bound.
const maxPlanReceiptBytes = 64 * 1024

// PlanBindings carries the current source, prerequisite and preparation
// values an implementation launch checks against the recorded head plan.
// FW12 supplies live values; the store refuses the launch when they differ.
type PlanBindings struct {
	SourceCommit  string
	Prerequisites []string
	Preparation   string
}

// Validate checks the binding shapes. Every prerequisite must be a digest;
// the match against the recorded plan happens in AdmitPlanHead.
func (b PlanBindings) Validate() error {
	if !factory.ValidCommit(b.SourceCommit) {
		return errors.New("invalid plan source binding")
	}
	for _, digest := range b.Prerequisites {
		if !factory.ValidDigest(digest) {
			return errors.New("invalid plan prerequisite binding")
		}
	}
	if !factory.ValidPreparationRef(b.Preparation) {
		return errors.New("invalid plan preparation binding")
	}
	return nil
}

func (b PlanBindings) matches(plan factory.InvestigationPlan) bool {
	if b.SourceCommit != plan.SourceCommit || b.Preparation != plan.Preparation {
		return false
	}
	return sameDigestMultiset(b.Prerequisites, plan.PrerequisiteDigest)
}

// sameDigestMultiset reports whether both slices carry the same digests.
// Collection order is not a binding: only membership and multiplicity match.
func sameDigestMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, digest := range a {
		counts[digest]++
	}
	for _, digest := range b {
		counts[digest]--
		if counts[digest] < 0 {
			return false
		}
	}
	return true
}

// PlanTaskReceipt is the persisted state and evidence for one planned task
// under one immutable plan revision. Revision is the CAS base: the first
// save uses zero and each accepted advance stores one more. An identical
// replay commits nothing and reports success.
type PlanTaskReceipt struct {
	AttemptRoot  string   `json:"attempt_root"`
	PlanID       string   `json:"plan_id"`
	TaskID       string   `json:"task_id"`
	Revision     int64    `json:"revision"`
	State        string   `json:"state"`
	Child        string   `json:"child,omitempty"`
	Parent       bool     `json:"parent,omitempty"`
	InputDigest  string   `json:"input_digest,omitempty"`
	OutputDigest string   `json:"output_digest,omitempty"`
	Events       []string `json:"events,omitempty"`
	Checks       []string `json:"checks,omitempty"`
	Tree         string   `json:"tree,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

// Validate checks the receipt shape and bounds. Task membership and check
// references are verified against the stored plan revision on save.
func (r PlanTaskReceipt) Validate() error {
	if err := r.validateTaskIdentity(); err != nil {
		return err
	}
	if err := r.validateTaskChild(); err != nil {
		return err
	}
	if err := r.validateTaskDigests(); err != nil {
		return err
	}
	if err := r.validateTaskContent(); err != nil {
		return err
	}
	return r.validateTaskReason()
}

// validateTaskIdentity refuses receipts outside the admitted identity,
// revision and state vocabulary.
func (r PlanTaskReceipt) validateTaskIdentity() error {
	if !factory.ValidID(r.AttemptRoot) || !factory.ValidID(r.PlanID) {
		return errors.New("invalid plan task identity")
	}
	if !factory.ValidPlanKey(r.TaskID) {
		return errors.New("invalid plan task key")
	}
	if r.Revision < 0 {
		return errors.New("invalid plan task revision")
	}
	switch r.State {
	case PlanTaskPending, PlanTaskRunning, PlanTaskOutputReady, PlanTaskCompleted, PlanTaskFailed, PlanTaskCancelled:
	default:
		return errors.New("unknown plan task state")
	}
	return nil
}

// validateTaskChild refuses pending tasks carrying a child and other
// states missing theirs. A cancelled task may never have launched a
// child; every other active or terminal state carries its native child
// identity.
func (r PlanTaskReceipt) validateTaskChild() error {
	if r.Parent {
		if r.Child != "" {
			return errors.New("singleton parent task cannot carry a child")
		}
		return nil
	}
	if r.State == PlanTaskPending {
		if r.Child != "" {
			return errors.New("pending plan task has no child")
		}
	} else if len(r.Child) > 128 || (r.Child == "" && r.State != PlanTaskCancelled) {
		return errors.New("plan task child outside its bound")
	}
	return nil
}

// validateTaskDigests refuses malformed digests and ready outputs
// missing theirs.
func (r PlanTaskReceipt) validateTaskDigests() error {
	if r.InputDigest != "" && !factory.ValidDigest(r.InputDigest) {
		return errors.New("invalid plan task input digest")
	}
	if r.OutputDigest != "" && !factory.ValidDigest(r.OutputDigest) {
		return errors.New("invalid plan task output digest")
	}
	if (r.State == PlanTaskOutputReady || r.State == PlanTaskCompleted) && r.OutputDigest == "" {
		return errors.New("plan task output lacks its digest")
	}
	return nil
}

// validateTaskContent refuses oversized events, non-key checks and
// unidentified tree/commit evidence.
func (r PlanTaskReceipt) validateTaskContent() error {
	if len(r.Events) > 512 {
		return errors.New("plan task event sequence exceeds its bound")
	}
	for _, event := range r.Events {
		if event == "" || len(event) > 256 {
			return errors.New("plan task event outside its bound")
		}
	}
	for _, id := range r.Checks {
		if !factory.ValidPlanKey(id) {
			return errors.New("plan task check is not a plan check key")
		}
	}
	if r.Tree != "" && !factory.ValidCommit(r.Tree) {
		return errors.New("plan task tree evidence lacks its identity")
	}
	if r.Commit != "" && !factory.ValidCommit(r.Commit) {
		return errors.New("plan task commit evidence lacks its identity")
	}
	return nil
}

// validateTaskReason refuses terminal states missing their reason,
// oversized reasons and oversized encodings.
func (r PlanTaskReceipt) validateTaskReason() error {
	if terminalPlanTaskState(r.State) {
		if r.Reason == "" || len(r.Reason) > factory.MaxPlanTextBytes {
			return errors.New("plan task terminal state lacks its reason")
		}
	} else if len(r.Reason) > factory.MaxPlanTextBytes {
		return errors.New("plan task reason exceeds its bound")
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > maxPlanReceiptBytes {
		return errors.New("encoded plan task receipt exceeds its bound")
	}
	return nil
}

func terminalPlanTaskState(state string) bool {
	return state == PlanTaskCompleted || state == PlanTaskFailed || state == PlanTaskCancelled
}

// validPlanTaskTransition reports whether a stored task state may advance to
// the next one. Same-state saves carry observation updates under CAS;
// terminal states change only through identical replays, which the save
// path accepts before reaching this check.
func validPlanTaskTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case PlanTaskPending:
		return to == PlanTaskRunning || to == PlanTaskCancelled
	case PlanTaskRunning:
		return to == PlanTaskOutputReady || to == PlanTaskFailed || to == PlanTaskCancelled
	case PlanTaskOutputReady:
		return to == PlanTaskCompleted || to == PlanTaskFailed || to == PlanTaskCancelled
	default:
		return false
	}
}

// SaveInvestigationPlan persists one immutable plan revision keyed by
// (attempt_root, plan_id). The plan must validate as a record; unresolved
// mandatory facts may persist but never admit (see AdmitPlanHead). Saving
// the identical revision twice succeeds; saving different content under a
// consumed identity fails without touching the original.
func (s *Store) SaveInvestigationPlan(ctx context.Context, plan factory.InvestigationPlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_plan_revisions(attempt_root,plan_id,generation,digest,data) VALUES($1,$2,$3,$4,$5) ON CONFLICT(attempt_root,plan_id) DO NOTHING`,
		plan.AttemptRoot, plan.ID, plan.Generation, plan.Digest, string(data))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		if err := recordInvestigationPlanFeedTx(ctx, tx, plan); err != nil {
			return err
		}
		return tx.Commit()
	}
	var stored factory.InvestigationPlan
	var storedRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_plan_revisions WHERE attempt_root=$1 AND plan_id=$2`, plan.AttemptRoot, plan.ID).Scan(&storedRaw); err != nil {
		return err
	}
	if err = json.Unmarshal(storedRaw, &stored); err != nil {
		return err
	}
	// Compare decoded records: JSONB storage normalizes the encoded bytes.
	if reflect.DeepEqual(stored, plan) {
		if err := recordInvestigationPlanFeedTx(ctx, tx, plan); err != nil {
			return err
		}
		return tx.Commit()
	}
	return ErrPlanConflict
}

// InvestigationPlan returns one persisted plan revision.
func (s *Store) InvestigationPlan(ctx context.Context, attemptRoot, planID string) (factory.InvestigationPlan, error) {
	var plan factory.InvestigationPlan
	if !factory.ValidID(attemptRoot) || !factory.ValidID(planID) {
		return plan, ErrNotFound
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_plan_revisions WHERE attempt_root=$1 AND plan_id=$2`, attemptRoot, planID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &plan)
	}
	return plan, err
}

// PlanHead returns the current plan pointer for one attempt root.
func (s *Store) PlanHead(ctx context.Context, attemptRoot string) (string, int64, error) {
	if !factory.ValidID(attemptRoot) {
		return "", 0, ErrNotFound
	}
	var planID string
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT plan_id,generation FROM factory_plan_heads WHERE attempt_root=$1`, attemptRoot).Scan(&planID, &generation)
	return planID, generation, err
}

// AdvancePlanHead moves the current plan pointer from the expected plan to
// the next one, which must already persist. An empty expected plan creates
// the pointer; a mismatched one fails without moving it. A replay that
// already reached the next plan succeeds.
func (s *Store) AdvancePlanHead(ctx context.Context, attemptRoot, expectedPlanID, nextPlanID string) error {
	if !factory.ValidID(attemptRoot) || !factory.ValidID(nextPlanID) {
		return ErrNotFound
	}
	if expectedPlanID != "" && !factory.ValidID(expectedPlanID) {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = advancePlanHeadTx(ctx, tx, attemptRoot, expectedPlanID, nextPlanID); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplacePlanHead saves a new immutable revision and moves the head pointer
// to it in one transaction. A conflicting reuse of the plan identity aborts
// before the pointer moves, so the consumed original survives replacement.
func (s *Store) ReplacePlanHead(ctx context.Context, plan factory.InvestigationPlan, expectedPlanID string) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if expectedPlanID != "" && !factory.ValidID(expectedPlanID) {
		return ErrNotFound
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_plan_revisions(attempt_root,plan_id,generation,digest,data) VALUES($1,$2,$3,$4,$5) ON CONFLICT(attempt_root,plan_id) DO NOTHING`,
		plan.AttemptRoot, plan.ID, plan.Generation, plan.Digest, string(data))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var stored factory.InvestigationPlan
		var storedRaw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_plan_revisions WHERE attempt_root=$1 AND plan_id=$2`, plan.AttemptRoot, plan.ID).Scan(&storedRaw); err != nil {
			return err
		}
		if err = json.Unmarshal(storedRaw, &stored); err != nil {
			return err
		}
		if !reflect.DeepEqual(stored, plan) {
			return ErrPlanConflict
		}
	}
	if err = advancePlanHeadTx(ctx, tx, plan.AttemptRoot, expectedPlanID, plan.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// advancePlanHeadTx CAS-moves an already-locked head pointer to a persisted
// revision. The caller owns the transaction.
func advancePlanHeadTx(ctx context.Context, tx *sql.Tx, attemptRoot, expectedPlanID, nextPlanID string) error {
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM factory_plan_revisions WHERE attempt_root=$1 AND plan_id=$2`, attemptRoot, nextPlanID).Scan(&generation); err != nil {
		return err
	}
	if expectedPlanID == "" {
		result, err := tx.ExecContext(ctx, `INSERT INTO factory_plan_heads(attempt_root,plan_id,generation) VALUES($1,$2,$3) ON CONFLICT(attempt_root) DO NOTHING`, attemptRoot, nextPlanID, generation)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE factory_plan_heads SET plan_id=$1,generation=$2 WHERE attempt_root=$3 AND plan_id=$4`, nextPlanID, generation, attemptRoot, expectedPlanID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
	}
	// A lost reply replays the intended head; any other mismatch is stale.
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT plan_id FROM factory_plan_heads WHERE attempt_root=$1`, attemptRoot).Scan(&current); err != nil {
		return err
	}
	if current == nextPlanID {
		return nil
	}
	return ErrStaleRevision
}

// AdmitPlanHead returns the current head revision when it is executable and
// its recorded source, prerequisite and preparation bindings still match the
// presented current values. Stale or incomplete plans never admit here:
// launching implementation from them is refused at this boundary.
func (s *Store) AdmitPlanHead(ctx context.Context, attemptRoot string, current PlanBindings) (factory.InvestigationPlan, error) {
	var plan factory.InvestigationPlan
	if !factory.ValidID(attemptRoot) {
		return plan, ErrNotFound
	}
	if err := current.Validate(); err != nil {
		return plan, err
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT r.data FROM factory_plan_heads h JOIN factory_plan_revisions r ON r.attempt_root=h.attempt_root AND r.plan_id=h.plan_id WHERE h.attempt_root=$1`, attemptRoot).Scan(&raw)
	if err != nil {
		return plan, err
	}
	if err = json.Unmarshal(raw, &plan); err != nil {
		return plan, err
	}
	if err = plan.Executable(); err != nil {
		return factory.InvestigationPlan{}, err
	}
	if !current.matches(plan) {
		return factory.InvestigationPlan{}, ErrStalePlanBindings
	}
	return plan, nil
}

// SavePlanTaskReceipt persists task state and evidence under CAS revision.
// The plan revision must persist and declare the task; check evidence must
// name its checks. The first save for a task starts at pending with revision
// zero. An identical replay commits nothing and succeeds; a write against a
// superseded revision fails without touching the stored receipt.
func (s *Store) SavePlanTaskReceipt(ctx context.Context, receipt PlanTaskReceipt) error {
	return s.SavePlanTaskReceipts(ctx, []PlanTaskReceipt{receipt})
}

// SavePlanTaskReceipts commits one verified journal page as a single unit.
// In particular run_stopped must not advance its receipt-derived cursor until
// every affected task cancellation has committed. Revisions are the CAS bases
// of each ordered transition, including repeated changes to the same task.
func (s *Store) SavePlanTaskReceipts(ctx context.Context, receipts []PlanTaskReceipt) error {
	if len(receipts) == 0 {
		return nil
	}
	if len(receipts) > 3*factory.MaxPlanTasks+64 {
		return errors.New("task receipt page exceeds its bound")
	}
	for _, receipt := range receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, receipt := range receipts {
		if err := savePlanTaskReceiptTx(ctx, tx, receipt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func savePlanTaskReceiptTx(ctx context.Context, tx *sql.Tx, receipt PlanTaskReceipt) error {
	var err error
	if err = planForTaskReceiptTx(ctx, tx, receipt); err != nil {
		return err
	}
	var storedRevision int64
	var storedRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,data FROM factory_plan_tasks WHERE attempt_root=$1 AND plan_id=$2 AND task_id=$3 FOR UPDATE`, receipt.AttemptRoot, receipt.PlanID, receipt.TaskID).Scan(&storedRevision, &storedRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return insertPendingTaskReceiptTx(ctx, tx, receipt)
	}
	if err != nil {
		return err
	}
	return advanceTaskReceiptTx(ctx, tx, receipt, storedRevision, storedRaw)
}

// planForTaskReceiptTx loads the stored plan revision behind one
// receipt and verifies the receipt names a task and checks the plan
// declares.
func planForTaskReceiptTx(ctx context.Context, tx *sql.Tx, receipt PlanTaskReceipt) error {
	var planRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_plan_revisions WHERE attempt_root=$1 AND plan_id=$2`, receipt.AttemptRoot, receipt.PlanID).Scan(&planRaw); err != nil {
		return err
	}
	var plan factory.InvestigationPlan
	if err := json.Unmarshal(planRaw, &plan); err != nil {
		return err
	}
	return checkPlanTaskRefs(plan, receipt)
}

// insertPendingTaskReceiptTx records the first revision of a task.
// Only a pending revision zero inserts; anything else is stale or an
// illegal transition.
func insertPendingTaskReceiptTx(ctx context.Context, tx *sql.Tx, receipt PlanTaskReceipt) error {
	if receipt.Revision != 0 {
		return ErrStaleRevision
	}
	if receipt.State != PlanTaskPending {
		return ErrPlanTaskTransition
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_plan_tasks(attempt_root,plan_id,task_id,revision,state,data) VALUES($1,$2,$3,$4,$5,$6)`,
		receipt.AttemptRoot, receipt.PlanID, receipt.TaskID, receipt.Revision, receipt.State, string(data)); err != nil {
		return err
	}
	return nil
}

// advanceTaskReceiptTx moves one recorded task receipt forward under
// CAS. Identical replays commit without touching the stored receipt;
// impossible states fail even for superseded writers; legal advances
// against a superseded revision report staleness so the caller
// refreshes and retries.
func advanceTaskReceiptTx(ctx context.Context, tx *sql.Tx, receipt PlanTaskReceipt, storedRevision int64, storedRaw []byte) error {
	var stored PlanTaskReceipt
	if err := json.Unmarshal(storedRaw, &stored); err != nil {
		return err
	}
	if stored.Revision != storedRevision {
		return errors.New("plan task receipt revision mismatch")
	}
	if stored.Parent != receipt.Parent || stored.Child != "" && stored.Child != receipt.Child {
		return ErrPlanTaskTransition
	}
	replay := receipt
	replay.Revision = storedRevision
	if reflect.DeepEqual(stored, replay) {
		return nil
	}
	if !validPlanTaskTransition(stored.State, receipt.State) {
		return ErrPlanTaskTransition
	}
	if storedRevision != receipt.Revision {
		return ErrStaleRevision
	}
	next := receipt
	next.Revision = storedRevision + 1
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_plan_tasks SET revision=$1,state=$2,data=$3 WHERE attempt_root=$4 AND plan_id=$5 AND task_id=$6 AND revision=$7`,
		next.Revision, next.State, string(data), receipt.AttemptRoot, receipt.PlanID, receipt.TaskID, storedRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

// checkPlanTaskRefs verifies the receipt names a task the stored plan
// revision declares and check evidence the plan defines.
func checkPlanTaskRefs(plan factory.InvestigationPlan, receipt PlanTaskReceipt) error {
	known := false
	for _, task := range plan.Tasks {
		if task.ID == receipt.TaskID {
			known = true
			break
		}
	}
	if !known {
		return ErrUnknownPlanTask
	}
	checks := make(map[string]bool, len(plan.Checks))
	for _, check := range plan.Checks {
		checks[check.ID] = true
	}
	for _, id := range receipt.Checks {
		if !checks[id] {
			return ErrUnknownPlanTask
		}
	}
	return nil
}

// PlanTaskReceipt returns the persisted state and evidence for one task.
func (s *Store) PlanTaskReceipt(ctx context.Context, attemptRoot, planID, taskID string) (PlanTaskReceipt, error) {
	var receipt PlanTaskReceipt
	if !factory.ValidID(attemptRoot) || !factory.ValidID(planID) || !factory.ValidPlanKey(taskID) {
		return receipt, ErrNotFound
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_plan_tasks WHERE attempt_root=$1 AND plan_id=$2 AND task_id=$3`, attemptRoot, planID, taskID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &receipt)
	}
	return receipt, err
}

// PlanTaskReceipts lists the persisted receipts under one plan revision in
// task-key order, so a restart observes task evidence instead of redoing it.
func (s *Store) PlanTaskReceipts(ctx context.Context, attemptRoot, planID string) ([]PlanTaskReceipt, error) {
	receipts := []PlanTaskReceipt{}
	if !factory.ValidID(attemptRoot) || !factory.ValidID(planID) {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_plan_tasks WHERE attempt_root=$1 AND plan_id=$2 ORDER BY task_id`, attemptRoot, planID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		var receipt PlanTaskReceipt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}
