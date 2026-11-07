package runtime

import (
	"context"
	"errors"
	"slices"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
)

// Derive the exact intermediate set from the intended final graph and remaining
// operations. Never absorb a human-added edge into a freshly captured baseline.
func (s NativeGraphs) dependencyBase(ctx context.Context, g factory.IntentGeneration, p nativeGraphPacket, progress nativeGraphProgress, index int, root collaboration.IssueRef) ([]string, error) {
	revision, err := s.Store.IntentGenerationHead(ctx, g.RevisionID)
	if err != nil {
		return nil, err
	}
	scoped := revision
	scoped.Tasks = g.SubmittedTasks
	desired, err := control.IntentGraphPlan(scoped)
	if err != nil {
		return nil, err
	}
	refs, err := s.taskRefs(ctx, g, p, progress)
	if err != nil {
		return nil, err
	}
	expected := map[string]bool{}
	found := false
	title, body := "", ""
	for i, task := range g.SubmittedTasks {
		if refs[task.Key] != root {
			continue
		}
		if _, delivered := g.Delivered[task.Key]; delivered {
			return nil, errors.New("native graph cannot mutate delivered history")
		}
		found = true
		title, body = desired.Creates[i].Title, desired.Creates[i].Body
		for _, key := range task.DependsOn {
			ref, ok := refs[key]
			if !ok {
				return nil, errNativeReadScope
			}
			expected[ref.ExternalID] = true
		}
	}
	if !found {
		return nil, errNativeReadScope
	}
	for _, edge := range p.Graph.Dependencies[index-len(p.Graph.Creates)-len(p.Graph.Edits):] {
		issue, err := nativeGraphRef(edge.Issue, progress.creates)
		if err != nil {
			return nil, err
		}
		if issue != root {
			continue
		}
		blocking, err := nativeGraphRef(edge.Prerequisite, progress.creates)
		if err != nil {
			return nil, err
		}
		if edge.Remove {
			expected[blocking.ExternalID] = true
		} else {
			delete(expected, blocking.ExternalID)
		}
	}
	observed, _, err := s.ReadGeneratedIssue(ctx, p.Repository, root.Number, g.ActorID)
	if err != nil {
		return nil, err
	}
	issue := observed.Issue
	if observed.AuthorityRevision != g.NativeRev || issue == nil || !issue.Visible || !issue.Complete || issue.ID != root.ExternalID || issue.IsLocked || issue.IsClosed || issue.Title != title || issue.Content != body || observed.Dependencies == nil || !observed.Dependencies.Complete || len(observed.Dependencies.Items) != len(expected) {
		return nil, errors.New("native graph dependency base changed")
	}
	actual := map[string]bool{}
	for _, edge := range observed.Dependencies.Items {
		if !edge.Visible || !edge.Complete || !expected[edge.DependencyID] || actual[edge.DependencyID] {
			return nil, errors.New("native graph dependency base changed")
		}
		actual[edge.DependencyID] = true
	}
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
