package factory

import (
	"errors"
	"sort"
)

// TaskDelta describes scope movement between two revisions of one intent.
// Reused keys keep their native issue identities across the revision;
// changed keys are edited in place while open and recreated under the stable
// key once delivered; added keys create new native issues; removed keys
// keep their retained native records, with a visible supersession note while
// open and silently once delivered, and grant no current authority. The
// native writer refuses closed edits atomically, so delivered records never
// appear in a delta packet. "Delivered" throughout this contract means
// closed and preserved unmutated; it asserts nothing about verified merged
// work, which only merge/publication evidence can prove.
type TaskDelta struct {
	OldRevision  string   `json:"old_revision"`
	NewRevision  string   `json:"new_revision"`
	Reused       []string `json:"reused"`
	Changed      []string `json:"changed"`
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
	EdgesChanged bool     `json:"edges_changed"`
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sameTaskContent(a, b IntentTask) bool {
	if a.Objective != b.Objective || len(a.Outcomes) != len(b.Outcomes) {
		return false
	}
	for i := range a.Outcomes {
		if a.Outcomes[i] != b.Outcomes[i] {
			return false
		}
	}
	aIdeas, bIdeas := sortedCopy(a.IdeaIDs), sortedCopy(b.IdeaIDs)
	if len(aIdeas) != len(bIdeas) {
		return false
	}
	for i := range aIdeas {
		if aIdeas[i] != bIdeas[i] {
			return false
		}
	}
	aDeps, bDeps := sortedCopy(a.DependsOn), sortedCopy(b.DependsOn)
	if len(aDeps) != len(bDeps) {
		return false
	}
	for i := range aDeps {
		if aDeps[i] != bDeps[i] {
			return false
		}
	}
	return true
}

// DiffIntentTasks compares two revisions of the same intent. Deltas across
// different intent identities are meaningless: a repository change after
// commitment creates a separate request, never a retargeted revision.
func DiffIntentTasks(old, current IntentRevision) (TaskDelta, error) {
	if old.Intent == "" || current.Intent == "" || old.Intent != current.Intent {
		return TaskDelta{}, errors.New("task delta needs two revisions of one intent")
	}
	d := TaskDelta{OldRevision: old.ID, NewRevision: current.ID, Reused: []string{}, Changed: []string{}, Added: []string{}, Removed: []string{}}
	before := map[string]IntentTask{}
	for _, task := range old.Tasks {
		before[task.Key] = task
	}
	after := map[string]IntentTask{}
	for _, task := range current.Tasks {
		after[task.Key] = task
	}
	for _, task := range old.Tasks {
		next, ok := after[task.Key]
		if !ok {
			d.Removed = append(d.Removed, task.Key)
			continue
		}
		if sameTaskContent(task, next) {
			d.Reused = append(d.Reused, task.Key)
		} else {
			d.Changed = append(d.Changed, task.Key)
		}
	}
	for _, task := range current.Tasks {
		if _, ok := before[task.Key]; !ok {
			d.Added = append(d.Added, task.Key)
		}
	}
	oldEdges := map[string]string{}
	for _, task := range old.Tasks {
		for _, dep := range task.DependsOn {
			oldEdges[task.Key+"\x00"+dep] = ""
		}
	}
	for _, task := range current.Tasks {
		for _, dep := range task.DependsOn {
			key := task.Key + "\x00" + dep
			if _, ok := oldEdges[key]; !ok {
				d.EdgesChanged = true
			}
			delete(oldEdges, key)
		}
	}
	d.EdgesChanged = d.EdgesChanged || len(oldEdges) > 0
	return d, nil
}
