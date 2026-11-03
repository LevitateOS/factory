package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// IssueGraphExecutor supplies native mutation/permission observations only.
// Accepted product authority and exact child adoption remain Soda-owned.
type IssueGraphExecutor interface {
	factory.GraphCodec
	CheckIntentOwner(context.Context, int64, int64, int64) (int64, error)
	SubmitGraph(context.Context, factory.IntentGeneration, int64) (factory.GraphOperationOutcome, error)
	LookupGraph(context.Context, factory.IntentGeneration, int64) (factory.GraphOperationLookup, error)
	CancelGraph(context.Context, string) (factory.GraphOperationOutcome, error)
	ReadGeneratedIssue(context.Context, int64, int64, int64) (factory.GeneratedIssueObservation, int64, error)
}

// IntentGraphPlan renders exact accepted tasks into semantic requirements.
// Provenance is visible in native issue content and separately retained in
// Soda's immutable generation ledger; it does not impersonate human authorship.
func IntentGraphPlan(r factory.IntentRevision) (collaboration.GraphIntent, error) {
	if err := r.Validate(); err != nil {
		return collaboration.GraphIntent{}, err
	}
	if r.State != "accepted" {
		return collaboration.GraphIntent{}, errors.New("product direction is not accepted")
	}
	p := collaboration.GraphIntent{}
	for _, task := range r.Tasks {
		body := intentTaskBody(r, task)
		p.Creates = append(p.Creates, collaboration.GraphCreate{Key: task.Key, Title: intentTaskTitle(task), Body: body})
		for _, dep := range task.DependsOn {
			p.Dependencies = append(p.Dependencies, collaboration.GraphDependency{Issue: collaboration.GraphRef{Key: task.Key}, Prerequisite: collaboration.GraphRef{Key: dep}})
		}
	}
	if err := p.ValidateShape(collaboration.RepositoryID(r.Repository)); err != nil {
		return collaboration.GraphIntent{}, err
	}
	return p, nil
}

func intentTaskBody(r factory.IntentRevision, t factory.IntentTask) string {
	var b strings.Builder
	writeTaskBodyHead(&b, r, t)
	selected := map[string]bool{}
	for _, id := range t.IdeaIDs {
		selected[id] = true
	}
	effective := map[string]bool{}
	for _, id := range r.EffectiveOutcomes {
		effective[id] = true
	}
	for _, idea := range r.Ideas {
		label, ok := admitTaskBodyIdea(idea, selected, effective)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n%s (%s):\n", label, idea.ID)
		writeTaskIdeaContent(&b, r, t, idea)
	}
	writeTaskBodyTail(&b, r, t)
	return b.String()
}

// writeTaskBodyHead renders the accepted objective, task objective
// and acceptance outcomes behind one task body.
func writeTaskBodyHead(b *strings.Builder, r factory.IntentRevision, t factory.IntentTask) {
	b.WriteString("Accepted product objective:\n")
	b.WriteString(r.Objective)
	if t.Objective != r.Objective {
		b.WriteString("\n\nTask objective:\n")
		b.WriteString(t.Objective)
	}
	b.WriteString("\n\nAcceptance outcomes:\n")
	for _, outcome := range t.Outcomes {
		b.WriteString("- ")
		b.WriteString(outcome)
		b.WriteByte('\n')
	}
}

// admitTaskBodyIdea reports whether one reviewed idea shapes the task
// body with its display label.
func admitTaskBodyIdea(idea factory.ProductIdea, selected, effective map[string]bool) (string, bool) {
	if !selected[idea.ID] && !idea.Assumption {
		return "", false
	}
	if len(effective) > 0 && !effective[idea.ID] {
		return "", false
	}
	if idea.Decision != "accept" && idea.Decision != "edit" {
		return "", false
	}
	label := "Accepted product choice"
	if idea.Assumption {
		label = "Accepted assumption"
	}
	if idea.Source != "" || idea.Provenance != "" {
		provenance := idea.Source
		if idea.Provenance != "" {
			if provenance != "" {
				provenance += ", "
			}
			provenance += idea.Provenance
		}
		if provenance != "" {
			label += " (" + provenance + ")"
		}
	}
	return label, true
}

// writeTaskIdeaContent renders one admitted idea's reviewed proposal
// or description with its consequence and evidence.
func writeTaskIdeaContent(b *strings.Builder, r factory.IntentRevision, t factory.IntentTask, idea factory.ProductIdea) {
	if idea.Decision == "edit" {
		if idea.Description != r.Objective && idea.Description != t.Objective {
			fmt.Fprintf(b, "Reviewed proposal: %s\n", idea.Description)
		}
		writeTaskIdeaCorrection(b, r, t, idea)
	} else if idea.Description != r.Objective && idea.Description != t.Objective {
		b.WriteString(idea.Description)
		b.WriteByte('\n')
	} else if idea.Description == r.Objective {
		b.WriteString("The accepted product objective stated above\n")
	} else {
		b.WriteString("The task objective stated above\n")
	}
	fmt.Fprintf(b, "Consequence: %s\n", idea.Consequence)
	if idea.Evidence != "" {
		fmt.Fprintf(b, "Evidence: %s\n", idea.Evidence)
	}
}

// writeTaskIdeaCorrection renders one edited idea's accepted
// correction, naming a repeated objective by reference.
func writeTaskIdeaCorrection(b *strings.Builder, r factory.IntentRevision, t factory.IntentTask, idea factory.ProductIdea) {
	if idea.Correction != r.Objective && idea.Correction != t.Objective {
		fmt.Fprintf(b, "Accepted correction: %s\n", idea.Correction)
	} else if idea.Correction == r.Objective {
		b.WriteString("Accepted correction: the accepted product objective stated above\n")
	} else {
		b.WriteString("Accepted correction: the task objective stated above\n")
	}
}

// writeTaskBodyTail renders the intent identity and task digest
// behind one task body.
func writeTaskBodyTail(b *strings.Builder, r factory.IntentRevision, t factory.IntentTask) {
	fmt.Fprintf(b, "\nAccepted product intent: %s\nAccepted revision: %s\nIntent digest: %s\nTask: %s\n", r.Intent, r.ID, r.Digest(), t.Key)
	raw, _ := json.Marshal(t)
	digest := sha256.Sum256(raw)
	fmt.Fprintf(b, "Task digest: %s\nProduct choices: %s\n", hex.EncodeToString(digest[:]), strings.Join(t.IdeaIDs, ", "))
}

func generatedDecisionID(prefix, revision, key string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + revision + "\x00" + key))
	if prefix == "command" {
		return hex.EncodeToString(sum[:16])
	}
	return "d" + hex.EncodeToString(sum[:12])
}

// GenerateAcceptedIntent resumes an existing operation under its original plan
// binding. Creation belongs to GenerateAcceptedIntentFromRecordedPlan.
// The original revision is mandatory even for background recovery/adoption.
func (c *Coordinator) GenerateAcceptedIntent(ctx context.Context, intentID, expectedRevision string, owner int64) (factory.IntentGeneration, error) {
	return c.generateAcceptedIntent(ctx, intentID, expectedRevision, owner, nil, c.validateGenerationBinding)
}

// GenerateAcceptedIntentFromChoice writes only work bound to the current
// human-accepted direction, adopting exactly one pre-chosen planner
// candidate (see ChooseSingleCandidate and ChoosePlannedCandidate). The
// full Generate pipeline runs unchanged — durable operation, native
// submission, exact receipt adoption — except task derivation: the chosen
// tasks replace the serving advised graph, so no triplet judgment or
// template restructuring evaluates on this path. The choice binds the
// reviewed draft this acceptance carries as its predecessor (or this exact
// revision); a choice bound to another intent, another operation, a third
// revision or outside accepted scope refuses before any native observation.
func (c *Coordinator) GenerateAcceptedIntentFromChoice(ctx context.Context, intentID, expectedRevision string, owner int64, chosen ChosenIntentPlan) (factory.IntentGeneration, error) {
	if chosen.Intent != intentID {
		return factory.IntentGeneration{}, errors.New("chosen planner candidate binds another intent")
	}
	if chosen.Attempt == "" {
		return factory.IntentGeneration{}, errors.New("chosen candidate carries no recorded plan binding")
	}
	return c.generateAcceptedIntent(ctx, intentID, expectedRevision, owner, &chosen, c.validateGenerationBinding)
}

// generationPlanValidator is the planning boundary used by graph mechanics.
// Production callers always supply validateGenerationBinding; mechanics tests
// supply an explicit planning stub independently of the native graph stub.
type generationPlanValidator func(context.Context, factory.IntentGeneration) (*DispatchWait, error)

func (c *Coordinator) generateAcceptedIntent(ctx context.Context, intentID, expectedRevision string, owner int64, chosen *ChosenIntentPlan, validate generationPlanValidator) (factory.IntentGeneration, error) {
	ctx, unlock := c.progressPass(ctx)
	defer unlock()
	if c.Store == nil || c.Graph == nil || c.GraphActorID <= 0 {
		return factory.IntentGeneration{}, errors.New("native issue graph is unavailable")
	}
	r, err := c.Store.IntentRevision(ctx, intentID, owner)
	if err != nil {
		return factory.IntentGeneration{}, err
	}
	if !project.ValidDecisionID(expectedRevision) || r.ID != expectedRevision {
		return factory.IntentGeneration{}, store.ErrStaleRevision
	}
	if r.State != "accepted" {
		return factory.IntentGeneration{}, errors.New("product direction is not accepted")
	}
	revision, err := c.Graph.CheckIntentOwner(ctx, r.Repository, r.Owner, c.GraphActorID)
	if err != nil {
		return factory.IntentGeneration{}, err
	}
	g, done, err := c.loadAcceptedGeneration(ctx, r, revision, chosen)
	if done || err != nil {
		return g, err
	}
	if g, err = c.submitAcceptedGeneration(ctx, r, g, validate); err != nil {
		return g, err
	}
	if g, err = c.revalidateGenerationBeforeAdopt(ctx, g, validate); err != nil {
		return g, err
	}
	if err = c.adoptIntentGraph(ctx, r, g, validate); err != nil {
		return g, err
	}
	// Optional lifecycle effect: new-issue label/assignee. Never gates
	// default delivery; duplicate runs reconcile by action identity.
	g.State = "completed"
	if err = c.recordIntentGeneration(ctx, g); err != nil {
		return g, err
	}
	return g, nil
}

// loadAcceptedGeneration loads or begins the generation behind one
// accepted intent, replaying terminal states without native contact.
func (c *Coordinator) loadAcceptedGeneration(ctx context.Context, r factory.IntentRevision, revision int64, chosen *ChosenIntentPlan) (factory.IntentGeneration, bool, error) {
	g, err := c.Store.IntentGeneration(ctx, r.ID)
	if errors.Is(err, store.ErrNotFound) {
		g, err = c.beginChosenIntentGeneration(ctx, r, revision, chosen)
	}
	if err != nil {
		return g, false, err
	}
	if err = checkGenerationIdentity(r, g, c.GraphActorID); err != nil {
		return g, false, err
	}
	if settled, done, serr := settledGenerationResult(g); done {
		return settled, true, serr
	}
	return g, false, nil
}

// submitAcceptedGeneration submits one pending generation's native
// operation and records its settled receipt.
func (c *Coordinator) submitAcceptedGeneration(ctx context.Context, r factory.IntentRevision, g factory.IntentGeneration, validate generationPlanValidator) (factory.IntentGeneration, error) {
	if g.State != "pending" {
		return g, nil
	}
	lookup, err := c.Graph.LookupGraph(ctx, g, r.Repository)
	if err != nil {
		return g, err
	}
	var record factory.GraphOperationOutcome
	if lookup.Record != nil {
		record = *lookup.Record
	} else if record, g, err = c.submitUnobservedGeneration(ctx, r, g, validate, lookup); err != nil {
		return g, err
	}
	return c.recordAcceptedGenerationOutcome(ctx, g, record)
}

func (c *Coordinator) recordAcceptedGenerationOutcome(ctx context.Context, g factory.IntentGeneration, record factory.GraphOperationOutcome) (factory.IntentGeneration, error) {
	switch record.Effect {
	case collaboration.EffectCommitted:
		g.State = "committed"
		g.Receipt = record.Receipt
	case collaboration.EffectNotCommitted:
		g.State = "refused"
		g.Receipt = record.Receipt
	default:
		return g, errors.New("native graph effect is unresolved")
	}
	if err := c.recordIntentGeneration(ctx, g); err != nil {
		return g, err
	}
	if g.State == "refused" {
		return g, errors.New("native graph mutation refused: " + record.Reason)
	}
	return g, nil
}

// submitUnobservedGeneration proves the accepted head is still fresh
// before submitting one unobserved generation operation.
func (c *Coordinator) submitUnobservedGeneration(ctx context.Context, r factory.IntentRevision, g factory.IntentGeneration, validate generationPlanValidator, lookup factory.GraphOperationLookup) (factory.GraphOperationOutcome, factory.IntentGeneration, error) {
	var record factory.GraphOperationOutcome
	if lookup.State != collaboration.LookupNotObserved {
		return record, g, errors.New("native graph outcome is unknown")
	}
	if err := c.currentAcceptedIntent(ctx, r); err != nil {
		g, _ = c.cancelIntentGeneration(ctx, g)
		return record, g, err
	}
	observed, err := c.Graph.CheckIntentOwner(ctx, r.Repository, r.Owner, g.ActorID)
	if err != nil {
		g, _ = c.cancelIntentGeneration(ctx, g)
		return record, g, err
	}
	if observed != g.NativeRev {
		g, _ = c.cancelIntentGeneration(ctx, g)
		return record, g, errors.New("accepted graph native observation is stale")
	}
	if g.NotAfter <= time.Now().Unix() {
		g, _ = c.cancelIntentGeneration(ctx, g)
		return record, g, errors.New("accepted graph operation expired before submission")
	}
	if g, err = c.revalidateGenerationBeforeSubmit(ctx, g, validate); err != nil {
		return record, g, err
	}
	record, err = c.Graph.SubmitGraph(ctx, g, r.Repository)
	return record, g, err
}

// beginChosenIntentGeneration persists the exact chosen graph before any native send.
func (c *Coordinator) beginChosenIntentGeneration(ctx context.Context, r factory.IntentRevision, revision int64, chosen *ChosenIntentPlan) (factory.IntentGeneration, error) {
	if chosen == nil || chosen.Attempt == "" {
		return factory.IntentGeneration{}, errors.New("a recorded plan is required to create native work")
	}
	payload, reused, submitted, delivered, e := c.prepareIntentGraphWithChoice(ctx, r, revision, chosen)
	if e != nil {
		return factory.IntentGeneration{}, e
	}
	fresh, e := c.Graph.CheckIntentOwner(ctx, r.Repository, r.Owner, c.GraphActorID)
	if e != nil {
		return factory.IntentGeneration{}, e
	}
	if len(payload.Edits) > 0 && fresh != revision {
		return factory.IntentGeneration{}, refuseAcceptance(RefusalStaleEvidence)
	}
	revision = fresh
	if e = c.currentAcceptedIntent(ctx, r); e != nil {
		return factory.IntentGeneration{}, e
	}
	raw, e := c.Graph.EncodeGraph(payload, r.Repository)
	if e != nil {
		return factory.IntentGeneration{}, e
	}
	g := factory.IntentGeneration{RevisionID: r.ID, IntentID: r.Intent, OperationID: "intent-" + r.ID, ActorID: c.GraphActorID, NativeRev: revision, AuthRevision: r.ID + ":" + r.Digest(), NotAfter: time.Now().Add(5 * time.Minute).Unix(), Payload: raw, TaskIssueIDs: reused, SubmittedTasks: submitted, Delivered: delivered, IntentDigest: r.Digest(), State: "pending", PlanAssignment: chosen.Attempt}
	g.VerificationManifest = chosen.VerificationManifest
	if g.VerificationManifest != nil {
		g.VerificationManifestDigest = g.VerificationManifest.Digest()
	}
	facts, err := c.Graph.InspectGraph(g.Payload)
	if err != nil {
		return g, err
	}
	if err := c.Store.BeginIntentGeneration(ctx, g, facts); err != nil {
		// Concurrent callers must converge on the winner's exact native
		// revision and deadline rather than invent a second operation.
		_, e := c.Store.IntentGeneration(ctx, r.ID)
		if e != nil {
			return g, err
		}
	}
	return c.Store.IntentGeneration(ctx, r.ID)
}

// checkGenerationIdentity proves a loaded operation still answers the
// current accepted head under this graph actor before it may advance.
func checkGenerationIdentity(r factory.IntentRevision, g factory.IntentGeneration, actor int64) error {
	if g.IntentDigest != r.Digest() || g.ActorID != actor {
		return store.ErrCommandConflict
	}
	return nil
}

// settledGenerationResult reports terminal generation states without
// touching the native graph: refused stays refused, completed replays.
func settledGenerationResult(g factory.IntentGeneration) (factory.IntentGeneration, bool, error) {
	switch g.State {
	case "refused":
		return g, true, errors.New("native graph operation was refused; revise accepted direction before retry")
	case "completed":
		return g, true, nil
	default:
		return g, false, nil
	}
}

// revalidateGenerationBeforeSubmit proves a stamped recorded plan after
// the pre-submit reads and immediately before native submission. Stale
// bindings cancel the pending operation and refuse; volatile bindings
// leave it pending with a retryable error. Missing bindings refuse.
func (c *Coordinator) revalidateGenerationBeforeSubmit(ctx context.Context, g factory.IntentGeneration, validate generationPlanValidator) (factory.IntentGeneration, error) {
	wait, verr := validate(ctx, g)
	if verr != nil {
		if errors.Is(verr, ErrPlanChoiceStore) {
			return g, verr
		}
		cancelled, _ := c.cancelIntentGeneration(ctx, g)
		return cancelled, verr
	}
	if wait != nil {
		return g, fmt.Errorf("recorded plan waits: %s", wait.Detail)
	}
	return g, nil
}

// revalidateGenerationBeforeAdopt proves a stamped recorded plan after
// the native effect settles and immediately before local adoption.
// Stale bindings return the durable committed receipt without
// completing, so retries neither duplicate native effects nor report
// them absent. Missing bindings refuse.
func (c *Coordinator) revalidateGenerationBeforeAdopt(ctx context.Context, g factory.IntentGeneration, validate generationPlanValidator) (factory.IntentGeneration, error) {
	wait, verr := validate(ctx, g)
	if verr != nil {
		return g, verr
	}
	if wait != nil {
		return g, fmt.Errorf("recorded plan waits: %s", wait.Detail)
	}
	return g, nil
}

func (c *Coordinator) currentAcceptedIntent(ctx context.Context, r factory.IntentRevision) error {
	head, err := c.Store.IntentRevision(ctx, r.Intent, r.Owner)
	if err != nil {
		return err
	}
	if head.ID != r.ID || head.State != "accepted" || head.Digest() != r.Digest() {
		return store.ErrStaleRevision
	}
	return nil
}

// intentAdoption carries the shared state behind one accepted
// intent-graph adoption so each duty reads through one small check.
type intentAdoption struct {
	c             *Coordinator
	ctx           context.Context
	r             factory.IntentRevision
	g             factory.IntentGeneration
	validate      generationPlanValidator
	scoped        factory.IntentRevision
	issues        map[string]factory.GeneratedIssueIdentity
	tasks         map[string]factory.IntentTask
	adopted       map[string]factory.IntentTaskBinding
	active        map[string]bool
	prevWork      *factory.IntentRevision
	priorTasks    map[string]factory.IntentTask
	priorBindings []factory.IntentTaskBinding
}

// adoptEvidence binds one submitted task to its native observation.
type adoptEvidence struct {
	task     factory.IntentTask
	entry    factory.GeneratedIssueIdentity
	ownerRev int64
	native   factory.GeneratedIssueObservation
	issue    *factory.GeneratedIssueFacts
}

func (c *Coordinator) adoptIntentGraph(ctx context.Context, r factory.IntentRevision, g factory.IntentGeneration, validate generationPlanValidator) error {
	a := &intentAdoption{
		c: c, ctx: ctx, r: r, g: g, validate: validate,
		tasks: map[string]factory.IntentTask{}, adopted: map[string]factory.IntentTaskBinding{}, active: map[string]bool{},
	}
	if err := a.admitReceipt(); err != nil {
		return err
	}
	if err := a.loadDeliveredPins(); err != nil {
		return err
	}
	if err := a.adoptExistingBindings(); err != nil {
		return err
	}
	for _, task := range g.SubmittedTasks {
		if err := a.adoptTask(task.Key); err != nil {
			return err
		}
	}
	return nil
}

// admitReceipt binds the native graph receipt to the submitted tasks
// and validates the scoped graph.
func (a *intentAdoption) admitReceipt() error {
	receipt, err := a.c.Graph.DecodeGraphReceipt(a.ctx, a.g.Receipt)
	if err != nil || receipt.Repository != a.r.Repository || receipt.Actor != a.g.ActorID {
		return errors.New("native graph receipt does not bind submitted tasks")
	}
	if len(a.g.SubmittedTasks) == 0 {
		return errors.New("generation predates submitted-task records")
	}
	a.scoped = a.r
	a.scoped.Tasks = a.g.SubmittedTasks
	if err := a.scoped.Validate(); err != nil {
		return errors.New("submitted graph exceeds accepted scope")
	}
	issues, err := intentReceiptIssues(a.g.SubmittedTasks, a.g, receipt)
	if err != nil {
		return err
	}
	a.issues = issues
	for _, task := range a.g.SubmittedTasks {
		a.tasks[task.Key] = task
		if a.issues[task.Key].IssueID == 0 {
			return errors.New("native graph receipt missing submitted task")
		}
	}
	return nil
}

// loadDeliveredPins resolves the previous acceptance pins behind
// delivered records, which verify against immutable history rather
// than the new scoped body.
func (a *intentAdoption) loadDeliveredPins() error {
	if len(a.g.Delivered) == 0 {
		return nil
	}
	if err := a.c.currentAcceptedIntent(a.ctx, a.r); err != nil {
		return err
	}
	previous, err := a.c.Store.PreviousGeneratedIntent(a.ctx, a.r.Intent, a.r.ID)
	if err != nil {
		return err
	}
	priorGeneration, err := a.c.Store.IntentGeneration(a.ctx, previous.ID)
	if err != nil {
		return err
	}
	if len(priorGeneration.SubmittedTasks) == 0 {
		return errors.New("previous generation predates submitted-task records")
	}
	pw := previous
	pw.Tasks = priorGeneration.SubmittedTasks
	a.prevWork = &pw
	a.priorTasks = map[string]factory.IntentTask{}
	for _, task := range pw.Tasks {
		a.priorTasks[task.Key] = task
	}
	for key := range a.g.Delivered {
		if _, ok := a.priorTasks[key]; !ok {
			return errors.New("delivered history unexpectedly mutated")
		}
	}
	priorBindings, err := a.c.Store.IntentTaskBindings(a.ctx, previous.ID)
	if err != nil {
		return err
	}
	a.priorBindings = priorBindings
	return nil
}

// adoptExistingBindings reuses bindings whose recorded acceptance
// still matches the deterministic decision for this revision.
func (a *intentAdoption) adoptExistingBindings() error {
	existing, err := a.c.Store.IntentTaskBindings(a.ctx, a.r.ID)
	if err != nil {
		return err
	}
	for _, b := range existing {
		decision, e := a.c.Store.AcceptanceDecision(a.ctx, b.AcceptanceID)
		if e == nil {
			copied := false
			for _, frozen := range a.g.CopiedBindings {
				if frozen.TaskKey == b.TaskKey && frozen == b && a.g.ProtectedGraphCycle() != "" {
					copied = true
					break
				}
			}
			if copied {
				a.adopted[b.TaskKey] = b
				continue
			}
			if prior, delivered := a.deliveredBinding(b.TaskKey); delivered && prior == b {
				a.adopted[b.TaskKey] = b
				continue
			}
			if !adoptionBindingMatches(a.r, b, decision, a.issues[b.TaskKey]) {
				return store.ErrCommandConflict
			}
			a.adopted[b.TaskKey] = b
		} else if !errors.Is(e, store.ErrNotFound) {
			return e
		}
	}
	return nil
}

// adoptionBindingMatches reports whether one recorded binding still
// matches the deterministic acceptance decision for this revision.
func adoptionBindingMatches(r factory.IntentRevision, b factory.IntentTaskBinding, decision factory.Acceptance, entry factory.GeneratedIssueIdentity) bool {
	return decision.ID == generatedDecisionID("acceptance", r.ID, b.TaskKey) && decision.Approver == r.Owner && decision.Repository == r.Repository && decision.IssueIndex == strconv.FormatInt(entry.IssueIndex, 10) && b.IssueID == entry.IssueID
}

// adoptTask adopts one submitted task after its dependencies, reading
// native evidence, matching it, then persisting the binding.
func (a *intentAdoption) adoptTask(key string) error {
	if _, ok := a.adopted[key]; ok {
		return nil
	}
	if a.active[key] {
		return errors.New("cyclic accepted graph")
	}
	a.active[key] = true
	task := a.tasks[key]
	for _, dep := range task.DependsOn {
		if err := a.adoptTask(dep); err != nil {
			return err
		}
	}
	evidence, err := a.readAdoptEvidence(task)
	if err != nil {
		return err
	}
	if err := a.checkAdoptedIssue(evidence); err != nil {
		return err
	}
	decision, err := a.matchAdoptedEdges(evidence)
	if err != nil {
		return err
	}
	if binding, delivered := a.deliveredBinding(key); delivered {
		// The closed native issue has already passed its exact content and
		// dependency checks. Keep the acceptance which actually completed;
		// inventing a new acceptance would require a second merge of old work.
		if err := a.c.bindValidatedGenerationTask(a.ctx, a.g, binding, a.validate); err != nil {
			return err
		}
		a.adopted[key], a.active[key] = binding, false
		return nil
	}
	if _, delivered := a.g.Delivered[key]; delivered {
		return errors.New("delivered task lacks its original acceptance binding")
	}
	return a.persistAdoptedTask(evidence, decision)
}

func (a *intentAdoption) deliveredBinding(key string) (factory.IntentTaskBinding, bool) {
	ref, ok := a.g.Delivered[key]
	if !ok {
		return factory.IntentTaskBinding{}, false
	}
	for _, prior := range a.priorBindings {
		if prior.TaskKey == key && prior.IssueID == ref.IssueID && prior.IssueIndex == ref.IssueIndex && prior.ContentVersion == ref.ContentVersion {
			prior.RevisionID = a.r.ID
			return prior, true
		}
	}
	return factory.IntentTaskBinding{}, false
}

// readAdoptEvidence reads one submitted task's native observation at
// the owner's revision.
func (a *intentAdoption) readAdoptEvidence(task factory.IntentTask) (adoptEvidence, error) {
	var evidence adoptEvidence
	if err := a.c.currentAcceptedIntent(a.ctx, a.r); err != nil {
		return evidence, err
	}
	ownerRev, err := a.c.Graph.CheckIntentOwner(a.ctx, a.r.Repository, a.r.Owner, a.g.ActorID)
	if err != nil {
		return evidence, err
	}
	entry := a.issues[task.Key]
	native, nativeRev, err := a.c.Graph.ReadGeneratedIssue(a.ctx, a.r.Repository, entry.IssueIndex, a.g.ActorID)
	if err != nil {
		return evidence, err
	}
	if nativeRev != ownerRev {
		return evidence, refuseAcceptance(RefusalStaleEvidence)
	}
	return adoptEvidence{task: task, entry: entry, ownerRev: nativeRev, native: native, issue: native.Issue}, nil
}

// checkAdoptedIssue matches one native observation against its
// identity and accepted content.
func (a *intentAdoption) checkAdoptedIssue(evidence adoptEvidence) error {
	issue, entry := evidence.issue, evidence.entry
	if issue == nil || !issue.Visible || !issue.Complete || issue.IsPull || issue.IsLocked || issue.ID != strconv.FormatInt(entry.IssueID, 10) || issue.Index != strconv.FormatInt(entry.IssueIndex, 10) || issue.Creator == nil || int64(*issue.Creator) != a.g.ActorID {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	// Delivered records adopt without mutation: the native stays closed
	// with its pinned content and original completed acceptance.
	// A reopened or rewritten record refuses rather than rebinding
	// history it no longer matches.
	if ref, delivered := a.g.Delivered[evidence.task.Key]; delivered {
		return a.checkDeliveredRecord(evidence, ref)
	}
	for _, retained := range a.g.RetainedBindings {
		if retained.TaskKey != evidence.task.Key || a.g.ProtectedGraphCycle() == "" {
			continue
		}
		prior, err := a.c.Store.AcceptanceDecision(a.ctx, retained.AcceptanceID)
		if err != nil || issue.TitleDigest != prior.TitleDigest || issue.ContentDigest != prior.ContentDigest || issue.ContentVersion != retained.ContentVersion || issue.ID != strconv.FormatInt(retained.IssueID, 10) {
			return refuseAcceptance(RefusalObjectiveChanged)
		}
		return nil
	}
	if issue.Title != intentTaskTitle(evidence.task) || issue.Content != intentTaskBody(a.scoped, evidence.task) || issue.ContentVersion != entry.ContentVersion {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	return nil
}

// checkDeliveredRecord verifies one delivered record against its
// pinned history and closed native state.
func (a *intentAdoption) checkDeliveredRecord(evidence adoptEvidence, ref factory.GeneratedIssueIdentity) error {
	entry, issue := evidence.entry, evidence.issue
	if entry.IssueID != ref.IssueID || entry.IssueIndex != ref.IssueIndex || entry.ContentVersion != ref.ContentVersion {
		return errors.New("delivered history unexpectedly mutated")
	}
	if !issue.IsClosed {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	stable, err := a.c.deliveredStable(a.ctx, a.priorBindings, a.priorTasks[evidence.task.Key], entry, issue, *a.prevWork)
	if err != nil {
		return err
	}
	if !stable {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	return nil
}

// matchAdoptedEdges matches native dependencies against adopted
// bindings and builds the deterministic acceptance decision.
func (a *intentAdoption) matchAdoptedEdges(evidence adoptEvidence) (factory.Acceptance, error) {
	var decision factory.Acceptance
	task, native, issue := evidence.task, evidence.native, evidence.issue
	if native.Dependencies == nil || !native.Dependencies.Complete || native.Dependencies.IssueID != issue.ID || len(native.Dependencies.Items) != len(task.DependsOn) {
		return decision, refuseAcceptance(RefusalEdgeChanged)
	}
	expected := map[string]factory.IntentTaskBinding{}
	for _, dep := range task.DependsOn {
		b := a.adopted[dep]
		expected[strconv.FormatInt(b.IssueID, 10)] = b
	}
	decision = factory.Acceptance{ID: generatedDecisionID("acceptance", a.r.ID, task.Key), Repository: a.r.Repository, IssueIndex: issue.Index, Approver: a.r.Owner, NativeRev: evidence.ownerRev, TitleDigest: issue.TitleDigest, TitleOccurrence: generatedTitleOccurrence(issue), ContentDigest: issue.ContentDigest, ContentVersion: issue.ContentVersion}
	head, headErr := a.c.Store.AcceptanceHead(a.ctx, a.r.Repository, evidence.entry.IssueIndex)
	if headErr == nil && head != decision.ID {
		decision.Predecessor = head
	} else if headErr != nil && !errors.Is(headErr, store.ErrNotFound) {
		return decision, headErr
	}
	for _, edge := range native.Dependencies.Items {
		binding, ok := expected[edge.DependencyID]
		if !ok || !edge.Visible || !edge.Complete {
			return decision, refuseAcceptance(RefusalEdgeChanged)
		}
		delete(expected, edge.DependencyID)
		decision.Prerequisites = append(decision.Prerequisites, factory.AcceptedPrerequisite{Occurrence: edge.OccurrenceID, DependsOn: edge.DependencyID, EndpointRepo: a.r.Repository, EndpointIssue: binding.IssueIndex, PrereqAcceptance: binding.AcceptanceID, Outcome: factory.PrereqCode})
	}
	if len(expected) > 0 {
		return decision, refuseAcceptance(RefusalEdgeChanged)
	}
	return decision, nil
}

// persistAdoptedTask records one matched task's binding and
// acceptance, reusing an interrupted run's exact decision.
func (a *intentAdoption) persistAdoptedTask(evidence adoptEvidence, decision factory.Acceptance) error {
	issue := evidence.issue
	// Persist authority provenance before acceptance can make the issue
	// eligible to any independently running coordinator pass.
	createdUnix := issue.CreatedUnix
	binding := factory.IntentTaskBinding{RevisionID: a.r.ID, TaskKey: evidence.task.Key, IssueID: evidence.entry.IssueID, IssueIndex: evidence.entry.IssueIndex, ContentVersion: evidence.entry.ContentVersion, AcceptanceID: decision.ID, CreatedUnix: createdUnix}
	if err := a.c.bindValidatedGenerationTask(a.ctx, a.g, binding, a.validate); err != nil {
		return err
	}
	// Interrupted adoption may have recorded acceptance before its binding.
	// Reuse only that exact deterministic decision, never a similar issue.
	old, e := a.c.Store.AcceptanceDecision(a.ctx, decision.ID)
	if errors.Is(e, store.ErrNotFound) {
		_, e = a.c.AdmitAcceptance(a.ctx, decision, func(ctx context.Context) error {
			if err := a.c.currentAcceptedIntent(ctx, a.r); err != nil {
				return err
			}
			_, err := a.c.Graph.CheckIntentOwner(ctx, a.r.Repository, a.r.Owner, a.g.ActorID)
			return err
		})
	} else if e == nil {
		if !adoptionDecisionMatches(old, decision) {
			return store.ErrCommandConflict
		}
		if e = a.c.verifyAcceptance(a.ctx, old, AcceptanceEvidence{Revision: old.NativeRev, Issue: AcceptanceIssueView{Index: issue.Index, Visible: true, TitleDigest: issue.TitleDigest, TitleOccurrence: generatedTitleOccurrence(issue), ContentDigest: issue.ContentDigest, ContentVer: issue.ContentVersion}, Dependencies: generatedEdges(evidence.native.Dependencies)}); e != nil {
			return e
		}
	}
	if e != nil {
		return e
	}
	a.adopted[evidence.task.Key] = binding
	a.active[evidence.task.Key] = false
	return nil
}

// adoptionDecisionMatches reports whether one recorded acceptance is
// the exact deterministic decision behind this adoption.
func adoptionDecisionMatches(old, decision factory.Acceptance) bool {
	return old.Approver == decision.Approver && old.Repository == decision.Repository && old.IssueIndex == decision.IssueIndex && old.TitleDigest == decision.TitleDigest && old.ContentDigest == decision.ContentDigest
}

// bindValidatedGenerationTask checks the planning evidence after native reads,
// immediately before the generated issue receives local authority provenance.
func (c *Coordinator) bindValidatedGenerationTask(ctx context.Context, g factory.IntentGeneration, binding factory.IntentTaskBinding, validate generationPlanValidator) error {
	if _, err := c.revalidateGenerationBeforeAdopt(ctx, g, validate); err != nil {
		return err
	}
	return c.Store.BindIntentTaskAcceptance(ctx, binding)
}

func generatedEdges(page *factory.GeneratedDependencies) []AcceptanceEdge {
	edges := []AcceptanceEdge{}
	for _, edge := range page.Items {
		edges = append(edges, AcceptanceEdge{Occurrence: edge.OccurrenceID, DependsOn: edge.DependencyID, Visible: edge.Visible})
	}
	return edges
}

func generatedTitleOccurrence(issue *factory.GeneratedIssueFacts) string {
	return issue.TitleOccurrence
}

// Native titles summarize a task; the body retains its complete accepted
// objective and outcomes. UTF-8 byte width follows the native title column.
func intentTaskTitle(task factory.IntentTask) string {
	title := strings.TrimSpace(task.Objective)
	if line, _, ok := strings.Cut(title, "\n"); ok {
		title = strings.TrimSpace(line)
	}
	if len(title) > 255 {
		end := 0
		for index := range title {
			if index > 255 {
				break
			}
			end = index
		}
		if end == 0 {
			end = 255
		}
		title = strings.TrimSpace(title[:end])
	}
	return title
}

func (c *Coordinator) cancelIntentGeneration(ctx context.Context, g factory.IntentGeneration) (factory.IntentGeneration, error) {
	record, err := c.Graph.CancelGraph(ctx, g.OperationID)
	if err != nil {
		return g, err
	}
	if record.OperationID != g.OperationID {
		return g, errors.New("native graph cancellation identity differs")
	}
	switch record.Effect {
	case collaboration.EffectNotCommitted:
		g.State = "refused"
		g.Receipt = record.Receipt
	case collaboration.EffectCommitted:
		g.State = "committed"
		g.Receipt = record.Receipt
	default:
		return g, nil
	}
	return g, c.recordIntentGeneration(context.WithoutCancel(ctx), g)
}

func (c *Coordinator) recordIntentGeneration(ctx context.Context, g factory.IntentGeneration) error {
	var facts factory.GraphReceiptFacts
	if len(g.Delivered) > 0 && len(g.Receipt) > 0 {
		var err error
		facts, err = c.Graph.DecodeGraphReceipt(ctx, g.Receipt)
		if err != nil {
			return err
		}
	}
	return c.Store.RecordIntentGeneration(ctx, g, facts)
}
