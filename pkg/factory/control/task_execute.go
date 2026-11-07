package control

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// FW14 ready-task execution against persisted plan task receipts (D05).
//
// This driver is the sole Go writer of plan task state. It folds verified
// typed task events — the executor's observations imported from the
// per-run journal — into receipt advances and release decisions. Run-output
// prose has no path here: ordinary model text never advances task state,
// and commit/tree evidence binds only from typed task_committed detail the
// executor observed. Checkpoint CAS and PR publication stay FW15 joints;
// this driver consumes their future events without performing them.
//
// The fold is deterministic over (plan, stored receipts, event page), so a
// restart observes receipts instead of redoing completed writes: an exact
// replay emits no writes, and SavePlanTaskReceipt replays are no-ops
// behind CAS. The progress loop persists each verified journal page atomically
// before settlement and publication can consume the resulting receipts.

// Task event import errors. Binding and order violations fence the run:
// the caller refuses advancement instead of guessing past a gap or a
// foreign identity.
var (
	ErrTaskEventGap        = errors.New("task event sequence gap refuses advancement")
	ErrTaskEventBinding    = errors.New("task event does not bind its run")
	ErrTaskEventUnknown    = errors.New("task event names an unknown plan task")
	ErrTaskEventTransition = errors.New("task event violates its task lifecycle")
)

// TaskEventImport binds one verified journal page to the run that produced
// it. Events arrive in journal sequence order. AttemptRoot and PlanID name
// the immutable plan revision the receipts belong to; Invocation and
// PlanDigest rebind every event to the admitted run (mirroring the
// executor's record rule at import); MaxChildren caps aggregate releases.
type TaskEventImport struct {
	AttemptRoot string
	PlanID      string
	Invocation  string
	PlanDigest  string
	MaxChildren int
	Phase       string
	Events      []project.TaskEvent
}

// TaskExecutionResult carries exact receipt writes. NextSequence is the
// journal cursor after the fold: the highest applied or replayed sequence,
// for the caller's next page. Stop reports that the run must admit no new
// task: a failed task was contained, the run stopped, or the journal
// recorded more active tasks than admitted (D05).
type TaskExecutionResult struct {
	Writes       []store.PlanTaskReceipt
	NextSequence int64
	Stop         bool
}

// taskEventRef records one applied event on its receipt. The suffix keeps
// the import cursor restart-safe without a second table.
func taskEventRef(event project.TaskEvent) string {
	return event.Type + "@" + strconv.FormatInt(event.Sequence, 10) + ":" + project.TaskEventDigest(event)
}

func parseTaskEventRef(ref string) (int64, string, bool) {
	typeName, suffix, found := strings.Cut(ref, "@")
	if !found || !project.ValidTaskEventType(typeName) {
		return 0, "", false
	}
	sequence, digest, found := strings.Cut(suffix, ":")
	seq, err := strconv.ParseInt(sequence, 10, 64)
	return seq, digest, found && err == nil && seq > 0 && factory.ValidDigest(digest)
}

// importCursor recovers the journal cursor from stored receipts: the
// highest applied event sequence. Refs the driver never wrote are ignored
// for the cursor; receipt content itself is still folded honestly.
func importCursor(receipts []store.PlanTaskReceipt) int64 {
	var cursor int64
	for _, receipt := range receipts {
		for _, ref := range receipt.Events {
			seq, _, ok := parseTaskEventRef(ref)
			if !ok {
				continue
			}
			if seq > cursor {
				cursor = seq
			}
		}
	}
	return cursor
}

// journalStopped reports whether a run_stopped event was ever applied.
func journalStopped(receipts []store.PlanTaskReceipt) bool {
	for _, receipt := range receipts {
		for _, ref := range receipt.Events {
			if strings.HasPrefix(ref, project.TaskEventRunStopped+"@") {
				return true
			}
		}
	}
	return false
}

// ImportTaskEvents folds one verified event page into receipt writes and
// the stop decision. Missing receipts seed as pending; identical replays
// emit nothing; conflicting bytes, gaps, foreign identities and lifecycle
// violations refuse with the sentinel above.
func ImportTaskEvents(plan factory.InvestigationPlan, receipts []store.PlanTaskReceipt, imp TaskEventImport) (TaskExecutionResult, error) {
	var result TaskExecutionResult
	byTask, err := admitTaskEventImport(plan, imp)
	if err != nil {
		return result, err
	}
	stored, err := admitTaskEventReceipts(receipts, imp, byTask)
	if err != nil {
		return result, err
	}
	fold := &taskEventFold{
		plan: plan, imp: imp, pending: stored, byTask: byTask, result: &result,
		next: importCursor(receipts), stopped: journalStopped(receipts),
	}
	for _, receipt := range receipts {
		for _, ref := range receipt.Events {
			sequence, digest, ok := parseTaskEventRef(ref)
			if !ok || sequence != fold.next {
				continue
			}
			if fold.lastDigest != "" && fold.lastDigest != digest {
				return result, ErrTaskEventBinding
			}
			fold.lastDigest = digest
		}
	}
	fold.seedPendingReceipts()
	for _, event := range imp.Events {
		if err := fold.foldEvent(event); err != nil {
			return result, err
		}
	}
	fold.finishExecution()
	return result, nil
}

// admitTaskEventImport validates one task event import against its plan.
func admitTaskEventImport(plan factory.InvestigationPlan, imp TaskEventImport) (map[string]int, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if !factory.ValidID(imp.AttemptRoot) || !factory.ValidID(imp.PlanID) ||
		plan.AttemptRoot != imp.AttemptRoot || plan.ID != imp.PlanID {
		return nil, errors.New("task event import names an unknown plan revision")
	}
	if imp.Phase == project.ExecutionCorrect {
		if imp.MaxChildren != 0 || len(plan.Tasks) != 1 {
			return nil, errors.New("correction task import requires its singleton parent")
		}
	} else if imp.Phase != project.ExecutionImplement || imp.MaxChildren < 0 || imp.MaxChildren > project.MaxTaskChildren {
		return nil, errors.New("task event import child limit is outside its range")
	}
	if imp.Invocation == "" || len(imp.Invocation) > 128 {
		return nil, errors.New("task event import invocation is invalid")
	}
	if !factory.ValidDigest(imp.PlanDigest) {
		return nil, errors.New("task event import plan digest is invalid")
	}
	byTask := make(map[string]int, len(plan.Tasks))
	for i, task := range plan.Tasks {
		byTask[task.ID] = i
	}
	return byTask, nil
}

// admitTaskEventReceipts binds stored receipts to one import's plan revision.
func admitTaskEventReceipts(receipts []store.PlanTaskReceipt, imp TaskEventImport, byTask map[string]int) (map[string]store.PlanTaskReceipt, error) {
	stored := make(map[string]store.PlanTaskReceipt, len(receipts))
	for _, receipt := range receipts {
		if receipt.AttemptRoot != imp.AttemptRoot || receipt.PlanID != imp.PlanID {
			return nil, errors.New("task event import mixes plan revisions")
		}
		if _, ok := byTask[receipt.TaskID]; !ok {
			return nil, ErrTaskEventUnknown
		}
		if receipt.Parent != (imp.MaxChildren == 0) {
			return nil, ErrTaskEventBinding
		}
		if _, dup := stored[receipt.TaskID]; dup {
			return nil, errors.New("task event import carries duplicate receipts")
		}
		stored[receipt.TaskID] = receipt
	}
	return stored, nil
}

// taskEventFold carries the shared state behind one task event import.
type taskEventFold struct {
	plan       factory.InvestigationPlan
	imp        TaskEventImport
	pending    map[string]store.PlanTaskReceipt
	byTask     map[string]int
	result     *TaskExecutionResult
	next       int64
	lastDigest string
	stopped    bool
}

// seedPendingReceipts seeds pending receipts for tasks the store
// never saw. Identical replays of an existing receipt emit nothing.
func (f *taskEventFold) seedPendingReceipts() {
	for _, task := range f.plan.Tasks {
		if _, ok := f.pending[task.ID]; ok {
			continue
		}
		seed := store.PlanTaskReceipt{
			AttemptRoot: f.imp.AttemptRoot, PlanID: f.imp.PlanID, TaskID: task.ID,
			State:  store.PlanTaskPending,
			Parent: f.imp.MaxChildren == 0,
		}
		f.pending[task.ID] = seed
		f.result.Writes = append(f.result.Writes, seed)
	}
}

// emit stages one receipt write when it changes stored state. The
// write carries the stored CAS base; identical content emits nothing,
// so replays never duplicate writes.
func (f *taskEventFold) emit(next store.PlanTaskReceipt) {
	if receiptsEqual(f.pending[next.TaskID], next) {
		return
	}
	f.result.Writes = append(f.result.Writes, next)
	// Each write consumes its CAS base. Later events in this same page must
	// use the revision the preceding write will commit, including changes
	// that leave the lifecycle state unchanged (start intent and checks).
	next.Revision++
	f.pending[next.TaskID] = next
}

// foldEvent folds one fresh event into the pending receipts.
func (f *taskEventFold) foldEvent(event project.TaskEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Invocation != f.imp.Invocation || event.PlanDigest != f.imp.PlanDigest {
		return ErrTaskEventBinding
	}
	parentEvent := event.Type == project.TaskEventParentStarted || event.Type == project.TaskEventParentOutput
	childEvent := event.Type == project.TaskEventChildStartIntent || event.Type == project.TaskEventChildStarted || event.Type == project.TaskEventChildReported
	if parentEvent && (f.imp.MaxChildren != 0 || event.ChildID != "") || childEvent && f.imp.MaxChildren == 0 {
		return ErrTaskEventBinding
	}
	if event.Sequence <= f.next {
		if !eventReplayed(f.pending, event) {
			return ErrTaskEventGap
		}
		return nil
	}
	if event.Sequence != f.next+1 {
		return ErrTaskEventGap
	}
	if event.PrevDigest != f.lastDigest {
		return ErrTaskEventBinding
	}
	if f.stopped && event.Type != project.TaskEventRunStopped {
		return ErrTaskEventTransition
	}
	advanced, err := applyTaskEvent(f.plan, f.pending, f.byTask, event)
	if err != nil {
		return err
	}
	for _, write := range advanced {
		f.emit(write)
	}
	if event.Type == project.TaskEventRunStopped {
		f.stopped = true
	}
	f.next = event.Sequence
	f.lastDigest = project.TaskEventDigest(event)
	return nil
}

// finishExecution computes failure containment over the folded state.
func (f *taskEventFold) finishExecution() {
	f.result.NextSequence = f.next
	failed := failedTasks(f.plan, f.pending)
	contained := len(failed) > 0
	if contained {
		cancelBlocked(f.plan, f.pending, failed, f.emit)
	}
	// A scheduler violation is recorded truthfully and stops the run: the
	// journal stays authoritative while the caller fences the over-limit
	// run instead of adding load to it.
	f.result.Stop = contained || f.stopped || overTaskLimits(f.plan, f.pending, f.imp.MaxChildren)
}

// receiptsEqual compares two receipts ignoring no field: the caller sets
// the CAS base before calling, so equality means the store would no-op.
func receiptsEqual(a, b store.PlanTaskReceipt) bool {
	if a.Revision != b.Revision {
		return false
	}
	aa, bb := a, b
	aa.Revision, bb.Revision = 0, 0
	return aa.AttemptRoot == bb.AttemptRoot && aa.PlanID == bb.PlanID &&
		aa.TaskID == bb.TaskID && aa.State == bb.State && aa.Child == bb.Child &&
		aa.Parent == bb.Parent &&
		aa.InputDigest == bb.InputDigest && aa.OutputDigest == bb.OutputDigest &&
		aa.Tree == bb.Tree && aa.Commit == bb.Commit && aa.Reason == bb.Reason &&
		strings.Join(aa.Events, "\x00") == strings.Join(bb.Events, "\x00") &&
		strings.Join(aa.Checks, "\x00") == strings.Join(bb.Checks, "\x00")
}

// eventReplayed reports whether an old sequence is already recorded on its
// task receipts. A lost reply replays the recorded bytes; anything else
// claiming an old sequence is a gap in our own evidence.
func eventReplayed(pending map[string]store.PlanTaskReceipt, event project.TaskEvent) bool {
	if event.Type == project.TaskEventRunStopped {
		for _, receipt := range pending {
			for _, ref := range receipt.Events {
				if ref == taskEventRef(event) {
					return true
				}
			}
		}
		return false
	}
	receipt, ok := pending[event.TaskKey]
	if !ok {
		return false
	}
	for _, ref := range receipt.Events {
		if ref == taskEventRef(event) {
			return true
		}
	}
	return false
}

// applyTaskEvent advances receipts for one fresh event. Terminal
// run_stopped cancels every non-terminal task; other types touch their
// named task only.
func applyTaskEvent(plan factory.InvestigationPlan, pending map[string]store.PlanTaskReceipt, byTask map[string]int, event project.TaskEvent) ([]store.PlanTaskReceipt, error) {
	if event.Type == project.TaskEventRunStopped {
		return cancelRunTasks(plan, pending, byTask, event)
	}
	if _, ok := byTask[event.TaskKey]; !ok {
		return nil, ErrTaskEventUnknown
	}
	receipt := pending[event.TaskKey]
	if terminalReceipt(receipt.State) {
		return nil, ErrTaskEventTransition
	}
	fold := &taskEventCase{plan: plan, receipt: receipt, ref: taskEventRef(event), event: event}
	switch event.Type {
	case project.TaskEventChildStartIntent:
		return fold.noteStartIntent()
	case project.TaskEventChildStarted:
		return fold.startChild()
	case project.TaskEventParentStarted:
		return fold.startParent()
	case project.TaskEventChildReported:
		return fold.reportChild()
	case project.TaskEventParentOutput:
		return fold.reportChild()
	case project.TaskEventCheckpointReady:
		return fold.readyCheckpoint()
	case project.TaskEventTaskCommitted:
		return fold.commitTask()
	case project.TaskEventTaskFailed:
		return fold.failTask()
	default:
		return nil, fmt.Errorf("%w: unknown task event type", ErrTaskEventTransition)
	}
}

// cancelRunTasks cancels every non-terminal task behind one run stop.
func cancelRunTasks(plan factory.InvestigationPlan, pending map[string]store.PlanTaskReceipt, byTask map[string]int, event project.TaskEvent) ([]store.PlanTaskReceipt, error) {
	if _, ok := byTask[event.TaskKey]; !ok {
		return nil, ErrTaskEventUnknown
	}
	var writes []store.PlanTaskReceipt
	for _, task := range plan.Tasks {
		receipt := pending[task.ID]
		if terminalReceipt(receipt.State) && task.ID != event.TaskKey {
			continue
		}
		next := receipt
		if !terminalReceipt(receipt.State) {
			next.State = store.PlanTaskCancelled
			next.Reason = boundSummary(event.Detail, "run stopped")
		}
		next.Events = appendRef(next.Events, taskEventRef(event))
		writes = append(writes, next)
	}
	return writes, nil
}

// taskEventCase carries one fresh event with its named receipt.
type taskEventCase struct {
	plan    factory.InvestigationPlan
	receipt store.PlanTaskReceipt
	ref     string
	event   project.TaskEvent
}

// seal records one task-state advance with its event ref.
func (fold *taskEventCase) seal(apply func(*store.PlanTaskReceipt)) ([]store.PlanTaskReceipt, error) {
	next := fold.receipt
	apply(&next)
	next.Events = appendRef(next.Events, fold.ref)
	return []store.PlanTaskReceipt{next}, nil
}

// noteStartIntent records one child start intent on its named task.
func (fold *taskEventCase) noteStartIntent() ([]store.PlanTaskReceipt, error) {
	return fold.seal(func(next *store.PlanTaskReceipt) {})
}

// startChild starts one task child, replaying its start intent when
// the child already runs under the same native identity.
func (fold *taskEventCase) startChild() ([]store.PlanTaskReceipt, error) {
	if fold.event.ChildID == "" {
		return nil, fmt.Errorf("%w: task child start lacks its native identity", ErrTaskEventTransition)
	}
	if fold.receipt.State != store.PlanTaskPending {
		if fold.receipt.Child != fold.event.ChildID {
			return nil, fmt.Errorf("%w: task already has its child", ErrTaskEventTransition)
		}
		return fold.seal(func(next *store.PlanTaskReceipt) {})
	}
	return fold.seal(func(next *store.PlanTaskReceipt) {
		next.State = store.PlanTaskRunning
		next.Child = fold.event.ChildID
	})
}

func (fold *taskEventCase) startParent() ([]store.PlanTaskReceipt, error) {
	if !fold.receipt.Parent || fold.event.ChildID != "" || fold.receipt.State != store.PlanTaskPending {
		return nil, ErrTaskEventTransition
	}
	return fold.seal(func(next *store.PlanTaskReceipt) { next.State = store.PlanTaskRunning })
}

// reportChild records one running child's output digest.
func (fold *taskEventCase) reportChild() ([]store.PlanTaskReceipt, error) {
	if !factory.ValidDigest(fold.event.Detail) {
		return nil, fmt.Errorf("%w: task report lacks its output digest", ErrTaskEventTransition)
	}
	if fold.receipt.State != store.PlanTaskRunning {
		return nil, fmt.Errorf("%w: task reported without its running child", ErrTaskEventTransition)
	}
	if fold.event.ChildID != "" && fold.event.ChildID != fold.receipt.Child {
		return nil, fmt.Errorf("%w: task report names a foreign child", ErrTaskEventTransition)
	}
	if fold.receipt.OutputDigest != "" && fold.receipt.OutputDigest != fold.event.Detail {
		return nil, fmt.Errorf("%w: task reported twice with different outputs", ErrTaskEventTransition)
	}
	return fold.seal(func(next *store.PlanTaskReceipt) {
		next.State = store.PlanTaskOutputReady
		next.OutputDigest = fold.event.Detail
	})
}

// readyCheckpoint records one checkpoint's verified plan checks.
func (fold *taskEventCase) readyCheckpoint() ([]store.PlanTaskReceipt, error) {
	if fold.receipt.State != store.PlanTaskRunning && fold.receipt.State != store.PlanTaskOutputReady {
		return nil, fmt.Errorf("%w: checkpoint precedes its task execution", ErrTaskEventTransition)
	}
	if fold.event.ChildID != "" && fold.receipt.Child != "" && fold.event.ChildID != fold.receipt.Child {
		return nil, fmt.Errorf("%w: checkpoint names a foreign child", ErrTaskEventTransition)
	}
	checks, err := parseCheckEvidence(fold.plan, fold.event.Detail)
	if err != nil {
		return nil, err
	}
	return fold.seal(func(next *store.PlanTaskReceipt) {
		next.Checks = unionKeys(next.Checks, checks)
	})
}

// commitTask completes one output-ready task with its commit evidence.
func (fold *taskEventCase) commitTask() ([]store.PlanTaskReceipt, error) {
	if fold.receipt.State != store.PlanTaskOutputReady || fold.receipt.OutputDigest == "" {
		return nil, fmt.Errorf("%w: task committed without its reported output", ErrTaskEventTransition)
	}
	if fold.event.ChildID != "" && fold.event.ChildID != fold.receipt.Child {
		return nil, fmt.Errorf("%w: task commit names a foreign child", ErrTaskEventTransition)
	}
	tree, commit, err := parseCommitEvidence(fold.event.Detail)
	if err != nil {
		return nil, err
	}
	return fold.seal(func(next *store.PlanTaskReceipt) {
		next.State = store.PlanTaskCompleted
		next.Tree, next.Commit = tree, commit
		next.Reason = boundSummary("task output accepted", "task output accepted")
	})
}

// failTask fails one running task with its reported reason.
func (fold *taskEventCase) failTask() ([]store.PlanTaskReceipt, error) {
	if fold.receipt.State != store.PlanTaskRunning && fold.receipt.State != store.PlanTaskOutputReady {
		return nil, fmt.Errorf("%w: task failed without its running child", ErrTaskEventTransition)
	}
	if fold.event.ChildID != "" && fold.event.ChildID != fold.receipt.Child {
		return nil, fmt.Errorf("%w: task failure names a foreign child", ErrTaskEventTransition)
	}
	return fold.seal(func(next *store.PlanTaskReceipt) {
		next.State = store.PlanTaskFailed
		next.Reason = boundSummary(fold.event.Detail, "task reported failure")
	})
}

// parseCheckEvidence reads checkpoint check evidence: space-separated plan
// check keys the executor verified. Empty detail carries none.
func parseCheckEvidence(plan factory.InvestigationPlan, detail string) ([]string, error) {
	if strings.TrimSpace(detail) == "" {
		return nil, nil
	}
	known := make(map[string]bool, len(plan.Checks))
	for _, check := range plan.Checks {
		known[check.ID] = true
	}
	var checks []string
	for _, id := range strings.Fields(detail) {
		if !factory.ValidPlanKey(id) || !known[id] {
			return nil, fmt.Errorf("%w: checkpoint names an unknown plan check", ErrTaskEventTransition)
		}
		checks = unionKeys(checks, []string{id})
	}
	return checks, nil
}

// parseCommitEvidence reads task_committed evidence: space-separated
// tree=<tree> and commit=<commit> pairs with 40-hex identities. Empty
// detail accepts a verification-only task with no commit; anything else
// must parse exactly, so model prose can never forge commit evidence.
func parseCommitEvidence(detail string) (tree, commit string, err error) {
	if strings.TrimSpace(detail) == "" {
		return "", "", nil
	}
	seen := make(map[string]bool)
	for _, field := range strings.Fields(detail) {
		key, value, found := strings.Cut(field, "=")
		if !found || seen[key] {
			return "", "", fmt.Errorf("%w: task commit evidence is malformed", ErrTaskEventTransition)
		}
		seen[key] = true
		if !factory.ValidCommit(value) {
			return "", "", fmt.Errorf("%w: task commit evidence lacks its identity", ErrTaskEventTransition)
		}
		switch key {
		case "tree":
			tree = value
		case "commit":
			commit = value
		default:
			return "", "", fmt.Errorf("%w: task commit evidence is malformed", ErrTaskEventTransition)
		}
	}
	return tree, commit, nil
}

// appendRef records one event ref once; replays stay byte-identical.
func appendRef(refs []string, ref string) []string {
	for _, existing := range refs {
		if existing == ref {
			return refs
		}
	}
	return append(refs, ref)
}

// unionKeys merges plan key sets without duplicates.
func unionKeys(into, from []string) []string {
	seen := make(map[string]bool, len(into)+len(from))
	var out []string
	for _, id := range into {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range from {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func terminalReceipt(state string) bool {
	return state == store.PlanTaskCompleted || state == store.PlanTaskFailed || state == store.PlanTaskCancelled
}

// failedTasks lists failed task keys in plan order.
func failedTasks(plan factory.InvestigationPlan, pending map[string]store.PlanTaskReceipt) []string {
	var failed []string
	for _, task := range plan.Tasks {
		if pending[task.ID].State == store.PlanTaskFailed {
			failed = append(failed, task.ID)
		}
	}
	return failed
}

// cancelBlocked cancels pending tasks whose prerequisites can never
// complete: anything transitively depending on a failed task. The reason
// names the nearest failed prerequisite in plan order, so a restart
// observes the block instead of releasing the descendant.
func cancelBlocked(plan factory.InvestigationPlan, pending map[string]store.PlanTaskReceipt, failed []string, emit func(store.PlanTaskReceipt)) {
	blocked := make(map[string]bool, len(failed))
	for _, id := range failed {
		blocked[id] = true
	}
	changed := true
	for changed {
		changed = false
		for _, task := range plan.Tasks {
			if blocked[task.ID] {
				continue
			}
			for _, dep := range task.DependsOn {
				if blocked[dep] {
					blocked[task.ID] = true
					changed = true
					break
				}
			}
		}
	}
	for _, task := range plan.Tasks {
		if !blocked[task.ID] {
			continue
		}
		receipt := pending[task.ID]
		if receipt.State != store.PlanTaskPending {
			continue
		}
		nearest := ""
		for _, dep := range task.DependsOn {
			if blocked[dep] {
				nearest = dep
				break
			}
		}
		if nearest == "" {
			for _, id := range failed {
				nearest = id
				break
			}
		}
		next := receipt
		next.State = store.PlanTaskCancelled
		next.Reason = "prerequisite " + nearest + " failed"
		emit(next)
	}
}

// overTaskLimits reports a scheduler violation the journal recorded
// anyway: more active tasks than admitted, or two actives in one lane.
func overTaskLimits(plan factory.InvestigationPlan, pending map[string]store.PlanTaskReceipt, maxChildren int) bool {
	active := 0
	lanes := make(map[string]int)
	for _, task := range plan.Tasks {
		if pending[task.ID].Parent && maxChildren != 0 {
			continue
		}
		switch pending[task.ID].State {
		case store.PlanTaskRunning, store.PlanTaskOutputReady:
			active++
			lanes[task.Lane]++
			if lanes[task.Lane] > 1 {
				return true
			}
		}
	}
	if maxChildren == 0 {
		return active > 1
	}
	return active > maxChildren
}
