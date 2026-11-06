package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// RequestDirection renders the effective direction of one build
// request: the owner's objective, the delivery approach, the explicit
// authorized outcome subset and the decision/source counts behind it.
// Idea bodies stay on the revision read; the overview carries direction
// and counts only.
type RequestDirection struct {
	IdeasByDecision   map[string]int `json:"ideas_by_decision"`
	IdeasBySource     map[string]int `json:"ideas_by_source"`
	EffectiveOutcomes []string       `json:"effective_outcomes"`
	Objective         string         `json:"objective"`
	Approach          string         `json:"approach,omitempty"`
	IdeasTotal        int            `json:"ideas_total"`
	TasksTotal        int            `json:"tasks_total"`
	Unresolved        int            `json:"unresolved_assumptions"`
}

func requestDirectionDTO(r factory.IntentRevision) RequestDirection {
	view := RequestDirection{
		IdeasByDecision: map[string]int{}, IdeasBySource: map[string]int{},
		EffectiveOutcomes: append([]string(nil), factory.ResolvedScope(r)...),
		Objective:         r.Objective, Approach: r.Approach,
		IdeasTotal: len(r.Ideas), TasksTotal: len(r.Tasks),
	}
	if view.EffectiveOutcomes == nil {
		view.EffectiveOutcomes = []string{}
	}
	for _, idea := range r.Ideas {
		view.IdeasByDecision[idea.Decision]++
		source := idea.Source
		if source == "" {
			// H-I v2: empty source is unclassified unfinished draft
			// input and infers no authorship.
			source = "unclassified"
		}
		view.IdeasBySource[source]++
	}
	view.Unresolved = len(requestUnresolvedAssumptions(r.Ideas))
	return view
}

// requestUnresolvedAssumptions returns the IDs of consequential
// assumptions still awaiting the owner: material assumptions with no
// recorded decision. Independent authorized work need not wait for
// them; their dependents stay pending.
func requestUnresolvedAssumptions(ideas []factory.ProductIdea) []string {
	unresolved := []string{}
	for _, idea := range ideas {
		if idea.Assumption && idea.Material && idea.Decision == "pending" {
			unresolved = append(unresolved, idea.ID)
		}
	}
	return unresolved
}

// RequestContext renders the request's exact bound context: the
// canonical repository and the private department label. Repository "0"
// means unbound; the draft stays owner-private either way.
type RequestContext struct {
	Department string `json:"department,omitempty"`
	Repository string `json:"repository"`
}

// RequestMapping renders one task's exact native mapping: the bound
// native issue and its acceptance. IDs only; native content stays
// behind native authorization.
type RequestMapping struct {
	TaskKey        string `json:"task_key"`
	IssueID        string `json:"issue_id"`
	IssueIndex     string `json:"issue_index"`
	AcceptanceID   string `json:"acceptance_id"`
	CreatedUnix    int64  `json:"created_unix"`
	ContentVersion int    `json:"content_version"`
	Effective      bool   `json:"effective"`
}

func requestMappingDTO(b factory.IntentTaskBinding, tasks map[string]factory.IntentTask, effective map[string]bool) RequestMapping {
	view := RequestMapping{
		TaskKey: b.TaskKey,
		IssueID: strconv.FormatInt(b.IssueID, 10), IssueIndex: strconv.FormatInt(b.IssueIndex, 10),
		AcceptanceID: b.AcceptanceID, CreatedUnix: b.CreatedUnix, ContentVersion: b.ContentVersion,
		Effective: requestMappingEffective(tasks[b.TaskKey], effective),
	}
	return view
}

// requestMappingEffective reports whether one mapped task stays within
// the revision's explicit authorized outcome subset. An empty subset
// means legacy unscoped review input and leaves mappings effective.
func requestMappingEffective(task factory.IntentTask, effective map[string]bool) bool {
	if len(effective) == 0 {
		return true
	}
	for _, id := range task.IdeaIDs {
		if !effective[id] {
			return false
		}
	}
	return true
}

// requestWorkStates name one mapped work item's evidenced state.
// Delivered needs verified merge and confirmed original closure;
// attention names failed, fenced or cancelled work.
const (
	requestWorkDelivered = "delivered"
	requestWorkWorking   = "working"
	requestWorkWaiting   = "waiting"
	requestWorkAttention = "attention"
)

type requestWorkInput struct {
	AssignmentOutcome   factory.Outcome
	MergeStage          string
	HasAssignment       bool
	HasMerge            bool
	MergeClosureChecked bool
}

// requestWorkState derives one work item's state from its recorded
// attempt and merge facts. It reports evidence only; product-outcome
// completion is tracked separately and never inferred from closure.
func requestWorkState(in requestWorkInput) string {
	if in.HasMerge && in.MergeStage == factory.MergeMerged && in.MergeClosureChecked {
		return requestWorkDelivered
	}
	if in.HasMerge && (in.MergeStage == factory.MergeFailed || in.MergeStage == factory.MergeWithdrawn || in.MergeStage == factory.MergeFenced) {
		return requestWorkAttention
	}
	if in.HasAssignment && (in.AssignmentOutcome == factory.Failed || in.AssignmentOutcome == factory.Cancelled || in.AssignmentOutcome == factory.NeedsHuman) {
		return requestWorkAttention
	}
	if in.HasAssignment || in.HasMerge {
		return requestWorkWorking
	}
	return requestWorkWaiting
}

// RequestCounts renders evidenced work-item counts plus the
// separate product-outcome evidence tally. Counts come from recorded
// facts; several delivered work items may satisfy only part of one
// accepted product outcome, so no percentage is ever computed.
type RequestCounts struct {
	ByState           map[string]int `json:"by_state"`
	OutcomesTotal     int            `json:"outcomes_total"`
	OutcomesEvidenced int            `json:"outcomes_evidenced"`
	Total             int            `json:"total"`
}

// requestAttentionCodes name what needs the owner on one request.
const (
	requestAttentionGeneration = "generation_refused"
	requestAttentionWork       = "work_attention"
	requestAttentionAssumption = "unresolved_assumption"
)

// RequestAttention names one item needing the owner: the code and
// the affected native issue or product idea where one exists.
type RequestAttention struct {
	Code  string `json:"code"`
	Issue string `json:"issue,omitempty"`
	Idea  string `json:"idea,omitempty"`
}

// requestGenerationStates name the native-graph generation facts. The
// recorded state renders verbatim; not_requested means no generation
// record exists for the current revision.
const RequestGenerationAbsent = "not_requested"

type RequestGeneration struct {
	State string `json:"state"`
}

// requestSetupStates name the environment setup facts for one request.
const (
	RequestSetupUnassigned  = "repository_unassigned"
	RequestSetupMissing     = "project_missing"
	RequestSetupPreparing   = "preparing"
	RequestSetupReady       = "ready"
	RequestSetupUnavailable = "unavailable"
)

type RequestSetup struct {
	Project string `json:"project,omitempty"`
	State   string `json:"state"`
}

// RequestOverview is the bounded request overview for one
// owner-private build request: direction, context, native mappings,
// evidenced work-item counts, attention, generation and setup state.
// Draft ownership and native repository visibility stay distinct:
// the read is owner-private, while mappings carry IDs that native
// authorization still governs.
type RequestOverview struct {
	Direction    RequestDirection   `json:"direction"`
	Context      RequestContext     `json:"context"`
	Generation   RequestGeneration  `json:"generation"`
	Setup        RequestSetup       `json:"setup"`
	WorkItems    RequestCounts      `json:"work_items"`
	Mappings     []RequestMapping   `json:"mappings"`
	Attention    []RequestAttention `json:"attention"`
	Intent       string             `json:"intent"`
	Revision     string             `json:"revision"`
	State        string             `json:"state"`
	ObservedUnix int64              `json:"observed_unix"`
}

// RequestSetupReader supplies runtime setup facts for a canonical repository.
// It must not provision environments or dispatch work while serving a read.
type RequestSetupReader func(context.Context, int64) (RequestSetup, error)

// Requests owns owner-private request reads and saves over the durable store.
// Hosts supply an admitted canonical actor and current WriteAccess for bound
// saves and bound verification allowance access. Setup is a read-only runtime
// port; missing setup cannot be represented as an unprepared or ready environment.
type Requests struct {
	Store       *store.Store
	Setup       RequestSetupReader
	WriteAccess RequestWriteAccess
}

var (
	ErrRequestNotFound         = errors.New("build request not found")
	ErrRequestUnavailable      = errors.New("request service is unavailable")
	ErrRequestSetupUnavailable = errors.New("request setup is unavailable")
	ErrRequestRevisionInvalid  = errors.New("invalid review revision")
)

// Revision returns the current head only for its admitted canonical owner.
func (s Requests) Revision(ctx context.Context, actor collaboration.ActorID, intent string) (factory.IntentRevision, error) {
	if actor <= 0 || !project.ValidDecisionID(intent) {
		return factory.IntentRevision{}, ErrRequestNotFound
	}
	if s.Store == nil {
		return factory.IntentRevision{}, ErrRequestUnavailable
	}
	head, err := s.Store.IntentRevision(ctx, intent, int64(actor))
	if errors.Is(err, store.ErrNotFound) {
		return factory.IntentRevision{}, ErrRequestNotFound
	}
	return head, err
}

// SavedRevision reads the exact owner-bound record of an earlier save. A later
// head does not invalidate that receipt, and the read grants no write authority.
func (s Requests) SavedRevision(ctx context.Context, actor collaboration.ActorID, intent, revision string) (factory.IntentRevision, error) {
	var empty factory.IntentRevision
	if actor <= 0 || !project.ValidDecisionID(intent) || !project.ValidDecisionID(revision) {
		return empty, ErrRequestNotFound
	}
	if s.Store == nil {
		return empty, ErrRequestUnavailable
	}
	result, err := s.Store.SavedIntentRevision(ctx, intent, revision, int64(actor))
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrRequestNotFound
	}
	return result, err
}

// Overview reads recorded facts only. Work delivery and verified product
// outcomes remain distinct; repository visibility does not grant draft access.
func (s Requests) Overview(ctx context.Context, actor collaboration.ActorID, intent string) (RequestOverview, error) {
	var empty RequestOverview
	revision, err := s.Revision(ctx, actor, intent)
	if err != nil {
		return empty, err
	}
	view := RequestOverview{
		Direction:  requestDirectionDTO(revision),
		Context:    RequestContext{Department: revision.Department, Repository: strconv.FormatInt(revision.Repository, 10)},
		Generation: RequestGeneration{State: RequestGenerationAbsent},
		WorkItems:  RequestCounts{ByState: map[string]int{}},
		Mappings:   []RequestMapping{}, Attention: []RequestAttention{},
		Intent: revision.Intent, Revision: revision.ID, State: revision.State,
		ObservedUnix: time.Now().Unix(),
	}
	view.WorkItems.OutcomesTotal = len(factory.ResolvedScope(revision))
	view.WorkItems.OutcomesEvidenced, err = s.Store.VerifiedApplicationOutcomeCount(ctx, revision)
	if err != nil {
		return empty, fmt.Errorf("read application verification evidence: %w", err)
	}
	if generation, err := s.Store.IntentGeneration(ctx, revision.ID); err == nil {
		view.Generation.State = generation.State
		if generation.State == "refused" {
			view.Attention = append(view.Attention, RequestAttention{Code: requestAttentionGeneration})
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, fmt.Errorf("read request generation: %w", err)
	}
	bindings, err := s.Store.IntentTaskBindings(ctx, revision.ID)
	if err != nil {
		return empty, fmt.Errorf("read request mappings: %w", err)
	}
	tasks := make(map[string]factory.IntentTask, len(revision.Tasks))
	for _, task := range revision.Tasks {
		tasks[task.Key] = task
	}
	effective := make(map[string]bool, len(factory.ResolvedScope(revision)))
	for _, id := range factory.ResolvedScope(revision) {
		effective[id] = true
	}
	for _, id := range requestUnresolvedAssumptions(revision.Ideas) {
		view.Attention = append(view.Attention, RequestAttention{Code: requestAttentionAssumption, Idea: id})
	}
	for _, binding := range bindings {
		view.Mappings = append(view.Mappings, requestMappingDTO(binding, tasks, effective))
		state, err := s.WorkItemState(ctx, revision.Repository, binding)
		if err != nil {
			return empty, err
		}
		view.WorkItems.ByState[state]++
		view.WorkItems.Total++
		if state == requestWorkAttention {
			view.Attention = append(view.Attention, RequestAttention{Code: requestAttentionWork, Issue: strconv.FormatInt(binding.IssueIndex, 10)})
		}
	}
	if revision.Repository <= 0 {
		view.Setup = RequestSetup{State: RequestSetupUnassigned}
	} else {
		if s.Setup == nil {
			view.Setup = RequestSetup{State: RequestSetupUnavailable}
		} else {
			view.Setup, err = s.Setup(ctx, revision.Repository)
			if err != nil {
				return empty, fmt.Errorf("read request setup: %w", err)
			}
		}
		switch view.Setup.State {
		case RequestSetupMissing, RequestSetupPreparing, RequestSetupReady, RequestSetupUnavailable:
		default:
			return empty, ErrRequestSetupUnavailable
		}
	}
	return view, nil
}

// WorkItemState projects an already admitted revision mapping. It does not
// authorize repository access or expose native issue content.
func (s Requests) WorkItemState(ctx context.Context, repository int64, binding factory.IntentTaskBinding) (string, error) {
	if s.Store == nil {
		return "", ErrRequestUnavailable
	}
	var in requestWorkInput
	if assignment, err := s.Store.LatestIssueAssignment(ctx, repository, binding.IssueIndex); err == nil {
		in.HasAssignment, in.AssignmentOutcome = true, assignment.Outcome
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("read work attempt: %w", err)
	}
	if merge, err := s.Store.MergeForIssue(ctx, repository, binding.IssueIndex); err == nil {
		in.HasMerge, in.MergeStage = true, merge.Stage
		in.MergeClosureChecked = merge.Stage == factory.MergeMerged && merge.OriginalClosedUnix > 0 && merge.OriginalClosingOccurrence != ""
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("read work merge: %w", err)
	}
	return requestWorkState(in), nil
}

// Review reports the saved current/stale/missing review without invoking a
// judge. Both the request head and any review must belong to the same owner.
func (s Requests) Review(ctx context.Context, actor collaboration.ActorID, intent, target string) (factory.SavedReviewRead, error) {
	var empty factory.SavedReviewRead
	head, err := s.Revision(ctx, actor, intent)
	if err != nil {
		return empty, err
	}
	if target == "" {
		target = head.ID
	}
	if !project.ValidDecisionID(target) {
		return empty, ErrRequestRevisionInvalid
	}
	var read factory.SavedReviewRead
	review, err := s.Store.IntentReview(ctx, target, int64(actor))
	switch {
	case errors.Is(err, store.ErrNotFound):
		read = factory.SavedReviewRead{Intent: intent, HeadRevision: head.ID, Status: factory.ReviewMissing}
	case err != nil:
		return empty, err
	case review.Intent != intent:
		return empty, ErrRequestNotFound
	default:
		if err := review.Validate(); err != nil {
			return empty, err
		}
		read = factory.SavedReviewRead{Intent: intent, HeadRevision: head.ID, Status: factory.ClassifyReview(head.ID, target, true), Review: &review}
	}
	if err := read.Validate(); err != nil {
		return empty, err
	}
	return read, nil
}
