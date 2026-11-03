// FW32 reassessment-coder adoption (T35–T39).
//
// A single coder reviews the sealed complete inventory (carried INLINE in
// the existing reassess prompt; there is no new inventory transport
// artifact) and proposes one coherent revision set through the typed
// ReassessmentReport. This file validates that proposal and stages it for
// serial per-intent adoption under the repository fence. It performs no
// source writes, spawns no children and publishes nothing: all native
// effects flow later through the existing intent adoption/generation path
// with exact readback, one immutable operation per intent.
//
// Frozen rules enforced here:
//   - T35: exactly one completion-subject coder on the reassess phase;
//     wrong subject, role, payload or candidate refuses; the 320 KiB
//     prompt exception is pinned to genuine reassessment cycles while
//     every result stays within 64 KiB.
//   - T36: full outcome coverage, valid empty deltas, attention holding
//     the event, no scope inflation, and per-intent packet preflight
//     (1–64 creates/edits/dependency changes, encoded <=48 KiB) before
//     any prefix commits. An accepted proposal judgment (accept with
//     option probability and confidence >=0.80) is required.
//   - T37: protected and unchanged tasks keep native issue ID, acceptance
//     ID, content, state and outgoing edges; pinned prerequisite
//     acceptance routes are preserved; changed idle prerequisites
//     transitively rebind idle dependents or refuse protected routes.
//     Frozen bindings are copied without AdmitAcceptance.
//   - T38: serial journal, committed prefixes survive later failure, lost
//     replies and head-CAS/packet crashes resume the same operation,
//     generic recovery cannot run a later staged revision, and an
//     activated-but-unmaterialized head is never abandoned.
//   - T39: source, scope and authority freshness invalidates before
//     effects; release covers only its own event.
//
// This deterministic preflight receives the typed proposal judgment.
// completion_proposal.go owns its funded serving consultation and
// completion_adoption.go owns the durable serial execution.
package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// Reassessment step states for the serial adoption journal. Steps apply in
// intent-ID order under the repository fence; a committed prefix stays
// committed when later work fails.
const (
	reassessmentStepStaged    = "staged"
	reassessmentStepActive    = "active"
	reassessmentStepCommitted = "committed"
)

// ReassessmentPrior carries the frozen prior state for one intent: the
// accepted revision the proposal revises, its recorded task bindings, the
// protected task set, and the pinned prerequisite acceptance route per
// task edge. Protected means delivered, active, PR-linked, manually
// changed since the recorded generation, or not factory-authorized.
type ReassessmentPrior struct {
	Revision             factory.IntentRevision
	SubmittedTasks       []factory.IntentTask
	SuccessorCreatedUnix int64
	Bindings             map[string]factory.IntentTaskBinding
	Protected            map[string]bool
	PrereqAcceptance     map[string]map[string]string
}

// ReassessmentAuthority carries the current-authority inputs the proposal
// must satisfy: the effective accepted idea per intent and the verified
// completion behind each delivered acceptance the coverage cites.
type ReassessmentAuthority struct {
	EffectiveIdeas    map[string]map[string]bool
	DeliveredVerified map[string]bool
}

// ReassessmentFreshness carries the pre-effect freshness inputs: the
// current source and preparation the driver rechecked against the
// report's sealed bindings. Any source, scope or authority change before
// effects invalidates the whole proposal; after a committed prefix only
// exact expected recorded effects are normalized while every other
// change still invalidates remaining work.
type ReassessmentFreshness struct {
	SourceCommit      string
	PreparationDigest string
	ScopeDigestValid  bool
	AuthorityValid    bool
}

// ReassessmentProposalJudgment carries the deterministic proposal
// assessment: selected accept with option probability and confidence at
// or above their bounds. Anything else waits for intervention.
type ReassessmentProposalJudgment struct {
	Option      string
	Probability float64
	Confidence  float64
}

// ReassessmentIntentPacket is one intent's preflighted native delta. A
// no-effect graph uses no native operation. CopiedBindings are frozen
// bindings preserved without invoking AdmitAcceptance; ReboundKeys get
// new exact acceptances through the existing adoption path; RetiredKeys
// receive supersession treatment and lose dispatch authority without any
// issue deletion or closure.
type ReassessmentIntentPacket struct {
	CopiedBindings    []factory.IntentTaskBinding
	Payload           json.RawMessage
	IntentID          string
	SuccessorRevision string
	PacketDigest      string
	ReboundKeys       []string
	RetiredKeys       []string
	NoEffect          bool
}

// ReassessmentJournalStep is one staged serial step in intent-ID order.
type ReassessmentJournalStep struct {
	IntentID          string
	SuccessorRevision string
	PacketDigest      string
	OperationID       string
	State             string
	NoEffect          bool
}

// ReassessmentJournal stages the accepted proposal as immutable inert
// data: exact successor revisions, native packet digests and readback
// expectations in intent-ID order with a durable cursor.
type ReassessmentJournal struct {
	Steps                   []ReassessmentJournalStep
	CompletionID            string
	StallID                 string
	DeliveryID              string
	FailedVerificationRunID string
	CycleID                 string
	InventoryDigest         string
	Cursor                  int
}

// Deterministic preflight grants no adoption authority. Both the paid-proposal
// producer and the read-only worker preview use it without inventing a judgment.
func preflightReassessmentAdoption(
	codec factory.GraphCodec,
	report factory.ReassessmentReport,
	priors map[string]ReassessmentPrior,
	authority ReassessmentAuthority,
	freshness ReassessmentFreshness,
	sealedInventoryDigest string,
	inventory, changedIssueIndexes []int64,
	completionID, cycleID string,
) ([]ReassessmentIntentPacket, factory.ReassessmentValidationReport, ReassessmentJournal, error) {
	var empty factory.ReassessmentValidationReport
	if err := report.Validate(); err != nil {
		return nil, empty, ReassessmentJournal{}, err
	}
	if report.CompletionID != completionID || report.CycleID != cycleID {
		return nil, empty, ReassessmentJournal{}, errors.New("reassessment report does not match its cycle")
	}
	if !factory.ValidDigest(sealedInventoryDigest) || report.InventoryDigest != sealedInventoryDigest {
		return nil, empty, ReassessmentJournal{}, errors.New("reassessment inventory is not the sealed inventory")
	}
	if err := report.AccountingHolds(inventory, changedIssueIndexes); err != nil {
		return nil, empty, ReassessmentJournal{}, err
	}
	if len(report.Attention) > 0 {
		return nil, empty, ReassessmentJournal{}, errors.New("reassessment attention holds the event")
	}
	if !freshness.AuthorityValid || !freshness.ScopeDigestValid {
		return nil, empty, ReassessmentJournal{}, errors.New("reassessment inputs changed before effects")
	}
	if !factory.ValidCommit(freshness.SourceCommit) || freshness.SourceCommit != report.SourceCommit {
		return nil, empty, ReassessmentJournal{}, errors.New("reassessment source changed before effects")
	}
	if !factory.ValidDigest(freshness.PreparationDigest) || freshness.PreparationDigest != report.PreparationDigest {
		return nil, empty, ReassessmentJournal{}, errors.New("reassessment preparation changed before effects")
	}
	packets := make([]ReassessmentIntentPacket, 0, len(report.IntentChanges))
	for _, change := range report.IntentChanges {
		packet, err := preflightReassessmentIntent(codec, change, priors, authority, report.Coverage)
		if err != nil {
			return nil, empty, ReassessmentJournal{}, err
		}
		packets = append(packets, packet)
	}
	validation, err := reassessmentValidationReport(report, packets)
	if err != nil {
		return nil, empty, ReassessmentJournal{}, err
	}
	journal := stageReassessmentJournal(report, packets)
	return packets, validation, journal, nil
}

// preflightReassessmentIntent checks one intent change against its frozen
// prior and preflights its exact native delta. Protected tasks stay
// byte-identical with copied bindings; changed idle prerequisites
// transitively rebind idle dependents; a changed protected route
// refuses. The delta packet is validated against the native 64-change
// and 48 KiB bounds with protected outgoing edges frozen.
func preflightReassessmentIntent(
	codec factory.GraphCodec,
	change factory.ReassessmentIntentChange,
	priors map[string]ReassessmentPrior,
	authority ReassessmentAuthority,
	coverage []factory.ReassessmentCoverage,
) (ReassessmentIntentPacket, error) {
	var empty ReassessmentIntentPacket
	prior, err := admitReassessmentPrior(change, priors)
	if err != nil {
		return empty, err
	}
	priorTasks, proposedTasks, err := admitReassessmentScope(change, prior, authority)
	if err != nil {
		return empty, err
	}
	if err := checkReassessmentCoverage(change, coverage, authority); err != nil {
		return empty, err
	}
	changedKeys, retiredKeys := diffReassessmentTasks(priorTasks, proposedTasks)
	affected, err := transitivelyAffectedTasks(proposedTasks, changedKeys, prior)
	if err != nil {
		return empty, err
	}
	for _, key := range retiredKeys {
		affected[key] = true
	}
	if err := checkReassessmentProtected(prior, priorTasks, proposedTasks, affected); err != nil {
		return empty, err
	}
	packet, err := buildReassessmentPacket(codec, change, prior, priorTasks, proposedTasks, changedKeys, affected)
	if err != nil {
		return empty, err
	}
	return packet, nil
}

// admitReassessmentPrior binds one reassessment change to its known prior.
func admitReassessmentPrior(
	change factory.ReassessmentIntentChange,
	priors map[string]ReassessmentPrior,
) (ReassessmentPrior, error) {
	var empty ReassessmentPrior
	prior, ok := priors[change.IntentID]
	if !ok {
		return empty, errors.New("reassessment change names an unknown intent")
	}
	if change.ExpectedRevision == prior.Revision.ID {
		return empty, errors.New("reassessment successor repeats its prior revision")
	}
	if change.ScopeDigest != prior.Revision.Digest() {
		return empty, errors.New("reassessment scope digest does not match its accepted revision")
	}
	return prior, nil
}

// admitReassessmentScope binds one reassessment change's tasks to the
// effective accepted scope and issue bound.
func admitReassessmentScope(
	change factory.ReassessmentIntentChange,
	prior ReassessmentPrior,
	authority ReassessmentAuthority,
) (map[string]factory.IntentTask, map[string]factory.IntentTask, error) {
	effective := authority.EffectiveIdeas[change.IntentID]
	if effective == nil {
		return nil, nil, errors.New("reassessment intent lacks its effective accepted scope")
	}
	priorTasks := make(map[string]factory.IntentTask, len(prior.SubmittedTasks))
	for _, task := range prior.SubmittedTasks {
		priorTasks[task.Key] = task
	}
	proposedTasks := make(map[string]factory.IntentTask, len(change.Tasks))
	for _, task := range change.Tasks {
		proposedTasks[task.Key] = task
		for _, id := range task.IdeaIDs {
			if !effective[id] {
				return nil, nil, errors.New("reassessment task leaves its accepted scope")
			}
		}
	}
	if len(change.Tasks) > prior.Revision.MaxIssues || prior.Revision.MaxIssues < 1 {
		return nil, nil, errors.New("reassessment graph exceeds its accepted issue bound")
	}
	return priorTasks, proposedTasks, nil
}

// checkReassessmentProtected requires protected routes, prerequisite
// routes and work to stay untouched behind one reassessment.
func checkReassessmentProtected(
	prior ReassessmentPrior,
	priorTasks, proposedTasks map[string]factory.IntentTask,
	affected map[string]bool,
) error {
	for key := range affected {
		if prior.Protected[key] {
			return errors.New("reassessment changes a protected route")
		}
	}
	for key, task := range proposedTasks {
		if !prior.Protected[key] {
			continue
		}
		for _, dep := range task.DependsOn {
			if affected[dep] {
				return errors.New("reassessment changes a protected prerequisite route")
			}
		}
		if err := checkPinnedPrereqRoute(prior, key, task); err != nil {
			return err
		}
	}
	for key, task := range proposedTasks {
		if !prior.Protected[key] {
			continue
		}
		priorTask, ok := priorTasks[key]
		if !ok || !equalIntentTasks(priorTask, task) {
			return errors.New("reassessment edits protected work")
		}
		if _, ok := prior.Bindings[key]; !ok {
			return errors.New("protected task lacks its frozen binding")
		}
	}
	return nil
}

// checkPinnedPrereqRoute requires one protected task's pinned
// prerequisite route behind one reassessment.
func checkPinnedPrereqRoute(prior ReassessmentPrior, key string, task factory.IntentTask) error {
	pinned := prior.PrereqAcceptance[key]
	if len(pinned) != len(task.DependsOn) {
		return errors.New("protected task lacks its pinned prerequisite route")
	}
	for _, dep := range task.DependsOn {
		if _, ok := pinned[dep]; !ok {
			return errors.New("protected prerequisite route changed")
		}
	}
	return nil
}

// checkReassessmentCoverage requires every affected effective idea to be
// covered exactly once by this intent's entries, with delivered
// acceptances behind verified completion and historic bindings.
func checkReassessmentCoverage(
	change factory.ReassessmentIntentChange,
	coverage []factory.ReassessmentCoverage,
	authority ReassessmentAuthority,
) error {
	byTask := make(map[string]factory.IntentTask, len(change.Tasks))
	affected := make(map[string]bool)
	for _, task := range change.Tasks {
		byTask[task.Key] = task
		for _, id := range task.IdeaIDs {
			affected[id] = true
		}
	}
	var entries []factory.ReassessmentCoverage
	for _, entry := range coverage {
		if entry.IntentID == change.IntentID {
			entries = append(entries, entry)
		}
	}
	if len(affected) == 0 {
		for _, entry := range entries {
			if entry.IntentID == change.IntentID {
				return errors.New("reassessment coverage without affected outcomes")
			}
		}
		return nil
	}
	if len(entries) != len(affected) {
		return errors.New("reassessment coverage misses affected outcomes")
	}
	for _, entry := range entries {
		if !affected[entry.OutcomeID] {
			return errors.New("reassessment coverage names an unaffected outcome")
		}
		includes := false
		for _, key := range entry.TaskKeys {
			task, ok := byTask[key]
			if !ok {
				return errors.New("reassessment coverage names an unproposed task")
			}
			for _, id := range task.IdeaIDs {
				if id == entry.OutcomeID {
					includes = true
					break
				}
			}
		}
		if !includes {
			return errors.New("reassessment coverage task misses its outcome idea")
		}
		for _, id := range entry.DeliveredAcceptanceIDs {
			if !authority.DeliveredVerified[id] {
				return errors.New("reassessment coverage lacks verified delivery")
			}
		}
	}
	return nil
}

// diffReassessmentTasks splits the full replacement graph into changed
// and retired keys against the prior. Identical prose with identical
// prerequisite routes is unchanged; anything else is changed.
func diffReassessmentTasks(
	prior, proposed map[string]factory.IntentTask,
) (changed map[string]bool, retired []string) {
	changed = make(map[string]bool)
	for key, task := range proposed {
		priorTask, ok := prior[key]
		if !ok || !equalIntentTasks(priorTask, task) {
			changed[key] = true
		}
	}
	for key := range prior {
		if _, ok := proposed[key]; !ok {
			retired = append(retired, key)
		}
	}
	sort.Strings(retired)
	return changed, retired
}

func equalIntentTasks(a, b factory.IntentTask) bool {
	if a.Key != b.Key || a.Objective != b.Objective || len(a.Outcomes) != len(b.Outcomes) ||
		len(a.IdeaIDs) != len(b.IdeaIDs) || len(a.DependsOn) != len(b.DependsOn) {
		return false
	}
	for i := range a.Outcomes {
		if a.Outcomes[i] != b.Outcomes[i] {
			return false
		}
	}
	for i := range a.IdeaIDs {
		if a.IdeaIDs[i] != b.IdeaIDs[i] {
			return false
		}
	}
	edges := make(map[string]bool, len(a.DependsOn))
	for _, dep := range a.DependsOn {
		edges[dep] = true
	}
	for _, dep := range b.DependsOn {
		if !edges[dep] {
			return false
		}
	}
	return true
}

// transitivelyAffectedTasks closes the changed set over idle dependents:
// a changed idle prerequisite acceptance makes its idle dependents
// materially affected in topological order even when their prose and
// native edges are unchanged.
func transitivelyAffectedTasks(
	proposed map[string]factory.IntentTask,
	changed map[string]bool,
	prior ReassessmentPrior,
) (map[string]bool, error) {
	affected := make(map[string]bool, len(changed))
	for key := range changed {
		affected[key] = true
	}
	dependents := make(map[string][]string)
	for key, task := range proposed {
		for _, dep := range task.DependsOn {
			dependents[dep] = append(dependents[dep], key)
		}
	}
	queue := make([]string, 0, len(changed))
	for key := range changed {
		queue = append(queue, key)
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, dependent := range dependents[key] {
			if affected[dependent] {
				continue
			}
			affected[dependent] = true
			queue = append(queue, dependent)
		}
	}
	_ = prior
	return affected, nil
}

// buildReassessmentPacket renders one intent's exact native delta and
// preflights it against the native bounds. New tasks create; materially
// changed idle tasks edit with exact expected content; retired tasks take
// local supersession with no native operation. A no-effect graph uses no
// operation at all: it is never chunked and never padded. Protected
// outgoing edges are frozen explicitly: no dependency change may touch a
// protected issue in either endpoint.
func buildReassessmentPacket(
	codec factory.GraphCodec,
	change factory.ReassessmentIntentChange,
	prior ReassessmentPrior,
	priorTasks, proposedTasks map[string]factory.IntentTask,
	changed map[string]bool,
	affected map[string]bool,
) (ReassessmentIntentPacket, error) {
	packet := ReassessmentIntentPacket{IntentID: change.IntentID, SuccessorRevision: change.ExpectedRevision, Payload: json.RawMessage(`{}`)}
	tasks := make([]factory.IntentTask, 0, len(proposedTasks))
	for _, task := range proposedTasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Key < tasks[j].Key })
	proposedRev, err := SuccessorIntentRevision(prior.Revision, change, prior.SuccessorCreatedUnix)
	if err != nil {
		return ReassessmentIntentPacket{}, err
	}
	proposedRev.Tasks = tasks
	var payload collaboration.GraphIntent
	protectedIssues := make(map[string]bool)
	for key, binding := range prior.Bindings {
		if prior.Protected[key] {
			protectedIssues[issueIDText(binding.IssueID)] = true
		}
	}
	rebound := make(map[string]bool)
	for key := range affected {
		if _, ok := proposedTasks[key]; ok {
			rebound[key] = true
		}
	}
	planReassessmentWrites(&payload, prior, priorTasks, proposedTasks, proposedRev, changed, affected, &packet)
	if err := planReassessmentEdges(&payload, prior, priorTasks, proposedTasks); err != nil {
		return ReassessmentIntentPacket{}, err
	}
	if err := checkReassessmentEdges(payload, protectedIssues); err != nil {
		return ReassessmentIntentPacket{}, err
	}
	_, retired := diffReassessmentTasks(priorTasks, proposedTasks)
	packet.RetiredKeys = retired
	for key := range rebound {
		packet.ReboundKeys = append(packet.ReboundKeys, key)
	}
	sort.Strings(packet.ReboundKeys)
	sort.Slice(packet.CopiedBindings, func(i, j int) bool {
		return packet.CopiedBindings[i].TaskKey < packet.CopiedBindings[j].TaskKey
	})
	if len(payload.Creates)+len(payload.Edits)+len(payload.Dependencies) == 0 {
		packet.NoEffect = true
		packet.PacketDigest = packetDigest(nil)
		return packet, nil
	}
	if codec == nil {
		return ReassessmentIntentPacket{}, errors.New("graph codec is unavailable")
	}
	encoded, err := codec.EncodeGraph(payload, prior.Revision.Repository)
	if err != nil {
		return ReassessmentIntentPacket{}, errors.New("reassessment packet exceeds its native bound")
	}
	packet.Payload = encoded
	packet.PacketDigest = packetDigest(encoded)
	return packet, nil
}

// planReassessmentWrites plans created and edited issues behind one
// reassessment, copying untouched bindings.
func planReassessmentWrites(
	payload *collaboration.GraphIntent,
	prior ReassessmentPrior,
	priorTasks, proposedTasks map[string]factory.IntentTask,
	proposedRev factory.IntentRevision,
	changed, affected map[string]bool,
	packet *ReassessmentIntentPacket,
) {
	for key, task := range proposedTasks {
		binding, bound := prior.Bindings[key]
		priorTask, known := priorTasks[key]
		switch {
		case !known || !bound:
			payload.Creates = append(payload.Creates, collaboration.GraphCreate{
				Key: task.Key, Title: intentTaskTitle(task), Body: intentTaskBody(proposedRev, task),
			})
		case changed[key]:
			priorWork := prior.Revision
			priorWork.Tasks = prior.SubmittedTasks
			payload.Edits = append(payload.Edits, collaboration.GraphEdit{
				Issue:    boundGraphIssue(prior.Revision.Repository, binding.IssueID, binding.IssueIndex),
				Expected: collaboration.IssueContent{Title: intentTaskTitle(priorTask), Body: intentTaskBody(priorWork, priorTask), Revision: collaboration.Revision(strconv.Itoa(binding.ContentVersion))},
				Title:    intentTaskTitle(task),
				Body:     intentTaskBody(proposedRev, task),
			})
		case affected[key]:
			// Transitively affected with identical prose: no native
			// edit, but the acceptance is recomputed in topological
			// order through the existing adoption path. The frozen
			// binding is never copied for such tasks.
		default:
			packet.CopiedBindings = append(packet.CopiedBindings, binding)
		}
	}
}

// planReassessmentEdges plans added and removed dependencies behind
// one reassessment.
func planReassessmentEdges(
	payload *collaboration.GraphIntent,
	prior ReassessmentPrior,
	priorTasks, proposedTasks map[string]factory.IntentTask,
) error {
	for key, task := range proposedTasks {
		priorTask, known := priorTasks[key]
		priorEdges := make(map[string]bool)
		if known {
			for _, dep := range priorTask.DependsOn {
				priorEdges[dep] = true
			}
		}
		for _, dep := range task.DependsOn {
			if priorEdges[dep] {
				continue
			}
			depRef, ok := reassessmentEdgeRef(dep, prior.Bindings, proposedTasks, prior.Revision.Repository)
			if !ok {
				return errors.New("reassessment dependency names an unmapped task")
			}
			payload.Dependencies = append(payload.Dependencies, collaboration.GraphDependency{Issue: taskRef(task.Key, prior.Bindings, prior.Revision.Repository), Prerequisite: depRef})
		}
		if known {
			proposedEdges := make(map[string]bool, len(task.DependsOn))
			for _, dep := range task.DependsOn {
				proposedEdges[dep] = true
			}
			for _, dep := range priorTask.DependsOn {
				if proposedEdges[dep] {
					continue
				}
				payload.Dependencies = append(payload.Dependencies, collaboration.GraphDependency{
					Issue: taskRef(task.Key, prior.Bindings, prior.Revision.Repository), Prerequisite: taskRef(dep, prior.Bindings, prior.Revision.Repository), Remove: true,
				})
			}
		}
	}
	return nil
}

// checkReassessmentEdges requires removals through bound issues only
// and no dependency change touching a protected issue.
func checkReassessmentEdges(payload collaboration.GraphIntent, protectedIssues map[string]bool) error {
	for _, edge := range payload.Dependencies {
		if edge.Remove && (edge.Issue.Key != "" || edge.Prerequisite.Key != "") {
			return errors.New("reassessment removes an edge through a new key")
		}
		for _, ref := range []collaboration.GraphRef{edge.Issue, edge.Prerequisite} {
			if ref.Issue != nil && protectedIssues[ref.Issue.ExternalID] {
				return errors.New("reassessment changes a protected outgoing edge")
			}
		}
	}
	return nil
}

// reassessmentEdgeRef maps one task key to its native edge reference: the
// bound issue ID for adopted tasks, the request-local key for new tasks.
func reassessmentEdgeRef(
	key string,
	bindings map[string]factory.IntentTaskBinding,
	proposed map[string]factory.IntentTask,
	repository int64,
) (collaboration.GraphRef, bool) {
	if _, ok := proposed[key]; !ok {
		return collaboration.GraphRef{}, false
	}
	if binding, ok := bindings[key]; ok {
		return boundGraphRef(repository, binding.IssueID, binding.IssueIndex), true
	}
	return collaboration.GraphRef{Key: key}, true
}

func taskRef(key string, bindings map[string]factory.IntentTaskBinding, repository int64) collaboration.GraphRef {
	if binding, ok := bindings[key]; ok {
		return boundGraphRef(repository, binding.IssueID, binding.IssueIndex)
	}
	return collaboration.GraphRef{Key: key}
}

func packetDigest(encoded []byte) string {
	if encoded == nil {
		encoded = []byte("{}")
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// reassessmentValidationReport builds the bounded assessment digest the
// proposal judgment sees alongside the proposal: exact digests and sorted
// unique index lists with no freeform narrative (<=16 KiB).
func reassessmentValidationReport(
	report factory.ReassessmentReport,
	packets []ReassessmentIntentPacket,
) (factory.ReassessmentValidationReport, error) {
	return graphRepairValidation(report, report.InventoryDigest, report.Coverage, report.IntentChanges, report.RetainedIssueIndexes, packets)
}

func graphRepairValidation(proposal any, inventoryDigest string, coverage []factory.ReassessmentCoverage, changes []factory.ReassessmentIntentChange, retained []int64, packets []ReassessmentIntentPacket) (factory.ReassessmentValidationReport, error) {
	var empty factory.ReassessmentValidationReport
	proposalBytes, err := json.Marshal(proposal)
	if err != nil {
		return empty, errors.New("reassessment proposal is not digestible")
	}
	proposalSum := sha256.Sum256(proposalBytes)
	coverageSum := sha256.Sum256([]byte(coverageDigestInput(coverage)))
	validation := factory.ReassessmentValidationReport{
		InventoryDigest: inventoryDigest,
		ProposalDigest:  hex.EncodeToString(proposalSum[:]),
		CoverageDigest:  hex.EncodeToString(coverageSum[:]),
	}
	for _, change := range changes {
		validation.ScopeDigests = append(validation.ScopeDigests, factory.IntentDigest{
			IntentID: change.IntentID, Digest: change.ScopeDigest,
		})
	}
	for _, packet := range packets {
		validation.PacketDigests = append(validation.PacketDigests, factory.IntentDigest{
			IntentID: packet.IntentID, Digest: packet.PacketDigest,
		})
	}
	seen := make(map[int64]bool)
	for _, packet := range packets {
		for _, binding := range packet.CopiedBindings {
			if !seen[binding.IssueIndex] {
				seen[binding.IssueIndex] = true
				validation.ProtectedIssueIndexes = append(validation.ProtectedIssueIndexes, binding.IssueIndex)
			}
		}
	}
	for _, index := range retained {
		if !seen[index] {
			seen[index] = true
			validation.MutableIssueIndexes = append(validation.MutableIssueIndexes, index)
		}
	}
	sort.Slice(validation.MutableIssueIndexes, func(i, j int) bool {
		return validation.MutableIssueIndexes[i] < validation.MutableIssueIndexes[j]
	})
	sort.Slice(validation.ProtectedIssueIndexes, func(i, j int) bool {
		return validation.ProtectedIssueIndexes[i] < validation.ProtectedIssueIndexes[j]
	})
	if err := validation.Validate(); err != nil {
		return empty, err
	}
	return validation, nil
}

func coverageDigestInput(coverage []factory.ReassessmentCoverage) string {
	keys := make([]string, 0, len(coverage))
	for _, entry := range coverage {
		keys = append(keys, entry.IntentID+"\x00"+entry.OutcomeID)
	}
	sort.Strings(keys)
	encoded, _ := json.Marshal(keys)
	return string(encoded)
}

// stageReassessmentJournal stages the accepted proposal as immutable inert
// data in intent-ID order with a durable cursor at the first step. No
// intent head advances here: heads advance one at a time through the
// existing intent adoption/generation path while the fence stays closed.
func stageReassessmentJournal(
	report factory.ReassessmentReport,
	packets []ReassessmentIntentPacket,
) ReassessmentJournal {
	ordered := append([]ReassessmentIntentPacket(nil), packets...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].IntentID < ordered[j].IntentID })
	journal := ReassessmentJournal{
		CompletionID: report.CompletionID, CycleID: report.CycleID,
		InventoryDigest: report.InventoryDigest,
	}
	for _, packet := range ordered {
		operation := ""
		if !packet.NoEffect {
			operation = "reassess-" + report.CycleID + "-" + packet.IntentID
		}
		journal.Steps = append(journal.Steps, ReassessmentJournalStep{
			IntentID: packet.IntentID, SuccessorRevision: packet.SuccessorRevision,
			PacketDigest: packet.PacketDigest, OperationID: operation,
			State: reassessmentStepStaged, NoEffect: packet.NoEffect,
		})
	}
	return journal
}

// CurrentStep returns the journal's current staged step: the oldest step
// that is not yet committed. Generic recovery binds to this step and
// cannot run a later staged revision.
func (j *ReassessmentJournal) CurrentStep() (ReassessmentJournalStep, error) {
	for i := range j.Steps {
		if j.Steps[i].State != reassessmentStepCommitted {
			j.Cursor = i
			return j.Steps[i], nil
		}
	}
	return ReassessmentJournalStep{}, errors.New("reassessment journal has no remaining step")
}

// MarkStepActive CAS-activates one successor step through the existing
// intent adoption path. A crash between head CAS and packet creation
// resumes this same step with the same operation identity.
func (j *ReassessmentJournal) MarkStepActive(intentID string) (ReassessmentJournalStep, error) {
	current, err := j.CurrentStep()
	if err != nil {
		return ReassessmentJournalStep{}, err
	}
	if current.IntentID != intentID {
		return ReassessmentJournalStep{}, errors.New("reassessment recovery cannot run a later staged revision")
	}
	if current.State == reassessmentStepCommitted {
		return ReassessmentJournalStep{}, errors.New("reassessment step is already committed")
	}
	for i := range j.Steps {
		if j.Steps[i].IntentID == intentID {
			j.Steps[i].State = reassessmentStepActive
			j.Cursor = i
			return j.Steps[i], nil
		}
	}
	return ReassessmentJournalStep{}, errors.New("reassessment step names an unstaged intent")
}

// ResumeOperation returns the exact immutable operation for a step after
// a lost reply or a crash: a lost reply never starts a second operation.
// No-effect steps carry no operation.
func (j *ReassessmentJournal) ResumeOperation(intentID string) (string, error) {
	for _, step := range j.Steps {
		if step.IntentID == intentID {
			if step.NoEffect {
				return "", nil
			}
			if step.OperationID == "" {
				return "", errors.New("reassessment step lacks its operation")
			}
			return step.OperationID, nil
		}
	}
	return "", errors.New("reassessment step names an unstaged intent")
}

// MarkStepCommitted reconciles one immutable native operation with its
// exact readback/adoption and advances the durable cursor. A committed
// prefix stays committed when later work fails: the journal records it,
// the fence stays closed, and recovery first observes the prefix. Only
// the exact expected recorded effects normalize; every other change
// still invalidates remaining work.
func (j *ReassessmentJournal) MarkStepCommitted(intentID, packetDigest string) error {
	current, err := j.CurrentStep()
	if err != nil {
		return err
	}
	if current.IntentID != intentID {
		return errors.New("reassessment commits do not skip staged steps")
	}
	if current.PacketDigest != packetDigest {
		return errors.New("reassessment readback does not match its staged packet")
	}
	for i := range j.Steps {
		if j.Steps[i].IntentID == intentID {
			j.Steps[i].State = reassessmentStepCommitted
			j.Cursor = i + 1
			return nil
		}
	}
	return errors.New("reassessment step names an unstaged intent")
}

// GuardRecoveryStep binds generic RecoverIntentGraphs pickup to the
// approved journal step: only the current step may run. Later staged
// revisions refuse until the cursor reaches them.
func (j *ReassessmentJournal) GuardRecoveryStep(intentID string) error {
	current, err := j.CurrentStep()
	if err != nil {
		return err
	}
	if current.IntentID != intentID {
		return errors.New("reassessment recovery cannot run a later staged revision")
	}
	return nil
}

// Releasable reports whether the event may release with its final
// inventory digest (T39): every staged step is confirmed adopted, and no
// activated-but-unmaterialized head remains. A no-change report (zero
// steps) releases without graph mutations. A successor cycle's continue
// cannot release this residual state.
func (j *ReassessmentJournal) Releasable() error {
	for _, step := range j.Steps {
		if step.State != reassessmentStepCommitted {
			return errors.New("reassessment adoption is not fully materialized")
		}
	}
	return nil
}

// SuccessorIntentRevision materializes one staged intent change as its
// exact successor revision for serial per-intent adoption (T38). The
// successor keeps the frozen accepted scope (intent, owner, repository,
// objective, ideas, effective outcomes, issue bound) with the prior as
// its predecessor and the proposal tasks as its full replacement graph.
// It carries state accepted so the existing intent adoption/generation
// path can adopt it one step at a time under the repository fence; the
// driver saves revisions in journal order and advances heads only
// through GenerateAcceptedIntent with exact readback. Staging an
// accepted successor is gated upstream by the T36 accept judgment and
// the held fence: this constructor performs no store or native effects.
func SuccessorIntentRevision(
	prior factory.IntentRevision,
	change factory.ReassessmentIntentChange,
	createdUnix int64,
) (factory.IntentRevision, error) {
	var empty factory.IntentRevision
	if prior.State != "accepted" {
		return empty, errors.New("reassessment successor needs its accepted prior")
	}
	if change.IntentID == "" || change.IntentID != prior.Intent {
		return empty, errors.New("reassessment change names another intent")
	}
	if !project.ValidDecisionID(change.ExpectedRevision) || change.ExpectedRevision == prior.ID {
		return empty, errors.New("reassessment successor needs its fresh revision identity")
	}
	if change.ScopeDigest == "" || change.ScopeDigest != prior.Digest() {
		return empty, errors.New("reassessment scope digest does not match its accepted revision")
	}
	if createdUnix <= 0 {
		return empty, errors.New("reassessment successor needs its creation time")
	}
	tasks := append([]factory.IntentTask(nil), change.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Key < tasks[j].Key })
	successor := factory.IntentRevision{
		ID:                change.ExpectedRevision,
		Intent:            prior.Intent,
		Predecessor:       prior.ID,
		Owner:             prior.Owner,
		Repository:        prior.Repository,
		Department:        prior.Department,
		Objective:         prior.Objective,
		Approach:          prior.Approach,
		Ideas:             prior.Ideas,
		Tasks:             tasks,
		State:             "accepted",
		MaxIssues:         prior.MaxIssues,
		PlanningCalls:     prior.PlanningCalls,
		CreatedUnix:       createdUnix,
		EffectiveOutcomes: prior.EffectiveOutcomes,
	}
	if err := successor.Validate(); err != nil {
		return empty, err
	}
	return successor, nil
}

// ReassessmentPromptInventory is the complete sealed input the single
// coder receives INLINE in its existing assignment prompt (T35). There
// is no new inventory transport artifact: the driver embeds every
// collected issue verbatim plus the complete canonical material body
// with its local digest, and the coder's typed report answers with the
// sealed digest. The local half rides as ONE CompletionLocal, never as
// separate source/preparation fields.
type ReassessmentPromptInventory struct {
	Issues          []NativeInventoryIssue
	CompletionID    string
	CycleID         string
	InventoryDigest string
	Local           factory.CompletionLocal
	Repository      int64
	Generation      int64
	Complete        bool
}

// reassessmentCoderRubric is the frozen coder instruction: review the
// complete inline inventory and propose one coherent revision set
// through the typed reassessment report. The coder investigates scope,
// never implements issues, writes no source and publishes nothing.
const reassessmentCoderRubric = "Review the complete sealed inventory below and propose one coherent revision set through the typed reassessment report: full outcome coverage, frozen scope and identities, protected bindings preserved. Do not implement any issue, write source, spawn children or publish."

const reassessmentWorkGuidance = "Use factory.reassessment_scope for the admitted cause, complete inventory, allowance and report references; follow its continuation rather than treating missing pages as empty. When useful, record material causal findings once with factory.record_diagnosis. For justified graph changes, save one coherent factory.draft_repair and inspect factory.preview_repair; revise only for a concrete refusal or new evidence. Recover a saved record using scope record/offset and verify payload_hash, retaining the original submission ID for uncertain writes. These tools require no extra model consultation and confirm no graph or readiness change. Finish once with the full typed report; no draft is automatically submitted or adopted."

// RenderReassessmentPrompt renders the single coder's assignment prompt
// with the COMPLETE sealed inventory inline. An incomplete collection,
// unordered indexes, or a prompt past the reassess-only 320 KiB bound
// refuses: the inventory is never truncated or summarized away. Every
// other phase keeps the 64 KiB prompt bound through MaxPromptForPhase.
func RenderReassessmentPrompt(in ReassessmentPromptInventory) ([]byte, error) {
	if !factory.ValidID(in.CompletionID) || !factory.ValidID(in.CycleID) {
		return nil, errors.New("reassessment prompt lacks its cycle identity")
	}
	if !factory.ValidDigest(in.InventoryDigest) {
		return nil, errors.New("reassessment prompt lacks its sealed inventory")
	}
	if in.Repository <= 0 || in.Generation < 0 {
		return nil, errors.New("reassessment prompt lacks its repository scope")
	}
	// The ONE local carries the complete material: validate it, bind it
	// to this repository, and inline its canonical body with its local
	// digest next to the complete native issues.
	if err := in.Local.Validate(); err != nil {
		return nil, errors.New("reassessment prompt lacks its sealed material")
	}
	if in.Local.Material.Repository != in.Repository {
		return nil, errors.New("reassessment prompt material names another repository")
	}
	localDigest, err := in.Local.Digest()
	if err != nil {
		return nil, errors.New("reassessment prompt lacks its sealed material")
	}
	materialRaw, err := json.Marshal(in.Local.Material)
	if err != nil {
		return nil, errors.New("reassessment prompt lacks its sealed material")
	}
	if !in.Complete {
		return nil, errors.New("reassessment prompt needs its complete inventory")
	}
	for i, issue := range in.Issues {
		if issue.Index < 1 {
			return nil, errors.New("reassessment inventory carries an invalid issue index")
		}
		if i > 0 && issue.Index <= in.Issues[i-1].Index {
			return nil, errors.New("reassessment inventory is not ordered by issue index")
		}
	}
	var prompt strings.Builder
	prompt.WriteString("Post-completion reassessment coder.\n")
	prompt.WriteString("Completion: " + in.CompletionID + "\n")
	prompt.WriteString("Cycle: " + in.CycleID + "\n")
	prompt.WriteString("Inventory: " + in.InventoryDigest + "\n")
	prompt.WriteString("Repository: " + strconv.FormatInt(in.Repository, 10) +
		" generation: " + strconv.FormatInt(in.Generation, 10) + "\n")
	prompt.WriteString("Source: " + in.Local.SourceCommit + " preparation: " + in.Local.PreparationDigest + "\n")
	prompt.WriteString("Local: " + localDigest + "\n")
	prompt.WriteString("Material: " + string(materialRaw) + "\n")
	prompt.WriteString("Issues: " + strconv.Itoa(len(in.Issues)) + " (complete, ordered by index)\n")
	prompt.WriteString(reassessmentCoderRubric + "\n")
	prompt.WriteString(reassessmentWorkGuidance + "\n")
	for _, issue := range in.Issues {
		prompt.WriteString("issue " + strconv.FormatInt(issue.Index, 10) + ":\n")
		prompt.WriteString(issue.Data + "\n")
	}
	rendered := []byte(prompt.String())
	if len(rendered) > project.MaxReassessPrompt {
		return nil, errors.New("reassessment prompt exceeds its bound")
	}
	return rendered, nil
}
