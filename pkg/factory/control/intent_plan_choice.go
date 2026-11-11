package control

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/levitateos/factory/pkg/collaboration"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// ErrPlanChoiceStale reports a recorded decomposition plan that no longer
// answers the accepted direction: the world moved (another draft reviewed,
// revised scope, another source commit or preparation ready, another head
// accepted) after the run completed. The caller revises the direction and
// plans again; the stale answer never funds a choice.
var ErrPlanChoiceStale = errors.New("recorded plan no longer answers the accepted direction")

// ErrPlanChoiceAttention reports a recorded decomposition answer that
// needs its product decision before native work. The attention text
// travels in the wrapped message; no choice is funded.
var ErrPlanChoiceAttention = errors.New("recorded plan needs a product decision first")

// ErrPlanChoiceStore reports a planning read the choice binding could
// not complete. Serving maps it to an infrastructure failure, never
// to a product refusal.
var ErrPlanChoiceStore = errors.New("planning store unavailable")

// ErrPlanChoiceReview reports a recorded decomposition answer that never
// completed its explicit walkthrough check: the plan carries no
// request-specific authored walkthrough, or its consolidation and
// consistency judgments have no clean recorded review. The person checks
// the walkthrough first, then native work may proceed. Replays cost
// nothing; a missing check never funds new judgments at serving time.
var ErrPlanChoiceReview = errors.New("recorded plan needs its walkthrough checked before native work")

// GenerateAcceptedIntentFromRecordedPlan is the bound R13 serving
// connector: it resolves the current accepted head's own recorded
// assessed planning choice and generates through the choice-bound pipeline
// in one step, so no caller can resolve a choice and adopt it later
// across an unchecked gap. A still-running plan or a volatile
// preparation waits truthfully through the returned wait.
// expectedRevision is the caller's selected acceptance, preserved across each
// reload and generation entry; a newer head never retargets the command.
func (c *Coordinator) GenerateAcceptedIntentFromRecordedPlan(ctx context.Context, intentID, expectedRevision string, owner int64) (factory.IntentGeneration, *DispatchWait, error) {
	var empty factory.IntentGeneration
	if c.Store == nil {
		return empty, nil, fmt.Errorf("%w: intent planning store is nil", ErrPlanChoiceStore)
	}
	r, err := c.Store.IntentRevision(ctx, intentID, owner)
	if err != nil {
		return empty, nil, err
	}
	if !project.ValidDecisionID(expectedRevision) || r.ID != expectedRevision {
		return empty, nil, store.ErrStaleRevision
	}
	if r.State != "accepted" {
		return empty, nil, errors.New("planned choice serves the accepted direction")
	}
	if _, err := c.Store.IntentGeneration(ctx, r.ID); err == nil {
		g, err := c.GenerateAcceptedIntent(ctx, intentID, expectedRevision, owner)
		if errors.Is(err, ErrGraphStepsPending) {
			return g, &DispatchWait{Repository: r.Repository, Reason: "graph_steps_pending", Detail: "Completed issue writes are retained; the remaining graph steps and full verification are pending."}, nil
		}
		return g, nil, err
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, nil, err
	}
	chosen, wait, err := c.ResolveAcceptedPlanChoice(ctx, owner, r)
	if err != nil || wait != nil {
		return empty, wait, err
	}
	g, err := c.GenerateAcceptedIntentFromChoice(ctx, intentID, expectedRevision, owner, chosen)
	if errors.Is(err, ErrGraphStepsPending) {
		return g, &DispatchWait{Repository: r.Repository, Reason: "graph_steps_pending", Detail: "Completed issue writes are retained; the remaining graph steps and full verification are pending."}, nil
	}
	return g, nil, err
}

// ResolveAcceptedPlanChoice binds the current accepted head back to its
// own recorded plan: the completed proposal or decomposition-options run the exact
// reviewed draft (this acceptance's predecessor) settled through the
// shared finish boundary. It returns the single funded comparative
// choice for one multi-candidate answer, or the lone candidate with
// zero comparative calls. Anything else refuses before any funded
// choice: a missing attempt, a corrupt staged input, a failed or
// cancelled run, an attention answer, revised scope, a stale source,
// preparation or head — each with the stored assignment, staged input
// and result as evidence. A still-running attempt waits truthfully
// through the returned wait. The funded comparison takes time, so
// source, preparation and head currency are rechecked after the choice
// before adoption.
func (c *Coordinator) ResolveAcceptedPlanChoice(ctx context.Context, owner int64, r factory.IntentRevision) (ChosenIntentPlan, *DispatchWait, error) {
	var empty ChosenIntentPlan
	if c.Store == nil {
		return empty, nil, fmt.Errorf("%w: intent planning store is nil", ErrPlanChoiceStore)
	}
	if err := admitAcceptedChoiceHead(r, owner); err != nil {
		return empty, nil, err
	}
	a, err := c.Store.IntentPlanAttempt(ctx, r.Intent, r.Predecessor)
	if errors.Is(err, store.ErrNotFound) {
		return empty, nil, errors.New("the reviewed draft has no recorded decomposition plan")
	}
	if err != nil {
		return empty, nil, fmt.Errorf("%w: plan attempt unreadable", ErrPlanChoiceStore)
	}
	in, err := bindRecordedPlanInput(a, owner, r)
	if err != nil {
		return empty, nil, err
	}
	if !factory.ValidIntentPlanningOperation(in.Operation) {
		return empty, nil, errors.New("accepted direction carries an unsupported recorded planning operation")
	}
	out, wait, err := settlePlanChoiceStatus(a)
	if err != nil || wait != nil {
		return empty, wait, err
	}
	if err := bindRecordedScopeCoverage(r, out); err != nil {
		return empty, nil, err
	}
	if wait, err := c.recheckPlanChoiceSource(ctx, r, a, in); err != nil || wait != nil {
		return empty, wait, err
	}
	if err := c.currentAcceptedIntent(ctx, r); err != nil {
		return empty, nil, fmt.Errorf("%w: accepted direction changed during choice", ErrPlanChoiceStale)
	}
	// The recorded walkthrough admits before any funded choice: a missing
	// or unchecked walkthrough refuses without spending a comparison.
	if err := c.admitRecordedWalkthroughReview(ctx, r, in.Head, *out); err != nil {
		return empty, nil, err
	}
	chosen, err := c.ChoosePlannedCandidate(ctx, r, in, *out)
	if err != nil {
		return empty, nil, err
	}
	chosen.Attempt = a.ID
	if out.VerificationManifestProposal != nil {
		catalog, err := c.ApprovedProjectCheckCatalog(ctx, a)
		if err != nil {
			return empty, nil, err
		}
		manifest := factory.AdoptedVerificationManifest{Intent: r.Intent, AcceptedRevision: r.ID, ScopeDigest: r.Digest(), Proposal: *out.VerificationManifestProposal, Catalog: catalog}
		if err := manifest.ValidateFor(r); err != nil {
			return empty, nil, err
		}
		chosen.VerificationManifest = &manifest
	}
	if wait, err := c.recheckPlanChoiceAfterComparison(ctx, r, a, in); err != nil || wait != nil {
		return empty, wait, err
	}
	if chosen.Intent != r.Intent {
		return empty, nil, errors.New("planned choice answers another intent")
	}
	return chosen, nil, nil
}

// admitAcceptedChoiceHead proves the caller serves the owning accepted
// direction carrying a reviewed draft before any recorded-plan read.
func admitAcceptedChoiceHead(r factory.IntentRevision, owner int64) error {
	if r.State != "accepted" {
		return errors.New("planned choice serves the accepted direction")
	}
	if r.Owner != owner || r.Owner <= 0 {
		return errors.New("planned choice answers its owning reviewer")
	}
	if r.Predecessor == "" {
		return errors.New("accepted direction carries no reviewed draft plan")
	}
	return nil
}

// bindRecordedPlanInput recovers the exact staged input a plan attempt
// recorded and proves it answers the funded operation behind this
// acceptance: the staged head must equal the assignment subject triple
// the shared finish boundary settled, its revision must be the exact
// reviewed draft this acceptance carries as predecessor, and the
// accepted direction must preserve the reviewed substantive scope.
func bindRecordedPlanInput(a factory.Assignment, owner int64, r factory.IntentRevision) (factory.SemanticPlannerInput, error) {
	var empty factory.SemanticPlannerInput
	subj := a.Subject.Intent
	if subj == nil || subj.OwnerID != owner {
		return empty, errors.New("recorded plan answers another owner")
	}
	in, staged, err := ParseStagedPlanningInput(a.Prompt)
	if err != nil || !staged {
		return empty, errors.New("recorded planning input is missing or corrupt")
	}
	if in.Head.Digest() != subj.ScopeDigest || in.Head.ID != subj.RevisionID ||
		in.Head.Intent != subj.IntentID || in.Head.Owner != subj.OwnerID {
		return empty, fmt.Errorf("%w: recorded plan answers another funded operation", ErrPlanChoiceStale)
	}
	if in.Head.ID != r.Predecessor {
		return empty, fmt.Errorf("%w: accepted direction reviewed another draft", ErrPlanChoiceStale)
	}
	if err := bindAcceptedScope(r, in.Head); err != nil {
		return empty, err
	}
	return in, nil
}

// bindAcceptedScope freezes the reviewed substantive planning content
// across draft to accepted: the objective, the repository, and every
// reviewed product choice's description, correction, consequence,
// provenance and decision. Only lifecycle revision, state and
// acceptance metadata plus planner-generated task differences may
// change; a revised product direction refuses before any funded
// comparison instead of spending on an answer to another intent.
func bindAcceptedScope(r, draft factory.IntentRevision) error {
	if r.Objective != draft.Objective {
		return fmt.Errorf("%w: accepted direction revised its reviewed objective", ErrPlanChoiceStale)
	}
	if r.Repository != draft.Repository {
		return fmt.Errorf("%w: accepted direction moved repositories", ErrPlanChoiceStale)
	}
	reviewed := make(map[string]factory.ProductIdea, len(draft.Ideas))
	for _, idea := range draft.Ideas {
		reviewed[idea.ID] = idea
	}
	if len(r.Ideas) != len(reviewed) {
		return fmt.Errorf("%w: accepted direction changed its reviewed product choices", ErrPlanChoiceStale)
	}
	for _, idea := range r.Ideas {
		prior, ok := reviewed[idea.ID]
		if !ok {
			return fmt.Errorf("%w: accepted direction adds an unreviewed product choice", ErrPlanChoiceStale)
		}
		if idea.Description != prior.Description || idea.Correction != prior.Correction ||
			idea.Consequence != prior.Consequence || idea.Provenance != prior.Provenance ||
			idea.Decision != prior.Decision {
			return fmt.Errorf("%w: accepted direction revised its reviewed product choice", ErrPlanChoiceStale)
		}
	}
	return nil
}

// bindRecordedScopeCoverage proves every recorded candidate fits inside
// the current accepted scope before any funded comparison, mirroring
// the adoption boundary's own rule. Scope that the acceptance no
// longer authorizes refuses here, not after spending on a choice the
// graph validation would reject.
func bindRecordedScopeCoverage(r factory.IntentRevision, out *factory.SemanticPlannerOutput) error {
	if out.VerificationManifestProposal != nil {
		if err := out.VerificationManifestProposal.ValidateOutcomes(r); err != nil {
			return fmt.Errorf("%w: recorded verification manifest exceeds accepted scope", ErrPlanChoiceStale)
		}
	}
	for _, candidate := range out.Candidates {
		work := r
		work.Tasks = append([]factory.IntentTask(nil), candidate.Tasks...)
		if err := work.Validate(); err != nil {
			return fmt.Errorf("%w: recorded choice exceeds accepted scope", ErrPlanChoiceStale)
		}
	}
	return nil
}

// settlePlanChoiceStatus reads one recorded attempt the way the draft
// status view does: a still-open run waits, a failed or cancelled run
// refuses with its recorded summary, a reported completed run yields
// its stored answer, and any other reported outcome carries the
// planner's attention text back to the product decision.
func settlePlanChoiceStatus(a factory.Assignment) (*factory.SemanticPlannerOutput, *DispatchWait, error) {
	switch a.PlanOutcome() {
	case factory.PlanPending:
		return nil, &DispatchWait{Repository: a.Repository, Reason: WaitAssigned, Detail: "recorded decomposition plan is still running"}, nil
	case factory.PlanFailed:
		return nil, nil, fmt.Errorf("recorded decomposition failed: %s", a.Result.Summary)
	case factory.PlanCancelled:
		return nil, nil, fmt.Errorf("recorded decomposition was cancelled: %s", a.Result.Summary)
	case factory.PlanCompleted:
		if a.Result.PhaseResult.Output == nil || a.Result.PhaseResult.Output.Decomposition == nil {
			return nil, nil, errors.New("recorded plan carries no decomposition result")
		}
		return a.Result.PhaseResult.Output.Decomposition, nil, nil
	default:
		attention := ""
		if pr := a.Result.PhaseResult; pr != nil && pr.Output != nil && pr.Output.Decomposition != nil {
			attention = pr.Output.Decomposition.Attention
		}
		if attention == "" {
			attention = "the recorded plan needs its product decision before native work"
		}
		return nil, nil, fmt.Errorf("%w: %s", ErrPlanChoiceAttention, attention)
	}
}

// recheckPlanChoiceSource proves the recorded plan still inspects the
// current source: the ready role preparation behind this repository
// must be the recorded preparation, or a fresh native preparation of the
// identical approved bytes after a runtime restart. Original run captures and
// deadlines stay fixed; changed source, setup or approval refuses as stale.
func (c *Coordinator) recheckPlanChoiceSource(ctx context.Context, r factory.IntentRevision, a factory.Assignment, in factory.SemanticPlannerInput) (*DispatchWait, error) {
	workspace, native, err := c.Store.ExecutionWorkspace(ctx, collaboration.RepositoryID(r.Repository))
	if errors.Is(err, store.ErrNotFound) {
		return &DispatchWait{Repository: r.Repository, Reason: WaitAuthority, Detail: "project changed during accepted planning choice"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: project unreadable", ErrPlanChoiceStore)
	}
	plan := &attemptPlan{projectID: workspace, nativeEnvironment: native, policy: factory.RepositoryPolicy{Repository: r.Repository}}
	first, second := selectPreparationFor(ctx, c.dispatchDeps(), plan, project.RoleCoder, "")
	if second != nil {
		return nil, fmt.Errorf("%w: %s", ErrPlanChoiceStore, second.Detail)
	}
	if first != nil {
		wait := timeOnlyWait(r.Repository, first)
		return &wait, nil
	}
	if a.Preparation == "" {
		return nil, errors.New("recorded plan carries no preparation binding")
	}
	if plan.prep.Preparation.ID != a.Preparation {
		if plan.prep.Native == nil {
			return nil, fmt.Errorf("%w: recorded plan answers another preparation", ErrPlanChoiceStale)
		}
		prior, err := c.Store.Preparation(ctx, a.Preparation)
		if err != nil {
			return nil, fmt.Errorf("%w: original preparation unreadable", ErrPlanChoiceStore)
		}
		run, err := c.Store.FactoryRun(ctx, a.Run)
		if err != nil {
			return nil, fmt.Errorf("%w: original source capture unreadable", ErrPlanChoiceStore)
		}
		old, current := prior.Preparation, plan.prep.Preparation
		if prior.Native == nil || run.Native == nil {
			return nil, fmt.Errorf("%w: original native source capture missing", ErrPlanChoiceStale)
		}
		sameCapture := prior.Native.Profile == plan.prep.Native.Profile && prior.Native.InstanceID != plan.prep.Native.InstanceID &&
			run.Native.Provenance.Profile == prior.Native.Profile && run.Native.Provenance.InstanceID == prior.Native.InstanceID &&
			run.Native.Provenance.WorktreeID == a.Preparation && run.Native.BundleSHA256 != "" && run.Native.BundleSHA256 == plan.prep.State.SourceBundleSHA256
		sameApproval := old.Project == current.Project && old.Role == current.Role && old.SourceCommit == a.SourceCommit && old.SourceCommit == current.SourceCommit &&
			old.SetupDigest == current.SetupDigest && old.Requirements.Equal(current.Requirements) && old.Approval == current.Approval &&
			old.Requirements.ID == a.Authority.RequirementsID && old.Approval.ID == a.Authority.ApprovalID
		if !sameCapture || !sameApproval {
			return nil, fmt.Errorf("%w: current preparation changes the captured approved source", ErrPlanChoiceStale)
		}
	}
	if plan.prep.Preparation.SourceCommit != a.SourceCommit {
		return nil, fmt.Errorf("%w: recorded plan inspects another source commit", ErrPlanChoiceStale)
	}
	for _, ref := range in.Inspected {
		if ref.Commit != a.SourceCommit {
			return nil, fmt.Errorf("%w: recorded evidence answers another source commit", ErrPlanChoiceStale)
		}
	}
	return nil, nil
}

// recheckPlanChoiceAfterComparison closes the funded-comparison gap: a
// comparative choice takes time, so source, preparation and head
// currency are proven again after ChoosePlannedCandidate returns and
// before the choice may reach native effects. Reads only; no provider
// call runs inside a transaction here.
func (c *Coordinator) recheckPlanChoiceAfterComparison(ctx context.Context, r factory.IntentRevision, a factory.Assignment, in factory.SemanticPlannerInput) (*DispatchWait, error) {
	if wait, err := c.recheckPlanChoiceSource(ctx, r, a, in); err != nil || wait != nil {
		return wait, err
	}
	if err := c.currentAcceptedIntent(ctx, r); err != nil {
		return nil, fmt.Errorf("%w: accepted direction changed during choice", ErrPlanChoiceStale)
	}
	return nil, nil
}

// admitRecordedWalkthroughReview proves a recorded answer carries a
// request-specific authored walkthrough and a clean recorded check
// before new builds: the walkthrough binds the recorded draft head,
// and the consolidation and consistency judgments replay clean from
// durable receipts. The replay coordinator carries no chooser, so a
// missing check refuses without funding new judgments at serving time;
// the person checks the walkthrough explicitly, then native work may
// proceed. Deterministic effect findings refuse the same way.
func (c *Coordinator) admitRecordedWalkthroughReview(ctx context.Context, accepted, head factory.IntentRevision, out factory.SemanticPlannerOutput) error {
	if out.Walkthroughs == nil {
		return fmt.Errorf("%w: recorded plan carries no authored walkthrough", ErrPlanChoiceReview)
	}
	if err := factory.ValidatePlanningWalkthroughs(head, *out.Walkthroughs); err != nil {
		return fmt.Errorf("%w: %v", ErrPlanChoiceReview, err)
	}
	replay := &Coordinator{Store: c.Store, acceptedAdvice: &accepted}
	recommended, err := replay.JudgeConsolidation(ctx, head, *out.Walkthroughs)
	if err != nil {
		return fmt.Errorf("%w: recorded walkthrough has no checked review", ErrPlanChoiceReview)
	}
	checked, err := replay.JudgeConsistency(ctx, head, *out.Walkthroughs, recommended)
	if err != nil {
		return fmt.Errorf("%w: recorded walkthrough has no checked review", ErrPlanChoiceReview)
	}
	if len(checked.Mismatches) != 0 {
		return fmt.Errorf("%w: recorded walkthrough needs correction", ErrPlanChoiceReview)
	}
	return nil
}

// validateGenerationBinding proves a generated operation's stamped
// recorded plan still answers the current direction: the exact trusted
// assignment row, its staged input against the current head, the
// accepted scope freeze, per-candidate coverage, and exact
// preparation/source/head currency. Pure reads; no comparative choice
// is funded and no provider is called, so submission and adoption can
// re-prove the binding after slow external reads. Generations without
// a stamped binding fail closed; provenance cannot be attached later.
func (c *Coordinator) validateGenerationBinding(ctx context.Context, g factory.IntentGeneration) (*DispatchWait, error) {
	if g.RepairCycle != "" {
		return c.validateRepairGeneration(ctx, g)
	}
	if g.ReassessmentCycle != "" {
		return c.validateReassessmentGeneration(ctx, g)
	}
	if g.PlanAssignment == "" {
		return nil, errors.New("generation carries no recorded plan binding")
	}
	a, err := c.Store.Assignment(ctx, g.PlanAssignment)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: recorded plan is unavailable", ErrPlanChoiceStale)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: recorded plan unreadable", ErrPlanChoiceStore)
	}
	subj := a.Subject.Intent
	if subj == nil || subj.OwnerID <= 0 {
		return nil, errors.New("recorded plan answers another owner")
	}
	r, err := c.Store.IntentRevision(ctx, subj.IntentID, subj.OwnerID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: accepted direction is unavailable", ErrPlanChoiceStale)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: accepted head unreadable", ErrPlanChoiceStore)
	}
	if r.ID != g.RevisionID || r.Intent != g.IntentID || r.Digest() != g.IntentDigest || r.State != "accepted" {
		return nil, fmt.Errorf("%w: generation answers another accepted direction", ErrPlanChoiceStale)
	}
	in, err := bindRecordedPlanInput(a, subj.OwnerID, r)
	if err != nil {
		return nil, err
	}
	if !factory.ValidIntentPlanningOperation(in.Operation) {
		return nil, errors.New("generation requires an assessed planning operation")
	}
	out, wait, err := settlePlanChoiceStatus(a)
	if err != nil || wait != nil {
		return wait, err
	}
	if err := bindGenerationTasks(r, g, out); err != nil {
		return nil, err
	}
	if g.VerificationManifest != nil {
		catalog, err := c.ApprovedProjectCheckCatalog(ctx, a)
		if err != nil {
			return nil, err
		}
		if catalog != g.VerificationManifest.Catalog {
			return nil, fmt.Errorf("%w: approved verification catalog changed", ErrPlanChoiceStale)
		}
	}
	if wait, err := c.recheckPlanChoiceSource(ctx, r, a, in); err != nil || wait != nil {
		return wait, err
	}
	if err := c.currentAcceptedIntent(ctx, r); err != nil {
		return nil, fmt.Errorf("%w: accepted direction changed", ErrPlanChoiceStale)
	}
	return nil, nil
}

// bindGenerationTasks proves the immutable submitted graph is one of the
// recorded candidates and remains inside the accepted scope.
func bindGenerationTasks(r factory.IntentRevision, g factory.IntentGeneration, out *factory.SemanticPlannerOutput) error {
	if (g.VerificationManifest == nil) != (out.VerificationManifestProposal == nil) {
		return errors.New("generation omitted or invented its recorded verification manifest")
	}
	if g.VerificationManifest != nil {
		if err := g.VerificationManifest.ValidateFor(r); err != nil {
			return err
		}
		if g.VerificationManifest.Proposal.Digest() != out.VerificationManifestProposal.Digest() || g.VerificationManifestDigest != g.VerificationManifest.Digest() {
			return errors.New("generation verification manifest differs from its recorded proposal")
		}
	} else if g.VerificationManifestDigest != "" {
		return errors.New("generation has an unbound verification manifest digest")
	}
	matched := false
	for _, candidate := range out.Candidates {
		matched = matched || reflect.DeepEqual(candidate.Tasks, g.SubmittedTasks)
	}
	if !matched {
		return errors.New("submitted tasks do not match the recorded plan")
	}
	return bindRecordedScopeCoverage(r, out)
}
