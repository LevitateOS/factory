package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// scopedIntentTasksFromChosen adopts exactly the single planner candidate
// the Generate work command chose before serving: it binds the choice to
// the current accepted head and enforces it inside accepted scope before
// any native observation. No triplet judgment or template restructuring
// runs here: the choice arrives decided, so a second evaluation cannot
// rewrite it on the serving path.
func (c *Coordinator) scopedIntentTasksFromChosen(_ context.Context, r factory.IntentRevision, chosen *ChosenIntentPlan) ([]factory.IntentTask, error) {
	if chosen == nil {
		return nil, errors.New("chosen planner candidate is missing")
	}
	if chosen.Intent != r.Intent {
		return nil, errors.New("chosen planner candidate binds another intent")
	}
	if !factory.ValidIntentPlanningOperation(chosen.Operation) {
		return nil, errors.New("chosen planner candidate uses an unsupported operation")
	}
	// The planner runs against the reviewed draft; the accepted successor
	// adopts its choice. Either the choice binds this exact revision or
	// the draft this acceptance reviewed — never a third revision.
	if chosen.Revision != r.ID && chosen.Revision != r.Predecessor {
		return nil, errors.New("chosen planner revision mismatch")
	}
	if len(chosen.Tasks) == 0 {
		return nil, errors.New("chosen planner candidate carries no tasks")
	}
	work := r
	work.Tasks = append([]factory.IntentTask(nil), chosen.Tasks...)
	if err := work.Validate(); err != nil {
		return nil, errors.New("chosen planner candidate exceeds accepted scope")
	}
	return work.Tasks, nil
}

// intentDeltaEdge binds one accepted dependency to its native issue pair.
type intentDeltaEdge struct{ a, b string }

// intentDeltaBuild carries the shared state behind one intent-graph
// delta so each duty reads through one small check.
type intentDeltaBuild struct {
	c             *Coordinator
	ctx           context.Context
	r             factory.IntentRevision
	revision      int64
	work          factory.IntentRevision
	prevWork      factory.IntentRevision
	tasks         []factory.IntentTask
	current       map[string]factory.IntentTask
	reused        map[string]bool
	oldIssues     map[string]factory.GeneratedIssueIdentity
	generation    factory.IntentGeneration
	priorBindings []factory.IntentTaskBinding
	p             collaboration.GraphIntent
	ids           map[string]int64
	refs          map[string]collaboration.GraphRef
	delivered     map[string]factory.GeneratedIssueIdentity
	closed        map[string]factory.GeneratedIssueIdentity
	oldEdges      map[intentDeltaEdge]bool
}

// prepareIntentGraphWithChoice expresses a reviewed decomposition change
// through the existing native issue owner, adopting exactly the one
// pre-chosen planner candidate. Reused open keys retain issue IDs and
// history; delivered (closed) records are never edited. It returns the
// submitted task graph and delivered bindings alongside the payload.
func (c *Coordinator) prepareIntentGraphWithChoice(ctx context.Context, r factory.IntentRevision, revision int64, chosen *ChosenIntentPlan) (collaboration.GraphIntent, map[string]int64, []factory.IntentTask, map[string]factory.GeneratedIssueIdentity, error) {
	previous, err := c.Store.PreviousGeneratedIntent(ctx, r.Intent, r.ID)
	if errors.Is(err, store.ErrNotFound) {
		return c.prepareFirstIntentGraph(ctx, r, chosen)
	}
	if err != nil {
		return collaboration.GraphIntent{}, nil, nil, nil, err
	}
	b, err := c.loadIntentDeltaBuild(ctx, r, revision, chosen, previous)
	if err != nil {
		return collaboration.GraphIntent{}, nil, nil, nil, err
	}
	if err := b.verifyPreviousNatives(); err != nil {
		return b.p, nil, nil, nil, err
	}
	return b.finishIntentDelta()
}

// prepareFirstIntentGraph scopes and packs the first generation
// behind one accepted intent, which has no previous revision.
func (c *Coordinator) prepareFirstIntentGraph(ctx context.Context, r factory.IntentRevision, chosen *ChosenIntentPlan) (collaboration.GraphIntent, map[string]int64, []factory.IntentTask, map[string]factory.GeneratedIssueIdentity, error) {
	tasks, err := c.scopedIntentTasksFromChosen(ctx, r, chosen)
	if err != nil {
		return collaboration.GraphIntent{}, nil, nil, nil, err
	}
	work := r
	work.Tasks = tasks
	p, err := IntentGraphPlan(work)
	if err != nil {
		return collaboration.GraphIntent{}, nil, nil, nil, err
	}
	return p, nil, tasks, nil, nil
}

// loadIntentDeltaBuild resolves the previous generation, its receipt
// issues and the task diff behind one intent-graph delta.
func (c *Coordinator) loadIntentDeltaBuild(ctx context.Context, r factory.IntentRevision, revision int64, chosen *ChosenIntentPlan, previous factory.IntentRevision) (*intentDeltaBuild, error) {
	generation, err := c.Store.IntentGeneration(ctx, previous.ID)
	if err != nil {
		return nil, err
	}
	if len(generation.SubmittedTasks) == 0 {
		return nil, errors.New("previous generation predates submitted-task records")
	}
	priorSubmitted := previous
	priorSubmitted.Tasks = generation.SubmittedTasks
	tasks, err := c.scopedIntentTasksFromChosen(ctx, r, chosen)
	if err != nil {
		return nil, err
	}
	var receipt factory.GraphReceiptFacts
	if !(generation.LocalOnly && generation.State == "completed" && generation.ProtectedGraphCycle() != "") {
		receipt, err = c.Graph.DecodeGraphReceipt(ctx, generation.Receipt)
		if err != nil || receipt.Repository != r.Repository || receipt.Actor != generation.ActorID {
			return nil, errors.New("previous graph receipt is unavailable")
		}
	}
	oldIssues, err := intentReceiptIssues(generation.SubmittedTasks, generation, receipt)
	if err != nil {
		return nil, err
	}
	work := r
	work.Tasks = tasks
	prevWork := previous
	prevWork.Tasks = generation.SubmittedTasks
	movement, err := factory.DiffIntentTasks(prevWork, work)
	if err != nil {
		return nil, err
	}
	b := &intentDeltaBuild{
		c: c, ctx: ctx, r: r, revision: revision,
		work: work, prevWork: prevWork, tasks: tasks, generation: generation,
		oldIssues: oldIssues, reused: map[string]bool{}, current: map[string]factory.IntentTask{},
		ids: map[string]int64{}, refs: map[string]collaboration.GraphRef{},
		delivered: map[string]factory.GeneratedIssueIdentity{}, closed: map[string]factory.GeneratedIssueIdentity{},
		oldEdges: map[intentDeltaEdge]bool{},
	}
	for _, key := range movement.Reused {
		b.reused[key] = true
	}
	for _, task := range tasks {
		b.current[task.Key] = task
	}
	priorBindings, err := c.Store.IntentTaskBindings(ctx, previous.ID)
	if err != nil {
		return nil, err
	}
	b.priorBindings = priorBindings
	return b, nil
}

// verifyPreviousNatives verifies every previously submitted task
// against its native observation at this revision.
func (b *intentDeltaBuild) verifyPreviousNatives() error {
	for _, task := range b.generation.SubmittedTasks {
		if err := b.verifyPreviousNative(task); err != nil {
			return err
		}
	}
	return nil
}

// verifyPreviousNative verifies one previously submitted task,
// recording its edit or its delivered pin.
func (b *intentDeltaBuild) verifyPreviousNative(task factory.IntentTask) error {
	entry := b.oldIssues[task.Key]
	native, nativeRev, err := b.c.Graph.ReadGeneratedIssue(b.ctx, b.r.Repository, entry.IssueIndex, b.generation.ActorID)
	if err != nil {
		return err
	}
	if nativeRev != b.revision {
		return refuseAcceptance(RefusalStaleEvidence)
	}
	issue := native.Issue
	if issue == nil || !issue.Visible || !issue.Complete || issue.IsPull || issue.IsLocked || issue.ID != strconv.FormatInt(entry.IssueID, 10) {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	// Delivered (closed) records are verified but never edited: the
	// native writer refuses closed edits atomically. A delivered
	// record still carries the body its writing revision gave it, so
	// stability is checked against the previous acceptance pins; the
	// rebuilt previous-revision body remains the fallback when the
	// previous adoption never pinned the key. Open records are
	// rewritten by every delta, so the rebuild is exact for them.
	if issue.IsClosed {
		return b.pinDeliveredNative(task, entry, issue, native)
	}
	if issue.Title != intentTaskTitle(task) || issue.Content != intentTaskBody(b.prevWork, task) || issue.ContentVersion != entry.ContentVersion {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	title, body := issue.Title, issue.Content+"\nSuperseded by accepted product revision: "+b.r.ID+"\nThis task is retained as history and grants no current generated-work authority.\n"
	if next, ok := b.current[task.Key]; ok {
		title = intentTaskTitle(next)
		body = intentTaskBody(b.work, next)
	}
	b.p.Edits = append(b.p.Edits, collaboration.GraphEdit{Issue: boundGraphIssue(b.r.Repository, entry.IssueID, entry.IssueIndex), Expected: collaboration.IssueContent{Title: issue.Title, Body: issue.Content, Revision: collaboration.Revision(strconv.Itoa(issue.ContentVersion))}, Title: title, Body: body})
	return b.verifyNativeEdges(task, entry, native)
}

// pinDeliveredNative pins one closed native record as delivered after
// proving it still carries its written content.
func (b *intentDeltaBuild) pinDeliveredNative(task factory.IntentTask, entry factory.GeneratedIssueIdentity, issue *factory.GeneratedIssueFacts, native factory.GeneratedIssueObservation) error {
	stable, err := b.c.deliveredStable(b.ctx, b.priorBindings, task, entry, issue, b.prevWork)
	if err != nil {
		return err
	}
	if !stable {
		return refuseAcceptance(RefusalObjectiveChanged)
	}
	b.closed[task.Key] = factory.GeneratedIssueIdentity{IssueID: entry.IssueID, IssueIndex: entry.IssueIndex, ContentVersion: entry.ContentVersion}
	return b.verifyNativeEdges(task, entry, native)
}

// verifyNativeEdges matches one native observation's dependencies
// against the accepted graph.
func (b *intentDeltaBuild) verifyNativeEdges(task factory.IntentTask, entry factory.GeneratedIssueIdentity, native factory.GeneratedIssueObservation) error {
	if native.Dependencies == nil || !native.Dependencies.Complete || len(native.Dependencies.Items) != len(task.DependsOn) {
		return refuseAcceptance(RefusalEdgeChanged)
	}
	expected := map[int64]bool{}
	for _, dep := range task.DependsOn {
		expected[b.oldIssues[dep].IssueID] = true
	}
	for _, d := range native.Dependencies.Items {
		dep, err := strconv.ParseInt(d.DependencyID, 10, 64)
		if err != nil || !d.Visible || !d.Complete || !expected[dep] {
			return refuseAcceptance(RefusalEdgeChanged)
		}
		delete(expected, dep)
		b.oldEdges[intentDeltaEdge{issueIDText(entry.IssueID), d.DependencyID}] = true
	}
	if len(expected) > 0 {
		return refuseAcceptance(RefusalEdgeChanged)
	}
	return nil
}

// finishIntentDelta plans creates, dependencies and removals behind
// one verified delta and packs the immutable packet.
func (b *intentDeltaBuild) finishIntentDelta() (collaboration.GraphIntent, map[string]int64, []factory.IntentTask, map[string]factory.GeneratedIssueIdentity, error) {
	b.planDeltaCreates()
	b.planDeltaDependencies()
	if err := b.p.ValidateShape(collaboration.RepositoryID(b.r.Repository)); err != nil {
		return b.p, nil, nil, nil, fmt.Errorf("accepted decomposition exceeds native packet bounds: %w", err)
	}
	if len(b.delivered) == 0 {
		b.delivered = nil
	}
	return b.p, b.ids, b.tasks, b.delivered, nil
}

// planDeltaCreates plans created and rebound issues behind one delta.
func (b *intentDeltaBuild) planDeltaCreates() {
	for _, task := range b.tasks {
		old, ok := b.oldIssues[task.Key]
		if !ok {
			b.p.Creates = append(b.p.Creates, collaboration.GraphCreate{Key: task.Key, Title: intentTaskTitle(task), Body: intentTaskBody(b.work, task)})
			b.refs[task.Key] = collaboration.GraphRef{Key: task.Key}
			continue
		}
		if ref, closed := b.closed[task.Key]; closed {
			if b.reused[task.Key] {
				b.delivered[task.Key] = ref
				b.refs[task.Key] = boundGraphRef(b.r.Repository, ref.IssueID, ref.IssueIndex)
			} else {
				b.p.Creates = append(b.p.Creates, collaboration.GraphCreate{Key: task.Key, Title: intentTaskTitle(task), Body: intentTaskBody(b.work, task)})
				b.refs[task.Key] = collaboration.GraphRef{Key: task.Key}
			}
			continue
		}
		b.ids[task.Key] = old.IssueID
		b.refs[task.Key] = boundGraphRef(b.r.Repository, old.IssueID, old.IssueIndex)
	}
}

// planDeltaDependencies plans added and removed dependencies behind
// one delta, keeping the retained native edges out of removals.
func (b *intentDeltaBuild) planDeltaDependencies() {
	retained := map[intentDeltaEdge]bool{}
	for _, task := range b.tasks {
		for _, dep := range task.DependsOn {
			a, c := b.refs[task.Key], b.refs[dep]
			key := intentDeltaEdge{graphRefID(a), graphRefID(c)}
			if key.a != "" && key.b != "" && b.oldEdges[key] {
				retained[key] = true
				continue
			}
			b.p.Dependencies = append(b.p.Dependencies, collaboration.GraphDependency{Issue: a, Prerequisite: c})
		}
	}
	// Stable order follows the previous task/dependency order; the immutable
	// packet is deterministic even though membership checks use maps.
	for _, task := range b.generation.SubmittedTasks {
		for _, dep := range task.DependsOn {
			key := intentDeltaEdge{issueIDText(b.oldIssues[task.Key].IssueID), issueIDText(b.oldIssues[dep].IssueID)}
			if !retained[key] {
				b.p.Dependencies = append(b.p.Dependencies, collaboration.GraphDependency{Issue: boundGraphRef(b.r.Repository, b.oldIssues[task.Key].IssueID, b.oldIssues[task.Key].IssueIndex), Prerequisite: boundGraphRef(b.r.Repository, b.oldIssues[dep].IssueID, b.oldIssues[dep].IssueIndex), Remove: true})
			}
		}
	}
}

// deliveredStable reports whether a closed native record still carries the
// exact content its writing revision gave it. The previous acceptance pins
// title and content digests at every adoption, so pins stay exact across
// any number of delivered hops with single-hop lookups. When the previous
// adoption never pinned the key, the rebuilt previous-revision body is the
// fallback; it is exact for records the previous submit wrote.
func (c *Coordinator) deliveredStable(ctx context.Context, prior []factory.IntentTaskBinding, task factory.IntentTask, entry factory.GeneratedIssueIdentity, issue *factory.GeneratedIssueFacts, prevWork factory.IntentRevision) (bool, error) {
	for _, b := range prior {
		if b.TaskKey != task.Key {
			continue
		}
		if b.IssueID != entry.IssueID || b.IssueIndex != entry.IssueIndex || b.ContentVersion != entry.ContentVersion {
			return false, errors.New("previous graph receipt does not bind submitted tasks")
		}
		decision, err := c.Store.AcceptanceDecision(ctx, b.AcceptanceID)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			return false, err
		}
		return issue.TitleDigest == decision.TitleDigest && issue.ContentDigest == decision.ContentDigest && issue.ContentVersion == decision.ContentVersion, nil
	}
	return issue.Title == intentTaskTitle(task) && issue.Content == intentTaskBody(prevWork, task) && issue.ContentVersion == entry.ContentVersion, nil
}

func intentReceiptIssues(tasks []factory.IntentTask, g factory.IntentGeneration, receipt factory.GraphReceiptFacts) (map[string]factory.GeneratedIssueIdentity, error) {
	issues := map[string]factory.GeneratedIssueIdentity{}
	byID := map[int64]factory.GeneratedIssueIdentity{}
	created := map[int64]bool{}
	for key, identity := range receipt.Creates {
		if key == "" || identity.IssueID <= 0 || identity.IssueIndex <= 0 || byID[identity.IssueID].IssueID != 0 {
			return nil, errors.New("invalid graph creation receipt")
		}
		issues[key] = identity
		byID[identity.IssueID] = identity
		created[identity.IssueID] = true
	}
	for _, identity := range receipt.Edits {
		if identity.IssueID <= 0 || identity.IssueIndex <= 0 || byID[identity.IssueID].IssueID != 0 {
			return nil, errors.New("invalid graph edit receipt")
		}
		byID[identity.IssueID] = identity
	}
	for key, id := range g.TaskIssueIDs {
		entry, exists := byID[id]
		if !exists || created[id] {
			return nil, errors.New("native graph edit receipt missing reused task")
		}
		issues[key] = entry
	}
	for _, binding := range g.RetainedBindings {
		if g.ProtectedGraphCycle() == "" || binding.RevisionID != g.RevisionID || g.TaskIssueIDs[binding.TaskKey] != 0 {
			return nil, errors.New("retained native identity lacks its reassessment owner")
		}
		if observed, ok := byID[binding.IssueID]; ok && (observed.IssueIndex != binding.IssueIndex || observed.ContentVersion != binding.ContentVersion || (created[binding.IssueID] && receipt.Creates[binding.TaskKey].IssueID != binding.IssueID)) {
			return nil, errors.New("native graph receipt changed a retained task")
		}
		if issues[binding.TaskKey].IssueID != 0 && issues[binding.TaskKey].IssueID != binding.IssueID {
			return nil, errors.New("retained task conflicts with native graph receipt")
		}
		issues[binding.TaskKey] = factory.GeneratedIssueIdentity{IssueID: binding.IssueID, IssueIndex: binding.IssueIndex, ContentVersion: binding.ContentVersion}
	}
	for key, ref := range g.Delivered {
		if issues[key].IssueID != 0 || byID[ref.IssueID].IssueID != 0 {
			return nil, errors.New("delivered history unexpectedly mutated")
		}
		issues[key] = ref
	}
	if len(issues) != len(tasks) {
		return nil, errors.New("native graph receipt does not bind submitted tasks")
	}
	for _, task := range tasks {
		if issues[task.Key].IssueID == 0 {
			return nil, errors.New("native graph receipt missing submitted task")
		}
	}
	return issues, nil
}

// Bind the existing generation domain's retained identities to semantic graph
// references. The separate identity cutover replaces native/local translations;
// these helpers allocate nothing and never guess an issue number.
func issueIDText(id int64) string { return strconv.FormatInt(id, 10) }

func boundGraphIssue(repository, id, number int64) collaboration.IssueRef {
	return collaboration.IssueRef{Repository: collaboration.RepositoryID(repository), Kind: collaboration.SubjectIssue, ExternalID: issueIDText(id), Number: number}
}

func boundGraphRef(repository, id, number int64) collaboration.GraphRef {
	issue := boundGraphIssue(repository, id, number)
	return collaboration.GraphRef{Issue: &issue}
}

func graphRefID(ref collaboration.GraphRef) string {
	if ref.Issue == nil {
		return ""
	}
	return ref.Issue.ExternalID
}
