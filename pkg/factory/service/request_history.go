package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// MaxRevisionComparisons bounds one scope-revision page. Each entry
// compares two durable revisions and classifies its mapped work, so
// pages stay small and explicit.
const MaxRevisionComparisons = 5

// DefaultRevisionComparisons covers the current revision against its
// predecessor: the usual change-direction review.
const DefaultRevisionComparisons = 2

// RevisionTaskChange renders one changed task: which structural
// fields differ plus the old and new objective/outcomes text when those
// changed. Added and removed tasks render whole; unchanged tasks count.
type RevisionTaskChange struct {
	ChangedFields []string `json:"changed_fields"`
	Key           string   `json:"key"`
	OldObjective  string   `json:"old_objective,omitempty"`
	NewObjective  string   `json:"new_objective,omitempty"`
	OldOutcomes   []string `json:"old_outcomes,omitempty"`
	NewOutcomes   []string `json:"new_outcomes,omitempty"`
}

// RevisionTaskDiff renders the structural task difference between
// two durable revisions: reused and changed keys in old task order,
// added bodies in new order. Task keys are the stable identity; bodies
// come from the compared revisions, never a speculative graph.
// UnchangedCount always equals len(Reused); it stays because the
// integrated V09 consumer parses it.
type RevisionTaskDiff struct {
	Added          []factory.IntentTask `json:"added"`
	Removed        []factory.IntentTask `json:"removed"`
	Changed        []RevisionTaskChange `json:"changed"`
	Reused         []string             `json:"reused"`
	UnchangedCount int                  `json:"unchanged_count"`
}

func revisionTaskChangedFields(old, current factory.IntentTask) []string {
	fields := []string{}
	if old.Objective != current.Objective {
		fields = append(fields, "objective")
	}
	if !slices.Equal(old.Outcomes, current.Outcomes) {
		fields = append(fields, "outcomes")
	}
	if !sameRevisionIDs(old.IdeaIDs, current.IdeaIDs) {
		fields = append(fields, "idea_ids")
	}
	if !sameRevisionIDs(old.DependsOn, current.DependsOn) {
		fields = append(fields, "depends_on")
	}
	return fields
}

func sameRevisionIDs(old, current []string) bool {
	if len(old) != len(current) {
		return false
	}
	seen := make(map[string]int, len(old))
	for _, id := range old {
		seen[id]++
	}
	for _, id := range current {
		seen[id]--
		if seen[id] < 0 {
			return false
		}
	}
	return true
}

// revisionTaskDiff renders the structural task difference from the
// authoritative I07 delta: reused/changed/removed keys in old task
// order, added bodies in new order, plus old-vs-new content for
// changed keys from the two compared revision bodies.
func revisionTaskDiff(delta factory.TaskDelta, old, current []factory.IntentTask) RevisionTaskDiff {
	diff := RevisionTaskDiff{
		Added: []factory.IntentTask{}, Removed: []factory.IntentTask{},
		Changed: []RevisionTaskChange{}, Reused: delta.Reused,
		UnchangedCount: len(delta.Reused),
	}
	if diff.Reused == nil {
		diff.Reused = []string{}
	}
	previous := make(map[string]factory.IntentTask, len(old))
	for _, task := range old {
		previous[task.Key] = task
	}
	now := make(map[string]factory.IntentTask, len(current))
	for _, task := range current {
		now[task.Key] = task
	}
	for _, key := range delta.Added {
		if task, ok := now[key]; ok {
			diff.Added = append(diff.Added, task)
		}
	}
	for _, key := range delta.Removed {
		if task, ok := previous[key]; ok {
			diff.Removed = append(diff.Removed, task)
		}
	}
	for _, key := range delta.Changed {
		before, okBefore := previous[key]
		next, okNext := now[key]
		if !okBefore || !okNext {
			continue
		}
		fields := revisionTaskChangedFields(before, next)
		change := RevisionTaskChange{ChangedFields: fields, Key: key}
		if slices.Contains(fields, "objective") {
			change.OldObjective, change.NewObjective = before.Objective, next.Objective
		}
		if slices.Contains(fields, "outcomes") {
			change.OldOutcomes, change.NewOutcomes = before.Outcomes, next.Outcomes
		}
		diff.Changed = append(diff.Changed, change)
	}
	return diff
}

// RevisionIdeaChange renders one idea's decision transition between
// two durable revisions: the recorded decisions plus whether its
// correction or substance changed.
type RevisionIdeaChange struct {
	ID                string `json:"id"`
	FromDecision      string `json:"from_decision"`
	ToDecision        string `json:"to_decision"`
	CorrectionChanged bool   `json:"correction_changed"`
	ContentChanged    bool   `json:"content_changed"`
}

// RevisionIdeaDiff renders the idea difference between two durable
// revisions. Idea IDs are the stable identity.
type RevisionIdeaDiff struct {
	Added   []factory.ProductIdea `json:"added"`
	Removed []factory.ProductIdea `json:"removed"`
	Changed []RevisionIdeaChange  `json:"changed"`
}

func revisionIdeaDiff(old, current []factory.ProductIdea) RevisionIdeaDiff {
	diff := RevisionIdeaDiff{Added: []factory.ProductIdea{}, Removed: []factory.ProductIdea{}, Changed: []RevisionIdeaChange{}}
	previous := make(map[string]factory.ProductIdea, len(old))
	for _, idea := range old {
		previous[idea.ID] = idea
	}
	seen := make(map[string]bool, len(current))
	for _, idea := range current {
		seen[idea.ID] = true
		before, ok := previous[idea.ID]
		if !ok {
			diff.Added = append(diff.Added, idea)
			continue
		}
		change := RevisionIdeaChange{
			ID: idea.ID, FromDecision: before.Decision, ToDecision: idea.Decision,
			CorrectionChanged: before.Correction != idea.Correction,
			ContentChanged:    before.Description != idea.Description || before.Consequence != idea.Consequence,
		}
		if change.FromDecision != change.ToDecision || change.CorrectionChanged || change.ContentChanged {
			diff.Changed = append(diff.Changed, change)
		}
	}
	for _, idea := range old {
		if !seen[idea.ID] {
			diff.Removed = append(diff.Removed, idea)
		}
	}
	return diff
}

// RevisionIDDiff renders added and removed IDs under set semantics.
type RevisionIDDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

func revisionIDDiff(old, current []string) RevisionIDDiff {
	diff := RevisionIDDiff{Added: []string{}, Removed: []string{}}
	before := make(map[string]bool, len(old))
	for _, id := range old {
		before[id] = true
	}
	now := make(map[string]bool, len(current))
	for _, id := range current {
		now[id] = true
		if !before[id] {
			diff.Added = append(diff.Added, id)
		}
	}
	for _, id := range old {
		if !now[id] {
			diff.Removed = append(diff.Removed, id)
		}
	}
	return diff
}

// RevisionScalarChange renders one changed scalar context value.
type RevisionScalarChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RevisionComparison renders the structural difference between one
// revision and its durable predecessor: tasks, ideas, effective
// outcomes and bound context. The first revision renders initial with
// empty diffs instead of comparing against nothing.
type RevisionComparison struct {
	Tasks        RevisionTaskDiff      `json:"tasks"`
	Ideas        RevisionIdeaDiff      `json:"ideas"`
	Effective    RevisionIDDiff        `json:"effective_outcomes"`
	Repository   *RevisionScalarChange `json:"repository_change,omitempty"`
	State        *RevisionScalarChange `json:"state_change,omitempty"`
	Initial      bool                  `json:"initial,omitempty"`
	EdgesChanged bool                  `json:"edges_changed"`
}

func revisionComparisonDTO(delta factory.TaskDelta, old, current factory.IntentRevision) RevisionComparison {
	diff := RevisionComparison{
		Tasks: revisionTaskDiff(delta, old.Tasks, current.Tasks), Ideas: revisionIdeaDiff(old.Ideas, current.Ideas),
		Effective:    revisionIDDiff(old.EffectiveOutcomes, current.EffectiveOutcomes),
		EdgesChanged: delta.EdgesChanged,
	}
	if old.Repository != current.Repository {
		diff.Repository = &RevisionScalarChange{From: strconv.FormatInt(old.Repository, 10), To: strconv.FormatInt(current.Repository, 10)}
	}
	if old.State != current.State {
		diff.State = &RevisionScalarChange{From: old.State, To: current.State}
	}
	return diff
}

func initialRevisionComparison() RevisionComparison {
	return RevisionComparison{
		Tasks: revisionTaskDiff(factory.TaskDelta{}, nil, nil), Ideas: revisionIdeaDiff(nil, nil),
		Effective: revisionIDDiff(nil, nil), Initial: true,
	}
}

// revisionAuthorityStates name whether one mapping's bound acceptance
// still carries head authority: current, superseded by a newer head,
// or unknown when no currency record exists for it.
const (
	revisionAuthorityCurrent    = "current"
	revisionAuthoritySuperseded = "superseded"
	revisionAuthorityUnknown    = "unknown"
)

// revisionAuthorityState maps the I07 currency read onto the rendered
// authority states. Unrecorded acceptances stay unknown, never current.
func revisionAuthorityState(current, found bool) string {
	if !found {
		return revisionAuthorityUnknown
	}
	if !current {
		return revisionAuthoritySuperseded
	}
	return revisionAuthorityCurrent
}

// RevisionGeneration renders one revision's confirmed submitted
// graph facts separately from its accepted direction: the generation
// state, the submitted task keys behind it, and the retained delivered
// refs bound to closed native history without mutation. DeliveredRefs
// pin preserved-closed issues and carry no merge proof; they must never
// be read as completed work. SubmittedDiffers marks a submitted graph
// whose keys diverge from the revision's accepted tasks; before S07
// adoption the two always match.
type RevisionGeneration struct {
	SubmittedKeys    []string                                  `json:"submitted_keys,omitempty"`
	DeliveredRefs    map[string]factory.GeneratedIssueIdentity `json:"delivered_refs,omitempty"`
	State            string                                    `json:"state"`
	SubmittedDiffers bool                                      `json:"submitted_differs,omitempty"`
}

func revisionGenerationDTO(generation factory.IntentGeneration, revision factory.IntentRevision) RevisionGeneration {
	view := RevisionGeneration{State: generation.State, SubmittedKeys: []string{}, DeliveredRefs: generation.Delivered}
	for _, task := range generation.SubmittedTasks {
		view.SubmittedKeys = append(view.SubmittedKeys, task.Key)
	}
	if len(generation.SubmittedTasks) > 0 {
		submitted := make(map[string]bool, len(generation.SubmittedTasks))
		for _, key := range view.SubmittedKeys {
			submitted[key] = true
		}
		if len(submitted) != len(revision.Tasks) {
			view.SubmittedDiffers = true
		} else {
			for _, task := range revision.Tasks {
				if !submitted[task.Key] {
					view.SubmittedDiffers = true
					break
				}
			}
		}
	}
	return view
}

// RevisionWork renders one revision's mapped work item with its
// content version, current evidenced state and authority freshness.
// States reuse the request-overview vocabulary; canceled or fenced work
// reports attention, never delivered.
type RevisionWork struct {
	TaskKey        string `json:"task_key"`
	Issue          string `json:"issue_index"`
	State          string `json:"state"`
	Authority      string `json:"authority"`
	ContentVersion int    `json:"content_version"`
}

// ScopeRevision renders one scope revision: its identity, lineage,
// structural comparison against its predecessor, confirmed submitted
// graph facts and affected mapped work with current states.
type ScopeRevision struct {
	Comparison    RevisionComparison `json:"comparison"`
	Generation    RevisionGeneration `json:"generation"`
	Work          []RevisionWork     `json:"work"`
	Revision      string             `json:"revision"`
	State         string             `json:"state"`
	Repository    string             `json:"repository"`
	GeneratedFrom string             `json:"generated_from,omitempty"`
	CreatedUnix   int64              `json:"created_unix"`
}

// RevisionHistory is the bounded scope-revision read for one
// owner-private build request: recent revisions newest-first, each with
// its structural delta and affected work states.
type RevisionHistory struct {
	Revisions    []ScopeRevision `json:"revisions"`
	Intent       string          `json:"intent"`
	ObservedUnix int64           `json:"observed_unix"`
	Truncated    bool            `json:"truncated,omitempty"`
}

var (
	ErrScopeHistoryInput       = errors.New("scope history limit must be between one and five")
	ErrScopeHistoryUnavailable = errors.New("scope history is unavailable")
	ErrScopeComparisonInvalid  = errors.New("scope revisions cannot be compared")
)

// ScopeHistory serves the admitted owner's recorded revisions, newest first.
// The extra predecessor provides a real comparison at the page boundary.
// Reads do not judge, generate, alter authority or advance work.
func (s Requests) ScopeHistory(ctx context.Context, actor collaboration.ActorID, intent string, limit int) (RevisionHistory, error) {
	var empty RevisionHistory
	if limit == 0 {
		limit = DefaultRevisionComparisons
	}
	if limit < 1 || limit > MaxRevisionComparisons {
		return empty, ErrScopeHistoryInput
	}
	if _, err := s.Revision(ctx, actor, intent); err != nil {
		return empty, err
	}
	chain, err := s.Store.IntentHistory(ctx, intent, int64(actor), limit+1)
	if err != nil {
		return empty, errors.Join(ErrScopeHistoryUnavailable, err)
	}
	view := RevisionHistory{Revisions: []ScopeRevision{}, Intent: intent, ObservedUnix: time.Now().Unix()}
	shown := chain
	if len(chain) > limit {
		shown, view.Truncated = chain[:limit], true
	}
	for i, revision := range shown {
		entry := ScopeRevision{
			Generation: RevisionGeneration{State: RequestGenerationAbsent},
			Work:       []RevisionWork{}, Revision: revision.ID, State: revision.State,
			Repository: strconv.FormatInt(revision.Repository, 10), CreatedUnix: revision.CreatedUnix,
		}
		if i+1 < len(chain) {
			delta, err := factory.DiffIntentTasks(chain[i+1], revision)
			if err != nil {
				return empty, errors.Join(ErrScopeComparisonInvalid, err)
			}
			entry.Comparison = revisionComparisonDTO(delta, chain[i+1], revision)
		} else if revision.Predecessor == "" {
			entry.Comparison = initialRevisionComparison()
		} else {
			return empty, ErrScopeHistoryUnavailable
		}
		if generated, err := s.Store.PreviousGeneratedIntent(ctx, intent, revision.ID); err == nil {
			entry.GeneratedFrom = generated.ID
		} else if !errors.Is(err, store.ErrNotFound) {
			return empty, errors.Join(ErrScopeHistoryUnavailable, err)
		}
		if generation, err := s.Store.IntentGeneration(ctx, revision.ID); err == nil {
			entry.Generation = revisionGenerationDTO(generation, revision)
		} else if !errors.Is(err, store.ErrNotFound) {
			return empty, errors.Join(ErrScopeHistoryUnavailable, err)
		}
		if err := s.scopeRevisionWork(ctx, &entry, revision); err != nil {
			return empty, err
		}
		view.Revisions = append(view.Revisions, entry)
	}
	return view, nil
}

func (s Requests) scopeRevisionWork(ctx context.Context, entry *ScopeRevision, revision factory.IntentRevision) error {
	bindings, err := s.Store.IntentTaskBindings(ctx, revision.ID)
	if err != nil {
		return errors.Join(ErrScopeHistoryUnavailable, err)
	}
	for _, binding := range bindings {
		state, err := s.WorkItemState(ctx, revision.Repository, binding)
		if err != nil {
			return err
		}
		authority := revisionAuthorityUnknown
		if binding.AcceptanceID != "" {
			current, found, err := s.Store.GeneratedIntentCurrent(ctx, binding.AcceptanceID)
			if err != nil {
				return errors.Join(ErrScopeHistoryUnavailable, err)
			}
			authority = revisionAuthorityState(current, found)
		}
		entry.Work = append(entry.Work, RevisionWork{
			TaskKey: binding.TaskKey, Issue: strconv.FormatInt(binding.IssueIndex, 10),
			State: state, Authority: authority, ContentVersion: binding.ContentVersion,
		})
	}
	return nil
}
