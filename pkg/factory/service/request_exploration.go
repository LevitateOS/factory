package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// RequestExploration owns private exploration and recorded walkthrough advice.
// Commands use the existing durable judgment producer; reads never wire a chooser.
type RequestExploration struct {
	Requests     Requests
	Chooser      control.ProductJudgment
	RetryCommand string
}

var (
	ErrExplorationProposalInvalid     = errors.New("invalid derived proposal")
	ErrExplorationTaskLimit           = errors.New("derived proposal exceeds task limit")
	ErrPlanningWalkthroughInvalid     = errors.New("saved walkthrough does not match this direction")
	ErrPlanningWalkthroughUnavailable = errors.New("plan has no completed authored walkthrough")
)

func intentID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "d" + hex.EncodeToString(raw[:]), nil
}

// Exploration carries the recorded product advice beside a revision.
// ExplorationResult keeps the revision fields flat for both frontend hosts.
type Exploration struct {
	Content        factory.ProposalContent       `json:"content"`
	Challenges     []factory.AssumptionChallenge `json:"challenges"`
	Settled        []string                      `json:"settled"`
	Recommendation *factory.ConsolidatedProposal `json:"recommendation,omitempty"`
	Consistency    *factory.ConsistencyReport    `json:"consistency,omitempty"`
}

type ExplorationResult struct {
	factory.IntentRevision
	Exploration Exploration `json:"exploration"`
}

func attachExploration(proposal factory.IntentRevision, content factory.ProposalContent, challenges []factory.AssumptionChallenge, settled []string, recommendation *factory.ConsolidatedProposal, consistency *factory.ConsistencyReport) ExplorationResult {
	if challenges == nil {
		challenges = []factory.AssumptionChallenge{}
	}
	if settled == nil {
		settled = []string{}
	}
	return ExplorationResult{IntentRevision: proposal, Exploration: Exploration{Content: content, Challenges: challenges, Settled: settled, Recommendation: recommendation, Consistency: consistency}}
}

func settledIDs(review factory.AssumptionReview) []string {
	ids := make([]string, 0, len(review.Settled))
	for _, settlement := range review.Settled {
		ids = append(ids, settlement.Idea)
	}
	return ids
}

const designRubric = "Using the full supplied objective and reviewed choices, choose a bounded implementation approach. Respect an explicitly selected approach; choose clarify if a declined or corrected assumption contradicts the objective or material outcomes remain unresolved. Do not impose declined assumptions or invent product capabilities. This advice grants no acceptance, repository authority or spending permission."

func mintExplorationProposal(current factory.IntentRevision) ([]string, factory.IntentRevision, error) {
	ids := make([]string, 104)
	for i := range ids {
		var err error
		ids[i], err = intentID()
		if err != nil {
			return nil, factory.IntentRevision{}, err
		}
	}
	proposal := factory.ProposeIntent(current, ids)
	proposal.Predecessor = current.ID
	var err error
	proposal.ID, err = intentID()
	if err != nil {
		return nil, factory.IntentRevision{}, err
	}
	proposal.CreatedUnix = time.Now().Unix()
	return ids, proposal, nil
}

func validateExploration(current, proposal factory.IntentRevision) error {
	if err := proposal.Validate(); err != nil {
		if len(proposal.Tasks) > current.MaxIssues {
			return fmt.Errorf("%w: The derived proposal carries %d tasks for max_issues %d: %v", ErrExplorationTaskLimit, len(proposal.Tasks), current.MaxIssues, err)
		}
		return errors.Join(ErrExplorationProposalInvalid, err)
	}
	return nil
}

func (s RequestExploration) persist(ctx context.Context, current factory.IntentRevision, content factory.ProposalContent, proposal factory.IntentRevision, review factory.AssumptionReview, recommendation *factory.ConsolidatedProposal, consistency *factory.ConsistencyReport) (ExplorationResult, error) {
	if err := validateExploration(current, proposal); err != nil {
		return ExplorationResult{}, err
	}
	challenges := review.Challenges
	if challenges == nil {
		challenges = []factory.AssumptionChallenge{}
	}
	saved := factory.SavedReview{
		Intent: current.Intent, BasisRevision: current.ID, ResultRevision: proposal.ID,
		Content: content, Challenges: challenges, Settled: settledIDs(review),
		Recommendation: recommendation, Consistency: consistency, SavedUnix: time.Now().Unix(),
	}
	if err := s.Requests.Store.SaveIntentRevisionWithReview(ctx, proposal, saved); err != nil {
		return ExplorationResult{}, err
	}
	return attachExploration(proposal, content, review.Challenges, settledIDs(review), recommendation, consistency), nil
}

// Explore produces and atomically saves a draft revision and its actual review.
// Private ownership admits exploration; the result grants no repository effects
// or execution authority. Acceptance and dispatch retain their separate gates.
func (s RequestExploration) Explore(ctx context.Context, actor collaboration.ActorID, intent, revision string) (ExplorationResult, error) {
	var empty ExplorationResult
	current, err := s.Requests.Revision(ctx, actor, intent)
	if err != nil {
		return empty, err
	}
	if current.ID != revision || current.State != "draft" {
		return empty, store.ErrStaleRevision
	}
	content, err := factory.ComposeProposalContent(current)
	if err != nil {
		return empty, err
	}
	ids, proposal, err := mintExplorationProposal(current)
	if err != nil {
		return empty, err
	}
	coord := &control.Coordinator{Store: s.Requests.Store, Planning: &control.PlanningJudgments{Chooser: s.Chooser}}
	design := current
	design.ID, design.Predecessor, design.CreatedUnix, design.Tasks = "", "", 0, nil
	answer, _, err := coord.AdvisePurpose(ctx, control.PurposeAdvice{
		Repository: current.Repository,
		Purpose:    factory.JudgmentProductApproaches, State: design, Question: designRubric,
		Options: intentDesignCriteria(), Outcome: "product_design", Candidates: intentDesignCandidates(current),
		Admit: func(ctx context.Context, snapshot string) (bool, error) {
			return true, s.Requests.Store.CheckIntentJudgment(ctx, current)
		},
	})
	if err != nil {
		return empty, fmt.Errorf("design consultation: %w", err)
	}
	proposal = applyIntentDesign(current, ids, proposal, answer)
	if answer.Selected == "clarify" || answer.Confidence < .25 {
		proposal.Tasks = []factory.IntentTask{}
		proposal = appendClarifyOnce(proposal, ids[103])
	}
	if err := validateExploration(current, proposal); err != nil {
		return empty, err
	}
	review, err := coord.JudgeAssumptions(ctx, current, content, proposal.Ideas)
	if err != nil {
		return empty, fmt.Errorf("assumption review: %w", err)
	}
	proposal = factory.SettleAssumptions(proposal, review)
	var recommendation *factory.ConsolidatedProposal
	if content.Supported {
		consolidated, err := coord.JudgeConsolidation(ctx, current, content)
		if err != nil {
			return empty, fmt.Errorf("consolidation: %w", err)
		}
		recommendation = &consolidated
	}
	var consistency *factory.ConsistencyReport
	if recommendation != nil {
		checked, err := coord.JudgeConsistency(ctx, current, content, *recommendation)
		if err != nil {
			return empty, fmt.Errorf("consistency review: %w", err)
		}
		consistency = &checked
	}
	return s.persist(ctx, current, content, proposal, review, recommendation, consistency)
}

// clarifyDescription and clarifyConsequence pin the one material product
// question blocking implementation. Matching reuses these exact texts so
// the carried-forward clarification keeps a stable identity and answers
// can settle instead of spawning a duplicate each round.
const (
	clarifyDescription = "Describe the first user action and the result that should count as success"
	clarifyConsequence = "Implementation waits until this material product question is resolved."
)

// clarifyIdea asks the one material product question blocking implementation.
// It carries autonomous assumption basis, never human approval.
func clarifyIdea(id string) factory.ProductIdea {
	return factory.ProductIdea{ID: id, Description: clarifyDescription, Consequence: clarifyConsequence, Assumption: true, Decision: "pending", Source: "assumption", Provenance: "autonomous", Material: true}
}

// isClarification reports whether idea is the producer-owned material
// question, pending or already answered. It matches assumption-source
// ideas carrying the exact pinned texts; ordinary human ideas never
// match, so no human decision is coalesced or rewritten.
func isClarification(idea factory.ProductIdea) bool {
	return idea.Assumption && idea.Source == "assumption" && idea.Material &&
		idea.Description == clarifyDescription && idea.Consequence == clarifyConsequence
}

// appendClarifyOnce reuses the carried-forward producer-owned
// clarification for the unchanged material question. A pending one keeps
// its stable identity so challenges and answers settle on it; an
// answered one preserves the recorded answer. Either way no duplicate is
// appended.
func appendClarifyOnce(proposal factory.IntentRevision, id string) factory.IntentRevision {
	for _, idea := range proposal.Ideas {
		if isClarification(idea) {
			return proposal
		}
	}
	proposal.Ideas = append(proposal.Ideas, clarifyIdea(id))
	return proposal
}

func intentDesignCriteria() map[string]any {
	return map[string]any{"complete_first": "One task per reviewed product choice combines implementation and verification outcomes", "incremental": "Two tasks per reviewed product choice separate implementation and dependent verification", "clarify": "No work graph until consequential scope contradictions or missing product outcomes are reviewed"}
}

func intentDesignCandidates(r factory.IntentRevision) []factory.PriorityCandidate {
	result := []factory.PriorityCandidate{}
	for _, approach := range []string{"complete_first", "incremental"} {
		if r.Approach != "" && r.Approach != approach {
			continue
		}
		result = append(result, factory.PriorityCandidate{ID: approach, Objective: r.Objective, Impact: intentDesignCriteria()[approach].(string)})
	}
	return append(result, factory.PriorityCandidate{ID: "clarify", Objective: r.Objective, Risk: intentDesignCriteria()["clarify"].(string)})
}

func applyIntentDesign(current factory.IntentRevision, ids []string, proposal factory.IntentRevision, answer factory.PriorityAnswer) factory.IntentRevision {
	if answer.Confidence < .25 || answer.Selected == "clarify" {
		return proposal
	}
	approach := current.Approach
	if approach == "" {
		approach = answer.Selected
	}
	current.Approach = approach
	derived := factory.ProposeIntent(current, ids)
	proposal.Approach, proposal.Tasks = derived.Approach, derived.Tasks
	return proposal
}

// ExplorationReceipt names a saved transition, not an in-flight judgment or a
// process launch. None does not rule out a consultation still finishing.
type ExplorationReceipt struct {
	Intent        string             `json:"intent"`
	BasisRevision string             `json:"basis_revision"`
	State         string             `json:"state"`
	Result        *ExplorationResult `json:"result,omitempty"`
}

// SavedExploration reads the original input's atomically saved proposal/review,
// even after the head advances. No chooser, funding or dispatch is reachable.
func (s Requests) SavedExploration(ctx context.Context, actor collaboration.ActorID, intent, basis string) (ExplorationReceipt, error) {
	var empty ExplorationReceipt
	selected, err := s.SavedRevision(ctx, actor, intent, basis)
	if err != nil {
		return empty, err
	}
	if selected.State != "draft" || selected.Validate() != nil {
		return empty, ErrRequestNotFound
	}
	review, err := s.Store.IntentExplorationReview(ctx, intent, basis, int64(actor))
	receipt := ExplorationReceipt{Intent: intent, BasisRevision: basis, State: "none"}
	if errors.Is(err, store.ErrNotFound) {
		return receipt, nil
	}
	if err != nil {
		return empty, err
	}
	if err := review.Validate(); err != nil {
		return empty, err
	}
	proposal, err := s.SavedRevision(ctx, actor, intent, review.ResultRevision)
	if err != nil {
		return empty, err
	}
	if proposal.State != "draft" || proposal.Predecessor != selected.ID || proposal.Repository != selected.Repository || proposal.Validate() != nil {
		return empty, ErrRequestNotFound
	}
	result := attachExploration(proposal, review.Content, review.Challenges, review.Settled, review.Recommendation, review.Consistency)
	receipt.State, receipt.Result = "recorded", &result
	return receipt, nil
}
