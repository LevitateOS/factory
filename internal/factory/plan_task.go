// Immutable plan task, lane and check contracts. Plans are validated before
// coding starts: unique keys, explicit dependencies in topological order,
// exclusive ownership or explicit serialization, bounded sizes and required
// acceptance coverage. No unresolved mandatory fact enters an executable
// plan. FW07 persists revisions and receipts; FW12 admits them.
package factory

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Frozen D04 plan bounds.
const (
	MaxPlanTasks       = 64
	MaxPlanLanes       = 4
	MaxTaskOwnership   = 32
	MaxPlanChecks      = 16
	MaxInspectedRefs   = 64
	MaxPlanTextBytes   = 4 * 1024
	MaxPlanPathBytes   = 1024
	MaxCheckArgv       = 64
	MaxCheckArgvBytes  = 16 * 1024
	MaxEncodedPlan     = 64 * 1024
	MinCheckTimeoutSec = 1
	MaxCheckTimeoutSec = 600
)

var planKey = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidPlanKey reports whether key is an admissible plan-local
// task, lane or check key.
func ValidPlanKey(key string) bool { return planKey.MatchString(key) }

// Check scopes admitted for verification.
const (
	CheckScopeTask        = "task"
	CheckScopeIntegration = "integration"
)

// CheckSpec is one reusable verification: executed directly, never through
// an implicit shell. An explicitly declared script interpreter is allowed.
// Environment and secret custody stays with the prepared Project. Required
// integration checks run on the final exact candidate even when component
// checks already passed.
type CheckSpec struct {
	ID             string   `json:"id"`
	Argv           []string `json:"argv"`
	Cwd            string   `json:"cwd"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	Scope          string   `json:"scope"`
	Required       bool     `json:"required"`
}

func (c CheckSpec) Validate() error {
	if !ValidPlanKey(c.ID) {
		return fmt.Errorf("invalid check key %.40q: check ids are a lowercase letter then up to 31 of a-z, 0-9, _ or -", c.ID)
	}
	if c.Scope != CheckScopeTask && c.Scope != CheckScopeIntegration {
		return fmt.Errorf("check %q has invalid scope %.40q: use %q or %q", c.ID, c.Scope, CheckScopeTask, CheckScopeIntegration)
	}
	if c.TimeoutSeconds < MinCheckTimeoutSec || c.TimeoutSeconds > MaxCheckTimeoutSec {
		return fmt.Errorf("check %q timeout_seconds %d is outside %d-%d", c.ID, c.TimeoutSeconds, MinCheckTimeoutSec, MaxCheckTimeoutSec)
	}
	if len(c.Argv) == 0 || len(c.Argv) > MaxCheckArgv {
		return fmt.Errorf("check %q argv needs 1-%d arguments, got %d", c.ID, MaxCheckArgv, len(c.Argv))
	}
	total := 0
	for _, arg := range c.Argv {
		if arg == "" || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("check %q has an empty or NUL-bearing argv entry", c.ID)
		}
		total += len(arg)
	}
	if total > MaxCheckArgvBytes {
		return fmt.Errorf("check %q argv exceeds %d bytes in total", c.ID, MaxCheckArgvBytes)
	}
	if c.Cwd == "" {
		return fmt.Errorf("check %q needs cwd: use \".\" for the repository root or a clean relative path", c.ID)
	}
	if c.Cwd != "." && !validOwnershipEntry(c.Cwd) {
		return fmt.Errorf("check %q cwd %.80q is not a clean relative path (no leading /, no .., no globs, not .git)", c.ID, c.Cwd)
	}
	return nil
}

// validOwnershipEntry reports whether entry is an exact relative file or an
// explicit directory prefix. Globs, absolute paths, parent escapes and
// version-control or runtime state directories never count as ownership.
func validOwnershipEntry(entry string) bool {
	if entry == "" || len(entry) > MaxPlanPathBytes {
		return false
	}
	// An explicit directory prefix ends in exactly one slash; an exact
	// file never does.
	base := strings.TrimSuffix(entry, "/")
	if base == "" || base != path.Clean(base) || strings.HasSuffix(base, "/") {
		return false
	}
	entry = base
	if path.IsAbs(entry) || entry == "." || entry == ".." || strings.HasPrefix(entry, "../") || strings.Contains(entry, "/../") {
		return false
	}
	if strings.ContainsAny(entry, "*?[]{}\\") || strings.ContainsRune(entry, 0) {
		return false
	}
	for _, part := range strings.Split(entry, "/") {
		if part == ".git" || part == ".soda-home" {
			return false
		}
	}
	return true
}

// PlanTask is one unit of planned work: unique key, objective, expected
// outputs, acceptance coverage, dependency keys, home lane, exclusive paths
// or interfaces, and check references. Dependencies name task keys, never
// ordinal positions.
type PlanTask struct {
	ID             string   `json:"id"`
	Objective      string   `json:"objective"`
	Outputs        []string `json:"outputs"`
	AcceptanceRefs []string `json:"acceptance_refs"`
	DependsOn      []string `json:"depends_on"`
	Lane           string   `json:"lane"`
	OwnedPaths     []string `json:"owned_paths"`
	InterfaceWrite []string `json:"interface_writes"`
	CheckIDs       []string `json:"check_ids"`
}

// PlanLane groups ordered task keys that run one at a time.
type PlanLane struct {
	ID      string   `json:"id"`
	TaskIDs []string `json:"task_ids"`
}

// ValidatePlanGraph checks one task/lane/check set: key shape and
// uniqueness, lane membership, topological dependency order, ownership
// exclusivity across potentially concurrent tasks, check references and
// frozen bounds. Lanes run one task at a time, so same-lane tasks are
// never concurrent; cross-lane tasks without an ordering dependency must
// not share ownership or interface writes.
func ValidatePlanGraph(tasks []PlanTask, lanes []PlanLane, checks []CheckSpec) error {
	if len(tasks) == 0 || len(tasks) > MaxPlanTasks {
		return fmt.Errorf("plan needs 1-%d tasks, got %d", MaxPlanTasks, len(tasks))
	}
	if len(lanes) == 0 || len(lanes) > MaxPlanLanes {
		return fmt.Errorf("plan needs 1-%d lanes, got %d", MaxPlanLanes, len(lanes))
	}
	if len(checks) > MaxPlanChecks {
		return fmt.Errorf("plan allows at most %d checks, got %d", MaxPlanChecks, len(checks))
	}
	knownChecks, err := indexPlanChecks(checks)
	if err != nil {
		return err
	}
	knownLanes, laneOf, seenLaneTasks, err := indexPlanLanes(lanes)
	if err != nil {
		return err
	}
	byKey := make(map[string]int, len(tasks))
	for i, task := range tasks {
		if !ValidPlanKey(task.ID) {
			return fmt.Errorf("invalid task key %.40q: task ids are a lowercase letter then up to 31 of a-z, 0-9, _ or -", task.ID)
		}
		if _, dup := byKey[task.ID]; dup {
			return fmt.Errorf("duplicate task key %q: every task id must be unique", task.ID)
		}
		byKey[task.ID] = i
		if err := checkPlanTask(task, knownLanes, laneOf, knownChecks); err != nil {
			return err
		}
		// Dependencies name earlier task keys: the array is topologically
		// ordered and every declared task is required.
		if err := checkPlanTaskDeps(task, i, byKey); err != nil {
			return err
		}
	}
	if len(seenLaneTasks) != len(tasks) {
		return errors.New("lane membership does not cover every task: list each task id in exactly one lane's task_ids")
	}
	return checkPlanConcurrency(tasks, byKey)
}

func checkPlanTaskDeps(task PlanTask, i int, byKey map[string]int) error {
	for _, dep := range task.DependsOn {
		pos, ok := byKey[dep]
		if !ok || dep == task.ID {
			return fmt.Errorf("task %q depends on unknown task %q: depends_on lists earlier task ids only; name checks in check_ids", task.ID, dep)
		}
		if pos > i {
			return fmt.Errorf("task %q depends on %q, which is not earlier in plan order", task.ID, dep)
		}
	}
	return nil
}

func checkPlanConcurrency(tasks []PlanTask, byKey map[string]int) error {
	// Serialize conflicting ownership across potentially concurrent tasks:
	// cross-lane pairs without an ordering dependency in either direction
	// must neither overlap paths nor share an interface write.
	ordered := func(a, b int) bool {
		seen := make(map[int]bool)
		var visit func(int)
		visit = func(n int) {
			if seen[n] {
				return
			}
			seen[n] = true
			for _, dep := range tasks[n].DependsOn {
				visit(byKey[dep])
			}
		}
		visit(a)
		if seen[b] {
			return true
		}
		seen = make(map[int]bool)
		visit(b)
		return seen[a]
	}
	for i := range tasks {
		for j := i + 1; j < len(tasks); j++ {
			if tasks[i].Lane == tasks[j].Lane || ordered(i, j) {
				continue
			}
			if err := checkPlanTaskPair(tasks[i], tasks[j]); err != nil {
				return err
			}
		}
	}
	return nil
}

func planPathOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") ||
		strings.HasPrefix(a, strings.TrimSuffix(b, "/")+"/") ||
		strings.HasPrefix(b, strings.TrimSuffix(a, "/")+"/")
}

func checkPlanTaskPair(a, b PlanTask) error {
	for _, owned := range a.OwnedPaths {
		for _, other := range b.OwnedPaths {
			if planPathOverlap(owned, other) {
				return fmt.Errorf("tasks %q and %q can run at once and both own %q and %q: put them in one lane or make one depend on the other", a.ID, b.ID, owned, other)
			}
		}
	}
	for _, written := range a.InterfaceWrite {
		for _, other := range b.InterfaceWrite {
			if written == other {
				return fmt.Errorf("tasks %q and %q can run at once and both write interface %q: put them in one lane or make one depend on the other", a.ID, b.ID, written)
			}
		}
	}
	return nil
}

func indexPlanChecks(checks []CheckSpec) (map[string]bool, error) {
	knownChecks := make(map[string]bool, len(checks))
	for _, c := range checks {
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if knownChecks[c.ID] {
			return nil, fmt.Errorf("duplicate check key %q: every check id must be unique", c.ID)
		}
		knownChecks[c.ID] = true
	}
	return knownChecks, nil
}

func indexPlanLanes(lanes []PlanLane) (map[string]int, map[string]string, map[string]bool, error) {
	knownLanes := make(map[string]int, len(lanes))
	for i, lane := range lanes {
		if !ValidPlanKey(lane.ID) {
			return nil, nil, nil, fmt.Errorf("invalid lane key %.40q: lane ids are a lowercase letter then up to 31 of a-z, 0-9, _ or -", lane.ID)
		}
		if _, dup := knownLanes[lane.ID]; dup {
			return nil, nil, nil, fmt.Errorf("duplicate lane key %q: every lane id must be unique", lane.ID)
		}
		knownLanes[lane.ID] = i
	}
	laneOf := make(map[string]string)
	seenLaneTasks := make(map[string]bool)
	for _, lane := range lanes {
		for _, id := range lane.TaskIDs {
			if !ValidPlanKey(id) {
				return nil, nil, nil, fmt.Errorf("lane %q task_ids holds invalid task key %.40q", lane.ID, id)
			}
			if seenLaneTasks[id] {
				return nil, nil, nil, fmt.Errorf("task %q is listed in two lanes; list it in exactly one", id)
			}
			seenLaneTasks[id] = true
			laneOf[id] = lane.ID
		}
	}
	return knownLanes, laneOf, seenLaneTasks, nil
}

func checkPlanTask(task PlanTask, knownLanes map[string]int, laneOf map[string]string, knownChecks map[string]bool) error {
	if laneOf[task.ID] == "" || laneOf[task.ID] != task.Lane {
		return fmt.Errorf("task %q lane %.40q does not match the lane whose task_ids list it (%q)", task.ID, task.Lane, laneOf[task.ID])
	}
	if _, ok := knownLanes[task.Lane]; !ok {
		return fmt.Errorf("task %q names unknown lane %.40q", task.ID, task.Lane)
	}
	if task.Objective == "" || len(task.Objective) > MaxPlanTextBytes {
		return fmt.Errorf("task %q objective must be 1-%d bytes", task.ID, MaxPlanTextBytes)
	}
	if len(task.Outputs) == 0 {
		return fmt.Errorf("task %q lacks outputs: name at least one expected output", task.ID)
	}
	for _, text := range append(append([]string(nil), task.Outputs...), task.AcceptanceRefs...) {
		if text == "" || len(text) > MaxPlanTextBytes {
			return fmt.Errorf("task %q has an empty or over-%d-byte output or acceptance_refs entry", task.ID, MaxPlanTextBytes)
		}
	}
	if len(task.AcceptanceRefs) == 0 {
		return fmt.Errorf("task %q lacks acceptance_refs: name the acceptance criteria it covers", task.ID)
	}
	return checkPlanTaskRefs(task, knownChecks)
}

func checkPlanTaskRefs(task PlanTask, knownChecks map[string]bool) error {
	if len(task.OwnedPaths) > MaxTaskOwnership {
		return fmt.Errorf("task %q owns %d paths; at most %d are allowed", task.ID, len(task.OwnedPaths), MaxTaskOwnership)
	}
	for _, entry := range task.OwnedPaths {
		if !validOwnershipEntry(entry) {
			return fmt.Errorf("task %q owned_paths entry %.80q is not an exact relative file or a directory prefix ending in one / (no leading /, no .., no globs, not .git)", task.ID, entry)
		}
	}
	for _, key := range task.InterfaceWrite {
		if key == "" || len(key) > 256 {
			return fmt.Errorf("task %q has an empty or over-256-byte interface_writes entry", task.ID)
		}
	}
	for _, id := range task.CheckIDs {
		if !knownChecks[id] {
			return fmt.Errorf("task %q check_ids names unknown check %.40q: declare it in checks first", task.ID, id)
		}
	}
	return nil
}
